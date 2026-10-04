// 計画の版（delegation_plan）。Delegation.md 6章・12.2。pb-247。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/plan                 ticket.create（+ main の身元確認）
//	POST /api/v1/projects/{key}/tickets/{seq}/plan/{version}/revert delegation.manage
//
// **{seq} は委任の根を指す。** 計画は委任そのものに属し、木のどこで起きた
// 変更かではなく「委任の進め方そのものをどう変えるか」を表すためである。
//
// **MCP には submit だけを開ける。** D7（Delegation.md 15章）の新設ツール
// `pb_submit_plan` がこの submit エンドポイントを叩く。revert は PM が画面・API
// から直接行う操作で、createDelegation（pb-246）と同じ理由で MCP ツールを持たない。
package v1

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"reflect"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// planClassMinor〜planClassScope は 6.3 の3段階。**この順が厳しさの順**
// （minor < structural < scope）——「main の申告と PB の判定の厳しい方を採る」
// をインデックスの大小比較1本で表すためにある。
const (
	planClassMinor      = "minor"
	planClassStructural = "structural"
	planClassScope      = "scope"
)

var planClassSeverity = map[string]int{
	planClassMinor:      0,
	planClassStructural: 1,
	planClassScope:      2,
}

// planBudgetShiftThreshold / planNewTicketThreshold は 6.3 の初期値。
//
// **固定値である（pb-247の既知の制約）。** 6.3 は「プロジェクトの設定にする」
// と定めるが、設定画面（`Design.md` 10.3）を伴う作り込みは本チケットの外に
// 出し、初期値だけをここに置く——Delegation.md 自身が「初期値は上のとおり」
// として 20% と5件を明示しているため、動かせる値として実装しないまま進めても
// 仕様に反しない。プロジェクト設定化は別チケットで行う。
const (
	planBudgetShiftThreshold = 0.20
	planNewTicketThreshold   = 5
)

// delegationPlanView は計画1件の応答（Delegation.md 6.2）。
type delegationPlanView struct {
	ID            string    `json:"id"`
	Version       int32     `json:"version"`
	Scope         any       `json:"scope"`
	Skeleton      any       `json:"skeleton"`
	BodyMd        string    `json:"body_md"`
	ReasonMd      string    `json:"reason_md"`
	DeclaredClass *string   `json:"declared_class"`
	MeasuredClass *string   `json:"measured_class"`
	Status        string    `json:"status"`
	RevertsTo     *int32    `json:"reverts_to"`
	SubmittedBy   actorRef  `json:"submitted_by"`
	SubmittedAt   Time      `json:"submitted_at"`
	DecidedBy     *actorRef `json:"decided_by"`
	DecidedAt     *Time     `json:"decided_at"`
}

// ── POST /tickets/{seq}/plan ───────────────────────────────────

type submitPlanRequest struct {
	Scope         json.RawMessage `json:"scope"`
	Skeleton      json.RawMessage `json:"skeleton"`
	BodyMd        string          `json:"body_md"`
	ReasonMd      string          `json:"reason_md"`
	DeclaredClass string          `json:"declared_class"`
}

