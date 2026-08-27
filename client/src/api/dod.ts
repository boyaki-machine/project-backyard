/**
 * チケットの完了条件（DoD）のエンドポイント（`ApiDesign.md` 9.9）。手順18b。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 *
 * **一覧を呼ぶ画面は無い。** 詳細応答（9.5.1）の `dod` に同じものが入っているので、
 * チケット詳細は `GET /tickets/:seq` 1本で足りる（`GuiDesign.md` 5.5、
 * `ApiDesign.md` 8章の「起動時1〜2本」）。`listDoD` を置いてあるのは、
 * **追加・削除のあとに手元の配列を取り直したくなったときの逃げ道**としてで、
 * 既定の経路ではない（`references.ts` と同じ位置づけ）。
 *
 * **Phase 1 が扱う `type` は `manual` だけである。** `assertion` / `artifact` /
 * `review` / `task_ref` は Phase 2（`Requirements.md` 10.5.2）で、サーバは
 * 422 `phase_2_only` を返す。**画面に型の選択そのものを置かない。**
 */
import { api } from './client'
import type { components } from './schema'

export type TicketDoDItem = components['schemas']['TicketDoDItem']
export type TicketDoDList = components['schemas']['TicketDoDList']
export type CreateTicketDoDRequest = components['schemas']['CreateTicketDoDRequest']
export type PatchTicketDoDRequest = components['schemas']['PatchTicketDoDRequest']

function base(key: string, seq: number): string {
  return `/projects/${encodeURIComponent(key)}/tickets/${seq}/dod`
}

/**
 * 完了条件の一覧（9.9）。`sort_order` → `created_at` の昇順で返る。
 *
 * 必要権限は `ticket.view`。
 */
export function listDoD(key: string, seq: number): Promise<TicketDoDList> {
  return api.get<TicketDoDList>(base(key, seq))
}

/**
 * 完了条件を1件足す（9.9）。必要権限は `ticket.edit`。
 *
 * **`sort_order` を省くと末尾**（現在の最大値 + 10）。画面は並べ替えを持たない
 * ので常に省く（`GuiDesign.md` 5.5）。
 *
 * **`type` と `is_satisfied` は明示して送る。** 生成された型が必須にしている
 * ためで（既定値を持つ項目は省略可にならない）、**サーバの既定と同じ値を
 * 送るので挙動は省いたときと変わらない**。
 *
 * **親チケットの `version` は動かない**ので、呼び出し側が持ち回っている
 * `version` を作り直す必要はない。
 */
export function createDoD(
  key: string,
  seq: number,
  body: CreateTicketDoDRequest,
): Promise<TicketDoDItem> {
  return api.post<TicketDoDItem>(base(key, seq), body)
}

/**
 * 完了条件を更新する（9.9）。必要権限は `ticket.edit`。
 *
 * **`is_satisfied` を `true` にすると、サーバが `satisfied_at` と `satisfied_by`
 * （呼び出し元）を同時に設定する。`false` に戻すと両方 `null` へ戻る。**
 * 3つは別々には送れない。
 *
 * **`type` は送れない**（422 `immutable_field`）。**`If-Match` は要らない**
 * （`dod_item` は `version` 列を持たない）。
 */
export function updateDoD(
  key: string,
  seq: number,
  id: string,
  body: PatchTicketDoDRequest,
): Promise<TicketDoDItem> {
  return api.patch<TicketDoDItem>(`${base(key, seq)}/${encodeURIComponent(id)}`, body)
}

/** 完了条件を消す（9.9）。`204`。物理削除で戻せない */
export function deleteDoD(key: string, seq: number, id: string): Promise<void> {
  return api.del<void>(`${base(key, seq)}/${encodeURIComponent(id)}`)
}
