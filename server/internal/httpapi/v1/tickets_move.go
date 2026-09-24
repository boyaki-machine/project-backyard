// POST /api/v1/projects/{key}/tickets/{seq}/move（ApiDesign.md 9.4）。
//
// バックログのドラッグ&ドロップによる並べ替え（GuiDesign.md 5.4）。
//
// **PATCH で sort_key を直接書かせない**（9.4）。LexoRank の桁生成規則を
// クライアントに持たせると、Web・MCP・将来の CLI がそれぞれ同じ規則を実装する
// ことになり、1つでもずれると順序が壊れる。順序キーの生成はサーバに1つだけ置く
// （internal/lexorank）。
//
// **If-Match を要求しない**（9.4）。2.8 の archive / unarchive と同じく、競合しても
// 失われる編集内容が無い（sort_key はフォームで編集する項目ではない）。ただし
// version は他の更新と同じく +1 する。
//
// **並び順はプロジェクト内で1本である**（9.4）。グループ化（親・タグ・スプリント）は
// 表示上の区切りにすぎず、グループを切り替えても sort_key は変わらない。
//
// **staged と parent_seq を受け取るのは、位置と同時に決まるものだからである**
// （9.4.1 / 9.4.2）。ドラッグの1操作で段や親と位置が一緒に決まるので、2本の
// エンドポイントに分けると中途半端な状態が残る。**位置を伴わない親の変更
// （行の中央へ落として子にする）は PATCH のままである**（9.5.2）。
//
// **activity に記録しない**。sort_key だけの更新で
// あり、記録するとバックログを一度並べ替えただけでチケット詳細の変更履歴
// （GuiDesign.md 5.5）が埋まる。読み手はプロジェクトのメンバーであって、
// 「誰がどこへドラッグしたか」は業務履歴として読む価値が薄い。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/lexorank"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// position に指定できる値（ApiDesign.md 9.4）。
const (
	positionFirst = "first"
	positionLast  = "last"
)

// moveTicketRequest は 9.4 のリクエスト。
//
// Staged は段を変える（9.4.1）。**省略すると段は変わらない**ので、ポインタで
// 「送られなかった」と「false が送られた」を区別する。
type moveTicketRequest struct {
	AfterSeq  *int32 `json:"after_seq"`
	BeforeSeq *int32 `json:"before_seq"`
	Position  string `json:"position"`
	Staged    *bool  `json:"staged"`

	// ParentSeq は「ルートにする」（9.4.2）。**null だけを受け取る。**
	//
	// **省略と null を区別する必要がある**ので、生の JSON で受けてから読む
	// （9.5.2 の PATCH が optional[T] でやっているのと同じ区別である）。
	// 数値は 422 に倒す——「別の親の下の、この位置へ」を表すドロップが
	// GuiDesign.md 5.4 に無く、受け取っても使い手がいない。
	ParentSeq json.RawMessage `json:"parent_seq"`
}

// unparent は「ルートにする」が指定されたか（9.4.2）。
//
// **省略（nil）と JSON の null を区別する。** encoding/json は前者で
// RawMessage を nil のまま残し、後者では []byte("null") を入れる。
func (r moveTicketRequest) unparent() bool {
	return len(r.ParentSeq) > 0 && string(r.ParentSeq) == "null"
}

// moveTicketResponse は 9.4 の応答。
//
// rebalanced は、隣接する2つのキーの間に新しいキーを作れず、プロジェクト全体の
// sort_key を振り直したことを示す。**true のとき、クライアントは一覧を取り直す**
// ——手元の sort_key がすべて古くなっている。
type moveTicketResponse struct {
	Seq        int32  `json:"seq"`
	SortKey    string `json:"sort_key"`
	StagedAt   *Time  `json:"staged_at"`
	Version    int32  `json:"version"`
	Rebalanced bool   `json:"rebalanced"`
}

