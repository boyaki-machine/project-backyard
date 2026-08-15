// POST /api/v1/projects（ApiDesign.md 5.3）。
//
// サーバ側の処理は 5.3 の記述をそのまま順に行う。
//
//  1. project を作る
//  2. project_counter を初期化する
//  3. テンプレートから workflow / workflow_status / workflow_transition を複製する
//  4. 作成者を project_member（project_admin）として登録する
//
// **これらは単一トランザクションで行う。** 途中で失敗したときに、ワークフローの
// 無いプロジェクトやカウンタの無いプロジェクトが残らないようにするためである
// （チケットの採番は project_counter の行に依存する。DbDesign.md 6.4.1）。
//
// 同じ手順は pb dev seed（cmd/pb/dev_seed.go）にもある。あちらは定義ファイルから
// 複数のプロジェクトを投入する CLI で、実行者となるアクターがいない点だけが違う。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// projectsPath は Location ヘッダに載せるパス（ApiDesign.md 5.3 / 2.1）。
//
// httpapi.BasePath を参照しない。httpapi が v1 を import しているため、
// 逆向きに参照すると循環する。
const projectsPath = "/api/v1/projects"

// creatorRoleKey は作成者に与えるプロジェクトロール（ApiDesign.md 5.3）。
// 値は DbDesign.md 7.3 のシードにある role.key で、FK が存在を保証する。
const creatorRoleKey = "project_admin"

// 入力の長さ制限（ApiDesign.md 5.3 の検証表。DbDesign.md 6.4 の CHECK と同じ）。
const (
	maxProjectNameLength        = 100
	maxProjectDescriptionLength = 1000
)

// defaultWorkflowTemplate は workflow_template 未指定時の既定（ApiDesign.md 5.3）。
const defaultWorkflowTemplate = "simple"

// workflowTemplates は受け付けるテンプレート（ApiDesign.md 5.3）。
// 実体は DbDesign.md 7.4 のシード（マイグレーション 0010）にある。
var workflowTemplates = map[string]bool{
	"simple": true, "with_review": true, "with_approval": true,
}

// createProjectRequest は 5.3 のリクエスト本体。
type createProjectRequest struct {
	Key              string `json:"key"`
	Name             string `json:"name"`
	Description      string `json:"description"`
	WorkflowTemplate string `json:"workflow_template"`
}

// createProject は POST /api/v1/projects を処理する。
func (h *handler) createProject(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /projects が認証ミドルウェアを通っていない")))
		return
	}
	if h.tx == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /projects にトランザクション実行口が渡っていない")))
		return
	}

	var req createProjectRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	if e := validateCreateProject(&req); e != nil {
		apierr.Write(w, r, e)
		return
	}

	// 応答の my_permissions を認可ミドルウェアと同じ値から作る（6.4.1）。
	// RequirePermission が既に解決してコンテキストへ載せているので、
	// ここでの呼び出しはDBを引かない。
	systemPerms, ctx, err := middleware.SystemPermissions(r.Context(), h.q, p)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	rec := audit.FromRequest(r)
	projectID := ulidgen.New()

	var view projectDetailView
	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if err := q.CreateProject(ctx, gen.CreateProjectParams{
			ID:          projectID,
			Key:         req.Key,
			Name:        req.Name,
			Description: optionalText(req.Description),
			CreatedBy:   pgtype.Text{String: p.ActorID, Valid: true},
		}); err != nil {
			// 一意制約違反はそのまま返す。呼び出し側で 409 に写す。
			return err
		}
		if err := q.CreateProjectCounter(ctx, projectID); err != nil {
			return fmt.Errorf("チケット採番カウンタを作成できない: %w", err)
		}
		if err := copyWorkflowTemplate(ctx, q, projectID, req.WorkflowTemplate); err != nil {
			return err
		}
		if err := q.AddProjectMember(ctx, gen.AddProjectMemberParams{
			ProjectID: projectID,
			ActorID:   p.ActorID,
			RoleKey:   creatorRoleKey,
		}); err != nil {
			return fmt.Errorf("作成者を %s として登録できない: %w", creatorRoleKey, err)
		}

		// **監査は同じトランザクションで書く**（手順4b の方針）。記録の無い
		// プロジェクトが生まれないようにするため、失敗したら作成ごと失敗させる。
		if err := rec.Record(ctx, q, audit.Entry{
			Action:     audit.ProjectCreate,
			Result:     audit.Success,
			TargetType: "project",
			TargetID:   projectID,
			Detail: map[string]any{
				"key":               req.Key,
				"workflow_template": req.WorkflowTemplate,
			},
		}); err != nil {
			return err
		}

		// 応答は 5.4 と同形式（5.3）。作成直後の状態を同じトランザクションから
		// 読むことで、created_at / version が確実にこの作成の結果になる。
		var err error
		view, err = buildProjectDetail(ctx, q, p, systemPerms, req.Key)
		return err
	})
	if err != nil {
		writeCreateProjectError(w, r, req.Key, err)
		return
	}

	w.Header().Set("Location", projectsPath+"/"+req.Key)
	WriteJSON(w, http.StatusCreated, view)
}

