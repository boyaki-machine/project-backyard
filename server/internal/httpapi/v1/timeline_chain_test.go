package v1

import (
	"slices"
	"strings"
	"testing"
	"time"
)

// 連鎖のずらしの規則（ApiDesign.md 9.17.1）の単体テスト。時刻は 2026-10-24（JST）の「時:分」で書く。

var chainTestLoc = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		panic(err)
	}
	return loc
}()

// hm は 2026-10-24 の JST の時:分をエポックミリ秒にする。
func hm(h, m int) int64 {
	return time.Date(2026, 10, 24, h, m, 0, 0, chainTestLoc).UnixMilli()
}

const minute = int64(60 * 1000)

func item(seq int32, title string, sh, sm, eh, em int) chainItem {
	return chainItem{Seq: seq, Title: title, Span: chainSpan{Start: hm(sh, sm), Due: hm(eh, em)}, Version: seq}
}

// basePlan は列とずらすきっかけから計画を作る。いまは 8:00（何も「いま」に掛からない）。
func basePlan(column []chainItem, targetSeq int32, newStart, newDue int64) chainPlan {
	var target chainItem
	for _, it := range column {
		if it.Seq == targetSeq {
			target = it
		}
	}
	return chainPlan{
		Columns: [][]chainItem{column}, Target: target,
		NewSpan: chainSpan{Start: newStart, Due: newDue},
		Ends:    map[int32]chainEnds{}, MinLen: minute, Now: hm(8, 0), Loc: chainTestLoc,
	}
}

func spanOf(changes []chainChange, seq int32) (chainSpan, string, bool) {
	for _, c := range changes {
		if c.Seq == seq {
			return c.After, c.Reason, true
		}
	}
	return chainSpan{}, "", false
}

func wantSpan(t *testing.T, changes []chainChange, seq int32, start, due int64, reason string) {
	t.Helper()
	got, gotReason, ok := spanOf(changes, seq)
	if !ok {
		t.Fatalf("%d が動いていない: %+v", seq, changes)
	}
	if got.Start != start || got.Due != due || gotReason != reason {
		t.Fatalf("%d = %s–%s（%s）, want %s–%s（%s）", seq,
			fmtHM(got.Start), fmtHM(got.Due), gotReason, fmtHM(start), fmtHM(due), reason)
	}
}

func wantUnmoved(t *testing.T, changes []chainChange, seq int32) {
	t.Helper()
	if _, _, ok := spanOf(changes, seq); ok {
		t.Fatalf("%d は動かないはず: %+v", seq, changes)
	}
}

func fmtHM(ms int64) string { return time.UnixMilli(ms).In(chainTestLoc).Format("15:04") }

// 例1（Timeline.md 5.4 を 7.1 で改めたもの）：延ばすと、後ろの隙間を吸収し、足りない分だけ押す。
func TestPlanChainExtendAbsorbsGapThenPushes(t *testing.T) {
	col := []chainItem{
		item(1, "吹奏楽部「秋の調べ」", 10, 0, 10, 30),
		item(2, "ダンス部「Re:Start」", 10, 40, 11, 10),
		item(3, "演劇部「夏の夜の夢」", 11, 10, 11, 40),
	}
	changes, block := planChain(basePlan(col, 1, hm(10, 0), hm(10, 45)))
	if block != nil {
		t.Fatalf("解けるはず: %+v", block)
	}
	wantSpan(t, changes, 1, hm(10, 0), hm(10, 45), chainReasonTarget)
	// 隙間は保たない：B は A の終わり（10:45）から、C は B に続く
	wantSpan(t, changes, 2, hm(10, 45), hm(11, 15), chainReasonRipple)
	wantSpan(t, changes, 3, hm(11, 15), hm(11, 45), chainReasonRipple)
	if changes[0].Seq != 1 || changes[1].Seq != 2 || changes[2].Seq != 3 {
		t.Fatalf("並びは列の順: %+v", changes)
	}
}