// moveTicket は並べ替えを1件処理する。
func (h *handler) moveTicket(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q,
		"POST /projects/{key}/tickets/{seq}/move")
	if !ok {
		return
	}

	seq, apiErr := ticketSeqParam(r)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	var req moveTicketRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateMoveTarget(req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	var (
		resp    moveTicketResponse
		moveErr *apierr.Error
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		row, err := q.GetTicketSortRow(ctx, gen.GetTicketSortRowParams{
			ProjectID: projectID, Seq: seq,
		})
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				moveErr = ticketNotFound(seq)
				return errTicketReference
			}
			return fmt.Errorf("チケット %d を読めない: %w", seq, err)
		}

		// **移動先の段を先に決める**（9.4.1）。staged を省略した場合は
		// 現在の段のままで、position もその段の中で解釈される。
		targetStaged := row.StagedAt.Valid
		if req.Staged != nil {
			targetStaged = *req.Staged
		}

		// **段に置けるのは表示上のトップレベルだけ**（9.4.1、GuiDesign.md 5.4）
		// ——親を持たないもの、または親がエピックのもの。配下は親と一緒に運ばれる
		// ので、子を個別に上げる操作は意味を持たない。**バックログへ戻すのは
		// 常に許す**（段から降ろすだけなので、置ける条件を問う理由がない）。
		//
		// **判定は parent_seq を適用した後の状態で行う**（9.4.2）。親を外せば
		// トップレベルになるので、**外す前の親を見て弾いてはならない**——
		// 同じトランザクションで両方が確定する以上、途中の状態は存在しない。
		parentType := row.ParentType
		if req.unparent() {
			parentType = pgtype.Text{}
		}
		if targetStaged && !row.StagedAt.Valid && !stageable(row.Type, parentType) {
			message := "配下のチケットはオンステージへ上げられません。親のチケットを上げてください"
			if row.Type == ticketTypeEpic {
				message = "エピックはオンステージへ上げられません。配下のチケットを上げてください"
			}
			moveErr = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "staged", Code: "not_stageable", Message: message,
			})
			return errTicketReference
		}

		prev, next, usable, e := resolveMoveNeighbors(ctx, q, projectID, seq, req, targetStaged)
		if e != nil {
			moveErr = e
			return errTicketReference
		}

		key, ok := lexorank.Between(prev, next)
		if !usable || !ok {
			// **基準のキーが無い／間が詰まった／既存のキーが読めない。** いずれも
			// プロジェクト全体を振り直せば回復する（9.4 の rebalanced）。振り直した
			// 後は隣も変わっているので、境界を読み直してから作り直す。
			if err := rebalanceTicketSortKeysErr(ctx, q, projectID); err != nil {
				return err
			}
			resp.Rebalanced = true

			prev, next, _, e = resolveMoveNeighbors(ctx, q, projectID, seq, req, targetStaged)
			if e != nil {
				moveErr = e
				return errTicketReference
			}
			key, ok = lexorank.Between(prev, next)
			if !ok {
				// 振り直した直後でも作れないのは、指定そのものが矛盾している
				// （after のほうが before より後ろにある）場合だけである。
				moveErr = apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
					Field: "after_seq", Code: "invalid",
					Message: "after_seq と before_seq の前後が逆になっています",
				})
				return errTicketReference
			}
		}

		// staged_at は「いつ上げたか」を持つ（DbDesign.md 6.6）。**既にオンステージの
		// ものを並べ替えただけで時刻を打ち直さない**——上げた順で読みたいときに、
		// 並べ替えのたびに新しくなると意味を失う。
		stagedAt := row.StagedAt
		changeStage := req.Staged != nil && targetStaged != row.StagedAt.Valid
		if changeStage {
			if targetStaged {
				stagedAt = nowTimestamptz()
			} else {
				stagedAt = pgtype.Timestamptz{}
			}
		}

		moved, err := q.MoveTicket(ctx, gen.MoveTicketParams{
			ProjectID:   projectID,
			ID:          row.ID,
			SortKey:     text(key),
			ChangeStage: changeStage,
			StagedAt:    stagedAt,
			// **同じ文で親を外す**（9.4.2）。位置と一緒に決まるものなので、
			// 別の文へ分けると片方だけ成功した状態が残りうる。
			Unparent: req.unparent(),
		})
		if err != nil {
			return fmt.Errorf("チケット %d の並び順を更新できない: %w", seq, err)
		}
		// **オンステージに置いたら、配下ごと進行中のスプリントへ入れる**（9.12.3）。
		// 段の中の並べ替えでも呼ぶが、既に所属していれば何も起きない
		if targetStaged {
			if err := joinActiveSprint(ctx, q, projectID, row.ID); err != nil {
				return err
			}
		}
		resp.Seq = moved.Seq
		resp.SortKey = moved.SortKey.String
		resp.StagedAt = apiTimestamptz(moved.StagedAt)
		resp.Version = moved.Version
		return nil
	})

	switch {
	case moveErr != nil:
		apierr.Write(w, r, moveErr)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケット %d を動かせない: %w", seq, err)))
		return
	}

	WriteJSON(w, http.StatusOK, resp)
}

