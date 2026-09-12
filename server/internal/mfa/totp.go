// TOTP（RFC 6238）の生成と照合（Design.md 6.7.2）。
//
// **自前で実装している。** 依存を足さない判断であり、規格の実装量が小さく
// （HMAC-SHA1 と切り出しだけ）、**RFC 6238 附録B のテストベクタで測れる**ため。
// MCP を公式 SDK なしで実装したのと同じ向きの判断である（Design.md 8.6）。
//
// パラメータは列に持たず、本ファイルの定数とする。**秘密を作るのは PB だけ**なので、
// 行ごとに違う値が入る経路が無い（Design.md 6.7.2）。
package mfa

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	// Digits は生成するコードの桁数（RFC 6238 §1.2 の既定）。
	Digits = 6
	// PeriodSec は刻みの長さ（秒）。同上。
	PeriodSec = 30
	// SkewSteps は許容する前後の刻み数（RFC 6238 §5.2 の時計ずれ対策）。
	//
	// **2刻み以上にしない。** 受け付ける窓が広がるほど総当たりの的が広がる。
	SkewSteps = 1
	// secretBytes は共有秘密の長さ。RFC 4226 §4 が160ビットを推奨する。
	secretBytes = 20
)

// base32NoPad は共有秘密の符号化。**パディングを付けない**——
// `=` は認証アプリの手入力欄で弾かれることがあり、otpauth URI でも慣例的に外す。
var base32NoPad = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewSecret は共有秘密を1つ作り、Base32 の文字列で返す。
func NewSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("TOTP の共有秘密を生成できない: %w", err)
	}
	return base32NoPad.EncodeToString(buf), nil
}

// Step は時刻を刻みの番号へ落とす。
//
// **保存するのはこの番号である**（`user_mfa_credential.last_used_step`）。
// 時刻で持つと、許容窓との比較のたびに刻みへ戻す計算が要る（DbDesign.md 6.18）。
func Step(t time.Time) int64 {
	return t.Unix() / PeriodSec
}

// Code は指定の刻みに対するコードを返す（RFC 6238 §4、RFC 4226 §5.3）。
func Code(secret string, step int64) (string, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}

	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// 動的切り出し（RFC 4226 §5.3）。末尾4ビットが取り出す位置を指す。
	offset := sum[len(sum)-1] & 0x0f
	truncated := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for range Digits {
		mod *= 10
	}
	return fmt.Sprintf("%0*d", Digits, truncated%mod), nil
}

// ErrCodeMismatch はコードが窓内のどの刻みとも一致しなかった。
var ErrCodeMismatch = errors.New("確認コードが一致しない")

// ErrCodeReused は窓内で一致したが、その刻みは既に使われている。
//
// **一致と再利用を分けて返す。** 呼び出し側の応答は同じ（どちらも失敗）だが、
// **サーバログでは区別できるほうがよい**——後者は盗み見られた疑いを示す。
var ErrCodeReused = errors.New("確認コードが再利用されている")

// Verify は受け取ったコードを前後 SkewSteps の窓で照合し、通った刻みを返す。
//
// lastUsedStep には保存済みの値を渡す（未使用なら 0）。**それ以下の刻みは
// 拒む**ので、同じコードを2回使えない（Design.md 6.7.2）。
func Verify(secret, code string, now time.Time, lastUsedStep int64) (int64, error) {
	code = Normalize(code)
	if len(code) != Digits {
		return 0, ErrCodeMismatch
	}

	current := Step(now)
	// **新しい刻みから先に試す。** 正しく打っている人は当刻みで一致するので、
	// HMAC の計算回数が1回で済む。
	for _, step := range []int64{current, current - 1, current + 1} {
		if step > current+SkewSteps || step < current-SkewSteps {
			continue
		}
		want, err := Code(secret, step)
		if err != nil {
			return 0, err
		}
		// **定数時間で比べる。** 桁ごとに早く抜けると、
		// 応答時間から正しい桁が読めてしまう。
		if hmac.Equal([]byte(want), []byte(code)) {
			if step <= lastUsedStep {
				return 0, ErrCodeReused
			}
			return step, nil
		}
	}
	return 0, ErrCodeMismatch
}

// Normalize は利用者が打った・貼ったコードから、空白と区切りを落とす。
//
// **認証アプリは「123 456」と空けて表示する**ものがあり、そのまま貼られる。
func Normalize(code string) string {
	return strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, code)
}

func decodeSecret(secret string) ([]byte, error) {
	key, err := base32NoPad.DecodeString(strings.ToUpper(strings.TrimSpace(secret)))
	if err != nil {
		return nil, fmt.Errorf("共有秘密を Base32 として読めない: %w", err)
	}
	if len(key) == 0 {
		return nil, errors.New("共有秘密が空である")
	}
	return key, nil
}
