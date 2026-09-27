/**
 * チケットのエンドポイント（`ApiDesign.md` 9.2 〜 9.7）。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 *
 * **消費者はバックログ画面と、その右に開く詳細ペイン**
 * （`GuiDesign.md` 5.4 / 5.5）。カンバン・ガント（未実装）も 9.2 の同じ
 * エンドポイントから描くので、フィルタの組み立てを画面に書かず、ここに集める。
 *
 * **記号と表示名（`ticketTypeIcons` / `priorityMarks` / `statusMarks` /
 * `*Labels`）もここが正本である。** 画面ごとに書き写さない——一覧と詳細で
 * 同じチケットに違う記号が出ると、同じものだと読めなくなる。
 */
import { api } from './client'
import type { components } from './schema'
import { uiText } from '../locales/ui'

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
/** 実行モード（`DbDesign.md` 6.6 の `CHECK` が持つ3値）。**`null` を取らない** */
export type TicketExecutionMode = TicketDetail['execution_mode']
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
  | 'due_at'
  | 'created_at'
  | 'updated_at'
  /** 完了日時。チケット検索の「完了日」の列。未完了は昇順・降順とも末尾 */
  | 'closed_at'

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
  get epic() { return uiText('エピック') },
  get story() { return uiText('ストーリー') },
  get task() { return uiText('タスク') },
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
 * 新規チケットの説明欄に入れる初期値（`GuiDesign.md` 5.4.3）。
 *
 * **`placeholder` ではなく本文の初期値である。** placeholder は1文字打つと
 * 消えるため、**書かせたい項目には機能しない**（見出しごと消える）。本文に
 * 入れておけば、書きたい欄から埋めて残りを消せる。
 *
 * **プロジェクトごとに変えられるようにはしない**（同 5.4.3。`project.settings`
 * に置く案は見送った）。必要になってから決める。
 */
export function newTicketBodyTemplate(): string {
  return uiText('# 概要\n\n# ゴール条件\n\n# 制約条件\n')
}

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

/**
 * 実行モードの表示名（`GuiDesign.md` 5.5「実行モード」）。
 *
 * **サーバと同じ語を使う**——`server/internal/mcp/context_pack.go` の
 * `executionModeLabels` が同じ3語をコンテキストパックに出している。
 * **語が違うと、人が「エージェントに見えているもの」を確かめられない。**
 */
export const executionModeLabels: Record<TicketExecutionMode, string> = {
  get human_only() { return uiText('人が行う') },
  get agent_draft() { return uiText('エージェントが下書きし、人が仕上げる') },
  get agent_only() { return uiText('エージェントに任せてよい') },
}

/**
 * 選択肢の並び。**弱いほうから強いほうへ**（人だけ → 下書き → 任せる）並べる。
 * `DbDesign.md` 6.6 の `CHECK` が持つ3値がすべてである。
 */
export const executionModeOptions: TicketExecutionMode[] = [
  'human_only',
  'agent_draft',
  'agent_only',
]

/** Readiness（`DbDesign.md` 6.6 の `CHECK` が持つ3値）。未判定は `null` で、ここには含めない */
export type TicketReadiness = NonNullable<TicketDetail['readiness']>

/**
 * Readiness の表示名（`GuiDesign.md` 5.5「Readiness」）。
 *
 * **サーバと同じ語を使う**——`server/internal/mcp/context_pack.go` の
 * `readinessLabels` が同じ3語をコンテキストパックに出している（実行モードと同じ理由）。
 */
export const readinessLabels: Record<TicketReadiness, string> = {
  get red() { return uiText('赤（前提が足りていない）') },
  get yellow() { return uiText('黄（不明点が残っている）') },
  get green() { return uiText('緑（着手してよい）') },
}

/** 選択肢の並び。**止まれ → 注意 → 進め**。未判定（`null`）は画面が先頭に足す */
export const readinessOptions: TicketReadiness[] = ['red', 'yellow', 'green']

/**
 * スコープ境界の既知のキー（`ApiDesign.md` 9.5.2、`GuiDesign.md` 5.5「スコープ境界」）。
 *
 * **並びと語はサーバと同じ**——`context_pack.go` の `scopeKeyLabels` がこの順で
 * パックの「1. スコープ境界と制約」に出している。
 */
export const scopeKeys = [
  { key: 'allow', get label() { return uiText('触ってよい範囲') } },
  { key: 'deny', get label() { return uiText('触ってはいけない範囲') } },
  { key: 'repositories', get label() { return uiText('リポジトリ') } },
  { key: 'external_apis', get label() { return uiText('外部API') } },
] as const

export type ScopeKey = (typeof scopeKeys)[number]['key']

