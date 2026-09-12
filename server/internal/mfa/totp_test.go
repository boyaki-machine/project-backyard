package mfa

import (
	"encoding/base32"
	"errors"
	"strings"
	"testing"
	"time"
)

// seedASCII は RFC 6238 附録B のテスト鍵 "12345678901234567890"。
// 附録は16進で書かれているが、本実装は Base32 を受けるので変換して渡す。
const seedASCII = "12345678901234567890"

func testSecret(t *testing.T) string {
	t.Helper()
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte(seedASCII))
}

// TestCodeMatchesRFC6238 は RFC 6238 附録B の SHA-1 の行を1つずつ確かめる。
//
// **期待値を自分で計算しない。** 規格に書かれた値をそのまま置く（Testing.md 3）。
// 附録は8桁で示しているので、下6桁を取る。
func TestCodeMatchesRFC6238(t *testing.T) {
	secret := testSecret(t)
	cases := []struct {
		unix int64
		want string // 附録B の TOTP（SHA-1、8桁）
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	}
	for _, c := range cases {
		step := Step(time.Unix(c.unix, 0))
		got, err := Code(secret, step)
		if err != nil {
			t.Fatalf("unix=%d: Code が失敗した: %v", c.unix, err)
		}
		want := c.want[len(c.want)-Digits:]
		if got != want {
			t.Errorf("unix=%d: コードが %q、附録B は %q（8桁 %q の下6桁）",
				c.unix, got, want, c.want)
		}
	}
}

// TestStepBoundary は刻みの境界で番号が1つ進むことを確かめる。
func TestStepBoundary(t *testing.T) {
	if a, b := Step(time.Unix(29, 0)), Step(time.Unix(30, 0)); a+1 != b {
		t.Errorf("29秒と30秒が同じ刻みに入っている（%d と %d）", a, b)
	}
	if a, b := Step(time.Unix(30, 0)), Step(time.Unix(59, 0)); a != b {
		t.Errorf("30秒と59秒が違う刻みになった（%d と %d）", a, b)
	}
}

func TestVerifyAcceptsCurrentAndAdjacentSteps(t *testing.T) {
	secret := testSecret(t)
	now := time.Unix(1111111111, 0)
	current := Step(now)

	for _, d := range []int64{-1, 0, 1} {
		code, err := Code(secret, current+d)
		if err != nil {
			t.Fatalf("Code が失敗した: %v", err)
		}
		step, err := Verify(secret, code, now, 0)
		if err != nil {
			t.Errorf("刻み %+d のコードが通らなかった: %v", d, err)
			continue
		}
		if step != current+d {
			t.Errorf("刻み %+d を %d と判定した（期待 %d）", d, step, current+d)
		}
	}
}

// TestVerifyRejectsOutsideWindow は窓の外（±2刻み）を拒むことを確かめる。
//
// **負の側である。** 通る側だけでは、窓の広さが効いている証拠にならない。
func TestVerifyRejectsOutsideWindow(t *testing.T) {
	secret := testSecret(t)
	now := time.Unix(1111111111, 0)
	current := Step(now)

	for _, d := range []int64{-2, 2, -10, 10} {
		code, err := Code(secret, current+d)
		if err != nil {
			t.Fatalf("Code が失敗した: %v", err)
		}
		if _, err := Verify(secret, code, now, 0); !errors.Is(err, ErrCodeMismatch) {
			t.Errorf("刻み %+d のコードが通った（err=%v）", d, err)
		}
	}
}

// TestVerifyRejectsReuse は同じ刻みのコードを2回受け付けないことを確かめる
// （Design.md 6.7.2「画面の後ろで盗み見たコードが30秒間使い回せるのを防ぐ」）。
func TestVerifyRejectsReuse(t *testing.T) {
	secret := testSecret(t)
	now := time.Unix(1111111111, 0)

	code, err := Code(secret, Step(now))
	if err != nil {
		t.Fatalf("Code が失敗した: %v", err)
	}
	step, err := Verify(secret, code, now, 0)
	if err != nil {
		t.Fatalf("1回目が通らなかった: %v", err)
	}
	if _, err := Verify(secret, code, now, step); !errors.Is(err, ErrCodeReused) {
		t.Errorf("同じコードの2回目が ErrCodeReused にならなかった（err=%v）", err)
	}
	// **1つ前の刻みも拒む。** 保存済みの番号以下は全部拒むという規則である。
	prev, err := Code(secret, Step(now)-1)
	if err != nil {
		t.Fatalf("Code が失敗した: %v", err)
	}
	if _, err := Verify(secret, prev, now, step); !errors.Is(err, ErrCodeReused) {
		t.Errorf("保存済みより古い刻みが通った（err=%v）", err)
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	secret := testSecret(t)
	now := time.Unix(1111111111, 0)

	for _, code := range []string{"", "12345", "1234567", "abcdef"} {
		if _, err := Verify(secret, code, now, 0); !errors.Is(err, ErrCodeMismatch) {
			t.Errorf("%q が ErrCodeMismatch にならなかった（err=%v）", code, err)
		}
	}
}

// TestNormalizeStripsSeparators は認証アプリの表示をそのまま貼れることを確かめる。
func TestNormalizeStripsSeparators(t *testing.T) {
	secret := testSecret(t)
	now := time.Unix(1111111111, 0)
	code, err := Code(secret, Step(now))
	if err != nil {
		t.Fatalf("Code が失敗した: %v", err)
	}

	spaced := code[:3] + " " + code[3:]
	if _, err := Verify(secret, spaced, now, 0); err != nil {
		t.Errorf("空白入りのコードが通らなかった（%q）: %v", spaced, err)
	}
}

func TestNewSecretIsUniqueAndUsable(t *testing.T) {
	seen := map[string]bool{}
	for range 16 {
		s, err := NewSecret()
		if err != nil {
			t.Fatalf("NewSecret が失敗した: %v", err)
		}
		// 160ビットを Base32 にすると32文字（パディング無し）。
		if len(s) != 32 {
			t.Errorf("共有秘密が %d 文字（期待 32）: %q", len(s), s)
		}
		if strings.Contains(s, "=") {
			t.Errorf("パディングが付いている: %q", s)
		}
		if seen[s] {
			t.Errorf("同じ共有秘密が2回出た: %q", s)
		}
		seen[s] = true
		if _, err := Code(s, 1); err != nil {
			t.Errorf("作った秘密でコードを作れない: %v", err)
		}
	}
}

func TestCodeRejectsBadSecret(t *testing.T) {
	for _, secret := range []string{"", "1", "!!!!"} {
		if _, err := Code(secret, 1); err == nil {
			t.Errorf("%q を共有秘密として受け付けた", secret)
		}
	}
}
