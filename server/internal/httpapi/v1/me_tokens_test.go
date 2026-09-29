package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// アクセストークン管理（ApiDesign.md 4.4、手順15b）のハンドラ単体テスト。
//
// 認証はミドルウェアの責務なので通さない（routes_test.go が別に見ている）。
// ここで確かめるのは**応答の形・検証の分岐・監査に何を書くか**である。
// 実DBでしか確かめられないもの（token_type の絞り込み・他人のトークン・
// 冪等な失効）は me_tokens_integration_test.go にある。

// ── 応答を読むための型 ──────────────────────────────────────
//
// **応答の型（accessTokenView）をそのまま使わない。** v1.Time は
// MarshalJSON だけを持ち（ApiDesign.md 2.2 の表記を1か所に固定するため）、
// 読み戻す口が無い。日時は文字列として受け、必要なら test 側で解釈する。
type tokenJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Token       string   `json:"token"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	IssuedAt    int64    `json:"issued_at"`
	LastUsedAt  *int64   `json:"last_used_at"`
	ExpiresAt   *int64   `json:"expires_at"`
	Status      string   `json:"status"`
}

type tokenListJSON struct {
	Items []tokenJSON `json:"items"`
}

// tokenFake は 4.4 のテストが使う既定のフェイク。
//
// **「通る」状態を既定にして、テストごとに1つだけ崩す**（meFake と同じ型）。
func tokenFake(t *testing.T) *fakeQuerier {
	t.Helper()
	q := newFake(t)
	q.profileRow = meProfileRow()
	q.catalogPerms = []gen.Permission{
		{Key: "project.view", Category: "project", Description: "プロジェクトの閲覧"},
		{Key: "ticket.view", Category: "ticket", Description: "チケットの閲覧"},
		{Key: "user.manage", Category: "admin", Description: "ユーザー管理"},
	}
	q.myTokenRevokedRows = 1
	return q
}

// tokenReq は /me/tokens 系のリクエストを組み立てる。
//
// id はパスパラメータ（4.4.3）。空文字なら付けない。**chi の RouteContext を
// 自分で載せる**——ハンドラを直接呼ぶのでルータを通らず、chi.URLParam が
// 空を返してしまうためである。
func tokenReq(method, target, body, id string, p *auth.Principal) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	ctx := auth.NewPrincipalContext(req.Context(), p)
	if id != "" {
		rc := chi.NewRouteContext()
		rc.URLParams.Add("id", id)
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
	}
	return req.WithContext(ctx)
}

// ── GET /me/tokens（ApiDesign.md 4.4.1）─────────────────────────

func TestListMyTokensReturnsItemsWithoutPlaintext(t *testing.T) {
	now := time.Now()
	q := tokenFake(t)
	q.myTokenRows = []gen.ListMyAPITokensRow{
		{
			ID: "01K2TOKEN0000000000000001", Name: txt("CLI (MacBook)"),
			TokenPrefix: txt("pb_api_9"), Scopes: []byte(`[]`),
			IssuedAt: ts(now.Add(-time.Hour)), LastUsedAt: tsp(now.Add(-5 * time.Minute)),
			ExpiresAt: tsp(now.Add(90 * 24 * time.Hour)),
		},
		{
			ID: "01K2TOKEN0000000000000002", Name: txt("CI"),
			TokenPrefix: txt("pb_api_3"), Scopes: []byte(`["ticket.view"]`),
			IssuedAt: ts(now.Add(-48 * time.Hour)),
			// last_used_at は NULL（一度も使われていない）
			ExpiresAt: tsp(now.Add(-time.Hour)),
		},
	}

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.listMyTokens(rec, tokenReq(http.MethodGet, "/api/v1/me/tokens", "", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}

	var got tokenListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if len(got.Items) != 2 {
		t.Fatalf("items = %d件, want 2", len(got.Items))
	}

	// **平文が応答に現れないこと。** 4.4.1 が返すのは token_prefix だけである。
	if strings.Contains(rec.Body.String(), `"token"`) {
		t.Errorf("一覧に token が含まれている: %s", rec.Body.String())
	}

	first := got.Items[0]
	if first.Name != "CLI (MacBook)" || first.TokenPrefix != "pb_api_9" {
		t.Errorf("items[0] = %+v", first)
	}
	if first.Status != tokenStatusActive {
		t.Errorf("items[0].status = %q, want %q", first.Status, tokenStatusActive)
	}
	if first.LastUsedAt == nil {
		t.Error("items[0].last_used_at が null になっている")
	}
	if len(first.Scopes) != 0 {
		t.Errorf("items[0].scopes = %v, want []（絞り込みなし）", first.Scopes)
	}

	second := got.Items[1]
	if second.Status != tokenStatusExpired {
		t.Errorf("items[1].status = %q, want %q（expires_at を過ぎている）",
			second.Status, tokenStatusExpired)
	}
	if second.LastUsedAt != nil {
		t.Errorf("items[1].last_used_at = %v, want null（一度も使われていない）", second.LastUsedAt)
	}
	if len(second.Scopes) != 1 || second.Scopes[0] != "ticket.view" {
		t.Errorf("items[1].scopes = %v, want [ticket.view]", second.Scopes)
	}
}

// **0件でも items は null ではなく空配列。** 画面が `items.length` を
// そのまま読めるようにする（2.6 の一覧と同じ扱い）。
func TestListMyTokensReturnsEmptyArrayNotNull(t *testing.T) {
	q := tokenFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.listMyTokens(rec, tokenReq(http.MethodGet, "/api/v1/me/tokens", "", "", selfPrincipal()))

	if body := rec.Body.String(); !strings.Contains(body, `"items":[]`) {
		t.Errorf("body = %s, want items が空配列", body)
	}
}

// scopes が壊れていても一覧は返す。**その行を失効させる導線を残す**ため。
func TestListMyTokensToleratesBrokenScopes(t *testing.T) {
	q := tokenFake(t)
	q.myTokenRows = []gen.ListMyAPITokensRow{{
		ID: "01K2TOKEN0000000000000001", Name: txt("壊れたスコープ"),
		TokenPrefix: txt("pb_api_9"), Scopes: []byte(`{"not":"an array"}`),
		IssuedAt: ts(time.Now()), ExpiresAt: tsp(time.Now().Add(time.Hour)),
	}}

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.listMyTokens(rec, tokenReq(http.MethodGet, "/api/v1/me/tokens", "", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"scopes":[]`) {
		t.Errorf("body = %s, want scopes が空配列", rec.Body.String())
	}
}

