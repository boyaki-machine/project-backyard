// POST /api/v1/projects/{key}/tickets/{seq}/reschedule（ApiDesign.md 9.17.2。pb-296）。
//
// **サーバが1回の要求・1トランザクションで計算して書く**（9.17.1）。プロジェクトの行を
// FOR NO KEY UPDATE で掴んで連鎖を直列にし、全行の列と依存で結ばれたチケットを FOR UPDATE で読み、純粋な関数
// （timeline_chain.go の planChain）で計算して、動く行だけ version を進めて書く。途中で失敗すれば
// トランザクションごと巻き戻るので、一部だけ動いた状態は残らない。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// on_block の値（9.17.1「解けないとき」）。
const (
	onBlockReject = "reject"
	onBlockShrink = "shrink"
)

// rescheduleRequest は 9.17.2 の本文。
//
// **expect はポインタで受ける。** 省略（照合しない）と空配列（きっかけの他は何も動かない、を照合する）を
// 分けるため。時刻は RawMessage で受け、どの欄が誤りかを 422 の details で返す。
type rescheduleRequest struct {
	Op      string              `json:"op"`
	StartAt json.RawMessage     `json:"start_at"`
	DueAt   json.RawMessage     `json:"due_at"`
	OnBlock string              `json:"on_block"`
	DryRun  bool                `json:"dry_run"`
	Expect  *[]rescheduleExpect `json:"expect"`
}

type rescheduleExpect struct {
	Seq     int32 `json:"seq"`
	Version int32 `json:"version"`
}

// rescheduleResult は 9.17.2 の応答（200）と、409 stale_plan の retrial。
type rescheduleResult struct {
	Applied     bool                   `json:"applied"`
	Result      string                 `json:"result"`
	Changes     []chainChange          `json:"changes"`
	Block       *chainBlock            `json:"block"`
	Alternative *rescheduleAlternative `json:"alternative"`
	// Items は確定のとき、動いた行を 9.2.7 の形で返す。試算では載せない。
	Items []timelineTicketItem `json:"items,omitempty"`
}

type rescheduleAlternative struct {
	Changes []chainChange `json:"changes"`
}

// parseRescheduleRequest は本文の形だけで判定できる検証を済ませる。
func parseRescheduleRequest(req rescheduleRequest) (start, due *int64, details []apierr.Detail) {
	switch req.Op {
	case "", rescheduleOpMove, rescheduleOpCompact:
	default:
		details = append(details, apierr.Detail{Field: "op", Code: "invalid", Message: "op は move または compact で指定してください"})
	}
	switch req.OnBlock {
	case "", onBlockReject, onBlockShrink:
	default:
		details = append(details, apierr.Detail{Field: "on_block", Code: "invalid", Message: "on_block は reject または shrink で指定してください"})
	}
	var so, do optional[time.Time]
	so, details = parseOptionalInstant(req.StartAt, "start_at", details)
	do, details = parseOptionalInstant(req.DueAt, "due_at", details)
	for _, f := range []struct {
		name string
		o    optional[time.Time]
		dst  **int64
	}{{"start_at", so, &start}, {"due_at", do, &due}} {
		switch {
		case f.o.Set && f.o.Null:
			details = append(details, apierr.Detail{Field: f.name, Code: "invalid", Message: "連鎖のずらしでは予定を空にできません"})
		case f.o.Set:
			ms := f.o.Value.UnixMilli()
			*f.dst = &ms
		}
	}
	if req.Op == rescheduleOpCompact && (start != nil || due != nil) {
		details = append(details, apierr.Detail{Field: "op", Code: "invalid", Message: "ここで詰める（compact）では開始・終了を送りません"})
	}
	// move で開始も終了も送らなければ、きっかけは動かさずに後ろを計算し直す（依存を作った直後にそろえる。
	// 9.17.2。pb-332）。端の欠けたきっかけは checkRescheduleTarget が not_timed で弾く
	return start, due, details
}

