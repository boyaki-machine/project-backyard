package mcp

import "net/http"

// ServerName はクライアントへ名乗るサーバ名（serverInfo.name）。
//
// **クライアントの設定ファイル（.mcp.json）が付ける名前とは別物である。**
// あちらは利用者が決める接続名（`pb`）で、ツール名の接頭辞になる。
const ServerName = "project-backyard"

// maxBodyBytes は受け付ける本文の上限。
//
// read 系ツールの引数は数十バイトで足りる。上限を置かないと、認証を
// 通った相手がメモリを食い潰せる。
const maxBodyBytes = 1 << 20 // 1MiB

// Handler はツールの定義と、その中身が叩く REST の宛先を持つ。
// HTTP の口は HTTP()（sdk.go）が返す。
type Handler struct {
	// rest は内部呼び出しの宛先（/api/v1 を持つルータ）。
	rest http.Handler
	// version は serverInfo に載せる PB のバージョン。
	version string
	// tools はツール定義。**tools/list の並びは SDK が名前の順に並べ替える**（pb-258）。
	tools []tool
	// delegationEnabled は自律実行が有効か（Delegation.md 1.3）。nil なら有効とみなす
	// （設定を持たない単体テストの既定）。
	delegationEnabled func() bool
}

// delegationToolNames は自律実行のためだけのツール。**自律実行が無効なら tools/list に
// 出さない**（Delegation.md 1.3）——無効でも REST が 403 で拒むが、使えない道具を
// 並べるとエージェントが呼んで空振りする。
var delegationToolNames = map[string]bool{
	"pb_submit_plan":        true,
	"pb_submit_evaluation":  true,
	"pb_raise_confirmation": true,
	"pb_update_handoff":     true,
	"pb_record_metric":      true,
}

// WithDelegationGate は自律実行が有効かを返す関数を渡す。要求ごとに読むので、
// 画面から切り替えたら次の接続から効く。
func (h *Handler) WithDelegationGate(enabled func() bool) *Handler {
	h.delegationEnabled = enabled
	return h
}

// toolVisible はこの要求で tools/list に出すかを返す。
func (h *Handler) toolVisible(name string) bool {
	if !delegationToolNames[name] || h.delegationEnabled == nil {
		return true
	}
	return h.delegationEnabled()
}

// New はハンドラを作る。
//
// rest には /api/v1 配下を解決できるハンドラを渡す（Design.md 8.4 の
// 「MCP 層は REST を内部の HTTP 呼び出しで叩く」）。
func New(rest http.Handler, version string) *Handler {
	// **ここの並びは tools/list の順にならない**——公式 SDK が名前の順に並べ替えて返す
	// （仕様 2026-07-28 がキャッシュのために決まった順を求める。pb-258）。呼ぶ順は
	// /pb-implement の手順が教える。ここでは読みやすさのために流れの順に置いておく：
	// read → コンテキストパック → write → 遷移 → 完了レポート（Requirements.md 10.8.5 / 10.8.6）。
	tools := readTools()
	tools = append(tools, usageTools()...)
	tools = append(tools, workTools()...)
	tools = append(tools, planningTools()...)
	tools = append(tools, contextTools()...)
	tools = append(tools, writeTools()...)
	tools = append(tools, transitionTools()...)
	tools = append(tools, reportTools()...)
	// 待ち受け（pb-158）。評価や返事を待つときに呼ぶ。
	tools = append(tools, waitTools()...)
	return &Handler{rest: rest, version: version, tools: tools}
}
