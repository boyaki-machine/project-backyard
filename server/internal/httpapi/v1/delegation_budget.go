// 委任の予算の計測と自動停止。Delegation.md 4.2・8.2・12.1。pb-253。
//
// **予算は delegation.scope の budget に書く**（PM が承認する委任範囲の一部。4.2）。
// どの項目も省略でき、省略した項目は上限を持たない。知らないキーは 422 で拒む——
// 綴りを誤った上限が黙って「上限なし」になるのを防ぐ。
//
//	tokens            トークンの総予算
//	tokens_per_month  暦月（プロジェクトのタイムゾーン）あたりのトークン
//	due_on            期日（YYYY-MM-DD）。この日を過ぎたら止める
//	tickets           木の子孫の数
//	depth             根からの段数（根は0、根の子は1）
//	rounds            同じ対象への不合格の往復の上限（省略時は evaluationRoundLimit）
//
// **測るのは、数が増える書き込みのその場である**（定期実行を持たない）。
//
//   - 起票（main）：起票数・深さ・期日。**超える起票そのものを拒む**
//   - 完了レポート（木の中）：期日は先に見て拒む。トークンは受け付けてから見る
//     （報告の後にしか分からない）
//   - 評価：トークンは受け付けてから見る。往復は rounds
//
// 超えたら確認待ち（budget_exceeded）を立て、委任を停止にする（8.2）。
// **拒んだ書き込みと同じトランザクションで停止を確定させる**——呼び出し側は
// 拒否の理由を持ったまま nil を返し、停止をコミットしてから 403 を返す。
//
// **#172（agent_run.retry_count のブレーカー）とは器を共有しない。** #172 は
// 実行1件の失敗の回数、こちらは委任全体の累計で、測る単位が違う。共有するのは
// 確認待ちを立てる raiseSystemConfirmation だけである。
package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// delegationBudget は scope.budget（4.2）。nil の項目は上限を持たない。
type delegationBudget struct {
	Tokens         *int64  `json:"tokens,omitempty"`
	TokensPerMonth *int64  `json:"tokens_per_month,omitempty"`
	DueOn          *string `json:"due_on,omitempty"`
	Tickets        *int64  `json:"tickets,omitempty"`
	Depth          *int32  `json:"depth,omitempty"`
	Rounds         *int32  `json:"rounds,omitempty"`
}

// budgetOfScope は scope から budget を読む。検証は済んでいる前提で、読めなければ
// 上限なしとして扱う（検証前の古い委任でも強制が 500 にならないように）。
func budgetOfScope(scope []byte) delegationBudget {
	var s struct {
		Budget delegationBudget `json:"budget"`
	}
	_ = json.Unmarshal(scope, &s)
	return s.Budget
}

// stopConditionsMax / stopConditionMaxLen は scope.stop_conditions の上限（pb-252）。
const (
	stopConditionsMax   = 20
	stopConditionMaxLen = 500
)

// validateScopeConstraints は scope の制約（budget と stop_conditions）の形を確かめる
// （4.2。pb-253・pb-252）。field は detail の名前の接頭辞（"scope"）。
func validateScopeConstraints(scope []byte, field string) []apierr.Detail {
	details := validateScopeBudget(scope, field)
	var s map[string]json.RawMessage
	if err := json.Unmarshal(scope, &s); err != nil {
		return details
	}
	if raw, ok := s["stop_conditions"]; ok {
		details = append(details, validateStopConditionsJSON(raw, field+".stop_conditions")...)
	}
	// 指標の定義と要約の周期（4.2・8.3。pb-277）。
	if raw, ok := s["metrics"]; ok {
		details = append(details, validateMetricsJSON(raw, field+".metrics")...)
	}
	if raw, ok := s["report_every_days"]; ok {
		details = append(details, validateReportEveryDaysJSON(raw, field+".report_every_days")...)
	}
	// main が止まったとみなす時間（10.4。pb-310）。
	if raw, ok := s["main_idle_minutes"]; ok {
		var n float64
		if err := json.Unmarshal(raw, &n); err != nil || n != math.Trunc(n) || n < 0 || n > mainIdleMinutesMax {
			details = append(details, apierr.Detail{Field: field + ".main_idle_minutes", Code: "invalid",
				Message: fmt.Sprintf("main が止まったとみなす時間は0〜%d分の整数で指定してください", mainIdleMinutesMax)})
		}
	}
	return details
}

// mainIdleMinutesMax / mainIdleMinutesDefault は scope.main_idle_minutes の上限と既定（10.4。pb-310）。
const (
	mainIdleMinutesMax     = 1440
	mainIdleMinutesDefault = 30
)

// mainIdleMinutesOf は scope から main が止まったとみなす時間（分）を読む。
// 省略は既定の30分、0 は判定しない。
func mainIdleMinutesOf(scope []byte) int {
	var s struct {
		MainIdleMinutes *int `json:"main_idle_minutes"`
	}
	_ = json.Unmarshal(scope, &s)
	if s.MainIdleMinutes == nil {
		return mainIdleMinutesDefault
	}
	return *s.MainIdleMinutes
}

