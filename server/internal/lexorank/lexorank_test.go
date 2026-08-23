package lexorank

import (
	"sort"
	"strings"
	"testing"
)

// 空のプロジェクトに最初の1件を置く（ApiDesign.md 9.3 の「現在の末尾の次」）。
func TestBetween_空のとき(t *testing.T) {
	got, ok := Between("", "")
	if !ok {
		t.Fatalf("Between(\"\", \"\") が失敗した")
	}
	if !Valid(got) {
		t.Fatalf("生成したキーが形式に合わない: %q", got)
	}
	if !strings.HasPrefix(got, "0|") || !strings.HasSuffix(got, ":") {
		t.Errorf("キーの外枠が 9.2.2 の例と違う: %q", got)
	}
}

// 末尾への追加と先頭への挿入。
func TestBetween_境界(t *testing.T) {
	first, _ := Between("", "")

	last, ok := Between(first, "")
	if !ok {
		t.Fatalf("末尾への追加が失敗した")
	}
	if !(first < last) {
		t.Errorf("末尾へ追加したのに大きくならない: %q → %q", first, last)
	}

	head, ok := Between("", first)
	if !ok {
		t.Fatalf("先頭への挿入が失敗した")
	}
	if !(head < first) {
		t.Errorf("先頭へ挿したのに小さくならない: %q → %q", head, first)
	}
}

// 隣り合う2つの間へ繰り返し挿す。**ここが詰まると rebalanced になる**（9.4）。
func TestBetween_間へ挿し続ける(t *testing.T) {
	lo, _ := Between("", "")
	hi, _ := Between(lo, "")

	prev := lo
	for i := 0; i < MaxBodyLen; i++ {
		mid, ok := Between(prev, hi)
		if !ok {
			// 上限に達した。ここで打ち切るのが設計どおりの振る舞い。
			if i < 8 {
				t.Fatalf("%d 回目で早々に詰まった（上限 %d 桁）", i+1, MaxBodyLen)
			}
			return
		}
		if !(prev < mid && mid < hi) {
			t.Fatalf("%d 回目で順序が壊れた: %q < %q < %q になっていない", i+1, prev, mid, hi)
		}
		prev = mid
	}
}

// 逆順・同値の組み合わせは呼び出し側の誤りとして弾く。
func TestBetween_矛盾した組み合わせ(t *testing.T) {
	a, _ := Between("", "")
	b, _ := Between(a, "")

	for _, tc := range []struct{ prev, next string }{
		{b, a}, // 逆順
		{a, a}, // 同値
	} {
		if _, ok := Between(tc.prev, tc.next); ok {
			t.Errorf("Between(%q, %q) は失敗すべき", tc.prev, tc.next)
		}
	}
}

// 形式に合わない値は ok=false。呼び出し側は Rebalance で回復する。
func TestBetween_形式外の入力(t *testing.T) {
	for _, bad := range []string{"0|n", "n:", "0|:", "0|N:", "0|n1:", "1|n:", "abc"} {
		if _, ok := Between(bad, ""); ok {
			t.Errorf("prev=%q は形式外として弾くべき", bad)
		}
		if Valid(bad) {
			t.Errorf("Valid(%q) は false であるべき", bad)
		}
	}
}

// **バイト順で比較しても、接頭辞のほうが必ず小さい。**
// 本体に数字を使わない理由（':' が '9' より大きい）を突く。
func Testキーはバイト順で比較できる(t *testing.T) {
	base, _ := Between("", "")
	deeper, ok := Between(base, "")
	if !ok {
		t.Fatalf("キーを作れない")
	}
	// base の直後へ潜って、base の本体を接頭辞に持つキーを作る。
	inner, ok := Between(base, deeper)
	if !ok {
		t.Fatalf("間のキーを作れない")
	}
	innerBody := inner[len("0|") : len(inner)-1]
	baseBody := base[len("0|") : len(base)-1]
	if !strings.HasPrefix(innerBody, baseBody) {
		t.Skipf("この組み合わせでは接頭辞にならない（base=%q inner=%q）", base, inner)
	}
	if !(base < inner) {
		t.Errorf("接頭辞のほうが大きくなっている: %q < %q が成り立たない", base, inner)
	}
	for i := 0; i < len(innerBody); i++ {
		if innerBody[i] < 'a' || innerBody[i] > 'z' {
			t.Fatalf("本体に a〜z 以外が混ざった: %q", innerBody)
		}
	}
}

