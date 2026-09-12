// 出す証明書の選定と、実行中に差し替わる入れ物（Design.md 6.6.1）。
package tlscert

import (
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Status は証明書1件の状態（ApiDesign.md 11.4）。
//
// **サーバが決める。** 画面が not_before / not_after と現在時刻から組み立てると、
// 「いまどれが出ているか」の判定がサーバと画面の2か所に分かれる。
type Status string

const (
	// StatusActive はいま出している1枚。**必ず1枚以下である。**
	StatusActive Status = "active"
	// StatusPending は not_before が未来のもの。
	StatusPending Status = "pending"
	// StatusExpired は not_after を過ぎたもの。
	StatusExpired Status = "expired"
	// StatusSuperseded は有効だが、より新しい有効なものがあるもの。
	//
	// **active と分けるのは、消してよいものが分かるようにするため**である
	// （期限が切れていなくても、より新しいものが出ているなら消して差し支えない）。
	StatusSuperseded Status = "superseded"
)

// Entry は選定の対象。DB の行から必要な分だけを写す。
type Entry struct {
	ID        string
	NotBefore time.Time
	NotAfter  time.Time
	// Pair は復号済みの鍵を含む。**選ばれたものだけが使われる。**
	Pair *tls.Certificate
}

// Select は出す証明書を選び、各件の状態を返す（Design.md 6.6.1）。
//
//	now が [NotBefore, NotAfter] に入る行のうち、NotBefore が最大のもの
//
// **利用者の決定（2026-09-11）。** チケットの本文は「旧証明書期限で切り替え」
// だったが、**その形は有効な証明書が1枚も無い窓を作りうる**——新証明書の
// NotBefore が旧証明書の NotAfter より後のとき、その間どちらも出せない。
//
// 選ばれたものの ID を第2返り値に返す（空なら有効なものが無い）。
func Select(entries []Entry, now time.Time) (map[string]Status, string) {
	status := make(map[string]Status, len(entries))
	activeID := ""
	var activeFrom time.Time

	for _, e := range entries {
		switch {
		case now.Before(e.NotBefore):
			status[e.ID] = StatusPending
		case now.After(e.NotAfter):
			status[e.ID] = StatusExpired
		default:
			// 有効。いちばん新しいものを探す。
			status[e.ID] = StatusSuperseded
			if activeID == "" || e.NotBefore.After(activeFrom) {
				activeID = e.ID
				activeFrom = e.NotBefore
			}
		}
	}
	if activeID != "" {
		status[activeID] = StatusActive
	}
	return status, activeID
}

// ErrNoUsableCertificate は有効な証明書が1枚も無いこと。
//
// **平文へ落とさずハンドシェイクを失敗させる**ためにこれを返す（Design.md 6.6.1）。
// 利用者は HTTPS で公開しているつもりなので、**黙って平文になるのが最悪の結果である。**
var ErrNoUsableCertificate = errors.New("有効な TLS 証明書がありません")

// Holder は実行中に差し替わる証明書の入れ物。
//
// **crypto/tls の GetCertificate から呼ばれる。** 再起動もリロードの合図も要らず、
// 画面から登録した瞬間から次の接続で効く。
type Holder struct {
	mu      sync.RWMutex
	entries []Entry
	// warned は「有効な証明書が無い」を一度だけ記録するための印。
	// **ハンドシェイクごとに1行出すと、走査に対してログが溢れる。**
	warned bool
}

// NewHolder は空の入れ物を返す。
func NewHolder() *Holder { return &Holder{} }

// Replace は持っている証明書を入れ替える。
func (h *Holder) Replace(entries []Entry) {
	h.mu.Lock()
	h.entries = entries
	h.warned = false
	h.mu.Unlock()

	_, activeID := Select(entries, time.Now())
	if activeID == "" {
		slog.Warn("有効な TLS 証明書が無い状態になった",
			slog.Int("registered", len(entries)),
			slog.String("hint", "PB_TLS_ENABLED=false を与えて起動し直すと平文へ戻せる"))
		return
	}
	slog.Info("出す TLS 証明書が決まった",
		slog.String("certificate_id", activeID), slog.Int("registered", len(entries)))
}

// Count は登録されている件数を返す。
func (h *Holder) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.entries)
}

// GetCertificate は crypto/tls.Config に渡す関数である。
//
// **毎ハンドシェイクで選ぶ。** 期限による切り替えも時刻の比較だけで起きるので、
// 切り替えのための仕掛けを持たない（Design.md 6.6.1）。
func (h *Holder) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	h.mu.RLock()
	entries := h.entries
	h.mu.RUnlock()

	_, activeID := Select(entries, time.Now())
	if activeID == "" {
		h.warnOnce()
		return nil, ErrNoUsableCertificate
	}
	for i := range entries {
		if entries[i].ID == activeID {
			return entries[i].Pair, nil
		}
	}
	// Select が返した ID が entries に無いことは起こらないが、
	// 起きたら平文へ落とさず失敗させる。
	return nil, fmt.Errorf("選んだ証明書が見つからない（id=%s）", activeID)
}

// warnOnce は「有効な証明書が無い」を一度だけ記録する。
func (h *Holder) warnOnce() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.warned {
		return
	}
	h.warned = true
	slog.Error("有効な TLS 証明書が無いためハンドシェイクを拒否した",
		slog.String("hint", "PB_TLS_ENABLED=false を与えて起動し直すと平文へ戻せる"))
}