// stageable は「段に置けるか」を返す（9.4.1、GuiDesign.md 5.4）。
//
// **表示上のトップレベルだけが置ける**——親を持たないもの（parentType が無効）、
// または**親がエピックのもの**。エピックはバックログに行として出さずフィルタに
// なるので（DbDesign.md 6.10）、その直下のチケットは画面上のトップレベルである。
// 親を持たないものだけに絞ると、実運用では大半の仕事がエピック配下に入るため
// オンステージがほとんど空になる。
//
// **エピック自身は置けない。** 親を持たないので上の条件だけでは通ってしまうが、
// エピックはどちらの段にも行として出ないため、上げても見えない状態になる
// （GuiDesign.md 5.4）。dev seed の検証（cmd/pb/dev_seed.go）と同じ規則である。
func stageable(ownType string, parentType pgtype.Text) bool {
	if ownType == ticketTypeEpic {
		return false
	}
	return !parentType.Valid || parentType.String == ticketTypeEpic
}

// nowTimestamptz は staged_at に入れる「いつ上げたか」。
func nowTimestamptz() pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.Now(), Valid: true}
}

// validateMoveTarget は 9.4 の「position と after_seq / before_seq の同時指定は
// 422。いずれも無い場合も 422」を見る。
func validateMoveTarget(req moveTicketRequest) *apierr.Error {
	hasNeighbor := req.AfterSeq != nil || req.BeforeSeq != nil
	hasPosition := req.Position != ""

	switch {
	case hasPosition && hasNeighbor:
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "position", Code: "invalid",
			Message: "position と after_seq / before_seq は同時に指定できません",
		})
	case !hasPosition && !hasNeighbor:
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "position", Code: "required",
			Message: "position か after_seq / before_seq のいずれかを指定してください",
		})
	case hasPosition && req.Position != positionFirst && req.Position != positionLast:
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "position", Code: "invalid",
			Message: "position は first または last で指定してください",
		})
	// **parent_seq は null しか受け取らない**（9.4.2）。数値を黙って捨てると、
	// 送った側は「親を変えたつもり」のまま位置だけ動いた結果を受け取る。
	case len(req.ParentSeq) > 0 && !req.unparent():
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "parent_seq", Code: "unsupported",
			Message: "parent_seq は null（ルートにする）だけ指定できます。親を変えるにはチケットの更新を使ってください",
		})
	}
	return nil
}

// resolveMoveNeighbors は「どのキーとどのキーの間へ入れるか」を決める（9.4 の表）。
//
// 空文字は境界（先頭より前／末尾より後）を表し、lexorank.Between の約束と同じ。
//
// 3つめの戻り値 usable は「基準に指定されたチケットが使えるキーを持っていたか」。
// **false のとき、呼び出し側は振り直してから読み直す。** sort_key が NULL の行を
// 基準にすると空文字（＝境界）と区別が付かず、「44 の直後へ」と言われたのに
// 先頭へ置いてしまうためである。position 指定にはこの問題が無い（境界そのものを
// 指しているので、空文字が正しい意味を持つ）。
//
// **targetStaged は position を解釈する段**（9.4.1）。"first" は「移動先の段の
// 先頭」であって「プロジェクト全体の先頭」ではない。空の段へ最初の1件を落とす
// ときに基準となる行が無いため、段で絞った min / max が要る。**after_seq /
// before_seq は段を問わない**——sort_key はプロジェクト内で1本であり、どの行の
// 隣を指定しても位置は一意に定まる。
func resolveMoveNeighbors(
	ctx context.Context, q gen.Querier, projectID string, seq int32,
	req moveTicketRequest, targetStaged bool,
) (string, string, bool, *apierr.Error) {
	switch {
	case req.Position == positionFirst:
		next, err := q.MinTicketSortKeyInStage(ctx, gen.MinTicketSortKeyInStageParams{
			ProjectID: projectID, Staged: targetStaged,
		})
		if err != nil {
			return "", "", false, internalMoveError("先頭の並び順を読めない", err)
		}
		return "", next, true, nil

	case req.Position == positionLast:
		prev, err := q.MaxTicketSortKeyInStage(ctx, gen.MaxTicketSortKeyInStageParams{
			ProjectID: projectID, Staged: targetStaged,
		})
		if err != nil {
			return "", "", false, internalMoveError("末尾の並び順を読めない", err)
		}
		return prev, "", true, nil
	}

	var prev, next string
	usable := true
	if req.AfterSeq != nil {
		key, e := neighborSortKey(ctx, q, projectID, seq, *req.AfterSeq, "after_seq")
		if e != nil {
			return "", "", false, e
		}
		prev = key
		usable = usable && key != ""
	}
	if req.BeforeSeq != nil {
		key, e := neighborSortKey(ctx, q, projectID, seq, *req.BeforeSeq, "before_seq")
		if e != nil {
			return "", "", false, e
		}
		next = key
		usable = usable && key != ""
	}

	// 片方しか指定が無ければ、もう一方は現在の並びから引く。
	var err error
	if req.AfterSeq != nil && req.BeforeSeq == nil {
		if next, err = q.TicketSortKeyAfter(ctx, gen.TicketSortKeyAfterParams{
			ProjectID: projectID, After: prev,
		}); err != nil {
			return "", "", false, internalMoveError("後ろ側の並び順を読めない", err)
		}
	}
	if req.BeforeSeq != nil && req.AfterSeq == nil {
		if prev, err = q.TicketSortKeyBefore(ctx, gen.TicketSortKeyBeforeParams{
			ProjectID: projectID, Before: next,
		}); err != nil {
			return "", "", false, internalMoveError("手前側の並び順を読めない", err)
		}
	}
	return prev, next, usable, nil
}

