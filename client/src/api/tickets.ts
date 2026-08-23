/**
 * チケットのエンドポイント（`ApiDesign.md` 9.2 / 9.3 / 9.4）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 *
 * **Phase 1 の消費者はバックログ画面ひとつ**（`GuiDesign.md` 5.4）。カンバン・
 * ガント（Phase 2）も 9.2 の同じエンドポイントから描くので、フィルタの組み立てを
 * 画面に書かず、ここに集める。
 */
import { api } from './client'
import type { components } from './schema'

export type Ticket = components['schemas']['Ticket']
export type TicketList = components['schemas']['TicketList']
export type TicketDetail = components['schemas']['TicketDetail']
export type TicketStatus = components['schemas']['TicketStatus']
export type TicketTagRef = components['schemas']['TicketTagRef']
export type ActorRef = components['schemas']['ActorRef']
export type CreateTicketRequest = components['schemas']['CreateTicketRequest']
export type MoveTicketRequest = components['schemas']['MoveTicketRequest']
export type MoveTicketResult = components['schemas']['MoveTicketResult']

export type TicketType = Ticket['type']
export type TicketPriority = NonNullable<Ticket['priority']>
export type StatusCategory = TicketStatus['category']

/**
 * ソートできる列（9.2.1）。**担当が無い**——`GuiDesign.md` 5.4 の「ソート」に明記。
 *
 * `priority` と `status` は意味の順で並ぶ（`lowest`→`highest`、ワークフローの
 * `sort_order`）。キーの辞書順ではない。
 */
export type TicketSort =
  | 'sort_key'
  | 'seq'
  | 'title'
  | 'status'
  | 'priority'
  | 'due_date'
  | 'created_at'
  | 'updated_at'

export type SortOrder = 'asc' | 'desc'

/**
 * 種別のアイコン（`GuiDesign.md` 5.4）。**色を使わず形状で区別する**（8.6）。
 *
 * **種別は3つだけである**（`DbDesign.md` 6.6。手順16d で `bug` / `phase` / `wbs`
 * を廃止した）。使い分けの定義がどの設計文書にも無く、実機で選べなかったため。
 * **バグは種別ではなくタグで表す**（6.10）。
 */
export const ticketTypeIcons: Record<TicketType, string> = {
  epic: '⚑',
  story: '▤',
  task: '☑',
}

/** 種別の表示名。サーバはキーしか返さないので、日本語は画面が持つ */
export const ticketTypeLabels: Record<TicketType, string> = {
  epic: 'エピック',
  story: 'ストーリー',
  task: 'タスク',
}

/**
 * バックログの行として出す種別（`GuiDesign.md` 5.4）。
 *
 * **エピックを含まない。** エピックはグルーピング専用で、行として並ぶと
 * 「やるべき仕事」の数に混ざるため、複数選択できるフィルタになる。
 * **一覧を取るときこの値を `type` に送る**——画面側で捨てると、下部に出す
 * 総件数（サーバが返す `total`）と食い違う。
 */
export const backlogTicketTypes: TicketType[] = ['story', 'task']

/**
 * 優先度の記号（`GuiDesign.md` 8.7）。**色を使わず記号のみ。**
 *
 * **`medium` は無表示。** 一覧上で目に入るのが高いものと低いものだけになり、
 * 走査の負荷が下がる（8.7）。
 */
export const priorityMarks: Record<TicketPriority, string> = {
  highest: '▲▲',
  high: '▲',
  medium: '',
  low: '▽',
  lowest: '▽▽',
}

export const priorityLabels: Record<TicketPriority, string> = {
  highest: '最高',
  high: '高',
  medium: '中',
  low: '低',
  lowest: '最低',
}

/** 優先度の並び（低い順）。フィルタの選択肢を作るのに使う */
export const priorityOrder: TicketPriority[] = ['lowest', 'low', 'medium', 'high', 'highest']

/**
 * ステータスバッジの記号（`GuiDesign.md` 8.7）。
 *
 * **`category` で決まる**——ワークフローのキーはプロジェクトごとに違うが、
 * この4値はプロジェクトを跨いでも意味が変わらない（`ApiDesign.md` 9.2.1）。
 */
export const statusMarks: Record<StatusCategory, string> = {
  todo: '○',
  in_progress: '●',
  review: '◐',
  done: '✓',
}

/**
 * 一覧のクエリ（9.2.1）。
 *
 * **複数指定はカンマ区切りで OR、異なる種類どうしは AND**。値の組み立ては
 * 呼び出し側（バックログ画面）が URL のクエリから作る。
 */
export interface ListTicketsQuery {
  status?: string
  status_category?: string
  type?: string
  assignee?: string
  priority?: string
  tag?: string
  sprint?: string
  open?: 'true' | 'false'
  due_within?: string
  parent?: number
  sort?: TicketSort
  order?: SortOrder
  page?: number
  per_page?: number
}

/**
 * チケット一覧（9.2）。必要権限は `ticket.view`。
 *
 * **既定が他の一覧と2か所ちがう**——`per_page` は 200、`sort` は `sort_key`。
 * どちらもサーバが持つので、未指定の項目は送らない。
 *
 * **ページャを持たない画面向けである**（9.2.3）。`total > per_page` になっても
 * エラーにはならず、200件で打ち切られる。件数の食い違いは画面が文言で伝える。
 */
export function listTickets(key: string, query: ListTicketsQuery = {}): Promise<TicketList> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== '') params.set(k, String(v))
  }
  const qs = params.toString()
  return api.get<TicketList>(
    `/projects/${encodeURIComponent(key)}/tickets${qs === '' ? '' : `?${qs}`}`,
  )
}

/**
 * チケットを作る（9.3）。必要権限は `ticket.create`。
 *
 * **`status_key` / `sort_key` / `seq` / `reporter_id` / `version` は送れない**
 * ——いずれもサーバが決める。応答は詳細（9.5.1）と同じ形式で返る。
 */
export function createTicket(key: string, body: CreateTicketRequest): Promise<TicketDetail> {
  return api.post<TicketDetail>(`/projects/${encodeURIComponent(key)}/tickets`, body)
}

/**
 * 並べ替える（9.4）。必要権限は `ticket.edit`。
 *
 * **`sort_key` はサーバが作る。** クライアントに LexoRank の桁生成規則を
 * 持たせない——Web・MCP・将来の CLI がそれぞれ実装すると、1つでもずれた
 * 時点で順序が壊れる。ここは「どの行の隣か」だけを送る。
 *
 * **応答の `rebalanced` が `true` なら一覧を取り直すこと。** プロジェクト全体の
 * `sort_key` が振り直されており、手元の値がすべて古くなっている。
 */
export function moveTicket(
  key: string,
  seq: number,
  body: MoveTicketRequest,
): Promise<MoveTicketResult> {
  return api.post<MoveTicketResult>(
    `/projects/${encodeURIComponent(key)}/tickets/${seq}/move`,
    body,
  )
}
