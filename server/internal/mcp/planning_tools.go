package mcp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
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
	tools = append(tools, tool{Name: "pb_reschedule_task", Description: "タイムラインの連鎖のずらし。きっかけ1件の新しい予定から、時間重複チェックの列と依存の後続を、開始・終了のアンカーを守ってずらす。まず dry_run:true で試算し、応答の changes を確かめ、result が ok なら同じ引数と応答の expect をそのまま渡して dry_run:false で確定する。試算の後に誰かが動かしていれば確定は失敗するので、試算からやり直す。ticket.plan.edit か ticket.edit が必要。", InputSchema: schema{Type: "object", Properties: map[string]property{
		"seq":      seq,
		"dry_run":  {Type: "boolean", Description: "true で試算（書かない）、false で確定"},
		"op":       {Type: "string", Description: "move（既定）は start_at / due_at で動かす。compact は完了したきっかけの終了を完了時刻まで縮め、後ろを前へ寄せる（start_at / due_at を渡さない）", Enum: []string{"move", "compact"}},
		"start_at": {Type: "string", Description: "move の新しい開始。時差を含む ISO8601。省くと今のまま"},
		"due_at":   {Type: "string", Description: "move の新しい終了。時差を含む ISO8601。省くと今のまま。両方省くと、きっかけは動かさずに後ろをそろえ直す"},
		"on_block": {Type: "string", Description: "アンカーに収まらないとき。reject（既定）は拒む、shrink は縮めて収める", Enum: []string{"reject", "shrink"}},
		"expect":   objectItems("確定のときだけ渡す。試算の応答の expect をそのまま渡す", map[string]property{"seq": seq, "version": {Type: "integer", Description: "試算で読んだ版"}}, "seq", "version"),
	}, Required: []string{"seq", "dry_run"}}, call: callReschedule})
	return tools
}

// rescheduleExpect は試算で読んだ行の版（ApiDesign.md 9.17.2 の expect と同じ形）。
type rescheduleExpect struct {
	Seq     int64 `json:"seq"`
	Version int64 `json:"version"`
}

// callReschedule は 9.17.2 を叩く（pb-314）。
//
// **試算の応答に、確定でそのまま渡す expect を添える。** REST はきっかけの版を If-Match で、
// 残りの行の版を expect で受け、**動かないきっかけは changes に載らない**。エージェントに
// 振り分けさせると誤るので、きっかけの版を含めた1本にして渡し、確定で MCP が振り分け直す。
// **確定は expect を必須にする**——試算を見ずに連鎖を書くと、どの行が動いたかを誰も見ていない。
func callReschedule(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in struct {
		Seq     flexInt             `json:"seq"`
		DryRun  *bool               `json:"dry_run"`
		Op      string              `json:"op"`
		StartAt *string             `json:"start_at"`
		DueAt   *string             `json:"due_at"`
		OnBlock string              `json:"on_block"`
		Expect  *[]rescheduleExpect `json:"expect"`
	}
	if e := decodeArgs(args, &in); e != nil {
		return toolResult{}, e
	}
	n, e := requireSeq(in.Seq)
	if e != nil {
		return toolResult{}, e
	}
	if in.DryRun == nil {
		return toolResult{}, newError(codeInvalidParams, "dry_run を渡すこと（まず true で試算する）")
	}
	dryRun := *in.DryRun
	if dryRun && in.Expect != nil {
		return toolResult{}, newError(codeInvalidParams, "試算（dry_run:true）では expect を渡さない")
	}
	if !dryRun && in.Expect == nil {
		return toolResult{}, newError(codeInvalidParams, "確定（dry_run:false）には、試算の応答の expect をそのまま渡すこと")
	}
	body := map[string]any{"dry_run": dryRun}
	if in.Op != "" {
		body["op"] = in.Op
	}
	if in.OnBlock != "" {
		body["on_block"] = in.OnBlock
	}
	for _, f := range []struct {
		arg   *string
		field string
	}{{in.StartAt, "start_at"}, {in.DueAt, "due_at"}} {
		if f.arg == nil {
			continue
		}
		t, err := time.Parse(time.RFC3339, strings.TrimSpace(*f.arg))
		if err != nil {
			return toolResult{}, newError(codeInvalidParams,
				"日時は時差を含む ISO8601 で渡すこと（例: 2026-09-30T17:00:00+09:00）: "+*f.arg)
		}
		body[f.field] = t.UnixMilli()
	}

	path := ticketPath(key, n)
	var version int64
	if dryRun {
		v, failedRes, rpcErr := h.readVersion(r, path, "チケット")
		if rpcErr != nil {
			return toolResult{}, rpcErr
		}
		if failedRes != nil {
			return *failedRes, nil
		}
		version = v
	} else {
		rest := []rescheduleExpect{}
		for _, x := range *in.Expect {
			if x.Seq == n {
				version = x.Version
			} else {
				rest = append(rest, x)
			}
		}
		if version < 1 {
			return toolResult{}, newError(codeInvalidParams,
				"expect にきっかけ（seq）の版が無い。試算の応答の expect をそのまま渡すこと")
		}
		body["expect"] = rest
	}

	raw, _ := json.Marshal(body)
	res, err := h.callREST(r, http.MethodPost, path+"/reschedule", nil, raw, ifMatch(version))
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	if res.status == http.StatusConflict {
		// **409 は isError のツール結果**（8.4）。版の食い違い（きっかけ）と stale_plan（後ろの行）を
		// 分けない——どちらも、試算からやり直せば済む。
		out := failed(r, res)
		out.Content[0].Text = "試算の後に、動かす行のどれかを他の人が変えた。dry_run:true で試算し直し、" +
			"新しい expect で確定すること。\n" + out.Content[0].Text
		return out, nil
	}
	if !res.ok() || !dryRun {
		return passThrough(r, res, nil)
	}
	var out map[string]any
	if err := json.Unmarshal(res.body, &out); err != nil {
		return toolResult{}, newError(codeInternalError, "試算の応答を解釈できない: "+err.Error())
	}
	// 解けない試算には expect を添えない。同じ引数で確定しても 422 になる。
	if out["result"] == "ok" {
		expect := []rescheduleExpect{{Seq: n, Version: version}}
		changes, _ := out["changes"].([]any)
		for _, c := range changes {
			m, _ := c.(map[string]any)
			s, _ := m["seq"].(float64)
			v, _ := m["version"].(float64)
			if int64(s) != n {
				expect = append(expect, rescheduleExpect{Seq: int64(s), Version: int64(v)})
			}
		}
		out["expect"] = expect
	}
	payload, _ := json.Marshal(out)
	return textResult(string(payload)), nil
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
		for _, k := range []string{"seq", "type", "title", "status", "parent_seq", "staged_at", "sort_key", "assignee", "estimate_point", "estimate_hours", "actual_point", "actual_point_version", "start_at", "due_at", "all_day", "no_overlap", "start_pinned", "due_pinned", "version"} {
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
