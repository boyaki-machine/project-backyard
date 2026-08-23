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
	ID          string           `json:"id"`
	Seq         int32            `json:"seq"`
	Type        string           `json:"type"`
	Title       string           `json:"title"`
	Status      ticketStatusView `json:"status"`
	Priority    *string          `json:"priority"`
	Assignee    *actorRef        `json:"assignee"`
	Reporter    *actorRef        `json:"reporter"`
	ParentSeq   *int32           `json:"parent_seq"`
	HasChildren bool             `json:"has_children"`
	SortKey     *string          `json:"sort_key"`
	Tags        []ticketTagRef   `json:"tags"`
	Sprint      *sprintRef       `json:"sprint"`

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
// **dod / links / comment_count は空で返す。** 中身を作るのは手順18（9.9 / 9.10 /
// 9.8）だが、作りたてのチケットではいずれも空・0が正しい値であり、手順18 で
// 項目が生えたように見せないほうが消費者にとって安定する。
type ticketDetailView struct {
	ticketListItem
	BodyMd       *string            `json:"body_md"`
	Parent       *ticketBrief       `json:"parent"`
	Children     []ticketChildBrief `json:"children"`
	DoD          []any              `json:"dod"`
	Links        []any              `json:"links"`
	CommentCount int64              `json:"comment_count"`
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
		ParentSeq:     int4Ptr(row.ParentSeq),
		HasChildren:   row.HasChildren,
		SortKey:       textPtr(row.SortKey),
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
			ParentSeq:     int4Ptr(row.ParentSeq),
			HasChildren:   row.HasChildren,
			SortKey:       textPtr(row.SortKey),
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
		BodyMd:   textPtr(row.BodyMd),
		Children: []ticketChildBrief{},
		// 手順18 で中身が入る（9.8 / 9.9 / 9.10）。作りたては空・0 が正しい。
		DoD:          []any{},
		Links:        []any{},
		CommentCount: 0,
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
