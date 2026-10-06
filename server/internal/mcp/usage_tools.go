package mcp

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
)

func usageTools() []tool {
	props := map[string]property{}
	for _, key := range []string{"from_date", "to_date", "client_kind", "model_name", "model_version"} {
		props[key] = property{Type: "string", Description: map[string]string{"from_date": "開始日（YYYY-MM-DD）。終了日と対で指定。省略時は直近30暦日", "to_date": "終了日を含む基準日付（YYYY-MM-DD）", "client_kind": "提出・評価時点のエージェント種別", "model_name": "提出・評価時点のモデル名", "model_version": "提出・評価時点のモデル版"}[key]}
	}
	paged := map[string]property{}
	for k, v := range props {
		paged[k] = v
	}
	for _, key := range []string{"page", "per_page"} {
		paged[key] = property{Type: "integer", Description: "ページ番号または1ページの件数（上限200）", Minimum: intPtr(1)}
	}
	return []tool{
		{Name: "pb_get_agent_usage", Description: "提出済みの実行・評価の実績を、日別・モデル別に集計する。自己申告のコストと欠測件数を区別し、未提出の作業は含まない。ticket.view が必要。", InputSchema: schema{Type: "object", Properties: props}, call: func(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
			return callUsage(h, r, key, args, false)
		}},
		{Name: "pb_list_agent_runs", Description: "提出済みの実行の明細を読む。本文を含まず、再提出を含む全件の件数とページ情報を返す。欠測はnull。ticket.view が必要。", InputSchema: schema{Type: "object", Properties: paged}, call: func(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
			return callUsage(h, r, key, args, true)
		}},
	}
}
func callUsage(h *Handler, r *http.Request, key string, args json.RawMessage, runs bool) (toolResult, *rpcError) {
	var in struct {
		FromDate     string  `json:"from_date"`
		ToDate       string  `json:"to_date"`
		ClientKind   string  `json:"client_kind"`
		ModelName    string  `json:"model_name"`
		ModelVersion string  `json:"model_version"`
		Page         flexInt `json:"page"`
		PerPage      flexInt `json:"per_page"`
	}
	if e := decodeArgs(args, &in); e != nil {
		return toolResult{}, e
	}
	q := url.Values{}
	for k, v := range map[string]string{"from_date": in.FromDate, "to_date": in.ToDate, "client_kind": in.ClientKind, "model_name": in.ModelName, "model_version": in.ModelVersion} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if in.Page.set {
		q.Set("page", strconv.FormatInt(in.Page.value, 10))
	}
	if in.PerPage.set {
		q.Set("per_page", strconv.FormatInt(in.PerPage.value, 10))
	}
	path := "/projects/" + url.PathEscape(key) + "/agent-usage"
	if runs {
		path += "/runs"
	}
	res, e := h.callREST(r, http.MethodGet, path, q, nil, nil)
	return passThrough(r, res, e)
}
