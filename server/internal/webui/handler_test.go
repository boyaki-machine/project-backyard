package webui

import (
	"io/fs"
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

// index.html も placeholder.html も無ければ 500 にする。
// 空の応答や 404 を返すと「画面が出ない」原因が分からなくなる。
func TestMissingIndexAndPlaceholderReturns500(t *testing.T) {
	rec := get(t, newHandler(fstest.MapFS{}), "/")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// client が未ビルド（index.html が無い）のときは placeholder.html を 503 で返す。
//
// **200 にしない。** 監視や自動確認から見て「画面が出ている」と区別が付かなくなる。
// 本文は自己完結の HTML なので、ブラウザではそのまま案内が読める。
func TestMissingIndexServesPlaceholder(t *testing.T) {
	fsys := fstest.MapFS{
		placeholderFile: {Data: []byte("<!doctype html><title>未ビルド</title>ビルドしてください")},
	}
	for _, path := range []string{"/", "/projects", "/admin/users"} {
		rec := get(t, newHandler(fsys), path)
		if rec.Code != http.StatusServiceUnavailable {
			t.Errorf("%s: status = %d, want 503", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "ビルドしてください") {
			t.Errorf("%s: プレースホルダが返っていない: %q", path, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s: Content-Type = %q, want text/html", path, ct)
		}
	}
}

// index.html があれば実ビルドとして 200 で返す（placeholder は使わない）。
func TestIndexWinsOverPlaceholder(t *testing.T) {
	fsys := fstest.MapFS{
		indexFile:       {Data: []byte("<!doctype html>実ビルド")},
		placeholderFile: {Data: []byte("<!doctype html>未ビルド")},
	}
	rec := get(t, newHandler(fsys), "/")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "実ビルド") {
		t.Errorf("index.html が返っていない: %q", rec.Body.String())
	}
}

// 埋め込み済みの dist（プレースホルダだけ）でもハンドラが成立する。
// //go:embed の対象が壊れていればここで落ちる。
//
// **コミットされているのは placeholder.html だけ**なので、素の作業ツリーでは
// 503 が返る。make build 済みなら index.html が入るため 200 になる——
// どちらでも通るよう、ステータスは 200 か 503 のいずれかであることだけを見る。
func TestEmbeddedDistIsServable(t *testing.T) {
	rec := get(t, Handler(), "/")
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 200（ビルド済み）か 503（未ビルド）", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Project Backyard") {
		t.Errorf("埋め込んだ HTML が返っていない: %q", rec.Body.String())
	}
}

// コミットされているプレースホルダは**自己完結**でなければならない。
//
// 外部のスクリプトやスタイルを参照すると、それらは埋め込まれていないため
// SPA フォールバックが HTML を返し、画面が真っ白になる（2026-08-22 まで
// 実際にそうなっていた）。
func TestCommittedPlaceholderIsSelfContained(t *testing.T) {
	sub, err := fs.Sub(embedded, distDir)
	if err != nil {
		t.Fatalf("dist を開けない: %v", err)
	}
	body, err := fs.ReadFile(sub, placeholderFile)
	if err != nil {
		t.Fatalf("%s が埋め込まれていない: %v", placeholderFile, err)
	}
	for _, ng := range []string{"<script src", "<script type=\"module\"", "/assets/", "rel=\"stylesheet\""} {
		if strings.Contains(string(body), ng) {
			t.Errorf("プレースホルダが外部資源を参照している: %q", ng)
		}
	}
	if !strings.Contains(string(body), "make build") {
		t.Error("プレースホルダに次の行動（make build）が書かれていない")
	}
}
