/**
 * チケットのコメントのエンドポイント（`ApiDesign.md` 9.8）。手順18b。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 *
 * **チケットの子資源でこれだけが別の `GET` を要する。** 完了条件（9.9）・
 * 関連チケット（9.10.1）・外部参照（9.10.2）は詳細応答（9.5.1）に本体が入るが、
 * **コメントは議論の量だけ増える**ためページネーションを持ち、詳細応答には
 * 件数（`comment_count`）しか入らない。画面は起動時にこの1本を足して**2本**
 * 呼ぶ（`ApiDesign.md` 8章）。
 *
 * **`kind` の表示名はここが正本である。** サーバの `activity` が同じ語を使うので
 * （`comments.go` の `commentKindLabels`）、**画面で違う語を当てると履歴と
 * コメント欄で同じものが別の名前で出る。**
 */
import { api } from './client'
import type { components } from './schema'
import { uiText } from '../locales/ui'

export type TicketComment = components['schemas']['TicketComment']
export type TicketCommentList = components['schemas']['TicketCommentList']
export type CreateTicketCommentRequest =
  components['schemas']['CreateTicketCommentRequest']
export type PatchTicketCommentRequest =
  components['schemas']['PatchTicketCommentRequest']

export type CommentKind = TicketComment['kind']

/**
 * 情報の類型の表示名（`GuiDesign.md` 5.5、`Requirements.md` 6.5）。
 *
 * **サーバの `commentKindLabels` と同じ語である。** 履歴（手順19）を読む人が、
 * コメント欄で見たものと同じ言葉を見られるようにするため。
 */
export const commentKindLabels: Record<CommentKind, string> = {
  get discussion() { return uiText('議論') },
  get decision() { return uiText('決定') },
  get artifact() { return uiText('成果物') },
  get caveat() { return uiText('注意') },
  get reference() { return uiText('参照') },
  get progress() { return uiText('経過') },
}

/**
 * 投稿欄の選択肢に出す順（`Requirements.md` 6.5 の並び）。
 *
 * **`progress` を末尾に置く。** 9.6 の遷移コメントが自動で使う値であり、
 * 手で選ぶ機会が最も少ない。
 */
export const commentKindOptions: CommentKind[] = [
  'discussion',
  'decision',
  'artifact',
  'caveat',
  'reference',
  'progress',
]

/** 投稿時の既定（`ApiDesign.md` 9.8）。サーバ側の既定と同じ値 */
export const defaultCommentKind: CommentKind = 'discussion'

function base(key: string, seq: number): string {
  return `/projects/${encodeURIComponent(key)}/tickets/${seq}/comments`
}

export interface ListCommentsQuery {
  page?: number
  per_page?: number
  order?: 'asc' | 'desc'
}

/**
 * コメントの一覧（9.8）。必要権限は `ticket.view`。
 *
 * **画面は `order=desc` で取り、描くときに反転する**（`GuiDesign.md` 5.5）。
 * サーバの既定は `asc` だが、その1ページ目は**最も古い50件**になり、
 * 120件あると最新のコメントが3ページ目に沈む——**議論は新しいほうから
 * 読めなければならない。**
 *
 * **`total` は削除済みも数える。** 見出しに使う「読めるコメントの件数」は
 * 詳細応答（9.5.1）の `comment_count` のほうで、**同じ表を数えて違う答えを
 * 返すのは意図的である**（9.8）。
 */
export function listComments(
  key: string,
  seq: number,
  query: ListCommentsQuery = {},
): Promise<TicketCommentList> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined) params.set(k, String(v))
  }
  const qs = params.toString()
  return api.get<TicketCommentList>(`${base(key, seq)}${qs === '' ? '' : `?${qs}`}`)
}

/**
 * コメントを投稿する（9.8）。必要権限は `comment.create`。
 *
 * **`in_reply_to` の参照先は、同じチケットの削除されていないコメント**でなければ
 * ならない（422 `not_found`）。画面は削除済みの行に `[返信]` を出さない。
 *
 * **親チケットの `version` と `updated_at` は動かない**ので、呼び出し側が
 * 持ち回っている `version` を作り直す必要はない。
 */
export function createComment(
  key: string,
  seq: number,
  body: CreateTicketCommentRequest,
): Promise<TicketComment> {
  return api.post<TicketComment>(base(key, seq), body)
}

/**
 * コメントを更新する（9.8）。必要権限は `comment.edit_own` かつ自分のもの。
 *
 * **変えられるのは `body_md` と `kind` だけである。** `in_reply_to` を送ると
 * 422 `immutable_field`——返信先を後から付け替えるとスレッドの形が変わり、
 * **既に読まれた並びが崩れる。**
 *
 * **削除済みへの `PATCH` は 404。** 論理削除でも「もう無い」として扱う。
 */
export function updateComment(
  key: string,
  seq: number,
  id: string,
  body: PatchTicketCommentRequest,
): Promise<TicketComment> {
  return api.patch<TicketComment>(`${base(key, seq)}/${encodeURIComponent(id)}`, body)
}

/**
 * コメントを消す（9.8）。`204`。
 *
 * 必要権限は `comment.delete_any`、**または** `comment.edit_own` かつ自分のもの。
 *
 * **論理削除である。** 行は `items` に残り、`body_md` が `null`、`deleted_at` が
 * 入った形で返る——画面は「削除されました」と出す（`GuiDesign.md` 5.5）。
 * **二重削除は 404**（`activity` に同じ削除が2行並ばないようにするため）。
 */
export function deleteComment(key: string, seq: number, id: string): Promise<void> {
  return api.del<void>(`${base(key, seq)}/${encodeURIComponent(id)}`)
}
