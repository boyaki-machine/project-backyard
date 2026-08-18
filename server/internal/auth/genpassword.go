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
		"brave", "calm", "clear", "early", "fresh", "gentle", "happy", "keen",
		"kind", "light", "lucky", "quiet", "rapid", "smart", "solid", "warm",
	}
	genNouns = []string{
		"anchor", "bridge", "canyon", "forest", "garden", "harbor", "island", "lake",
		"maple", "meadow", "mint", "orchard", "prairie", "river", "summit", "valley",
	}
)

// GeneratePassword は初期パスワードを1つ作る（ApiDesign.md 6.2）。
//
// 形式は <形容詞>-<名詞>-<4桁数字>（例：quiet-harbor-4172）。
// 語彙は各16語なので 16 × 16 × 10^4 ≈ 2^21.3 の強度になる。
//
// **この強度は暫定である。** Phase 1 の開発中は生成された値を手で打ち込んで
// 動作確認するため、長さと打ちやすさを優先している。単発の初期パスワードで
// あり、must_change_password が既定で true、かつアカウントロック（5回/15分、
// Design.md 6.3）が効くので、オンラインでの推測は現実的でない。
// **セキュリティ監査の際に強度を上げる**（docs/PROGRESS.md「手順外の作業」に起票済み）。
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
	// 1000〜9999 ではなく 0000〜9999 とし、4桁に零詰めする。
	// 先頭を1以上に絞ると候補が1割減るうえ、見た目の桁数も変わらない。
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", fmt.Errorf("初期パスワードの数字を生成できない: %w", err)
	}
	return fmt.Sprintf("%s-%s-%04d", adj, noun, n.Int64()), nil
}

// pickWord は words から一様に1語選ぶ。
func pickWord(words []string) (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(int64(len(words))))
	if err != nil {
		return "", fmt.Errorf("初期パスワードの語を選べない: %w", err)
	}
	return words[n.Int64()], nil
}
