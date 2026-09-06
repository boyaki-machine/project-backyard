package v1

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 自分の接続設定（ApiDesign.md 4.5.8、系統B）を**実際のDBに対して**通す。
//
// **ここでしか確かめられないのは、ルータと認証を通した経路そのものである。**
// 単体は chi の RouteContext を手で載せてハンドラを直接呼ぶので、
// **ルート定義（`/me/agents/{id}/setup.zip` の `.zip` が別ルートとして刺さるか）**と
// **本人以外が 404 になること**は、ここでしか出ない。
//
// 測るのは次の5点。
//
//   - 自分のエージェントの接続設定が 200 で返り、URL とプロジェクトが実物と一致する
//   - **他人のエージェントは 404**（WHERE の owner_actor_id が効いているか）
//   - **Copilot は export_line が null**（ApiDesign.md 4.5.8.2）
//   - **Codex の設定にツールの許可が入る**（Requirements.md 10.8.4.1）
//   - zip がルータ越しに落ち、手引きと改名済みの接続設定が入っている
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db）。
func TestMeAgentSetupIntegration(t *testing.T) {
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

	ownerID := ulidgen.New()
	ownerEmail := "cnx-owner-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, ownerID, ownerEmail, auth.SystemRoleOperator)

	otherID := ulidgen.New()
	otherEmail := "cnx-other-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, otherID, otherEmail, auth.SystemRoleOperator)

	projectID := ulidgen.New()
	projectKey := "cnx-" + suffix
	if err := q.CreateProject(ctx, gen.CreateProjectParams{
		ID: projectID, Key: projectKey, Name: "接続設定の結合テスト",
	}); err != nil {
		t.Fatalf("プロジェクトを作れない: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE id = $1`, projectID); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	for _, actorID := range []string{ownerID, otherID} {
		if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
			ProjectID: projectID, ActorID: actorID, RoleKey: "project_admin",
		}); err != nil {
			t.Fatalf("メンバーを追加できない: %v", err)
		}
	}

	// **エージェントの actor は project の CASCADE では消えない**ので明示的に片付ける。
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
	otherSession := loginAs(t, r, otherEmail)

	register := func(t *testing.T, session, name, kind, envSuffix string) agentJSON {
		t.Helper()
		body := `{"display_name":"` + name + `","project_key":"` + projectKey +
			`","client_kind":"` + kind + `","token_env_suffix":"` + envSuffix + `"}`
		rec := postWithCookie(r, "/api/v1/me/agents", session, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("登録の status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		return got
	}

	fetchSetup := func(t *testing.T, session, agentID string) agentConnectView {
		t.Helper()
		rec := getWithCookie(r, "/api/v1/me/agents/"+agentID+"/setup", session)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		var v agentConnectView
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
			t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
		}
		return v
	}

	t.Run("自分の Claude Code の接続設定が返る", func(t *testing.T) {
		ag := register(t, ownerSession, "接続の検証", "claude_code", "CNXLAPTOP")
		v := fetchSetup(t, ownerSession, ag.ID)

		if v.Agent.ID != ag.ID {
			t.Errorf("agent.id = %q, want %q", v.Agent.ID, ag.ID)
		}
		if v.Agent.TokenEnvName != "PB_TOKEN_CNXLAPTOP" {
			t.Errorf("token_env_name = %q", v.Agent.TokenEnvName)
		}
		// **カタログの表示名が引けていること**（画面が対応表を持たない。4.5.7）。
		if v.Agent.ClientDisplayName != "Claude Code" {
			t.Errorf("client_display_name = %q, want Claude Code", v.Agent.ClientDisplayName)
		}
		if v.Project.Key != projectKey {
			t.Errorf("project.key = %q, want %q", v.Project.Key, projectKey)
		}
		if want := v.BaseURL + "/mcp/" + projectKey; v.MCPURL != want {
			t.Errorf("mcp_url = %q, want %q", v.MCPURL, want)
		}
		if len(v.Files) != 1 || v.Files[0].Path != ".mcp.json" {
			t.Fatalf("files = %+v, want .mcp.json 1枚", v.Files)
		}
		if v.Files[0].Mode != "merge" {
			t.Errorf("mode = %q, want merge（4.5.8.4）", v.Files[0].Mode)
		}
		// **生成物の中の URL が mcp_url と同じであること。**
		if !strings.Contains(v.Files[0].Content, v.MCPURL) {
			t.Errorf("生成物の URL が mcp_url と違う:\n%s", v.Files[0].Content)
		}
		if v.ExportLine == nil || !strings.Contains(*v.ExportLine, "PB_TOKEN_CNXLAPTOP") {
			t.Errorf("export_line = %v", v.ExportLine)
		}
	})

	t.Run("他人のエージェントは404", func(t *testing.T) {
		ag := register(t, ownerSession, "他人からは見えない", "claude_code", "CNXOTHER")

		for _, path := range []string{
			"/api/v1/me/agents/" + ag.ID + "/setup",
			"/api/v1/me/agents/" + ag.ID + "/setup.zip",
		} {
			rec := getWithCookie(r, path, otherSession)
			if rec.Code != http.StatusNotFound {
				t.Errorf("%s の status = %d, want 404（403 にすると存在が漏れる）",
					path, rec.Code)
			}
		}
	})

	t.Run("Copilot は export_line が null", func(t *testing.T) {
		ag := register(t, ownerSession, "VS Code の検証", "copilot", "CNXVSCODE")
		v := fetchSetup(t, ownerSession, ag.ID)

		if v.ExportLine != nil {
			t.Errorf("export_line = %q, want null（10.8.4）", *v.ExportLine)
		}
		if len(v.Files) != 1 || v.Files[0].Path != ".vscode/mcp.json" {
			t.Fatalf("files = %+v", v.Files)
		}
		// **環境変数名がファイルに漏れていないこと。**
		if strings.Contains(v.Files[0].Content, "PB_TOKEN_CNXVSCODE") {
			t.Errorf("Copilot の設定に環境変数名が入っている:\n%s", v.Files[0].Content)
		}
	})

	t.Run("Codex の設定にツールの許可が入る", func(t *testing.T) {
		ag := register(t, ownerSession, "Codex の検証", "codex", "CNXCODEX")
		v := fetchSetup(t, ownerSession, ag.ID)

		if len(v.Files) != 1 || v.Files[0].Path != ".codex/config.toml" {
			t.Fatalf("files = %+v", v.Files)
		}
		body := v.Files[0].Content
		// **Claude Code が .claude/settings.json（コミットする）に書くものを、
		// Codex はこのファイル（コミットしない）に書く**ので、系統B が配る。
		for _, want := range []string{
			`bearer_token_env_var = "PB_TOKEN_CNXCODEX"`,
			`default_tools_approval_mode = "prompt"`,
			"[mcp_servers.pb.tools.pb_get_context]",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("%q が無い:\n%s", want, body)
			}
		}
	})

	t.Run("zip に手引きと改名済みの接続設定が入る", func(t *testing.T) {
		ag := register(t, ownerSession, "zip の検証", "claude_code", "CNXZIP")
		rec := getWithCookie(r, "/api/v1/me/agents/"+ag.ID+"/setup.zip", ownerSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "application/zip" {
			t.Errorf("Content-Type = %q", got)
		}
		want := `attachment; filename="pb-connect-` + projectKey + `-claude_code.zip"`
		if got := rec.Header().Get("Content-Disposition"); got != want {
			t.Errorf("Content-Disposition:\n got %q\nwant %q", got, want)
		}

		blob := rec.Body.Bytes()
		zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
		if err != nil {
			t.Fatalf("zip を読めない: %v", err)
		}
		names := make([]string, 0, len(zr.File))
		for _, f := range zr.File {
			names = append(names, f.Name)
		}
		// **展開した瞬間に既存を消さない**——接続設定は改名され、手引きは実名で入る。
		if len(names) != 2 || names[0] != "PB-README.md" || names[1] != ".mcp.pb-block.json" {
			t.Fatalf("zip の中身 = %v", names)
		}
		for _, name := range names {
			if name == ".mcp.json" || name == "README.md" {
				t.Errorf("%q は既存を壊しうる名前である", name)
			}
		}
	})
}
