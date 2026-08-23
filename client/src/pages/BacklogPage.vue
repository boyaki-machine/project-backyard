<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import EmptyState from '../components/EmptyState.vue'
import NewTicketModal from '../components/NewTicketModal.vue'
import type { NewTicketDefaults } from '../components/NewTicketModal.vue'
import PageHeader from '../components/PageHeader.vue'
import { ApiError } from '../api/client'
import * as sprintsApi from '../api/sprints'
import type { Sprint } from '../api/sprints'
import * as tagsApi from '../api/tags'
import type { Tag } from '../api/tags'
import * as ticketsApi from '../api/tickets'
import {
  priorityLabels,
  priorityMarks,
  priorityOrder,
  statusMarks,
  ticketTypeIcons,
  ticketTypeLabels,
} from '../api/tickets'
import type {
  CreateTicketRequest,
  Ticket,
  TicketPriority,
  TicketSort,
  TicketType,
  SortOrder,
} from '../api/tickets'
import { formatPlainDate, todayPlainDate } from '../lib/datetime'
import { useAuthStore } from '../stores/auth'
import { useProjectStore } from '../stores/project'

/**
 * バックログ（`GuiDesign.md` 5.4）。
 *
 * チケットを見る5つの視点（4.1.1）のうち Phase 1 で実装する唯一のもので、
 * **全体を並び順・階層・グループで見て、次に何をやるかを決める**画面である。
 *
 * **ページャを持たない**（5.4.2）。グループ化・階層のインデント・D&D の
 * 並べ替えがいずれもページ境界をまたげないためで、フィルタ後の全件（上限
 * 200件）を1回で取り切る。超えた分は件数の隣で伝える。
 *
 * **状態はすべて URL のクエリに置く**（5.4「フィルタとグループ化の保持」）。
 * ストアを持たないのは、リロードと共有で同じ画面が再現できることが要件
 * だからである。パラメータ名は `ApiDesign.md` 9.2.1 と同じにし、グループ化の
 * 軸だけ `group` を足す。
 */
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const projectStore = useProjectStore()

const projectKey = computed(() => {
  const key = route.params.key
  return typeof key === 'string' ? key : ''
})

const canCreate = computed(() => auth.canInProject(projectKey.value, 'ticket.create'))
const canEdit = computed(() => auth.canInProject(projectKey.value, 'ticket.edit'))

// ── URL クエリ ───────────────────────────────────────────────

/** グループ化の軸（5.4.1）。空文字が「なし」で、これが既定 */
type GroupAxis = '' | 'parent' | 'tag' | 'sprint' | 'assignee' | 'status'

const GROUP_AXES: GroupAxis[] = ['', 'parent', 'tag', 'sprint', 'assignee', 'status']

const groupLabels: Record<GroupAxis, string> = {
  '': 'なし',
  parent: '親チケット',
  tag: 'タグ',
  sprint: 'スプリント',
  assignee: '担当',
  status: '状態',
}

const SORTS: TicketSort[] = [
  'sort_key',
  'seq',
  'title',
  'status',
  'priority',
  'due_date',
  'created_at',
  'updated_at',
]

/** フィルタのキー。**API のクエリ名をそのまま使う**（5.4） */
const FILTER_KEYS = ['status', 'type', 'assignee', 'priority', 'tag', 'sprint'] as const
type FilterKey = (typeof FILTER_KEYS)[number]

function queryValue(name: string): string {
  const v = route.query[name]
  return typeof v === 'string' ? v : ''
}

/**
 * フィルタの現在値。**それぞれ単一選択**である。
 *
 * `ApiDesign.md` 9.2.1 はカンマ区切りの OR を受け付けるが、5.4 のワイヤーの
 * フィルタ行は `[すべて ▾]` の単一ドロップダウンで、複数選択のUIを持たない。
 * 必要になったらここを配列にすれば API 側の変更は要らない。
 */
const filters = computed<Record<FilterKey, string>>(() => ({
  status: queryValue('status'),
  type: queryValue('type'),
  assignee: queryValue('assignee'),
  priority: queryValue('priority'),
  tag: queryValue('tag'),
  sprint: queryValue('sprint'),
}))

const sort = computed<TicketSort>(() => {
  const v = queryValue('sort') as TicketSort
  return SORTS.includes(v) ? v : 'sort_key'
})

const order = computed<SortOrder>(() => (queryValue('order') === 'desc' ? 'desc' : 'asc'))

const group = computed<GroupAxis>(() => {
  const v = queryValue('group') as GroupAxis
  return GROUP_AXES.includes(v) ? v : ''
})

/** 素の状態か。`[解除]` を出すかどうかの判定に使う */
const isPristine = computed(
  () =>
    FILTER_KEYS.every((k) => filters.value[k] === '') &&
    group.value === '' &&
    sort.value === 'sort_key' &&
    order.value === 'asc',
)

/**
 * クエリを書き換える。**空の値はキーごと落とす**——URL に `?status=` が
 * 並ぶと、共有されたリンクの読み取りが難しくなる。
 *
 * `replace` を使うのは、フィルタの操作1つずつが履歴に積まれると
 * 「戻る」でバックログから出られなくなるためである。
 */
function setQuery(patch: Record<string, string>): void {
  const next: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...route.query, ...patch })) {
    if (typeof v === 'string' && v !== '') next[k] = v
  }
  void router.replace({ path: route.path, query: next })
}

/** `[解除]`：フィルタ・グループ化・ソートを素の状態へ戻す（5.4） */
function clearAll(): void {
  void router.replace({ path: route.path })
}

