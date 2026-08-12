// pb serve — APIサーバの起動（Design.md 4.1）。
//
// 手順4a の時点ではエンドポイントを1つも公開しておらず、ミドルウェア連鎖と
// ApiDesign.md 2.5 形式の 404 だけを返す。ログインは手順5で入る。
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
	initLogger(cfg.LogFormat)

	pool, err := store.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	srv := &http.Server{
		Addr:              cfg.Bind,
		Handler:           httpapi.NewRouter(httpapi.Deps{Pool: pool}),
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

// initLogger は log/slog の既定ロガーを差し替える（Design.md 3.1）。
// PB_LOG_FORMAT が json 以外なら人間が読みやすいテキスト形式にする。
func initLogger(format string) {
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, nil)
	} else {
		h = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(h))
}
