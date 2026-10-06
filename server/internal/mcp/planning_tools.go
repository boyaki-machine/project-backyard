package mcp

import (
	"encoding/json"
	"net/http"
	"net/url"
)

func planningTools() []tool {
	seq := property{Type: "integer", Description: "プロジェクト内のチケット番号", Minimum: intPtr(1)}
	lag := property{Type: "integer", Description: "依存の日数のずらし。負数も可", Minimum: intPtr(-365), Maximum: intPtr(365)}
	readProps := map[string]property{}
	for _, k := range []string{"parent", "tag", "status", "status_category", "priority", "assignee", "staged", "open", "sprint", "planned_from", "planned_to", "q"} {
		readProps[k] = property{Type: "string", Description: "ガント一覧と同じ絞り込み。parentは親番号。staged/openは真偽値"}
	}
	readProps["staged"] = property{Type: "boolean", Description: "オンステージに絞る"}
	readProps["open"] = property{Type: "boolean", Description: "trueで未完了、falseで完了に絞る"}
	tools := []tool{{Name: "pb_get_planning_data", Description: "計画用にチケットの状態・順序・見積り・日程とFS/SS/FF/SF/blocksの依存を読む。本文を省く。最大5000件、totalとtruncatedで取りこぼしを示す。ticket.view が必要。", InputSchema: schema{Type: "object", Properties: readProps}, call: callPlanningData}}
	for _, op := range []struct {
		name, method, description string
		props                     map[string]property
		required                  []string
	}{
		{"pb_add_link", http.MethodPost, "依存・関連を1件追加する。向きはseqからtarget_seq。FS/SS/FF/SFは時間の制約であり実行順とは限らない。ticket.plan.editが必要。", map[string]property{"seq": seq, "target_seq": seq, "link_type": {Type: "string", Description: "関連の種類", Enum: []string{"FS", "SS", "FF", "SF", "blocks", "relates", "duplicates"}}, "lag_days": lag}, []string{"seq", "target_seq", "link_type"}},
		{"pb_update_link", http.MethodPatch, "既存の依存のずらしだけを変更する。ticket.plan.editが必要。", map[string]property{"seq": seq, "id": {Type: "string", Description: "リンクのID"}, "lag_days": lag}, []string{"seq", "id", "lag_days"}},
		{"pb_delete_link", http.MethodDelete, "既存の依存・関連を1件削除する。ticket.plan.editが必要。", map[string]property{"seq": seq, "id": {Type: "string", Description: "リンクのID"}}, []string{"seq", "id"}},
		{"pb_move_task", http.MethodPost, "同じ親・段の中で順序だけを変える。after_seq/before_seqかposition(first/last)を指定する。担当・状態・日程は変わらない。ticket.plan.editが必要。", map[string]property{"seq": seq, "after_seq": seq, "before_seq": seq, "position": {Type: "string", Description: "同じ親・段での位置", Enum: []string{"first", "last"}}}, []string{"seq"}},
	} {
		name, method := op.name, op.method
		tools = append(tools, tool{Name: name, Description: op.description, InputSchema: schema{Type: "object", Properties: op.props, Required: op.required}, call: func(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
			return callPlanningWrite(h, r, key, args, name, method)
		}})
	}
	return tools
}
func callPlanningWrite(h *Handler, r *http.Request, key string, args json.RawMessage, name, method string) (toolResult, *rpcError) {
	var in map[string]json.RawMessage
	if e := decodeArgs(args, &in); e != nil {
		return toolResult{}, e
	}
	var seq flexInt
	if e := json.Unmarshal(in["seq"], &seq); e != nil {
		return toolResult{}, newError(codeInvalidParams, "seqを指定してください")
	}
	n, e := requireSeq(seq)
	if e != nil {
		return toolResult{}, e
	}
	path := ticketPath(key, n)
	body := map[string]json.RawMessage{}
	allowed := []string{"target_seq", "link_type", "lag_days"}
	if name == "pb_move_task" {
		path += "/move"
		allowed = []string{"after_seq", "before_seq", "position"}
	} else {
		path += "/links"
		if method != http.MethodPost {
			var id string
			if err := json.Unmarshal(in["id"], &id); err != nil || id == "" {
				return toolResult{}, newError(codeInvalidParams, "idを指定してください")
			}
			path += "/" + url.PathEscape(id)
			allowed = []string{"lag_days"}
		}
	}
	// 制限された引数を黙って捨てると、呼び手が変更済みと誤認する。
	for _, k := range []string{"staged", "parent_seq"} {
		if _, ok := in[k]; ok {
			return toolResult{}, newError(codeInvalidParams, "並べ替えでは段と親を変更できません")
		}
	}
	for _, k := range allowed {
		if v, ok := in[k]; ok {
			body[k] = v
		}
	}
	var payload []byte
	if method != http.MethodDelete {
		payload, _ = json.Marshal(body)
	}
	res, err := h.callREST(r, method, path, nil, payload, nil)
	return passThrough(r, res, err)
}
func callPlanningData(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in map[string]json.RawMessage
	if e := decodeArgs(args, &in); e != nil {
		return toolResult{}, e
	}
	if in["page"] != nil || in["per_page"] != nil {
		return toolResult{}, newError(codeInvalidParams, "計画の取得ではページを指定せず、対象を絞ってください")
	}
	q := url.Values{"view": {"gantt"}}
	for _, k := range []string{"parent", "tag", "status", "status_category", "priority", "assignee", "staged", "open", "sprint", "planned_from", "planned_to", "q"} {
		if raw, ok := in[k]; ok {
			var v string
			if k == "staged" || k == "open" {
				var b bool
				if err := json.Unmarshal(raw, &b); err != nil {
					return toolResult{}, newError(codeInvalidParams, k+"は真偽値で指定してください")
				}
				v = string(raw)
			} else if err := json.Unmarshal(raw, &v); err != nil {
				return toolResult{}, newError(codeInvalidParams, k+"は文字列で指定してください")
			}
			if v != "" {
				q.Set(k, v)
			}
		}
	}
	res, err := h.getREST(r, "/projects/"+url.PathEscape(key)+"/tickets", q)
	if err != nil || !res.ok() {
		return passThrough(r, res, err)
	}
	loc, e := h.projectLocation(r, key)
	if e != nil {
		return toolResult{}, e
	}
	var envelope map[string]any
	if err := json.Unmarshal(isoTimes(res.body), &envelope); err != nil {
		return toolResult{}, newError(codeInternalError, "計画の一覧を解釈できません")
	}
	items, _ := envelope["items"].([]any)
	out := make([]any, 0, len(items))
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		row := map[string]any{}
		for _, k := range []string{"seq", "type", "title", "status", "parent_seq", "staged_at", "sort_key", "assignee", "estimate_point", "estimate_hours", "actual_point", "actual_point_version", "start_at", "due_at", "all_day", "version"} {
			if v, ok := item[k]; ok {
				row[k] = v
			}
		}
		start, due := scheduleDates(row, loc)
		if start != "" {
			row["start_date"] = start
		}
		if due != "" {
			row["due_date"] = due
		}
		out = append(out, row)
	}
	envelope["items"] = out
	envelope["timezone"] = loc.String()
	total, _ := envelope["total"].(float64)
	envelope["truncated"] = int(total) > len(out)
	payload, _ := json.Marshal(envelope)
	return textResult(string(payload)), nil
}
