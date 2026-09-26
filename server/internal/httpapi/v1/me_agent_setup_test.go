package v1

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/agentsetup"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 自分の接続設定（ApiDesign.md 4.5.8、系統B）の単体テスト。
//
// **接続設定の中身そのものは internal/agentsetup のテストが見る。** ここで確かめるのは
// 応答の形・export_line の出し分け・URL の組み立て・404・zip の中身である。
// **二重に持つと、テンプレートを直すたびに2か所を直すことになる**（系統A と同じ方針）。

// connectFake は自分のエージェント1件が引ける状態のフェイクを返す。
func connectFake(kind string, envSuffix string) *fakeQuerier {
	suffix := pgtype.Text{}
	if envSuffix != "" {
		suffix = pgtype.Text{String: envSuffix, Valid: true}
	}
	return &fakeQuerier{
		agentRow: gen.FindMyAgentRow{
			ActorID:        "01K2AGENT0000000000000000",
			DisplayName:    "私の Claude Code",
			IsActive:       true,
			ClientKind:     kind,
			TokenEnvSuffix: suffix,
			ProjectID:      pgtype.Text{String: testProjectID, Valid: true},
			ProjectKey:     pgtype.Text{String: "demo", Valid: true},
			ProjectName:    pgtype.Text{String: "デモ", Valid: true},
		},
		agentClientKinds: []gen.ListAgentClientKindsRow{
			{Key: "claude_code", DisplayName: "Claude Code", HasSetupTemplate: true},
			{Key: "copilot", DisplayName: "GitHub Copilot", HasSetupTemplate: true},
			{Key: "codex", DisplayName: "OpenAI Codex", HasSetupTemplate: true},
			{Key: "gemini", DisplayName: "Gemini（CLI / Code Assist）", HasSetupTemplate: false},
		},
	}
}

// connectReq は /me/agents/{id}/setup 系のリクエストを組み立てる。
//
// **chi の RouteContext を自分で載せる**——ハンドラを直接呼ぶのでルータを通らず、
// chi.URLParam が空を返してしまうためである（agent_setup_test と同じ）。
func connectReq() *http.Request {
	req := httptest.NewRequest(http.MethodGet,
		"/api/v1/me/agents/01K2AGENT0000000000000000/setup", nil)
	req.Host = "localhost:8081"
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", "01K2AGENT0000000000000000")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rc)
	ctx = auth.NewPrincipalContext(ctx, &auth.Principal{ActorID: testActorID})
	return req.WithContext(ctx)
}

func decodeConnect(t *testing.T, rec *httptest.ResponseRecorder) agentConnectView {
	t.Helper()
	var v agentConnectView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("応答を読めない: %v (%s)", err, rec.Body.String())
	}
	return v
}

// TestGetMyAgentSetupClaudeCode は 200 の形を確かめる（ApiDesign.md 4.5.8.1）。
func TestGetMyAgentSetupClaudeCode(t *testing.T) {
	h := &handler{q: connectFake("claude_code", "MY_LAPTOP")}
	rec := httptest.NewRecorder()
	h.getMyAgentSetup(rec, connectReq())

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := decodeConnect(t, rec)

	if v.Agent.ClientDisplayName != "Claude Code" {
		t.Errorf("client_display_name: got %q（カタログから引く。4.5.7）", v.Agent.ClientDisplayName)
	}
	if v.Agent.TokenEnvName != "PB_TOKEN_MY_LAPTOP" {
		t.Errorf("token_env_name: got %q", v.Agent.TokenEnvName)
	}
	// **base_url はリクエストの Host から組み立てる**（5.7.1 と同じ規則）。
	if v.BaseURL != "http://localhost:8081" {
		t.Errorf("base_url: got %q", v.BaseURL)
	}
	if v.MCPURL != "http://localhost:8081/mcp/demo" {
		t.Errorf("mcp_url: got %q", v.MCPURL)
	}
	if v.Project.Key != "demo" {
		t.Errorf("project.key: got %q", v.Project.Key)
	}

	if len(v.Files) != 1 {
		t.Fatalf("files は1枚のはず: got %d", len(v.Files))
	}
	if v.Files[0].Path != ".mcp.json" || v.Files[0].Mode != "merge" {
		t.Errorf("files[0]: got %+v, want .mcp.json / merge（4.5.8.4）", v.Files[0])
	}
	// **応答の URL と、生成物の中に埋まった URL が同じであること。**
	// 別々に組み立てると黙ってずれる。
	if !bytes.Contains([]byte(v.Files[0].Content), []byte(v.MCPURL)) {
		t.Errorf("生成物の中の URL が mcp_url と違う:\n%s", v.Files[0].Content)
	}

	if v.ExportLine == nil {
		t.Fatal("Claude Code では export_line を返す")
	}
	if !bytes.Contains([]byte(*v.ExportLine), []byte("PB_TOKEN_MY_LAPTOP")) {
		t.Errorf("export_line: got %q", *v.ExportLine)
	}
	// **平文のトークンは返さない**（Requirements.md 10.10.1）。
	if bytes.Contains(rec.Body.Bytes(), []byte("pb_agt_")) {
		t.Errorf("応答にトークンらしき文字列がある: %s", rec.Body.String())
	}
}

