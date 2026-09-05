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

// property は1つの引数のスキーマ。
//
// **Items / Properties は手順26c で足した。** pb_submit_result の引数
// （Requirements.md 10.6.1 のレポート）が配列とオブジェクトの入れ子を持つため
// である。**ここが表せないと、モデルは中身の形を知らないまま埋めることになる**
// ——description で言葉にするより、スキーマで宣言したほうが取り違えが減る。
type property struct {
	Type        string              `json:"type"`
	Description string              `json:"description"`
	Enum        []string            `json:"enum,omitempty"`
	Minimum     *int                `json:"minimum,omitempty"`
	Maximum     *int                `json:"maximum,omitempty"`
	Items       *property           `json:"items,omitempty"`
	Properties  map[string]property `json:"properties,omitempty"`
	Required    []string            `json:"required,omitempty"`
}

// objectItems は「オブジェクトの配列」を1行で書くための小道具。
func objectItems(desc string, props map[string]property, required ...string) property {
	return property{
		Type:        "array",
		Description: desc,
		Items: &property{
			Type:       "object",
			Properties: props,
			Required:   required,
		},
	}
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

// lightItem は pb_list_tasks が残す11項目（Design.md 8.5）。
//
// **すべて json.RawMessage で持つ。** 値の型を写すと、null を取りうる欄
// （priority / assignee / due_date …）ごとにポインタの判断が要り、REST 側が
// 欄を増やしたときに型の食い違いで落ちる。ここが行うのは選別だけである。
type lightItem struct {
	Seq      json.RawMessage `json:"seq"`
	Type     json.RawMessage `json:"type"`
	Title    json.RawMessage `json:"title"`
	Status   json.RawMessage `json:"status"`
	Priority json.RawMessage `json:"priority"`
	Assignee json.RawMessage `json:"assignee"`
	// WorkingAgent は「誰が実際に処理しているか」（手順26b で11項目目にした）。
	//
	// **一覧に残すのは、排他が無いためである。** 同じ所有者の別のエージェントが
	// 既に触ったチケットを、それと知らずにもう一度進めることが起こりうる
	// （ApiDesign.md 9.6 は上書きを許す）。/pb-onboard の
	// pb_list_tasks(assignee=me) でこれが見えていれば、モデルが気づける。
	WorkingAgent json.RawMessage `json:"working_agent"`
	ParentSeq    json.RawMessage `json:"parent_seq"`
	StagedAt     json.RawMessage `json:"staged_at"`
	DueDate      json.RawMessage `json:"due_date"`
	UpdatedAt    json.RawMessage `json:"updated_at"`
}

// lightList は軽量化した一覧。ページングの4項目は 2.6 のまま残す。
type lightList struct {
	Items      []lightItem     `json:"items"`
	Page       json.RawMessage `json:"page,omitempty"`
	PerPage    json.RawMessage `json:"per_page,omitempty"`
	Total      json.RawMessage `json:"total,omitempty"`
	TotalPages json.RawMessage `json:"total_pages,omitempty"`
}

// lighten は 9.2.2 の応答から11項目だけを抜き出す（Design.md 8.5）。
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
		head += tokenScopeHint(auth.PrincipalFromContext(r.Context()), body.Error.Message)
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

// ── write 系（手順26a。Design.md 8.5.1）─────────────────────

// writeTools は手順26a で実装する write 系3件を返す。
//
// **並び順は 10.7.1 の開発フローに合わせてある**——議論の結果を起票し
// （pb_create_ticket）、実装中に分かったことを書き（pb_post_note）、指示が
// あれば憲章へ反映する（pb_put_doc）。tools/list はこの順で出る。
//
// **pb_claim_task / pb_release_task は 26b、pb_submit_result は 26c**
// （Design.md 8.2）。
func writeTools() []tool {
	return []tool{
		{
			Name: "pb_create_ticket",
			Description: "チケットを1件起票する。議論の結果として「これは別の作業だ」と決まったものを、" +
				"その場で PB に残すために使う。作ったチケットは必ずバックログに入り、" +
				"担当も状態も後から人が決められる。**勝手に着手しないこと。**",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"type":    {Type: "string", Description: "チケットの種別", Enum: []string{"epic", "story", "task"}},
					"title":   {Type: "string", Description: "1〜200文字。何をするかが1行で分かる文にする"},
					"body_md": {Type: "string", Description: "本文（Markdown）。背景・やること・完了の見分け方を書く"},
					"priority": {Type: "string", Description: "優先度。省略すると未設定",
						Enum: []string{"lowest", "low", "medium", "high", "highest"}},
					"parent_seq": {Type: "integer", Description: "親チケットの番号（seq）。省略するとトップレベル", Minimum: intPtr(1)},
					"assignee_id": {Type: "string", Description: "担当者。me で自分（エージェントのトークンでは所有者）、" +
						"アクターの ULID も渡せる。省略すると未割当。**当該プロジェクトのメンバーであること**"},
				},
				Required: []string{"type", "title"},
			},
			call: callCreateTicket,
		},
		{
			Name: "pb_post_note",
			Description: "チケットにコメントを1件書く。途中経過・判明した事実・試して駄目だったことを、" +
				"次に同じ場所を触る人が読める形で残すために使う。" +
				"kind で種類を選ぶと、あとから決定や注意点だけを拾える。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"seq":     {Type: "integer", Description: "チケット番号（seq）", Minimum: intPtr(1)},
					"body_md": {Type: "string", Description: "本文（Markdown）。1文字以上"},
					"kind": {Type: "string", Description: "種類。既定は discussion。" +
						"decision=決めたこと、caveat=次の人が踏む落とし穴、artifact=成果物の所在、" +
						"reference=参照先、progress=途中経過",
						Enum: []string{"discussion", "decision", "artifact", "caveat", "reference", "progress"}},
				},
				Required: []string{"seq", "body_md"},
			},
			call: callPostNote,
		},
		{
			Name: "pb_put_doc",
			Description: "プロジェクト文書（憲章）の本文を書き換える。**全置換である**——" +
				"pb_get_doc で全文を読み、直した全文を渡すこと。章だけを差し替える口は無い。" +
				"**憲章は全参加者を縛るので、権限を持つ人が明示的に指示したときにだけ呼ぶこと。**" +
				"自分の判断で書き換えてはならない。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"path":    {Type: "string", Description: "文書のパス。pb_list_docs が返す path をそのまま渡す（例: rules、rules/naming）"},
					"body_md": {Type: "string", Description: "**文書全体**の Markdown。渡した内容で本文がまるごと置き換わる"},
					"change_reason": {Type: "string", Description: "何をなぜ変えたかを200文字以内で。履歴に残り、" +
						"あとから版を選ぶときの手がかりになる"},
				},
				Required: []string{"path", "body_md"},
			},
			call: callPutDoc,
		},
	}
}