func (h *handler) submitDelegationPlan(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/plan")
	if !ok {
		return
	}

	var req submitPlanRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateSubmitPlan(req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view     delegationPlanView
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
				WithMessage("この委任の main だけが計画を提出できます")
			return errTicketReference
		}
		if deleg.Status == "paused" || deleg.Status == "ended" {
			rejected = delegationForbidden(scope.seq,
				fmt.Sprintf("委任が%sのあいだは計画を提出できません", delegationStatusLabel(deleg.Status)))
			return errTicketReference
		}
		// **確認待ちが開いているあいだは計画を提出できない**（8.2・5章。pb-249）。
		if open, oerr := q.HasOpenConfirmation(ctx, deleg.ID); oerr != nil {
			return fmt.Errorf("確認待ちを確認できない: %w", oerr)
		} else if open {
			rejected = delegationForbidden(scope.seq, "確認待ちに答えられるまで、計画を提出できません")
			return errTicketReference
		}

		effectiveScope := req.Scope
		if len(effectiveScope) == 0 {
			effectiveScope = deleg.Scope
		}

		version, err := q.NextDelegationPlanVersion(ctx, deleg.ID)
		if err != nil {
			return fmt.Errorf("計画の版を採番できない: %w", err)
		}

		var measuredClass string
		if version > 1 {
			prev, err := q.GetLatestAcceptedDelegationPlan(ctx, deleg.ID)
			switch {
			case err == pgx.ErrNoRows:
				// 承認済みの前の版が無い（まだ初版が評価待ち）。判定規則を適用しない。
			case err != nil:
				return fmt.Errorf("直前の承認済み計画を読めない: %w", err)
			default:
				measuredClass = classifyPlanChange(
					prev.Scope, effectiveScope, prev.Skeleton, req.Skeleton)
				if measuredClass != planClassScope {
					n, err := q.CountTicketsInDelegationTreeSince(ctx, gen.CountTicketsInDelegationTreeSinceParams{
						RootID: ticketID, Since: prev.SubmittedAt,
					})
					if err != nil {
						return fmt.Errorf("起票数を数えられない: %w", err)
					}
					if n > planNewTicketThreshold && planClassSeverity[measuredClass] < planClassSeverity[planClassStructural] {
						measuredClass = planClassStructural
					}
				}
			}
		}

		finalClass := measuredClass
		if req.DeclaredClass != "" && (finalClass == "" || planClassSeverity[req.DeclaredClass] > planClassSeverity[finalClass]) {
			finalClass = req.DeclaredClass
		}

		status := "proposed"
		insertErr := q.CreateDelegationPlan(ctx, gen.CreateDelegationPlanParams{
			ID: id, DelegationID: deleg.ID, Version: version,
			Scope: effectiveScope, Skeleton: req.Skeleton,
			BodyMd: req.BodyMd, ReasonMd: req.ReasonMd,
			DeclaredClass: textOrNullPg(req.DeclaredClass),
			MeasuredClass: textOrNullPg(measuredClass),
			Status:        status,
			RevertsTo:     pgtype.Int4{},
			SubmittedBy:   scope.actorID,
		})
		if insertErr != nil {
			return fmt.Errorf("計画を保存できない: %w", insertErr)
		}

		// **軽微は main の判断で反映する**（6.3）。版が1件も承認されていない
		// 初版は、6.1 により評価が先なのでここでは反映しない。
		if version > 1 && finalClass == planClassMinor {
			if _, err := q.AcceptDelegationPlan(ctx, id); err != nil {
				return fmt.Errorf("計画を反映できない: %w", err)
			}
		}
		// **範囲の変更は、委任を計画中へ戻す**（6.3）。PM の事前承認を要るため。
		if finalClass == planClassScope {
			if _, err := q.SetDelegationStatus(ctx, gen.SetDelegationStatusParams{
				ID: deleg.ID, Status: "planning",
			}); err != nil {
				return fmt.Errorf("委任を計画中へ戻せない: %w", err)
			}
		}

		row, err := q.GetDelegationPlanByID(ctx, id)
		if err != nil {
			return fmt.Errorf("作成した計画を読めない: %w", err)
		}
		view = delegationPlanViewOf(row)

		field := "delegation_plan"
		newVal := fmt.Sprintf("v%d (%s)", version, orDash(finalClass))
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
		"/api/v1/projects/%s/tickets/%d/plan/%d", scope.key, scope.seq, view.Version))
	WriteJSON(w, http.StatusCreated, view)
}

func validateSubmitPlan(req submitPlanRequest) *apierr.Error {
	var details []apierr.Detail
	if len(req.Skeleton) == 0 {
		details = append(details, apierr.Detail{Field: "skeleton", Code: "required"})
	} else if !isJSONObject(req.Skeleton) {
		details = append(details, apierr.Detail{Field: "skeleton", Code: "invalid", Message: "skeleton はオブジェクトで指定してください"})
	}
	if len(req.Scope) > 0 && !isJSONObject(req.Scope) {
		details = append(details, apierr.Detail{Field: "scope", Code: "invalid", Message: "scope はオブジェクトで指定してください"})
	} else if len(req.Scope) > 0 {
		// 予算の形（4.2。pb-253）。範囲の案に書かれた予算は、承認されると強制に使われる。
		details = append(details, validateScopeBudget(req.Scope, "scope")...)
	}
	if req.BodyMd == "" {
		details = append(details, apierr.Detail{Field: "body_md", Code: "required"})
	}
	if req.ReasonMd == "" {
		details = append(details, apierr.Detail{Field: "reason_md", Code: "required"})
	}
	if req.DeclaredClass != "" {
		if _, ok := planClassSeverity[req.DeclaredClass]; !ok {
			details = append(details, apierr.Detail{
				Field: "declared_class", Code: "invalid",
				Message: "declared_class は minor / structural / scope のいずれかです",
			})
		}
	}
	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}

