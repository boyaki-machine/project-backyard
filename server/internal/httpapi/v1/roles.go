// ロール・権限API（ApiDesign.md 7章）。
//
//	GET /api/v1/roles        7.1
//	GET /api/v1/permissions  7.2
//
// どちらも読み取り専用で、返すのは DbDesign.md 7.2 / 7.3 のシードそのもの
// である。カスタムロールの作成・権限の編集は Phase 3（7.3）。
//
// **監査ログには残さない。** 参照だけの操作であり、ApiDesign.md 2.10 が
// 対象と定める「認証・権限変更・データ変更」のいずれでもない。
package v1

import (
	"fmt"
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// scope の値（ApiDesign.md 7.1）。role.scope の CHECK 制約（DbDesign.md 6.3）
// と同じ語彙で、all は絞り込みをしないことを表す。
//
// **値域をこの3つに閉じる。** プロジェクトIDやユーザIDは受け付けない——
// role テーブルは project_id を持たず、カスタムロールも同じグローバルな表に
// 入るため、プロジェクトIDで絞っても結果が変わらない。「そのユーザに与えられた
// ロール」は 6.3 の GET /admin/users/:id が返す（7.1 の但し書き）。
const (
	roleScopeSystem  = "system"
	roleScopeProject = "project"

	// roleScopeAll は「絞り込まない」を表す**内部の値**で、クエリの値域には
	// 入らない。7.1 は `scope` の値域を system / project の2つと定め、全件は
	// 「未指定」で表す。**parseUserKindFilter（6.1 の kind）とはここが違う**
	// ——あちらは all を明示的な値として受けると 6.1 が定めている。
	roleScopeAll = "all"
)

// roleItem は 7.1 の items[] 要素。
//
// description は role.description（NULL 許容）。**キーは常に返し、
// フィールド自体を省略しない**——6.1 と同じ方針で、フロントの分岐を
// 単純にするためである。
type roleItem struct {
	Key         string   `json:"key"`
	Scope       string   `json:"scope"`
	DisplayName string   `json:"display_name"`
	Description *string  `json:"description"`
	IsBuiltin   bool     `json:"is_builtin"`
	SortOrder   int32    `json:"sort_order"`
	Permissions []string `json:"permissions"`
}

// permissionItem は 7.2 の items[] 要素。
type permissionItem struct {
	Key         string `json:"key"`
	Category    string `json:"category"`
	Description string `json:"description"`
	SortOrder   int32  `json:"sort_order"`
}

// catalog は 7.1 / 7.2 の応答エンベロープ。
//
// **List[T]（2.6）を使わない。** 件数がシードで固定されており、page /
// per_page / total / total_pages のいずれも意味を持たない。7.1 と 7.2 の
// 応答例も items だけを持つ。同じ理由で ETag（2.7）も付けない。
type catalog[T any] struct {
	Items []T `json:"items"`
}

// listRoles は GET /api/v1/roles を処理する（ApiDesign.md 7.1）。
//
// **必要権限は scope によって変わる**（?scope=project は不要、それ以外は
// user.manage）。判定は middleware.RequireRoleScopePermission がルート定義側で
// 行う。ここへ来た時点で認可は済んでいる。
func (h *handler) listRoles(w http.ResponseWriter, r *http.Request) {
	scope, err := parseRoleScopeFilter(r)
	if err != nil {
		apierr.Write(w, r, err)
		return
	}

	ctx := r.Context()
	roles, dbErr := h.q.ListRoles(ctx, scope)
	if dbErr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("ロール一覧を取得できない: %w", dbErr)))
		return
	}

	// **ロール1件ずつ問い合わせない**（設計方針3）。1回で取って role_key で畳む。
	assignments, dbErr := h.q.ListRolePermissionAssignments(ctx, scope)
	if dbErr != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("ロールの権限を取得できない: %w", dbErr)))
		return
	}

	// クエリが permission.sort_order の順で返すため、追記していけば順序が保たれる。
	byRole := make(map[string][]string, len(roles))
	for _, a := range assignments {
		byRole[a.RoleKey] = append(byRole[a.RoleKey], a.PermissionKey)
	}

	items := make([]roleItem, 0, len(roles))
	for _, role := range roles {
		// 権限を1件も持たないロールでも permissions は [] にする。
		// null にすると、フロントが「未取得」と「0件」を区別できない。
		perms := byRole[role.Key]
		if perms == nil {
			perms = []string{}
		}
		items = append(items, roleItem{
			Key:         role.Key,
			Scope:       role.Scope,
			DisplayName: role.DisplayName,
			Description: textPtr(role.Description),
			IsBuiltin:   role.IsBuiltin,
			SortOrder:   role.SortOrder,
			Permissions: perms,
		})
	}

	WriteJSON(w, http.StatusOK, catalog[roleItem]{Items: items})
}

// listPermissions は GET /api/v1/permissions を処理する（ApiDesign.md 7.2）。
//
// **必要権限は無い**（認証済みであればよい。2026-09-02 に user.manage から変更）。
// 消費者が2つある——GuiDesign.md 5.6.3 の権限マトリクスと、5.8.2 の
// エージェント用トークンの発行結果である。**後者の必要権限は「本人」**なので、
// user.manage を要求したままだと description を引けない。
//
// **description を画面へ焼き込む案は退けた。** 手順24a が「旧語彙で発行すると
// 実効権限が0件になる」という形で、写しが腐る失敗を踏んだばかりである。
func (h *handler) listPermissions(w http.ResponseWriter, r *http.Request) {
	rows, err := h.q.ListPermissions(r.Context())
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("権限一覧を取得できない: %w", err)))
		return
	}

	items := make([]permissionItem, 0, len(rows))
	for _, p := range rows {
		items = append(items, permissionItem{
			Key:         p.Key,
			Category:    p.Category,
			Description: p.Description,
			SortOrder:   p.SortOrder,
		})
	}

	WriteJSON(w, http.StatusOK, catalog[permissionItem]{Items: items})
}

// parseRoleScopeFilter は scope クエリを解析する（ApiDesign.md 7.1）。
//
// 未指定は all。解釈できない値は 2.6 の方針どおり既定へ丸めず 422 にする
// （parseUserKindFilter と同じ扱い）。
//
// **middleware.RoleScopeFromRequest と同じ判定をしている。** あちらは
// 「素通ししてよい要求か」だけを見るため system / all / 不正値を区別せず、
// 値の検証はここが行う。したがって user.manage を持たない呼び出し元が
// 不正な値を送ると、422 ではなく 403 が先に返る。**認可を検証より先に
// 通すのは意図した順序である**——その呼び出し元は project 以外を要求できない。
func parseRoleScopeFilter(r *http.Request) (string, *apierr.Error) {
	switch v := r.URL.Query().Get("scope"); v {
	case "":
		return roleScopeAll, nil
	case roleScopeSystem, roleScopeProject:
		return v, nil
	default:
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "scope", Code: "invalid",
			Message: "scope は system / project のいずれかで指定してください",
		})
	}
}