// 振り直しは昇順で、間隔がそろっていること。
func TestRebalance(t *testing.T) {
	for _, n := range []int{1, 2, 3, 10, 200, 5000} {
		keys := Rebalance(n)
		if len(keys) != n {
			t.Fatalf("n=%d に対して %d 件返った", n, len(keys))
		}
		if !sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] }) {
			t.Errorf("n=%d の結果が昇順でない", n)
		}
		for i, k := range keys {
			if !Valid(k) {
				t.Fatalf("n=%d の %d 件目が形式に合わない: %q", n, i, k)
			}
		}
		// 振り直した直後は、どの隣り合う2つの間にも1つ挿せること。
		for i := 0; i+1 < len(keys); i++ {
			if _, ok := Between(keys[i], keys[i+1]); !ok {
				t.Fatalf("n=%d の %d 番目と %d 番目の間に挿せない: %q %q",
					n, i, i+1, keys[i], keys[i+1])
			}
		}
		// 先頭より前・末尾より後にも置けること。
		if _, ok := Between("", keys[0]); !ok {
			t.Errorf("n=%d で先頭より前に置けない（%q）", n, keys[0])
		}
		if _, ok := Between(keys[len(keys)-1], ""); !ok {
			t.Errorf("n=%d で末尾より後に置けない", n)
		}
	}
}

func TestRebalance_ゼロ件(t *testing.T) {
	if got := Rebalance(0); len(got) != 0 {
		t.Errorf("0件のとき空を返すべき: %v", got)
	}
}

// 並べ替えを何度も繰り返しても順序が壊れないことを、実際に動かして確かめる。
func Test並べ替えを繰り返す(t *testing.T) {
	keys := Rebalance(12)

	// 末尾を先頭へ、先頭を末尾へ、を交互に20回。
	for round := 0; round < 20; round++ {
		if round%2 == 0 {
			moved, ok := Between("", keys[0])
			if !ok {
				t.Fatalf("%d 周目：先頭へ動かせない", round)
			}
			keys = append([]string{moved}, keys[:len(keys)-1]...)
		} else {
			moved, ok := Between(keys[len(keys)-1], "")
			if !ok {
				t.Fatalf("%d 周目：末尾へ動かせない", round)
			}
			keys = append(keys[1:], moved)
		}
		if !sort.SliceIsSorted(keys, func(i, j int) bool { return keys[i] < keys[j] }) {
			t.Fatalf("%d 周目で順序が壊れた: %v", round, keys)
		}
	}
}

// **末尾への追加で桁がすぐ伸びないこと。**
//
// チケットは「作った順に末尾へ積む」のが最も多い使われ方（ApiDesign.md 9.3）。
// 中点法のままだと25回ほどで桁が伸び、振り直し（rebalanced=true。9.4）が
// 早く来て、そのたびにクライアントが一覧を取り直すことになる。
func TestBetween_末尾へ積んでも浅いまま(t *testing.T) {
	key := ""
	for i := 0; i < 20; i++ {
		next, ok := Between(key, "")
		if !ok {
			t.Fatalf("%d 回目で末尾に足せない（key=%q）", i+1, key)
		}
		if key != "" && !(key < next) {
			t.Fatalf("%d 回目で順序が壊れた: %q < %q が成り立たない", i+1, key, next)
		}
		key = next
	}

	body := key[len("0|") : len(key)-1]
	if len(body) > 2 {
		t.Errorf("20回積んだ後の桁数 = %d（%q）。末尾追加で桁が伸びすぎている", len(body), key)
	}
}

// 末尾の桁が最大（z）のときは中点法へ戻り、1桁だけ伸びる。
func TestBetween_末尾がzのとき(t *testing.T) {
	got, ok := Between("0|z:", "")
	if !ok {
		t.Fatalf("末尾が z のときに足せない")
	}
	if !("0|z:" < got) {
		t.Errorf("%q が %q より後ろにならない", got, "0|z:")
	}
	if body := got[len("0|") : len(got)-1]; len(body) != 2 {
		t.Errorf("桁数 = %d（%q）。1桁だけ伸びるはず", len(body), got)
	}
}
