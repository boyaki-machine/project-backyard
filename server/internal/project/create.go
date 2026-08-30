// Package project はプロジェクト作成の手順を1本に持つ。
//
// **入口が2つあり、手順は1つである。** POST /api/v1/projects（ApiDesign.md 5.3）と
// pb dev seed（cmd/pb/dev_seed.go）がどちらも「project を作り、カウンタを初期化し、
// テンプレートから複製し、監査へ記録する」を行う。手順22 までは両方が同じ処理を
// 別々に持っており、**片方だけ直すと差が開く**状態だった（docs/PROGRESS.md
// 「手順外の作業」）。手順23 で文書テンプレートの複製が加わるのを機に1本へ寄せた。
//
// **入口ごとに違うのは3点だけ**で、いずれも CreateParams で表す。
//
//	作成者のアクター      HTTP は認証されたプリンシパル、CLI は定義ファイルの project_admin
//	作成者のメンバー登録  HTTP だけが行う（CLI は定義ファイルのメンバーを別途登録する）
//	監査の detail の via  CLI だけが "pb dev seed" を足す
//
// **トランザクションはこの関数の外で張る**（ApiDesign.md 5.3「単一トランザクション」）。
// HTTP は h.tx.RunInTx、CLI は seed 全体の1トランザクションの中にあり、どちらも
// gen.Querier としてこの関数へ渡ってくる。
package project

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// CreatorRoleKey は作成者に与えるプロジェクトロール（ApiDesign.md 5.3）。
// 値は DbDesign.md 7.3 のシードにある role.key で、FK が存在を保証する。
const CreatorRoleKey = "project_admin"

// DefaultDocTemplateKey は複製する文書テンプレートの束（DbDesign.md 8.1.2）。
//
// **固定である。** workflow_template と違って入力フィールドを持たない——
// テンプレートが1種類しかないうちは選ばせる意味がないため（ApiDesign.md 5.3）。
const DefaultDocTemplateKey = "default"

// CreateParams は1つのプロジェクトを作るために要る値。
type CreateParams struct {
	// ID は呼び出し側が採番した project.id（ULID）。作成後に他の行を
	// 紐づける呼び出し側が、戻り値を待たずに使えるようにしてある。
	ID          string
	Key         string
	Name        string
	Description pgtype.Text

	// WorkflowTemplate は 7.4 のテンプレートキー（simple / with_review /
	// with_approval）。入力の検証は呼び出し側が済ませている。
	WorkflowTemplate string

	// CreatedBy は project / document の created_by に入るアクター。
	// 無効（NULL）でもよい——CLI は定義ファイルに project_admin がいなければ空になる。
	CreatedBy pgtype.Text

	// RegisterCreatorAsAdmin は CreatedBy を project_admin として
	// project_member へ登録するかどうか。
	//
	// **HTTP だけが true。** CLI は定義ファイルの members をそのまま登録するので、
	// ここで先に入れると役割の出所が2つになる。
	RegisterCreatorAsAdmin bool

	// AuditVia は監査 detail の via。空なら入れない（HTTP がそうする）。
	AuditVia string
}

