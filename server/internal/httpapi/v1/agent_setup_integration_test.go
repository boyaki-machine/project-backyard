package v1

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// エージェント連携セットアップ（ApiDesign.md 5.7）を**実際のDBに対して**通す。
//
// **ここでしか確かめられないことが2つある。**
//
//   - **必要権限が agent.register であること。** 0010 が配っているのは
//     project_admin だけなので、**project_member は 403 になる**。ハンドラ単体では
//     ミドルウェアを通さないため見えない
//   - **0023 が has_setup_template を正しく立てたこと。** フェイクは自分で並べた
//     値を返すので、**マイグレーションが実際に流れたかは実DBでしか分からない**
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db）。
func TestAgentSetupIntegration(t *testing.T) {
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

	// 管理者（project_admin）・一般メンバー（project_member）・非メンバーの3人。
	//
	// **どちらも operator にしてある。** システムロールで差を付けると、
	// 403 の原因がプロジェクトロールなのかシステムロールなのか切り分けられない。
	adminID := ulidgen.New()
	adminEmail := "setup-admin-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleOperator)

	memberID := ulidgen.New()
	memberEmail := "setup-member-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, memberID, memberEmail, auth.SystemRoleOperator)

	outsiderID := ulidgen.New()
	outsiderEmail := "setup-out-" + uniq + "@example.com"
	seedUserWithRole(t, ctx, pool, q, outsiderID, outsiderEmail, auth.SystemRoleOperator)

	projectID := ulidgen.New()
	projectKey := "setup-" + suffix
	if err := q.CreateProject(ctx, gen.CreateProjectParams{
		ID: projectID, Key: projectKey, Name: "セットアップ結合テスト",
	}); err != nil {
		t.Fatalf("プロジェクトを作れない: %v", err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		if _, err := pool.Exec(bg, `DELETE FROM project WHERE id = $1`, projectID); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	for _, m := range []struct {
		actorID string
		role    string
	}{{adminID, "project_admin"}, {memberID, "project_member"}} {
		if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
			ProjectID: projectID, ActorID: m.actorID, RoleKey: m.role,
		}); err != nil {
			t.Fatalf("メンバー(%s)を追加できない: %v", m.role, err)
		}
	}

	adminSession := loginAs(t, r, adminEmail)
	memberSession := loginAs(t, r, memberEmail)
	outsiderSession := loginAs(t, r, outsiderEmail)

	base := "/api/v1/projects/" + projectKey + "/agent-setup"

	t.Run("project_admin は 200 で配置ファイルを受け取れる", func(t *testing.T) {
		rec := getWithCookie(r, base+"?client=claude_code&client=codex", adminSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		var got struct {
			Project struct {
				Key string `json:"key"`
			} `json:"project"`
			BaseURL         string `json:"base_url"`
			WorkflowVersion int    `json:"workflow_version"`
			Clients         []string
			Files           []struct {
				Path       string `json:"path"`
				ClientKind string `json:"client_kind"`
				Mode       string `json:"mode"`
				Content    string `json:"content"`
			} `json:"files"`
		}
		decodeJSONBody(t, rec, &got)

		if got.Project.Key != projectKey {
			t.Errorf("project.key = %q, want %q", got.Project.Key, projectKey)
		}
		if got.WorkflowVersion != 1 {
			t.Errorf("workflow_version = %d, want 1", got.WorkflowVersion)
		}
		if !strings.HasPrefix(got.BaseURL, "http://") {
			t.Errorf("base_url = %q, want http:// で始まる", got.BaseURL)
		}

		paths := map[string]string{}
		for _, f := range got.Files {
			paths[f.Path] = f.Mode
		}
		// **2種別ぶんの手順と常時コンテキストが並ぶ。**
		for _, p := range []string{
			".claude/commands/pb-onboard.md",
			".agents/skills/pb-onboard/SKILL.md",
			"CLAUDE.md", "AGENTS.md", ".gitignore",
		} {
			if _, ok := paths[p]; !ok {
				t.Errorf("%q が無い（%v）", p, paths)
			}
		}
		// **接続設定は出さない**（Requirements.md 10.8.1）。
		for _, p := range []string{".mcp.json", ".codex/config.toml", ".vscode/mcp.json"} {
			if _, ok := paths[p]; ok {
				t.Errorf("接続設定 %q が含まれている。系統A は出さない", p)
			}
		}
		// **選ばなかった種別のものは出ない。**
		if _, ok := paths[".github/prompts/pb-onboard.prompt.md"]; ok {
			t.Error("選んでいない copilot のファイルが含まれている")
		}
		// **プロジェクトキーが差し込まれている。**
		for _, f := range got.Files {
			if f.Path == "CLAUDE.md" && !strings.Contains(f.Content, "`"+projectKey+"`") {
				t.Errorf("CLAUDE.md にプロジェクトキーが入っていない:\n%s", f.Content)
			}
		}
	})

	// **これが 0023 の検証である。** gemini は行として在るが
	// has_setup_template が偽なので、実DBを引いた結果として 422 になる。
	t.Run("テンプレートを持たない種別は 422", func(t *testing.T) {
		rec := getWithCookie(r, base+"?client=gemini", adminSession)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("client を指定しなければ 422", func(t *testing.T) {
		rec := getWithCookie(r, base, adminSession)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// **必要権限が agent.register であることの証拠。** project_member は
	// project.view を持つのでプロジェクトには到達できるが、この口は 403 になる。
	t.Run("project_member は 403", func(t *testing.T) {
		rec := getWithCookie(r, base+"?client=claude_code", memberSession)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})

	// **非メンバーは 404**（Design.md 6.4.5「存在を隠す」）。
	t.Run("非メンバーは 404", func(t *testing.T) {
		rec := getWithCookie(r, base+"?client=claude_code", outsiderSession)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("zip が落とせる", func(t *testing.T) {
		rec := getWithCookie(r, base+".zip?client=claude_code", adminSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "application/zip" {
			t.Errorf("Content-Type = %q", got)
		}
		blob := rec.Body.Bytes()
		zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
		if err != nil {
			t.Fatalf("zip を開けない: %v", err)
		}
		if len(zr.File) == 0 {
			t.Fatal("zip が空")
		}
	})

	t.Run("zip も権限を通る", func(t *testing.T) {
		rec := getWithCookie(r, base+".zip?client=claude_code", memberSession)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
	})
}
