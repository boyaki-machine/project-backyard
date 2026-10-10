// 引き継ぎノートと次の一手。Delegation.md 9.3・11章・12.2。pb-250。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/handoff   ticket.create（main の身元確認）
//
// **{seq} は委任の根**（plan・evaluation・confirmation と同じ形）。
// 引き継ぎノートは**追記のみ**で、最新の行が現在のノートになる（4.4）。
//
// **次の一手（9.3）は、この提出のたびに計算して返す。** PB が役割と段階から
// 1つ決める——main の自己申告（body.next_move）とは別の、PB 自身の判定である。
// 「パック・待ち受け・通知で同じものを返す」（9.3）うち、パック（pb-251）・
// 待ち受け・通知（D6 未決。10章）への配線はまだ無い（finding）。
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// delegationHandoffTrigger* は11.2の更新の区切り（5つ）。
const (
	handoffTriggerPlanVersion        = "plan_version"
	handoffTriggerEvaluation         = "evaluation"
	handoffTriggerReport             = "report"
	handoffTriggerBeforeConfirmation = "before_confirmation"
	handoffTriggerSessionEnd         = "session_end"
)

var handoffTriggers = map[string]bool{
	handoffTriggerPlanVersion:        true,
	handoffTriggerEvaluation:         true,
	handoffTriggerReport:             true,
	handoffTriggerBeforeConfirmation: true,
	handoffTriggerSessionEnd:         true,
}

// handoffView は引き継ぎノート1件の応答。
type handoffView struct {
	ID        string   `json:"id"`
	Version   int32    `json:"version"`
	Body      any      `json:"body"`
	Trigger   string   `json:"trigger"`
	WrittenBy actorRef `json:"written_by"`
	WrittenAt Time     `json:"written_at"`
	NextMove  string   `json:"next_move"`
}

// ── POST /tickets/{seq}/handoff ─────────────────────────────────

type updateHandoffRequest struct {
	Body    json.RawMessage `json:"body"`
	Trigger string          `json:"trigger"`
}

func (h *handler) updateHandoff(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/handoff")
	if !ok {
		return
	}

	var req updateHandoffRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateUpdateHandoff(req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view     handoffView
		rejected *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		deleg, err := q.GetDelegationByRootTicket(ctx, ticketID)
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "seq", Code: "invalid",
					Message: "このチケットは委任の根ではありません",
				})
				return errTicketReference
			}
			return fmt.Errorf("委任を読めない: %w", err)
		}
		if deleg.MainActorID != scope.actorID {
			rejected = apierr.New(apierr.Forbidden).
				WithMessage("この委任の main だけが引き継ぎノートを更新できます")
			return errTicketReference
		}
		// **ノートの更新そのものは、確認待ちが開いていても止めない**
		// （5章「main：ノートを書く・引き継ぎノートを更新する」は確認待ちの
		// 列でも可）。delegationGate・HasOpenConfirmation をここでは呼ばない。

		version, err := q.NextDelegationHandoffVersion(ctx, deleg.ID)
		if err != nil {
			return fmt.Errorf("引き継ぎノートの版を採番できない: %w", err)
		}
		if err := q.CreateDelegationHandoff(ctx, gen.CreateDelegationHandoffParams{
			ID: id, DelegationID: deleg.ID, Version: version,
			Body: req.Body, Trigger: req.Trigger, WrittenBy: scope.actorID,
		}); err != nil {
			return fmt.Errorf("引き継ぎノートを保存できない: %w", err)
		}

		move, err := computeNextMove(ctx, q, deleg.ID, roleMain)
		if err != nil {
			return fmt.Errorf("次の一手を計算できない: %w", err)
		}

		row, err := q.GetLatestDelegationHandoff(ctx, deleg.ID)
		if err != nil {
			return fmt.Errorf("作成した引き継ぎノートを読めない: %w", err)
		}
		view = handoffViewOf(row)
		view.NextMove = move

		field := "delegation_handoff"
		newVal := fmt.Sprintf("v%d (%s)", version, req.Trigger)
		return rec.Record(ctx, q, activity.Entry{
			ProjectID: scope.projectID, EntityType: activity.EntityTicket,
			EntityID: ticketID, Action: activity.Create,
			Field: &field, NewValue: &newVal,
		})
	})
	switch {
	case rejected != nil:
		apierr.Write(w, r, rejected)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.Header().Set("Location", fmt.Sprintf(
		"/api/v1/projects/%s/tickets/%d/handoff", scope.key, scope.seq))
	WriteJSON(w, http.StatusCreated, view)
}

func validateUpdateHandoff(req updateHandoffRequest) *apierr.Error {
	var details []apierr.Detail
	if len(req.Body) == 0 {
		details = append(details, apierr.Detail{Field: "body", Code: "required"})
	} else {
		var v any
		if err := json.Unmarshal(req.Body, &v); err != nil {
			details = append(details, apierr.Detail{
				Field: "body", Code: "invalid", Message: "body はオブジェクトで指定してください",
			})
		} else if _, ok := v.(map[string]any); !ok {
			details = append(details, apierr.Detail{
				Field: "body", Code: "invalid", Message: "body はオブジェクトで指定してください",
			})
		}
	}
	if !handoffTriggers[req.Trigger] {
		details = append(details, apierr.Detail{
			Field: "trigger", Code: "invalid",
			Message: "trigger は11.2の更新の区切りのいずれかです",
		})
	}
	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}

