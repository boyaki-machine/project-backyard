// pb serve — APIサーバの起動（Design.md 4.1）。
//
// 手順4b の時点では /healthcheck 以外のエンドポイントを公開しておらず、
// ミドルウェア連鎖（request_id → アクセスログ → 認証）と ApiDesign.md 2.5
// 形式の 404 だけを返す。ログインは手順5で入る。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
)

// HTTP サーバのタイムアウト。設計文書に規定が無いため実装側の既定として置く。
// 読み取りヘッダのタイムアウトは、接続を掴んだまま送らないクライアントを切るために要る。
const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 60 * time.Second
	idleTimeout       = 120 * time.Second

	// 停止時に処理中のリクエストを待つ上限。
	shutdownTimeout = 15 * time.Second
)

func serve(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	initLogger(cfg.LogFormat, cfg.LogLevel)

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr: cfg.Bind,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Pool:              pool,
			Version:           version,
			HealthShowVersion: cfg.HealthShowVersion,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("サーバを起動した", slog.String("bind", cfg.Bind), slog.String("version", version))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("サーバを起動できない: %w", err)
		}
		return nil
	case <-ctx.Done():
		slog.Info("停止信号を受け取った。処理中のリクエストの完了を待つ")
	}

	// ctx は既に打ち切られているため、猶予つきの別コンテキストで停止する。
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("サーバの停止に失敗した: %w", err)
	}
	slog.Info("サーバを停止した")
	return nil
}

// initLogger は log/slog の既定ロガーを差し替える（Design.md 10.1）。
//
// 出力先は標準出力に一本化する。stderr は「異常」の含意を持ち、正常な
// アクセスログを流すと収集基盤で誤って error 扱いされやすいため。
// また2ストリームに分けると行の到着順が保証されない。
// PB_LOG_FORMAT が json 以外なら人間が読みやすいテキスト形式にする。
func initLogger(format, level string) {
	opts := &slog.HandlerOptions{Level: parseLevel(level)}

	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

// parseLevel は PB_LOG_LEVEL を slog.Level に写す。
// 値の検証は config.Load が済ませているため、ここでは既定へ倒すだけでよい。
func parseLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
