package v1

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// newUserHandler は一覧・作成のテストが使うハンドラ。
//
// 認証・認可はミドルウェアの責務なので通さない（routes_test.go が別に見ている）。
// ここで確かめるのはクエリパラメータの解釈・応答の形・検証・409 の分岐である。
func newUserHandler(q *fakeQuerier) (*handler, *fakeTxRunner) {
	tx := &fakeTxRunner{q: q}
	return &handler{q: q, tx: tx}, tx
}

// listUsers はトランザクションを使わないので、戻り値の2つ目を捨てる。
func newUserListHandler(q *fakeQuerier) *handler {
	h, _ := newUserHandler(q)
	return h
}

func userRow(id, kind, name, email, role string, active bool, projects int64) gen.ListAdminUsersRow {
	row := gen.ListAdminUsersRow{
		ID:           id,
		Kind:         kind,
		DisplayName:  name,
		IsActive:     active,
		ProjectCount: projects,
		CreatedAt:    ts(time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)),
	}
	if email != "" {
		row.Email = txt(email)
	}
	if role != "" {
		row.SystemRole = txt(role)
	}
	return row
}

// ── GET /admin/users（ApiDesign.md 6.1）─────────────────────────

func TestListUsersDefaults(t *testing.T) {
	q := &fakeQuerier{
		userRows: []gen.ListAdminUsersRow{
			userRow(testActorID, "user", "田中", testEmail, "administrator", true, 3),
		},
		userSummary: gen.SummarizeAdminUsersRow{
			Total:         1,
			LastUpdatedAt: ts(time.Date(2026, 8, 11, 9, 3, 12, 0, time.UTC)),
		},
	}
	rec := httptest.NewRecorder()
	newUserListHandler(q).listUsers(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("状態コード = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	// 既定値（6.1 と 2.6）。order は 6.1 に記述が無く、手順12a で asc と決めた。
	arg := q.userListParam[0]
	if arg.KindFilter != "all" || arg.ActiveFilter != "all" || arg.QPattern != "" {
		t.Errorf("絞り込みの既定 = %+v, want kind=all is_active=all q=空", arg)
	}
	if arg.Sort != "display_name" || arg.SortOrder != "asc" {
		t.Errorf("並びの既定 = %s %s, want display_name asc", arg.Sort, arg.SortOrder)
	}
	if arg.PageLimit != 25 || arg.PageOffset != 0 {
		t.Errorf("ページの既定 = limit %d offset %d, want 25 0", arg.PageLimit, arg.PageOffset)
	}

	// 総件数は同じ絞り込みで数える（2.6）。
	if got := q.userSumParam[0]; got.KindFilter != arg.KindFilter ||
		got.ActiveFilter != arg.ActiveFilter || got.QPattern != arg.QPattern {
		t.Errorf("総件数の絞り込みが一覧と違う: list=%+v summary=%+v", arg, got)
	}

	// ETag（2.7）。弱い検証子で、W/ は引用符の外に置く（RFC 9110 8.8.3）。
	if etag := rec.Header().Get("ETag"); !strings.HasPrefix(etag, `W/"user-1-`) {
		t.Errorf("ETag = %q, want W/\"user-1-… で始まる", etag)
	}

	body := viewOf(t, rec)
	items, ok := body["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("items = %v, want 1件", body["items"])
	}
	item := items[0].(map[string]any)
	for _, f := range []string{
		"id", "kind", "display_name", "email", "system_role", "agent",
		"is_active", "last_login_at", "project_count", "created_at",
	} {
		if _, ok := item[f]; !ok {
			t.Errorf("items[0] に %s が無い。6.1 はフィールドを省略しないと定める", f)
		}
	}
	if item["agent"] != nil {
		t.Errorf("agent = %v, want null（Phase 1 に agent テーブルが無い）", item["agent"])
	}
	if item["project_count"] != float64(3) {
		t.Errorf("project_count = %v, want 3", item["project_count"])
	}
}

// エージェントの行は email / system_role / last_login_at が null になる（6.1）。
func TestListUsersAgentNullFields(t *testing.T) {
	q := &fakeQuerier{
		userRows:    []gen.ListAdminUsersRow{userRow("01K2AGENT", "agent", "claude-code (my-app)", "", "", true, 1)},
		userSummary: gen.SummarizeAdminUsersRow{Total: 1},
	}
	rec := httptest.NewRecorder()
	newUserListHandler(q).listUsers(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?kind=agent", nil))

	item := viewOf(t, rec)["items"].([]any)[0].(map[string]any)
	for _, f := range []string{"email", "system_role", "last_login_at", "agent"} {
		if v, ok := item[f]; !ok || v != nil {
			t.Errorf("%s = %v (存在 %v), want null", f, v, ok)
		}
	}
	if item["kind"] != "agent" {
		t.Errorf("kind = %v, want agent", item["kind"])
	}
}

func TestListUsersFilters(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		wantKind   string
		wantActive string
		wantQ      string
	}{
		{"種別", "?kind=user", "user", "all", ""},
		{"状態", "?is_active=false", "all", "false", ""},
		{"検索", "?q=yamada", "all", "all", "%yamada%"},
		{"メタ文字のエスケープ", "?q=a_b%25c", "all", "all", `%a\_b\%c%`},
		{"空白だけの検索は絞り込まない", "?q=%20%20", "all", "all", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQuerier{userSummary: gen.SummarizeAdminUsersRow{}}
			rec := httptest.NewRecorder()
			newUserListHandler(q).listUsers(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users"+tt.query, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("状態コード = %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			arg := q.userListParam[0]
			if arg.KindFilter != tt.wantKind || arg.ActiveFilter != tt.wantActive || arg.QPattern != tt.wantQ {
				t.Errorf("= kind %q active %q q %q, want %q %q %q",
					arg.KindFilter, arg.ActiveFilter, arg.QPattern, tt.wantKind, tt.wantActive, tt.wantQ)
			}
		})
	}
}

// 解釈できない値は既定へ丸めず 422（ApiDesign.md 2.6）。
// 6.1 が許可するソート項目をすべて受け付ける（手順12c で system_role と
// is_active が加わった）。**並びの正しさはDBが決める**ので、ここでは
// クエリ層へそのまま渡ることだけを見る（実際の行順は結合テスト）。
func TestListUsersAcceptsAllSortFields(t *testing.T) {
	for _, sort := range []string{
		"display_name", "email", "system_role", "is_active", "last_login_at", "created_at",
	} {
		t.Run(sort, func(t *testing.T) {
			q := &fakeQuerier{}
			rec := httptest.NewRecorder()
			newUserListHandler(q).listUsers(rec,
				httptest.NewRequest(http.MethodGet, "/api/v1/admin/users?sort="+sort+"&order=desc", nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("状態コード = %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			if len(q.userListParam) != 1 {
				t.Fatalf("クエリ回数 = %d, want 1", len(q.userListParam))
			}
			if got := q.userListParam[0]; got.Sort != sort || got.SortOrder != "desc" {
				t.Errorf("並び = %s %s, want %s desc", got.Sort, got.SortOrder, sort)
			}
		})
	}
}

func TestListUsersInvalidQuery(t *testing.T) {
	tests := []struct {
		name  string
		query string
		field string
	}{
		{"種別", "?kind=system", "kind"},
		{"状態", "?is_active=yes", "is_active"},
		{"ソート項目", "?sort=password", "sort"},
		{"並び順", "?order=up", "order"},
		{"ページ", "?page=0", "page"},
		{"件数の上限", "?per_page=201", "per_page"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQuerier{}
			rec := httptest.NewRecorder()
			newUserListHandler(q).listUsers(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/users"+tt.query, nil))
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("状態コード = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			e := errorOf(t, rec)
			if e.Code != "validation_failed" {
				t.Errorf("code = %q, want validation_failed", e.Code)
			}
			var found bool
			for _, d := range e.Details {
				if d.Field == tt.field {
					found = true
				}
			}
			if !found {
				t.Errorf("details に %s が無い: %+v", tt.field, e.Details)
			}
			if len(q.userListParam) != 0 {
				t.Errorf("422 なのにDBを引いている")
			}
		})
	}
}

// 検証の誤りは1回の応答にまとめる（2.5）。
func TestListUsersReportsAllInvalidParams(t *testing.T) {
	q := &fakeQuerier{}
	rec := httptest.NewRecorder()
	newUserListHandler(q).listUsers(rec, httptest.NewRequest(http.MethodGet,
		"/api/v1/admin/users?kind=x&is_active=y&page=0", nil))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("状態コード = %d, want 422", rec.Code)
	}
	fields := map[string]bool{}
	for _, d := range errorOf(t, rec).Details {
		fields[d.Field] = true
	}
	for _, f := range []string{"kind", "is_active", "page"} {
		if !fields[f] {
			t.Errorf("details に %s が無い: %v", f, fields)
		}
	}
}

func TestLikePattern(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""},
		{"   ", ""},
		{"yamada", "%yamada%"},
		{" 田中 ", "%田中%"},
		{"50%", `%50\%%`},
		{"a_b", `%a\_b%`},
		{`a\b`, `%a\\b%`},
		// バックスラッシュを先に処理しても、後続の置換が生んだ \ を
		// 拾い直さないことの確認（NewReplacer は1度しか走査しない）。
		{`\%`, `%\\\%%`},
	}
	for _, tt := range tests {
		if got := likePattern(tt.in); got != tt.want {
			t.Errorf("likePattern(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// ── POST /admin/users（ApiDesign.md 6.2）────────────────────────

func postUser(t *testing.T, q *fakeQuerier, body string) (*httptest.ResponseRecorder, *fakeTxRunner) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/admin/users", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h, tx := newUserHandler(q)
	h.createUser(rec, r)
	return rec, tx
}

func TestCreateUserGeneratesPassword(t *testing.T) {
	q := &fakeQuerier{}
	rec, tx := postUser(t, q, `{"display_name":"山田 太郎","email":"yamada@example.com"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("状態コード = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}

	// **単一トランザクションで4表を順に作る**（6.2、DbDesign.md 6.2）。
	// 監査も同じトランザクション（手順4b の方針）。
	want := []string{"CreateUserActor", "CreateAppUser", "CreateUserIdentity", "CreateLocalCredential", "InsertAuditLog"}
	if strings.Join(q.opLog, ",") != strings.Join(want, ",") {
		t.Errorf("呼び出し順 = %v, want %v", q.opLog, want)
	}
	if !tx.committed {
		t.Errorf("トランザクションがコミットされていない")
	}

	body := viewOf(t, rec)
	pw, ok := body["generated_password"].(string)
	if !ok || pw == "" {
		t.Fatalf("generated_password = %v, want 生成された平文", body["generated_password"])
	}
	// 形式は <形容詞>-<名詞>-<4桁>-<名詞>（6.2.1）。
	if parts := strings.Split(pw, "-"); len(parts) != 4 || len(parts[2]) != 4 {
		t.Errorf("generated_password = %q, want <形容詞>-<名詞>-<4桁数字>-<名詞>", pw)
	}
	if err := auth.ValidatePassword(pw); err != nil {
		t.Errorf("生成した平文が最小長を満たさない: %v", err)
	}

	// 保存したハッシュが、返した平文と照合できること。
	cred := q.createdCreds[0]
	okMatch, err := auth.VerifyPassword(pw, cred.PasswordHash)
	if err != nil || !okMatch {
		t.Errorf("返した平文と保存したハッシュが一致しない（err=%v）", err)
	}
	// 省略時の must_change_password は true（GuiDesign.md 5.6.1）。
	if !cred.MustChange {
		t.Errorf("must_change = false, want true（省略時の既定）")
	}
	// 省略時の system_role は operator（6.2）。
	if q.createdUsers[0].SystemRole != "operator" {
		t.Errorf("system_role = %q, want operator", q.createdUsers[0].SystemRole)
	}
	// user_identity.subject は app_user.email と同じ文字列（DbDesign.md 6.2）。
	if q.createdIdents[0].Subject != q.createdUsers[0].Email {
		t.Errorf("subject = %q, email = %q。突き合わせに失敗する",
			q.createdIdents[0].Subject, q.createdUsers[0].Email)
	}
	if q.createdIdents[0].ProviderKey != "local" {
		t.Errorf("provider_key = %q, want local", q.createdIdents[0].ProviderKey)
	}
	if rec.Header().Get("Location") == "" {
		t.Errorf("Location ヘッダが無い")
	}

	// **監査ログに平文を残さない**（6.2 の「この応答でのみ返る」）。
	detail := string(q.audits[0].Detail)
	if strings.Contains(detail, pw) {
		t.Errorf("audit_log.detail に平文が入っている: %s", detail)
	}
	if q.audits[0].Action != "user.create" {
		t.Errorf("action = %q, want user.create", q.audits[0].Action)
	}
}

func TestCreateUserManualPassword(t *testing.T) {
	q := &fakeQuerier{}
	rec, _ := postUser(t, q, `{"display_name":"佐藤 花子","email":"sato@example.com",
		"system_role":"administrator","password_mode":"manual",
		"password":"correct-horse-battery","must_change_password":false}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("状態コード = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	body := viewOf(t, rec)
	// manual では平文を返さない（呼び出し側が既に知っている）。
	// **フィールド自体は省略しない**（6.1 の規約に揃える）。
	v, ok := body["generated_password"]
	if !ok {
		t.Errorf("generated_password のフィールドが無い。null で返すこと")
	}
	if v != nil {
		t.Errorf("generated_password = %v, want null（manual）", v)
	}
	if body["system_role"] != "administrator" {
		t.Errorf("system_role = %v, want administrator", body["system_role"])
	}
	if q.createdCreds[0].MustChange {
		t.Errorf("must_change = true, want false（明示的に false を送った）")
	}
	okMatch, err := auth.VerifyPassword("correct-horse-battery", q.createdCreds[0].PasswordHash)
	if err != nil || !okMatch {
		t.Errorf("手動指定のパスワードが保存されていない（err=%v）", err)
	}
}

func TestCreateUserValidation(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		field string
	}{
		{"表示名なし", `{"email":"a@example.com"}`, "display_name"},
		{"表示名が空白だけ", `{"display_name":"   ","email":"a@example.com"}`, "display_name"},
		{"表示名が長すぎる", `{"display_name":"` + strings.Repeat("あ", 61) + `","email":"a@example.com"}`, "display_name"},
		{"メールなし", `{"display_name":"A"}`, "email"},
		{"メールの形式", `{"display_name":"A","email":"not-an-email"}`, "email"},
		{"表示名付きメール", `{"display_name":"A","email":"山田 <a@example.com>"}`, "email"},
		{"ロールが不正", `{"display_name":"A","email":"a@example.com","system_role":"root"}`, "system_role"},
		{"モードが不正", `{"display_name":"A","email":"a@example.com","password_mode":"auto"}`, "password_mode"},
		{"manual でパスワードなし", `{"display_name":"A","email":"a@example.com","password_mode":"manual"}`, "password"},
		{"manual でパスワードが短い", `{"display_name":"A","email":"a@example.com","password_mode":"manual","password":"short"}`, "password"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q := &fakeQuerier{}
			rec, _ := postUser(t, q, tt.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("状態コード = %d, want 422 (%s)", rec.Code, rec.Body.String())
			}
			var found bool
			for _, d := range errorOf(t, rec).Details {
				if d.Field == tt.field {
					found = true
				}
			}
			if !found {
				t.Errorf("details に %s が無い: %+v", tt.field, errorOf(t, rec).Details)
			}
			if len(q.opLog) != 0 {
				t.Errorf("422 なのに書き込んでいる: %v", q.opLog)
			}
		})
	}
}

// generate のときに password が送られても弾かない（モード切替の残りを許す）。
func TestCreateUserIgnoresPasswordInGenerateMode(t *testing.T) {
	q := &fakeQuerier{}
	rec, _ := postUser(t, q, `{"display_name":"A","email":"a@example.com",
		"password_mode":"generate","password":"short"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("状態コード = %d, want 201 (%s)", rec.Code, rec.Body.String())
	}
	pw := viewOf(t, rec)["generated_password"].(string)
	if pw == "short" {
		t.Errorf("送られた password を使ってしまっている")
	}
}

// メールの重複は 409 already_exists（6.2）。
func TestCreateUserDuplicateEmail(t *testing.T) {
	q := &fakeQuerier{createUserErr: &pgconn.PgError{Code: "23505", TableName: "app_user"}}
	rec, tx := postUser(t, q, `{"display_name":"山田","email":"yamada@example.com"}`)

	if rec.Code != http.StatusConflict {
		t.Fatalf("状態コード = %d, want 409 (%s)", rec.Code, rec.Body.String())
	}
	e := errorOf(t, rec)
	if e.Code != "already_exists" {
		t.Errorf("code = %q, want already_exists", e.Code)
	}
	if !strings.Contains(e.Message, "yamada@example.com") {
		t.Errorf("message = %q, want メールアドレスを含む", e.Message)
	}
	if tx.committed {
		t.Errorf("409 なのにコミットされている")
	}
}

// 一意制約以外の失敗は 500 に倒す（既存ユーザーの存在を漏らさない）。
func TestCreateUserOtherDBErrorIs500(t *testing.T) {
	q := &fakeQuerier{createUserErr: &pgconn.PgError{Code: "23514", TableName: "app_user"}}
	rec, _ := postUser(t, q, `{"display_name":"山田","email":"yamada@example.com"}`)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("状態コード = %d, want 500 (%s)", rec.Code, rec.Body.String())
	}
}

func TestValidateEmail(t *testing.T) {
	ok := []string{"a@example.com", "tanaka+pb@example.co.jp", "x.y@sub.example.com"}
	for _, e := range ok {
		if d := validateEmail(e); d != nil {
			t.Errorf("validateEmail(%q) = %+v, want nil", e, d)
		}
	}
	ng := []string{"", "plain", "a@", "@example.com", "a b@example.com",
		"山田 <a@example.com>", "<a@example.com>", strings.Repeat("a", 250) + "@example.com"}
	for _, e := range ng {
		if d := validateEmail(e); d == nil {
			t.Errorf("validateEmail(%q) = nil, want 誤り", e)
		}
	}
}

func TestGeneratePasswordShape(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		pw, err := auth.GeneratePassword()
		if err != nil {
			t.Fatalf("GeneratePassword: %v", err)
		}
		if err := auth.ValidatePassword(pw); err != nil {
			t.Fatalf("生成した %q が最小長を満たさない: %v", pw, err)
		}
		parts := strings.Split(pw, "-")
		if len(parts) != 4 {
			t.Fatalf("%q は <形容詞>-<名詞>-<4桁数字>-<名詞> でない", pw)
		}
		if len(parts[2]) != 4 {
			t.Fatalf("%q の数字が4桁でない", pw)
		}
		if strings.ToLower(pw) != pw {
			t.Fatalf("%q に大文字が混ざっている（読み上げ・転記のため小文字のみ）", pw)
		}
		seen[pw] = true
	}
	// 同じ値ばかり出ていないこと（乱数が働いているかの粗い確認）。
	if len(seen) < 100 {
		t.Errorf("200回で異なる値が %d 種類しか出ていない", len(seen))
	}
}

// 応答が snake_case であること（ApiDesign.md 2.2）。
func TestCreateUserResponseFieldNames(t *testing.T) {
	q := &fakeQuerier{}
	rec, _ := postUser(t, q, `{"display_name":"A","email":"a@example.com"}`)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("応答が JSON でない: %v", err)
	}
	for _, f := range []string{"id", "kind", "display_name", "email", "system_role", "is_active", "generated_password"} {
		if _, ok := raw[f]; !ok {
			t.Errorf("応答に %s が無い: %v", f, rec.Body.String())
		}
	}
}