// 延ばしても隙間に収まれば後ろは動かない。
func TestPlanChainExtendWithinGap(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 40, 11, 10)}
	changes, block := planChain(basePlan(col, 1, hm(10, 0), hm(10, 35)))
	if block != nil {
		t.Fatal(block)
	}
	wantUnmoved(t, changes, 2)
	if len(changes) != 1 {
		t.Fatalf("きっかけだけ動く: %+v", changes)
	}
}

// 縮めた（早く終わった）ときは後ろを動かさず、隙間が空く。
func TestPlanChainShrinkLeavesGap(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0)}
	changes, block := planChain(basePlan(col, 1, hm(10, 0), hm(10, 20)))
	if block != nil {
		t.Fatal(block)
	}
	wantUnmoved(t, changes, 2)
}

// FS の後続は先行の終了に隙間なく寄る。縮めば前へ（長さを保つ）、延びれば押される。
func TestPlanChainFSSuccessorFollows(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0), item(3, "C", 11, 0, 11, 30)}
	p := basePlan(col, 1, hm(10, 0), hm(10, 20))
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 2}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 2, hm(10, 20), hm(10, 50), chainReasonRipple)
	// C には FS が無いので、隙間が空いても動かない
	wantUnmoved(t, changes, 3)

	p = basePlan(col, 1, hm(10, 0), hm(10, 40))
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 2}}
	changes, _ = planChain(p)
	wantSpan(t, changes, 2, hm(10, 40), hm(11, 10), chainReasonRipple)
	wantSpan(t, changes, 3, hm(11, 10), hm(11, 40), chainReasonRipple)
}

// FS の後続の終了にアンカーがあれば、先行が縮んだとき開始が前へ寄って期間が伸びる。
func TestPlanChainFSSuccessorWithDuePinStretches(t *testing.T) {
	b := item(2, "B", 10, 30, 11, 0)
	b.DuePinned = true
	col := []chainItem{item(1, "A", 10, 0, 10, 30), b}
	p := basePlan(col, 1, hm(10, 0), hm(10, 15))
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 2}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 2, hm(10, 15), hm(11, 0), chainReasonRipple)
}

// FS の lag_days は暦日で足す。
func TestPlanChainFSLagDays(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0)}
	p := basePlan(col, 1, hm(10, 0), hm(10, 30))
	p.NewSpan = chainSpan{Start: hm(9, 50), Due: hm(10, 30)}
	p.Target.StartPinned = false
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 2, LagDays: 1}}
	changes, _ := planChain(p)
	next := time.Date(2026, 10, 25, 10, 30, 0, 0, chainTestLoc).UnixMilli()
	wantSpan(t, changes, 2, next, next+30*minute, chainReasonRipple)
}

// 例3a：開始のアンカーの手前の隙間で収まる。
func TestPlanChainStartAnchorAbsorbedByGap(t *testing.T) {
	c := item(3, "閉会式・表彰", 11, 30, 12, 0)
	c.StartPinned = true
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 40, 11, 10), c}
	changes, block := planChain(basePlan(col, 1, hm(10, 0), hm(11, 0)))
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 2, hm(11, 0), hm(11, 30), chainReasonRipple)
	wantUnmoved(t, changes, 3)
}

