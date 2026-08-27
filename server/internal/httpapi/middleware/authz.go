package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

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

// RequirePermissionUnlessQuery は RequirePermission と同じ判定を行うが、
// クエリパラメータ param が exempt と一致する要求だけは素通しする。
//
//	r.With(middleware.RequirePermissionUnlessQuery(q, "user.manage", "scope", "project")).
//	  Get("/roles", h.listRoles)
//
// ApiDesign.md 7.1 の GET /roles が、scope によって必要権限を変えるために使う
// （?scope=project は権限不要、それ以外は user.manage）。
//
// **1本のルートで権限が変わる場合にも、宣言をルート定義に残すためのものである。**
// 判定をハンドラ本体へ移すと、routes.go を眺めてもこの行だけ必要権限が読めなく
// なる。Design.md 6.4.4 が権限をミドルウェアとして宣言せよと定めるのは、
// 書き忘れに気づけるようにするためであり、その防御を1本だけ外さない。
//
// **読み取り専用で、素通しする部分集合を意図して公開しているエンドポイントに
// だけ使うこと。** 書き込みの認可をクエリパラメータで緩めてはならない。呼び出し側が
// パラメータを付け替えるだけで通ってしまう。
//
// **認可が値の検証より先に走る。** exempt 以外の値（不正な値を含む）はすべて
// permission を要求するため、権限を持たない呼び出し元が解釈できない値を送ると
// 422 ではなく 403 が返る。**意図した順序である**——その呼び出し元は exempt 以外を
// 要求できないのだから、値が正しいかどうかは判定の後で足りる。
//
// **Authenticate の後に置くこと。** プリンシパルが無いと判定できない。
func RequirePermissionUnlessQuery(q gen.Querier, permission, param, exempt string) func(http.Handler) http.Handler {
	guard := RequirePermission(q, permission)
	return func(next http.Handler) http.Handler {
		guarded := guard(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get(param) == exempt {
				next.ServeHTTP(w, r)
				return
			}
			guarded.ServeHTTP(w, r)
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
	return requireProjectPermissions(q, "RequireProjectPermission", permission)
}

// RequireAnyProjectPermission は、列挙した権限の**いずれか1つ**を要求する
// （手順18a）。
//
//	r.With(middleware.RequireAnyProjectPermission(q,
//	     "comment.delete_any", "comment.edit_own")).
//	  Delete("/projects/{key}/tickets/{seq}/comments/{id}", h.deleteTicketComment)
//
// **ApiDesign.md 9.8 のコメント削除だけが OR の必要権限を持つ**——
// 「comment.delete_any、または comment.edit_own かつ自分のもの」である。
// 「自分のものか」は行を読まないと決まらないのでハンドラ側で見るが、
// **どの権限で通りうるかはルート定義に残す**（Design.md 6.4.4 の
// 「routes.go を眺めるだけで必要権限が分かる」）。
//
// **AND ではなく OR である。** 追加の条件を AND で重ねたいときは、
// RequireProjectPermission を .With で並べれば済む。
func RequireAnyProjectPermission(q gen.Querier, permissions ...string) func(http.Handler) http.Handler {
	return requireProjectPermissions(q, "RequireAnyProjectPermission", permissions...)
}

// requireProjectPermissions は上2つの共通部分。permissions のいずれか1つを
// 満たせば通す（1つだけ渡せば RequireProjectPermission と同じ意味になる）。
//
// name は内部エラーと監査に載せる呼び出し元の名前である。
func requireProjectPermissions(q gen.Querier, name string, permissions ...string) func(http.Handler) http.Handler {
	// 監査（permission.denied）に載せる表記。OR であることが読めるように
	// 区切りを入れる——required_permission が1語でないことは、記録を読む人に
	// とって「どちらでもよかった」という情報である。
	label := strings.Join(permissions, " or ")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := requirePrincipal(w, r, fmt.Sprintf("%s(%s)", name, label))
			if p == nil {
				return
			}
			if len(permissions) == 0 {
				apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
					fmt.Errorf("%s に権限が1つも渡されていない", name)))
				return
			}

			key := chi.URLParam(r, ProjectKeyURLParam)
			if key == "" {
				apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
					fmt.Errorf("%s(%s) のルートに {%s} が無い", name, label, ProjectKeyURLParam)))
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
					denyNotFound(w, r, q, label, "", key, "プロジェクトが存在しない")
					return
				}
			}

			// トークンが別のプロジェクトに紐づいている場合を先に見る。
			// Reachable にも畳み込んであるが（projectAuthz を参照）、
			// 監査に残す理由を「非メンバー」と取り違えないよう分けて判定する。
			if !p.CanReachProject(a.ProjectID) {
				denyNotFound(w, r, q, label, a.ProjectID, key,
					"トークンが別のプロジェクトに紐づいている（access_token.project_id）")
				return
			}

			if !a.Reachable {
				denyNotFound(w, r, q, label, a.ProjectID, key,
					"プロジェクトのメンバーではなく、アドミニストレータでもない")
				return
			}

			granted := slices.ContainsFunc(permissions, func(perm string) bool {
				return auth.HasPermission(a.Permissions, perm)
			})
			if !granted {
				denyForbidden(w, r, q, label, a.ProjectID, key)
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
