// 指標の値と、周期の要約（Delegation.md 4.2・8.1 の 7・8.3）。pb-277。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/metrics/{metric}/values   ticket.create（+ 身元の確認）
//	GET  /api/v1/projects/{key}/tickets/{seq}/summary                   ticket.view
//
// **指標の定義は scope.metrics にある**（PM が承認する制約の一部。変えると範囲の変更）。
// 値は delegation_metric_value に追記のみで積む（PM の判断 2026-10-06）。
//
// **値を入れられるのは、人と、その委任の main だけ**（評価・他のエージェントは 403）。
// main の値は自己申告なので、入れた人を残し、要約で見分けられるようにする。
//
// **要約は PB が記録から組み立てる**（8.3。main に書かせない）。周期（scope.report_every_days）
// が過ぎたら、状態が読まれたときに PB が checkpoint の確認待ちを立てる（ensureCheckpoint）。
// PB は定期実行を持たないので、区切りは「読まれたとき」に判定する。checkpoint は
// ヒアリングと違い関門を立て、PM が答えるまで main を止める（PM の判断 2026-10-06）。
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// metricDef は scope.metrics の1件（4.2「成功の測り方」）。
type metricDef struct {
	Key       string   `json:"key"`
	Name      string   `json:"name"`
	Unit      string   `json:"unit,omitempty"`
	Target    *float64 `json:"target,omitempty"`
	Direction string   `json:"direction"` // higher | lower（良い向き）
}

const (
	metricsMax          = 10
	metricNameMaxLen    = 100
	metricUnitMaxLen    = 20
	metricNoteMaxLen    = 2000
	reportEveryDaysMax  = 365
	uqOpenCheckpointIdx = "uq_confirmation_open_checkpoint"
)

var metricKeyPattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

// metricsOfScope は scope から指標の定義を読む（検証済みの前提。読めなければ空）。
func metricsOfScope(scope []byte) []metricDef {
	var s struct {
		Metrics []metricDef `json:"metrics"`
	}
	_ = json.Unmarshal(scope, &s)
	return s.Metrics
}

// reportEveryDaysOf は scope から要約の周期（日数）を読む。無ければ 0（周期を持たない）。
func reportEveryDaysOf(scope []byte) int {
	var s struct {
		ReportEveryDays int `json:"report_every_days"`
	}
	_ = json.Unmarshal(scope, &s)
	return s.ReportEveryDays
}

// validateMetricsJSON は scope.metrics を確かめる。
func validateMetricsJSON(raw json.RawMessage, field string) []apierr.Detail {
	invalid := func(f, msg string) []apierr.Detail {
		return []apierr.Detail{{Field: f, Code: "invalid", Message: msg}}
	}
	var list []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &list); err != nil || list == nil {
		return invalid(field, "指標はオブジェクトの配列で指定してください")
	}
	if len(list) > metricsMax {
		return invalid(field, fmt.Sprintf("指標は%d件までです", metricsMax))
	}
	seen := map[string]bool{}
	var details []apierr.Detail
	for i, m := range list {
		f := fmt.Sprintf("%s[%d]", field, i)
		var d metricDef
		b, _ := json.Marshal(m)
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&d); err != nil {
			details = append(details, invalid(f, "指標の項目は key・name・unit・target・direction です")...)
			continue
		}
		switch {
		case !metricKeyPattern.MatchString(d.Key):
			details = append(details, invalid(f+".key", "key は英小文字・数字・_ の32文字までです")...)
		case seen[d.Key]:
			details = append(details, invalid(f+".key", "key が重複しています")...)
		case strings.TrimSpace(d.Name) == "" || utf8.RuneCountInString(d.Name) > metricNameMaxLen:
			details = append(details, invalid(f+".name", fmt.Sprintf("name は1〜%d文字で指定してください", metricNameMaxLen))...)
		case utf8.RuneCountInString(d.Unit) > metricUnitMaxLen:
			details = append(details, invalid(f+".unit", fmt.Sprintf("unit は%d文字までです", metricUnitMaxLen))...)
		case d.Direction != "higher" && d.Direction != "lower":
			details = append(details, invalid(f+".direction", "direction は higher か lower です")...)
		}
		seen[d.Key] = true
	}
	return details
}

// validateReportEveryDaysJSON は scope.report_every_days を確かめる。
func validateReportEveryDaysJSON(raw json.RawMessage, field string) []apierr.Detail {
	var n float64
	if err := json.Unmarshal(raw, &n); err != nil || n != math.Trunc(n) || n < 1 || n > reportEveryDaysMax {
		return []apierr.Detail{{Field: field, Code: "invalid",
			Message: fmt.Sprintf("要約の周期は1〜%d日の整数で指定してください", reportEveryDaysMax)}}
	}
	return nil
}

