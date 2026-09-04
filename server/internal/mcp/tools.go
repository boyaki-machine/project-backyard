package mcp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
)

// tool は1件のツール定義（Design.md 8.2 / 8.5）。
//
// **description はエージェントの行動を規定する**（8.6）。日本語で書くのは、
// 憲章・チケット・文書がすべて日本語であり、指示する語彙と読む対象の語彙を
// 揃えるためである。
type tool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema schema `json:"inputSchema"`

	// call は REST を1回叩いて結果を組み立てる。JSON には出さない。
	call func(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) `json:"-"`
}

// schema は入力の JSON Schema。**object に限る**（MCP の要求）。
type schema struct {
	Type       string              `json:"type"`
	Properties map[string]property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

type property struct {
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Enum        []string `json:"enum,omitempty"`
	Minimum     *int     `json:"minimum,omitempty"`
	Maximum     *int     `json:"maximum,omitempty"`
}

// callParams は tools/call の params。
type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// toolResult は tools/call の result。
//
// **IsError は「呼び出しは届いたが、結果が失敗だった」を表す**（Design.md 8.4）。
// 権限不足や見つからないはここに入り、モデルが読んで利用者へ伝えられる。
type toolResult struct {
	Content []contentBlock `json:"content"`
	IsError bool           `json:"isError,omitempty"`
}

type contentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// textResult は成功の結果を1つのテキストブロックにする。
func textResult(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}}
}

// errorResult は失敗の結果を1つのテキストブロックにする。
func errorResult(text string) toolResult {
	return toolResult{Content: []contentBlock{{Type: "text", Text: text}}, IsError: true}
}

// perPageMax は 9.2.1 と 2.6 が定める一覧の上限。
const perPageMax = 200

// readTools は手順25 で実装する read 系5件を返す（Design.md 8.2 の read 行から
// pb_get_context を除いたもの。あれは手順27）。
//
// **並び順がそのまま tools/list の順になる。** /pb-onboard が呼ぶ順
// （Requirements.md 10.8.5）に並べてある。
func readTools() []tool {
	return []tool{
		{
			Name: "pb_get_project",
			Description: "このプロジェクトの名前・説明・ワークフロー・メンバー・自分の役割を返す。" +
				"参画したときに最初に呼ぶ。リポジトリの所在は settings.repositories にある。",
			InputSchema: schema{Type: "object", Properties: map[string]property{}},
			call:        callGetProject,
		},
		{
			Name: "pb_list_docs",
			Description: "プロジェクト文書（憲章）の目次を返す。文書ごとのパス・表題と、本文の見出し一覧（outline）を含む。" +
				"本文は含まないので、読む章を決めてから pb_get_doc を呼ぶこと。",
			InputSchema: schema{Type: "object", Properties: map[string]property{}},
			call:        callListDocs,
		},
		{
			Name: "pb_get_doc",
			Description: "プロジェクト文書の本文を Markdown で返す。" +
				"section に見出しを渡すとその章だけを返す。" +
				"価値観・規約・判断基準にあたる文書は全文を読み、それ以外は必要な章だけを読むこと。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"path": {Type: "string", Description: "文書のパス。pb_list_docs が返す path をそのまま渡す（例: rules、rules/naming）"},
					"section": {Type: "string", Description: "見出しテキスト。outline の section をそのまま渡す。" +
						"省略すると全文を返す。見つからないときは、その文書にある見出しの一覧を添えて失敗する"},
				},
				Required: []string{"path"},
			},
			call: callGetDoc,
		},
		{
			Name: "pb_list_tasks",
			Description: "チケットの一覧を軽量な形で返す。ボードの状況把握と、自分の担当を知るために使う。" +
				"1件の詳細（本文・完了条件・関連リンク）が要るときは pb_get_task を呼ぶこと。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"status":          {Type: "string", Description: "ワークフローのステータスキー。カンマ区切りで複数指定すると OR"},
					"status_category": {Type: "string", Description: "todo / in_progress / review / done のいずれか。ワークフローに依存しない4値で、カンマ区切りで複数指定すると OR"},
					"assignee": {Type: "string", Description: "担当者。me で自分（エージェントのトークンでは所有者）、" +
						"none で未割当、アクターの ULID も渡せる。カンマ区切りで複数指定すると OR"},
					"open":     {Type: "boolean", Description: "true で未完了のものだけ、false で完了したものだけ"},
					"parent":   {Type: "string", Description: "チケット番号（seq）。そのチケットと全子孫に絞る。カンマ区切りで複数指定すると OR"},
					"per_page": {Type: "integer", Description: "返す件数。既定 200、上限 200", Minimum: intPtr(1), Maximum: intPtr(perPageMax)},
				},
			},
			call: callListTasks,
		},
		{
			Name: "pb_get_task",
			Description: "チケット1件の契約内容（本文・種別・状態・優先度・完了条件・関連リンク）を返す。" +
				"seq は画面と URL に出るチケット番号である。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"seq": {Type: "integer", Description: "チケット番号（プロジェクト内で一意の連番）", Minimum: intPtr(1)},
				},
				Required: []string{"seq"},
			},
			call: callGetTask,
		},
	}
}

