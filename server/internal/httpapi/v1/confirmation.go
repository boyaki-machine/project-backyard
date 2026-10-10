// 確認待ち（confirmation）。Delegation.md 8章・12.2。pb-249。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/confirmation         ticket.create（main/評価の身元確認）
//	POST /api/v1/projects/{key}/tickets/{seq}/confirmation/{id}/answer  delegation.manage
//
// **{seq} は委任の根**（plan・evaluation と同じ形）。確認待ちは根の下の
// どのチケットに対して立ってもよいが、起票元は常に main か評価（身元確認は
// ハンドラ内）。
//
// **関門は列に持たず、開いている確認待ちから導く**（Delegation.md 4.3）。
// `hasOpenConfirmation` が、他のゲート（delegationGate・評価待ち）と同じ場所
// （tickets_create.go の12.2・delegation_plan.go・evaluation.go・reports.go）
// から呼ばれる。hearing（8.1 の8）だけは関門に数えない（8.2）。
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// confirmationReason* は8.1の条件キー（1〜8のうち、PBが表として持つもの）。
const (
	confirmationReasonScopeChange          = "scope_change"          // 1
	confirmationReasonIrreversibleExternal = "irreversible_external" // 2
	confirmationReasonCharterChange        = "charter_change"        // 3
	confirmationReasonSensitiveArea        = "sensitive_area"        // 4
	confirmationReasonBudgetExceeded       = "budget_exceeded"       // 5（往復の上限超過を含む）
	confirmationReasonEvaluationUnable     = "evaluation_unable"     // 6
	confirmationReasonCheckpoint           = "checkpoint"            // 7
	confirmationReasonHearing              = "hearing"               // 8
)

var confirmationReasons = map[string]bool{
	confirmationReasonScopeChange:          true,
	confirmationReasonIrreversibleExternal: true,
	confirmationReasonCharterChange:        true,
	confirmationReasonSensitiveArea:        true,
	confirmationReasonBudgetExceeded:       true,
	confirmationReasonEvaluationUnable:     true,
	confirmationReasonCheckpoint:           true,
	confirmationReasonHearing:              true,
}

// confirmationReasonActor は、このエンドポイントを main が立てられる理由を
// 表す（8.1「誰が立てるか」のうち main の列）。
//
// **評価が立てる evaluation_unable（6）は、ここに無い。** `pb_submit_evaluation`
// が verdict=unable を受けたときに `raiseSystemConfirmation` を直接呼ぶ
// （evaluation.go）——評価エージェントのトークンは `ticket.create` を持たず
// （7.1「起票・編集・遷移は要らない」）、この公開エンドポイントを通れない。
//
// **PB 自身（system）が立てるもの（5・7）も、このエンドポイントの外から
// 呼ぶ**（evaluation.go の round 超過、将来の予算計測・周期の要約）。
var confirmationReasonActor = map[string]string{
	confirmationReasonScopeChange:          "main",
	confirmationReasonIrreversibleExternal: "main",
	confirmationReasonCharterChange:        "main",
	confirmationReasonSensitiveArea:        "main",
	confirmationReasonHearing:              "main",
}

// confirmationView は確認待ち1件の応答。
type confirmationView struct {
	ID         string    `json:"id"`
	TicketSeq  int32     `json:"ticket_seq"`
	Reason     string    `json:"reason"`
	QuestionMd string    `json:"question_md"`
	Options    any       `json:"options"`
	Status     string    `json:"status"`
	AnswerMd   *string   `json:"answer_md"`
	RaisedBy   actorRef  `json:"raised_by"`
	AnsweredBy *actorRef `json:"answered_by"`
	CreatedAt  Time      `json:"created_at"`
	AnsweredAt *Time     `json:"answered_at"`
}

// ── POST /tickets/{seq}/confirmation ───────────────────────────

type raiseConfirmationRequest struct {
	Reason     string          `json:"reason"`
	QuestionMd string          `json:"question_md"`
	Options    json.RawMessage `json:"options"`
}