/**
 * 列ヘッダのソート。同じ列なら向きを反転する。
 *
 * 列を切り替えたときの初期の向きは、文字列の列は昇順、数値・日時の列は
 * 降順から始める（`ProjectsPage` と同じ規則）。**`sort_key` だけは常に昇順**
 * ——人が手で並べた順であり、逆順に意味が無い。
 */
function sortBy(next: TicketSort): void {
  if (next === 'sort_key') {
    setQuery({ sort: '', order: '' })
    return
  }
  if (sort.value === next) {
    setQuery({ sort: next, order: order.value === 'asc' ? 'desc' : 'asc' })
    return
  }
  const desc = next === 'created_at' || next === 'updated_at'
  setQuery({ sort: next, order: desc ? 'desc' : 'asc' })
}

function ariaSort(col: TicketSort): 'ascending' | 'descending' | 'none' {
  if (sort.value !== col) return 'none'
  return order.value === 'asc' ? 'ascending' : 'descending'
}

// ── データ ───────────────────────────────────────────────────

const tickets = ref<Ticket[]>([])
const total = ref(0)
const perPage = ref(200)
const loading = ref(false)
const loaded = ref(false)
const error = ref<ApiError | null>(null)

const tags = ref<Tag[]>([])
const sprints = ref<Sprint[]>([])

/** 操作の結果は操作した場所に出す（6.4）。作成と並べ替えで1つの欄を使い回す */
const result = ref('')
const busy = ref(false)

const statuses = computed(() => projectStore.current?.workflow?.statuses ?? [])
const members = computed(() => projectStore.current?.members ?? [])

/** 応答の追い越しを防ぐ通し番号（`ProjectsPage` と同じ対策） */
let fetchSeq = 0

function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({
        status: 0,
        code: 'internal_error',
        message: '予期しないエラーが発生しました',
      })
}

async function loadTickets(): Promise<void> {
  const mine = ++fetchSeq
  loading.value = true
  error.value = null
  try {
    const res = await ticketsApi.listTickets(projectKey.value, {
      ...filters.value,
      sort: sort.value,
      order: order.value,
    })
    if (mine !== fetchSeq) return
    tickets.value = res.items
    total.value = res.total
    perPage.value = res.per_page
    loaded.value = true
  } catch (e) {
    if (mine !== fetchSeq) return
    error.value = toApiError(e)
    // エラー時に前回の内容を残さない。古い一覧を新しい条件の結果として
    // 見せないため（6.2）。
    tickets.value = []
  } finally {
    if (mine === fetchSeq) loading.value = false
  }
}

/**
 * フィルタとグループ化の選択肢に要るもの。
 *
 * **一覧の取得と直列にしない**——タグ名が無くても行は読めるので、
 * 一覧を待たせる理由が無い（`UsersPage` のロール取得と同じ扱い）。
 * 失敗しても画面は止めず、その軸の選択肢が空になるだけにする。
 */
async function loadVocabulary(): Promise<void> {
  const key = projectKey.value
  const [t, s] = await Promise.allSettled([tagsApi.listTags(key), sprintsApi.listSprints(key)])
  if (t.status === 'fulfilled') tags.value = t.value.items
  if (s.status === 'fulfilled') sprints.value = s.value.items
}

// ── 行の組み立て ─────────────────────────────────────────────

interface Row {
  ticket: Ticket
  /** インデントの段数。0 がトップレベル */
  depth: number
}

interface Section {
  key: string
  label: string
  rows: Row[]
}

/** インデントは5段までで打ち切る（5.4）。それ以深は同じ深さに置く */
const MAX_DEPTH = 5

/**
 * ツリーに組み直すのは**グループ化が「なし」かつソートが `sort_key` の昇順**の
 * ときだけである（5.4「階層表示」）。
 *
 * 他の列で並べているときにツリーを優先すると、全体が昇順に見えなくなる。
 * グループ化中は 5.4.1 の軸より親子が優先されてしまう（親と子が同じタグ・
 * 同じ担当を持つとは限らない）。
 */
const treeMode = computed(
  () => group.value === '' && sort.value === 'sort_key' && order.value === 'asc',
)

/**
 * `parent_seq` からツリーを組む。
 *
 * **親が結果に含まれていない子はトップレベルに並べる**（`ApiDesign.md` 9.2.4）。
 * サーバはフィルタを行単位で適用し、親を補完しない——補完すると、フィルタに
 * 合致しない行が一覧に現れて `total` と表示件数が食い違う。
 */
function buildTree(items: Ticket[]): Row[] {
  const present = new Set(items.map((t) => t.seq))
  const childrenOf = new Map<number, Ticket[]>()
  const roots: Ticket[] = []

  for (const t of items) {
    if (t.parent_seq !== null && present.has(t.parent_seq)) {
      const siblings = childrenOf.get(t.parent_seq)
      if (siblings) siblings.push(t)
      else childrenOf.set(t.parent_seq, [t])
    } else {
      roots.push(t)
    }
  }

  const rows: Row[] = []
  const emitted = new Set<number>()
  const walk = (list: Ticket[], depth: number): void => {
    for (const t of list) {
      if (emitted.has(t.seq)) continue
      emitted.add(t.seq)
      rows.push({ ticket: t, depth: Math.min(depth, MAX_DEPTH) })
      const kids = childrenOf.get(t.seq)
      if (kids) walk(kids, depth + 1)
    }
  }
  walk(roots, 0)

  // 親子が輪になっていると根から辿り着けない。サーバは `parent_cycle` で
  // 弾いている（9.5.2）が、**行を落として件数と食い違わせない**ようにする。
  for (const t of items) {
    if (!emitted.has(t.seq)) rows.push({ ticket: t, depth: 0 })
  }
  return rows
}

