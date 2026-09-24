// ユーザー管理API（ApiDesign.md 6章）のうち、詳細。
//
//	GET /api/v1/admin/users/:id  6.3
//
// 一覧（6.1）は users.go、作成（6.2）は users_create.go、更新・削除（6.4 / 6.5）は
// users_update.go、資格情報（6.6 / 6.7）は users_credentials.go、
// メンバーシップ（6.8）は users_memberships.go にある。
//
// **応答の組み立て（buildUserDetail）は本ファイルが持つ。** 6.4 の PATCH も
// 同じ形を返すため（5.5 が 5.4 と同形式であるのと同じ前例）、同じ形を
// 2か所で作らない。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// userIDURLParam は /admin/users/{id} のパスパラメータ名。
// ルート定義（routes.go）と chi.URLParam の引数を1か所で揃える。
const userIDURLParam = "id"

// userDetailView は 6.3 の応答。
//
// **`GuiDesign.md` 5.6.2 の詳細画面が必要とする情報を1回で返す**（設計方針3）。
// 基本情報・システムロール・プロジェクトごとの権限・認証手段・有効なセッションの
// 5ブロックが、それぞれ本体・project_memberships・identities・sessions に対応する。
//
// **一覧（6.1）の要素とは別の型にする。** 6.3 は project_count を持たず、
// 代わりに version と3つの配列を持つ。共通の親を作って埋め込むと、どちらに
// 何があるかが型から読めなくなる（createdUserView と同じ方針）。
type userDetailView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	SystemRole  string `json:"system_role"`
	IsActive    bool   `json:"is_active"`
	LastLoginAt *Time  `json:"last_login_at"`
	CreatedAt   Time   `json:"created_at"`
	Version     int32  `json:"version"`

	Identities         []userIdentityView   `json:"identities"`
	ProjectMemberships []userMembershipView `json:"project_memberships"`
	Sessions           []userSessionView    `json:"sessions"`

	// MFACredentialCount は確定済みの第2要素の件数（ApiDesign.md 6.3）。
	//
	// **配列ではなく件数だけを返す。** 画面（GuiDesign.md 5.6.2）が出すのも
	// 件数であり、他人の端末の名前は管理に要らない。
	MFACredentialCount int64 `json:"mfa_credential_count"`

	// PasskeyCount は登録済みのパスキーの件数（ApiDesign.md 6.3）。
	// MFACredentialCount と同じ理由で件数だけを返す。
	PasskeyCount int64 `json:"passkey_count"`
}

// userIdentityView は 6.3 の identities[] 要素（DbDesign.md 6.2 の user_identity）。
//
// **この配列が IdP 連携（構想）をそのまま受け入れる。** OIDC を追加しても
// 要素が1つ増えるだけで、応答構造もUIも変わらない。
//
// password_updated_at は local_credential の列であり、ローカル以外の
// プロバイダでは null になる。
type userIdentityView struct {
	ID                string `json:"id"`
	ProviderKey       string `json:"provider_key"`
	ProviderType      string `json:"provider_type"`
	Subject           string `json:"subject"`
	LinkedAt          Time   `json:"linked_at"`
	LastUsedAt        *Time  `json:"last_used_at"`
	PasswordUpdatedAt *Time  `json:"password_updated_at"`
}

// userMembershipView は 6.3 の project_memberships[] 要素。
//
// **JSON のキーは `role` である**（6.3 の例）。DB の列名は
// project_member.role_key だが、API のフィールド名は設計文書に従う。
//
// 6.8 の PUT もこの型を返す。同じものを2つの形で返さないためである。
type userMembershipView struct {
	ProjectID   string `json:"project_id"`
	ProjectKey  string `json:"project_key"`
	ProjectName string `json:"project_name"`
	Role        string `json:"role"`
	JoinedAt    Time   `json:"joined_at"`
}

// userSessionView は 6.3 の sessions[] 要素（access_token の token_type='session'）。
//
// **平文のトークンも token_hash も載せない。** 画面（GuiDesign.md 5.6.2）が
// 必要とするのは「いつ・どの端末から入っているか」だけである。
type userSessionView struct {
	ID         string  `json:"id"`
	ClientInfo *string `json:"client_info"`
	IssuedAt   Time    `json:"issued_at"`
	LastUsedAt *Time   `json:"last_used_at"`
	ExpiresAt  *Time   `json:"expires_at"`
}

// getUser は GET /api/v1/admin/users/:id を処理する（ApiDesign.md 6.3）。
func (h *handler) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFromRequest(w, r, "GET /admin/users/{id}")
	if !ok {
		return
	}

	ctx := r.Context()
	view, err := buildUserDetail(ctx, h.q, id)
	if err != nil {
		writeUserDetailError(w, r, id, err)
		return
	}

	// **一覧（6.1）と違い ETag は返さない。** 詳細の鮮度は本体の version が
	// 表しており（2.8 の楽観ロックがそれを使う）、検証子を二重に持たせない。
	// GET /projects/{key}（5.4）も同じ扱いである。
	WriteJSON(w, http.StatusOK, view)
}