type TicketScope = TicketDetail['scope']

/** `scope` の1キーを、1行1件の文字列にする。文字列の配列でなければ空 */
export function scopeText(scope: TicketScope, key: ScopeKey): string {
  const v = scope[key]
  if (!Array.isArray(v)) return ''
  return v.filter((s): s is string => typeof s === 'string').join('\n')
}

/**
 * 1キーだけを、1行1件の文字列で差し替えた**新しい** `scope` を返す。
 *
 * **知らないキーを落とさない**（5.5）。`PATCH` は `scope` を丸ごと置き換えるので、
 * 受け取ったものを写してから差し替える。各行の前後の空白を落とし、空の行は捨てる。
 * **全行が空ならキーごと消す**——`[]` を残さない（未設定は `{}`。9.5.2）。
 */
export function withScopeLines(scope: TicketScope, key: ScopeKey, text: string): TicketScope {
  const lines = text
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s !== '')
  const next: TicketScope = { ...scope }
  if (lines.length === 0) delete next[key]
  else next[key] = lines
  return next
}

/** 画面が知らないキーと、その値の JSON（読み取り専用で出す。5.5） */
export function unknownScopeEntries(scope: TicketScope): [string, string][] {
  const known: readonly string[] = scopeKeys.map((s) => s.key)
  return Object.entries(scope)
    .filter(([k]) => !known.includes(k))
    .map(([k, v]) => [k, JSON.stringify(v)])
}