// ── POST .../metrics/{metric}/values ───────────────────────────

type recordMetricRequest struct {
	Value  *float64 `json:"value"`
	NoteMd string   `json:"note_md"`
}

type metricValueView struct {
	ID         string   `json:"id"`
	MetricKey  string   `json:"metric_key"`
	Value      float64  `json:"value"`
	NoteMd     string   `json:"note_md"`
	RecordedBy actorRef `json:"recorded_by"`
	RecordedAt Time     `json:"recorded_at"`
}

func (h *handler) recordDelegationMetric(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "POST /projects/{key}/tickets/{seq}/metrics/{metric}/values")
	if !ok {
		return
	}
	metricKey := chi.URLParam(r, "metric")
	var req recordMetricRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req.NoteMd = strings.TrimSpace(req.NoteMd)
	if req.Value == nil || math.IsNaN(*req.Value) || math.IsInf(*req.Value, 0) {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "value", Code: "required", Message: "値を数で指定してください",
		}))
		return
	}
	if utf8.RuneCountInString(req.NoteMd) > metricNoteMaxLen {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "note_md", Code: "invalid", Message: fmt.Sprintf("メモは%d文字までです", metricNoteMaxLen),
		}))
		return
	}

	deleg, err := h.q.GetDelegationByRootTicket(ctx, ticketID)
	if err != nil {
		if err == pgx.ErrNoRows {
			apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "seq", Code: "invalid", Message: "このチケットは自律実行の根ではありません",
			}))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	if deleg.Status == "ended" {
		apierr.Write(w, r, apierr.New(apierr.Conflict).WithMessage("自律実行は終了しています"))
		return
	}
	isMain := scope.actorID == deleg.MainActorID
	switch {
	case isMain && deleg.Status == "paused":
		apierr.Write(w, r, apierr.New(apierr.Forbidden).
			WithMessage("委任が停止中のため、指標の値を入れられません。再開は PM が行います").
			WithDetails(apierr.Detail{Field: "status", Code: "delegation_stage"}))
		return
	case !isMain && scope.actorKind != "user":
		apierr.Write(w, r, apierr.New(apierr.Forbidden).
			WithMessage("指標の値を入れられるのは、人とこの委任の main だけです"))
		return
	}
	found := false
	for _, m := range metricsOfScope(deleg.Scope) {
		found = found || m.Key == metricKey
	}
	if !found {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "metric", Code: "not_found", Message: "この自律実行に、その指標はありません",
		}))
		return
	}

	id := ulidgen.New()
	if err := h.q.CreateDelegationMetricValue(ctx, gen.CreateDelegationMetricValueParams{
		ID: id, DelegationID: deleg.ID, MetricKey: metricKey, Value: *req.Value,
		NoteMd: req.NoteMd, RecordedBy: scope.actorID,
	}); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("指標の値を保存できない: %w", err)))
		return
	}
	rows, err := h.q.ListDelegationMetricValues(ctx, deleg.ID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	for _, row := range rows {
		if row.ID == id {
			WriteJSON(w, http.StatusCreated, metricValueViewOf(row))
			return
		}
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(errors.New("保存した値を読めない")))
}

func metricValueViewOf(row gen.ListDelegationMetricValuesRow) metricValueView {
	return metricValueView{
		ID: row.ID, MetricKey: row.MetricKey, Value: row.Value, NoteMd: row.NoteMd,
		RecordedBy: actorRef{ID: row.RecordedByID, Kind: row.RecordedByKind, DisplayName: row.RecordedByName},
		RecordedAt: Time(row.RecordedAt),
	}
}

// ── GET .../summary ────────────────────────────────────────────

type summaryPlanView struct {
	Version       int32   `json:"version"`
	Status        string  `json:"status"`
	Class         *string `json:"class"`
	LatestVerdict *string `json:"latest_verdict"`
	ReasonMd      string  `json:"reason_md"`
	RevertsTo     *int32  `json:"reverts_to"`
}

type summaryMetricView struct {
	metricDef
	Values []metricValueView `json:"values"` // 期間内の値（古い順）
	Latest *metricValueView  `json:"latest"` // 期間の終わりまでの最新の値
}

type delegationSummaryView struct {
	From            Time                `json:"from"`
	To              Time                `json:"to"`
	ReportEveryDays int                 `json:"report_every_days"`
	Plans           []summaryPlanView   `json:"plans"`
	Metrics         []summaryMetricView `json:"metrics"`
	Budget          budgetStatusView    `json:"budget"`
	SummaryMd       string              `json:"summary_md"`
}

