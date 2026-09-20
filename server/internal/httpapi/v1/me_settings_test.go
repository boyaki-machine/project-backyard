package v1

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 自分自身に関するAPI（ApiDesign.md 4.2 / 4.3、手順15）のハンドラ単体テスト。
//
// 認証はミドルウェアの責務なので通さない（routes_test.go が別に見ている）。
// ここで確かめるのは**応答の形・検証の分岐・監査に何を書くか**である。
// 実DBでしか確かめられないもの（UNIQUE 制約・メール変更後のログイン・
// 他セッションだけの失効）は me_integration_test.go にある。

// selfPrincipal は自分自身を操作するオペレータ。
//
// **アドミニストレータにしない。** 4章は権限キーを要求しないので、
// オペレータで通ることがそのまま仕様の確認になる。
func selfPrincipal() *auth.Principal {
	return &auth.Principal{
		ActorID:     testActorID,
		ActorKind:   auth.ActorKindUser,
		DisplayName: "田中",
		Email:       testEmail,
		SystemRole:  auth.SystemRoleOperator,
		TokenID:     "01K2F8QW3H7YRJ4M5N6P7Q8TOK",
	}
}

// meProfileRow は既定の自分自身。
func meProfileRow() gen.GetActorProfileRow {
	return gen.GetActorProfileRow{
		ActorID:     testActorID,
		Kind:        "user",
		DisplayName: "田中",
		Email:       txt(testEmail),
		SystemRole:  txt(auth.SystemRoleOperator),
		Locale:      txt("ja"),
		Timezone:    txt("Asia/Tokyo"),
		Theme:       txt("system"),
		Hue:         txt("blue"),
	}
}

// meFake は 4.2 / 4.3 のテストが使う既定のフェイク。
//
// **「通る」状態を既定にして、テストごとに1つだけ崩す**（userFake と同じ型）。
func meFake(t *testing.T) *fakeQuerier {
	t.Helper()
	q := newFake(t)
	q.profileRow = meProfileRow()
	q.myProfileRows = 1
	q.myCredential = gen.FindMyLocalCredentialRow{
		IdentityID:   testIdentity,
		PasswordHash: hashFor(t, testPassword),
	}
	return q
}

// hashFor は平文から PHC 文字列を作る。現在のパスワード検証を通すために使う。
func hashFor(t *testing.T, plain string) string {
	t.Helper()
	h, err := auth.HashPassword(plain)
	if err != nil {
		t.Fatalf("パスワードをハッシュ化できない: %v", err)
	}
	return h
}

// meReq は /me 系のリクエストを組み立てる。URL パラメータを持たない。
func meReq(method, target, body string, p *auth.Principal) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	return req.WithContext(auth.NewPrincipalContext(req.Context(), p))
}

// ── PATCH /me（ApiDesign.md 4.2）────────────────────────────────

func TestPatchMeUpdatesProfileAndReturnsSession(t *testing.T) {
	q := meFake(t)
	// 更新後に読み直した姿。応答が**更新後の値**であることを見るため、
	// 現在値（meProfileRow）とは違う値を返させる。
	after := meProfileRow()
	after.DisplayName = "田中 太郎"
	after.Theme = txt("dark")
	after.Hue = txt("green")
	q.profileRow = after

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"display_name":"田中 太郎","theme":"dark","hue":"green"}`, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	// **応答は GET /me と同一構造である**（4.2）。auth ストアへそのまま
	// 流し込めることが要件なので、トップレベルのキーを固定する。
	view := viewOf(t, rec)
	for _, key := range []string{"actor", "permissions", "projects", "expires_at"} {
		if _, ok := view[key]; !ok {
			t.Errorf("応答に %q が無い: %s", key, rec.Body.String())
		}
	}

	actor, ok := view["actor"].(map[string]any)
	if !ok {
		t.Fatalf("actor がオブジェクトでない: %s", rec.Body.String())
	}
	if actor["display_name"] != "田中 太郎" {
		t.Errorf("display_name = %v, want 田中 太郎", actor["display_name"])
	}
	// theme / hue を actor が持つこと（手順15 で足した。8.11）。
	if actor["theme"] != "dark" {
		t.Errorf("theme = %v, want dark", actor["theme"])
	}
	if actor["hue"] != "green" {
		t.Errorf("hue = %v, want green", actor["hue"])
	}

	// 送った項目だけが narg に載ること。**送らなかった locale / timezone を
	// 書きに行かない**（COALESCE で据え置かれる側に倒す）。
	if len(q.myProfileParams) != 1 {
		t.Fatalf("UpdateMyProfile の呼び出しが %d 回", len(q.myProfileParams))
	}
	arg := q.myProfileParams[0]
	if arg.Theme.String != "dark" || !arg.Theme.Valid {
		t.Errorf("theme の引数 = %+v, want dark", arg.Theme)
	}
	if arg.Locale.Valid {
		t.Errorf("送っていない locale が引数に載っている: %+v", arg.Locale)
	}
	if arg.Email.Valid {
		t.Errorf("送っていない email が引数に載っている: %+v", arg.Email)
	}
}

// メールを変えたら user_identity.subject も同じトランザクションで追随する
// （4.2）。**これを落とすと当人がログインできなくなる。**
func TestPatchMeUpdatesIdentitySubjectWithEmail(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"email":"tanaka.new@example.com"}`, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.subjectUpdates) != 1 {
		t.Fatalf("UpdateLocalIdentitySubject の呼び出しが %d 回, want 1", len(q.subjectUpdates))
	}
	if got := q.subjectUpdates[0].Subject; got != "tanaka.new@example.com" {
		t.Errorf("subject = %q, want tanaka.new@example.com", got)
	}
	if got := q.subjectUpdates[0].UserID; got != testActorID {
		t.Errorf("user_id = %q, want %q", got, testActorID)
	}
}

