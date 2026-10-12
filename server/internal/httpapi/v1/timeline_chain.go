// 連鎖のずらしの計算（ApiDesign.md 9.17.1。pb-296・pb-332）。
//
// **DB にも HTTP にも触れない純粋な関数に切り出してある。** 規則の組み合わせ（隙間の吸収・依存・
// アンカー・着手済み・完了・「いま」・縮める・並びと依存の輪）が多く、結合テストでは場面を網羅できないため、
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

// chainLink は依存（FS / SS / FF / SF。9.17.1）。先行 Pred の端 ＋ ずらし → 後続 Succ の端。
// 種別の1文字目が先行の端、2文字目が後続の端（S＝開始、F＝終了）。
type chainLink struct {
	Pred, Succ int32
	Type       string
	LagDays    int32
	LagSeconds int32
}

// chainEnds は動かせないチケット（終日・片端だけ）の端。依存の先行としてだけ読む（9.17.1「動かせる」）。
type chainEnds struct {
	Start, Due *int64
}

// chainPlan は計算の入力。
type chainPlan struct {
	// Columns は行ごとの列（時間重複チェック付き・時刻付き・両端あり）。きっかけが列に入るなら含める。
	Columns [][]chainItem
	// Others は列の外の、動かせるチケット（依存で結ばれたもの。pb-332）。
	Others []chainItem
	// Ends は動かせないチケットの端。依存の先行として読む。
	Ends   map[int32]chainEnds
	Target chainItem
	// NewSpan はきっかけの新しい予定（checkRescheduleTarget が検証済みのもの）。
	NewSpan chainSpan
	// Compact は [ここで詰める]（op=compact）。依存の先行の無い後続を同じだけ前へ寄せる（9.17.1 の手順1）。
	Compact bool
	Links   []chainLink
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
	chainOrderCycle       = "order_cycle"
)

// chainMaxShrinkRounds は on_block=shrink の繰り返しの上限（保険）。1回ごとに必ずどれかの行が
// 縮むか打ち切るので、行の数と長さで有限に終わるが、計算が返らなくなる事故を避ける。
const chainMaxShrinkRounds = 10000

// chainGraph は列の並びと依存を、seq で引ける形にしたもの。
type chainGraph struct {
	// items は動かせるチケット（列・列の外・きっかけ）。
	items map[int32]chainItem
	// colPrev / colNext は列で1つ前・1つ後ろ（動かす前の並び）。
	colPrev, colNext map[int32]int32
	preds, succs     map[int32][]chainLink
}

func buildChainGraph(p chainPlan) chainGraph {
	g := chainGraph{
		items:   map[int32]chainItem{},
		colPrev: map[int32]int32{}, colNext: map[int32]int32{},
		preds: map[int32][]chainLink{}, succs: map[int32][]chainLink{},
	}
	for _, it := range p.Others {
		g.items[it.Seq] = it
	}
	for _, col := range p.Columns {
		order := slices.Clone(col)
		sortChainColumn(order)
		for i, it := range order {
			g.items[it.Seq] = it
			if i > 0 {
				g.colPrev[it.Seq] = order[i-1].Seq
				g.colNext[order[i-1].Seq] = it.Seq
			}
		}
	}
	g.items[p.Target.Seq] = p.Target
	for _, l := range p.Links {
		if l.Pred == l.Succ {
			continue
		}
		g.preds[l.Succ] = append(g.preds[l.Succ], l)
		g.succs[l.Pred] = append(g.succs[l.Pred], l)
	}
	return g
}

// after は x の直後に決めるべきもの（列で1つ後ろと、依存の後続）のうち、in に入るもの。
func (g chainGraph) after(x int32, in map[int32]bool) []int32 {
	var out []int32
	if n, ok := g.colNext[x]; ok && in[n] {
		out = append(out, n)
	}
	for _, l := range g.succs[x] {
		if in[l.Succ] {
			out = append(out, l.Succ)
		}
	}
	return out
}