// rescheduleTicket は連鎖を計算して返す（試算）か、書く（確定）。
func (h *handler) rescheduleTicket(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/tickets/{seq}/reschedule")
	if !ok {
		return
	}
	seq, seqErr := ticketSeqParam(r)
	if seqErr != nil {
		apierr.Write(w, r, seqErr)
		return
	}
	var req rescheduleRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	version, versionErr := parseIfMatch(r)
	reqStart, reqDue, details := parseRescheduleRequest(req)
	var bodyErr *apierr.Error
	if len(details) > 0 {
		bodyErr = apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	if e := mergeValidationErrors(versionErr, bodyErr); e != nil {
		apierr.Write(w, r, e)
		return
	}
	op := req.Op
	if op == "" {
		op = rescheduleOpMove
	}

	ctx := r.Context()
	rec := activity.FromRequest(r)
	var (
		result   rescheduleResult
		writeErr *apierr.Error
	)
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// プロジェクトの行を掴み、連鎖のずらしと依存の追加を直列にする（9.17.2「同時更新の扱い」）。
		// 依存は行をまたぐので、行（エピック）ごとに掴むと2つの連鎖が互いの行を待ち合う。
		if _, err := q.LockProjectForDependency(ctx, projectID); err != nil {
			return fmt.Errorf("プロジェクトを掴めない: %w", err)
		}
		project, err := q.GetProjectTimelineSettings(ctx, projectID)
		if err != nil {
			return fmt.Errorf("プロジェクトの設定を読めない: %w", err)
		}
		settings := readTimelineSettings(project.Settings)
		loc, err := time.LoadLocation(project.Timezone)
		if err != nil {
			return fmt.Errorf("基準タイムゾーン %q を解決できない: %w", project.Timezone, err)
		}
		minLen := settings.SnapSeconds * 1000

		tgt, err := q.GetRescheduleTicket(ctx, gen.GetRescheduleTicketParams{ProjectID: projectID, Seq: seq})
		if errors.Is(err, pgx.ErrNoRows) {
			writeErr = ticketNotFound(seq)
			return errTicketReference
		} else if err != nil {
			return fmt.Errorf("チケット %d を読めない: %w", seq, err)
		}
		if tgt.Version != version {
			writeErr = apierr.New(apierr.Conflict).
				WithMessage("他の利用者がこのチケットを更新しました。内容を読み直してからやり直してください")
			return errTicketReference
		}

		category := fallbackStatusCategory
		if tgt.StatusCategory.Valid {
			category = tgt.StatusCategory.String
		}
		target := rescheduleTarget{
			Start: msPtr(tgt.StartAt), Due: msPtr(tgt.DueAt), AllDay: tgt.AllDay,
			StartPinned: tgt.StartPinned, DuePinned: tgt.DuePinned,
			Started: category != "todo", Done: category == "done", ClosedAt: msPtr(tgt.ClosedAt),
		}
		newSpan, d := checkRescheduleTarget(target, op, reqStart, reqDue, minLen)
		if len(d) > 0 {
			writeErr = apierr.New(apierr.ValidationFailed).WithDetails(d...)
			return errTicketReference
		}

		curSpan := newSpan // 端が欠けていたきっかけは、新しい予定の位置で列に並べる
		if target.Start != nil && target.Due != nil {
			curSpan = chainSpan{Start: *target.Start, Due: *target.Due}
		}
		plan := chainPlan{
			Target: chainItem{
				Seq: tgt.Seq, Title: tgt.Title, Span: curSpan,
				StartPinned: tgt.StartPinned && target.Start != nil, DuePinned: tgt.DuePinned && target.Due != nil,
				Started: target.Started, Done: target.Done, Version: tgt.Version,
			},
			NewSpan: newSpan, Compact: op == rescheduleOpCompact,
			Ends: map[int32]chainEnds{}, MinLen: minLen, Now: time.Now().UnixMilli(),
			Shrink: req.OnBlock == onBlockShrink, Loc: loc,
		}
		idBySeq := map[int32]string{tgt.Seq: tgt.ID}
		spanBySeq := map[int32]chainSpan{tgt.Seq: curSpan}
		known := map[int32]bool{tgt.Seq: true}
		// add は読んだチケットを、動かせるなら列か列の外へ、動かせなければ端だけを計画に入れる（9.17.1「動かせる」）
		columns := map[int32][]chainItem{}
		add := func(epicSeq int32, inRow, noOverlap bool, c chainCandidate) {
			idBySeq[c.Seq] = c.ID
			known[c.Seq] = true
			it, movable := c.item()
			switch {
			case !movable:
				plan.Ends[c.Seq] = chainEnds{Start: msPtr(c.StartAt), Due: msPtr(c.DueAt)}
			case inRow && noOverlap:
				spanBySeq[c.Seq] = it.Span
				columns[epicSeq] = append(columns[epicSeq], it)
			default:
				spanBySeq[c.Seq] = it.Span
				plan.Others = append(plan.Others, it)
			}
		}

		// 列（9.17.1）。タイムライン対象エピックの全行を読む——依存で別の行の列へ届きうる
		members, err := q.ListTimelineMembersForUpdate(ctx, gen.ListTimelineMembersForUpdateParams{ProjectID: projectID, EpicSeqs: settings.Epics})
		if err != nil {
			return fmt.Errorf("行のチケットを読めない: %w", err)
		}
		for _, m := range members {
			if m.Seq == tgt.Seq {
				idBySeq[m.Seq] = m.ID
				if tgt.NoOverlap {
					columns[m.EpicSeq] = append(columns[m.EpicSeq], plan.Target)
				}
				continue
			}
			add(m.EpicSeq, true, m.NoOverlap, chainCandidate{
				ID: m.ID, Seq: m.Seq, Title: m.Title, StartAt: m.StartAt, DueAt: m.DueAt, AllDay: m.AllDay,
				StartPinned: m.StartPinned, DuePinned: m.DuePinned, Version: m.Version, StatusCategory: m.StatusCategory,
			})
		}

		// 依存（FS / SS / FF / SF）。プロジェクトの全部で、行の外の端も読む（pb-332）
		links, err := q.ListScheduleLinks(ctx, projectID)
		if err != nil {
			return fmt.Errorf("依存を読めない: %w", err)
		}
		var outside []string
		seen := map[string]bool{}
		for _, l := range links {
			plan.Links = append(plan.Links, chainLink{
				Pred: l.SourceSeq, Succ: l.TargetSeq, Type: l.LinkType, LagDays: l.LagDays, LagSeconds: l.LagSeconds,
			})
			for _, e := range []struct {
				seq int32
				id  string
			}{{l.SourceSeq, l.SourceID}, {l.TargetSeq, l.TargetID}} {
				if !known[e.seq] && !seen[e.id] {
					seen[e.id] = true
					outside = append(outside, e.id)
				}
			}
		}
		if len(outside) > 0 {
			rows, err := q.ListChainTicketsForUpdate(ctx, gen.ListChainTicketsForUpdateParams{ProjectID: projectID, Ids: outside})
			if err != nil {
				return fmt.Errorf("依存で結ばれたチケットを読めない: %w", err)
			}
			for _, m := range rows {
				add(0, false, false, chainCandidate{
					ID: m.ID, Seq: m.Seq, Title: m.Title, StartAt: m.StartAt, DueAt: m.DueAt, AllDay: m.AllDay,
					StartPinned: m.StartPinned, DuePinned: m.DuePinned, Version: m.Version, StatusCategory: m.StatusCategory,
				})
			}
		}
		for _, e := range settings.Epics {
			if col := columns[e]; len(col) > 0 {
				plan.Columns = append(plan.Columns, col)
			}
		}

		changes, block := planChain(plan)

		// エージェントは委任の範囲の判定を、動かす全行の ID で掛ける（9.17.2）
		moving := make([]string, 0, len(changes)+1)
		moving = append(moving, tgt.ID)
		for _, c := range changes {
			if c.Seq != tgt.Seq {
				moving = append(moving, idBySeq[c.Seq])
			}
		}
		if writeErr = planningWriteProblem(ctx, q, r, projectID, moving...); writeErr != nil {
			return errTicketReference
		}

		if block != nil {
			var alt *rescheduleAlternative
			if !plan.Shrink && block.Code == chainCannotFit {
				shrinkPlan := plan
				shrinkPlan.Shrink = true
				if altChanges, altBlock := planChain(shrinkPlan); altBlock == nil {
					alt = &rescheduleAlternative{Changes: altChanges}
				}
			}
			if req.DryRun {
				// 試算では解けないことも答えの1つなので 200（9.17.2）
				result = rescheduleResult{Result: "blocked", Changes: changes, Block: block, Alternative: alt}
				return nil
			}
			field := "due_at"
			if block.Code == chainOverlapsPrevious {
				field = "start_at"
			}
			writeErr = apierr.New(apierr.ValidationFailed).WithMessage(block.Message).
				WithDetails(apierr.Detail{Field: field, Code: block.Code, Message: block.Message})
			return errTicketReference
		}

		result = rescheduleResult{Result: "ok", Changes: changes}
		if req.DryRun {
			return nil
		}

		// 試算の後に誰かが動かしていないか（expect。9.17.2）。食い違えば計算し直した試算を載せて 409
		if req.Expect != nil && !expectMatches(*req.Expect, changes, tgt.Seq) {
			writeErr = apierr.New(apierr.Conflict).
				WithMessage("試算の後に他の変更があり、動くチケットが変わりました。内容を確かめてからやり直してください").
				WithDetails(apierr.Detail{Field: "expect", Code: "stale_plan", Message: "試算の後に、動くチケットの顔ぶれか版が変わりました"}).
				WithRetrial(rescheduleResult{Result: "ok", Changes: changes})
			return errTicketReference
		}

		// 確定。動く行だけ version を進めて書き、変更履歴を同じ request_id で残す（9.17.2「変更履歴」）
		changedIDs := make([]string, 0, len(changes))
		for i, c := range changes {
			id := idBySeq[c.Seq]
			before := spanBySeq[c.Seq]
			start, due := time.UnixMilli(c.After.Start).UTC(), time.UnixMilli(c.After.Due).UTC()
			v, err := q.SetTicketSchedule(ctx, gen.SetTicketScheduleParams{ID: id, StartAt: &start, DueAt: &due, Version: c.Version})
			if errors.Is(err, pgx.ErrNoRows) {
				writeErr = apierr.New(apierr.Conflict).WithMessage("他の利用者がチケットを更新しました。読み直してからやり直してください")
				return errTicketReference
			} else if err != nil {
				return fmt.Errorf("チケット %d の予定を書けない: %w", c.Seq, err)
			}
			changes[i].Version = v
			changedIDs = append(changedIDs, id)

			// 変更履歴の「前」は、きっかけは今の値（欠けた端は null）、列の行は読んだ値
			var oldStart, oldDue *time.Time
			if c.Seq == tgt.Seq {
				oldStart, oldDue = tgt.StartAt, tgt.DueAt
			} else {
				s, d := time.UnixMilli(before.Start), time.UnixMilli(before.Due)
				oldStart, oldDue = &s, &d
			}
			for _, f := range []struct {
				field    string
				old, new *time.Time
				isDue    bool
			}{{"start_at", oldStart, &start, false}, {"due_at", oldDue, &due, true}} {
				o := scheduleActivityValue(f.old, false, f.isDue, loc)
				n := scheduleActivityValue(f.new, false, f.isDue, loc)
				if equalStringPtr(o, n) {
					continue
				}
				field := f.field
				if err := rec.Record(ctx, q, activity.Entry{
					ProjectID: projectID, EntityType: activity.EntityTicket, EntityID: id,
					Action: activity.Update, Field: &field, OldValue: o, NewValue: n,
				}); err != nil {
					return err
				}
			}
		}
		result.Applied = true
		result.Changes = changes

		items, err := rescheduledItems(ctx, q, projectID, changedIDs, settings.Epics)
		if err != nil {
			return err
		}
		result.Items = items
		return nil
	})
	switch {
	case writeErr != nil:
		apierr.Write(w, r, writeErr)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケット %d の連鎖のずらしに失敗した: %w", seq, err)))
		return
	}
	if result.Changes == nil {
		result.Changes = []chainChange{}
	}
	WriteJSON(w, http.StatusOK, result)
}

