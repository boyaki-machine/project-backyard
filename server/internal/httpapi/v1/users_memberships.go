// ユーザー管理API（ApiDesign.md 6章）のうち、プロジェクトメンバーシップ。
//
//	PUT    /api/v1/admin/users/:id/memberships/:project_key  6.8
//	DELETE /api/v1/admin/users/:id/memberships/:project_key  6.8
//
// `GuiDesign.md` 5.6.2 の「プロジェクトごとの権限」ブロックに対応する。
//
// **プロジェクト側からも同じ操作ができるようになる**（未実装の
// `POST /projects/:key/members`）。6.8 は「同一の状態を2経路で変更することに
// なるため、内部実装は共通の1関数に集約する」と定めるので、そのときは
// setProjectMembership を両方から呼ぶこと。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// membershipRequest は 6.8 の PUT のリクエスト本体。
type membershipRequest struct {
	Role *string `json:"role"`
}

// putUserMembership は PUT /admin/users/:id/memberships/:project_key を
// 処理する（ApiDesign.md 6.8）。
//
// **追加と変更を兼ねる（冪等）。** 同じ内容で2度呼んでも結果は同じで、
// joined_at は最初の1回のまま動かない。
//
// 応答は 6.3 の project_memberships[] の要素と同形（200）。画面が
// 一覧を取り直さずにその行を差し替えられるようにするためである
// （GuiDesign.md 6.4「サーバ応答で画面の値を更新する」）。
func (h *handler) putUserMembership(w http.ResponseWriter, r *http.Request) {
	const route = "PUT /admin/users/{id}/memberships/{key}"

	_, id, ok := h.adminUserContext(w, r, route)
	if !ok {
		return
	}
	key, ok := membershipProjectKey(w, r, route)
	if !ok {
		return
	}

	var req membershipRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.Role == nil || *req.Role == "" {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "role", Code: "required", Message: "プロジェクトロールを指定してください",
		}))
		return
	}
	role := *req.Role

	ctx := r.Context()
	rec := audit.FromRequest(r)

	var view userMembershipView
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		cur, err := q.GetAdminUser(ctx, id)
		if err != nil {
			return err
		}
		projectID, err := resolveMembershipProject(ctx, q, key)
		if err != nil {
			return err
		}

		// **ロールの妥当性はDBに問い合わせる**（IsProjectScopedRole）。
		// Go 側に 'project_admin' などを書き写すと 0010 のシード
		// （DbDesign.md 7.3）と二重管理になる。POST /projects と同じ扱い。
		valid, err := q.IsProjectScopedRole(ctx, role)
		if err != nil {
			return fmt.Errorf("ロール %q を確認できない: %w", role, err)
		}
		if !valid {
			return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "role", Code: "invalid",
				Message: "プロジェクトロールが正しくありません",
			})
		}

		if err := q.UpsertProjectMember(ctx, gen.UpsertProjectMemberParams{
			ProjectID: projectID,
			ActorID:   id,
			RoleKey:   role,
		}); err != nil {
			return fmt.Errorf("メンバーシップを更新できない: %w", err)
		}

		// **メンバーシップは実効権限の第2層である**（Design.md 6.4.1）。
		// 変えたらキャッシュを捨てる（6.4.5、authz.sql が呼び出し元として
		// 名指ししている2つのうちの片方）。
		if err := q.InvalidateActorPermissionCache(ctx, id); err != nil {
			return fmt.Errorf("権限キャッシュを無効化できない: %w", err)
		}

		if err := recordMembershipChange(ctx, q, rec, id, cur.Email, key, &role); err != nil {
			return err
		}

		row, err := q.GetProjectMembership(ctx, gen.GetProjectMembershipParams{
			ProjectID: projectID,
			ActorID:   id,
		})
		if err != nil {
			return fmt.Errorf("メンバーシップを読めない: %w", err)
		}
		view = newUserMembershipView(
			row.ProjectID, row.ProjectKey, row.ProjectName, row.RoleKey, row.JoinedAt,
		)
		return nil
	})
	if err != nil {
		writeUserUpdateError(w, r, id, nil, err)
		return
	}

	WriteJSON(w, http.StatusOK, view)
}