// createTicketArgs は pb_create_ticket の引数（Design.md 8.5.1）。
//
// **9.3 のフィールド名に揃えてある。** Requirements.md 10.3.2 は body / parent /
// assignee と書いていたが、名前が ApiDesign.md と一致していれば、エージェントは
// 迷ったときに設計文書を引ける（8.5）。
//
// **tag_ids / sprint_id / 見積 / 日付は開けていない**——いずれも ULID か画面の
// 文脈が要り、エージェントが持たない。増やすときは 8.5.1 の表を先に直すこと。
type createTicketArgs struct {
	Type       string  `json:"type"`
	Title      string  `json:"title"`
	BodyMD     string  `json:"body_md"`
	Priority   string  `json:"priority"`
	ParentSeq  flexInt `json:"parent_seq"`
	AssigneeID string  `json:"assignee_id"`
}

func callCreateTicket(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in createTicketArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if strings.TrimSpace(in.Type) == "" {
		return toolResult{}, newError(codeInvalidParams, "type は必須である（epic / story / task）")
	}
	if strings.TrimSpace(in.Title) == "" {
		return toolResult{}, newError(codeInvalidParams, "title は必須である")
	}

	// **本文は「送られた項目だけ」を組み立てる。** 空文字を載せると、9.3 が
	// 任意と定める欄に空を明示したことになり、既定の解釈が変わりうる。
	body := map[string]any{"type": in.Type, "title": in.Title}
	if in.BodyMD != "" {
		body["body_md"] = in.BodyMD
	}
	if in.Priority != "" {
		body["priority"] = in.Priority
	}
	if in.ParentSeq.set {
		body["parent_seq"] = in.ParentSeq.value
	}
	// **me は所有者を指す**（8.5 の assignee と同じ写し方）。エージェントは
	// アクターの ULID を知らないため、me が無いと担当を付ける経路が実質無い。
	if a := resolveAssignee(in.AssigneeID, auth.PrincipalFromContext(r.Context())); a != "" {
		body["assignee_id"] = a
	}

	raw, err := json.Marshal(body)
	if err != nil {
		return toolResult{}, newError(codeInternalError, "本文の組み立てに失敗した: "+err.Error())
	}
	res, err := h.callREST(r, http.MethodPost,
		"/projects/"+url.PathEscape(key)+"/tickets", nil, raw, nil)
	return passThrough(r, res, err)
}

