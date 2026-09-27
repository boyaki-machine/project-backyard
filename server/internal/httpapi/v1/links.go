// チケット間リンク（ApiDesign.md 9.10.1）。手順18a。
//
//	GET    /api/v1/projects/{key}/tickets/{seq}/links        ticket.view
//	POST   /api/v1/projects/{key}/tickets/{seq}/links        ticket.edit
//	DELETE /api/v1/projects/{key}/tickets/{seq}/links/{id}   ticket.edit
//
// **チケットから「同じプロジェクトの別のチケット」を指す**（9.10）。外部参照
// （references.go）が PB の外を指すのとはここが違い、target_ticket_id に FK が
// あるぶん整合性を DB が保証する。
//
// **双方向を1本の GET で返す**（9.10.1）。GuiDesign.md 5.5 の「関連チケット」は
// 「ブロック元」と「ブロック先」を同じリストに並べるため、2回問い合わせると
// N+1 になる（設計方針3）。DELETE も direction を問わない——片方だけ消せないと
// 画面に「消せない行」が混ざる。
//
// **PATCH は持たない**（9.10.1）。一意制約が (source, target, link_type) である
// 以上、link_type の変更は別の行になるのと同じである。
//
// **画面が出す link_type は relates / duplicates / blocks の3つだけ**。
// FS〜SF と lag_days はガントの依存線のためのもので、
// ガントは未実装である。**API は7種すべて受け続ける**——MCP とエージェントが
// ガント用の依存を先に積むことは妨げない。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// linkTypes は link_type の CHECK（DbDesign.md 6.6）。9.10.1 の値域と一対一。
//
// **API は7種すべて受ける。** 画面が3種に絞るのは GuiDesign.md 5.5 の判断で、
// ここで狭めると MCP からガント用の依存を積めなくなる。
var linkTypes = []string{"FS", "SS", "FF", "SF", "relates", "duplicates", "blocks"}

// linkOriginHuman は API が作る唯一の origin（9.10.1）。
//
// **ticket_link.origin の値域は human / ai_suggested で、comment.origin とは違う**
// （DbDesign.md 6.6 / 6.7）。エージェントが API から作った行も human である——
// あちらの ai_suggested は「AIが提案し、人がまだ採用していない」を表す状態で
// あって、書き手の種別ではない（未実装）。
const linkOriginHuman = "human"

// errLinkHandled は RunInTx を巻き戻さずに抜けるための番人。
var errLinkHandled = errors.New("link: handled")

// linkTicketBrief は 9.10.1 の ticket。**常に「相手」が入る。**
//
// 9.5.1 の parent と同じ形にしてある（type を持つのは、画面が行の先頭に種別
// アイコンを出すため。GuiDesign.md 5.4 / 5.5）。
type linkTicketBrief struct {
	Seq    int32            `json:"seq"`
	Title  string           `json:"title"`
	Type   string           `json:"type"`
	Status ticketStatusView `json:"status"`
}

// linkView は 9.10.1 が返す1行。
type linkView struct {
	ID        string          `json:"id"`
	Direction string          `json:"direction"`
	LinkType  string          `json:"link_type"`
	Ticket    linkTicketBrief `json:"ticket"`
	LagDays   int32           `json:"lag_days"`
	Origin    string          `json:"origin"`
	CreatedAt Time            `json:"created_at"`
}

// linkListView は 9.10.1 の一覧応答。
//
// **page / per_page / total を持たない**（9.10.1）。1チケットあたり数件に収まり、
// 詳細応答（9.5.1）の links に同じ一覧が入る。
type linkListView struct {
	Items []linkView `json:"items"`
}

// createLinkRequest は POST の本文（9.10.1）。
type createLinkRequest struct {
	TargetSeq *int32  `json:"target_seq"`
	LinkType  *string `json:"link_type"`
	LagDays   *int32  `json:"lag_days"`
}

// ── GET /api/v1/projects/{key}/tickets/{seq}/links ───────────

// listTicketLinks はチケット間リンクを双方向で返す（9.10.1）。
func (h *handler) listTicketLinks(w http.ResponseWriter, r *http.Request) {
	ctx, _, ticketID, ok := h.ticketScope(w, r,
		"GET /projects/{key}/tickets/{seq}/links")
	if !ok {
		return
	}

	items, err := ticketLinksFor(ctx, h.q, ticketID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, linkListView{Items: items})
}

// ── POST /api/v1/projects/{key}/tickets/{seq}/links ──────────

