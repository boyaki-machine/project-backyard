package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// tokenQuerier は認証経路の2クエリだけを持つ Querier。
type tokenQuerier struct {
	gen.Querier // 未使用のメソッドは nil のまま（呼べば panic して気づける）

	rows      map[string]gen.FindAccessTokenByHashRow
	findErr   error
	touchErr  error
	touchedID []string
}

func (q *tokenQuerier) FindAccessTokenByHash(_ context.Context, hash string) (gen.FindAccessTokenByHashRow, error) {
	if q.findErr != nil {
		return gen.FindAccessTokenByHashRow{}, q.findErr
	}
	row, ok := q.rows[hash]
	if !ok {
		return gen.FindAccessTokenByHashRow{}, pgx.ErrNoRows
	}
	return row, nil
}

func (q *tokenQuerier) TouchAccessTokenLastUsed(_ context.Context, id string) error {
	q.touchedID = append(q.touchedID, id)
	return q.touchErr
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func txt(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

// validRow は有効なセッショントークン1件。
func validRow() gen.FindAccessTokenByHashRow {
	return gen.FindAccessTokenByHashRow{
		TokenID:     "01K2F8QW3H7YRJ4M5N6P7Q8R9T",
		TokenType:   auth.TokenTypeSession,
		Scopes:      []byte(`[]`),
		ExpiresAt:   ts(time.Now().Add(24 * time.Hour)),
		ActorID:     "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
		ActorKind:   auth.ActorKindUser,
		DisplayName: "田中",
		IsActive:    true,
		SystemRole:  txt(auth.SystemRoleAdministrator),
		Email:       txt("tanaka@example.com"),
	}
}

// serve は Authenticate を通したリクエストを実行し、応答と到達したプリンシパルを返す。
func serve(q gen.Querier, r *http.Request) (*httptest.ResponseRecorder, *auth.Principal) {
	var got *auth.Principal
	h := Authenticate(q)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = auth.PrincipalFromContext(r.Context())
		w.WriteHeader(http.StatusNoContent)
	}))

	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w, got
}

// errorCode は 2.5 形式の応答からエラーコードを取り出す。
func errorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が 2.5 の形式でない: %v（body=%s）", err, w.Body.String())
	}
	if body.Error.Message == "" {
		t.Error("message が空。そのまま画面に出せる日本語であること")
	}
	return body.Error.Code
}

func TestAuthenticateWithCookie(t *testing.T) {
	const plaintext = "pb_sess_valid"
	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(plaintext): validRow(),
	}}

	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: plaintext})

	w, p := serve(q, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
	}
	if p == nil {
		t.Fatal("プリンシパルがコンテキストに載っていない")
	}
	if p.ActorID != "01K2F8QW3H7YRJ4M5N6P7Q8R9S" || p.DisplayName != "田中" {
		t.Errorf("actor = %+v", p)
	}
	if p.Email != "tanaka@example.com" || p.SystemRole != auth.SystemRoleAdministrator {
		t.Errorf("app_user 由来の項目が載っていない: %+v", p)
	}
	if p.Source != auth.SourceCookie {
		t.Errorf("Source = %q, want %q（CSRF の要否判定に使う）", p.Source, auth.SourceCookie)
	}
	if len(q.touchedID) != 1 || q.touchedID[0] != p.TokenID {
		t.Errorf("last_used_at が更新されていない: %v", q.touchedID)
	}
}

func TestAuthenticateWithBearer(t *testing.T) {
	const plaintext = "pb_api_valid"
	row := validRow()
	row.TokenType = auth.TokenTypeAPI
	row.Scopes = []byte(`["ticket:read","ticket:write"]`)
	row.ProjectID = txt("01K2F8QW3H7YRJ4M5N6P7Q8RAA")

	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(plaintext): row,
	}}

	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+plaintext)

	w, p := serve(q, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
	}
	if p.Source != auth.SourceBearer {
		t.Errorf("Source = %q, want %q", p.Source, auth.SourceBearer)
	}
	if len(p.Scopes) != 2 || p.Scopes[0] != "ticket:read" {
		t.Errorf("Scopes = %v", p.Scopes)
	}
	if p.ProjectID != "01K2F8QW3H7YRJ4M5N6P7Q8RAA" {
		t.Errorf("ProjectID = %q", p.ProjectID)
	}
}

func TestAuthenticatePrefersCookieOverBearer(t *testing.T) {
	// Cookie が付いているのに Bearer 側を採ると、CSRF の対象外になってしまう
	// （ApiDesign.md 2.4）。安全側に倒して Cookie を優先する。
	const cookieTok, bearerTok = "pb_sess_cookie", "pb_api_bearer"

	cookieRow := validRow()
	bearerRow := validRow()
	bearerRow.TokenType = auth.TokenTypeAPI

	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(cookieTok): cookieRow,
		auth.HashToken(bearerTok): bearerRow,
	}}

	r := httptest.NewRequest("POST", "/api/v1/projects", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: cookieTok})
	r.Header.Set("Authorization", "Bearer "+bearerTok)

	_, p := serve(q, r)
	if p.Source != auth.SourceCookie || p.TokenType != auth.TokenTypeSession {
		t.Errorf("Source/TokenType = %q/%q, want cookie/session", p.Source, p.TokenType)
	}
}

