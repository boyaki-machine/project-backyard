// プロジェクトの物理削除（ApiDesign.md 5.6.1）。
//
//	DELETE /api/v1/projects/{key}   5.6.1   アーカイブ済みのプロジェクトを消す
package v1

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// deleteProjectRequest は 5.6.1 の本文。confirm_key は消す相手をキーで名指しさせる確認である。
type deleteProjectRequest struct {
	ConfirmKey *string `json:"confirm_key"`
}

// errProjectNotArchived はアーカイブされていないプロジェクトを消そうとしたことを表す。
// トランザクションの中から外へ 409 を運ぶ。
var errProjectNotArchived = errors.New("プロジェクトがアーカイブされていない")

// deleteProject は DELETE /api/v1/projects/{key} を処理する（ApiDesign.md 5.6.1）。
//
// **確認の文字列をサーバでも照合する。** 画面の確認だけに頼ると、誤ったスクリプトや
// 再送が1回の DELETE で全部を消せてしまう。前後の空白だけは落とす（画面と同じ）。
//
// 消えるものは ON DELETE CASCADE に任せる。**プロジェクトに紐づくエージェントの
// actor 行だけは連鎖で消えない**（agent 行が消えるだけ）ので、消す前に引いておき、
// 同じトランザクションで消す。監査は削除と同じトランザクションで書く。
func (h *handler) deleteProject(w http.ResponseWriter, r *http.Request) {
	const route = "DELETE /projects/{key}"
	_, key, ok := projectRequestContext(w, r, route)
	if !ok {
		return
	}
	if h.tx == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s にトランザクション実行口が渡っていない", route)))
		return
	}

	var req deleteProjectRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.ConfirmKey == nil || strings.TrimSpace(*req.ConfirmKey) != key {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "confirm_key", Code: "mismatch",
			Message: "確認のため、プロジェクトキーを正しく入力してください",
		}))
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		target, err := q.LockProjectForDelete(ctx, key)
		if err != nil {
			return err
		}
		if target.Status != projectStatusArchived {
			return errProjectNotArchived
		}

		agentActors, err := q.ListProjectAgentActorIDs(ctx, pgtype.Text{String: target.ID, Valid: true})
		if err != nil {
			return fmt.Errorf("プロジェクト %q のエージェントを引けない: %w", key, err)
		}
		if _, err := q.DeleteProjectByKey(ctx, key); err != nil {
			return fmt.Errorf("プロジェクト %q を消せない: %w", key, err)
		}
		if len(agentActors) > 0 {
			if _, err := q.DeleteOrphanAgentActors(ctx, agentActors); err != nil {
				return fmt.Errorf("プロジェクト %q のエージェントのアクターを消せない: %w", key, err)
			}
		}

		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.ProjectDelete,
			Result:     audit.Success,
			TargetType: "project",
			TargetID:   target.ID,
			Detail: map[string]any{
				"key":          key,
				"name":         target.Name,
				"ticket_count": target.TicketCount,
			},
		})
	})
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, errProjectNotArchived):
		apierr.Write(w, r, apierr.New(apierr.Conflict).
			WithMessage("アーカイブしてから削除してください").
			WithCause(fmt.Errorf("プロジェクト %q: %w", key, err)))
	case errors.Is(err, pgx.ErrNoRows):
		writeProjectDetailError(w, r, key, err)
	default:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
	}
}