func (h *handler) raiseConfirmation(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/confirmation")
	if !ok {
		return
	}

	var req raiseConfirmationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateRaiseConfirmation(req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view     confirmationView
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

		switch confirmationReasonActor[req.Reason] {
		case "main":
			if deleg.MainActorID != scope.actorID {
				rejected = apierr.New(apierr.Forbidden).
					WithMessage("この理由の確認待ちは、この委任の main だけが立てられます")
				return errTicketReference
			}
		case "evaluator":
			if deleg.EvaluatorActorID != scope.actorID {
				rejected = apierr.New(apierr.Forbidden).
					WithMessage("この理由の確認待ちは、この委任の評価エージェントだけが立てられます")
				return errTicketReference
			}
		default:
			// budget_exceeded・checkpoint は PB 自身が立てる（このエンドポイントの外）。
			rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "reason", Code: "invalid",
				Message: "この理由は PB が自動的に立てるため、ここからは指定できません",
			})
			return errTicketReference
		}

		options := req.Options
		if len(options) == 0 {
			options = json.RawMessage(`[]`)
		}
		if err := q.CreateConfirmation(ctx, gen.CreateConfirmationParams{
			ID: id, DelegationID: deleg.ID, TicketID: ticketID,
			Reason: req.Reason, QuestionMd: req.QuestionMd,
			Options: options, RaisedBy: scope.actorID,
		}); err != nil {
			return fmt.Errorf("確認待ちを作成できない: %w", err)
		}

		row, err := q.GetConfirmationByID(ctx, id)
		if err != nil {
			return fmt.Errorf("作成した確認待ちを読めない: %w", err)
		}
		view = confirmationViewOf(row)

		field := "confirmation"
		newVal := fmt.Sprintf("%s: %s", req.Reason, truncateForActivity(req.QuestionMd))
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
		"/api/v1/projects/%s/tickets/%d/confirmation/%s", scope.key, scope.seq, view.ID))
	WriteJSON(w, http.StatusCreated, view)
}

func validateRaiseConfirmation(req raiseConfirmationRequest) *apierr.Error {
	var details []apierr.Detail
	if !confirmationReasons[req.Reason] {
		details = append(details, apierr.Detail{
			Field: "reason", Code: "invalid",
			Message: "reason は8.1の条件キーのいずれかです",
		})
	}
	if req.QuestionMd == "" {
		details = append(details, apierr.Detail{Field: "question_md", Code: "required"})
	}
	if len(req.Options) > 0 {
		var v any
		if err := json.Unmarshal(req.Options, &v); err != nil {
			details = append(details, apierr.Detail{
				Field: "options", Code: "invalid", Message: "options は配列で指定してください",
			})
		} else if _, ok := v.([]any); !ok {
			details = append(details, apierr.Detail{
				Field: "options", Code: "invalid", Message: "options は配列で指定してください",
			})
		}
	}
	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}

// ── POST /tickets/{seq}/confirmation/{id}/answer ───────────────

type answerConfirmationRequest struct {
	AnswerMd string `json:"answer_md"`
}