// 例3b：開始のアンカーに足りない → 拒む（cannot_fit・相手と足りない時間・文言）と、縮める案。
func TestPlanChainStartAnchorCannotFitAndShrink(t *testing.T) {
	c := item(3, "閉会式・表彰", 11, 30, 12, 0)
	c.StartPinned = true
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 40, 11, 10), c}
	p := basePlan(col, 1, hm(10, 0), hm(11, 15))
	p.MinLen = 10 * minute
	changes, block := planChain(p)
	if block == nil || block.Code != chainCannotFit || block.Seq != 3 || block.ShortMs != 15*minute {
		t.Fatalf("block = %+v", block)
	}
	if !strings.Contains(block.Message, "「閉会式・表彰」の開始のアンカー（11:30）に 15分 足りません") {
		t.Fatalf("message = %q", block.Message)
	}
	// 詰まった相手の手前までの計算は返す（試算の影）
	wantSpan(t, changes, 1, hm(10, 0), hm(11, 15), chainReasonTarget)

	p.Shrink = true
	changes, block = planChain(p)
	if block != nil {
		t.Fatalf("縮めれば解けるはず: %+v", block)
	}
	// B を 15分に縮める（最小 10分までは縮めない。足りる分だけ）
	wantSpan(t, changes, 2, hm(11, 15), hm(11, 30), chainReasonShrink)
	wantUnmoved(t, changes, 3)
}

// 縮める余地が尽きれば shrink でも解けない。
func TestPlanChainShrinkExhausted(t *testing.T) {
	c := item(3, "C", 11, 30, 12, 0)
	c.StartPinned = true
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 40, 11, 10), c}
	p := basePlan(col, 1, hm(10, 0), hm(11, 25))
	p.MinLen = 10 * minute
	p.Shrink = true
	_, block := planChain(p)
	if block == nil || block.Seq != 3 || block.ShortMs != 5*minute {
		t.Fatalf("B を 10分まで縮めても 5分足りない: %+v", block)
	}
}

// 縮めるのは詰まった相手の直前から順に。両端とも固定でないものだけ。
func TestPlanChainShrinkOrderFromBlocker(t *testing.T) {
	d := item(4, "D", 11, 30, 12, 0)
	d.StartPinned = true
	b := item(2, "B", 10, 30, 10, 50)
	c := item(3, "C", 10, 50, 11, 30)
	c.DuePinned = true // 終了が固定なので縮める対象にしない（終了のアンカーで縮むことはある）
	col := []chainItem{item(1, "A", 10, 0, 10, 30), b, c, d}
	p := basePlan(col, 1, hm(10, 0), hm(10, 40))
	p.MinLen = 10 * minute
	p.Shrink = true
	changes, block := planChain(p)
	if block != nil {
		t.Fatalf("C が終了のアンカーで縮んで収まる: %+v", block)
	}
	wantSpan(t, changes, 2, hm(10, 40), hm(11, 0), chainReasonRipple)
	wantSpan(t, changes, 3, hm(11, 0), hm(11, 30), chainReasonShrink)
}

// 例4：終了のアンカーで縮み、後ろへ波及しない。最小の長さを割れば解けない。
func TestPlanChainDueAnchorShrinks(t *testing.T) {
	b := item(2, "体育館の返却", 13, 40, 14, 20)
	b.DuePinned = true
	col := []chainItem{item(1, "A", 13, 0, 13, 40), b, item(3, "C", 14, 30, 15, 0)}
	p := basePlan(col, 1, hm(13, 0), hm(13, 50))
	p.MinLen = 10 * minute
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 2, hm(13, 50), hm(14, 20), chainReasonShrink)
	wantUnmoved(t, changes, 3)

	p = basePlan(col, 1, hm(13, 0), hm(14, 15))
	p.MinLen = 10 * minute
	_, block = planChain(p)
	if block == nil || block.Code != chainCannotFit || block.Seq != 2 || block.ShortMs != 5*minute {
		t.Fatalf("block = %+v", block)
	}
	if !strings.Contains(block.Message, "終了のアンカー（14:20）") {
		t.Fatalf("message = %q", block.Message)
	}
}

// 着手済みは開始が動かない（暗黙のアンカー）。押されれば解けない。
func TestPlanChainStartedIsStartAnchor(t *testing.T) {
	b := item(2, "B", 10, 30, 11, 0)
	b.Started = true
	col := []chainItem{item(1, "A", 10, 0, 10, 30), b}
	_, block := planChain(basePlan(col, 1, hm(10, 0), hm(10, 40)))
	if block == nil || block.Seq != 2 || !strings.Contains(block.Message, "着手済み") {
		t.Fatalf("block = %+v", block)
	}
}