func TestGetMyAgentSetupCodexBridge(t *testing.T) {
	h := &handler{q: connectFake("codex", "MY_LAPTOP")}
	req := connectReq()
	q := req.URL.Query()
	q.Set("transport", "bridge")
	q.Set("os", "darwin")
	q.Set("arch", "arm64")
	req.URL.RawQuery = q.Encode()
	rec := httptest.NewRecorder()
	h.getMyAgentSetup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := decodeConnect(t, rec)
	if v.Transport != "bridge" {
		t.Errorf("transport: got %q, want bridge", v.Transport)
	}
	if len(v.Files) != 1 || !bytes.Contains([]byte(v.Files[0].Content), []byte(`command = "pb-mcp-bridge"`)) {
		t.Fatalf("Codex bridge の stdio 設定がない: %+v", v.Files)
	}
}

func TestGetMyAgentSetupBridgePlatform(t *testing.T) {
	h := &handler{q: connectFake("codex", "MY_LAPTOP")}
	for _, tc := range []struct {
		name, transport, osName, arch string
		wantStatus                    int
		wantCommand                   string
	}{
		{"windows", "bridge", "windows", "amd64", 200, `command = "pb-mcp-bridge.exe"`},
		{"linux", "bridge", "linux", "arm64", 200, `command = "pb-mcp-bridge"`},
		{"missing os", "bridge", "", "arm64", 422, ""},
		{"missing arch", "bridge", "windows", "", 422, ""},
		{"unknown os", "bridge", "freebsd", "amd64", 422, ""},
		{"path traversal", "bridge", "../windows", "amd64", 422, ""},
		{"unknown arch", "bridge", "windows", "386", 422, ""},
		{"unknown transport", "serial", "", "", 422, ""},
		{"direct with platform", "direct", "windows", "amd64", 422, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := connectReq()
			q := req.URL.Query()
			q.Set("transport", tc.transport)
			q.Set("os", tc.osName)
			q.Set("arch", tc.arch)
			req.URL.RawQuery = q.Encode()
			rec := httptest.NewRecorder()
			h.getMyAgentSetup(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status: got %d, want %d (%s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCommand != "" {
				v := decodeConnect(t, rec)
				if !bytes.Contains([]byte(v.Files[0].Content), []byte(tc.wantCommand)) {
					t.Errorf("接続設定に %q がない", tc.wantCommand)
				}
				if tc.osName == "windows" && (v.ExportLine == nil || !bytes.Contains([]byte(*v.ExportLine), []byte("$env:"))) {
					t.Errorf("Windows の export_line が PowerShell ではない: %v", v.ExportLine)
				}
			}
		})
	}
}

func TestBridgeAssetSelectsClientPlatform(t *testing.T) {
	base := t.TempDir()
	for _, osName := range []string{"darwin", "windows", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			name := "pb-mcp-bridge"
			if osName == "windows" {
				name += ".exe"
			}
			dir := filepath.Join(base, "bridges", osName+"-"+arch)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(osName+"/"+arch), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, osName := range []string{"darwin", "windows", "linux"} {
		for _, arch := range []string{"amd64", "arm64"} {
			a, err := bridgeAssetFromDir(base, osName, arch)
			if err != nil {
				t.Fatal(err)
			}
			if string(a.Content) != osName+"/"+arch {
				t.Errorf("%s/%s の中身: %q", osName, arch, a.Content)
			}
			if osName == "windows" && a.Path != "pb-mcp-bridge.exe" {
				t.Errorf("Windows のZIP名: %q", a.Path)
			}
			connect, err := agentsetup.RenderConnect("codex", agentsetup.ConnectParams{
				Transport: agentsetup.TransportBridge, BridgeOS: osName, BridgeArch: arch,
			})
			if err != nil {
				t.Fatal(err)
			}
			connect.Assets = []agentsetup.Asset{a}
			blob, err := agentsetup.ConnectZip(connect)
			if err != nil {
				t.Fatal(err)
			}
			zr, err := zip.NewReader(bytes.NewReader(blob), int64(len(blob)))
			if err != nil {
				t.Fatal(err)
			}
			found := 0
			for _, f := range zr.File {
				if f.Name == a.Path {
					found++
					rc, err := f.Open()
					if err != nil {
						t.Fatal(err)
					}
					content, err := io.ReadAll(rc)
					if err != nil {
						t.Fatal(err)
					}
					_ = rc.Close()
					if !bytes.Equal(content, a.Content) {
						t.Errorf("ZIP の %s/%s のバイナリが違う", osName, arch)
					}
				}
			}
			if found != 1 {
				t.Errorf("ZIP に選択したバイナリが1本ではない: %s/%s", osName, arch)
			}
		}
	}
	if _, err := bridgeAssetFromDir(base, "linux", "386"); err == nil {
		t.Error("未対応のCPUを拒否しない")
	}
}

// TestGetMyAgentSetupCopilotHasNoExportLine は export_line の出し分けを確かめる
// （ApiDesign.md 4.5.8.2）。
//
// **この形式だけ環境変数を使わない**（Requirements.md 10.8.4）。
func TestGetMyAgentSetupCopilotHasNoExportLine(t *testing.T) {
	h := &handler{q: connectFake("copilot", "MY_LAPTOP")}
	rec := httptest.NewRecorder()
	h.getMyAgentSetup(rec, connectReq())

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
	}
	v := decodeConnect(t, rec)
	if v.ExportLine != nil {
		t.Errorf("Copilot では export_line は null: got %q", *v.ExportLine)
	}
	// **token_env_name は null にしない**——種別を変えれば意味を持ち直す。
	if v.Agent.TokenEnvName != "PB_TOKEN_MY_LAPTOP" {
		t.Errorf("token_env_name は値を返す: got %q", v.Agent.TokenEnvName)
	}
	if len(v.Files) != 1 || v.Files[0].Path != ".vscode/mcp.json" {
		t.Fatalf("files: got %+v", v.Files)
	}
}

// TestGetMyAgentSetupWithoutTemplate は配置ファイルを持たない種別を確かめる
// （ApiDesign.md 4.5.8.3）。
//
// **200 で files を空にする。** エージェントは既に登録されており、URL も変数名も
// 正しく決まっている——5.7.1 が 422 で拒むのは「これから選ぶ」ものだからである。
func TestGetMyAgentSetupWithoutTemplate(t *testing.T) {
	h := &handler{q: connectFake("gemini", "MY_LAPTOP")}
	rec := httptest.NewRecorder()
	h.getMyAgentSetup(rec, connectReq())

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200（エラーにしない。4.5.8.3） (%s)",
			rec.Code, rec.Body.String())
	}
	v := decodeConnect(t, rec)
	if len(v.Files) != 0 {
		t.Errorf("files は空のはず: got %+v", v.Files)
	}
	if v.MCPURL == "" || v.Agent.TokenEnvName == "" {
		t.Error("自分で設定するのに要る値（URL・変数名）は返す")
	}
	if v.Agent.ClientDisplayName != "Gemini（CLI / Code Assist）" {
		t.Errorf("client_display_name: got %q", v.Agent.ClientDisplayName)
	}
}

