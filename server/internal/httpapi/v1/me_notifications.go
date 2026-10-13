// 自分宛ての通知（ApiDesign.md 4.8、DbDesign.md 6.33）。
//
//	GET    /api/v1/me/notifications                4.8.1
//	GET    /api/v1/me/notifications/unread-count   4.8.2
//	PATCH  /api/v1/me/notifications/{id}           4.8.3
//	POST   /api/v1/me/notifications/read-all       4.8.4
//	DELETE /api/v1/me/notifications/{id}           4.8.5
//	DELETE /api/v1/admin/notifications/{id}        4.8.6
//
// **通知を作る口は無い。** 通知は使い手の処理が notify.Create で作る。
//
// **本人の口は、他人の通知を 404 にする。** 触れる範囲はクエリが recipient_id
// （削除は source_id も）で閉じている（notification.sql）。
package v1

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 一覧の絞り込み（4.8.1）。
const (
	notificationFilterAll    = "all"
	notificationFilterUnread = "unread"
)

// ── 応答の形（4.8.1）──────────────────────────────────────

type notificationProjectView struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

type notificationSourceView struct {
	ID   *string `json:"id"`
	Name string  `json:"name"`
}

type notificationView struct {
	ID        string                   `json:"id"`
	Kind      string                   `json:"kind"`
	Title     string                   `json:"title"`
	Body      string                   `json:"body"`
	Link      string                   `json:"link"`
	LinkLabel string                   `json:"link_label"`
	Project   *notificationProjectView `json:"project"`
	Source    notificationSourceView   `json:"source"`
	CreatedAt Time                     `json:"created_at"`
	ReadAt    *Time                    `json:"read_at"`
}

// toNotificationView は一覧と1件の行を同じ形にする。sqlc は2つのクエリに別の型を
// 作るので、1件の行（GetMyNotificationRow）を一覧の行の型へ写してから渡す。
func toNotificationView(row gen.ListMyNotificationsRow) notificationView {
	v := notificationView{
		ID:        row.ID,
		Kind:      row.Kind,
		Title:     row.Title,
		Body:      row.Body,
		Link:      row.Link,
		LinkLabel: row.LinkLabel,
		Source:    notificationSourceView{Name: row.SourceName},
		CreatedAt: Time(row.CreatedAt),
		ReadAt:    apiTime(row.ReadAt),
	}
	if row.ProjectKey.Valid {
		v.Project = &notificationProjectView{Key: row.ProjectKey.String, Name: row.ProjectName.String}
	}
	if row.SourceID.Valid {
		id := row.SourceID.String
		v.Source.ID = &id
	}
	return v
}

// ── GET /api/v1/me/notifications（4.8.1）────────────────────

func (h *handler) listMyNotifications(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "GET /me/notifications")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	q := r.URL.Query()
	var details []apierr.Detail
	filter := notificationFilterAll
	if v := q.Get("filter"); v != "" {
		if v != notificationFilterAll && v != notificationFilterUnread {
			details = append(details, apierr.Detail{
				Field: "filter", Code: "invalid", Message: "filter は unread か all で指定してください",
			})
		}
		filter = v
	}
	// **並びは新しい順で固定**（4.8.1）。受けない値を黙って捨てない（2.6）。
	for _, k := range []string{"sort", "order"} {
		if q.Has(k) {
			details = append(details, apierr.Detail{
				Field: k, Code: "invalid", Message: "通知の並びは新しい順で固定です。" + k + " は指定できません",
			})
		}
	}
	page, pe := ParsePage(r, SortSpec{Allowed: []string{"created_at"}, DefaultSort: "created_at"})
	if pe != nil {
		apierr.Write(w, r, pe)
		return
	}
	if len(details) > 0 {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(details...))
		return
	}

	ctx := r.Context()
	unreadOnly := filter == notificationFilterUnread
	rows, err := h.q.ListMyNotifications(ctx, gen.ListMyNotificationsParams{
		RecipientID: p.ActorID,
		UnreadOnly:  unreadOnly,
		PageLimit:   int32(page.Limit()),
		PageOffset:  int32(page.Offset()),
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("通知を読めない: %w", err)))
		return
	}
	total, err := h.q.CountMyNotifications(ctx, gen.CountMyNotificationsParams{
		RecipientID: p.ActorID, UnreadOnly: unreadOnly,
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("通知の件数を読めない: %w", err)))
		return
	}

	items := make([]notificationView, 0, len(rows))
	for _, row := range rows {
		items = append(items, toNotificationView(row))
	}
	WriteJSON(w, http.StatusOK, NewList(items, page, int(total)))
}

// ── GET /api/v1/me/notifications/unread-count（4.8.2）───────