// 完了は両端とも動かない。寄せも押しもしない。
func TestPlanChainDoneNeverMoves(t *testing.T) {
	b := item(2, "B", 10, 40, 11, 0)
	b.Started, b.Done = true, true
	col := []chainItem{item(1, "A", 10, 0, 10, 30), b}
	p := basePlan(col, 1, hm(10, 0), hm(10, 10))
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 2}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantUnmoved(t, changes, 2)
	_, block = planChain(basePlan(col, 1, hm(10, 0), hm(10, 50)))
	if block == nil || !strings.Contains(block.Message, "完了") {
		t.Fatalf("block = %+v", block)
	}
}

// 時間重複チェックの無いもの・列の外のきっかけは自分だけが動く。
func TestPlanChainOutsideColumnMovesOnlyTarget(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0)}
	p := basePlan(col, 1, hm(10, 0), hm(10, 50))
	p.Others, p.Columns = p.Columns[0], nil
	changes, block := planChain(p)
	if block != nil || len(changes) != 1 || changes[0].Seq != 1 {
		t.Fatalf("自分だけ: %+v %+v", changes, block)
	}
}

// 「いま」より前に未着手のものを寄せない（例2の後半）。
func TestPlanChainNotBeforeNow(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 40, 11, 10), item(3, "C", 11, 10, 11, 40)}
	p := basePlan(col, 1, hm(10, 0), hm(10, 20))
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 2}}
	p.Now = hm(10, 35)
	changes, _ := planChain(p)
	wantSpan(t, changes, 2, hm(10, 35), hm(11, 5), chainReasonRipple)
	// C は FS が無いので動かない（隙間が空く）
	wantUnmoved(t, changes, 3)
}

// [ここで詰める]：後ろの列を同じだけ前へ寄せる。「いま」より前には寄せない。
func TestPlanChainCompact(t *testing.T) {
	a := item(1, "A", 10, 0, 10, 30)
	a.Started, a.Done = true, true
	col := []chainItem{a, item(2, "B", 10, 40, 11, 10), item(3, "C", 11, 20, 11, 50)}
	p := basePlan(col, 1, hm(10, 0), hm(10, 20))
	p.Compact = true
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 2, hm(10, 30), hm(11, 0), chainReasonRipple)
	wantSpan(t, changes, 3, hm(11, 10), hm(11, 40), chainReasonRipple)

	p.Now = hm(10, 35)
	changes, _ = planChain(p)
	wantSpan(t, changes, 2, hm(10, 35), hm(11, 5), chainReasonRipple)
}

// きっかけの開始を前へ動かして前の列に重なれば overlaps_previous。前の列は動かさない。
func TestPlanChainOverlapsPrevious(t *testing.T) {
	col := []chainItem{item(1, "吹奏楽部「秋の調べ」", 10, 0, 10, 30), item(2, "B", 10, 40, 11, 10)}
	changes, block := planChain(basePlan(col, 2, hm(10, 20), hm(11, 10)))
	if block == nil || block.Code != chainOverlapsPrevious || block.Seq != 1 || block.ShortMs != 10*minute {
		t.Fatalf("block = %+v", block)
	}
	if !strings.Contains(block.Message, "吹奏楽部「秋の調べ」") {
		t.Fatalf("message = %q", block.Message)
	}
	wantUnmoved(t, changes, 1)
}

// バーごと後ろへ動かしても、後ろにあったものは後ろに残る（列の順を入れ替えない）。
func TestPlanChainKeepsOrder(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0)}
	changes, _ := planChain(basePlan(col, 1, hm(11, 0), hm(11, 30)))
	wantSpan(t, changes, 2, hm(11, 30), hm(12, 0), chainReasonRipple)
}

