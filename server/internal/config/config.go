// Package config は実行時設定を環境変数から読む。
//
// 秘密（接続文字列・パスワード）は環境変数に直接置かず、`<KEY>_FILE` が指す
// ファイル経由で渡す。docker inspect や ps で見えないようにするため
// （DbDesign.md 3.2、Design.md 3.1「環境変数＋*_FILE 展開」）。
// 設定項目は deploy/base/env.example に列挙したものと一対一に対応する。
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config は PB の実行時設定。
type Config struct {
	Bind        string // PB_BIND        待受アドレス
	DatabaseURL string // PB_DATABASE_URL 接続文字列（pb_app）
	LogFormat   string // PB_LOG_FORMAT  log/slog の出力形式
	LogLevel    string // PB_LOG_LEVEL   log/slog の最低レベル

	// HealthShowVersion は PB_HEALTH_SHOW_VERSION。
	// GET /healthcheck にバージョンを含めるか（ApiDesign.md 2.11）。
	// 既定を false にするのは、未認証の呼び出し元への情報開示になるため。
	HealthShowVersion bool

	// CookieSecure は PB_COOKIE_SECURE。
	// pb_session / pb_csrf に Secure 属性を付けるか（Design.md 6.2.1 手順7
	// 「Secure(本番)」、6.6）。
	//
	// リクエストの TLS 有無から自動判定しない。リバースプロキシで TLS を
	// 終端する構成ではアプリに平文で届くため、自動判定は「HTTPS で公開して
	// いるのに Secure が付かない」を招く。既定は false（開発端末の http）。
	CookieSecure bool
}

const (
	defaultBind      = "0.0.0.0:8080"
	defaultLogFormat = "json"
	defaultLogLevel  = "info"
)

// logLevels は PB_LOG_LEVEL に指定できる値（Design.md 10.1）。
var logLevels = map[string]bool{"debug": true, "info": true, "warn": true, "error": true}

// Load は環境変数を読んで Config を組み立てる。
// DatabaseURL は必須で、未設定ならエラーを返す。
func Load() (Config, error) {
	dbURL, err := lookup("PB_DATABASE_URL")
	if err != nil {
		return Config{}, err
	}
	if dbURL == "" {
		return Config{}, errors.New("PB_DATABASE_URL または PB_DATABASE_URL_FILE を設定してください")
	}

	bind, err := lookup("PB_BIND")
	if err != nil {
		return Config{}, err
	}
	if bind == "" {
		bind = defaultBind
	}

	logFormat, err := lookup("PB_LOG_FORMAT")
	if err != nil {
		return Config{}, err
	}
	if logFormat == "" {
		logFormat = defaultLogFormat
	}

	logLevel, err := lookup("PB_LOG_LEVEL")
	if err != nil {
		return Config{}, err
	}
	if logLevel == "" {
		logLevel = defaultLogLevel
	}
	logLevel = strings.ToLower(logLevel)
	if !logLevels[logLevel] {
		return Config{}, fmt.Errorf("PB_LOG_LEVEL は debug / info / warn / error のいずれかを指定してください（%q）", logLevel)
	}

	showVersion, err := lookupBool("PB_HEALTH_SHOW_VERSION")
	if err != nil {
		return Config{}, err
	}

	cookieSecure, err := lookupBool("PB_COOKIE_SECURE")
	if err != nil {
		return Config{}, err
	}

	return Config{
		Bind:              bind,
		DatabaseURL:       dbURL,
		LogFormat:         logFormat,
		LogLevel:          logLevel,
		HealthShowVersion: showVersion,
		CookieSecure:      cookieSecure,
	}, nil
}

// lookupBool は真偽値の設定を読む。未設定なら false。
// 解釈できない値は既定へ倒さずエラーにする（設定の書き誤りを黙って無視しないため）。
func lookupBool(key string) (bool, error) {
	v, err := lookup(key)
	if err != nil {
		return false, err
	}
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("%s は true / false で指定してください（%q）", key, v)
	}
	return b, nil
}

// lookup は <key>_FILE が指すファイルの内容を優先して返し、
// 無ければ環境変数 <key> の値を返す。どちらも無ければ空文字を返す。
//
// ファイル経由を優先するのは、compose が両方を渡した場合に、より秘密を
// 漏らしにくい経路を採るため。読み取った値は末尾の改行のみを取り除く
// （Makefile の $(cat ...) と挙動を揃える）。
func lookup(key string) (string, error) {
	if path := os.Getenv(key + "_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("%s_FILE の読み込みに失敗した: %w", key, err)
		}
		return strings.TrimRight(string(b), "\r\n"), nil
	}
	return os.Getenv(key), nil
}
