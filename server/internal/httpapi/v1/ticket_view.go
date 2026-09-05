// チケットの応答の組み立て（ApiDesign.md 9.2.2 / 9.5.1）。
//
// 一覧（9.2）と詳細（9.5）は「詳細 = 一覧の1行 + 6項目」という関係にあると
// 9.5.1 が定めている。**その関係を型でも保つ**ため、ticketDetailView は
// ticketListItem を埋め込んでいる。片方だけ項目が増えると JSON の形が割れる。
//
// 手順16b が使うのは、一覧（9.2）と、POST /tickets の応答（9.3 が「9.5 の GET と
// 同形式」と定める）である。GET /tickets/:seq そのものは手順17 で足す。
package v1

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// fallbackStatusCategory はワークフローに無いステータスキーを見たときの category。
//
// **Phase 1 でここへ来る経路は無い。** status_key を書くのはサーバだけで、値は
// 必ずプロジェクトのワークフローから取る（9.3 の ResolveInitialStatusKey、
// 手順17 の遷移）。ワークフローを差し替える画面も Phase 1 には無い。
// それでも NULL を素通しできないのは、画面がこの値でバッジの見た目を選ぶため
// （GuiDesign.md 8.7）で、未着手側に寄せるのが最も害が小さい。
const fallbackStatusCategory = "todo"

// actorRef は担当者・報告者（ApiDesign.md 9.2.2）。
//
// kind は user / agent / system。**画面はこれを見てエージェントに 🤖 を付ける**
// （GuiDesign.md 5.4、設計原則5）。不在のときは項目ごと null になる。
type actorRef struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
}

