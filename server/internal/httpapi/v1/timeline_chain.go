// 連鎖のずらしの計算（ApiDesign.md 9.17.1。pb-296）。
//
// **DB にも HTTP にも触れない純粋な関数に切り出してある。** 規則の組み合わせ（隙間の吸収・FS・
// アンカー・着手済み・完了・「いま」・縮める）が多く、結合テストでは場面を網羅できないため、
// 単体テスト（timeline_chain_test.go）で厚く確かめる。ハンドラ（timeline_reschedule.go）は
// 行を読んでここへ渡し、結果を書くだけである。
//
// 時刻はすべてエポックミリ秒で持つ（REST と同じ単位。ApiDesign.md 2.2）。
package v1

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// chainSpan は予定の半開区間 [Start, Due)（エポックミリ秒）。
type chainSpan struct {
	Start int64 `json:"start_at"`
	Due   int64 `json:"due_at"`
}

// chainItem は列（9.17.1）の1件。列の外でも、きっかけはこの形で渡す。
type chainItem struct {
	Seq         int32
	Title       string
	Span        chainSpan
	StartPinned bool
	DuePinned   bool
	// Started は着手済み（状態の区分が todo でない）、Done は完了（区分が done）。
	// **状態から導く暗黙のアンカー**で、列には写さない（DbDesign.md 6.6.2）。
	Started bool
	Done    bool
	Version int32
}

// startFixed は「開始が固定」（9.17.1 の用語）。start_pinned か着手済み。完了は着手済みに含まれる。
func (it chainItem) startFixed() bool { return it.StartPinned || it.Started || it.Done }

// dueFixed は「終了が固定」。due_pinned か完了。
func (it chainItem) dueFixed() bool { return it.DuePinned || it.Done }

// chainFS は同じ行の FS の依存（先行 Pred の終了 ＋ LagDays 暦日 → 後続 Succ の開始）。
type chainFS struct {
	Pred, Succ int32
	LagDays    int32
}

// chainPlan は計算の入力。
type chainPlan struct {
	// Column は行の列（時間重複チェック付き・時刻付き・両端あり）。きっかけが列に入るなら含める。
	Column []chainItem
	Target chainItem
	// InColumn はきっかけが列に入るか。偽ならきっかけ自身だけが動く（9.17.1「列の外は一切動かさない」）。
	InColumn bool
	// NewSpan はきっかけの新しい予定（checkRescheduleTarget が検証済みのもの）。
	NewSpan chainSpan
	// Compact は [ここで詰める]（op=compact）。後ろの列を同じだけ前へ寄せる（9.17.1 の手順1）。
	Compact bool
	Links   []chainFS
	// OtherDue は列の外の、同じ行のチケットの終了（FS の先行が列の外にあるとき読む）。
	OtherDue map[int32]int64
	// MinLen は最小の長さ m（timeline_snap_seconds）、Now はサーバの時刻。
	MinLen int64
	Now    int64
	// Shrink は on_block=shrink（明示したときだけ縮める）。
	Shrink bool
	// Loc は基準タイムゾーン。lag_days の暦日の足し算と、理由の文言の時刻に使う。
	Loc *time.Location
}

// chainChange は動く行の1件（9.17.2 の changes[]）。
type chainChange struct {
	Seq     int32     `json:"seq"`
	Reason  string    `json:"reason"`
	Before  chainSpan `json:"before"`
	After   chainSpan `json:"after"`
	Version int32     `json:"version"`
}

// chainBlock は解けない理由（9.17.2 の block）。
type chainBlock struct {
	Code    string `json:"code"`
	Seq     int32  `json:"seq"`
	ShortMs int64  `json:"short_ms"`
	Message string `json:"message"`
}

// 変更の理由（9.17.2 の changes[].reason）と、解けない理由の code（9.14）。
const (
	chainReasonTarget = "target"
	chainReasonRipple = "ripple"
	chainReasonShrink = "shrink"

	chainCannotFit        = "cannot_fit"
	chainOverlapsPrevious = "overlaps_previous"
)

// chainMaxShrinkRounds は on_block=shrink の繰り返しの上限（保険）。1回ごとに必ずどれかの行が
// 縮むか打ち切るので、行の数と長さで有限に終わるが、計算が返らなくなる事故を避ける。
const chainMaxShrinkRounds = 10000