func (h *handler) getDelegationSummary(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r, "GET /projects/{key}/tickets/{seq}/summary")
	if !ok {
		return
	}
	deleg, err := h.q.GetDelegationByRootTicket(ctx, ticketID)
	if err != nil {
		if err == pgx.ErrNoRows {
			apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "seq", Code: "invalid", Message: "このチケットは自律実行の根ではありません",
			}))
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	var from, to time.Time
	var details []apierr.Detail
	for _, p := range []struct {
		name string
		dst  *time.Time
	}{{"from", &from}, {"to", &to}} {
		if raw := r.URL.Query().Get(p.name); raw != "" {
			ms, perr := parseEpochMillis(raw)
			if perr != nil {
				details = append(details, apierr.Detail{Field: p.name, Code: "invalid", Message: p.name + " はエポックミリ秒で指定してください"})
				continue
			}
			*p.dst = time.UnixMilli(ms)
		}
	}
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}
	if from.IsZero() {
		if from, err = h.q.GetCheckpointBase(ctx, deleg.ID); err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
			return
		}
	}
	if to.IsZero() {
		to = time.Now()
	}
	v, err := buildDelegationSummary(ctx, h.q, scope.projectID, deleg.ID, deleg.Scope, from, to)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

func parseEpochMillis(raw string) (int64, error) {
	var ms int64
	_, err := fmt.Sscanf(raw, "%d", &ms)
	if err != nil || fmt.Sprint(ms) != raw {
		return 0, errors.New("not epoch millis")
	}
	return ms, nil
}

// buildDelegationSummary は期間 [from, to) の要約を委任の記録から組み立てる（8.3）。
func buildDelegationSummary(
	ctx context.Context, q gen.Querier, projectID, delegationID string, scopeJSON []byte, from, to time.Time,
) (delegationSummaryView, error) {
	v := delegationSummaryView{From: Time(from), To: Time(to), ReportEveryDays: reportEveryDaysOf(scopeJSON),
		Plans: []summaryPlanView{}, Metrics: []summaryMetricView{}}

	plans, err := q.ListDelegationPlansBetween(ctx, gen.ListDelegationPlansBetweenParams{
		DelegationID: delegationID, Since: from, Until: to,
	})
	if err != nil {
		return v, fmt.Errorf("計画を読めない: %w", err)
	}
	for _, p := range plans {
		pv := summaryPlanView{Version: p.Version, Status: p.Status, ReasonMd: p.ReasonMd}
		if c := stricterClass(p.DeclaredClass.String, p.MeasuredClass.String); c != "" {
			pv.Class = &c
		}
		if p.LatestVerdict != "" {
			verdict := p.LatestVerdict
			pv.LatestVerdict = &verdict
		}
		if p.RevertsTo.Valid {
			pv.RevertsTo = &p.RevertsTo.Int32
		}
		v.Plans = append(v.Plans, pv)
	}

	values, err := q.ListDelegationMetricValues(ctx, delegationID)
	if err != nil {
		return v, fmt.Errorf("指標の値を読めない: %w", err)
	}
	for _, def := range metricsOfScope(scopeJSON) {
		mv := summaryMetricView{metricDef: def, Values: []metricValueView{}}
		for _, row := range values {
			if row.MetricKey != def.Key || !row.RecordedAt.Before(to) {
				continue
			}
			view := metricValueViewOf(row)
			mv.Latest = &view
			if !row.RecordedAt.Before(from) {
				mv.Values = append(mv.Values, view)
			}
		}
		v.Metrics = append(v.Metrics, mv)
	}

	usage, err := q.GetDelegationBudgetUsage(ctx, gen.GetDelegationBudgetUsageParams{DelegationID: delegationID, ProbeTicketID: ""})
	if err != nil {
		return v, fmt.Errorf("予算の消費を測れない: %w", err)
	}
	v.Budget = budgetStatusOf(scopeJSON, usage)

	loc, err := projectLocation(ctx, q, projectID)
	if err != nil {
		return v, err
	}
	v.SummaryMd = summaryMarkdown(v, loc)
	return v, nil
}

func stricterClass(a, b string) string {
	rank := map[string]int{"": 0, planClassMinor: 1, planClassStructural: 2, planClassScope: 3}
	if rank[b] > rank[a] {
		return b
	}
	return a
}

