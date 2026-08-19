/**
 * ユーザー管理のエンドポイント（`ApiDesign.md` 6.1 / 6.2）。
 *
 * すべて **アドミニストレータ専用**（`user.manage`）。型は `docs/openapi.yaml` の
 * 生成物をそのまま使う。ここで別名を定義し直さない（`projects.ts` と同じ方針）。
 */
import { api } from './client'
import type { components } from './schema'

export type UserList = components['schemas']['UserList']
export type UserListItem = components['schemas']['UserListItem']
export type CreateUserRequest = components['schemas']['CreateUserRequest']
export type CreatedUser = components['schemas']['CreatedUser']

/**
 * ソート可能な列（6.1）。**種別以外のすべての列**が対象である。
 *
 * `system_role` は `role.sort_order` の順（オペレータ → アドミニストレータ）で、
 * 表示名の五十音順ではない。`is_active` の昇順は無効が先。**並びの意味づけは
 * サーバが持つ**ので、画面はキーを送るだけでよい。
 */
export type UserSort =
  | 'display_name'
  | 'email'
  | 'system_role'
  | 'is_active'
  | 'last_login_at'
  | 'created_at'

export type SortOrder = 'asc' | 'desc'

/** `kind` の3値（6.1）。`system` の actor は API が常に除外する */
export type UserKindFilter = 'user' | 'agent' | 'all'

/**
 * `is_active` の3値（6.1）。**真偽値ではなく文字列**である。
 * 未指定（`all`）と `false` を区別するため。
 */
export type UserActiveFilter = 'true' | 'false' | 'all'

export interface ListUsersQuery {
  kind?: UserKindFilter
  is_active?: UserActiveFilter
  q?: string
  sort?: UserSort
  order?: SortOrder
  page?: number
  per_page?: number
}

/**
 * ユーザー一覧（`ApiDesign.md` 6.1）。
 *
 * **人間とエージェントが同じ一覧に返る**（`DbDesign.md` 6.2 の `actor` 統合設計）。
 * `kind` によって意味を持たないフィールドは `null` で返り、キー自体は省略されない。
 *
 * 既定値はサーバが持つ（`kind=all` / `is_active=all` / `sort=display_name` /
 * `order=asc` / `page=1` / `per_page=25`）。**`order` の既定だけ `GET /projects`
 * と違う**（名簿は昇順で読むため）。未指定の項目は送らない。
 */
export function listUsers(query: ListUsersQuery = {}): Promise<UserList> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v === undefined || v === '') continue
    params.set(k, String(v))
  }
  const qs = params.toString()
  return api.get<UserList>(`/admin/users${qs === '' ? '' : `?${qs}`}`)
}

/**
 * ユーザーの作成（`ApiDesign.md` 6.2）。
 *
 * 応答は一覧の要素とは別の形（`CreatedUser`）で、**`generated_password` は
 * この応答でのみ返る**。再表示できず、監査ログにも残らない。
 * `password_mode=manual` のときは `null`（呼び出し側が既に平文を持っているため）。
 *
 * メールの重複は 409 `already_exists`。
 */
export function createUser(body: CreateUserRequest): Promise<CreatedUser> {
  return api.post<CreatedUser>('/admin/users', body)
}
