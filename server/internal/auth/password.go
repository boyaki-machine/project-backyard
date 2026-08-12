// Package auth は認証・認可のドメインロジックを持つ（Design.md 6章）。
package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"unicode/utf8"

	"github.com/alexedwards/argon2id"
)

// MinPasswordLength はパスワードの最小長（Design.md 6.3）。
// 複雑性要件（記号必須等）は課さず、長さを優先する。
const MinPasswordLength = 12

// hashParams は Design.md 6.3 の初期値 m=64MiB, t=3, p=4。
// SaltLength / KeyLength は同節に記載がないため argon2id の既定値に従う。
var hashParams = &argon2id.Params{
	Memory:      64 * 1024, // KiB 単位。64MiB
	Iterations:  3,
	Parallelism: 4,
	SaltLength:  16,
	KeyLength:   32,
}

// ValidatePassword はパスワードが最小長を満たすかを検査する。
func ValidatePassword(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLength {
		return fmt.Errorf("パスワードは%d文字以上にしてください（入力は%d文字）", MinPasswordLength, n)
	}
	return nil
}

// HashPassword は Argon2id で PHC 文字列を生成する。
// local_credential.password_hash にそのまま格納する（DbDesign.md 6.2）。
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	hash, err := argon2id.CreateHash(password, hashParams)
	if err != nil {
		return "", fmt.Errorf("パスワードのハッシュ化に失敗した: %w", err)
	}
	return hash, nil
}

// VerifyPassword は PHC 文字列と平文を照合する。
func VerifyPassword(password, phc string) (bool, error) {
	ok, err := argon2id.ComparePasswordAndHash(password, phc)
	if err != nil {
		return false, fmt.Errorf("パスワードの照合に失敗した: %w", err)
	}
	return ok, nil
}

// NeedsRehash は保存済みの PHC 文字列が現行パラメータより弱いかを返す
// （Design.md 6.2.1 手順5「ハッシュパラメータが旧世代なら再ハッシュ」）。
//
// **弱いときだけ true にする。** 保存済みのパラメータが現行より強い場合
// （運用者が一時的に強度を上げていた等）に作り直すと、強度を下げることに
// なるため。解釈できない PHC 文字列は再ハッシュの対象にしない（照合が
// そもそも通らず、成功経路に入らない）。
func NeedsRehash(phc string) bool {
	params, _, _, err := argon2id.DecodeHash(phc)
	if err != nil {
		return false
	}
	return params.Memory < hashParams.Memory ||
		params.Iterations < hashParams.Iterations ||
		params.Parallelism < hashParams.Parallelism ||
		params.KeyLength < hashParams.KeyLength ||
		params.SaltLength < hashParams.SaltLength
}

// dummyHash は「ユーザーが存在しない」場合でも同じだけ計算時間を使うための
// 使い捨てハッシュ（Design.md 6.2.1 の末尾）。
//
// 起動ごとに乱数から作る。固定値を埋め込むと、照合に成功しうる平文が
// 理論上は存在してしまうため。生成は初回の呼び出し時に一度だけ行う。
var dummyHash = sync.OnceValue(func() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// 乱数が取れない環境では時間合わせを諦める。照合自体には使わない値であり、
		// ここで起動を止めるほうが害が大きい。
		return ""
	}
	phc, err := argon2id.CreateHash(base64.RawURLEncoding.EncodeToString(b), hashParams)
	if err != nil {
		return ""
	}
	return phc
})

// VerifyAgainstDummy はダミーハッシュに対する照合を行い、結果を捨てる。
//
// メールアドレスが未登録のときに呼ぶ。存在するアカウントと応答時間を
// 揃えることで、応答の速さから登録の有無を推測されないようにする
// （Design.md 6.2.1「ユーザーが存在しない場合もダミーハッシュを検証し、
// 応答時間を揃える」）。
func VerifyAgainstDummy(password string) {
	phc := dummyHash()
	if phc == "" {
		return
	}
	_, _ = VerifyPassword(password, phc)
}