// きっかけの前の列は、FS があっても動かさない。
func TestPlanChainPreviousUntouched(t *testing.T) {
	col := []chainItem{item(1, "A", 9, 0, 9, 30), item(2, "B", 10, 0, 10, 30), item(3, "C", 10, 30, 11, 0)}
	changes, _ := planChain(basePlan(col, 2, hm(10, 0), hm(10, 45)))
	wantUnmoved(t, changes, 1)
	wantSpan(t, changes, 3, hm(10, 45), hm(11, 15), chainReasonRipple)
}

// ── 依存の4種・行またぎ（pb-332）────────────────────────────

// twoRows は行1（A→B）と行2（X→Y）の2列。きっかけは A。
func twoRows() ([]chainItem, []chainItem) {
	return []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0)},
		[]chainItem{item(11, "X", 9, 0, 10, 0), item(12, "Y", 11, 0, 11, 30)}
}

// 4種それぞれが求める端に、隙間なくそろう。
func TestPlanChainLinkTypes(t *testing.T) {
	cases := []struct {
		typ        string
		start, due int64
	}{
		{"FS", hm(10, 45), hm(11, 15)}, // 先行の終了（10:45）→ 後続の開始
		{"SS", hm(10, 15), hm(10, 45)}, // 開始どうし
		{"FF", hm(10, 15), hm(10, 45)}, // 終了どうし（長さ 30分を保つ）
		{"SF", hm(9, 45), hm(10, 15)},  // 先行の開始 → 後続の終了
	}
	for _, c := range cases {
		t.Run(c.typ, func(t *testing.T) {
			// 列の外（行2の X とは別の、時間重複チェックの無い Z）を後続にして、列の押しを混ぜない
			p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 15), hm(10, 45))
			p.Others = []chainItem{item(21, "Z", 12, 0, 12, 30)}
			p.Links = []chainLink{{Type: c.typ, Pred: 1, Succ: 21}}
			p.Now = hm(8, 0)
			changes, block := planChain(p)
			if block != nil {
				t.Fatal(block)
			}
			wantSpan(t, changes, 21, c.start, c.due, chainReasonRipple)
		})
	}
}

// 行をまたぐ依存の後続は寄り、寄った後続は自分の列の後ろを押す。
func TestPlanChainCrossRowPushesOtherColumn(t *testing.T) {
	// B を 11:20 まで延ばす（A は動かさない）→ X は B の終了に寄り、Y を押す
	r1, r2 := twoRows()
	p := basePlan(r1, 2, hm(10, 30), hm(11, 20))
	p.Columns = append(p.Columns, r2)
	p.Links = []chainLink{{Type: "FS", Pred: 2, Succ: 11}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 11, hm(11, 20), hm(12, 20), chainReasonRipple)
	wantSpan(t, changes, 12, hm(12, 20), hm(12, 50), chainReasonRipple)
	if changes[0].Seq != 2 || changes[1].Seq != 11 || changes[2].Seq != 12 {
		t.Fatalf("並びは決めた順: %+v", changes)
	}
}

// 時間重複チェックの無いチケットも、依存の後続なら動く。依存の無いものは動かない。
func TestPlanChainOthersMoveOnlyAsSuccessors(t *testing.T) {
	p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 0), hm(10, 50))
	p.Others = []chainItem{item(31, "準備", 10, 30, 10, 40), item(32, "片付け", 10, 30, 10, 40)}
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 31}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 31, hm(10, 50), hm(11, 0), chainReasonRipple)
	wantUnmoved(t, changes, 32)
}

// 秒のずらしは暦日の後に足す。
func TestPlanChainLagSeconds(t *testing.T) {
	p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 0), hm(10, 40))
	p.Others = []chainItem{item(21, "Z", 12, 0, 12, 30)}
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 21, LagDays: 1, LagSeconds: 600}}
	changes, _ := planChain(p)
	next := time.Date(2026, 10, 25, 10, 50, 0, 0, chainTestLoc).UnixMilli()
	wantSpan(t, changes, 21, next, next+30*minute, chainReasonRipple)
}