// neighborSortKey は after_seq / before_seq が指すチケットのキーを引く。
func neighborSortKey(
	ctx context.Context, q gen.Querier, projectID string, movingSeq, targetSeq int32, field string,
) (string, *apierr.Error) {
	if targetSeq == movingSeq {
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: field, Code: "invalid",
			Message: "動かすチケット自身を基準にはできません",
		})
	}
	row, err := q.GetTicketSortRow(ctx, gen.GetTicketSortRowParams{
		ProjectID: projectID, Seq: targetSeq,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: field, Code: "not_found",
				Message: fmt.Sprintf("基準に指定したチケット %d が見つかりません", targetSeq),
			})
		}
		return "", internalMoveError("基準のチケットを読めない", err)
	}
	// sort_key が NULL の行を基準にされたら空を返す。呼び出し側が usable=false と
	// して扱い、振り直してから読み直す。
	return row.SortKey.String, nil
}

// rebalanceTicketSortKeysErr はプロジェクト全体の sort_key を振り直す（9.4）。
func rebalanceTicketSortKeysErr(ctx context.Context, q gen.Querier, projectID string) error {
	_, err := rebalanceTicketSortKeys(ctx, q, projectID)
	return err
}

// rebalanceTicketSortKeys は現在の並びを保ったまま、間隔をそろえて振り直す。
// 振り直した件数を返す。
//
// **version は上げない**（ticket.sql の SetTicketSortKey）。全行に触るので、
// 上げると開いている詳細画面がすべて 409 になる。
func rebalanceTicketSortKeys(
	ctx context.Context, q gen.Querier, projectID string,
) (int, error) {
	ids, err := q.ListTicketIDsInSortOrder(ctx, projectID)
	if err != nil {
		return 0, fmt.Errorf("振り直しの対象を読めない: %w", err)
	}
	keys := lexorank.Rebalance(len(ids))
	for i, id := range ids {
		if err := q.SetTicketSortKey(ctx, gen.SetTicketSortKeyParams{
			ID: id, SortKey: text(keys[i]),
		}); err != nil {
			return 0, fmt.Errorf("チケット %q の並び順を振り直せない: %w", id, err)
		}
	}
	return len(ids), nil
}

// ticketSeqParam は URL の {seq} を読む（9.1）。
func ticketSeqParam(r *http.Request) (int32, *apierr.Error) {
	raw := chi.URLParam(r, "seq")
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, apierr.New(apierr.NotFound).
			WithMessage("チケットが見つかりません").
			WithCause(fmt.Errorf("チケット番号として読めない: %q", raw))
	}
	return int32(n), nil
}

// ticketNotFound は「そのプロジェクトに無いチケット」への応答。
//
// **他プロジェクトのチケットも 404 に寄せる。** クエリの WHERE が project_id を
// 含むので、存在しない番号と区別せず1つの結果になる（Design.md 6.4.5）。
func ticketNotFound(seq int32) *apierr.Error {
	return apierr.New(apierr.NotFound).
		WithMessage("チケットが見つかりません").
		WithCause(fmt.Errorf("チケット %d が見つからない", seq))
}

func internalMoveError(what string, err error) *apierr.Error {
	return apierr.New(apierr.InternalError).WithCause(fmt.Errorf("%s: %w", what, err))
}