// buildUserDetail は 6.3 の応答を組み立てる。
//
// **4本のクエリを1つの Querier で通す。** 呼び出し側が gen.Querier を渡すので、
// PATCH（6.4）は更新と同じトランザクションから読める。別トランザクションで
// 読むと、返した version が既に古いことがありうる（buildProjectDetail と同じ）。
//
// 本体が見つからなければ pgx.ErrNoRows がそのまま返る（＝404）。
func buildUserDetail(ctx context.Context, q gen.Querier, actorID string) (userDetailView, error) {
	u, err := q.GetAdminUser(ctx, actorID)
	if err != nil {
		return userDetailView{}, err
	}

	identities, err := q.ListUserIdentities(ctx, actorID)
	if err != nil {
		return userDetailView{}, fmt.Errorf("認証手段を取得できない: %w", err)
	}
	memberships, err := q.ListUserProjectMemberships(ctx, actorID)
	if err != nil {
		return userDetailView{}, fmt.Errorf("プロジェクトメンバーシップを取得できない: %w", err)
	}
	sessions, err := q.ListUserSessions(ctx, actorID)
	if err != nil {
		return userDetailView{}, fmt.Errorf("有効なセッションを取得できない: %w", err)
	}

	view := userDetailView{
		ID:          u.ID,
		Kind:        u.Kind,
		DisplayName: u.DisplayName,
		Email:       u.Email,
		SystemRole:  u.SystemRole,
		IsActive:    u.IsActive,
		LastLoginAt: apiTimestamptz(u.LastLoginAt),
		CreatedAt:   Time(u.CreatedAt.Time),
		Version:     u.Version,
		// **nil ではなく空スライスで初期化する。** JSON が null になると、
		// 画面が配列として回せない（NewList が items に対して行うのと同じ理由）。
		Identities:         make([]userIdentityView, 0, len(identities)),
		ProjectMemberships: make([]userMembershipView, 0, len(memberships)),
		Sessions:           make([]userSessionView, 0, len(sessions)),
		MFACredentialCount: u.MfaCredentialCount,
		PasskeyCount:       u.PasskeyCount,
	}

	for _, row := range identities {
		view.Identities = append(view.Identities, userIdentityView{
			ID:                row.ID,
			ProviderKey:       row.ProviderKey,
			ProviderType:      row.ProviderType,
			Subject:           row.Subject,
			LinkedAt:          Time(row.LinkedAt.Time),
			LastUsedAt:        apiTimestamptz(row.LastUsedAt),
			PasswordUpdatedAt: apiTimestamptz(row.PasswordUpdatedAt),
		})
	}
	for _, row := range memberships {
		view.ProjectMemberships = append(view.ProjectMemberships, newUserMembershipView(
			row.ProjectID, row.ProjectKey, row.ProjectName, row.RoleKey, row.JoinedAt,
		))
	}
	for _, row := range sessions {
		view.Sessions = append(view.Sessions, userSessionView{
			ID:         row.ID,
			ClientInfo: textPtr(row.ClientInfo),
			IssuedAt:   Time(row.IssuedAt.Time),
			LastUsedAt: apiTimestamptz(row.LastUsedAt),
			ExpiresAt:  apiTimestamptz(row.ExpiresAt),
		})
	}
	return view, nil
}

// newUserMembershipView は 6.3 と 6.8 で共有する要素の組み立て。
// 引数を並べているのは、sqlc が2つのクエリに別々の Row 型を生成するためである。
func newUserMembershipView(
	projectID, projectKey, projectName, roleKey string, joinedAt pgtype.Timestamptz,
) userMembershipView {
	return userMembershipView{
		ProjectID:   projectID,
		ProjectKey:  projectKey,
		ProjectName: projectName,
		Role:        roleKey,
		JoinedAt:    Time(joinedAt.Time),
	}
}

// userIDFromRequest は /admin/users/{id} 配下のハンドラが共通で行う取り出し。
//
// {id} が空なのはルート定義の誤りであって利用者の入力の誤りではないため、
// 500 に倒して原因をログへ残す（projectRequestContext と同じ扱い）。
//
// **値の形（ULID 26文字）はここで検証しない。** 不正な ID は単に行が
// 見つからず 404 になる。存在しないものと形式が違うものを別の応答に
// 分けると、ID の総当たりに手がかりを与える（ApiDesign.md 1.2-5）。
func userIDFromRequest(w http.ResponseWriter, r *http.Request, route string) (string, bool) {
	id := chi.URLParam(r, userIDURLParam)
	if id == "" {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s のルートに {%s} が無い", route, userIDURLParam)))
		return "", false
	}
	return id, true
}

// adminUserContext は /admin/users/{id} 配下の**状態を変える**ハンドラが
// 共通で行う取り出し（6.4〜6.8）。
//
// プリンシパルと {id}、そしてトランザクション実行口の有無をまとめて確かめる。
// いずれも欠けているのは配線の誤り（ルート定義や Deps の組み立て）であって
// 利用者の入力の誤りではないため、500 に倒して原因をログへ残す。
//
// GET（6.3）はトランザクションを要らないので userIDFromRequest だけを使う。
func (h *handler) adminUserContext(
	w http.ResponseWriter, r *http.Request, route string,
) (*auth.Principal, string, bool) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s が認証ミドルウェアを通っていない", route)))
		return nil, "", false
	}
	id, ok := userIDFromRequest(w, r, route)
	if !ok {
		return nil, "", false
	}
	if h.tx == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("%s にトランザクション実行口が渡っていない", route)))
		return nil, "", false
	}
	return p, id, true
}

// writeUserDetailError は 6.3 の応答を組み立てられなかったときの応答。
//
// **行が無いのは 404。** kind が user でないアクター（エージェント・システム）も
// GetAdminUser が 0 行を返すため、ここに合流する（手順13a の判断。6章が扱うのは
// 人間のアカウントであり、エージェントの詳細は未実装）。
func writeUserDetailError(w http.ResponseWriter, r *http.Request, id string, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		apierr.Write(w, r, apierr.New(apierr.NotFound).
			WithCause(fmt.Errorf("ユーザー %q が見つからない", id)))
		return
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).
		WithCause(fmt.Errorf("ユーザー %q を読めない: %w", id, err)))
}