func TestAuthenticateRejects(t *testing.T) {
	expired := validRow()
	expired.ExpiresAt = ts(time.Now().Add(-time.Minute))

	revoked := validRow()
	revoked.RevokedAt = ts(time.Now().Add(-time.Hour))

	inactive := validRow()
	inactive.IsActive = false

	agentAPI := validRow()
	agentAPI.ActorKind = auth.ActorKindAgent
	agentAPI.TokenType = auth.TokenTypeAPI
	agentAPI.OwnerActorID = txt("01K2F8QW3H7YRJ4M5N6P7Q8RAA")

	userAgentToken := validRow()
	userAgentToken.TokenType = auth.TokenTypeAgent

	tests := []struct {
		name    string
		row     *gen.FindAccessTokenByHashRow // nil なら DB に無い
		useAuth bool                          // false なら資格情報を付けない
	}{
		{name: "資格情報が無い"},
		{name: "未知のトークン", useAuth: true},
		{name: "失効している", row: &revoked, useAuth: true},
		{name: "期限切れ", row: &expired, useAuth: true},
		{name: "アクターが無効", row: &inactive, useAuth: true},
		{name: "エージェント名義のAPIトークン", row: &agentAPI, useAuth: true},
		{name: "人間名義のエージェントトークン", row: &userAgentToken, useAuth: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const plaintext = "pb_sess_x"
			q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{}}
			if tt.row != nil {
				q.rows[auth.HashToken(plaintext)] = *tt.row
			}

			r := httptest.NewRequest("GET", "/api/v1/me", nil)
			if tt.useAuth {
				r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: plaintext})
			}

			w, p := serve(q, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401（body=%s）", w.Code, w.Body.String())
			}
			// 「無い」「失効」「無効」を応答で区別しない（総当たりの手がかりを与えない）。
			if code := errorCode(t, w); code != "unauthenticated" {
				t.Errorf("code = %q, want unauthenticated", code)
			}
			if p != nil {
				t.Error("拒否したのにハンドラへ到達した")
			}
			if len(q.touchedID) != 0 {
				t.Error("拒否したのに last_used_at を更新した")
			}
		})
	}
}

func TestAuthenticateAcceptsNullExpiresAt(t *testing.T) {
	// expires_at は NULL 許容。NULL は無期限として通す。
	const plaintext = "pb_api_noexpiry"
	row := validRow()
	row.ExpiresAt = pgtype.Timestamptz{}

	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(plaintext): row,
	}}

	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+plaintext)

	w, p := serve(q, r)
	if w.Code != http.StatusNoContent || p == nil {
		t.Fatalf("status = %d, principal = %v（NULL は無期限）", w.Code, p)
	}
}

func TestAuthenticateReturns500OnDatabaseError(t *testing.T) {
	// DB 障害を 401 にすると、利用者が再ログインを試み続けることになる。
	q := &tokenQuerier{findErr: errors.New("connection refused")}

	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "pb_sess_x"})

	w, _ := serve(q, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if code := errorCode(t, w); code != "internal_error" {
		t.Errorf("code = %q, want internal_error", code)
	}
	// 内部原因は応答に出さない。
	if body := w.Body.String(); strings.Contains(body, "connection refused") {
		t.Errorf("内部原因が応答に漏れている: %s", body)
	}
}

func TestAuthenticateRejectsMalformedScopes(t *testing.T) {
	// 壊れた scopes を空に倒すと「絞り込みなし」に化ける（Design.md 6.4.1）。
	const plaintext = "pb_agt_broken"
	row := validRow()
	row.Scopes = []byte(`{"not":"an array"}`)

	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(plaintext): row,
	}}

	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+plaintext)

	w, p := serve(q, r)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", w.Code)
	}
	if p != nil {
		t.Error("スコープを解釈できないのに通した")
	}
}

func TestAuthenticateSurvivesTouchFailure(t *testing.T) {
	// last_used_at が書けなかっただけでリクエストを落とさない。
	const plaintext = "pb_sess_valid"
	q := &tokenQuerier{
		rows:     map[string]gen.FindAccessTokenByHashRow{auth.HashToken(plaintext): validRow()},
		touchErr: errors.New("deadlock detected"),
	}

	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: plaintext})

	w, p := serve(q, r)
	if w.Code != http.StatusNoContent || p == nil {
		t.Fatalf("status = %d, principal = %v", w.Code, p)
	}
}

