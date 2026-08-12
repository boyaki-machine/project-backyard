// GET /api/v1/me（ApiDesign.md 4.1）と、POST /auth/login（3.1）が共有する
// 応答の組み立て。
//
// 「ログイン応答に GET /me と同じ内容を含める」（3.1）ため、両者は同じ
// 構造体・同じ組み立て関数を使う。片方だけ形が変わることを防ぐ。
package v1

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// actorView は応答の actor 部分（ApiDesign.md 3.1）。
//
// email / system_role / locale / timezone をポインタにしているのは、
// エージェント（Phase 2）が app_user の行を持たないためである。
// 「kind によって意味を持たないフィールドは null を返し、フィールド自体を
// 省略しない」（ApiDesign.md 6.1）に従う。
type actorView struct {
	ID                 string  `json:"id"`
	Kind               string  `json:"kind"`
	DisplayName        string  `json:"display_name"`
	Email              *string `json:"email"`
	SystemRole         *string `json:"system_role"`
	Locale             *string `json:"locale"`
	Timezone           *string `json:"timezone"`
	MustChangePassword bool    `json:"must_change_password"`
}

// projectView は応答の projects[] 要素（ApiDesign.md 3.1）。
type projectView struct {
	ID          string   `json:"id"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

// sessionView は POST /auth/login と GET /me が返す共通の本体。
type sessionView struct {
	Actor actorView `json:"actor"`
	// Permissions はシステムロール由来の実効権限（ApiDesign.md 4.1）。
	Permissions []string `json:"permissions"`
	// Projects は所属プロジェクトと、そこでの実効権限。
	Projects []projectView `json:"projects"`
	// ExpiresAt は現在のトークンの有効期限。無期限のAPIトークンでは null。
	ExpiresAt *Time `json:"expires_at"`
}

// profile は sessionView を組み立てるための素材。
//
// ログインは FindLocalLoginByEmail の結果から、GET /me は GetActorProfile の
// 結果から、それぞれこの形に詰め替える。
type profile struct {
	ActorID            string
	Kind               string
	DisplayName        string
	Email              string
	SystemRole         string
	Locale             string
	Timezone           string
	MustChangePassword bool
}

// me は GET /api/v1/me を処理する（ApiDesign.md 4.1）。
//
// 全画面の起動時に呼ばれる。認証ミドルウェアを通っているので、
// プリンシパルは必ずコンテキストにある。
func (h *handler) me(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		// 認証必須グループの外にこのハンドラを置いてしまった場合にだけ起きる。
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("GET /me が認証ミドルウェアを通っていない")))
		return
	}

	row, err := h.q.GetActorProfile(r.Context(), p.ActorID)
	if err != nil && err != pgx.ErrNoRows {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	// 行が取れないのは actor が消えた直後などの競合。認証済みの素材で埋める。
	prof := profile{
		ActorID:     p.ActorID,
		Kind:        p.ActorKind,
		DisplayName: p.DisplayName,
		Email:       p.Email,
		SystemRole:  p.SystemRole,
	}
	if err == nil {
		prof = profile{
			ActorID:            row.ActorID,
			Kind:               row.Kind,
			DisplayName:        row.DisplayName,
			Email:              row.Email.String,
			SystemRole:         row.SystemRole.String,
			Locale:             row.Locale.String,
			Timezone:           row.Timezone.String,
			MustChangePassword: row.MustChange.Bool,
		}
	}

	view, err := h.buildSessionView(r.Context(), prof, p.Scopes, p.ExpiresAt)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	WriteJSON(w, http.StatusOK, view)
}

// buildSessionView は実効権限を計算して応答を組み立てる（Design.md 6.4.1）。
//
//	実効権限 = ( システムロールの権限 ∪ プロジェクトロールの権限 ) ∩ スコープ
//
// permissions はシステムロール由来のみ、projects[].permissions は当該
// プロジェクトでの実効権限とする（ApiDesign.md 4.1）。
func (h *handler) buildSessionView(
	ctx context.Context, prof profile, scopes []string, expiresAt *time.Time,
) (sessionView, error) {
	var rolePermissions []string
	if prof.SystemRole != "" {
		var err error
		rolePermissions, err = h.q.ListRolePermissions(ctx, prof.SystemRole)
		if err != nil {
			return sessionView{}, fmt.Errorf("システムロール %q の権限を読めない: %w", prof.SystemRole, err)
		}
	}

	rows, err := h.q.ListProjectMembershipsByActor(ctx, prof.ActorID)
	if err != nil {
		return sessionView{}, fmt.Errorf("所属プロジェクトを読めない: %w", err)
	}

	// プロジェクトごとに複数行（権限の数だけ）返るので畳む。
	// SQL 側が p.key 昇順で返すため、出現順がそのまま応答の順序になる。
	type acc struct {
		view  projectView
		perms []string
	}
	var order []string
	byID := map[string]*acc{}
	for _, row := range rows {
		a, ok := byID[row.ProjectID]
		if !ok {
			a = &acc{view: projectView{
				ID:   row.ProjectID,
				Key:  row.ProjectKey,
				Name: row.ProjectName,
				Role: row.RoleKey,
			}}
			byID[row.ProjectID] = a
			order = append(order, row.ProjectID)
		}
		if row.PermissionKey.Valid {
			a.perms = append(a.perms, row.PermissionKey.String)
		}
	}

	projects := make([]projectView, 0, len(order))
	for _, id := range order {
		a := byID[id]
		a.view.Permissions = auth.EffectivePermissions(rolePermissions, a.perms, scopes)
		projects = append(projects, a.view)
	}

	return sessionView{
		Actor: actorView{
			ID:                 prof.ActorID,
			Kind:               prof.Kind,
			DisplayName:        prof.DisplayName,
			Email:              nullable(prof.Email),
			SystemRole:         nullable(prof.SystemRole),
			Locale:             nullable(prof.Locale),
			Timezone:           nullable(prof.Timezone),
			MustChangePassword: prof.MustChangePassword,
		},
		Permissions: auth.EffectivePermissions(rolePermissions, nil, scopes),
		Projects:    projects,
		ExpiresAt:   apiTime(expiresAt),
	}, nil
}

// nullable は空文字を JSON の null に写す。
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