type unreadCountView struct {
	Count int64 `json:"count"`
}

// unreadMyNotificationCount は未読の数を返す。**画面がどの画面にいても30秒ごとに引く。**
// 上限で丸めない（丸めは画面が行う）。
func (h *handler) unreadMyNotificationCount(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "GET /me/notifications/unread-count")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	n, err := h.q.CountMyUnreadNotifications(r.Context(), p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("未読の通知を数えられない: %w", err)))
		return
	}
	WriteJSON(w, http.StatusOK, unreadCountView{Count: n})
}

// ── PATCH /api/v1/me/notifications/{id}（4.8.3）─────────────

type patchNotificationRequest struct {
	Read *bool `json:"read"`
}

// patchMyNotification は既読にする・未読に戻す。**If-Match を要求しない**（4.8.3）。
func (h *handler) patchMyNotification(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "PATCH /me/notifications/{id}")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	var req patchNotificationRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if req.Read == nil {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "read", Code: "required", Message: "read を true か false で指定してください",
		}))
		return
	}

	ctx := r.Context()
	id := chi.URLParam(r, "id")
	n, err := h.q.SetMyNotificationRead(ctx, gen.SetMyNotificationReadParams{
		Read: *req.Read, ID: id, RecipientID: p.ActorID,
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("通知の既読を変えられない: %w", err)))
		return
	}
	if n == 0 {
		apierr.WriteCode(w, r, apierr.NotFound)
		return
	}

	row, err := h.q.GetMyNotification(ctx, gen.GetMyNotificationParams{ID: id, RecipientID: p.ActorID})
	if errors.Is(err, pgx.ErrNoRows) {
		// 更新と読み直しの間に、取り下げか定期処理で消えた。
		apierr.WriteCode(w, r, apierr.NotFound)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("通知を読めない: %w", err)))
		return
	}
	WriteJSON(w, http.StatusOK, toNotificationView(gen.ListMyNotificationsRow(row)))
}

// ── POST /api/v1/me/notifications/read-all（4.8.4）──────────

type readAllView struct {
	Updated int64 `json:"updated"`
}

func (h *handler) readAllMyNotifications(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "POST /me/notifications/read-all")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	n, err := h.q.MarkAllMyNotificationsRead(r.Context(), p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("通知をすべて既読にできない: %w", err)))
		return
	}
	WriteJSON(w, http.StatusOK, readAllView{Updated: n})
}

// ── DELETE /api/v1/me/notifications/{id}（4.8.5）────────────

// deleteMyNotification は受けた人か出した人が消す。
func (h *handler) deleteMyNotification(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "DELETE /me/notifications/{id}")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	id := chi.URLParam(r, "id")
	row, err := h.q.DeleteMyNotification(r.Context(), gen.DeleteMyNotificationParams{ID: id, ActorID: p.ActorID})
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.WriteCode(w, r, apierr.NotFound)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("通知を消せない: %w", err)))
		return
	}
	h.auditNotificationDelete(r, p, id, row.RecipientID, row.Kind)
	w.WriteHeader(http.StatusNoContent)
}

// ── DELETE /api/v1/admin/notifications/{id}（4.8.6）─────────

// deleteNotificationByAdmin は誰宛ての通知でも消す。**権限（system.settings）は
// ルートに宣言してある**（routes.go）。ここでは判定しない。
func (h *handler) deleteNotificationByAdmin(w http.ResponseWriter, r *http.Request) {
	p, e := requirePrincipal(r, "DELETE /admin/notifications/{id}")
	if e != nil {
		apierr.Write(w, r, e)
		return
	}
	id := chi.URLParam(r, "id")
	row, err := h.q.DeleteNotificationByAdmin(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.WriteCode(w, r, apierr.NotFound)
		return
	}
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("通知を消せない: %w", err)))
		return
	}
	h.auditNotificationDelete(r, p, id, row.RecipientID, row.Kind)
	w.WriteHeader(http.StatusNoContent)
}

// auditNotificationDelete は**受けた人以外が消したときだけ**記録する（4.8.7）。
// 題名と本文は detail に入れない。
func (h *handler) auditNotificationDelete(r *http.Request, p *auth.Principal, id, recipientID, kind string) {
	if recipientID == p.ActorID {
		return
	}
	audit.FromRequest(r).WithActor(p.ActorID, p.ActorKind, p.AuditLabel()).
		RecordOrLog(r.Context(), h.q, audit.Entry{
			Action:     audit.NotificationDelete,
			Result:     audit.Success,
			TargetType: "notification",
			TargetID:   id,
			Detail:     map[string]any{"kind": kind, "recipient_id": recipientID},
		})
}