// postNoteArgs は pb_post_note の引数（Design.md 8.5.1）。
//
// **in_reply_to は開けていない。** 同じチケットのコメントの ULID を指す欄だが
// （9.8）、エージェントがその ULID を得る経路が無い。
type postNoteArgs struct {
	Seq    flexInt `json:"seq"`
	BodyMD string  `json:"body_md"`
	Kind   string  `json:"kind"`
}

func callPostNote(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in postNoteArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !in.Seq.set || in.Seq.value < 1 {
		return toolResult{}, newError(codeInvalidParams, "seq は 1 以上の整数である")
	}
	if strings.TrimSpace(in.BodyMD) == "" {
		return toolResult{}, newError(codeInvalidParams, "body_md は必須である")
	}

	body := map[string]any{"body_md": in.BodyMD}
	if in.Kind != "" {
		body["kind"] = in.Kind
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return toolResult{}, newError(codeInternalError, "本文の組み立てに失敗した: "+err.Error())
	}
	res, err := h.callREST(r, http.MethodPost,
		"/projects/"+url.PathEscape(key)+"/tickets/"+strconv.FormatInt(in.Seq.value, 10)+"/comments",
		nil, raw, nil)
	return passThrough(r, res, err)
}

// putDocArgs は pb_put_doc の引数（Design.md 8.5.1）。
type putDocArgs struct {
	Path         string `json:"path"`
	BodyMD       string `json:"body_md"`
	ChangeReason string `json:"change_reason"`
}

// callPutDoc は文書の本文を全置換する。
//
// **内部で2往復する。** 10.4 が PATCH に If-Match を必須とする一方、pb_get_doc は
// 本文の Markdown しか返さないので（8.5）**エージェントは version を持てない**。
// GET で読んで If-Match に載せる。**これは MCP が独自のルールを持つことにはならない**
// ——楽観ロックの判定は REST 側のままで、ここがしているのは「エージェントが
// 渡せない値を、同じ REST から取ってくる」ことだけである（8.1 / 8.5.1）。
func callPutDoc(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in putDocArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	path := strings.Trim(in.Path, "/")
	if path == "" {
		return toolResult{}, newError(codeInvalidParams, "path は必須である")
	}
	// **空文字は弾く。** 10.4 の PATCH は body_md を任意とするので、空のまま
	// 送ると「本文を空にする更新」として通ってしまう。全置換のツールで
	// 引数を省いた呼び出しが憲章を消すのは、事故として重い。
	if in.BodyMD == "" {
		return toolResult{}, newError(codeInvalidParams,
			"body_md は必須である（本文を全置換するツールなので、空では呼べない）")
	}

	docPath := "/projects/" + url.PathEscape(key) + "/docs/" + escapePath(path)

	// ① いまの version を読む。**403 / 404 はここで出る**ので、書く前に返せる。
	cur, err := h.getREST(r, docPath, nil)
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	if !cur.ok() {
		return failed(r, cur), nil
	}
	var doc struct {
		Version int64 `json:"version"`
	}
	if err := json.Unmarshal(cur.body, &doc); err != nil {
		return toolResult{}, newError(codeInternalError, "文書の応答を解釈できない: "+err.Error())
	}
	if doc.Version < 1 {
		return toolResult{}, newError(codeInternalError, "文書の応答に version が無い")
	}

	// ② 書き戻す。
	body := map[string]any{"body_md": in.BodyMD}
	if in.ChangeReason != "" {
		body["change_reason"] = in.ChangeReason
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return toolResult{}, newError(codeInternalError, "本文の組み立てに失敗した: "+err.Error())
	}
	// 引用符付きの entity-tag で送る（2.8 / 5.5 の例と同じ形）。
	header := http.Header{"If-Match": []string{`"` + strconv.FormatInt(doc.Version, 10) + `"`}}

	res, err := h.callREST(r, http.MethodPatch, docPath, nil, raw, header)
	if err != nil {
		return toolResult{}, newError(codeInternalError, err.Error())
	}
	if res.status == http.StatusConflict {
		// **409 は isError のツール結果**（8.4）。読み直してやり直すのは
		// モデルが判断できることで、プロトコルの誤りではない。
		return errorResult("この文書は、読んでから書くまでのあいだに他の人が更新した。" +
			"pb_get_doc で読み直し、その内容に自分の変更を重ねてから、もう一度 pb_put_doc を呼ぶこと。\n" +
			string(res.body)), nil
	}
	if !res.ok() {
		return failed(r, res), nil
	}
	return textResult(string(res.body)), nil
}

