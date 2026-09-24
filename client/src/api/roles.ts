/**
 * ロール・権限カタログのエンドポイント（`ApiDesign.md` 7.1 / 7.2）。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない
 * （`users.ts` と同じ方針）。
 */
import { api } from './client'
import type { components } from './schema'

export type RoleList = components['schemas']['RoleList']
export type Role = components['schemas']['Role']
export type PermissionList = components['schemas']['PermissionList']
export type Permission = components['schemas']['Permission']

/**
 * 取得する範囲（7.1 の `scope`）。
 *
 * **`all` は「クエリを付けない」を表す**——サーバの既定が全件であり、
 * `scope=all` という値は 422 になる（値域は `system` / `project` の2つ）。
 */
export type RoleScope = 'all' | 'system' | 'project'

/**
 * ロールのカタログ（`ApiDesign.md` 7.1）。
 *
 * **`scope` によって必要権限が変わる。** `project` は認証済みなら誰でも呼べるが、
 * それ以外は `user.manage` を要し、持たなければ 403 になる。呼び出し側は
 * `stores/roles.ts` を通すこと——直接呼ぶと画面ごとに 403 の扱いが散らばる。
 */
export function getRoles(scope: RoleScope = 'all'): Promise<RoleList> {
  const query = scope === 'all' ? '' : `?scope=${scope}`
  return api.get<RoleList>(`/roles${query}`)
}

/**
 * 権限のカタログ32件（`ApiDesign.md` 7.2）。**必要権限は無い**（認証済みであればよい）。
 *
 * 消費者は `GuiDesign.md` 5.6.3 の権限マトリクスと、5.8.2 のエージェント用トークンの
 * 発行結果である。
 */
export function getPermissions(): Promise<PermissionList> {
  return api.get<PermissionList>('/permissions')
}
