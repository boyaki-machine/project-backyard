// Package auth は認証・認可のドメインロジックを持つ（Design.md 6章）。
package auth

import (
	"fmt"
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
