// POST /api/v1/projects/{key}/tickets（ApiDesign.md 9.3）。
//
// **サーバが決めるものをリクエストで指定させない**（9.3 の表）。
//
//	seq         project_counter の1文 UPDATE ... RETURNING（DbDesign.md 6.4.1）
//	status_key  ワークフローのうち category='todo' かつ sort_order 最小
//	sort_key    現在の末尾の次（9.4 の LexoRank）
//	reporter_id 呼び出し元のアクター
//	version     1
//
// 初期ステータスを選ばせないのは、ワークフローの入口が workflow_transition に
// 定義されておらず（遷移元が無い）、任意のステータスで作成できると 9.6 の遷移
// 検証を素通りできてしまうためである。作成後に遷移させれば同じ状態に到達でき、
// その経路は検証を通る。
//
// **採番・ワークフロー解決・タグ付与・activity 記録は単一トランザクションで行う**
// （9.3）。5.3 の POST /projects と同じ方針で、途中で失敗したときに採番だけ
// 進んだ欠番や、履歴の無いチケットが残らないようにする。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/lexorank"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// ticketTitleMaxLen は 9.3 の検証表（DbDesign.md 6.6 の CHECK と同じ）。
const ticketTitleMaxLen = 200

// createTicketRequest は 9.3 のリクエスト。
//
// **ポインタなのは「送られていない」を区別する必要がある項目だけ**である。
// type / title は必須なので値型でよい。
type createTicketRequest struct {
	Type       string   `json:"type"`
	Title      string   `json:"title"`
	BodyMd     string   `json:"body_md"`
	Priority   string   `json:"priority"`
	AssigneeID string   `json:"assignee_id"`
	ParentSeq  *int32   `json:"parent_seq"`
	TagIDs     []string `json:"tag_ids"`
	// **sprint_id は 0028 で受け付けなくなった**（9.3）。struct から
	// 落とさず受けてから 422 に倒すのは、**黙って捨てると送った側が設定できた
	// つもりになる**ためである（decodeJSON は未知のキーを無視する）。
	SprintID      *string  `json:"sprint_id"`
	EstimatePoint *float64 `json:"estimate_point"`
	EstimateHours *float64 `json:"estimate_hours"`
	StartDate     string   `json:"start_date"`
	DueDate       string   `json:"due_date"`
}