const rows = computed<Row[]>(() =>
  treeMode.value
    ? buildTree(tickets.value)
    : tickets.value.map((t) => ({ ticket: t, depth: 0 })),
)

/**
 * グループ化の軸ごとに、行がどのセクションへ入るかを返す。
 *
 * **タグだけは複数返る**——複数タグを持つチケットは各セクションに重複して
 * 現れる（5.4.1。「これを避けない」）。
 */
function sectionsOf(t: Ticket): { key: string; label: string }[] {
  switch (group.value) {
    case 'parent':
      return t.parent_seq === null
        ? [{ key: 'top', label: 'トップレベル' }]
        : [{ key: String(t.parent_seq), label: parentLabel(t.parent_seq) }]
    case 'tag':
      return t.tags.length === 0
        ? [{ key: 'none', label: '未分類' }]
        : t.tags.map((tag) => ({ key: tag.id, label: tag.name }))
    case 'sprint':
      return [
        t.sprint === null
          ? { key: 'none', label: 'スプリント未設定' }
          : { key: t.sprint.id, label: t.sprint.name },
      ]
    case 'assignee':
      return [
        t.assignee === null
          ? { key: 'none', label: '未割当' }
          : { key: t.assignee.id, label: t.assignee.display_name },
      ]
    case 'status':
      return [{ key: t.status.key, label: t.status.name }]
    default:
      return []
  }
}

/**
 * 親チケットの見出し。
 *
 * **フィルタで親が落ちているとタイトルが手元に無い。** その場合は一覧と同じ
 * `-12` の形（9.1）で出す——「不明」と書くより、詳細を開ける番号のほうが役に立つ。
 */
function parentLabel(seq: number): string {
  const parent = tickets.value.find((t) => t.seq === seq)
  return parent ? parent.title : `-${seq}`
}

/**
 * セクションの並び順。**軸の語彙の順に出す**——タグは `sort_order`、
 * スプリントは一覧の順、状態はワークフローの `sort_order` である。
 * 「未分類」「未割当」「スプリント未設定」は末尾に置く。
 */
function sectionOrder(): string[] {
  switch (group.value) {
    case 'parent':
      return ['top', ...tickets.value.map((t) => String(t.seq))]
    case 'tag':
      return [...tags.value.map((t) => t.id), 'none']
    case 'sprint':
      return [...sprints.value.map((s) => s.id), 'none']
    case 'assignee':
      return [...members.value.map((m) => m.actor_id), 'none']
    case 'status':
      return statuses.value.map((s) => s.key)
    default:
      return []
  }
}

const sections = computed<Section[]>(() => {
  if (group.value === '') return [{ key: '', label: '', rows: rows.value }]

  const buckets = new Map<string, Section>()
  const seen: string[] = []
  for (const row of rows.value) {
    for (const { key, label } of sectionsOf(row.ticket)) {
      const bucket = buckets.get(key)
      if (bucket) {
        bucket.rows.push(row)
      } else {
        buckets.set(key, { key, label, rows: [row] })
        seen.push(key)
      }
    }
  }

  // 語彙の順に並べ、語彙に無いもの（削除されたメンバーの担当など）は
  // 出現順で後ろへ付ける。**空のセクションは出さない。**
  const ordered = sectionOrder().filter((k) => buckets.has(k))
  const rest = seen.filter((k) => !ordered.includes(k))
  return [...ordered, ...rest].map((k) => buckets.get(k)!)
})

// ── セクションの開閉（5.4.1「ブラウザに保持する」）─────────────

const COLLAPSE_KEY = 'pb.backlog_collapsed'

/** 開閉はプロジェクトと軸の組ごとに覚える。軸を変えるとセクションの顔ぶれが変わる */
const collapseScope = computed(() => `${projectKey.value}:${group.value}`)

const collapsed = ref<Set<string>>(new Set())

function readCollapseStore(): Record<string, string[]> {
  try {
    const raw = localStorage.getItem(COLLAPSE_KEY)
    const parsed: unknown = raw === null ? {} : JSON.parse(raw)
    return typeof parsed === 'object' && parsed !== null
      ? (parsed as Record<string, string[]>)
      : {}
  } catch {
    // 壊れた値が入っていても画面は開く。開閉は失われてよい情報である
    return {}
  }
}

function loadCollapsed(): void {
  const stored = readCollapseStore()[collapseScope.value]
  collapsed.value = new Set(Array.isArray(stored) ? stored : [])
}

