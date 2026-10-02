package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"
)

// pb_wait は、チケットの変化を待つ（Design.md 8.5.6。Delegation.md 10章。pb-158）。
//
// **通知はどのクライアントでもモデルを起こさない**（Delegation.md 10.2 の実機検証）ので、
// エージェントが状態の変化を知る口は「待ち受けを繰り返す」になる。評価エージェントが
// 評価をコメントで書けば、待っている main の呼び出しが返る。
//
// **内部の REST の日時は ISO8601 の文字列で返る**（getREST が isoTimes を通す。8.4）。
//
// **変化は activity（ApiDesign.md 9.13.2）で見る**——コメント・遷移・本文の更新が1つの
// 時系列に並ぶ。**MCP 層で既存の REST を間隔を置いて問い合わせる**だけで、新しい REST の
// 口もスキーマも持たない（8.1）。権限（ticket.view）とプロジェクトのスコープは REST が
// 判定するので、プロジェクトの外を待つ経路は無い。

// 待ち受けの間隔と長さ。**試験で短くするために変数にしてある。**
var (
	waitPollInterval     = 3 * time.Second
	waitProgressInterval = 20 * time.Second
	// waitWithProgress は進捗の通知を流せるときの1回の長さ。**Copilot は約5分で打ち切る**
	// （Delegation.md 10.2）ので、それより短く切る。
	waitWithProgress = 4 * time.Minute
	// waitWithoutProgress は進捗の通知を流せないときの長さ。**Claude Desktop は60秒で
	// 打ち切り、打ち切りのエラーが続くとモデルが待ち受けをやめる**（同 10.2）。
	waitWithoutProgress = 50 * time.Second
)

// waitCommentLimit は、変化に添えるコメント本文の上限（1件あたりの文字数と件数）。
// 評価の全文を渡すためのもので、1回の応答を膨らませすぎないよう上限を置く。
const (
	waitCommentRunes = 4000
	waitCommentCount = 5
)

// progressFunc は進捗の通知を1回送る。呼び出しに progressToken が無ければ nil である。
type progressFunc func(elapsed time.Duration)

func waitTools() []tool {
	return []tool{{
		Name: "pb_wait",
		Description: "チケットの変化（コメント・状態の遷移・本文の更新）を待つ。変化があるか、PB が決めた時間（数十秒〜数分）が過ぎたら返る。" +
			"**評価や返事を待つときはこれを呼び、結果の next に従うこと。** 変化が無ければ、返った cursor を since に渡してもう一度呼ぶ。" +
			"変化にコメントが含まれれば、その本文を comments に添えて返す。" +
			"同じセッションで繰り返し呼んでよい。" +
			"エラーで返ったときも待ち受けをやめず、最後に受け取った cursor を since に渡して呼び直す（省くと、そのあいだの変化を見逃す）。",
		InputSchema: schema{
			Type: "object",
			Properties: map[string]property{
				"seq":   {Type: "integer", Description: "待つチケットの番号（seq）", Minimum: intPtr(1)},
				"since": {Type: "string", Description: "前回の pb_wait が返した cursor。省略すると「いまから後」の変化を待つ"},
			},
			Required: []string{"seq"},
		},
		long: callWait,
	}}
}

type waitArgs struct {
	Seq   flexInt `json:"seq"`
	Since *string `json:"since"`
}

// activityItem は 9.13.2 の1行のうち、待ち受けが使う項目。
type activityItem struct {
	ID    string `json:"id"`
	Actor *struct {
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
	} `json:"actor"`
	Action     string  `json:"action"`
	Field      *string `json:"field"`
	NewValue   *string `json:"new_value"`
	OccurredAt string  `json:"occurred_at"`
}

// commentItem は 9.8 の1件のうち、待ち受けが添える項目。
type commentItem struct {
	ID     string  `json:"id"`
	Kind   string  `json:"kind"`
	BodyMD *string `json:"body_md"`
	Origin string  `json:"origin"`
	Author struct {
		Kind        string `json:"kind"`
		DisplayName string `json:"display_name"`
	} `json:"author"`
	CreatedAt string `json:"created_at"`
}

