/**
 * スプリントのエンドポイント（`ApiDesign.md` 9.12）。
 *
 * **Phase 1 で開けるのは定義だけである。** バーンダウン・ベロシティを含む
 * 運用画面は Phase 2 の `/p/:key/sprints`（`GuiDesign.md` 10章）で、ここは
 * プロジェクト設定のスプリントタブ（同 5.9.5）が消費者になる。
 */
import { api } from './client'
import type { components } from './schema'

export type Sprint = components['schemas']['Sprint']
export type SprintList = components['schemas']['SprintList']
export type SprintStatus = components['schemas']['SprintStatus']
export type CreateSprintRequest = components['schemas']['CreateSprintRequest']
export type PatchSprintRequest = components['schemas']['PatchSprintRequest']

/**
 * スプリントの状態の表示名（`GuiDesign.md` 5.9.5）。
 *
 * **キーから画面の文言を作らない。** サーバが返すのは `planned` などの
 * キーであり、日本語は画面が持つ。
 */
export const sprintStatusLabels: Record<SprintStatus, string> = {
  planned: '計画中',
  active: '進行中',
  completed: '完了',
}

/**
 * プロジェクトのスプリント一覧（9.12）。
 *
 * `start_date` 降順（`null` は末尾）、同値は `created_at` 降順で返る。
 * タグと同じくページャを持たない。
 */
export function listSprints(key: string): Promise<SprintList> {
  return api.get<SprintList>(`/projects/${encodeURIComponent(key)}/sprints`)
}

/** スプリントを作る（9.12）。`status` を省くと `planned`。 */
export function createSprint(key: string, body: CreateSprintRequest): Promise<Sprint> {
  return api.post<Sprint>(`/projects/${encodeURIComponent(key)}/sprints`, body)
}

/**
 * スプリントを更新する（9.12）。
 *
 * **`goal` / `start_date` / `end_date` は `null` を送ると値が消える。**
 * キーを含めなければ据え置きで、両者は別の意味を持つ。
 */
export function updateSprint(
  key: string,
  id: string,
  body: PatchSprintRequest,
): Promise<Sprint> {
  return api.patch<Sprint>(
    `/projects/${encodeURIComponent(key)}/sprints/${encodeURIComponent(id)}`,
    body,
  )
}

/**
 * スプリントを消す（9.12）。**チケットは消えない**——`ticket.sprint_id` が
 * `NULL` に戻るだけである。確認は画面側が出す（`GuiDesign.md` 6.3）。
 */
export function deleteSprint(key: string, id: string): Promise<void> {
  return api.del<void>(
    `/projects/${encodeURIComponent(key)}/sprints/${encodeURIComponent(id)}`,
  )
}
