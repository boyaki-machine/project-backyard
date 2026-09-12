package mfa

import (
	"net/url"
	"strings"
	"testing"
)

func TestNewRecoveryCodesShape(t *testing.T) {
	codes, err := NewRecoveryCodes()
	if err != nil {
		t.Fatalf("NewRecoveryCodes が失敗した: %v", err)
	}
	if len(codes) != RecoveryCodeCount {
		t.Fatalf("%d 本を作った（期待 %d）", len(codes), RecoveryCodeCount)
	}

	seen := map[string]bool{}
	for _, c := range codes {
		if len(c) != RecoveryCodeLength {
			t.Errorf("%q が %d 文字（期待 %d）", c, len(c), RecoveryCodeLength)
		}
		for _, r := range c {
			if !strings.ContainsRune(recoveryAlphabet, r) {
				t.Errorf("%q に見間違えやすい文字が入っている（%q）", c, r)
			}
		}
		if seen[c] {
			t.Errorf("同じコードが2回出た: %q", c)
		}
		seen[c] = true
	}
}

// TestRecoveryAlphabetExcludesConfusables は I L O U が入らないことを確かめる。
// **紙に書き写す値なので、見間違える文字を外す**という判断そのものの検査である。
func TestRecoveryAlphabetExcludesConfusables(t *testing.T) {
	for _, r := range "ILOU" {
		if strings.ContainsRune(recoveryAlphabet, r) {
			t.Errorf("%q がアルファベットに入っている", r)
		}
	}
	if len(recoveryAlphabet) != 32 {
		t.Errorf("アルファベットが %d 文字（Crockford Base32 は 32）", len(recoveryAlphabet))
	}
}

func TestNormalizeRecoveryCode(t *testing.T) {
	cases := map[string]string{
		"K7M2QX9B4T":   "K7M2QX9B4T",
		"k7m2qx9b4t":   "K7M2QX9B4T",
		"K7M2Q-X9B4T":  "K7M2QX9B4T",
		" K7M2Q X9B4T": "K7M2QX9B4T",
		"":             "",
	}
	for in, want := range cases {
		if got := NormalizeRecoveryCode(in); got != want {
			t.Errorf("%q を %q にした（期待 %q）", in, got, want)
		}
	}
}

// TestOtpauthURI は認証アプリが読む形になっているかを確かめる。
func TestOtpauthURI(t *testing.T) {
	raw := OtpauthURI("tanaka@example.com", "JBSWY3DPEHPK3PXP")

	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("組み立てた URI を解析できない（%q）: %v", raw, err)
	}
	if u.Scheme != "otpauth" || u.Host != "totp" {
		t.Errorf("scheme/host が %q/%q（期待 otpauth/totp）", u.Scheme, u.Host)
	}

	// ラベルは発行者とアカウントを `:` で繋いだもの。
	label := strings.TrimPrefix(u.Path, "/")
	if want := Issuer + ":tanaka@example.com"; label != want {
		t.Errorf("ラベルが %q（期待 %q）", label, want)
	}

	q := u.Query()
	for key, want := range map[string]string{
		"secret":    "JBSWY3DPEHPK3PXP",
		"issuer":    Issuer,
		"algorithm": "SHA1",
		"digits":    "6",
		"period":    "30",
	} {
		if got := q.Get(key); got != want {
			t.Errorf("%s が %q（期待 %q）", key, got, want)
		}
	}
}