export const priorityLabels: Record<TicketPriority, string> = {
  get highest() { return uiText('最高') },
  get high() { return uiText('高') },
  get medium() { return uiText('中') },
  get low() { return uiText('低') },
  get lowest() { return uiText('最低') },
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
 * ステータス区分の表示名（`ApiDesign.md` 9.2.1 の `status_category`）。
 *
 * **ワークフローのステータス名とは別物である。** こちらは4値の固定で、
 * プロジェクトのワークフローが何であっても意味が変わらない（9.2.1）。
 * ダッシュボードの集計カード（`GuiDesign.md` 5.3）とバックログの状態フィルタ
 * の「区分」群（同 5.4）が使う。
 *
 * `simple` / `with_review` テンプレートでは**ステータス名と同じ語になる**が、
 * `with_approval` は `review` 区分に「レビュー中」と「承認待ち」の2つを持つ
 * （`DbDesign.md` 7.4）。同じ語だからといって同じものではない。
 */
export const statusCategoryLabels: Record<StatusCategory, string> = {
  get todo() { return uiText('未着手') },
  get in_progress() { return uiText('進行中') },
  get review() { return uiText('レビュー中') },
  get done() { return uiText('完了') },
}

/** 集計カードとフィルタで使う区分の並び（9.13.1 の `by_category` と同じ順） */
export const statusCategoryOrder: StatusCategory[] = ['todo', 'in_progress', 'review', 'done']

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
  /** `true` で**期限を過ぎた未完了のもの**（9.2.1。手順19b）。`false` は受け付けない */
  overdue?: 'true'
  /** 予定期間の境界（YYYY-MM-DD、両端を含む）。チケットの予定との重なりで絞る */
  planned_from?: string
  planned_to?: string
  /** `14d` 形式。**その日数より前から更新されていない未完了のもの**（9.2.1。手順19b） */
  stale?: string
  /**
   * 部分木で絞る（9.2.1）。**カンマ区切りで複数指定は OR** なので数値ではなく
   * 文字列である。バックログのエピックフィルタがこれを使う（`GuiDesign.md` 5.4）。
   */
  parent?: string
  /**
   * 棚に戻ったものも返すか（9.2.1）。既定は返さない。
   *
   * **スプリントを終えて消化し終えたものは、既定で一覧から外れる。**
   * バックログは状態フィルタで完了を明示的に選んだときだけ `'true'` を送り
   * （`GuiDesign.md` 5.4）、**チケット検索は常に送る**（5.13）。
   */
  retired?: 'true'
  /** キーワード（9.2.1「検索の条件」）。空白で区切った語をすべて含むもの */
  q?: string
  /** バックログは番号・タイトル・祖先エピック・タグを検索し、祖先を補完する */
  search_mode?: 'fulltext' | 'backlog'
  /** 番号の範囲（両端を含む）。**URL のクエリの値をそのまま渡す**ので文字列で持つ */
  seq_from?: string
  seq_to?: string
  /**
   * 着手した日時・完了した日時の範囲（ISO8601。`since` 以上・`before` 未満）。
   * **日の境界は画面が利用者のタイムゾーンで作る**（`datetime.ts` の `startOfDayInstant`）
   */
  started_since?: string
  started_before?: string
  closed_since?: string
  closed_before?: string
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

// ── チケット1件（`ApiDesign.md` 9.5 / 9.6 / 9.7。手順17b で追加）─────

export type TicketBrief = components['schemas']['TicketBrief']
export type TicketChild = components['schemas']['TicketChild']
export type UpdateTicketRequest = components['schemas']['UpdateTicketRequest']
export type TransitionTicketRequest = components['schemas']['TransitionTicketRequest']
export type TicketTransitionList = components['schemas']['TicketTransitionList']
export type TicketTransitionOption = components['schemas']['TicketTransitionOption']

/**
 * チケット1件を取る（9.5.1）。必要権限は `ticket.view`。
 *
 * **9.2 の `items[]` に6項目を加えたもの**（`body_md` / `parent` / `children` /
 * `dod` / `links` / `comment_count`）。**遷移先の一覧は含まれない**——
 * ドロップダウンを開いたときに `listTransitions` を別に呼ぶ（9.7）。
 */
export function getTicket(key: string, seq: number): Promise<TicketDetail> {
  return api.get<TicketDetail>(`/projects/${encodeURIComponent(key)}/tickets/${seq}`)
}

/**
 * 部分更新（9.5.2）。必要権限は `ticket.edit`（`assignee_id` を変えるなら `ticket.assign` も）。
 *
 * **`If-Match` は必須である。** 省略すると 422 で、食い違えば 409。成功すると
 * `version` が +1 されるので、**応答の `version` を次の更新に使い回す**
 * （`GuiDesign.md` 5.5「編集の単位」。項目ごとに個別に送るため）。
 *
 * **`null` は「その項目を空にする」**であり、キーごと送らないのとは区別される。
 * 担当を外す・親を外す・期限を消すはこの形でしか表せない。
 *
 * **送れないものが3系統ある**（`details[].code`）——`immutable_field`
 * （サーバが決めるもの）、`use_move_endpoint`（`sort_key` / `staged_at` → 9.4）、
 * `use_transition_endpoint`（`status_key` / `closed_at` → 9.6）。
 */
export function updateTicket(
  key: string,
  seq: number,
  version: number,
  body: UpdateTicketRequest,
): Promise<TicketDetail> {
  return api.patch<TicketDetail>(`/projects/${encodeURIComponent(key)}/tickets/${seq}`, body, {
    headers: { 'If-Match': `"${version}"` },
  })
}

/**
 * 削除（9.5.3）。必要権限は `ticket.delete`。`204 No Content`。
 *
 * **子チケットは消えない。** `ticket.parent_id` は `ON DELETE SET NULL` なので、
 * 子は親を失ってトップレベルへ上がる。**この挙動を確認ダイアログに件数付きで
 * 明示する**（`GuiDesign.md` 6.3）。
 */
export function deleteTicket(key: string, seq: number): Promise<void> {
  return api.del<void>(`/projects/${encodeURIComponent(key)}/tickets/${seq}`)
}

/**
 * ステータスを遷移させる（9.6）。必要権限は `ticket.transition`
 * （加えて `workflow_transition.required_permission` があればそれも）。
 *
 * **`If-Match` は要求しない**（9.6）。遷移そのものが競合を検出するためで、
 * 2人が同時に同じ遷移を実行すると後発は「レビュー → レビュー」を要求することに
 * なり、定義が無いので 409 `invalid_transition` になる。
 *
 * **`comment` は手順17b では送らない**（`GuiDesign.md` 5.5）。投稿したコメントを
 * 表示する場所が手順18 まで無く、**送ったのに見えない**状態になる。
 *
 * 応答は 9.5.1 と同形式で、`version` は +1 される。
 */
export function transitionTicket(
  key: string,
  seq: number,
  body: TransitionTicketRequest,
): Promise<TicketDetail> {
  return api.post<TicketDetail>(
    `/projects/${encodeURIComponent(key)}/tickets/${seq}/transition`,
    body,
  )
}

/**
 * 遷移先の候補（9.7）。必要権限は `ticket.view`。
 *
 * **`items[]` はワークフローの全ステータス（現在のものを除く）である。**
 * 遷移できない先も `allowed: false` と `reason` を付けて返る——ステータスを
 * 存在ごと隠すと「なぜ完了にできないのか」が分からなくなるため。
 * `reason` は**そのまま画面に出せる日本語**（2.5）。
 *
 * **ドロップダウンを開いたときに呼ぶ**（9.7。詳細応答には入っていない）。
 */
export function listTransitions(key: string, seq: number): Promise<TicketTransitionList> {
  return api.get<TicketTransitionList>(
    `/projects/${encodeURIComponent(key)}/tickets/${seq}/transitions`,
  )
}