func intPtr(v int) *int { return &v }

// lookup は名前でツールを引く。
func (h *Handler) lookup(name string) *tool {
	for i := range h.tools {
		if h.tools[i].Name == name {
			return &h.tools[i]
		}
	}
	return nil
}

// callTool は tools/call を処理する。
//
// **知らないツール名と壊れた引数は JSON-RPC エラー**（呼び出し側の不具合）、
// **REST が返した 4xx / 5xx は isError のツール結果**（呼び出しの結果）である
// （Design.md 8.4）。
func (h *Handler) callTool(r *http.Request, req rpcRequest) rpcResponse {
	var params callParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errorOf(req.ID, newError(codeInvalidParams, "params を解釈できない"))
		}
	}
	if params.Name == "" {
		return errorOf(req.ID, newError(codeInvalidParams, "name が無い"))
	}

	t := h.lookup(params.Name)
	if t == nil {
		return errorOf(req.ID, newError(codeInvalidParams, "知らないツール: "+params.Name))
	}

	// プロジェクトキーは URL から取る（Design.md 8.3）。トークンのスコープとの
	// 整合は RequireProjectPermission が検証済みである。
	key := chi.URLParam(r, middleware.ProjectKeyURLParam)
	if key == "" {
		return errorOf(req.ID, newError(codeInternalError,
			"ルートに {"+middleware.ProjectKeyURLParam+"} が無い"))
	}

	res, rpcErr := t.call(h, r, key, params.Arguments)
	if rpcErr != nil {
		return errorOf(req.ID, rpcErr)
	}
	return resultOf(req.ID, res)
}

// ── ツール本体 ──────────────────────────────────────────────