// createTicket はチケットを1件作る。201 + Location + 9.5 形式の本体。
func (h *handler) createTicket(w http.ResponseWriter, r *http.Request) {
	p, key, projectID, ok := projectScopeContext(w, r, h.q, "POST /projects/{key}/tickets")
	if !ok {
		return
	}

	var req createTicketRequest
	if apiErr := decodeJSON(r, &req); apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	// 形だけで判定できる検証は、DBを引く前にまとめて済ませる。
	startDate, dueDate, apiErr := validateCreateTicket(&req)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	ctx := r.Context()
	rec := activity.FromRequest(r)
	ticketID := ulidgen.New()
	var (
		seq     int32
		view    ticketDetailView
		refErr  *apierr.Error // 参照先が見つからない類（422）
		created bool
	)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// 参照先の検証はトランザクションの中で行う。外で確かめてから入ると、
		// 確かめた行が消えていることがある（5.3 が check-key の結果を
		// 信頼しないのと同じ理由）。
		parentID, e := resolveTicketParent(ctx, q, projectID, req.ParentSeq)
		if e != nil {
			refErr = e
			return errTicketReference
		}
		if e := validateTicketAssignee(ctx, q, projectID, req.AssigneeID); e != nil {
			refErr = e
			return errTicketReference
		}
		if e := validateTicketTags(ctx, q, projectID, req.TagIDs); e != nil {
			refErr = e
			return errTicketReference
		}

		n, err := q.NextTicketSeq(ctx, projectID)
		if err != nil {
			return fmt.Errorf("チケット番号を採番できない: %w", err)
		}
		seq = n

		statusKey, err := q.ResolveInitialStatusKey(ctx, projectID)
		if err != nil {
			return fmt.Errorf("初期ステータスを決められない: %w", err)
		}

		sortKey, err := nextTicketSortKey(ctx, q, projectID)
		if err != nil {
			return err
		}

		if err := q.CreateTicket(ctx, gen.CreateTicketParams{
			ID:            ticketID,
			ProjectID:     projectID,
			Seq:           seq,
			ParentID:      parentID,
			Type:          req.Type,
			Title:         req.Title,
			BodyMd:        optionalText(req.BodyMd),
			StatusKey:     statusKey,
			Priority:      optionalText(req.Priority),
			AssigneeID:    optionalText(req.AssigneeID),
			ReporterID:    pgtype.Text{String: p.ActorID, Valid: true},
			EstimatePoint: float8Of(req.EstimatePoint),
			EstimateHours: float8Of(req.EstimateHours),
			StartDate:     startDate,
			DueDate:       dueDate,
			SortKey:       pgtype.Text{String: sortKey, Valid: true},
		}); err != nil {
			return fmt.Errorf("チケットを作成できない: %w", err)
		}

		for _, tagID := range dedupe(req.TagIDs) {
			if err := q.AttachTicketTag(ctx, gen.AttachTicketTagParams{
				TicketID: ticketID, TagID: tagID,
			}); err != nil {
				return fmt.Errorf("タグ %q を付けられない: %w", tagID, err)
			}
		}

		// **履歴は同じトランザクションで書く**（9.3）。記録の無いチケットが
		// 生まれないようにするため、失敗したら作成ごと失敗させる。
		if err := rec.Record(ctx, q, activity.Entry{
			ProjectID:  projectID,
			EntityType: activity.EntityTicket,
			EntityID:   ticketID,
			Action:     activity.Create,
		}); err != nil {
			return err
		}

		// **オンステージの配下に作ったら、進行中のスプリントへ入れる**（9.12.3）
		if err := joinActiveSprint(ctx, q, projectID, ticketID); err != nil {
			return err
		}

		// 応答は 9.5 と同形式（9.3）。作成直後の状態を同じトランザクションから読む。
		v, err := buildTicketDetail(ctx, q, projectID, seq)
		if err != nil {
			return fmt.Errorf("作成したチケットを読めない: %w", err)
		}
		view = v
		created = true
		return nil
	})

	switch {
	case refErr != nil:
		apierr.Write(w, r, refErr)
		return
	case err != nil:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケットを作成できない: %w", err)))
		return
	case !created:
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケットの作成が完了しなかった")))
		return
	}

	w.Header().Set("Location",
		fmt.Sprintf("%s/%s/tickets/%d", projectsPath, key, seq))
	WriteJSON(w, http.StatusCreated, view)
}

// errTicketReference は「参照先が見つからない」をトランザクションの外へ運ぶ番兵。
//
// RunInTx は fn が返した error でロールバックするだけなので、422 として返す
// べき失敗と 500 にすべき失敗を、呼び出し側で区別できる形にしておく。
var errTicketReference = fmt.Errorf("チケットの参照先が不正")

