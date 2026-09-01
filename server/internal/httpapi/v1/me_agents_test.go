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
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 自分のエージェント（ApiDesign.md 4.5、手順24a）のハンドラ単体テスト。
//
// 認証はミドルウェアの責務なので通さない。ここで確かめるのは**応答の形・
// 検証の分岐・監査に何を書くか**である。実DBでしか確かめられないもの
// （他人のエージェント・冪等な失効・委譲の実効権限）は
// me_agents_integration_test.go にある。

// ── 応答を読むための型 ──────────────────────────────────────
//
// **応答の型（myAgentView）をそのまま使わない。** v1.Time は MarshalJSON
// だけを持ち、読み戻す口が無い（tokenJSON と同じ事情）。

type agentTokenJSON struct {
	ID          string  `json:"id"`
	TokenPrefix string  `json:"token_prefix"`
	IssuedAt    string  `json:"issued_at"`
	LastUsedAt  *string `json:"last_used_at"`
	ExpiresAt   *string `json:"expires_at"`
	Status      string  `json:"status"`
}

type agentJSON struct {
	ID           string          `json:"id"`
	DisplayName  string          `json:"display_name"`
	ClientKind   string          `json:"client_kind"`
	ModelName    *string         `json:"model_name"`
	ModelVersion *string         `json:"model_version"`
	Project      *projectRefJSON `json:"project"`
	TrustLevel   int32           `json:"trust_level"`
	IsActive     bool            `json:"is_active"`
	CreatedAt    string          `json:"created_at"`
	Token        *agentTokenJSON `json:"token"`
}

type projectRefJSON struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type agentListJSON struct {
	Items []agentJSON `json:"items"`
}