// ── 遷移系（手順26b。Design.md 8.5.3）──────────────────────

// transitionTools は手順26b で実装する2件を返す。
//
// **叩く REST（9.6 / 9.7）は Phase 1 から在る。** 設計原則7 が「エージェントから
// 見える面は MCP のみ」と定めているのに、状態遷移だけが REST に在って MCP に
// 無かった——Requirements.md 10.3.2 が pb_claim_task の説明に「着手宣言。
// ステータスを『実装中』へ」と書いていたため、**状態を動かす機能がリースの中に
// 埋まって見えなくなっていた**（Design.md 8.2）。
//
// **リース（pb_claim_task / pb_release_task）は Phase 3 へ送った**
// （Requirements.md 10.3.3）。排他が実際に要るのは自律取得（pb_next_task）からで、
// Phase 2 は人がチケット番号を指定して走らせる。
//
// **並び順は「見てから動かす」。** 先に pb_list_transitions を置くのは、進める先と
// 進めない理由を1往復で知ってから pb_transition_task を呼ぶ流れにするためである。
func transitionTools() []tool {
	return []tool{
		{
			Name: "pb_list_transitions",
			Description: "このチケットがいまどの状態へ進めるかを、進めない先の理由つきで返す。" +
				"状態を変える前にこれを呼ぶこと。" +
				"進めない理由には「担当が自分の所有者でない」「人しか通せない順路である」などがあり、" +
				"そのまま利用者に伝えれば次に何をすればよいかが分かる。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"seq": {Type: "integer", Description: "チケット番号（seq）", Minimum: intPtr(1)},
				},
				Required: []string{"seq"},
			},
			call: callListTransitions,
		},
		{
			Name: "pb_transition_task",
			Description: "チケットの状態を1つ進める。着手するときは、まずこれで進行中にすること" +
				"（同時に「自分が処理している」という記録がチケットに残る）。" +
				"**進められるのは、自分の所有者が担当になっているチケットだけである。**" +
				"担当が付いていなければ、進めずに利用者へ伝えること。" +
				"**チケットを完了にすることはできない**——完了は人が確認して行う。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"seq": {Type: "integer", Description: "チケット番号（seq）", Minimum: intPtr(1)},
					"to": {Type: "string", Description: "遷移先のステータスキー（例: in_progress、review）。" +
						"表示名（「進行中」）ではない。pb_list_transitions が返す key をそのまま渡す"},
					"comment": {Type: "string", Description: "この遷移に添えるコメント（Markdown）。" +
						"なぜこの状態にしたかを1〜2行で書くと、次に見た人が経緯を辿れる。" +
						"チケットのコメント欄に残る"},
				},
				Required: []string{"seq", "to"},
			},
			call: callTransitionTask,
		},
	}
}