// ── POST /me/tokens（ApiDesign.md 4.4.2）────────────────────────

func TestCreateMyTokenIssuesPlaintextOnce(t *testing.T) {
	q := tokenFake(t)
	h, tx := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens",
		`{"name":"CLI (MacBook)","expires_in_days":90}`, "", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}

	var got tokenJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	if !strings.HasPrefix(got.Token, auth.APITokenPrefix) {
		t.Errorf("token = %q, want %q で始まる", got.Token, auth.APITokenPrefix)
	}
	if got.TokenPrefix != auth.TokenPrefix(got.Token) {
		t.Errorf("token_prefix = %q, want %q", got.TokenPrefix, auth.TokenPrefix(got.Token))
	}
	if got.Status != tokenStatusActive {
		t.Errorf("status = %q, want %q", got.Status, tokenStatusActive)
	}
	if got.ExpiresAt == nil {
		t.Fatal("expires_at が null（4.4.2 は無期限を許さない）")
	}

	// **DB に載るのはハッシュだけ**（DbDesign.md 6.2）。
	if len(q.created) != 1 {
		t.Fatalf("CreateAccessToken の呼び出し = %d回, want 1", len(q.created))
	}
	arg := q.created[0]
	if arg.TokenType != auth.TokenTypeAPI {
		t.Errorf("token_type = %q, want %q", arg.TokenType, auth.TokenTypeAPI)
	}
	if arg.TokenHash != auth.HashToken(got.Token) {
		t.Error("token_hash が平文の SHA-256 と一致しない")
	}
	if arg.TokenHash == got.Token {
		t.Error("平文がそのまま token_hash に入っている")
	}
	if arg.Name.String != "CLI (MacBook)" {
		t.Errorf("name = %q", arg.Name.String)
	}
	if arg.ProjectID.Valid {
		t.Error("project_id が入っている。人のトークンは常に NULL（全プロジェクト）")
	}
	if string(arg.Scopes) != `[]` {
		t.Errorf("scopes = %s, want []（絞り込みなし）", arg.Scopes)
	}
	if arg.ExpiresAt == nil {
		t.Error("expires_at が NULL（無期限は許さない）")
	}

	// **有効期限は「発行から N 日後」である。**
	wantExpiry := time.Now().AddDate(0, 0, 90)
	if d := (*arg.ExpiresAt).Sub(wantExpiry); d > time.Minute || d < -time.Minute {
		t.Errorf("expires_at = %v, want %v 付近", (*arg.ExpiresAt), wantExpiry)
	}

	if !tx.committed {
		t.Error("トランザクションがコミットされていない")
	}
}

