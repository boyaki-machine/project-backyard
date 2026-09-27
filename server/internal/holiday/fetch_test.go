package holiday

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fetcherTo は Google 行きの要求を srv へ振り向ける HTTPFetcher を作る。
// 転送に従わない設定（CheckRedirect）は NewHTTPFetcher のものをそのまま使う。
func fetcherTo(t *testing.T, srv *httptest.Server, gotPath *string) *HTTPFetcher {
	t.Helper()
	f := NewHTTPFetcher("pb-test")
	target, _ := url.Parse(srv.URL)
	f.Client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if gotPath != nil {
			*gotPath = r.URL.EscapedPath()
		}
		r2 := r.Clone(r.Context())
		r2.URL.Scheme, r2.URL.Host = target.Scheme, target.Host
		return http.DefaultTransport.RoundTrip(r2)
	})
	return f
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPFetcher_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != "pb-test" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		_, _ = w.Write([]byte(googleJA))
	}))
	defer srv.Close()
	var path string
	body, err := fetcherTo(t, srv, &path).Fetch(context.Background(), "ja.japanese")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if body != googleJA {
		t.Error("本文が一致しない")
	}
	if path != "/calendar/ical/ja.japanese%23holiday%40group.v.calendar.google.com/public/basic.ics" {
		t.Errorf("path = %q", path)
	}
}

func TestHTTPFetcher_Failures(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"404": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		// 転送に従わない
		"redirect": func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
		},
		"too large": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(strings.Repeat("x", maxBodyBytes+1)))
		},
	}
	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			_, err := fetcherTo(t, srv, nil).Fetch(context.Background(), "ja.japanese")
			var fe *FetchError
			if !errors.As(err, &fe) || fe.Message == "" {
				t.Fatalf("err = %v, want *FetchError", err)
			}
		})
	}
}

func TestHTTPFetcher_RejectsBadID(t *testing.T) {
	_, err := NewHTTPFetcher("").Fetch(context.Background(), "ja.japanese/../../x")
	var fe *FetchError
	if !errors.As(err, &fe) {
		t.Fatalf("err = %v", err)
	}
}