// ticketStatusView は status（9.2.2）。key / name / category の3点で、
// name は画面に出す日本語、category はバッジの見た目を決める4値である。
type ticketStatusView struct {
	Key      string `json:"key"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

// ticketTagRef は tags[] の要素（9.2.2）。**色を持たない**（GuiDesign.md 8.6）。
type ticketTagRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// sprintRef は sprint（9.2.2）。未割当なら項目ごと null。
type sprintRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ticketListItem は 9.2.2 の items[] の1件。
//
// **body_md を持たない**（9.2.2）。一覧は本文を表示せず、200件分の Markdown は
// 応答を数十倍にする。**execution_mode / readiness / scope / custom_fields も
// 持たない**——列は DbDesign.md 6.6 に先行定義されているが、GuiDesign.md 5.5 が
// 「Phase 1 では非表示」と決めている。画面が使わない項目を応答に載せない。
type ticketListItem struct {
	ID       string           `json:"id"`
	Seq      int32            `json:"seq"`
	Type     string           `json:"type"`
	Title    string           `json:"title"`
	Status   ticketStatusView `json:"status"`
	Priority *string          `json:"priority"`
	Assignee *actorRef        `json:"assignee"`
	Reporter *actorRef        `json:"reporter"`
	// WorkingAgent は「誰が実際に処理しているか」（9.2.2、DbDesign.md 6.6。手順26b）。
	// **Assignee が「誰の仕事か」を表すのに対し、こちらは実行者である。**
	// エージェントが遷移したときに自分で立て（9.6）、消化しても消えない。
	WorkingAgent *actorRef `json:"working_agent"`
	ParentSeq    *int32    `json:"parent_seq"`
	HasChildren  bool      `json:"has_children"`
	SortKey      *string   `json:"sort_key"`
	// StagedAt は「オンステージ」（9.2.2、DbDesign.md 6.6）。null がバックログ、
	// 値が入っているものがオンステージで、値は「いつ上げたか」である。
	// **進捗（status）とは独立した軸**で、「未着手だがオンステージ」を表せる。
	StagedAt *Time          `json:"staged_at"`
	Tags     []ticketTagRef `json:"tags"`
	Sprint   *sprintRef     `json:"sprint"`

	EstimatePoint *float64 `json:"estimate_point"`
	EstimateHours *float64 `json:"estimate_hours"`
	ActualHours   *float64 `json:"actual_hours"`

	StartDate *Date `json:"start_date"`
	DueDate   *Date `json:"due_date"`
	ClosedAt  *Time `json:"closed_at"`

	Version   int32 `json:"version"`
	CreatedAt Time  `json:"created_at"`
	UpdatedAt Time  `json:"updated_at"`
}

// ticketBrief は詳細の parent（9.5.1）。
type ticketBrief struct {
	Seq    int32            `json:"seq"`
	Title  string           `json:"title"`
	Type   string           `json:"type"`
	Status ticketStatusView `json:"status"`
}

// ticketChildBrief は詳細の children（9.5.1）。**孫は含めない。**
type ticketChildBrief struct {
	Seq      int32            `json:"seq"`
	Title    string           `json:"title"`
	Type     string           `json:"type"`
	Status   ticketStatusView `json:"status"`
	Assignee *actorRef        `json:"assignee"`
}

// ticketDetailView は 9.5.1 の応答。手順16b では POST /tickets が返す
// （9.3 が「応答は 9.5 の GET と同形式」と定めるため）。
//
// **dod / links は手順18a から実数である**（9.9 / 9.10.1）。それまでは空配列を
// 返していた——作りたてのチケットではどちらも空が正しい値であり、実装が入った
// ときに項目が生えたように見せないためである。
//
// **references は手順17c から実数である**（9.5.1 / 9.10.2）。別の GET に切らず
// 詳細応答へ入れるのは dod / links と同じ理由で、画面を開いた時点で見えている
// ものだからである（GuiDesign.md 5.5）。遷移先の一覧（9.7）のように「開いた
// ときだけ要るもの」ではない。
//
// **comment_count は手順17a から実数である。** 9.6 の遷移が kind='progress' の
// コメントを作る（DbDesign.md 6.7）ので、0 を固定で返すと事実と食い違う。
// コメントAPI（9.8）そのものは手順18 だが、件数の供給元は先に要る。
//
// **execution_mode / readiness / readiness_note / scope は手順27 で足した**（9.5.1）。
// 9.2.2 が「Phase 2 で有効化する際に足す」と書いていたもので、**一覧には足していない**
// ——読む相手（pb_get_task と pb_get_context）はどちらもチケット1件を指して呼ぶ。
//
// **足した理由は、pb_get_task が果たせていない約束があったためである**（9.5.1）。
// Requirements.md 10.8.6 の /pb-implement は手順1 で「実行主体属性が human-only なら
// 実装せず報告して終了」「readiness が赤なら確認する」と定めているが、Design.md 8.5.2 の
// とおり pb_get_task は本応答をそのまま返すので、**ここが返さない限りどちらの分岐も
// 起こりえなかった。**
type ticketDetailView struct {
	ticketListItem
	BodyMd       *string            `json:"body_md"`
	Parent       *ticketBrief       `json:"parent"`
	Children     []ticketChildBrief `json:"children"`
	DoD          []dodView          `json:"dod"`
	Links        []linkView         `json:"links"`
	References   []referenceView    `json:"references"`
	CommentCount int64              `json:"comment_count"`

	ExecutionMode string  `json:"execution_mode"`
	Readiness     *string `json:"readiness"`
	ReadinessNote *string `json:"readiness_note"`
	// Scope はスコープ境界（Requirements.md 10.5.3）。**未設定でも null にせず
	// {} を返す**——「境界が無い」と「項目が無い」は違うもので、パック
	// （Design.md 8.5.5）が前者に文を当てる。
	Scope json.RawMessage `json:"scope"`
}

// emptyScope は scope 列が空だったときに返す値（9.5.1）。
//
// **DB は NOT NULL DEFAULT '{}' だが、それを当てにしない。** ここが null を返すと
// 生成した型が object と null の両方を持つことになり、読む側が分岐を持つ。
var emptyScope = json.RawMessage(`{}`)

// scopeOrEmpty は jsonb の生バイトを応答へ載せる形にする。
func scopeOrEmpty(raw []byte) json.RawMessage {
	if len(raw) == 0 {
		return emptyScope
	}
	return json.RawMessage(raw)
}

// buildTicketListItem は一覧の1行を組み立てる。tags は別クエリで引いたもの。
func buildTicketListItem(row gen.ListTicketsRow, tags []ticketTagRef) ticketListItem {
	if tags == nil {
		tags = []ticketTagRef{}
	}
	return ticketListItem{
		ID:            row.ID,
		Seq:           row.Seq,
		Type:          row.Type,
		Title:         row.Title,
		Status:        statusView(row.StatusKey, row.StatusName, row.StatusCategory),
		Priority:      textPtr(row.Priority),
		Assignee:      actorRefOf(row.AssigneeID, row.AssigneeKind, row.AssigneeName),
		Reporter:      actorRefOf(row.ReporterID, row.ReporterKind, row.ReporterName),
		WorkingAgent:  actorRefOf(row.WorkingAgentID, row.WorkingAgentKind, row.WorkingAgentName),
		ParentSeq:     int4Ptr(row.ParentSeq),
		HasChildren:   row.HasChildren,
		SortKey:       textPtr(row.SortKey),
		StagedAt:      apiTimestamptz(row.StagedAt),
		Tags:          tags,
		Sprint:        sprintRefOf(row.SprintID, row.SprintName),
		EstimatePoint: float8Ptr(row.EstimatePoint),
		EstimateHours: float8Ptr(row.EstimateHours),
		ActualHours:   float8Ptr(row.ActualHours),
		StartDate:     apiDate(row.StartDate),
		DueDate:       apiDate(row.DueDate),
		ClosedAt:      apiTimestamptz(row.ClosedAt),
		Version:       row.Version,
		CreatedAt:     Time(row.CreatedAt.Time),
		UpdatedAt:     Time(row.UpdatedAt.Time),
	}
}

// buildTicketDetail は 9.5.1 の応答を1件ぶん読み直して組み立てる。
//
// **作成の応答（9.3）に使うため、書き込みと同じトランザクションから呼べること。**
// q を引数で受けているのはそのためで、projects_create.go が 5.4 形式を
// トランザクション内で組み立てているのと同じ形である。
func buildTicketDetail(
	ctx context.Context, q gen.Querier, projectID string, seq int32,
) (ticketDetailView, error) {
	row, err := q.GetTicketBySeq(ctx, gen.GetTicketBySeqParams{ProjectID: projectID, Seq: seq})
	if err != nil {
		return ticketDetailView{}, err
	}

	tags, err := ticketTagsFor(ctx, q, []string{row.ID})
	if err != nil {
		return ticketDetailView{}, err
	}

	// 9.5.1 の comment_count。**本文は引かない**——コメント本体は 9.8 の
	// 別エンドポイントであり、詳細は件数だけで 5.5 の見出し「コメント (4)」を作る。
	commentCount, err := q.CountTicketComments(ctx, row.ID)
	if err != nil {
		return ticketDetailView{}, fmt.Errorf("コメント件数を読めない: %w", err)
	}

	// 9.5.1 の references（手順17c）。**一覧APIと同じ関数を通す**ので、
	// 並び順の規則を2か所で書き分けない（references.go）。
	references, err := ticketReferencesFor(ctx, q, row.ID)
	if err != nil {
		return ticketDetailView{}, err
	}

	// 9.5.1 の dod / links（手順18a）。references と同じく一覧APIと同じ関数を
	// 通す（dod.go / links.go）。**別の GET に切らないのは、画面を開いた時点で
	// 見えているものだからである**（GuiDesign.md 5.5、ApiDesign.md 8章の
	// 「起動時1〜2本」）。遷移先の一覧（9.7）のように開いたときだけ要るもの
	// ではない。
	dod, err := ticketDoDFor(ctx, q, row.ID)
	if err != nil {
		return ticketDetailView{}, err
	}
	links, err := ticketLinksFor(ctx, q, row.ID)
	if err != nil {
		return ticketDetailView{}, err
	}

	view := ticketDetailView{
		ticketListItem: ticketListItem{
			ID:            row.ID,
			Seq:           row.Seq,
			Type:          row.Type,
			Title:         row.Title,
			Status:        statusView(row.StatusKey, row.StatusName, row.StatusCategory),
			Priority:      textPtr(row.Priority),
			Assignee:      actorRefOf(row.AssigneeID, row.AssigneeKind, row.AssigneeName),
			Reporter:      actorRefOf(row.ReporterID, row.ReporterKind, row.ReporterName),
			WorkingAgent:  actorRefOf(row.WorkingAgentID, row.WorkingAgentKind, row.WorkingAgentName),
			ParentSeq:     int4Ptr(row.ParentSeq),
			HasChildren:   row.HasChildren,
			SortKey:       textPtr(row.SortKey),
			StagedAt:      apiTimestamptz(row.StagedAt),
			Tags:          tagsOrEmpty(tags[row.ID]),
			Sprint:        sprintRefOf(row.SprintID, row.SprintName),
			EstimatePoint: float8Ptr(row.EstimatePoint),
			EstimateHours: float8Ptr(row.EstimateHours),
			ActualHours:   float8Ptr(row.ActualHours),
			StartDate:     apiDate(row.StartDate),
			DueDate:       apiDate(row.DueDate),
			ClosedAt:      apiTimestamptz(row.ClosedAt),
			Version:       row.Version,
			CreatedAt:     Time(row.CreatedAt.Time),
			UpdatedAt:     Time(row.UpdatedAt.Time),
		},
		BodyMd:       textPtr(row.BodyMd),
		Children:     []ticketChildBrief{},
		DoD:          dod,
		Links:        links,
		References:   references,
		CommentCount: commentCount,

		ExecutionMode: row.ExecutionMode,
		Readiness:     textPtr(row.Readiness),
		ReadinessNote: textPtr(row.ReadinessNote),
		Scope:         scopeOrEmpty(row.Scope),
	}

	if row.ParentSeq.Valid {
		parentID, err := q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{
			ProjectID: projectID, Seq: row.ParentSeq.Int32,
		})
		if err != nil {
			return ticketDetailView{}, fmt.Errorf("親チケットを読めない: %w", err)
		}
		brief, err := q.GetTicketBrief(ctx, parentID)
		if err != nil {
			return ticketDetailView{}, fmt.Errorf("親チケットの要約を読めない: %w", err)
		}
		view.Parent = &ticketBrief{
			Seq:    brief.Seq,
			Title:  brief.Title,
			Type:   brief.Type,
			Status: statusView(brief.StatusKey, brief.StatusName, brief.StatusCategory),
		}
	}

	children, err := q.ListTicketChildrenBrief(ctx, pgtype.Text{String: row.ID, Valid: true})
	if err != nil {
		return ticketDetailView{}, fmt.Errorf("子チケットを読めない: %w", err)
	}
	for _, c := range children {
		view.Children = append(view.Children, ticketChildBrief{
			Seq:      c.Seq,
			Title:    c.Title,
			Type:     c.Type,
			Status:   statusView(c.StatusKey, c.StatusName, c.StatusCategory),
			Assignee: actorRefOf(c.AssigneeID, c.AssigneeKind, c.AssigneeName),
		})
	}
	return view, nil
}

// ticketTagsFor は複数チケットのタグをまとめて引き、ticket_id で束ねて返す。
func ticketTagsFor(
	ctx context.Context, q gen.Querier, ticketIDs []string,
) (map[string][]ticketTagRef, error) {
	byTicket := make(map[string][]ticketTagRef, len(ticketIDs))
	if len(ticketIDs) == 0 {
		return byTicket, nil
	}
	rows, err := q.ListTagsForTickets(ctx, ticketIDs)
	if err != nil {
		return nil, fmt.Errorf("チケットのタグを読めない: %w", err)
	}
	for _, row := range rows {
		byTicket[row.TicketID] = append(byTicket[row.TicketID],
			ticketTagRef{ID: row.ID, Name: row.Name})
	}
	return byTicket, nil
}

func tagsOrEmpty(tags []ticketTagRef) []ticketTagRef {
	if tags == nil {
		return []ticketTagRef{}
	}
	return tags
}

// statusView は status_key と、ワークフローから引いた名前・category を束ねる。
func statusView(key string, name, category pgtype.Text) ticketStatusView {
	view := ticketStatusView{Key: key, Name: key, Category: fallbackStatusCategory}
	if name.Valid {
		view.Name = name.String
	}
	if category.Valid {
		view.Category = category.String
	}
	return view
}

// actorRefOf は担当者・報告者を写す。ID が NULL なら不在（JSON の null）。
func actorRefOf(id, kind, displayName pgtype.Text) *actorRef {
	if !id.Valid {
		return nil
	}
	return &actorRef{ID: id.String, Kind: kind.String, DisplayName: displayName.String}
}

func sprintRefOf(id, name pgtype.Text) *sprintRef {
	if !id.Valid {
		return nil
	}
	return &sprintRef{ID: id.String, Name: name.String}
}

func int4Ptr(v pgtype.Int4) *int32 {
	if !v.Valid {
		return nil
	}
	n := v.Int32
	return &n
}

func float8Ptr(v pgtype.Float8) *float64 {
	if !v.Valid {
		return nil
	}
	f := v.Float64
	return &f
}
