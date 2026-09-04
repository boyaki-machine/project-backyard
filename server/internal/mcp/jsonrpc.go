// Package mcp は MCP サーバを持つ（Design.md 8章）。
//
// **実装するのは JSON-RPC 2.0 の4メソッドを話す HTTP ハンドラであって、
// MCP の全機能ではない**（8.4）。SSE ストリーム・セッション・resources /
// prompts・サーバ発の通知を持たない。公式 SDK を使わない判断と、乗り換えを
// 検討する条件は 8.6 にある。
//
// **本パッケージにビジネスルールを置かないこと**（8.1）。権限判定・検証・
// 状態遷移は REST 層（internal/httpapi）が持ち、ここが行うのは入出力の
// 形を変えることだけである。REST は内部の HTTP 呼び出しで叩く（rest.go）。
package mcp

import "encoding/json"

// JSON-RPC 2.0 のバージョン文字列。
const jsonRPCVersion = "2.0"

// JSON-RPC 2.0 のエラーコード（https://www.jsonrpc.org/specification §5.1）。
//
// **MCP はこの層とツールの層で errorを分ける**（Design.md 8.4）。ここに
// 並ぶのは「呼び出し側の作りが間違っている」もので、権限不足や見つからない
// といったツールの結果は toolResult.IsError で返す。
const (
	codeParseError     = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternalError  = -32603
)

// rpcRequest は1件のリクエスト。
//
// **ID が無いものは通知（notification）である。** 応答を返してはならず、
// HTTP では本文なしの 202 を返す（8.4 の notifications/initialized）。
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// isNotification は応答を返さない呼び出しかを返す。
//
// **JSON の null は「ID が無い」ではない。** 仕様上 id: null は通知ではなく
// 不正なリクエストだが、応答を返すこと自体は妨げられないので、ここでは
// 「ID の欄が無い」ときだけ通知として扱う。
func (r rpcRequest) isNotification() bool { return len(r.ID) == 0 }

// rpcResponse は1件の応答。Result と Error はどちらか一方だけを持つ。
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// rpcError は JSON-RPC のエラー本体。
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// newError はエラーを組み立てる。
func newError(code int, message string) *rpcError {
	return &rpcError{Code: code, Message: message}
}

// resultOf は成功応答を組み立てる。
//
// **id をそのまま返す**（数値・文字列のどちらもありうるため RawMessage で
// 持ち回る）。通知に対しては呼ばない。
func resultOf(id json.RawMessage, result any) rpcResponse {
	return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Result: result}
}

// errorOf は失敗応答を組み立てる。
//
// ID が無いリクエスト（解釈できなかった本文を含む）には null を入れる。
// 仕様が「判定できない場合の id は null」と定めているためである。
func errorOf(id json.RawMessage, e *rpcError) rpcResponse {
	if len(id) == 0 {
		id = json.RawMessage("null")
	}
	return rpcResponse{JSONRPC: jsonRPCVersion, ID: id, Error: e}
}