// 先行が2つなら、最も遅い条件にそろう。届かなかった先行はいまの値で数える。
func TestPlanChainMultiplePredecessors(t *testing.T) {
	p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 0), hm(10, 20))
	p.Others = []chainItem{item(21, "Z", 10, 45, 11, 15), item(22, "W", 9, 0, 10, 45)}
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 21}, {Type: "FS", Pred: 22, Succ: 21}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	// A は 10:20 に縮んだが、W の終了（10:45）が遅いので Z は動かない
	wantUnmoved(t, changes, 21)
}

// 動かせない先行（片端だけ）は、持っている端だけを数える。
func TestPlanChainEndsPredecessor(t *testing.T) {
	p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 0), hm(10, 30))
	due := hm(11, 0)
	p.Ends = map[int32]chainEnds{40: {Due: &due}}
	p.Others = []chainItem{item(21, "Z", 10, 30, 11, 0)}
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 21}, {Type: "FS", Pred: 40, Succ: 21}, {Type: "SS", Pred: 40, Succ: 21}}
	p.NewSpan = chainSpan{Start: hm(10, 0), Due: hm(10, 40)}
	changes, _ := planChain(p)
	// A の終了 10:40 と、40 の終了 11:00 の遅い方。40 は開始を持たないので SS は数えない
	wantSpan(t, changes, 21, hm(11, 0), hm(11, 30), chainReasonRipple)
}

// 開始が固定（着手済み）の FF の後続は、終了だけが寄る。最小の長さを割れば解けない。
func TestPlanChainStartFixedFFMovesDue(t *testing.T) {
	z := item(21, "照明", 10, 0, 10, 30)
	z.Started = true
	p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 0), hm(10, 50))
	p.Others = []chainItem{z}
	p.Links = []chainLink{{Type: "FF", Pred: 1, Succ: 21}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 21, hm(10, 0), hm(10, 50), chainReasonRipple)

	p.NewSpan = chainSpan{Start: hm(9, 0), Due: hm(10, 0)}
	p.Target.Span = chainSpan{Start: hm(9, 0), Due: hm(10, 30)}
	p.MinLen = 10 * minute
	_, block = planChain(p)
	if block == nil || block.Code != chainCannotFit || block.Seq != 21 || block.ShortMs != 10*minute {
		t.Fatalf("block = %+v", block)
	}
}

// 終了が固定の FF の後続より後ろの終了を求めれば解けない。
func TestPlanChainDueFixedFFCannotFit(t *testing.T) {
	z := item(21, "閉会", 10, 0, 10, 30)
	z.DuePinned = true
	p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 0), hm(10, 45))
	p.Others = []chainItem{z}
	p.Links = []chainLink{{Type: "FF", Pred: 1, Succ: 21}}
	_, block := planChain(p)
	if block == nil || block.Seq != 21 || block.ShortMs != 15*minute || !strings.Contains(block.Message, "動かせず") {
		t.Fatalf("block = %+v", block)
	}
}

// 並びと依存が輪になれば order_cycle。同じ列で後ろの B から前の A への依存を、A を動かして辿る。
func TestPlanChainOrderCycle(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0), item(3, "C", 11, 0, 11, 30)}
	p := basePlan(col, 2, hm(10, 30), hm(11, 10))
	p.Links = []chainLink{{Type: "FS", Pred: 3, Succ: 1}}
	// B → C（列）→ A（依存）→ B（列）で輪になる
	_, block := planChain(p)
	if block == nil || block.Code != chainOrderCycle {
		t.Fatalf("block = %+v", block)
	}
	if !strings.Contains(block.Message, "輪になる") {
		t.Fatalf("message = %q", block.Message)
	}
}