type issuedAgentTokenJSON struct {
	ID          string   `json:"id"`
	Token       string   `json:"token"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	IssuedAt    string   `json:"issued_at"`
	ExpiresAt   *string  `json:"expires_at"`
	Status      string   `json:"status"`
}

// agentFake は 4.5 のテストが使う既定のフェイク。
//
// **「通る」状態を既定にして、テストごとに1つだけ崩す**（tokenFake と同じ型）。
func agentFake(t *testing.T) *fakeQuerier {
	t.Helper()
	q := newFake(t)
	q.agentProject = gen.FindMyProjectByKeyRow{
		ID: "01PROJECT00000000000000000", Key: "demo", Name: "デモプロジェクト",
	}
	q.agentRow = gen.FindMyAgentRow{
		ActorID:     "01AGENT0000000000000000000",
		DisplayName: "私の Claude Code",
		IsActive:    true,
		ClientKind:  "claude_code",
		CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		ProjectID:   pgtype.Text{String: "01PROJECT00000000000000000", Valid: true},
		ProjectKey:  pgtype.Text{String: "demo", Valid: true},
		ProjectName: pgtype.Text{String: "デモプロジェクト", Valid: true},
	}
	q.agentTokenRevokedRow = 1
	return q
}

// agentHandler はハンドラを組み立てる。トランザクションを使う経路が多いので
// newUserHandler の戻り値の2つ目は捨てる。
func agentHandler(q *fakeQuerier) *handler {
	h, _ := newUserHandler(q)
	return h
}

// hasDetailField は details に該当の field があるかを見る。
//
// **projects_update_test.go の hasDetail とは別物である。** あちらは
// field と code の組で見る。ここでは code まで固定すると、検証の文言を
// 変えただけでテストが落ちる——測りたいのは「どの項目が弾かれたか」である。
func hasDetailField(e apiError, field string) bool {
	for _, d := range e.Details {
		if d.Field == field {
			return true
		}
	}
	return false
}

// agentReq は /me/agents 系のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを
// 通らず、chi.URLParam が空を返してしまうため（tokenReq と同じ）。
func agentReq(method, target, body, id, tokenID string, p *auth.Principal) *http.Request {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(body))
	}
	ctx := auth.NewPrincipalContext(req.Context(), p)
	if id != "" || tokenID != "" {
		rc := chi.NewRouteContext()
		if id != "" {
			rc.URLParams.Add("id", id)
		}
		if tokenID != "" {
			rc.URLParams.Add("token_id", tokenID)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rc)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return req.WithContext(ctx)
}

// ── GET /me/agents（4.5.1）──────────────────────────────────

func TestListMyAgentsShapesResponse(t *testing.T) {
	q := agentFake(t)
	issued := time.Date(2026, 8, 30, 9, 3, 12, 0, time.UTC)
	q.agentRows = []gen.ListMyAgentsRow{{
		ActorID:       "01AGENT0000000000000000000",
		DisplayName:   "私の Claude Code",
		IsActive:      true,
		ClientKind:    "claude_code",
		ModelName:     pgtype.Text{String: "claude-opus-5", Valid: true},
		TrustLevel:    1,
		CreatedAt:     pgtype.Timestamptz{Time: issued, Valid: true},
		ProjectKey:    pgtype.Text{String: "demo", Valid: true},
		ProjectName:   pgtype.Text{String: "デモプロジェクト", Valid: true},
		TokenID:       "01TOKEN0000000000000000000",
		TokenPrefix:   pgtype.Text{String: "pb_agt_7", Valid: true},
		TokenIssuedAt: pgtype.Timestamptz{Time: issued, Valid: true},
		// **有効期限を未来に置く**——過去にすると status が expired になり、
		// この検査が「書式」ではなく「期限判定」を測ることになる。
		TokenExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(24 * time.Hour), Valid: true},
	}}

	rec := httptest.NewRecorder()
	agentHandler(q).listMyAgents(rec, agentReq(http.MethodGet, "/me/agents", "", "", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（本文: %s）", rec.Code, rec.Body.String())
	}
	var got agentListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を解釈できない: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d件, want 1", len(got.Items))
	}
	it := got.Items[0]
	if it.ID != "01AGENT0000000000000000000" || it.ClientKind != "claude_code" {
		t.Errorf("id/client_kind = %q/%q", it.ID, it.ClientKind)
	}
	if it.Project == nil || it.Project.Key != "demo" {
		t.Errorf("project = %+v, want key=demo", it.Project)
	}
	if it.Token == nil {
		t.Fatal("token が null。有効なトークンがある行では出るはず")
	}
	if it.Token.TokenPrefix != "pb_agt_7" {
		t.Errorf("token_prefix = %q, want pb_agt_7", it.Token.TokenPrefix)
	}
	if it.Token.Status != "active" {
		t.Errorf("status = %q, want active", it.Token.Status)
	}
	// **平文が混ざっていないこと**（4.5.1 は token を返さない）。
	if strings.Contains(rec.Body.String(), `"token":"pb_agt_`) {
		t.Error("一覧に平文トークンが混ざっている")
	}
}

// TestListMyAgentsWithoutTokenReturnsNull は「有効なトークンが無い」経路。
//
// **これが agent.sql の token_id の設計そのものを測っている。** LEFT JOIN
// LATERAL が空になると token_id は空文字で返り、応答では null になる。
func TestListMyAgentsWithoutTokenReturnsNull(t *testing.T) {
	q := agentFake(t)
	q.agentRows = []gen.ListMyAgentsRow{{
		ActorID:     "01AGENT0000000000000000000",
		DisplayName: "トークン未発行",
		IsActive:    true,
		ClientKind:  "copilot",
		CreatedAt:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
		TokenID:     "", // 有効なトークンが無い
	}}

	rec := httptest.NewRecorder()
	agentHandler(q).listMyAgents(rec, agentReq(http.MethodGet, "/me/agents", "", "", "", selfPrincipal()))

	var got agentListJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を解釈できない: %v", err)
	}
	if len(got.Items) != 1 {
		t.Fatalf("items = %d件, want 1", len(got.Items))
	}
	if got.Items[0].Token != nil {
		t.Errorf("token = %+v, want null", got.Items[0].Token)
	}
	// project が無い行でも落ちないこと（LEFT JOIN の反対側）。
	if got.Items[0].Project != nil {
		t.Errorf("project = %+v, want null", got.Items[0].Project)
	}
}

// ── POST /me/agents（4.5.2）─────────────────────────────────