// validateCreateTicket は形だけで判定できる検証（9.3 の表）。
func validateCreateTicket(req *createTicketRequest) (pgtype.Date, pgtype.Date, *apierr.Error) {
	var details []apierr.Detail

	// **sprint_id は受け付けない**（9.3）。9.5.2 の PATCH と同じ
	// use_sprint_endpoint に倒す——作成時にだけ設定できて後から変えられないのは、
	// どちらの規則としても読めない中途半端な状態になる。
	if req.SprintID != nil {
		details = append(details, apierr.Detail{
			Field: "sprint_id", Code: "use_sprint_endpoint",
			Message: "スプリントの変更はスプリントの開始・終了で行ってください",
		})
	}

	if req.Type == "" {
		details = append(details, apierr.Detail{
			Field: "type", Code: "required", Message: "種別を選んでください",
		})
	} else if !slices.Contains(ticketTypes, req.Type) {
		details = append(details, apierr.Detail{
			Field: "type", Code: "invalid",
			Message: "種別は " + strings.Join(ticketTypes, " / ") + " のいずれかです",
		})
	}

	req.Title = strings.TrimSpace(req.Title)
	switch n := utf8.RuneCountInString(req.Title); {
	case n == 0:
		details = append(details, apierr.Detail{
			Field: "title", Code: "required", Message: "タイトルを入力してください",
		})
	case n > ticketTitleMaxLen:
		details = append(details, apierr.Detail{
			Field: "title", Code: "too_long",
			Message: fmt.Sprintf("タイトルは%d文字以内で入力してください", ticketTitleMaxLen),
		})
	}

	if req.Priority != "" && !slices.Contains(ticketPriorities, req.Priority) {
		details = append(details, apierr.Detail{
			Field: "priority", Code: "invalid",
			Message: "優先度は " + strings.Join(ticketPriorities, " / ") + " のいずれかです",
		})
	}

	if req.ParentSeq != nil && *req.ParentSeq < 1 {
		details = append(details, apierr.Detail{
			Field: "parent_seq", Code: "invalid",
			Message: "親チケットの番号は1以上で指定してください",
		})
	}

	details = appendNonNegative(details, "estimate_point", req.EstimatePoint)
	details = appendNonNegative(details, "estimate_hours", req.EstimateHours)

	// 日付は date 列であって timestamptz ではない（DbDesign.md 6.6）。
	// "2026-08-05T00:00:00Z" のような時刻つきは受けない（apitime.go）。
	startDate, okStart := parseAPIDate(req.StartDate)
	if !okStart {
		details = append(details, apierr.Detail{
			Field: "start_date", Code: "invalid",
			Message: "開始日は YYYY-MM-DD の形式で指定してください",
		})
	}
	dueDate, okDue := parseAPIDate(req.DueDate)
	if !okDue {
		details = append(details, apierr.Detail{
			Field: "due_date", Code: "invalid",
			Message: "期限は YYYY-MM-DD の形式で指定してください",
		})
	}
	// DbDesign.md 6.6 の ck_ticket_dates。DB の CHECK に当てると 500 になるので、
	// 同じ規則をここで先に見て 422 にする。
	if okStart && okDue && startDate.Valid && dueDate.Valid &&
		startDate.Time.After(dueDate.Time) {
		details = append(details, apierr.Detail{
			Field: "due_date", Code: "invalid",
			Message: "期限は開始日以降の日付で指定してください",
		})
	}

	if len(details) > 0 {
		return pgtype.Date{}, pgtype.Date{},
			apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return startDate, dueDate, nil
}

// resolveTicketParent は parent_seq を ticket.id へ解決する（9.3）。
//
// **同一プロジェクトに限る。** 親もリンク先も同一プロジェクト内に限る
// （9.1）。**循環の検査は要らない**——作りたてのチケットに
// 子孫はいないため、9.5.2 の parent_cycle は更新のときだけ起こる。
func resolveTicketParent(
	ctx context.Context, q gen.Querier, projectID string, parentSeq *int32,
) (pgtype.Text, *apierr.Error) {
	if parentSeq == nil {
		return pgtype.Text{}, nil
	}
	id, err := q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{
		ProjectID: projectID, Seq: *parentSeq,
	})
	if err != nil {
		return pgtype.Text{}, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "parent_seq", Code: "not_found",
			Message: fmt.Sprintf("親に指定したチケット %d が見つかりません", *parentSeq),
		})
	}
	return pgtype.Text{String: id, Valid: true}, nil
}

// validateTicketAssignee は担当者が当該プロジェクトのメンバーかを見る（9.3）。
// 違えば 422 の details[].code = "not_a_member"（9.14）。
func validateTicketAssignee(
	ctx context.Context, q gen.Querier, projectID, assigneeID string,
) *apierr.Error {
	if assigneeID == "" {
		return nil
	}
	member, err := q.IsProjectMember(ctx, gen.IsProjectMemberParams{
		ProjectID: projectID, ActorID: assigneeID,
	})
	if err != nil {
		return apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("担当者の所属を確認できない: %w", err))
	}
	if !member {
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "assignee_id", Code: "not_a_member",
			Message: "担当者はこのプロジェクトのメンバーから選んでください",
		})
	}
	return nil
}