// before は x より先に決めるべきもの（列で1つ前と、依存の先行）のうち、in に入るもの。
func (g chainGraph) before(x int32, in map[int32]bool) []int32 {
	var out []int32
	if pr, ok := g.colPrev[x]; ok && in[pr] {
		out = append(out, pr)
	}
	for _, l := range g.preds[x] {
		if in[l.Pred] {
			out = append(out, l.Pred)
		}
	}
	return out
}

// reach はきっかけから列の並び（1つ後ろ）と依存（後続）をたどって届く、動かせるチケット（9.17.1）。
func (g chainGraph) reach(from int32) map[int32]bool {
	movable := func(seq int32) bool { _, ok := g.items[seq]; return ok }
	all := map[int32]bool{}
	for seq := range g.items {
		all[seq] = true
	}
	seen := map[int32]bool{from: true}
	stack := []int32{from}
	for len(stack) > 0 {
		x := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, n := range g.after(x, all) {
			if movable(n) && !seen[n] {
				seen[n] = true
				stack = append(stack, n)
			}
		}
	}
	return seen
}

// order は届いたチケットを、列の並びと依存の両方を守る順に並べる（9.17.1）。先頭はきっかけ。
// 同じ順に並べられるものは、いまの開始の昇順、次に seq。並べられなければ、輪に掛かった
// 2件（先に決めるべきもの, 後に決めるべきもの）と false を返す。
func (g chainGraph) order(reached map[int32]bool, root int32) ([]int32, [2]int32, bool) {
	indeg := map[int32]int{}
	for x := range reached {
		indeg[x] += 0
		for _, y := range g.after(x, reached) {
			indeg[y]++
		}
	}
	less := func(a, b int32) bool {
		if a == root || b == root {
			return a == root
		}
		sa, sb := g.items[a].Span.Start, g.items[b].Span.Start
		if sa != sb {
			return sa < sb
		}
		return a < b
	}
	var ready []int32
	for x, d := range indeg {
		if d == 0 {
			ready = append(ready, x)
		}
	}
	out := make([]int32, 0, len(reached))
	for len(ready) > 0 {
		bi := 0
		for i := range ready {
			if less(ready[i], ready[bi]) {
				bi = i
			}
		}
		x := ready[bi]
		ready = slices.Delete(ready, bi, bi+1)
		out = append(out, x)
		for _, y := range g.after(x, reached) {
			indeg[y]--
			if indeg[y] == 0 {
				ready = append(ready, y)
			}
		}
	}
	if len(out) == len(reached) {
		return out, [2]int32{}, true
	}
	// 残ったもの（輪の上か、その後ろ）から、残ったものに先を求められている1件を選ぶ
	done := map[int32]bool{}
	for _, x := range out {
		done[x] = true
	}
	left := map[int32]bool{}
	var rest []int32
	for x := range reached {
		if !done[x] {
			left[x] = true
			rest = append(rest, x)
		}
	}
	slices.Sort(rest)
	for _, x := range rest {
		if ps := g.before(x, left); len(ps) > 0 {
			return nil, [2]int32{ps[0], x}, false
		}
	}
	return nil, [2]int32{rest[0], rest[0]}, false
}

// ancestors は in の中で、x へたどり着けるもの（x を含まない）。on_block=shrink が縮める候補。
func (g chainGraph) ancestors(x int32, in map[int32]bool) map[int32]bool {
	seen := map[int32]bool{}
	stack := []int32{x}
	for len(stack) > 0 {
		y := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, p := range g.before(y, in) {
			if !seen[p] {
				seen[p] = true
				stack = append(stack, p)
			}
		}
	}
	return seen
}

