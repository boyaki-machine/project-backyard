/**
 * ユーザー管理のエンドポイント（`ApiDesign.md` 6.1 / 6.2）。
 *
 * すべて **アドミニストレータ専用**（`user.manage`）。型は `docs/design/openapi.yaml` の
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

// ── ユーザー詳細・編集（`ApiDesign.md` 6.3〜6.8）────────────────────
//
// **API は手順13a で実装済み**で、ここに足すのは呼び出しのラッパだけである
// （消費者と同じセッションに入れるため 13a では書かなかった）。

export type UserDetail = components['schemas']['UserDetail']
export type UserIdentity = components['schemas']['UserIdentity']
export type UserMembership = components['schemas']['UserMembership']
export type UserSession = components['schemas']['UserSession']
export type UpdateUserRequest = components['schemas']['UpdateUserRequest']
export type PasswordResetRequest = components['schemas']['PasswordResetRequest']
export type GeneratedPassword = components['schemas']['GeneratedPassword']
export type MembershipRequest = components['schemas']['MembershipRequest']

/**
 * ユーザーの詳細（`ApiDesign.md` 6.3）。
 *
 * 本体・認証手段・プロジェクトごとの権限・有効なセッションが1回で返るので、
 * 詳細画面（`GuiDesign.md` 5.6.2）はこの1本で描ける（設計方針3）。
 *
 * **`kind='user'` のアクターだけを返す**。エージェント・システムアクターは 404
 * になる（13a の判断）。**一覧と違い `ETag` は返らない**——鮮度は `version` が表す。
 */
export function getUser(id: string): Promise<UserDetail> {
  return api.get<UserDetail>(`/admin/users/${encodeURIComponent(id)}`)
}

/**
 * ユーザーの更新（`ApiDesign.md` 6.4）。
 *
 * **送るのは変更したフィールドだけ**（部分更新）。`version` は取得時の値を
 * `If-Match` に載せる（2.8）。食い違えば 409 で、成功すると +1 される。
 * **`display_name` や `is_active` だけを変えた場合も +1 される**——ユーザー1人に
 * つき `version` は1つ、という約束にしないと古い値で二度目が通ってしまう。
 *
 * 409 は4種類ありうる（`conflict` / `already_exists` /
 * `self_modification_forbidden` / `last_administrator`）。**画面は
 * `message` をそのまま出す**ので、コードで分岐するのは楽観ロックの
 * `conflict`（再取得を促す）だけでよい。
 */
export function updateUser(
  id: string,
  version: number,
  body: UpdateUserRequest,
): Promise<UserDetail> {
  return api.patch<UserDetail>(`/admin/users/${encodeURIComponent(id)}`, body, {
    // RFC 9110 8.8.3 の entity-tag。引用符で囲む（`projects.ts` と同じ）
    headers: { 'If-Match': `"${version}"` },
  })
}

/**
 * ユーザーの削除（`ApiDesign.md` 6.5）。物理削除で `204`。
 *
 * **`If-Match` は要求しない**（2.8）。失われる編集内容が無く、競合しても
 * 「消えている」に収束するため。自分自身と最後の有効なアドミニストレータは 409。
 */
export function deleteUser(id: string): Promise<void> {
  return api.del<void>(`/admin/users/${encodeURIComponent(id)}`)
}

/**
 * パスワードのリセット（`ApiDesign.md` 6.6）。
 *
 * **本文そのものを省略してよい**（`mode=generate` / `must_change_password=true`）。
 * `mode` は `generate` のみ受け付けるので、画面から選ばせるものが無い。
 *
 * `local_credential` を差し替え、`failed_attempts` と `locked_until` をリセットし、
 * **全セッションを失効**する。**`generated_password` はこの応答でのみ返る**
 * （再表示できず、監査ログにも残らない）。
 */
export function resetUserPassword(id: string): Promise<GeneratedPassword> {
  return api.post<GeneratedPassword>(`/admin/users/${encodeURIComponent(id)}/password-reset`)
}

/**
 * 全セッションの失効（`ApiDesign.md` 6.7）。`204`。
 *
 * **冪等**で、有効なトークンが1本も無くても `204` を返す。
 * **個別のセッションを失効させる API は Phase 1 では持たない**——一覧は参照のみ
 * で、行ごとの `[失効]` は出さない（`GuiDesign.md` 5.6.2、13a の判断）。
 */
export function revokeUserSessions(id: string): Promise<void> {
  return api.post<void>(`/admin/users/${encodeURIComponent(id)}/sessions/revoke`)
}

/**
 * 第2要素の解除（`ApiDesign.md` 6.9。pb-103）。`204`。
 *
 * **本人がリカバリコードまで失ったときの口である。** 対象は次のログインから
 * パスワードだけで入れるようになる。**パスワードには触らず、セッションも切らない**
 * ——締め出しの原因が「パスワードを忘れた」（6.6）とは別物だからである。
 *
 * **冪等**で、1件も登録が無くても `204` を返す。
 */
export function resetUserMfa(id: string): Promise<void> {
  return api.post<void>(`/admin/users/${encodeURIComponent(id)}/mfa/reset`)
}

/**
 * パスキーの全削除（`ApiDesign.md` 6.10。pb-104）。`204`。
 *
 * **乗っ取りの疑いがあるときの口である。** パスキーはパスワード無しで入れる鍵なので、
 * 乗っ取った人が登録した1本は、パスワードのリセットも第2要素の解除も消さない。
 * **パスワードには触らず、セッションも切らない**——直すなら 6.6・6.7 と組み合わせる。
 *
 * **冪等**で、1件も登録が無くても `204` を返す。
 */
export function resetUserPasskeys(id: string): Promise<void> {
  return api.post<void>(`/admin/users/${encodeURIComponent(id)}/passkeys/reset`)
}

/**
 * プロジェクトメンバーシップの付与・変更（`ApiDesign.md` 6.8）。
 *
 * **追加と変更を兼ねる（冪等）。** 既にメンバーならロールを上書きし、
 * `joined_at` は最初の1回のまま動かない。応答は 6.3 の
 * `project_memberships[]` の要素と同形。
 */
export function putMembership(
  id: string,
  projectKey: string,
  role: string,
): Promise<UserMembership> {
  const body: MembershipRequest = { role }
  return api.put<UserMembership>(
    `/admin/users/${encodeURIComponent(id)}/memberships/${encodeURIComponent(projectKey)}`,
    body,
  )
}

/**
 * プロジェクトメンバーシップの剥奪（`ApiDesign.md` 6.8）。`204`。
 *
 * **冪等**で、元からメンバーでなくても `204` を返す。
 */
export function deleteMembership(id: string, projectKey: string): Promise<void> {
  return api.del<void>(
    `/admin/users/${encodeURIComponent(id)}/memberships/${encodeURIComponent(projectKey)}`,
  )
}
