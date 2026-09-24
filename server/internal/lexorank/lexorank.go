// Package lexorank はチケットの並び順キー `ticket.sort_key` を生成する
// （ApiDesign.md 9.4、DbDesign.md 6.6）。
//
// **順序キーの生成規則はサーバに1つだけ置く**（9.4）。Web・MCP・将来の CLI が
// それぞれ同じ規則を実装すると、1つでもずれた時点で順序が壊れる。したがって
// PATCH で sort_key を直接書かせず、POST /tickets/:seq/move だけが順序を動かす。
//
// # キーの形
//
//	0|n:
//	↑ ↑↑
//	│ │└─ 終端。ApiDesign.md 9.2.2 の例（"0|hzzzzz:"）と同じ形
//	│ └── 本体。a〜z の26進で、桁数は可変
//	└──── バケット。0 固定（振り直しのたびに束を切り替える用途は持たない）
//
// **本体に数字を使わない。** 終端の ':'（0x3A）は '9'（0x39）より大きく 'a'（0x61）
// より小さいため、本体に数字が混ざると「短いほうが先」という前提が崩れる
// （"0|a:" と "0|a9:" の大小が入れ替わる）。a〜z だけなら本体の文字は必ず ':' より
// 大きく、接頭辞のほうが小さいという規則がそのまま成り立つ。
//
// # 比較は必ず COLLATE "C" で行う
//
// `ticket.sort_key` は照合順の指定を持たない text 列であり、DBの既定は
// ja-JP-x-icu である（DbDesign.md 4.4）。ICU の照合は句読点の重みを言語規則で
// 決めるため、':' を含むキーの大小がバイト順と一致する保証がない。**この
// パッケージが仮定しているのはバイト順**なので、SQL 側は ORDER BY / 比較の
// どちらも `COLLATE "C"` を明示すること（ticket.sql）。
package lexorank

import "strings"

const (
	// bucket は現在使っているバケット。
	bucket = "0"
	// prefix / suffix はキーの外枠。
	prefix = bucket + "|"
	suffix = ":"

	// radix は本体の基数（a〜z の26文字）。
	radix = 26
	// minDigit / maxDigit は本体に現れる桁の値域。
	minDigit = 0
	maxDigit = radix - 1

	// beforeFirst / afterLast は桁が尽きた側を表す番兵。
	// prev が尽きた＝どの桁より小さい、next が尽きた＝どの桁より大きい。
	beforeFirst = minDigit - 1
	afterLast   = radix

	// MaxBodyLen は本体の桁数の上限。
	//
	// Between は隣り合う2つのキーの間にいくらでも桁を足せるが、伸び続けると
	// 1行あたりの記憶域と比較のコストが上がる。上限に達したら ok=false を返し、
	// 呼び出し側にプロジェクト全体の振り直し（Rebalance）を促す。これが
	// 9.4 の応答にある rebalanced の正体である。
	//
	// 32桁は、同じ2行の間へ挿し続けた場合におよそ32回で到達する深さである。
	// 実運用でここまで来るのは、同一箇所への連続挿入が続いたときだけになる。
	MaxBodyLen = 32
)

// digitLetters は桁の値と文字の対応。
const digitLetters = "abcdefghijklmnopqrstuvwxyz"

// Between は prev と next の間に入るキーを返す。
//
// prev が空文字なら「先頭より前」、next が空文字なら「末尾より後」を意味する。
// 両方が空なら、まだ1件も無いプロジェクトの最初のキーになる。
//
// ok が false になるのは次の2つで、**どちらも呼び出し側は Rebalance で回復できる**。
//
//   - 結果の本体が MaxBodyLen を超える（詰まりすぎた）
//   - prev / next がこのパッケージの形式でない（過去のデータ・手で入れた行）
//
// prev >= next のような矛盾した組み合わせは呼び出し側の誤りであり、
// ok=false を返す。
func Between(prev, next string) (string, bool) {
	pb, okPrev := parse(prev)
	nb, okNext := parse(next)
	if !okPrev || !okNext {
		return "", false
	}
	if len(nb) > 0 && len(pb) > 0 && !less(pb, nb) {
		return "", false
	}

	body := appendBody(pb, nb)
	if len(body) == 0 || len(body) > MaxBodyLen {
		return "", false
	}
	return format(body), true
}

// appendBody は「末尾へ足す」場合だけ中点法を使わずに桁を1つ進める。
//
// **中点法は末尾追加に向いていない。** next が空（末尾より後）のとき、中点は
// prev と上限のちょうど間になるので、追加のたびに残りの幅が半分になる
// ——n → u → x → z → zn → zu … と、25回ほどで桁が伸びる。チケットは
// 「作った順に末尾へ積む」のが最も多い使われ方（ApiDesign.md 9.3 の
// 「現在の末尾の次」）なので、そこだけ桁を1つ進める形にする。
// n → o → p … となり、桁が伸びるのは25件ごとになる。
//
// **末尾の桁が最大（z）のときは中点法へ戻す。** 進める先が無いためで、
// そのとき1桁伸びる。
//
// 先頭への挿入（prev が空）は中点法のままにしてある。桁を1つ戻す形にすると
// 最小の桁（a）に触れてしまい、「本体は最小の桁で終わらない」という前提
// ——接頭辞との間に必ず値を作れること——が崩れる。先頭への連続挿入は
// 末尾への追加ほど多くない。
func appendBody(prev, next []int) []int {
	if len(next) > 0 || len(prev) == 0 || prev[len(prev)-1] >= maxDigit {
		return midBody(prev, next)
	}
	body := make([]int, len(prev))
	copy(body, prev)
	body[len(body)-1]++
	return body
}

