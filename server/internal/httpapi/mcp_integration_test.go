package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
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
		// **agent_run は actor を ON DELETE RESTRICT で参照する**（手順26c。
		// DbDesign.md 8.2.4）。project を消せば ticket 経由の CASCADE で
		// agent_run も落ちるが、**順序が入れ替わると actor の削除が止まる**ので
		// 明示で消す。agent_report は agent_run の CASCADE で落ちる。
		if _, err := pool.Exec(bg, `DELETE FROM agent_run WHERE actor_id = ANY($1)`,
			[]string{ownerID, agentID}); err != nil {
			t.Errorf("agent_run の後始末に失敗した: %v", err)
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
	// **実際のエージェントが持つ既定スコープのトークン**（Design.md 6.5。pb-75）。
	//
	// **fullToken では ticket.self_edit の絞り込みを測れない。** あちらは
	// スコープが空＝絞り込みなしなので所有者の ticket.edit まで通り、
	// **狭いほうの経路に一度も入らない。** 既定スコープは ticket.self_edit を
	// 持ち ticket.edit を持たないので、ここが 9.5.2 の負の側になる。
	agentToken := auth.AgentTokenPrefix + "agent-" + suffix
	issue("agent", agentToken, projectID, []string{
		"agent.run", "comment.create", "doc.view", "project.view",
		"ticket.assign", "ticket.create", "ticket.reference.edit",
		"ticket.self_edit", "ticket.transition", "ticket.view",
	})

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

	// **逆側も同じ道具で1回測る**（Testing.md 6）——助言が付くべき 403 で
	// 付いていることを確かめないと、上の「付かない」は検知が効いている証拠に
	// ならない（付け忘れでも通ってしまう）。
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
		// **こちらには助言が付く**（原因がスコープだから）。検証6 の側で
		// 「付かない」を測っているので、両側を同じ道具で見ている。
		if !strings.Contains(text, "発行時のスコープ") {
			t.Errorf("スコープの助言が付いていない: %s", text)
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
		if len(got.Items[0]) != 11 {
			t.Errorf("項目数 = %d, want 11（Design.md 8.5。手順26b で working_agent を足した）",
				len(got.Items[0]))
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

	// ── write 系（手順26a。Design.md 8.5.1）─────────────────────
	//
	// **単体はフェイクの REST を相手にしている**ので、ここで初めて
	// 「実際に行が増えたか」「所有者のロールで弾かれるか」が測れる。

	t.Run("pb_create_ticket がチケットを作る", func(t *testing.T) {
		// **project_counter を実データに合わせる。** 上の準備が ticket を
		// 直接 INSERT していて採番器を進めていないため、そのままだと
		// 9.3 の `UPDATE ... RETURNING` が seq=1 を返して一意制約に当たる。
		if _, err := pool.Exec(ctx, `
			UPDATE project_counter SET last_ticket_seq =
				(SELECT coalesce(max(seq), 0) FROM ticket WHERE project_id = $1)
			WHERE project_id = $1`, projectID); err != nil {
			t.Fatalf("採番器を合わせられない: %v", err)
		}

		text, isErr := tool(t, fullToken, "pb_create_ticket",
			`{"type":"task","title":"MCP から起票したチケット","body_md":"本文","priority":"high","assignee_id":"me"}`)
		if isErr {
			t.Fatalf("起票できない: %s", text)
		}

		var got struct {
			Seq      int    `json:"seq"`
			Title    string `json:"title"`
			Assignee *struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"assignee"`
			StagedAt *string `json:"staged_at"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Seq < 2 {
			t.Errorf("seq = %d, want 2 以上（既存の1件の次）", got.Seq)
		}
		// **assignee_id=me は所有者を指す**（Design.md 8.5）。担当を持つのは人である。
		if got.Assignee == nil || got.Assignee.ID != ownerID {
			t.Errorf("assignee = %+v, want 所有者（%s）", got.Assignee, ownerID)
		}
		// **作ったチケットは必ずバックログに入る**（9.3）。
		if got.StagedAt != nil {
			t.Errorf("staged_at = %v, want null（作成時はバックログ）", *got.StagedAt)
		}

		// 実際に行が増えていること。**reporter はエージェント自身である**
		// （権限の根拠は所有者だが、操作したのはエージェント。Design.md 6.5）。
		var reporter string
		if err := pool.QueryRow(ctx,
			`SELECT coalesce(reporter_id, '') FROM ticket WHERE project_id = $1 AND seq = $2`,
			projectID, got.Seq).Scan(&reporter); err != nil {
			t.Fatalf("作ったチケットを引けない: %v", err)
		}
		if reporter != agentID {
			t.Errorf("reporter_id = %q, want エージェント（%s）", reporter, agentID)
		}
	})

	t.Run("ticket.create を持たないトークンでは起票できない", func(t *testing.T) {
		// narrowToken は agent.run / project.view / ticket.view だけ。
		// **所有者は ticket.create を持つ**（project_member）ので、
		// ここで効いているのはトークンのスコープである（6.4.1 の積）。
		text, isErr := tool(t, narrowToken, "pb_create_ticket",
			`{"type":"task","title":"通らないはず"}`)
		if !isErr {
			t.Fatalf("403 になるはずが成功した: %s", text)
		}
		if !strings.Contains(text, "403") {
			t.Errorf("403 と読めない: %s", text)
		}
	})

	t.Run("pb_post_note がコメントを書き、origin が agent になる", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_post_note",
			`{"seq":1,"body_md":"並列実行すると落ちる","kind":"caveat"}`)
		if isErr {
			t.Fatalf("コメントを書けない: %s", text)
		}

		var got struct {
			ID     string `json:"id"`
			Kind   string `json:"kind"`
			Origin string `json:"origin"`
			Author struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"author"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Kind != "caveat" {
			t.Errorf("kind = %q, want caveat", got.Kind)
		}
		// **origin はリクエストで指定できない**（9.8）。呼び出し元の種別から決まる。
		if got.Origin != "agent" {
			t.Errorf("origin = %q, want agent", got.Origin)
		}
		// **画面はこの kind でアバターを角丸四角にする**（GuiDesign.md 8.4.2）。
		if got.Author.Kind != "agent" || got.Author.ID != agentID {
			t.Errorf("author = %+v, want エージェント（%s）", got.Author, agentID)
		}

		// activity に1行入る（9.8「コメントの変更を activity に記録する」）。
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM activity
			WHERE entity_type = 'ticket' AND entity_id = $1 AND field = 'comment'`,
			ticketID).Scan(&n); err != nil {
			t.Fatalf("activity を数えられない: %v", err)
		}
		if n != 1 {
			t.Errorf("activity = %d行, want 1", n)
		}
	})

	t.Run("doc.edit を持たない所有者では pb_put_doc が 403", func(t *testing.T) {
		// **所有者は project_member であり、doc.edit は project_admin だけが持つ**
		// （0017。DbDesign.md 8.1.4）。fullToken はスコープを絞っていないので、
		// **ここで効いているのは所有者のロールのほう**である（6.4.1 の積の左辺）。
		text, isErr := tool(t, fullToken, "pb_put_doc",
			`{"path":"handbook","body_md":"# 書き換え\n\n通らないはず"}`)
		if !isErr {
			t.Fatalf("403 になるはずが成功した: %s", text)
		}

		// **本文が変わっていないこと。** 読めるが書けない、が正しい形である。
		var body string
		if err := pool.QueryRow(ctx, `SELECT body_md FROM document WHERE id = $1`,
			docID).Scan(&body); err != nil {
			t.Fatalf("文書を引けない: %v", err)
		}
		if strings.Contains(body, "通らないはず") {
			t.Error("403 のはずが本文が書き換わっている")
		}
	})

	t.Run("所有者が doc.edit を持てば pb_put_doc が通る", func(t *testing.T) {
		// **委譲を実データで測る**（Design.md 6.5）。所有者のロールを上げると、
		// トークンを再発行しなくてもエージェントの実効権限が変わる。
		if _, err := pool.Exec(ctx,
			`UPDATE project_member SET role_key = 'project_admin'
			 WHERE project_id = $1 AND actor_id = $2`, projectID, ownerID); err != nil {
			t.Fatalf("所有者のロールを上げられない: %v", err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(),
				`UPDATE project_member SET role_key = 'project_member'
				 WHERE project_id = $1 AND actor_id = $2`, projectID, ownerID); err != nil {
				t.Errorf("所有者のロールを戻せなかった: %v", err)
			}
		})
		// **実効権限のキャッシュを落とす**（Design.md 6.4.5）。ロールを直接
		// 書き換えたので、トークンに焼かれた cached_permissions が古い。
		if _, err := pool.Exec(ctx,
			`UPDATE access_token SET cached_permissions = NULL, permissions_cached_at = NULL
			 WHERE actor_id = $1`, agentID); err != nil {
			t.Fatalf("権限キャッシュを落とせない: %v", err)
		}

		var before int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM document_revision WHERE document_id = $1`,
			docID).Scan(&before); err != nil {
			t.Fatalf("リビジョンを数えられない: %v", err)
		}

		text, isErr := tool(t, fullToken, "pb_put_doc",
			`{"path":"handbook","body_md":"# 手引き\n\n## 命名\n\nエージェントが書き換えた。\n",`+
				`"change_reason":"MCP から更新した"}`)
		if isErr {
			t.Fatalf("書き換えられない: %s", text)
		}

		var got struct {
			Version   int `json:"version"`
			UpdatedBy *struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"updated_by"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		// **If-Match が効いている証拠。** GET で読んだ version の次になる。
		if got.Version < 2 {
			t.Errorf("version = %d, want 2 以上", got.Version)
		}
		// **憲章に「エージェントが最後に更新した」と出るのは正常である**
		// （ApiDesign.md 10.3）。
		if got.UpdatedBy == nil || got.UpdatedBy.Kind != "agent" || got.UpdatedBy.ID != agentID {
			t.Errorf("updated_by = %+v, want エージェント（%s）", got.UpdatedBy, agentID)
		}

		// **リビジョンが1件増える**（10.4「title か body_md が変わったときだけ」）。
		var after int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM document_revision WHERE document_id = $1`,
			docID).Scan(&after); err != nil {
			t.Fatalf("リビジョンを数えられない: %v", err)
		}
		if after != before+1 {
			t.Errorf("リビジョン = %d件, want %d件", after, before+1)
		}
		// change_reason が入っていること（10.5 が履歴に出す）。
		var reason string
		if err := pool.QueryRow(ctx, `
			SELECT coalesce(change_reason, '') FROM document_revision
			WHERE document_id = $1 ORDER BY revision_no DESC LIMIT 1`,
			docID).Scan(&reason); err != nil {
			t.Fatalf("change_reason を引けない: %v", err)
		}
		if reason != "MCP から更新した" {
			t.Errorf("change_reason = %q", reason)
		}
	})

	// ── 遷移系（手順26b。Design.md 8.5.3、ApiDesign.md 9.6 の検証6）──────

	// 担当が「所有者ではない」チケットを1件用意する。**検証6 の負の側**であり、
	// これが無いと「エージェントは所有者の担当だけを進められる」を測れない。
	//
	// **未割当（assignee_id が NULL）にする。** 別の人を作って割り当てるより
	// 材料が少なく、9.6 が「担当が未割当のものも進められない」と定めている
	// ぶん、規則そのものを直接測れる。
	foreignTicketID := ulidgen.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO ticket (id, project_id, seq, type, title, status_key)
		VALUES ($1, $2, 90, 'task', '担当が付いていないチケット', 'todo')`,
		foreignTicketID, projectID); err != nil {
		t.Fatalf("担当なしのチケットを作れない: %v", err)
	}

	t.Run("pb_list_transitions が進める先と理由を返す", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_list_transitions", `{"seq":1}`)
		if isErr {
			t.Fatalf("失敗した: %s", text)
		}
		var got struct {
			Current struct {
				Key string `json:"key"`
			} `json:"current"`
			Items []struct {
				Key     string  `json:"key"`
				Allowed bool    `json:"allowed"`
				Reason  *string `json:"reason"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Current.Key != "todo" {
			t.Errorf("current = %q, want todo", got.Current.Key)
		}
		// simple テンプレート（DbDesign.md 7.4）：todo からは in_progress のみ。
		// **done は allowed:false で残る**——9.7 が「遷移できない先も理由付きで
		// 返す」と定めており、隠すと「なぜ完了にできないか」が読めなくなる。
		seen := map[string]bool{}
		for _, it := range got.Items {
			seen[it.Key] = it.Allowed
			if !it.Allowed && (it.Reason == nil || *it.Reason == "") {
				t.Errorf("%s が allowed:false なのに reason が無い", it.Key)
			}
		}
		if !seen["in_progress"] {
			t.Errorf("in_progress へ進めない（items=%s）", text)
		}
		if seen["done"] {
			t.Error("done へ進めることになっている（エージェントはクローズできない）")
		}
	})

	t.Run("担当が所有者ならエージェントが状態を進められる", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_transition_task",
			`{"seq":1,"to":"in_progress","comment":"着手します"}`)
		if isErr {
			t.Fatalf("失敗した: %s", text)
		}
		var got struct {
			Status struct {
				Key string `json:"key"`
			} `json:"status"`
			WorkingAgent *struct {
				ID   string `json:"id"`
				Kind string `json:"kind"`
			} `json:"working_agent"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Status.Key != "in_progress" {
			t.Errorf("status = %q, want in_progress", got.Status.Key)
		}
		// **遷移の副作用で実行者が立つ**（ApiDesign.md 9.6）。
		if got.WorkingAgent == nil {
			t.Fatalf("working_agent が立っていない（%s）", text)
		}
		if got.WorkingAgent.ID != agentID {
			t.Errorf("working_agent.id = %q, want %q", got.WorkingAgent.ID, agentID)
		}
		if got.WorkingAgent.Kind != "agent" {
			t.Errorf("working_agent.kind = %q, want agent", got.WorkingAgent.Kind)
		}

		// **遷移コメントが同じトランザクションで入る**（9.6）。
		var kind, origin string
		if err := pool.QueryRow(ctx, `
			SELECT kind, origin FROM comment
			WHERE ticket_id = $1 ORDER BY created_at DESC LIMIT 1`,
			ticketID).Scan(&kind, &origin); err != nil {
			t.Fatalf("遷移コメントを引けない: %v", err)
		}
		if kind != "progress" || origin != "agent" {
			t.Errorf("遷移コメント = (%s, %s), want (progress, agent)", kind, origin)
		}
	})

	t.Run("再送は 409 に倒れる（冪等キーが要らない根拠）", func(t *testing.T) {
		// Design.md 8.6：「再送で何が二重になるか」を1つも言えないことの実測。
		// 2度目は「進行中 → 進行中」を要求することになり、
		// ck_workflow_transition_diff により定義が無いため検証2 で落ちる。
		text, isErr := tool(t, fullToken, "pb_transition_task",
			`{"seq":1,"to":"in_progress"}`)
		if !isErr {
			t.Fatalf("再送が通ってしまった: %s", text)
		}
		if !strings.Contains(text, "invalid_transition") {
			t.Errorf("invalid_transition が返っていない: %s", text)
		}
	})

	t.Run("担当が所有者でないチケットは検証6 で拒まれる", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_transition_task",
			`{"seq":90,"to":"in_progress"}`)
		if !isErr {
			t.Fatalf("拒まれなかった: %s", text)
		}
		if !strings.Contains(text, "403") {
			t.Errorf("403 が返っていない: %s", text)
		}
		if !strings.Contains(text, "所有者") {
			t.Errorf("検証6 の理由が返っていない: %s", text)
		}
		// **スコープの助言を添えない**（手順26b。mcp/rest.go の tokenScopeHint）。
		// 検証6 の 403 はスコープと無関係で、添えるとモデルを誤った方向へ送る。
		// **実サーバの検証で実際に付いていたので、ここで押さえる。**
		if strings.Contains(text, "発行時のスコープ") {
			t.Errorf("スコープの助言が付いている（原因はスコープではない）: %s", text)
		}

		// **状態も実行者も動いていないこと。** 拒否が「返り値だけ」で、
		// 副作用が残っていないかを実物で見る。
		var status string
		var workingAgent *string
		if err := pool.QueryRow(ctx,
			`SELECT status_key, working_agent_id FROM ticket WHERE id = $1`,
			foreignTicketID).Scan(&status, &workingAgent); err != nil {
			t.Fatalf("チケットを引けない: %v", err)
		}
		if status != "todo" || workingAgent != nil {
			t.Errorf("拒んだのに動いている: status=%s working_agent=%v", status, workingAgent)
		}
	})

	t.Run("拒まれるチケットでは全ての遷移先が allowed:false になる", func(t *testing.T) {
		// 検証6 はチケット単位の条件なので、9.7 の items[] が全行同時に落ちる。
		text, isErr := tool(t, fullToken, "pb_list_transitions", `{"seq":90}`)
		if isErr {
			t.Fatalf("失敗した: %s", text)
		}
		var got struct {
			Items []struct {
				Key     string  `json:"key"`
				Allowed bool    `json:"allowed"`
				Reason  *string `json:"reason"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if len(got.Items) == 0 {
			t.Fatal("items が空である")
		}
		for _, it := range got.Items {
			if it.Allowed {
				t.Errorf("%s が allowed:true になっている", it.Key)
			}
		}
	})

	t.Run("pb_list_tasks が working_agent を運ぶ", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_list_tasks", `{"assignee":"me"}`)
		if isErr {
			t.Fatalf("失敗した: %s", text)
		}
		var got struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("一覧を読めない: %v（%s）", err, text)
		}
		// **items[0] を見ない。** この時点で所有者の担当は複数あり（先の
		// pb_create_ticket が1件足している）、並びは sort_key 順である。
		// 遷移したのは seq 1 なので、そこを名指しで探す。
		var target map[string]json.RawMessage
		for _, it := range got.Items {
			if string(it["seq"]) == "1" {
				target = it
				break
			}
		}
		if target == nil {
			t.Fatalf("seq 1 が一覧に無い（%s）", text)
		}
		raw, ok := target["working_agent"]
		if !ok {
			t.Fatalf("working_agent が落ちている（%s）", text)
		}
		if !strings.Contains(string(raw), agentID) {
			t.Errorf("working_agent = %s, want %s を含む", raw, agentID)
		}
	})

	// restJSON は /api/v1 を Bearer トークンで1回叩く（PATCH の検証用）。
	//
	// **MCP の口を通さない。** working_agent_id を人が消す経路は REST の
	// PATCH（ApiDesign.md 9.5.2）であり、MCP には出していない——実行者を
	// 立てるのはエージェント自身で、消すのは人だからである。
	restJSON := func(method, path, token, body, ifMatch string) *httptest.ResponseRecorder {
		var rd *strings.Reader
		if body == "" {
			rd = strings.NewReader("")
		} else {
			rd = strings.NewReader(body)
		}
		req := httptest.NewRequest(method, "/api/v1"+path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		if ifMatch != "" {
			req.Header.Set("If-Match", ifMatch)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("人は PATCH で実行者を消せる", func(t *testing.T) {
		// 直前の遷移で seq 1 に実行者が立っている。**版を実物から取る**
		// ——手で書いた期待値は、先行する検証が1つ増えるたびに腐る。
		get := restJSON(http.MethodGet, "/projects/"+projectKey+"/tickets/1", fullToken, "", "")
		if get.Code != http.StatusOK {
			t.Fatalf("チケットを読めない: %d（%s）", get.Code, get.Body.String())
		}
		var cur struct {
			Version      int  `json:"version"`
			WorkingAgent *any `json:"working_agent"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &cur); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		if cur.WorkingAgent == nil {
			t.Fatal("前提が崩れている：実行者が立っていない")
		}

		w := restJSON(http.MethodPatch, "/projects/"+projectKey+"/tickets/1", fullToken,
			`{"working_agent_id":null}`, `"`+strconv.Itoa(cur.Version)+`"`)
		if w.Code != http.StatusOK {
			t.Fatalf("PATCH の status = %d, want 200（%s）", w.Code, w.Body.String())
		}
		var after struct {
			WorkingAgent *any `json:"working_agent"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &after); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		if after.WorkingAgent != nil {
			t.Errorf("working_agent = %v, want null", *after.WorkingAgent)
		}

		// **activity に1行残る**（9.5.2）。人が変えたときは記録する
		// ——記録しないのは遷移の副作用のほうだけである。
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM activity
			WHERE entity_id = $1 AND field = 'working_agent_id'`, ticketID).Scan(&n); err != nil {
			t.Fatalf("activity を数えられない: %v", err)
		}
		if n != 1 {
			t.Errorf("working_agent_id の activity = %d行, want 1", n)
		}
	})

	t.Run("実行者に人を指定すると 422", func(t *testing.T) {
		get := restJSON(http.MethodGet, "/projects/"+projectKey+"/tickets/1", fullToken, "", "")
		var cur struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &cur); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		// **所有者（人）の ULID を渡す。** 実行者の欄に人が入る経路を作らない
		// ——担当と実行者を分けた意味が消える（DbDesign.md 6.6）。
		w := restJSON(http.MethodPatch, "/projects/"+projectKey+"/tickets/1", fullToken,
			`{"working_agent_id":"`+ownerID+`"}`, `"`+strconv.Itoa(cur.Version)+`"`)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（%s）", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "not_found") {
			t.Errorf("not_found が返っていない: %s", w.Body.String())
		}
	})

	// ── コンテキストパック（手順27。Design.md 8.5.5）───────────
	//
	// **ここに置くのは restJSON が要るためである**（スコープ境界を書く経路が
	// PATCH しかない）。/pb-implement の流れでは pb_get_task の直後に来る。
	//
	// **単体はフェイクの REST を相手にしている**ので、ここで初めて
	// 「3種類の応答が実データで合成されるか」「doc.view を外したときに憲章だけが
	// 落ちるか」が測れる。

	t.Run("pb_get_context が境界・憲章・依存を1枚にまとめる", func(t *testing.T) {
		// **版を実物から取る**（先行する検証が1つ増えるたびに腐るため）。
		get := restJSON(http.MethodGet, "/projects/"+projectKey+"/tickets/1", fullToken, "", "")
		if get.Code != http.StatusOK {
			t.Fatalf("チケットを読めない: %d（%s）", get.Code, get.Body.String())
		}
		var cur struct {
			Version int `json:"version"`
		}
		if err := json.Unmarshal(get.Body.Bytes(), &cur); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}

		// **境界を書く経路は PATCH だけである**（9.5.2。手順27 で開けた）。
		w := restJSON(http.MethodPatch, "/projects/"+projectKey+"/tickets/1", fullToken,
			`{"scope":{"allow":["src/auth/**"],"deny":["migrations/**"]},`+
				`"execution_mode":"agent_draft","readiness":"yellow",`+
				`"readiness_note":"認証方式が未決",`+
				// **本文を入れるのは、下の「重ねて運ばない」を空振りさせないためである。**
				// 固定データの本文は NULL なので、書かずに測ると必ず通ってしまう。
				`"body_md":"この本文はパックに載らない"}`,
			`"`+strconv.Itoa(cur.Version)+`"`)
		if w.Code != http.StatusOK {
			t.Fatalf("PATCH の status = %d, want 200（%s）", w.Code, w.Body.String())
		}

		text, isErr := tool(t, fullToken, "pb_get_context", `{"seq":1}`)
		if isErr {
			t.Fatalf("pb_get_context が失敗した: %s", text)
		}
		// 5節すべてと、3種類の応答それぞれから来た値が1枚に入っていること。
		for _, want := range []string{
			"## 1. スコープ境界と制約",
			"## 2. 実行の前提",
			"## 3. 憲章",
			"## 4. 依存・関連するチケット",
			"## 5. 足りないときの調べ方",
			"`src/auth/**`",   // チケット（9.5.1 の scope）
			"認証方式が未決",         // 同上（readiness_note）
			"価値観・世界観",         // 憲章の目次（10.2）
			"### 規約（`rules`）", // 憲章の本文（10.3）
		} {
			if !strings.Contains(text, want) {
				t.Errorf("パックに %q が無い:\n%s", want, text)
			}
		}
		// **本文と完了条件は重ねて運ばない**（8.5.5）。
		if strings.Contains(text, "この本文はパックに載らない") {
			t.Errorf("チケット本文が入っている:\n%s", text)
		}
	})

	t.Run("doc.view が無いと憲章だけが落ちる", func(t *testing.T) {
		// Design.md 8.5.5：**403 で全体を落とすと、読めるはずのものまで届かない。**
		text, isErr := tool(t, narrowToken, "pb_get_context", `{"seq":1}`)
		if isErr {
			t.Fatalf("憲章の欠落は切り詰めであって失敗ではない: %s", text)
		}
		if !strings.Contains(text, "doc.view") {
			t.Errorf("省いた理由を書いていない:\n%s", text)
		}
		if !strings.Contains(text, "`src/auth/**`") {
			t.Errorf("読めるはずのスコープ境界まで落ちている:\n%s", text)
		}
		if strings.Contains(text, "### 規約（`rules`）") {
			t.Errorf("憲章が載っている（doc.view を外したのに）:\n%s", text)
		}
	})

	// ── 完了レポート（手順26c。ApiDesign.md 9.15）─────────────

	// **完了条件を2件足す。** unsatisfied_dod が「自己申告との突き合わせ」で
	// あることを実データで確かめるための材料である。
	dodA, dodB := ulidgen.New(), ulidgen.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO dod_item (id, ticket_id, sort_order, type, body) VALUES
		  ($1, $3, 10, 'manual', 'ユニットテストが通ること'),
		  ($2, $3, 20, 'manual', '設計文書を更新すること')`,
		dodA, dodB, ticketID); err != nil {
		t.Fatalf("完了条件を作れない: %v", err)
	}

	t.Run("pb_submit_result が実行記録・レポート・コメントを作る", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_submit_result", `{
			"seq":1,"status":"partial",
			"artifacts":[{"type":"pull_request","url":"https://example.com/pr/1"}],
			"dod_results":[{"id":`+quote(dodA)+`,"passed":true,"evidence":"go test → ok"}],
			"failures":[{"approach":"ライブラリZ","reason":"版が競合"}],
			"cost":{"tokens":128000,"turns":34,"wall_clock_min":42},
			"knowledge_impact":"minor","weather":"晴れ"}`)
		if isErr {
			t.Fatalf("失敗した: %s", text)
		}
		var got struct {
			ID             string `json:"id"`
			AgentRunID     string `json:"agent_run_id"`
			Status         string `json:"status"`
			CommentID      string `json:"comment_id"`
			UnsatisfiedDoD []struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			} `json:"unsatisfied_dod"`
			SubmittedBy struct {
				Kind string `json:"kind"`
			} `json:"submitted_by"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}

		// **unsatisfied_dod は passed: true として現れなかったもの**（9.15）。
		// dodA は is_satisfied=false のままだが、申告したので返らない。
		if len(got.UnsatisfiedDoD) != 1 || got.UnsatisfiedDoD[0].ID != dodB {
			t.Errorf("unsatisfied_dod = %+v, want [%s] のみ", got.UnsatisfiedDoD, dodB)
		}
		if got.SubmittedBy.Kind != "agent" {
			t.Errorf("submitted_by.kind = %q, want agent", got.SubmittedBy.Kind)
		}

		// **agent_run は実行時点の値を写す**（DbDesign.md 8.2.4）。
		var runStatus, clientKind string
		var tokens int64
		var turns, retry int32
		var gapMin float64
		if err := pool.QueryRow(ctx, `
			SELECT status, client_kind, tokens_used, turns, retry_count,
			       EXTRACT(EPOCH FROM (ended_at - started_at)) / 60
			  FROM agent_run WHERE id = $1`, got.AgentRunID).
			Scan(&runStatus, &clientKind, &tokens, &turns, &retry, &gapMin); err != nil {
			t.Fatalf("agent_run を引けない: %v", err)
		}
		if runStatus != "completed" {
			t.Errorf("agent_run.status = %q, want completed（Phase 2 はこれだけ）", runStatus)
		}
		if clientKind != "claude_code" {
			t.Errorf("client_kind = %q, want claude_code", clientKind)
		}
		if tokens != 128000 || turns != 34 || retry != 0 {
			t.Errorf("コスト/retry = (%d, %d, %d), want (128000, 34, 0)", tokens, turns, retry)
		}
		// **started_at は wall_clock_min から逆算する**（8.2.4）。
		if gapMin < 41.9 || gapMin > 42.1 {
			t.Errorf("ended_at - started_at = %.2f分, want 42", gapMin)
		}

		// **report jsonb は本文をそのまま持つ**（知らないキーも落ちない。9.15）。
		var impact string
		var report []byte
		if err := pool.QueryRow(ctx, `
			SELECT knowledge_impact, report FROM agent_report WHERE id = $1`, got.ID).
			Scan(&impact, &report); err != nil {
			t.Fatalf("agent_report を引けない: %v", err)
		}
		if impact != "minor" {
			t.Errorf("knowledge_impact = %q, want minor", impact)
		}
		if !strings.Contains(string(report), "晴れ") {
			t.Errorf("知らないキーが保存されていない: %s", report)
		}

		// **完了レポートのコメントが agent_run を指す**（0007 が空けていた列。6.7）。
		var kind, origin, body string
		var runRef *string
		if err := pool.QueryRow(ctx, `
			SELECT kind, origin, body_md, agent_run_id FROM comment WHERE id = $1`,
			got.CommentID).Scan(&kind, &origin, &body, &runRef); err != nil {
			t.Fatalf("完了レポートのコメントを引けない: %v", err)
		}
		if kind != "progress" || origin != "agent" {
			t.Errorf("コメント = (%s, %s), want (progress, agent)", kind, origin)
		}
		if runRef == nil || *runRef != got.AgentRunID {
			t.Errorf("comment.agent_run_id = %v, want %q", runRef, got.AgentRunID)
		}
		// 人が読む面としての中身（9.15 の本文の形）。
		for _, want := range []string{
			"## 完了レポート（一部完了）",
			"| ✅ ユニットテストが通ること | go test → ok |",
			"| — 設計文書を更新すること | 報告なし |",
			"- ライブラリZ — 版が競合",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("コメント本文に %q が無い:\n%s", want, body)
			}
		}
	})

	t.Run("提出は盤面を動かさない", func(t *testing.T) {
		// **dod_item.is_satisfied も status_key も動かない**（9.15）。
		var satisfied bool
		if err := pool.QueryRow(ctx,
			`SELECT is_satisfied FROM dod_item WHERE id = $1`, dodA).Scan(&satisfied); err != nil {
			t.Fatalf("完了条件を引けない: %v", err)
		}
		if satisfied {
			t.Error("エージェントの申告で完了条件のチェックが立っている（manual の検証者は人間）")
		}
		var status string
		if err := pool.QueryRow(ctx,
			`SELECT status_key FROM ticket WHERE id = $1`, ticketID).Scan(&status); err != nil {
			t.Fatalf("チケットを引けない: %v", err)
		}
		if status != "in_progress" {
			t.Errorf("status_key = %q, want in_progress（レポートは状態を進めない）", status)
		}
	})

	t.Run("再提出は別の run になり retry_count が増える", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_submit_result", `{
			"seq":1,"status":"completed",
			"dod_results":[{"id":`+quote(dodA)+`,"passed":true},
			               {"id":`+quote(dodB)+`,"passed":true}]}`)
		if isErr {
			t.Fatalf("失敗した: %s", text)
		}
		var got struct {
			AgentRunID     string `json:"agent_run_id"`
			UnsatisfiedDoD []any  `json:"unsatisfied_dod"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		// 全部申告したので未充足が無くなる（/pb-implement 手順7 が終わる条件）。
		if len(got.UnsatisfiedDoD) != 0 {
			t.Errorf("unsatisfied_dod = %+v, want 空", got.UnsatisfiedDoD)
		}
		var retry int32
		if err := pool.QueryRow(ctx,
			`SELECT retry_count FROM agent_run WHERE id = $1`, got.AgentRunID).
			Scan(&retry); err != nil {
			t.Fatalf("agent_run を引けない: %v", err)
		}
		if retry != 1 {
			t.Errorf("retry_count = %d, want 1（2回目の提出）", retry)
		}
	})

	t.Run("知らない完了条件は 422 に倒れる", func(t *testing.T) {
		text, isErr := tool(t, fullToken, "pb_submit_result",
			`{"seq":1,"status":"completed","dod_results":[{"id":"01K2NOPE0000000000000000X","passed":true}]}`)
		if !isErr {
			t.Fatalf("拒まれなかった: %s", text)
		}
		if !strings.Contains(text, "not_found") {
			t.Errorf("not_found が返っていない: %s", text)
		}
	})

	t.Run("ticket.transition を持たないトークンでは提出できない", func(t *testing.T) {
		// narrow は agent.run / project.view / ticket.view しか持たない。
		text, isErr := tool(t, narrowToken, "pb_submit_result",
			`{"seq":1,"status":"completed"}`)
		if !isErr {
			t.Fatalf("提出が通ってしまった: %s", text)
		}
		if !strings.Contains(text, "403") {
			t.Errorf("403 が返っていない: %s", text)
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

	// ── pb-75 / pb-76：起票したあと直す ────────────────────────
	//
	// **フェイクでは権限の積を測れない**（Design.md 6.4.1 の
	// 「所有者のロール ∩ トークンのスコープ」）。ここが唯一の場所である。

	t.Run("pb_list_tags がプロジェクトのタグを返す", func(t *testing.T) {
		// **始点を作る。** 0件のまま「表の件数と一致する」を測ると、
		// **実装が空配列を返すだけでも通る**（憲章「ゼロを測るなら先に始点を作る」）。
		for _, name := range []string{"設計", "MCP連携"} {
			if _, err := pool.Exec(ctx,
				`INSERT INTO tag (id, project_id, name, sort_order) VALUES ($1, $2, $3, $4)
				 ON CONFLICT (project_id, name) DO NOTHING`,
				ulidgen.New(), projectID, name, 10); err != nil {
				t.Fatalf("タグを作れない: %v", err)
			}
		}

		text, isErr := tool(t, agentToken, "pb_list_tags", `{}`)
		if isErr {
			t.Fatalf("タグを引けない: %s", text)
		}
		var got struct {
			Items []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		// **件数を書き下さない**（憲章「期待値は決め打ちせず、正本から読む」）。
		// 表に何件あるかを引いて突き合わせる。
		var want int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM tag WHERE project_id = $1`, projectID).Scan(&want); err != nil {
			t.Fatalf("タグの件数を読めない: %v", err)
		}
		if want == 0 {
			t.Fatal("tag 表が空である（始点が意味を持たない）")
		}
		if len(got.Items) != want {
			t.Errorf("タグ = %d件, want %d件（tag 表の全行）", len(got.Items), want)
		}
	})

	t.Run("pb_update_ticket が記述を直す", func(t *testing.T) {
		text, isErr := tool(t, agentToken, "pb_update_ticket",
			`{"seq":1,"title":"エージェントが直したタイトル","body_md":"直した本文"}`)
		if isErr {
			t.Fatalf("直せない: %s", text)
		}
		var got struct {
			Title  string `json:"title"`
			BodyMD string `json:"body_md"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.Title != "エージェントが直したタイトル" {
			t.Errorf("title = %q", got.Title)
		}
		// 実際に行が変わっていること。
		var stored string
		if err := pool.QueryRow(ctx,
			`SELECT title FROM ticket WHERE project_id = $1 AND seq = 1`,
			projectID).Scan(&stored); err != nil {
			t.Fatalf("チケットを引けない: %v", err)
		}
		if stored != "エージェントが直したタイトル" {
			t.Errorf("DB の title = %q", stored)
		}
	})

	// **縛る側の項目は断られる**（9.5.2。0029）。ここが pb-75 の制約条件
	// 「エージェントが自分の縛りを緩められる状態を作らない」の実測である。
	//
	// **REST を直接叩く。** pb_update_ticket はこれらの引数を宣言していないので、
	// MCP 経由では「引数が落ちて空の更新になる」だけで**守りを測れない**
	// ——測りたいのは**権限のほうが最後の砦であること**である。
	patchAsAgent := func(t *testing.T, body string) *httptest.ResponseRecorder {
		t.Helper()
		var cur struct {
			Version int64 `json:"version"`
		}
		get := httptest.NewRequest(http.MethodGet,
			"/api/v1/projects/"+projectKey+"/tickets/1", nil)
		get.Header.Set("Authorization", "Bearer "+agentToken)
		gw := httptest.NewRecorder()
		r.ServeHTTP(gw, get)
		if err := json.Unmarshal(gw.Body.Bytes(), &cur); err != nil {
			t.Fatalf("version を読めない: %v（%s）", err, gw.Body.String())
		}

		req := httptest.NewRequest(http.MethodPatch,
			"/api/v1/projects/"+projectKey+"/tickets/1", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+agentToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", `"`+strconv.FormatInt(cur.Version, 10)+`"`)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	t.Run("エージェントのスコープでは実行モードを変えられない", func(t *testing.T) {
		res := patchAsAgent(t, `{"execution_mode":"agent_only"}`)
		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", res.Code, res.Body.String())
		}
		if !strings.Contains(res.Body.String(), "execution_mode") {
			t.Errorf("断った項目名が本文に無い: %s", res.Body.String())
		}
		// **盤面が動いていないこと。**
		var mode string
		if err := pool.QueryRow(ctx,
			`SELECT execution_mode FROM ticket WHERE project_id = $1 AND seq = 1`,
			projectID).Scan(&mode); err != nil {
			t.Fatalf("チケットを引けない: %v", err)
		}
		if mode == "agent_only" {
			t.Error("403 なのに execution_mode が変わっている")
		}
	})

	t.Run("エージェントのスコープでは種別を変えられない", func(t *testing.T) {
		res := patchAsAgent(t, `{"type":"epic"}`)
		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", res.Code, res.Body.String())
		}
	})

	// **MCP のツールはそもそもこれらの引数を宣言していない**（Design.md 8.5.1）。
	// 送っても落ちるだけで、**空の更新として JSON-RPC の invalid params になる**
	// ——ツールの失敗（isError）ではないので、生の応答で見る。
	t.Run("pb_update_ticket は縛る側の引数を受け取らない", func(t *testing.T) {
		w := call(agentToken, projectKey,
			`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"pb_update_ticket",`+
				`"arguments":{"seq":1,"execution_mode":"agent_only"}}}`)
		var res struct {
			Error *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, w.Body.String())
		}
		if res.Error == nil {
			t.Fatalf("引数が落ちて空の更新になるはずが、成功した: %s", w.Body.String())
		}
		if !strings.Contains(res.Error.Message, "直す項目") {
			t.Errorf("空の更新として断られていない: %+v", res.Error)
		}
	})

	// **人（ticket.edit）は従来どおり変えられる。** self_edit は部分集合で
	// あって、画面の振る舞いを変えるものではない。
	t.Run("スコープを絞らないトークンでは実行モードも変えられる", func(t *testing.T) {
		var cur struct {
			Version int64 `json:"version"`
		}
		get := httptest.NewRequest(http.MethodGet,
			"/api/v1/projects/"+projectKey+"/tickets/1", nil)
		get.Header.Set("Authorization", "Bearer "+fullToken)
		gw := httptest.NewRecorder()
		r.ServeHTTP(gw, get)
		if err := json.Unmarshal(gw.Body.Bytes(), &cur); err != nil {
			t.Fatalf("version を読めない: %v（%s）", err, gw.Body.String())
		}

		req := httptest.NewRequest(http.MethodPatch,
			"/api/v1/projects/"+projectKey+"/tickets/1",
			strings.NewReader(`{"execution_mode":"agent_draft"}`))
		req.Header.Set("Authorization", "Bearer "+fullToken)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("If-Match", `"`+strconv.FormatInt(cur.Version, 10)+`"`)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", w.Code, w.Body.String())
		}
	})

	t.Run("pb_put_dod が完了条件を足して直して消す", func(t *testing.T) {
		// **始点は0件ではない**（この準備が seq=1 に DoD を2件入れている）。
		// **全置換ではなく差分なので、既存の条件はそのまま残る**——それを
		// 確かめるために、足す前の件数を採る。
		var before int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM dod_item d JOIN ticket t ON t.id = d.ticket_id
			  WHERE t.project_id = $1 AND t.seq = 1`, projectID).Scan(&before); err != nil {
			t.Fatalf("完了条件の件数を読めない: %v", err)
		}

		// ① 足す
		text, isErr := tool(t, agentToken, "pb_put_dod",
			`{"seq":1,"add":[{"body":"試験が通ること"},{"body":"消される条件"}]}`)
		if isErr {
			t.Fatalf("足せない: %s", text)
		}
		var list struct {
			Items []struct {
				ID          string `json:"id"`
				Body        string `json:"body"`
				IsSatisfied bool   `json:"is_satisfied"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(text), &list); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if len(list.Items) != before+2 {
			t.Fatalf("完了条件 = %d件, want %d件（既存 + 2）: %s",
				len(list.Items), before+2, text)
		}

		// ② 直して消す（**同じ呼び出しでまとめて送れる**）。
		// **id は body で引く**——並び順に依存すると、既存の条件が混ざったときに
		// 別のものを掴む。
		byBody := map[string]string{}
		for _, it := range list.Items {
			byBody[it.Body] = it.ID
		}
		keep, drop := byBody["試験が通ること"], byBody["消される条件"]
		if keep == "" || drop == "" {
			t.Fatalf("足したはずの条件が見つからない: %s", text)
		}
		text, isErr = tool(t, agentToken, "pb_put_dod",
			`{"seq":1,"update":[{"id":`+quote(keep)+`,"body":"試験と画面の確認が通ること"}],`+
				`"delete":[`+quote(drop)+`]}`)
		if isErr {
			t.Fatalf("直せない: %s", text)
		}
		if err := json.Unmarshal([]byte(text), &list); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if len(list.Items) != before+1 {
			t.Fatalf("完了条件 = %d件, want %d件（1件消したので）: %s",
				len(list.Items), before+1, text)
		}
		// **触っていない既存の条件は残っている**（差分であって全置換ではない）。
		bodies := map[string]bool{}
		for _, it := range list.Items {
			bodies[it.Body] = true
		}
		if !bodies["試験と画面の確認が通ること"] {
			t.Errorf("直した条件が無い: %s", text)
		}
		if bodies["消される条件"] {
			t.Errorf("消したはずの条件が残っている: %s", text)
		}
		if before > 0 && !bodies["ユニットテストが通ること"] {
			t.Errorf("触っていない既存の条件が消えている（全置換になっている）: %s", text)
		}
	})

	// **チェックは付けられない**（9.9。0029）。完了の判定は人が行う。
	t.Run("pb_put_dod ではチェックを付けられない", func(t *testing.T) {
		// **ツールの引数に is_satisfied が無い**ので、そもそも渡せない。
		// ここで測るのは REST 側の守り——MCP の引数定義が変わっても、
		// 権限のほうが最後の砦であることを見る。
		req := httptest.NewRequest(http.MethodPost,
			"/api/v1/projects/"+projectKey+"/tickets/1/dod",
			strings.NewReader(`{"body":"満たしたことにする","is_satisfied":true}`))
		req.Header.Set("Authorization", "Bearer "+agentToken)
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		r.ServeHTTP(res, req)

		if res.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", res.Code, res.Body.String())
		}
	})

	t.Run("pb_create_ticket が見積もりと日付とタグを受ける", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `
			UPDATE project_counter SET last_ticket_seq =
				(SELECT coalesce(max(seq), 0) FROM ticket WHERE project_id = $1)
			WHERE project_id = $1`, projectID); err != nil {
			t.Fatalf("採番器を合わせられない: %v", err)
		}
		var tagID string
		if err := pool.QueryRow(ctx,
			`SELECT id FROM tag WHERE project_id = $1 ORDER BY sort_order LIMIT 1`,
			projectID).Scan(&tagID); err != nil {
			t.Fatalf("タグを引けない: %v", err)
		}

		text, isErr := tool(t, agentToken, "pb_create_ticket",
			`{"type":"task","title":"見積もりつきで起票","estimate_point":5,`+
				`"start_date":"2026-09-09","due_date":"2026-09-21","tag_ids":[`+quote(tagID)+`]}`)
		if isErr {
			t.Fatalf("起票できない: %s", text)
		}
		var got struct {
			Seq           int      `json:"seq"`
			EstimatePoint *float64 `json:"estimate_point"`
			StartDate     *string  `json:"start_date"`
			DueDate       *string  `json:"due_date"`
			Tags          []struct {
				ID string `json:"id"`
			} `json:"tags"`
		}
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("応答を読めない: %v（%s）", err, text)
		}
		if got.EstimatePoint == nil || *got.EstimatePoint != 5 {
			t.Errorf("estimate_point = %v, want 5", got.EstimatePoint)
		}
		// **date 列は時刻を持たない**（前日へずれる経路を作らない）。
		if got.StartDate == nil || *got.StartDate != "2026-09-09" {
			t.Errorf("start_date = %v", got.StartDate)
		}
		if got.DueDate == nil || *got.DueDate != "2026-09-21" {
			t.Errorf("due_date = %v", got.DueDate)
		}
		if len(got.Tags) != 1 || got.Tags[0].ID != tagID {
			t.Errorf("tags = %+v, want [%s]", got.Tags, tagID)
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