func isJSONObject(raw json.RawMessage) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	_, ok := v.(map[string]any)
	return ok
}

// classifyPlanChange は 6.3 の判定規則を測れる範囲で実施する。
//
// **機微な領域に触れる枝の増加と、木の深さの増加は測らない。** 前者は
// 「機微な領域」自体の器がまだ無く（8.1 の4。pb-249）、後者は版ごとの深さの
// 基準値を持たないため、安全に測れない。両方とも次のチケットへ送る
// （pb_submit_result の proposed_subtasks）。
func classifyPlanChange(prevScope, newScope, prevSkeleton, newSkeleton []byte) string {
	if !jsonDeepEqual(prevScope, newScope) {
		return planClassScope
	}
	prevBranches := parsePlanBranches(prevSkeleton)
	newBranches := parsePlanBranches(newSkeleton)
	if prevBranches != nil && newBranches != nil {
		if !sameBranchNames(prevBranches, newBranches) {
			return planClassStructural
		}
		if budgetShareShifted(prevBranches, newBranches, planBudgetShiftThreshold) {
			return planClassStructural
		}
	}
	return planClassMinor
}

// planBranch は skeleton.branches[] の1件（6.2 の「骨格」）。
//
// **skeleton の形を固定しない。** `branches` が無い・形が違う計画は、
// その観点の判定だけを諦める（classifyPlanChange）——Delegation.md は
// 骨格の中身を「根の直下の枝・予算配分・チェックポイント」としか定めておらず、
// JSON の正式なスキーマを持たない。
type planBranch struct {
	Name        string
	BudgetShare float64
}

func parsePlanBranches(skeleton []byte) []planBranch {
	var v struct {
		Branches []struct {
			Name        string  `json:"name"`
			BudgetShare float64 `json:"budget_share"`
		} `json:"branches"`
	}
	if err := json.Unmarshal(skeleton, &v); err != nil || v.Branches == nil {
		return nil
	}
	out := make([]planBranch, 0, len(v.Branches))
	for _, b := range v.Branches {
		out = append(out, planBranch{Name: b.Name, BudgetShare: b.BudgetShare})
	}
	return out
}

func sameBranchNames(a, b []planBranch) bool {
	if len(a) != len(b) {
		return false
	}
	names := make(map[string]bool, len(a))
	for _, x := range a {
		names[x.Name] = true
	}
	for _, y := range b {
		if !names[y.Name] {
			return false
		}
	}
	return true
}

func budgetShareShifted(a, b []planBranch, threshold float64) bool {
	prev := make(map[string]float64, len(a))
	for _, x := range a {
		prev[x.Name] = x.BudgetShare
	}
	for _, y := range b {
		if math.Abs(y.BudgetShare-prev[y.Name]) > threshold {
			return true
		}
	}
	return false
}

func jsonDeepEqual(a, b []byte) bool {
	var va, vb any
	if err := json.Unmarshal(a, &va); err != nil {
		return false
	}
	if err := json.Unmarshal(b, &vb); err != nil {
		return false
	}
	return reflect.DeepEqual(va, vb)
}

func textOrNullPg(s string) pgtype.Text {
	if s == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: s, Valid: true}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func delegationStatusLabel(status string) string {
	switch status {
	case "planning":
		return "計画中"
	case "approved":
		return "承認済"
	case "paused":
		return "停止"
	case "ended":
		return "終了"
	default:
		return status
	}
}

