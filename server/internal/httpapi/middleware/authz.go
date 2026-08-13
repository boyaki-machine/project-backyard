package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ProjectKeyURLParam は、プロジェクトを指す URL パラメータの名前。
//
// ApiDesign.md 5.4 が `/api/v1/projects/:key` と定めており、chi では
// `/projects/{key}` として宣言する。RequireProjectPermission はこの名前で
// パラメータを読む。
const ProjectKeyURLParam = "key"

// RequirePermission は、システムロール由来の実効権限に permission が
// 含まれることを要求する（Design.md 6.4.4）。
//
//	r.With(middleware.RequirePermission(q, "user.manage")).
//	  Get("/admin/users", h.listUsers)
//
// **権限はルート定義に宣言する。** ハンドラ本体に権限チェックを書くと、
// 新しいエンドポイントで書き忘れても気づけない。ルート定義に並べておけば
// routes.go を眺めるだけで全エンドポイントの必要権限を確認できる。
//
// 判定する集合は Design.md 6.4.1 の式のうちシステムロールの層である。
//
//	実効権限 = システムロールの権限 ∩ トークンのスコープ
//
// プロジェクト個別の資源には使わない。プロジェクトロールを併せて見る必要が
// あるため、そちらは RequireProjectPermission を使う。
//
// **Authenticate の後に置くこと。** プリンシパルが無いと判定できない。
func RequirePermission(q gen.Querier, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := requirePrincipal(w, r, fmt.Sprintf("RequirePermission(%s)", permission))
			if p == nil {
				return
			}

			permissions, ctx, err := SystemPermissions(r.Context(), q, p)
			if err != nil {
				apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
				return
			}
			r = r.WithContext(ctx)

			if !auth.HasPermission(permissions, permission) {
				denyForbidden(w, r, q, permission, "", "")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireProjectPermission は、URL の :key が指すプロジェクトに対して
// permission を要求する（Design.md 6.4.1 / 6.4.4）。
//
//	r.With(middleware.RequireProjectPermission(q, "ticket.close")).
//	  Post("/projects/{key}/tickets/{seq}/close", h.closeTicket)
//
// 判定は Design.md 6.4.1 の式そのものである。
//
//	実効権限 = ( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ スコープ
//
// 応答は3つに分かれる。
//
//	プロジェクトが存在しない                  404 not_found
//	到達できない（非メンバーかつ非管理者）    404 not_found ← 存在を隠す
//	到達できるが権限が足りない                403 forbidden
//
// **前2者を同じ 404 にするのは Design.md 6.4.5 の要請である**（存在を隠したい
// 資源は 404）。403 で返すと「そのキーのプロジェクトは在る」と漏れ、キーを
// 変えながら叩けばプロジェクトの一覧を復元できてしまう。
//
// **Authenticate の後、かつ chi のルートパターンに {key} を含むルートで使うこと。**
func RequireProjectPermission(q gen.Querier, permission string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := requirePrincipal(w, r, fmt.Sprintf("RequireProjectPermission(%s)", permission))
			if p == nil {
				return
			}

			key := chi.URLParam(r, ProjectKeyURLParam)
			if key == "" {
				apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
					fmt.Errorf("RequireProjectPermission(%s) のルートに {%s} が無い", permission, ProjectKeyURLParam)))
				return
			}

			a := auth.ProjectAuthzFromContext(r.Context(), key)
			if a == nil {
				var err error
				var ctx context.Context
				a, ctx, err = projectAuthz(r.Context(), q, p, key)
				if err != nil {
					apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
					return
				}
				r = r.WithContext(ctx)
				if a == nil {
					// プロジェクトそのものが無い。存在しないことは隠さなくてよいが、
					// 到達不可のときと同じ応答にしておかないと、応答の違いで
					// 「在るが見えない」と「無い」を区別できてしまう。
					denyNotFound(w, r, q, permission, "", key, "プロジェクトが存在しない")
					return
				}
			}

			// トークンが別のプロジェクトに紐づいている場合を先に見る。
			// Reachable にも畳み込んであるが（projectAuthz を参照）、
			// 監査に残す理由を「非メンバー」と取り違えないよう分けて判定する。
			if !p.CanReachProject(a.ProjectID) {
				denyNotFound(w, r, q, permission, a.ProjectID, key,
					"トークンが別のプロジェクトに紐づいている（access_token.project_id）")
				return
			}

			if !a.Reachable {
				denyNotFound(w, r, q, permission, a.ProjectID, key,
					"プロジェクトのメンバーではなく、アドミニストレータでもない")
				return
			}

			if !auth.HasPermission(a.Permissions, permission) {
				denyForbidden(w, r, q, permission, a.ProjectID, key)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// projectAuthz は key が指すプロジェクトの認可結果を組み立てる。
// プロジェクトが存在しなければ nil を返す（エラーではない）。
func projectAuthz(ctx context.Context, q gen.Querier, p *auth.Principal, key string) (*auth.ProjectAuthz, context.Context, error) {
	rows, err := q.FindProjectAuthzByKey(ctx, gen.FindProjectAuthzByKeyParams{
		ActorID:    p.ActorID,
		ProjectKey: key,
	})
	if err != nil {
		return nil, ctx, fmt.Errorf("プロジェクト %q の認可情報を読めない: %w", key, err)
	}
	if len(rows) == 0 {
		return nil, ctx, nil
	}

	a := &auth.ProjectAuthz{
		ProjectID: rows[0].ProjectID,
		Key:       rows[0].ProjectKey,
		Status:    rows[0].ProjectStatus,
		RoleKey:   rows[0].RoleKey.String,
	}
	projectPermissions := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.PermissionKey.Valid {
			projectPermissions = append(projectPermissions, row.PermissionKey.String)
		}
	}

	// システムロール側は RequirePermission と同じ入れ物を共有する。
	// そこに入っているのは既にスコープと積を取った後の集合だが、
	// ( A ∪ B ) ∩ S = ( A ∩ S ) ∪ ( B ∩ S ) であり、S との積は冪等なので
	// 6.4.1 の式と同じ結果になる。DBを引く回数を1回減らせる。
	systemPerms, ctx, err := SystemPermissions(ctx, q, p)
	if err != nil {
		return nil, ctx, err
	}

	// 到達可否は「当人が見てよいか」と「このトークンで触れてよいか」の積。
	// 後者は access_token.project_id による限定である（Principal.CanReachProject）。
	a.Reachable = (a.RoleKey != "" || p.IsAdministrator()) && p.CanReachProject(a.ProjectID)
	a.Permissions = auth.EffectivePermissions(systemPerms, projectPermissions, p.Scopes)
	return a, auth.NewProjectAuthzContext(ctx, a), nil
}

// denyForbidden は 403 forbidden を返し、permission.denied を記録する。
func denyForbidden(w http.ResponseWriter, r *http.Request, q gen.Querier,
	permission, projectID, projectKey string) {

	recordPermissionDenied(r, q, permission, projectID, projectKey, "権限が足りない")
	apierr.Write(w, r, apierr.New(apierr.Forbidden).WithCause(
		fmt.Errorf("必要な権限 %q を持たない", permission)))
}

// denyNotFound は 404 not_found を返し、permission.denied を記録する。
//
// **応答は「見つからない」だが、記録は permission.denied で残す。** 存在を
// 隠すのは呼び出し元に対してであって、監査に対してではない。誰がどの
// プロジェクトを突いたかは運用者に見えている必要がある（Design.md 6.4.5）。
func denyNotFound(w http.ResponseWriter, r *http.Request, q gen.Querier,
	permission, projectID, projectKey, reason string) {

	recordPermissionDenied(r, q, permission, projectID, projectKey, reason)
	apierr.Write(w, r, apierr.New(apierr.NotFound).WithCause(errors.New(reason)))
}

// recordPermissionDenied は audit_log('permission.denied') を1行書く
// （Design.md 6.4.5、ApiDesign.md 2.10）。
//
// **RecordOrLog を使う。** 認可の拒否は業務トランザクションを持たないため、
// 監査DBの一時障害で拒否そのものを返せなくなるほうが害が大きい。失敗は
// ERROR ログに残る（audit パッケージの方針）。
func recordPermissionDenied(r *http.Request, q gen.Querier,
	permission, projectID, projectKey, reason string) {

	detail := map[string]any{
		"required_permission": permission,
		"method":              r.Method,
		"path":                r.URL.Path,
		"reason":              reason,
	}
	entry := audit.Entry{
		Action: audit.PermissionDenied,
		Result: audit.Failure,
		Detail: detail,
	}
	if projectKey != "" {
		detail["project_key"] = projectKey
		// プロジェクトが存在しないときは ID が無い。target_id は char(26) なので
		// キーを入れられない。キーは detail 側に残す。
		if projectID != "" {
			entry.TargetType = "project"
			entry.TargetID = projectID
		}
	}
	audit.FromRequest(r).RecordOrLog(r.Context(), q, entry)
}