// planChain は連鎖を計算する（9.17.1）。
//
// 戻り値の changes は**列の順**（9.17.2）。解けないときは block が入り、changes は詰まった
// 相手の手前までの計算結果（試算の影に使える）になる。
//
// **列の順は動かす前の並びで決め、連鎖で入れ替えない。** 「T より後ろの列」はいまの並びで
// T より後ろにあるものであり、T をバーごと後ろへ動かしても、後ろにあったものは T の後ろに残る
// （押される）。並びを変えたいときは人が個別に動かす。
func planChain(p chainPlan) ([]chainChange, *chainBlock) {
	target := chainChange{
		Seq: p.Target.Seq, Reason: chainReasonTarget,
		Before: p.Target.Span, After: p.NewSpan, Version: p.Target.Version,
	}
	var head []chainChange
	if target.Before != target.After {
		head = []chainChange{target}
	}
	if !p.InColumn {
		return orEmptyChanges(head), nil
	}

	order := slices.Clone(p.Column)
	sortChainColumn(order)
	ti := slices.IndexFunc(order, func(it chainItem) bool { return it.Seq == p.Target.Seq })
	if ti < 0 {
		// 呼び出し側の誤り（InColumn なのに列に居ない）。自分だけを動かす扱いに倒す。
		return orEmptyChanges(head), nil
	}

	// 前の列は動かさない。きっかけの開始を前へ動かして前の列に重なれば拒む（9.14 overlaps_previous）
	if p.NewSpan.Start < p.Target.Span.Start {
		for i := ti - 1; i >= 0; i-- {
			prev := order[i]
			if prev.Span.Due > p.NewSpan.Start {
				short := prev.Span.Due - p.NewSpan.Start
				return orEmptyChanges(head), &chainBlock{
					Code: chainOverlapsPrevious, Seq: prev.Seq, ShortMs: short,
					Message: fmt.Sprintf("前の「%s」（%s まで）に %s 重なります。前のチケットは動かしません",
						prev.Title, chainClock(prev.Span.Due, p.Loc), chainDuration(short)),
				}
			}
		}
	}

	lenOverride := map[int32]int64{}
	var firstBlocker int32
	for round := 0; ; round++ {
		changes, block, blockIdx := chainRipple(p, order, ti, lenOverride)
		if block == nil || !p.Shrink || round >= chainMaxShrinkRounds {
			return append(slices.Clone(head), changes...), block
		}
		// on_block=shrink（9.17.1）。**詰まった相手の直前から T の直後へ向かって順に**、両端とも
		// 固定でない列を縮める。足りる分だけ縮め、計算し直す。別の相手で詰まれば打ち切る。
		if firstBlocker == 0 {
			firstBlocker = block.Seq
		} else if block.Seq != firstBlocker {
			return append(slices.Clone(head), changes...), block
		}
		shrunk := false
		for j := blockIdx - 1; j > ti; j-- {
			x := order[j]
			if x.startFixed() || x.dueFixed() {
				continue
			}
			cur := x.Span.Due - x.Span.Start
			if v, ok := lenOverride[x.Seq]; ok {
				cur = v
			}
			room := cur - p.MinLen
			if room <= 0 {
				continue
			}
			lenOverride[x.Seq] = cur - min(room, block.ShortMs)
			shrunk = true
			break
		}
		if !shrunk {
			return append(slices.Clone(head), changes...), block
		}
	}
}