func callWait(ctx context.Context, h *Handler, r *http.Request, key string, args json.RawMessage, progress progressFunc) (toolResult, *rpcError) {
	var in waitArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	seq, rpcErr := requireSeq(in.Seq)
	if rpcErr != nil {
		return toolResult{}, rpcErr
	}

	// **チケットが在ることを先に確かめる。** activity は存在しない seq に空の一覧を
	// 200 で返す（9.13.2）ので、そのまま待つと、無いチケットを時間いっぱい待ってしまう。
	if res, err := h.getREST(r, ticketPath(key, seq), nil); err != nil || !res.ok() {
		return passThrough(r, res, err)
	}

	start := time.Now()
	items, res, err := h.activityOf(r, key, seq)
	if err != nil || !res.ok() {
		return passThrough(r, res, err)
	}
	cursor := ""
	if in.Since != nil {
		cursor = *in.Since
	} else if len(items) > 0 {
		cursor = items[0].ID
	}

	limit := waitWithoutProgress
	if progress != nil {
		limit = waitWithProgress
	}
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	poll := time.NewTicker(waitPollInterval)
	defer poll.Stop()
	var tick <-chan time.Time
	if progress != nil {
		t := time.NewTicker(waitProgressInterval)
		defer t.Stop()
		tick = t.C
	}

	for {
		if fresh := newerThan(items, cursor); len(fresh) > 0 {
			return h.waitResult(r, key, seq, fresh, start, len(fresh) == len(items))
		}
		select {
		case <-ctx.Done():
			// クライアントが切った。結果は届かないので、返すものは何でもよい。
			return toolResult{}, newError(codeInternalError, "待ち受けが取り消された: "+context.Cause(ctx).Error())
		case <-deadline.C:
			return waitTimeout(cursor, start), nil
		case <-tick:
			progress(time.Since(start))
		case <-poll.C:
			items, res, err = h.activityOf(r, key, seq)
			if err != nil || !res.ok() {
				return passThrough(r, res, err)
			}
		}
	}
}

// activityOf はチケット1件の変更履歴の最新ページを読む（新しい順）。
func (h *Handler) activityOf(r *http.Request, key string, seq int64) ([]activityItem, restResult, error) {
	q := url.Values{}
	q.Set("entity", "ticket:"+strconv.FormatInt(seq, 10))
	q.Set("per_page", "20")
	res, err := h.getREST(r, projectPath(key)+"/activity", q)
	if err != nil || !res.ok() {
		return nil, res, err
	}
	var page struct {
		Items []activityItem `json:"items"`
	}
	if err := json.Unmarshal(res.body, &page); err != nil {
		return nil, res, errors.New("変更履歴の応答を解釈できない: " + err.Error())
	}
	return page.Items, res, nil
}

// newerThan は cursor より新しい行を返す（新しい順のまま）。
//
// **activity.id は ULID で、時刻順に単調に増える**（ApiDesign.md 9.13.2 の並びの tie-break）。
// 文字列の大小がそのまま新旧になる。cursor が空なら全部が新しい。
func newerThan(items []activityItem, cursor string) []activityItem {
	var out []activityItem
	for _, it := range items {
		if it.ID > cursor {
			out = append(out, it)
		}
	}
	return out
}