func TestCreateMyAgentCreatesActorAndAgent(t *testing.T) {
	q := agentFake(t)
	body := `{"display_name":"私の Claude Code","project_key":"demo",` +
		`"client_kind":"claude_code","model_name":"claude-opus-5"}`

	rec := httptest.NewRecorder()
	agentHandler(q).createMyAgent(rec, agentReq(http.MethodPost, "/me/agents", body, "", "", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（本文: %s）", rec.Code, rec.Body.String())
	}
	if len(q.createdAgentActors) != 1 || len(q.createdAgents) != 1 {
		t.Fatalf("actor %d件 / agent %d件, want 1件ずつ",
			len(q.createdAgentActors), len(q.createdAgents))
	}
	ag := q.createdAgents[0]
	// **所有者は操作した本人である**（委譲の起点。Design.md 6.5）。
	if ag.OwnerActorID != selfPrincipal().ActorID {
		t.Errorf("owner_actor_id = %q, want %q", ag.OwnerActorID, selfPrincipal().ActorID)
	}
	if ag.ProjectID.String != "01PROJECT00000000000000000" {
		t.Errorf("project_id = %q", ag.ProjectID.String)
	}
	if ag.ActorID != q.createdAgentActors[0].ID {
		t.Error("actor と agent の ID が食い違っている")
	}

	var got agentJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を解釈できない: %v", err)
	}
	// **登録と発行を分ける**（4.5.2）。token は常に null。
	if got.Token != nil {
		t.Errorf("token = %+v, want null（登録では発行しない）", got.Token)
	}
	if got.TrustLevel != 1 {
		t.Errorf("trust_level = %d, want 1（既定値）", got.TrustLevel)
	}

	// 監査（4.5.6）。
	if len(q.audits) != 1 {
		t.Fatalf("audit = %d件, want 1", len(q.audits))
	}
	if q.audits[0].Action != "agent.register" {
		t.Errorf("action = %q, want agent.register", q.audits[0].Action)
	}
	if q.audits[0].TargetType.String != "agent" {
		t.Errorf("target_type = %q, want agent", q.audits[0].TargetType.String)
	}
}

// TestCreateMyAgentRejectsForeignProject は「自分がメンバーでないプロジェクト」。
//
// **422 に倒す**（4.5.2）。project_key は本体のフィールドであり、
// Design.md 6.4.5 の「存在を隠す」はパスで指した資源についての規約である。
func TestCreateMyAgentRejectsForeignProject(t *testing.T) {
	q := agentFake(t)
	q.agentProjectErr = pgx.ErrNoRows
	body := `{"display_name":"よそのPJ","project_key":"secret","client_kind":"claude_code"}`

	rec := httptest.NewRecorder()
	agentHandler(q).createMyAgent(rec, agentReq(http.MethodPost, "/me/agents", body, "", "", selfPrincipal()))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（本文: %s）", rec.Code, rec.Body.String())
	}
	if len(q.createdAgents) != 0 {
		t.Error("検証に落ちたのにエージェントを作っている")
	}
	if !hasDetailField(errorOf(t, rec), "project_key") {
		t.Errorf("details に project_key が無い: %s", rec.Body.String())
	}
}

func TestCreateMyAgentRejectsInvalidInput(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"名前が空", `{"display_name":"  ","project_key":"demo","client_kind":"claude_code"}`, "display_name"},
		{"名前が61文字", `{"display_name":"` + strings.Repeat("あ", 61) +
			`","project_key":"demo","client_kind":"claude_code"}`, "display_name"},
		{"プロジェクト未指定", `{"display_name":"a","project_key":"","client_kind":"claude_code"}`, "project_key"},
		{"種別が値域外", `{"display_name":"a","project_key":"demo","client_kind":"cursor"}`, "client_kind"},
		{"種別が空", `{"display_name":"a","project_key":"demo","client_kind":""}`, "client_kind"},
		{"モデル名が101文字", `{"display_name":"a","project_key":"demo","client_kind":"other",` +
			`"model_name":"` + strings.Repeat("x", 101) + `"}`, "model_name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := agentFake(t)
			rec := httptest.NewRecorder()
			agentHandler(q).createMyAgent(rec,
				agentReq(http.MethodPost, "/me/agents", c.body, "", "", selfPrincipal()))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（本文: %s）", rec.Code, rec.Body.String())
			}
			if !hasDetailField(errorOf(t, rec), c.field) {
				t.Errorf("details に %s が無い: %s", c.field, rec.Body.String())
			}
			if len(q.createdAgents) != 0 {
				t.Error("検証に落ちたのにエージェントを作っている")
			}
		})
	}
}