function toggleSection(key: string): void {
  const next = new Set(collapsed.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  collapsed.value = next

  const store = readCollapseStore()
  store[collapseScope.value] = [...next]
  try {
    localStorage.setItem(COLLAPSE_KEY, JSON.stringify(store))
  } catch {
    // 保存できなくても画面の開閉は効いている（プライベートモード等）
  }
}

// ── 表示のための小さな関数 ───────────────────────────────────

/** 端末のローカル時刻での今日。`date` 列と文字列のまま比べる */
const today = todayPlainDate()

/**
 * 期限超過（5.4）。**完了したチケットは含めない**——期限を過ぎてから
 * 終わったものに `⚠` を出し続けても、次にやることの判断には使えない。
 */
function isOverdue(t: Ticket): boolean {
  return t.due_date !== null && t.closed_at === null && t.due_date < today
}

function assigneeMark(t: Ticket): string {
  if (t.assignee === null) return ''
  return t.assignee.kind === 'agent' ? '🤖' : '👤'
}

/** 件数は端末の設定に依らない形で区切る（`ja-JP` を明示する） */
function withComma(n: number): string {
  return n.toLocaleString('ja-JP')
}

const skeletonRows = computed(() => Math.min(Math.max(tickets.value.length, 5), 10))

/** 200件で打ち切られたか（5.4.2）。超えてもエラーにはならない */
const truncated = computed(() => total.value > perPage.value)

// ── 行の操作 ─────────────────────────────────────────────────

const rowLinks = useTemplateRef<HTMLAnchorElement[]>('rowLink')

/**
 * 行クリックでチケット詳細へ（5.4）。`Ctrl/⌘+クリック` で新規タブ。
 *
 * **文字を選択しただけのときは遷移しない。** ID をなぞってコピーしようと
 * すると mouseup のあとに click が来るため、そのまま遷移してしまう。
 */
function openRow(t: Ticket, e: MouseEvent): void {
  if ((window.getSelection()?.toString() ?? '') !== '') return
  const path = `/p/${projectKey.value}/tickets/${t.seq}`
  if (e.metaKey || e.ctrlKey || e.shiftKey) {
    window.open(router.resolve(path).href, '_blank', 'noopener')
    return
  }
  void router.push(path)
}

/**
 * ID 列は `-31` と接尾だけを表示し、**コピー時は `my-app-31` の完全形**にする（5.4）。
 *
 * 表示を長くせずに、貼り付け先（チャット・コミットメッセージ）で通じる形を渡す。
 */
function onCopyId(t: Ticket, e: ClipboardEvent): void {
  e.clipboardData?.setData('text/plain', `${projectKey.value}-${t.seq}`)
  e.preventDefault()
}

// ── 並べ替え（`⠿` のドラッグ。`ApiDesign.md` 9.4）────────────

/**
 * 掴めるのは**ソートが `sort_key` の昇順のとき**だけである（5.4）。
 * 他の並びでは、画面上の位置と `after_seq` の意味が一致しない。
 */
const canReorder = computed(() => canEdit.value && sort.value === 'sort_key' && order.value === 'asc')

const draggingSeq = ref<number | null>(null)

/**
 * ドロップを受けてよい相手か。
 *
 * `move` は `sort_key` だけを変えて親子は変えない（9.4）ので、**別の親の下へ
 * 落としても表示は動かない**。ツリー表示中は同じ親を持つ行、グループ化中は
 * 同じセクション内の行に限る。
 */
function canDropOn(sourceSeq: number | null, target: Ticket, sectionKey: string): boolean {
  if (sourceSeq === null || sourceSeq === target.seq) return false
  const source = tickets.value.find((t) => t.seq === sourceSeq)
  if (source === undefined) return false
  if (treeMode.value) return source.parent_seq === target.parent_seq
  return sectionsOf(source).some((s) => s.key === sectionKey)
}

/**
 * 受け取れる相手の上でだけ `preventDefault()` する。
 *
 * HTML の D&D は「既定の動作を止めた要素」だけがドロップ先になる規約なので、
 * これで**落とせない行の上ではカーソルが禁止の形になる**。すべて受け取って
 * から弾くと、落とせるように見えて何も起きない。
 */
function onDragOver(e: DragEvent, target: Ticket, sectionKey: string): void {
  if (canDropOn(draggingSeq.value, target, sectionKey)) e.preventDefault()
}

/**
 * 並べ替えを確定する。
 *
 * **画面を先に動かし、サーバの応答で `sort_key` を書き戻す**（タグの
 * 並べ替え、5.9.4 と同じ形）。`rebalanced` が返ったときだけ一覧を取り直す
 * ——プロジェクト全体の `sort_key` が振り直されており、手元の値がすべて
 * 古くなっているためである（9.4）。
 */
async function dropOn(target: Ticket, sectionKey: string): Promise<void> {
  // **掴んでいた seq を先に控える。** `draggingSeq` を消してから判定に渡すと、
  // 判定側が null を見て必ず false になる（ドロップが一切効かなくなる）。
  const seq = draggingSeq.value
  draggingSeq.value = null
  if (!canDropOn(seq, target, sectionKey)) return

  const from = tickets.value.findIndex((t) => t.seq === seq)
  const to = tickets.value.findIndex((t) => t.seq === target.seq)
  if (from < 0 || to < 0) return

  // 下へ動かすなら相手の後ろ、上へ動かすなら相手の前。掴んだ行が
  // 落とした行を「越えた」向きがそのまま指定になる。
  const body = from < to ? { after_seq: target.seq } : { before_seq: target.seq }

  const before = tickets.value
  const next = [...tickets.value]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved!)
  tickets.value = next

  busy.value = true
  result.value = ''
  try {
    const res = await ticketsApi.moveTicket(projectKey.value, moved!.seq, body)
    if (res.rebalanced) {
      await loadTickets()
    } else {
      tickets.value = tickets.value.map((t) =>
        t.seq === res.seq ? { ...t, sort_key: res.sort_key, version: res.version } : t,
      )
    }
    result.value = `✓ ${projectKey.value}-${moved!.seq}「${moved!.title}」の並び順を変更しました`
  } catch (e) {
    tickets.value = before
    error.value = toApiError(e)
    await loadTickets()
  } finally {
    busy.value = false
  }
}