// 上限（5本）に達していたら 409。**数えるのと入れるのが同じトランザクション**。
func TestCreateMyTokenRejectsWhenAtLimit(t *testing.T) {
	q := tokenFake(t)
	q.myTokenCount = maxAPITokensPerActor

	h, tx := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens",
		`{"name":"6本目","expires_in_days":30}`, "", selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.created) != 0 {
		t.Error("上限に達しているのにトークンが作られた")
	}
	if tx.committed {
		t.Error("409 なのにコミットされている")
	}
	if !strings.Contains(rec.Body.String(), "5本") {
		t.Errorf("message に本数が出ていない: %s", rec.Body.String())
	}
}

// 上限の1つ手前（4本）は通る。**境界の内側を別に確かめる**。
func TestCreateMyTokenAllowsJustBelowLimit(t *testing.T) {
	q := tokenFake(t)
	q.myTokenCount = maxAPITokensPerActor - 1

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens",
		`{"name":"5本目","expires_in_days":30}`, "", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
}

func TestCreateMyTokenValidatesInput(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
		code  string
	}{
		{"名前が空", `{"name":"   ","expires_in_days":90}`, "name", "required"},
		{"名前が長すぎる",
			`{"name":"` + strings.Repeat("あ", tokenNameMaxLen+1) + `","expires_in_days":90}`,
			"name", "too_long"},
		{"有効期限が無い", `{"name":"CLI"}`, "expires_in_days", "required"},
		{"有効期限が0", `{"name":"CLI","expires_in_days":0}`, "expires_in_days", "invalid"},
		{"有効期限が長すぎる", `{"name":"CLI","expires_in_days":366}`, "expires_in_days", "invalid"},
		{"スコープが空文字", `{"name":"CLI","expires_in_days":90,"scopes":[""]}`, "scopes", "invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := tokenFake(t)
			h, _ := newUserHandler(q)
			rec := httptest.NewRecorder()
			h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens", c.body, "", selfPrincipal()))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
			}
			if !hasDetail(errorOf(t, rec), c.field, c.code) {
				t.Errorf("details に %s/%s が無い: %s", c.field, c.code, rec.Body.String())
			}
			if len(q.created) != 0 {
				t.Error("422 なのにトークンが作られた")
			}
		})
	}
}

