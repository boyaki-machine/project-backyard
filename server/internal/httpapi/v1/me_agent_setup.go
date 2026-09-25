// 自分の接続設定（ApiDesign.md 4.5.8、Requirements.md 10.9.1 の系統B、手順28b）。
//
//	GET /api/v1/me/agents/{id}/setup       本人
//	GET /api/v1/me/agents/{id}/setup.zip   本人
//
// **agent_setup.go（系統A）と対になる。** あちらはリポジトリにコミットするファイルを
// プロジェクト管理者が1回作る口で、こちらは**各人の手元にしか残らないもの**を
// 本人が何度でも取り直す口である。組み立ては internal/agentsetup にあり、
// ここは入口の検証と受け渡しだけを行う。
//
// **答えるのは「MCP が使える状態になるまで」だけである。** 作業の材料をどこから
// どう用意するかは PB が知らない（リポジトリ型・配布型・MCP 型がある。10.9.1）ので
// 返さない——プロジェクトの文書に書かれ、人もエージェントもそこから読む（手順28c）。
//
// **平文のトークンは返さない。** 何度でも開ける口に平文を置くのは 10.10.1 に反する。
// **接続できたかどうかも返さない**——GET /me/agents の token.last_used_at が
// 既にそれを表しており、同じ事実を2か所から出すと必ず食い違う。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/agentsetup"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// agentConnectAgentView は 4.5.8.1 の agent 部分。
//
// **myAgentView をそのまま使わない。** あちらは一覧の1件で、トークンや状態を持つ。
// ここで要るのは「どこへ、どの名前で置くか」を決める4つだけである。
type agentConnectAgentView struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	ClientKind  string `json:"client_kind"`
	// ClientDisplayName はカタログの表示名（4.5.7）。**画面が対応表を持たないために返す。**
	ClientDisplayName string `json:"client_display_name"`
	// TokenEnvName は接頭付きの実際の変数名（4.5.1）。**export_line が null の
	// 種別でも値は返す**——種別を変えれば意味を持ち直す。
	TokenEnvName string `json:"token_env_name"`
}

// agentConnectView は 4.5.8.1 の応答。
type agentConnectView struct {
	Agent     agentConnectAgentView `json:"agent"`
	Project   projectRef            `json:"project"`
	BaseURL   string                `json:"base_url"`
	MCPURL    string                `json:"mcp_url"`
	Transport string                `json:"transport"`
	// ExportLine は環境変数へトークンを置く行。**null になる種別がある**（4.5.8.2）。
	ExportLine *string              `json:"export_line"`
	Files      []agentSetupFileView `json:"files"`
	// CATrust は「HTTPS の証明書を信頼させる」手順（4.5.8.1b）。**null にしない**
	// ——接続先が http でも返す（後から HTTPS にする人が先に読めるように）。
	CATrust agentCATrustView `json:"ca_trust"`
}

// agentCATrustView は種別ごとの「CA を信頼させる」手順（4.5.8.1b）。
type agentCATrustView struct {
	// Verification は実機で確かめたか（verified / partial / unverified）。
	Verification string `json:"verification"`
	// BodyMD は本文（Markdown）。**zip の PB-README.md の同じ節と同じ文である。**
	BodyMD string `json:"body_md"`
}

// getMyAgentSetup は接続設定を JSON で返す（ApiDesign.md 4.5.8.1）。
func (h *handler) getMyAgentSetup(w http.ResponseWriter, r *http.Request) {
	view, _, e := h.buildAgentConnect(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// getMyAgentSetupZip は同じ内容を zip で返す（ApiDesign.md 4.5.8.5）。
//
// **別パスにしてあるのは、ブラウザの <a download href> で素直に落とすためである**
// （系統A と同じ。認証は Cookie が載る）。
func (h *handler) getMyAgentSetupZip(w http.ResponseWriter, r *http.Request) {
	view, connect, e := h.buildAgentConnect(r)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if view.Transport == agentsetup.TransportBridge {
		asset, err := bridgeAsset()
		if err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
			return
		}
		connect.Assets = []agentsetup.Asset{asset}
	}

	blob, err := agentsetup.ConnectZip(connect)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("zip を作れない: %w", err)))
		return
	}

	// **ファイル名に種別を入れる**（4.5.8.5）。1人が同じプロジェクトに
	// Claude Code と Copilot の2件を持つことがあり、同じ名前の zip が
	// ダウンロードフォルダに並ぶとどちらがどちらか分からなくなる。
	name := fmt.Sprintf("pb-connect-%s-%s.zip", view.Project.Key, view.Agent.ClientKind)
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", name))
	w.WriteHeader(http.StatusOK)
	// **書き込み失敗（クライアント切断など）はこの時点で回復手段がない**
	// （WriteJSON と同じ扱い。paging.go）。
	_, _ = w.Write(blob)
}

