package holiday

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

// 取得の制限（ApiDesign.md 5.8.3）。
const (
	fetchTimeout = 15 * time.Second
	maxBodyBytes = 2 << 20 // 2MiB
)

// googleIDPattern は holiday_source.google_id の CHECK と揃える。
var googleIDPattern = regexp.MustCompile(`^[a-z]{2}\.[a-z_]+$`)

// ValidGoogleID は Google の暦の識別子（ja.japanese など）の形式を確かめる。
func ValidGoogleID(id string) bool { return googleIDPattern.MatchString(id) }

// GoogleURL は識別子から公開 iCal の URL を組み立てる。
//
// **URL を利用者から受けない。** ホストを固定し、識別子だけを差し込むことで、
// サーバが内部ネットワークへ要求を飛ばす足場（SSRF）にならないようにする
// （ApiDesign.md 5.8.2）。id は ValidGoogleID を通したものに限る——英小文字と
// ドットと下線だけなので、エスケープせずに差し込める。
//
// `#` と `@` は実測で通った形（%23 / %40）に固定する。url.PathEscape は
// `@` をエスケープしない。
func GoogleURL(id string) string {
	return "https://calendar.google.com/calendar/ical/" + id +
		"%23holiday%40group.v.calendar.google.com/public/basic.ics"
}

// Fetcher は Google の暦を取りに行く口。テストで差し替える。
type Fetcher interface {
	Fetch(ctx context.Context, googleID string) (string, error)
}

// FetchError は取得の失敗。Message は画面にそのまま出せる日本語で、
// holiday_source.last_error にも同じ文を書く。
type FetchError struct {
	Message string
	Cause   error
}

func (e *FetchError) Error() string { return fmt.Sprintf("%s: %v", e.Message, e.Cause) }
func (e *FetchError) Unwrap() error { return e.Cause }

// HTTPFetcher は実際に Google へ取りに行く Fetcher。
type HTTPFetcher struct {
	Client    *http.Client
	UserAgent string
}

// NewHTTPFetcher は 5.8.3 の制限（15秒・転送に従わない）を持つ Fetcher を作る。
func NewHTTPFetcher(userAgent string) *HTTPFetcher {
	return &HTTPFetcher{
		Client: &http.Client{
			Timeout: fetchTimeout,
			// **転送には従わない。** 固定したホストから別の場所へ誘導されない
			// ようにする。Google の公開 iCal は転送せずに 200 を返す（実測）。
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		UserAgent: userAgent,
	}
}

// Fetch は本文を返す。本文は 2MiB まで読み、超えたら失敗にする。
func (f *HTTPFetcher) Fetch(ctx context.Context, googleID string) (string, error) {
	if !ValidGoogleID(googleID) {
		return "", &FetchError{Message: "暦の識別子の形式が正しくありません",
			Cause: fmt.Errorf("google_id %q", googleID)}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, GoogleURL(googleID), nil)
	if err != nil {
		return "", &FetchError{Message: "取得の要求を組み立てられません", Cause: err}
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", &FetchError{Message: "Google カレンダーに接続できませんでした", Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", &FetchError{
			Message: fmt.Sprintf("Google カレンダーが %d を返しました", resp.StatusCode),
			Cause:   errors.New(resp.Status),
		}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return "", &FetchError{Message: "Google カレンダーの応答を読み切れませんでした", Cause: err}
	}
	if len(body) > maxBodyBytes {
		return "", &FetchError{Message: "Google カレンダーの応答が大きすぎます",
			Cause: fmt.Errorf("%d バイトを超えた", maxBodyBytes)}
	}
	return string(body), nil
}
