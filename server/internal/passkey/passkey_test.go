package passkey

import (
	"errors"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
)

// RPID は Host ヘッダから RP ID を導く（Design.md 6.8.3）。
//
// **通る側と落ちる側の両方を並べる。** IP アドレスと単一ラベルのホスト名は
// 別の理由で落ちるので、ErrIPAddress かどうかも見る。
func TestRPID(t *testing.T) {
	cases := []struct {
		host   string
		want   string
		wantIP bool
		bad    bool
	}{
		{host: "localhost:8080", want: "localhost"},
		{host: "localhost", want: "localhost"},
		{host: "PB.Localhost:8081", want: "pb.localhost"},
		{host: "pb.example.com", want: "pb.example.com"},
		{host: "127.0.0.1:8080", wantIP: true},
		{host: "[::1]:8080", wantIP: true},
		{host: "::1", wantIP: true},
		// 単一ラベルは localhost だけが通る（go-webauthn の ValidateRPID）
		{host: "intranet:8080", bad: true},
		{host: "", bad: true},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			got, err := RPID(c.host)
			switch {
			case c.wantIP:
				if !errors.Is(err, ErrIPAddress) {
					t.Fatalf("RPID(%q) err = %v, want ErrIPAddress", c.host, err)
				}
			case c.bad:
				if err == nil {
					t.Fatalf("RPID(%q) = %q, want error", c.host, got)
				}
				if errors.Is(err, ErrIPAddress) {
					t.Fatalf("RPID(%q) を IP アドレスとして扱った", c.host)
				}
			default:
				if err != nil {
					t.Fatalf("RPID(%q) err = %v", c.host, err)
				}
				if got != c.want {
					t.Errorf("RPID(%q) = %q, want %q", c.host, got, c.want)
				}
			}
		})
	}
}

// New が組む RP の設定を、ブラウザへ渡る options で確かめる（Design.md 6.8.1 / 6.8.5）。
//
// **UV・residentKey・attestation・期限は、ここで決めた値がそのまま外へ出る。**
// 設定の取り違えは「登録はできるが UV の無いパスキーで入れる」のような
// 見えにくい穴になるので、組み立ての結果を読む。
func TestNewSetsPolicy(t *testing.T) {
	wa, err := New("localhost", "http://localhost:8080")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	user := &User{ID: "01K2F8QW3H7YRJ4M5N6P7Q8R9S", Name: "tanaka@example.com", DisplayName: "田中"}
	creation, regSession, err := wa.BeginRegistration(user)
	if err != nil {
		t.Fatalf("BeginRegistration: %v", err)
	}
	sel := creation.Response.AuthenticatorSelection
	if sel.ResidentKey != protocol.ResidentKeyRequirementRequired {
		t.Errorf("residentKey = %q, want required", sel.ResidentKey)
	}
	if sel.UserVerification != protocol.VerificationRequired {
		t.Errorf("userVerification = %q, want required", sel.UserVerification)
	}
	if creation.Response.Attestation != protocol.PreferNoAttestation {
		t.Errorf("attestation = %q, want none", creation.Response.Attestation)
	}
	if creation.Response.RelyingParty.ID != "localhost" {
		t.Errorf("rp.id = %q, want localhost", creation.Response.RelyingParty.ID)
	}
	if string(regSession.UserID) != user.ID {
		t.Errorf("user handle = %q, want actor.id（%q）", regSession.UserID, user.ID)
	}

	assertion, loginSession, err := wa.BeginDiscoverableLogin()
	if err != nil {
		t.Fatalf("BeginDiscoverableLogin: %v", err)
	}
	if assertion.Response.UserVerification != protocol.VerificationRequired {
		t.Errorf("login userVerification = %q, want required", assertion.Response.UserVerification)
	}
	// **allowCredentials を持たない**——アカウントの有無を応答に出さない（ApiDesign.md 3.5）
	if len(assertion.Response.AllowedCredentials) != 0 {
		t.Errorf("allowCredentials = %d 件, want 0", len(assertion.Response.AllowedCredentials))
	}
	if assertion.Response.Timeout != int(ChallengeTTL.Milliseconds()) {
		t.Errorf("timeout = %d, want %d", assertion.Response.Timeout, ChallengeTTL.Milliseconds())
	}
	// **サーバ側でも期限を持つ**（Enforce）。0 だとライブラリは期限を見ない。
	if until := time.Until(loginSession.Expires); until <= 0 || until > ChallengeTTL {
		t.Errorf("session.Expires まで %v, want (0, %v]", until, ChallengeTTL)
	}
}
