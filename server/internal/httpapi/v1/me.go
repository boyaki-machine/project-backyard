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
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// actorView は応答の actor 部分（ApiDesign.md 3.1）。
//
// email / system_role / locale / timezone をポインタにしているのは、
// エージェント（Phase 2）が app_user の行を持たないためである。
// 「kind によって意味を持たないフィールドは null を返し、フィールド自体を
// 省略しない」（ApiDesign.md 6.1）に従う。
type actorView struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	DisplayName string  `json:"display_name"`
	Email       *string `json:"email"`
	SystemRole  *string `json:"system_role"`
	Locale      *string `json:"locale"`
	Timezone    *string `json:"timezone"`
	// Theme / Hue は GuiDesign.md 8.11 のテーマ設定（手順15 で足した）。
	//
	// **サーバが返さないと、別の端末でログインしたときに同じ見た目にならない。**
	// 8.11 が app_user と localStorage の両方に保存すると定めた目的がそれで
	// あり、localStorage だけでは端末をまたげない。
	Theme              *string `json:"theme"`
	Hue                *string `json:"hue"`
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
	ActorID string
	// AuthzActorID は「誰のプロジェクトロールを読むか」（Design.md 6.5 の委譲）。
	//
	// **エージェントでは所有者の actor.id が入る。** エージェントは
	// project_member の行を持たないため、ActorID で引くと所属プロジェクトが
	// 常に空になり、MCP がどのプロジェクトにも到達できない。
	//
	// **空なら ActorID を使う。** ログイン（login.go）と PATCH /me
	// （me_update.go）は人間しか通らない経路なので、そちらは詰めていない。
	AuthzActorID       string
	Kind               string
	DisplayName        string
	Email              string
	SystemRole         string
	Locale             string
	Timezone           string
	Theme              string
	Hue                string
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
		ActorID:      p.ActorID,
		AuthzActorID: p.AuthzActorID(),
		Kind:         p.ActorKind,
		DisplayName:  p.DisplayName,
		Email:        p.Email,
		SystemRole:   p.SystemRole,
	}
	if err == nil {
		prof = profile{
			ActorID:            row.ActorID,
			AuthzActorID:       p.AuthzActorID(),
			Kind:               row.Kind,
			DisplayName:        row.DisplayName,
			Email:              row.Email.String,
			SystemRole:         row.SystemRole.String,
			Locale:             row.Locale.String,
			Timezone:           row.Timezone.String,
			Theme:              row.Theme.String,
			Hue:                row.Hue.String,
			MustChangePassword: row.MustChange.Bool,
		}
	}

	// 認可ミドルウェアと同じ経路でシステムロール層を解決する
	// （キャッシュ優先。Design.md 6.4.5）。**両者で解決の仕方を変えない。**
	// /me が新しい権限を返すのにミドルウェアが古い権限で拒むと、画面が
	// 出したボタンが 403 になる。
	systemPerms, _, err := middleware.SystemPermissions(r.Context(), h.q, p)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	view, err := h.buildSessionView(r.Context(), h.q, prof, systemPerms, p.Scopes, p.ExpiresAt)
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
//
// systemPerms は解決済みのシステムロール層（スコープとの積を取った後）を
// 受け取る。ログインは計算した値を、GET /me はキャッシュ優先で解決した値を
// 渡す（6.4.5）。**プロジェクト層はキャッシュしない**（0012 の説明を参照）
// ため、ここでは毎回引く。
//
// **問い合わせ口を引数で受ける。** PATCH /me（4.2）はトランザクションの中で
// 応答を組み立てるため、h.q ではなくそのトランザクションの Querier を渡す
// 必要がある。プールの側を使うと、まだコミットしていない更新が応答に
// 載らない（buildUserDetail が同じ理由で q を受けているのと同じ）。
func (h *handler) buildSessionView(
	ctx context.Context, q gen.Querier, prof profile,
	systemPerms, scopes []string, expiresAt *time.Time,
) (sessionView, error) {
	if systemPerms == nil {
		// permissions を JSON の null にしない。権限0件は [] で表す。
		systemPerms = []string{}
	}

	// **所属は AuthzActorID で引く**（Design.md 6.5 の委譲）。エージェントは
	// 自前の project_member を持たず、所有者の所属をそのまま使う。
	authzActorID := prof.AuthzActorID
	if authzActorID == "" {
		authzActorID = prof.ActorID
	}
	rows, err := q.ListProjectMembershipsByActor(ctx, authzActorID)
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

	// systemPerms は既にスコープと積を取った後の集合だが、
	// ( A ∪ B ) ∩ S = ( A ∩ S ) ∪ ( B ∩ S ) であり S との積は冪等なので、
	// もう一度積を取っても 6.4.1 の式と同じ結果になる（手順6a と同じ議論）。
	projects := make([]projectView, 0, len(order))
	for _, id := range order {
		a := byID[id]
		a.view.Permissions = auth.EffectivePermissions(systemPerms, a.perms, scopes)
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
			Theme:              nullable(prof.Theme),
			Hue:                nullable(prof.Hue),
			MustChangePassword: prof.MustChangePassword,
		},
		Permissions: systemPerms,
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