// createTicketLink はリンクを1件足す。201 + Location + 作った1件。
//
// **当該チケットが常に source になる。** incoming の行を直接作る手段は置かない
// ——相手側のチケットから outgoing として作れば同じ行になり、**どちらの向きで
// 作ったかを利用者に決めさせる意味がない。**
func (h *handler) createTicketLink(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/links")
	if !ok {
		return
	}

	var req createLinkRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	targetSeq, linkType, lagDays, e := validateNewLink(req, scope.seq)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	rec := activity.FromRequest(r)
	id := ulidgen.New()
	var (
		view      linkView
		invalid   *apierr.Error
		duplicate bool
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// 相手は同一プロジェクト内に存在すること（9.10.1）。**他プロジェクトの
		// seq を投げても「無い」と同じ**——project_id で絞って引いているため、
		// 存在を探る経路にならない（Design.md 6.4.5）。
		targetID, err := q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{
			ProjectID: scope.projectID, Seq: targetSeq,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				invalid = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "target_seq", Code: "not_found",
					Message: "指定したチケットが見つかりません",
				})
				return errLinkHandled
			}
			return fmt.Errorf("相手のチケット %d を読めない: %w", targetSeq, err)
		}

		// uq_ticket_link の重複を INSERT の前に見る（9.10.1 の 409）。
		exists, err := q.TicketLinkExists(ctx, gen.TicketLinkExistsParams{
			SourceTicketID: ticketID, TargetTicketID: targetID, LinkType: linkType,
		})
		if err != nil {
			return fmt.Errorf("既存のリンクを読めない: %w", err)
		}
		if exists {
			duplicate = true
			return errLinkHandled
		}

		if err := q.CreateTicketLink(ctx, gen.CreateTicketLinkParams{
			ID:             id,
			SourceTicketID: ticketID,
			TargetTicketID: targetID,
			LinkType:       linkType,
			LagDays:        lagDays,
			Origin:         linkOriginHuman,
			CreatedBy:      pgtype.Text{String: scope.actorID, Valid: scope.actorID != ""},
		}); err != nil {
			return fmt.Errorf("リンクを作成できない: %w", err)
		}

		// **一覧から作った1件を拾う。** GetTicketLink は要約用に列を絞って
		// いるので、応答の形（status を含む）は一覧と同じ関数から採る。
		items, err := ticketLinksFor(ctx, q, ticketID)
		if err != nil {
			return err
		}
		idx := slices.IndexFunc(items, func(v linkView) bool { return v.ID == id })
		if idx < 0 {
			return fmt.Errorf("作成したリンク %q を読めない", id)
		}
		view = items[idx]

		summary := linkSummaryOf(scope.key, view)
		return recordLinkChange(ctx, q, rec, scope.projectID, ticketID, nil, summary)
	})
	switch {
	case invalid != nil:
		apierr.Write(w, r, invalid)
		return
	case duplicate:
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage("同じ関連はすでに登録されています").
			WithCause(fmt.Errorf("リンク %d -> %d (%s) は既にある", scope.seq, targetSeq, linkType)))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.Header().Set("Location", fmt.Sprintf(
		"/api/v1/projects/%s/tickets/%d/links/%s", scope.key, scope.seq, id))
	WriteJSON(w, http.StatusCreated, view)
}

// ── DELETE /api/v1/projects/{key}/tickets/{seq}/links/{id} ───

