package v1

import (
	"errors"
	"net/http"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

// ログアウトは 204 を返し、トークンを失効させ、Cookie を削除する
// （ApiDesign.md 3.2）。
func TestLogout(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)

	rec := authed(q, http.MethodPost, "/api/v1/auth/logout", token)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("204 に本文がある: %s", rec.Body.String())
	}

	if len(q.revoked) != 1 || q.revoked[0] != q.tokenRow.TokenID {
		t.Errorf("失効させたトークン = %v, want [%s]", q.revoked, q.tokenRow.TokenID)
	}

	for _, name := range []string{auth.SessionCookieName, auth.CSRFCookieName} {
		c := cookieOf(rec, name)
		if c == nil {
			t.Errorf("%s の削除指示が無い", name)
			continue
		}
		if c.MaxAge >= 0 || c.Value != "" {
			t.Errorf("%s が削除になっていない: MaxAge=%d Value=%q", name, c.MaxAge, c.Value)
		}
		if c.Path != sessionCookiePath {
			t.Errorf("%s の Path=%q（発行時と揃えないと消えない）", name, c.Path)
		}
	}

	if got := q.auditActions(); len(got) != 1 || got[0] != "logout" {
		t.Errorf("監査記録 = %v, want [logout]", got)
	}
}

// 失効に失敗したら 500。Cookie だけ消えて実際は有効、という状態を作らない。
func TestLogoutRevokeFailure(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	q.revokeErr = errors.New("update failed")

	rec := authed(q, http.MethodPost, "/api/v1/auth/logout", token)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if cookieOf(rec, auth.SessionCookieName) != nil {
		t.Error("失効できていないのに Cookie を削除した")
	}
}

// 未認証では 401（認証必須グループにある）。
func TestLogoutRequiresAuthentication(t *testing.T) {
	q := newFake(t)
	q.tokenErr = notFoundErr

	rec := call(router(q), http.MethodPost, "/api/v1/auth/logout", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if len(q.revoked) != 0 {
		t.Error("未認証のリクエストでトークンを失効させた")
	}
}