func TestCreateMyAgentRejectsDuplicate(t *testing.T) {
	q := agentFake(t)
	q.agentExists = true
	body := `{"display_name":"私の Claude Code","project_key":"demo","client_kind":"claude_code"}`

	rec := httptest.NewRecorder()
	agentHandler(q).createMyAgent(rec, agentReq(http.MethodPost, "/me/agents", body, "", "", selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（本文: %s）", rec.Code, rec.Body.String())
	}
	if len(q.createdAgents) != 0 {
		t.Error("重複なのにエージェントを作っている")
	}
}

// ── POST /me/agents/:id/tokens（4.5.3）──────────────────────

func TestCreateMyAgentTokenReturnsPlaintextOnce(t *testing.T) {
	q := agentFake(t)
	rec := httptest.NewRecorder()
	agentHandler(q).createMyAgentToken(rec, agentReq(http.MethodPost,
		"/me/agents/01AGENT0000000000000000000/tokens", `{"expires_in_days":90}`,
		"01AGENT0000000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（本文: %s）", rec.Code, rec.Body.String())
	}
	var got issuedAgentTokenJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答を解釈できない: %v", err)
	}
	if !strings.HasPrefix(got.Token, auth.AgentTokenPrefix) {
		t.Errorf("token = %q, want %s… で始まる", got.Token, auth.AgentTokenPrefix)
	}
	if got.TokenPrefix != got.Token[:auth.TokenPrefixLen] {
		t.Errorf("token_prefix = %q, token の先頭8文字と一致しない", got.TokenPrefix)
	}

	// **既定スコープをそのまま載せる**（4.5.3。要求では選べない）。
	if len(got.Scopes) != len(agentDefaultScopes) {
		t.Fatalf("scopes = %d件, want %d件", len(got.Scopes), len(agentDefaultScopes))
	}
	for i, s := range agentDefaultScopes {
		if got.Scopes[i] != s {
			t.Errorf("scopes[%d] = %q, want %q", i, got.Scopes[i], s)
		}
	}
	// **禁止されているものが入っていないこと**（Design.md 6.5）。
	for _, banned := range []string{"ticket.close", "doc.edit"} {
		for _, s := range got.Scopes {
			if s == banned {
				t.Errorf("既定スコープに %q が入っている（6.5 の禁止）", banned)
			}
		}
	}

	if len(q.created) != 1 {
		t.Fatalf("access_token = %d件, want 1", len(q.created))
	}
	tk := q.created[0]
	if tk.TokenType != auth.TokenTypeAgent {
		t.Errorf("token_type = %q, want agent", tk.TokenType)
	}
	// **プロジェクトスコープ必須**（Design.md 6.5）。
	if tk.ProjectID.String != "01PROJECT00000000000000000" {
		t.Errorf("project_id = %q, want エージェントのプロジェクト", tk.ProjectID.String)
	}
	// **client_info にクライアント種別**（ApiDesign.md 4.5.3）。
	if tk.ClientInfo.String != "claude_code" {
		t.Errorf("client_info = %q, want claude_code", tk.ClientInfo.String)
	}
	// **actor_id はエージェント**（所有者ではない）。
	if tk.ActorID != "01AGENT0000000000000000000" {
		t.Errorf("actor_id = %q, want エージェントの ID", tk.ActorID)
	}
	// **DB に平文を入れない。**
	if strings.Contains(tk.TokenHash, got.Token) {
		t.Error("token_hash に平文が入っている")
	}

	// 監査に平文を入れない（4.5.6）。
	if strings.Contains(string(q.audits[len(q.audits)-1].Detail), got.Token) {
		t.Error("監査の detail に平文が入っている")
	}
}

