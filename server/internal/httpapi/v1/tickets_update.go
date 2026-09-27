// PATCH /api/v1/projects/{key}/tickets/{seq}（ApiDesign.md 9.5.2）。
//
// **送られたフィールドだけを更新する。** 「送られていない」と「null が送られた」を
// 区別するため、リクエストは全項目を json.RawMessage で受けてから解く（後述の
// updateTicketRequest）。ポインタだけでは、null を送って項目を空にする操作
// （担当を外す・親を外す・期限を消す）を表せない。
//
// **サーバが決めるものは 422 で弾く**（9.5.2 の表）。details[].code で3系統に
// 分かれ、いずれも error.code は validation_failed である。
//
//	immutable_field         id / seq / version / created_at / updated_at / reporter_id
//	use_move_endpoint       sort_key / staged_at      → 9.4 の move
//	use_sprint_endpoint     sprint_id                 → 9.12 の start / finish
//	use_transition_endpoint status_key / closed_at    → 9.6 の transition
//
// **assignee_id を変えるときだけ ticket.assign も要る**（9.5.2）。ルート定義の
// 宣言は ticket.edit のままで、この追加分はハンドラ内で見る——必要権限が
// リクエスト本文の内容で変わるため、ミドルウェアの宣言では表せない
// （手順14 の RequirePermissionUnlessQuery はクエリ用で、本文は読めない）。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 9.5.2 が PATCH で受け付けない項目。**details[].code ごとに分けてある**——
// 「なぜ書けないか」で正しい代替手段が変わるためで、利用者に返す message も違う。
var (
	ticketImmutableFields = []string{
		"id", "seq", "version", "created_at", "updated_at", "reporter_id",
	}
	ticketMoveOnlyFields       = []string{"sort_key", "staged_at"}
	ticketTransitionOnlyFields = []string{"status_key", "closed_at"}
	// **sprint_id は 0028 で書けなくなった**（9.5.2）。スプリントは
	// 「チケットにあらかじめ付ける属性」ではなく「いまどの期間で消化しようと
	// しているか」であり、付け替えはオンステージ全体に対して1回起きる。
	ticketSprintOnlyFields = []string{"sprint_id"}
)

// updateTicketRequest は 9.5.2 のリクエスト。
//
// **json.RawMessage で受けるのは、3つの状態を区別する必要があるため**である。
//
//	キーが無い      → 触らない
//	キーがあり null → その項目を空にする
//	キーがあり値    → その値にする
//
// *T のポインタでは、キーが無い場合と null が送られた場合がどちらも nil になり、
// 「担当を外す」を表せない。
type updateTicketRequest map[string]json.RawMessage

// ticketPatch は解いたあとの更新内容。**Set が false の項目は触らない。**
type ticketPatch struct {
	Type  optional[string]
	Title optional[string]

	BodyMd     optional[string]
	Priority   optional[string]
	AssigneeID optional[string]
	// WorkingAgentID は実行者（9.5.2。手順26b）。**この経路は人が使う**——
	// エージェント自身の宣言は 9.6 の遷移が副作用として立てる。
	WorkingAgentID optional[string]
	ParentSeq      optional[int32]
	TagIDs         optional[[]string]

	EstimatePoint      optional[float64]
	EstimateHours      optional[float64]
	ActualHours        optional[float64]
	ActualPoint        optional[float64]
	ActualPointVersion optional[string]

	StartDate optional[pgtype.Date]
	DueDate   optional[pgtype.Date]

	// 手順27 で開けた4つ（9.5.2）。**エージェントの逸脱防止を構造データで行う**
	// （Requirements.md 10.5.3）以上、その構造データを書く経路が要る。列は 0006 から
	// 在ったが、本文のリストにも 9.3 の POST にも無く、どの経路からも書けなかった。
	//
	// **ExecutionMode と Scope は null を受けない**（列が NOT NULL）。
	// Readiness と ReadinessNote は null が「未判定へ戻す」を表す。
	ExecutionMode optional[string]
	Readiness     optional[string]
	ReadinessNote optional[string]
	Scope         optional[json.RawMessage]
}