// chainCandidate は計画へ入れる前のチケット1件（行のチケットと、依存で結ばれた行の外のチケット）。
type chainCandidate struct {
	ID, Title              string
	Seq, Version           int32
	StartAt, DueAt         *time.Time
	AllDay                 bool
	StartPinned, DuePinned bool
	StatusCategory         pgtype.Text
}

// item は計算に渡す形にする。時刻付きで両端そろわなければ動かせない（9.17.1「動かせる」）。
func (c chainCandidate) item() (chainItem, bool) {
	if c.AllDay || c.StartAt == nil || c.DueAt == nil {
		return chainItem{}, false
	}
	cat := fallbackStatusCategory
	if c.StatusCategory.Valid {
		cat = c.StatusCategory.String
	}
	return chainItem{
		Seq: c.Seq, Title: c.Title, Span: chainSpan{Start: c.StartAt.UnixMilli(), Due: c.DueAt.UnixMilli()},
		StartPinned: c.StartPinned, DuePinned: c.DuePinned,
		Started: cat != "todo", Done: cat == "done", Version: c.Version,
	}, true
}

// expectMatches は試算で示した行（きっかけを除く）と、計算し直した結果が同じかを見る（9.17.2）。
// 動かす行の集合と版の両方が一致しなければ偽。
func expectMatches(expect []rescheduleExpect, changes []chainChange, targetSeq int32) bool {
	want := map[int32]int32{}
	for _, e := range expect {
		want[e.Seq] = e.Version
	}
	got := map[int32]int32{}
	for _, c := range changes {
		if c.Seq != targetSeq {
			got[c.Seq] = c.Version
		}
	}
	if len(want) != len(got) {
		return false
	}
	for s, v := range got {
		if w, ok := want[s]; !ok || w != v {
			return false
		}
	}
	return true
}