// transitionArgs は pb_transition_task の引数（Design.md 8.5.3）。
type transitionArgs struct {
	Seq     flexInt `json:"seq"`
	To      string  `json:"to"`
	Comment string  `json:"comment"`
}

func callListTransitions(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in taskArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !in.Seq.set || in.Seq.value < 1 {
		return toolResult{}, newError(codeInvalidParams, "seq は 1 以上の整数である")
	}
	res, err := h.getREST(r,
		"/projects/"+url.PathEscape(key)+"/tickets/"+
			strconv.FormatInt(in.Seq.value, 10)+"/transitions", nil)
	return passThrough(r, res, err)
}

// callTransitionTask は状態を1つ進める。
//
// **comment を開けているのは、9.6 が同じトランザクションでコメントを作るためである**
// （Design.md 8.5.3）。pb_post_note を別に呼ばせると2往復になり、途中で落ちると
// 遷移だけが残って経緯が残らない。
//
// **working_agent_id は MCP 層では触らない。** REST 側の副作用であり
// （ApiDesign.md 9.6）、人が画面から遷移したときと同じ経路を通る。ここで書くと
// 同じ規則が2か所に生まれる（8.1）。
func callTransitionTask(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	var in transitionArgs
	if rpcErr := decodeArgs(args, &in); rpcErr != nil {
		return toolResult{}, rpcErr
	}
	if !in.Seq.set || in.Seq.value < 1 {
		return toolResult{}, newError(codeInvalidParams, "seq は 1 以上の整数である")
	}
	if strings.TrimSpace(in.To) == "" {
		return toolResult{}, newError(codeInvalidParams,
			"to は必須である（ステータスキー。pb_list_transitions が返す key を渡すこと）")
	}

	body := map[string]any{"to": strings.TrimSpace(in.To)}
	if in.Comment != "" {
		body["comment"] = in.Comment
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return toolResult{}, newError(codeInternalError, "本文の組み立てに失敗した: "+err.Error())
	}
	res, err := h.callREST(r, http.MethodPost,
		"/projects/"+url.PathEscape(key)+"/tickets/"+
			strconv.FormatInt(in.Seq.value, 10)+"/transition", nil, raw, nil)
	return passThrough(r, res, err)
}

// ── 完了レポート系（手順26c。Design.md 8.5.4）───────────────

