// エージェントの完了レポート（ApiDesign.md 9.15）。手順26c。
//
//	POST /api/v1/projects/{key}/tickets/{seq}/reports   ticket.transition
//
// **エージェントが1回の作業の結果を構造化して提出する口である**
// （Requirements.md 10.6.1）。DbDesign.md 8.2.4 の agent_run と agent_report に
// 1行ずつ書き、**同じトランザクションで完了レポートのコメントを1件作る。**
//
// **状態を進めない。** 26b で遷移が 9.6 として独立したので、完了レポートの提出と
// 状態遷移を1つの操作に混ぜない。**チケットもクローズしない**——done への遷移は
// is_agent_reachable=false かつ allowed_actor_kinds=["user"] で、DB とワークフローが
// 拒む（DbDesign.md 7.4）。ここに if を置かない（Design.md 8.1）。
//
// **完了条件（dod_item.is_satisfied）を書き換えない**（9.15）。いま API が開けている
// DoD の型は manual だけで、その定義は「人間がチェックを入れる」である
// （Requirements.md 10.5.2）。**エージェントが立てると型の定義に反する。**
//
// **9.6 の検証6（エージェントは所有者の担当だけ）は適用しない**（9.15）。あの規則は
// 「ボードの状態を動かすこと」への制約であり、レポートは状態を動かさない。
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/activity"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// レポートの status（9.15。DbDesign.md 8.2.4 の CHECK と一致する）。
var reportStatuses = []string{"completed", "blocked", "partial"}

// knowledge_impact の値域（同上）。
var knowledgeImpacts = []string{"none", "minor", "major"}

// runStatusCompleted は agent_run.status に立つ唯一の値
// （DbDesign.md 8.2.4）。failed / abandoned は「レポートを出さずに終わった run」で、
// それを観測する口が無い。running は開始を告げる口が無いので作られない。
const runStatusCompleted = "completed"

// reportRequest は 9.15 の要求。
//
// **本文は Requirements.md 10.6.1 のレポートから task_id を抜いたものである**
// （チケットは URL が指す。9.1）。
//
// **列に出す値だけを型で受ける。** 残りは json.RawMessage のまま report jsonb へ
// 入れる——知らないキーも拒まず保存する（9.15）。
type reportRequest struct {
	Status           string          `json:"status"`
	KnowledgeImpact  *string         `json:"knowledge_impact"`
	Cost             *reportCost     `json:"cost"`
	DoDResults       []reportDoD     `json:"dod_results"`
	Artifacts        json.RawMessage `json:"artifacts"`
	Findings         json.RawMessage `json:"findings"`
	Failures         json.RawMessage `json:"failures"`
	ProposedSubtasks json.RawMessage `json:"proposed_subtasks"`
}

// reportCost は 10.6.1 の cost。agent_run の tokens_used / turns へ展開する。
type reportCost struct {
	Tokens       *int64 `json:"tokens"`
	Turns        *int32 `json:"turns"`
	WallClockMin *int32 `json:"wall_clock_min"`
}

// reportDoD は 10.6.1 の dod_results の1件。
//
// **id は完了条件の ULID である**（pb_get_task の応答が返す値）。
type reportDoD struct {
	ID       string `json:"id"`
	Passed   *bool  `json:"passed"`
	Evidence string `json:"evidence"`
	Note     string `json:"note"`
}

// reportView は 9.15 の応答。
type reportView struct {
	ID              string          `json:"id"`
	AgentRunID      string          `json:"agent_run_id"`
	Seq             int32           `json:"seq"`
	Status          string          `json:"status"`
	KnowledgeImpact *string         `json:"knowledge_impact"`
	SubmittedAt     Time            `json:"submitted_at"`
	SubmittedBy     actorRef        `json:"submitted_by"`
	CommentID       string          `json:"comment_id"`
	UnsatisfiedDoD  []unsatisfiedID `json:"unsatisfied_dod"`
}

// unsatisfiedID は「レポートが passed: true として触れなかった完了条件」1件。
type unsatisfiedID struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Body string `json:"body"`
}

// ── POST /api/v1/projects/{key}/tickets/{seq}/reports ────────

