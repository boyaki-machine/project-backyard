// 評価記録（evaluation）。Delegation.md 7章・12.2。pb-248。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/evaluation   delegation.evaluate
//
// **{seq} は委任の根**（plan・delegation と同じ形。4.4 の root_ticket_id が
// UNIQUE）。評価対象は本文の target_kind で選ぶ——plan なら plan_version も渡し、
// deliverable なら根の最新の完了レポート（agent_report）が暗黙の対象になる。
//
// **unable は確認待ち（evaluation_unable）を立てる**（7.2・8.1 の6）。
// **往復の上限超過は確認待ち（budget_exceeded）を立て、委任を paused にする**
// （7.2・8.1 の5・8.2「PB が立てた確認待ちは、主状態を停止にする」）。
// どちらも `confirmation.go` の `raiseSystemConfirmation` を直接呼ぶ——
// evaluation_unable は評価エージェントが立てる理由なので、人の経路を締める
// `raiseConfirmation`（REST）は通らない（身元確認がここで既に済んでいる）。
package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// evaluationTargetPlan / evaluationTargetDeliverable は 7.2 の評価の対象。
const (
	evaluationTargetPlan        = "plan"
	evaluationTargetDeliverable = "deliverable"
)

// evaluationVerdictPass〜Unable は 7.2 の合否。
const (
	evaluationVerdictPass   = "pass"
	evaluationVerdictFail   = "fail"
	evaluationVerdictUnable = "unable"
)

// evaluationAspectPurposeDoD / evaluationAspectValues は 7.2 が定める最低2観点
// （「目的・完了条件」と「価値観・世界観」）。findings[].aspect の値域に使う
// キーで、観点自体は増やしてよい（7.2「少なくとも」）。
const (
	evaluationAspectPurposeDoD = "purpose_and_dod"
	evaluationAspectValues     = "values"
)

// evaluationRoundLimit は 7.2「往復の上限（初期値3回）」。
//
// **固定値である。** delegation_plan.go の閾値（20% / 5件）と同じ理由で、
// プロジェクト設定化（画面を第一の口に。Design.md 10.3）は別チケットへ送る。
const evaluationRoundLimit = 3

// evaluationFinding は findings[] の1件（7.2・7.3）。
type evaluationFinding struct {
	Aspect  string `json:"aspect"`
	Verdict string `json:"verdict"`
	NoteMd  string `json:"note_md"`
}

// evaluationView は評価1件の応答。
type evaluationView struct {
	ID          string   `json:"id"`
	TargetKind  string   `json:"target_kind"`
	PlanID      *string  `json:"plan_id"`
	PlanVersion *int32   `json:"plan_version"`
	ReportID    *string  `json:"agent_report_id"`
	Round       int32    `json:"round"`
	Verdict     string   `json:"verdict"`
	Findings    any      `json:"findings"`
	ClassCheck  *string  `json:"class_check"`
	Evaluator   actorRef `json:"evaluator"`
	TokensUsed  *int64   `json:"tokens_used"`
	CreatedAt   Time     `json:"created_at"`
}

// ── POST /tickets/{seq}/evaluation ─────────────────────────────

type submitEvaluationRequest struct {
	TargetKind  string          `json:"target_kind"`
	PlanVersion *int32          `json:"plan_version"`
	Verdict     string          `json:"verdict"`
	Findings    json.RawMessage `json:"findings"`
	ClassCheck  string          `json:"class_check"`
	TokensUsed  *int64          `json:"tokens_used"`
}