// deleteTicketLink はリンクを1件消す。204（9.10.1）。
//
// **direction を問わない。** incoming の行——相手のチケットが source である
// 行——もここから消せる（9.10.1）。
func (h *handler) deleteTicketLink(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"DELETE /projects/{key}/tickets/{seq}/links/{id}")
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")

	rec := activity.FromRequest(r)
	var notFound bool

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		row, err := q.GetTicketLink(ctx, gen.GetTicketLinkParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				notFound = true
				return errLinkHandled
			}
			return fmt.Errorf("リンク %q を読めない: %w", id, err)
		}

		rows, err := q.DeleteTicketLink(ctx, gen.DeleteTicketLinkParams{
			TicketID: ticketID, ID: id,
		})
		if err != nil {
			return fmt.Errorf("リンク %q を削除できない: %w", id, err)
		}
		if rows == 0 {
			notFound = true
			return errLinkHandled
		}

		// 要約は「<link_type> <相手の完全形ID>」（9.10.1）。GetTicketLink は
		// 相手の seq を返すので、一覧を引き直さずに組み立てられる。
		summary := row.LinkType + " " + fullTicketID(scope.key, row.TicketSeq)
		return recordLinkChange(ctx, q, rec, scope.projectID, ticketID, &summary, "")
	})
	switch {
	case notFound:
		apierr.Write(w, r, linkNotFound(id))
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ── 組み立てと検証 ───────────────────────────────────────────

// ticketLinksFor は1チケットぶんのリンクを組み立てる。
//
// **詳細応答（9.5.1）と一覧（9.10.1）が同じ関数を通る。** 並び順の規則
// （direction → link_type → 相手の seq）はクエリ側にあり、2か所で書き分けない。
func ticketLinksFor(
	ctx context.Context, q gen.Querier, ticketID string,
) ([]linkView, error) {
	rows, err := q.ListTicketLinks(ctx, ticketID)
	if err != nil {
		return nil, fmt.Errorf("関連チケットを読めない: %w", err)
	}
	items := make([]linkView, 0, len(rows))
	for _, row := range rows {
		items = append(items, linkView{
			ID:        row.ID,
			Direction: row.Direction,
			LinkType:  row.LinkType,
			Ticket: linkTicketBrief{
				Seq:   row.TicketSeq,
				Title: row.TicketTitle,
				Type:  row.TicketType,
				Status: statusView(
					row.TicketStatusKey, row.TicketStatusName, row.TicketStatusCategory),
			},
			LagDays:   row.LagDays,
			Origin:    row.Origin,
			CreatedAt: Time(row.CreatedAt),
		})
	}
	return items, nil
}

// linkNotFound は「そのチケットに紐づかないリンク」への応答。
//
// **他チケットのリンクも 404 に寄せる。** クエリの WHERE が source / target の
// どちらかに ticket_id を要求するので、存在しない ID と区別せず1つの結果になる
// （Design.md 6.4.5）。
func linkNotFound(id string) *apierr.Error {
	return apierr.New(apierr.NotFound).
		WithMessage("関連チケットが見つかりません").
		WithCause(fmt.Errorf("リンク %q が見つからない", id))
}

// validateNewLink は POST の本文を検証する（9.10.1）。
//
// **自分自身へのリンクは self_link で弾く**（9.14）。DB の ck_ticket_link_diff が
// 最後の砦だが、そこで落ちると 2.5 の形式ではなく 500 になる。
func validateNewLink(req createLinkRequest, selfSeq int32) (
	targetSeq int32, linkType string, lagDays int32, apiErr *apierr.Error,
) {
	var details []apierr.Detail

	switch {
	case req.TargetSeq == nil:
		details = append(details, apierr.Detail{
			Field: "target_seq", Code: "required", Message: "関連するチケットを指定してください",
		})
	case *req.TargetSeq < 1:
		details = append(details, apierr.Detail{
			Field: "target_seq", Code: "invalid", Message: "チケット番号は1以上で指定してください",
		})
	case *req.TargetSeq == selfSeq:
		details = append(details, apierr.Detail{
			Field: "target_seq", Code: "self_link",
			Message: "同じチケットを関連付けることはできません",
		})
	default:
		targetSeq = *req.TargetSeq
	}

	switch {
	case req.LinkType == nil:
		details = append(details, apierr.Detail{
			Field: "link_type", Code: "required", Message: "関連の種類を指定してください",
		})
	case !slices.Contains(linkTypes, *req.LinkType):
		details = append(details, apierr.Detail{
			Field: "link_type", Code: "invalid", Message: "関連の種類が正しくありません",
		})
	default:
		linkType = *req.LinkType
	}

	if req.LagDays != nil {
		lagDays = *req.LagDays
	}

	if len(details) > 0 {
		return 0, "", 0, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return targetSeq, linkType, lagDays, nil
}

// ── activity（9.10.1）──────────────────────────────────────

// recordLinkChange は追加・削除を activity へ1行書く（9.1.1 / 9.10.1）。
//
// entity は操作したチケット、action は常に update。**相手のチケットの履歴には
// 書かない**——1回の操作で2行増えると、手順19 の「最近の動き」で同じ出来事が
// 二重に見える。
func recordLinkChange(
	ctx context.Context, q gen.Querier, rec *activity.Recorder,
	projectID, ticketID string, old *string, next string,
) error {
	field := "link"
	entry := activity.Entry{
		ProjectID:  projectID,
		EntityType: activity.EntityTicket,
		EntityID:   ticketID,
		Action:     activity.Update,
		Field:      &field,
		OldValue:   old,
	}
	if next != "" {
		entry.NewValue = &next
	}
	return rec.Record(ctx, q, entry)
}

// linkSummaryOf は履歴に載せる要約（9.10.1）。
//
//	blocks my-app-12
//
// **direction は含めない。** 読み手はそのチケットの履歴を見ており、
// 相手が誰かだけが要る。
func linkSummaryOf(key string, v linkView) string {
	return v.LinkType + " " + fullTicketID(key, v.Ticket.Seq)
}
