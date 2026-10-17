package v1

import (
	"net/http"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 自律実行を機能として切り替える（Delegation.md 1.3。pb-353）。

func delegationOn() *config.Live {
	return config.LiveWith(config.Row{Key: config.KeyDelegationEnabled, Value: "true"})
}

// delegationWritePaths は機能の関門を掛けた書き込みの口（routes.go）。
var delegationWritePaths = []string{
	"/api/v1/projects/pb/tickets/1/delegation",
	"/api/v1/projects/pb/tickets/1/delegation/approve",
	"/api/v1/projects/pb/tickets/1/delegation/reject",
	"/api/v1/projects/pb/tickets/1/delegation/pause",
	"/api/v1/projects/pb/tickets/1/delegation/resume",
	"/api/v1/projects/pb/tickets/1/delegation/withdraw",
	"/api/v1/projects/pb/tickets/1/plan",
	"/api/v1/projects/pb/tickets/1/plan/2/revert",
	"/api/v1/projects/pb/tickets/1/plan/2/object",
	"/api/v1/projects/pb/tickets/1/evaluation",
	"/api/v1/projects/pb/tickets/1/confirmation",
	"/api/v1/projects/pb/tickets/1/confirmation/01K0000000000000000000000A/answer",
	"/api/v1/projects/pb/tickets/1/handoff",
	"/api/v1/projects/pb/tickets/1/metrics/m1/values",
}

// 既定（無効）では、書き込みの口がすべて同じ文面の 403 を返す。
func TestDelegationWritesRejectedWhenDisabled(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	r := routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}, Settings: config.LiveDefaults()})

	for _, p := range delegationWritePaths {
		rec := postWithCookie(r, p, token, `{}`)
		if rec.Code != http.StatusForbidden {
			t.Errorf("POST %s = %d, want 403（body=%s）", p, rec.Code, rec.Body.String())
			continue
		}
		if msg := errorOf(t, rec).Message; msg != middleware.DelegationDisabledMessage {
			t.Errorf("POST %s の文面 = %q", p, msg)
		}
	}
}

// 有効なら関門を通り、無効の文面は返らない（その先の権限・存在の判定に進む）。
//
// **関門を外すと落ちることも確かめる対照**：上の試験と同じ口を、設定だけ変えて叩く。
func TestDelegationWritesPassGateWhenEnabled(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	r := routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}, Settings: delegationOn()})

	for _, p := range delegationWritePaths {
		rec := postWithCookie(r, p, token, `{}`)
		if rec.Code == http.StatusForbidden && errorOf(t, rec).Message == middleware.DelegationDisabledMessage {
			t.Errorf("POST %s が有効なのに無効の文面で拒まれた", p)
		}
	}
}

// 読み取りは無効でも関門を掛けない。
func TestDelegationReadsNotGated(t *testing.T) {
	q := newFake(t)
	token := validToken(q, `[]`)
	r := routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}, Settings: config.LiveDefaults()})

	for _, p := range []string{
		"/api/v1/projects/pb/tickets/1/delegation-status",
		"/api/v1/projects/pb/tickets/1/plans",
		"/api/v1/projects/pb/tickets/1/evaluations",
		"/api/v1/projects/pb/tickets/1/summary",
	} {
		rec := getWithCookie(r, p, token)
		if rec.Code == http.StatusForbidden && errorOf(t, rec).Message == middleware.DelegationDisabledMessage {
			t.Errorf("GET %s が無効の文面で拒まれた", p)
		}
	}
}

// GET /me の features.delegation は設定の実効値を写す（ApiDesign.md 4.1）。
func TestMeFeaturesDelegation(t *testing.T) {
	for _, tc := range []struct {
		name string
		live *config.Live
		want bool
	}{
		{"既定は無効", config.LiveDefaults(), false},
		{"有効にした", delegationOn(), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newFake(t)
			token := validToken(q, `[]`)
			r := routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}, Settings: tc.live})
			rec := getWithCookie(r, "/api/v1/me", token)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET /me = %d（body=%s）", rec.Code, rec.Body.String())
			}
			features, ok := viewOf(t, rec)["features"].(map[string]any)
			if !ok {
				t.Fatalf("features が無い: %s", rec.Body.String())
			}
			if features["delegation"] != tc.want {
				t.Errorf("features.delegation = %v, want %v", features["delegation"], tc.want)
			}
		})
	}
}

// enableInDB は「画面から有効にした」状態を作る。**LiveWith では土台に焼き込まれ、
// 保存後の引き直し（settingsSnapshot は Base から重ねる）でも有効のまま残る。**
func enableInDB(q *fakeQuerier, live *config.Live) {
	q.settings.rows = []gen.ListAppSettingsRow{{Key: config.KeyDelegationEnabled, Value: "true"}}
	live.ApplyRows([]config.Row{{Key: config.KeyDelegationEnabled, Value: "true"}})
}

// 終了していない委任があれば、無効化を 409 で拒み、何も書かない（ApiDesign.md 11.2）。
func TestDisableDelegationRejectedWhileUnended(t *testing.T) {
	for _, body := range []string{
		`{"items":[{"key":"delegation_enabled","value":"false"}]}`,
		`{"items":[{"key":"delegation_enabled","value":null}]}`,
	} {
		live := config.LiveDefaults()
		r, q, token := settingsRouter(t, live)
		enableInDB(q, live)
		q.settings.unendedDelegations = 2

		rec := putWithCookie(r, "/api/v1/admin/settings", token, body)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s: PUT = %d, want 409（body=%s）", body, rec.Code, rec.Body.String())
		}
		if msg := errorOf(t, rec).Message; !strings.Contains(msg, "2 件") {
			t.Errorf("%s: 文面に件数が無い: %q", body, msg)
		}
		if len(q.settings.upserted) != 0 || len(q.settings.deleted) != 0 {
			t.Errorf("%s: 拒んだのに書いた", body)
		}
	}
}

// 終了していない委任が無ければ無効にできる。有効にするのは委任があっても拒まない。
func TestToggleDelegationAllowed(t *testing.T) {
	t.Run("委任が無ければ無効にできる", func(t *testing.T) {
		live := config.LiveDefaults()
		r, q, token := settingsRouter(t, live)
		enableInDB(q, live)
		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"delegation_enabled","value":"false"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if live.DelegationEnabled() {
			t.Error("保存しても無効にならない")
		}
	})
	t.Run("有効にするのは拒まない", func(t *testing.T) {
		live := config.LiveDefaults()
		r, q, token := settingsRouter(t, live)
		q.settings.unendedDelegations = 3
		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"delegation_enabled","value":"true"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if !live.DelegationEnabled() {
			t.Error("保存しても有効にならない")
		}
	})
}

// 既定は無効（Delegation.md 1.3）。
func TestDelegationDefaultDisabled(t *testing.T) {
	if config.LiveDefaults().DelegationEnabled() {
		t.Fatal("delegation_enabled の既定が有効になっている")
	}
	d, ok := config.Lookup(config.KeyDelegationEnabled)
	if !ok || d.Layer != config.LayerRuntime || d.EnvKey != "PB_DELEGATION_ENABLED" {
		t.Errorf("定義 = %+v", d)
	}
}