// copyWorkflowTemplate はテンプレートをプロジェクト固有のワークフローへ複製する
// （ApiDesign.md 5.3、DbDesign.md 6.5 / 7.4）。
//
// workflow は project より後に作る。非テンプレートの workflow は project_id が
// 必須で（ck_workflow_template）、プロジェクトより先に作れない。そのため
// project.workflow_id は複製し終えてから埋める。
func copyWorkflowTemplate(ctx context.Context, q gen.Querier, projectID, templateKey string) error {
	tpl, err := q.FindWorkflowTemplate(ctx, pgtype.Text{String: templateKey, Valid: true})
	if errors.Is(err, pgx.ErrNoRows) {
		// 入力は検証済みなので、ここに来るのはシード（0010）が未適用のとき。
		// 利用者の入力の誤りではないため 422 ではなく 500 に倒す。
		return fmt.Errorf("ワークフローテンプレート %q がDBに無い（DbDesign.md 7.4 のシードが未適用）", templateKey)
	}
	if err != nil {
		return fmt.Errorf("ワークフローテンプレート %q を取得できない: %w", templateKey, err)
	}

	workflowID := ulidgen.New()
	if err := q.CreateProjectWorkflow(ctx, gen.CreateProjectWorkflowParams{
		ID:         workflowID,
		ProjectID:  pgtype.Text{String: projectID, Valid: true},
		Name:       tpl.Name,
		Definition: tpl.Definition,
	}); err != nil {
		return fmt.Errorf("ワークフローを作成できない: %w", err)
	}

	statuses, err := q.ListWorkflowStatuses(ctx, tpl.ID)
	if err != nil {
		return fmt.Errorf("テンプレート %q のステータスを取得できない: %w", templateKey, err)
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
		return fmt.Errorf("テンプレート %q の遷移を取得できない: %w", templateKey, err)
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
		ID:         projectID,
		WorkflowID: pgtype.Text{String: workflowID, Valid: true},
	}); err != nil {
		return fmt.Errorf("プロジェクトにワークフローを紐づけられない: %w", err)
	}
	return nil
}

// validateCreateProject は 5.3 の検証表を実装する。未指定の
// workflow_template には既定を埋める。
//
// **details には見つかった誤りをすべて載せる**（ApiDesign.md 2.5）。
// フォームの各入力欄に紐づけるため、最初の1件で打ち切らない。
func validateCreateProject(req *createProjectRequest) *apierr.Error {
	var details []apierr.Detail

	switch {
	case req.Key == "":
		details = append(details, apierr.Detail{
			Field: "key", Code: "required", Message: "プロジェクトキーを入力してください",
		})
	case !projectKeyPattern.MatchString(req.Key):
		details = append(details, apierr.Detail{
			Field: "key", Code: "invalid",
			Message: "プロジェクトキーは半角英小文字・数字・ハイフンの2〜20文字で、先頭は英小文字か数字にしてください",
		})
	case reservedProjectKeys[req.Key]:
		details = append(details, apierr.Detail{
			Field: "key", Code: "reserved", Message: "このプロジェクトキーは予約されているため使えません",
		})
	}

	switch n := utf8.RuneCountInString(req.Name); {
	case n == 0:
		details = append(details, apierr.Detail{
			Field: "name", Code: "required", Message: "プロジェクト名を入力してください",
		})
	case n > maxProjectNameLength:
		details = append(details, apierr.Detail{
			Field: "name", Code: "too_long",
			Message: fmt.Sprintf("プロジェクト名は%d文字以内で入力してください", maxProjectNameLength),
		})
	}

	if utf8.RuneCountInString(req.Description) > maxProjectDescriptionLength {
		details = append(details, apierr.Detail{
			Field: "description", Code: "too_long",
			Message: fmt.Sprintf("説明は%d文字以内で入力してください", maxProjectDescriptionLength),
		})
	}

	if req.WorkflowTemplate == "" {
		req.WorkflowTemplate = defaultWorkflowTemplate
	} else if !workflowTemplates[req.WorkflowTemplate] {
		details = append(details, apierr.Detail{
			Field: "workflow_template", Code: "invalid",
			Message: "ワークフローは simple / with_review / with_approval のいずれかを選んでください",
		})
	}

	if len(details) == 0 {
		return nil
	}
	return apierr.New(apierr.ValidationFailed).WithDetails(details...)
}

// writeCreateProjectError は作成の失敗を応答に写す。
//
// **キー重複は 409 already_exists**（ApiDesign.md 5.3 / 2.5.1）。検出をDBの
// UNIQUE 制約に委ねるのは、check-key の確認から作成までの間に他者が同じキーを
// 取りうるためである（TOCTOU）。
func writeCreateProjectError(w http.ResponseWriter, r *http.Request, key string, err error) {
	if isProjectKeyConflict(err) {
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage("そのプロジェクトキーは既に使われています。別のキーを指定してください").
			WithDetails(apierr.Detail{
				Field: "key", Code: "already_exists",
				Message: "そのプロジェクトキーは既に使われています",
			}).
			WithCause(fmt.Errorf("プロジェクトキー %q が重複した: %w", key, err)))
		return
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).
		WithCause(fmt.Errorf("プロジェクト %q を作成できない: %w", key, err)))
}

// isProjectKeyConflict は project テーブルの一意制約違反かどうかを返す。
//
// 23505 は unique_violation（PostgreSQL のエラーコード）。同じトランザクション内の
// 他の INSERT は ULID を主キーにしており衝突しないため、project 表の 23505 は
// key の重複を意味する。
func isProjectKeyConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return pgErr.TableName == "project" || pgErr.ConstraintName == "project_key_key"
}

// optionalText は任意入力の文字列を NULL 可能な列へ写す。空文字は NULL。
func optionalText(s string) pgtype.Text {
	return pgtype.Text{String: s, Valid: s != ""}
}
