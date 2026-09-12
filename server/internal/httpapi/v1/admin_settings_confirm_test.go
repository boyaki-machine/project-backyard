package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// pendingRow は未確認の記録を1件作る。
func pendingRow(t *testing.T, previous map[string]*string, expiresIn time.Duration) gen.GetPendingSettingChangeRow {
	t.Helper()
	blob, err := json.Marshal(previous)
	if err != nil {
		t.Fatal(err)
	}
	return gen.GetPendingSettingChangeRow{
		ID:        "01K2F8QW3H7YRJ4M5N6P7Q8PND",
		Previous:  blob,
		ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(expiresIn), Valid: true},
		CreatedAt: pgtype.Timestamptz{Time: time.Now(), Valid: true},
	}
}

// TestUpdateSettingsRecordsPending は締め出されうる設定の保存が未確認を残すことを見る（pb-97）。
func TestUpdateSettingsRecordsPending(t *testing.T) {
	t.Run("tls_enabled を変えると未確認が残る", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"tls_enabled","value":"true"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d, body=%s", rec.Code, rec.Body.String())
		}
		if len(q.settings.pendingCreated) != 1 {
			t.Fatalf("未確認の記録 = %d件, want 1", len(q.settings.pendingCreated))
		}

		// **戻す値は DB の行から採る。** 行が無かったので null である——
		// 実効値（既定の false）を書くと、戻したときに行が残ってしまう。
		var previous map[string]*string
		if err := json.Unmarshal(q.settings.pendingCreated[0].Previous, &previous); err != nil {
			t.Fatal(err)
		}
		v, ok := previous[config.KeyTLSEnabled]
		if !ok {
			t.Fatalf("previous に tls_enabled が無い: %v", previous)
		}
		if v != nil {
			t.Errorf("previous = %q, want null（行が無かった）", *v)
		}
	})

	t.Run("戻す値は行の値である", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())
		q.settings.rows = []gen.ListAppSettingsRow{{Key: config.KeyCookieSecure, Value: "true"}}

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"cookie_secure","value":"false"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d, body=%s", rec.Code, rec.Body.String())
		}
		var previous map[string]*string
		if err := json.Unmarshal(q.settings.pendingCreated[0].Previous, &previous); err != nil {
			t.Fatal(err)
		}
		v := previous[config.KeyCookieSecure]
		if v == nil {
			t.Fatalf("previous = null, want \"true\"（%v）", previous)
		}
		if *v != "true" {
			t.Errorf("previous = %q, want \"true\"", *v)
		}
	})

	t.Run("危険でない設定では未確認を残さない", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"log_level","value":"debug"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d, body=%s", rec.Code, rec.Body.String())
		}
		if len(q.settings.pendingCreated) != 0 {
			t.Errorf("未確認を残している: %+v", q.settings.pendingCreated)
		}
	})

	t.Run("未確認が残っている間は断る", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())
		row := pendingRow(t, map[string]*string{config.KeyTLSEnabled: nil}, ConfirmWindow)
		q.settings.pending = &row

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"cookie_secure","value":"true"}]}`)
		if rec.Code != http.StatusConflict {
			t.Fatalf("PUT = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
	})
}

// TestConfirmSettings は 11.8 の確認の口を見る。
func TestConfirmSettings(t *testing.T) {
	t.Run("未確認が無ければ 404", func(t *testing.T) {
		r, _, token := settingsRouter(t, config.LiveDefaults())
		rec := postWithCookie(r, "/api/v1/admin/settings/confirm", token, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("POST = %d, want 404（body=%s）", rec.Code, rec.Body.String())
		}
	})

	t.Run("cookie_secure は届いただけで確定する", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())
		row := pendingRow(t, map[string]*string{config.KeyCookieSecure: nil}, ConfirmWindow)
		q.settings.pending = &row

		rec := postWithCookie(r, "/api/v1/admin/settings/confirm", token, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("POST = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
		if len(q.settings.pendingDeleted) != 1 {
			t.Errorf("記録を消していない: %v", q.settings.pendingDeleted)
		}
	})

	t.Run("tls_enabled が有効なのに平文で届いたら 409", func(t *testing.T) {
		// **設定を有効にした状態を作る。** 実効値が true でないと条件が効かない。
		live := config.LiveDefaults()
		live.Replace(config.OverlayDatabase(live.Base(),
			[]config.Row{{Key: config.KeyTLSEnabled, Value: "true"}}))

		r, q, token := settingsRouter(t, live)
		row := pendingRow(t, map[string]*string{config.KeyTLSEnabled: nil}, ConfirmWindow)
		q.settings.pending = &row

		// httptest のリクエストは r.TLS が nil（＝平文で届いた）。
		rec := postWithCookie(r, "/api/v1/admin/settings/confirm", token, "")
		if rec.Code != http.StatusConflict {
			t.Fatalf("POST = %d, want 409（body=%s）", rec.Code, rec.Body.String())
		}
		if len(q.settings.pendingDeleted) != 0 {
			t.Error("平文で届いた確認で確定してしまっている")
		}
	})

	t.Run("tls_enabled が無効なら平文で確定できる", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())
		row := pendingRow(t, map[string]*string{config.KeyTLSEnabled: nil}, ConfirmWindow)
		q.settings.pending = &row

		rec := postWithCookie(r, "/api/v1/admin/settings/confirm", token, "")
		if rec.Code != http.StatusNoContent {
			t.Fatalf("POST = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
	})
}

// TestSettingsGuardRevertsExpired は期限切れを戻すことを見る（pb-97）。
func TestSettingsGuardRevertsExpired(t *testing.T) {
	t.Run("行が無かったものは消して戻す", func(t *testing.T) {
		q := newFake(t)
		blob, _ := json.Marshal(map[string]*string{config.KeyTLSEnabled: nil})
		q.settings.expiredPending = []gen.ListExpiredPendingSettingChangesRow{{
			ID: "01K2F8QW3H7YRJ4M5N6P7Q8PND", Previous: blob,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
		}}
		row := pendingRow(t, map[string]*string{config.KeyTLSEnabled: nil}, -time.Minute)
		q.settings.pending = &row

		var changed int
		g := SettingsGuard{
			Tx: &fakeTxRunner{q: q}, Q: q, Settings: config.LiveDefaults(),
			OnChanged: func(*config.Set) { changed++ },
		}
		n, err := g.RevertExpired(context.Background())
		if err != nil {
			t.Fatalf("戻せない: %v", err)
		}
		if n != 1 {
			t.Fatalf("戻した件数 = %d, want 1", n)
		}
		// **行が無かったので DELETE で戻す**（UPSERT ではない）。
		if len(q.settings.deleted) != 1 || q.settings.deleted[0] != config.KeyTLSEnabled {
			t.Errorf("DELETE していない: %v", q.settings.deleted)
		}
		if len(q.settings.pendingDeleted) != 1 {
			t.Errorf("記録を消していない: %v", q.settings.pendingDeleted)
		}
		// **フックを呼ぶのが要点である。** 呼ばないと DB だけ戻り、待受は新しいまま。
		if changed != 1 {
			t.Errorf("OnChanged の呼び出し = %d回, want 1", changed)
		}
	})

	t.Run("値があったものは書き戻す", func(t *testing.T) {
		q := newFake(t)
		prev := "true"
		blob, _ := json.Marshal(map[string]*string{config.KeyCookieSecure: &prev})
		q.settings.expiredPending = []gen.ListExpiredPendingSettingChangesRow{{
			ID: "01K2F8QW3H7YRJ4M5N6P7Q8PND", Previous: blob,
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(-time.Minute), Valid: true},
		}}
		row := pendingRow(t, map[string]*string{config.KeyCookieSecure: &prev}, -time.Minute)
		q.settings.pending = &row

		g := SettingsGuard{Tx: &fakeTxRunner{q: q}, Q: q, Settings: config.LiveDefaults()}
		if _, err := g.RevertExpired(context.Background()); err != nil {
			t.Fatalf("戻せない: %v", err)
		}
		if len(q.settings.upserted) != 1 || q.settings.upserted[0].Value != "true" {
			t.Errorf("書き戻していない: %+v", q.settings.upserted)
		}
	})

	t.Run("起動時は期限に関わらず戻す", func(t *testing.T) {
		// **締め出された人が最初に試すのは再起動である**（改訂、2026-09-12）。
		// 期限内でも戻さないと、**その設定では起動に失敗する場合に永遠に
		// 戻らない**——プロセスが上がらないのでタイマも動かない。
		q := newFake(t)
		blob, _ := json.Marshal(map[string]*string{config.KeyTLSEnabled: nil})
		q.settings.expiredPending = []gen.ListExpiredPendingSettingChangesRow{{
			ID: "01K2F8QW3H7YRJ4M5N6P7Q8PND", Previous: blob,
			// **期限はまだ先である。**
			ExpiresAt: pgtype.Timestamptz{Time: time.Now().Add(5 * time.Minute), Valid: true},
		}}
		row := pendingRow(t, map[string]*string{config.KeyTLSEnabled: nil}, 5*time.Minute)
		q.settings.pending = &row

		g := SettingsGuard{Tx: &fakeTxRunner{q: q}, Q: q, Settings: config.LiveDefaults()}
		n, err := g.RevertAll(context.Background())
		if err != nil {
			t.Fatalf("戻せない: %v", err)
		}
		if n != 1 {
			t.Fatalf("戻した件数 = %d, want 1（期限内でも戻す）", n)
		}
		if len(q.settings.deleted) != 1 {
			t.Errorf("設定を戻していない: %v", q.settings.deleted)
		}
	})

	t.Run("期限が来ていなければ、タイマの点検では何もしない", func(t *testing.T) {
		q := newFake(t)
		g := SettingsGuard{Tx: &fakeTxRunner{q: q}, Q: q, Settings: config.LiveDefaults()}
		n, err := g.RevertExpired(context.Background())
		if err != nil || n != 0 {
			t.Fatalf("n=%d, err=%v", n, err)
		}
		if len(q.settings.deleted) != 0 || len(q.settings.upserted) != 0 {
			t.Error("何も起きていないのに設定を触っている")
		}
	})
}