// optional は「送られたか」と「null か」を持つ値。
//
// Set=false は「キーが無い」、Set=true かつ Null=true は「null が送られた」。
type optional[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// updateTicket はチケット1件を部分更新する（9.5.2）。
func (h *handler) updateTicket(w http.ResponseWriter, r *http.Request) {
	_, key, projectID, ok := projectScopeContext(w, r, h.q,
		"PATCH /projects/{key}/tickets/{seq}")
	if !ok {
		return
	}

	seq, seqErr := ticketSeqParam(r)
	if seqErr != nil {
		apierr.Write(w, r, seqErr)
		return
	}

	var raw updateTicketRequest
	if e := decodeJSON(r, &raw); e != nil {
		apierr.Write(w, r, e)
		return
	}

	version, versionErr := parseIfMatch(r)
	patch, patchErr := parseTicketPatch(raw)
	// 2.5 の details は「項目ごとの誤り」を並べるものなので、If-Match の欠落と
	// 本文の誤りを1つの 422 にまとめる（projects_update.go と同じ）。
	if e := mergeValidationErrors(versionErr, patchErr); e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **ticket.self_edit だけの呼び出し元は、送れる項目が絞られる**（9.5.2、
	// DbDesign.md 6.13。0029）。ルートは ticket.edit と
	// ticket.self_edit を OR で宣言しているので、狭いほうしか持たない
	// 相手をここで見る。
	//
	// **raw（生のキー集合）で見る。** patch へ写した後だと、そもそも
	// parseTicketPatch が読まない項目（type 以外の拒否対象）を検出できない。
	if e := denySelfEditFields(r, key, raw); e != nil {
		apierr.Write(w, r, e)
		return
	}
	_, point := raw["actual_point"]
	_, versionField := raw["actual_point_version"]
	if point || versionField {
		if e := requireActualPointPermission(r, key); e != nil {
			apierr.Write(w, r, e)
			return
		}
	}

	// **担当者を変えるなら ticket.assign が要る**（9.5.2）。ルートの宣言
	// （ticket.edit / ticket.self_edit）に足りない分をここで見る。
	//
	// **判定は RequireProjectPermission が計算済みのものを使う。** 同じ
	// リクエストの中で実効権限を2回計算しないため、また「ミドルウェアと
	// ハンドラで別々に数えた権限が食い違う」状態を作らないためである
	// （Design.md 6.4.1 の式は1か所にしか無い）。
	if patch.AssigneeID.Set {
		a := auth.ProjectAuthzFromContext(r.Context(), key)
		if a == nil || !auth.HasPermission(a.Permissions, permTicketAssign) {
			apierr.Write(w, r, apierr.New(apierr.Forbidden).
				WithMessage("担当者を変更する権限がありません"))
			return
		}
	}

	// **実行者も「誰がやるか」を決める操作なので ticket.assign を要る**（9.5.2）。
	// 担当と同じ扱いにする。
	if patch.WorkingAgentID.Set {
		a := auth.ProjectAuthzFromContext(r.Context(), key)
		if a == nil || !auth.HasPermission(a.Permissions, permTicketAssign) {
			apierr.Write(w, r, apierr.New(apierr.Forbidden).
				WithMessage("実行者を変更する権限がありません"))
			return
		}
	}

	ctx := r.Context()
	rec := activity.FromRequest(r)
	var (
		view     ticketDetailView
		writeErr *apierr.Error // 422 / 409 / 404 として返すもの
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		before, err := q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{
			ProjectID: projectID, Seq: seq,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				writeErr = ticketNotFound(seq)
				return errTicketReference
			}
			return fmt.Errorf("チケット %d を読めない: %w", seq, err)
		}

		params, e := h.resolveTicketPatch(ctx, q, projectID, seq, before, patch, version)
		if e != nil {
			writeErr = e
			return errTicketReference
		}

		rows, err := q.UpdateTicket(ctx, params)
		if err != nil {
			return fmt.Errorf("チケット %d を更新できない: %w", seq, err)
		}
		if rows == 0 {
			// 行が消えた可能性は上で潰してあるので、0 行は version 不一致である。
			writeErr = apierr.New(apierr.Conflict).
				WithMessage("他の利用者がこのチケットを更新しました。内容を読み直してからやり直してください")
			return errTicketReference
		}

		if patch.TagIDs.Set {
			if err := replaceTicketTags(ctx, q, before.ID, patch.TagIDs); err != nil {
				return err
			}
		}

		// **変更した項目ごとに1行**（9.5.2）。5.5 の変更履歴が
		// 「いつ担当が誰から誰へ変わったか」を出せるようにするため。
		if err := recordTicketFieldChanges(ctx, q, rec, projectID, before, patch); err != nil {
			return err
		}

		// **オンステージの配下へ付け替えたら、部分木ごと進行中のスプリントへ入れる**
		// （9.12.3）。外す方向（null）は扱わない
		if patch.ParentSeq.Set && !patch.ParentSeq.Null {
			if err := joinActiveSprint(ctx, q, projectID, before.ID); err != nil {
				return err
			}
		}

		v, err := buildTicketDetail(ctx, q, projectID, seq)
		if err != nil {
			return fmt.Errorf("更新したチケットを読めない: %w", err)
		}
		view = v
		return nil
	})

	switch {
	case writeErr != nil:
		apierr.Write(w, r, writeErr)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケット %d を更新できない: %w", seq, err)))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// resolveTicketPatch は参照先の検証とオンステージの判定を行い、UPDATE の引数を作る。
//
// **トランザクションの中で呼ぶ。** 外で確かめてから入ると、確かめた行が消えている
// ことがある（tickets_create.go の resolveTicketParent と同じ理由）。
func (h *handler) resolveTicketPatch(
	ctx context.Context, q gen.Querier, projectID string, seq int32,
	before gen.GetTicketBySeqRow, patch ticketPatch, version int32,
) (gen.UpdateTicketParams, *apierr.Error) {
	params := gen.UpdateTicketParams{ProjectID: projectID, Seq: seq, Version: version}

	if patch.Type.Set {
		params.Type = pgtype.Text{String: patch.Type.Value, Valid: true}
	}
	if patch.Title.Set {
		params.Title = pgtype.Text{String: patch.Title.Value, Valid: true}
	}
	params.BodyMdSet, params.BodyMd = textParam(patch.BodyMd)
	params.PrioritySet, params.Priority = textParam(patch.Priority)
	params.EstimatePointSet, params.EstimatePoint = floatParam(patch.EstimatePoint)
	params.EstimateHoursSet, params.EstimateHours = floatParam(patch.EstimateHours)
	params.ActualHoursSet, params.ActualHours = floatParam(patch.ActualHours)
	params.ActualPointSet, params.ActualPoint = floatParam(patch.ActualPoint)
	params.ActualPointVersionSet, params.ActualPointVersion = textParam(patch.ActualPointVersion)
	params.StartDateSet, params.StartDate = dateParam(patch.StartDate)
	params.DueDateSet, params.DueDate = dateParam(patch.DueDate)

	// 手順27 の4つ（9.5.2）。**execution_mode と scope は COALESCE で足りる**
	// ——null は parseTicketPatch が先に 422 で落としているので、ここへ来る値は
	// 「送られなかった」か「実体のある値」のどちらかしかない。
	if patch.ExecutionMode.Set {
		params.ExecutionMode = pgtype.Text{String: patch.ExecutionMode.Value, Valid: true}
	}
	params.ReadinessSet, params.Readiness = textParam(patch.Readiness)
	params.ReadinessNoteSet, params.ReadinessNote = textParam(patch.ReadinessNote)
	if patch.Scope.Set {
		params.Scope = []byte(patch.Scope.Value)
	}

	// ── 開始日と期限の前後関係（DbDesign.md 6.6 の ck_ticket_dates）─────
	//
	// **片方だけ送られたときは、もう片方の現在値と比べる。** DB の CHECK に
	// 当てると 500 になるので、同じ規則をここで先に見て 422 にする
	// （tickets_create.go の validateCreateTicket と同じ扱い）。
	start, due := before.StartDate, before.DueDate
	if params.StartDateSet {
		start = params.StartDate
	}
	if params.DueDateSet {
		due = params.DueDate
	}
	if start.Valid && due.Valid && start.Time.After(due.Time) {
		return params, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "due_date", Code: "invalid",
			Message: "期限は開始日以降の日付で指定してください",
		})
	}

	// ── 参照先の検証（9.3 と同じ規則を使い回す）───────────────────
	if patch.AssigneeID.Set && !patch.AssigneeID.Null {
		if e := validateTicketAssignee(ctx, q, projectID, patch.AssigneeID.Value); e != nil {
			return params, e
		}
	}
	params.AssigneeIDSet, params.AssigneeID = textParam(patch.AssigneeID)

	if patch.WorkingAgentID.Set && !patch.WorkingAgentID.Null {
		if e := validateTicketWorkingAgent(ctx, q, projectID, patch.WorkingAgentID.Value); e != nil {
			return params, e
		}
	}
	params.WorkingAgentIDSet, params.WorkingAgentID = textParam(patch.WorkingAgentID)

	if patch.TagIDs.Set && !patch.TagIDs.Null {
		if e := validateTicketTags(ctx, q, projectID, patch.TagIDs.Value); e != nil {
			return params, e
		}
	}

	// ── 親（9.5.2 の parent_cycle）──────────────────────────────
	newParentType := pgtype.Text{}
	if patch.ParentSeq.Set {
		params.ParentIDSet = true
		if !patch.ParentSeq.Null {
			parentID, e := resolveTicketParent(ctx, q, projectID, &patch.ParentSeq.Value)
			if e != nil {
				return params, e
			}
			// **自分自身または自分の子孫を親にできない**（9.5.2）。DB の
			// ck_ticket_not_self_parent は自己参照しか防げない。
			cycle, err := q.IsTicketDescendant(ctx, gen.IsTicketDescendantParams{
				AncestorID: before.ID, CandidateID: parentID.String,
			})
			if err != nil {
				return params, apierr.New(apierr.InternalError).
					WithCause(fmt.Errorf("親子関係を確認できない: %w", err))
			}
			if cycle {
				return params, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "parent_seq", Code: "parent_cycle",
					Message: "自分自身や配下のチケットを親にはできません",
				})
			}
			params.ParentID = parentID

			t, err := q.GetTicketTypeByID(ctx, parentID.String)
			if err != nil {
				return params, apierr.New(apierr.InternalError).
					WithCause(fmt.Errorf("親の種別を読めない: %w", err))
			}
			newParentType = pgtype.Text{String: t, Valid: true}
		}
	} else if before.ParentSeq.Valid {
		// 親を触らない場合は現在の親の種別を使う（下のオンステージ判定に要る）。
		parentID, err := q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{
			ProjectID: projectID, Seq: before.ParentSeq.Int32,
		})
		if err != nil {
			return params, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("現在の親を読めない: %w", err))
		}
		t, err := q.GetTicketTypeByID(ctx, parentID)
		if err != nil {
			return params, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("現在の親の種別を読めない: %w", err))
		}
		newParentType = pgtype.Text{String: t, Valid: true}
	}

	// ── オンステージの規則を破らせない（9.5.2）──────────────────
	//
	// **9.4.1 が move で 422 not_stageable に倒している条件と同じもの**を、
	// PATCH からも迂回できないようにする。**自動で段から降ろす方式は採らない**
	// ——種別や親を変えただけのつもりの利用者が、オンステージから消えたことに
	// 気づく手段がないため（9.5.2）。
	if before.StagedAt != nil && (patch.Type.Set || patch.ParentSeq.Set) {
		newType := before.Type
		if patch.Type.Set {
			newType = patch.Type.Value
		}
		if !stageable(newType, newParentType) {
			message := "オンステージのチケットを配下にはできません。先にバックログへ戻してください"
			if newType == ticketTypeEpic {
				message = "オンステージのチケットをエピックにはできません。先にバックログへ戻してください"
			}
			field := "parent_seq"
			if newType == ticketTypeEpic {
				field = "type"
			}
			return params, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: field, Code: "not_stageable", Message: message,
			})
		}
	}

	return params, nil
}