// planChain は連鎖を計算する（9.17.1）。
//
// 戻り値の changes は**決めた順**（きっかけが先頭。9.17.2）。解けないときは block が入り、changes は
// 詰まった相手の手前までの計算結果（試算の影に使える）になる。
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
	g := buildChainGraph(p)

	// 前の列は動かさない。きっかけの開始を前へ動かして前の列に重なれば拒む（9.14 overlaps_previous）
	if p.NewSpan.Start < p.Target.Span.Start {
		for pr, ok := g.colPrev[p.Target.Seq]; ok; pr, ok = g.colPrev[pr] {
			prev := g.items[pr]
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

	reached := g.reach(p.Target.Seq)
	order, cyc, ok := g.order(reached, p.Target.Seq)
	if !ok {
		a, b := g.items[cyc[0]], g.items[cyc[1]]
		return orEmptyChanges(head), &chainBlock{
			Code: chainOrderCycle, Seq: b.Seq,
			Message: fmt.Sprintf("「%s」と「%s」の並びと依存が輪になるため、ずらせません", a.Title, b.Title),
		}
	}

	lenOverride := map[int32]int64{}
	var firstBlocker int32
	for round := 0; ; round++ {
		changes, block, blockPos := chainRipple(p, g, order, lenOverride)
		if block == nil || !p.Shrink || round >= chainMaxShrinkRounds {
			return append(slices.Clone(head), changes...), block
		}
		// on_block=shrink（9.17.1）。**詰まった相手へ届く道の上を、相手に近いものから順に**、両端とも
		// 固定でないものを縮める。足りる分だけ縮め、計算し直す。別の相手で詰まれば打ち切る。
		if firstBlocker == 0 {
			firstBlocker = block.Seq
		} else if block.Seq != firstBlocker {
			return append(slices.Clone(head), changes...), block
		}
		anc := g.ancestors(block.Seq, reached)
		shrunk := false
		for j := blockPos - 1; j >= 1; j-- {
			x := g.items[order[j]]
			if !anc[x.Seq] || x.startFixed() || x.dueFixed() {
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

// chainRipple はきっかけの後ろを、決めた順に1件ずつ決める（9.17.1「届いたチケット X の決め方」）。
// 解けなければ block と、詰まった相手の order 上の位置を返す。
func chainRipple(p chainPlan, g chainGraph, order []int32, lenOverride map[int32]int64) ([]chainChange, *chainBlock, int) {
	moved := map[int32]chainSpan{p.Target.Seq: p.NewSpan}
	endOf := func(seq int32, due bool) (int64, bool) {
		if s, ok := moved[seq]; ok {
			return chainEnd(s, due), true
		}
		if it, ok := g.items[seq]; ok {
			return chainEnd(it.Span, due), true
		}
		e, ok := p.Ends[seq]
		switch {
		case !ok:
			return 0, false
		case due && e.Due != nil:
			return *e.Due, true
		case !due && e.Start != nil:
			return *e.Start, true
		}
		return 0, false
	}

	delta := p.NewSpan.Due - p.Target.Span.Due
	changes := []chainChange{}
	for pos := 1; pos < len(order); pos++ {
		x := g.items[order[pos]]
		length := x.Span.Due - x.Span.Start
		override, overridden := lenOverride[x.Seq]
		if overridden {
			length = override
		}

		// 1. 依存が求める端。FS / SS は開始を、FF / SF は終了を求める（隙間を空けない）
		var startReqs, dueReqs []int64
		for _, l := range g.preds[x.Seq] {
			end, ok := endOf(l.Pred, l.Type == "FS" || l.Type == "FF")
			if !ok {
				continue
			}
			v := chainAddLag(end, l.LagDays, l.LagSeconds, p.Loc)
			switch l.Type {
			case "FS", "SS":
				startReqs = append(startReqs, v)
			case "FF", "SF":
				dueReqs = append(dueReqs, v)
			}
		}
		hasStartReq := len(startReqs) > 0
		// 望む開始 w。開始の依存だけで決めるもの（固定の端があるとき）と、終了の依存も長さで開始へ直したもの
		wStart := x.Span.Start
		switch {
		case hasStartReq:
			wStart = slices.Max(startReqs)
		case len(dueReqs) == 0 && p.Compact:
			wStart = x.Span.Start + delta
		}
		wAll := wStart
		if len(dueReqs) > 0 {
			v := slices.Max(dueReqs) - length
			if hasStartReq {
				wAll = max(wAll, v)
			} else {
				wAll = v
			}
		}
		// 2. 列で前のものの終わりに重なれば押す、隙間があれば吸収する。3. 未着手を「いま」より前に置かない
		prevDue, hasPrev := int64(0), false
		if pr, ok := g.colPrev[x.Seq]; ok {
			prevDue, hasPrev = endOf(pr, true)
		}
		place := func(w int64) int64 {
			s := w
			if hasPrev {
				s = max(s, prevDue)
			}
			if !x.Started && s < x.Span.Start {
				s = min(x.Span.Start, max(s, p.Now))
			}
			return s
		}

		next := x.Span
		reason := chainReasonRipple
		switch {
		case x.startFixed():
			// 4a. 開始が固定。押されるなら解けない。終了の依存があれば終了だけを動かす
			if s := place(wStart); s > x.Span.Start {
				short := s - x.Span.Start
				return changes, &chainBlock{
					Code: chainCannotFit, Seq: x.Seq, ShortMs: short,
					Message: chainStartFixedMessage(x, short, p.Loc),
				}, pos
			}
			if len(dueReqs) > 0 && !x.dueFixed() {
				d := slices.Max(dueReqs)
				if d-x.Span.Start < p.MinLen {
					short := p.MinLen - (d - x.Span.Start)
					return changes, &chainBlock{
						Code: chainCannotFit, Seq: x.Seq, ShortMs: short,
						Message: fmt.Sprintf("「%s」は開始を動かせないため、依存がそろえる終了（%s）までに最小の長さ（%s）が取れず、%s 足りません",
							x.Title, chainClock(d, p.Loc), chainDuration(p.MinLen), chainDuration(short)),
					}, pos
				}
				next = chainSpan{Start: x.Span.Start, Due: d}
			}
		case x.dueFixed():
			// 4b. 終了が固定。終了の依存が後ろを求めれば解けない。押されたら開始だけが動いて縮む。
			// 前へは寄らない（開始の依存の後続は伸びる）
			if len(dueReqs) > 0 {
				if d := slices.Max(dueReqs); d > x.Span.Due {
					short := d - x.Span.Due
					return changes, &chainBlock{
						Code: chainCannotFit, Seq: x.Seq, ShortMs: short,
						Message: fmt.Sprintf("「%s」の終了（%s）は動かせず、依存がそろえる終了に %s 足りません",
							x.Title, chainClock(x.Span.Due, p.Loc), chainDuration(short)),
					}, pos
				}
			}
			s := place(wStart)
			if s > x.Span.Start {
				if x.Span.Due-s < p.MinLen {
					short := p.MinLen - (x.Span.Due - s)
					return changes, &chainBlock{
						Code: chainCannotFit, Seq: x.Seq, ShortMs: short,
						Message: fmt.Sprintf("「%s」の終了のアンカー（%s）までに最小の長さ（%s）が取れず、%s 足りません",
							x.Title, chainClock(x.Span.Due, p.Loc), chainDuration(p.MinLen), chainDuration(short)),
					}, pos
				}
				next = chainSpan{Start: s, Due: x.Span.Due}
				reason = chainReasonShrink
			} else if s < x.Span.Start && hasStartReq {
				next = chainSpan{Start: s, Due: x.Span.Due}
			}
		default:
			// 4c. 長さを保って s から置く（on_block=shrink で縮めた行は縮めた長さで）
			s := place(wAll)
			next = chainSpan{Start: s, Due: s + length}
			if overridden {
				reason = chainReasonShrink
			}
		}
		if next != x.Span {
			changes = append(changes, chainChange{
				Seq: x.Seq, Reason: reason, Before: x.Span, After: next, Version: x.Version,
			})
			moved[x.Seq] = next
		}
	}
	return changes, nil, -1
}

// chainEnd は区間の開始か終了。
func chainEnd(s chainSpan, due bool) int64 {
	if due {
		return s.Due
	}
	return s.Start
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

// chainAddLag は依存のずらしを足す。暦日（lag_days）を足してから秒（lag_seconds）を足す（DbDesign.md 6.6.3）。
func chainAddLag(ms int64, days, seconds int32, loc *time.Location) int64 {
	return chainAddDays(ms, days, loc) + int64(seconds)*1000
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
