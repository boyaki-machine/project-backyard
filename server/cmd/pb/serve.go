// pb serve — APIサーバの起動（Design.md 4.1）。
//
// 手順4b の時点では /healthcheck 以外のエンドポイントを公開しておらず、
// ミドルウェア連鎖（request_id → アクセスログ → 認証）と ApiDesign.md 2.5
// 形式の 404 だけを返す。ログインは手順5で入る。
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/tlscert"
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

	// **DB の行を重ねてから待ち受ける。** 第2層（ログ・ヘルスチェック・Cookie）は
	// app_setting の行が正本であり（Design.md 10.3）、ファイルや環境変数で
	// 固定されていないキーだけが置き換わる。
	//
	// **行が引けなくても起動を止めない。** 設定は既定値で動けるものだけが
	// 第2層に入っており、ここで止めると「設定表が読めないので起動しない」
	// という復旧しにくい状態を作る。
	live := config.NewLive(cfg.Set)
	set := cfg.Set
	if rows, err := gen.New(pool).ListAppSettings(ctx); err != nil {
		slog.Warn("設定の行を読めなかったため、ファイルと環境変数と既定値で起動する",
			slog.String("error", err.Error()))
	} else {
		overlay := make([]config.Row, 0, len(rows))
		for _, row := range rows {
			overlay = append(overlay, config.Row{Key: row.Key, Value: row.Value})
		}
		set = live.ApplyRows(overlay)
	}

	// 重ねた結果でログを組み直す。**DB で debug にしてあれば、ここから効く。**
	logs := &logState{format: cfg.LogFormat}
	if set.String(config.KeyLogFormat) != cfg.LogFormat || set.String(config.KeyLogLevel) != cfg.LogLevel {
		applyLogSettings(logs, set)
	}

	// TLS の準備（Design.md 6.6.1）。**証明書を読めなければ起動を失敗させる。**
	certs, tlsConfig, err := setupTLS(ctx, pool, set)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:      set.String(config.KeyBind),
		TLSConfig: tlsConfig,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Pool:     pool,
			Version:  version,
			Settings: live,
			OnSettingsChanged: func(s *config.Set) {
				applyLogSettings(logs, s)
			},
			Certs:        certs,
			TLSListening: tlsConfig != nil,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	errCh := make(chan error, 1)
	go func() {
		scheme := "http"
		if tlsConfig != nil {
			scheme = "https"
		}
		slog.Info("サーバを起動した",
			slog.String("bind", srv.Addr), slog.String("scheme", scheme),
			slog.String("version", version))

		// **証明書と鍵のパスを渡さない。** TLSConfig.GetCertificate が
		// 毎ハンドシェイクで選ぶので（Design.md 6.6.1）、起動時に固定しない。
		var err error
		if tlsConfig != nil {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
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

// logLevelVar は実行中にログレベルを差し替えるための入れ物。
//
// **slog.LevelVar を使うのは、ハンドラを作り直さずにレベルを変えられるからである。**
// レベルだけなら差し替えが要らず、形式（json / text）が変わったときだけ
// ハンドラを組み直す（pb-2、Design.md 10.3 の第2層）。
var logLevelVar = new(slog.LevelVar)

// setupTLS は TLS の待受を準備する（Design.md 6.6.1）。
//
// **tls_enabled が偽なら何もしない**（平文で待ち受ける）。真なら証明書を
// 読んで入れ物に載せ、tls.Config を返す。
//
// **証明書があるのに secret_key が無ければ起動を失敗させる。** 復号できない
// 証明書を抱えて平文で上がると、**HTTPS で公開しているつもりの利用者が
// 平文で公開する**——これが最悪の結果である。
//
// **有効な証明書が1枚も無い状態でも起動する。** ハンドシェイクは失敗するが、
// 起動自体を止めると PB_TLS_ENABLED=false で戻す以外の手が無くなり、
// 「なぜ上がらないのか」をログでしか伝えられない。
func setupTLS(ctx context.Context, pool *pgxpool.Pool, set *config.Set) (*tlscert.Holder, *tls.Config, error) {
	if !set.Bool(config.KeyTLSEnabled) {
		return nil, nil, nil
	}

	rows, err := gen.New(pool).ListTLSCertificates(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("TLS 証明書を引けない: %w", err)
	}

	key, err := tlscert.DecodeKey(set.String(config.KeySecretKey))
	if err != nil {
		if len(rows) == 0 {
			// 証明書が無いなら鍵も要らない。**ただし TLS では上がれない。**
			return nil, nil, fmt.Errorf(
				"TLS で待ち受ける設定だが、証明書が1枚も登録されていない。" +
					"PB_TLS_ENABLED=false で平文に戻すか、証明書を登録すること")
		}
		return nil, nil, fmt.Errorf("TLS 証明書があるが秘密の暗号鍵を読めない（PB_SECRET_KEY）: %w", err)
	}

	holder := tlscert.NewHolder()
	entries := make([]tlscert.Entry, 0, len(rows))
	for _, row := range rows {
		pair, err := loadCertPair(row, key)
		if err != nil {
			continue // loadCertPair が警告を出している
		}
		entries = append(entries, tlscert.Entry{
			ID: row.ID, NotBefore: row.NotBefore.Time, NotAfter: row.NotAfter.Time, Pair: pair,
		})
	}
	holder.Replace(entries)

	return holder, &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: holder.GetCertificate,
	}, nil
}

// loadCertPair は行の秘密鍵を復号して tls.Certificate を作る。
//
// **1行の失敗で全部を止めない。** 鍵を交換したあとに古い行が残っている場合などで、
// 使える証明書があるなら上がるべきである。
func loadCertPair(row gen.ListTLSCertificatesRow, key []byte) (*tls.Certificate, error) {
	keyPEM, err := tlscert.Open(key, row.KeyCiphertext, row.KeyNonce)
	if err != nil {
		slog.Warn("証明書の秘密鍵を復号できないため、この行を使わない",
			slog.String("certificate_id", row.ID), slog.String("key_id", row.KeyID),
			slog.String("error", err.Error()))
		return nil, err
	}
	pair, err := tls.X509KeyPair([]byte(row.CertPem), []byte(keyPEM))
	if err != nil {
		slog.Warn("証明書と秘密鍵が対応しないため、この行を使わない",
			slog.String("certificate_id", row.ID), slog.String("error", err.Error()))
		return nil, err
	}
	return &pair, nil
}

// initLogger は log/slog の既定ロガーを差し替える（Design.md 10.1）。
//
// 出力先は標準出力に一本化する。stderr は「異常」の含意を持ち、正常な
// アクセスログを流すと収集基盤で誤って error 扱いされやすいため。
// また2ストリームに分けると行の到着順が保証されない。
// log_format が json 以外なら人間が読みやすいテキスト形式にする。
func initLogger(format, level string) {
	logLevelVar.Set(parseLevel(level))
	opts := &slog.HandlerOptions{Level: logLevelVar}

	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		h = slog.NewTextHandler(os.Stdout, opts)
	}
	slog.SetDefault(slog.New(h))
}

// applyLogSettings はログの設定が変わったときに反映する。
//
// **形式が変わったときだけハンドラを組み直す。** レベルは LevelVar を
// 動かすだけで済み、組み直すと出力先のバッファを掴み直すことになる。
func applyLogSettings(cur *logState, set *config.Set) {
	format := set.String(config.KeyLogFormat)
	level := set.String(config.KeyLogLevel)

	logLevelVar.Set(parseLevel(level))
	if format != cur.format {
		initLogger(format, level)
		cur.format = format
	}
	slog.Info("設定を反映した",
		slog.String("log_format", format), slog.String("log_level", level))
}

// logState はいま効いているログ形式を覚えておく入れ物。
type logState struct{ format string }

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