// chainRipple は T より後ろの列を1件ずつ決める（9.17.1「後ろの列の決め方」）。
// 解けなければ block と、詰まった相手の order 上の位置を返す。
func chainRipple(p chainPlan, order []chainItem, ti int, lenOverride map[int32]int64) ([]chainChange, *chainBlock, int) {
	preds := map[int32][]chainFS{}
	for _, l := range p.Links {
		preds[l.Succ] = append(preds[l.Succ], l)
	}
	inColumn := map[int32]int64{}
	for _, it := range order {
		inColumn[it.Seq] = it.Span.Due
	}
	moved := map[int32]int64{p.Target.Seq: p.NewSpan.Due}
	dueOf := func(seq int32) (int64, bool) {
		if d, ok := moved[seq]; ok {
			return d, true
		}
		if d, ok := inColumn[seq]; ok {
			return d, true
		}
		d, ok := p.OtherDue[seq]
		return d, ok
	}

	delta := p.NewSpan.Due - p.Target.Span.Due
	prevEnd := p.NewSpan.Due
	changes := []chainChange{}
	for j := ti + 1; j < len(order); j++ {
		x := order[j]
		length := x.Span.Due - x.Span.Start
		override, overridden := lenOverride[x.Seq]
		if overridden {
			length = override
		}

		// 1. 望む開始 w。原則はいまの開始。FS の先行があれば先行の（動いた後の）終了＋lag の最大
		w := x.Span.Start
		hasFS := false
		for _, l := range preds[x.Seq] {
			d, ok := dueOf(l.Pred)
			if !ok || l.Pred == x.Seq {
				continue
			}
			v := chainAddDays(d, l.LagDays, p.Loc)
			if !hasFS || v > w {
				w = v
			}
			hasFS = true
		}
		if !hasFS && p.Compact {
			w = x.Span.Start + delta
		}
		// 2. 前の終わりに重なれば押す、隙間があれば吸収する
		s := max(w, prevEnd)
		// 3. 未着手のものを「いま」より前に置かない
		if !x.Started && s < x.Span.Start {
			s = min(x.Span.Start, max(s, p.Now))
		}

		next := x.Span
		reason := chainReasonRipple
		switch {
		case x.startFixed():
			// 4a. 開始が固定。押されるなら解けない。そうでなければ動かない
			if s > x.Span.Start {
				short := s - x.Span.Start
				return changes, &chainBlock{
					Code: chainCannotFit, Seq: x.Seq, ShortMs: short,
					Message: chainStartFixedMessage(x, short, p.Loc),
				}, j
			}
		case x.dueFixed():
			// 4b. 終了が固定。押されたら開始だけが動いて縮む。前へは寄らない（FS の後続は伸びる）
			if s > x.Span.Start {
				if x.Span.Due-s < p.MinLen {
					short := p.MinLen - (x.Span.Due - s)
					return changes, &chainBlock{
						Code: chainCannotFit, Seq: x.Seq, ShortMs: short,
						Message: fmt.Sprintf("「%s」の終了のアンカー（%s）までに最小の長さ（%s）が取れず、%s 足りません",
							x.Title, chainClock(x.Span.Due, p.Loc), chainDuration(p.MinLen), chainDuration(short)),
					}, j
				}
				next = chainSpan{Start: s, Due: x.Span.Due}
				reason = chainReasonShrink
			} else if s < x.Span.Start && hasFS {
				next = chainSpan{Start: s, Due: x.Span.Due}
			}
		default:
			// 4c. 長さを保って s から置く（on_block=shrink で縮めた行は縮めた長さで）
			next = chainSpan{Start: s, Due: s + length}
			if overridden {
				reason = chainReasonShrink
			}
		}
		if next != x.Span {
			changes = append(changes, chainChange{
				Seq: x.Seq, Reason: reason, Before: x.Span, After: next, Version: x.Version,
			})
			moved[x.Seq] = next.Due
		}
		// 5. X の終了を「前の終わり」にして次へ
		prevEnd = next.Due
	}
	return changes, nil, -1
}

// sortChainColumn は列を開始の昇順に並べる（同じなら終了の昇順、次に seq。9.17.1）。
func sortChainColumn(items []chainItem) {
	slices.SortStableFunc(items, func(a, b chainItem) int {
		switch {
		case a.Span.Start != b.Span.Start:
			return cmpInt64(a.Span.Start, b.Span.Start)
		case a.Span.Due != b.Span.Due:
			return cmpInt64(a.Span.Due, b.Span.Due)
		default:
			return int(a.Seq) - int(b.Seq)
		}
	})
}

func cmpInt64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func orEmptyChanges(c []chainChange) []chainChange {
	if c == nil {
		return []chainChange{}
	}
	return c
}

// chainAddDays は lag_days（暦日）を基準タイムゾーンの暦で足す（9.17.1 の手順1）。
func chainAddDays(ms int64, days int32, loc *time.Location) int64 {
	if days == 0 {
		return ms
	}
	if loc == nil {
		loc = time.UTC
	}
	return time.UnixMilli(ms).In(loc).AddDate(0, 0, int(days)).UnixMilli()
}

func chainStartFixedMessage(x chainItem, short int64, loc *time.Location) string {
	at := chainClock(x.Span.Start, loc)
	switch {
	case x.StartPinned:
		return fmt.Sprintf("「%s」の開始のアンカー（%s）に %s 足りません", x.Title, at, chainDuration(short))
	case x.Done:
		return fmt.Sprintf("「%s」は完了しているため動かせず（%s 開始）、%s 足りません", x.Title, at, chainDuration(short))
	default:
		return fmt.Sprintf("「%s」は着手済みのため開始（%s）を動かせず、%s 足りません", x.Title, at, chainDuration(short))
	}
}

// chainClock は理由の文言に出す時刻（基準タイムゾーン）。秒が0でなければ秒まで出す。
func chainClock(ms int64, loc *time.Location) string {
	if loc == nil {
		loc = time.UTC
	}
	t := time.UnixMilli(ms).In(loc)
	if t.Second() != 0 {
		return t.Format("15:04:05")
	}
	return t.Format("15:04")
}

