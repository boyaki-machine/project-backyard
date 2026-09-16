package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeConfigFile は PB_CONFIG_FILE が指す YAML を書き、環境変数を向ける。
func writeConfigFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "pb.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("設定ファイルを書けない: %v", err)
	}
	t.Setenv(ConfigFileEnv, path)
	return path
}

// 優先順の全段を1つの表で確かめる（Design.md 10.3）。
//
// **通る側と落ちる側の両方を見る。** 判定を出す経路は片側だけ試すと、
// いつも同じ答えを返していても気づけない。
func TestResolvePrecedence(t *testing.T) {
	t.Run("設定ファイルは環境変数に勝つ", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log_level: warn\n")
		t.Setenv("PB_LOG_LEVEL", "error")

		set, err := Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		v, _ := set.Get(KeyLogLevel)
		if v.Value != "warn" || v.Source != SourceConfigFile {
			t.Errorf("log_level = %q（%s）, want warn（config_file）", v.Value, v.Source)
		}
	})

	t.Run("秘密のファイルは設定ファイルに勝つ", func(t *testing.T) {
		secret := filepath.Join(t.TempDir(), "level")
		if err := os.WriteFile(secret, []byte("debug\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log_level: warn\n")
		t.Setenv("PB_LOG_LEVEL_FILE", secret)

		set, err := Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		v, _ := set.Get(KeyLogLevel)
		if v.Value != "debug" || v.Source != SourceSecretFile {
			t.Errorf("log_level = %q（%s）, want debug（secret_file）", v.Value, v.Source)
		}
	})

	t.Run("書かれていないキーは既定へ落ちる", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log_level: warn\n")

		set, err := Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		// **ファイルが言及したキーだけを押さえる。** そうでないと、
		// ファイルを置いた瞬間に画面から何も変えられなくなる。
		v, _ := set.Get(KeyLogFormat)
		if v.Source != SourceDefault {
			t.Errorf("log_format の source = %s, want default", v.Source)
		}
	})
}

// ${環境変数名} の補間（Design.md 10.3）。
func TestResolveInterpolation(t *testing.T) {
	t.Run("環境変数を引く", func(t *testing.T) {
		t.Setenv("MY_DSN", "postgres://from-env")
		writeConfigFile(t, "database_url: ${MY_DSN}\n")

		set, err := Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		v, _ := set.Get(KeyDatabaseURL)
		if v.Value != "postgres://from-env" {
			t.Errorf("database_url = %q", v.Value)
		}
		// **出どころは設定ファイルである。** 値の出どころは「どの層が答えたか」で
		// あって、その層が内部で何を引いたかではない。
		if v.Source != SourceConfigFile {
			t.Errorf("source = %s, want config_file", v.Source)
		}
	})

	t.Run("未設定の環境変数は起動を失敗させる", func(t *testing.T) {
		os.Unsetenv("PB_NO_SUCH_VAR")
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log_level: ${PB_NO_SUCH_VAR}\n")

		// **空文字へ倒さない。** 書いてあるのに効いていない状態を黙って作らない。
		if _, err := Resolve(); err == nil {
			t.Fatal("未設定の環境変数を参照したのに誤りにならなかった")
		}
	})

	t.Run("$$ で $ を書ける", func(t *testing.T) {
		writeConfigFile(t, "database_url: 'postgres://u:p$$w@h/db'\n")

		set, err := Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if got := set.String(KeyDatabaseURL); got != "postgres://u:p$w@h/db" {
			t.Errorf("database_url = %q", got)
		}
	})
}

// 設定ファイルの誤りは起動時に知らせる。
func TestResolveRejectsBadConfigFile(t *testing.T) {
	t.Run("知らないキー", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log_levle: warn\n") // 綴り違い
		if _, err := Resolve(); err == nil {
			t.Fatal("知らないキーが通った")
		}
	})

	t.Run("入れ子", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log:\n  level: warn\n")
		if _, err := Resolve(); err == nil {
			t.Fatal("入れ子が通った")
		}
	})

	t.Run("値域に合わない値", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log_level: verbose\n")
		if _, err := Resolve(); err == nil {
			t.Fatal("値域外が通った")
		}
	})

	t.Run("YAML として壊れている", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		writeConfigFile(t, "log_level: [unclosed\n")
		if _, err := Resolve(); err == nil {
			t.Fatal("壊れた YAML が通った")
		}
	})

	t.Run("ファイルが無い", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		t.Setenv(ConfigFileEnv, filepath.Join(t.TempDir(), "missing.yaml"))
		if _, err := Resolve(); err == nil {
			t.Fatal("無いファイルを指しても通った")
		}
	})
}

// YAML の真偽値と数値も文字列として受け、正規化する。
func TestResolveYAMLScalars(t *testing.T) {
	t.Setenv("PB_DATABASE_URL", "postgres://x")
	// YAML の裸の true と、大文字の列挙
	writeConfigFile(t, "cookie_secure: true\nlog_level: WARN\n")

	set, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !set.Bool(KeyCookieSecure) {
		t.Error("cookie_secure が true にならない")
	}
	if got := set.String(KeyLogLevel); got != "warn" {
		t.Errorf("log_level = %q, want warn（小文字へ正規化）", got)
	}
}