// Create は 5.3 の手順をそのまま順に行う。
//
//  1. project を作る
//  2. project_counter を初期化する
//  3. テンプレートから workflow / workflow_status / workflow_transition を複製する
//  4. テンプレートから document を複製する（DbDesign.md 8.1.2）
//  5. 作成者を project_member（project_admin）として登録する（HTTP のみ）
//  6. audit_log へ記録する
//
// **キーの重複はそのまま返す。** 呼び出し側が pgconn.PgError の 23505 を見て
// 409 already_exists に写す（projects_create.go）ため、ここでは判定しない。
func Create(ctx context.Context, q gen.Querier, rec *audit.Recorder, p CreateParams) error {
	if err := q.CreateProject(ctx, gen.CreateProjectParams{
		ID:          p.ID,
		Key:         p.Key,
		Name:        p.Name,
		Description: p.Description,
		CreatedBy:   p.CreatedBy,
	}); err != nil {
		// 一意制約違反を握り潰さない。%w で包むので errors.As は通る。
		return fmt.Errorf("プロジェクト %s を作成できない: %w", p.Key, err)
	}
	if err := q.CreateProjectCounter(ctx, p.ID); err != nil {
		return fmt.Errorf("プロジェクト %s のチケット採番カウンタを作成できない: %w", p.Key, err)
	}
	if err := copyWorkflowTemplate(ctx, q, p); err != nil {
		return err
	}
	if err := copyDocumentTemplates(ctx, q, p); err != nil {
		return err
	}

	if p.RegisterCreatorAsAdmin {
		if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
			ProjectID: p.ID,
			ActorID:   p.CreatedBy.String,
			RoleKey:   CreatorRoleKey,
		}); err != nil {
			return fmt.Errorf("作成者を %s として登録できない: %w", CreatorRoleKey, err)
		}
	}

	// **監査は同じトランザクションで書く**（手順4b の方針）。記録の無い
	// プロジェクトが生まれないようにするため、失敗したら作成ごと失敗させる。
	detail := map[string]any{
		"key":               p.Key,
		"workflow_template": p.WorkflowTemplate,
	}
	if p.AuditVia != "" {
		detail["via"] = p.AuditVia
	}
	return rec.Record(ctx, q, audit.Entry{
		Action:     audit.ProjectCreate,
		Result:     audit.Success,
		TargetType: "project",
		TargetID:   p.ID,
		Detail:     detail,
	})
}