func delegationPlanViewOf(row gen.GetDelegationPlanByIDRow) delegationPlanView {
	var scope, skeleton any
	_ = json.Unmarshal(row.Scope, &scope)
	_ = json.Unmarshal(row.Skeleton, &skeleton)
	v := delegationPlanView{
		ID: row.ID, Version: row.Version, Scope: scope, Skeleton: skeleton,
		BodyMd: row.BodyMd, ReasonMd: row.ReasonMd,
		Status: row.Status,
		SubmittedBy: actorRef{
			ID: row.SubmittedByID, Kind: row.SubmittedByKind, DisplayName: row.SubmittedByName,
		},
		SubmittedAt: Time(row.SubmittedAt),
	}
	if row.DeclaredClass.Valid {
		v.DeclaredClass = &row.DeclaredClass.String
	}
	if row.MeasuredClass.Valid {
		v.MeasuredClass = &row.MeasuredClass.String
	}
	if row.RevertsTo.Valid {
		v.RevertsTo = &row.RevertsTo.Int32
	}
	if row.DecidedByID.Valid {
		v.DecidedBy = &actorRef{ID: row.DecidedByID.String, Kind: row.DecidedByKind.String, DisplayName: row.DecidedByName.String}
	}
	if row.DecidedAt != nil {
		t := Time(*row.DecidedAt)
		v.DecidedAt = &t
	}
	return v
}

// ── POST /tickets/{seq}/plan/{version}/revert ──────────────────

type revertPlanRequest struct {
	ReasonMd string `json:"reason_md"`
}

// revertDelegationPlan は前の版を写した新しい版を作る（6.2）。
//
// **PM が直接行う操作であり、評価は挟まない。** 作った版は即座に accepted
// になる——main の変更とは違い、6.1 の2層をすでに満たした人の判断だからである。
func (h *handler) revertDelegationPlan(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/plan/{version}/revert")
	if !ok {
		return
	}
	targetVersion, verErr := planVersionParam(r)
	if verErr != nil {
		apierr.Write(w, r, verErr)
		return
	}

	var req revertPlanRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.ReasonMd == "" {
		req.ReasonMd = fmt.Sprintf("版 %d への差し戻し", targetVersion)
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view     delegationPlanView
		rejected *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		deleg, err := q.GetDelegationByRootTicket(ctx, ticketID)
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "seq", Code: "invalid", Message: "このチケットは委任の根ではありません",
				})
				return errTicketReference
			}
			return fmt.Errorf("委任を読めない: %w", err)
		}

		old, err := q.GetDelegationPlanByVersion(ctx, gen.GetDelegationPlanByVersionParams{
			DelegationID: deleg.ID, Version: targetVersion,
		})
		if err != nil {
			if err == pgx.ErrNoRows {
				rejected = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "version", Code: "not_found", Message: "指定した版が見つかりません",
				})
				return errTicketReference
			}
			return fmt.Errorf("差し戻し元の版を読めない: %w", err)
		}

		newVersion, err := q.NextDelegationPlanVersion(ctx, deleg.ID)
		if err != nil {
			return fmt.Errorf("計画の版を採番できない: %w", err)
		}

		if err := q.CreateDelegationPlan(ctx, gen.CreateDelegationPlanParams{
			ID: id, DelegationID: deleg.ID, Version: newVersion,
			Scope: old.Scope, Skeleton: old.Skeleton,
			BodyMd: old.BodyMd, ReasonMd: req.ReasonMd,
			DeclaredClass: pgtype.Text{}, MeasuredClass: pgtype.Text{},
			Status:      "accepted",
			RevertsTo:   pgtype.Int4{Int32: targetVersion, Valid: true},
			SubmittedBy: scope.actorID,
		}); err != nil {
			return fmt.Errorf("差し戻しの版を作成できない: %w", err)
		}
		if _, err := q.AcceptDelegationPlan(ctx, id); err != nil {
			return fmt.Errorf("差し戻しの版を確定できない: %w", err)
		}

		row, err := q.GetDelegationPlanByID(ctx, id)
		if err != nil {
			return fmt.Errorf("作成した計画を読めない: %w", err)
		}
		view = delegationPlanViewOf(row)

		field := "delegation_plan"
		newVal := fmt.Sprintf("v%d（v%d への差し戻し）", newVersion, targetVersion)
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
	WriteJSON(w, http.StatusCreated, view)
}

func planVersionParam(r *http.Request) (int32, *apierr.Error) {
	raw := chi.URLParam(r, "version")
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "version", Code: "invalid", Message: "version は1以上の整数で指定してください",
		})
	}
	return int32(n), nil
}