// deleteUserMembership は DELETE /admin/users/:id/memberships/:project_key を
// 処理する（ApiDesign.md 6.8）。
//
// **元から居なくても 204**。削除は冪等な操作であり、何度呼んでも
// 「メンバーではない」状態に収束する。ただし**監査は実際に消えたときだけ
// 書く**（空振りを記録すると本当の剥奪が埋もれる。setProjectStatus と同じ方針）。
func (h *handler) deleteUserMembership(w http.ResponseWriter, r *http.Request) {
	const route = "DELETE /admin/users/{id}/memberships/{key}"

	_, id, ok := h.adminUserContext(w, r, route)
	if !ok {
		return
	}
	key, ok := membershipProjectKey(w, r, route)
	if !ok {
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		cur, err := q.GetAdminUser(ctx, id)
		if err != nil {
			return err
		}
		projectID, err := resolveMembershipProject(ctx, q, key)
		if err != nil {
			return err
		}

		rows, err := q.DeleteProjectMember(ctx, gen.DeleteProjectMemberParams{
			ProjectID: projectID,
			ActorID:   id,
		})
		if err != nil {
			return fmt.Errorf("メンバーシップを削除できない: %w", err)
		}
		if rows == 0 {
			return nil
		}

		if err := q.InvalidateActorPermissionCache(ctx, id); err != nil {
			return fmt.Errorf("権限キャッシュを無効化できない: %w", err)
		}
		return recordMembershipChange(ctx, q, rec, id, cur.Email, key, nil)
	})
	if err != nil {
		writeUserUpdateError(w, r, id, nil, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// resolveMembershipProject はプロジェクトキーを ID に変える。
//
// **存在しないキーは 404**（ApiDesign.md 1.2-5）。ここは user.manage を
// 持つアドミニストレータしか通らないため、5.4 のような「見えないから 404」の
// 判断は要らない——単に無いものが無い。
func resolveMembershipProject(ctx context.Context, q gen.Querier, key string) (string, error) {
	projectID, err := q.FindProjectIDByKey(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", apierr.New(apierr.NotFound).
			WithMessage(fmt.Sprintf("プロジェクト %s が見つかりません", key)).
			WithCause(fmt.Errorf("プロジェクト %q が見つからない", key))
	}
	if err != nil {
		return "", fmt.Errorf("プロジェクト %q を引けない: %w", key, err)
	}
	return projectID, nil
}

// membershipProjectKey は {key} を取り出す。
//
// **プロジェクト系のミドルウェアと同じパラメータ名を使う**
// （middleware.ProjectKeyURLParam）。/admin/users 配下では
// RequireProjectPermission を通さないが、名前を揃えておけば
// 「このセグメントはプロジェクトキーである」と読める。
func membershipProjectKey(w http.ResponseWriter, r *http.Request, route string) (string, bool) {
	key := chi.URLParam(r, middleware.ProjectKeyURLParam)
	if key == "" {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s のルートに {%s} が無い", route, middleware.ProjectKeyURLParam)))
		return "", false
	}
	return key, true
}

// recordMembershipChange は 6.8 の監査を書く。
//
// **role.change を使う**（手順13a の判断）。2.10 のカタログにメンバーの
// 追加・削除の action は無いが、この操作の実質はプロジェクトロールの
// 付与・変更・剥奪であり、role.change の意味に収まる。カタログを増やすと
// 監査ログ画面（GuiDesign.md 5.7）の「操作」の選択肢も増える。
//
// role が nil なら剥奪。detail の project_role が null になることで区別できる。
func recordMembershipChange(
	ctx context.Context, q gen.Querier, rec *audit.Recorder,
	actorID, email, projectKey string, role *string,
) error {
	return rec.Record(ctx, q, audit.Entry{
		Action: audit.RoleChange,
		Result: audit.Success,
		// **対象は app_user ではなく project_member。** 変えたのはユーザーの
		// 属性ではなく、ユーザーとプロジェクトの結び付きである。
		TargetType: "project_member",
		TargetID:   actorID,
		Detail: map[string]any{
			"email":        email,
			"project_key":  projectKey,
			"project_role": role,
		},
	})
}