// mainStopped は main が止まっているとみなすかを返す（10.4）。委任が承認済か計画中で、
// 最後に要求が届いてから閾値を過ぎたとき真。1度も要求が無ければ判定しない（始めたばかり）。
func mainStopped(status string, scope []byte, lastSeen *time.Time, now time.Time) bool {
	minutes := mainIdleMinutesOf(scope)
	if minutes == 0 || lastSeen == nil || (status != "approved" && status != "planning") {
		return false
	}
	return now.Sub(*lastSeen) >= time.Duration(minutes)*time.Minute
}

// validateStopConditionsJSON は止まる条件の配列を確かめる。**PB は条件を判定しない**
// ——エージェントが読んで守る文である（4.2）。空の項目は意味を持たないので拒む。
func validateStopConditionsJSON(raw json.RawMessage, field string) []apierr.Detail {
	invalid := func(msg string) []apierr.Detail {
		return []apierr.Detail{{Field: field, Code: "invalid", Message: msg}}
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err != nil || list == nil {
		return invalid("止まる条件は文字列の配列で指定してください")
	}
	if len(list) > stopConditionsMax {
		return invalid(fmt.Sprintf("止まる条件は%d件までです", stopConditionsMax))
	}
	for _, c := range list {
		if strings.TrimSpace(c) == "" {
			return invalid("空の止まる条件は書けません")
		}
		if utf8.RuneCountInString(c) > stopConditionMaxLen {
			return invalid(fmt.Sprintf("止まる条件は1件%d文字までです", stopConditionMaxLen))
		}
	}
	return nil
}

// validateScopeBudget は scope.budget の形を確かめる。field は detail の名前の
// 接頭辞（"scope" / "budget"）。budget が無ければ何も言わない。
func validateScopeBudget(scope []byte, field string) []apierr.Detail {
	var s map[string]json.RawMessage
	if err := json.Unmarshal(scope, &s); err != nil {
		return nil // scope 自体の形は呼び出し側が見る
	}
	raw, ok := s["budget"]
	if !ok {
		return nil
	}
	return validateBudgetJSON(raw, field+".budget")
}

// validateBudgetJSON は budget オブジェクト1つを確かめる。
func validateBudgetJSON(raw json.RawMessage, field string) []apierr.Detail {
	invalid := func(f, msg string) []apierr.Detail {
		return []apierr.Detail{{Field: f, Code: "invalid", Message: msg}}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return invalid(field, "budget はオブジェクトで指定してください")
	}
	var details []apierr.Detail
	for key, v := range m {
		f := field + "." + key
		switch key {
		case "tokens", "tokens_per_month", "tickets", "depth", "rounds":
			// any へ読んで json.Number であることを見る——json.Number へ直接読むと
			// "100" のような文字列も数として通ってしまう。
			dec := json.NewDecoder(bytes.NewReader(v))
			dec.UseNumber()
			var val any
			num, isNum := json.Number(""), false
			if err := dec.Decode(&val); err == nil {
				num, isNum = val.(json.Number)
			}
			if !isNum {
				details = append(details, invalid(f, key+" は1以上の整数で指定してください")...)
				continue
			}
			n, err := num.Int64()
			if err != nil || n < 1 {
				details = append(details, invalid(f, key+" は1以上の整数で指定してください")...)
				continue
			}
			if (key == "depth" || key == "rounds") && n > 1<<30 {
				details = append(details, invalid(f, key+" が大きすぎます")...)
			}
		case "due_on":
			var d string
			if err := json.Unmarshal(v, &d); err != nil {
				details = append(details, invalid(f, "due_on は YYYY-MM-DD で指定してください")...)
				continue
			}
			if _, err := time.Parse("2006-01-02", d); err != nil {
				details = append(details, invalid(f, "due_on は YYYY-MM-DD で指定してください")...)
			}
		default:
			details = append(details, invalid(f, fmt.Sprintf(
				"budget に %q は書けません。tokens / tokens_per_month / due_on / tickets / depth / rounds のいずれかです", key))...)
		}
	}
	return details
}

// budgetCheck は1回の判定の入力。起票の前なら newTickets=1 と親の深さを渡す。
type budgetCheck struct {
	newTickets  int64
	parentDepth int32 // newTickets>0 のときだけ見る
	checkTokens bool  // トークンの超過を見るか（報告・評価の後）
}

// exceeded は上限を超えた項目を、画面に出せる文で返す（空なら超えていない）。
func (b delegationBudget) exceeded(u gen.GetDelegationBudgetUsageRow, c budgetCheck) []string {
	var out []string
	if b.DueOn != nil && u.Today > *b.DueOn {
		out = append(out, fmt.Sprintf("期日（%s）を過ぎました", *b.DueOn))
	}
	if c.newTickets > 0 {
		if b.Tickets != nil && u.Tickets+c.newTickets > *b.Tickets {
			out = append(out, fmt.Sprintf("子チケットの数の上限（%d件）を超えます", *b.Tickets))
		}
		if b.Depth != nil && c.parentDepth+1 > *b.Depth {
			out = append(out, fmt.Sprintf("子チケットの階層の上限（%d段）を超えます", *b.Depth))
		}
	}
	if c.checkTokens {
		if b.Tokens != nil && u.TokensTotal > *b.Tokens {
			out = append(out, fmt.Sprintf("トークンの上限（%d）を超えました（申告された消費 %d）", *b.Tokens, u.TokensTotal))
		}
		if b.TokensPerMonth != nil && u.TokensThisMonth > *b.TokensPerMonth {
			out = append(out, fmt.Sprintf("今月のトークンの上限（%d）を超えました（申告された消費 %d）", *b.TokensPerMonth, u.TokensThisMonth))
		}
	}
	return out
}

// checkDelegationBudget は消費を測って、超えた項目を返す。
func checkDelegationBudget(
	ctx context.Context, q gen.Querier, delegationID string, scope []byte, probeTicketID string, c budgetCheck,
) ([]string, error) {
	b := budgetOfScope(scope)
	if b == (delegationBudget{}) {
		return nil, nil
	}
	u, err := q.GetDelegationBudgetUsage(ctx, gen.GetDelegationBudgetUsageParams{
		DelegationID: delegationID, ProbeTicketID: probeTicketID,
	})
	if err != nil {
		return nil, fmt.Errorf("予算の消費を測れない: %w", err)
	}
	if c.newTickets > 0 {
		c.parentDepth = u.ProbeDepth
	}
	return b.exceeded(u, c), nil
}

// tripDelegationBudget は委任を停止にし、確認待ち（budget_exceeded）を立てる（8.2）。
// raised_by は超過を検出した書き込みの呼び出し元に当てる——専用の system actor は
// まだ無い（evaluation.go の往復の上限と同じ扱い）。
func tripDelegationBudget(
	ctx context.Context, q gen.Querier, delegationID, rootTicketID, raisedBy string, reasons []string,
) error {
	if _, err := q.SetDelegationStatus(ctx, gen.SetDelegationStatusParams{
		ID: delegationID, Status: "paused",
	}); err != nil {
		return fmt.Errorf("委任を停止できない: %w", err)
	}
	return raiseSystemConfirmation(ctx, q, delegationID, rootTicketID,
		confirmationReasonBudgetExceeded, budgetExceededQuestion(reasons), raisedBy)
}

func budgetExceededQuestion(reasons []string) string {
	// **PM が画面で読む文である**（ユーザ確認待ち。GuiDesign.md 5.5「自律実行」）。
	// 設計の語（委任・予算）を出さず、画面の語（自律実行・制約）で書く（pb-252）。
	return "制約を超えたため、PB が自律実行を一時停止しました。\n\n- " +
		strings.Join(reasons, "\n- ") +
		"\n\n制約を見直して再開するか、このまま止めておくかを決めてください。回答した後、「再開…」から再開できます（制約は再開のときに直せます）。"
}

// budgetForbidden は予算の超過で拒んだ書き込みの 403。
func budgetForbidden(rootSeq int32, field string, reasons []string) *apierr.Error {
	return apierr.New(apierr.Forbidden).
		WithMessage("委任の予算を超えるため受け付けません。PB が委任を一時停止しました：" + strings.Join(reasons, "／")).
		WithDetails(apierr.Detail{
			Field: field, Code: "delegation_budget",
			Message: fmt.Sprintf("根のチケット #%d の委任の予算により拒まれました", rootSeq),
		})
}

// ── 消費の見せ方（delegation-status。12.1・9.1）──────────────────

// budgetStatusView は delegation-status に載せる予算と消費。
type budgetStatusView struct {
	Limits          delegationBudget `json:"limits"`
	Tickets         int64            `json:"tickets"`
	MaxDepth        int32            `json:"max_depth"`
	TokensTotal     int64            `json:"tokens_total"`
	TokensThisMonth int64            `json:"tokens_this_month"`
	TokensMissing   int64            `json:"tokens_missing"`
	Today           string           `json:"today"`
}

func budgetStatusOf(scope []byte, u gen.GetDelegationBudgetUsageRow) budgetStatusView {
	return budgetStatusView{
		Limits: budgetOfScope(scope), Tickets: u.Tickets, MaxDepth: u.MaxDepth,
		TokensTotal: u.TokensTotal, TokensThisMonth: u.TokensThisMonth,
		TokensMissing: u.TokensMissing, Today: u.Today,
	}
}

// roundLimitOf は評価の往復の上限を返す（budget.rounds が無ければ固定値）。
func roundLimitOf(scope []byte) int32 {
	if r := budgetOfScope(scope).Rounds; r != nil {
		return *r
	}
	return evaluationRoundLimit
}