func (h *handler) submitEvaluation(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/evaluation")
	if !ok {
		return
	}

	var req submitEvaluationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	findings, e := validateSubmitEvaluation(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view     evaluationView
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
		if deleg.EvaluatorActorID != scope.actorID {
			rejected = apierr.New(apierr.Forbidden).
				WithMessage("この委任の評価エージェントだけが評価を書けます")
			return errTicketReference
		}
		if deleg.Status == "paused" || deleg.Status == "ended" {
			rejected = delegationForbidden(scope.seq,
				fmt.Sprintf("委任が%sのあいだは評価できません", delegationStatusLabel(deleg.Status)))
			return errTicketReference
		}
		// **確認待ちが開いているあいだは評価を書けない**（8.2・5章。pb-249）。
		if open, oerr := q.HasOpenConfirmation(ctx, deleg.ID); oerr != nil {
			return fmt.Errorf("確認待ちを確認できない: %w", oerr)
		} else if open {
			rejected = delegationForbidden(scope.seq, "確認待ちに答えられるまで、評価を書けません")
			return errTicketReference
		}

		var planID, reportID pgtype.Text
		switch req.TargetKind {
		case evaluationTargetPlan:
			plan, err := q.GetDelegationPlanByVersion(ctx, gen.GetDelegationPlanByVersionParams{
				DelegationID: deleg.ID, Version: *req.PlanVersion,
			})
			if err != nil {
				if err == pgx.ErrNoRows {
					rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
						Field: "plan_version", Code: "not_found", Message: "指定した版が見つかりません",
					})
					return errTicketReference
				}
				return fmt.Errorf("計画の版を読めない: %w", err)
			}
			if plan.Status != "proposed" {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "plan_version", Code: "invalid",
					Message: "この版はすでに判定されています",
				})
				return errTicketReference
			}
			planID = pgtype.Text{String: plan.ID, Valid: true}
		case evaluationTargetDeliverable:
			report, err := q.GetLatestAgentReportForTicket(ctx, ticketID)
			if err != nil {
				if err == pgx.ErrNoRows {
					rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
						Field: "target_kind", Code: "invalid",
						Message: "根にはまだ完了レポートが提出されていません",
					})
					return errTicketReference
				}
				return fmt.Errorf("完了レポートを読めない: %w", err)
			}
			if _, err := q.GetEvaluationVerdictForReport(ctx, pgtype.Text{String: report.ID, Valid: true}); err == nil {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "target_kind", Code: "invalid",
					Message: "この完了レポートはすでに評価済みです。main の出し直しを待ってください",
				})
				return errTicketReference
			} else if err != pgx.ErrNoRows {
				return fmt.Errorf("評価済みかを確認できない: %w", err)
			}
			reportID = pgtype.Text{String: report.ID, Valid: true}
		}

		prior, err := q.CountEvaluationsSinceLastPass(ctx, gen.CountEvaluationsSinceLastPassParams{
			DelegationID: deleg.ID, TargetKind: req.TargetKind,
		})
		if err != nil {
			return fmt.Errorf("往復の回数を数えられない: %w", err)
		}
		round := int32(prior) + 1

		evalInfo, err := q.GetAgentRuntimeInfo(ctx, deleg.EvaluatorActorID)
		if err != nil {
			return fmt.Errorf("評価エージェントの情報を読めない: %w", err)
		}
		mainInfo, err := q.GetAgentRuntimeInfo(ctx, deleg.MainActorID)
		if err != nil {
			return fmt.Errorf("main の情報を読めない: %w", err)
		}

		if err := q.CreateEvaluation(ctx, gen.CreateEvaluationParams{
			ID: id, DelegationID: deleg.ID, TargetKind: req.TargetKind,
			PlanID: planID, AgentReportID: reportID, Round: round, Verdict: req.Verdict,
			Findings:              findings,
			ClassCheck:            textOrNullPg(req.ClassCheck),
			EvaluatorActorID:      scope.actorID,
			EvaluatorClientKind:   pgtype.Text{String: evalInfo.ClientKind, Valid: true},
			EvaluatorModelName:    evalInfo.ModelName,
			EvaluatorModelVersion: evalInfo.ModelVersion,
			MainClientKind:        pgtype.Text{String: mainInfo.ClientKind, Valid: true},
			MainModelName:         mainInfo.ModelName,
			MainModelVersion:      mainInfo.ModelVersion,
			TokensUsed:            int8OrNull(req.TokensUsed),
		}); err != nil {
			return fmt.Errorf("評価を保存できない: %w", err)
		}

		// **委任が計画中なら、計画の版を反映しない**（6.1・4.3）。初版と、範囲の
		// 変更（6.3。提出の時点で委任を計画中へ戻している）は、どちらも
		// 「評価が先、PM が後」——計画の accepted/rejected は PM の承認・却下
		// （delegation_approval.go。pb-260）が決める。版番号で分けると、範囲の
		// 変更の版が評価の合格だけで accepted になり、PM を通らずに範囲が変わる。
		if req.TargetKind == evaluationTargetPlan && deleg.Status != "planning" {
			switch req.Verdict {
			case evaluationVerdictPass:
				if _, err := q.AcceptDelegationPlan(ctx, planID.String); err != nil {
					return fmt.Errorf("計画を反映できない: %w", err)
				}
			case evaluationVerdictFail:
				if _, err := q.RejectDelegationPlan(ctx, planID.String); err != nil {
					return fmt.Errorf("計画を不合格にできない: %w", err)
				}
			}
		}

		// **unable は確認待ち（evaluation_unable）を立てる**（7.2・8.1 の6）。
		// 主状態は動かさない——評価が立てる確認待ちは、関門（開いているか）
		// だけで main・評価の書き込みを止める（4.3・8.2）。
		if req.Verdict == evaluationVerdictUnable {
			if err := raiseSystemConfirmation(ctx, q, deleg.ID, ticketID,
				confirmationReasonEvaluationUnable,
				unableConfirmationQuestion(req.TargetKind), scope.actorID,
			); err != nil {
				return err
			}
		}
		// **往復の上限超過は確認待ち（budget_exceeded）を立て、委任を paused
		// にする**（7.2・8.1 の5。8.2「PB が立てた確認待ちは、主状態を停止に
		// する」）。raised_by は、この条件を実際に検出した評価の提出者
		// （scope.actorID）に当てる——専用の system actor はまだ無い（finding）。
		// 上限は budget.rounds があればそれ、無ければ固定値（12.1。pb-253）。
		if req.Verdict == evaluationVerdictFail && round >= roundLimitOf(deleg.Scope) {
			if _, err := q.SetDelegationStatus(ctx, gen.SetDelegationStatusParams{
				ID: deleg.ID, Status: "paused",
			}); err != nil {
				return fmt.Errorf("委任を停止できない: %w", err)
			}
			if err := raiseSystemConfirmation(ctx, q, deleg.ID, ticketID,
				confirmationReasonBudgetExceeded,
				roundLimitConfirmationQuestion(req.TargetKind, round), scope.actorID,
			); err != nil {
				return err
			}
		}

		// **評価のトークンも予算に数える**（12.1。pb-253）。往復の上限で既に
		// 停止したなら重ねて立てない。
		if !(req.Verdict == evaluationVerdictFail && round >= roundLimitOf(deleg.Scope)) {
			reasons, berr := checkDelegationBudget(ctx, q, deleg.ID, deleg.Scope, "", budgetCheck{checkTokens: true})
			if berr != nil {
				return berr
			}
			if len(reasons) > 0 {
				if berr := tripDelegationBudget(ctx, q, deleg.ID, ticketID, scope.actorID, reasons); berr != nil {
					return berr
				}
			}
		}

		row, err := q.GetEvaluationByID(ctx, id)
		if err != nil {
			return fmt.Errorf("作成した評価を読めない: %w", err)
		}
		view = evaluationViewOf(row)

		field := "evaluation"
		newVal := fmt.Sprintf("%s round%d: %s", req.TargetKind, round, req.Verdict)
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
		"/api/v1/projects/%s/tickets/%d/evaluation/%s", scope.key, scope.seq, view.ID))
	WriteJSON(w, http.StatusCreated, view)
}

