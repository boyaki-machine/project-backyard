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
	"strings"
)

// Config は PB の実行時設定。
type Config struct {
	Bind        string // PB_BIND        待受アドレス
	DatabaseURL string // PB_DATABASE_URL 接続文字列（pb_app）
	LogFormat   string // PB_LOG_FORMAT  log/slog の出力形式
}

const (
	defaultBind      = "0.0.0.0:8080"
	defaultLogFormat = "json"
)

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

	return Config{Bind: bind, DatabaseURL: dbURL, LogFormat: logFormat}, nil
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
