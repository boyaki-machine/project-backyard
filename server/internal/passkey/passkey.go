// Package passkey はパスキー（Design.md 6.8）のうち、HTTP にも DB にも
// 依存しない部分を持つ。
//
// **検証そのものは go-webauthn に任せる**（Design.md 6.8.5）。ここに置くのは、
// PB がライブラリへ渡す設定（RP ID・origin・UV・attestation・期限）を1か所に
// 閉じ込めることと、Host ヘッダから RP ID を導く規則だけである。
package passkey

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const (
	// RPDisplayName は認証器が利用者に見せる RP の名前。
	RPDisplayName = "Project Backyard"

	// ChallengeTTL は挑戦の有効期間（Design.md 6.8.2）。
	//
	// **ブラウザに渡す timeout と同じ長さにする。** 端末のダイアログを開いて
	// 生体認証を済ませる時間であり、6.7.4 の TOTP の挑戦と揃えた。
	ChallengeTTL = 5 * time.Minute

	// MaxPerUser は1人あたりの上限（ApiDesign.md 4.7.2）。
	//
	// **/me/tokens と TOTP の5件に揃えた。** 増えるほど「どの端末が自分の
	// アカウントを開けるか」を本人が把握できなくなる。
	MaxPerUser = 5
)

// ErrIPAddress は Host が IP アドレスであることを表す（Design.md 6.8.3）。
//
// **IP アドレスは RP ID になれない。** 呼び出し側はこれを 409 に写す。
var ErrIPAddress = errors.New("IP アドレスは RP ID になれない")

// RPID は Host ヘッダから RP ID を導く（Design.md 6.8.3）。
//
// ポートを除いて小文字にする。**IP アドレスなら ErrIPAddress を返す**——
// `http://127.0.0.1:8080` で開いた画面ではパスキーを使えない。
func RPID(host string) (string, error) {
	h := host
	if hostOnly, _, err := net.SplitHostPort(host); err == nil {
		h = hostOnly
	}
	h = strings.ToLower(strings.Trim(h, "[]"))
	if net.ParseIP(h) != nil {
		return "", ErrIPAddress
	}
	if err := protocol.ValidateRPID(h); err != nil {
		return "", fmt.Errorf("RP ID にできないホスト名（%q）: %w", h, err)
	}
	return h, nil
}

// New は1回の儀式（登録かログイン）のための RP を組む。
//
// **要求ごとに作る。** RP ID と origin が Host ヘッダから決まるため、起動時に
// 1つ作って使い回せない（Design.md 6.8.3）。組み立ては設定の検査だけである。
func New(rpID, origin string) (*webauthn.WebAuthn, error) {
	timeout := webauthn.TimeoutConfig{Enforce: true, Timeout: ChallengeTTL, TimeoutUVD: ChallengeTTL}
	return webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: RPDisplayName,
		RPOrigins:     []string{origin},
		// **attestation は none**（Design.md 6.8.5）。PB は機種を選ばない。
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			// メールアドレスを入力させずに引けるパスキーだけを受ける（6.8.1）。
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			RequireResidentKey: protocol.ResidentKeyRequired(),
			// **UV を必須にする。** パスキー1回で多要素を満たす根拠である（6.8.1）。
			// ログインの挑戦もこの値を引き継ぎ、UV の無い応答をライブラリが拒む。
			UserVerification: protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{Login: timeout, Registration: timeout},
	})
}

// User は go-webauthn に渡す利用者。
//
// **user handle は actor.id をそのまま使う**（Design.md 6.8.4）。ULID は個人を
// 特定する情報を持たず、すでに API で出している識別子なので、別の乱数を持たない。
type User struct {
	ID string
	// Name は認証器のパスキー一覧に出る名前。メールアドレスを入れる。
	Name        string
	DisplayName string
	Credentials []webauthn.Credential
}

func (u *User) WebAuthnID() []byte                         { return []byte(u.ID) }
func (u *User) WebAuthnName() string                       { return u.Name }
func (u *User) WebAuthnDisplayName() string                { return u.DisplayName }
func (u *User) WebAuthnCredentials() []webauthn.Credential { return u.Credentials }
