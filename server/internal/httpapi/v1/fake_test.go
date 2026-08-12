package v1

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// アクセスログとエラーログがテスト出力を埋めないよう、既定ロガーを捨てる。
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// testPassword は照合に成功する平文。Design.md 6.3 の最小長12を満たす。
const testPassword = "quiet-harbor-4172-mint"

const (
	testActorID  = "01K2F8QW3H7YRJ4M5N6P7Q8R9S"
	testIdentity = "01K2F8QW3H7YRJ4M5N6P7Q8R9I"
	testEmail    = "tanaka@example.com"
)

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func txt(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }

// fakeQuerier は手順5で使うクエリだけを実装した Querier。
//
// 埋め込んだ gen.Querier は nil のままなので、実装し忘れたメソッドを
// 呼べば panic して気づける。
type fakeQuerier struct {
	gen.Querier

	// login 経路
	loginRow  gen.FindLocalLoginByEmailRow
	loginErr  error // 未設定なら loginRow を返す。pgx.ErrNoRows で「未登録」
	loginedBy []string

	// 認証ミドルウェア
	tokenRow gen.FindAccessTokenByHashRow
	tokenErr error

	// プロフィールと権限
	profileRow  gen.GetActorProfileRow
	profileErr  error
	permissions map[string][]string
	memberships []gen.ListProjectMembershipsByActorRow

	// 書き込みの記録
	created      []gen.CreateAccessTokenParams
	failures     []gen.RecordLoginFailureParams
	resets       []string
	rehashes     []gen.RehashPasswordParams
	revoked      []string
	touchedLogin []string
	audits       []gen.InsertAuditLogParams

	// 故意に失敗させる
	createErr error
	resetErr  error
	failErr   error
	revokeErr error
}

func (q *fakeQuerier) FindLocalLoginByEmail(_ context.Context, email string) (gen.FindLocalLoginByEmailRow, error) {
	q.loginedBy = append(q.loginedBy, email)
	if q.loginErr != nil {
		return gen.FindLocalLoginByEmailRow{}, q.loginErr
	}
	return q.loginRow, nil
}

func (q *fakeQuerier) FindAccessTokenByHash(context.Context, string) (gen.FindAccessTokenByHashRow, error) {
	if q.tokenErr != nil {
		return gen.FindAccessTokenByHashRow{}, q.tokenErr
	}
	return q.tokenRow, nil
}

func (q *fakeQuerier) TouchAccessTokenLastUsed(context.Context, string) error { return nil }

func (q *fakeQuerier) GetActorProfile(_ context.Context, actorID string) (gen.GetActorProfileRow, error) {
	if q.profileErr != nil {
		return gen.GetActorProfileRow{}, q.profileErr
	}
	row := q.profileRow
	if row.ActorID == "" {
		row.ActorID = actorID
	}
	return row, nil
}

func (q *fakeQuerier) ListRolePermissions(_ context.Context, roleKey string) ([]string, error) {
	return q.permissions[roleKey], nil
}

func (q *fakeQuerier) ListProjectMembershipsByActor(context.Context, string) ([]gen.ListProjectMembershipsByActorRow, error) {
	return q.memberships, nil
}

func (q *fakeQuerier) CreateAccessToken(_ context.Context, arg gen.CreateAccessTokenParams) error {
	if q.createErr != nil {
		return q.createErr
	}
	q.created = append(q.created, arg)
	return nil
}

func (q *fakeQuerier) RecordLoginFailure(_ context.Context, arg gen.RecordLoginFailureParams) error {
	if q.failErr != nil {
		return q.failErr
	}
	q.failures = append(q.failures, arg)
	return nil
}

func (q *fakeQuerier) ResetLoginFailure(_ context.Context, identityID string) error {
	if q.resetErr != nil {
		return q.resetErr
	}
	q.resets = append(q.resets, identityID)
	return nil
}

func (q *fakeQuerier) RehashPassword(_ context.Context, arg gen.RehashPasswordParams) error {
	q.rehashes = append(q.rehashes, arg)
	return nil
}

func (q *fakeQuerier) RevokeAccessToken(_ context.Context, id string) error {
	if q.revokeErr != nil {
		return q.revokeErr
	}
	q.revoked = append(q.revoked, id)
	return nil
}