// TestGetMyAgentSetupFallsBackToAgentID は接尾が未設定のときの変数名を確かめる
// （ApiDesign.md 4.5.1）。
//
// **0023 より前に登録された行は token_env_suffix が NULL である。**
// **画面と生成器が各々フォールバックを計算すると、.mcp.json に書いた名前と
// export 行がずれる**ので、サーバの1か所で組み立てる。
func TestGetMyAgentSetupFallsBackToAgentID(t *testing.T) {
	h := &handler{q: connectFake("claude_code", "")}
	rec := httptest.NewRecorder()
	h.getMyAgentSetup(rec, connectReq())

	v := decodeConnect(t, rec)
	want := "PB_TOKEN_01K2AGENT0000000000000000"
	if v.Agent.TokenEnvName != want {
		t.Errorf("token_env_name: got %q, want %q", v.Agent.TokenEnvName, want)
	}
	// **生成物と export 行が同じ名前を使っていること**（ずれると「繋がらない」になる）。
	if !bytes.Contains([]byte(v.Files[0].Content), []byte(want)) {
		t.Errorf("生成物が別の変数名を使っている:\n%s", v.Files[0].Content)
	}
	if v.ExportLine == nil || !bytes.Contains([]byte(*v.ExportLine), []byte(want)) {
		t.Errorf("export_line が別の変数名を使っている: %v", v.ExportLine)
	}
}