// ── 新規チケット（5.4.3）───────────────────────────────────

const showNewModal = ref(false)
const newDefaults = ref<NewTicketDefaults>({})
const newFieldErrors = ref<Record<string, string>>({})

/**
 * グループ化中にセクション内から作成した場合、**その軸の値を初期値に入れる**（5.4.3）。
 *
 * 状態の軸だけは初期値を持たない——ワークフローの入口はサーバが決めるため、
 * モーダルに状態の欄そのものが無い（9.3）。
 */
function openNewModal(sectionKey?: string): void {
  const defaults: NewTicketDefaults = {}
  if (sectionKey !== undefined && sectionKey !== 'none' && sectionKey !== 'top') {
    if (group.value === 'parent') defaults.parent_seq = Number(sectionKey)
    if (group.value === 'tag') defaults.tag_ids = [sectionKey]
    if (group.value === 'sprint') defaults.sprint_id = sectionKey
    if (group.value === 'assignee') defaults.assignee_id = sectionKey
  }
  newDefaults.value = defaults
  newFieldErrors.value = {}
  result.value = ''
  showNewModal.value = true
}

/**
 * 作成する。**チケット詳細へは飛ばさない**（Phase 1 の詳細はまだ
 * プレースホルダである）。一覧を取り直し、結果を操作した場所に出す（6.4）。
 */
async function createTicket(body: CreateTicketRequest): Promise<void> {
  busy.value = true
  newFieldErrors.value = {}
  try {
    const created = await ticketsApi.createTicket(projectKey.value, body)
    showNewModal.value = false
    await loadTickets()
    result.value = `✓ ${projectKey.value}-${created.seq}「${created.title}」を作成しました`
  } catch (e) {
    const err = toApiError(e)
    if (err.status === 422) {
      // `field` は 2.5 の形式で必ず入る（`ErrorDetail` の required）
      const fields: Record<string, string> = {}
      for (const d of err.details) fields[d.field] = d.message
      newFieldErrors.value = fields
    }
    // 欄に紐づかない理由は、モーダルを閉じずに伝える必要がある。
    // 422 以外（403 など）はここで拾って画面上部へ出す。
    if (err.status !== 422) {
      showNewModal.value = false
      error.value = err
    }
  } finally {
    busy.value = false
  }
}

// ── キーボード（`GuiDesign.md` 9.1）─────────────────────────

/** `j` / `k` で行を上下移動、`c` で新規チケット。`Enter` はリンクの既定動作 */
function onKeydown(e: KeyboardEvent): void {
  if (e.metaKey || e.ctrlKey || e.altKey) return
  // モーダルが開いている間は背後の一覧へフォーカスを移さない（9.2 のトラップ）
  if (showNewModal.value) return
  const el = e.target as HTMLElement | null
  // 入力中は横取りしない（AppShell の `[` と同じ扱い）
  if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return

  if (e.key === 'c') {
    if (!canCreate.value) return
    e.preventDefault()
    openNewModal()
    return
  }

  if (e.key !== 'j' && e.key !== 'k') return
  const links = rowLinks.value
  if (!links || links.length === 0) return
  e.preventDefault()

  const current = links.findIndex((a) => a === document.activeElement)
  const next =
    current === -1
      ? 0
      : Math.min(Math.max(current + (e.key === 'j' ? 1 : -1), 0), links.length - 1)
  links[next]?.focus()
}

// ── 起動と追随 ───────────────────────────────────────────────

const typeOptions = Object.keys(ticketTypeLabels) as TicketType[]
/** 優先度は高い順に出す。走査するとき上から強いものを見たい */
const priorityOptions = [...priorityOrder].reverse() as TicketPriority[]

/**
 * 権限エラーは 403 ページへ送る（6.2）。ガード通過後に権限が変わった場合に来る。
 * 404 はプロジェクトごと到達できない場合で、こちらも画面を出さない（7.2）。
 */
async function retry(): Promise<void> {
  await loadTickets()
  if (error.value?.status === 403) await router.replace('/403')
  if (error.value?.status === 404) await router.replace('/404')
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  loadCollapsed()
  void projectStore.fetchCurrent(projectKey.value)
  void loadVocabulary()
  void loadTickets()
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  projectStore.clearCurrent()
})

// フィルタ・ソート・グループ化は URL に載っている。**クエリが変わったら
// 取り直す**——グループ化だけはサーバへ送らないので取り直しは要らないが、
// 判定を分けるより1本にしたほうが取りこぼさない。
watch(
  () => route.fullPath,
  () => {
    if (route.params.key === undefined) return
    loadCollapsed()
    void loadTickets()
  },
)

// プロジェクトを切り替えたら語彙とプロジェクト詳細も取り直す（4.4）
watch(projectKey, (key) => {
  if (key === '') return
  result.value = ''
  tags.value = []
  sprints.value = []
  void projectStore.fetchCurrent(key)
  void loadVocabulary()
})
</script>