// rescheduledItems は確定で動いた行を 9.2.7 の items の形で読み直す（画面が取り直さずに差し替える）。
// 一覧と同じ ListTickets を ID で絞って通すので、形が割れない。
func rescheduledItems(ctx context.Context, q gen.Querier, projectID string, ids []string, epics []int32) ([]timelineTicketItem, error) {
	if len(ids) == 0 {
		return []timelineTicketItem{}, nil
	}
	rows, err := q.ListTickets(ctx, gen.ListTicketsParams{
		ProjectID: projectID, StatusKeys: []string{}, StatusCategories: []string{}, Types: []string{},
		Priorities: []string{}, AssigneeIds: []string{}, TagIds: []string{}, SprintIds: []string{},
		OpenFilter: filterAll, StaleDays: -1, ParentSeqs: []int32{},
		KeywordSet: true, KeywordIds: ids, IncludeRetired: true,
		TimelineEpicSeqs: epics, Sort: "seq", SortOrder: string(OrderAsc),
		PageLimit: int32(len(ids)), PageOffset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("動いたチケットを読み直せない: %w", err)
	}
	tickets := make([]string, 0, len(rows))
	for _, row := range rows {
		tickets = append(tickets, row.ID)
	}
	tags, err := ticketTagsFor(ctx, q, tickets)
	if err != nil {
		return nil, err
	}
	items := make([]ticketListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildTicketListItem(row, tags[row.ID]))
	}
	return timelineItemsFor(ctx, q, rows, items)
}

// msPtr は NULL になりうる時刻をエポックミリ秒にする。
func msPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}