func handoffViewOf(row gen.GetLatestDelegationHandoffRow) handoffView {
	var body any
	_ = json.Unmarshal(row.Body, &body)
	return handoffView{
		ID: row.ID, Version: row.Version, Body: body, Trigger: row.Trigger,
		WrittenBy: actorRef{ID: row.WrittenByID, Kind: row.WrittenByKind, DisplayName: row.WrittenByName},
		WrittenAt: Time(row.WrittenAt),
	}
}

// ── 次の一手（Delegation.md 9.3）────────────────────────────────

// delegationRole は9.1の4つの役割。
type delegationRole string

const (
	roleMain      delegationRole = "main"
	roleSub       delegationRole = "sub"
	roleEvaluator delegationRole = "evaluator"
	roleReadonly  delegationRole = "readonly"
)

// computeNextMove は9.3の表のうち、既存のクエリで測れる範囲を判定する。
//
// **近似である（finding）。** 9.3 の「ヒアリングに答えが付いた」「前の版へ
// 差し戻された」「確認待ちに答えが付いた」は、本来「main がまだ見ていない」
// ことを条件に含むが、PB は「main が最後に何を見たか」を記録していない
// （引き継ぎノートの trigger は更新のたびに積むだけで、既読管理ではない）。
// ここでは「答えが存在する／直前の版が差し戻しである」という弱い条件で
// 近似し、本来より広く「次の一手」を示すことがある——main 自身の判断を
// 止めるものではなく、PB の提案に過ぎない（9.1「指示は強制の代わりではない」
// と同じ扱い）。
func computeNextMove(ctx context.Context, q gen.Querier, delegationID string, role delegationRole) (string, error) {
	deleg, err := q.GetDelegationByID(ctx, delegationID)
	if err != nil {
		return "", fmt.Errorf("委任を読めない: %w", err)
	}

	if deleg.Status == "paused" || deleg.Status == "ended" {
		return "何もしない（委任は" + delegationStatusLabel(deleg.Status) + "）", nil
	}
	if open, err := q.HasOpenConfirmation(ctx, delegationID); err != nil {
		return "", fmt.Errorf("確認待ちを確認できない: %w", err)
	} else if open {
		return "何もしない（確認待ちが開いている）", nil
	}

	switch role {
	case roleEvaluator:
		return nextMoveForEvaluator(ctx, q, delegationID)
	case roleMain:
		return nextMoveForMain(ctx, q, delegationID, deleg.Status)
	default:
		return "何もしない（この役割には次の一手が無い）", nil
	}
}

func nextMoveForEvaluator(ctx context.Context, q gen.Querier, delegationID string) (string, error) {
	plan, err := q.GetLatestDelegationPlan(ctx, delegationID)
	switch {
	case err == pgx.ErrNoRows:
		// 計画が1件も無い。
	case err != nil:
		return "", fmt.Errorf("最新の計画を読めない: %w", err)
	default:
		if plan.Status == "proposed" {
			return "対象（計画の版）を評価する", nil
		}
	}
	return "対象（完了レポート、あれば）を評価する", nil
}

func nextMoveForMain(ctx context.Context, q gen.Querier, delegationID, status string) (string, error) {
	plan, err := q.GetLatestDelegationPlan(ctx, delegationID)
	hasPlan := true
	if err == pgx.ErrNoRows {
		hasPlan = false
	} else if err != nil {
		return "", fmt.Errorf("最新の計画を読めない: %w", err)
	}

	if status == "planning" {
		hearingAnswered, herr := q.HasAnsweredConfirmation(ctx, gen.HasAnsweredConfirmationParams{
			DelegationID: delegationID, Reason: confirmationReasonHearing,
		})
		if herr != nil {
			return "", fmt.Errorf("ヒアリングの回答を確認できない: %w", herr)
		}
		if !hasPlan && !hearingAnswered {
			return "目的を掘り下げる問いを立てる（ヒアリング）", nil
		}
		return "答えを踏まえて次の問いを立てるか、掘り下げが足りたら計画を提出する", nil
	}

	// status == "approved"。
	if hasPlan && plan.Status == "rejected" {
		return "指摘（一覧）を直して出し直す", nil
	}
	if hasPlan && plan.RevertsTo.Valid {
		return "骨格に合わないチケットを畳み、引き継ぎノートを更新する", nil
	}
	if answered, aerr := q.HasAnsweredConfirmation(ctx, gen.HasAnsweredConfirmationParams{
		DelegationID: delegationID, Reason: confirmationReasonScopeChange,
	}); aerr == nil && answered {
		return "答え（本文）に従って再開する", nil
	} else if aerr != nil {
		return "", fmt.Errorf("確認待ちの回答を確認できない: %w", aerr)
	}
	return "引き継ぎノートの「次の一手」を続ける", nil
}
