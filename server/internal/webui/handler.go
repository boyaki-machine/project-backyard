package webui

import (
	"bytes"
	"io/fs"
	"net/http"
	"path"
	"strings"
	"time"
)

const (
	distDir   = "dist"
	indexFile = "index.html"

	// Vite は assets/ 配下のファイル名に内容のハッシュを含める（index-DdMrV5xD.js）。
	// 内容が変われば名前も変わるため、恒久的にキャッシュしてよい（Design.md 3.4）。
	assetsDir = "assets"

	immutableCacheControl = "public, max-age=31536000, immutable"
	// index.html はハッシュを持たず、参照するアセット名が毎ビルド変わる。
	// 保持は許すが、必ず再検証させる。
	revalidateCacheControl = "no-cache"
)

// Handler は埋め込んだ SPA を配信するハンドラを返す。
//
// 実在するファイルはそのまま返し、それ以外のパスは index.html を返す
// （SPA フォールバック。Design.md 3.4）。/api /mcp /healthcheck を除外するのは
// 呼び出し側の責務である（httpapi.NewRouter）。
func Handler() http.Handler {
	sub, err := fs.Sub(embedded, distDir)
	if err != nil {
		// dist はコンパイル時に埋め込まれているため、ここへは来ない。
		panic("webui: " + distDir + " を開けない: " + err.Error())
	}
	return newHandler(sub)
}

func newHandler(fsys fs.FS) http.Handler {
	// index.html は全リクエストのフォールバック先であり、内容は起動中変わらない。
	// 読めない場合でもここでは落とさず、フォールバック時に 500 を返す。
	index, err := fs.ReadFile(fsys, indexFile)
	if err != nil {
		index = nil
	}
	return &handler{
		fsys:    fsys,
		files:   http.FileServer(http.FS(fsys)),
		index:   index,
		started: time.Now(),
	}
}

type handler struct {
	fsys  fs.FS
	files http.Handler
	index []byte
	// started は index.html の Last-Modified に使う。埋め込みファイルは
	// 更新時刻を持たないため（embed.FS は常にゼロ値を返す）、起動時刻で代用する。
	started time.Time
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")

	if name != "" && name != indexFile && fileExists(h.fsys, name) {
		if strings.HasPrefix(name, assetsDir+"/") {
			w.Header().Set("Cache-Control", immutableCacheControl)
		} else {
			// favicon などハッシュを持たないファイル。安全側に倒して再検証させる。
			w.Header().Set("Cache-Control", revalidateCacheControl)
		}
		h.files.ServeHTTP(w, r)
		return
	}

	// 画面のルーティングはクライアント側にあるため、未知のパスでも index.html を返す。
	// 実在しない画面かどうかはフロントのルーターが判断する（GuiDesign.md 3.2 の /404）。
	h.serveIndex(w, r)
}

func (h *handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	if h.index == nil {
		http.Error(w, "web client is not built", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", revalidateCacheControl)
	http.ServeContent(w, r, indexFile, h.started, bytes.NewReader(h.index))
}

func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}
