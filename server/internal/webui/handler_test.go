package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// fakeDist は実ビルドの client/dist を模した内容。
// 埋め込み済みの dist にはプレースホルダの index.html しか無いため、
// ハッシュ付きアセットの扱いはこちらで確かめる。
func fakeDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":               {Data: []byte("<!doctype html><title>PB</title>")},
		"assets/index-DdMrV5xD.js": {Data: []byte("console.log(1)")},
		"favicon.ico":              {Data: []byte("icon")},
	}
}

func get(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

// 実在するアセットはそのまま返る。
func TestServesExistingAsset(t *testing.T) {
	rec := get(t, newHandler(fakeDist()), "/assets/index-DdMrV5xD.js")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != "console.log(1)" {
		t.Errorf("body = %q", got)
	}
}

// ハッシュ付きアセットは immutable、それ以外は再検証させる（Design.md 3.4）。
func TestCacheControl(t *testing.T) {
	h := newHandler(fakeDist())

	for _, tc := range []struct{ path, want string }{
		{"/assets/index-DdMrV5xD.js", immutableCacheControl},
		{"/favicon.ico", revalidateCacheControl},
		{"/", revalidateCacheControl},
		{"/projects", revalidateCacheControl},
	} {
		if got := get(t, h, tc.path).Header().Get("Cache-Control"); got != tc.want {
			t.Errorf("%s: Cache-Control = %q, want %q", tc.path, got, tc.want)
		}
	}
}

// 未知のパスは index.html を返す（SPA フォールバック）。
// 画面のルーティングはクライアント側にあるため、サーバは 404 にしない。
func TestUnknownPathFallsBackToIndex(t *testing.T) {
	h := newHandler(fakeDist())

	for _, path := range []string{"/", "/projects", "/p/my-app/tickets/31", "/404"} {
		rec := get(t, h, path)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<title>PB</title>") {
			t.Errorf("%s: index.html が返っていない: %q", path, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q", path, ct)
		}
	}
}

// dist から外へ出ようとするパスでファイルを読ませない。
func TestDoesNotEscapeDist(t *testing.T) {
	h := newHandler(fakeDist())

	for _, path := range []string{"/../handler.go", "/assets/../../embed.go"} {
		rec := get(t, h, path)
		if strings.Contains(rec.Body.String(), "package webui") {
			t.Errorf("%s: dist の外のファイルが読めている", path)
		}
	}
}

// index.html が無いビルド成果物を掴んだ場合は 500 にする。
// 空の応答や 404 を返すと「画面が出ない」原因が分からなくなる。
func TestMissingIndexReturns500(t *testing.T) {
	rec := get(t, newHandler(fstest.MapFS{}), "/")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// 埋め込み済みの dist（プレースホルダ）でもハンドラが成立する。
// //go:embed の対象が壊れていればここで落ちる。
func TestEmbeddedDistIsServable(t *testing.T) {
	rec := get(t, Handler(), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Project Backyard") {
		t.Errorf("埋め込んだ index.html が返っていない: %q", rec.Body.String())
	}
}
