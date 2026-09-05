// ユーザー管理API（ApiDesign.md 6章）のうち、更新と削除。
//
//	PATCH  /api/v1/admin/users/:id  6.4  部分更新・If-Match 必須
//	DELETE /api/v1/admin/users/:id  6.5  物理削除
//
// **3つのガードを API 側に置くことがこの2本の要点である**（6.4）。UIだけで
// 防ぐと、直接APIを叩いた場合に**誰もログインできないインスタンス**が生まれうる。
//
//	自分自身の system_role 変更・無効化・削除   409 self_modification_forbidden
//	最後の有効なアドミニストレータの降格・無効化・削除  409 last_administrator
//	email 重複                                  409 already_exists
//
// PATCH の応答は 6.3 と同形式（buildUserDetail）。5.5 の PATCH が 5.4 と
// 同形式であるのと同じ扱いで、同じ形を2か所で作らない。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// deletedUserDisplayName は「削除されたユーザー」のシステムアクターの表示名
// （DbDesign.md 6.7）。comment.author_id の付け替え先である。
//
// **このアクターはシードに無く、最初に必要になった削除で作る**（手順13a の判断）。
// Phase 1 には comment を作る経路が無く（チケットAPIは手順16）、置いても
// 一度も参照されないため。
const deletedUserDisplayName = "削除されたユーザー"

// updateUserRequest は 6.4 のリクエスト本体。
//
// **「送られなかった」と「送られた」を区別できる型で受ける**（updateProjectRequest
// と同じ）。4項目とも NULL への更新が無い列なので、*string / *bool で足りる。
type updateUserRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	SystemRole  *string `json:"system_role"`
	IsActive    *bool   `json:"is_active"`
}

// updateUserFields は検証を通ったあとの確定値。nil は「変更しない」。
type updateUserFields struct {
	displayName *string
	email       *string
	systemRole  *string
	isActive    *bool
}

// changed は変更が1つでもあるかを返す。
func (f updateUserFields) changed() bool {
	return f.displayName != nil || f.email != nil || f.systemRole != nil || f.isActive != nil
}

