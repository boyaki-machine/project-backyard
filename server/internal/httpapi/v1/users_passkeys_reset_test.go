package v1

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
)

// パスキーの全削除（ApiDesign.md 6.10。pb-104）のテスト。
//
// **6.9（第2要素の解除）と同じ形で、触らないものを並べて確かめる。**
// 締め出しの原因ごとに口を分けてあるので、パスワード・セッション・第2要素に
// 触っていないことが、この口の仕様そのものである。

func callPasskeyReset(q *fakeQuerier) *httptest.ResponseRecorder {
	h, _ := newUserHandler(q)
	rec := httptest.NewRecorder()
	h.resetUserPasskeys(rec, adminUserReq(http.MethodPost,
		"/api/v1/admin/users/"+targetID+"/passkeys/reset", "", adminPrincipal(), "id", targetID))
	return rec
}

func TestResetUserPasskeysRemovesKeysAndChallenges(t *testing.T) {
	q := userFake(t)
	q.passkey.allDeletedRows = 2

	rec := callPasskeyReset(q)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.passkey.allDeletedFor) != 1 || q.passkey.allDeletedFor[0] != targetID {
		t.Errorf("パスキーの削除 = %v, want %s を1回", q.passkey.allDeletedFor, targetID)
	}
	// **未消費の登録の挑戦も捨てる**——乗っ取った人が登録の途中にいた場合に備える
	if len(q.passkey.challengesDeletedFor) != 1 || q.passkey.challengesDeletedFor[0].String != targetID {
		t.Errorf("挑戦の削除 = %+v, want %s を1回", q.passkey.challengesDeletedFor, targetID)
	}

	// **パスワードにもセッションにも第2要素にも触らない**（6.10）
	if len(q.credentialResets) != 0 {
		t.Errorf("パスワードを %d 回書き換えた", len(q.credentialResets))
	}
	if len(q.revoked) != 0 {
		t.Errorf("セッションを %d 回失効させた", len(q.revoked))
	}
	if len(q.mfa.allDeletedFor) != 0 {
		t.Errorf("第2要素を %d 回消した", len(q.mfa.allDeletedFor))
	}

	resets := pkAudits(q, "passkey.reset")
	if len(resets) != 1 {
		t.Fatalf("passkey.reset が %d 件, want 1", len(resets))
	}
	if got := pkAuditDetail(t, resets[0])["removed_passkeys"]; got != float64(2) {
		t.Errorf("removed_passkeys = %v, want 2", got)
	}
}

// TestResetUserPasskeysIsIdempotent は登録0件でも 204 を返し、監査に残すことを見る。
func TestResetUserPasskeysIsIdempotent(t *testing.T) {
	q := userFake(t) // passkey は空

	rec := callPasskeyReset(q)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
	}
	if len(pkAudits(q, "passkey.reset")) != 1 {
		t.Error("0件でも passkey.reset を残すべき（消そうとしたことが監査の対象である）")
	}
}

func TestResetUserPasskeysUnknownUser(t *testing.T) {
	q := userFake(t)
	q.detailUserErr = pgx.ErrNoRows

	rec := callPasskeyReset(q)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404（body=%s）", rec.Code, rec.Body.String())
	}
	if len(q.passkey.allDeletedFor) != 0 {
		t.Error("存在しないユーザーに対して削除を実行した")
	}
}
