package auth

import (
	"context"
	"reflect"
	"testing"
	"time"
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

// ── FreshPermissions（Design.md 6.4.5、手順6b） ──────────

func TestFreshPermissions(t *testing.T) {
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time {
		t := now.Add(d)
		return &t
	}

	tests := []struct {
		name      string
		p         *Principal
		want      []string
		wantFresh bool
	}{
		{
			name:      "キャッシュが無い",
			p:         &Principal{},
			wantFresh: false,
		},
		{
			name: "書いた直後",
			p: &Principal{
				CachedPermissions:   []string{"ticket.view"},
				PermissionsCachedAt: at(0),
			},
			want:      []string{"ticket.view"},
			wantFresh: true,
		},
		{
			name: "TTL の直前",
			p: &Principal{
				CachedPermissions:   []string{"ticket.view"},
				PermissionsCachedAt: at(-PermissionCacheTTL + time.Second),
			},
			want:      []string{"ticket.view"},
			wantFresh: true,
		},
		{
			name: "TTL ちょうどで期限切れ",
			p: &Principal{
				CachedPermissions:   []string{"ticket.view"},
				PermissionsCachedAt: at(-PermissionCacheTTL),
			},
			wantFresh: false,
		},
		{
			// 権限0件と「キャッシュが無い」は別物。0件のキャッシュは有効である。
			name: "権限0件のキャッシュ",
			p: &Principal{
				CachedPermissions:   []string{},
				PermissionsCachedAt: at(0),
			},
			want:      []string{},
			wantFresh: true,
		},
		{
			// 無効化は2列を同時に消すので起きないはずだが、片方だけ
			// 残っていたら計算し直す側へ倒す。
			name: "時刻だけある",
			p: &Principal{
				PermissionsCachedAt: at(0),
			},
			wantFresh: false,
		},
		{
			// DBの時計がアプリより進んでいる場合。有効に見えるだけで破綻しない。
			name: "未来の時刻",
			p: &Principal{
				CachedPermissions:   []string{"ticket.view"},
				PermissionsCachedAt: at(time.Minute),
			},
			want:      []string{"ticket.view"},
			wantFresh: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, fresh := tt.p.FreshPermissions(now)
			if fresh != tt.wantFresh {
				t.Fatalf("fresh = %v, want %v", fresh, tt.wantFresh)
			}
			if fresh && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got = %v, want %v", got, tt.want)
			}
		})
	}
}

// nil レシーバでも落ちない（未認証の経路から呼ばれても安全にする）。
func TestFreshPermissionsNilPrincipal(t *testing.T) {
	var p *Principal
	if _, fresh := p.FreshPermissions(time.Now()); fresh {
		t.Error("nil のプリンシパルがキャッシュを持っていることになっている")
	}
}

// ── 委譲（Design.md 6.5、0019）──────────────────────────────

// TestAuthzActorIDDelegatesToOwner は「誰のロールを読むか」を測る。
//
// **ActorID と AuthzActorID を取り違えると、監査がエージェントではなく
// 所有者の名前で残る**（あるいはその逆で、権限が0件になる）。両方を見る。
func TestAuthzActorIDDelegatesToOwner(t *testing.T) {
	agent := &Principal{
		ActorID:      "01AGENT0000000000000000000",
		ActorKind:    ActorKindAgent,
		OwnerActorID: "01OWNER0000000000000000000",
	}
	if got := agent.AuthzActorID(); got != "01OWNER0000000000000000000" {
		t.Errorf("AuthzActorID = %q, want 所有者", got)
	}
	// **監査と書き手はエージェント自身のまま。**
	if agent.ActorID != "01AGENT0000000000000000000" {
		t.Errorf("ActorID = %q, want エージェント自身", agent.ActorID)
	}
	if !agent.IsAgent() {
		t.Error("IsAgent = false")
	}

	human := &Principal{ActorID: "01USER00000000000000000000", ActorKind: ActorKindUser}
	if got := human.AuthzActorID(); got != "01USER00000000000000000000" {
		t.Errorf("人間の AuthzActorID = %q, want ActorID と同じ", got)
	}
	if human.IsAgent() {
		t.Error("人間で IsAgent = true")
	}

	// nil でも落ちない（呼び出し側が種別で分岐しなくて済む前提）。
	var nilP *Principal
	if got := nilP.AuthzActorID(); got != "" {
		t.Errorf("nil の AuthzActorID = %q, want 空", got)
	}
}

// TestAgentEffectivePermissionsAreOwnersNarrowedByScopes は 6.5 の式を測る。
//
//	実効権限 = ( 所有者のシステムロール ∪ 所有者のプロジェクトロール ) ∩ スコープ
func TestAgentEffectivePermissionsAreOwnersNarrowedByScopes(t *testing.T) {
	// 所有者は operator（project.view / ticket.view）＋ project_member
	// （ticket.create / comment.create）を持つとする。
	ownerSystem := []string{"project.view", "ticket.view"}
	ownerProject := []string{"ticket.create", "comment.create"}
	// エージェントの既定スコープには doc.view が入るが、所有者が持たない。
	scopes := []string{"project.view", "ticket.view", "ticket.create", "doc.view"}

	got := EffectivePermissions(ownerSystem, ownerProject, scopes)
	want := []string{"project.view", "ticket.create", "ticket.view"}
	if len(got) != len(want) {
		t.Fatalf("実効権限 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("実効権限 = %v, want %v", got, want)
		}
	}
	// **所有者が持たない権限はスコープに書いても付かない**（縮小のみ）。
	for _, p := range got {
		if p == "doc.view" {
			t.Error("所有者が持たない doc.view が付いている")
		}
	}
	// **comment.create はスコープに無いので落ちる。**
	for _, p := range got {
		if p == "comment.create" {
			t.Error("スコープに無い comment.create が残っている")
		}
	}
}
