// Package config は実行時設定を5段の優先順で解決する（Design.md 10.3）。
//
//	<KEY>_FILE ＞ PB_CONFIG_FILE の YAML ＞ 環境変数 <KEY> ＞ app_setting の行 ＞ 既定値
//
// **キー・型・既定値・検証規則の正本は registry.go である。** 設定を1件足すときに
// 触るのはあのファイルだけで、マイグレーションは要らない。
//
// 秘密（接続文字列・パスワード）は環境変数に直接置かず、`<KEY>_FILE` が指す
// ファイル経由で渡す。docker inspect や ps で見えないようにするためで
// （DbDesign.md 3.2）、compose の `secrets:` と K8s の Secret ボリュームが
// この形でファイルを配る。
//
// **pb.env はこのパッケージが読むファイルではない。** あれはシェルが読んで
// 環境変数へ export するもので（`run.sh` の `set -a`）、バイナリは開かない
// （Design.md 4.4）。バイナリが直接読む設定ファイルは PB_CONFIG_FILE の YAML だけである。
package config

import (
	"fmt"
	"os"
	"strings"
)

// Config は起動時に要る設定の写しである。
//
// **第2層（ログ・ヘルスチェック・Cookie）の値も持つが、これは起動時点の
// スナップショットにすぎない。** 実行中に画面から変えられるので、
// リクエストを捌く側は Live を見ること（Set を経由する）。
type Config struct {
	Bind        string // bind        待受アドレス
	DatabaseURL string // database_url 接続文字列（pb_app）
	LogFormat   string // log_format  log/slog の出力形式
	LogLevel    string // log_level   log/slog の最低レベル

	// HealthShowVersion は GET /healthcheck にバージョンを含めるか
	// （ApiDesign.md 2.11）。既定を false にするのは、未認証の呼び出し元への
	// 情報開示になるため。
	HealthShowVersion bool

	// CookieSecure は pb_session / pb_csrf に Secure 属性を付けるか
	// （Design.md 6.2.1 手順7「Secure(本番)」、6.6）。
	//
	// リクエストの TLS 有無から自動判定しない。リバースプロキシで TLS を
	// 終端する構成ではアプリに平文で届くため、自動判定は「HTTPS で公開して
	// いるのに Secure が付かない」を招く。既定は false（開発端末の http）。
	CookieSecure bool

	// Set は全設定の実効値と出どころ。**設定APIと設定画面の材料**であり、
	// Live の初期値になる。
	Set *Set
}

// 設定キー。**コード中からはこの定数で引く**——文字列を散らすと、
// レジストリのキーを変えたときに追えなくなる。
const (
	KeyDatabaseURL       = "database_url"
	KeyBind              = "bind"
	KeyLogFormat         = "log_format"
	KeyLogLevel          = "log_level"
	KeyHealthShowVersion = "health_show_version"
	KeyCookieSecure      = "cookie_secure"
	KeySecretKey         = "secret_key"
	KeyTLSEnabled        = "tls_enabled"
)

// Load は設定を解決して Config を組み立てる。
func Load() (Config, error) {
	set, err := Resolve()
	if err != nil {
		return Config{}, err
	}
	return FromSet(set), nil
}

// FromSet は Set から Config を写す。**Live が入れ替わったあとの値で
// 組み直すためにも使う。**
func FromSet(set *Set) Config {
	return Config{
		Bind:              set.String(KeyBind),
		DatabaseURL:       set.String(KeyDatabaseURL),
		LogFormat:         set.String(KeyLogFormat),
		LogLevel:          set.String(KeyLogLevel),
		HealthShowVersion: set.Bool(KeyHealthShowVersion),
		CookieSecure:      set.Bool(KeyCookieSecure),
		Set:               set,
	}
}

// lookupSecretFile は <KEY>_FILE が指すファイルの内容を返す。
//
// 読み取った値は末尾の改行のみを取り除く（Makefile の $(cat ...) と挙動を揃える）。
// **ファイルが指定されているのに読めなければ誤りにする**——既定へ倒すと、
// 秘密が渡っていないまま起動してしまう。
func lookupSecretFile(envKey string) (string, bool, error) {
	path := os.Getenv(envKey + "_FILE")
	if path == "" {
		return "", false, nil
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("%s_FILE の読み込みに失敗した: %w", envKey, err)
	}
	return strings.TrimRight(string(b), "\r\n"), true, nil
}

// lookupEnv は環境変数を読む。
//
// **空文字は「未設定」として扱う。** compose や shell で `PB_LOG_LEVEL=` と
// 書いたときに、空文字で検証を落とすより下の層へ落としたほうが素直である。
func lookupEnv(envKey string) (string, bool) {
	v := os.Getenv(envKey)
	if v == "" {
		return "", false
	}
	return v, true
}
