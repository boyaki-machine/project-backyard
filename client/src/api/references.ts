/**
 * チケットの外部参照のエンドポイント（`ApiDesign.md` 9.10.2）。手順17c。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 *
 * **一覧を呼ぶ画面は無い。** 詳細応答（9.5.1）の `references` に同じものが
 * 入っているので、チケット詳細は `GET /tickets/:seq` 1本で足りる
 * （`GuiDesign.md` 5.5、`ApiDesign.md` 8章の「起動時1〜2本」）。
 * `listReferences` を置いてあるのは、**追加・削除のあとに手元の配列を
 * 取り直したくなったときの逃げ道**としてで、既定の経路ではない。
 *
 * **`kind='code'` の追加は画面に無い**（5.5）。この欄はエージェントの作業記録で
 * あり、人が手で書くものではない。Phase 1 の書き手は `/me/tokens` で発行した
 * API トークンを持つクライアントである。
 */
import { api } from './client'
import type { components } from './schema'
import { uiText } from '../locales/ui'

export type TicketReference = components['schemas']['TicketReference']
export type TicketReferenceList = components['schemas']['TicketReferenceList']
export type CreateTicketReferenceRequest =
  components['schemas']['CreateTicketReferenceRequest']
export type PatchTicketReferenceRequest =
  components['schemas']['PatchTicketReferenceRequest']

export type ReferenceKind = TicketReference['kind']

/** 見出しに出す名前（`GuiDesign.md` 5.5）。 */
export const referenceSectionLabels: Record<ReferenceKind, string> = {
  get code() { return uiText('コード') },
  get doc() { return uiText('参考リンク') },
}

function base(key: string, seq: number): string {
  return `/projects/${encodeURIComponent(key)}/tickets/${seq}/references`
}

/**
 * 外部参照の一覧（9.10.2）。`kind` 昇順、同じ `kind` の中は
 * `sort_order` → `created_at` の昇順で返る。
 */
export function listReferences(key: string, seq: number): Promise<TicketReferenceList> {
  return api.get<TicketReferenceList>(base(key, seq))
}

/**
 * 外部参照を1件足す（9.10.2）。`sort_order` を省くと末尾に置かれる。
 *
 * **親チケットの `version` は動かない**ので、呼び出し側が持ち回っている
 * `version` を作り直す必要はない。
 */
export function createReference(
  key: string,
  seq: number,
  body: CreateTicketReferenceRequest,
): Promise<TicketReference> {
  return api.post<TicketReference>(base(key, seq), body)
}

/**
 * 外部参照を更新する（9.10.2）。**`kind` は送れない**（422 になる）。
 *
 * `null` は「その項目を空にする」を意味する。ただし `kind` ごとの必須項目
 * （`code` の `repository`、`doc` の `url`）は空にできない。
 */
export function updateReference(
  key: string,
  seq: number,
  id: string,
  body: PatchTicketReferenceRequest,
): Promise<TicketReference> {
  return api.patch<TicketReference>(`${base(key, seq)}/${encodeURIComponent(id)}`, body)
}

/** 外部参照を消す（9.10.2）。`204`。 */
export function deleteReference(key: string, seq: number, id: string): Promise<void> {
  return api.del<void>(`${base(key, seq)}/${encodeURIComponent(id)}`)
}

/**
 * コードの行に出す表記（`GuiDesign.md` 5.5）。
 *
 * `my-app : pb/31 : a1b2c3d` の形で、**欠けている要素は詰める**。サーバが
 * `activity` に載せる要約（`ApiDesign.md` 9.10.2）と同じ形にしてあるので、
 * 手順19 の履歴と画面で同じ文字列が読める。
 */
export function codeSummary(ref: TicketReference): string {
  const parts = [ref.repository, ref.branch, ref.commit_sha].filter(
    (p): p is string => p != null && p !== '',
  )
  return parts.length > 0 ? parts.join(' : ') : uiText('(コード)')
}

/**
 * 参考リンクの行に出す表記（`GuiDesign.md` 5.5）。`label`、無ければ `url`。
 */
export function docSummary(ref: TicketReference): string {
  if (ref.label != null && ref.label !== '') return ref.label
  return ref.url ?? uiText('(参考リンク)')
}