func callGetProject(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	if rpcErr := rejectUnknownArgs(args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	res, err := h.getREST(r, "/projects/"+url.PathEscape(key), nil)
	return passThrough(r, res, err)
}

func callListDocs(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	if rpcErr := rejectUnknownArgs(args); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	// **目次は常に outline つきで取る**（ApiDesign.md 10.2）。見出しが無いと、
	// エージェントは「どの章を読むか」を決められず全文を読むことになる。
	res, err := h.getREST(r, "/projects/"+url.PathEscape(key)+"/docs", url.Values{"outline": {"1"}})
	return passThrough(r, res, err)
}

// docArgs は pb_get_doc の引数。
type docArgs struct {
	Path    string `json:"path"`
	Section string `json:"section"`
}

func callGetDoc(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in docArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	path := strings.Trim(in.Path, "/")
	if path == "" {
		return toolResult{}, newError(codeInvalidParams, "path は必須である")
	}

	q := url.Values{}
	if in.Section != "" {
		q.Set("section", in.Section)
	}
	res, err := h.getREST(r, "/projects/"+url.PathEscape(key)+"/docs/"+escapePath(path), q)
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	if !res.ok() {
		return failed(r, res), nil
	}

	// **本文だけを返す**（Design.md 8.5）。JSON でくるむと、Markdown が
	// エスケープされて読みにくくなるうえ、本文の大きさに対して器が無駄になる。
	var doc struct {
		BodyMD string `json:"body_md"`
	}
	if err := json.Unmarshal(res.body, &doc); err != nil {
		return toolResult{}, newError(codeInternalError, "文書の応答を解釈できない: "+err.Error())
	}
	return textResult(doc.BodyMD), nil
}

// taskArgs は pb_get_task の引数。
type taskArgs struct {
	Seq flexInt `json:"seq"`
}

func callGetTask(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in taskArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !in.Seq.set || in.Seq.value < 1 {
		return toolResult{}, newError(codeInvalidParams, "seq は 1 以上の整数である")
	}

	res, err := h.getREST(r,
		"/projects/"+url.PathEscape(key)+"/tickets/"+strconv.FormatInt(in.Seq.value, 10), nil)
	return passThrough(r, res, err)
}

// listArgs は pb_list_tasks の引数（Design.md 8.5）。
//
// **9.2.1 のパラメータをすべては開けていない。** ボードの状況把握と自分の担当を
// 知るのに要るものだけを出している。増やすなら 8.5 の表を先に直すこと。
type listArgs struct {
	Status         string     `json:"status"`
	StatusCategory string     `json:"status_category"`
	Assignee       string     `json:"assignee"`
	Open           *bool      `json:"open"`
	Parent         flexString `json:"parent"`
	PerPage        flexInt    `json:"per_page"`
}

func callListTasks(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in listArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}

	q := url.Values{}
	setIfNotEmpty(q, "status", in.Status)
	setIfNotEmpty(q, "status_category", in.StatusCategory)
	setIfNotEmpty(q, "assignee", resolveAssignee(in.Assignee, auth.PrincipalFromContext(r.Context())))
	setIfNotEmpty(q, "parent", in.Parent.value)
	if in.Open != nil {
		q.Set("open", strconv.FormatBool(*in.Open))
	}
	if in.PerPage.set {
		if in.PerPage.value < 1 || in.PerPage.value > perPageMax {
			return toolResult{}, newError(codeInvalidParams,
				fmt.Sprintf("per_page は 1 以上 %d 以下である", perPageMax))
		}
		q.Set("per_page", strconv.FormatInt(in.PerPage.value, 10))
	}

	res, err := h.getREST(r, "/projects/"+url.PathEscape(key)+"/tickets", q)
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	if !res.ok() {
		return failed(r, res), nil
	}

	light, err := lighten(res.body)
	if err != nil {
		return toolResult{}, newError(codeInternalError, "一覧の応答を解釈できない: "+err.Error())
	}
	return textResult(string(light)), nil
}

// resolveAssignee は me を実際のアクターへ写す（Design.md 8.5）。
//
// **エージェントのアクターに担当は付かない**（担当を持つのは人である）。
// me を文字どおり REST へ渡すと、/pb-onboard の「自分の担当を知る」が必ず
// 0件になる。6.5 の委譲が権限の根拠を所有者に置くのと同じ理由で、担当の
// 視点も所有者に置く。**人のトークンでは AuthzActorID が自分自身を返す**ので、
// 種別で分岐しない。
//
// カンマ区切りの複数指定（9.2.1）を保つため、要素ごとに写す。
func resolveAssignee(value string, p *auth.Principal) string {
	if value == "" || p == nil {
		return value
	}
	parts := strings.Split(value, ",")
	for i, v := range parts {
		if strings.TrimSpace(v) == "me" {
			parts[i] = p.AuthzActorID()
		}
	}
	return strings.Join(parts, ",")
}

// lightItem は pb_list_tasks が残す10項目（Design.md 8.5）。
//
// **すべて json.RawMessage で持つ。** 値の型を写すと、null を取りうる欄
// （priority / assignee / due_date …）ごとにポインタの判断が要り、REST 側が
// 欄を増やしたときに型の食い違いで落ちる。ここが行うのは選別だけである。
type lightItem struct {
	Seq       json.RawMessage `json:"seq"`
	Type      json.RawMessage `json:"type"`
	Title     json.RawMessage `json:"title"`
	Status    json.RawMessage `json:"status"`
	Priority  json.RawMessage `json:"priority"`
	Assignee  json.RawMessage `json:"assignee"`
	ParentSeq json.RawMessage `json:"parent_seq"`
	StagedAt  json.RawMessage `json:"staged_at"`
	DueDate   json.RawMessage `json:"due_date"`
	UpdatedAt json.RawMessage `json:"updated_at"`
}

// lightList は軽量化した一覧。ページングの4項目は 2.6 のまま残す。
type lightList struct {
	Items      []lightItem     `json:"items"`
	Page       json.RawMessage `json:"page,omitempty"`
	PerPage    json.RawMessage `json:"per_page,omitempty"`
	Total      json.RawMessage `json:"total,omitempty"`
	TotalPages json.RawMessage `json:"total_pages,omitempty"`
}

