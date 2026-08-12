package middleware

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

// fixedClock は limiter へ差し込む時計。テストから時刻を進める。
type fixedClock struct{ at time.Time }

func (c *fixedClock) now() time.Time          { return c.at }
func (c *fixedClock) advance(d time.Duration) { c.at = c.at.Add(d) }

func newTestClock() *fixedClock {
	return &fixedClock{at: time.Date(2026, 8, 12, 9, 0, 0, 0, time.UTC)}
}

// 上限まで通り、超えた分は拒む。
func TestLimiterAllowsUpToLimit(t *testing.T) {
	clock := newTestClock()
	l := newLimiter(3, time.Minute)
	l.now = clock.now

	for i := 1; i <= 3; i++ {
		ok, remaining, _ := l.allow("k")
		if !ok {
			t.Fatalf("%d回目が拒まれた（上限3）", i)
		}
		if want := 3 - i; remaining != want {
			t.Errorf("%d回目の残り = %d, want %d", i, remaining, want)
		}
	}

	ok, remaining, retry := l.allow("k")
	if ok {
		t.Fatal("上限を超えたのに通った")
	}
	if remaining != 0 {
		t.Errorf("拒否時の残り = %d, want 0", remaining)
	}
	if retry <= 0 || retry > time.Minute {
		t.Errorf("retry = %v, want (0, 1分]", retry)
	}
}

// 窓が明ければ再び通る。**スライディングなので古い試行から順に空く。**
func TestLimiterSlidesWindow(t *testing.T) {
	clock := newTestClock()
	l := newLimiter(2, time.Minute)
	l.now = clock.now

	l.allow("k") // 9:00:00
	clock.advance(30 * time.Second)
	l.allow("k") // 9:00:30

	if ok, _, _ := l.allow("k"); ok {
		t.Fatal("窓の中に2回あるのに3回目が通った")
	}

	// 9:01:01 では 9:00:00 の1回だけが窓から出ている。
	clock.advance(31 * time.Second)
	if ok, remaining, _ := l.allow("k"); !ok || remaining != 0 {
		t.Fatalf("1回分空いたのに通らない（ok=%v remaining=%d）", ok, remaining)
	}
	if ok, _, _ := l.allow("k"); ok {
		t.Fatal("9:00:30 の試行がまだ窓の中なのに通った")
	}
}

// Retry-After は「最古の試行が窓から出るまで」。固定窓のように窓の
// 残り時間で答えると、実際にはもっと早く空く。
func TestLimiterRetryAfterFollowsOldestHit(t *testing.T) {
	clock := newTestClock()
	l := newLimiter(1, time.Minute)
	l.now = clock.now

	l.allow("k")
	clock.advance(50 * time.Second)

	_, _, retry := l.allow("k")
	if got := retryAfterSeconds(retry); got != 10 {
		t.Errorf("retry_after_sec = %d, want 10", got)
	}
}

// キーが違えば独立して数える。
func TestLimiterCountsPerKey(t *testing.T) {
	l := newLimiter(1, time.Minute)

	if ok, _, _ := l.allow("a"); !ok {
		t.Fatal("a の1回目が拒まれた")
	}
	if ok, _, _ := l.allow("b"); !ok {
		t.Fatal("b が a の消費に巻き込まれた")
	}
	if ok, _, _ := l.allow("a"); ok {
		t.Error("a の2回目が通った")
	}
}

// 使われなくなったキーは掃除される。放置するとメモリが増え続ける。
func TestLimiterSweepsIdleKeys(t *testing.T) {
	clock := newTestClock()
	l := newLimiter(5, time.Minute)
	l.now = clock.now

	for i := range 10 {
		l.allow("k" + strconv.Itoa(i))
	}
	if len(l.hits) != 10 {
		t.Fatalf("キー数 = %d, want 10", len(l.hits))
	}

	// 窓を越えてから別のキーで叩くと、古い10件が掃除される。
	clock.advance(2 * time.Minute)
	l.allow("other")

	if len(l.hits) != 1 {
		t.Errorf("掃除後のキー数 = %d, want 1", len(l.hits))
	}
}

// キー数の上限に達したら新しいキーを拒む。**既存のキーは追い出さない。**
// 追い出す方式だと、送信元を変え続けるだけで正規の利用者を締め出せる。
func TestLimiterRejectsNewKeysAtCapacity(t *testing.T) {
	l := newLimiter(5, time.Minute)
	for i := range maxRateLimitKeys {
		l.allow("k" + strconv.Itoa(i))
	}

	if ok, _, _ := l.allow("new"); ok {
		t.Error("上限を超えて新しいキーを受け入れた")
	}
	if ok, _, _ := l.allow("k0"); !ok {
		t.Error("既存のキーが上限到達に巻き込まれた")
	}
}