// メールを送っていないときは subject を触らない。
func TestPatchMeLeavesIdentitySubjectWhenEmailAbsent(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"display_name":"田中 太郎"}`, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.subjectUpdates) != 0 {
		t.Errorf("メールを送っていないのに subject を更新した: %+v", q.subjectUpdates)
	}
}

// **system_role は送られたこと自体が誤りである**（4.2）。
// 黙って捨てると、呼び出し側は権限が上がったと誤解したまま動く。
func TestPatchMeRejectsSystemRole(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"system_role":"administrator"}`, selfPrincipal()))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if code := detailCodeOf(t, rec, "system_role"); code != "not_allowed" {
		t.Errorf("details[system_role].code = %q, want not_allowed", code)
	}
	// **DB を1度も触っていないこと。** 検証は書き込みより前に通す。
	if len(q.myProfileParams) != 0 {
		t.Errorf("422 なのに UpdateMyProfile を呼んだ: %+v", q.myProfileParams)
	}
}

// 値域の検証（4.2）。DB の CHECK 制約に頼ると 500 になってしまう。
func TestPatchMeValidatesEnums(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"テーマ", `{"theme":"sepia"}`, "theme"},
		{"色相", `{"hue":"red"}`, "hue"},
		{"言語", `{"locale":"fr"}`, "locale"},
		{"タイムゾーン", `{"timezone":"Mars/Olympus"}`, "timezone"},
		// **"Local" は time.LoadLocation が解決してしまう。**
		// 誰のローカルかがサーバ設定に依存するので、明示的に弾く。
		{"Local は弾く", `{"timezone":"Local"}`, "timezone"},
		{"表示名が空", `{"display_name":"   "}`, "display_name"},
		{"メールの形式", `{"email":"not-an-email"}`, "email"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := meFake(t)
			h, _ := newUserHandler(q)
			rec := httptest.NewRecorder()
			h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me", tc.body, selfPrincipal()))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			if code := detailCodeOf(t, rec, tc.field); code == "" {
				t.Errorf("details に %q が無い: %s", tc.field, rec.Body.String())
			}
		})
	}
}

// 英語は pb-17 で追加した2つ目の表示言語。DBへ渡る値まで確かめる。
func TestPatchMeAcceptsEnglish(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me", `{"locale":"en"}`, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.myProfileParams) != 1 || q.myProfileParams[0].Locale.String != localeEn {
		t.Fatalf("locale の引数 = %+v, want en", q.myProfileParams)
	}
}

// UTC は名前だけで一意に定まるので通す（"Local" との対比）。
func TestPatchMeAcceptsUTC(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me", `{"timezone":"UTC"}`, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
}

// **すべての項目を見てから返す**（2.5 の details は項目ごとに紐づける）。
// 1つ目で打ち切ると、利用者が誤りを1つずつ潰すことになる。
func TestPatchMeReportsEveryInvalidField(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"theme":"sepia","hue":"red","locale":"fr"}`, selfPrincipal()))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	for _, field := range []string{"theme", "hue", "locale"} {
		if code := detailCodeOf(t, rec, field); code == "" {
			t.Errorf("details に %q が無い: %s", field, rec.Body.String())
		}
	}
}

// メールの重複は 6.4 と同じ already_exists に写す（4.2）。
func TestPatchMeMapsEmailConflict(t *testing.T) {
	q := meFake(t)
	q.myProfileErr = &pgconn.PgError{Code: "23505", ConstraintName: "app_user_email_key"}

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"email":"yamada@example.com"}`, selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if code := errorCodeOf(t, rec); code != "already_exists" {
		t.Errorf("error.code = %q, want already_exists", code)
	}
}