// patchUser は PATCH /api/v1/admin/users/:id を処理する（ApiDesign.md 6.4）。
func (h *handler) patchUser(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.adminUserContext(w, r, "PATCH /admin/users/{id}")
	if !ok {
		return
	}

	var req updateUserRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	version, versionErr := parseIfMatch(r)
	fields, validationErr := validateUpdateUser(req)
	// If-Match の欠落と本文の誤りを別々の応答に分けず、1つの 422 にまとめる
	// （2.5 の details は「項目ごとの誤り」を並べるもの。patchProject と同じ）。
	if e := mergeValidationErrors(versionErr, validationErr); e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	var view userDetailView
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **現在値をトランザクションの中で読む。** ガードの判定材料と更新が
		// 別の時点の状態を見ると、「最後の管理者」の判定をすり抜けられる。
		cur, err := q.GetAdminUser(ctx, id)
		if err != nil {
			return err
		}
		if e := guardUserUpdate(ctx, q, p, cur, fields); e != nil {
			return e
		}

		rows, err := q.UpdateAdminUserProfile(ctx, gen.UpdateAdminUserProfileParams{
			Email:      nargText(fields.email),
			SystemRole: nargText(fields.systemRole),
			ActorID:    id,
			Version:    version,
		})
		if err != nil {
			return err // メールの一意制約違反はそのまま返す。呼び出し側で 409 に写す
		}
		if rows == 0 {
			// 0 行の理由は version 不一致（409）か、消えた（404）かの2つ。
			// **行が在るかどうかを引いて区別する**（classifyUpdateMiss と同じ）。
			return classifyUserUpdateMiss(ctx, q, id)
		}

		// actor 側（display_name / is_active）。**同じトランザクションで通す。**
		// 片方だけ成功すると、表示名は変わったのに version が進んでいない
		// 状態が残る。
		if err := q.UpdateAdminUserActor(ctx, gen.UpdateAdminUserActorParams{
			DisplayName: nargText(fields.displayName),
			IsActive:    nargBool(fields.isActive),
			ActorID:     id,
		}); err != nil {
			return fmt.Errorf("actor を更新できない: %w", err)
		}

		// **メールを変えたら user_identity.subject も追随させる。**
		// ログインは subject = app_user.email で突き合わせており
		// （Design.md 6.2.1 手順2〜3、auth.sql の FindLocalLoginByEmail）、
		// 片方だけ変えると当人がログインできなくなる。
		// **設計文書に無い操作である**（ApiDesign.md 6.4 への追記を提案する）。
		if fields.email != nil {
			if err := q.UpdateLocalIdentitySubject(ctx, gen.UpdateLocalIdentitySubjectParams{
				Subject: *fields.email,
				UserID:  id,
			}); err != nil {
				return fmt.Errorf("user_identity.subject を更新できない: %w", err)
			}
		}

		// **ロールを変えたら権限キャッシュを捨てる**（Design.md 6.4.5）。
		// 消せなかったまま成功を返すと、降格したはずの利用者が TTL の間だけ
		// 旧権限で動く。したがって失敗は業務処理ごと失敗させる。
		if fields.systemRole != nil && *fields.systemRole != cur.SystemRole {
			if err := q.InvalidateActorPermissionCache(ctx, id); err != nil {
				return fmt.Errorf("権限キャッシュを無効化できない: %w", err)
			}
		}

		if err := recordUserUpdate(ctx, q, rec, id, cur, fields); err != nil {
			return err
		}

		view, err = buildUserDetail(ctx, q, id)
		return err
	})
	if err != nil {
		writeUserUpdateError(w, r, id, fields.email, err)
		return
	}

	WriteJSON(w, http.StatusOK, view)
}