// submitTicketReport は完了レポートを1件受け取る（9.15）。
func (h *handler) submitTicketReport(w http.ResponseWriter, r *http.Request) {
	ctx, scope, ticketID, ok := h.ticketScope(w, r,
		"POST /projects/{key}/tickets/{seq}/reports")
	if !ok {
		return
	}

	// **本文を2つの形で持つ。** raw は report jsonb へそのまま入れるためのもので、
	// **知らないキーもここに残る**（9.15）。req は検証と列への展開に使う。
	var raw map[string]json.RawMessage
	if e := decodeJSON(r, &raw); e != nil {
		apierr.Write(w, r, e)
		return
	}
	req, e := reportOf(raw)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	// 完了条件は検証にも応答にも要るので、先に1回だけ引く。
	dod, err := h.q.ListTicketDoD(ctx, ticketID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	if e := validateReport(req, dod); e != nil {
		apierr.Write(w, r, e)
		return
	}

	// **コメントの本文はトランザクションの前に組み立てて長さを見る**（9.15）。
	// 上限は 9.8 と同じ commentBodyMaxLen。超えたら何も作らずに弾く——切ると
	// 切った部分を人が読む手段が無く、そのまま入れると PATCH で保存し直せない。
	body := renderReportComment(req, dod)
	if n := utf8.RuneCountInString(body); n > commentBodyMaxLen {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "report", Code: "too_long",
			Message: fmt.Sprintf(
				"完了レポートが長すぎます（整形後%d文字、上限%d文字）。findings や failures を短くして出し直してください",
				n, commentBodyMaxLen),
		}))
		return
	}

	p := auth.PrincipalFromContext(ctx)
	rec := activity.FromRequest(r)
	runID, reportID, commentID := ulidgen.New(), ulidgen.New(), ulidgen.New()
	var view reportView

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		run, err := buildAgentRun(ctx, q, runID, ticketID, p, req)
		if err != nil {
			return err
		}
		if err := q.CreateAgentRun(ctx, run); err != nil {
			return fmt.Errorf("実行記録を作成できない: %w", err)
		}

		// **要求の本文をそのまま保存する**（9.15）。組み直すと、拒まないと決めた
		// 知らないキーが結局落ちる。
		payload, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("レポートを保存形式にできない: %w", err)
		}
		if err := q.CreateAgentReport(ctx, gen.CreateAgentReportParams{
			ID:              reportID,
			AgentRunID:      runID,
			TicketID:        ticketID,
			Status:          req.Status,
			Report:          payload,
			KnowledgeImpact: textOrNull(req.KnowledgeImpact),
		}); err != nil {
			return fmt.Errorf("完了レポートを保存できない: %w", err)
		}

		// **完了レポートのコメントを同じトランザクションで作る**（9.15）。
		// これが人がレポートを読む面である（GuiDesign.md 5.5）。**整形は
		// REST 層が行う**——MCP 層に置くと同じ規則が2か所に生まれる（Design.md 8.1）。
		if err := q.CreateComment(ctx, gen.CreateCommentParams{
			ID:         commentID,
			TicketID:   ticketID,
			AuthorID:   scope.actorID,
			BodyMd:     body,
			Kind:       commentKindProgress,
			Origin:     scope.origin(),
			InReplyTo:  pgtype.Text{},
			AgentRunID: pgtype.Text{String: runID, Valid: true},
		}); err != nil {
			return fmt.Errorf("完了レポートのコメントを作成できない: %w", err)
		}

		// **activity は field='report'、action='create'**（9.15）。コメントの
		// 作成は別に記録しない——同じトランザクションで作られた1件が2行になると、
		// 履歴が同じ事実を二度言う（9.6 が遷移コメントについて同じ扱いをしている）。
		field, newValue := "report", req.Status
		if err := rec.Record(ctx, q, activity.Entry{
			ProjectID:  scope.projectID,
			EntityType: activity.EntityTicket,
			EntityID:   ticketID,
			Action:     activity.Create,
			Field:      &field,
			NewValue:   &newValue,
		}); err != nil {
			return err
		}

		view = reportView{
			ID:              reportID,
			AgentRunID:      runID,
			Seq:             scope.seq,
			Status:          req.Status,
			KnowledgeImpact: req.KnowledgeImpact,
			SubmittedAt:     Time(run.EndedAt.Time),
			SubmittedBy: actorRef{
				ID: scope.actorID, Kind: scope.actorKind, DisplayName: p.DisplayName,
			},
			CommentID:      commentID,
			UnsatisfiedDoD: unsatisfiedDoD(req, dod),
		}
		return nil
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.Header().Set("Location", fmt.Sprintf(
		"/api/v1/projects/%s/tickets/%d/reports/%s", scope.key, scope.seq, reportID))
	WriteJSON(w, http.StatusCreated, view)
}