// 認証を通った直後に自分のアカウントが消えた場合。**401 に倒す**——
// 404 だと「/me というパスが無い」と読めてしまう。
func TestPatchMeReturns401WhenActorGone(t *testing.T) {
	q := meFake(t)
	q.myProfileRows = 0

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"display_name":"田中 太郎"}`, selfPrincipal()))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
}

// 監査は user.update を1件。**変更した項目だけ**を前後の形で入れる（4.2）。
func TestPatchMeRecordsAudit(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me",
		`{"theme":"dark"}`, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.audits) != 1 {
		t.Fatalf("監査ログが %d 件, want 1", len(q.audits))
	}
	a := q.audits[0]
	if a.Action != "user.update" {
		t.Errorf("action = %q, want user.update", a.Action)
	}
	if a.TargetID.String != testActorID {
		t.Errorf("target_id = %q, want %q", a.TargetID.String, testActorID)
	}
	detail := string(a.Detail)
	if !strings.Contains(detail, "theme") {
		t.Errorf("detail に theme が無い: %s", detail)
	}
	// 送っていない項目は detail に出さない。何が変わったかが読めなくなる。
	if strings.Contains(detail, "display_name") {
		t.Errorf("送っていない display_name が detail にある: %s", detail)
	}
}

// 空の PATCH は 200 で通し、監査は書かない（記録することが無い）。
func TestPatchMeWithNoChangesSkipsAudit(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.patchMe(rec, meReq(http.MethodPatch, "/api/v1/me", `{}`, selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.audits) != 0 {
		t.Errorf("変更が無いのに監査を書いた: %+v", q.audits)
	}
}

// ── POST /me/password（ApiDesign.md 4.3）───────────────────────

func TestChangeMyPasswordSucceeds(t *testing.T) {
	q := meFake(t)
	q.myRevokedCount = 3

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.changeMyPassword(rec, meReq(http.MethodPost, "/api/v1/me/password",
		`{"current_password":"`+testPassword+`","new_password":"brand-new-passphrase"}`,
		selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.myPasswordParams) != 1 {
		t.Fatalf("ChangeMyPassword の呼び出しが %d 回, want 1", len(q.myPasswordParams))
	}
	// **平文を保存しない。** 保存するのは PHC 文字列だけである。
	if got := q.myPasswordParams[0].PasswordHash; strings.Contains(got, "brand-new-passphrase") {
		t.Errorf("平文がハッシュに混ざっている: %q", got)
	}
	// 新しい平文でハッシュを照合できること（＝正しい値を保存した）。
	ok, err := auth.VerifyPassword("brand-new-passphrase", q.myPasswordParams[0].PasswordHash)
	if err != nil || !ok {
		t.Errorf("保存したハッシュが新しいパスワードと一致しない（err=%v）", err)
	}
}

// **現在のセッションだけを残す**（Design.md 6.3）。全失効にすると、
// 変更した本人がその操作の直後に締め出される。
func TestChangeMyPasswordKeepsCurrentSession(t *testing.T) {
	q := meFake(t)
	p := selfPrincipal()

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.changeMyPassword(rec, meReq(http.MethodPost, "/api/v1/me/password",
		`{"current_password":"`+testPassword+`","new_password":"brand-new-passphrase"}`, p))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.myRevokeParams) != 1 {
		t.Fatalf("RevokeMyOtherSessions の呼び出しが %d 回, want 1", len(q.myRevokeParams))
	}
	if got := q.myRevokeParams[0].CurrentTokenID; got != p.TokenID {
		t.Errorf("残すトークン = %q, want %q", got, p.TokenID)
	}
	if got := q.myRevokeParams[0].ActorID; got != testActorID {
		t.Errorf("actor_id = %q, want %q", got, testActorID)
	}
}

// 現在のパスワードが違えば 401 invalid_credentials（4.3）。
//
// **failed_attempts を増やさない**ことも同時に見る。増やすと、自分で自分を
// 締め出せてしまう。
func TestChangeMyPasswordRejectsWrongCurrent(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.changeMyPassword(rec, meReq(http.MethodPost, "/api/v1/me/password",
		`{"current_password":"wrong-password-x","new_password":"brand-new-passphrase"}`,
		selfPrincipal()))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
	}
	if code := errorCodeOf(t, rec); code != "invalid_credentials" {
		t.Errorf("error.code = %q, want invalid_credentials", code)
	}
	if len(q.failures) != 0 {
		t.Errorf("失敗回数を増やした: %+v", q.failures)
	}
	if len(q.myPasswordParams) != 0 {
		t.Errorf("検証に失敗したのにパスワードを書き換えた: %+v", q.myPasswordParams)
	}
	if len(q.myRevokeParams) != 0 {
		t.Errorf("検証に失敗したのにセッションを失効した: %+v", q.myRevokeParams)
	}
}

// 12文字未満は 422（Design.md 6.3）。作成時（6.2）と同じ関数を通す。
func TestChangeMyPasswordValidatesPolicy(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"新パスワードが短い", `{"current_password":"x","new_password":"short"}`, "new_password"},
		{"新パスワードが空", `{"current_password":"x","new_password":""}`, "new_password"},
		{"現パスワードが空", `{"current_password":"","new_password":"brand-new-passphrase"}`, "current_password"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := meFake(t)
			h, _ := newUserHandler(q)
			rec := httptest.NewRecorder()
			h.changeMyPassword(rec, meReq(http.MethodPost, "/api/v1/me/password", tc.body, selfPrincipal()))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			if code := detailCodeOf(t, rec, tc.field); code == "" {
				t.Errorf("details に %q が無い: %s", tc.field, rec.Body.String())
			}
		})
	}
}

// パスワード認証を使っていないアカウント（IdP のみ、Phase 3）は 409。
// 6.6（管理者によるリセット）と同じ扱いにする。
func TestChangeMyPasswordConflictsWithoutLocalCredential(t *testing.T) {
	q := meFake(t)
	q.myCredentialErr = pgx.ErrNoRows

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.changeMyPassword(rec, meReq(http.MethodPost, "/api/v1/me/password",
		`{"current_password":"`+testPassword+`","new_password":"brand-new-passphrase"}`,
		selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
}

// 監査は password.change を1件だけ。**session.revoke は足さない**（4.3）。
// 1つの操作が2行になると、監査ログの読み手が二重に数える。
func TestChangeMyPasswordRecordsSingleAudit(t *testing.T) {
	q := meFake(t)
	q.myRevokedCount = 2

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.changeMyPassword(rec, meReq(http.MethodPost, "/api/v1/me/password",
		`{"current_password":"`+testPassword+`","new_password":"brand-new-passphrase"}`,
		selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.audits) != 1 {
		t.Fatalf("監査ログが %d 件, want 1: %+v", len(q.audits), q.audits)
	}
	if q.audits[0].Action != "password.change" {
		t.Errorf("action = %q, want password.change", q.audits[0].Action)
	}
	detail := string(q.audits[0].Detail)
	if !strings.Contains(detail, "revoked_sessions") {
		t.Errorf("detail に revoked_sessions が無い: %s", detail)
	}
	// **平文は監査に残さない。** audit_log は長期保存される記録である。
	if strings.Contains(detail, "brand-new-passphrase") {
		t.Errorf("平文が detail に入っている: %s", detail)
	}
}

// 書き込みの順序を固定する。検証 → 書き換え → 失効 → 監査。
//
// **失効を書き換えより先に置かない。** 途中で失敗したときに、
// 古いパスワードのまま全端末が切れた状態が残る。
func TestChangeMyPasswordOperationOrder(t *testing.T) {
	q := meFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.changeMyPassword(rec, meReq(http.MethodPost, "/api/v1/me/password",
		`{"current_password":"`+testPassword+`","new_password":"brand-new-passphrase"}`,
		selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	want := []string{"ChangeMyPassword", "RevokeMyOtherSessions", "InsertAuditLog"}
	if !slices.Equal(q.opLog, want) {
		t.Errorf("書き込みの順序 = %v, want %v", q.opLog, want)
	}
}

// ── 小さな助け ──────────────────────────────────────────────

// detailCodeOf は 2.5 の details から field に対応する code を取り出す。
// 無ければ空文字。
func detailCodeOf(t *testing.T, rec *httptest.ResponseRecorder, field string) string {
	t.Helper()
	body := viewOf(t, rec)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		return ""
	}
	details, ok := errObj["details"].([]any)
	if !ok {
		return ""
	}
	for _, d := range details {
		item, ok := d.(map[string]any)
		if !ok {
			continue
		}
		if item["field"] == field {
			code, _ := item["code"].(string)
			return code
		}
	}
	return ""
}

// errorCodeOf は 2.5 の error.code を取り出す。
func errorCodeOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	body := viewOf(t, rec)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		return ""
	}
	code, _ := errObj["code"].(string)
	return code
}