// validateSubmitEvaluation は本文を検証し、保存用の findings（jsonb）を返す。
func validateSubmitEvaluation(req submitEvaluationRequest) ([]byte, *apierr.Error) {
	var details []apierr.Detail

	switch req.TargetKind {
	case evaluationTargetPlan, evaluationTargetDeliverable:
	case "":
		details = append(details, apierr.Detail{Field: "target_kind", Code: "required"})
	default:
		details = append(details, apierr.Detail{
			Field: "target_kind", Code: "invalid",
			Message: "target_kind は plan / deliverable のいずれかです",
		})
	}
	if req.TargetKind == evaluationTargetPlan && (req.PlanVersion == nil || *req.PlanVersion < 1) {
		details = append(details, apierr.Detail{
			Field: "plan_version", Code: "required", Message: "plan 対象には plan_version が要ります",
		})
	}
	if req.TargetKind == evaluationTargetDeliverable && req.PlanVersion != nil {
		details = append(details, apierr.Detail{
			Field: "plan_version", Code: "invalid", Message: "deliverable 対象には plan_version を指定しません",
		})
	}

	switch req.Verdict {
	case evaluationVerdictPass, evaluationVerdictFail, evaluationVerdictUnable:
	case "":
		details = append(details, apierr.Detail{Field: "verdict", Code: "required"})
	default:
		details = append(details, apierr.Detail{
			Field: "verdict", Code: "invalid",
			Message: "verdict は pass / fail / unable のいずれかです",
		})
	}

	if req.ClassCheck != "" {
		switch req.ClassCheck {
		case "agree", "stricter", "looser":
		default:
			details = append(details, apierr.Detail{
				Field: "class_check", Code: "invalid",
				Message: "class_check は agree / stricter / looser のいずれかです",
			})
		}
	}

	var findings []evaluationFinding
	if len(req.Findings) == 0 {
		details = append(details, apierr.Detail{Field: "findings", Code: "required"})
	} else if err := json.Unmarshal(req.Findings, &findings); err != nil {
		details = append(details, apierr.Detail{
			Field: "findings", Code: "invalid", Message: "findings は配列で指定してください",
		})
	} else {
		seenAspect := make(map[string]bool, len(findings))
		hasFailNote := false
		for i, f := range findings {
			if f.Aspect == "" {
				details = append(details, apierr.Detail{Field: fmt.Sprintf("findings[%d].aspect", i), Code: "required"})
			}
			seenAspect[f.Aspect] = true
			switch f.Verdict {
			case evaluationVerdictPass, evaluationVerdictFail:
			default:
				details = append(details, apierr.Detail{
					Field: fmt.Sprintf("findings[%d].verdict", i), Code: "invalid",
					Message: "findings[].verdict は pass / fail のいずれかです",
				})
			}
			if f.Verdict == evaluationVerdictFail && f.NoteMd != "" {
				hasFailNote = true
			}
		}
		// **7.2「少なくとも『目的・完了条件』と『価値観・世界観』の2観点」。**
		if !seenAspect[evaluationAspectPurposeDoD] || !seenAspect[evaluationAspectValues] {
			details = append(details, apierr.Detail{
				Field: "findings", Code: "invalid",
				Message: fmt.Sprintf("findings には少なくとも %s と %s の観点が要ります",
					evaluationAspectPurposeDoD, evaluationAspectValues),
			})
		}
		// **不合格には指摘を必ず書く**（7.2）。
		if req.Verdict == evaluationVerdictFail && !hasFailNote {
			details = append(details, apierr.Detail{
				Field: "findings", Code: "required",
				Message: "不合格のときは、不合格にした観点の findings[].note_md に指摘を書いてください",
			})
		}
	}

	if len(details) > 0 {
		return nil, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return []byte(req.Findings), nil
}

// unableConfirmationQuestion / roundLimitConfirmationQuestion は、評価が
// evaluation_unable・budget_exceeded の確認待ちを立てるときの問い（8.1）。
func unableConfirmationQuestion(targetKind string) string {
	return fmt.Sprintf("評価エージェントが判断できませんでした（対象: %s）。どう進めるか指示してください。", targetKind)
}

func roundLimitConfirmationQuestion(targetKind string, round int32) string {
	return fmt.Sprintf(
		"同じ対象（%s）への不合格が%d回目に達しました。委任を一時停止しています。どう進めるか指示してください。",
		targetKind, round)
}

// rootHasUnpassedEvaluation は ticket_workflow.go の検証8 が使う
// （Delegation.md 5章「PM：根を受け入れる（完了）」・7.2）。
//
// **このチケットが委任の根でなければ false。** 委任に属さないチケットと、
// 木の中の子（D2 の案a）はこの検証の対象ではない。
//
// **根であっても、まだ完了レポートが1件も無ければ true（合格していない）。**
// 「成果が無い」は「合格した成果がある」の否定として扱う——根の受け入れは
// 成果の評価を前提とするので、評価の対象が無い状態を素通りさせない。
func rootHasUnpassedEvaluation(ctx context.Context, q gen.Querier, ticketID string) (bool, error) {
	if _, err := q.GetDelegationByRootTicket(ctx, ticketID); err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("委任を読めない: %w", err)
	}

	report, err := q.GetLatestAgentReportForTicket(ctx, ticketID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return true, nil
		}
		return false, fmt.Errorf("完了レポートを読めない: %w", err)
	}

	verdict, err := q.GetEvaluationVerdictForReport(ctx, pgtype.Text{String: report.ID, Valid: true})
	if err != nil {
		if err == pgx.ErrNoRows {
			return true, nil
		}
		return false, fmt.Errorf("評価を読めない: %w", err)
	}
	return verdict != evaluationVerdictPass, nil
}

