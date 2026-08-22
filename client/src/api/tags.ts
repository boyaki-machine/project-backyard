/**
 * タグのエンドポイント（`ApiDesign.md` 9.11）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う（`roles.ts` と同じ方針）。
 *
 * **読みと書きで必要権限が違う。** 一覧は `ticket.view`、定義の変更は
 * `project.edit` である。呼び出す画面（プロジェクト設定のタグタブ、
 * `GuiDesign.md` 5.9.4）は `project.edit` を要するので両方を満たすが、
 * 手順16b のバックログは一覧だけを読む。
 */
import { api } from './client'
import type { components } from './schema'

export type Tag = components['schemas']['Tag']
export type TagList = components['schemas']['TagList']
export type CreateTagRequest = components['schemas']['CreateTagRequest']
export type PatchTagRequest = components['schemas']['PatchTagRequest']

/**
 * プロジェクトのタグ一覧（9.11）。
 *
 * **ページャを持たない。** バックログのグループ化が全件を必要とするため
 * （`GuiDesign.md` 5.4.1）。`sort_order` 昇順・同値は `name` 昇順で返る。
 */
export function listTags(key: string): Promise<TagList> {
  return api.get<TagList>(`/projects/${encodeURIComponent(key)}/tags`)
}

/** タグを作る（9.11）。`sort_order` を省くと末尾に置かれる。 */
export function createTag(key: string, body: CreateTagRequest): Promise<Tag> {
  return api.post<Tag>(`/projects/${encodeURIComponent(key)}/tags`, body)
}

/**
 * タグを更新する（9.11）。**並べ替えもこれで表現する**（9.11.1）。
 *
 * `If-Match` は要らない（`tag` は `version` 列を持たない。2.8）。
 */
export function updateTag(key: string, id: string, body: PatchTagRequest): Promise<Tag> {
  return api.patch<Tag>(`/projects/${encodeURIComponent(key)}/tags/${encodeURIComponent(id)}`, body)
}

/**
 * タグを消す（9.11）。**使用中でも消せる**——`ticket_tag` が `CASCADE` で
 * 消えるだけで、チケットは残る。確認は画面側が出す（`GuiDesign.md` 6.3）。
 */
export function deleteTag(key: string, id: string): Promise<void> {
  return api.del<void>(`/projects/${encodeURIComponent(key)}/tags/${encodeURIComponent(id)}`)
}

/**
 * ドラッグ&ドロップの結果を反映する（`ApiDesign.md` 9.11.1）。
 *
 * 新しい並びへ `10, 20, 30, …` を割り当て直し、**値が変わった行だけ**送る。
 * 専用の move エンドポイントは無い——`sort_order` は単なる整数で、チケットの
 * `sort_key`（LexoRank）のような生成規則を持たないためである。
 *
 * **この操作は原子的ではない。** 途中で失敗すると順序が中途半端に残るので、
 * 呼び出し側は失敗時に一覧を取り直して画面を戻すこと。
 *
 * @param ordered 並べ替え後のタグ（表示順）
 * @returns 実際に送った件数
 */
export async function reorderTags(key: string, ordered: Tag[]): Promise<number> {
  const changed = ordered
    .map((tag, index) => ({ tag, sortOrder: (index + 1) * 10 }))
    .filter(({ tag, sortOrder }) => tag.sort_order !== sortOrder)

  for (const { tag, sortOrder } of changed) {
    await updateTag(key, tag.id, { sort_order: sortOrder })
  }
  return changed.length
}