// きっかけを動かさずに計算し直すと、依存に合っていない後続がそろう（依存を作った直後。9.17.2）。
func TestPlanChainRecomputeWithoutMove(t *testing.T) {
	p := basePlan([]chainItem{item(1, "A", 10, 0, 10, 30)}, 1, hm(10, 0), hm(10, 30))
	p.Others = []chainItem{item(21, "Z", 11, 0, 11, 30)}
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 21}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantUnmoved(t, changes, 1)
	wantSpan(t, changes, 21, hm(10, 30), hm(11, 0), chainReasonRipple)
}

// 行をまたいだ先で詰まっても、縮めるのは詰まった相手へ届く道の上だけ。
func TestPlanChainShrinkAlongPath(t *testing.T) {
	y := item(12, "Y", 11, 30, 12, 0)
	y.StartPinned = true
	r1 := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 30, 11, 0)}
	r2 := []chainItem{item(11, "X", 10, 30, 11, 0), y}
	p := basePlan(r1, 1, hm(10, 0), hm(11, 10))
	p.Columns = append(p.Columns, r2)
	p.Links = []chainLink{{Type: "FS", Pred: 1, Succ: 11}}
	p.MinLen = 10 * minute
	_, block := planChain(p)
	if block == nil || block.Seq != 12 || block.ShortMs != 10*minute {
		t.Fatalf("X が 11:10–11:40 に寄り、Y の開始のアンカー（11:30）に 10分 足りない: %+v", block)
	}
	p.Shrink = true
	changes, block := planChain(p)
	if block != nil {
		t.Fatalf("X を縮めれば収まる: %+v", block)
	}
	wantSpan(t, changes, 11, hm(11, 10), hm(11, 30), chainReasonShrink)
	// B は Y へ届く道の上に無いので縮めない（押されるだけ）
	wantSpan(t, changes, 2, hm(11, 10), hm(11, 40), chainReasonRipple)
}

// ── きっかけの検証 ─────────────────────────────────────────

func codes(t *testing.T, target rescheduleTarget, op string, start, due *int64, minLen int64) []string {
	t.Helper()
	_, d := checkRescheduleTarget(target, op, start, due, minLen)
	out := []string{}
	for _, x := range d {
		out = append(out, x.Code)
	}
	return out
}