// Rebalance は n 件ぶんの、間隔をそろえたキーを昇順で返す。
//
// Between が桁を作れなくなったときに、プロジェクトの全チケットへ割り当て直す
// （9.4 の rebalanced=true）。**呼び出し側は割り当て後に一覧を取り直させること**
// ——手元の sort_key がすべて古くなっている。
//
// n が 0 以下なら空を返す。
func Rebalance(n int) []string {
	if n <= 0 {
		return nil
	}

	// 桁数は「n+2 個の区切りを置いても間隔が2以上になる」最小の幅を選ぶ。
	// 間隔が2以上あることが、末尾が a（値0）になった値を1つ進めても
	// 隣を追い越さないことの根拠になる（下の bump）。
	width := 1
	space := uint64(radix)
	for width < 12 && space < 2*uint64(n+2) {
		width++
		space *= radix
	}

	keys := make([]string, 0, n)
	for i := 1; i <= n; i++ {
		v := space / uint64(n+2) * uint64(i)
		// **末尾を a（値0）にしない。** 末尾が最小の桁だと、その手前までの
		// 接頭辞（例 "aa" に対する "a"）との間へ Between が値を作れなくなる。
		if v%radix == 0 {
			v++
		}
		keys = append(keys, format(toDigits(v, width)))
	}
	return keys
}

// Valid はこのパッケージが生成した形式かを返す。
func Valid(s string) bool {
	body, ok := parse(s)
	return ok && len(body) > 0
}

// ── 内部 ────────────────────────────────────────────────────

// midBody は2つの本体の間に入る本体を返す。
//
// 一般に知られた「中間文字列」の算法をそのまま a〜z へ当てたものである。
// prev を空、next を空にしたときの番兵（-1 と radix）が「先頭より前」
// 「末尾より後」を表す。
func midBody(prev, next []int) []int {
	// 食い違う最初の桁まで進める。**番兵は prev 側と next 側で違う**
	// ——prev が尽きたら「先頭より前」、next が尽きたら「末尾より後」であり、
	// 同じ値を返すと両方が空のときに永久に一致し続ける。
	i := 0
	for digitAt(prev, i, beforeFirst) == digitAt(next, i, afterLast) {
		i++
	}
	p, n := digitAt(prev, i, beforeFirst), digitAt(next, i, afterLast)

	out := make([]int, 0, i+2)
	out = append(out, prev[:min(i, len(prev))]...)
	j := i + 1

	switch {
	case p < minDigit:
		// prev が尽きている。next の最小桁が続くあいだ、その桁を写して進む。
		for n == minDigit {
			out = append(out, minDigit)
			n = digitAt(next, j, afterLast)
			j++
		}
		if n == minDigit+1 {
			out = append(out, minDigit)
			n = radix
		}
	case p+1 == n:
		// 隣り合っていて間が無い。prev 側へ潜り、最大桁が続くあいだ写して進む。
		out = append(out, p)
		n = radix
		for {
			p = digitAt(prev, j, beforeFirst)
			j++
			if p != maxDigit {
				break
			}
			out = append(out, maxDigit)
		}
	}

	// 切り上げの中点。p < n が保証されているので、必ず p より大きい桁になる。
	return append(out, (p+n+1)/2)
}

// digitAt は i 桁目の値を返す。桁が尽きていたら渡された番兵を返す。
func digitAt(body []int, i, sentinel int) int {
	if i < len(body) {
		return body[i]
	}
	return sentinel
}

// less は本体どうしの大小。バイト順（＝桁の値の辞書順）で比べる。
func less(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return len(a) < len(b)
}

// parse はキーから本体を取り出す。空文字は「境界（先頭より前／末尾より後）」を
// 表すため、本体なし・ok=true として扱う。
func parse(key string) ([]int, bool) {
	if key == "" {
		return nil, true
	}
	if !strings.HasPrefix(key, prefix) || !strings.HasSuffix(key, suffix) {
		return nil, false
	}
	body := key[len(prefix) : len(key)-len(suffix)]
	if body == "" {
		return nil, false
	}
	digits := make([]int, 0, len(body))
	for i := 0; i < len(body); i++ {
		d := int(body[i]) - 'a'
		if d < minDigit || d > maxDigit {
			return nil, false
		}
		digits = append(digits, d)
	}
	return digits, true
}

// format は本体をキーの形へ包む。
func format(body []int) string {
	var sb strings.Builder
	sb.Grow(len(prefix) + len(body) + len(suffix))
	sb.WriteString(prefix)
	for _, d := range body {
		sb.WriteByte(digitLetters[d])
	}
	sb.WriteString(suffix)
	return sb.String()
}

// toDigits は値を width 桁の本体へ写す（上位から詰める）。
func toDigits(v uint64, width int) []int {
	body := make([]int, width)
	for i := width - 1; i >= 0; i-- {
		body[i] = int(v % radix)
		v /= radix
	}
	return body
}