// TestCreateMyAgentTokenRevokesPrevious は「有効なトークンは1本」（4.5.3）。
func TestCreateMyAgentTokenRevokesPrevious(t *testing.T) {
	q := agentFake(t)
	q.activeAgentTokens = []gen.ListActiveAgentTokensRow{
		{ID: "01OLDTOKEN0000000000000000", TokenPrefix: pgtype.Text{String: "pb_agt_1", Valid: true}},
	}

	rec := httptest.NewRecorder()
	agentHandler(q).createMyAgentToken(rec, agentReq(http.MethodPost,
		"/me/agents/01AGENT0000000000000000000/tokens", `{"expires_in_days":30}`,
		"01AGENT0000000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201（本文: %s）", rec.Code, rec.Body.String())
	}
	if len(q.agentTokenRevokes) != 1 {
		t.Fatalf("失効 = %d件, want 1（再発行は前を失効させる）", len(q.agentTokenRevokes))
	}
	if q.agentTokenRevokes[0].ID != "01OLDTOKEN0000000000000000" {
		t.Errorf("失効させた ID = %q", q.agentTokenRevokes[0].ID)
	}
	// **失効も発行も同じトランザクションで行われること。**
	// opLog に両方が並び、失効が先であることを見る。
	revokeAt, createAt := -1, -1
	for i, op := range q.opLog {
		if op == "RevokeAgentToken" && revokeAt < 0 {
			revokeAt = i
		}
		if op == "CreateAccessToken" && createAt < 0 {
			createAt = i
		}
	}
	if revokeAt < 0 || createAt < 0 || revokeAt > createAt {
		t.Errorf("opLog = %v, want 失効→発行の順", q.opLog)
	}
	// **暗黙の失効も監査に残す**（4.5.6）。token.revoke と token.issue の両方。
	var revoked, issued int
	for _, a := range q.audits {
		switch a.Action {
		case "token.revoke":
			revoked++
		case "token.issue":
			issued++
		}
	}
	if revoked != 1 || issued != 1 {
		t.Errorf("監査 revoke=%d issue=%d, want 1/1", revoked, issued)
	}
}

func TestCreateMyAgentTokenRejectsInactiveAgent(t *testing.T) {
	q := agentFake(t)
	q.agentRow.IsActive = false

	rec := httptest.NewRecorder()
	agentHandler(q).createMyAgentToken(rec, agentReq(http.MethodPost,
		"/me/agents/01AGENT0000000000000000000/tokens", `{"expires_in_days":30}`,
		"01AGENT0000000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409（本文: %s）", rec.Code, rec.Body.String())
	}
	if len(q.created) != 0 {
		t.Error("無効なエージェントにトークンを発行している")
	}
}

