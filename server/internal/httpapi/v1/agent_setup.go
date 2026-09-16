// エージェント連携セットアップ（ApiDesign.md 5.7、手順28a）。
//
//	GET /api/v1/projects/{key}/agent-setup       agent.register
//	GET /api/v1/projects/{key}/agent-setup.zip   agent.register
//
// **リポジトリにコミットする配置ファイルを組み立てて返す**（Requirements.md 10.9.1 の系統A）。
// 組み立て自体は internal/agentsetup にあり、ここは入口の検証と受け渡しだけを行う。
//
// **接続設定は返さない。** 10.8.1 が履歴管理の対象外と定めたので、リポジトリに置くものを
// 作るこの口には置き場がない。あれは各人が /me/agents から受け取る（手順28b）。
//
// **必要権限は agent.register。** project.edit ではない（GuiDesign.md 3.2）。持つのは
// project_admin だけで、0010 の割り当てをそのまま使う——新しい権限キーを作らない。
package v1

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/boyaki-machine/project-backyard/server/internal/agentsetup"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// agentSetupFileView は 5.7.1 の files[] の1件。
//
// **agentsetup.File をそのまま使わない。** あちらは組み立ての都合で
// Content を必ず持つが、応答の形は ApiDesign.md が正本である。**層をまたぐ
// ところで一度写しておくと、内部の都合が API の形へ漏れない。**
type agentSetupFileView struct {
	Path        string `json:"path"`
	ClientKind  string `json:"client_kind"`
	Mode        string `json:"mode"`
	Language    string `json:"language"`
	MarkerBegin string `json:"marker_begin,omitempty"`
	MarkerEnd   string `json:"marker_end,omitempty"`
	Content     string `json:"content"`
}

// agentSetupView は 5.7.1 の応答。
type agentSetupView struct {
	Project         projectRef           `json:"project"`
	BaseURL         string               `json:"base_url"`
	WorkflowVersion int                  `json:"workflow_version"`
	Clients         []string             `json:"clients"`
	Files           []agentSetupFileView `json:"files"`
}

// getAgentSetup は配置ファイルを JSON で返す（ApiDesign.md 5.7.1）。
func (h *handler) getAgentSetup(w http.ResponseWriter, r *http.Request) {
	view, e := h.buildAgentSetup(w, r, "GET /projects/{key}/agent-setup")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if view == nil {
		return // projectScopeContext が応答済み
	}
	WriteJSON(w, http.StatusOK, view)
}

// getAgentSetupZip は同じ内容を zip で返す（ApiDesign.md 5.7.2）。
//
// **別パスにしてあるのは、ブラウザの <a download href> で素直に落とすためである**
// （Accept ヘッダでの切り替えにしない）。認証は Cookie が載る。
func (h *handler) getAgentSetupZip(w http.ResponseWriter, r *http.Request) {
	view, e := h.buildAgentSetup(w, r, "GET /projects/{key}/agent-setup.zip")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	if view == nil {
		return
	}

	files := make([]agentsetup.File, 0, len(view.Files))
	for _, f := range view.Files {
		files = append(files, agentsetup.File{
			Path: f.Path, Mode: agentsetup.Mode(f.Mode), Content: f.Content,
		})
	}
	blob, err := agentsetup.Zip(files)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("zip を作れない: %w", err)))
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf("attachment; filename=%q", "pb-setup-"+view.Project.Key+".zip"))
	w.WriteHeader(http.StatusOK)
	// **書き込み失敗（クライアント切断など）はこの時点で回復手段がない**
	// （WriteJSON と同じ扱い。paging.go）。
	_, _ = w.Write(blob)
}