// lighten は 9.2.2 の応答から10項目だけを抜き出す（Design.md 8.5）。
//
// **落とした項目が要るときは pb_get_task が全部を返す。** ボードの状況把握に
// 要らない項目を、件数ぶん掛け算しないための選別である。
func lighten(body []byte) ([]byte, error) {
	var in struct {
		Items      []lightItem     `json:"items"`
		Page       json.RawMessage `json:"page"`
		PerPage    json.RawMessage `json:"per_page"`
		Total      json.RawMessage `json:"total"`
		TotalPages json.RawMessage `json:"total_pages"`
	}
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, err
	}
	out := lightList{
		Items:      in.Items,
		Page:       in.Page,
		PerPage:    in.PerPage,
		Total:      in.Total,
		TotalPages: in.TotalPages,
	}
	if out.Items == nil {
		out.Items = []lightItem{}
	}
	return json.Marshal(out)
}

// ── 共通の組み立て ──────────────────────────────────────────

// passThrough は REST の応答をそのままテキストにする（Design.md 8.5）。
func passThrough(r *http.Request, res restResult, err error) (toolResult, *rpcError) {
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	if !res.ok() {
		return failed(r, res), nil
	}
	return textResult(string(res.body)), nil
}

// failed は REST の 4xx / 5xx を isError のツール結果にする（Design.md 8.4）。
//
// **本文をそのまま添える。** 2.5 のエラー本体には code と message のほか、
// 章が見つからないときの available_sections（10.3）のように**次の一手を選ぶ
// ための情報**が入る。要約すると、それが落ちる。
func failed(r *http.Request, res restResult) toolResult {
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	head := fmt.Sprintf("PB が要求を拒んだ（HTTP %d）", res.status)
	if err := json.Unmarshal(res.body, &body); err == nil && body.Error.Code != "" {
		head = fmt.Sprintf("PB が要求を拒んだ（HTTP %d %s）: %s",
			res.status, body.Error.Code, body.Error.Message)
	}
	if res.status == http.StatusForbidden {
		head += tokenScopeHint(auth.PrincipalFromContext(r.Context()))
	}
	return errorResult(head + "\n" + string(res.body))
}

// setIfNotEmpty は空でない値だけをクエリに載せる。
func setIfNotEmpty(q url.Values, name, value string) {
	if value != "" {
		q.Set(name, value)
	}
}

// escapePath はワイルドカードのパスを、区切りを残したまま安全にする。
func escapePath(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// decodeArgs は arguments を構造体へ入れる。
//
// **引数が無い呼び出しを許す。** MCP のクライアントは引数を持たないツールで
// arguments 自体を省くことがある。
func decodeArgs(args json.RawMessage, dst any) *rpcError {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	if err := json.Unmarshal(args, dst); err != nil {
		return newError(codeInvalidParams, "arguments を解釈できない: "+err.Error())
	}
	return nil
}

// rejectUnknownArgs は引数を取らないツールのための検証。
//
// **知らない引数は黙って捨てる。** 弾くと、気を利かせて余分な欄を付けた
// クライアントが1つも呼べなくなる。ここでは形（オブジェクトであること）
// だけを見る。
func rejectUnknownArgs(args json.RawMessage) *rpcError {
	if len(args) == 0 || string(args) == "null" {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(args, &m); err != nil {
		return newError(codeInvalidParams, "arguments はオブジェクトで渡すこと")
	}
	return nil
}

// flexInt は数値と数字の文字列のどちらでも受ける整数。
//
// **モデルは型を取り違える。** seq を "31" と書いてくる呼び出しを
// -32602 で突き返すより、受けて通したほうが往復が減る。値の妥当性
// （範囲・存在）は REST 層が判定する。
type flexInt struct {
	value int64
	set   bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		s = strings.TrimSpace(str)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("整数として読めない: %s", string(b))
	}
	f.value, f.set = v, true
	return nil
}

// flexString は文字列と数値のどちらでも受ける文字列（parent の "12,30" 用）。
type flexString struct {
	value string
}

func (f *flexString) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
		var str string
		if err := json.Unmarshal(b, &str); err != nil {
			return err
		}
		f.value = strings.TrimSpace(str)
		return nil
	}
	f.value = strings.TrimSpace(s)
	return nil
}
