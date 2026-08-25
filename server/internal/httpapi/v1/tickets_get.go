// GET /api/v1/projects/{key}/tickets/{seq}（ApiDesign.md 9.5.1）。
//
// チケット詳細画面（GuiDesign.md 5.5）のデータ源。**9.2 の items[] に6項目
// （body_md / parent / children / dod / links / comment_count）を加えたもの**を返す。
//
// **組み立てそのものは buildTicketDetail が持つ**（ticket_view.go）。手順16b が
// POST /tickets の応答のために先に作ってあり、9.3 が「応答は 9.5 の GET と
// 同形式」と定める以上、両者は同じ関数を通る必要がある。片方だけ項目が増えると
// JSON の形が割れる。
//
// **ETag を付けない。** 2.7 の ETag は一覧（9.2.5）のためのもので、1件の詳細は
// 軽く、競合検出は 9.5.2 の楽観ロック（version + If-Match）が担う。同じ資源に
// 2つの競合検出手段を置くと、どちらが正本か読めなくなる。
package v1

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// getTicket はチケット1件の詳細を返す（9.5.1）。
func (h *handler) getTicket(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q,
		"GET /projects/{key}/tickets/{seq}")
	if !ok {
		return
	}

	seq, apiErr := ticketSeqParam(r)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	view, err := buildTicketDetail(r.Context(), h.q, projectID, seq)
	if err != nil {
		writeTicketReadError(w, r, seq, err)
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// writeTicketReadError は詳細の読み取りで出た失敗を 404 と 500 に振り分ける。
//
// **行が無いのは 404 であって 500 ではない。** buildTicketDetail は
// GetTicketBySeq が 0 行のときに pgx.ErrNoRows をそのまま返すので、ここで
// 「このプロジェクトにその番号のチケットが無い」に翻訳する。到達可否（メンバーか）
// は RequireProjectPermission が済ませている（Design.md 6.4.5）ので、ここへ来る
// 呼び出し元は必ずプロジェクトを見られる。
func writeTicketReadError(w http.ResponseWriter, r *http.Request, seq int32, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, r, ticketNotFound(seq))
		return
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).
		WithCause(fmt.Errorf("チケット %d を読めない: %w", seq, err)))
}
