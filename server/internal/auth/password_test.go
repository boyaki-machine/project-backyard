package auth

import (
	"strings"
	"testing"
)

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{"11文字は不可", strings.Repeat("a", 11), true},
		{"12文字は可", strings.Repeat("a", 12), false},
		{"空は不可", "", true},
		{"生成パスワードの例", "quiet-harbor-4172-mint", false},
		{"日本語は文字数で数える", "パスワードは十二文字です", false}, // 12文字（36バイト）
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePassword(tt.password)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidatePassword(%q) = %v, wantErr = %v", tt.password, err, tt.wantErr)
			}
		})
	}
}

// PHC 文字列が Design.md 6.3 のパラメータ（m=64MiB, t=3, p=4）で生成され、
// 平文と照合できること。
func TestHashPassword(t *testing.T) {
	const password = "quiet-harbor-4172-mint"

	phc, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	const wantPrefix = "$argon2id$v=19$m=65536,t=3,p=4$"
	if !strings.HasPrefix(phc, wantPrefix) {
		t.Errorf("PHC = %q, want prefix %q", phc, wantPrefix)
	}

	ok, err := VerifyPassword(password, phc)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Error("正しいパスワードが照合できなかった")
	}

	ok, err = VerifyPassword(password+"x", phc)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if ok {
		t.Error("誤ったパスワードが照合できてしまった")
	}
}

// 同じパスワードでもソルトが異なるため、毎回異なるハッシュになること。
func TestHashPasswordUsesRandomSalt(t *testing.T) {
	const password = "quiet-harbor-4172-mint"

	first, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	second, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if first == second {
		t.Error("同一のハッシュが2回生成された（ソルトが固定されている）")
	}
}

func TestHashPasswordRejectsShort(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Fatal("最小長未満のパスワードがハッシュ化された")
	}
}
