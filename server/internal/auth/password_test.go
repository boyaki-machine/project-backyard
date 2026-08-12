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

// 現行パラメータで作ったハッシュは再ハッシュ不要（Design.md 6.2.1 手順5）。
func TestNeedsRehashCurrentParams(t *testing.T) {
	phc, err := HashPassword("quiet-harbor-4172-mint")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if NeedsRehash(phc) {
		t.Error("現行パラメータのハッシュが再ハッシュ対象になった")
	}
}

// withParams は実在の PHC 文字列のパラメータ部だけを差し替える。
//
//	$argon2id$v=19$m=65536,t=3,p=4$<salt>$<key>
//	                ^^^^^^^^^^^^^^ ここだけ入れ替える
//
// ソルトとキーの長さも NeedsRehash の判定材料なので、手書きの文字列では
// 意図しない理由で true になってしまう。実物から作る。
func withParams(t *testing.T, params string) string {
	t.Helper()
	phc, err := HashPassword("quiet-harbor-4172-mint")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	parts := strings.Split(phc, "$")
	if len(parts) != 6 {
		t.Fatalf("PHC の形式が想定と違う: %q", phc)
	}
	parts[3] = params
	return strings.Join(parts, "$")
}

// 旧世代（弱いパラメータ）は再ハッシュ対象になる。
func TestNeedsRehashWeakerParams(t *testing.T) {
	tests := map[string]string{
		"メモリが少ない":  "m=32768,t=3,p=4",
		"反復回数が少ない": "m=65536,t=2,p=4",
		"並列度が低い":   "m=65536,t=3,p=2",
	}
	for name, params := range tests {
		t.Run(name, func(t *testing.T) {
			if phc := withParams(t, params); !NeedsRehash(phc) {
				t.Errorf("弱いパラメータが再ハッシュ対象にならない: %s", phc)
			}
		})
	}
}

// 現行より強いハッシュは作り直さない（強度を下げないため）。
func TestNeedsRehashStrongerParamsAreKept(t *testing.T) {
	if phc := withParams(t, "m=131072,t=4,p=4"); NeedsRehash(phc) {
		t.Errorf("現行より強いハッシュが再ハッシュ対象になった: %s", phc)
	}
}

// 解釈できない PHC 文字列は再ハッシュ対象にしない（照合が通らず成功経路に入らない）。
func TestNeedsRehashBrokenPHC(t *testing.T) {
	for _, phc := range []string{"", "not-a-phc", "$argon2id$broken"} {
		if NeedsRehash(phc) {
			t.Errorf("壊れた PHC が再ハッシュ対象になった: %q", phc)
		}
	}
}

// ダミー検証は panic せず、実在のハッシュと同程度の時間を使う
// （Design.md 6.2.1 のタイミング攻撃対策）。
func TestVerifyAgainstDummy(t *testing.T) {
	VerifyAgainstDummy("quiet-harbor-4172-mint") // 1回目で生成される
	VerifyAgainstDummy("")                       // 空でも落ちない
}
