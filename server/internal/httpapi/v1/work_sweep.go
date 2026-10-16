package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 作業記録の浮きの片付け（Design.md 8.5.7、ApiDesign.md 11.14、GuiDesign.md 5.12.3）。
//
// **閉じ方は abandon と同じである。** 最後の区間を数えずに欠測の印を付け、終了時刻を推定しない。

// WorkSweepInterval は日次の処理の間隔。**設定にしない**——閾値は時間単位なので、1日1回で足りる。
const WorkSweepInterval = 24 * time.Hour

// workSweepMaxIDs は POST で受ける記録の上限（ApiDesign.md 11.14）。
const workSweepMaxIDs = 500

const (
	sweepReasonClosed = "ticket_closed"
	sweepReasonStale  = "stale"
	sweepTriggerDaily = "daily"
	sweepTriggerUser  = "manual"
)

type workSweepActor struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Kind        string `json:"kind"`
}

type workSweepItem struct {
	WorkID       string         `json:"work_id"`
	ProjectKey   string         `json:"project_key"`
	ProjectName  string         `json:"project_name"`
	TicketSeq    int32          `json:"ticket_seq"`
	TicketTitle  string         `json:"ticket_title"`
	Actor        workSweepActor `json:"actor"`
	State        string         `json:"state"`
	StartedAt    int64          `json:"started_at"`
	LastActiveAt int64          `json:"last_active_at"`
	Reason       string         `json:"reason"`
}

type workSweepLastRun struct {
	RanAt       int64   `json:"ran_at"`
	Trigger     string  `json:"trigger"`
	ActorLabel  *string `json:"actor_label"`
	ClosedCount int     `json:"closed_count"`
}

type workSweepView struct {
	ThresholdHours int               `json:"threshold_hours"`
	CheckedAt      int64             `json:"checked_at"`
	Items          []workSweepItem   `json:"items"`
	LastRun        *workSweepLastRun `json:"last_run"`
}

type workSweepResult struct {
	workSweepView
	ClosedIDs    []string `json:"closed_ids"`
	SkippedCount int      `json:"skipped_count"`
}

// WorkSweeper は浮きを数えて閉じる。画面の口と日次の処理が同じものを使う。
type WorkSweeper struct {
	Tx       TxRunner
	Q        gen.Querier
	Settings *config.Live
	// Now は時計。nil なら time.Now。**結合テストが差し替える。**
	Now func() time.Time
}

func (w WorkSweeper) now() time.Time {
	if w.Now != nil {
		return w.Now()
	}
	return time.Now()
}

