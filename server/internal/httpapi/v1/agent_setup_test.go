package v1

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// エージェント連携セットアップ（ApiDesign.md 5.7）の単体テスト。
//
// 認可（agent.register）はミドルウェアの責務なのでここでは通さない
// （routes_test.go が宣言を見ている）。ここで確かめるのは、`client` の検証・
// 応答の形・base_url の組み立て・zip の中身である。
//
// **配置ファイルの中身そのものは internal/agentsetup のテストが見る。**
// 二重に持つと、テンプレートを直すたびに2か所を直すことになる。

// setupFake はプロジェクト demo が解決でき、3種別のうち2つが
// テンプレートを持つ状態のフェイクを返す。
//
// **gemini を「テンプレート無し」として混ぜてある。** 絞り込みが効いているかは、
// 落ちる側の値が1つ無いと確かめられない。
func setupFake() *fakeQuerier {
	return &fakeQuerier{
		projectIDByKey: map[string]string{"demo": testProjectID},
		detailRow: gen.GetProjectByKeyRow{
			ID: testProjectID, Key: "demo", Name: "デモ",
		},
		agentClientKinds: []gen.ListAgentClientKindsRow{
			{Key: "claude_code", DisplayName: "Claude Code", HasSetupTemplate: true},
			{Key: "codex", DisplayName: "OpenAI Codex", HasSetupTemplate: true},
			{Key: "gemini", DisplayName: "Gemini", HasSetupTemplate: false},
		},
	}
}

