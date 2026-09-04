package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/project"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// MCP の口を**実際のDBに対して**通す（Design.md 8.3 / 8.4、手順25）。
//
// mcp パッケージの単体テストはフェイクの REST を相手にしているため、
// **委譲（6.5）・トークンのプロジェクトスコープ・スコープによる絞り込みは
// 一度も実行されない。** それらは access_token と role_permission の実データを
// 通って初めて確かめられる。
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	make test-db
func TestMCPIntegration(t *testing.T) {
	dsn := os.Getenv("PB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	rec := audit.FromCLI("mcp integration test")

	ownerID := ulidgen.New()
	agentID := ulidgen.New()
	projectID := ulidgen.New()
	otherProjectID := ulidgen.New()
	suffix := strings.ToLower(projectID[len(projectID)-6:])
	projectKey := "mcp-" + suffix
	otherKey := "mcp-other-" + suffix

	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = ANY($1)`,
			[]string{ownerID, agentID}); err != nil {
			t.Errorf("audit_log の後始末に失敗した: %v", err)
		}
		// project を消せば ticket / document / project_member が CASCADE で落ちる。
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE id = ANY($1)`,
			[]string{projectID, otherProjectID}); err != nil {
			t.Errorf("project の後始末に失敗した: %v", err)
		}
		// actor を消せば access_token と agent も CASCADE で落ちる。
		// **エージェントを先に消す**——owner_actor_id が app_user を参照している。
		if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, agentID); err != nil {
			t.Errorf("エージェントの後始末に失敗した: %v", err)
		}
		if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, ownerID); err != nil {
			t.Errorf("所有者の後始末に失敗した: %v", err)
		}
	})

	// ── 所有者（オペレータ）と、その人が参加するプロジェクト ──────────
	if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
		ID: ownerID, DisplayName: "MCPテスト（所有者）",
	}); err != nil {
		t.Fatalf("所有者の actor を作れない: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO app_user (actor_id, email, system_role) VALUES ($1, $2, $3)`,
		ownerID, "mcp-"+ownerID+"@example.com", auth.SystemRoleOperator); err != nil {
		t.Fatalf("所有者の app_user を作れない: %v", err)
	}

	// **project.Create を通す**（ApiDesign.md 5.3、手順23）。ワークフローと
	// 文書テンプレート4件が入るので、pb_list_docs / pb_get_doc を実データで
	// 確かめられる。
	for _, p := range []struct{ id, key, name string }{
		{projectID, projectKey, "MCPテスト"},
		{otherProjectID, otherKey, "MCPテスト（別プロジェクト）"},
	} {
		if err := project.Create(ctx, q, rec, project.CreateParams{
			ID:   p.id,
			Key:  p.key,
			Name: p.name,
			// 7.4 のテンプレート3種のうち最も単純なもの。チケットの状態は
			// この検証の関心ではない。
			WorkflowTemplate: "simple",
			CreatedBy:        pgtype.Text{String: ownerID, Valid: true},
		}); err != nil {
			t.Fatalf("プロジェクト %s を作れない: %v", p.key, err)
		}
	}
	// **project_member として参加させる**（project_admin にしない）。
	// 委譲が効いていることを、管理者ではないロールで確かめるため。
	if _, err := pool.Exec(ctx,
		`INSERT INTO project_member (project_id, actor_id, role_key) VALUES ($1, $2, 'project_member')`,
		projectID, ownerID); err != nil {
		t.Fatalf("project_member を作れない: %v", err)
	}

	// チケットを1件。API ではなく直接 INSERT でよい——ここで見るのは
	// MCP がその行を運べるかであって、チケットの作成規則ではない。
	ticketID := ulidgen.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ticket (id, project_id, seq, type, title, status_key, assignee_id)
		VALUES ($1, $2, 1, 'task', 'MCP から読むチケット', 'todo', $3)`,
		ticketID, projectID, ownerID); err != nil {
		t.Fatalf("チケットを作れない: %v", err)
	}

	// **見出しを持つ文書を1件足す。** 0018 のテンプレート4件は本文に `##` を
	// 持たないので、テンプレートだけでは outline も ?section= も一度も通らない。
	// **期待値をテンプレートの本文から取れないと分かった時点で、材料のほうを
	// 作る**（見出しを手で書いた期待値は、テンプレートが変わると腐る）。
	docID := ulidgen.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO document (id, project_id, slug, title, body_md, sort_order)
		VALUES ($1, $2, 'handbook', '検証用の手引き', $3, 50)`,
		docID, projectID,
		"前書き。\n\n## 命名\n\n本文A。\n\n## ブランチ\n\n本文B。\n"); err != nil {
		t.Fatalf("見出しを持つ文書を作れない: %v", err)
	}

	// ── エージェントと、そのトークン4本 ───────────────────────
	if err := q.CreateAgentActor(ctx, gen.CreateAgentActorParams{
		ID: agentID, DisplayName: "MCPテスト（エージェント）",
	}); err != nil {
		t.Fatalf("エージェントの actor を作れない: %v", err)
	}
	if err := q.CreateAgent(ctx, gen.CreateAgentParams{
		ActorID:      agentID,
		OwnerActorID: ownerID,
		ProjectID:    pgtype.Text{String: projectID, Valid: true},
		ClientKind:   "claude_code",
	}); err != nil {
		t.Fatalf("agent を作れない: %v", err)
	}

	issue := func(name, plaintext, project string, scopes []string) {
		t.Helper()
		raw, err := json.Marshal(scopes)
		if err != nil {
			t.Fatalf("scopes を組み立てられない: %v", err)
		}
		if err := q.CreateAccessToken(ctx, gen.CreateAccessTokenParams{
			ID:        ulidgen.New(),
			ActorID:   agentID,
			TokenType: auth.TokenTypeAgent,
			TokenHash: auth.HashToken(plaintext),
			Name:      pgtype.Text{String: name, Valid: true},
			ProjectID: pgtype.Text{String: project, Valid: project != ""},
			Scopes:    raw,
		}); err != nil {
			t.Fatalf("トークン %s を発行できない: %v", name, err)
		}
	}

	// **空のスコープは「絞り込みなし」である**（Design.md 6.4.1）。
	fullToken := auth.AgentTokenPrefix + "full-" + suffix
	issue("full", fullToken, projectID, []string{})

	// doc.view を含めないトークン。**これが権限による出し分けの負の側になる**
	// （Design.md 付録A 論点③）——所有者は doc.view を持つが、積で消える。
	narrowToken := auth.AgentTokenPrefix + "narrow-" + suffix
	issue("narrow", narrowToken, projectID, []string{"agent.run", "project.view", "ticket.view"})

	// 別プロジェクトに紐づくトークン（8.3 の 404 を作る）。
	otherToken := auth.AgentTokenPrefix + "other-" + suffix
	issue("other", otherToken, otherProjectID, []string{})

	revokedToken := auth.AgentTokenPrefix + "revoked-" + suffix
	issue("revoked", revokedToken, projectID, []string{})
	if _, err := pool.Exec(ctx,
		`UPDATE access_token SET revoked_at = now() WHERE token_hash = $1`,
		auth.HashToken(revokedToken)); err != nil {
		t.Fatalf("トークンを失効させられない: %v", err)
	}

	r := NewRouter(Deps{Pool: pool, Version: "mcp-test"})

	// call は /mcp/<key> を1回叩く。
	call := func(token, key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, MCPPath+"/"+key, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	// tool はツールを1回呼び、テキストと isError を返す。
	tool := func(t *testing.T, token, name, args string) (string, bool) {
		t.Helper()
		if args == "" {
			args = "{}"
		}
		w := call(token, projectKey,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+
				`","arguments":`+args+`}}`)
		if w.Code != http.StatusOK {
			t.Fatalf("%s の HTTP status = %d, want 200（body=%s）", name, w.Code, w.Body.String())
		}
		var res struct {
			Result struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				IsError bool `json:"isError"`
			} `json:"result"`
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("%s の応答を読めない: %v（%s）", name, err, w.Body.String())
		}
		if res.Error != nil {
			t.Fatalf("%s が JSON-RPC エラーで返った: %+v", name, res.Error)
		}
		if len(res.Result.Content) != 1 {
			t.Fatalf("%s の content が1件でない: %s", name, w.Body.String())
		}
		return res.Result.Content[0].Text, res.Result.IsError
	}

	const initializeBody = `{"jsonrpc":"2.0","id":1,"method":"initialize",` +
		`"params":{"protocolVersion":"2025-06-18","capabilities":{},` +
		`"clientInfo":{"name":"integration","version":"0"}}}`

	t.Run("トークンが無ければ 401", func(t *testing.T) {
		w := call("", projectKey, initializeBody)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("失効したトークンは 401", func(t *testing.T) {
		w := call(revokedToken, projectKey, initializeBody)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("別プロジェクトに紐づくトークンは 404", func(t *testing.T) {
		// Design.md 6.4.5 / 8.3。**存在を隠すため 403 ではない。**
		w := call(otherToken, projectKey, initializeBody)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("存在しないプロジェクトも 404", func(t *testing.T) {
		w := call(fullToken, "no-such-project", initializeBody)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("GET は 405", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, MCPPath+"/"+projectKey, nil)
		req.Header.Set("Authorization", "Bearer "+fullToken)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("initialize が通る", func(t *testing.T) {
		w := call(fullToken, projectKey, initializeBody)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", w.Code, w.Body.String())
		}
		var res struct {
			Result struct {
				ProtocolVersion string `json:"protocolVersion"`
				ServerInfo      struct {
					Name    string `json:"name"`
					Version string `json:"version"`
				} `json:"serverInfo"`
			} `json:"result"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, w.Body.String())
		}
		if res.Result.ProtocolVersion != "2025-06-18" {
			t.Errorf("protocolVersion = %q", res.Result.ProtocolVersion)
		}
		if res.Result.ServerInfo.Version != "mcp-test" {
			t.Errorf("serverInfo.version = %q, want mcp-test", res.Result.ServerInfo.Version)
		}
	})

	t.Run("委譲によりプロジェクトを読める", func(t *testing.T) {
		// **エージェント自身は app_user も project_member も持たない**
		// （Design.md 6.5）。所有者のロールで通ることがここで確かめられる。
		text, isErr := tool(t, fullToken, "pb_get_project", "")
		if isErr {
			t.Fatalf("pb_get_project が失敗した: %s", text)
		}
		var got struct {
			Key    string `json:"key"`
			MyRole string `json:"my_role"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Key != projectKey {
			t.Errorf("key = %q, want %q", got.Key, projectKey)
		}
		if got.MyRole != "project_member" {
			t.Errorf("my_role = %q, want project_member（所有者のロールが出る）", got.MyRole)
		}
	})

	t.Run("文書テンプレート4件が目次に並ぶ", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_list_docs", "")
		if isErr {
			t.Fatalf("pb_list_docs が失敗した: %s", text)
		}
		var got struct {
			Items []struct {
				Path    string `json:"path"`
				Title   string `json:"title"`
				Outline []struct {
					Section string `json:"section"`
				} `json:"outline"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}

		var paths []string
		for _, it := range got.Items {
			paths = append(paths, it.Path)
		}
		// DbDesign.md 8.1.2 の型4件（手順21 / 23）。
		want := []string{"vision", "rules", "decisions", "learnings"}
		for _, w := range want {
			if !contains(paths, w) {
				t.Errorf("%s が目次に無い（並び %v）", w, paths)
			}
		}
		// **outline が付いていること**——付いていないと、エージェントは
		// どの章を読むか決められない（ApiDesign.md 10.2）。
		//
		// **テンプレート4件は見出しを持たない**（0018 の本文）。空の outline は
		// 「?outline=1 が効いていない」と区別が付かないので、見出しを持つ
		// handbook で測る。
		var sections []string
		for _, it := range got.Items {
			if it.Path != "handbook" {
				continue
			}
			for _, o := range it.Outline {
				sections = append(sections, o.Section)
			}
		}
		if strings.Join(sections, ",") != "命名,ブランチ" {
			t.Errorf("handbook の outline = %v, want [命名 ブランチ]", sections)
		}
		// 本文は返さない（目次は本文を持たない）。
		if strings.Contains(text, "body_md") {
			t.Error("目次に body_md が入っている")
		}
	})

	t.Run("文書の本文が Markdown で返る", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_get_doc", `{"path":"rules"}`)
		if isErr {
			t.Fatalf("pb_get_doc が失敗した: %s", text)
		}
		// Design.md 8.5：JSON でくるまず本文そのものを返す。
		if strings.HasPrefix(strings.TrimSpace(text), "{") {
			t.Errorf("JSON が返っている: %s", text)
		}
		if text == "" {
			t.Error("本文が空である")
		}
	})

	t.Run("章を指定すると本文が短くなる", func(t *testing.T) {
		full, _ := tool(t, fullToken, "pb_get_doc", `{"path":"handbook"}`)

		// **章の名前は本文から取る。** 見出しを手で書くと、本文だけが
		// 直ったときに検証が古い値を見続ける（0018 が実際にそうだった）。
		section := firstHeading(full)
		if section == "" {
			t.Fatalf("見出しを読み取れない: %s", full)
		}

		part, isErr := tool(t, fullToken, "pb_get_doc",
			`{"path":"handbook","section":`+quote(section)+`}`)
		if isErr {
			t.Fatalf("章の取得が失敗した: %s", part)
		}
		if len(part) >= len(full) {
			t.Errorf("章を指定しても短くなっていない（全文 %d / 章 %d）", len(full), len(part))
		}
		if !strings.Contains(part, section) {
			t.Errorf("指定した章 %q が本文に無い: %s", section, part)
		}
	})

	t.Run("見つからない章は次の一手つきで失敗する", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_get_doc", `{"path":"handbook","section":"存在しない章"}`)
		if !isErr {
			t.Fatalf("isError が立っていない: %s", text)
		}
		// ApiDesign.md 10.3：available_sections を落とさないこと。
		// **エージェントが、もう一度目次を取りに行かずに次の一手を選べる。**
		if !strings.Contains(text, "available_sections") || !strings.Contains(text, "ブランチ") {
			t.Errorf("次の一手の材料が落ちている: %s", text)
		}
	})

	t.Run("スコープから doc.view を外すと文書を読めない", func(t *testing.T) {
		// **所有者は doc.view を持つ**（0017 が operator と project_member へ
		// 与えている）。それでも読めないのは、トークンのスコープとの積で
		// 消えるためである（Design.md 6.4.1）。
		text, isErr := tool(t, narrowToken, "pb_get_doc", `{"path":"rules"}`)
		if !isErr {
			t.Fatalf("スコープ外なのに読めた: %s", text)
		}
		if !strings.Contains(text, "403") || !strings.Contains(text, "forbidden") {
			t.Errorf("403 forbidden が伝わっていない: %s", text)
		}
		// 同じトークンでも、スコープに含めたものは読める（絞り込みが
		// 効いていることと、トークンごと死んでいないことの両方を見る）。
		if _, isErr := tool(t, narrowToken, "pb_get_project", ""); isErr {
			t.Error("project.view はスコープに含めたのに読めない")
		}
	})

	t.Run("チケットの一覧が軽量な形で返る", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_list_tasks", `{"open":true}`)
		if isErr {
			t.Fatalf("pb_list_tasks が失敗した: %s", text)
		}
		var got struct {
			Items []map[string]json.RawMessage `json:"items"`
			Total int                          `json:"total"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Total != 1 || len(got.Items) != 1 {
			t.Fatalf("件数 = %d（items %d）, want 1", got.Total, len(got.Items))
		}
		if len(got.Items[0]) != 10 {
			t.Errorf("項目数 = %d, want 10（Design.md 8.5）", len(got.Items[0]))
		}
		if string(got.Items[0]["seq"]) != "1" {
			t.Errorf("seq = %s, want 1", got.Items[0]["seq"])
		}
	})

	t.Run("assignee=me は所有者の担当を返す", func(t *testing.T) {
		// **チケットの担当は所有者（人）である。** エージェント自身を指すと
		// 0件になる（Design.md 8.5）。
		text, isErr := tool(t, fullToken, "pb_list_tasks", `{"assignee":"me"}`)
		if isErr {
			t.Fatalf("pb_list_tasks が失敗した: %s", text)
		}
		var got struct {
			Total int `json:"total"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Total != 1 {
			t.Errorf("assignee=me の件数 = %d, want 1（所有者の担当が返ること）", got.Total)
		}
	})

	t.Run("チケット1件を seq で引ける", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_get_task", `{"seq":1}`)
		if isErr {
			t.Fatalf("pb_get_task が失敗した: %s", text)
		}
		var got struct {
			Seq   int    `json:"seq"`
			Title string `json:"title"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Seq != 1 || got.Title != "MCP から読むチケット" {
			t.Errorf("チケット = %+v", got)
		}
	})

	t.Run("見つからないチケットはツールの失敗になる", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_get_task", `{"seq":9999}`)
		if !isErr {
			t.Fatalf("isError が立っていない: %s", text)
		}
		if !strings.Contains(text, "404") {
			t.Errorf("404 が伝わっていない: %s", text)
		}
	})

	t.Run("所有者を無効化するとエージェントも通らない", func(t *testing.T) {
		// Design.md 6.5 の委譲。**最後に置く**——この後の検証は通らなくなる。
		if _, err := pool.Exec(ctx, `UPDATE actor SET is_active = false WHERE id = $1`,
			ownerID); err != nil {
			t.Fatalf("所有者を無効化できない: %v", err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(),
				`UPDATE actor SET is_active = true WHERE id = $1`, ownerID); err != nil {
				t.Errorf("所有者を戻せなかった: %v", err)
			}
		})

		w := call(fullToken, projectKey, initializeBody)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401（body=%s）", w.Code, w.Body.String())
		}
	})
}

// contains は文字列の並びに v があるかを返す。
func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// firstHeading は Markdown の最初の ## 見出しのテキストを返す。
//
// **期待値をテンプレートの本文から取るための道具である。** 見出しを手で
// 書くと、0018 のように本文だけが直ったときに検証が古い値を見続ける。
func firstHeading(md string) string {
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(line, "## ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "## "))
		}
	}
	return ""
}

// quote は JSON の文字列リテラルにする（日本語の見出しを引数に埋めるため）。
func quote(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `""`
	}
	return string(b)
}