// DB の層は上の層に負ける（Design.md 10.3）。
func TestOverlayDatabase(t *testing.T) {
	t.Run("既定のキーには重なる", func(t *testing.T) {
		out := OverlayDatabase(Defaults(), []Row{{Key: KeyLogLevel, Value: "debug"}})
		v, _ := out.Get(KeyLogLevel)
		if v.Value != "debug" || v.Source != SourceDatabase {
			t.Errorf("log_level = %q（%s）, want debug（database）", v.Value, v.Source)
		}
	})

	t.Run("環境変数で固定されたキーには重ならない", func(t *testing.T) {
		t.Setenv("PB_DATABASE_URL", "postgres://x")
		t.Setenv("PB_LOG_LEVEL", "error")
		base, err := Resolve()
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		out := OverlayDatabase(base, []Row{{Key: KeyLogLevel, Value: "debug"}})
		v, _ := out.Get(KeyLogLevel)
		if v.Value != "error" || v.Source != SourceEnv {
			t.Errorf("log_level = %q（%s）, want error（env）", v.Value, v.Source)
		}
	})

	t.Run("第1層のキーには重ならない", func(t *testing.T) {
		// **接続文字列を DB から読むことはない。** 直接 SQL で書かれても無視する。
		//
		// **bind はここから外れた**（pb-99 で第2層へ移した）。第1層に残るのは
		// **DB に到るために要るもの**だけである。
		out := OverlayDatabase(Defaults(), []Row{{Key: KeyDatabaseURL, Value: "postgres://x"}})
		v, _ := out.Get(KeyDatabaseURL)
		if v.Source == SourceDatabase {
			t.Errorf("database_url が DB 由来になった（%q）", v.Value)
		}
	})

	t.Run("bind の既定はこの端末に閉じる（pb-125）", func(t *testing.T) {
		// **何も設定しないまま起動したとき、平文ですべてのアドレスへ出さない**
		// （`Requirements.md` 10.10.2）。**コンテナは自分で `PB_BIND` を明示する。**
		v, _ := Defaults().Get(KeyBind)
		if v.Value != "127.0.0.1:8080" || v.Source != SourceDefault {
			t.Errorf("bind の既定 = %q（%s）, want 127.0.0.1:8080（default）", v.Value, v.Source)
		}
	})

	t.Run("bind は DB から重なる（pb-99）", func(t *testing.T) {
		out := OverlayDatabase(Defaults(), []Row{{Key: KeyBind, Value: "0.0.0.0:9999"}})
		v, _ := out.Get(KeyBind)
		if v.Value != "0.0.0.0:9999" || v.Source != SourceDatabase {
			t.Errorf("bind = %q（%s）, want 0.0.0.0:9999（database）", v.Value, v.Source)
		}
	})

	t.Run("知らないキーの行は無視する", func(t *testing.T) {
		// **古いバイナリへ戻したときに落ちないため。**
		out := OverlayDatabase(Defaults(), []Row{{Key: "future_setting", Value: "x"}})
		if _, ok := out.Get("future_setting"); ok {
			t.Error("知らないキーが Set に入った")
		}
	})

	t.Run("値域に合わない行は無視する", func(t *testing.T) {
		out := OverlayDatabase(Defaults(), []Row{{Key: KeyLogLevel, Value: "verbose"}})
		v, _ := out.Get(KeyLogLevel)
		if v.Source == SourceDatabase {
			t.Errorf("値域外の行が効いた（%q）", v.Value)
		}
	})
}

// Editable は層と出どころの組で決まる（ApiDesign.md 11.1）。
func TestEditable(t *testing.T) {
	cases := []struct {
		key    string
		source Source
		want   bool
	}{
		{KeyLogLevel, SourceDefault, true},
		{KeyLogLevel, SourceDatabase, true},
		{KeyLogLevel, SourceEnv, false},
		{KeyLogLevel, SourceConfigFile, false},
		{KeyLogLevel, SourceSecretFile, false},
		// **bind は第2層へ移した**（pb-99）。既定と DB なら編集できる。
		{KeyBind, SourceDefault, true},
		{KeyBind, SourceEnv, false},
		// 第1層はどの出どころでも編集できない。
		{KeyDatabaseURL, SourceSecretFile, false},
	}
	for _, tc := range cases {
		def, ok := Lookup(tc.key)
		if !ok {
			t.Fatalf("レジストリに %s が無い", tc.key)
		}
		if got := def.Editable(tc.source); got != tc.want {
			t.Errorf("%s（%s）: editable = %v, want %v", tc.key, tc.source, got, tc.want)
		}
	}
}

// Live は入れ替えられ、nil でも既定で答える。
func TestLive(t *testing.T) {
	live := LiveWith(Row{Key: KeyCookieSecure, Value: "true"})
	if !live.CookieSecure() {
		t.Error("LiveWith の上書きが効かない")
	}
	live.Replace(Defaults())
	if live.CookieSecure() {
		t.Error("Replace が効かない")
	}

	var nilLive *Live
	if nilLive.CookieSecure() {
		t.Error("nil Live が既定（false）を返さない")
	}
	if nilLive.LogLevel() == "" {
		t.Error("nil Live がログレベルの既定を返さない")
	}
}
