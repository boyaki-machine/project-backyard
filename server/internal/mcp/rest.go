package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
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

// callREST は REST を内部で1回叩く（Design.md 8.4）。
//
// **同一プロセス内で同じルータへ渡す。** ネットワークへは出ない。
// こうするのは、8.1 が定める「権限判定・検証は REST 層に置く」を**経路として
// 強制する**ためである——ハンドラを直接呼ぶ形にすると RequireProjectPermission
// を通らない道ができ、権限判定が2か所になる。
//
// **Authorization ヘッダを引き継ぐ**ので、内部呼び出しも同じ認証・認可を通る。
// トークンの照会が1回増えるが、権限の判定が1か所に留まる対価として払う。
//
// body が nil でなければ Content-Type: application/json を付ける。
// header には If-Match のような追加のヘッダを渡す（手順26a の pb_put_doc）。
func (h *Handler) callREST(r *http.Request, method, path string, query url.Values,
	body []byte, header http.Header) (restResult, error) {

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

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return restResult{}, fmt.Errorf("内部呼び出しの組み立てに失敗した（%s %s）: %w", method, u.String(), err)
	}
	// **資格情報はヘッダごと持ち回る。** Principal をコンテキストで渡す形に
	// すると Authenticate を素通りでき、失効したトークンでも通ってしまう。
	req.Header.Set("Authorization", r.Header.Get("Authorization"))
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// **CSRF ヘッダは付けない。** MCP は Bearer だけを受けるので（Design.md 8.3）、
	// 内側の CSRF ミドルウェアは Cookie 認証でないリクエストを素通しする。
	// ここで付けると「Cookie でも通る」ように読める経路が1本増える。

	// 監査とレート制限が送信元を見るため、外側のリクエストのものを写す。
	req.RemoteAddr = r.RemoteAddr

	rec := &recorder{header: make(http.Header)}
	h.rest.ServeHTTP(rec, req)

	return restResult{status: rec.statusCode(), body: isoTimes(rec.body.Bytes())}, nil
}

// isoTimes は REST の応答にある日時を ISO8601 UTC の文字列へ戻す（pb-224）。
//
// **REST はエポックミリ秒、MCP は ISO8601 UTC**（ApiDesign.md 2.2）。MCP は REST を
// 内部で呼んで応答を読むので、ここで戻さないとエポック値がエージェントへ漏れる
// ——エージェントはエポック値の換算を誤りやすい、というのが MCP を ISO に残した理由である。
//
// **名前で見分ける。** キーが `_at` で終わるか `not_before` / `not_after` で、値が整数の
// ものだけを変える。REST の日時はすべてこの形の名前である（apitime.go の Time を
// 通る項目）。JSON でない本文と、変える項目が無い本文はそのまま返す。
func isoTimes(body []byte) []byte {
	if len(body) == 0 || (body[0] != '{' && body[0] != '[') {
		return body
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return body
	}
	if !rewriteTimes(v) {
		return body
	}
	out, err := json.Marshal(v)
	if err != nil {
		return body
	}
	return out
}

// rewriteTimes は v の中の日時を書き換え、1つでも変えたら true を返す。
func rewriteTimes(v any) bool {
	changed := false
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if n, ok := x.(json.Number); ok && isTimeKey(k) {
				if ms, err := n.Int64(); err == nil {
					t[k] = time.UnixMilli(ms).UTC().Format(time.RFC3339)
					changed = true
					continue
				}
			}
			if rewriteTimes(x) {
				changed = true
			}
		}
	case []any:
		for _, x := range t {
			if rewriteTimes(x) {
				changed = true
			}
		}
	}
	return changed
}

func isTimeKey(k string) bool {
	return strings.HasSuffix(k, "_at") || k == "not_before" || k == "not_after"
}

// getREST は REST の GET を内部で1回叩く。callREST の薄いラッパである。
func (h *Handler) getREST(r *http.Request, path string, query url.Values) (restResult, error) {
	return h.callREST(r, http.MethodGet, path, query, nil, nil)
}

// tokenScopeHint は、権限不足の説明に添える1行を返す。
//
// **エージェントの実効権限は所有者のロールとの積である**（Design.md 6.4.1）。
// 403 の原因が「所有者が権限を持たない」なのか「トークンのスコープで絞った」
// なのかは、モデルにも利用者にも区別が付かない。**どちらを見ればよいかだけを
// 伝える**（権限キーの一覧そのものは返さない——8.2 が「トークンや接続情報を
// 返すツールを持たない」と定めており、実効権限は PB の画面が持つ情報である）。
//
// **REST が具体的な理由を返したときは添えない**（手順26b）。ApiDesign.md 9.6 の
// 検証3・4・6 は「エージェントからは行えません」「担当が所有者ではありません」の
// ように、**スコープとは無関係な 403** を返す。そこへスコープを見よと足すと、
// モデルを誤った方向へ送る——実際、検証6 を入れた直後の実サーバ検証で、
// 担当が付いていないことが原因の 403 に「スコープを確認せよ」が付いた。
//
// 判定は**既定文言かどうか**で行う（apierr.DefaultMessage）。ミドルウェアが
// 素の 403 を返したときだけ message が既定のままで、ハンドラが理由を作った
// ときは WithMessage で上書きされている。
func tokenScopeHint(p *auth.Principal, message string) string {
	if p == nil || !p.IsAgent() {
		return ""
	}
	if message != "" && message != apierr.DefaultMessage(apierr.Forbidden) {
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