// **境界の内側**（1日・365日・100文字）は通ることを確かめる。
func TestCreateMyTokenAcceptsBoundaryValues(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"1日", `{"name":"CLI","expires_in_days":1}`},
		{"365日", `{"name":"CLI","expires_in_days":365}`},
		{"名前100文字",
			`{"name":"` + strings.Repeat("あ", tokenNameMaxLen) + `","expires_in_days":90}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := tokenFake(t)
			h, _ := newUserHandler(q)
			rec := httptest.NewRecorder()
			h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens", c.body, "", selfPrincipal()))

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
			}
		})
	}
}

// スコープの語彙は**権限カタログのキー**（4.4.2）。カタログに無い値は 422。
func TestCreateMyTokenRejectsUnknownScope(t *testing.T) {
	q := tokenFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	// 4.4 の旧例（ticket:read）は権限キーではない。これが通ると
	// 「絞ったつもりで権限0件」のトークンが発行できてしまう。
	h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens",
		`{"name":"CLI","expires_in_days":90,"scopes":["ticket:read"]}`, "", selfPrincipal()))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
	}
	if !hasDetail(errorOf(t, rec), "scopes", "invalid") {
		t.Errorf("details に scopes/invalid が無い: %s", rec.Body.String())
	}
	if len(q.created) != 0 {
		t.Error("422 なのにトークンが作られた")
	}
}

func TestCreateMyTokenAcceptsPermissionKeyScopes(t *testing.T) {
	q := tokenFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens",
		`{"name":"読み取り専用","expires_in_days":90,"scopes":["ticket.view","project.view"]}`,
		"", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.created) != 1 {
		t.Fatalf("CreateAccessToken の呼び出し = %d回", len(q.created))
	}
	if s := string(q.created[0].Scopes); s != `["ticket.view","project.view"]` {
		t.Errorf("scopes = %s", s)
	}
}

// 監査（2.10）。**平文を detail に入れない。**
func TestCreateMyTokenRecordsAudit(t *testing.T) {
	q := tokenFake(t)
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.createMyToken(rec, tokenReq(http.MethodPost, "/api/v1/me/tokens",
		`{"name":"CLI (MacBook)","expires_in_days":90}`, "", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.audits) != 1 {
		t.Fatalf("監査ログ = %d件, want 1", len(q.audits))
	}
	a := q.audits[0]
	if a.Action != "token.issue" {
		t.Errorf("action = %q, want token.issue", a.Action)
	}
	if a.TargetType.String != "access_token" {
		t.Errorf("target_type = %q, want access_token", a.TargetType.String)
	}

	var got tokenJSON
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if a.TargetID.String != got.ID {
		t.Errorf("target_id = %q, want %q（発行されたトークンの ID）", a.TargetID.String, got.ID)
	}
	if strings.Contains(string(a.Detail), got.Token) {
		t.Error("監査ログの detail に平文が入っている")
	}
	if !strings.Contains(string(a.Detail), "CLI (MacBook)") {
		t.Errorf("detail に name が無い: %s", a.Detail)
	}
}

// ── DELETE /me/tokens/{id}（ApiDesign.md 4.4.3）─────────────────

func TestDeleteMyTokenRevokes(t *testing.T) {
	const id = "01K2TOKEN0000000000000001"
	q := tokenFake(t)
	q.myTokenRow = gen.FindMyAPITokenRow{
		ID: id, Name: txt("CLI (MacBook)"), TokenPrefix: txt("pb_api_9"),
	}

	h, tx := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.deleteMyToken(rec, tokenReq(http.MethodDelete, "/api/v1/me/tokens/"+id, "", id, selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.myTokenRevokes) != 1 {
		t.Fatalf("RevokeMyAPIToken の呼び出し = %d回, want 1", len(q.myTokenRevokes))
	}
	// **持ち主の確認をクエリの条件で行う**（4.4.3）。
	if q.myTokenRevokes[0].ActorID != testActorID || q.myTokenRevokes[0].ID != id {
		t.Errorf("失効の引数 = %+v", q.myTokenRevokes[0])
	}
	if !tx.committed {
		t.Error("トランザクションがコミットされていない")
	}

	if len(q.audits) != 1 {
		t.Fatalf("監査ログ = %d件, want 1", len(q.audits))
	}
	if q.audits[0].Action != "token.revoke" {
		t.Errorf("action = %q, want token.revoke", q.audits[0].Action)
	}
	if !strings.Contains(string(q.audits[0].Detail), "pb_api_9") {
		t.Errorf("detail に token_prefix が無い: %s", q.audits[0].Detail)
	}
}

// **冪等**（4.4.3）。既に失効済みでも 204 で、監査ログは足さない。
func TestDeleteMyTokenIsIdempotent(t *testing.T) {
	const id = "01K2TOKEN0000000000000001"
	q := tokenFake(t)
	q.myTokenRow = gen.FindMyAPITokenRow{
		ID: id, Name: txt("CLI"), TokenPrefix: txt("pb_api_9"),
		RevokedAt: tsp(time.Now().Add(-time.Hour)),
	}

	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.deleteMyToken(rec, tokenReq(http.MethodDelete, "/api/v1/me/tokens/"+id, "", id, selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.myTokenRevokes) != 0 {
		t.Error("失効済みなのに UPDATE が走った（revoked_at を上書きしている）")
	}
	if len(q.audits) != 0 {
		t.Errorf("監査ログ = %d件, want 0（同じ操作が2行になる）", len(q.audits))
	}
}

// 他人のトークン・セッション・存在しない ID は**すべて 404**（4.4.3）。
func TestDeleteMyTokenReturns404WhenNotFound(t *testing.T) {
	q := tokenFake(t)
	q.myTokenFindErr = pgx.ErrNoRows

	h, tx := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.deleteMyToken(rec, tokenReq(http.MethodDelete,
		"/api/v1/me/tokens/01K2OTHER000000000000001", "", "01K2OTHER000000000000001", selfPrincipal()))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.myTokenRevokes) != 0 {
		t.Error("見つからないのに UPDATE が走った")
	}
	if tx.committed {
		t.Error("404 なのにコミットされている")
	}
}
