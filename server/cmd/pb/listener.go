package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
)

// swappableServer は待受を張り替えられる HTTP サーバ（pb-106、Design.md 10.3）。
//
// **TLS の切り替えに再起動を要らなくする。** 画面から `tls_enabled` を変えた
// 時点で待受が変わる。**pb-97「確認しないと元に戻す」の成立条件**でもある——
// 戻す操作がプロセス内で完結しないと、**プロセスは自分を確実に再起動できない**
// （監視プロセスがある保証がない）。
//
// **サーバごと作り直す**（利用者の判断、2026-09-12）。棄却したのは、素のリスナを
// 1つ持ち続けて `tls.Server` で包むかをフラグで決める案である。待受を閉じないので
// 単純だが、**HTTP/2 が使えなくなる**——`http.Server` が HTTP/2 を有効にするのは
// `ServeTLS` を通ったときだけで、自分で包むと平文の `Serve` 扱いになる。
type swappableServer struct {

	// build は待受ごとに Server を作る。
	//
	// **アドレスと TLS の有無で Handler も変わる。** 応答に出る `tls_enabled` と
	// `listen_url` は**実際の待受**であり（ApiDesign.md 11.4）、設定の実効値
	// ではない。作り直さないと、切り替えたあとも古い値を返し続ける。
	build func(addr string, tlsConfig *tls.Config) *http.Server

	mu sync.Mutex
	// addr はいま張っている待受のアドレス。**画面から変えられる**（pb-99）。
	addr    string
	srv     *http.Server
	ln      net.Listener
	tlsConf *tls.Config
	stopped bool

	// serveErr は待受が**予期せず**終わったことを伝える。
	// 張り替えと停止で閉じたときは流れない。
	serveErr chan error
}

func newSwappableServer(build func(string, *tls.Config) *http.Server) *swappableServer {
	return &swappableServer{build: build, serveErr: make(chan error, 1)}
}

// Start は待受を張る。**成立してから返る**ので、呼び出し側はここでログを書ける（pb-29）。
func (s *swappableServer) Start(addr string, tlsConfig *tls.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.listen(addr, tlsConfig); err != nil {
		return err
	}
	s.addr, s.tlsConf = addr, tlsConfig
	return nil
}

// Swap は待受を張り替える。
//
// **同じアドレスなので、旧を閉じてからでないと新しく開けない。** 開けなかったら
// 元の設定で開き直す——**そこも失敗すると待受が無くなる**ので、そのときだけ
// 呼び出し側へ致命的な誤りとして返す。
func (s *swappableServer) Swap(addr string, tlsConfig *tls.Config) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return errors.New("停止済みのサーバは張り替えられない")
	}

	old, prevAddr, prevTLS := s.srv, s.addr, s.tlsConf
	if err := s.ln.Close(); err != nil {
		return fmt.Errorf("いまの待受を閉じられない: %w", err)
	}

	// **Shutdown を待たない。** 張り替えを指示しているのは画面からのリクエスト
	// であり、**その処理中に待つと自分自身を待つ**（処理中のリクエストが終わる
	// のを Shutdown が待ち、そのリクエストは Shutdown の完了を待つ）。
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := old.Shutdown(ctx); err != nil {
			slog.Warn("張り替え前のサーバを閉じきれなかった", slog.String("error", err.Error()))
		}
	}()

	if err := s.listen(addr, tlsConfig); err != nil {
		// **アドレスごと元へ戻す**（pb-99）。**ポートを誤ると新しい待受は
		// 開けない**ので、ここが実際に通る経路になる。
		if back := s.listen(prevAddr, prevTLS); back != nil {
			return fmt.Errorf("待受を張り替えられず、元にも戻せない（%v）: %w", err, back)
		}
		return fmt.Errorf("待受を張り替えられないため元に戻した: %w", err)
	}
	s.addr, s.tlsConf = addr, tlsConfig
	return nil
}

// Err は待受が予期せず終わったときに値が流れる。
func (s *swappableServer) Err() <-chan error { return s.serveErr }

// Shutdown は待受を閉じ、処理中のリクエストの完了を待つ。
func (s *swappableServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.stopped = true
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

// Addr はいま実際に掴んでいるアドレス。**設定の文字列ではない**ので、
// ポートに 0 を指定したときも実物が返る（pb-29）。
func (s *swappableServer) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return s.addr
	}
	return s.ln.Addr().String()
}

// listen は新しい待受を張り、Serve を回す。**s.mu を持った状態で呼ぶこと。**
func (s *swappableServer) listen(addr string, tlsConfig *tls.Config) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := s.build(addr, tlsConfig)
	s.srv, s.ln = srv, ln

	go func() {
		// **証明書と鍵のパスを渡さない。** TLSConfig.GetCertificate が
		// 毎ハンドシェイクで選ぶので（Design.md 6.6.1）、起動時に固定しない。
		var err error
		if tlsConfig != nil {
			err = srv.ServeTLS(ln, "", "")
		} else {
			err = srv.Serve(ln)
		}
		// **張り替えと停止で閉じたときは流さない。** リスナを閉じると
		// net.ErrClosed が、Shutdown を呼ぶと http.ErrServerClosed が返る。
		// どちらもこちらが意図して起こしたものである。
		if err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			select {
			case s.serveErr <- err:
			default:
			}
		}
	}()
	return nil
}

// TLSOn はいま TLS で待ち受けているか。**設定の実効値ではない。**
func (s *swappableServer) TLSOn() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tlsConf != nil
}