// ── 検証（9.15）─────────────────────────────────────────────

// validateReport は 9.15 の検証表を実施する。
//
// **列に出す値だけを厳しく見る。** status と knowledge_impact は agent_report の
// 列に、cost.tokens / cost.turns は agent_run の列に展開されるので、綴りや値域の
// 誤りが静かに落ちると集計が壊れる。**残りは report jsonb にそのまま入るだけ**
// なので、知らないキーも拒まない（Design.md 8.4 が「モデルが型を取り違えても
// 受けて通す」を採っているのと同じ判断）。
//
// **dod_results[].id だけは厳しくする。** 存在しない id を黙って捨てると、
// エージェントは報告したつもりの項目が未充足として返ってくることになり、
// /pb-implement の手順7 が「修正して再提出」を繰り返す。
//
// **details[].code は既存の語彙だけを使う**（required / invalid / out_of_range /
// not_found）。新しいコードを発明しない（9.15）。
func validateReport(req reportRequest, dod []gen.ListTicketDoDRow) *apierr.Error {
	var details []apierr.Detail

	switch status := strings.TrimSpace(req.Status); {
	case status == "":
		details = append(details, apierr.Detail{
			Field: "status", Code: "required", Message: "結果の区分を指定してください",
		})
	case !contains(reportStatuses, status):
		details = append(details, apierr.Detail{
			Field: "status", Code: "invalid",
			Message: "結果の区分は completed / blocked / partial のいずれかです",
		})
	}

	if req.KnowledgeImpact != nil && !contains(knowledgeImpacts, *req.KnowledgeImpact) {
		details = append(details, apierr.Detail{
			Field: "knowledge_impact", Code: "invalid",
			Message: "知識への影響は none / minor / major のいずれかです",
		})
	}

	if c := req.Cost; c != nil {
		if c.Tokens != nil && *c.Tokens < 0 {
			details = append(details, negativeCost("cost.tokens"))
		}
		if c.Turns != nil && *c.Turns < 0 {
			details = append(details, negativeCost("cost.turns"))
		}
		if c.WallClockMin != nil && *c.WallClockMin < 0 {
			details = append(details, negativeCost("cost.wall_clock_min"))
		}
	}

	known := make(map[string]bool, len(dod))
	for _, d := range dod {
		known[d.ID] = true
	}
	for i, res := range req.DoDResults {
		if !known[res.ID] {
			details = append(details, apierr.Detail{
				Field:   fmt.Sprintf("dod_results[%d].id", i),
				Code:    "not_found",
				Message: "このチケットに、その完了条件はありません",
			})
		}
	}

	if len(details) > 0 {
		return apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return nil
}

func negativeCost(field string) apierr.Detail {
	return apierr.Detail{
		Field: field, Code: "out_of_range", Message: "0以上の数値で指定してください",
	}
}

// ── 組み立て ────────────────────────────────────────────────

// buildAgentRun は 1回の実行記録を組み立てる（DbDesign.md 8.2.4）。
//
// **1提出 = 1 run である。** 開始を告げる口を持たず、「走っている run」を
// 読む者も居ない（ticket.working_agent_id が「いま誰が処理しているか」を担う）。
// **再提出は別の run になる**——その修正は、エージェントが実際に作業をやり直した
// ことを意味する。
func buildAgentRun(
	ctx context.Context, q gen.Querier, runID, ticketID string,
	p *auth.Principal, req reportRequest,
) (gen.CreateAgentRunParams, error) {
	now := time.Now().UTC()

	// started_at は cost.wall_clock_min から逆算する（DbDesign.md 8.2.4）。
	// **エージェントの自己申告である**——無ければ ended_at と同じ値を入れる。
	started := now
	if c := req.Cost; c != nil && c.WallClockMin != nil && *c.WallClockMin > 0 {
		started = now.Add(-time.Duration(*c.WallClockMin) * time.Minute)
	}

	// retry_count は同じ（チケット × アクター）の既存の run 数（8.2.4）。
	prior, err := q.CountAgentRunsForTicket(ctx, gen.CountAgentRunsForTicketParams{
		TicketID: ticketID, ActorID: p.ActorID,
	})
	if err != nil {
		return gen.CreateAgentRunParams{}, fmt.Errorf("過去の実行記録を数えられない: %w", err)
	}

	params := gen.CreateAgentRunParams{
		ID:         runID,
		TicketID:   ticketID,
		ActorID:    p.ActorID,
		TokenID:    pgtype.Text{String: p.TokenID, Valid: p.TokenID != ""},
		StartedAt:  pgtype.Timestamptz{Time: started, Valid: true},
		EndedAt:    pgtype.Timestamptz{Time: now, Valid: true},
		Status:     runStatusCompleted,
		RetryCount: int32(prior),
	}
	if c := req.Cost; c != nil {
		params.TokensUsed = int8OrNull(c.Tokens)
		params.Turns = int4OrNull(c.Turns)
	}

	// **client_kind / model_name / model_version は実行時点の値を写す**（8.2.4）。
	// 人のトークンで叩かれたときは agent の行が無いので、3つとも NULL になる。
	info, err := q.GetAgentRuntimeInfo(ctx, p.ActorID)
	switch {
	case err == nil:
		params.ClientKind = pgtype.Text{String: info.ClientKind, Valid: true}
		params.ModelName = info.ModelName
		params.ModelVersion = info.ModelVersion
	case errors.Is(err, pgx.ErrNoRows):
		// 人が叩いた。写す値が無い。
	default:
		return gen.CreateAgentRunParams{}, fmt.Errorf("エージェントの情報を読めない: %w", err)
	}
	return params, nil
}

// reportOf は生の本文を、検証と列への展開に使える形へ写す。
//
// **知らないキーはここで落ちるが、保存には raw を使うので消えない**（9.15）。
// 落ちるのは検証と描画の対象から外れることだけである。
func reportOf(raw map[string]json.RawMessage) (reportRequest, *apierr.Error) {
	var req reportRequest
	if len(raw) == 0 {
		return req, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return req, apierr.New(apierr.BadRequest).WithCause(err)
	}
	if err := json.Unmarshal(b, &req); err != nil {
		return req, apierr.New(apierr.BadRequest).
			WithCause(fmt.Errorf("レポートの形が読めない: %w", err))
	}
	return req, nil
}

// unsatisfiedDoD は「レポートが passed: true として触れなかった完了条件」を返す（9.15）。
//
// **is_satisfied=false のものではない。** この API は is_satisfied を動かさないので、
// それを返すと作業直後は必ず全件になり、/pb-implement の手順7 が終わらなくなる。
//
// **つまり自己申告どうしの突き合わせである。** 触れなかった項目と、passed: false と
// 申告した項目が返る。盤面は動かないまま、報告の漏れだけがその場で分かる。
func unsatisfiedDoD(req reportRequest, dod []gen.ListTicketDoDRow) []unsatisfiedID {
	passed := make(map[string]bool, len(req.DoDResults))
	for _, res := range req.DoDResults {
		if res.Passed != nil && *res.Passed {
			passed[res.ID] = true
		}
	}
	out := make([]unsatisfiedID, 0, len(dod))
	for _, d := range dod {
		if !passed[d.ID] {
			out = append(out, unsatisfiedID{ID: d.ID, Type: d.Type, Body: d.Body})
		}
	}
	return out
}

// ── 小道具 ──────────────────────────────────────────────────

func textOrNull(v *string) pgtype.Text {
	if v == nil {
		return pgtype.Text{}
	}
	return pgtype.Text{String: *v, Valid: true}
}

func int8OrNull(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func int4OrNull(v *int32) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: *v, Valid: true}
}
