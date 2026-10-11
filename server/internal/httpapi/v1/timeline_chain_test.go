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
		Column: column, Target: target, InColumn: true,
		NewSpan:  chainSpan{Start: newStart, Due: newDue},
		OtherDue: map[int32]int64{}, MinLen: minute, Now: hm(8, 0), Loc: chainTestLoc,
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
	p.Links = []chainFS{{Pred: 1, Succ: 2}}
	changes, block := planChain(p)
	if block != nil {
		t.Fatal(block)
	}
	wantSpan(t, changes, 2, hm(10, 20), hm(10, 50), chainReasonRipple)
	// C には FS が無いので、隙間が空いても動かない
	wantUnmoved(t, changes, 3)

	p = basePlan(col, 1, hm(10, 0), hm(10, 40))
	p.Links = []chainFS{{Pred: 1, Succ: 2}}
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
	p.Links = []chainFS{{Pred: 1, Succ: 2}}
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
	p.Links = []chainFS{{Pred: 1, Succ: 2, LagDays: 1}}
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
	p.Links = []chainFS{{Pred: 1, Succ: 2}}
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
	p.InColumn = false
	changes, block := planChain(p)
	if block != nil || len(changes) != 1 || changes[0].Seq != 1 {
		t.Fatalf("自分だけ: %+v %+v", changes, block)
	}
}

// 「いま」より前に未着手のものを寄せない（例2の後半）。
func TestPlanChainNotBeforeNow(t *testing.T) {
	col := []chainItem{item(1, "A", 10, 0, 10, 30), item(2, "B", 10, 40, 11, 10), item(3, "C", 11, 10, 11, 40)}
	p := basePlan(col, 1, hm(10, 0), hm(10, 20))
	p.Links = []chainFS{{Pred: 1, Succ: 2}}
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
