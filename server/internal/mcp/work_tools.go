package mcp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

func workTools() []tool {
	common := map[string]property{
		"seq":         {Type: "integer", Minimum: intPtr(1), Description: "作業を開始するチケット番号"},
		"request_id":  {Type: "string", Description: "操作ごとの一意なID。同じ内容の再送には同じIDを使う"},
		"note":        {Type: "string", Description: "待機・訂正の理由、取得元、共有作業の配分根拠"},
		"allocations": objectItems("共有作業の配分。shareの合計は1。省略時はこのチケットに全量を記録する", map[string]property{"seq": {Type: "integer", Minimum: intPtr(1)}, "share": {Type: "number"}}, "seq", "share"),
	}
	events := map[string]property{}
	for k, v := range common {
		events[k] = v
	}
	events["work_id"] = property{Type: "string", Description: "pb_start_workが返した作業記録ID"}
	events["action"] = property{Type: "string", Enum: []string{"pause", "resume", "finish", "abandon", "correct", "restore_auto"}, Description: "pause=人の返答待ち、resume=作業再開、finish=実績保存、abandon=未確認の最後の区間を計時せず欠測として終了（note必須）。correct/restore_autoは人の訂正権限が必要"}
	events["result"] = property{Type: "string", Enum: []string{"completed", "partial", "blocked"}, Description: "finishでは必須。チケットの状態は変更しない"}
	events["outcome"] = property{Type: "string", Enum: []string{"completed", "failed"}, Description: "finishで実行結果を明示する。failedには理由とpartial/blockedの作業結果が必要。partial/blockedだけでは失敗と数えない。abandonは放棄として監視する"}
	events["tokens"] = property{Type: "integer", Minimum: intPtr(0), Description: "取得できた実測トークン数。取得不可なら省略（0で代用しない）"}
	events["reported_active_ms"] = property{Type: "integer", Minimum: intPtr(0), Description: "ランタイムが取得した稼働時間。ミリ秒。取得元をnoteに記す。省略時はPB計時を採用"}
	events["change_metrics"] = objectItems("配分先ごとのチケット全体の累積変更量。生成物・記録を除外し、重複加算しない。取得できなければ省略しPoint未算出とする", map[string]property{"seq": {Type: "integer", Minimum: intPtr(1)}, "code_lines": {Type: "integer", Minimum: intPtr(0)}, "test_lines": {Type: "integer", Minimum: intPtr(0)}, "doc_lines": {Type: "integer", Minimum: intPtr(0)}, "other_lines": {Type: "integer", Minimum: intPtr(0)}, "files": {Type: "integer", Minimum: intPtr(0)}, "branches": {Type: "integer", Minimum: intPtr(1)}, "learning_events": {Type: "integer", Minimum: intPtr(0)}, "source": {Type: "string", Description: "集計したリポジトリ・コミット範囲・除外条件などの取得元"}}, "seq", "code_lines", "test_lines", "doc_lines", "other_lines", "files", "branches", "learning_events", "source")
	events["incomplete"] = property{Type: "boolean", Description: "中断や待機申告漏れなどで不完全な計測ならtrue"}
	return []tool{
		{Name: "pb_start_work", Description: "PBサーバの時計で作業を開始する。同じAgentの同じPJで開ける作業は1つ。着手承認後に呼ぶ。複数チケットの共同作業は1記録を配分し二重計上を避ける。", InputSchema: schema{Type: "object", Properties: common, Required: []string{"seq", "request_id"}}, call: callStartWork},
		{Name: "pb_record_work", Description: "共通作業タイマーの待機・再開・終了を記録する。人に質問する直前にpause、返答後にresumeを呼ぶ。finishで開始時の運営ノート契約を用いて実績(時間)/実績Point(actual-v0)をPBが計算し保存する。変更量欠測はPoint未算出。savedで保存状態を確認する。手入力値は保護する。", InputSchema: schema{Type: "object", Properties: events, Required: []string{"seq", "work_id", "request_id", "action"}}, call: callRecordWork},
		{Name: "pb_list_work", Description: "チケットの共通作業記録、欠測・不完全件数、集計値・手入力/自動算出の状態を読む。PBの計測は申告した待機を除く経過時間でありAgent内部の思考時間ではない。", InputSchema: schema{Type: "object", Properties: map[string]property{"seq": common["seq"], "page": {Type: "integer", Minimum: intPtr(1)}, "per_page": {Type: "integer", Minimum: intPtr(1)}}, Required: []string{"seq"}}, call: callListWork},
	}
}
func callStartWork(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	return callWork(h, r, key, args, false, false)
}
func callRecordWork(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	return callWork(h, r, key, args, true, false)
}
func callListWork(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	return callWork(h, r, key, args, false, true)
}
func callWork(h *Handler, r *http.Request, key string, args json.RawMessage, event, list bool) (toolResult, *rpcError) {
	fields := map[string]json.RawMessage{}
	if e := decodeArgs(args, &fields); e != nil {
		return toolResult{}, e
	}
	var n flexInt
	if e := n.UnmarshalJSON(fields["seq"]); e != nil {
		return toolResult{}, newError(codeInvalidParams, "seqは正の整数です")
	}
	seq, e := requireSeq(n)
	if e != nil {
		return toolResult{}, e
	}
	delete(fields, "seq")
	path := ticketPath(key, seq) + "/work-sessions"
	if list {
		query := url.Values{}
		for _, k := range []string{"page", "per_page"} {
			if raw, ok := fields[k]; ok {
				var v flexInt
				if err := v.UnmarshalJSON(raw); err != nil {
					return toolResult{}, newError(codeInvalidParams, k+"は整数です")
				}
				query.Set(k, strconv.FormatInt(v.value, 10))
			}
		}
		res, err := h.callREST(r, http.MethodGet, path, query, nil, nil)
		return passThrough(r, res, err)
	}
	if event {
		var id string
		if json.Unmarshal(fields["work_id"], &id) != nil || id == "" {
			return toolResult{}, newError(codeInvalidParams, "work_idは必須です")
		}
		delete(fields, "work_id")
		path += "/" + url.PathEscape(id) + "/events"
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return toolResult{}, newError(codeInvalidParams, "本文を組み立てられません")
	}
	res, rpc := h.callREST(r, http.MethodPost, path, nil, body, nil)
	return passThrough(r, res, rpc)
}
