// プロジェクト詳細（ApiDesign.md 5.4）の応答の組み立て。
//
// **POST /projects（5.3）の応答も 5.4 と同形式である**と定められているため、
// 作成と取得で同じ構造体・同じ組み立て関数を使う。片方だけ形が変わることを防ぐ
// （me.go が login と /me で sessionView を共有しているのと同じ方針）。
//
// GET /projects/:key（5.4）そのものは手順11で足す。本ファイルの buildProjectDetail
// をそのまま呼ぶだけになる。
package v1

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// workflowStatusView は 5.4 の workflow.statuses[] 要素。
type workflowStatusView struct {
	Key                   string `json:"key"`
	Name                  string `json:"name"`
	Category              string `json:"category"`
	SortOrder             int32  `json:"sort_order"`
	RequiresHumanApproval bool   `json:"requires_human_approval"`
	IsAgentReachable      bool   `json:"is_agent_reachable"`
}

// workflowView は 5.4 の workflow。
type workflowView struct {
	ID       string               `json:"id"`
	Name     string               `json:"name"`
	Statuses []workflowStatusView `json:"statuses"`
}

// projectMemberView は 5.4 の members[] 要素。
//
// Email は app_user.email。**エージェントとシステムアクターは null になる**
// （app_user の行を持たない。ApiDesign.md 5.4）。表示名とは別物であり、
// 画面はログイン名として出す（GuiDesign.md 5.9.2）。
type projectMemberView struct {
	ActorID     string  `json:"actor_id"`
	Kind        string  `json:"kind"`
	DisplayName string  `json:"display_name"`
	Email       *string `json:"email"`
	Role        string  `json:"role"`
	JoinedAt    Time    `json:"joined_at"`
}

// projectDetailView は 5.4 の応答本体。
type projectDetailView struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	// Workflow は project.workflow_id が NULL のとき null になる
	// （DbDesign.md 6.4 の ON DELETE SET NULL）。
	Workflow *workflowView       `json:"workflow"`
	Members  []projectMemberView `json:"members"`
	// MyRole はプロジェクトロール。メンバーでないアドミニストレータでは null。
	MyRole *string `json:"my_role"`
	// MyPermissions は当該プロジェクトでの実効権限（Design.md 6.4.1）。
	MyPermissions []string `json:"my_permissions"`
	// Settings は project.settings（jsonb）をそのまま返す。
	Settings  json.RawMessage `json:"settings"`
	Version   int32           `json:"version"`
	CreatedAt Time            `json:"created_at"`
	UpdatedAt Time            `json:"updated_at"`
}

// buildProjectDetail は key が指すプロジェクトの 5.4 応答を組み立てる。
//
// プロジェクトが無ければ pgx.ErrNoRows を返す（呼び出し側が 404 に倒す）。
//
// systemPerms は解決済みのシステムロール層（スコープとの積を取った後）。
// 認可ミドルウェアと同じ値を渡すこと。ここで計算し直すと、画面に出す
// my_permissions とミドルウェアの判定がずれる余地が生まれる。
func buildProjectDetail(
	ctx context.Context, q gen.Querier, p *auth.Principal, systemPerms []string, key string,
) (projectDetailView, error) {
	row, err := q.GetProjectByKey(ctx, key)
	if err != nil {
		return projectDetailView{}, err
	}

	view := projectDetailView{
		ID:          row.ID,
		Key:         row.Key,
		Name:        row.Name,
		Description: textPtr(row.Description),
		Status:      row.Status,
		Settings:    settingsJSON(row.Settings),
		Version:     row.Version,
		CreatedAt:   Time(row.CreatedAt.Time),
		UpdatedAt:   Time(row.UpdatedAt.Time),
	}

	if row.WorkflowID.Valid {
		statuses, err := q.ListWorkflowStatuses(ctx, row.WorkflowID.String)
		if err != nil {
			return projectDetailView{}, fmt.Errorf("ワークフローのステータスを読めない: %w", err)
		}
		wf := &workflowView{
			ID:       row.WorkflowID.String,
			Name:     row.WorkflowName.String,
			Statuses: make([]workflowStatusView, 0, len(statuses)),
		}
		for _, s := range statuses {
			wf.Statuses = append(wf.Statuses, workflowStatusView{
				Key:                   s.Key,
				Name:                  s.Name,
				Category:              s.Category,
				SortOrder:             s.SortOrder,
				RequiresHumanApproval: s.RequiresHumanApproval,
				IsAgentReachable:      s.IsAgentReachable,
			})
		}
		view.Workflow = wf
	}

	members, err := q.ListProjectMembers(ctx, row.ID)
	if err != nil {
		return projectDetailView{}, fmt.Errorf("プロジェクトのメンバーを読めない: %w", err)
	}
	view.Members = make([]projectMemberView, 0, len(members))
	var myRole string
	for _, m := range members {
		var email *string
		if m.Email.Valid {
			v := m.Email.String
			email = &v
		}
		view.Members = append(view.Members, projectMemberView{
			ActorID:     m.ActorID,
			Kind:        m.Kind,
			DisplayName: m.DisplayName,
			Email:       email,
			Role:        m.RoleKey,
			JoinedAt:    Time(m.JoinedAt.Time),
		})
		// **委譲：エージェントでは所有者のメンバーシップを見る**
		// （Design.md 6.5、ApiDesign.md 5.4）。エージェントは project_member の
		// 行を持たないので、ActorID で照合すると my_role が常に null になり、
		// **下の my_permissions からプロジェクトロール層が丸ごと落ちる**——
		// 認可は AuthzActorID で通す（middleware/authz.go）ので、
		// 「できるのに、できないと応答している」状態になる。
		//
		// GET /me（4.1）は 24a でこの形になっている。手順25 で MCP の
		// pb_get_project が 5.4 を初めてエージェントのトークンで叩き、
		// 割れていることが分かった。
		if m.ActorID == p.AuthzActorID() {
			myRole = m.RoleKey
		}
	}
	view.MyRole = nullable(myRole)

	// 実効権限 = ( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ スコープ
	// （Design.md 6.4.1）。メンバーでなければプロジェクト層は空になる。
	var projectPerms []string
	if myRole != "" {
		projectPerms, err = q.ListRolePermissions(ctx, myRole)
		if err != nil {
			return projectDetailView{}, fmt.Errorf("プロジェクトロール %q の権限を読めない: %w", myRole, err)
		}
	}
	view.MyPermissions = auth.EffectivePermissions(systemPerms, projectPerms, p.Scopes)

	return view, nil
}

// settingsJSON は jsonb 列を応答へそのまま載せる。
//
// NULL や空バイト列は空オブジェクトに倒す。settings は NOT NULL DEFAULT '{}'
// （DbDesign.md 6.4）なので通常は起こらないが、JSON として壊れた応答を
// 返さないための保険である。
func settingsJSON(b []byte) json.RawMessage {
	if len(b) == 0 {
		return json.RawMessage(`{}`)
	}
	return json.RawMessage(b)
}
