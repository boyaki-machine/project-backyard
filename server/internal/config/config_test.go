package config

import (
	"os"
	"path/filepath"
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