// deleteUser は DELETE /api/v1/admin/users/:id を処理する（ApiDesign.md 6.5）。
//
// **物理削除である**（DbDesign.md 4.6）。app_user / user_identity /
// local_credential / access_token / project_member は ON DELETE CASCADE で
// 追従する。無効化のみ行いたい場合は 6.4 の is_active: false を使う。
//
// **If-Match は要求しない**（2.8）。削除に「失われる編集内容」が無く、
// 競合しても結果は同じ「消えている」に収束する。
func (h *handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	p, id, ok := h.adminUserContext(w, r, "DELETE /admin/users/{id}")
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
		if e := guardUserDelete(ctx, q, p, cur); e != nil {
			return e
		}

		// **所有するエージェントを先に始末する**（6.5。手順26a で足した）。
		// agent.owner_actor_id の ON DELETE CASCADE が消すのは agent の行だけで、
		// **エージェントの actor 行・その access_token・そのコメントは残る**
		// ——FK の向きは agent.actor_id → actor だからである。放置すると、
		// **認証は通るが実効権限が0件のトークン**が残り続ける。
		deletedAgents, err := purgeOwnedAgents(ctx, q, id)
		if err != nil {
			return err
		}

		// **監査は削除の前に書く**（6.5）。detail に削除時点の表示名とメールを
		// 残さないと、後から「誰を消したか」を追えなくなる。同じトランザクション
		// なので、削除が失敗すれば記録も残らない。
		//
		// **エージェントごとに agent.delete を並べない**（6.5）。利用者から見た
		// 操作は1回であり、件数だけを detail に載せる。
		if err := rec.Record(ctx, q, audit.Entry{
			Action:     audit.UserDelete,
			Result:     audit.Success,
			TargetType: "app_user",
			TargetID:   id,
			Detail: map[string]any{
				"display_name":   cur.DisplayName,
				"email":          cur.Email,
				"system_role":    cur.SystemRole,
				"deleted_agents": deletedAgents,
			},
		}); err != nil {
			return err
		}

		if err := reassignCommentsToSystemActor(ctx, q, id); err != nil {
			return err
		}

		rows, err := q.DeleteActorByID(ctx, id)
		if err != nil {
			return fmt.Errorf("ユーザー %q を削除できない: %w", id, err)
		}
		if rows == 0 {
			// GetAdminUser を通った直後に消えた場合。204 ではなく 404 に倒す。
			return pgx.ErrNoRows
		}
		return nil
	})
	if err != nil {
		writeUserUpdateError(w, r, id, nil, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// reassignCommentsToSystemActor は投稿者をシステムアクターへ付け替える
// （DbDesign.md 6.7、ApiDesign.md 6.5）。
//
// comment.author_id は NOT NULL かつ ON DELETE RESTRICT であり、**DBが
// 「付け替えてからでないと消せない」という順序を強制する。**
//
// **コメントが1件も無ければ何もしない。** Phase 1 は comment を作る経路が
// 無いため常にこちらを通り、システムアクターも作られない。
func reassignCommentsToSystemActor(ctx context.Context, q gen.Querier, actorID string) error {
	n, err := q.CountCommentsByAuthor(ctx, actorID)
	if err != nil {
		return fmt.Errorf("コメント数を数えられない: %w", err)
	}
	if n == 0 {
		return nil
	}

	systemID, err := q.FindDeletedUserActor(ctx, deletedUserDisplayName)
	if errors.Is(err, pgx.ErrNoRows) {
		systemID = ulidgen.New()
		if err := q.CreateSystemActor(ctx, gen.CreateSystemActorParams{
			ID:          systemID,
			DisplayName: deletedUserDisplayName,
		}); err != nil {
			return fmt.Errorf("システムアクターを作成できない: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("システムアクターを引けない: %w", err)
	}

	if _, err := q.ReassignComments(ctx, gen.ReassignCommentsParams{
		NewAuthorID: systemID,
		OldAuthorID: actorID,
	}); err != nil {
		return fmt.Errorf("コメントの投稿者を付け替えられない: %w", err)
	}
	return nil
}

// ── ガード（ApiDesign.md 6.4 / 6.5）───────────────────────────

// guardUserUpdate は 6.4 の表のうち、DBの制約で表せない3つを判定する。
//
// **メールの重複はここで見ない。** 6.2 と同じく UNIQUE 制約に委ねる
// （事前に確認してから INSERT すると、その間に割り込まれる。5.3 の TOCTOU 対策）。
func guardUserUpdate(
	ctx context.Context, q gen.Querier,
	p *auth.Principal, cur gen.GetAdminUserRow, f updateUserFields,
) error {
	demoting := f.systemRole != nil && *f.systemRole != cur.SystemRole
	deactivating := f.isActive != nil && !*f.isActive && cur.IsActive

	// **自分自身のロール変更と無効化を禁じる**（6.4）。表示名とメールは
	// 自分でも変えてよい——禁止の目的は「管理者が自分の権限を失って
	// 誰も管理できなくなる」ことの防止であり、表示名はそれに当たらない。
	if cur.ID == p.ActorID && (demoting || deactivating) {
		return apierr.New(apierr.SelfModificationForbidden).
			WithMessage("自分自身のロール変更・無効化はできません。他のアドミニストレータに依頼してください")
	}

	if demoting || deactivating {
		if err := guardLastAdministrator(ctx, q, cur); err != nil {
			return err
		}
	}
	return nil
}

// guardUserDelete は 6.5 の表を判定する。
//
// task_lease（Phase 2）の条件は実装しない。テーブルがまだ存在しない。
func guardUserDelete(
	ctx context.Context, q gen.Querier, p *auth.Principal, cur gen.GetAdminUserRow,
) error {
	if cur.ID == p.ActorID {
		return apierr.New(apierr.SelfModificationForbidden).
			WithMessage("自分自身を削除することはできません")
	}
	return guardLastAdministrator(ctx, q, cur)
}

// guardLastAdministrator は「最後の有効なアドミニストレータ」を守る
// （ApiDesign.md 6.4 / 6.5 の last_administrator）。
//
// **対象が有効なアドミニストレータでなければ何も見ない。** 無効な管理者や
// オペレータをいくら消しても、管理できる者が居なくなることはない。
func guardLastAdministrator(ctx context.Context, q gen.Querier, cur gen.GetAdminUserRow) error {
	if cur.SystemRole != systemRoleAdministrator || !cur.IsActive {
		return nil
	}
	n, err := q.CountActiveAdministrators(ctx)
	if err != nil {
		return fmt.Errorf("アドミニストレータの人数を数えられない: %w", err)
	}
	if n > 1 {
		return nil
	}
	return apierr.New(apierr.LastAdministrator).
		WithMessage("最後のアドミニストレータのため、この操作はできません。先に別のアドミニストレータを作成してください")
}

// ── 監査（ApiDesign.md 2.10）─────────────────────────────────

// recordUserUpdate は 6.4 の監査を書く。
//
// **user.update を常に、system_role が変わったときは加えて role.change を
// 記録する**（手順13a の判断）。2.10 が両方を挙げているのは、ロール変更を
// 単独で追える必要があるためである（監査ログ画面 GuiDesign.md 5.7 の
// 「操作」による絞り込み）。user.update だけにすると detail の中身を
// 開かないとロール変更を見つけられない。
//
// detail には**変更した項目だけ**を前後の形で入れる。送られなかった項目まで
// 並べると、何が変わったのかが読み取れなくなる。
func recordUserUpdate(
	ctx context.Context, q gen.Querier, rec *audit.Recorder,
	id string, cur gen.GetAdminUserRow, f updateUserFields,
) error {
	if !f.changed() {
		// 何も送られていない PATCH。version は進むが、記録することが無い。
		return nil
	}

	detail := map[string]any{}
	if f.displayName != nil {
		detail["display_name"] = changeDetail(cur.DisplayName, *f.displayName)
	}
	if f.email != nil {
		detail["email"] = changeDetail(cur.Email, *f.email)
	}
	if f.systemRole != nil {
		detail["system_role"] = changeDetail(cur.SystemRole, *f.systemRole)
	}
	if f.isActive != nil {
		detail["is_active"] = map[string]any{"before": cur.IsActive, "after": *f.isActive}
	}

	if err := rec.Record(ctx, q, audit.Entry{
		Action:     audit.UserUpdate,
		Result:     audit.Success,
		TargetType: "app_user",
		TargetID:   id,
		Detail:     detail,
	}); err != nil {
		return err
	}

	if f.systemRole == nil || *f.systemRole == cur.SystemRole {
		return nil
	}
	return rec.Record(ctx, q, audit.Entry{
		Action:     audit.RoleChange,
		Result:     audit.Success,
		TargetType: "app_user",
		TargetID:   id,
		Detail:     map[string]any{"system_role": changeDetail(cur.SystemRole, *f.systemRole)},
	})
}

// changeDetail は監査ログの detail に入れる変更前後。
func changeDetail(before, after string) map[string]any {
	return map[string]any{"before": before, "after": after}
}

// ── 入力の検証（ApiDesign.md 6.4）─────────────────────────────

// validateUpdateUser は 6.4 の変更可能項目を検証する。
//
// **検証の内容は 6.2（作成）と同じでなければならない。** 作成時に弾いた値が
// 更新で通ると、同じ表に別の規則で入った行が混ざる。display_name の長さと
// email の形式は validateEmail を含めて 6.2 と共有している。
//
// **すべての項目を見てから返す**（2.5 の details は項目ごとに紐づける）。
func validateUpdateUser(req updateUserRequest) (updateUserFields, *apierr.Error) {
	var details []apierr.Detail
	var f updateUserFields

	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		switch n := utf8.RuneCountInString(name); {
		case n < displayNameMinLen:
			details = append(details, apierr.Detail{
				Field: "display_name", Code: "required", Message: "表示名を入力してください",
			})
		case n > displayNameMaxLen:
			details = append(details, apierr.Detail{
				Field: "display_name", Code: "too_long",
				Message: fmt.Sprintf("表示名は%d文字以内で入力してください", displayNameMaxLen),
			})
		default:
			f.displayName = &name
		}
	}

	if req.Email != nil {
		email := strings.TrimSpace(*req.Email)
		if d := validateEmail(email); d != nil {
			details = append(details, *d)
		} else {
			f.email = &email
		}
	}

	if req.SystemRole != nil {
		switch *req.SystemRole {
		case systemRoleOperator, systemRoleAdministrator:
			f.systemRole = req.SystemRole
		default:
			details = append(details, apierr.Detail{
				Field: "system_role", Code: "invalid",
				Message: "system_role は operator または administrator で指定してください",
			})
		}
	}

	f.isActive = req.IsActive

	if len(details) > 0 {
		return updateUserFields{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return f, nil
}

// ── 応答への写し ────────────────────────────────────────────

// classifyUserUpdateMiss は UpdateAdminUserProfile が 0 行だった理由を分ける。
func classifyUserUpdateMiss(ctx context.Context, q gen.Querier, id string) error {
	exists, err := q.AppUserExists(ctx, id)
	if err != nil {
		return fmt.Errorf("ユーザー %q の存在を確認できない: %w", id, err)
	}
	if exists {
		return errVersionConflict
	}
	return pgx.ErrNoRows
}

// writeUserUpdateError は 6.4 / 6.5 の失敗を応答へ写す。
//
// ガード（self_modification_forbidden / last_administrator）は
// *apierr.Error のままトランザクションの外へ出てくるので、そのまま書く。
func writeUserUpdateError(
	w http.ResponseWriter, r *http.Request, id string, email *string, err error,
) {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		apierr.Write(w, r, apiErr)
		return
	}
	if errors.Is(err, errVersionConflict) {
		apierr.Write(w, r, apierr.New(apierr.Conflict).
			WithMessage("他の利用者がこのユーザーを更新しました。内容を読み直してからやり直してください").
			WithCause(fmt.Errorf("ユーザー %q の version が一致しない", id)))
		return
	}
	// **メールの重複は 6.2 と同じ already_exists に写す。** 6.4 の表は
	// 「409 conflict」とだけ書いているが、2.5.1 は already_exists を
	// 「一意なキーが既に使われている」と定めており、6.2 は同じ状況で
	// already_exists を返す。呼び出し側が「メールが使われている」を
	// 1つのコードで扱えるようにする（手順13a の判断）。
	if email != nil && isEmailConflict(err) {
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage(fmt.Sprintf("メールアドレス %s は既に使われています", *email)).
			WithDetails(apierr.Detail{
				Field: "email", Code: "already_exists",
				Message: "別のメールアドレスを指定してください",
			}))
		return
	}
	writeUserDetailError(w, r, id, err)
}

// ── 小さな写し ─────────────────────────────────────────────

// nargText は *string を sqlc.narg の引数へ写す。nil は SQL の NULL
// （＝COALESCE により現在値の据え置き）になる。
//
// **optionalText（projects_create.go）とは別物である。** あちらは
// 「空文字を NULL に倒す」——任意入力の欄に何も書かれなかったことを表す。
// こちらは「送られなかった」を NULL に倒すもので、空文字は 422 で弾かれる
// ため、そもそもここへ来ない。
func nargText(s *string) pgtype.Text {
	if s == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *s, Valid: true}
}

// nargBool は *bool を sqlc.narg の引数へ写す。
func nargBool(b *bool) pgtype.Bool {
	if b == nil {
		return pgtype.Bool{}
	}
	return pgtype.Bool{Bool: *b, Valid: true}
}
