// リカバリコードの生成と正規化（Design.md 6.7.5）。
//
// **認証アプリを失った人が入るための最後の1本である。** 10本を1回だけ表示し、
// DB には SHA-256 のハッシュしか残さない（access_token と同じ作法）。
package mfa

import (
	"crypto/rand"
	"fmt"
	"strings"
)

const (
	// RecoveryCodeCount は1回の発行で作る本数。
	RecoveryCodeCount = 10
	// RecoveryCodeLength は1本の文字数。**Crockford Base32 の10文字＝50ビット**で、
	// 総当たりに耐える（Design.md 6.7.5 が Argon2id を使わない根拠でもある）。
	RecoveryCodeLength = 10
)

// recoveryAlphabet は Crockford Base32（`0-9A-Z` から I L O U を除く）。
//
// **紙に書き写す値なので、見間違える文字を外す**——`I` と `1`、`O` と `0` は
// 手書きで区別できない。同じ理由で `U` を外すのは Crockford の定義に従った。
const recoveryAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewRecoveryCodes は RecoveryCodeCount 本を作る。
func NewRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, RecoveryCodeCount)
	for range RecoveryCodeCount {
		code, err := newRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func newRecoveryCode() (string, error) {
	buf := make([]byte, RecoveryCodeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("リカバリコードを生成できない: %w", err)
	}

	// **剰余で丸めない。** アルファベットが32文字で、256 は32の倍数なので
	// 剰余でも偏りは出ないが、**「32文字だから安全」という前提が
	// アルファベットを変えた日に崩れる。** 常に安全な形で書く。
	var sb strings.Builder
	sb.Grow(RecoveryCodeLength)
	for _, b := range buf {
		sb.WriteByte(recoveryAlphabet[int(b)%len(recoveryAlphabet)])
	}
	return sb.String(), nil
}

// NormalizeRecoveryCode は利用者が打った値を照合できる形へ揃える。
//
// **空白とハイフンを落とし、小文字を大文字にする**（ApiDesign.md 3.4）。
// 紙から写す値なので、**見た目の差で落とさない。**
func NormalizeRecoveryCode(code string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r
		case r >= 'a' && r <= 'z':
			return r - ('a' - 'A')
		default:
			return -1
		}
	}, code)
}