func TestCreateMyAgentTokenRejectsBadExpiry(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"未指定", `{}`},
		{"0日", `{"expires_in_days":0}`},
		{"366日", `{"expires_in_days":366}`},
		{"負", `{"expires_in_days":-1}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := agentFake(t)
			rec := httptest.NewRecorder()
			agentHandler(q).createMyAgentToken(rec, agentReq(http.MethodPost,
				"/me/agents/01AGENT0000000000000000000/tokens", c.body,
				"01AGENT0000000000000000000", "", selfPrincipal()))

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422（本文: %s）", rec.Code, rec.Body.String())
			}
			if len(q.created) != 0 {
				t.Error("検証に落ちたのに発行している")
			}
		})
	}
}

func TestCreateMyAgentTokenNotFoundForForeignAgent(t *testing.T) {
	q := agentFake(t)
	q.agentFindErr = pgx.ErrNoRows

	rec := httptest.NewRecorder()
	agentHandler(q).createMyAgentToken(rec, agentReq(http.MethodPost,
		"/me/agents/01OTHER00000000000000000/tokens", `{"expires_in_days":30}`,
		"01OTHER00000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（403 にしない。Design.md 6.4.5）", rec.Code)
	}
}

// ── PATCH /me/agents/:id（4.5.4）────────────────────────────

func TestUpdateMyAgentDeactivationRevokesTokens(t *testing.T) {
	q := agentFake(t)
	rec := httptest.NewRecorder()
	agentHandler(q).updateMyAgent(rec, agentReq(http.MethodPatch,
		"/me/agents/01AGENT0000000000000000000", `{"is_active":false}`,
		"01AGENT0000000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（本文: %s）", rec.Code, rec.Body.String())
	}
	// **無効化するとトークンも失効する**（4.5.4）。
	if len(q.agentAllTokenRevokes) != 1 {
		t.Fatalf("全失効 = %d件, want 1（無効化したら動き続けない）", len(q.agentAllTokenRevokes))
	}
	if q.agentAllTokenRevokes[0] != "01AGENT0000000000000000000" {
		t.Errorf("失効の対象 = %q", q.agentAllTokenRevokes[0])
	}
	if len(q.audits) != 1 || q.audits[0].Action != "agent.update" {
		t.Errorf("監査 = %+v, want agent.update 1件", q.audits)
	}
}

// TestUpdateMyAgentActivationKeepsTokens は「有効化ではトークンを触らない」。
//
// **is_active を見るだけの実装だと、有効化でも全失効が走る。**
func TestUpdateMyAgentActivationKeepsTokens(t *testing.T) {
	q := agentFake(t)
	rec := httptest.NewRecorder()
	agentHandler(q).updateMyAgent(rec, agentReq(http.MethodPatch,
		"/me/agents/01AGENT0000000000000000000", `{"is_active":true}`,
		"01AGENT0000000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（本文: %s）", rec.Code, rec.Body.String())
	}
	if len(q.agentAllTokenRevokes) != 0 {
		t.Errorf("有効化でトークンを失効させている（%d件）", len(q.agentAllTokenRevokes))
	}
}

func TestUpdateMyAgentPartialUpdate(t *testing.T) {
	q := agentFake(t)
	rec := httptest.NewRecorder()
	agentHandler(q).updateMyAgent(rec, agentReq(http.MethodPatch,
		"/me/agents/01AGENT0000000000000000000", `{"model_name":"claude-opus-5"}`,
		"01AGENT0000000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200（本文: %s）", rec.Code, rec.Body.String())
	}
	// 表示名も is_active も送っていないので actor は更新しない。
	if len(q.updatedAgentActors) != 0 {
		t.Errorf("actor を更新している（送っていない項目）: %+v", q.updatedAgentActors)
	}
	if len(q.updatedAgentModels) != 1 {
		t.Fatalf("agent の更新 = %d件, want 1", len(q.updatedAgentModels))
	}
	m := q.updatedAgentModels[0]
	if !m.ModelName.Valid || m.ModelName.String != "claude-opus-5" {
		t.Errorf("model_name = %+v", m.ModelName)
	}
	// **送られなかった項目は NULL（＝据え置き）**（nargText）。
	if m.ModelVersion.Valid {
		t.Errorf("model_version = %+v, want 未設定", m.ModelVersion)
	}
}

func TestUpdateMyAgentNotFoundForForeignAgent(t *testing.T) {
	q := agentFake(t)
	q.agentFindErr = pgx.ErrNoRows

	rec := httptest.NewRecorder()
	agentHandler(q).updateMyAgent(rec, agentReq(http.MethodPatch,
		"/me/agents/01OTHER00000000000000000", `{"display_name":"x"}`,
		"01OTHER00000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(q.updatedAgentActors) != 0 {
		t.Error("他人のエージェントを更新している")
	}
}

func TestUpdateMyAgentRejectsEmptyName(t *testing.T) {
	q := agentFake(t)
	rec := httptest.NewRecorder()
	agentHandler(q).updateMyAgent(rec, agentReq(http.MethodPatch,
		"/me/agents/01AGENT0000000000000000000", `{"display_name":"   "}`,
		"01AGENT0000000000000000000", "", selfPrincipal()))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422（本文: %s）", rec.Code, rec.Body.String())
	}
}

// ── DELETE /me/agents/:id/tokens/:token_id（4.5.5）──────────

func TestDeleteMyAgentTokenRevokes(t *testing.T) {
	q := agentFake(t)
	rec := httptest.NewRecorder()
	agentHandler(q).deleteMyAgentToken(rec, agentReq(http.MethodDelete,
		"/me/agents/01AGENT0000000000000000000/tokens/01TOKEN0000000000000000000", "",
		"01AGENT0000000000000000000", "01TOKEN0000000000000000000", selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（本文: %s）", rec.Code, rec.Body.String())
	}
	if len(q.agentTokenRevokes) != 1 {
		t.Fatalf("失効 = %d件, want 1", len(q.agentTokenRevokes))
	}
	if len(q.audits) != 1 || q.audits[0].Action != "token.revoke" {
		t.Errorf("監査 = %+v, want token.revoke 1件", q.audits)
	}
}

// TestDeleteMyAgentTokenIdempotent は既に失効済みの場合（4.5.5）。
//
// **204 を返すが監査は書かない。** RevokeAgentToken が 0 行を返すことで
// 「既に失効済み」と分かる。
func TestDeleteMyAgentTokenIdempotent(t *testing.T) {
	q := agentFake(t)
	q.agentTokenRevokedRow = 0

	rec := httptest.NewRecorder()
	agentHandler(q).deleteMyAgentToken(rec, agentReq(http.MethodDelete,
		"/me/agents/01AGENT0000000000000000000/tokens/01TOKEN0000000000000000000", "",
		"01AGENT0000000000000000000", "01TOKEN0000000000000000000", selfPrincipal()))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（冪等）", rec.Code)
	}
	if len(q.audits) != 0 {
		t.Errorf("既に失効済みなのに監査を書いている: %+v", q.audits)
	}
}

func TestDeleteMyAgentTokenNotFoundForForeignAgent(t *testing.T) {
	q := agentFake(t)
	q.agentFindErr = pgx.ErrNoRows

	rec := httptest.NewRecorder()
	agentHandler(q).deleteMyAgentToken(rec, agentReq(http.MethodDelete,
		"/me/agents/01OTHER00000000000000000/tokens/01TOKEN0000000000000000000", "",
		"01OTHER00000000000000000", "01TOKEN0000000000000000000", selfPrincipal()))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if len(q.agentTokenRevokes) != 0 {
		t.Error("他人のエージェントのトークンを失効させている")
	}
}

// ── 既定スコープそのもの（Design.md 6.5）─────────────────────

// TestAgentDefaultScopesAreCatalogKeys は、既定スコープが**権限カタログのキーで
// あること**を測る。
//
// **これが 6.5 の書き直しの要点である。** 旧語彙（ticket:read 等）のままだと
// auth.EffectivePermissions の積が空になり、発行したトークンが何もできない。
// 「キーらしい形か」ではなく**実際に積を取って残るか**で見る。
func TestAgentDefaultScopesSurviveIntersection(t *testing.T) {
	// 所有者がカタログの全権を持つ場合、既定スコープはすべて残るはず。
	roleAll := append([]string(nil), agentDefaultScopes...)
	got := auth.EffectivePermissions(roleAll, nil, agentDefaultScopes)
	if len(got) != len(agentDefaultScopes) {
		t.Fatalf("実効権限 = %d件, want %d件（語彙が権限キーでないと0件になる）",
			len(got), len(agentDefaultScopes))
	}

	// 旧語彙は1件も残らない——この検査が、戻したときに落ちる。
	old := []string{"ticket:read", "context:read", "ticket:claim",
		"note:write", "result:submit", "proposal:create"}
	if n := len(auth.EffectivePermissions(roleAll, nil, old)); n != 0 {
		t.Errorf("旧語彙の実効権限 = %d件, want 0件", n)
	}

	// 所有者が持たない権限はスコープに書いても付かない（縮小のみ）。
	narrow := auth.EffectivePermissions([]string{"ticket.view"}, nil, agentDefaultScopes)
	if len(narrow) != 1 || narrow[0] != "ticket.view" {
		t.Errorf("実効権限 = %v, want [ticket.view]（所有者の権限を超えない）", narrow)
	}
}

// TestAgentDefaultScopesExcludeBanned は 6.5 の禁止を定数側で押さえる。
func TestAgentDefaultScopesExcludeBanned(t *testing.T) {
	for _, banned := range []string{"ticket.close", "doc.edit"} {
		for _, s := range agentDefaultScopes {
			if s == banned {
				t.Errorf("既定スコープに %q が入っている（Design.md 6.5 の禁止）", banned)
			}
		}
	}
}
