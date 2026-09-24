package auth

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// 自動生成パスワードの語彙（ApiDesign.md 6.2）。
//
// **読み上げ・転記しやすい語句連結方式**を採る。ランダム英数字は電話や
// チャットでの伝達時に誤りが生じやすい、というのが 6.2 の理由である。
//
// 語彙の選定基準
//   - 英小文字のみ。大小・記号の打ち間違いを避ける
//   - 4文字以上。連結して常に MinPasswordLength（12文字）を超えさせる
//   - 同音・類似の綴りを混ぜない（例：sea と see を両方入れない）
//   - 意味が中立な語に限る。人名・地名・否定的な語を入れない
var (
	genAdjectives = []string{
		"able", "active", "agile", "alert", "ample", "bold", "brave", "bright",
		"broad", "busy", "calm", "civic", "clear", "clever", "cool", "crisp",
		"deep", "eager", "early", "easy", "epic", "even", "fair", "fine",
		"firm", "free", "fresh", "gentle", "glad", "good", "grand", "great",
		"green", "happy", "humble", "ideal", "jolly", "keen", "kind", "light",
		"lively", "lucky", "merry", "mild", "neat", "noble", "open", "plain",
		"polite", "proud", "pure", "quiet", "rapid", "ready", "rich", "ripe",
		"royal", "smart", "smooth", "solid", "steady", "sunny", "swift", "warm",
	}
	genNouns = []string{
		"acorn", "anchor", "basin", "beach", "birch", "bloom", "breeze", "bridge",
		"brook", "cabin", "canopy", "canyon", "cedar", "cliff", "cloud", "coast",
		"coral", "creek", "dawn", "delta", "dune", "ember", "fern", "field",
		"fjord", "forest", "garden", "glade", "grove", "harbor", "haven", "heath",
		"hill", "island", "knoll", "lagoon", "lake", "ledge", "maple", "marsh",
		"meadow", "mesa", "mint", "moss", "oasis", "orchard", "peak", "pine",
		"pond", "prairie", "reef", "river", "shore", "spring", "spruce", "stone",
		"stream", "summit", "thicket", "tide", "trail", "tundra", "valley", "willow",
	}
)

// GeneratePassword は初期パスワードを1つ作る（ApiDesign.md 6.2）。
//
// 形式は <形容詞>-<名詞>-<4桁数字>-<名詞>（例：quiet-harbor-4172-mint）。
// 語彙は各64語なので 64 × 64 × 10^4 × 64 ≈ 2^31.3 の強度になる。
//
// **語を増やす形を採り、ランダム英数字にはしない。** 読み上げ・転記のしやすさは
// 6.2 が挙げる採用理由そのものであり、強度のために捨てない。**名詞を2回引く**
// ので同じ語が並ぶことはあるが、一様独立に引く限り強度は変わらない。
//
// **単発の初期パスワードである。** must_change_password が既定で true、かつ
// アカウントロック（5回/15分、Design.md 6.3）が効く。
//
// 乱数は crypto/rand を使う。math/rand は種が推測できると全ユーザーの
// 初期パスワードが再現できてしまう。
func GeneratePassword() (string, error) {
	adj, err := pickWord(genAdjectives)
	if err != nil {
		return "", err
	}
	noun, err := pickWord(genNouns)
	if err != nil {
		return "", err
	}
	tail, err := pickWord(genNouns)
	if err != nil {
		return "", err
	}
	// 1000〜9999 ではなく 0000〜9999 とし、4桁に零詰めする。
	// 先頭を1以上に絞ると候補が1割減るうえ、見た目の桁数も変わらない。
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", fmt.Errorf("初期パスワードの数字を生成できない: %w", err)
	}
	return fmt.Sprintf("%s-%s-%04d-%s", adj, noun, n.Int64(), tail), nil
}

// pickWord は words から一様に1語選ぶ。
func pickWord(words []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return "", fmt.Errorf("初期パスワードの語を選べない: %w", err)
	}
	return words[n.Int64()], nil
}
