package middleware

import (
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// レート制限の応答ヘッダ（ApiDesign.md 2.9）。
// 残る Retry-After は apierr が本体の retry_after_sec と揃えて書く（2.5）。
const (
	rateLimitHeader     = "X-RateLimit-Limit"
	rateRemainingHeader = "X-RateLimit-Remaining"
)

// maxRateLimitKeys は同時に覚えるキーの上限。
//
// 設計文書に規定は無い。キーを無制限に持つと、送信元を変え続けるだけで
// メモリを食い潰せてしまうため、実装側の安全弁として置く。到達するのは
// 1つの窓の間に1万の異なる送信元またはアクターが来た場合であり、
// ローカル端末で動かす前提（Design.md 4.3）では正常な状態ではない。
const maxRateLimitKeys = 10000

// RateLimit は key が返す主体ごとに、window あたり limit 回までを通す
// （ApiDesign.md 2.9）。超過したリクエストは 429 rate_limited で弾く。
//
//	POST /auth/login             IPあたり 10回/分   → ClientIPKey
//	その他の認証済みリクエスト   アクターあたり 600回/分 → ActorKey
//
// 2.9 のもう1つの制限「アカウントあたり 5回/15分（超過で account_locked）」は
// Design.md 6.3 のロックそのものであり、local_credential の failed_attempts /
// locked_until で実装済みである（v1/login.go）。ここでは扱わない。
//
// **成否によらず数える。** 本ミドルウェアはハンドラより前段にあり、結果を
// 知らずに判定するため。送った回数そのものが制限の対象である。
//
// カウンタはプロセス内メモリにある（limiter の説明を参照）。
func RateLimit(limit int, window time.Duration, key func(*http.Request) string) func(http.Handler) http.Handler {
	l := newLimiter(limit, window)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			k := key(r)
			if k == "" {
				// 主体を特定できないものは数えようがない。
				next.ServeHTTP(w, r)
				return
			}

			allowed, remaining, retry := l.allow(k)

			// 上限に近づいたことを事前に知らせる。2.9 は付与範囲を定めて
			// いないが、429 になってから初めて分かるのでは手遅れなため、
			// 対象リクエストの応答には常に付ける。
			w.Header().Set(rateLimitHeader, strconv.Itoa(limit))
			w.Header().Set(rateRemainingHeader, strconv.Itoa(remaining))

			if !allowed {
				// 監査ログには残さない。ApiDesign.md 2.10 が列挙する15の
				// アクションに該当が無いため。痕跡はアクセスログと、この
				// WithCause が出す WARN 行に残る（Design.md 10.1）。
				apierr.Write(w, r, apierr.New(apierr.RateLimited).
					WithRetryAfter(retryAfterSeconds(retry)).
					WithCause(fmt.Errorf("レート制限を超過した（key=%s、上限 %d回/%s）", k, limit, window)))
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ClientIPKey は接続元アドレスをキーにする（ApiDesign.md 2.9「IPあたり」）。
//
// 解決は clientIP に委ね、audit_log.ip・アクセスログの ip と同じ値にする
// （Design.md 10.1）。X-Forwarded-For は見ない。詐称できるヘッダで制限を
// かけると、値を変えるだけで回避されるため。
func ClientIPKey(r *http.Request) string { return clientIP(r) }

// ActorKey は認証済みアクターをキーにする（ApiDesign.md 2.9「アクターあたり」）。
//
// **Authenticate の後に置くこと。** 未認証なら空文字を返して制限をかけないが、
// これは認証ミドルウェアが先に 401 で弾いている前提での縮退動作である。
func ActorKey(r *http.Request) string {
	if p := auth.PrincipalFromContext(r.Context()); p != nil {
		return p.ActorID
	}
	return ""
}

// limiter はキーごとの試行時刻を保持するスライディングウィンドウ。
//
// 固定窓ではなく試行時刻そのものを持つのは、窓の境界で上限の2倍を通して
// しまわないようにするためと、Retry-After を「最古の試行が窓から出るまで」
// として正確に出せるようにするためである。10回/分・600回/分と上限が小さく、
// キーあたり最大でも limit 個の time.Time で足りる。
//
// **カウンタはプロセス内メモリにある。** 再起動で消え、複数プロセス間でも
// 共有されない。PB はローカル端末で単一プロセスとして動く前提のため
// （Design.md 4.3）この範囲で足りる。総当たり対策の本体であるアカウント
// 単位のロックは DB（local_credential）にあり、再起動しても残る。
// 外部ストア（Redis 等）は、複数プロセスで動かす必要が出た時点の課題とする。
type limiter struct {
	limit  int
	window time.Duration
	// now は時刻の取得口。テストが窓をまたぐために差し替える。
	now func() time.Time

	mu        sync.Mutex
	hits      map[string][]time.Time
	lastSweep time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{
		limit:  limit,
		window: window,
		now:    time.Now,
		hits:   make(map[string][]time.Time),
	}
}

// allow は1回の試行を記録し、通してよいかを返す。
//
// 戻り値は (通してよいか, 窓の残り回数, 再試行までの時間) である。
// 通す場合の残り時間は 0、拒む場合の残り回数は 0 になる。
func (l *limiter) allow(key string) (bool, int, time.Duration) {
	now := l.now()
	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	// 使われなくなったキーを窓ごとに1回だけ掃除する。毎回走らせると
	// キー数に比例した時間を全リクエストが負担することになる。
	if now.Sub(l.lastSweep) >= l.window {
		l.sweep(cutoff)
		l.lastSweep = now
	}

	times, known := l.hits[key]
	times = within(times, cutoff)

	// 上限に達したら新しいキーを拒む。古いキーを捨てて場所を空ける方式に
	// すると、送信元を変え続けるだけで正規の利用者のカウンタを追い出せる。
	if !known && len(l.hits) >= maxRateLimitKeys {
		return false, 0, l.window
	}

	if len(times) >= l.limit {
		l.hits[key] = times
		// 最古の試行が窓から出れば1回分空く。
		return false, 0, times[0].Add(l.window).Sub(now)
	}

	l.hits[key] = append(times, now)
	return true, l.limit - len(times) - 1, 0
}

// within は cutoff より後の時刻だけを残す。
//
// 時刻は古い順に並ぶため、落とすのは先頭からの連続した範囲になる。
func within(times []time.Time, cutoff time.Time) []time.Time {
	i := 0
	for i < len(times) && !times[i].After(cutoff) {
		i++
	}
	return times[i:]
}

// sweep は窓から出きったキーを取り除く。呼び出し側が mu を保持していること。
func (l *limiter) sweep(cutoff time.Time) {
	for key, times := range l.hits {
		if len(within(times, cutoff)) == 0 {
			delete(l.hits, key)
		}
	}
}

// retryAfterSeconds は Retry-After に入れる秒数を返す。端数は切り上げる。
//
// **1未満にはしない。** apierr.WithRetryAfter は 0 以下を「値なし」として
// 落とすため、0 を返すとヘッダも retry_after_sec も消え、429 なのに
// 再試行の目安が無い応答になる。
func retryAfterSeconds(d time.Duration) int {
	if d <= time.Second {
		return 1
	}
	return int((d + time.Second - 1) / time.Second)
}
