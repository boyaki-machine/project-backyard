// GET /api/v1/projects/{key}（ApiDesign.md 5.4）。
//
// 応答の組み立ては project_view.go の buildProjectDetail が持つ。POST /projects
// （5.3）の応答も 5.4 と同形式であると定められているため、両者で同じ関数を通す。
// 配置手順の版が指定されたときだけ、現在の配布版と古い場合の警告を追加する。
//
// 到達可否（メンバーでなければ 404）の判定は RequireProjectPermission が済ませて
// いる（Design.md 6.4.5）。ここまで来たということは、少なくとも project.view を
// 持つメンバーかアドミニストレータである。それでも GetProjectByKey が
// ErrNoRows を返しうるのは、認可の直後に他者が削除した場合だけである。
package v1

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/agentsetup"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// getProject は GET /api/v1/projects/{key} を処理する（ApiDesign.md 5.4）。
func (h *handler) getProject(w http.ResponseWriter, r *http.Request) {
	p, key, ok := projectRequestContext(w, r, "GET /projects/{key}")
	if !ok {
		return
	}

	var clientVersion int64
	if values, present := r.URL.Query()["workflow_version"]; present {
		var err error
		if len(values) == 1 {
			clientVersion, err = strconv.ParseInt(values[0], 10, 32)
		}
		if len(values) != 1 || err != nil || clientVersion < 1 {
			apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "workflow_version", Code: "invalid", Message: "配置手順の版番号は1以上の32bit整数で指定してください",
			}))
			return
		}
	}

	// my_permissions を認可ミドルウェアと同じ値から作る（Design.md 6.4.1）。
	// RequireProjectPermission が解決済みなので、ここでの呼び出しはDBを引かない。
	systemPerms, ctx, err := middleware.SystemPermissions(r.Context(), h.q, p)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	view, err := buildProjectDetail(ctx, h.q, p, systemPerms, key)
	if err != nil {
		writeProjectDetailError(w, r, key, err)
		return
	}

	if clientVersion > 0 {
		view.WorkflowVersion = agentsetup.WorkflowVersion
		view.Warning = agentsetup.WorkflowWarning(clientVersion)
	}
	WriteJSON(w, http.StatusOK, view)
}

// projectRequestContext は /projects/{key} 配下のハンドラが共通で行う取り出し。
//
// プリンシパルと {key} はどちらもミドルウェアが用意している。欠けているのは
// ルート定義の誤り（認証や RequireProjectPermission を通していない、あるいは
// パスに {key} が無い）であって、利用者の入力の誤りではない。したがって
// 500 に倒し、原因を構造化ログへ残す。
func projectRequestContext(
	w http.ResponseWriter, r *http.Request, route string,
) (*auth.Principal, string, bool) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s が認証ミドルウェアを通っていない", route)))
		return nil, "", false
	}

	key := chi.URLParam(r, middleware.ProjectKeyURLParam)
	if key == "" {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s のルートに {%s} が無い", route, middleware.ProjectKeyURLParam)))
		return nil, "", false
	}
	return p, key, true
}

// writeProjectDetailError は 5.4 の応答を組み立てられなかったときの応答。
//
// **行が無いのは 404**（Design.md 6.4.5。存在を隠す資源は 403 ではなく 404）。
// 認可を通った後にここへ来るのは、判定と読み取りの間に削除された場合である。
func writeProjectDetailError(w http.ResponseWriter, r *http.Request, key string, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.NotFound).
			WithCause(fmt.Errorf("プロジェクト %q が見つからない", key)))
		return
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).
		WithCause(fmt.Errorf("プロジェクト %q を読めない: %w", key, err)))
}

// projectScopeContext は /projects/{key} 配下の**子資源**のハンドラが行う取り出し。
//
// projectRequestContext がプリンシパルと {key} を返すのに対し、こちらは
// **プロジェクトの ID まで解決して返す**。タグ（9.11）・スプリント（9.12）・
// チケット（9.2〜）はいずれも project_id で行を絞るためである。
//
// 到達可否（メンバーか）の判定は RequireProjectPermission が済ませている
// （Design.md 6.4.5）。ここで ErrNoRows になるのは、認可の直後に他者が
// プロジェクトを削除した場合だけで、writeProjectDetailError と同じく 404 に倒す。
func projectScopeContext(
	w http.ResponseWriter, r *http.Request, q gen.Querier, route string,
) (*auth.Principal, string, string, bool) {
	p, key, ok := projectRequestContext(w, r, route)
	if !ok {
		return nil, "", "", false
	}
	projectID, err := q.FindProjectIDByKey(r.Context(), key)
	if err != nil {
		writeProjectDetailError(w, r, key, err)
		return nil, "", "", false
	}
	return p, key, projectID, true
}
