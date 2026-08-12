package auth

import (
	"context"
	"reflect"
	"testing"
)

func TestPrincipalContextRoundTrip(t *testing.T) {
	want := &Principal{ActorID: "01K2F8QW3H7YRJ4M5N6P7Q8R9S", ActorKind: ActorKindUser}

	ctx := NewPrincipalContext(context.Background(), want)
	if got := PrincipalFromContext(ctx); got != want {
		t.Errorf("PrincipalFromContext = %v, want %v", got, want)
	}
}

func TestPrincipalFromContextWhenAbsent(t *testing.T) {
	// 未認証のリクエストでは nil が返り、呼び出し側が nil 判定できること。
	if got := PrincipalFromContext(context.Background()); got != nil {
		t.Errorf("PrincipalFromContext = %v, want nil", got)
	}
}

func TestPrincipalPredicates(t *testing.T) {
	var nilP *Principal
	if nilP.IsUser() || nilP.IsAdministrator() {
		t.Error("nil のプリンシパルが真を返した")
	}

	op := &Principal{ActorKind: ActorKindUser, SystemRole: SystemRoleOperator}
	if !op.IsUser() {
		t.Error("IsUser = false, want true")
	}
	if op.IsAdministrator() {
		t.Error("オペレータが IsAdministrator = true を返した")
	}

	admin := &Principal{ActorKind: ActorKindUser, SystemRole: SystemRoleAdministrator}
	if !admin.IsAdministrator() {
		t.Error("IsAdministrator = false, want true")
	}

	agent := &Principal{ActorKind: ActorKindAgent}
	if agent.IsUser() {
		t.Error("エージェントが IsUser = true を返した")
	}
}

func TestAuditLabel(t *testing.T) {
	tests := []struct {
		name string
		p    *Principal
		want string
	}{
		{"表示名とメール", &Principal{DisplayName: "田中", Email: "tanaka@example.com"}, "田中 <tanaka@example.com>"},
		{"メール無し（エージェント）", &Principal{DisplayName: "reviewer-bot"}, "reviewer-bot"},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.p.AuditLabel(); got != tt.want {
				t.Errorf("AuditLabel = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDecodeScopes(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{"通常", `["ticket:read","ticket:write"]`, []string{"ticket:read", "ticket:write"}, false},
		{"空配列", `[]`, []string{}, false},
		{"列が空", ``, nil, false},
		{"壊れた JSON", `[`, nil, true},
		{"配列でない", `{"a":1}`, nil, true},
		{"文字列でない要素", `[1,2]`, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DecodeScopes([]byte(tt.raw))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("エラーを期待したが nil。got=%v", got)
				}
				// 壊れた値を空スライスに倒すと、絞ったはずのスコープが
				// 「絞り込みなし」に化ける。必ず nil を返すこと。
				if got != nil {
					t.Errorf("エラー時に %v を返した。nil であるべき", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeScopes: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DecodeScopes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestEncodeScopes(t *testing.T) {
	// nil でも列は NOT NULL なので、JSON の null ではなく空配列にする。
	got, err := EncodeScopes(nil)
	if err != nil {
		t.Fatalf("EncodeScopes: %v", err)
	}
	if string(got) != "[]" {
		t.Errorf("EncodeScopes(nil) = %q, want %q", got, "[]")
	}

	got, err = EncodeScopes([]string{"ticket:read"})
	if err != nil {
		t.Fatalf("EncodeScopes: %v", err)
	}
	if string(got) != `["ticket:read"]` {
		t.Errorf("EncodeScopes = %q", got)
	}

	// 往復して値が保たれること。
	back, err := DecodeScopes(got)
	if err != nil {
		t.Fatalf("DecodeScopes: %v", err)
	}
	if !reflect.DeepEqual(back, []string{"ticket:read"}) {
		t.Errorf("往復後 = %v", back)
	}
}
