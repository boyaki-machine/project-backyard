// チケットの子資源（コメント・DoD・リンク）が共通して行う入口の処理。手順18a。
//
// **3つの層を順に通す**（手順16b の引き継ぎ、2026-08-23）。
//
//	projectScopeContext   プリンシパルと {key} を解き、project_id まで解決する
//	ticketSeqParam        {seq} を解く（9.1）
//	FindTicketIDBySeq     そのプロジェクトのチケットか確かめ、内部 ID を得る
//
// 到達可否（メンバーか）は RequireProjectPermission が済ませている
// （Design.md 6.4.5）。ここから先のクエリはすべて ticket_id で閉じているので、
// **他プロジェクト・他チケットの行に触れる経路は残らない。**
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ticketScopeInfo は子資源のハンドラが後段で使う値。
//
// **ticketID を返り値で分けてある**のは、クエリの引数がすべて ticket_id で
// あり、構造体から取り出す手数を毎回踏ませないためである。
type ticketScopeInfo struct {
	key       string
	projectID string
	seq       int32
	actorID   string
	actorKind string
}

// origin は 9.8 / 9.10.1 が「呼び出し元のアクター種別から決まる」とする値。
//
// **リクエストで指定できない。** 人が 'agent' を名乗れると、GuiDesign.md 5.5 の
// 「エージェントのコメントを角丸四角で区別する」が意味を失う。
func (s ticketScopeInfo) origin() string {
	if s.actorKind == actorKindAgent {
		return originAgent
	}
	return originHuman
}

// origin の値域（DbDesign.md 6.7 の comment.origin）。
//
// **ticket_link.origin は human / ai_suggested で値域が違う**（DbDesign.md 6.6）。
// Phase 1 が作るのは human だけなので、リンク側は originHuman だけを使う。
const (
	originHuman = "human"
	originAgent = "agent"
)

// ticketScope は {key} と {seq} を解いて、チケットの内部 ID までたどり着く。
//
// **projects_get.go の3つを通す**（プリンシパル → {key} → project_id）。
// そのうえで {seq} をチケットの id まで解き、**他プロジェクトの番号を指しても
// 404 に寄せる**（Design.md 6.4.5）。
//
// route は 403 / 404 の監査に載せる識別子で、projectScopeContext がそのまま使う。
//
// **外部参照の4本もこれを通す**（pb-26）。references.go に同型の referenceScope が
// 並んでいたが、**返す構造体の名前だけが違う35行の写し**だった。片方に直しが入ると
// もう片方が置き去りになるので、上位集合であるこちらへ寄せた。
func (h *handler) ticketScope(
	w http.ResponseWriter, r *http.Request, route string,
) (context.Context, ticketScopeInfo, string, bool) {
	p, key, projectID, ok := projectScopeContext(w, r, h.q, route)
	if !ok {
		return nil, ticketScopeInfo{}, "", false
	}
	seq, apiErr := ticketSeqParam(r)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return nil, ticketScopeInfo{}, "", false
	}

	ctx := r.Context()
	ticketID, err := h.q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{
		ProjectID: projectID, Seq: seq,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			apierr.Write(w, r, ticketNotFound(seq))
		} else {
			apierr.Write(w, r, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("チケット %d を読めない: %w", seq, err)))
		}
		return nil, ticketScopeInfo{}, "", false
	}

	info := ticketScopeInfo{key: key, projectID: projectID, seq: seq}
	if p != nil {
		info.actorID = p.ActorID
		info.actorKind = p.ActorKind
	}
	return ctx, info, ticketID, true
}

// fullTicketID は 9.1 の「<key>-<seq>」。activity の要約と画面の表記に使う。
func fullTicketID(key string, seq int32) string {
	return fmt.Sprintf("%s-%d", key, seq)
}
