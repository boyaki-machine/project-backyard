<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import EmptyState from '../components/EmptyState.vue'
import EpicFilter from '../components/EpicFilter.vue'
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
  backlogTicketTypes,
  priorityLabels,
  priorityMarks,
  priorityOrder,
  statusMarks,
  ticketTypeIcons,
  ticketTypeLabels,
} from '../api/tickets'
import type {
  CreateTicketRequest,
  MoveTicketRequest,
  Ticket,
  TicketPriority,
  TicketSort,
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
 * **上下二段**（5.4「二段」）。上が**オンステージ**（いま仕掛り中で、直近の
 * スプリントで消化すべきもの）、下が**バックログ**（行うべき仕事の保管庫）で、
 * 段の実体は `ticket.staged_at` である。**行は片方にしか出ない。**
 *
 * **エピックは行として出さない**（5.4「エピックをフィルタにする」）。複数選択
 * できるフィルタになり、絞り込みの実体は `parent`（部分木）である。
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

/** ドロップダウンで選ぶフィルタ。**API のクエリ名をそのまま使う**（5.4） */
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
 * **複数選択を持つのはエピックだけ**（下の `epicSeqs`）。
 */
const filters = computed<Record<FilterKey, string>>(() => ({
  status: queryValue('status'),
  type: queryValue('type'),
  assignee: queryValue('assignee'),
  priority: queryValue('priority'),
  tag: queryValue('tag'),
  sprint: queryValue('sprint'),
}))

/**
 * エピックフィルタの選択（5.4「エピックをフィルタにする」）。
 *
 * **URL 上の実体は `parent` のカンマ区切り**である（`ApiDesign.md` 9.2.1）。
 * 種別ではなく部分木で絞るので、`epic` という専用パラメータは持たない。
 */
const epicSeqs = computed<number[]>(() =>
  queryValue('parent')
    .split(',')
    .map((v) => Number(v))
    .filter((n) => Number.isInteger(n) && n > 0),
)

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
    epicSeqs.value.length === 0 &&
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
/** エピックの選択肢（5.4「フィルタ」）。タグ・スプリントと同じ「語彙の取得」である */
const epics = ref<Ticket[]>([])

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
      // **種別が「すべて」のときは `story,task` を送る**（5.4）。エピックを
      // 画面側で捨てると、下部に出す総件数（サーバの `total`）と食い違う。
      type: filters.value.type === '' ? backlogTicketTypes.join(',') : filters.value.type,
      // エピックフィルタの実体（9.2.1）。空なら `listTickets` がキーごと落とす
      parent: epicSeqs.value.join(','),
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
 *
 * **エピックだけは一覧と同じエンドポイントから取る**（5.4）。他のフィルタを
 * 掛けない——絞り込みの結果に関わらず、選択肢は常に全エピックである。
 */
async function loadVocabulary(): Promise<void> {
  const key = projectKey.value
  const [t, s, e] = await Promise.allSettled([
    tagsApi.listTags(key),
    sprintsApi.listSprints(key),
    ticketsApi.listTickets(key, { type: 'epic' }),
  ])
  if (t.status === 'fulfilled') tags.value = t.value.items
  if (s.status === 'fulfilled') sprints.value = s.value.items
  if (e.status === 'fulfilled') epics.value = e.value.items
}

const epicSeqSet = computed(() => new Set(epics.value.map((e) => e.seq)))

/**
 * 段に置けるか（`ApiDesign.md` 9.4.1）。**表示上のトップレベルだけ**——
 * 親を持たないもの、または**親がエピックのもの**。
 *
 * サーバも同じ検証をして 422 `not_stageable` を返すが、ここで見るのは
 * **落とせない行の上でカーソルを禁止の形にする**ためである。
 * 判定に親の種別が要るので、エピック一覧（語彙）を使う。
 */
function isStageable(t: Ticket): boolean {
  return t.parent_seq === null || epicSeqSet.value.has(t.parent_seq)
}

// ── 行の組み立て ─────────────────────────────────────────────

interface Row {
  ticket: Ticket
  /** インデントの段数。0 がトップレベル */
  depth: number
  /**
   * **結果の中に子がいるか。** `has_children`（プロジェクト内に子がいるか）は
   * 使わない——絞り込みで子が落ちた行に、押しても何も起きないキャレットが出る。
   */
  hasChildren: boolean
  /**
   * **表示上の親**（5.4「並べ替えられる相手」）。`parent_seq` の指す行が結果の中に
   * 居ればその `seq`、居なければ `null`（＝この行は根として並んでいる）。
   *
   * **`parent_seq` をそのまま使わない。** エピックを行として出さなくなったので、
   * エピック配下のチケットは `parent_seq` を持ったまま**根として並ぶ**。
   * `parent_seq` で判定すると、**画面上で同じ深さに見える行どうしが入れ替えられない**。
   */
  parentKey: number | null
}

interface Section {
  key: string
  label: string
  rows: Row[]
  /** **二段のときだけ値を持つ。** `true` がオンステージ、`false` がバックログ */
  stage?: boolean
}

/** インデントは5段までで打ち切る（5.4）。それ以深は同じ深さに置く */
const MAX_DEPTH = 5

/**
 * 二段で出すか（5.4「二段」）。**グループ化を選んだら1つの表に戻す**（5.4.1）
 * ——二段とセクションの入れ子は、上下どちらのセクションへ落としたかが読めない。
 */
const twoTier = computed(() => group.value === '')

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
 * 段ごとに振り分ける（5.4「二段」）。
 *
 * **上げた行の配下は、どちらの段にも行として出さない**（5.4「配下の行き先」。
 * 利用者の判断、2026-08-23）。親と一緒に運ばれた以上、片方の段にだけ子が
 * 残ると上下を見比べる作業がここで復活する。**グループ化に切り替えると
 * 段の軸が消えるので、伏せた行もふたたび出る。**
 */
function splitByStage(items: Ticket[]): { staged: Ticket[]; backlog: Ticket[] } {
  const childrenOf = new Map<number, Ticket[]>()
  for (const t of items) {
    if (t.parent_seq === null) continue
    const siblings = childrenOf.get(t.parent_seq)
    if (siblings) siblings.push(t)
    else childrenOf.set(t.parent_seq, [t])
  }

  const carried = new Set<number>()
  const bury = (seq: number): void => {
    for (const kid of childrenOf.get(seq) ?? []) {
      if (carried.has(kid.seq)) continue
      carried.add(kid.seq)
      bury(kid.seq)
    }
  }
  for (const t of items) if (t.staged_at !== null) bury(t.seq)

  const staged: Ticket[] = []
  const backlog: Ticket[] = []
  for (const t of items) {
    if (carried.has(t.seq)) continue
    if (t.staged_at !== null) staged.push(t)
    else backlog.push(t)
  }
  return { staged, backlog }
}

const staged = computed(() => splitByStage(tickets.value))

/** 出していない配下の件数。総件数との差を画面で説明するために数える */
const carriedCount = computed(() =>
  twoTier.value
    ? tickets.value.length - staged.value.staged.length - staged.value.backlog.length
    : 0,
)

/**
 * `parent_seq` からツリーを組む。
 *
 * **親が結果に含まれていない子はトップレベルに並べる**（`ApiDesign.md` 9.2.4）。
 * サーバはフィルタを行単位で適用し、親を補完しない——補完すると、フィルタに
 * 合致しない行が一覧に現れて `total` と表示件数が食い違う。**エピックを行から
 * 外す帰結として、エピック配下のチケットはここでトップレベルになる。**
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
  // **「行として出した」と「根から辿り着けた」を分けて持つ。** 畳んだ行の
  // 配下は出さないが辿り着けてはいるので、末尾の取りこぼし救済に混ぜない。
  const reached = new Set<number>()

  const markReached = (list: Ticket[]): void => {
    for (const t of list) {
      if (reached.has(t.seq)) continue
      reached.add(t.seq)
      const kids = childrenOf.get(t.seq)
      if (kids) markReached(kids)
    }
  }

  const walk = (list: Ticket[], depth: number): void => {
    for (const t of list) {
      if (reached.has(t.seq)) continue
      reached.add(t.seq)
      const kids = childrenOf.get(t.seq)
      rows.push({
        ticket: t,
        depth: Math.min(depth, MAX_DEPTH),
        hasChildren: kids !== undefined,
        parentKey: depth === 0 ? null : t.parent_seq,
      })
      if (kids === undefined) continue
      if (treeCollapsed.value.has(t.seq)) markReached(kids)
      else walk(kids, depth + 1)
    }
  }
  walk(roots, 0)

  // 親子が輪になっていると根から辿り着けない。サーバは `parent_cycle` で
  // 弾いている（9.5.2）が、**行を落として件数と食い違わせない**ようにする。
  for (const t of items) {
    if (!reached.has(t.seq)) rows.push({ ticket: t, depth: 0, hasChildren: false, parentKey: null })
  }
  return rows
}

/** ツリーを組まないときの行。インデントもキャレットも持たない */
function flatRows(items: Ticket[]): Row[] {
  return items.map((t) => ({ ticket: t, depth: 0, hasChildren: false, parentKey: null }))
}

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
 * **エピックは一覧に出ないが、語彙として手元にある**ので名前を引ける。
 * どちらにも無い場合は完全形の ID で出す（5.4「ID列」）——「不明」と書くより、
 * 詳細を開ける番号のほうが役に立つ。
 */
function parentLabel(seq: number): string {
  const parent = tickets.value.find((t) => t.seq === seq) ?? epics.value.find((e) => e.seq === seq)
  return parent ? parent.title : `${projectKey.value}-${seq}`
}

/**
 * セクションの並び順。**軸の語彙の順に出す**——タグは `sort_order`、
 * スプリントは一覧の順、状態はワークフローの `sort_order` である。
 * 「未分類」「未割当」「スプリント未設定」は末尾に置く。
 */
function sectionOrder(): string[] {
  switch (group.value) {
    case 'parent':
      return ['top', ...epics.value.map((e) => String(e.seq)), ...tickets.value.map((t) => String(t.seq))]
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
  // 二段（5.4）。**オンステージはフラットな消化順リスト**で、ツリーを組まない
  // ——段に置けるのは表示上のトップレベルだけなので、子の行がそもそも来ない。
  if (twoTier.value) {
    return [
      { key: 'staged', label: 'オンステージ', rows: flatRows(staged.value.staged), stage: true },
      {
        key: 'backlog',
        label: 'バックログ',
        rows: treeMode.value ? buildTree(staged.value.backlog) : flatRows(staged.value.backlog),
        stage: false,
      },
    ]
  }

  const buckets = new Map<string, Section>()
  const seen: string[] = []
  for (const row of flatRows(tickets.value)) {
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

/**
 * 開閉はプロジェクトと軸の組ごとに覚える。軸を変えるとセクションの顔ぶれが
 * 変わるためで、**二段（軸「なし」）の `staged` / `backlog` も同じ器に入る。**
 */
const collapseScope = computed(() => `${projectKey.value}:${group.value}`)

const collapsed = ref<Set<string>>(new Set())

function readStore(key: string): Record<string, unknown> {
  try {
    const raw = localStorage.getItem(key)
    const parsed: unknown = raw === null ? {} : JSON.parse(raw)
    return typeof parsed === 'object' && parsed !== null ? (parsed as Record<string, unknown>) : {}
  } catch {
    // 壊れた値が入っていても画面は開く。開閉は失われてよい情報である
    return {}
  }
}

function writeStore(key: string, value: Record<string, unknown>): void {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // 保存できなくても画面の開閉は効いている（プライベートモード等）
  }
}

function loadCollapsed(): void {
  const stored = readStore(COLLAPSE_KEY)[collapseScope.value]
  collapsed.value = new Set(Array.isArray(stored) ? (stored as string[]) : [])
}

function toggleSection(key: string): void {
  const next = new Set(collapsed.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  collapsed.value = next

  const store = readStore(COLLAPSE_KEY)
  store[collapseScope.value] = [...next]
  writeStore(COLLAPSE_KEY, store)
}

// ── ツリーの折りたたみ（5.4「折りたたみ」）───────────────────

/**
 * **セクションの開閉とは別のキーに置く**（5.4「折りたたみ」）。セクションは
 * グループ化の軸ごとに顔ぶれが変わるが、**ツリーの開閉は軸に依らない**。
 * 1つに混ぜると、軸を変えたときにツリーの開閉まで巻き添えになる。
 */
const TREE_COLLAPSE_KEY = 'pb.backlog_tree_collapsed'

const treeCollapsed = ref<Set<number>>(new Set())

function loadTreeCollapsed(): void {
  const stored = readStore(TREE_COLLAPSE_KEY)[projectKey.value]
  treeCollapsed.value = new Set(
    Array.isArray(stored) ? stored.filter((v): v is number => typeof v === 'number') : [],
  )
}

function toggleTree(seq: number): void {
  const next = new Set(treeCollapsed.value)
  if (next.has(seq)) next.delete(seq)
  else next.add(seq)
  treeCollapsed.value = next

  const store = readStore(TREE_COLLAPSE_KEY)
  store[projectKey.value] = [...next]
  writeStore(TREE_COLLAPSE_KEY, store)
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

/** チケットIDは**完全形**で出す（5.4「ID列」）。`-31` は負の数に見える */
function fullId(t: Ticket): string {
  return `${projectKey.value}-${t.seq}`
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

// ── 並べ替えと段の行き来（`⠿` のドラッグ。`ApiDesign.md` 9.4）─

/**
 * 掴めるのは**ソートが `sort_key` の昇順のとき**だけである（5.4）。
 * 他の並びでは、画面上の位置と `after_seq` の意味が一致しない。
 */
const canReorder = computed(
  () => canEdit.value && sort.value === 'sort_key' && order.value === 'asc',
)

/**
 * 掴んでいる行。
 *
 * **この値をきっかけに要素を差し込んではいけない。** `dragstart` の直後に
 * 掴んだ行の位置がずれると、**Chrome はドラッグを取り消す**（`dragstart` の
 * 1ms 後に `dragend` が来る）。手順16d-b では「〜の末尾へ」の帯を掴んだ瞬間に
 * 出しており、オンステージ側の帯がバックログ表の上に入るため、
 * **バックログの行だけ一度もドラッグできなかった**（利用者の実機確認、2026-08-24）。
 * 見た目だけを変える（`opacity` など）のは安全である。
 */
const draggingSeq = ref<number | null>(null)

/**
 * 落ちる位置の目印（5.4「ドロップ先の見せ方」）。
 *
 * `seq` が行の上、`null` が段そのもの（見出し＝先頭／末尾の帯＝末尾）。
 * **掴んでいる間だけ値を持ち、落とせない相手の上では `null` に戻す**——
 * 線が残っていると、落ちない場所に落ちるように見える。
 */
type DropSide = 'before' | 'after' | 'first' | 'last'

const dropHint = ref<{ key: string; seq: number | null; side: DropSide } | null>(null)

function endDrag(): void {
  draggingSeq.value = null
  dropHint.value = null
}

/** 表示中の行を `seq` から引く。**表示上の親**の判定に要る */
const rowIndex = computed(() => {
  const m = new Map<number, Row>()
  for (const section of sections.value) {
    for (const row of section.rows) if (!m.has(row.ticket.seq)) m.set(row.ticket.seq, row)
  }
  return m
})

/**
 * 行の上へ落としてよいか（5.4「並べ替えられる相手」）。
 *
 * | 場面 | 落とせる相手 |
 * |---|---|
 * | 同じ段のオンステージ | どの行でもよい（全行が表示上の根である） |
 * | 同じ段のバックログ | **同じ「表示上の親」を持つ行**——根どうしは自由、インデントされた行は兄弟だけ |
 * | 段をまたぐ | **段に置ける行**だけ、かつ着地先は移動先の段の**根** |
 * | グループ化中 | **同じセクション内の行** |
 *
 * **`parent_seq` で比べない。** エピックを行として出さないので、エピック配下の
 * チケットは `parent_seq` を持ったまま根として並ぶ。`parent_seq` で比べると
 * 画面上で同じ深さに見える行どうしが入れ替えられなくなる（実機で判明。
 * 利用者の指摘、2026-08-23）。**表示が動かないのはインデントされた行だけ**で、
 * 根は `sort_key` の順に並ぶので `move` の結果がそのまま出る。
 */
function canDropOn(sourceSeq: number | null, row: Row, section: Section): boolean {
  if (sourceSeq === null || sourceSeq === row.ticket.seq) return false
  const sourceRow = rowIndex.value.get(sourceSeq)
  if (sourceRow === undefined) return false
  const source = sourceRow.ticket

  if (section.stage !== undefined) {
    if ((source.staged_at !== null) === section.stage) {
      return sourceRow.parentKey === row.parentKey
    }
    return isStageable(source) && row.parentKey === null
  }
  return sectionsOf(source).some((s) => s.key === section.key)
}

/**
 * 段そのもの（見出し＝先頭、末尾の帯＝末尾）へ落としてよいか。
 *
 * 空の段には基準にできる行が無いので、この落とし場所が無いと最初の1件を
 * 上げられない（`ApiDesign.md` 9.4.1 が `position` を段の中で解釈する理由）。
 *
 * **掴んでいるのが表示上の根のときだけ受け取る。** インデントされた行は段の中の
 * 位置を持たない——`position: "last"` を送っても、その行は親の下で兄弟の末尾へ
 * 動くだけで、「バックログの末尾へ」という表示と食い違う。
 */
function canDropOnSection(sourceSeq: number | null, section: Section): boolean {
  if (sourceSeq === null || section.stage === undefined) return false
  const sourceRow = rowIndex.value.get(sourceSeq)
  if (sourceRow === undefined || sourceRow.parentKey !== null) return false
  if ((sourceRow.ticket.staged_at !== null) === section.stage) return true
  return isStageable(sourceRow.ticket)
}

/**
 * ポインタが行のどちら半分を指しているか（5.4「ドロップ先の見せ方」）。
 *
 * **掴んだ行がどこから来たかに依らない。** 「越えた向き」で決める方式は、
 * 行の下半分を指しても上に入ることがあり、線を出した意味がなくなる。
 */
function sideOf(e: DragEvent): 'before' | 'after' {
  const el = e.currentTarget as HTMLElement | null
  if (el === null) return 'after'
  const r = el.getBoundingClientRect()
  return e.clientY < r.top + r.height / 2 ? 'before' : 'after'
}

/**
 * 受け取れる相手の上でだけ `preventDefault()` する。
 *
 * HTML の D&D は「既定の動作を止めた要素」だけがドロップ先になる規約なので、
 * これで**落とせない行の上ではカーソルが禁止の形になる**。すべて受け取って
 * から弾くと、落とせるように見えて何も起きない。
 */
function onDragOverRow(e: DragEvent, row: Row, section: Section): void {
  if (!canDropOn(draggingSeq.value, row, section)) {
    dropHint.value = null
    return
  }
  e.preventDefault()
  dropHint.value = { key: section.key, seq: row.ticket.seq, side: sideOf(e) }
}

function onDragOverSection(e: DragEvent, section: Section, side: 'first' | 'last'): void {
  if (!canDropOnSection(draggingSeq.value, section)) {
    dropHint.value = null
    return
  }
  e.preventDefault()
  dropHint.value = { key: section.key, seq: null, side }
}

/** 目印を出すか。`dropHint` は1つしか持たないので、線も同時に1本しか出ない */
function hintsRow(row: Row, section: Section, side: 'before' | 'after'): boolean {
  const h = dropHint.value
  return h !== null && h.key === section.key && h.seq === row.ticket.seq && h.side === side
}

function hintsSection(section: Section, side: 'first' | 'last'): boolean {
  const h = dropHint.value
  return h !== null && h.key === section.key && h.seq === null && h.side === side
}

function moveMessage(t: Ticket, stagedChange: boolean | undefined): string {
  const id = `${fullId(t)}「${t.title}」`
  if (stagedChange === true) return `✓ ${id}をオンステージへ上げました`
  if (stagedChange === false) return `✓ ${id}をバックログへ戻しました`
  return `✓ ${id}の並び順を変更しました`
}

/**
 * `move` を送って結果を反映する。
 *
 * **`optimistic` を渡したときだけ画面を先に動かす**（タグの並べ替え、5.9.4 と
 * 同じ形）。段の末尾へ置く操作は手元で正しい位置を作れない——`position` は
 * 段の中で解釈される（9.4.1）のに `sort_key` は二段で1本だからで、
 * その場合は取り直して合わせる。
 *
 * `rebalanced` が返ったときも取り直す——プロジェクト全体の `sort_key` が
 * 振り直されており、手元の値がすべて古くなっているためである（9.4）。
 */
async function runMove(
  source: Ticket,
  body: MoveTicketRequest,
  optimistic: Ticket[] | null,
  stagedChange: boolean | undefined,
): Promise<void> {
  const before = tickets.value
  if (optimistic !== null) tickets.value = optimistic

  busy.value = true
  result.value = ''
  try {
    const res = await ticketsApi.moveTicket(projectKey.value, source.seq, body)
    if (res.rebalanced || optimistic === null) {
      await loadTickets()
    } else {
      tickets.value = tickets.value.map((t) =>
        t.seq === res.seq
          ? { ...t, sort_key: res.sort_key, staged_at: res.staged_at, version: res.version }
          : t,
      )
    }
    result.value = moveMessage(source, stagedChange)
  } catch (e) {
    tickets.value = before
    error.value = toApiError(e)
    await loadTickets()
  } finally {
    busy.value = false
  }
}

/** 段が変わるか。変わらないときは `undefined`（`move` に `staged` を送らない） */
function stageChangeOf(source: Ticket, section: Section): boolean | undefined {
  if (section.stage === undefined) return undefined
  return (source.staged_at !== null) === section.stage ? undefined : section.stage
}

/** 段の見た目を先に変えるための行。正しい値はサーバ応答で上書きする */
function optimisticRow(source: Ticket, stagedChange: boolean | undefined): Ticket {
  if (stagedChange === undefined) return source
  return { ...source, staged_at: stagedChange ? new Date().toISOString() : null }
}

/**
 * 行の上へ落とす。**ポインタが指した半分がそのまま前後になる**
 * （5.4「ドロップ先の見せ方」）——出した挿入線と着地を一致させるためで、
 * 掴んだ行がどこから来たかには依らない。
 *
 * 段をまたぐときも同じ規則で決める。`sort_key` は二段で1本なので（9.4）、
 * どちらの段の行を基準にしても位置は一意に定まる。
 */
async function dropOnRow(e: DragEvent, row: Row, section: Section): Promise<void> {
  // **掴んでいた seq を先に控える。** `draggingSeq` を消してから判定に渡すと、
  // 判定側が null を見て必ず false になる（ドロップが一切効かなくなる）。
  const seq = draggingSeq.value
  const side = sideOf(e)
  endDrag()
  if (!canDropOn(seq, row, section)) return

  const from = tickets.value.findIndex((t) => t.seq === seq)
  if (from < 0) return
  const source = tickets.value[from]!
  const stagedChange = stageChangeOf(source, section)

  const body: MoveTicketRequest =
    side === 'before' ? { before_seq: row.ticket.seq } : { after_seq: row.ticket.seq }
  if (stagedChange !== undefined) body.staged = stagedChange

  // **掴んだ行を抜いてから相手の位置を数え直す。** 抜く前の添字で挿入すると、
  // 下へ動かしたときだけ1つ手前に入る。
  const next = [...tickets.value]
  next.splice(from, 1)
  const to = next.findIndex((t) => t.seq === row.ticket.seq)
  if (to < 0) return
  const insertAt = side === 'before' ? to : to + 1

  // 位置も段も変わらないなら送らない。**`version` を無駄に上げない**
  // （`If-Match` を使う画面が 409 になる。9.4）
  if (insertAt === from && stagedChange === undefined) return

  next.splice(insertAt, 0, optimisticRow(source, stagedChange))
  await runMove(source, body, next, stagedChange)
}

/**
 * 段そのものへ落とす（5.4「ドロップ先の見せ方」）。
 *
 * **見出し＝その段の先頭、末尾の帯＝その段の末尾。** 見出しは段の上端にあるので、
 * ここへ落として末尾へ飛ぶと、見えている場所と着地が食い違う。
 */
async function dropOnSection(section: Section, position: 'first' | 'last'): Promise<void> {
  const seq = draggingSeq.value
  endDrag()
  if (!canDropOnSection(seq, section)) return

  const source = rowIndex.value.get(seq!)?.ticket
  if (source === undefined || section.stage === undefined) return

  const stagedChange = stageChangeOf(source, section)
  const body: MoveTicketRequest = { position }
  if (stagedChange !== undefined) body.staged = stagedChange

  // 段の中の先頭・末尾は手元で正しい位置を作れない——`position` は段の中で
  // 解釈される（9.4.1）のに `sort_key` は二段で1本だからで、取り直して合わせる。
  await runMove(source, body, null, stagedChange)
}

// ── 新規チケット（5.4.3）───────────────────────────────────

const showNewModal = ref(false)
const newDefaults = ref<NewTicketDefaults>({})
const newFieldErrors = ref<Record<string, string>>({})

/**
 * 親の選択肢（5.4.3）。**いま一覧に出ているチケット**から選ぶ。
 *
 * **エピックで絞り込み中は「そのエピック配下かつ未完了」に絞る**——絞り込んで
 * 作業しているときに、視野の外のチケットを親に選べても選ぶ理由がない。
 * **選択中のエピック自身も候補に入れる**（直下にストーリーを足すのが普通の
 * 操作であり、エピックは行として出ないのでここでしか選べない）。
 */
const parentCandidates = computed<Ticket[]>(() => {
  if (epicSeqs.value.length === 0) return tickets.value
  const selected = epics.value.filter((e) => epicSeqs.value.includes(e.seq))
  return [...selected, ...tickets.value.filter((t) => t.closed_at === null)]
})

/**
 * グループ化中にセクション内から作成した場合、**その軸の値を初期値に入れる**（5.4.3）。
 *
 * 状態の軸だけは初期値を持たない——ワークフローの入口はサーバが決めるため、
 * モーダルに状態の欄そのものが無い（9.3）。
 *
 * **エピックを1つだけ選んでいるときは、そのエピックが親の初期値になる**（5.4）。
 * 2つ以上のときは入れない——どちらの配下に作るのかを決められない。
 */
function openNewModal(sectionKey?: string): void {
  const defaults: NewTicketDefaults = {}
  if (
    sectionKey !== undefined &&
    group.value !== '' &&
    sectionKey !== 'none' &&
    sectionKey !== 'top'
  ) {
    if (group.value === 'parent') defaults.parent_seq = Number(sectionKey)
    if (group.value === 'tag') defaults.tag_ids = [sectionKey]
    if (group.value === 'sprint') defaults.sprint_id = sectionKey
    if (group.value === 'assignee') defaults.assignee_id = sectionKey
  }
  if (defaults.parent_seq === undefined && epicSeqs.value.length === 1) {
    defaults.parent_seq = epicSeqs.value[0]
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

/** **種別の選択肢にエピックを出さない**（5.4）。行として出ないものは絞れない */
const typeOptions = backlogTicketTypes
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
  loadTreeCollapsed()
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
  epics.value = []
  loadTreeCollapsed()
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

        <!-- エピックだけは複数選択（5.4）。URL 上の実体は `parent` である -->
        <div class="filter">
          <span class="filter-label" aria-hidden="true">エピック</span>
          <EpicFilter
            :epics="epics"
            :selected="epicSeqs"
            :project-key="projectKey"
            @update="setQuery({ parent: $event.join(',') })"
          />
        </div>

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

      <!-- 操作の結果は操作した場所に出す（6.4）。作成・並べ替え・段の行き来で使い回す -->
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
          <!-- 見出し。件数は**そのセクションに表示されている行数**であり、タグの
               重複を含む（5.4.1）。下部の総件数とは一致しないことがある。
               **段そのものが末尾への落とし場所を兼ねる**（9.4.1 の position） -->
          <div
            class="section-head"
            :class="{ 'drop-first': hintsSection(section, 'first') }"
            @dragover="onDragOverSection($event, section, 'first')"
            @drop.prevent="dropOnSection(section, 'first')"
          >
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
            <!-- 段の見出しには `[+]` を置かない。**作ったチケットは必ず
                 バックログに入る**（9.3）ので、オンステージから作れるように
                 見せると嘘になる -->
            <button
              v-if="canCreate && section.stage === undefined"
              type="button"
              class="secondary small"
              :aria-label="`${section.label} にチケットを追加`"
              @click="openNewModal(section.key)"
            >
              +
            </button>
          </div>

          <template v-if="!collapsed.has(section.key)">
            <table v-if="section.rows.length > 0" class="table">
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
                  :class="{
                    dragging: draggingSeq === row.ticket.seq,
                    'drop-before': hintsRow(row, section, 'before'),
                    'drop-after': hintsRow(row, section, 'after'),
                  }"
                  @click="openRow(row.ticket, $event)"
                  @dragover="onDragOverRow($event, row, section)"
                  @drop.prevent="dropOnRow($event, row, section)"
                >
                  <td class="grip-col">
                    <span class="grip-line">
                      <span
                        v-if="canReorder"
                        class="grip"
                        draggable="true"
                        role="button"
                        :aria-label="`${row.ticket.title} を並べ替える`"
                        @click.stop
                        @dragstart="draggingSeq = row.ticket.seq"
                        @dragend="endDrag()"
                        >⠿</span
                      >
                      <!-- 折りたたみ（5.4）。**結果の中に子がいる行にだけ出す。**
                           場所は常に取っておき、行ごとにアイコンの位置がずれないようにする -->
                      <button
                        v-if="row.hasChildren"
                        type="button"
                        class="tree-toggle"
                        :aria-expanded="!treeCollapsed.has(row.ticket.seq)"
                        :aria-label="`${row.ticket.title} の配下を開閉する`"
                        @click.stop="toggleTree(row.ticket.seq)"
                      >
                        {{ treeCollapsed.has(row.ticket.seq) ? '▸' : '▾' }}
                      </button>
                      <span v-else class="tree-spacer" aria-hidden="true"></span>
                      <span class="type-icon" :title="ticketTypeLabels[row.ticket.type]">
                        {{ ticketTypeIcons[row.ticket.type] }}
                      </span>
                    </span>
                  </td>

                  <!-- **完全形で出す**（5.4「ID列」）。`-31` は負の数に見える -->
                  <td class="id-col">
                    <code class="seq">{{ fullId(row.ticket) }}</code>
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

            <!-- 空の段（5.4）。**見出しごと消さない**——落とし場所が無くなると、
                 最初の1件をオンステージへ上げられない -->
            <p
              v-if="section.rows.length === 0"
              class="stage-empty"
              :class="{ 'drop-last': hintsSection(section, 'last') }"
              @dragover="onDragOverSection($event, section, 'last')"
              @drop.prevent="dropOnSection(section, 'last')"
            >
              <template v-if="section.stage === true && canReorder">
                いま取りかかるものを ⠿ でここへドラッグすると、オンステージへ上がります
              </template>
              <template v-else>この段にチケットはありません</template>
            </p>

          </template>
        </div>

        <!-- 総件数は**チケットの実数**で、タグの重複を含まない（5.4.1）。
             **出していない配下を含む**ので、上下の件数の合計とは一致しない
             ことがある（5.4「配下の行き先」） -->
        <p class="total">
          <template v-if="truncated">
            {{ withComma(total) }}件中 {{ withComma(perPage) }}件を表示しています。フィルタで絞り込んでください
          </template>
          <template v-else>{{ withComma(total) }}件</template>
          <span v-if="carriedCount > 0" class="carried">
            （うち{{ withComma(carriedCount) }}件はオンステージの配下として出していません）
          </span>
        </p>
      </template>
    </div>

    <NewTicketModal
      v-if="showNewModal"
      :project-key="projectKey"
      :members="members"
      :tags="tags"
      :sprints="sprints"
      :candidates="parentCandidates"
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

/* ── セクション（二段とグループ化）─────────────────────── */

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

/* 空の段。**破線で「落とせる場所」だと分かるようにする**（色は使わない） */
.stage-empty {
  margin: 0;
  padding: var(--pb-space-3) var(--pb-space-2);
  border: 1px dashed var(--pb-border);
  border-top: none;
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* 帯そのものが落とし場所なので、枠を実線にして受け取る状態を示す */
.stage-empty.drop-last {
  border-color: var(--pb-accent);
  border-style: solid;
  color: var(--pb-text);
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

/* ── ドロップ先の挿入線（5.4「ドロップ先の見せ方」）───────
   **罫線ではなく `box-shadow` の内側で描く。** `border` を足すと行の高さが
   2px 変わり、掴んで動かすたびに表全体が上下にずれる。
   **行を面で塗らない**——塗ると「その行**に**入る」（親子にする）と読めるが、
   `move` は `sort_key` だけを変えて親子を変えない（`ApiDesign.md` 9.4）。
   色は `--pb-accent`（進行中バッジや主ボタンと同じ操作の色。8.6 が禁じるのは
   danger / warning / ai の意味色である） */
.row.drop-before td {
  box-shadow: inset 0 2px 0 0 var(--pb-accent);
}

.row.drop-after td {
  box-shadow: inset 0 -2px 0 0 var(--pb-accent);
}

/* 見出しは「その段の先頭へ」なので、線は見出しの下端に引く */
.section-head.drop-first {
  box-shadow: inset 0 -2px 0 0 var(--pb-accent);
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

/* `⠿` ＋ 折りたたみ ＋ 種別アイコンの3つが入る */
.grip-col {
  width: 68px;
}

/* **完全形の ID を出す**（5.4）。`my-app-31` が入る幅にする */
.id-col {
  width: 116px;
}

.status-col {
  width: 104px;
}

.priority-col {
  width: 56px;
}

/* `👤 開発メンバー` が 1440px で切れない幅。**狭い窓ではタイトルを優先する**が、
   それは `.title` の下限（`min-width`）が担うので、ここは固定値のままにする
   ——メディアクエリを1つ足すより、下限を1か所に置くほうが読める */
.assignee-col {
  width: 130px;
}

/* `⚠ 2026-08-14` が入る幅。**年を省かない**（5.4）ので、`⚠` の分まで数える */
.due-col {
  width: 120px;
}

/* ── セルの中身 ───────────────────────────────────────── */

/* セルは表レイアウトの構成要素なので display を変えない。並べるのは
   中の入れ物の役目である（手順16a の教訓） */
.grip-line {
  display: inline-flex;
  align-items: center;
  gap: 2px;
}

/* `⠿` は掴む対象だと分かるようにカーソルを変える */
.grip {
  color: var(--pb-text-muted);
  cursor: grab;
}

.grip-sort {
  color: var(--pb-text-muted);
}

/* 子を持たない行でも場所を取る。**行ごとに種別アイコンの位置がずれない** */
.tree-toggle,
.tree-spacer {
  display: inline-block;
  width: 16px;
  text-align: center;
}

.tree-toggle {
  padding: 0;
  border: none;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  line-height: 1;
  cursor: pointer;
}

.tree-toggle:hover {
  color: var(--pb-text);
}

.type-icon {
  color: var(--pb-text-muted);
}

.seq {
  color: var(--pb-text-muted);
  font-size: 13px;
}

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

/* **タイトルはタグより先に縮まない。** `flex` を指定しないと `0 1 auto` に
   なり、`flex: none` のタグが残ったままタイトルだけが0幅まで潰れる（900px で
   実測。タグは出ているのに何のチケットか読めなくなる）。下限を置いて、
   あふれるのはタグの側にする——td が `overflow: hidden` なので外へは出ない */
.title {
  flex: 1 1 auto;
  min-width: 6em;
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

.carried {
  font-size: 13px;
}
</style>