// setupReq は /projects/{key}/agent-setup 系のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを
// 通らず、chi.URLParam が空を返してしまうためである（tags_test と同じ）。
func setupReq(target string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add(middleware.ProjectKeyURLParam, "demo")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

func decodeSetup(t *testing.T, rec *httptest.ResponseRecorder) agentSetupView {
	t.Helper()
	var v agentSetupView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// TestGetAgentSetupReturnsCommittedFiles は 200 の形を確かめる。
func TestGetAgentSetupReturnsCommittedFiles(t *testing.T) {
	h := &handler{q: setupFake()}
	rec := httptest.NewRecorder()
	h.getAgentSetup(rec, setupReq("/api/v1/projects/demo/agent-setup?client=claude_code"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := decodeSetup(t, rec)
	if v.Project.Key != "demo" || v.Project.Name != "デモ" {
		t.Errorf("project: got %+v", v.Project)
	}
	if v.WorkflowVersion != 1 {
		t.Errorf("workflow_version: got %d, want 1", v.WorkflowVersion)
	}
	if len(v.Clients) != 1 || v.Clients[0] != "claude_code" {
		t.Errorf("clients: got %v", v.Clients)
	}
	if len(v.Files) == 0 {
		t.Fatal("files が空")
	}

	// **接続設定を返さない**（ApiDesign.md 5.7。履歴管理の対象外）。
	byPath := map[string]agentSetupFileView{}
	for _, f := range v.Files {
		byPath[f.Path] = f
	}
	if _, ok := byPath[".mcp.json"]; ok {
		t.Error(".mcp.json が応答に含まれている。系統A は接続設定を出さない")
	}
	if _, ok := byPath[".claude/commands/pb-onboard.md"]; !ok {
		t.Errorf("手順ファイルが無い: %v", byPath)
	}

	// **mode が付いていること**が本節の要点である（5.7.1）。
	if got := byPath["CLAUDE.md"].Mode; got != "append" {
		t.Errorf("CLAUDE.md の mode: got %q, want append", got)
	}
	if got := byPath["CLAUDE.md"].MarkerBegin; got == "" {
		t.Error("追記のファイルに marker_begin が無い")
	}
	if got := byPath[".claude/settings.json"].Mode; got != "merge" {
		t.Errorf(".claude/settings.json の mode: got %q, want merge", got)
	}
	if got := byPath[".gitignore"].ClientKind; got != "" {
		t.Errorf(".gitignore の client_kind: got %q, want 空（共通）", got)
	}
}

// TestGetAgentSetupBaseURL は base_url の組み立てを確かめる（5.7.1）。
//
// **PB は自分の公開 URL を知らない。** Host ヘッダから組むのが暫定の答えで、
// スキームは cookieSecure を見る（プロキシの背後では r.TLS が nil になるため）。
func TestGetAgentSetupBaseURL(t *testing.T) {
	for _, tc := range []struct {
		name         string
		host         string
		cookieSecure bool
		want         string
	}{
		{"dev", "127.0.0.1:8080", false, "http://127.0.0.1:8080"},
		{"stg", "localhost:8081", false, "http://localhost:8081"},
		{"TLS 終端の背後", "pb.example.com", true, "https://pb.example.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &handler{q: setupFake(),
				settings: config.LiveWith(config.Row{Key: config.KeyCookieSecure, Value: strconv.FormatBool(tc.cookieSecure)})}
			req := setupReq("/api/v1/projects/demo/agent-setup?client=claude_code")
			req.Host = tc.host
			rec := httptest.NewRecorder()
			h.getAgentSetup(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d (%s)", rec.Code, rec.Body.String())
			}
			if got := decodeSetup(t, rec).BaseURL; got != tc.want {
				t.Errorf("base_url: got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestGetAgentSetupClientParam は ?client= の検証を確かめる（5.7.1）。
func TestGetAgentSetupClientParam(t *testing.T) {
	for _, tc := range []struct {
		name   string
		query  string
		status int
		want   []string
	}{
		{"指定なしは 422", "", http.StatusUnprocessableEntity, nil},
		{"テンプレート無しの種別は 422", "?client=gemini", http.StatusUnprocessableEntity, nil},
		{"未知の種別は 422", "?client=zed", http.StatusUnprocessableEntity, nil},
		{"1件", "?client=codex", http.StatusOK, []string{"codex"}},
		// **重複は畳む。**
		{"重複", "?client=codex&client=codex", http.StatusOK, []string{"codex"}},
		// **並びはカタログの順（sort_order）に揃える。** 画面がチェックの順で
		// 送ってきても、応答と生成物の並びが揺れないようにするため。
		{"並べ替え", "?client=codex&client=claude_code", http.StatusOK,
			[]string{"claude_code", "codex"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &handler{q: setupFake()}
			rec := httptest.NewRecorder()
			h.getAgentSetup(rec, setupReq("/api/v1/projects/demo/agent-setup"+tc.query))

			if rec.Code != tc.status {
				t.Fatalf("status: got %d, want %d (%s)", rec.Code, tc.status, rec.Body.String())
			}
			if tc.status != http.StatusOK {
				return
			}
			got := decodeSetup(t, rec).Clients
			if len(got) != len(tc.want) {
				t.Fatalf("clients: got %v, want %v", got, tc.want)
			}
			for i := range tc.want {
				if got[i] != tc.want[i] {
					t.Errorf("clients[%d]: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestGetAgentSetupZip は zip の応答を確かめる（5.7.2）。
func TestGetAgentSetupZip(t *testing.T) {
	h := &handler{q: setupFake()}
	rec := httptest.NewRecorder()
	h.getAgentSetupZip(rec, setupReq("/api/v1/projects/demo/agent-setup.zip?client=claude_code"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type: got %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="pb-setup-demo.zip"` {
		t.Errorf("Content-Disposition: got %q", got)
	}

	blob := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatalf("zip を開けない: %v", err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	// **追記のものは別名で入る**——展開した瞬間に既存の CLAUDE.md を消さない。
	if !names["CLAUDE.pb-block.md"] {
		t.Errorf("CLAUDE.pb-block.md が無い: %v", names)
	}
	if names["CLAUDE.md"] {
		t.Error("CLAUDE.md がそのままの名前で入っている（展開で既存を潰す）")
	}
	if !names[".claude/commands/pb-onboard.md"] {
		t.Errorf("手順ファイルが無い: %v", names)
	}
}

// TestGetAgentSetupUnknownProject は到達できないプロジェクトを 404 に倒す。
//
// **Design.md 6.4.5「存在を隠す」。** 実運用では RequireProjectPermission が
// 先に落とすが、ハンドラ単体でも同じ結果になることを確かめる。
func TestGetAgentSetupUnknownProject(t *testing.T) {
	q := setupFake()
	q.projectIDByKey = map[string]string{} // demo を解決できなくする
	h := &handler{q: q}
	rec := httptest.NewRecorder()
	h.getAgentSetup(rec, setupReq("/api/v1/projects/demo/agent-setup?client=claude_code"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status: got %d, want 404 (%s)", rec.Code, rec.Body.String())
	}
}