// buildAgentSetup は2本のハンドラが共通で行う組み立て。
//
// 戻り値が (nil, nil) のときは、projectScopeContext が既に応答している。
func (h *handler) buildAgentSetup(
	w http.ResponseWriter, r *http.Request, route string,
) (*agentSetupView, *apierr.Error) {
	_, key, _, ok := projectScopeContext(w, r, h.q, route)
	if !ok {
		return nil, nil
	}

	clients, e := h.parseSetupClients(r)
	if e != nil {
		return nil, e
	}

	proj, err := h.q.GetProjectByKey(r.Context(), key)
	if err != nil {
		return nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("プロジェクトを読めない: %w", err))
	}

	files, err := agentsetup.Render(clients, agentsetup.Params{
		ProjectKey:  proj.Key,
		ProjectName: proj.Name,
		BaseURL:     h.publicBaseURL(r),
	})
	if err != nil {
		// **ここへ来るのは検証の漏れである。** parseSetupClients が
		// has_setup_template で絞った後なので、Render が拒む種別は残らない。
		return nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("配置ファイルを組み立てられない: %w", err))
	}

	views := make([]agentSetupFileView, 0, len(files))
	for _, f := range files {
		views = append(views, agentSetupFileView{
			Path:        f.Path,
			ClientKind:  f.ClientKind,
			Mode:        string(f.Mode),
			Language:    f.Language,
			MarkerBegin: f.MarkerBegin,
			MarkerEnd:   f.MarkerEnd,
			Content:     f.Content,
		})
	}
	return &agentSetupView{
		Project:         projectRef{Key: proj.Key, Name: proj.Name},
		BaseURL:         h.publicBaseURL(r),
		WorkflowVersion: agentsetup.WorkflowVersion,
		Clients:         clients,
		Files:           views,
	}, nil
}

// parseSetupClients は ?client= を読んで検証する（ApiDesign.md 5.7.1）。
//
// **値域の正本は agent_client_kind の has_setup_template である**（DbDesign.md 8.2.1.1）。
// Go 側の specs と二重に持たない——食い違いは agentsetup のテストが
// 0023 と突き合わせて捕まえる。
//
// **重複は畳み、並びはカタログの順（sort_order）にする。** 画面がチェックの順で
// 送ってきても、応答と生成物の並びが揺れないようにするため。
func (h *handler) parseSetupClients(r *http.Request) ([]string, *apierr.Error) {
	raw := r.URL.Query()["client"]
	if len(raw) == 0 {
		return nil, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "client", Code: "required",
			Message: "使うクライアントを1つ以上選んでください",
		})
	}

	rows, err := h.q.AgentClientKindsWithTemplate(r.Context())
	if err != nil {
		return nil, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("クライアント種別を読めない: %w", err))
	}
	// order はカタログの並び（sort_order 昇順）を保つための順位表。
	order := make(map[string]int, len(rows))
	for i, row := range rows {
		order[row.Key] = i
	}

	seen := make(map[string]bool, len(raw))
	out := make([]string, 0, len(raw))
	var details []apierr.Detail
	for _, k := range raw {
		if _, ok := order[k]; !ok {
			details = append(details, apierr.Detail{
				Field: "client", Code: "not_found",
				Message: fmt.Sprintf("%q 向けの配置ファイルは、まだ用意されていません", k),
			})
			continue
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	if len(details) > 0 {
		return nil, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out, nil
}

// publicBaseURL は PB の公開 URL を組み立てる（ApiDesign.md 5.7.1）。
//
// **PB は自分の公開 URL を知らない。** 設定にあるのは待受アドレス（PB_BIND）だけで、
// これは URL に使えない。**外から見えるアドレスを伝えるのはリクエストの Host ヘッダ
// だけ**なので、暫定としてそこから組む。dev（:8080）と stg（:8081）は、利用者が
// いま開いている側が自動で入る。
//
// **スキームは cookieSecure を見る。** config.CookieSecure の注釈が
// 「リバースプロキシで TLS を終端する構成ではアプリに平文で届くため、自動判定は
// 『HTTPS で公開しているのに Secure が付かない』を招く」と書いており、**同じ問題を
// 同じ設定で解く**。r.TLS だけを見ると、プロキシの背後で必ず http:// になる。
//
// **新しい設定項目は足さない**（利用者の判断、2026-09-06）。着地は
// アプリケーション設定画面（Design.md 10.3）である。
func (h *handler) publicBaseURL(r *http.Request) string {
	scheme := "http"
	if h.settings.CookieSecure() || r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
