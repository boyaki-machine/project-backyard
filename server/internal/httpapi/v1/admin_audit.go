package v1

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

type auditFilters struct {
	fromAt, toAt             *time.Time
	action, actor, result, q string
}

func parseAuditFilters(r *http.Request) (auditFilters, *apierr.Error) {
	q := r.URL.Query()
	f := auditFilters{action: likePattern(q.Get("action")), actor: likePattern(q.Get("actor")), q: likePattern(q.Get("q")), result: q.Get("result")}
	var details []apierr.Detail
	parseTime := func(key string) *time.Time {
		v := q.Get(key)
		if v == "" {
			return nil
		}
		ms, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			details = append(details, apierr.Detail{Field: key, Code: "invalid", Message: key + " はエポックミリ秒で指定してください"})
			return nil
		}
		t := time.UnixMilli(ms).UTC()
		if t.Year() < 1 || t.Year() > 9999 {
			details = append(details, apierr.Detail{Field: key, Code: "out_of_range", Message: key + " の日時が範囲外です"})
			return nil
		}
		return &t
	}
	f.fromAt, f.toAt = parseTime("from_at"), parseTime("to_at")
	if f.fromAt != nil && f.toAt != nil && !f.fromAt.Before(*f.toAt) {
		details = append(details, apierr.Detail{Field: "to_at", Code: "out_of_range", Message: "終了日時は開始日時より後にしてください"})
	}
	if f.result != "" && f.result != "success" && f.result != "failure" {
		details = append(details, apierr.Detail{Field: "result", Code: "invalid", Message: "result は success / failure で指定してください"})
	}
	if len(details) > 0 {
		return f, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return f, nil
}

type auditLogItem struct {
	ID         string          `json:"id"`
	OccurredAt Time            `json:"occurred_at"`
	ActorID    *string         `json:"actor_id"`
	ActorKind  *string         `json:"actor_kind"`
	ActorName  *string         `json:"actor_name"`
	TokenID    *string         `json:"token_id"`
	IP         *string         `json:"ip"`
	UserAgent  *string         `json:"user_agent"`
	Action     string          `json:"action"`
	TargetType *string         `json:"target_type"`
	TargetID   *string         `json:"target_id"`
	Result     string          `json:"result"`
	Detail     json.RawMessage `json:"detail"`
	RequestID  *string         `json:"request_id"`
}

func auditItem(id string, occurredAt time.Time, actorID, actorKind, actorLabel, tokenID pgtype.Text, ip *netip.Addr, userAgent pgtype.Text, action string, targetType, targetID pgtype.Text, result string, detail []byte, requestID pgtype.Text) auditLogItem {
	var ipPtr *string
	if ip != nil {
		value := ip.String()
		ipPtr = &value
	}
	return auditLogItem{id, Time(occurredAt), textPtr(actorID), textPtr(actorKind), auditActorName(actorLabel), textPtr(tokenID), ipPtr, textPtr(userAgent), action, textPtr(targetType), textPtr(targetID), result, json.RawMessage(detail), textPtr(requestID)}
}

// 人間の actor_label は「表示名 <メールアドレス>」で保存される。記録は保持し、
// 閲覧用の応答からだけメールアドレスを除く。
func auditActorName(label pgtype.Text) *string {
	if !label.Valid {
		return nil
	}
	name := label.String
	if start := strings.LastIndex(name, " <"); start >= 0 && strings.HasSuffix(name, ">") && strings.Contains(name[start+2:len(name)-1], "@") {
		name = name[:start]
	}
	return &name
}

func (h *handler) listAuditLogs(w http.ResponseWriter, r *http.Request) {
	page, pageErr := ParsePage(r, SortSpec{Allowed: []string{"occurred_at"}, DefaultSort: "occurred_at", DefaultOrder: OrderDesc, DefaultPerPage: 50, MaxPerPage: 400})
	f, filterErr := parseAuditFilters(r)
	if err := mergeValidationErrors(pageErr, filterErr); err != nil {
		apierr.Write(w, r, err)
		return
	}
	if page.Order != OrderDesc {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{Field: "order", Code: "invalid", Message: "order は desc のみ指定できます"}))
		return
	}
	rows, err := h.q.ListAuditLogs(r.Context(), gen.ListAuditLogsParams{FromAt: f.fromAt, ToAt: f.toAt, ActionPattern: f.action, ActorPattern: f.actor, ResultFilter: f.result, QPattern: f.q, PageLimit: int32(page.Limit()), PageOffset: int32(page.Offset())})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("監査ログ一覧を取得できない: %w", err)))
		return
	}
	total, err := h.q.SummarizeAuditLogs(r.Context(), gen.SummarizeAuditLogsParams{FromAt: f.fromAt, ToAt: f.toAt, ActionPattern: f.action, ActorPattern: f.actor, ResultFilter: f.result, QPattern: f.q})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("監査ログ件数を取得できない: %w", err)))
		return
	}
	items := make([]auditLogItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, auditItem(row.ID, row.OccurredAt, row.ActorID, row.ActorKind, row.ActorLabel, row.TokenID, row.Ip, row.UserAgent, row.Action, row.TargetType, row.TargetID, row.Result, row.Detail, row.RequestID))
	}
	list := NewList(items, page, int(total))
	// 監査ログには updated_at がない。現在ページの内容から検証子を作る。
	body, err := json.Marshal(list)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	digest := sha256.Sum256(body)
	w.Header().Set("ETag", fmt.Sprintf(`W/"audit-%x"`, digest[:8]))
	WriteJSON(w, http.StatusOK, list)
}

func (h *handler) exportAuditLogs(w http.ResponseWriter, r *http.Request) {
	f, err := parseAuditFilters(r)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}
	rows, queryErr := h.q.ExportAuditLogs(r.Context(), gen.ExportAuditLogsParams{FromAt: f.fromAt, ToAt: f.toAt, ActionPattern: f.action, ActorPattern: f.actor, ResultFilter: f.result, QPattern: f.q})
	if queryErr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(fmt.Errorf("監査ログCSVを取得できない: %w", queryErr)))
		return
	}
	var buf bytes.Buffer
	cw := csv.NewWriter(&buf)
	_ = cw.Write([]string{"id", "occurred_at", "actor_id", "actor_kind", "actor_name", "token_id", "ip", "user_agent", "action", "target_type", "target_id", "result", "detail", "request_id"})
	for _, row := range rows {
		item := auditItem(row.ID, row.OccurredAt, row.ActorID, row.ActorKind, row.ActorLabel, row.TokenID, row.Ip, row.UserAgent, row.Action, row.TargetType, row.TargetID, row.Result, row.Detail, row.RequestID)
		_ = cw.Write([]string{item.ID, time.Time(item.OccurredAt).Format(time.RFC3339Nano), csvValue(item.ActorID), csvValue(item.ActorKind), csvValue(item.ActorName), csvValue(item.TokenID), csvValue(item.IP), csvValue(item.UserAgent), csvSafe(item.Action), csvValue(item.TargetType), csvValue(item.TargetID), csvSafe(item.Result), csvSafe(string(item.Detail)), csvValue(item.RequestID)})
	}
	cw.Flush()
	if err := cw.Error(); err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="pb-audit.csv"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}

func csvValue(s *string) string {
	if s == nil {
		return ""
	}
	return csvSafe(*s)
}

// 表計算ソフトの数式として解釈される文字列を無害化する。
func csvSafe(s string) string {
	trimmed := strings.TrimLeft(s, " \t\r\n")
	if trimmed != "" && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + s
	}
	return s
}
