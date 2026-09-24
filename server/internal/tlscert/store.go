// 出す証明書の選定と、実行中に差し替わる入れ物（Design.md 6.6.1）。
package tlscert

import (
	"crypto/tls"
	"errors"
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
// **旧証明書の期限で切り替える形にはしない。** **その形は有効な証明書が1枚も
// 無い窓を作りうる**——新証明書の
// NotBefore が旧証明書の NotAfter より後のとき、その間どちらも出せない。
//
// 選ばれたものの ID を第2返り値に返す（空なら有効なものが無い）。
func Select(entries []Entry, now time.Time) (map[string]Status, string) {
	status := make(map[string]Status, len(entries))
	for _, e := range entries {
		switch {
		case now.Before(e.NotBefore):
			status[e.ID] = StatusPending
		case now.After(e.NotAfter):
			status[e.ID] = StatusExpired
		default:
			// 有効。**選ばれた1枚だけを下で active に上書きする。**
			status[e.ID] = StatusSuperseded
		}
	}
	activeID := ""
	if i := selectActive(entries, now); i >= 0 {
		activeID = entries[i].ID
		status[activeID] = StatusActive
	}
	return status, activeID
}

// selectActive は出す1枚を選び、entries の添字を返す（無ければ -1）。
//
// **全件の状態が要るのは画面だけである**（ApiDesign.md 11.4）。ハンドシェイクの
// たびに要るのは選ばれた1枚なので、**そこで map を組み立てて捨てない**ように
// 選定だけを切り出してある。添字で返すので、呼び出し側は ID から引き直さずに済む。
func selectActive(entries []Entry, now time.Time) int {
	active := -1
	var activeFrom time.Time
	for i, e := range entries {
		if now.Before(e.NotBefore) || now.After(e.NotAfter) {
			continue
		}
		if active < 0 || e.NotBefore.After(activeFrom) {
			active, activeFrom = i, e.NotBefore
		}
	}
	return active
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

	i := selectActive(entries, time.Now())
	if i < 0 {
		slog.Warn("有効な TLS 証明書が無い状態になった",
			slog.Int("registered", len(entries)),
			slog.String("hint", "PB_TLS_ENABLED=false を与えて起動し直すと平文へ戻せる"))
		return
	}
	slog.Info("出す TLS 証明書が決まった",
		slog.String("certificate_id", entries[i].ID), slog.Int("registered", len(entries)))
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
//
// **Select ではなく selectActive を呼ぶ。** あちらは全件の状態を map に組み立てる
// ——接続のたびにそれを作って捨てることになるうえ、返った ID から Pair を引くのに
// もう一度走査が要る。**添字で受ければどちらも要らない。**
func (h *Holder) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	h.mu.RLock()
	entries := h.entries
	h.mu.RUnlock()

	i := selectActive(entries, time.Now())
	if i < 0 {
		h.warnOnce()
		return nil, ErrNoUsableCertificate
	}
	return entries[i].Pair, nil
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