// summaryMarkdown は PM が画面で読む要約（GuiDesign.md 5.5「自律実行」。画面の語で書く）。
func summaryMarkdown(v delegationSummaryView, loc *time.Location) string {
	day := func(t Time) string { return time.Time(t).In(loc).Format("2006-01-02") }
	classLabel := map[string]string{planClassMinor: "軽微", planClassStructural: "構造", planClassScope: "範囲"}
	statusLabel := map[string]string{"proposed": "判定待ち", "accepted": "反映済み", "rejected": "却下",
		"superseded": "置き換え済み", "reverted": "差し戻し済み"}
	verdictLabel := map[string]string{"pass": "合格", "fail": "不合格", "unable": "判断できない"}

	var b strings.Builder
	fmt.Fprintf(&b, "**周期の要約**（%s 〜 %s）\n\n", day(v.From), day(v.To))
	b.WriteString("**計画をどう変えたか・なぜか**\n\n")
	if len(v.Plans) == 0 {
		b.WriteString("- この期間に出た計画の版はありません\n")
	}
	for _, p := range v.Plans {
		parts := []string{statusLabel[p.Status]}
		if p.Class != nil {
			parts = append([]string{classLabel[*p.Class]}, parts...)
		}
		if p.LatestVerdict != nil {
			parts = append(parts, "評価："+verdictLabel[*p.LatestVerdict])
		}
		reason := strings.ReplaceAll(strings.TrimSpace(p.ReasonMd), "\n", " ")
		if p.RevertsTo != nil {
			reason = fmt.Sprintf("第%d版への差し戻し。%s", *p.RevertsTo, reason)
		}
		fmt.Fprintf(&b, "- 第%d版（%s）：%s\n", p.Version, strings.Join(parts, "・"), reason)
	}
	b.WriteString("\n**指標はどう動いたか**\n\n")
	if len(v.Metrics) == 0 {
		b.WriteString("- 指標は決められていません\n")
	}
	for _, m := range v.Metrics {
		unit := m.Unit
		vals := make([]string, 0, len(m.Values))
		byMain := false
		for _, x := range m.Values {
			vals = append(vals, fmt.Sprintf("%g%s", x.Value, unit))
			byMain = byMain || x.RecordedBy.Kind == "agent"
		}
		line := "この期間の値はありません"
		if len(vals) > 0 {
			line = strings.Join(vals, " → ")
		} else if m.Latest != nil {
			line += fmt.Sprintf("（最新は %g%s）", m.Latest.Value, unit)
		}
		goal := ""
		if m.Target != nil {
			dir := "高いほど良い"
			if m.Direction == "lower" {
				dir = "低いほど良い"
			}
			goal = fmt.Sprintf("（目標 %g%s、%s）", *m.Target, unit, dir)
		}
		if byMain {
			line += "［エージェントが入れた値を含む］"
		}
		fmt.Fprintf(&b, "- %s：%s%s\n", m.Name, line, goal)
	}
	b.WriteString("\n**制約の消費**\n\n")
	l := v.Budget.Limits
	limit := func(used int64, max *int64, unit string) string {
		if max == nil {
			return fmt.Sprintf("%d%s", used, unit)
		}
		return fmt.Sprintf("%d / %d%s", used, *max, unit)
	}
	var depthMax *int64
	if l.Depth != nil {
		d := int64(*l.Depth)
		depthMax = &d
	}
	fmt.Fprintf(&b, "- 子チケット %s ・ 階層 %s ・ 申告されたトークン %d\n",
		limit(v.Budget.Tickets, l.Tickets, "件"), limit(int64(v.Budget.MaxDepth), depthMax, "段"), v.Budget.TokensTotal)
	return b.String()
}

// ── 周期の確認待ち（8.1 の 7）──────────────────────────────────

// ensureCheckpoint は、要約の周期が過ぎていれば checkpoint の確認待ちを立てる。
// delegation-status が読まれたときに呼ぶ（PB は定期実行を持たない）。承認済のときだけ判定し、
// 開いている checkpoint があれば立てない（uq_confirmation_open_checkpoint が二重を防ぐ）。
// raised_by は読んだ人に当てる——PB 自身の actor はまだ無い（予算の超過と同じ扱い）。
func (h *handler) ensureCheckpoint(
	ctx context.Context, projectID, delegationID, status, rootTicketID string, scopeJSON []byte, readerID string,
) error {
	days := reportEveryDaysOf(scopeJSON)
	if days <= 0 || status != "approved" {
		return nil
	}
	return h.tx.RunInTx(ctx, func(q gen.Querier) error {
		base, err := q.GetCheckpointBase(ctx, delegationID)
		if err != nil {
			return fmt.Errorf("周期の起点を読めない: %w", err)
		}
		now := time.Now()
		if now.Before(base.Add(time.Duration(days) * 24 * time.Hour)) {
			return nil
		}
		v, err := buildDelegationSummary(ctx, q, projectID, delegationID, scopeJSON, base, now)
		if err != nil {
			return err
		}
		question := v.SummaryMd + "\n要約を確認したら回答してください。回答するまで、エージェントは作業を止めて待ちます。" +
			"気になる変更は「計画と評価の履歴」から異議・差し戻しができます。"
		err = raiseSystemConfirmation(ctx, q, delegationID, rootTicketID, confirmationReasonCheckpoint, question, readerID)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == uqOpenCheckpointIdx {
			return errCheckpointExists
		}
		return err
	})
}

var errCheckpointExists = errors.New("checkpoint already open")