// chainDuration は足りない時間を「1時間5分」「30秒」の形にする。秒未満は切り上げる。
func chainDuration(ms int64) string {
	sec := (ms + 999) / 1000
	h, m, s := sec/3600, sec%3600/60, sec%60
	var b strings.Builder
	if h > 0 {
		fmt.Fprintf(&b, "%d時間", h)
	}
	if m > 0 {
		fmt.Fprintf(&b, "%d分", m)
	}
	if s > 0 || b.Len() == 0 {
		fmt.Fprintf(&b, "%d秒", s)
	}
	return b.String()
}

// ── きっかけの検証（9.17.2「きっかけの検証」）─────────────────────

// rescheduleOp は 9.17.2 の op。
const (
	rescheduleOpMove    = "move"
	rescheduleOpCompact = "compact"
)

// rescheduleTarget はきっかけの今の状態。端は値が無いことがある（nil）。
type rescheduleTarget struct {
	Start, Due  *int64
	AllDay      bool
	StartPinned bool
	DuePinned   bool
	Started     bool
	Done        bool
	ClosedAt    *int64
}

// checkRescheduleTarget はきっかけを検証し、新しい予定を返す（9.17.2）。dry_run でも 422 にする。
//
// **値の無い端のアンカーは無いものとして扱う**（DbDesign.md 6.6.2）。動かす端とは、値が変わる端である
// （同じ値を送り直しただけでは動かさない）。
func checkRescheduleTarget(t rescheduleTarget, op string, reqStart, reqDue *int64, minLen int64) (chainSpan, []apierr.Detail) {
	var details []apierr.Detail
	add := func(field, code, msg string) {
		details = append(details, apierr.Detail{Field: field, Code: code, Message: msg})
	}
	if t.AllDay {
		add("all_day", "not_timed", "終日のチケットは連鎖でずらせません。時刻付きにしてください")
		return chainSpan{}, details
	}

	switch op {
	case rescheduleOpCompact:
		if t.Start == nil || t.Due == nil {
			add("start_at", "not_timed", "開始と終了の両方がそろっていないチケットは連鎖でずらせません")
			return chainSpan{}, details
		}
		if !t.Done || t.ClosedAt == nil || *t.Due-*t.ClosedAt < minLen {
			add("op", "nothing_to_compact",
				fmt.Sprintf("完了していないか、予定より %s 以上早く終わっていないため、詰められません", chainDuration(minLen)))
			return chainSpan{}, details
		}
		next := chainSpan{Start: *t.Start, Due: ceilTo(*t.ClosedAt, minLen)}
		if t.DuePinned {
			add("due_at", "pinned", "終了にアンカーが付いているため、詰められません")
		}
		if next.Due-next.Start < minLen {
			add("due_at", "too_short", fmt.Sprintf("長さが最小の長さ（%s）を割ります", chainDuration(minLen)))
		}
		return next, details
	default:
		next := chainSpan{}
		start, due := t.Start, t.Due
		if reqStart != nil {
			start = reqStart
		}
		if reqDue != nil {
			due = reqDue
		}
		if start == nil || due == nil {
			add("start_at", "not_timed", "変えた後に開始と終了の両方がそろわないため、連鎖でずらせません")
			return chainSpan{}, details
		}
		next = chainSpan{Start: *start, Due: *due}
		startMoves := t.Start == nil || *t.Start != next.Start
		dueMoves := t.Due == nil || *t.Due != next.Due
		if t.Done && (startMoves || dueMoves) {
			add("op", "closed", "完了したチケットの予定は動かせません。早く終わった分は「ここで詰める」を使ってください")
		}
		if startMoves && t.StartPinned && t.Start != nil {
			add("start_at", "pinned", "開始にアンカーが付いているため、開始は動かせません")
		}
		if dueMoves && t.DuePinned && t.Due != nil {
			add("due_at", "pinned", "終了にアンカーが付いているため、終了は動かせません")
		}
		if startMoves && t.Started && !t.Done {
			add("start_at", "started", "着手済みのチケットの開始は動かせません")
		}
		if next.Due-next.Start < minLen {
			add("due_at", "too_short", fmt.Sprintf("長さが最小の長さ（%s）を割ります", chainDuration(minLen)))
		}
		return next, details
	}
}

// ceilTo は ms を unit の倍数へ切り上げる（[ここで詰める] の丸め。9.17.2）。
// 完了時刻より前へ丸めると「終わる前に終わった」予定になるので、切り上げる。
func ceilTo(ms, unit int64) int64 {
	if unit <= 0 {
		return ms
	}
	r := ms % unit
	if r == 0 {
		return ms
	}
	if r < 0 {
		return ms - r
	}
	return ms + unit - r
}