// copyWorkflowTemplate はテンプレートをプロジェクト固有のワークフローへ複製する
// （ApiDesign.md 5.3、DbDesign.md 6.5 / 7.4）。
//
// workflow は project より後に作る。非テンプレートの workflow は project_id が
// 必須で（ck_workflow_template）、プロジェクトより先に作れない。そのため
// project.workflow_id は複製し終えてから埋める。
func copyWorkflowTemplate(ctx context.Context, q gen.Querier, p CreateParams) error {
	tpl, err := q.FindWorkflowTemplate(ctx, pgtype.Text{String: p.WorkflowTemplate, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		// 入力は検証済みなので、ここに来るのはシード（0010）が未適用のとき。
		// 利用者の入力の誤りではないため 422 ではなく 500 に倒す。
		return fmt.Errorf("ワークフローテンプレート %q がDBに無い（DbDesign.md 7.4 のシードが未適用）", p.WorkflowTemplate)
	}
	if err != nil {
		return fmt.Errorf("ワークフローテンプレート %q を取得できない: %w", p.WorkflowTemplate, err)
	}

	workflowID := ulidgen.New()
	if err := q.CreateProjectWorkflow(ctx, gen.CreateProjectWorkflowParams{
		ID:         workflowID,
		ProjectID:  pgtype.Text{String: p.ID, Valid: true},
		Name:       tpl.Name,
		Definition: tpl.Definition,
	}); err != nil {
		return fmt.Errorf("プロジェクト %s のワークフローを作成できない: %w", p.Key, err)
	}

	statuses, err := q.ListWorkflowStatuses(ctx, tpl.ID)
	if err != nil {
		return fmt.Errorf("テンプレート %q のステータスを取得できない: %w", p.WorkflowTemplate, err)
	}
	for _, s := range statuses {
		if err := q.CreateWorkflowStatus(ctx, gen.CreateWorkflowStatusParams{
			ID:                    ulidgen.New(),
			WorkflowID:            workflowID,
			Key:                   s.Key,
			Name:                  s.Name,
			Category:              s.Category,
			SortOrder:             s.SortOrder,
			RequiresHumanApproval: s.RequiresHumanApproval,
			IsAgentReachable:      s.IsAgentReachable,
		}); err != nil {
			return fmt.Errorf("ステータス %q を複製できない: %w", s.Key, err)
		}
	}

	transitions, err := q.ListWorkflowTransitions(ctx, tpl.ID)
	if err != nil {
		return fmt.Errorf("テンプレート %q の遷移を取得できない: %w", p.WorkflowTemplate, err)
	}
	for _, t := range transitions {
		if err := q.CreateWorkflowTransition(ctx, gen.CreateWorkflowTransitionParams{
			ID:                 ulidgen.New(),
			WorkflowID:         workflowID,
			FromStatusKey:      t.FromStatusKey,
			ToStatusKey:        t.ToStatusKey,
			RequiredPermission: t.RequiredPermission,
			AllowedActorKinds:  t.AllowedActorKinds,
		}); err != nil {
			return fmt.Errorf("遷移 %s→%s を複製できない: %w", t.FromStatusKey, t.ToStatusKey, err)
		}
	}

	if err := q.SetProjectWorkflow(ctx, gen.SetProjectWorkflowParams{
		ID:         p.ID,
		WorkflowID: pgtype.Text{String: workflowID, Valid: true},
	}); err != nil {
		return fmt.Errorf("プロジェクト %s にワークフローを紐づけられない: %w", p.Key, err)
	}
	return nil
}

// copyDocumentTemplates は文書テンプレートをプロジェクトの文書へ複製する
// （DbDesign.md 8.1.2、ApiDesign.md 5.3）。
//
// **木として複製する。** いまテンプレートは4件ともトップレベルだが、
// uq_document_template_slug が parent_id を含んでおり、テンプレート側は子を持てる。
// ListDocumentTemplates が親を先に返す（parent_id NULLS FIRST）ので、届いた順に
// 作りながら旧 id → 新 id を貯めれば parent_id を張り替えられる。
//
// **sort_order はテンプレートの値をそのまま使う**（10 / 20 / 30 / 40）。並べ替え
// （ApiDesign.md 10.4）が同じ 10 刻みで書き戻すので、初期値が揃っていれば
// 最初の並べ替えで余計な更新が出ない。
//
// **1件ごとに revision_no = 1 を作る**（ApiDesign.md 5.3 / 10.4）。テンプレートの
// 初期本文は「ここに何を書くか」の案内であり、消して書き直したあとに戻したくなる。
// 作成時の1件が無いと、その本文はどの版にも残らない。
//
// **テンプレートが1件も無くても失敗させない。** 0017 を適用していないDBでも
// プロジェクトは作れてよい——文書はプロジェクトの成立条件ではなく、後から
// 画面で作れる（GuiDesign.md 5.10「空状態」）。ワークフローが 500 に倒れるのと
// 扱いが違うのは、あちらがチケットの採番と遷移に要るためである。
func copyDocumentTemplates(ctx context.Context, q gen.Querier, p CreateParams) error {
	key := pgtype.Text{String: DefaultDocTemplateKey, Valid: true}
	templates, err := q.ListDocumentTemplates(ctx, key)
	if err != nil {
		return fmt.Errorf("文書テンプレート %q を取得できない: %w", DefaultDocTemplateKey, err)
	}

	// テンプレートの id → 複製先の id。親が先に来るので、子を作るときには
	// 必ず親の対応が入っている。
	newID := make(map[string]string, len(templates))
	for _, t := range templates {
		id := ulidgen.New()
		newID[t.ID] = id

		parentID := pgtype.Text{}
		if t.ParentID.Valid {
			mapped, ok := newID[t.ParentID.String]
			if !ok {
				// 並び順の前提が崩れている（親より先に子が来た）。黙って
				// トップレベルへ落とすと木の形が静かに変わるので、止める。
				return fmt.Errorf("文書テンプレート %q の親 %s が複製されていない", t.Slug, t.ParentID.String)
			}
			parentID = pgtype.Text{String: mapped, Valid: true}
		}

		if err := q.CreateDocument(ctx, gen.CreateDocumentParams{
			ID:        id,
			ProjectID: pgtype.Text{String: p.ID, Valid: true},
			ParentID:  parentID,
			Slug:      t.Slug,
			Title:     t.Title,
			BodyMd:    t.BodyMd,
			SortOrder: t.SortOrder,
			CreatedBy: p.CreatedBy,
		}); err != nil {
			return fmt.Errorf("文書 %q を複製できない: %w", t.Slug, err)
		}
		if err := q.CreateDocumentRevision(ctx, gen.CreateDocumentRevisionParams{
			ID:         ulidgen.New(),
			DocumentID: id,
			RevisionNo: 1,
			Title:      t.Title,
			BodyMd:     t.BodyMd,
			ChangedBy:  p.CreatedBy,
		}); err != nil {
			return fmt.Errorf("文書 %q の初版を作成できない: %w", t.Slug, err)
		}
	}
	return nil
}
