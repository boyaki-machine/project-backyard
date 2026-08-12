package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PB_DATABASE_URL", "postgres://pb_app@127.0.0.1:5432/pb")

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Bind != defaultBind {
		t.Errorf("Bind = %q, want %q", got.Bind, defaultBind)
	}
	if got.LogFormat != defaultLogFormat {
		t.Errorf("LogFormat = %q, want %q", got.LogFormat, defaultLogFormat)
	}
	if got.LogLevel != defaultLogLevel {
		t.Errorf("LogLevel = %q, want %q", got.LogLevel, defaultLogLevel)
	}
	// 未認証の呼び出し元への情報開示になるため、既定は false（ApiDesign.md 2.11）。
	if got.HealthShowVersion {
		t.Error("HealthShowVersion の既定が true になっている")
	}
}

func TestLoadLogLevel(t *testing.T) {
	for _, level := range []string{"debug", "info", "warn", "error", "DEBUG"} {
		t.Setenv("PB_DATABASE_URL", "postgres://pb_app@127.0.0.1:5432/pb")
		t.Setenv("PB_LOG_LEVEL", level)

		got, err := Load()
		if err != nil {
			t.Fatalf("PB_LOG_LEVEL=%s: %v", level, err)
		}
		if want := strings.ToLower(level); got.LogLevel != want {
			t.Errorf("PB_LOG_LEVEL=%s: LogLevel = %q, want %q", level, got.LogLevel, want)
		}
	}
}

// 書き誤りを既定へ倒さずエラーにする。
func TestLoadRejectsUnknownLogLevel(t *testing.T) {
	t.Setenv("PB_DATABASE_URL", "postgres://pb_app@127.0.0.1:5432/pb")
	t.Setenv("PB_LOG_LEVEL", "verbose")

	if _, err := Load(); err == nil {
		t.Fatal("未知の PB_LOG_LEVEL がエラーにならなかった")
	}
}

func TestLoadHealthShowVersion(t *testing.T) {
	cases := []struct {
		value string
		want  bool
		ok    bool
	}{
		{"true", true, true},
		{"false", false, true},
		{"1", true, true},
		{"", false, true},
		{"yes", false, false}, // ParseBool が受け付けない
	}
	for _, tt := range cases {
		t.Setenv("PB_DATABASE_URL", "postgres://pb_app@127.0.0.1:5432/pb")
		t.Setenv("PB_HEALTH_SHOW_VERSION", tt.value)

		got, err := Load()
		if tt.ok && err != nil {
			t.Errorf("PB_HEALTH_SHOW_VERSION=%q: %v", tt.value, err)
			continue
		}
		if !tt.ok {
			if err == nil {
				t.Errorf("PB_HEALTH_SHOW_VERSION=%q: エラーにならなかった", tt.value)
			}
			continue
		}
		if got.HealthShowVersion != tt.want {
			t.Errorf("PB_HEALTH_SHOW_VERSION=%q: %v, want %v", tt.value, got.HealthShowVersion, tt.want)
		}
	}
}

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("PB_DATABASE_URL", "")

	if _, err := Load(); err == nil {
		t.Fatal("PB_DATABASE_URL が空でもエラーにならなかった")
	}
}

// *_FILE が環境変数より優先され、末尾の改行が取り除かれること。
func TestLoadFilePrecedence(t *testing.T) {
	const want = "postgres://pb_app@127.0.0.1:5432/pb?sslmode=disable"

	path := filepath.Join(t.TempDir(), "app_database_url")
	if err := os.WriteFile(path, []byte(want+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	t.Setenv("PB_DATABASE_URL", "postgres://from-env/pb")
	t.Setenv("PB_DATABASE_URL_FILE", path)

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.DatabaseURL != want {
		t.Errorf("DatabaseURL = %q, want %q", got.DatabaseURL, want)
	}
}

func TestLoadMissingFile(t *testing.T) {
	t.Setenv("PB_DATABASE_URL_FILE", filepath.Join(t.TempDir(), "not-exist"))

	if _, err := Load(); err == nil {
		t.Fatal("存在しない *_FILE を指してもエラーにならなかった")
	}
}
