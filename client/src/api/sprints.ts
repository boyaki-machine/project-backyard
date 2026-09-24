/**
 * スプリントのエンドポイント（`ApiDesign.md` 9.12）。
 *
 * **Phase 1 で開けるのは定義だけである。** バーンダウン・ベロシティを含む
 * 運用画面は Phase 2 の `/p/:key/sprints`（`GuiDesign.md` 10章）で、ここは
 * プロジェクト設定のスプリントタブ（同 5.9.5）が消費者になる。
 */
import { api } from './client'
import type { components } from './schema'
import { uiText } from '../locales/ui'

export type Sprint = components['schemas']['Sprint']
export type SprintList = components['schemas']['SprintList']
export type SprintStatus = components['schemas']['SprintStatus']
export type CreateSprintRequest = components['schemas']['CreateSprintRequest']
export type PatchSprintRequest = components['schemas']['PatchSprintRequest']
export type StartSprintRequest = components['schemas']['StartSprintRequest']

/**
 * スプリントの状態の表示名（`GuiDesign.md` 5.9.5）。
 *
 * **キーから画面の文言を作らない。** サーバが返すのは `planned` などの
 * キーであり、日本語は画面が持つ。
 */
export const sprintStatusLabels: Record<SprintStatus, string> = {
  get planned() { return uiText('計画中') },
  get active() { return uiText('進行中') },
  get completed() { return uiText('完了') },
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

/**
 * スプリントを始める（9.12.1）。
 *
 * **新しく作り、`active` にし、オンステージに載っているものを対象に入れる**
 * ——3つがサーバ側の1トランザクションで起きる。**対象を画面から渡さない**のは、
 * 「オンステージ段に出ている行」の判定が `staged_at` だけでは決まらない
 * （子は親と一緒に運ばれる）ためで、サーバが部分木ごと取る。
 *
 * **進行中のスプリントが既にあると 409**（`active_sprint_exists`）。画面は
 * ボタンを出さないことで先に防ぐが、他の人が同時に始めた場合はここへ来る。
 */
export function startSprint(key: string, body: StartSprintRequest): Promise<Sprint> {
  return api.post<Sprint>(`/projects/${encodeURIComponent(key)}/sprints/start`, body)
}

/**
 * スプリントを終える（9.12.2）。**本文を取らない。**
 *
 * 完了しているオンステージの根が段から降り、**9.2.1 の3条件を満たすので
 * 一覧から消える**。未完了のものはオンステージに残る。
 *
 * **呼んだあとは一覧を引き直すこと。** 外れる行と残る行が同時に決まるので、
 * 手元で差分を当てずにサーバの答えを採る。
 */
export function finishSprint(key: string, id: string): Promise<Sprint> {
  return api.post<Sprint>(
    `/projects/${encodeURIComponent(key)}/sprints/${encodeURIComponent(id)}/finish`,
    {},
  )
}
