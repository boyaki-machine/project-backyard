package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

// freeAddr は空いているアドレスを1つ返す。
//
// **取ってから閉じる。** swappableServer は同じアドレスで開き直すので、
// ポート 0 のままでは張り替えのたびに別のポートになってしまう。
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func testTLSConfig(t *testing.T) *tls.Config {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Config{Certificates: []tls.Certificate{{
		Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf,
	}}}
}

// echoScheme は届いた接続が TLS かどうかを本文で返す。
func echoScheme(addr string, tc *tls.Config) *http.Server {
	return &http.Server{
		Addr:      addr,
		TLSConfig: tc,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.TLS != nil {
				_, _ = io.WriteString(w, "https")
				return
			}
			_, _ = io.WriteString(w, "http")
		}),
		ReadHeaderTimeout: 2 * time.Second,
	}
}

// fetch は1回叩いて本文を返す。**毎回新しい接続を使う**（張り替えの前後で
// keep-alive の接続を使い回すと、古い待受を見てしまう）。
func fetch(t *testing.T, url string) (string, error) {
	t.Helper()
	c := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // 自己署名の試験用
		},
	}
	res, err := c.Get(url)
	if err != nil {
		return "", err
	}
	defer func() { _ = res.Body.Close() }()
	b, err := io.ReadAll(res.Body)
	return string(b), err
}

// TestSwappableServerSwitchesTLS は待受の張り替えを見る。
//
// **起動ログではなく実際の接続で確かめる**（チケットの完了の見分け方）。
func TestSwappableServerSwitchesTLS(t *testing.T) {
	addr := freeAddr(t)
	s := newSwappableServer(echoScheme)

	if err := s.Start(addr, nil); err != nil {
		t.Fatalf("起動できない: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	if got, err := fetch(t, "http://"+addr); err != nil || got != "http" {
		t.Fatalf("平文で待ち受けていない: %q, %v", got, err)
	}

	// ── TLS へ張り替える ──────────────────────────────
	if err := s.Swap(addr, testTLSConfig(t)); err != nil {
		t.Fatalf("TLS へ張り替えられない: %v", err)
	}
	if !s.TLSOn() {
		t.Error("TLSOn が偽のまま")
	}
	if got, err := fetch(t, "https://"+addr); err != nil || got != "https" {
		t.Fatalf("TLS で待ち受けていない: %q, %v", got, err)
	}
	// **平文では話せなくなる。** 待受は1つだけである（Design.md 6.6.1）。
	//
	// **接続の失敗にはならない。** Go の http.Server は TLS の待受へ平文で来た
	// 接続に「Client sent an HTTP request to an HTTPS server.」を 400 で返す
	// （実測）。**ハンドラまで届いていないことを見る。**
	if got, _ := fetch(t, "http://"+addr); got == "http" {
		t.Error("TLS にしたのに平文の待受が残っている")
	}

	// ── 平文へ戻す ──────────────────────────────────
	if err := s.Swap(addr, nil); err != nil {
		t.Fatalf("平文へ戻せない: %v", err)
	}
	if got, err := fetch(t, "http://"+addr); err != nil || got != "http" {
		t.Fatalf("平文へ戻っていない: %q, %v", got, err)
	}
}

// TestSwapFromInsideHandlerReturnsResponse は、**張り替えを指示した
// リクエスト自身が応答を返せる**ことを見る。
//
// **ここが方式の要点である。** 張り替えでは旧サーバを Shutdown するが、
// Shutdown は処理中のリクエストの完了を待つ。**同期で待つと、自分の完了を
// 自分で待つことになって返らない。**
func TestSwapFromInsideHandlerReturnsResponse(t *testing.T) {
	addr := freeAddr(t)
	tc := testTLSConfig(t)

	var s *swappableServer
	build := func(bindAddr string, cur *tls.Config) *http.Server {
		return &http.Server{
			Addr:      bindAddr,
			TLSConfig: cur,
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/switch" {
					// **ハンドラの中から張り替える。** 画面からの設定変更と同じ経路。
					if err := s.Swap(addr, tc); err != nil {
						http.Error(w, err.Error(), http.StatusInternalServerError)
						return
					}
				}
				_, _ = io.WriteString(w, "ok")
			}),
			ReadHeaderTimeout: 2 * time.Second,
		}
	}
	s = newSwappableServer(build)
	if err := s.Start(addr, nil); err != nil {
		t.Fatalf("起動できない: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	done := make(chan error, 1)
	go func() {
		body, err := fetch(t, "http://"+addr+"/switch")
		if err == nil && body != "ok" {
			err = fmt.Errorf("本文が %q", body)
		}
		done <- err
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("張り替えを指示したリクエストが失敗した: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("張り替えを指示したリクエストが返らない（自分自身を待っている）")
	}

	// 張り替わっていること。
	if got, err := fetch(t, "https://"+addr); err != nil || got != "ok" {
		t.Fatalf("TLS へ張り替わっていない: %q, %v", got, err)
	}
}

// TestSwapChangesAddress は**待受のアドレスを張り替えられる**ことを見る。
//
// **画面から待受を変える**のがこの機能である。開けなかったときに**アドレスごと
// 元へ戻る**ことも見る——ポートを誤ると新しい待受は開けないので、実際に通る経路である。
func TestSwapChangesAddress(t *testing.T) {
	first := freeAddr(t)
	second := freeAddr(t)
	s := newSwappableServer(echoScheme)

	if err := s.Start(first, nil); err != nil {
		t.Fatalf("起動できない: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})

	if got, err := fetch(t, "http://"+first); err != nil || got != "http" {
		t.Fatalf("最初のアドレスで待ち受けていない: %q, %v", got, err)
	}

	// ── 別のアドレスへ張り替える ──────────────────────
	if err := s.Swap(second, nil); err != nil {
		t.Fatalf("アドレスを張り替えられない: %v", err)
	}
	if s.Addr() != second {
		t.Errorf("Addr() = %q, want %q", s.Addr(), second)
	}
	if got, err := fetch(t, "http://"+second); err != nil || got != "http" {
		t.Fatalf("新しいアドレスで待ち受けていない: %q, %v", got, err)
	}
	if _, err := fetch(t, "http://"+first); err == nil {
		t.Error("古いアドレスがまだ生きている")
	}

	// ── 開けないアドレスへ張り替えると、元へ戻る ──────────
	blocker, err := net.Listen("tcp", first)
	if err != nil {
		t.Fatalf("塞げない: %v", err)
	}
	defer func() { _ = blocker.Close() }()

	if err := s.Swap(first, nil); err == nil {
		t.Error("塞がれているアドレスへ張り替えられてしまった")
	}
	// **アドレスごと元へ戻っていること。**
	if s.Addr() != second {
		t.Errorf("戻ったあとの Addr() = %q, want %q", s.Addr(), second)
	}
	if got, err := fetch(t, "http://"+second); err != nil || got != "http" {
		t.Fatalf("元のアドレスへ戻っていない: %q, %v", got, err)
	}
}
