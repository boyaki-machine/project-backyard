package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strings"
	"testing"
)

func TestNewTokenHasPrefixAndEntropy(t *testing.T) {
	tok, err := NewToken(SessionTokenPrefix)
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	if !strings.HasPrefix(tok, SessionTokenPrefix) {
		t.Errorf("接頭辞が付いていない: %q", tok)
	}

	// base64url（パディングなし）32バイト = 43文字。Design.md 6.2.1。
	body := strings.TrimPrefix(tok, SessionTokenPrefix)
	if len(body) != 43 {
		t.Errorf("乱数部の長さ = %d, want 43", len(body))
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		t.Fatalf("base64url として読めない: %v", err)
	}
	if len(raw) != tokenRandomBytes {
		t.Errorf("乱数のバイト数 = %d, want %d", len(raw), tokenRandomBytes)
	}

	// URL・Cookie でエスケープが要る文字を含まないこと。
	if strings.ContainsAny(tok, "+/= ;,") {
		t.Errorf("エスケープが要る文字を含む: %q", tok)
	}
}

func TestNewTokenIsUnique(t *testing.T) {
	const n = 1000
	seen := make(map[string]bool, n)
	for range n {
		tok, err := NewToken(APITokenPrefix)
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if seen[tok] {
			t.Fatalf("トークンが重複した: %q", tok)
		}
		seen[tok] = true
	}
}

func TestHashToken(t *testing.T) {
	const plaintext = "pb_sess_example"
	got := HashToken(plaintext)

	want := sha256.Sum256([]byte(plaintext))
	if got != hex.EncodeToString(want[:]) {
		t.Errorf("HashToken = %q, want %q", got, hex.EncodeToString(want[:]))
	}
	if len(got) != 64 {
		t.Errorf("長さ = %d, want 64（SHA-256 の16進）", len(got))
	}
	if strings.Contains(got, plaintext) {
		t.Error("ハッシュに平文が含まれている")
	}
	// 同じ入力からは同じハッシュが出る（UNIQUE 索引で引けること）。
	if HashToken(plaintext) != got {
		t.Error("同じ平文から異なるハッシュが出た")
	}
	if HashToken(plaintext+"x") == got {
		t.Error("異なる平文から同じハッシュが出た")
	}
}

func TestTokenPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"pb_sess_abcdefgh", "pb_sess_"},
		{"pb_api_9f3cabcd", "pb_api_9"},
		{"short", "short"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := TokenPrefix(tt.in); got != tt.want {
			t.Errorf("TokenPrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
	}{
		{"通常", "Bearer pb_api_abc", "pb_api_abc"},
		{"スキームの大小は区別しない", "bearer pb_api_abc", "pb_api_abc"},
		{"BEARER", "BEARER pb_api_abc", "pb_api_abc"},
		{"前後の空白を落とす", "Bearer   pb_api_abc  ", "pb_api_abc"},
		{"空のヘッダ", "", ""},
		{"スキームのみ", "Bearer ", ""},
		{"別スキーム", "Basic dXNlcjpwdw==", ""},
		{"スキーム無し", "pb_api_abc", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BearerToken(tt.header); got != tt.want {
				t.Errorf("BearerToken(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestEqualTokenHash(t *testing.T) {
	h := HashToken("pb_sess_a")
	if !EqualTokenHash(h, h) {
		t.Error("同じハッシュが一致しない")
	}
	if EqualTokenHash(h, HashToken("pb_sess_b")) {
		t.Error("異なるハッシュが一致した")
	}
	if EqualTokenHash(h, h[:10]) {
		t.Error("長さの違うものが一致した")
	}
}
