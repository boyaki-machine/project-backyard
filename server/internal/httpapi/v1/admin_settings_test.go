package v1

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// settingsRouter は設定APIを叩けるルータと fake を返す。
func settingsRouter(t *testing.T, live *config.Live) (http.Handler, *fakeQuerier, string) {
	t.Helper()
	q := newFake(t)
	grantSystemSettings(q)
	token := validToken(q, "[]")
	tx := &fakeTxRunner{q: q}
	r := routerWithDeps(Deps{Queries: q, Tx: tx, Settings: live})
	return r, q, token
}

// grantSystemSettings は administrator に system.settings を足す。
//
// **共有フェイクの既定へは足さない。** TestLoginSuccess と TestMeReturnsSessionView が
// administrator の実効権限を件数で検査しているため、既定を増やすとそちらが落ちる。
// 本番のシード（0010）では administrator が全権限を持つので、ここで足すのが実物に近い。
func grantSystemSettings(q *fakeQuerier) {
	q.permissions[auth.SystemRoleAdministrator] =
		append(q.permissions[auth.SystemRoleAdministrator], "system.settings")
}

// getSettings は一覧を取り、items をキーで引ける形にして返す。
func getSettings(t *testing.T, r http.Handler, token string) (map[string]map[string]any, map[string]any) {
	t.Helper()
	rec := getWithCookie(r, "/api/v1/admin/settings", token)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/settings = %d, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items          []map[string]any `json:"items"`
		ConfigFilePath *string          `json:"config_file_path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	byKey := make(map[string]map[string]any, len(body.Items))
	for _, item := range body.Items {
		byKey[item["key"].(string)] = item
	}
	return byKey, map[string]any{"config_file_path": body.ConfigFilePath}
}

// 一覧は層と出どころを返し、秘密の値は返さない（ApiDesign.md 11.1）。
func TestListSettings(t *testing.T) {
	r, q, token := settingsRouter(t, config.LiveDefaults())
	q.settings.rows = []gen.ListAppSettingsRow{{
		Key:                  config.KeyLogLevel,
		Value:                "debug",
		UpdatedAt:            ts(time.Date(2026, 9, 11, 4, 10, 0, 0, time.UTC)),
		UpdatedBy:            txt(testActorID),
		UpdatedByKind:        txt(auth.ActorKindUser),
		UpdatedByDisplayName: txt("田中"),
	}}

	items, _ := getSettings(t, r, token)

	t.Run("DB の行が実効値になり、誰がいつ変えたかが付く", func(t *testing.T) {
		got := items[config.KeyLogLevel]
		if got["value"] != "debug" || got["source"] != "database" {
			t.Errorf("log_level = %v（%v）", got["value"], got["source"])
		}
		if got["editable"] != true {
			t.Error("log_level が editable でない")
		}
		if got["updated_at"] == nil {
			t.Error("updated_at が空")
		}
		by, ok := got["updated_by"].(map[string]any)
		if !ok || by["display_name"] != "田中" {
			t.Errorf("updated_by = %v", got["updated_by"])
		}
	})

	t.Run("秘密は value を返さない", func(t *testing.T) {
		got := items[config.KeyDatabaseURL]
		if got["secret"] != true {
			t.Error("database_url の secret が真でない")
		}
		if got["value"] != nil {
			t.Errorf("秘密の value が返っている: %v", got["value"])
		}
		if got["editable"] != false {
			t.Error("第1層が editable になっている")
		}
	})

	t.Run("env_key は出どころに関わらず必ず返る", func(t *testing.T) {
		// **変更できない値について「ではどこで変えるのか」を画面が言えるように。**
		for key, item := range items {
			if item["env_key"] == nil || item["env_key"] == "" {
				t.Errorf("%s の env_key が空", key)
			}
		}
	})

	t.Run("列挙は allowed を返し、それ以外は null", func(t *testing.T) {
		if items[config.KeyLogLevel]["allowed"] == nil {
			t.Error("log_level に allowed が無い")
		}
		if items[config.KeyCookieSecure]["allowed"] != nil {
			t.Error("真偽値に allowed が付いている")
		}
	})

	t.Run("第2層は再起動が要らない", func(t *testing.T) {
		if items[config.KeyLogLevel]["restart_required"] != false {
			t.Error("log_level に再起動が要るとある")
		}
		if items[config.KeyBind]["restart_required"] != true {
			t.Error("bind に再起動が要らないとある")
		}
	})
}

// 環境変数で固定されている項目は編集できない（ApiDesign.md 11.1）。
func TestListSettingsPinnedByEnv(t *testing.T) {
	t.Setenv("PB_DATABASE_URL", "postgres://x")
	t.Setenv("PB_LOG_LEVEL", "error")
	set, err := config.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	r, q, token := settingsRouter(t, config.NewLive(set))
	// **DB に行があっても、環境変数が勝つので効かない。**
	q.settings.rows = []gen.ListAppSettingsRow{{Key: config.KeyLogLevel, Value: "debug"}}

	items, _ := getSettings(t, r, token)
	got := items[config.KeyLogLevel]
	if got["value"] != "error" || got["source"] != "env" {
		t.Errorf("log_level = %v（%v）, want error（env）", got["value"], got["source"])
	}
	if got["editable"] != false {
		t.Error("環境変数で固定されているのに editable になっている")
	}
	// 行があっても実効値でなければ「誰がいつ」を返さない。
	if got["updated_by"] != nil {
		t.Errorf("効いていない行の updated_by が返っている: %v", got["updated_by"])
	}
}

// 保存は配列で受け、変わるものだけを書く（ApiDesign.md 11.2）。
func TestUpdateSettings(t *testing.T) {
	t.Run("書いた値が upsert される", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"log_level","value":"debug"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d, body=%s", rec.Code, rec.Body.String())
		}
		if len(q.settings.upserted) != 1 {
			t.Fatalf("upsert = %d件, want 1", len(q.settings.upserted))
		}
		if got := q.settings.upserted[0]; got.Key != config.KeyLogLevel || got.Value != "debug" {
			t.Errorf("upsert = %+v", got)
		}
		if q.settings.upserted[0].UpdatedBy.String != testActorID {
			t.Error("updated_by に操作者が入っていない")
		}
	})

	t.Run("value が null なら行を消す", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())
		q.settings.rows = []gen.ListAppSettingsRow{{Key: config.KeyLogLevel, Value: "debug"}}

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"log_level","value":null}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d, body=%s", rec.Code, rec.Body.String())
		}
		if len(q.settings.deleted) != 1 || q.settings.deleted[0] != config.KeyLogLevel {
			t.Errorf("delete = %v", q.settings.deleted)
		}
	})

	t.Run("既に既定なら何も書かない", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"log_level","value":null}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d", rec.Code)
		}
		if len(q.settings.deleted) != 0 || len(q.settings.upserted) != 0 {
			t.Errorf("既定のままなのに書いた（delete=%v upsert=%v）",
				q.settings.deleted, q.settings.upserted)
		}
	})

	t.Run("真偽値と列挙は正規化して書く", func(t *testing.T) {
		r, q, token := settingsRouter(t, config.LiveDefaults())

		rec := putWithCookie(r, "/api/v1/admin/settings", token,
			`{"items":[{"key":"cookie_secure","value":"1"},{"key":"log_level","value":"WARN"}]}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("PUT = %d, body=%s", rec.Code, rec.Body.String())
		}
		want := map[string]string{config.KeyCookieSecure: "true", config.KeyLogLevel: "warn"}
		for _, got := range q.settings.upserted {
			if want[got.Key] != got.Value {
				t.Errorf("%s = %q, want %q", got.Key, got.Value, want[got.Key])
			}
		}
	})
}