<template>
  <div class="page">
    <PageHeader title="バックログ">
      <template #actions>
        <button v-if="canCreate" type="button" class="primary" @click="openNewModal()">
          + 新規チケット
        </button>
      </template>
    </PageHeader>

    <div class="page-body">
      <!-- フィルタ行（5.4）。条件は URL のクエリに載る -->
      <div class="filters">
        <label class="filter">
          <span class="filter-label">状態</span>
          <select
            :value="filters.status"
            @change="setQuery({ status: ($event.target as HTMLSelectElement).value })"
          >
            <option value="">すべて</option>
            <option v-for="s in statuses" :key="s.key" :value="s.key">{{ s.name }}</option>
          </select>
        </label>

        <label class="filter">
          <span class="filter-label">種別</span>
          <select
            :value="filters.type"
            @change="setQuery({ type: ($event.target as HTMLSelectElement).value })"
          >
            <option value="">すべて</option>
            <option v-for="t in typeOptions" :key="t" :value="t">
              {{ ticketTypeIcons[t] }} {{ ticketTypeLabels[t] }}
            </option>
          </select>
        </label>

        <label class="filter">
          <span class="filter-label">担当</span>
          <select
            :value="filters.assignee"
            @change="setQuery({ assignee: ($event.target as HTMLSelectElement).value })"
          >
            <option value="">すべて</option>
            <option value="me">自分</option>
            <option value="none">未割当</option>
            <option v-for="m in members" :key="m.actor_id" :value="m.actor_id">
              {{ m.kind === 'agent' ? '🤖' : '👤' }} {{ m.display_name }}
            </option>
          </select>
        </label>

        <label class="filter">
          <span class="filter-label">優先</span>
          <select
            :value="filters.priority"
            @change="setQuery({ priority: ($event.target as HTMLSelectElement).value })"
          >
            <option value="">すべて</option>
            <option v-for="p in priorityOptions" :key="p" :value="p">
              {{ priorityLabels[p] }}
            </option>
          </select>
        </label>

        <label class="filter">
          <span class="filter-label">タグ</span>
          <select
            :value="filters.tag"
            @change="setQuery({ tag: ($event.target as HTMLSelectElement).value })"
          >
            <option value="">すべて</option>
            <option value="none">未分類</option>
            <option v-for="t in tags" :key="t.id" :value="t.id">{{ t.name }}</option>
          </select>
        </label>

        <label class="filter">
          <span class="filter-label">スプリント</span>
          <select
            :value="filters.sprint"
            @change="setQuery({ sprint: ($event.target as HTMLSelectElement).value })"
          >
            <option value="">すべて</option>
            <option value="none">スプリント未設定</option>
            <option v-for="s in sprints" :key="s.id" :value="s.id">{{ s.name }}</option>
          </select>
        </label>

        <!-- グループ化と解除は右端に寄せる（5.4 のワイヤー） -->
        <label class="filter push">
          <span class="filter-label">グループ化</span>
          <select
            :value="group"
            @change="setQuery({ group: ($event.target as HTMLSelectElement).value })"
          >
            <option v-for="axis in GROUP_AXES" :key="axis" :value="axis">
              {{ groupLabels[axis] }}
            </option>
          </select>
        </label>

        <button
          type="button"
          class="secondary"
          :disabled="isPristine"
          title="フィルタ・グループ化・ソートを元に戻す"
          @click="clearAll"
        >
          解除
        </button>
      </div>

      <!-- 操作の結果は操作した場所に出す（6.4）。作成と並べ替えで使い回す -->
      <p v-if="result" class="ok" role="status">{{ result }}</p>

      <!-- エラー（6.2）。原因はサーバが返した message をそのまま出す -->
      <EmptyState
        v-if="error"
        title="チケットを取得できませんでした"
        :description="error.message"
      >
        <template #action>
          <button type="button" class="primary" @click="retry">再試行</button>
        </template>
      </EmptyState>

      <!-- 読み込み中はスケルトン。スピナーではなく実際の行の形を模す（6.2） -->
      <table v-else-if="loading && !loaded" class="table" aria-busy="true">
        <tbody>
          <tr v-for="n in skeletonRows" :key="n" class="skeleton-row">
            <td v-for="c in 7" :key="c"><span class="skeleton"></span></td>
          </tr>
        </tbody>
      </table>

      <!-- 空（6.2）。作れない利用者にはボタンを出さない -->
      <EmptyState
        v-else-if="tickets.length === 0 && isPristine"
        title="チケットがありません"
        description="最初のチケットを作って、やることを並べていきましょう"
      >
        <template v-if="canCreate" #action>
          <button type="button" class="primary" @click="openNewModal()">+ 新規チケット</button>
        </template>
      </EmptyState>
      <EmptyState
        v-else-if="tickets.length === 0"
        title="条件に合うチケットがありません"
        description="フィルタを緩めるか、解除してください"
      >
        <template #action>
          <button type="button" class="secondary" @click="clearAll">解除</button>
        </template>
      </EmptyState>

      <template v-else>
        <div v-for="section in sections" :key="section.key" class="group-section">
          <!-- セクション見出し。件数は**表示されている行数**であり、タグの
               重複を含む（5.4.1）。下部の総件数とは一致しないことがある -->
          <div v-if="group !== ''" class="section-head">
            <button
              type="button"
              class="section-toggle"
              :aria-expanded="!collapsed.has(section.key)"
              @click="toggleSection(section.key)"
            >
              <span class="caret" aria-hidden="true">
                {{ collapsed.has(section.key) ? '▸' : '▾' }}
              </span>
              <span class="section-name">{{ section.label }}</span>
              <span class="section-count">({{ section.rows.length }})</span>
            </button>
            <button
              v-if="canCreate"
              type="button"
              class="secondary small"
              :aria-label="`${section.label} にチケットを追加`"
              @click="openNewModal(section.key)"
            >
              +
            </button>
          </div>

          <table v-if="!collapsed.has(section.key)" class="table">
            <thead>
              <tr>
                <th scope="col" class="grip-col" :aria-sort="ariaSort('sort_key')">
                  <!-- `⠿` 列のヘッダが `sort_key` へ戻すボタンを兼ねる（5.4） -->
                  <button
                    type="button"
                    class="sort grip-sort"
                    aria-label="手動の並び順にする"
                    title="手動の並び順にする"
                    @click="sortBy('sort_key')"
                  >
                    ⠿
                  </button>
                </th>
                <th scope="col" class="id-col" :aria-sort="ariaSort('seq')">
                  <button type="button" class="sort" @click="sortBy('seq')">
                    ID
                    <span class="caret" aria-hidden="true">{{
                      sort === 'seq' ? (order === 'asc' ? '▴' : '▾') : ''
                    }}</span>
                  </button>
                </th>
                <th scope="col" :aria-sort="ariaSort('title')">
                  <button type="button" class="sort" @click="sortBy('title')">
                    タイトル
                    <span class="caret" aria-hidden="true">{{
                      sort === 'title' ? (order === 'asc' ? '▴' : '▾') : ''
                    }}</span>
                  </button>
                </th>
                <th scope="col" class="status-col" :aria-sort="ariaSort('status')">
                  <button type="button" class="sort" @click="sortBy('status')">
                    状態
                    <span class="caret" aria-hidden="true">{{
                      sort === 'status' ? (order === 'asc' ? '▴' : '▾') : ''
                    }}</span>
                  </button>
                </th>
                <th scope="col" class="priority-col" :aria-sort="ariaSort('priority')">
                  <button type="button" class="sort" @click="sortBy('priority')">
                    優先
                    <span class="caret" aria-hidden="true">{{
                      sort === 'priority' ? (order === 'asc' ? '▴' : '▾') : ''
                    }}</span>
                  </button>
                </th>
                <!-- 担当だけソートできない（`ApiDesign.md` 9.2.1 の sort に無い） -->
                <th scope="col" class="assignee-col">担当</th>
                <th scope="col" class="due-col" :aria-sort="ariaSort('due_date')">
                  <button type="button" class="sort" @click="sortBy('due_date')">
                    期限
                    <span class="caret" aria-hidden="true">{{
                      sort === 'due_date' ? (order === 'asc' ? '▴' : '▾') : ''
                    }}</span>
                  </button>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="row in section.rows"
                :key="`${section.key}:${row.ticket.seq}`"
                class="row"
                :class="{ dragging: draggingSeq === row.ticket.seq }"
                @click="openRow(row.ticket, $event)"
                @dragover="onDragOver($event, row.ticket, section.key)"
                @drop.prevent="dropOn(row.ticket, section.key)"
              >
                <td class="grip-col">
                  <span
                    v-if="canReorder"
                    class="grip"
                    draggable="true"
                    role="button"
                    :aria-label="`${row.ticket.title} を並べ替える`"
                    @click.stop
                    @dragstart="draggingSeq = row.ticket.seq"
                    @dragend="draggingSeq = null"
                    >⠿</span
                  >
                  <span class="type-icon" :title="ticketTypeLabels[row.ticket.type]">
                    {{ ticketTypeIcons[row.ticket.type] }}
                  </span>
                </td>

                <!-- 接尾のみ表示、コピー時は完全形（5.4） -->
                <td class="id-col">
                  <code class="seq" @copy="onCopyId(row.ticket, $event)">-{{ row.ticket.seq }}</code>
                </td>

                <td class="title-col">
                  <span class="title-line" :style="{ paddingLeft: `${row.depth * 20}px` }">
                    <span v-if="row.depth > 0" class="branch" aria-hidden="true">└</span>
                    <RouterLink
                      v-slot="{ href, navigate }"
                      :to="`/p/${projectKey}/tickets/${row.ticket.seq}`"
                      custom
                    >
                      <a ref="rowLink" class="title" :href="href" @click.stop="navigate">
                        {{ row.ticket.title }}
                      </a>
                    </RouterLink>
                    <!-- タグは枠線＋文字（8.6）。色は使わない -->
                    <span v-for="tag in row.ticket.tags" :key="tag.id" class="tag">{{
                      tag.name
                    }}</span>
                  </span>
                </td>

                <td class="status-col">
                  <span class="status" :class="row.ticket.status.category">
                    <span class="status-mark" aria-hidden="true">{{
                      statusMarks[row.ticket.status.category]
                    }}</span>
                    {{ row.ticket.status.name }}
                  </span>
                </td>

                <!-- 優先度は色を使わず記号のみ。中は無表示（8.7） -->
                <td class="priority-col">
                  <span
                    v-if="row.ticket.priority"
                    class="priority"
                    :title="priorityLabels[row.ticket.priority]"
                    >{{ priorityMarks[row.ticket.priority] }}</span
                  >
                </td>

                <td class="assignee-col">
                  <template v-if="row.ticket.assignee">
                    <span class="actor-mark" aria-hidden="true">{{
                      assigneeMark(row.ticket)
                    }}</span
                    >{{ row.ticket.assignee.display_name }}
                  </template>
                  <span v-else class="muted">—</span>
                </td>

                <td class="due-col">
                  <span v-if="row.ticket.due_date" :class="{ overdue: isOverdue(row.ticket) }">
                    <span v-if="isOverdue(row.ticket)" aria-hidden="true">⚠ </span>
                    {{ formatPlainDate(row.ticket.due_date) }}
                  </span>
                  <span v-else class="muted">—</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 総件数は**チケットの実数**で、タグの重複を含まない（5.4.1） -->
        <p class="total">
          <template v-if="truncated">
            {{ withComma(total) }}件中 {{ withComma(perPage) }}件を表示しています。フィルタで絞り込んでください
          </template>
          <template v-else>{{ withComma(total) }}件</template>
        </p>
      </template>
    </div>

    <NewTicketModal
      v-if="showNewModal"
      :members="members"
      :tags="tags"
      :sprints="sprints"
      :candidates="tickets"
      :defaults="newDefaults"
      :busy="busy"
      :field-errors="newFieldErrors"
      @close="showNewModal = false"
      @save="createTicket"
    />
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.page-body {
  flex: 1;
  overflow: auto;
  padding: var(--pb-space-6);
}

