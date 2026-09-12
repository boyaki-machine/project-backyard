// pb serve — APIサーバの起動（Design.md 4.1）。
//
// 手順4b の時点では /healthcheck 以外のエンドポイントを公開しておらず、
// ミドルウェア連鎖（request_id → アクセスログ → 認証）と ApiDesign.md 2.5
// 形式の 404 だけを返す。ログインは手順5で入る。
package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi"
	v1 "github.com/boyaki-machine/project-backyard/server/internal/httpapi/v1"
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

	// settingsGuardInterval は未確認の設定変更の期限を点検する間隔（pb-97）。
	//
	// **設定にしない**（Design.md 10.3）。期限の既定は 300 秒なので、
	// この粒度で「期限から最大10秒遅れて戻る」ことになる。
	settingsGuardInterval = 10 * time.Second
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

	// **期限の切れた未確認の変更を、待受を張る前に戻す**（pb-97、DbDesign.md 6.17）。
	//
	// **前のプロセスが落ちたあと、未確認のまま期限が切れている可能性がある。**
	// ここで戻さないと、締め出す設定のまま待ち受けてしまう。
	//
	// **OnChanged は渡さない。** 待受はまだ張っていないので、戻した値は
	// このあとの setupTLS が読む。
	txRunner := store.NewTxRunner(pool)
	bootGuard := v1.SettingsGuard{Tx: txRunner, Q: gen.New(pool), Settings: live}
	if n, err := bootGuard.RevertExpired(ctx); err != nil {
		slog.Error("期限切れの設定変更を戻せなかったため、いまの設定で起動する",
			slog.String("error", err.Error()))
	} else if n > 0 {
		set = live.Snapshot()
	}

	// 重ねた結果でログを組み直す。**DB で debug にしてあれば、ここから効く。**
	logs := &logState{format: cfg.LogFormat}
	if set.String(config.KeyLogFormat) != cfg.LogFormat || set.String(config.KeyLogLevel) != cfg.LogLevel {
		applyLogSettings(logs, set)
	}

	// TLS の準備（Design.md 6.6.1）。**証明書を読めなければ起動を失敗させる。**
	startCerts, tlsConfig, err := setupTLS(ctx, pool, set)
	if err != nil {
		return err
	}

	bind := set.String(config.KeyBind)

	// **待受は張り替えられる**（pb-106、Design.md 10.3）。tls_enabled を画面から
	// 変えた時点で切り替わり、再起動は要らない。
	var server *swappableServer

	// certs は**いま出している証明書の入れ物**。TLS へ切り替えるたびに作り直す。
	certs := startCerts

	// swapMu は張り替えを直列化する。**設定の保存は稀なので、素朴な排他で足りる。**
	var swapMu sync.Mutex

	onChanged := func(s *config.Set) {
		applyLogSettings(logs, s)

		swapMu.Lock()
		defer swapMu.Unlock()

		want := s.Bool(config.KeyTLSEnabled)
		if want == server.TLSOn() {
			return
		}

		if !want {
			certs = nil
			if err := server.Swap(nil); err != nil {
				slog.Error("平文へ切り替えられない", slog.String("error", err.Error()))
				return
			}
			slog.Info("平文で待ち受けるよう切り替えた", slog.String("bind", server.Addr()))
			return
		}

		// **証明書を読み直してから切り替える。** 読めなければ切り替えない——
		// 設定は有効になっているが待受は平文のままで、**画面はそのずれを
		// 出せる**（ApiDesign.md 11.4 の tls_enabled は実際の待受である）。
		holder, tc, err := setupTLS(ctx, pool, s)
		if err != nil {
			slog.Error("TLS へ切り替えられないため平文のままにする",
				slog.String("error", err.Error()))
			return
		}
		certs = holder
		if err := server.Swap(tc); err != nil {
			slog.Error("TLS へ切り替えられない", slog.String("error", err.Error()))
			return
		}
		slog.Info("TLS で待ち受けるよう切り替えた", slog.String("bind", server.Addr()))
	}

	// **TLS の有無で Handler ごと作り直す。** 応答に出る tls_enabled と
	// listen_url は実際の待受であり（11.4）、設定の実効値ではない。
	build := func(tc *tls.Config) *http.Server {
		return &http.Server{
			Addr:      bind,
			TLSConfig: tc,
			Handler: httpapi.NewRouter(httpapi.Deps{
				Pool:              pool,
				Version:           version,
				Settings:          live,
				OnSettingsChanged: onChanged,
				Certs:             certs,
				TLSListening:      tc != nil,
				ListenURL:         listenURL(bind, tc != nil),
			}),
			ReadHeaderTimeout: readHeaderTimeout,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
		}
	}
	server = newSwappableServer(bind, build)

	// **待受を先に張り、成立してからログを書く**（pb-29）。bind に失敗したのに
	// 「サーバを起動した」が先に出ると、2026-08-30 のような読み違えを生む。
	if err := server.Start(tlsConfig); err != nil {
		return fmt.Errorf("サーバを起動できない: %w", err)
	}

	scheme := "http"
	if tlsConfig != nil {
		scheme = "https"
	}
	// **実際に掴んだアドレスを出す。** ポートに 0 を指定したときも嘘にならない。
	slog.Info("サーバを起動した",
		slog.String("bind", server.Addr()), slog.String("scheme", scheme),
		slog.String("version", version))

	// **期限を数える主体は2つある**（pb-97）。起動時の点検（上）と、この定期点検。
	// **どちらも DB の expires_at を見る**ので、判定は1つである。
	//
	// **間隔は設定にしない**（Design.md 10.3。設定の反映を設定で決めると、
	// その設定自身の反映が説明できなくなる）。
	guard := v1.SettingsGuard{
		Tx: txRunner, Q: gen.New(pool), Settings: live, OnChanged: onChanged,
	}
	go func() {
		t := time.NewTicker(settingsGuardInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				// **停止信号で打ち切られた ctx を使わない。** 戻す途中で
				// 打ち切ると、設定だけ戻って記録が残る。
				if _, err := guard.RevertExpired(context.WithoutCancel(ctx)); err != nil {
					slog.Error("期限切れの設定変更を戻せない", slog.String("error", err.Error()))
				}
			}
		}
	}()

	select {
	case err := <-server.Err():
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
	if err := server.Shutdown(shutdownCtx); err != nil {
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

// listenURL は待受のスキームとアドレスを組み立てる（ApiDesign.md 11.4）。
//
// **画面が bind の設定値から組み立てない**ので、実際に待ち受ける側で作る。
// 待受の変更には再起動が要るため、設定の値と実際の待受は再起動をまたぐとずれる。
func listenURL(bind string, tls bool) string {
	scheme := "http"
	if tls {
		scheme = "https"
	}
	return scheme + "://" + bind
}

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
// warnIfKeyChanged は、登録済みの証明書をいまの鍵で復号できるかを起動時に1回見る（pb-98）。
//
// **止めない。** TLS で待ち受けないなら、読めない証明書があっても動作に影響しない。
// **止めると平文へ戻す経路が細くなる**——鍵を取り違えた利用者が、画面を開いて直す
// 手段まで失う。
func warnIfKeyChanged(ctx context.Context, pool *pgxpool.Pool, set *config.Set) {
	q := gen.New(pool)
	rows, err := q.ListTLSCertificates(ctx)
	if err != nil || len(rows) == 0 {
		// **証明書が無いなら鍵も要らない。** ここで ResolveKey を呼ぶと、
		// 使いもしない鍵を生成して DB へ書くことになる。
		return
	}

	key, _, err := tlscert.ResolveKey(ctx, v1.SecretStore{Q: q}, set.String(config.KeySecretKey))
	if err != nil {
		slog.Warn("暗号鍵を用意できないため、登録済みの証明書を確かめられない",
			slog.String("error", err.Error()))
		return
	}

	bad := 0
	for _, row := range rows {
		if _, err := tlscert.Open(key, row.KeyCiphertext, row.KeyNonce); err != nil {
			bad++
		}
	}
	if bad == 0 {
		return
	}
	slog.Warn("登録済みの TLS 証明書を復号できない（暗号鍵の出どころが登録時から変わっている）",
		slog.Int("undecryptable", bad),
		slog.Int("registered", len(rows)),
		slog.String("hint", "元の PB_SECRET_KEY に戻すか、証明書を登録し直すこと。"+
			"このまま TLS を有効にすると起動に失敗する"))
}

func setupTLS(ctx context.Context, pool *pgxpool.Pool, set *config.Set) (*tlscert.Holder, *tls.Config, error) {
	if !set.Bool(config.KeyTLSEnabled) {
		// **TLS で上がらないときも、鍵の食い違いは知らせる**（pb-98）。
		// 気づくのが「次に TLS で起動したとき」では遅い——そのとき起動は失敗し、
		// ローリングアップデートの途中なら**一部の Pod だけが落ちる**形で現れる。
		warnIfKeyChanged(ctx, pool, set)
		return nil, nil, nil
	}

	rows, err := gen.New(pool).ListTLSCertificates(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("TLS 証明書を引けない: %w", err)
	}

	if len(rows) == 0 {
		// 証明書が無いなら鍵も要らない。**ただし TLS では上がれない。**
		return nil, nil, fmt.Errorf(
			"TLS で待ち受ける設定だが、証明書が1枚も登録されていない。" +
				"PB_TLS_ENABLED=false で平文に戻すか、証明書を登録すること")
	}

	// **鍵は PB が用意する**（Design.md 6.6.1）。PB_SECRET_KEY があればそれ、
	// 無ければ DB の行、それも無ければ生成して保存する。
	key, origin, err := tlscert.ResolveKey(ctx, v1.SecretStore{Q: gen.New(pool)},
		set.String(config.KeySecretKey))
	if err != nil {
		return nil, nil, fmt.Errorf("秘密の暗号鍵を用意できない: %w", err)
	}
	if origin == tlscert.OriginGenerated {
		slog.Warn("PB が生成した暗号鍵を使っている",
			slog.String("hint", "バックアップの持ち出しから秘密鍵を守るには PB_SECRET_KEY を与えること"))
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

	// **1枚も読めなかったら起動を失敗させる。** 行があるのに全部読めないのは
	// 鍵の取り違えか行の破損であり、**時間が経っても直らない設定の誤りである。**
	// 起動してしまうと、利用者には「繋がらない」としか見えない
	// （実サーバ検証で、別の鍵を渡すと scheme=https で上がってしまった。2026-09-12）。
	//
	// **日付のせいで有効なものが無い場合とは区別する。** あちらは時刻で変わるので、
	// 警告を出して起動する（Holder.Replace が出す）。
	if len(entries) == 0 {
		return nil, nil, fmt.Errorf(
			"TLS 証明書が %d 件あるが、1枚も読めなかった（秘密鍵を復号できない）。"+
				"PB_SECRET_KEY が登録時と同じか確かめること。"+
				"平文へ戻すなら PB_TLS_ENABLED=false を与えて起動し直す", len(rows))
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
