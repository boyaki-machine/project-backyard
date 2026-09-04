package mcp

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
)

// apiBasePath は REST のベースパス（ApiDesign.md 2.1）。
//
// httpapi.BasePath と同じ値を持つ。あちらを参照すると
// httpapi → mcp → httpapi の循環になるため、写しを1つ置いている。
// **ずれたら結合テストが 404 で落ちる**（mcp_integration_test.go）。
const apiBasePath = "/api/v1"

// restResult は内部呼び出しの結果。
type restResult struct {
	status int
	body   []byte
}

// ok は 2xx かどうかを返す。
func (r restResult) ok() bool { return r.status >= 200 && r.status < 300 }

// getREST は REST の GET を内部で1回叩く（Design.md 8.4）。
//
// **同一プロセス内で同じルータへ渡す。** ネットワークへは出ない。
// こうするのは、8.1 が定める「権限判定・検証は REST 層に置く」を**経路として
// 強制する**ためである——ハンドラを直接呼ぶ形にすると RequireProjectPermission
// を通らない道ができ、権限判定が2か所になる。
//
// **Authorization ヘッダを引き継ぐ**ので、内部呼び出しも同じ認証・認可を通る。
// トークンの照会が1回増えるが、権限の判定が1か所に留まる対価として払う。
func (h *Handler) getREST(r *http.Request, path string, query url.Values) (restResult, error) {
	u := url.URL{Path: apiBasePath + path}
	if len(query) > 0 {
		u.RawQuery = query.Encode()
	}

	// **chi のルーティング文脈を外す。** 外側の /mcp/{key} を解決したときの
	// RouteContext がコンテキストに残っており、そのまま渡すと内側のルータが
	// 「すでに経路が決まっている」と判断して、消費済みのパスで照合する
	// （chi.Mux.ServeHTTP は RouteCtxKey の有無で分岐する）。**手順25 の結合
	// テストで実際に踏んだ**——GET /api/v1/projects/{key} が 405 や 403 になり、
	// 最後は MCP ハンドラ自身へ再入して Body が nil で panic した。
	//
	// nil を入れて型アサーションを外す。request_id など他の値は残るので、
	// ログと監査は外側のリクエストと同じ id で並ぶ。
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, nil)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return restResult{}, fmt.Errorf("内部呼び出しの組み立てに失敗した（%s）: %w", u.String(), err)
	}
	// **資格情報はヘッダごと持ち回る。** Principal をコンテキストで渡す形に
	// すると Authenticate を素通りでき、失効したトークンでも通ってしまう。
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Accept", "application/json")
	// 監査とレート制限が送信元を見るため、外側のリクエストのものを写す。
	req.RemoteAddr = r.RemoteAddr

	rec := &recorder{header: make(http.Header)}
	h.rest.ServeHTTP(rec, req)

	return restResult{status: rec.statusCode(), body: rec.body.Bytes()}, nil
}

// tokenScopeHint は、権限不足の説明に添える1行を返す。
//
// **エージェントの実効権限は所有者のロールとの積である**（Design.md 6.4.1）。
// 403 の原因が「所有者が権限を持たない」なのか「トークンのスコープで絞った」
// なのかは、モデルにも利用者にも区別が付かない。**どちらを見ればよいかだけを
// 伝える**（権限キーの一覧そのものは返さない——8.2 が「トークンや接続情報を
// 返すツールを持たない」と定めており、実効権限は PB の画面が持つ情報である）。
func tokenScopeHint(p *auth.Principal) string {
	if p == nil || !p.IsAgent() {
		return ""
	}
	return "（エージェントの権限は所有者のロールとトークンのスコープの積である。" +
		"PB の /me/agents で発行時のスコープを確認できる）"
}

// recorder は内部呼び出しの応答を受け取る http.ResponseWriter。
//
// **net/http/httptest を使わない。** テスト専用パッケージを実行時のコードから
// 参照しないためで、必要なのは3つのメソッドだけである。
type recorder struct {
	code   int
	header http.Header
	body   bytes.Buffer
}

func (r *recorder) Header() http.Header { return r.header }

func (r *recorder) Write(b []byte) (int, error) {
	if r.code == 0 {
		r.code = http.StatusOK
	}
	return r.body.Write(b)
}

func (r *recorder) WriteHeader(code int) {
	if r.code == 0 {
		r.code = code
	}
}

// statusCode は書かれたステータスを返す。
//
// **WriteHeader も Write も呼ばれなかった場合は 200 である**（net/http の
// 既定と同じ）。0 のまま返すと、呼び出し側が「2xx ではない」と読み違える。
func (r *recorder) statusCode() int {
	if r.code == 0 {
		return http.StatusOK
	}
	return r.code
}