// TestGetMyAgentSetupNotFound は他人のエージェントを確かめる（Design.md 6.4.5）。
func TestGetMyAgentSetupNotFound(t *testing.T) {
	q := connectFake("claude_code", "MY_LAPTOP")
	q.agentFindErr = pgx.ErrNoRows
	h := &handler{q: q}

	for _, tc := range []struct {
		name string
		call func(http.ResponseWriter, *http.Request)
	}{
		{"setup", h.getMyAgentSetup},
		{"setup.zip", h.getMyAgentSetupZip},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			tc.call(rec, connectReq())
			if rec.Code != http.StatusNotFound {
				t.Errorf("status: got %d, want 404（403 にすると存在が漏れる）", rec.Code)
			}
		})
	}
}

// TestGetMyAgentSetupZip は zip の中身とファイル名を確かめる（ApiDesign.md 4.5.8.5）。
func TestGetMyAgentSetupZip(t *testing.T) {
	h := &handler{q: connectFake("claude_code", "MY_LAPTOP")}
	rec := httptest.NewRecorder()
	h.getMyAgentSetupZip(rec, connectReq())

	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/zip" {
		t.Errorf("Content-Type: got %q", got)
	}
	// **種別を名前に入れる。** 同じプロジェクトに2件持つと見分けられなくなる。
	want := `attachment; filename="pb-connect-demo-claude_code.zip"`
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
	// **手引きは実名、接続設定は別名。** 展開した瞬間に既存を消さない。
	if len(names) != 2 || names[0] != "PB-README.md" || names[1] != ".mcp.pb-block.json" {
		t.Errorf("zip の中身: got %v", names)
	}
}

// TestGetMyAgentSetupReturnsCATrust は「HTTPS の証明書を信頼させる」手順を返すことを確かめる
// （ApiDesign.md 4.5.8.1b）。
//
// **接続先が http でも返す**（画面は常に畳んで出す）。**Codex は接続方式で手順が変わる。**
// 本文の中身は internal/agentsetup のテストが見る。
func TestGetMyAgentSetupReturnsCATrust(t *testing.T) {
	cases := []struct {
		kind, transport, want string
	}{
		{"claude_code", "", "verified"},
		{"codex", "", "unverified"},
		{"codex", "bridge", "partial"},
		{"gemini", "", "partial"},
	}
	for _, tc := range cases {
		t.Run(tc.kind+"/"+tc.transport, func(t *testing.T) {
			h := &handler{q: connectFake(tc.kind, "MY_LAPTOP")}
			req := connectReq()
			if tc.transport != "" {
				q := req.URL.Query()
				q.Set("transport", tc.transport)
				if tc.transport == "bridge" {
					q.Set("os", "darwin")
					q.Set("arch", "arm64")
				}
				req.URL.RawQuery = q.Encode()
			}
			rec := httptest.NewRecorder()
			h.getMyAgentSetup(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status: got %d, want 200 (%s)", rec.Code, rec.Body.String())
			}
			v := decodeConnect(t, rec)
			if v.CATrust.Verification != tc.want {
				t.Errorf("ca_trust.verification: got %q, want %q", v.CATrust.Verification, tc.want)
			}
			if v.CATrust.BodyMD == "" {
				t.Error("ca_trust.body_md が空（http でも返す）")
			}
			if !bytes.Contains(rec.Body.Bytes(), []byte(`"ca_trust":{"verification":`)) {
				t.Errorf("応答に ca_trust が無い: %s", rec.Body.String())
			}
		})
	}
}