func (q *fakeQuerier) TouchLastLoginAt(_ context.Context, actorID string) error {
	q.touchedLogin = append(q.touchedLogin, actorID)
	return nil
}

func (q *fakeQuerier) InsertAuditLog(_ context.Context, arg gen.InsertAuditLogParams) error {
	q.audits = append(q.audits, arg)
	return nil
}

// auditActions は記録された監査アクションを順に返す。
func (q *fakeQuerier) auditActions() []string {
	out := make([]string, 0, len(q.audits))
	for _, a := range q.audits {
		out = append(out, a.Action)
	}
	return out
}

// loginRow は照合に成功する既定の行を返す。
func loginRow(t *testing.T) gen.FindLocalLoginByEmailRow {
	t.Helper()
	phc, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	return gen.FindLocalLoginByEmailRow{
		ActorID:      testActorID,
		DisplayName:  "田中",
		IsActive:     true,
		Email:        testEmail,
		SystemRole:   auth.SystemRoleAdministrator,
		Locale:       "ja",
		Timezone:     "Asia/Tokyo",
		IdentityID:   testIdentity,
		PasswordHash: phc,
	}
}

// newFake は既定の権限割り当てを持つフェイクを返す。
func newFake(t *testing.T) *fakeQuerier {
	t.Helper()
	return &fakeQuerier{
		loginRow: loginRow(t),
		permissions: map[string][]string{
			auth.SystemRoleAdministrator: {"project.view", "ticket.view", "user.manage"},
			auth.SystemRoleOperator:      {"project.view", "ticket.view"},
			"project_admin":              {"ticket.close"},
		},
	}
}

// routerWithDeps は v1.Mount を通した実際のルータを返す。
// ルート定義（Design.md 6.4.4）ごと検証したいので、ハンドラを直接呼ばない。
func routerWithDeps(deps Deps) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(r chi.Router) {
		Mount(r, deps)
	})
	return r
}

func router(q gen.Querier) http.Handler { return routerWithDeps(Deps{Queries: q}) }

// testCSRFToken はテストで使う CSRF トークン。値そのものに意味は無く、
// Cookie とヘッダで同じ値を送れば 2.4 の double-submit を満たす。
const testCSRFToken = "test-csrf-token-01K2F8QW"

// addCSRF は pb_csrf Cookie と X-PB-CSRF ヘッダを同じ値で付ける（ApiDesign.md 2.4）。
//
// Cookie 認証の状態変更系は CSRF ミドルウェアを通るため、ブラウザが行うのと
// 同じことをテスト側でも行う。安全なメソッドでは検証されないので害は無い。
func addCSRF(req *http.Request) {
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: testCSRFToken})
	req.Header.Set(auth.CSRFHeaderName, testCSRFToken)
}

// call はルータへ1リクエスト投げる。body が空なら本文なし。
func call(r http.Handler, method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// postLogin は JSON 本文でログインを叩く。
func postLogin(q gen.Querier, body string) *httptest.ResponseRecorder {
	return call(router(q), http.MethodPost, "/api/v1/auth/login", body)
}

// errorOf は 2.5 形式の応答からエラー部を取り出す。
type apiError struct {
	Code          string `json:"code"`
	Message       string `json:"message"`
	RetryAfterSec int    `json:"retry_after_sec"`
	Details       []struct {
		Field string `json:"field"`
		Code  string `json:"code"`
	} `json:"details"`
}

func errorOf(t *testing.T, rec *httptest.ResponseRecorder) apiError {
	t.Helper()
	var body struct {
		Error apiError `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が 2.5 の JSON 形式でない: %v (%s)", err, rec.Body.String())
	}
	return body.Error
}

// viewOf は成功応答（3.1 / 4.1）を読む。
func viewOf(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が JSON でない: %v (%s)", err, rec.Body.String())
	}
	return body
}

// cookieOf は Set-Cookie から目的の Cookie を取り出す。無ければ nil。
func cookieOf(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range (&http.Response{Header: rec.Header()}).Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// notFoundErr は「該当なし」を表す。
var notFoundErr = pgx.ErrNoRows

func pgBool(b bool) pgtype.Bool { return pgtype.Bool{Bool: b, Valid: true} }

// pgNull は NULL を表す pgtype.Text。
func pgNull() pgtype.Text { return pgtype.Text{} }
