package mcp

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// ServerName は initialize の応答に載せるサーバ名。
//
// **クライアントの設定ファイル（.mcp.json）が付ける名前とは別物である。**
// あちらは利用者が決める接続名（`pb`）で、ツール名の接頭辞になる。
const ServerName = "project-backyard"

// protocolVersions は PB が話せる MCP のプロトコル版（新しい順）。
//
// **クライアントが送ってきた版がこの中にあれば、それをそのまま返す**
// （Design.md 8.4）。無ければ先頭（PB の最新）を返し、クライアントに
// 切断するかどうかを選ばせる。
var protocolVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

// maxBodyBytes は受け付ける本文の上限。
//
// read 系ツールの引数は数十バイトで足りる。上限を置かないと、認証を
// 通った相手がメモリを食い潰せる。
const maxBodyBytes = 1 << 20 // 1MiB

// Handler は /mcp/{key} を処理する（Design.md 8.3）。
//
// **プロジェクトキーは URL から取る**——トークンのスコープとの整合は
// ルート定義の RequireProjectPermission が検証済みである（8.3）。
type Handler struct {
	// rest は内部呼び出しの宛先（/api/v1 を持つルータ）。
	rest http.Handler
	// version は initialize の serverInfo に載せる PB のバージョン。
	version string
	// tools はツール定義。並び順がそのまま tools/list の順になる。
	tools []tool
}

// New はハンドラを作る。
//
// rest には /api/v1 配下を解決できるハンドラを渡す（Design.md 8.4 の
// 「MCP 層は REST を内部の HTTP 呼び出しで叩く」）。
func New(rest http.Handler, version string) *Handler {
	return &Handler{rest: rest, version: version, tools: readTools()}
}

// ServeHTTP は Streamable HTTP の POST 経路だけを実装する（Design.md 8.4）。
//
// **GET は 405 である。** サーバ発の通知を持たないため SSE ストリームが要らず、
// 応答は常に application/json で返す。
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// SSE ストリームの開設（GET）と、セッションの終了（DELETE）を
		// 実装していないことが、そのままこの応答になる。
		apierr.WriteCode(w, r, apierr.MethodNotAllowed)
		return
	}

	// **Cookie では通さない**（Design.md 8.3）。CSRF の検証を持たない POST を
	// 1本増やさないためである。ミドルウェアは Cookie を優先して認証するので、
	// ブラウザから叩かれるとここに到達する。
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(
			errors.New("/mcp が Authenticate の後に置かれていない")))
		return
	}
	if p.Source != auth.SourceBearer {
		apierr.Write(w, r, apierr.New(apierr.Unauthenticated).WithCause(
			errors.New("MCP は Bearer トークンだけを受ける（Design.md 8.3）")))
		return
	}

	// **Body が nil のこともある。** サーバが受けるリクエストでは常に
	// non-nil だが、プロセス内で組み立てたリクエストは nil を持ちうる。
	// io.LimitReader(nil, …) は読んだ瞬間に panic する。
	if r.Body == nil {
		writeRPC(w, r, errorOf(nil, newError(codeInvalidRequest, "本文が無い")))
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		writeRPC(w, r, errorOf(nil, newError(codeParseError, "本文を読み取れない")))
		return
	}

	// **配列（バッチ）は受けない。** MCP は 2025-06-18 で JSON-RPC の
	// バッチを外しており、PB も1リクエスト1件だけを扱う。struct への
	// unmarshal では型エラー（＝parse error）に見えてしまうので、先に分ける。
	if trimmed := strings.TrimLeft(string(body), " \t\r\n"); strings.HasPrefix(trimmed, "[") {
		writeRPC(w, r, errorOf(nil, newError(codeInvalidRequest,
			"JSON-RPC のバッチは受け付けない。1リクエストにつき1件で送ること")))
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeRPC(w, r, errorOf(nil, newError(codeParseError, "本文が JSON として壊れている")))
		return
	}
	if req.JSONRPC != jsonRPCVersion || req.Method == "" {
		writeRPC(w, r, errorOf(req.ID, newError(codeInvalidRequest,
			`jsonrpc は "2.0"、method は必須である`)))
		return
	}

	// **通知には応答を返さない**（Design.md 8.4）。知らない通知は黙って
	// 捨てる——仕様がそう定めており、ここで method not found を返すと
	// クライアントが受け取れない応答を送りつけることになる。
	if req.isNotification() {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	writeRPC(w, r, h.dispatch(r, req))
}

// dispatch はメソッド1件を処理する。
func (h *Handler) dispatch(r *http.Request, req rpcRequest) rpcResponse {
	switch req.Method {
	case "initialize":
		return resultOf(req.ID, h.initialize(req.Params))
	case "tools/list":
		return resultOf(req.ID, listResult{Tools: h.tools})
	case "tools/call":
		return h.callTool(r, req)
	case "ping":
		// 仕様上、応答は空のオブジェクトでよい。
		return resultOf(req.ID, struct{}{})
	default:
		return errorOf(req.ID, newError(codeMethodNotFound,
			"知らないメソッド: "+req.Method))
	}
}

// initializeResult は initialize の応答（Design.md 8.4）。
type initializeResult struct {
	ProtocolVersion string       `json:"protocolVersion"`
	Capabilities    capabilities `json:"capabilities"`
	ServerInfo      serverInfo   `json:"serverInfo"`
}

// capabilities は PB が持つ面。**tools だけを持つ。**
//
// resources / prompts / logging / completions は実装していないので、
// 欄そのものを出さない（空のオブジェクトを出すと「持っている」になる）。
type capabilities struct {
	Tools toolsCapability `json:"tools"`
}

// toolsCapability の listChanged は「ツールの増減を通知するか」。
// サーバ発の通知を持たないため false である（Design.md 8.4）。
type toolsCapability struct {
	ListChanged bool `json:"listChanged"`
}

type serverInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// listResult は tools/list の応答。
type listResult struct {
	Tools []tool `json:"tools"`
}

// initialize はプロトコル版を合意し、PB が持つ面を返す。
//
// **params が壊れていても失敗にしない。** 版の交渉は「クライアントの希望を
// 受け取る」だけで、読めなければ PB の最新版を提示すれば合意は成立する。
func (h *Handler) initialize(params json.RawMessage) initializeResult {
	var in struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if len(params) > 0 {
		_ = json.Unmarshal(params, &in)
	}

	version := protocolVersions[0]
	for _, v := range protocolVersions {
		if v == in.ProtocolVersion {
			version = v
			break
		}
	}

	return initializeResult{
		ProtocolVersion: version,
		Capabilities:    capabilities{Tools: toolsCapability{ListChanged: false}},
		ServerInfo:      serverInfo{Name: ServerName, Version: h.version},
	}
}

// writeRPC は応答を1件書く。
//
// **HTTP のステータスは常に 200 である。** JSON-RPC のエラーは本文で表す。
// 認証・認可の失敗だけが HTTP のステータスで返る（Design.md 8.4 の表）が、
// それはこの関数へ来る前に apierr が書いている。
func writeRPC(w http.ResponseWriter, r *http.Request, res rpcResponse) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(res); err != nil {
		// 応答の途中で切れている。ステータスは送信済みなので書き直せない。
		slog.WarnContext(r.Context(), "MCP の応答を書けなかった",
			slog.String("request_id", apierr.RequestIDFromContext(r.Context())),
			slog.String("cause", err.Error()),
		)
	}
}
