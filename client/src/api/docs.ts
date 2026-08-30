/**
 * プロジェクト文書のエンドポイント（`ApiDesign.md` 10章）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 *
 * **読みと書きで必要権限が違う。** 目次と本文は `doc.view`、作成・更新・削除は
 * **`doc.edit`** で、**`doc.edit` は `operator` と `project_member` が持たない**
 * （`DbDesign.md` 8.1.4）。画面は `doc.edit` の有無でボタンを出し分けるが、
 * それは利便性であって境界ではない——同じ判定をサーバが必ず行う（7.3）。
 *
 * **文書はパスで指す**（10.1）。`slug` を根から連ねたもので、`vision` や
 * `rules/naming` になる。ULID も返るが指定には使わない——共有できる URL になり、
 * 画面の URL（`/p/:key/docs/rules/naming`）とそのまま一致する。
 */
import { api } from './client'
import type { components } from './schema'

export type DocTreeList = components['schemas']['DocTreeList']
export type DocTreeItem = components['schemas']['DocTreeItem']
export type DocOutlineItem = components['schemas']['DocOutlineItem']
export type Doc = components['schemas']['Doc']
export type DocSection = components['schemas']['DocSection']
export type CreateDocRequest = components['schemas']['CreateDocRequest']
export type PatchDocRequest = components['schemas']['PatchDocRequest']

/**
 * パスを URL へ埋める。
 *
 * **セグメントごとに符号化する。** 全体を `encodeURIComponent` に通すと区切りの
 * `/` まで `%2F` になり、サーバのワイルドカード（`/docs/*`）が1階層としてしか
 * 読めなくなる。`slug` の CHECK は `^[a-z0-9][a-z0-9-]{0,63}$` なので実際には
 * 変換される文字が無いが、**符号化を省くと「変換が要らない」が前提として
 * 埋まる**——`slug` の規則が緩んだときに静かに壊れる。
 */
function docPath(key: string, path: string): string {
  const segments = path.split('/').map(encodeURIComponent).join('/')
  return `/projects/${encodeURIComponent(key)}/docs/${segments}`
}

/**
 * 目次（10.2）。**`body_md` を含まない。**
 *
 * **ページネーションを持たない**——目次は木であり、途中で切ると子が親から外れる。
 * `items[]` は各階層で `sort_order` 昇順、同値は `slug` 昇順で返る。
 *
 * `outline` を真にすると各文書の見出し一覧が付く（`?outline=1`）。**これは
 * エージェントが「どの章を読むか」を決めるための情報**であり（10.2）、
 * Docs 画面は使わない。
 */
export function listDocs(key: string, options?: { outline?: boolean }): Promise<DocTreeList> {
  const query = options?.outline === true ? '?outline=1' : ''
  return api.get<DocTreeList>(`/projects/${encodeURIComponent(key)}/docs${query}`)
}

/**
 * 本文1件（10.3）。
 *
 * **`created_by` / `updated_by` は `null` になりうる**（`ON DELETE SET NULL`）。
 * コメントの `author` と違い、**文書は書いた人が消えても内容が生き続ける。**
 */
export function getDoc(key: string, path: string): Promise<Doc> {
  return api.get<Doc>(docPath(key, path))
}

/**
 * 文書を1件作る（10.4）。**`revision_no = 1` がサーバ側で同時に作られる**ので、
 * 最初の編集の前に「作ったときの本文」が版として残る。
 *
 * `parent_path` を省くとトップレベル、`sort_order` を省くと同じ親の末尾に入る。
 * 同じ親の下で `slug` が重複すると `409 already_exists`。
 */
export function createDoc(key: string, body: CreateDocRequest): Promise<Doc> {
  return api.post<Doc>(`/projects/${encodeURIComponent(key)}/docs`, body)
}

/**
 * 文書を部分更新する（10.4）。
 *
 * **`If-Match` は必須である。** 省略すると 422、食い違えば `409 conflict` になる。
 * **人とエージェントが同じ文書を触るため、Phase 1 のプロジェクト設定より競合が
 * 起きやすい**——呼び出し側は 409 を必ず扱うこと（`GuiDesign.md` 5.10「競合したとき」）。
 *
 * `title` か `body_md` が実際に変わったときだけリビジョンが1行積まれる。
 * `sort_order` だけの更新では積まれず、**`change_reason` を送っても捨てられる**
 * （422 にはしない）。
 */
export function updateDoc(
  key: string,
  path: string,
  version: number,
  body: PatchDocRequest,
): Promise<Doc> {
  return api.patch<Doc>(docPath(key, path), body, {
    headers: { 'If-Match': `"${version}"` },
  })
}

/**
 * 文書を消す（10.4）。**物理削除で、部分木ごと消える**（`parent_id` の `CASCADE`）。
 * `document_revision` も一緒に消える。
 *
 * **API は子を持つ文書の削除を止めない。** 件数を示して確認するのは画面の役目である
 * （`GuiDesign.md` 6.3）。
 */
export function deleteDoc(key: string, path: string): Promise<void> {
  return api.del<void>(docPath(key, path))
}