// 落ちる側（ApiDesign.md 11.2）。
func TestUpdateSettingsRejects(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"空の items は 422", `{"items":[]}`, http.StatusUnprocessableEntity},
		{"知らないキーは 422", `{"items":[{"key":"nope","value":"x"}]}`, http.StatusUnprocessableEntity},
		{"値域外は 422", `{"items":[{"key":"log_level","value":"verbose"}]}`, http.StatusUnprocessableEntity},
		{"真偽値でない値は 422", `{"items":[{"key":"cookie_secure","value":"maybe"}]}`, http.StatusUnprocessableEntity},
		{"同じキーを2回は 422", `{"items":[{"key":"log_level","value":"warn"},{"key":"log_level","value":"debug"}]}`, http.StatusUnprocessableEntity},
		// **第1層は 409。** 値の誤りではなく、画面から変えられないという状態の衝突である。
		{"第1層は 409", `{"items":[{"key":"bind","value":"0.0.0.0:9999"}]}`, http.StatusConflict},
		{"秘密も 409", `{"items":[{"key":"database_url","value":"postgres://y"}]}`, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, q, token := settingsRouter(t, config.LiveDefaults())
			rec := putWithCookie(r, "/api/v1/admin/settings", token, tc.body)
			if rec.Code != tc.want {
				t.Errorf("PUT = %d, want %d（body=%s）", rec.Code, tc.want, rec.Body.String())
			}
			if len(q.settings.upserted) != 0 || len(q.settings.deleted) != 0 {
				t.Error("弾いたのに書き込みが走った")
			}
		})
	}
}