// 並行して叩いても上限を超えて通さない（-race で検出する）。
func TestLimiterIsConcurrencySafe(t *testing.T) {
	l := newLimiter(50, time.Minute)

	results := make(chan bool, 200)
	for range 200 {
		go func() { ok, _, _ := l.allow("k"); results <- ok }()
	}

	allowed := 0
	for range 200 {
		if <-results {
			allowed++
		}
	}
	if allowed != 50 {
		t.Errorf("通った数 = %d, want 50", allowed)
	}
}

// ── ミドルウェアとしての振る舞い ────────────────────────────

// serveRateLimited は RateLimit を通したリクエストを実行する。
func serveRateLimited(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// 超過したら 429 rate_limited。ヘッダ3種が付く（ApiDesign.md 2.9）。
func TestRateLimitReturns429(t *testing.T) {
	h := RateLimit(2, time.Minute, ClientIPKey)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

	req := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	}

	first := serveRateLimited(h, req())
	if first.Code != http.StatusNoContent {
		t.Fatalf("1回目 status = %d, want 204", first.Code)
	}
	if got := first.Header().Get(rateLimitHeader); got != "2" {
		t.Errorf("X-RateLimit-Limit = %q, want 2", got)
	}
	if got := first.Header().Get(rateRemainingHeader); got != "1" {
		t.Errorf("X-RateLimit-Remaining = %q, want 1", got)
	}

	serveRateLimited(h, req())

	third := serveRateLimited(h, req())
	if third.Code != http.StatusTooManyRequests {
		t.Fatalf("3回目 status = %d, want 429（body=%s）", third.Code, third.Body.String())
	}
	if got := errorCode(t, third); got != "rate_limited" {
		t.Errorf("code = %q, want rate_limited", got)
	}
	if got := third.Header().Get(rateRemainingHeader); got != "0" {
		t.Errorf("超過時の X-RateLimit-Remaining = %q, want 0", got)
	}
	if got := third.Header().Get("Retry-After"); got == "" || got == "0" {
		t.Errorf("Retry-After = %q（再試行の目安が無い）", got)
	}
}

// 送信元が違えば独立して数える。
func TestRateLimitSeparatesClients(t *testing.T) {
	h := RateLimit(1, time.Minute, ClientIPKey)(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

	for _, addr := range []string{"192.0.2.10:1111", "192.0.2.11:2222"} {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
		r.RemoteAddr = addr
		if w := serveRateLimited(h, r); w.Code != http.StatusNoContent {
			t.Errorf("%s の1回目が拒まれた（status=%d）", addr, w.Code)
		}
	}
}

// アクター単位のキーは Principal から取る（ApiDesign.md 2.9「アクターあたり」）。
func TestActorKey(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
	if got := ActorKey(r); got != "" {
		t.Errorf("未認証の ActorKey = %q, want 空文字", got)
	}

	p := &auth.Principal{ActorID: "01K2F8QW3H7YRJ4M5N6P7Q8R9S"}
	r = r.WithContext(auth.NewPrincipalContext(r.Context(), p))
	if got := ActorKey(r); got != p.ActorID {
		t.Errorf("ActorKey = %q, want %q", got, p.ActorID)
	}
}

// 主体を特定できないリクエストは数えず、素通しする。
func TestRateLimitSkipsEmptyKey(t *testing.T) {
	h := RateLimit(1, time.Minute, func(*http.Request) string { return "" })(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))

	for i := range 3 {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		if w := serveRateLimited(h, r); w.Code != http.StatusNoContent {
			t.Fatalf("%d回目が弾かれた（キーが空なら数えない）", i+1)
		}
	}
}

// Retry-After は切り上げ、かつ 1 を下回らせない。
// 0 だと apierr が値ごと落とし、429 なのに目安の無い応答になる。
func TestRetryAfterSeconds(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want int
	}{
		{-1 * time.Second, 1},
		{0, 1},
		{100 * time.Millisecond, 1},
		{time.Second, 1},
		{1500 * time.Millisecond, 2},
		{10 * time.Second, 10},
		{899500 * time.Millisecond, 900},
	}
	for _, tt := range tests {
		if got := retryAfterSeconds(tt.in); got != tt.want {
			t.Errorf("retryAfterSeconds(%v) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