func TestAuthenticateIgnoresEmptyCookie(t *testing.T) {
	// Cookie を消す際に空値が残ることがある。Bearer へ落ちること。
	const plaintext = "pb_api_valid"
	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(plaintext): validRow(),
	}}

	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: ""})
	r.Header.Set("Authorization", "Bearer "+plaintext)

	w, p := serve(q, r)
	if w.Code != http.StatusNoContent || p == nil {
		t.Fatalf("status = %d, principal = %v", w.Code, p)
	}
	if p.Source != auth.SourceBearer {
		t.Errorf("Source = %q, want bearer", p.Source)
	}
}

// ── エージェントの委譲（Design.md 6.5、0019）────────────────

// agentRow は有効なエージェントトークン1件。
//
// **SystemRole には所有者のロールが入る。** FindAccessTokenByHash が
// app_user を COALESCE(agent.owner_actor_id, actor.id) で結合するためで、
// エージェント自身は app_user の行を持たない（DbDesign.md 8.2.1）。
func agentRow() gen.FindAccessTokenByHashRow {
	row := validRow()
	row.TokenID = "01AGENTTOKEN00000000000000"
	row.TokenType = auth.TokenTypeAgent
	row.ActorID = "01AGENT0000000000000000000"
	row.ActorKind = auth.ActorKindAgent
	row.DisplayName = "私の Claude Code"
	row.OwnerActorID = txt("01OWNER0000000000000000000")
	row.OwnerIsActive = pgtype.Bool{Bool: true, Valid: true}
	row.SystemRole = txt(auth.SystemRoleOperator) // 所有者のロール
	row.Email = txt("owner@example.com")          // 所有者のメール
	return row
}

func serveAgent(row gen.FindAccessTokenByHashRow) (*httptest.ResponseRecorder, *auth.Principal) {
	const plaintext = "pb_agt_valid"
	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(plaintext): row,
	}}
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.Header.Set("Authorization", "Bearer "+plaintext)
	return serve(q, r)
}

// TestAuthenticateAgentCarriesOwner は所有者がプリンシパルへ載ることを見る。
func TestAuthenticateAgentCarriesOwner(t *testing.T) {
	w, p := serveAgent(agentRow())
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", w.Code, w.Body.String())
	}
	if p.ActorID != "01AGENT0000000000000000000" {
		t.Errorf("ActorID = %q, want エージェント自身（監査の主体）", p.ActorID)
	}
	if p.OwnerActorID != "01OWNER0000000000000000000" {
		t.Errorf("OwnerActorID = %q", p.OwnerActorID)
	}
	// **認可はここを見る**（Design.md 6.5 の委譲）。
	if p.AuthzActorID() != "01OWNER0000000000000000000" {
		t.Errorf("AuthzActorID = %q, want 所有者", p.AuthzActorID())
	}
	// **所有者のシステムロールが載る。** 載らないと第1層が空になり、
	// スコープに何を書いても権限0件で全部 403 になる。
	if p.SystemRole != auth.SystemRoleOperator {
		t.Errorf("SystemRole = %q, want 所有者の operator", p.SystemRole)
	}
}

// TestAuthenticateRejectsAgentOfInactiveOwner は「所有者を無効化したら
// そのエージェントも止まる」を見る（Design.md 6.5）。
//
// **401 に倒す**（403 ではない）。ApiDesign.md 3.1 がアカウント無効を
// 認証失敗と区別しないと定めているため。
func TestAuthenticateRejectsAgentOfInactiveOwner(t *testing.T) {
	row := agentRow()
	row.OwnerIsActive = pgtype.Bool{Bool: false, Valid: true}

	w, p := serveAgent(row)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401（所有者が無効なら通さない）", w.Code)
	}
	if p != nil {
		t.Error("認証を通してしまっている")
	}
}

// TestAuthenticateHumanUnaffectedByOwnerColumn は人間の経路が変わっていないこと。
//
// **OwnerIsActive は人間では NULL（Valid=false）である。** ここを
// 「false なら弾く」と書くと、全ユーザーがログインできなくなる。
func TestAuthenticateHumanUnaffectedByOwnerColumn(t *testing.T) {
	row := validRow() // OwnerActorID も OwnerIsActive も未設定＝NULL
	if row.OwnerIsActive.Valid {
		t.Fatal("前提が違う: 人間の行に owner_is_active が入っている")
	}
	const plaintext = "pb_sess_valid"
	q := &tokenQuerier{rows: map[string]gen.FindAccessTokenByHashRow{
		auth.HashToken(plaintext): row,
	}}
	r := httptest.NewRequest("GET", "/api/v1/me", nil)
	r.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: plaintext})

	w, p := serve(q, r)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（人間の経路を壊していないこと）", w.Code)
	}
	if p.OwnerActorID != "" {
		t.Errorf("OwnerActorID = %q, want 空", p.OwnerActorID)
	}
	if p.AuthzActorID() != p.ActorID {
		t.Errorf("AuthzActorID = %q, want ActorID と同じ", p.AuthzActorID())
	}
}