// reportTools は手順26c で実装する1件を返す。
//
// **並び順は最後である。** /pb-implement の流れが「読む → 起票・記録 → 状態を
// 進める → 報告する」だからで（Requirements.md 10.8.6）、tools/list はこの順で出る。
func reportTools() []tool {
	return []tool{
		{
			Name: "pb_submit_result",
			Description: "作業の結果を構造化した完了レポートとして提出する。" +
				"成果物・完了条件ごとの判定と証跡・判明したこと・**試して駄目だったこと**・" +
				"分割の提案・コストを渡すと、チケットのコメントとして人が読める形で残る。" +
				"**状態は進まず、チケットも完了にならない**——完了は人が確認して行うので、" +
				"状態を進めたいときは pb_transition_task を別に呼ぶこと。" +
				"応答の unsatisfied_dod に項目が残っていたら、直して出し直すこと。",
			InputSchema: schema{
				Type: "object",
				Properties: map[string]property{
					"seq": {Type: "integer", Description: "チケット番号（seq）", Minimum: intPtr(1)},
					"status": {Type: "string", Enum: []string{"completed", "blocked", "partial"},
						Description: "作業の結果。completed=やり切った / blocked=進められない / partial=一部だけ終わった"},
					"artifacts": objectItems(
						"作った成果物。プルリクエスト・変更したファイルなど",
						map[string]property{
							"type": {Type: "string", Description: "種別（例: pull_request、file）"},
							"url":  {Type: "string", Description: "URL（プルリクエスト等）"},
							"path": {Type: "string", Description: "リポジトリ内のパス（ファイル）"},
						}, "type"),
					"dod_results": objectItems(
						"完了条件ごとの自己検証の結果。**pb_get_task が返した完了条件の id をそのまま使うこと。**"+
							"ここで passed: true にしなかった条件は、応答の unsatisfied_dod に残る",
						map[string]property{
							"id":       {Type: "string", Description: "完了条件の id（pb_get_task の dod[].id）"},
							"passed":   {Type: "boolean", Description: "満たしたかどうか"},
							"evidence": {Type: "string", Description: "証跡。実行したコマンドと結果など"},
							"note":     {Type: "string", Description: "満たせなかった理由や補足"},
						}, "id", "passed"),
					"findings": objectItems(
						"判明したこと。次に同じ領域を触る人が知っておくべきこと",
						map[string]property{
							"kind": {Type: "string",
								Enum:        []string{"decision", "discussion", "artifact", "caveat", "reference"},
								Description: "情報の種類。decision=決めたこと / caveat=注意すべきこと"},
							"body": {Type: "string", Description: "本文"},
						}, "kind", "body"),
					"failures": objectItems(
						"**試して駄目だったこと。** 同じ失敗を繰り返さないために必ず書くこと",
						map[string]property{
							"approach": {Type: "string", Description: "試したやり方"},
							"reason":   {Type: "string", Description: "うまくいかなかった理由"},
						}, "approach"),
					"proposed_subtasks": objectItems(
						"分割の提案。**チケットにはならない**——人が読んで判断する",
						map[string]property{
							"title":     {Type: "string", Description: "提案するチケットの表題"},
							"rationale": {Type: "string", Description: "なぜ要ると考えたか"},
						}, "title"),
					"knowledge_impact": {Type: "string", Enum: []string{"none", "minor", "major"},
						Description: "この作業で得た知見が、プロジェクトの規約や設計にどれだけ効くか。" +
							"major なら憲章への反映を利用者に提案すること"},
					"cost": {Type: "object", Description: "この作業に掛かったもの",
						Properties: map[string]property{
							"tokens":         {Type: "integer", Description: "使ったトークン数", Minimum: intPtr(0)},
							"turns":          {Type: "integer", Description: "やり取りの回数", Minimum: intPtr(0)},
							"wall_clock_min": {Type: "integer", Description: "掛かった時間（分）", Minimum: intPtr(0)},
						}},
				},
				Required: []string{"seq", "status"},
			},
			call: callSubmitResult,
		},
	}
}

// callSubmitResult は完了レポートを提出する。
//
// **seq を URL へ写し、残りの引数をそのまま本体にする。** レポートの中身を
// 構造体で受け直すと、9.15 が「知らないキーも拒まず保存する」と定めているのに
// **MCP 層で落ちる**ことになり、同じ規則が2か所で食い違う（Design.md 8.1）。
// 検証は REST 層の仕事である。
//
// **状態は進めない**（Design.md 8.5.4）。26b で遷移が pb_transition_task として
// 独立したので、完了レポートの提出と状態遷移を1つのツールに混ぜない。
func callSubmitResult(h *Handler, r *http.Request, key string, args json.RawMessage) (toolResult, *rpcError) {
	fields := map[string]json.RawMessage{}
	if rpcErr := decodeArgs(args, &fields); rpcErr != nil {
		return toolResult{}, rpcErr
	}

	rawSeq, ok := fields["seq"]
	if !ok {
		return toolResult{}, newError(codeInvalidParams, "seq は必須である")
	}
	var seq flexInt
	if err := seq.UnmarshalJSON(rawSeq); err != nil || !seq.set || seq.value < 1 {
		return toolResult{}, newError(codeInvalidParams, "seq は 1 以上の整数である")
	}
	delete(fields, "seq")

	body, err := json.Marshal(fields)
	if err != nil {
		return toolResult{}, newError(codeInternalError, "本文の組み立てに失敗した: "+err.Error())
	}
	res, err := h.callREST(r, http.MethodPost,
		"/projects/"+url.PathEscape(key)+"/tickets/"+
			strconv.FormatInt(seq.value, 10)+"/reports", nil, body, nil)
	return passThrough(r, res, err)
}