// replaceTicketTags は tag_ids を丸ごと置き換える（9.5.2）。
//
// **差分を計算しない。** 消してから付け直すほうが、付ける側と外す側の2つの集合を
// 持つより読み違えが少ない。null または空配列でタグを全て外す。
func replaceTicketTags(
	ctx context.Context, q gen.Querier, ticketID string, tagIDs optional[[]string],
) error {
	if err := q.DetachTicketTags(ctx, ticketID); err != nil {
		return fmt.Errorf("タグを外せない: %w", err)
	}
	if tagIDs.Null {
		return nil
	}
	for _, tagID := range dedupe(tagIDs.Value) {
		if err := q.AttachTicketTag(ctx, gen.AttachTicketTagParams{
			TicketID: ticketID, TagID: tagID,
		}); err != nil {
			return fmt.Errorf("タグ %q を付けられない: %w", tagID, err)
		}
	}
	return nil
}

// recordTicketFieldChanges は変更した項目ごとに activity を1行書く（9.5.2）。
//
// **値が現在値と同じ項目は記録しない。** 送られてはいるが変更が生じていない
// ので、履歴に「変えていない」行が並ぶことになる。version は 2.8 の規約どおり
// +1 されるが、それは競合検出の都合であって業務上の変更ではない。
//
// **body_md だけ old_value / new_value を NULL にする**（9.5.2）。本文は長く、
// 9.13.2 は per_page=20 の一覧APIなので、20件ぶんの Markdown を載せると
// 8章の「応答を軽く保つ」方針に反する。field は記録するので「いつ誰が本文を
// 変えたか」は追える。
//
// **tag_ids は記録しない。** activity.field は列名を持つ想定で（DbDesign.md 6.8）、
// 多対多の付け外しを old_value / new_value の1組で表せない。手順18 で
// コメント・DoD・リンクを足すときに、entity_type を増やすかどうかと一緒に見る。
func recordTicketFieldChanges(
	ctx context.Context, q gen.Querier, rec *activity.Recorder,
	projectID string, before gen.GetTicketBySeqRow, patch ticketPatch,
) error {
	type change struct {
		field    string
		old, new *string
	}
	var changes []change

	add := func(field string, oldVal, newVal *string) {
		if equalStringPtr(oldVal, newVal) {
			return
		}
		changes = append(changes, change{field: field, old: oldVal, new: newVal})
	}

	if patch.Type.Set {
		add("type", strPtr(before.Type), strPtr(patch.Type.Value))
	}
	if patch.Title.Set {
		add("title", strPtr(before.Title), strPtr(patch.Title.Value))
	}
	if patch.BodyMd.Set {
		// 値は載せない（上記）。変更が生じたときだけ1行書く。
		if !equalStringPtr(textPtr(before.BodyMd), optionalStrPtr(patch.BodyMd)) {
			changes = append(changes, change{field: "body_md"})
		}
	}
	if patch.Priority.Set {
		add("priority", textPtr(before.Priority), optionalStrPtr(patch.Priority))
	}
	if patch.WorkingAgentID.Set {
		add("working_agent_id", textPtr(before.WorkingAgentID), optionalStrPtr(patch.WorkingAgentID))
	}
	if patch.AssigneeID.Set {
		add("assignee_id", textPtr(before.AssigneeID), optionalStrPtr(patch.AssigneeID))
	}
	if patch.ParentSeq.Set {
		add("parent_id", int4StrPtr(before.ParentSeq), optionalInt32StrPtr(patch.ParentSeq))
	}
	if patch.EstimatePoint.Set {
		add("estimate_point", float8StrPtr(before.EstimatePoint), optionalFloatStrPtr(patch.EstimatePoint))
	}
	if patch.EstimateHours.Set {
		add("estimate_hours", float8StrPtr(before.EstimateHours), optionalFloatStrPtr(patch.EstimateHours))
	}
	if patch.ActualHours.Set {
		add("actual_hours", float8StrPtr(before.ActualHours), optionalFloatStrPtr(patch.ActualHours))
	}
	if patch.ActualPoint.Set {
		add("actual_point", float8StrPtr(before.ActualPoint), optionalFloatStrPtr(patch.ActualPoint))
	}
	if patch.ActualPointVersion.Set {
		add("actual_point_version", textPtr(before.ActualPointVersion), optionalStrPtr(patch.ActualPointVersion))
	}
	if patch.StartDate.Set {
		add("start_date", dateStrPtr(before.StartDate), optionalDateStrPtr(patch.StartDate))
	}
	if patch.DueDate.Set {
		add("due_date", dateStrPtr(before.DueDate), optionalDateStrPtr(patch.DueDate))
	}

	// 手順27 の4つ。**scope も old/new を残す**（body_md のように落とさない）
	// ——「誰が縛りを緩めたか」は履歴として読む価値が本文より高く、値は数行に
	// 収まるので 9.13.2 の per_page=20 を圧迫しない（9.5.2）。
	if patch.ExecutionMode.Set {
		add("execution_mode", strPtr(before.ExecutionMode), strPtr(patch.ExecutionMode.Value))
	}
	if patch.Readiness.Set {
		add("readiness", textPtr(before.Readiness), optionalStrPtr(patch.Readiness))
	}
	if patch.ReadinessNote.Set {
		add("readiness_note", textPtr(before.ReadinessNote), optionalStrPtr(patch.ReadinessNote))
	}
	if patch.Scope.Set {
		add("scope", strPtr(canonicalJSON(before.Scope)), strPtr(canonicalJSON(patch.Scope.Value)))
	}

	for _, c := range changes {
		field := c.field
		if err := rec.Record(ctx, q, activity.Entry{
			ProjectID:  projectID,
			EntityType: activity.EntityTicket,
			EntityID:   before.ID,
			Action:     activity.Update,
			Field:      &field,
			OldValue:   c.old,
			NewValue:   c.new,
		}); err != nil {
			return err
		}
	}
	return nil
}

