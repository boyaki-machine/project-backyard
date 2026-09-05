package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 自分のエージェント（ApiDesign.md 4.5）を**実際のDBに対して**通す。
//
// **ここでしか確かめられないのが委譲である**（Design.md 6.5）。ハンドラ単体は
// 「所有者の ID を渡した」ところまでしか見えず、**その ID で本当に所有者の
// ロールが引かれ、実効権限になるか**は認証・認可・DBを通さないと分からない。
//
// 測るのは次の7点。
//
//   - 発行した平文が Bearer 認証を通り、**所有者のプロジェクトが GET /me に出る**
//   - **doc.view は通り doc.edit は 403**（既定スコープが効いている）
//   - **所有者を無効化すると、そのエージェントのトークンが 401 になる**
//   - 他人のエージェントは 404（WHERE の owner_actor_id が効いているか）
//   - 再発行で前のトークンが失効する（有効なトークンは1本）
//   - 無効化するとトークンも失効する
//   - GET /admin/users にエージェントが所有者つきで出る
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db）。
func TestMeAgentsIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	uniq := strings.ToLower(ulidgen.New())
	suffix := strings.ToLower(uniq[len(uniq)-6:])

	// ── 材料を作る ───────────────────────────────────────────
	//
	// 所有者は operator（システムロール）＋ project_admin（プロジェクトロール）。
	// **project_admin にするのは doc.view を持たせるため**——8.1.4 により
	// operator も project_member も doc.view / doc.edit を持たない。
	ownerID := ulidgen.New()
	ownerEmail := "agt-owner-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, ownerID, ownerEmail, auth.SystemRoleOperator)

	otherID := ulidgen.New()
	otherEmail := "agt-other-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, otherID, otherEmail, auth.SystemRoleOperator)

	adminID := ulidgen.New()
	adminEmail := "agt-admin-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)

	projectID := ulidgen.New()
	projectKey := "agt-" + suffix
	if err := q.CreateProject(ctx, gen.CreateProjectParams{
		ID: projectID, Key: projectKey, Name: "エージェント結合テスト",
	}); err != nil {
		t.Fatalf("プロジェクトを作れない: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE id = $1`, projectID); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
		ProjectID: projectID, ActorID: ownerID, RoleKey: "project_admin",
	}); err != nil {
		t.Fatalf("メンバーを追加できない: %v", err)
	}

	// **エージェントの actor は project の CASCADE では消えない**ので、
	// 明示的に後始末する。作られた ID を集めておく。
	var createdAgents []string
	t.Cleanup(func() {
		bg := context.Background()
		for _, id := range createdAgents {
			if _, err := pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = $1`, id); err != nil {
				t.Errorf("エージェントの監査ログを消せない: %v", err)
			}
			if _, err := pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, id); err != nil {
				t.Errorf("エージェントの後始末に失敗した: %v", err)
			}
		}
	})

	ownerSession := loginAs(t, r, ownerEmail)

	// register はエージェントを1件作り、その ID を返す。
	register := func(t *testing.T, session, name, key, kind string) agentJSON {
		t.Helper()
		body := `{"display_name":"` + name + `","project_key":"` + key +
			`","client_kind":"` + kind + `"}`
		rec := postWithCookie(r, "/api/v1/me/agents", session, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("登録の status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		return got
	}

	// issueToken は 4.5.3 を叩く。
	issueToken := func(t *testing.T, session, agentID, body string) *httptest.ResponseRecorder {
		t.Helper()
		return postWithCookie(r, "/api/v1/me/agents/"+agentID+"/tokens", session, body)
	}

	// ── ①委譲そのもの（Design.md 6.5）───────────────────────

	t.Run("発行した平文で認証が通り、所有者のプロジェクトが実効権限になる", func(t *testing.T) {
		ag := register(t, ownerSession, "委譲の検証", projectKey, "claude_code")
		if ag.Token != nil {
			t.Fatalf("登録直後の token = %+v, want null（4.5.2）", ag.Token)
		}

		rec := issueToken(t, ownerSession, ag.ID, `{"expires_in_days":30}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var issued issuedAgentTokenJSON
		decodeJSONBody(t, rec, &issued)
		if !strings.HasPrefix(issued.Token, auth.AgentTokenPrefix) {
			t.Fatalf("token = %q, want %s… で始まる", issued.Token, auth.AgentTokenPrefix)
		}

		// **これが委譲の証拠である。** エージェントは project_member の行を
		// 1つも持たないのに、GET /me が所有者のプロジェクトを返す。
		me := bearerGet(r, "/api/v1/me", issued.Token)
		if me.Code != http.StatusOK {
			t.Fatalf("Bearer での GET /me status = %d, want 200（body=%s）",
				me.Code, me.Body.String())
		}
		view := viewOf(t, me)

		actor, _ := view["actor"].(map[string]any)
		if actor["kind"] != "agent" {
			t.Errorf("actor.kind = %v, want agent（借用していないこと）", actor["kind"])
		}
		if actor["id"] != ag.ID {
			t.Errorf("actor.id = %v, want エージェント自身 %q", actor["id"], ag.ID)
		}
		// **エージェント自身は app_user を持たないので system_role は null。**
		if actor["system_role"] != nil {
			t.Errorf("actor.system_role = %v, want null", actor["system_role"])
		}

		projects, _ := view["projects"].([]any)
		if len(projects) != 1 {
			t.Fatalf("projects = %d件, want 1（所有者の所属が出ること）: %v", len(projects), projects)
		}
		p0, _ := projects[0].(map[string]any)
		if p0["key"] != projectKey {
			t.Errorf("projects[0].key = %v, want %q", p0["key"], projectKey)
		}

		// **実効権限は所有者の権限 ∩ 既定スコープ。**
		perms, _ := p0["permissions"].([]any)
		has := func(k string) bool {
			for _, p := range perms {
				if p == k {
					return true
				}
			}
			return false
		}
		if !has("doc.view") {
			t.Errorf("doc.view が無い（所有者は project_admin で持つはず）: %v", perms)
		}
		if !has("ticket.view") {
			t.Errorf("ticket.view が無い: %v", perms)
		}
		// **既定スコープに無いものは、所有者が持っていても付かない**（縮小のみ）。
		if has("doc.edit") {
			t.Errorf("doc.edit が付いている（既定スコープに無い。6.5 の禁止）: %v", perms)
		}
		if has("ticket.close") {
			t.Errorf("ticket.close が付いている（6.5 の禁止）: %v", perms)
		}

		// ── ②権限の正負を実際のエンドポイントで測る ──────────
		//
		// **/me の permissions と実際の応答が一致するかは別の層である。**
		docs := bearerGet(r, "/api/v1/projects/"+projectKey+"/docs", issued.Token)
		if docs.Code != http.StatusOK {
			t.Errorf("GET docs status = %d, want 200（doc.view を持つ）: %s",
				docs.Code, docs.Body.String())
		}
		// doc.edit は既定スコープに無いので書けない。
		mkDoc := bearerPost(r, "/api/v1/projects/"+projectKey+"/docs", issued.Token,
			`{"slug":"agent-should-not-write","title":"書けないはず"}`)
		if mkDoc.Code != http.StatusForbidden {
			t.Errorf("POST docs status = %d, want 403（doc.edit を持たない）: %s",
				mkDoc.Code, mkDoc.Body.String())
		}

		// ── ③所有者を無効化すると止まる（Design.md 6.5）────────
		if _, err := pool.Exec(ctx,
			`UPDATE actor SET is_active = false WHERE id = $1`, ownerID); err != nil {
			t.Fatalf("所有者を無効化できない: %v", err)
		}
		after := bearerGet(r, "/api/v1/me", issued.Token)
		if after.Code != http.StatusUnauthorized {
			t.Errorf("所有者が無効なときの status = %d, want 401（body=%s）",
				after.Code, after.Body.String())
		}
		// 戻す。**次の副検査が同じ所有者を使うため、ここで必ず戻す。**
		if _, err := pool.Exec(ctx,
			`UPDATE actor SET is_active = true WHERE id = $1`, ownerID); err != nil {
			t.Fatalf("所有者を戻せない: %v", err)
		}
		if back := bearerGet(r, "/api/v1/me", issued.Token); back.Code != http.StatusOK {
			t.Fatalf("所有者を戻したのに status = %d, want 200", back.Code)
		}
	})

	// ── ④再発行は前を失効させる（4.5.3）─────────────────────

	t.Run("再発行すると前のトークンが401になる", func(t *testing.T) {
		ag := register(t, ownerSession, "再発行の検証", projectKey, "copilot")

		var first, second issuedAgentTokenJSON
		decodeJSONBody(t, issueToken(t, ownerSession, ag.ID, `{"expires_in_days":30}`), &first)
		decodeJSONBody(t, issueToken(t, ownerSession, ag.ID, `{"expires_in_days":30}`), &second)
		if first.Token == second.Token {
			t.Fatal("同じ平文が2度返っている")
		}

		if old := bearerGet(r, "/api/v1/me", first.Token); old.Code != http.StatusUnauthorized {
			t.Errorf("古いトークンの status = %d, want 401", old.Code)
		}
		if now := bearerGet(r, "/api/v1/me", second.Token); now.Code != http.StatusOK {
			t.Errorf("新しいトークンの status = %d, want 200", now.Code)
		}

		// **一覧に出る有効なトークンは1本**（4.5.1 の LATERAL）。
		items := listAgents(t, r, ownerSession)
		for _, it := range items {
			if it.ID != ag.ID {
				continue
			}
			if it.Token == nil {
				t.Fatal("token が null。有効な1本が出るはず")
			}
			if it.Token.ID != second.ID {
				t.Errorf("一覧の token.id = %q, want 新しいほう %q", it.Token.ID, second.ID)
			}
		}
		// DB でも数える（応答の形ではなく実データで確かめる）。
		active := scalarInt(t, pool,
			`SELECT count(*) FROM access_token
			  WHERE actor_id = $1 AND token_type = 'agent' AND revoked_at IS NULL`, ag.ID)
		if active != 1 {
			t.Errorf("有効なトークン = %d本, want 1", active)
		}
	})

	// ── ⑤無効化するとトークンも失効する（4.5.4）─────────────

	t.Run("無効化するとトークンが失効し401になる", func(t *testing.T) {
		ag := register(t, ownerSession, "無効化の検証", projectKey, "other")
		var tok issuedAgentTokenJSON
		decodeJSONBody(t, issueToken(t, ownerSession, ag.ID, `{"expires_in_days":30}`), &tok)
		if ok := bearerGet(r, "/api/v1/me", tok.Token); ok.Code != http.StatusOK {
			t.Fatalf("発行直後の status = %d, want 200", ok.Code)
		}

		off := bodyWithCookie(r, http.MethodPatch, "/api/v1/me/agents/"+ag.ID,
			ownerSession, `{"is_active":false}`, "")
		if off.Code != http.StatusOK {
			t.Fatalf("無効化の status = %d, want 200（body=%s）", off.Code, off.Body.String())
		}
		if after := bearerGet(r, "/api/v1/me", tok.Token); after.Code != http.StatusUnauthorized {
			t.Errorf("無効化後の status = %d, want 401", after.Code)
		}

		// 無効なエージェントには発行できない（409）。
		again := issueToken(t, ownerSession, ag.ID, `{"expires_in_days":30}`)
		if again.Code != http.StatusConflict {
			t.Errorf("無効なエージェントへの発行 status = %d, want 409（body=%s）",
				again.Code, again.Body.String())
		}
	})

	// ── ⑥他人のエージェントは 404（Design.md 6.4.5）───────────

	t.Run("他人のエージェントは404で、一覧にも出ない", func(t *testing.T) {
		ag := register(t, ownerSession, "他人から見えないこと", projectKey, "claude_code")
		otherSession := loginAs(t, r, otherEmail)

		patch := bodyWithCookie(r, http.MethodPatch, "/api/v1/me/agents/"+ag.ID,
			otherSession, `{"display_name":"乗っ取り"}`, "")
		if patch.Code != http.StatusNotFound {
			t.Errorf("他人の PATCH status = %d, want 404（403 にしない）", patch.Code)
		}
		issue := issueToken(t, otherSession, ag.ID, `{"expires_in_days":30}`)
		if issue.Code != http.StatusNotFound {
			t.Errorf("他人の発行 status = %d, want 404", issue.Code)
		}
		for _, it := range listAgents(t, r, otherSession) {
			if it.ID == ag.ID {
				t.Error("他人のエージェントが一覧に出ている")
			}
		}
		// 名前が変わっていないこと（404 を返しつつ書き換えていないか）。
		name := scalarText(t, pool, `SELECT display_name FROM actor WHERE id = $1`, ag.ID)
		if name != "他人から見えないこと" {
			t.Errorf("display_name = %q, 他人の PATCH で書き換わっている", name)
		}
	})

	// ── ⑦自分が入っていないプロジェクトは 422（4.5.2）─────────

	t.Run("メンバーでないプロジェクトのエージェントは作れない", func(t *testing.T) {
		otherSession := loginAs(t, r, otherEmail)
		rec := postWithCookie(r, "/api/v1/me/agents", otherSession,
			`{"display_name":"よそのPJ","project_key":"`+projectKey+
				`","client_kind":"claude_code"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if !hasDetailField(errorOf(t, rec), "project_key") {
			t.Errorf("details に project_key が無い: %s", rec.Body.String())
		}
	})

	// ── 0020：クライアント種別のカタログと変更（4.5.7 / 4.5.4）──

	t.Run("クライアント種別のカタログが sort_order の順で返る", func(t *testing.T) {
		rec := getWithCookie(r, "/api/v1/agent-client-kinds", ownerSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		view := viewOf(t, rec)
		items, _ := view["items"].([]any)
		if len(items) == 0 {
			t.Fatal("カタログが空である")
		}

		// **期待値を決め打ちしない。** DB の行が正本なので、
		// 「claude_code が先頭」「other が末尾」「display_name が空でない」だけを見る。
		// 値を足したときにテストが落ちない形にする。
		first, _ := items[0].(map[string]any)
		if first["key"] != "claude_code" {
			t.Errorf("先頭 = %v, want claude_code（sort_order 10）", first["key"])
		}
		last, _ := items[len(items)-1].(map[string]any)
		if last["key"] != "other" {
			t.Errorf("末尾 = %v, want other（sort_order 90）", last["key"])
		}
		for i, raw := range items {
			it, _ := raw.(map[string]any)
			if s, _ := it["display_name"].(string); s == "" {
				t.Errorf("items[%d](%v) の display_name が空", i, it["key"])
			}
		}
	})

	t.Run("client_kind を変えられ、値域外は422になる", func(t *testing.T) {
		ag := register(t, ownerSession, "種別を変える", projectKey, "claude_code")
		path := "/api/v1/me/agents/" + ag.ID

		rec := bodyWithCookie(r, http.MethodPatch, path, ownerSession,
			`{"client_kind":"codex"}`, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if got := viewOf(t, rec)["client_kind"]; got != "codex" {
			t.Errorf("client_kind = %v, want codex", got)
		}

		// **値域外は 422。** 正本は agent_client_kind の行で、Go 側に一覧を持たない。
		rec = bodyWithCookie(r, http.MethodPatch, path, ownerSession,
			`{"client_kind":"cursor"}`, "")
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("値域外の status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if !hasDetailField(errorOf(t, rec), "client_kind") {
			t.Errorf("details に client_kind が無い: %s", rec.Body.String())
		}

		// **変更で4つ組がぶつかると 409**（4.5.4）。同じ名前・同じ種別を先に作る。
		register(t, ownerSession, "衝突する名前", projectKey, "copilot")
		rec = bodyWithCookie(r, http.MethodPatch, path, ownerSession,
			`{"display_name":"衝突する名前","client_kind":"copilot"}`, "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("重複の status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// ── ⑧/admin/users にエージェントが所有者つきで出る（6.1）───

	// ── ⑨発行時のスコープ（ApiDesign.md 4.5.3。手順26a）─────────

	t.Run("scopes を省略すると既定8件、doc.edit を足すと9件になる", func(t *testing.T) {
		ag := register(t, ownerSession, "スコープ検証", projectKey, "claude_code")

		// 省略＝既定。
		rec := issueToken(t, ownerSession, ag.ID, `{"expires_in_days":30}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("既定の発行 status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var def issuedAgentTokenJSON
		decodeJSONBody(t, rec, &def)
		if len(def.Scopes) != len(agentDefaultScopes) {
			t.Errorf("既定の scopes = %v, want %d件", def.Scopes, len(agentDefaultScopes))
		}

		// doc.edit を足す。**所有者は project_admin なので積で消えない。**
		with := append(append([]string{}, agentDefaultScopes...), "doc.edit")
		raw, err := json.Marshal(with)
		if err != nil {
			t.Fatalf("scopes を組み立てられない: %v", err)
		}
		rec = issueToken(t, ownerSession, ag.ID,
			`{"expires_in_days":30,"scopes":`+string(raw)+`}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("doc.edit つきの発行 status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var got issuedAgentTokenJSON
		decodeJSONBody(t, rec, &got)

		// **実効権限に doc.edit が入ること**を GET /me で実測する
		// （応答の scopes は「要求どおり」でしかない。積の結果はこちら）。
		me := bearerGet(r, "/api/v1/me", got.Token)
		if me.Code != http.StatusOK {
			t.Fatalf("/me の status = %d（body=%s）", me.Code, me.Body.String())
		}
		if !strings.Contains(me.Body.String(), `"doc.edit"`) {
			t.Errorf("実効権限に doc.edit が無い: %s", me.Body.String())
		}
	})

	t.Run("許可リスト外のスコープは422で、トークンを作らない", func(t *testing.T) {
		ag := register(t, ownerSession, "許可リスト検証", projectKey, "claude_code")

		before := scalarText(t, pool,
			`SELECT count(*)::text FROM access_token WHERE actor_id = $1`, ag.ID)

		for _, sc := range []string{"user.manage", "ticket.close", "doc.delete"} {
			rec := issueToken(t, ownerSession, ag.ID,
				`{"expires_in_days":30,"scopes":["project.view","`+sc+`"]}`)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("scope=%q の status = %d, want 422（body=%s）", sc, rec.Code, rec.Body.String())
			}
		}

		after := scalarText(t, pool,
			`SELECT count(*)::text FROM access_token WHERE actor_id = $1`, ag.ID)
		if before != after {
			t.Errorf("トークンが増えている（%s → %s）", before, after)
		}
	})

	// ── ⑩削除（ApiDesign.md 4.5.4。手順26a）───────────────────

	t.Run("削除するとエージェントとトークンが消える", func(t *testing.T) {
		ag := register(t, ownerSession, "削除されるエージェント", projectKey, "claude_code")
		rec := issueToken(t, ownerSession, ag.ID, `{"expires_in_days":30}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d, want 201", rec.Code)
		}
		var tok issuedAgentTokenJSON
		decodeJSONBody(t, rec, &tok)

		// 消す前は通ること（**「効いていた資格情報が効かなくなる」を測るため**）。
		if me := bearerGet(r, "/api/v1/me", tok.Token); me.Code != http.StatusOK {
			t.Fatalf("削除前の /me = %d, want 200", me.Code)
		}

		// **実行記録を1件書かせてから消す**（手順26c）。agent_run.actor_id は
		// ON DELETE RESTRICT なので、**付け替えが漏れるとここで 500 になる。**
		runTicketID, runID := ulidgen.New(), ulidgen.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO ticket (id, project_id, seq, type, title, status_key)
			VALUES ($1, $2, 9002, 'task', 'レポートを出したチケット', 'todo')`,
			runTicketID, projectID); err != nil {
			t.Fatalf("チケットを作れない: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_run (id, ticket_id, actor_id, status, ended_at)
			VALUES ($1, $2, $3, 'completed', now())`,
			runID, runTicketID, ag.ID); err != nil {
			t.Fatalf("実行記録を作れない: %v", err)
		}
		t.Cleanup(func() {
			if _, err := pool.Exec(context.Background(),
				`DELETE FROM ticket WHERE id = $1`, runTicketID); err != nil {
				t.Errorf("チケットの後始末に失敗した: %v", err)
			}
		})

		del := deleteWithCookie(r, "/api/v1/me/agents/"+ag.ID, ownerSession)
		if del.Code != http.StatusNoContent {
			t.Fatalf("削除の status = %d, want 204（body=%s）", del.Code, del.Body.String())
		}
		// **実行記録は残り、アクターだけが付け替わる。**
		if got := scalarText(t, pool,
			`SELECT count(*)::text FROM agent_run WHERE id = $1`, runID); got != "1" {
			t.Errorf("実行記録が消えている（%s 件）", got)
		}

		// actor / agent / access_token がすべて消えていること。
		for _, c := range []struct {
			name string
			sql  string
		}{
			{"actor", `SELECT count(*)::text FROM actor WHERE id = $1`},
			{"agent", `SELECT count(*)::text FROM agent WHERE actor_id = $1`},
			{"access_token", `SELECT count(*)::text FROM access_token WHERE actor_id = $1`},
		} {
			if got := scalarText(t, pool, c.sql, ag.ID); got != "0" {
				t.Errorf("%s が %s 件残っている", c.name, got)
			}
		}
		// **平文のトークンが効かなくなること**（4.5.4 の目的そのもの）。
		if me := bearerGet(r, "/api/v1/me", tok.Token); me.Code != http.StatusUnauthorized {
			t.Errorf("削除後の /me = %d, want 401", me.Code)
		}
		// 監査は残る（audit_log.actor_id は ON DELETE SET NULL）。
		if got := scalarText(t, pool,
			`SELECT count(*)::text FROM audit_log WHERE action = 'agent.delete' AND target_id = $1`,
			ag.ID); got != "1" {
			t.Errorf("agent.delete の監査 = %s件, want 1", got)
		}
	})

	t.Run("他人のエージェントは削除できない", func(t *testing.T) {
		ag := register(t, ownerSession, "他人のもの", projectKey, "claude_code")
		otherSession := loginAs(t, r, otherEmail)

		del := deleteWithCookie(r, "/api/v1/me/agents/"+ag.ID, otherSession)
		// **403 ではなく 404**（Design.md 6.4.5）。
		if del.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404（body=%s）", del.Code, del.Body.String())
		}
		if got := scalarText(t, pool,
			`SELECT count(*)::text FROM agent WHERE actor_id = $1`, ag.ID); got != "1" {
			t.Error("他人のエージェントが消えている")
		}
	})

	t.Run("利用者を削除すると所有するエージェントも残らない", func(t *testing.T) {
		// **ApiDesign.md 6.5（手順26a で改訂）。** agent.owner_actor_id の
		// ON DELETE CASCADE が消すのは agent の行だけで、**エージェントの
		// actor / access_token / コメントは残る**（FK の向きは
		// agent.actor_id → actor）。放置すると、agent 行を失った actor が
		// FindAccessTokenByHash の LEFT JOIN agent から外れ、
		// **認証は通るが実効権限が0件のトークン**が残る。

		victimID := ulidgen.New()
		victimEmail := "agt-victim-" + strings.ToLower(victimID) + "@example.com"
		seedUserWithRole(t, ctx, pool, q, victimID, victimEmail, auth.SystemRoleOperator)
		if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
			ProjectID: projectID, ActorID: victimID, RoleKey: "project_member",
		}); err != nil {
			t.Fatalf("メンバーを追加できない: %v", err)
		}

		victimSession := loginAs(t, r, victimEmail)
		ag := register(t, victimSession, "所有者ごと消えるエージェント", projectKey, "claude_code")
		rec := issueToken(t, victimSession, ag.ID, `{"expires_in_days":30}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var tok issuedAgentTokenJSON
		decodeJSONBody(t, rec, &tok)

		// **そのエージェントにコメントを書かせる**（RESTRICT の経路を作る）。
		// API ではなく直接 INSERT でよい——ここで見るのは削除の後始末であって、
		// コメントの作成規則ではない。
		ticketID := ulidgen.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO ticket (id, project_id, seq, type, title, status_key)
			VALUES ($1, $2, 9001, 'task', 'エージェントが書き込むチケット', 'todo')`,
			ticketID, projectID); err != nil {
			t.Fatalf("チケットを作れない: %v", err)
		}
		commentID := ulidgen.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO comment (id, ticket_id, author_id, body_md, kind)
			VALUES ($1, $2, $3, 'エージェントの記録', 'caveat')`,
			commentID, ticketID, ag.ID); err != nil {
			t.Fatalf("コメントを作れない: %v", err)
		}
		// **実行記録も書かせる**（手順26c）。agent_run.actor_id も
		// NOT NULL かつ ON DELETE RESTRICT なので、**同じ付け替えが要る**
		// （DbDesign.md 8.2.4、ApiDesign.md 4.5.4）。
		runID := ulidgen.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO agent_run (id, ticket_id, actor_id, status, ended_at)
			VALUES ($1, $2, $3, 'completed', now())`,
			runID, ticketID, ag.ID); err != nil {
			t.Fatalf("実行記録を作れない: %v", err)
		}

		// ── 削除する ────────────────────────────────────
		del := callWithCookie(r, http.MethodDelete, "/api/v1/admin/users/"+victimID,
			loginAs(t, r, adminEmail))
		if del.Code != http.StatusNoContent {
			t.Fatalf("利用者の削除 status = %d, want 204（body=%s）", del.Code, del.Body.String())
		}

		// **エージェントの actor / agent / access_token が残っていないこと。**
		for _, c := range []struct{ name, sql string }{
			{"actor", `SELECT count(*)::text FROM actor WHERE id = $1`},
			{"agent", `SELECT count(*)::text FROM agent WHERE actor_id = $1`},
			{"access_token", `SELECT count(*)::text FROM access_token WHERE actor_id = $1`},
		} {
			if got := scalarText(t, pool, c.sql, ag.ID); got != "0" {
				t.Errorf("%s が %s 件残っている（孤児のアクター）", c.name, got)
			}
		}
		// **平文のトークンが効かないこと**（残っていれば 200 が返ってしまう）。
		if me := bearerGet(r, "/api/v1/me", tok.Token); me.Code != http.StatusUnauthorized {
			t.Errorf("削除後の /me = %d, want 401", me.Code)
		}

		// **コメントは残り、書き手が「削除されたエージェント」になること。**
		// kind='agent' なので、画面のアバターは角丸四角のままである
		// （GuiDesign.md 8.4.2）。
		author := scalarText(t, pool, `
			SELECT a.kind || '/' || a.display_name
			FROM comment c JOIN actor a ON a.id = c.author_id WHERE c.id = $1`, commentID)
		if author != "agent/"+deletedAgentDisplayName {
			t.Errorf("コメントの書き手 = %q, want %q", author, "agent/"+deletedAgentDisplayName)
		}

		// **実行記録も残り、アクターが「削除されたエージェント」になること**
		// （手順26c）。付け替えが漏れると、そもそも削除が RESTRICT で
		// 止まって上の 204 に届かない。
		runActor := scalarText(t, pool, `
			SELECT a.kind || '/' || a.display_name
			FROM agent_run ar JOIN actor a ON a.id = ar.actor_id WHERE ar.id = $1`, runID)
		if runActor != "agent/"+deletedAgentDisplayName {
			t.Errorf("実行記録のアクター = %q, want %q", runActor, "agent/"+deletedAgentDisplayName)
		}

		// **監査は user.delete 1行に集約する**（6.5）。エージェントごとに
		// agent.delete を並べない。
		if got := scalarText(t, pool,
			`SELECT count(*)::text FROM audit_log WHERE action = 'agent.delete' AND target_id = $1`,
			ag.ID); got != "0" {
			t.Errorf("agent.delete が %s件ある（user.delete に集約するはず）", got)
		}
		detail := scalarText(t, pool, `
			SELECT coalesce(detail->>'deleted_agents', '') FROM audit_log
			WHERE action = 'user.delete' AND target_id = $1`, victimID)
		if detail != "1" {
			t.Errorf("user.delete の deleted_agents = %q, want 1", detail)
		}

		// 後始末（チケットを消せばコメントも落ちる。付け替え先の actor は残す
		// ——他の削除でも共用される1件であり、消すと次回また作られるだけである）。
		t.Cleanup(func() {
			bg := context.Background()
			if _, err := pool.Exec(bg, `DELETE FROM ticket WHERE id = $1`, ticketID); err != nil {
				t.Errorf("チケットの後始末に失敗した: %v", err)
			}
		})
	})

	t.Run("管理者の一覧にエージェントが所有者つきで出る", func(t *testing.T) {
		ag := register(t, ownerSession, "一覧に出ること", projectKey, "claude_code")
		adminSession := loginAs(t, r, adminEmail)

		rec := getWithCookie(r, "/api/v1/admin/users?kind=agent&per_page=100", adminSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		view := viewOf(t, rec)
		items, _ := view["items"].([]any)

		var found map[string]any
		for _, raw := range items {
			it, _ := raw.(map[string]any)
			if it["id"] == ag.ID {
				found = it
				break
			}
		}
		if found == nil {
			t.Fatalf("登録したエージェント %q が一覧に無い", ag.ID)
		}
		if found["kind"] != "agent" {
			t.Errorf("kind = %v, want agent", found["kind"])
		}
		// **ここが 11.2 の約束の消化点である**（agent は null でなくなった）。
		agent, ok := found["agent"].(map[string]any)
		if !ok {
			t.Fatalf("agent = %v, want オブジェクト", found["agent"])
		}
		if agent["client_kind"] != "claude_code" {
			t.Errorf("agent.client_kind = %v", agent["client_kind"])
		}
		if agent["project_key"] != projectKey {
			t.Errorf("agent.project_key = %v, want %q", agent["project_key"], projectKey)
		}
		owner, ok := agent["owner"].(map[string]any)
		if !ok {
			t.Fatalf("agent.owner = %v, want オブジェクト", agent["owner"])
		}
		if owner["id"] != ownerID {
			t.Errorf("agent.owner.id = %v, want %q", owner["id"], ownerID)
		}
		// **エージェントは project_member の行を持たない**（委譲のため）。
		if found["project_count"] != float64(0) {
			t.Errorf("project_count = %v, want 0（自前のメンバーシップを持たない）",
				found["project_count"])
		}

		// 人間の行では agent が null のままであること（対の検査）。
		human := getWithCookie(r, "/api/v1/admin/users?kind=user&per_page=100", adminSession)
		for _, raw := range viewOf(t, human)["items"].([]any) {
			it, _ := raw.(map[string]any)
			if it["id"] == ownerID && it["agent"] != nil {
				t.Errorf("人間の行に agent が入っている: %v", it["agent"])
			}
		}
	})
}

// listAgents は GET /me/agents を叩いて items を返す。
func listAgents(t *testing.T, r http.Handler, session string) []agentJSON {
	t.Helper()
	rec := getWithCookie(r, "/api/v1/me/agents", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("一覧の status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
	}
	var got agentListJSON
	decodeJSONBody(t, rec, &got)
	return got.Items
}

// bearerPost は Bearer 認証で POST する。
//
// **CSRF を付けない。** Bearer 認証では要求されない（ApiDesign.md 2.4）ので、
// この呼び出しが 403 CSRF で落ちないこと自体が規約の確認になる。
func bearerPost(r http.Handler, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// scalarText は1つの文字列を引く（scalarInt の文字列版）。
func scalarText(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&s); err != nil {
		t.Fatalf("クエリに失敗した（%s）: %v", sql, err)
	}
	return s
}