// 環境変数で固定された第2層は 409 で、環境変数名を message に含める。
func TestUpdateSettingsPinnedByEnvConflicts(t *testing.T) {
	t.Setenv("PB_DATABASE_URL", "postgres://x")
	t.Setenv("PB_LOG_LEVEL", "error")
	set, err := config.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	r, q, token := settingsRouter(t, config.NewLive(set))

	rec := putWithCookie(r, "/api/v1/admin/settings", token,
		`{"items":[{"key":"log_level","value":"debug"}]}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("PUT = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}
	// **どこを直せばよいかを誤りが言えなければ、409 は行き止まりになる。**
	if msg := errorOf(t, rec).Message; !strings.Contains(msg, "PB_LOG_LEVEL") {
		t.Errorf("message に環境変数名が無い: %q", msg)
	}
	if len(q.settings.upserted) != 0 {
		t.Error("固定されているのに書いた")
	}
}

// 保存が成功すると Live が入れ替わり、次のリクエストから効く（ApiDesign.md 11.3）。
func TestUpdateSettingsAppliesImmediately(t *testing.T) {
	live := config.LiveDefaults()
	r, q, token := settingsRouter(t, live)

	if live.CookieSecure() {
		t.Fatal("前提：既定は false のはず")
	}

	// **フェイクは書いた値を rows に反映しないので、保存後の引き直しで
	// 見えるように仕込んでおく。** 実物では同じトランザクションの結果を読む。
	q.settings.rows = []gen.ListAppSettingsRow{{Key: config.KeyCookieSecure, Value: "true"}}

	rec := putWithCookie(r, "/api/v1/admin/settings", token,
		`{"items":[{"key":"cookie_secure","value":"true"}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT = %d, body=%s", rec.Code, rec.Body.String())
	}
	if !live.CookieSecure() {
		t.Error("保存しても Live に反映されていない")
	}
}

// 既定に戻すと、応答の実効値も既定へ戻る。
//
// **実サーバ検証で見つけた退行の回帰試験である**（pb-2、2026-09-11）。行は
// 消えていたのに、応答は source=database のままだった——**重ねる土台に前回の
// 重ね結果を使っていた**ためで、OverlayDatabase は足すだけなので消えた行の
// 影響が残った。土台は必ず Live.Base（DB を含まない Set）から取る。
func TestUpdateSettingsResetReflectsInResponse(t *testing.T) {
	live := config.LiveDefaults()
	r, q, token := settingsRouter(t, live)

	// まず DB 由来にする。
	if rec := putWithCookie(r, "/api/v1/admin/settings", token,
		`{"items":[{"key":"log_level","value":"debug"}]}`); rec.Code != http.StatusOK {
		t.Fatalf("1回目の PUT = %d, body=%s", rec.Code, rec.Body.String())
	}
	items, _ := getSettings(t, r, token)
	if items[config.KeyLogLevel]["source"] != "database" {
		t.Fatalf("前提：DB 由来になっていない（%v）", items[config.KeyLogLevel]["source"])
	}

	// 既定へ戻す。
	rec := putWithCookie(r, "/api/v1/admin/settings", token,
		`{"items":[{"key":"log_level","value":null}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("2回目の PUT = %d, body=%s", rec.Code, rec.Body.String())
	}
	if len(q.settings.deleted) != 1 {
		t.Fatalf("delete = %v", q.settings.deleted)
	}

	var body struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答を読めない: %v", err)
	}
	for _, item := range body.Items {
		if item["key"] != config.KeyLogLevel {
			continue
		}
		def, _ := config.Lookup(config.KeyLogLevel)
		if item["source"] != "default" {
			t.Errorf("source = %v, want default", item["source"])
		}
		if item["value"] != def.Default {
			t.Errorf("value = %v, want %q", item["value"], def.Default)
		}
		if item["updated_by"] != nil {
			t.Errorf("既定に戻したのに updated_by が残っている: %v", item["updated_by"])
		}
	}

	// **Live も戻っていること。** 次のリクエストが古い値で動かないように。
	if live.LogLevel() != "info" {
		t.Errorf("Live の log_level = %q, want info", live.LogLevel())
	}
}

// system.settings を持たない人は触れない（Design.md 6.4.4）。
func TestSettingsRequiresPermission(t *testing.T) {
	q := newFake(t)
	grantSystemSettings(q)
	token := validToken(q, "[]")
	// オペレータは system.settings を持たない（administrator にだけ足した）。
	q.tokenRow.SystemRole = txt(auth.SystemRoleOperator)
	r := routerWithDeps(Deps{Queries: q, Tx: &fakeTxRunner{q: q}, Settings: config.LiveDefaults()})

	if rec := getWithCookie(r, "/api/v1/admin/settings", token); rec.Code != http.StatusForbidden {
		t.Errorf("GET = %d, want 403", rec.Code)
	}
	rec := putWithCookie(r, "/api/v1/admin/settings", token,
		`{"items":[{"key":"log_level","value":"debug"}]}`)
	if rec.Code != http.StatusForbidden {
		t.Errorf("PUT = %d, want 403", rec.Code)
	}
}