// answerConfirmation はPMの回答を記録する（8.2）。
//
// **主状態は動かさない。** `budget_exceeded`（PBが立てて paused にしたもの）は、
// 答えるだけで再開してよいものではない（8.2 の最後の箇条書き）——
// 再開は別の操作である（delegation_pause.go の resumeDelegation。pb-253）。
func (h *handler) answerConfirmation(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/confirmation/{id}/answer")
	if !ok {
		return
	}
	confirmationID := chi.URLParam(r, "id")

	var req answerConfirmationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.AnswerMd == "" {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "answer_md", Code: "required",
		}))
		return
	}

	rec := activity.FromRequest(r)
	var (
		view     confirmationView
		rejected *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if _, err := q.GetDelegationByRootTicket(ctx, ticketID); err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "seq", Code: "invalid", Message: "このチケットは委任の根ではありません",
				})
				return errTicketReference
			}
			return fmt.Errorf("委任を読めない: %w", err)
		}

		before, err := q.GetConfirmationByID(ctx, confirmationID)
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "id", Code: "not_found", Message: "指定した確認待ちが見つかりません",
				})
				return errTicketReference
			}
			return fmt.Errorf("確認待ちを読めない: %w", err)
		}
		if before.Status != "open" {
			rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "id", Code: "invalid", Message: "この確認待ちは既に答えられています",
			})
			return errTicketReference
		}

		if n, err := q.AnswerConfirmation(ctx, gen.AnswerConfirmationParams{
			ID: confirmationID, AnswerMd: pgtype.Text{String: req.AnswerMd, Valid: true},
			AnsweredBy: pgtype.Text{String: scope.actorID, Valid: true},
		}); err != nil {
			return fmt.Errorf("確認待ちへの回答を保存できない: %w", err)
		} else if n == 0 {
			rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "id", Code: "invalid", Message: "この確認待ちは既に答えられています",
			})
			return errTicketReference
		}

		row, err := q.GetConfirmationByID(ctx, confirmationID)
		if err != nil {
			return fmt.Errorf("回答した確認待ちを読めない: %w", err)
		}
		view = confirmationViewOf(row)

		field := "confirmation"
		newVal := "answered: " + truncateForActivity(req.AnswerMd)
		return rec.Record(ctx, q, activity.Entry{
			ProjectID: scope.projectID, EntityType: activity.EntityTicket,
			EntityID: ticketID, Action: activity.Update,
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
	WriteJSON(w, http.StatusOK, view)
}

func confirmationViewOf(row gen.GetConfirmationByIDRow) confirmationView {
	var options any
	_ = json.Unmarshal(row.Options, &options)
	v := confirmationView{
		ID: row.ID, TicketSeq: row.TicketSeq, Reason: row.Reason,
		QuestionMd: row.QuestionMd, Options: options, Status: row.Status,
		RaisedBy:  actorRef{ID: row.RaisedByID, Kind: row.RaisedByKind, DisplayName: row.RaisedByName},
		CreatedAt: Time(row.CreatedAt),
	}
	if row.AnswerMd.Valid {
		v.AnswerMd = &row.AnswerMd.String
	}
	if row.AnsweredByID.Valid {
		v.AnsweredBy = &actorRef{
			ID: row.AnsweredByID.String, Kind: row.AnsweredByKind.String, DisplayName: row.AnsweredByName.String,
		}
	}
	if row.AnsweredAt != nil {
		t := Time(*row.AnsweredAt)
		v.AnsweredAt = &t
	}
	return v
}

// truncateForActivity は activity.new_value に積む文字列を短く切る。
//
// **activity は一覧の要約欄であり、全文を持たない**（他の Create アクション
// と同じ扱い。delegation_plan.go の「v%d (%s)」と同様に短い値だけを残す）。
func truncateForActivity(s string) string {
	const max = 80
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max]) + "…"
}

// raiseSystemConfirmation は PB 自身（budget_exceeded・checkpoint）が確認待ちを
// 立てるときに使う内部ヘルパー（8.1 の5・7）。REST を経由しない——
// raiseConfirmation は人の経路（main・評価の身元確認）を締めるためのものだが、
// PB 自身の判定にはその確認は要らない（当てはまらない）。
//
// **evaluation.go の往復の上限超過から呼ぶ**（pb-248 の代替処理を置き換える）。
func raiseSystemConfirmation(
	ctx context.Context, q gen.Querier, delegationID, ticketID, reason, questionMd string, raisedBy string,
) error {
	if err := q.CreateConfirmation(ctx, gen.CreateConfirmationParams{
		ID: ulidgen.New(), DelegationID: delegationID, TicketID: ticketID,
		Reason: reason, QuestionMd: questionMd, Options: json.RawMessage(`[]`),
		RaisedBy: raisedBy,
	}); err != nil {
		return fmt.Errorf("確認待ちを作成できない: %w", err)
	}
	return nil
}