// bridgeAsset reads the helper shipped next to the currently running PB
// executable. It never builds code or accepts a client-provided path.
func bridgeAsset() (agentsetup.Asset, error) {
	exe, err := os.Executable()
	if err != nil {
		return agentsetup.Asset{}, fmt.Errorf("PB 実行ファイルの場所を取得できない: %w", err)
	}
	name := "pb-mcp-bridge"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	binary, err := os.ReadFile(filepath.Join(filepath.Dir(exe), name))
	if err != nil {
		return agentsetup.Asset{}, fmt.Errorf("同梱の %s を読めない: %w", name, err)
	}
	return agentsetup.Asset{Path: name, Content: binary, Mode: 0o755}, nil
}

// buildAgentConnect は2本のハンドラが共通で行う組み立て。
func (h *handler) buildAgentConnect(
	r *http.Request,
) (*agentConnectView, agentsetup.Connect, *apierr.Error) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		return nil, agentsetup.Connect{}, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("GET /me/agents/:id/setup が認証ミドルウェアを通っていない"))
	}
	ctx := r.Context()
	agentID := chi.URLParam(r, "id")

	ag, err := h.q.FindMyAgent(ctx, gen.FindMyAgentParams{
		ActorID: agentID, OwnerActorID: p.ActorID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// **他人のエージェントも 404 である**（Design.md 6.4.5）。
			return nil, agentsetup.Connect{}, apierr.New(apierr.NotFound)
		}
		return nil, agentsetup.Connect{}, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("エージェントを読めない: %w", err))
	}

	base := h.publicBaseURL(r)
	// **URL は1か所で組み立てる。** 画面が出す文字列と生成物の中身が別々に
	// 組まれると、黙ってずれる（4.5.1 が token_env_name について定めたのと同じ）。
	mcpURL := base + "/mcp/" + ag.ProjectKey.String

	transport := r.URL.Query().Get("transport")
	if transport == "" {
		transport = agentsetup.TransportDirect
	}
	params := agentsetup.ConnectParams{
		ProjectKey:        ag.ProjectKey.String,
		ProjectName:       ag.ProjectName.String,
		MCPURL:            mcpURL,
		TokenEnvName:      agentTokenEnvName(ag.TokenEnvSuffix, ag.ActorID),
		DisplayName:       ag.DisplayName,
		ClientDisplayName: h.clientKindLabel(ctx, ag.ClientKind),
		Transport:         transport,
	}

	connect, err := agentsetup.RenderConnect(ag.ClientKind, params)
	if err != nil {
		return nil, agentsetup.Connect{}, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("接続設定を組み立てられない: %w", err))
	}

	files := make([]agentSetupFileView, 0, len(connect.Files))
	for _, f := range connect.Files {
		files = append(files, agentSetupFileView{
			Path:       f.Path,
			ClientKind: f.ClientKind,
			Mode:       string(f.Mode),
			Language:   f.Language,
			Content:    f.Content,
		})
	}

	view := &agentConnectView{
		Agent: agentConnectAgentView{
			ID:                ag.ActorID,
			DisplayName:       ag.DisplayName,
			ClientKind:        ag.ClientKind,
			ClientDisplayName: params.ClientDisplayName,
			TokenEnvName:      params.TokenEnvName,
		},
		Project:   projectRef{Key: ag.ProjectKey.String, Name: ag.ProjectName.String},
		BaseURL:   base,
		MCPURL:    mcpURL,
		Transport: transport,
		Files:     files,
		CATrust: agentCATrustView{
			Verification: string(connect.CATrust.Verification),
			BodyMD:       connect.CATrust.BodyMD,
		},
	}
	// **2種別で null になる**（4.5.8.2）。Copilot は ${input:pb-token} を使って
	// 環境変数を読まず、Claude Desktop は GUI アプリなのでシェルの環境が届かない。
	// **意味のない行を出すと、利用者は書かれていない前提を自分の期待で埋める。**
	if connect.UsesTokenEnvVar {
		line := agentsetup.ExportLine(params.TokenEnvName)
		view.ExportLine = &line
	}
	return view, connect, nil
}

// clientKindLabel はカタログの表示名を引く（ApiDesign.md 4.5.7）。
//
// **画面に対応表を持たせないために、サーバが引いて返す**（4.5.1 の
// token_env_name と同じ形）。**カタログを読めなかった場合と、カタログに
// 無い種別ではキーをそのまま返す**——表示名が引けないだけで、接続設定そのものは
// 正しく作れる。**手引きの見出しが英字のキーになるが、出ないよりは読める。**
func (h *handler) clientKindLabel(ctx context.Context, kind string) string {
	rows, err := h.q.ListAgentClientKinds(ctx)
	if err != nil {
		return kind
	}
	for _, row := range rows {
		if row.Key == kind {
			return row.DisplayName
		}
	}
	return kind
}