// validateTicketWorkingAgent は working_agent_id を検証する（9.5.2。手順26b）。
//
// **メンバーであることを所有者で見る**（Design.md 6.5 の委譲）。エージェントは
// project_member の行を持たない——持たせると所有者のロールと二重になり、
// 所有者のロールを変えたときに片方だけ古くなる。したがって
// validateTicketAssignee と同じ検証をエージェント自身に対して行うと必ず落ちる。
//
// **エージェントでなければ not_found に倒す。** 実行者の欄に人を入れる経路を
// 作らない（DbDesign.md 6.6 が担当と実行者を分けた意味が消える）。
func validateTicketWorkingAgent(
	ctx context.Context, q gen.Querier, projectID, agentActorID string,
) *apierr.Error {
	if agentActorID == "" {
		return nil
	}
	owner, err := q.GetAgentOwner(ctx, agentActorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
				Field: "working_agent_id", Code: "not_found",
				Message: "指定されたエージェントが見つかりません",
			})
		}
		return apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("エージェントの所有者を確認できない: %w", err))
	}
	member, err := q.IsProjectMember(ctx, gen.IsProjectMemberParams{
		ProjectID: projectID, ActorID: owner,
	})
	if err != nil {
		return apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("エージェントの所有者の所属を確認できない: %w", err))
	}
	if !member {
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "working_agent_id", Code: "not_a_member",
			Message: "実行者は、このプロジェクトのメンバーが所有するエージェントから選んでください",
		})
	}
	return nil
}

// validateTicketTags は tag_ids がすべて当該プロジェクトのタグかを見る（9.3）。
func validateTicketTags(
	ctx context.Context, q gen.Querier, projectID string, tagIDs []string,
) *apierr.Error {
	ids := dedupe(tagIDs)
	if len(ids) == 0 {
		return nil
	}
	n, err := q.CountProjectTagsByIDs(ctx, gen.CountProjectTagsByIDsParams{
		ProjectID: projectID, Ids: ids,
	})
	if err != nil {
		return apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("タグの所属を確認できない: %w", err))
	}
	if int(n) != len(ids) {
		return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "tag_ids", Code: "not_found",
			Message: "このプロジェクトに無いタグが含まれています",
		})
	}
	return nil
}

// nextTicketSortKey は末尾の次のキーを作る（9.3 の「現在の末尾の次」）。
//
// **末尾のキーが読めない・詰まっているときは振り直す。** 手で入れた行や、
// 手順16a 以前に直接 INSERT した行は sort_key を持たない（NULL）ことがあり、
// そのままでは Between が形式外として弾く。作成のたびに壊れるより、
// その場で整えたほうが利用者から見た挙動が素直になる。
func nextTicketSortKey(ctx context.Context, q gen.Querier, projectID string) (string, error) {
	last, err := q.MaxTicketSortKey(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("末尾の並び順を読めない: %w", err)
	}
	if key, ok := lexorank.Between(last, ""); ok {
		return key, nil
	}
	if _, err := rebalanceTicketSortKeys(ctx, q, projectID); err != nil {
		return "", err
	}
	last, err = q.MaxTicketSortKey(ctx, projectID)
	if err != nil {
		return "", fmt.Errorf("振り直し後の末尾を読めない: %w", err)
	}
	key, ok := lexorank.Between(last, "")
	if !ok {
		return "", fmt.Errorf("振り直しても並び順のキーを作れない（末尾=%q）", last)
	}
	return key, nil
}

// appendNonNegative は「0以上」の検証（9.3 の estimate_point / estimate_hours）。
func appendNonNegative(details []apierr.Detail, field string, v *float64) []apierr.Detail {
	if v != nil && *v < 0 {
		details = append(details, apierr.Detail{
			Field: field, Code: "out_of_range", Message: "0以上の数値で指定してください",
		})
	}
	return details
}

// float8Of は *float64 を NULL 可能な倍精度列へ写す。
func float8Of(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

// dedupe は順序を保ったまま重複を落とす。
func dedupe(values []string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
