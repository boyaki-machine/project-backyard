// Package mcp は MCP サーバを持つ（Design.md 8章）。
//
// **プロトコルは公式 SDK（github.com/modelcontextprotocol/go-sdk）に任せる**（8.4 / 8.6）。
// 版の交渉（旧方式の initialize と 2026-07-28 の server/discover の両方）・ヘッダの検証・
// 応答の形（JSON か SSE か）は SDK が持ち、本パッケージはツールの定義と中身だけを持つ。
//
// **本パッケージにビジネスルールを置かないこと**（8.1）。権限判定・検証・
// 状態遷移は REST 層（internal/httpapi）が持ち、ここが行うのは入出力の
// 形を変えることだけである。REST は内部の HTTP 呼び出しで叩く（rest.go）。
package mcp

// JSON-RPC 2.0 のエラーコード（https://www.jsonrpc.org/specification §5.1）。
//
// **MCP はこの層とツールの層で error を分ける**（Design.md 8.4）。ここに
// 並ぶのは「呼び出し側の作りが間違っている」もので、権限不足や見つからない
// といったツールの結果は toolResult.IsError で返す。SDK へは jsonrpc.Error に
// 写して渡す（sdk.go）。
const (
	codeInvalidParams = -32602
	codeInternalError = -32603
)

// rpcError はツールの処理が返す JSON-RPC のエラー（引数の誤りなど）。
type rpcError struct {
	Code    int
	Message string
}

// newError はエラーを組み立てる。
func newError(code int, message string) *rpcError {
	return &rpcError{Code: code, Message: message}
}