// thresholdHours は設定 work_stale_hours の実効値。0 は時間で判定しない。
func (w WorkSweeper) thresholdHours() int {
	settings := w.Settings
	if settings == nil {
		settings = config.LiveDefaults()
	}
	n, err := strconv.Atoi(settings.Snapshot().String(config.KeyWorkStaleHours))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// classifySweep は開いた記録が浮きに当たるかを判定する。
//
// 最後の活動は、エージェントなら記録の最終変更とトークンの最終使用の遅いほう、人は記録の最終変更。
// **配分先がすべて完了していれば、時間に関わらず対象にする。**
func classifySweep(row gen.ListOpenWorkForSweepRow, now time.Time, hours int) (reason string, lastActive time.Time) {
	lastActive = row.ChangedAt
	if row.ActorKind == auth.ActorKindAgent && row.TokenLastUsedAt.After(lastActive) {
		lastActive = row.TokenLastUsedAt
	}
	switch {
	case row.AllClosed:
		return sweepReasonClosed, lastActive
	case hours > 0 && now.Sub(lastActive) >= time.Duration(hours)*time.Hour:
		return sweepReasonStale, lastActive
	}
	return "", lastActive
}

func sweepItemOf(row gen.ListOpenWorkForSweepRow, reason string, lastActive time.Time) workSweepItem {
	return workSweepItem{
		WorkID: row.ID, ProjectKey: row.ProjectKey, ProjectName: row.ProjectName,
		TicketSeq: row.TicketSeq, TicketTitle: row.TicketTitle,
		Actor: workSweepActor{ID: row.ActorID, DisplayName: row.ActorName, Kind: row.ActorKind},
		State: row.State, StartedAt: row.StartedAt.UnixMilli(), LastActiveAt: lastActive.UnixMilli(),
		Reason: reason,
	}
}

// View は浮きを数える。何も変えない。
func (w WorkSweeper) View(ctx context.Context) (workSweepView, error) {
	now := w.now()
	hours := w.thresholdHours()
	view := workSweepView{ThresholdHours: hours, CheckedAt: now.UnixMilli(), Items: []workSweepItem{}}
	rows, err := w.Q.ListOpenWorkForSweep(ctx, pgtype.Text{})
	if err != nil {
		return view, err
	}
	for _, row := range rows {
		if reason, last := classifySweep(row, now, hours); reason != "" {
			view.Items = append(view.Items, sweepItemOf(row, reason, last))
		}
	}
	sort.SliceStable(view.Items, func(i, j int) bool { return view.Items[i].LastActiveAt < view.Items[j].LastActiveAt })
	view.LastRun, err = w.lastRun(ctx)
	return view, err
}

func (w WorkSweeper) lastRun(ctx context.Context) (*workSweepLastRun, error) {
	row, err := w.Q.GetLatestAuditByAction(ctx, string(audit.WorkSweep))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var detail struct {
		Trigger     string `json:"trigger"`
		ClosedCount int    `json:"closed_count"`
	}
	if err := json.Unmarshal(row.Detail, &detail); err != nil {
		return nil, err
	}
	out := &workSweepLastRun{RanAt: row.OccurredAt.UnixMilli(), Trigger: detail.Trigger, ClosedCount: detail.ClosedCount}
	// **日次の処理は誰の操作でもない**ので、操作者の名前を出さない（ApiDesign.md 11.14）。
	if detail.Trigger != sweepTriggerDaily && row.ActorLabel.Valid {
		out.ActorLabel = &row.ActorLabel.String
	}
	return out, nil
}

// Close は渡された記録のうち、**閉じる直前に判定し直して**まだ浮きに当たるものだけを閉じ、
// 監査ログ work.sweep を1行残す。
func (w WorkSweeper) Close(ctx context.Context, ids []string, trigger string, rec *audit.Recorder, act *activity.Recorder) (closed []string, skipped int, err error) {
	closed = []string{}
	hours := w.thresholdHours()
	type closedEntry struct {
		WorkID     string `json:"work_id"`
		ProjectKey string `json:"project_key"`
		TicketSeq  int32  `json:"ticket_seq"`
		Reason     string `json:"reason"`
	}
	entries := []closedEntry{}
	for _, id := range ids {
		var entry *closedEntry
		err = w.Tx.RunInTx(ctx, func(q gen.Querier) error {
			s, e := q.GetWorkSessionByID(ctx, id)
			if errors.Is(e, pgx.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			// 作業記録の操作と同じロックで、終了・整理と排他する。
			if e = q.LockWorkProject(ctx, s.ProjectID); e != nil {
				return e
			}
			rows, e := q.ListOpenWorkForSweep(ctx, pgtype.Text{String: id, Valid: true})
			if e != nil || len(rows) == 0 {
				return e
			}
			now := w.now()
			reason, last := classifySweep(rows[0], now, hours)
			if reason == "" {
				return nil
			}
			if e = abandonStaleWork(ctx, q, s, reason, last, now, rec, act); e != nil {
				return e
			}
			entry = &closedEntry{WorkID: s.ID, ProjectKey: rows[0].ProjectKey, TicketSeq: rows[0].TicketSeq, Reason: reason}
			return nil
		})
		if err != nil {
			return closed, skipped, err
		}
		if entry == nil {
			skipped++
			continue
		}
		closed = append(closed, entry.WorkID)
		entries = append(entries, *entry)
	}
	err = rec.Record(ctx, w.Q, audit.Entry{
		Action: audit.WorkSweep, Result: audit.Success,
		Detail: map[string]any{"trigger": trigger, "threshold_hours": hours, "closed_count": len(entries), "closed": entries},
	})
	return closed, skipped, err
}

// abandonStaleWork は記録を abandon と同じ扱いで閉じる（呼び出し側がトランザクションとロックを持つ）。
func abandonStaleWork(ctx context.Context, q gen.Querier, s gen.WorkSession, reason string, last, now time.Time, rec *audit.Recorder, act *activity.Recorder) error {
	why := "チケットが完了していた"
	if reason == sweepReasonStale {
		why = "最後の活動から時間が経っていた"
	}
	s.State = "finished"
	s.ChangedAt = now
	s.EndedAt = &now
	s.Result = usageText("blocked")
	s.Outcome = usageText("abandoned")
	s.Incomplete = true
	s.TimeMissing = true
	s.Note = fmt.Sprintf("浮きとして閉じた（%s。最後の活動 %s）", why, last.UTC().Format(time.RFC3339))
	if err := q.UpdateWorkSession(ctx, gen.UpdateWorkSessionParams{Outcome: s.Outcome, ID: s.ID, State: s.State, ChangedAt: s.ChangedAt, EndedAt: s.EndedAt, ActiveMs: s.ActiveMs, WaitingMs: s.WaitingMs, ReportedActiveMs: s.ReportedActiveMs, Result: s.Result, Tokens: s.Tokens, Note: s.Note, Incomplete: s.Incomplete}); err != nil {
		return err
	}
	if err := q.SetWorkMissingTime(ctx, gen.SetWorkMissingTimeParams{ID: s.ID, TimeMissing: true}); err != nil {
		return err
	}
	// 承認待ちで止まっていた記録は、保留も解く（DbDesign.md 6.34）。最後の区間は数えないので、
	// 承認待ちの累計にも足さない。
	if s.Hold.Valid {
		s.Hold = usageText("")
		if err := q.SetWorkHold(ctx, gen.SetWorkHoldParams{ID: s.ID, State: s.State, ChangedAt: s.ChangedAt, ActiveMs: s.ActiveMs, WaitingMs: s.WaitingMs, Hold: s.Hold, HoldMs: s.HoldMs}); err != nil {
			return err
		}
	}
	if s.ClientKind.Valid {
		if err := closeAgentAttempt(ctx, q, s, rec); err != nil {
			return err
		}
	}
	allocations, err := q.ListWorkAllocations(ctx, s.ID)
	if err != nil {
		return err
	}
	for _, a := range allocations {
		if err = recalculateEffort(ctx, q, a.TicketID); err != nil {
			return err
		}
	}
	field, value := "work_session", "sweep:"+s.ID
	return act.Record(ctx, q, activity.Entry{ProjectID: s.ProjectID, EntityType: activity.EntityTicket, EntityID: s.TicketID, Action: activity.Update, Field: &field, NewValue: &value})
}

// closeAgentAttempt はエージェントの記録を閉じたときの監視の記録を残す。
// 終了・abandon と浮きの片付けが共有する。failed / abandoned なら実行記録も作る。
func closeAgentAttempt(ctx context.Context, q gen.Querier, s gen.WorkSession, rec *audit.Recorder) error {
	before, e := agentMonitorState(ctx, q, s.ProjectID, s.ActorID, s.TicketID, false)
	if e != nil {
		return e
	}
	if e = q.FinishAgentAttempt(ctx, gen.FinishAgentAttemptParams{ID: s.ID, EndedAt: s.EndedAt, Outcome: s.Outcome}); e != nil {
		return e
	}
	after, e := agentMonitorState(ctx, q, s.ProjectID, s.ActorID, s.TicketID, false)
	if e != nil {
		return e
	}
	if after.Stopped && !before.Stopped {
		if e = rec.Record(ctx, q, audit.Entry{Action: audit.AgentUpdate, Result: audit.Success, TargetType: "agent", TargetID: s.ActorID, Detail: map[string]any{"monitor_stop_reasons": after.Reasons, "project_id": s.ProjectID, "ticket_id": s.TicketID, "work_session_id": s.ID}}); e != nil {
			return e
		}
	}
	if s.Outcome.String != "failed" && s.Outcome.String != "abandoned" {
		return nil
	}
	principal := &auth.Principal{ActorID: s.ActorID, ActorKind: auth.ActorKindAgent}
	runID := ulidgen.New()
	run, e := buildAgentRun(ctx, q, runID, s.TicketID, principal, reportRequest{})
	if e != nil {
		return e
	}
	run.StartedAt = s.StartedAt
	run.EndedAt = s.EndedAt
	run.Status = s.Outcome.String
	run.ClientKind = s.ClientKind
	run.ModelName = s.ModelName
	run.ModelVersion = s.ModelVersion
	run.TokensUsed = s.Tokens
	if e = q.CreateAgentRun(ctx, run); e != nil {
		return e
	}
	return q.LinkFailedAgentRun(ctx, gen.LinkFailedAgentRunParams{ID: runID, WorkSessionID: usageText(s.ID)})
}

// RunDaily は日次の処理の1回分。**閾値が0なら何もせず、記録も残さない。**
func (w WorkSweeper) RunDaily(ctx context.Context) (int, error) {
	if w.thresholdHours() == 0 {
		return 0, nil
	}
	view, err := w.View(ctx)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(view.Items))
	for _, it := range view.Items {
		ids = append(ids, it.WorkID)
	}
	closed, _, err := w.Close(ctx, ids, sweepTriggerDaily, audit.FromCLI("日次の処理"), activity.System("日次の処理"))
	return len(closed), err
}

func (h *handler) workSweeper() WorkSweeper {
	return WorkSweeper{Tx: h.tx, Q: h.q, Settings: h.settings}
}

// getWorkSweep は GET /api/v1/admin/work-sweep（ApiDesign.md 11.14）。
func (h *handler) getWorkSweep(w http.ResponseWriter, r *http.Request) {
	view, err := h.workSweeper().View(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// runWorkSweep は POST /api/v1/admin/work-sweep（ApiDesign.md 11.14）。
func (h *handler) runWorkSweep(w http.ResponseWriter, r *http.Request) {
	var req struct {
		WorkIDs []string `json:"work_ids"`
	}
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if len(req.WorkIDs) == 0 || len(req.WorkIDs) > workSweepMaxIDs {
		apierr.Write(w, r, usageInvalid("work_ids", "閉じる作業記録を1〜500件で指定してください"))
		return
	}
	seen := map[string]bool{}
	ids := []string{}
	for _, id := range req.WorkIDs {
		if len(id) != 26 {
			apierr.Write(w, r, usageInvalid("work_ids", "作業記録の ID が正しくありません"))
			return
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	sweeper := h.workSweeper()
	closed, skipped, err := sweeper.Close(r.Context(), ids, sweepTriggerUser, audit.FromRequest(r), activity.FromRequest(r))
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	view, err := sweeper.View(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, workSweepResult{workSweepView: view, ClosedIDs: closed, SkippedCount: skipped})
}