func TestCheckRescheduleTarget(t *testing.T) {
	s, d := i64(hm(10, 0)), i64(hm(10, 30))
	base := rescheduleTarget{Start: s, Due: d}
	cases := []struct {
		name   string
		target rescheduleTarget
		op     string
		start  *int64
		due    *int64
		want   []string
	}{
		{"延ばす", base, rescheduleOpMove, nil, i64(hm(10, 45)), []string{}},
		{"終日", rescheduleTarget{AllDay: true, Start: s, Due: d}, rescheduleOpMove, nil, i64(hm(10, 45)), []string{"not_timed"}},
		{"片側しかない", rescheduleTarget{Due: d}, rescheduleOpMove, nil, i64(hm(10, 45)), []string{"not_timed"}},
		{"片側しかないが両方送れば通る", rescheduleTarget{Due: d}, rescheduleOpMove, s, i64(hm(10, 45)), []string{}},
		{"終了のアンカー", rescheduleTarget{Start: s, Due: d, DuePinned: true}, rescheduleOpMove, nil, i64(hm(10, 45)), []string{"pinned"}},
		{"開始のアンカーは終了だけなら通る", rescheduleTarget{Start: s, Due: d, StartPinned: true}, rescheduleOpMove, nil, i64(hm(10, 45)), []string{}},
		{"値の無い端のアンカーは無視", rescheduleTarget{Due: d, StartPinned: true}, rescheduleOpMove, s, nil, []string{}},
		{"同じ値の送り直しは動かさない", rescheduleTarget{Start: s, Due: d, StartPinned: true}, rescheduleOpMove, s, i64(hm(10, 45)), []string{}},
		{"着手済みの開始", rescheduleTarget{Start: s, Due: d, Started: true}, rescheduleOpMove, i64(hm(9, 50)), nil, []string{"started"}},
		{"着手済みの終了は延ばせる", rescheduleTarget{Start: s, Due: d, Started: true}, rescheduleOpMove, nil, i64(hm(11, 0)), []string{}},
		{"完了は move で動かせない", rescheduleTarget{Start: s, Due: d, Started: true, Done: true, ClosedAt: i64(hm(10, 10))}, rescheduleOpMove, nil, i64(hm(11, 0)), []string{"closed"}},
		{"短すぎる", base, rescheduleOpMove, nil, i64(hm(10, 0) + 30*1000), []string{"too_short"}},
		{"詰める相手が未完了", base, rescheduleOpCompact, nil, nil, []string{"nothing_to_compact"}},
		{"m 以上早くない", rescheduleTarget{Start: s, Due: d, Started: true, Done: true, ClosedAt: i64(hm(10, 29) + 30_000)}, rescheduleOpCompact, nil, nil, []string{"nothing_to_compact"}},
		{"詰められる", rescheduleTarget{Start: s, Due: d, Started: true, Done: true, ClosedAt: i64(hm(10, 10))}, rescheduleOpCompact, nil, nil, []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := codes(t, c.target, c.op, c.start, c.due, minute)
			if !slices.Equal(got, c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
		})
	}

	// 詰める：完了時刻を m の単位へ切り上げる
	span, _ := checkRescheduleTarget(rescheduleTarget{Start: s, Due: d, Started: true, Done: true, ClosedAt: i64(hm(10, 10) + 1500)}, rescheduleOpCompact, nil, nil, minute)
	if span.Due != hm(10, 11) || span.Start != hm(10, 0) {
		t.Fatalf("compact span = %s–%s", fmtHM(span.Start), fmtHM(span.Due))
	}
}

func TestChainDuration(t *testing.T) {
	for ms, want := range map[int64]string{
		15 * minute: "15分", 65 * minute: "1時間5分", 30_000: "30秒", 90_500: "1分31秒", 0: "0秒",
	} {
		if got := chainDuration(ms); got != want {
			t.Errorf("chainDuration(%d) = %q, want %q", ms, got, want)
		}
	}
}

func TestReadTimelineSettings(t *testing.T) {
	cases := []struct {
		raw   string
		snap  int64
		epics []int32
	}{
		{`{}`, 60, []int32{}},
		{`{"timeline_snap_seconds": 300, "timeline_epics": [301, 302, 301]}`, 300, []int32{301, 302}},
		{`{"timeline_snap_seconds": 45}`, 60, []int32{}},
		{`{"timeline_snap_seconds": "60", "timeline_epics": "301"}`, 60, []int32{}},
		{`{"timeline_epics": [1.5, -3, "x", 7, null]}`, 60, []int32{7}},
		{`not json`, 60, []int32{}},
	}
	for _, c := range cases {
		got := readTimelineSettings([]byte(c.raw))
		if got.SnapSeconds != c.snap || !slices.Equal(got.Epics, c.epics) {
			t.Errorf("%s → %+v", c.raw, got)
		}
	}
}

func TestExpectMatches(t *testing.T) {
	changes := []chainChange{{Seq: 1, Version: 4}, {Seq: 2, Version: 7}, {Seq: 3, Version: 1}}
	if !expectMatches([]rescheduleExpect{{2, 7}, {3, 1}}, changes, 1) {
		t.Fatal("同じなら一致")
	}
	if expectMatches([]rescheduleExpect{{2, 6}, {3, 1}}, changes, 1) {
		t.Fatal("版が違えば不一致")
	}
	if expectMatches([]rescheduleExpect{{2, 7}}, changes, 1) {
		t.Fatal("顔ぶれが違えば不一致")
	}
	if !expectMatches([]rescheduleExpect{}, []chainChange{{Seq: 1}}, 1) {
		t.Fatal("きっかけだけなら空と一致")
	}
}