// parseTicketPatch は本文を解き、形だけで判定できる検証を済ませる（9.5.2）。
//
// **受け付けない項目は、そこにキーが在るだけで 422 にする。** 値が現在値と
// 同じでも通さない——「送れば通ることがある」と読めてしまい、9.4 / 9.6 へ
// 誘導する意味が薄れる。
func parseTicketPatch(raw updateTicketRequest) (ticketPatch, *apierr.Error) {
	var patch ticketPatch
	var details []apierr.Detail

	for _, field := range ticketImmutableFields {
		if _, ok := raw[field]; ok {
			details = append(details, apierr.Detail{
				Field: field, Code: "immutable_field",
				Message: "この項目はサーバが決めるため変更できません",
			})
		}
	}
	for _, field := range ticketMoveOnlyFields {
		if _, ok := raw[field]; ok {
			details = append(details, apierr.Detail{
				Field: field, Code: "use_move_endpoint",
				Message: "並び順と段の変更は move で行ってください",
			})
		}
	}
	for _, field := range ticketTransitionOnlyFields {
		if _, ok := raw[field]; ok {
			details = append(details, apierr.Detail{
				Field: field, Code: "use_transition_endpoint",
				Message: "ステータスの変更は transition で行ってください",
			})
		}
	}
	for _, field := range ticketSprintOnlyFields {
		if _, ok := raw[field]; ok {
			details = append(details, apierr.Detail{
				Field: field, Code: "use_sprint_endpoint",
				Message: "スプリントの変更はスプリントの開始・終了で行ってください",
			})
		}
	}

	// type / title は NOT NULL。null を送るのは「空にする」ではなく誤りである。
	if v, ok := raw["type"]; ok {
		s, isNull, err := decodeOptionalString(v)
		switch {
		case err != nil || isNull:
			details = append(details, apierr.Detail{
				Field: "type", Code: "invalid", Message: "種別を選んでください",
			})
		case !slices.Contains(ticketTypes, s):
			details = append(details, apierr.Detail{
				Field: "type", Code: "invalid",
				Message: "種別は " + strings.Join(ticketTypes, " / ") + " のいずれかです",
			})
		default:
			patch.Type = optional[string]{Set: true, Value: s}
		}
	}
	if v, ok := raw["title"]; ok {
		s, isNull, err := decodeOptionalString(v)
		s = strings.TrimSpace(s)
		switch n := utf8.RuneCountInString(s); {
		case err != nil || isNull || n == 0:
			details = append(details, apierr.Detail{
				Field: "title", Code: "required", Message: "タイトルを入力してください",
			})
		case n > ticketTitleMaxLen:
			details = append(details, apierr.Detail{
				Field: "title", Code: "too_long",
				Message: fmt.Sprintf("タイトルは%d文字以内で入力してください", ticketTitleMaxLen),
			})
		default:
			patch.Title = optional[string]{Set: true, Value: s}
		}
	}

	patch.BodyMd, details = optionalStringField(raw, "body_md", details, nil)
	patch.Priority, details = optionalStringField(raw, "priority", details,
		func(s string) *apierr.Detail {
			if slices.Contains(ticketPriorities, s) {
				return nil
			}
			return &apierr.Detail{
				Field: "priority", Code: "invalid",
				Message: "優先度は " + strings.Join(ticketPriorities, " / ") + " のいずれかです",
			}
		})
	patch.AssigneeID, details = optionalStringField(raw, "assignee_id", details, nil)
	patch.WorkingAgentID, details = optionalStringField(raw, "working_agent_id", details, nil)

	patch.EstimatePoint, details = optionalFloatField(raw, "estimate_point", details)
	patch.EstimateHours, details = optionalFloatField(raw, "estimate_hours", details)
	patch.ActualHours, details = optionalFloatField(raw, "actual_hours", details)
	patch.ActualPoint, details = optionalFloatField(raw, "actual_point", details)
	patch.ActualPointVersion, details = optionalStringField(raw, "actual_point_version", details, func(s string) *apierr.Detail {
		if actualPointVersionPattern.MatchString(s) {
			return nil
		}
		return &apierr.Detail{Field: "actual_point_version", Code: "invalid", Message: "算出式の版は actual-v0 の形で指定してください"}
	})
	if patch.ActualPoint.Set != patch.ActualPointVersion.Set || (patch.ActualPoint.Set && patch.ActualPoint.Null != patch.ActualPointVersion.Null) {
		details = append(details, apierr.Detail{Field: "actual_point_version", Code: "required", Message: "実績ポイントと算出式の版は一緒に指定してください"})
	}

	patch.StartDate, details = optionalDateField(raw, "start_date", details)
	patch.DueDate, details = optionalDateField(raw, "due_date", details)

	// ── 手順27 で開けた4つ（9.5.2）─────────────────────────────
	//
	// **新しい details[].code を発明しない。** 4項目とも既存の invalid で表せる
	// （9.14 の表に加わるものは無い）。
	if v, ok := raw["execution_mode"]; ok {
		s, isNull, err := decodeOptionalString(v)
		switch {
		case err != nil || isNull || !slices.Contains(ticketExecutionModes, s):
			details = append(details, apierr.Detail{
				Field: "execution_mode", Code: "invalid",
				Message: "実行モードは " + strings.Join(ticketExecutionModes, " / ") + " のいずれかです",
			})
		default:
			patch.ExecutionMode = optional[string]{Set: true, Value: s}
		}
	}
	patch.Readiness, details = optionalStringField(raw, "readiness", details,
		func(s string) *apierr.Detail {
			if slices.Contains(ticketReadinessSet, s) {
				return nil
			}
			return &apierr.Detail{
				Field: "readiness", Code: "invalid",
				Message: "Readiness は " + strings.Join(ticketReadinessSet, " / ") + " のいずれかです",
			}
		})
	patch.ReadinessNote, details = optionalStringField(raw, "readiness_note", details, nil)

	if v, ok := raw["scope"]; ok {
		if d := validateTicketScope(v); d != nil {
			details = append(details, *d)
		} else {
			patch.Scope = optional[json.RawMessage]{Set: true, Value: v}
		}
	}

	if v, ok := raw["parent_seq"]; ok {
		if isJSONNull(v) {
			patch.ParentSeq = optional[int32]{Set: true, Null: true}
		} else {
			var n int32
			if err := json.Unmarshal(v, &n); err != nil || n < 1 {
				details = append(details, apierr.Detail{
					Field: "parent_seq", Code: "invalid",
					Message: "親チケットの番号は1以上で指定してください",
				})
			} else {
				patch.ParentSeq = optional[int32]{Set: true, Value: n}
			}
		}
	}

	if v, ok := raw["tag_ids"]; ok {
		if isJSONNull(v) {
			patch.TagIDs = optional[[]string]{Set: true, Null: true}
		} else {
			var ids []string
			if err := json.Unmarshal(v, &ids); err != nil {
				details = append(details, apierr.Detail{
					Field: "tag_ids", Code: "invalid",
					Message: "タグは ID の配列で指定してください",
				})
			} else {
				patch.TagIDs = optional[[]string]{Set: true, Value: ids}
			}
		}
	}

	if len(details) > 0 {
		return ticketPatch{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return patch, nil
}

// validateTicketScope は scope の形を見る（9.5.2。手順27）。
//
// **形（配列かオブジェクトか）だけを見て、そのまま保存する。** 9.15 の完了レポートと
// 同じ判断で、**未知のキーは拒まない**——境界の表し方はプロジェクトごとに育つ
// （触ってよい API、触ってよい環境、承認が要る操作）。知らないキーを1つ付けた
// だけで更新が丸ごと落ちると、往復が増えるだけで誰も得をしない。
//
// **null は受けない。** 列が NOT NULL DEFAULT '{}' であり、空にするのは {} である。
func validateTicketScope(v json.RawMessage) *apierr.Detail {
	invalid := func(msg string) *apierr.Detail {
		return &apierr.Detail{Field: "scope", Code: "invalid", Message: msg}
	}
	if isJSONNull(v) {
		return invalid("スコープ境界は空にするとき {} を指定してください")
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(v, &obj); err != nil {
		return invalid("スコープ境界はオブジェクトで指定してください")
	}
	for _, key := range ticketScopeArrayKeys {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		var items []string
		if err := json.Unmarshal(raw, &items); err != nil {
			return invalid(key + " は文字列の配列で指定してください")
		}
	}
	return nil
}

// canonicalJSON は jsonb を比較・記録できる1つの表記に均す（9.5.2 の activity）。
//
// **これが無いと「同じ内容の送り直し」が変更として記録される。** DB から返る
// jsonb は Postgres が正規化した並び（キーの長さ順）で、送られてくる JSON の
// 並びとは一致しない。encoding/json は map のキーを名前順に並べるので、
// **両側を通せば同じ内容が同じ文字列になる。**
//
// 解けないものはそのまま返す（検証を通った値しか来ないが、記録のために
// エラーを持ち上げる価値が無い）。
func canonicalJSON(raw []byte) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return string(raw)
	}
	return string(b)
}

// ── 本文を解くための小物 ──────────────────────────────────────

func isJSONNull(v json.RawMessage) bool {
	return string(v) == "null"
}

func decodeOptionalString(v json.RawMessage) (string, bool, error) {
	if isJSONNull(v) {
		return "", true, nil
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return "", false, err
	}
	return s, false, nil
}

// optionalStringField は「省略 / null / 文字列」を解く。validate は値がある
// ときだけ呼ばれ、空文字は null と同じ「空にする」として扱う。
func optionalStringField(
	raw updateTicketRequest, field string, details []apierr.Detail,
	validate func(string) *apierr.Detail,
) (optional[string], []apierr.Detail) {
	v, ok := raw[field]
	if !ok {
		return optional[string]{}, details
	}
	s, isNull, err := decodeOptionalString(v)
	if err != nil {
		return optional[string]{}, append(details, apierr.Detail{
			Field: field, Code: "invalid", Message: "文字列で指定してください",
		})
	}
	if isNull || s == "" {
		return optional[string]{Set: true, Null: true}, details
	}
	if validate != nil {
		if d := validate(s); d != nil {
			return optional[string]{}, append(details, *d)
		}
	}
	return optional[string]{Set: true, Value: s}, details
}

func optionalFloatField(
	raw updateTicketRequest, field string, details []apierr.Detail,
) (optional[float64], []apierr.Detail) {
	v, ok := raw[field]
	if !ok {
		return optional[float64]{}, details
	}
	if isJSONNull(v) {
		return optional[float64]{Set: true, Null: true}, details
	}
	var f float64
	if err := json.Unmarshal(v, &f); err != nil {
		return optional[float64]{}, append(details, apierr.Detail{
			Field: field, Code: "invalid", Message: "数値で指定してください",
		})
	}
	if f < 0 {
		return optional[float64]{}, append(details, apierr.Detail{
			Field: field, Code: "out_of_range", Message: "0以上の数値で指定してください",
		})
	}
	return optional[float64]{Set: true, Value: f}, details
}

// optionalDateField は date 列を解く（DbDesign.md 6.6）。**時刻つきは受けない**
// ——"2026-08-05T00:00:00Z" を通すと、タイムゾーンによって前日へずれる
// （apitime.go の parseAPIDate と同じ規則）。
func optionalDateField(
	raw updateTicketRequest, field string, details []apierr.Detail,
) (optional[pgtype.Date], []apierr.Detail) {
	v, ok := raw[field]
	if !ok {
		return optional[pgtype.Date]{}, details
	}
	if isJSONNull(v) {
		return optional[pgtype.Date]{Set: true, Null: true}, details
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return optional[pgtype.Date]{}, append(details, apierr.Detail{
			Field: field, Code: "invalid",
			Message: "日付は YYYY-MM-DD の形式で指定してください",
		})
	}
	if s == "" {
		return optional[pgtype.Date]{Set: true, Null: true}, details
	}
	d, okDate := parseAPIDate(s)
	if !okDate || !d.Valid {
		return optional[pgtype.Date]{}, append(details, apierr.Detail{
			Field: field, Code: "invalid",
			Message: "日付は YYYY-MM-DD の形式で指定してください",
		})
	}
	return optional[pgtype.Date]{Set: true, Value: d}, details
}

// ── UPDATE の引数へ写す小物 ───────────────────────────────────

func textParam(o optional[string]) (bool, pgtype.Text) {
	if !o.Set {
		return false, pgtype.Text{}
	}
	if o.Null {
		return true, pgtype.Text{}
	}
	return true, pgtype.Text{String: o.Value, Valid: true}
}

func floatParam(o optional[float64]) (bool, pgtype.Float8) {
	if !o.Set {
		return false, pgtype.Float8{}
	}
	if o.Null {
		return true, pgtype.Float8{}
	}
	return true, pgtype.Float8{Float64: o.Value, Valid: true}
}

func dateParam(o optional[pgtype.Date]) (bool, pgtype.Date) {
	if !o.Set {
		return false, pgtype.Date{}
	}
	if o.Null {
		return true, pgtype.Date{}
	}
	return true, o.Value
}

// ── activity の old_value / new_value へ写す小物 ─────────────────
//
// **すべて text で持つ**（DbDesign.md 6.8 の列がそうであるため）。表示名への
// 変換は画面が行う（9.13.2）。

func strPtr(s string) *string { return &s }

func int4StrPtr(v pgtype.Int4) *string {
	if !v.Valid {
		return nil
	}
	s := strconv.FormatInt(int64(v.Int32), 10)
	return &s
}

func float8StrPtr(v pgtype.Float8) *string {
	if !v.Valid {
		return nil
	}
	s := strconv.FormatFloat(v.Float64, 'f', -1, 64)
	return &s
}

func dateStrPtr(v pgtype.Date) *string {
	if !v.Valid {
		return nil
	}
	s := v.Time.Format(time.DateOnly)
	return &s
}

func optionalStrPtr(o optional[string]) *string {
	if o.Null {
		return nil
	}
	s := o.Value
	return &s
}

func optionalInt32StrPtr(o optional[int32]) *string {
	if o.Null {
		return nil
	}
	s := strconv.FormatInt(int64(o.Value), 10)
	return &s
}

func optionalFloatStrPtr(o optional[float64]) *string {
	if o.Null {
		return nil
	}
	s := strconv.FormatFloat(o.Value, 'f', -1, 64)
	return &s
}

func optionalDateStrPtr(o optional[pgtype.Date]) *string {
	if o.Null || !o.Value.Valid {
		return nil
	}
	s := o.Value.Time.Format(time.DateOnly)
	return &s
}

func equalStringPtr(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// denySelfEditFields は ticket.self_edit だけを持つ呼び出し元が、開けていない
// 項目を送っていないかを見る（ApiDesign.md 9.5.2、DbDesign.md 6.13）。
//
// **ticket.edit を持っていれば何もしない。** 人が画面から編集する経路は
// 従来どおりで、**ticket.self_edit は ticket.edit の部分集合**である。
//
// **403 を返す**（422 ではない）。項目の値が誤っているのではなく、
// **その項目を書く権限が無い**からである——利用者が次に取る行動は
// 「値を直す」ではなく「権限を持つ人に頼む」になる。
func denySelfEditFields(r *http.Request, key string, raw updateTicketRequest) *apierr.Error {
	a := auth.ProjectAuthzFromContext(r.Context(), key)
	if a == nil {
		// ミドルウェアを通っていれば必ず入る。念のため、狭いほうとして扱う。
		return apierr.New(apierr.Forbidden).
			WithMessage("この項目を変更する権限がありません").
			WithCause(errors.New("権限の計算結果が文脈に無い"))
	}
	if auth.HasPermission(a.Permissions, permTicketEdit) {
		return nil
	}

	var denied []string
	for _, field := range selfEditDeniedFields {
		if _, ok := raw[field]; ok {
			denied = append(denied, field)
		}
	}
	if len(denied) == 0 {
		return nil
	}
	// **どの項目で断ったかを本文に出す。** 「権限がありません」だけでは、
	// 18項目のうちどれを外せば通るのかが分からない。
	return apierr.New(apierr.Forbidden).
		WithMessage(fmt.Sprintf("次の項目を変更する権限がありません: %s",
			strings.Join(denied, " / "))).
		WithCause(fmt.Errorf("ticket.self_edit では書けない項目が来た: %v", denied))
}

// 実績ポイントは PM の専用権限で更新する。ticket.edit だけでは足りない。
func requireActualPointPermission(r *http.Request, key string) *apierr.Error {
	a := auth.ProjectAuthzFromContext(r.Context(), key)
	if a != nil && auth.HasPermission(a.Permissions, permTicketActualPointEdit) {
		return nil
	}
	return apierr.New(apierr.Forbidden).WithMessage("実績ポイントを変更する権限がありません")
}