// waitResult は変化があったときの応答を組み立てる。
func (h *Handler) waitResult(r *http.Request, key string, seq int64, fresh []activityItem, start time.Time, maybeMore bool) (toolResult, *rpcError) {
	type change struct {
		ID         string  `json:"id"`
		OccurredAt string  `json:"occurred_at"`
		Actor      string  `json:"actor"`
		ActorKind  string  `json:"actor_kind"`
		Action     string  `json:"action"`
		Field      *string `json:"field"`
		Summary    *string `json:"summary"`
	}
	changes := make([]change, 0, len(fresh))
	var oldestComment time.Time
	for _, it := range slices.Backward(fresh) { // 古い順にして返す
		c := change{ID: it.ID, OccurredAt: it.OccurredAt, Action: it.Action, Field: it.Field, Summary: it.NewValue}
		if it.Actor != nil {
			c.Actor, c.ActorKind = it.Actor.DisplayName, it.Actor.Kind
		}
		changes = append(changes, c)
		if at, err := time.Parse(time.RFC3339Nano, it.OccurredAt); err == nil &&
			it.Field != nil && *it.Field == "comment" && (oldestComment.IsZero() || at.Before(oldestComment)) {
			oldestComment = at
		}
	}

	out := map[string]any{
		"changed":        true,
		"cursor":         fresh[0].ID,
		"changes":        changes,
		"waited_seconds": int(time.Since(start).Seconds()),
		"next": "変化があった。changes と comments を読み、それに応じて動く。" +
			"引き続き待つなら、cursor を since に渡して pb_wait を呼ぶ。",
	}
	if maybeMore {
		out["more_changes_may_exist"] = true
	}
	if !oldestComment.IsZero() {
		comments, rpcErr := h.newComments(r, key, seq, oldestComment)
		if rpcErr != nil {
			return toolResult{}, rpcErr
		}
		out["comments"] = comments
	}
	b, err := json.Marshal(out)
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	return textResult(string(b)), nil
}

// newComments は since 以降に書かれたコメントの本文を、古い順に返す。
//
// activity のコメントの行は本文の先頭40字の要約しか持たない（ApiDesign.md 9.8）ので、
// **評価の全文を渡すために 9.8 の一覧を引く。** 新しい順に1ページだけ読む。
func (h *Handler) newComments(r *http.Request, key string, seq int64, since time.Time) ([]map[string]any, *rpcError) {
	q := url.Values{}
	q.Set("order", "desc")
	q.Set("per_page", "20")
	res, err := h.getREST(r, ticketPath(key, seq)+"/comments", q)
	if err != nil {
		return nil, newError(codeInternalError, err.Error())
	}
	if !res.ok() {
		return nil, nil // 本文が読めなくても、変化があったことは返す
	}
	var page struct {
		Items []commentItem `json:"items"`
	}
	if err := json.Unmarshal(res.body, &page); err != nil {
		return nil, newError(codeInternalError, "コメントの応答を解釈できない: "+err.Error())
	}
	var out []map[string]any
	// activity の occurred_at とコメントの created_at は別の行の時刻なので、1秒の幅を見る。
	for _, c := range page.Items {
		at, err := time.Parse(time.RFC3339Nano, c.CreatedAt)
		if err != nil || at.Before(since.Add(-time.Second)) || c.BodyMD == nil {
			continue
		}
		body, cut := truncateRunes(*c.BodyMD, waitCommentRunes)
		m := map[string]any{
			"id": c.ID, "kind": c.Kind, "origin": c.Origin,
			"author": c.Author.DisplayName, "author_kind": c.Author.Kind,
			"created_at": c.CreatedAt, "body_md": body,
		}
		if cut {
			m["truncated"] = true
		}
		out = append(out, m)
		if len(out) == waitCommentCount {
			break
		}
	}
	slices.Reverse(out)
	return out, nil
}

// waitTimeout は時間切れの応答である。**エラーにしない**——打ち切りのエラーが続くと、
// モデルは待ち受けをやめてしまう（Delegation.md 10.2 の Claude Desktop）。
func waitTimeout(cursor string, start time.Time) toolResult {
	b, _ := json.Marshal(map[string]any{
		"changed":        false,
		"cursor":         cursor,
		"waited_seconds": int(time.Since(start).Seconds()),
		"next":           "まだ変化は無い。cursor を since に渡して、もう一度 pb_wait を呼ぶ。",
	})
	return textResult(string(b))
}

func truncateRunes(s string, n int) (string, bool) {
	if utf8.RuneCountInString(s) <= n {
		return s, false
	}
	return string([]rune(s)[:n]), true
}