/* ── フィルタ行 ─────────────────────────────────────────── */
.filters {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--pb-space-3);
  margin-bottom: var(--pb-space-4);
}

.filter {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

/* グループ化から右は右端へ寄せる。狭い窓では折り返して先頭に来る */
.push {
  margin-left: auto;
}

.filter-label {
  color: var(--pb-text-muted);
  font-size: 13px;
  white-space: nowrap;
}

.filters select {
  height: 32px;
  max-width: 180px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

.ok {
  margin-bottom: var(--pb-space-3);
  color: var(--pb-text);
}

/* ── セクション（グループ化）───────────────────────────── */

/* **`.section` にしない。** `SideMenu.vue` が「管理」の見出しに同じ名前を
   使っており、scoped スタイルは見た目を分けても DOM のクラス名は分けない。
   `document.querySelectorAll('.section')` が両方に当たる（検証で実際に踏んだ） */
.group-section + .group-section {
  margin-top: var(--pb-space-5);
}

/* **見出しを面で出す。** 中央寄せや太字は位置と強さの情報であって、
   「どこからどこまでが1つの群か」を示さない。地より一段明るい帯にすると、
   帯から次の帯までが1つの群だと読める（8.2 の輝度による階層）。色は使わない */
.section-head {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  padding: var(--pb-space-1) var(--pb-space-2);
  border-radius: var(--pb-radius) var(--pb-radius) 0 0;
  background: var(--pb-elevated);
}

.section-toggle {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.section-name {
  overflow: hidden;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.section-count {
  flex: none;
  color: var(--pb-text-muted);
}

/* ── 表 ───────────────────────────────────────────────── */
.table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.table th,
.table td {
  padding: 0 var(--pb-space-2);
  overflow: hidden;
  border-bottom: 1px solid var(--pb-line);
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.table th {
  height: var(--pb-row-h);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
}

.table td {
  height: var(--pb-row-h);
}

.sort {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.caret {
  color: var(--pb-text-muted);
}

.row {
  cursor: pointer;
}

.row:hover {
  background: var(--pb-hover);
}

.row.dragging {
  opacity: 0.5;
}

.skeleton-row td {
  height: var(--pb-row-h);
}

.skeleton {
  display: block;
  width: 60%;
  height: 12px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

/* ── 列幅 ─────────────────────────────────────────────── */
.grip-col {
  width: 56px;
}

.id-col {
  width: 72px;
}

.status-col {
  width: 110px;
}

.priority-col {
  width: 64px;
}

.assignee-col {
  width: 140px;
}

.due-col {
  width: 116px;
}

/* ── セルの中身 ───────────────────────────────────────── */

/* `⠿` は掴む対象だと分かるようにカーソルを変える。種別アイコンと同居する */
.grip {
  margin-right: var(--pb-space-1);
  color: var(--pb-text-muted);
  cursor: grab;
}

.grip-sort {
  color: var(--pb-text-muted);
}

.type-icon {
  color: var(--pb-text-muted);
}

.seq {
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* タイトルセルは表レイアウトの構成要素なので display を変えない。
   並べるのは中の入れ物の役目である（手順16a の教訓） */
.title-line {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-2);
  max-width: 100%;
  overflow: hidden;
}

.branch {
  flex: none;
  color: var(--pb-text-muted);
}

.title {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.title:hover {
  text-decoration: underline;
}

/* タグは枠線＋文字。**ユーザーによる任意色の指定も許さない**（8.6） */
.tag {
  flex: none;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  color: var(--pb-text-muted);
  font-size: 12px;
  line-height: 18px;
}

/* ステータスは輝度差＋記号で表す（8.7）。進行中ほど濃く、終わったものほど淡い */
.status {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 0 var(--pb-space-2);
  border-radius: var(--pb-radius);
  font-size: 13px;
  line-height: 22px;
}

.status.todo {
  border: 1px solid var(--pb-border);
  color: var(--pb-text-muted);
}

.status.in_progress {
  background: var(--pb-elevated);
  color: var(--pb-text);
}

.status.in_progress .status-mark {
  color: var(--pb-accent);
}

.status.review {
  background: var(--pb-hover);
  color: var(--pb-text);
}

.status.done {
  color: var(--pb-text-muted);
}

.priority,
.actor-mark {
  color: var(--pb-text-muted);
}

.actor-mark {
  margin-right: 4px;
}

.muted {
  color: var(--pb-text-muted);
}

/* 期限超過だけ danger を使う（8.6 の「事実として発生した問題」） */
.overdue {
  color: var(--pb-danger-text);
}

.total {
  margin-top: var(--pb-space-4);
  color: var(--pb-text-muted);
}
</style>