// rootReportEvaluationPending は reports.go の根向けの追加ゲートが使う
// （Delegation.md 5章「main：成果の提出（根の完了レポート）」）。
//
// **「評価待ち」だけを true にする。** 最新の完了レポートにまだ評価が無ければ
// pending。評価が付いていれば、pass でも fail（差し戻し）でも false——
// どちらも次の提出が許される側である（fail は出し直し、pass は新しい周期）。
// 完了レポートが1件も無ければ、これが最初の提出なので false。
func rootReportEvaluationPending(ctx context.Context, q gen.Querier, ticketID string) (bool, error) {
	report, err := q.GetLatestAgentReportForTicket(ctx, ticketID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return false, nil
		}
		return false, fmt.Errorf("完了レポートを読めない: %w", err)
	}
	if _, err := q.GetEvaluationVerdictForReport(ctx, pgtype.Text{String: report.ID, Valid: true}); err != nil {
		if err == pgx.ErrNoRows {
			return true, nil
		}
		return false, fmt.Errorf("評価を読めない: %w", err)
	}
	return false, nil
}

func evaluationViewOf(row gen.GetEvaluationByIDRow) evaluationView {
	var findings any
	_ = json.Unmarshal(row.Findings, &findings)
	v := evaluationView{
		ID: row.ID, TargetKind: row.TargetKind, Round: row.Round,
		Verdict: row.Verdict, Findings: findings,
		Evaluator: actorRef{
			ID: row.EvaluatorActorID, Kind: row.EvaluatorActorKind, DisplayName: row.EvaluatorActorName,
		},
		CreatedAt: Time(row.CreatedAt),
	}
	if row.PlanID.Valid {
		v.PlanID = &row.PlanID.String
	}
	if row.PlanVersion.Valid {
		v.PlanVersion = &row.PlanVersion.Int32
	}
	if row.AgentReportID.Valid {
		v.ReportID = &row.AgentReportID.String
	}
	if row.ClassCheck.Valid {
		v.ClassCheck = &row.ClassCheck.String
	}
	if row.TokensUsed.Valid {
		v.TokensUsed = &row.TokensUsed.Int64
	}
	return v
}
