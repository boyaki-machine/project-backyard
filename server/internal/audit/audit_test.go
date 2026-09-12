package audit

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// fakeQuerier は InsertAuditLog だけを受け止める Querier。
// DB を立てずに、組み立てられた行を検査するために使う。
type fakeQuerier struct {
	gen.Querier // 未使用のメソッドは埋め込みの nil のまま（呼べば panic して気づける）

	rows []gen.InsertAuditLogParams
	err  error
}

func (f *fakeQuerier) InsertAuditLog(_ context.Context, arg gen.InsertAuditLogParams) error {
	if f.err != nil {
		return f.err
	}
	f.rows = append(f.rows, arg)
	return nil
}

func TestRecordFromRequestWithPrincipal(t *testing.T) {
	p := &auth.Principal{
		ActorID:     "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
		ActorKind:   auth.ActorKindUser,
		DisplayName: "田中",
		Email:       "tanaka@example.com",
		TokenID:     "01K2F8QW3H7YRJ4M5N6P7Q8R9T",
	}

	r := httptest.NewRequest("POST", "/api/v1/projects", nil)
	r.RemoteAddr = "192.0.2.10:51234"
	r.Header.Set("User-Agent", "curl/8.7.1")
	ctx := apierr.NewRequestIDContext(r.Context(), "01K2F8QW3H7YRJ4M5N6P7Q8R9Z")
	r = r.WithContext(auth.NewPrincipalContext(ctx, p))

	q := &fakeQuerier{}
	err := FromRequest(r).Record(r.Context(), q, Entry{
		Action:     ProjectCreate,
		Result:     Success,
		TargetType: "project",
		TargetID:   "01K2F8QW3H7YRJ4M5N6P7Q8RAA",
		Detail:     map[string]any{"key": "my-app"},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}
	if len(q.rows) != 1 {
		t.Fatalf("行数 = %d, want 1", len(q.rows))
	}

	got := q.rows[0]
	if len(got.ID) != 26 {
		t.Errorf("id = %q（26文字の ULID であるべき）", got.ID)
	}
	if got.ActorID.String != p.ActorID {
		t.Errorf("actor_id = %q, want %q", got.ActorID.String, p.ActorID)
	}
	if got.ActorKind.String != auth.ActorKindUser {
		t.Errorf("actor_kind = %q", got.ActorKind.String)
	}
	// actor 削除後も誰の操作か分かるよう、表示名とメールを非正規化する。
	if got.ActorLabel.String != "田中 <tanaka@example.com>" {
		t.Errorf("actor_label = %q", got.ActorLabel.String)
	}
	if got.TokenID.String != p.TokenID {
		t.Errorf("token_id = %q", got.TokenID.String)
	}
	if got.Ip == nil || got.Ip.String() != "192.0.2.10" {
		t.Errorf("ip = %v, want 192.0.2.10", got.Ip)
	}
	if got.UserAgent.String != "curl/8.7.1" {
		t.Errorf("user_agent = %q", got.UserAgent.String)
	}
	if got.RequestID.String != "01K2F8QW3H7YRJ4M5N6P7Q8R9Z" {
		t.Errorf("request_id = %q", got.RequestID.String)
	}
	if got.Action != string(ProjectCreate) || got.Result != string(Success) {
		t.Errorf("action/result = %q/%q", got.Action, got.Result)
	}
	if string(got.Detail) != `{"key":"my-app"}` {
		t.Errorf("detail = %s", got.Detail)
	}
}

func TestRecordFromRequestWithoutPrincipal(t *testing.T) {
	// ログイン失敗のように、認証を通っていないリクエストからの記録。
	r := httptest.NewRequest("POST", "/api/v1/auth/login", nil)
	r.RemoteAddr = "203.0.113.7:40000"

	q := &fakeQuerier{}
	err := FromRequest(r).Record(r.Context(), q, Entry{
		Action: LoginFailure,
		Result: Failure,
		Detail: map[string]any{"email": "tanaka@example.com"},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	got := q.rows[0]
	if got.ActorID.Valid {
		t.Errorf("actor_id が NULL でない: %q", got.ActorID.String)
	}
	if got.ActorLabel.Valid {
		t.Errorf("actor_label が NULL でない: %q", got.ActorLabel.String)
	}
	// アクターが分からなくても発信元は残す（総当たりの検知に要る）。
	if got.Ip == nil || got.Ip.String() != "203.0.113.7" {
		t.Errorf("ip = %v", got.Ip)
	}
}

func TestFromCLI(t *testing.T) {
	q := &fakeQuerier{}
	err := FromCLI("pb admin create (CLI)").Record(context.Background(), q, Entry{
		Action:     UserCreate,
		Result:     Success,
		TargetType: "app_user",
		TargetID:   "01K2F8QW3H7YRJ4M5N6P7Q8R9S",
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	got := q.rows[0]
	// 端末の操作者にはアカウントが無いため actor_id は NULL、kind は system。
	if got.ActorID.Valid {
		t.Errorf("actor_id が NULL でない: %q", got.ActorID.String)
	}
	if got.ActorKind.String != auth.ActorKindSystem {
		t.Errorf("actor_kind = %q, want %q", got.ActorKind.String, auth.ActorKindSystem)
	}
	if got.ActorLabel.String != "pb admin create (CLI)" {
		t.Errorf("actor_label = %q", got.ActorLabel.String)
	}
	// HTTP リクエストが無いので ip / user_agent / request_id は NULL。
	if got.Ip != nil || got.UserAgent.Valid || got.RequestID.Valid {
		t.Errorf("HTTP 由来の列が埋まっている: ip=%v ua=%v rid=%v", got.Ip, got.UserAgent, got.RequestID)
	}
	// 誰を作ったかは target に残る。
	if got.TargetType.String != "app_user" || got.TargetID.String != "01K2F8QW3H7YRJ4M5N6P7Q8R9S" {
		t.Errorf("target = %q/%q", got.TargetType.String, got.TargetID.String)
	}
}

func TestRecordRejectsUnknownAction(t *testing.T) {
	// audit_log.action に CHECK 制約が無いため、綴り誤りをここで止める。
	q := &fakeQuerier{}
	err := FromCLI("test").Record(context.Background(), q, Entry{
		Action: Action("login.succeeded"), // 正しくは login.success
		Result: Success,
	})
	if err == nil {
		t.Fatal("未知のアクションが通った")
	}
	if len(q.rows) != 0 {
		t.Error("未知のアクションで INSERT が走った")
	}
}

func TestRecordRejectsBadResult(t *testing.T) {
	q := &fakeQuerier{}
	err := FromCLI("test").Record(context.Background(), q, Entry{
		Action: Logout,
		Result: Result("ok"), // CHECK は success / failure のみ
	})
	if err == nil {
		t.Fatal("不正な result が通った")
	}
	if len(q.rows) != 0 {
		t.Error("不正な result で INSERT が走った")
	}
}

func TestAllDocumentedActionsAreAccepted(t *testing.T) {
	// ApiDesign.md 2.10 が列挙する21件。定数の取りこぼしを検出する。
	// agent. の3件は Phase 2 で加わった（ApiDesign.md 4.5.6）——agent.register と
	// agent.update は 0019、agent.delete は手順26a である。
	// setting.update は pb-2（ApiDesign.md 11.2）。tls.certificate.* は pb-3（11.5 / 11.6）。
	documented := []Action{
		"login.success", "login.failure", "logout",
		"password.change", "password.reset",
		"token.issue", "token.revoke", "session.revoke",
		"user.create", "user.update", "user.delete", "role.change",
		"project.create", "project.archive", "permission.denied",
		"agent.register", "agent.update", "agent.delete",
		"setting.update",
		"tls.certificate.upload", "tls.certificate.delete",
	}
	if len(actions) != len(documented) {
		t.Errorf("アクション数 = %d, want %d（ApiDesign.md 2.10）", len(actions), len(documented))
	}
	for _, a := range documented {
		if !actions[a] {
			t.Errorf("%q が定数に無い", a)
		}
	}
}

func TestDetailDefaultsToEmptyObject(t *testing.T) {
	// detail は NOT NULL DEFAULT '{}'。nil を渡すと NULL になり制約違反になる。
	q := &fakeQuerier{}
	if err := FromCLI("test").Record(context.Background(), q, Entry{
		Action: Logout, Result: Success,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	if string(q.rows[0].Detail) != "{}" {
		t.Errorf("detail = %s, want {}", q.rows[0].Detail)
	}
	if !json.Valid(q.rows[0].Detail) {
		t.Error("detail が JSON として不正")
	}
}

func TestRecordOrLogSurvivesFailure(t *testing.T) {
	// 監査DBの一時障害でログインを止めない（RecordOrLog は panic せず戻る）。
	q := &fakeQuerier{err: errors.New("connection refused")}
	FromCLI("test").RecordOrLog(context.Background(), q, Entry{
		Action: LoginFailure, Result: Failure,
	})
}

func TestRecordOrLogIgnoresCanceledContext(t *testing.T) {
	// クライアントが接続を切っただけで login.failure の記録を落とせると、
	// 総当たりの痕跡を攻撃者側から消せてしまう。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	q := &fakeQuerier{}
	FromCLI("test").RecordOrLog(ctx, q, Entry{Action: LoginFailure, Result: Failure})

	if len(q.rows) != 1 {
		t.Fatalf("キャンセル済みコンテキストで記録されなかった（行数 = %d）", len(q.rows))
	}
}

func TestWithActorAndWithTokenDoNotMutate(t *testing.T) {
	base := FromCLI("base")
	derived := base.WithActor("01K2F8QW3H7YRJ4M5N6P7Q8R9S", auth.ActorKindUser, "田中").
		WithToken("01K2F8QW3H7YRJ4M5N6P7Q8R9T")

	if base.actorID != "" || base.actorLabel != "base" || base.tokenID != "" {
		t.Error("元の Recorder が書き換えられた")
	}
	if derived.actorID == "" || derived.actorLabel != "田中" || derived.tokenID == "" {
		t.Errorf("複製に反映されていない: %+v", derived)
	}
}

func TestClientIP(t *testing.T) {
	tests := []struct {
		name       string
		remoteAddr string
		want       string
	}{
		{"IPv4", "192.0.2.10:51234", "192.0.2.10"},
		{"IPv6", "[2001:db8::1]:443", "2001:db8::1"},
		// inet 列に同じ相手が2通りで入らないよう IPv4 に畳む。
		{"IPv4-mapped IPv6", "[::ffff:127.0.0.1]:8080", "127.0.0.1"},
		{"ポート無し", "192.0.2.10", "192.0.2.10"},
		{"解釈できない", "@", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/", nil)
			r.RemoteAddr = tt.remoteAddr

			addr := ClientIP(r)
			if tt.want == "" {
				if addr.IsValid() {
					t.Errorf("ClientIP = %v, want 無効", addr)
				}
				return
			}
			if !addr.IsValid() || addr.String() != tt.want {
				t.Errorf("ClientIP = %v, want %s", addr, tt.want)
			}
		})
	}
}

func TestXForwardedForIsIgnored(t *testing.T) {
	// 詐称可能なヘッダを信じると、監査ログの発信元を偽装できてしまう。
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.10:51234"
	r.Header.Set("X-Forwarded-For", "203.0.113.99")

	if got := ClientIP(r); got.String() != "192.0.2.10" {
		t.Errorf("ClientIP = %v, want 192.0.2.10（X-Forwarded-For を採らない）", got)
	}
}
