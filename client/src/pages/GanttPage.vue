<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import EmptyState from '../components/EmptyState.vue'
import EpicFilter from '../components/EpicFilter.vue'
import PageHeader from '../components/PageHeader.vue'
import PlannedPeriodFilter from '../components/PlannedPeriodFilter.vue'
import TicketDetailPane from '../components/TicketDetailPane.vue'
import { ApiError } from '../api/client'
import * as calendarApi from '../api/calendar'
import * as linksApi from '../api/links'
import { readGanttSnap } from '../api/projects'
import type { ProjectCalendarDay } from '../api/calendar'
import * as sprintsApi from '../api/sprints'
import type { Sprint } from '../api/sprints'
import * as tagsApi from '../api/tags'
import type { Tag } from '../api/tags'
import * as ticketsApi from '../api/tickets'
import { backlogTicketTypes, statusCategoryLabels, statusCategoryOrder, statusMarks } from '../api/tickets'
import type { Ticket, TicketDetail, TicketGanttLink } from '../api/tickets'
import { statusLabel } from '../lib/catalogLabels'
import { currentTimezone, planInstant } from '../lib/datetime'
import { groupAxisToRestore, saveGroupAxis } from '../lib/groupAxis'
import { GROUP_AXES, groupAxisLabel } from '../lib/ticketGroups'
import type { GroupAxis, GroupVocabulary } from '../lib/ticketGroups'
import { buildRows, rowIndexOf, spanOf, timeRange, toItems } from '../lib/gantt/model'
import type { GItem, GRow } from '../lib/gantt/model'
import { HANDLE_R, depTypeOf, endAt, grabAt, handlesOf, planAt, wouldCycle } from '../lib/gantt/edit'
import type { EditOp, End, Plan } from '../lib/gantt/edit'
import { MS_TAIL, RH, geometry, rangeLabel, render } from '../lib/gantt/render'
import type { DayKind, EdgeHit, EditView, GSprint } from '../lib/gantt/render'
import {
  DAY,
  PPD_DAY,
  PPD_MAX,
  PPD_MIN,
  ZOOM_LEVELS,
  nearestLevel,
  stepLevel,
  tzShort,
  wall,
  wallParts,
  ymd,
} from '../lib/gantt/time'
import type { DayTick } from '../lib/gantt/time'
import * as ganttTime from '../lib/gantt/time'
import { uiNumber, uiText } from '../locales/ui'
import { useAuthStore } from '../stores/auth'
import { useProjectStore } from '../stores/project'
import { useUiStore } from '../stores/ui'

/**
 * ガント（`GuiDesign.md` 5.14）。
 *
 * チケットを見る視点（4.1.1）の1つで、**時間軸に置いて期日と依存関係を見る**画面である。
 * バックログと同じチケットを、同じツリーのまま横に伸ばして描く。
 *
 * **描くのは `lib/gantt/render.ts` の純粋な関数である。** この画面が持つのは、取得した
 * データ・URL のクエリ・スクロールと拡大縮小・ポインタの状態だけで、SVG は
 * `requestAnimationFrame` で1枚の文字列として差し替える（判断の記録「ガントは描画
 * ライブラリを入れず、SVG で自前で描く」）。
 *
 * **状態はすべて URL のクエリに置く**（バックログと同じ。パラメータ名も `ApiDesign.md`
 * 9.2.1 と同じ）。**詳細は `/p/:key/tickets/:seq?from=gantt&<条件>` で開く**（3.2）——
 * `TicketViewsPage` が `from` を見てこの画面を出し続けるので、帯を押しても再マウント
 * されず、スクロール位置と拡大の具合が残る。
 */
const route = useRoute()
const router = useRouter()
const projectStore = useProjectStore()
const auth = useAuthStore()
const ui = useUiStore()

const projectKey = computed(() => {
  const key = route.params.key
  return typeof key === 'string' ? key : ''
})

// ── URL クエリ ───────────────────────────────────────────────

function queryValue(name: string): string {
  const v = route.query[name]
  return typeof v === 'string' ? v : ''
}

/** ガント自身のクエリ（`from` を除く）。詳細の URL へ持ち回る */
const ganttQuery = computed<Record<string, string>>(() => {
  const q: Record<string, string> = {}
  for (const [k, v] of Object.entries(route.query)) {
    if (k !== 'from' && typeof v === 'string' && v !== '') q[k] = v
  }
  return q
})

/**
 * クエリを書き換える。**空の値はキーごと落とす**。`replace` を使うのはバックログと同じ
 * 理由——フィルタの操作1つずつが履歴に積まれると「戻る」でガントから出られなくなる。
 * **詳細を開いていてもパスを変えない**（フィルタを変えても詳細は開いたまま）。
 */
function setQuery(patch: Record<string, string>): void {
  const next: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...route.query, ...patch })) {
    if (typeof v === 'string' && v !== '') next[k] = v
  }
  void router.replace({ path: route.path, query: next })
}

const group = computed<GroupAxis>(() => {
  const v = queryValue('group') as GroupAxis
  return GROUP_AXES.includes(v) ? v : ''
})

const epicSeqs = computed<number[]>(() =>
  queryValue('parent')
    .split(',')
    .map((v) => Number(v))
    .filter((n) => Number.isInteger(n) && n > 0),
)

/**
 * 「状態」の箱（5.4 と同じく1つの箱で3系列を出し入れする）。値は `<クエリ名>:<値>`
 */
const STATE_KEYS = ['status', 'status_category', 'open'] as const

const stateValue = computed<string>(() => {
  for (const k of STATE_KEYS) {
    const v = queryValue(k)
    if (v !== '') return `${k}:${v}`
  }
  return ''
})

function setState(selected: string): void {
  const patch: Record<string, string> = {}
  for (const k of STATE_KEYS) patch[k] = ''
  if (selected !== '') {
    const at = selected.indexOf(':')
    patch[selected.slice(0, at)] = selected.slice(at + 1)
  }
  setQuery(patch)
}

/** 絞り込みの数（ボタンに出す）。グループ化は数えない */
const filterCount = computed(
  () =>
    (epicSeqs.value.length > 0 ? 1 : 0) +
    (queryValue('tag') !== '' ? 1 : 0) +
    (stateValue.value !== '' ? 1 : 0) +
    (queryValue('planned_from') !== '' || queryValue('planned_to') !== '' ? 1 : 0) +
    (queryValue('staged') === 'true' ? 1 : 0) +
    (queryValue('sprint') === 'active' ? 1 : 0),
)

function clearFilters(): void {
  setQuery({ parent: '', tag: '', status: '', status_category: '', open: '', planned_from: '', planned_to: '', staged: '', sprint: '' })
}

/** 軸を選ぶ。**選んだ軸をブラウザにも覚える**（4.1.1。視点をまたいで共有する） */
function setGroup(axis: string): void {
  saveGroupAxis(projectKey.value, axis)
  setQuery({ group: axis })
}

/** URL に `group` が無ければ、覚えた軸で補う（4.1.1）。補ったら `true` */
function restoreGroupAxis(): boolean {
  const axis = groupAxisToRestore(projectKey.value, route.query, GROUP_AXES)
  if (axis === null) return false
  void router.replace({ path: route.path, query: { ...route.query, group: axis } })
  return true
}

const filtersOpen = ref(false)
const filterPanel = useTemplateRef<HTMLElement>('filterPanel')
const filterButton = useTemplateRef<HTMLButtonElement>('filterButton')

function onDocumentPointerDown(e: PointerEvent): void {
  if (!filtersOpen.value) return
  const t = e.target as Node
  if (filterPanel.value?.contains(t) || filterButton.value?.contains(t)) return
  // 絞り込みの部品（エピック・予定期間）は自分のパネルを body 直下に出すことがある
  if ((t as HTMLElement).closest?.('[role="dialog"], [role="listbox"], .popover, .filter-panel')) return
  filtersOpen.value = false
}

// ── 取得 ─────────────────────────────────────────────────────

const tickets = shallowRef<Ticket[]>([])
const links = shallowRef<TicketGanttLink[]>([])
const total = ref(0)
const loading = ref(false)
const loaded = ref(false)
const error = ref<ApiError | null>(null)

const tags = ref<Tag[]>([])
const sprints = ref<Sprint[]>([])
const epics = ref<Ticket[]>([])

/** 休日（基準タイムゾーンの `YYYY-MM-DD` → その日）。取れていなければ土日だけで塗る */
const calendarDays = shallowRef<Map<string, ProjectCalendarDay>>(new Map())
const calendarLoaded = ref(false)

const members = computed(() => projectStore.current?.members ?? [])
const statuses = computed(() => projectStore.current?.workflow?.statuses ?? [])
const baseTz = computed(() => projectStore.planTimezone)
const viewTz = computed(() => currentTimezone() ?? Intl.DateTimeFormat().resolvedOptions().timeZone)

/** 上限（`ApiDesign.md` 9.2.6） */
const GANTT_LIMIT = 5000
const truncated = computed(() => total.value > tickets.value.length)

let fetchSeq = 0

function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({ status: 0, code: 'internal_error', message: uiText('予期しないエラーが発生しました') })
}

/** 予定期間は URL では日付のまま持ち、API へは基準タイムゾーンの半開区間で送る（5.4 と同じ） */
function plannedParam(date: string, isEnd: boolean): string | undefined {
  const ms = date === '' ? null : planInstant(date, baseTz.value, isEnd)
  return ms === null ? undefined : String(ms)
}

async function loadTickets(): Promise<void> {
  const mine = ++fetchSeq
  loading.value = true
  error.value = null
  try {
    const open = queryValue('open')
    const res = await ticketsApi.listTickets(projectKey.value, {
      view: 'gantt',
      // **エピックは行に出さない**（5.14「行の並び」。5.4 と同じ）
      type: backlogTicketTypes.join(','),
      parent: epicSeqs.value.join(','),
      tag: queryValue('tag'),
      status: queryValue('status'),
      status_category: queryValue('status_category'),
      open: open === 'true' || open === 'false' ? open : undefined,
      planned_from: plannedParam(queryValue('planned_from'), false),
      planned_to: plannedParam(queryValue('planned_to'), true),
      staged: queryValue('staged') === 'true' ? 'true' : undefined,
      sprint: queryValue('sprint') === 'active' ? 'active' : undefined,
      sort: 'sort_key',
      order: 'asc',
    })
    if (mine !== fetchSeq) return
    tickets.value = res.items
    links.value = res.links ?? []
    total.value = res.total
    loaded.value = true
    void loadCalendar()
  } catch (e) {
    if (mine !== fetchSeq) return
    error.value = toApiError(e)
    tickets.value = []
    links.value = []
    if (error.value.status === 403) void router.replace('/403')
    if (error.value.status === 404) void router.replace('/404')
  } finally {
    if (mine === fetchSeq) loading.value = false
  }
}

/** 語彙。**一覧の取得と直列にしない**（5.4 と同じ）。失敗しても画面は止めない */
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

/** 取った範囲（取り直しを避ける） */
let calendarRange = ''

/**
 * 休日を時間軸の範囲ぶん取る（`ApiDesign.md` 5.8.5。1回 1,098日まで）。
 * **失敗しても土日だけで塗る**——休日が塗れないことは、画面を止める理由にならない。
 */
async function loadCalendar(): Promise<void> {
  const key = projectKey.value
  const tz = baseTz.value
  const from = ymd(tz, range.value.t0)
  const to = ymd(tz, range.value.t1 + DAY)
  const want = `${key}|${tz}|${from}|${to}`
  if (want === calendarRange) return
  calendarRange = want
  try {
    const out = new Map<string, ProjectCalendarDay>()
    const p = wallParts(tz, range.value.t0)
    const end = range.value.t1 + DAY
    for (let i = 0; ; i += 1098) {
      const a = wall(tz, p.y, p.m, p.d + i)
      if (a > end) break
      const b = Math.min(wall(tz, p.y, p.m, p.d + i + 1098), end)
      const res = await calendarApi.listDays(key, ymd(tz, a), ymd(tz, b))
      for (const d of res.days) out.set(d.day, d)
      if (b >= end) break
    }
    if (calendarRange !== want) return
    calendarDays.value = out
    calendarLoaded.value = true
  } catch {
    if (calendarRange !== want) return
    calendarLoaded.value = false
    calendarRange = ''
  }
}

// ── 行 ───────────────────────────────────────────────────────

/** いま（「今」の線と期限超過の判定）。1分ごとに進める */
const now = ref(Date.now())
let nowTimer: ReturnType<typeof setInterval> | undefined

const items = computed(() => toItems(tickets.value, now.value))
const range = computed(() => {
  const r = timeRange(items.value.values(), now.value)
  return { t0: r.t0, t1: r.t1 }
})

const groupVocabulary = computed<GroupVocabulary>(() => ({
  projectKey: projectKey.value,
  tickets: tickets.value,
  epics: epics.value,
  tags: tags.value,
  sprints: sprints.value,
  members: members.value,
  statuses: statuses.value,
}))

/**
 * ツリーの開閉は**バックログと共有する**（5.14。`pb.backlog_tree_collapsed`）。
 * 行グループの開閉も、バックログのセクション開閉（`pb.backlog_collapsed`）と同じ器に入れる。
 */
const TREE_COLLAPSE_KEY = 'pb.backlog_tree_collapsed'
const GROUP_COLLAPSE_KEY = 'pb.backlog_collapsed'
const treeCollapsed = ref<Set<number>>(new Set())
const groupCollapsed = ref<Set<string>>(new Set())
const groupScope = computed(() => `${projectKey.value}:${group.value}`)

function readStore(key: string): Record<string, unknown> {
  try {
    const raw = localStorage.getItem(key)
    const parsed: unknown = raw === null ? {} : JSON.parse(raw)
    return typeof parsed === 'object' && parsed !== null ? (parsed as Record<string, unknown>) : {}
  } catch {
    return {}
  }
}

function writeStore(key: string, value: Record<string, unknown>): void {
  try {
    localStorage.setItem(key, JSON.stringify(value))
  } catch {
    // 保存できなくても、いま開いている画面の開閉は効いている
  }
}

function loadCollapsed(): void {
  const tree = readStore(TREE_COLLAPSE_KEY)[projectKey.value]
  treeCollapsed.value = new Set(Array.isArray(tree) ? tree.filter((v): v is number => typeof v === 'number') : [])
  const grp = readStore(GROUP_COLLAPSE_KEY)[groupScope.value]
  groupCollapsed.value = new Set(Array.isArray(grp) ? grp.filter((v): v is string => typeof v === 'string') : [])
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

function toggleGroup(key: string): void {
  const next = new Set(groupCollapsed.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  groupCollapsed.value = next
  const store = readStore(GROUP_COLLAPSE_KEY)
  store[groupScope.value] = [...next]
  writeStore(GROUP_COLLAPSE_KEY, store)
}

const rows = computed<GRow[]>(() =>
  buildRows(tickets.value, items.value, group.value, groupVocabulary.value, treeCollapsed.value, groupCollapsed.value),
)
const rowOf = computed(() => rowIndexOf(rows.value))

function fullId(seq: number): string {
  return `${projectKey.value}-${seq}`
}

// ── 詳細ペイン（5.14「詳細ペイン」）──────────────────────────

const detailSeq = computed<number | null>(() => {
  const raw = route.params.seq
  const n = typeof raw === 'string' ? Number(raw) : Number.NaN
  return Number.isInteger(n) && n > 0 ? n : null
})

/** 詳細の中のリンクにも `from=gantt` を載せる（押した先もガントの上に開く） */
const detailLinkQuery = computed(() => ({ ...ganttQuery.value, from: 'gantt' }))

function openDetail(seq: number, e?: MouseEvent): void {
  const to = { path: `/p/${projectKey.value}/tickets/${seq}`, query: detailLinkQuery.value }
  if (e && (e.metaKey || e.ctrlKey || e.shiftKey)) {
    window.open(router.resolve(to).href, '_blank', 'noopener')
    return
  }
  void router.replace(to)
}

function closeDetail(): void {
  void router.replace({ path: `/p/${projectKey.value}/gantt`, query: ganttQuery.value })
}

/** 自分の予定を持たない親の、配下の期間（詳細の開始・終了に添える） */
const detailRollup = computed(() => {
  const it = detailSeq.value === null ? undefined : items.value.get(detailSeq.value)
  if (!it?.roll) return null
  return { start: ymd(baseTz.value, it.roll.s), end: ymd(baseTz.value, it.roll.e - 1) }
})

/** 日付を持たない相手（詳細の関連チケットに「線は描かない」を添える） */
const undatedSeqs = computed(() =>
  [...items.value.values()].filter((it) => it.s === null && it.e === null).map((it) => it.seq),
)

const GANTT_LINK_TYPES = new Set(['FS', 'SS', 'FF', 'SF', 'blocks'])

/**
 * 詳細で変更が確定した。**その行だけ差し替えて描き直す**（5.14「変更の反映」）。
 * 依存（ガントが描く種別）が増減していれば、線の材料が無いので一覧を取り直す。
 */
function onDetailUpdated(next: TicketDetail): void {
  tickets.value = tickets.value.map((t) => (t.seq === next.seq ? { ...t, ...next } : t))
  const mine = (l: { source_seq: number; target_seq: number }) => l.source_seq === next.seq || l.target_seq === next.seq
  const had = links.value.filter(mine).length
  const present = new Set(tickets.value.map((t) => t.seq))
  const has = next.links.filter((l) => GANTT_LINK_TYPES.has(l.link_type) && present.has(l.ticket.seq)).length
  if (had !== has) void loadTickets()
}

function onDetailCreated(): void {
  void loadTickets()
}

function onDetailDeleted(): void {
  closeDetail()
  void loadTickets()
}

/** 浮かせた詳細ペインの幅（5.14。320〜560px、`pb.gantt_detail_pane_w`） */
const PANE_KEY = 'pb.gantt_detail_pane_w'
const PANE_MIN = 320
const PANE_MAX = 560
const paneWidth = ref(380)

function loadPaneWidth(): void {
  try {
    const v = Number(localStorage.getItem(PANE_KEY))
    if (v >= PANE_MIN && v <= PANE_MAX) paneWidth.value = v
  } catch {
    // 既定の幅で開く
  }
}

function startPaneResize(e: PointerEvent): void {
  const grip = e.currentTarget as HTMLElement
  const x0 = e.clientX
  const w0 = paneWidth.value
  grip.setPointerCapture(e.pointerId)
  grip.classList.add('dragging')
  const move = (ev: PointerEvent) => {
    paneWidth.value = Math.min(PANE_MAX, Math.max(PANE_MIN, Math.round(w0 + (x0 - ev.clientX))))
    requestDraw()
  }
  const up = () => {
    grip.classList.remove('dragging')
    grip.removeEventListener('pointermove', move)
    grip.removeEventListener('pointerup', up)
    grip.removeEventListener('pointercancel', up)
    try {
      localStorage.setItem(PANE_KEY, String(paneWidth.value))
    } catch {
      // 幅は失われてよい情報である
    }
  }
  grip.addEventListener('pointermove', move)
  grip.addEventListener('pointerup', up)
  grip.addEventListener('pointercancel', up)
}

// ── 編集の状態（5.14「編集」）────────────────────────────────

/** ドラッグの操作を出すか。**`ticket.edit` を持つ人だけ**（持たない人にはカーソルも変えない） */
const canEdit = computed(() => auth.canInProject(projectKey.value, 'ticket.edit'))
/** 吸着の単位（分）。プロジェクトの設定（5.9.6「ガントの吸着」） */
const snapUnit = computed(() => readGanttSnap(projectStore.current?.settings))

/** ドラッグ中・応答待ちの予定。描画はこの値で帯と依存線を描き直す */
const override = shallowRef<Map<number, Plan>>(new Map())
/** 応答を待っているチケット（破線で描き、掴めなくする） */
const pending = shallowRef<Set<number>>(new Set())
/** 編集の結果を出す通知の行（本体の上。トーストにしない。6.4） */
const editNotice = ref('')

// ── 描画 ─────────────────────────────────────────────────────

const chart = useTemplateRef<HTMLDivElement>('chart')
const canvas = useTemplateRef<HTMLDivElement>('canvas')
const svg = useTemplateRef<SVGSVGElement>('svg')

/** 1日の幅（px）。**連続量として持ち、段階はその途中の止まり先である**（5.14） */
const ppd = ref(PPD_DAY)
/** 縦のスクロール量（行の仮想スクロールに使う） */
const scrollTop = ref(0)
const viewHeight = ref(0)
const hover = ref<number | null>(null)
let cursor: { x: number; y: number } | null = null
let edges: EdgeHit[] = []

const geom = computed(() => geometry(baseTz.value, viewTz.value, ppd.value, now.value))

/**
 * 横幅の上限。**要素の幅がブラウザの上限（Chrome で約 3,300万px）を超えない**ように、
 * 時間軸が長いときは最大の拡大を抑える。
 */
const ppdMax = computed(() => Math.min(PPD_MAX, 30_000_000 / ((range.value.t1 - range.value.t0) / DAY)))

/** 帯を見せられる右端（浮かせた詳細ペインの左まで。5.14） */
function freeRight(W: number): number {
  return detailSeq.value !== null ? W - paneWidth.value - 12 : W
}

function sizeCanvas(): void {
  const c = canvas.value
  if (!c) return
  c.style.width = `${((range.value.t1 - range.value.t0) / DAY) * ppd.value}px`
  c.style.height = `${geom.value.top + rows.value.length * RH + 40}px`
}

let raf = 0

function requestDraw(): void {
  if (raf === 0) raf = requestAnimationFrame(draw)
}

/** 土日祝（5.14「土日祝」）。何日が休日かはプロジェクトの暦、塗り方は見る人のタイムゾーン */
function dayKind(d: DayTick): DayKind {
  const weekend: DayKind = d.wd === 6 ? 'sat' : d.wd === 0 ? 'sun' : ''
  if (!calendarLoaded.value) return weekend
  const entry = calendarDays.value.get(`${d.y}-${String(d.m).padStart(2, '0')}-${String(d.d).padStart(2, '0')}`)
  if (!entry || !entry.is_holiday) return ''
  if (entry.reason === 'weekend') return weekend === '' ? 'sun' : weekend
  return 'hol'
}

function holidayName(d: DayTick): string | null {
  if (!calendarLoaded.value) return null
  const entry = calendarDays.value.get(`${d.y}-${String(d.m).padStart(2, '0')}-${String(d.d).padStart(2, '0')}`)
  if (!entry || !entry.is_holiday || entry.reason === 'weekend') return null
  return entry.override?.name ?? entry.events.find((e) => e.kind === 'holiday')?.name ?? null
}

const gSprints = computed<GSprint[]>(() =>
  sprints.value
    .filter((s) => s.start_at !== null && s.end_at !== null)
    .map((s) => ({ name: s.name, s: s.start_at!, e: s.end_at!, status: s.status })),
)

const renderText = {
  get under() { return uiText('配下') },
  get noDate() { return uiText('日付なし') },
  days: (n: number) => uiText('{value0}日', { value0: n }),
}

function draw(): void {
  raf = 0
  const el = chart.value
  const s = svg.value
  if (!el || !s) return
  sizeCanvas()
  const W = el.clientWidth
  const H = el.clientHeight
  s.setAttribute('width', String(W))
  s.setAttribute('height', String(H))
  s.setAttribute('viewBox', `0 0 ${W} ${H}`)
  const out = render({
    W,
    H,
    sl: el.scrollLeft,
    st: el.scrollTop,
    ppd: ppd.value,
    T0: range.value.t0,
    T1: range.value.t1,
    rows: rows.value,
    rowOf: rowOf.value,
    items: items.value,
    links: links.value,
    sprints: gSprints.value,
    dayKind,
    holidayName,
    baseTz: baseTz.value,
    viewTz: viewTz.value,
    now: now.value,
    hover: hover.value,
    sel: detailSeq.value,
    cursor,
    freeRight: freeRight(W),
    idOf: fullId,
    text: renderText,
    edit: editView(),
  })
  s.innerHTML = out.svg
  edges = out.edges
}

// 材料が変わったら描き直す（描画は rAF で1回にまとめる）
watch(
  [rows, links, gSprints, calendarDays, calendarLoaded, ppd, hover, detailSeq, paneWidth, now, baseTz, range, override, pending],
  () => requestDraw(),
)

// ── 拡大縮小（5.14「拡大縮小」）──────────────────────────────

/** 1日の幅を変える。**`anchorPx` の位置の時刻を動かさない**（ポインタを中心に拡大する） */
function setPpd(next: number, anchorPx: number): void {
  const el = chart.value
  if (!el) return
  const t = range.value.t0 + ((el.scrollLeft + anchorPx) / ppd.value) * DAY
  ppd.value = Math.min(ppdMax.value, Math.max(PPD_MIN, next))
  sizeCanvas()
  el.scrollLeft = ((t - range.value.t0) / DAY) * ppd.value - anchorPx
  requestDraw()
}

const reducedMotion = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches
let anim = 0

/** 段階へ移る（150ms、`ease-out`。動きを減らす設定では即時） */
function animateTo(target: number, anchorPx: number): void {
  cancelAnimationFrame(anim)
  if (reducedMotion) {
    setPpd(target, anchorPx)
    return
  }
  const from = Math.log(ppd.value)
  const to = Math.log(target)
  const t0 = performance.now()
  const step = (t: number) => {
    const k = Math.min(1, (t - t0) / 150)
    const e = 1 - Math.pow(1 - k, 3)
    setPpd(Math.exp(from + (to - from) * e), anchorPx)
    if (k < 1) anim = requestAnimationFrame(step)
  }
  anim = requestAnimationFrame(step)
}

/** 段階のボタンと `+` / `-` の中心：「今」が見えていれば「今」、見えていなければ中央 */
function levelAnchor(): number {
  const el = chart.value
  if (!el) return 0
  const xn = ((now.value - range.value.t0) / DAY) * ppd.value - el.scrollLeft
  return xn >= 0 && xn <= freeRight(el.clientWidth) ? xn : el.clientWidth / 2
}

const currentLevel = computed(() => nearestLevel(ppd.value))

function pickLevel(i: number): void {
  const l = ZOOM_LEVELS[i]
  if (l) animateTo(l.ppd, levelAnchor())
}

function stepZoom(dir: 1 | -1): void {
  const l = stepLevel(ppd.value, dir)
  if (l) animateTo(l.ppd, levelAnchor())
}

let lastStep = 0

/**
 * `⌘` / `Ctrl` ＋ ホイールで拡大縮小する。**ブラウザの拡大を横取りする**（5.14）——
 * トラックパッドのピンチも `Ctrl` ＋ ホイールとして届く。`⇧` を足すと段階ごと。
 */
function onChartWheel(e: WheelEvent): void {
  if (!(e.ctrlKey || e.metaKey)) return
  e.preventDefault()
  const el = chart.value
  if (!el) return
  const px = e.clientX - el.getBoundingClientRect().left
  if (e.shiftKey) {
    const t = performance.now()
    if (t - lastStep < 220) return
    lastStep = t
    const dy = e.deltaY || e.deltaX
    if (!dy) return
    const l = stepLevel(ppd.value, dy > 0 ? -1 : 1)
    if (l) animateTo(l.ppd, px)
    return
  }
  setPpd(ppd.value * Math.exp(-e.deltaY * 0.0022), px)
}

/** 行（ツリー）の上のホイールは本体へ送る（縦のスクロールを1つにする） */
function onTreeWheel(e: WheelEvent): void {
  const el = chart.value
  if (!el || e.ctrlKey || e.metaKey) return
  e.preventDefault()
  el.scrollTop += e.deltaY
  el.scrollLeft += e.deltaX
}

/** `[今日]`：「今」を、帯を見せられる幅の 30% の位置へ送る */
function toNow(): void {
  const el = chart.value
  if (!el) return
  el.scrollLeft = ((now.value - range.value.t0) / DAY) * ppd.value - freeRight(el.clientWidth) * 0.3
  requestDraw()
}

/** 帯を、帯を見せられる幅の 30% の位置まで送る（端の札・詳細を開いたとき） */
function scrollToSeq(seq: number, smooth: boolean): void {
  const el = chart.value
  const it = items.value.get(seq)
  if (!el || !it) return
  const sp = spanOf(it)
  if (!sp) return
  const at = sp.s ?? sp.e!
  const left = ((at - range.value.t0) / DAY) * ppd.value - freeRight(el.clientWidth) * 0.3
  el.scrollTo({ left, behavior: smooth && !reducedMotion ? 'smooth' : 'auto' })
}

/** 詳細を開いたとき、選んだ帯がペインの下に隠れるなら見える位置まで送る（5.14） */
function revealUnderPane(seq: number): void {
  const el = chart.value
  const it = items.value.get(seq)
  if (!el || !it) return
  const sp = spanOf(it)
  if (!sp) return
  const x = (((sp.s ?? sp.e!) - range.value.t0) / DAY) * ppd.value - el.scrollLeft
  const xe = (((sp.e ?? sp.s!) - range.value.t0) / DAY) * ppd.value - el.scrollLeft
  if (x > freeRight(el.clientWidth) - 60 || xe < 0) scrollToSeq(seq, false)
}

// ── ポインタ ─────────────────────────────────────────────────

function localPoint(e: MouseEvent): { x: number; y: number } {
  const r = chart.value!.getBoundingClientRect()
  return { x: e.clientX - r.left, y: e.clientY - r.top }
}

function rowAtY(y: number): number | null {
  const i = Math.floor((y - geom.value.top + (chart.value?.scrollTop ?? 0)) / RH)
  return y >= geom.value.top && i >= 0 && i < rows.value.length ? i : null
}

function edgeAt(p: { x: number; y: number }): EdgeHit | undefined {
  return edges.find((c) => p.x >= c.x && p.x <= c.x + c.w && p.y >= c.y && p.y <= c.y + c.h)
}

function onChartMove(e: PointerEvent): void {
  const p = localPoint(e)
  cursor = p
  if (drag) {
    dragMove(p, e.altKey)
    requestDraw()
    return
  }
  const i = rowAtY(p.y)
  const r = i === null ? undefined : rows.value[i]
  hover.value = r?.kind === 'ticket' ? r.item.seq : null
  const h = edgeAt(p) ? null : hitAt(p)
  hotHandle = h?.type === 'handle' ? { seq: h.seq, end: h.end } : null
  chart.value!.style.cursor = edgeAt(p)
    ? 'pointer'
    : h?.type === 'handle' || h?.type === 'create'
      ? 'crosshair'
      : h?.type === 'grab'
        ? h.kind === 'move' ? 'grab' : 'ew-resize'
        : ''
  requestDraw()
}

function onChartLeave(): void {
  if (drag) return
  cursor = null
  hover.value = null
  hotHandle = null
  requestDraw()
}

function onChartClick(e: MouseEvent): void {
  // ドラッグした後の click と、pointerup で処理済みの click は捨てる
  if (suppressClick) {
    suppressClick = false
    return
  }
  clickAt(e)
}

/** 押した場所を開く（端の札なら送り、行なら詳細） */
function clickAt(e: MouseEvent): void {
  const p = localPoint(e)
  const edge = edgeAt(p)
  if (edge) {
    scrollToSeq(edge.seq, true)
    return
  }
  const i = rowAtY(p.y)
  const r = i === null ? undefined : rows.value[i]
  if (r?.kind === 'ticket') openDetail(r.item.seq, e)
}

// ── 編集（5.14「編集」）──────────────────────────────────────

/** 掴む前に動かしてよい距離。**3px 未満の動きはクリック**として詳細を開く */
const CLICK_SLOP = 3
/** 本体の左右の端に寄せると横へ送る幅 */
const AUTO_SCROLL_EDGE = 32

type Hit =
  | { type: 'handle'; seq: number; end: End }
  | { type: 'grab'; seq: number; kind: 'move' | 'start' | 'end' }
  | { type: 'create'; seq: number }

type Drag =
  | { mode: 'plan'; seq: number; op: EditOp; x0: number; y0: number; started: boolean; plan: Plan | null; alt: boolean }
  | {
      mode: 'link'
      seq: number
      from: End
      x0: number
      y0: number
      started: boolean
      target: { seq: number; end: End; ok: boolean; text: string } | null
      at: { x: number; y: number }
    }

let drag: Drag | null = null
let dragPointer = -1
let suppressClick = false
/** ポインタが載っている取っ手（濃く描く） */
let hotHandle: { seq: number; end: End } | null = null

/** 画面上の x の時刻 */
function timeAtX(x: number): number {
  return range.value.t0 + (((chart.value?.scrollLeft ?? 0) + x) / ppd.value) * DAY
}

/** 時刻の画面上の x */
function xAt(t: number): number {
  return ((t - range.value.t0) / DAY) * ppd.value - (chart.value?.scrollLeft ?? 0)
}

/** 編集中の予定を当てた item（描画と同じ値で当たりを取る） */
function liveItem(seq: number): GItem | undefined {
  const it = items.value.get(seq)
  const p = override.value.get(seq)
  return it && p ? { ...it, s: p.s, e: p.e, allDay: p.allDay } : it
}

/** 帯の両端の画面上の位置。自分の予定が無ければ null（寸法線は掴めない） */
function spanX(it: GItem): { xs: number; xf: number; has: { s: boolean; e: boolean } } | null {
  if (it.s === null && it.e === null) return null
  const xs = xAt((it.s ?? it.e)!)
  let xf = xAt((it.e ?? it.s)!)
  if (it.s !== null && it.e !== null && xf - xs < 3) xf = xs + 3
  return { xs, xf, has: { s: it.s !== null, e: it.e !== null } }
}

function rowCenterY(i: number): number {
  return geom.value.top + i * RH - (chart.value?.scrollTop ?? 0) + RH / 2
}

/** ポインタの下で何ができるか（5.14「編集」の表）。編集できなければ常に null */
function hitAt(p: { x: number; y: number }): Hit | null {
  if (!canEdit.value || p.x > freeRight(chart.value?.clientWidth ?? 0)) return null
  const i = rowAtY(p.y)
  const r = i === null ? undefined : rows.value[i]
  if (r?.kind !== 'ticket' || pending.value.has(r.item.seq)) return null
  const it = liveItem(r.item.seq)!
  const sx = spanX(it)
  if (!sx) return { type: 'create', seq: it.seq }
  const y = rowCenterY(i!)
  for (const h of handlesOf(sx.xs, sx.xf, sx.has)) {
    // 当たりは半径＋2px（端の変更の当たり＝外側 3px と重ならない）
    if (Math.hypot(p.x - h.x, p.y - y) <= HANDLE_R + 2) return { type: 'handle', seq: it.seq, end: h.end }
  }
  if (Math.abs(p.y - y) > 9) return null
  const kind = grabAt(p.x, sx.xs, sx.xf, sx.has, MS_TAIL)
  return kind === null || kind === 'create' ? null : { type: 'grab', seq: it.seq, kind }
}

function onChartPointerDown(e: PointerEvent): void {
  // 前の操作の click が来なかったとき（Esc で取り消した等）に、抑止を持ち越さない
  suppressClick = false
  if (e.button !== 0 || drag) return
  const p = localPoint(e)
  if (edgeAt(p)) return
  const h = hitAt(p)
  if (!h) return
  const t = timeAtX(p.x)
  if (h.type === 'handle') {
    drag = { mode: 'link', seq: h.seq, from: h.end, x0: p.x, y0: p.y, started: false, target: null, at: p }
  } else {
    const it = liveItem(h.seq)!
    const kind = h.type === 'create' ? 'create' : h.kind
    drag = {
      mode: 'plan',
      seq: h.seq,
      op: { kind, s0: it.s, e0: it.e, allDay0: it.allDay, t0: t },
      x0: p.x,
      y0: p.y,
      started: false,
      plan: null,
      alt: e.altKey,
    }
  }
  dragPointer = e.pointerId
  // 既定の動作（文字の選択）を止める。止めないと、なぞった先のツリーの文字が選択される
  e.preventDefault()
  chart.value!.setPointerCapture(e.pointerId)
}

/** ドラッグを進める。3px を超えるまでは始めない（クリックのまま） */
function dragMove(p: { x: number; y: number }, alt: boolean): void {
  const d = drag
  if (!d) return
  if (!d.started) {
    if (Math.hypot(p.x - d.x0, p.y - d.y0) < CLICK_SLOP) return
    d.started = true
    suppressClick = true
    editNotice.value = ''
  }
  if (d.mode === 'plan') {
    d.alt = alt
    const plan = planAt(d.op, timeAtX(p.x), baseTz.value, alt ? null : snapUnit.value)
    d.plan = plan
    const next = new Map(override.value)
    next.set(d.seq, plan)
    override.value = next
    chart.value!.style.cursor = d.op.kind === 'move' ? 'grabbing' : d.op.kind === 'create' ? 'crosshair' : 'ew-resize'
  } else {
    d.at = p
    d.target = linkTargetAt(d, p)
    chart.value!.style.cursor = d.target && !d.target.ok ? 'not-allowed' : 'crosshair'
  }
  autoScroll(p.x)
}

/** 依存の相手（5.14「依存を引く」）。ポインタが載っている行の帯で、落とせるかと理由も返す */
function linkTargetAt(d: Extract<Drag, { mode: 'link' }>, p: { x: number; y: number }): Extract<Drag, { mode: 'link' }>['target'] {
  const i = rowAtY(p.y)
  const r = i === null ? undefined : rows.value[i]
  if (r?.kind !== 'ticket') return null
  const it = liveItem(r.item.seq)!
  const sx = spanX(it)
  if (!sx) {
    // 日付の無い行（寸法線だけの親を含む）は、依存線を繋がない
    return { seq: it.seq, end: 'S', ok: false, text: uiText('日付の無いチケットには引けません') }
  }
  const lo = sx.has.s ? sx.xs - 8 : sx.xf - MS_TAIL
  const hi = sx.has.e ? sx.xf + 8 : sx.xs + MS_TAIL
  if (p.x < lo || p.x > hi) return null
  const end = endAt(p.x, sx.xs, sx.xf, sx.has)
  const type = depTypeOf(d.from, end)
  if (it.seq === d.seq) return { seq: it.seq, end, ok: false, text: uiText('同じチケットには引けません') }
  if (links.value.some((l) => l.source_seq === d.seq && l.target_seq === it.seq && l.link_type === type)) {
    return { seq: it.seq, end, ok: false, text: uiText('{type} の依存はすでにあります', { type }) }
  }
  if (wouldCycle(links.value, d.seq, it.seq)) {
    return { seq: it.seq, end, ok: false, text: uiText('依存が輪になるため引けません') }
  }
  return { seq: it.seq, end, ok: true, text: `${type} ${fullId(d.seq)} → ${fullId(it.seq)}` }
}

let scrollRaf = 0
let scrollSpeed = 0

/** 本体の左右の端 32px に寄せると横へ送る（離れた日付へ運ぶため） */
function autoScroll(x: number): void {
  const el = chart.value
  if (!el) return
  const right = freeRight(el.clientWidth)
  scrollSpeed = x < AUTO_SCROLL_EDGE ? -Math.ceil((AUTO_SCROLL_EDGE - x) / 3) : x > right - AUTO_SCROLL_EDGE ? Math.ceil((x - right + AUTO_SCROLL_EDGE) / 3) : 0
  if (scrollSpeed === 0 || scrollRaf !== 0) return
  const step = () => {
    scrollRaf = 0
    if (!drag?.started || scrollSpeed === 0 || !cursor) return
    el.scrollLeft += scrollSpeed
    dragMove(cursor, drag.mode === 'plan' ? drag.alt : false)
    requestDraw()
    scrollRaf = requestAnimationFrame(step)
  }
  scrollRaf = requestAnimationFrame(step)
}

function endDrag(): Drag | null {
  const d = drag
  drag = null
  scrollSpeed = 0
  cancelAnimationFrame(scrollRaf)
  scrollRaf = 0
  if (dragPointer >= 0 && chart.value?.hasPointerCapture(dragPointer)) chart.value.releasePointerCapture(dragPointer)
  dragPointer = -1
  if (chart.value) chart.value.style.cursor = ''
  return d
}

function onChartPointerUp(e: PointerEvent): void {
  if (!drag || e.pointerId !== dragPointer) return
  const d = endDrag()
  if (!d?.started) {
    // **動かさずに離したら、ここでクリックとして扱う。** 押した SVG の要素は描き直しで
    // DOM から消えており、ポインタを捕捉していると Chrome が click を出さないことがある
    // （押した要素と離した要素の共通の祖先が取れない）。出たときの click は捨てる
    suppressClick = true
    clickAt(e)
    return
  }
  if (d.mode === 'plan') {
    if (d.plan) void commitPlan(d.seq, d.plan)
  } else if (d.target?.ok) {
    void commitLink(d.seq, d.target.seq, depTypeOf(d.from, d.target.end))
  }
  requestDraw()
}

/** `Esc` で取り消す（掴む前の位置に戻り、何も送らない） */
function cancelDrag(): void {
  const d = endDrag()
  if (d?.mode === 'plan') dropOverride(d.seq)
  requestDraw()
}

function onPointerCancel(): void {
  if (drag) cancelDrag()
}

function dropOverride(seq: number): void {
  if (!override.value.has(seq)) return
  const next = new Map(override.value)
  next.delete(seq)
  override.value = next
}

/** `Alt`（Mac では `Option`）の押し離しは、ドラッグの途中でも吸着に効かせる */
function onEditKey(e: KeyboardEvent): void {
  if (!drag) return
  if (e.type === 'keydown' && e.key === 'Escape') {
    e.preventDefault()
    cancelDrag()
    return
  }
  if (e.key === 'Alt' && drag.mode === 'plan' && drag.started && cursor) {
    dragMove(cursor, e.type === 'keydown')
    requestDraw()
  }
}

/** 一覧の行を、サーバの応答で差し替える（「変更の反映」と同じ） */
function replaceTicket(next: TicketDetail): void {
  tickets.value = tickets.value.map((t) => (t.seq === next.seq ? { ...t, ...next } : t))
}

const detailPane = useTemplateRef<InstanceType<typeof TicketDetailPane>>('detailPane')

/**
 * 予定を送る（5.14「送り方と見え方」）。**応答までは新しい位置を破線で描き**、
 * 成功したら行を差し替える。409 は上書きせず、取り直して最新の位置に戻す。
 */
async function commitPlan(seq: number, plan: Plan): Promise<void> {
  const t = tickets.value.find((x) => x.seq === seq)
  if (!t) {
    dropOverride(seq)
    return
  }
  if (t.start_at === plan.s && t.due_at === plan.e && t.all_day === plan.allDay) {
    dropOverride(seq)
    return
  }
  pending.value = new Set(pending.value).add(seq)
  try {
    const next = await ticketsApi.updateTicket(projectKey.value, seq, t.version, {
      start_at: plan.s,
      due_at: plan.e,
      all_day: plan.allDay,
    })
    replaceTicket(next)
    // 詳細で同じチケットを開いていれば、応答を渡す（渡さないと次の編集が 409 になる）
    if (detailSeq.value === seq) detailPane.value?.replace(next)
  } catch (e) {
    const err = toApiError(e)
    if (err.status === 409) {
      try {
        const fresh = await ticketsApi.getTicket(projectKey.value, seq)
        replaceTicket(fresh)
        if (detailSeq.value === seq) detailPane.value?.replace(fresh)
      } catch {
        // 取り直せなくても、上書きしていないことは変わらない
      }
      editNotice.value = uiText('{id} は他の人が先に更新していました。最新の予定を表示しています', { id: fullId(seq) })
    } else {
      editNotice.value = err.details[0]?.message ?? err.message
    }
  } finally {
    const next = new Set(pending.value)
    next.delete(seq)
    pending.value = next
    dropOverride(seq)
  }
}

/** 依存を作る（5.14「依存を引く」）。成功したら一覧を取り直す（「変更の反映」） */
async function commitLink(source: number, target: number, type: string): Promise<void> {
  try {
    await linksApi.createLink(projectKey.value, source, {
      target_seq: target,
      link_type: type as linksApi.LinkType,
      lag_days: 0,
    })
    await loadTickets()
    if (detailSeq.value === source || detailSeq.value === target) detailPane.value?.reload()
  } catch (e) {
    const err = toApiError(e)
    editNotice.value = err.details[0]?.message ?? err.message
  }
}

// ── Excel 出力（5.14「Excel 出力」）───────────────────────────

const exporting = ref(false)

/**
 * いま見えているガントを `.xlsx` で落とす。**作るコードは押したときだけ読み込む**
 * （動的 `import()`。Vite が別のファイルに分ける）——ガントを開いたときの読み込みを増やさない。
 * 失敗したら、編集と同じ通知の行に出す（6.4）。
 */
async function exportExcel(): Promise<void> {
  if (exporting.value) return
  exporting.value = true
  editNotice.value = ''
  try {
    const { buildGanttXlsx } = await import('../lib/gantt/excel')
    const { blob, name } = await buildGanttXlsx({
      // 日付と文言の関数は渡す（excel.ts の冒頭。静的に引くと Vue の本体が別ファイルに割れる）
      env: { time: ganttTime, text: uiText },
      projectKey: projectKey.value,
      rows: rows.value,
      links: links.value,
      sprints: gSprints.value,
      // 休日タブに並べる日（祝日と手動の休日。週末だけの日は WEEKDAY で塗るので入れない）
      holidays: [...calendarDays.value.values()]
        .filter((d) => d.is_holiday && d.reason !== 'weekend')
        .sort((a, b) => (a.day < b.day ? -1 : a.day > b.day ? 1 : 0))
        .map((d) => ({ day: d.day, name: d.override?.name ?? d.events.find((ev) => ev.kind === 'holiday')?.name ?? '' })),
      dayKind,
      calStyle: calStyle.value,
      baseTz: baseTz.value,
      viewTz: viewTz.value,
      now: now.value,
      hue: ui.hue,
      idOf: fullId,
      statusText: (it) => statusLabel(it.ticket.status.key, it.ticket.status.name),
      truncated: truncated.value ? { shown: tickets.value.length, total: total.value } : null,
    })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = name
    document.body.appendChild(a)
    a.click()
    a.remove()
    setTimeout(() => URL.revokeObjectURL(url), 10_000)
  } catch (e) {
    editNotice.value = uiText('Excel を作れませんでした：{value0}', { value0: e instanceof Error ? e.message : String(e) })
  } finally {
    exporting.value = false
  }
}

/** 描画に渡す編集の見え方 */
function editView(): EditView | undefined {
  if (!canEdit.value && override.value.size === 0) return undefined
  const d = drag?.started ? drag : null
  let tag: EditView['tag'] = null
  let link: EditView['link'] = null
  if (d?.mode === 'plan' && d.plan) {
    const it = liveItem(d.seq)
    if (it) {
      const label = rangeLabel(it, baseTz.value, viewTz.value)
      const toTimed = (d.op.kind === 'create' || d.op.allDay0) && !d.plan.allDay
      tag = { seq: d.seq, text: toTimed ? `${label} · ${uiText('時刻付きになります')}` : label }
    }
  } else if (d?.mode === 'link') {
    const it = liveItem(d.seq)
    const i = rowOf.value.get(d.seq)
    const sx = it ? spanX(it) : null
    if (sx && i !== undefined) {
      const x0 = d.from === 'S' ? sx.xs - 9 : sx.xf + 9
      let ring: { x: number; y: number } | null = null
      if (d.target && d.target.ok) {
        const ti = rowOf.value.get(d.target.seq)
        const tsx = spanX(liveItem(d.target.seq)!)
        if (ti !== undefined && tsx) ring = { x: d.target.end === 'S' ? tsx.xs : tsx.xf, y: rowCenterY(ti) }
      }
      link = { x0, y0: rowCenterY(i), x1: d.at.x, y1: d.at.y, ok: d.target?.ok ?? true, text: d.target?.text ?? '', ring }
    }
  }
  const handleSeq = d?.mode === 'link' ? d.seq : drag ? null : hover.value
  return {
    override: override.value,
    ghost: d?.mode === 'plan' && d.op.kind !== 'create' ? d.seq : null,
    pending: pending.value,
    tag,
    handles: canEdit.value && handleSeq !== null && !pending.value.has(handleSeq)
      ? { seq: handleSeq, hot: hotHandle?.seq === handleSeq ? hotHandle.end : d?.mode === 'link' ? d.from : null }
      : null,
    link,
  }
}

function onChartScroll(): void {
  scrollTop.value = chart.value?.scrollTop ?? 0
  requestDraw()
}

// ── 行（ツリー）の仮想スクロール ─────────────────────────────

/** 見えている行だけを描く（5.14「大量の行」）。上下に数行の余りを取る */
const visibleRows = computed(() => {
  const top = geom.value.top
  const i0 = Math.max(0, Math.floor(scrollTop.value / RH) - 3)
  const i1 = Math.min(rows.value.length, Math.ceil((scrollTop.value + viewHeight.value - top) / RH) + 3)
  const out: { row: GRow; index: number; y: number }[] = []
  for (let i = i0; i < i1; i++) out.push({ row: rows.value[i]!, index: i, y: i * RH - scrollTop.value })
  return out
})

function rowKey(r: GRow, index: number): string {
  return r.kind === 'group' ? `g:${r.key}` : `t:${r.item.seq}:${index}`
}

// ── キーボード（5.14「拡大縮小」の `+` / `-`）─────────────────

function onKeydown(e: KeyboardEvent): void {
  if (e.metaKey || e.ctrlKey || e.altKey) return
  const el = e.target as HTMLElement | null
  if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return
  if (e.key === '+') {
    e.preventDefault()
    stepZoom(1)
  } else if (e.key === '-') {
    e.preventDefault()
    stepZoom(-1)
  }
}

// ── 起動と追随 ───────────────────────────────────────────────

let resizeObserver: ResizeObserver | null = null
let firstPlaced = false

onMounted(() => {
  loadPaneWidth()
  loadCollapsed()
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('keydown', onEditKey)
  window.addEventListener('keyup', onEditKey)
  document.addEventListener('pointerdown', onDocumentPointerDown)
  void projectStore.fetchCurrent(projectKey.value)
  void loadVocabulary()
  if (!restoreGroupAxis()) void loadTickets()
  nowTimer = setInterval(() => (now.value = Date.now()), 60_000)
  resizeObserver = new ResizeObserver(() => {
    viewHeight.value = chart.value?.clientHeight ?? 0
    requestDraw()
  })
  if (chart.value) resizeObserver.observe(chart.value)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('keydown', onEditKey)
  window.removeEventListener('keyup', onEditKey)
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  cancelAnimationFrame(scrollRaf)
  clearInterval(nowTimer)
  cancelAnimationFrame(raf)
  cancelAnimationFrame(anim)
  resizeObserver?.disconnect()
  projectStore.clearCurrent()
})

// 最初に取れたら「今」を `[今日]` と同じ位置に置く（5.14）
watch(loaded, async (v) => {
  if (!v || firstPlaced) return
  firstPlaced = true
  await nextTick()
  viewHeight.value = chart.value?.clientHeight ?? 0
  sizeCanvas()
  toNow()
  if (detailSeq.value !== null) revealUnderPane(detailSeq.value)
})

// 詳細を開いたら、選んだ帯がペインの下に隠れないように送る
watch(detailSeq, async (seq) => {
  if (seq === null) return
  await nextTick()
  revealUnderPane(seq)
})

// 取得の条件（`group` と `from` を除くクエリ）が変わったら取り直す。**詳細の開閉では取り直さない**
const fetchKey = computed(() =>
  Object.entries(route.query)
    .filter((e): e is [string, string] => typeof e[1] === 'string' && e[0] !== 'group' && e[0] !== 'from')
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([k, v]) => `${k}=${v}`)
    .join('&'),
)

watch(fetchKey, () => {
  if (route.params.key === undefined) return
  void loadTickets()
})

watch(group, () => loadCollapsed())

// 時間軸の範囲や基準タイムゾーンが変わったら、休日を取り直す
watch([range, baseTz], () => {
  if (loaded.value) void loadCalendar()
})

watch(projectKey, (key) => {
  if (key === '') return
  tags.value = []
  sprints.value = []
  epics.value = []
  calendarRange = ''
  calendarLoaded.value = false
  firstPlaced = false
  loaded.value = false
  loadCollapsed()
  void projectStore.fetchCurrent(key)
  void loadVocabulary()
  if (!restoreGroupAxis()) void loadTickets()
})

/** 土日祝の塗り方の慣習（5.14）。日本と韓国は 土＝青・日祝＝赤、それ以外は 土日＝灰 */
const calStyle = computed(() => (['Asia/Tokyo', 'Asia/Seoul'].includes(viewTz.value) ? 'east' : 'other'))

const zoneLabel = computed(() => ({
  base: tzShort(baseTz.value, now.value),
  view: tzShort(viewTz.value, now.value),
}))

const epicLinkQuery = computed(() => ({ from: 'gantt' }))
</script>

<template>
  <div class="gantt" :data-cal="calStyle">
    <PageHeader :title="$ui('ガント')">
      <template #actions>
        <div class="gantt-tools">
          <div class="gantt-filter">
            <button
              ref="filterButton"
              type="button"
              class="secondary"
              :aria-expanded="filtersOpen"
              aria-controls="gantt-filter-panel"
              @click="filtersOpen = !filtersOpen"
            >
              {{ $ui('絞り込み') }}<template v-if="filterCount > 0">（{{ filterCount }}）</template> ▾
            </button>
            <div v-show="filtersOpen" id="gantt-filter-panel" ref="filterPanel" class="gantt-filter-panel">
              <div class="filter">
                <span class="filter-label" aria-hidden="true">{{ $ui('エピック') }}</span>
                <EpicFilter
                  :epics="epics"
                  :selected="epicSeqs"
                  :project-key="projectKey"
                  :link-query="epicLinkQuery"
                  @update="setQuery({ parent: $event.join(',') })"
                />
              </div>
              <label class="filter">
                <span class="filter-label">{{ $ui('タグ') }}</span>
                <select :value="queryValue('tag')" @change="setQuery({ tag: ($event.target as HTMLSelectElement).value })">
                  <option value="">{{ $ui('すべて') }}</option>
                  <option value="none">{{ $ui('未分類') }}</option>
                  <option v-for="t in tags" :key="t.id" :value="t.id">{{ t.name }}</option>
                </select>
              </label>
              <label class="filter">
                <span class="filter-label">{{ $ui('状態') }}</span>
                <select :value="stateValue" @change="setState(($event.target as HTMLSelectElement).value)">
                  <option value="">{{ $ui('すべて') }}</option>
                  <optgroup :label="$ui('進み具合')">
                    <option value="open:true">{{ $ui('未完了') }}</option>
                    <option value="open:false">{{ $ui('完了') }}</option>
                  </optgroup>
                  <optgroup :label="$ui('区分')">
                    <option v-for="c in statusCategoryOrder" :key="c" :value="`status_category:${c}`">
                      {{ statusCategoryLabels[c] }}
                    </option>
                  </optgroup>
                  <optgroup :label="$ui('ステータス')">
                    <option v-for="s in statuses" :key="s.key" :value="`status:${s.key}`">
                      {{ statusLabel(s.key, s.name) }}
                    </option>
                  </optgroup>
                </select>
              </label>
              <div class="filter">
                <span class="filter-label" aria-hidden="true">{{ $ui('予定期間') }}</span>
                <PlannedPeriodFilter
                  :from="queryValue('planned_from')"
                  :to="queryValue('planned_to')"
                  @update="setQuery({ planned_from: $event.from, planned_to: $event.to })"
                />
              </div>
              <label class="check">
                <input
                  type="checkbox"
                  :checked="queryValue('staged') === 'true'"
                  @change="setQuery({ staged: ($event.target as HTMLInputElement).checked ? 'true' : '' })"
                />
                {{ $ui('オンステージ') }}
              </label>
              <label class="check">
                <input
                  type="checkbox"
                  :checked="queryValue('sprint') === 'active'"
                  @change="setQuery({ sprint: ($event.target as HTMLInputElement).checked ? 'active' : '' })"
                />
                {{ $ui('スプリント中') }}
              </label>
              <button type="button" class="secondary" :disabled="filterCount === 0" @click="clearFilters">
                {{ $ui('解除') }}
              </button>
            </div>
          </div>

          <label class="gantt-group">
            <span class="filter-label">{{ $ui('グループ化') }}</span>
            <select :value="group" @change="setGroup(($event.target as HTMLSelectElement).value)">
              <option v-for="axis in GROUP_AXES" :key="axis" :value="axis">{{ groupAxisLabel(axis) }}</option>
            </select>
          </label>

          <div class="gantt-seg" role="group" :aria-label="$ui('拡大縮小の段階')">
            <button
              v-for="(l, i) in ZOOM_LEVELS"
              :key="l.key"
              type="button"
              :aria-pressed="currentLevel === i"
              @click="pickLevel(i)"
            >
              {{ { minute: $ui('分'), hour: $ui('時'), day: $ui('日'), week: $ui('週'), month: $ui('月'), quarter: $ui('四半期') }[l.key] }}
            </button>
          </div>
          <button type="button" class="secondary" @click="toNow">{{ $ui('今日') }}</button>
          <button
            type="button"
            class="secondary gantt-excel"
            :disabled="exporting || !loaded"
            :title="$ui('いま見えているガントを Excel で保存する')"
            @click="exportExcel"
          >
            ⤓ Excel
          </button>
          <!-- 集中モード（2.3.2）。メニューを 0px まで畳む。768px 未満では出さない -->
          <button
            v-if="!ui.narrow"
            type="button"
            class="secondary gantt-focus"
            :aria-pressed="ui.focusMode"
            :title="ui.focusMode ? $ui('集中モードを解除する（Esc / Shift + [）') : $ui('メニューを畳んで時間軸を広げる（Shift + [）')"
            @click="ui.toggleFocus()"
          >
            ⛶ {{ $ui('集中') }}
          </button>
        </div>
      </template>
    </PageHeader>

    <p v-if="editNotice" class="gantt-notice gantt-edit-notice" role="alert">
      <span>{{ editNotice }}</span>
      <button type="button" class="notice-close" :aria-label="$ui('閉じる')" @click="editNotice = ''">✕</button>
    </p>
    <p v-if="truncated" class="gantt-notice" role="status">
      {{ $ui('{value0}件を超えています。絞り込むと残りを表示できます（全{value1}件）', { value0: uiNumber(GANTT_LIMIT), value1: uiNumber(total) }) }}
    </p>

    <EmptyState v-if="error" :title="$ui('チケットを取得できませんでした')" :description="error.message">
      <template #action>
        <button type="button" class="secondary" @click="loadTickets">{{ $ui('再試行') }}</button>
      </template>
    </EmptyState>

    <div v-show="!error" class="gantt-body" :class="{ loading: loading && !loaded }">
      <div class="gantt-tree" @wheel="onTreeWheel">
        <div class="gantt-thdr" :style="{ height: `${geom.top}px` }">
          <div class="gantt-thdr-row" style="height: 34px">
            <b>{{ $ui('基準') }}</b><span class="mono">{{ baseTz }}</span><span class="mono">{{ zoneLabel.base }}</span>
          </div>
          <div v-if="geom.showV" class="gantt-thdr-row" style="height: 16px">
            <b>{{ $ui('あなた') }}</b><span class="mono">{{ viewTz }}</span><span class="mono">{{ zoneLabel.view }}</span>
          </div>
          <!-- 枠の下線 1px を最後の段で吸収する（本体の最初の行と高さを揃える） -->
          <div class="gantt-thdr-row" style="height: 15px">{{ $ui('スプリント') }}</div>
        </div>
        <div class="gantt-trows">
          <template v-for="v in visibleRows" :key="rowKey(v.row, v.index)">
            <div
              v-if="v.row.kind === 'group'"
              class="gantt-tr gantt-tgroup"
              :style="{ transform: `translateY(${v.y}px)` }"
            >
              <button
                type="button"
                class="caret"
                :aria-expanded="!v.row.collapsed"
                :aria-label="v.row.collapsed ? $ui('開く') : $ui('畳む')"
                @click="toggleGroup(v.row.key)"
              >
                {{ v.row.collapsed ? '▸' : '▾' }}
              </button>
              <span class="ttl">{{ v.row.label }}</span>
              <span class="count">{{ v.row.count }}</span>
            </div>
            <div
              v-else
              class="gantt-tr"
              :class="{
                done: v.row.item.done,
                hov: hover === v.row.item.seq && detailSeq !== v.row.item.seq,
                sel: detailSeq === v.row.item.seq,
              }"
              :style="{ transform: `translateY(${v.y}px)`, paddingLeft: `${6 + v.row.depth * 16}px` }"
              :data-seq="v.row.item.seq"
              @pointerenter="hover = v.row.item.seq"
              @pointerleave="hover = null"
              @click="openDetail(v.row.item.seq, $event)"
            >
              <button
                v-if="v.row.hasKids"
                type="button"
                class="caret"
                :aria-expanded="!v.row.collapsed"
                :aria-label="v.row.collapsed ? $ui('開く') : $ui('畳む')"
                @click.stop="toggleTree(v.row.item.seq)"
              >
                {{ v.row.collapsed ? '▸' : '▾' }}
              </button>
              <span v-else class="caret-sp"></span>
              <span class="st" :class="v.row.item.cat" :title="statusLabel(v.row.item.ticket.status.key, v.row.item.ticket.status.name)">
                {{ statusMarks[v.row.item.done ? 'done' : v.row.item.cat] }}
              </span>
              <span class="id">{{ fullId(v.row.item.seq) }}</span>
              <span class="ttl" :title="v.row.item.title">{{ v.row.item.title }}</span>
              <span v-if="v.row.item.overdue" class="od" :title="$ui('期限超過')">⚠</span>
            </div>
          </template>
          <p v-if="loaded && rows.length === 0" class="gantt-empty">{{ $ui('該当するチケットはありません') }}</p>
        </div>
      </div>

      <div
        ref="chart"
        class="gantt-chart"
        @scroll="onChartScroll"
        @wheel="onChartWheel"
        @pointerdown="onChartPointerDown"
        @pointermove="onChartMove"
        @pointerup="onChartPointerUp"
        @pointercancel="onPointerCancel"
        @pointerleave="onChartLeave"
        @click="onChartClick"
      >
        <div ref="canvas" class="gantt-canvas">
          <svg ref="svg" class="gantt-svg" xmlns="http://www.w3.org/2000/svg" role="img" :aria-label="$ui('ガントチャート')"></svg>
        </div>
      </div>

      <aside v-if="detailSeq !== null" class="gantt-detail" :style="{ width: `${paneWidth}px` }">
        <div class="gantt-grip" :title="$ui('ドラッグで幅を変える')" @pointerdown="startPaneResize"></div>
        <TicketDetailPane
          ref="detailPane"
          :project-key="projectKey"
          :seq="detailSeq"
          :members="members"
          :tags="tags"
          :workflow="projectStore.current?.workflow ?? null"
          :candidates="tickets"
          :epics="epics"
          :link-query="detailLinkQuery"
          :rollup="detailRollup"
          :undated-seqs="undatedSeqs"
          @close="closeDetail"
          @updated="onDetailUpdated"
          @created="onDetailCreated"
          @deleted="onDetailDeleted"
        />
      </aside>
    </div>
  </div>
</template>

<style scoped>
.gantt {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}

.gantt-tools {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
  justify-content: flex-end;
}

.gantt-filter {
  position: relative;
}

/* 押している間は段階のボタンと同じ見え方にする */
.gantt-focus[aria-pressed='true'] {
  background: var(--pb-accent);
  border-color: var(--pb-accent);
  color: var(--pb-on-accent);
}

/* 共通の `.secondary:hover` が背景を薄くし、白い文字が読めなくなるのを防ぐ */
.gantt-focus[aria-pressed='true']:hover:not(:disabled) {
  background: var(--pb-accent-hover);
}

.gantt-filter-panel {
  position: absolute;
  right: 0;
  top: calc(100% + 4px);
  z-index: 20;
  display: grid;
  gap: var(--pb-space-2);
  min-width: 260px;
  padding: var(--pb-space-3);
  background: var(--pb-bg);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  box-shadow: var(--pb-shadow-2);
}

.filter {
  display: grid;
  gap: 2px;
}

.filter-label {
  font-size: 12px;
  color: var(--pb-text-muted);
}

.check {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  font-size: 13px;
}

.gantt-group {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
}

.gantt-seg {
  display: inline-flex;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  overflow: hidden;
}

.gantt-seg button {
  font: inherit;
  font-size: 12px;
  color: var(--pb-text);
  background: var(--pb-bg);
  border: 0;
  border-left: 1px solid var(--pb-line);
  border-radius: 0;
  padding: 3px 9px;
  cursor: pointer;
}

.gantt-seg button:first-child {
  border-left: 0;
}

.gantt-seg button:hover {
  background: var(--pb-hover);
}

.gantt-seg button[aria-pressed='true'] {
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.gantt-notice {
  margin: 0;
  padding: var(--pb-space-1) var(--pb-space-4);
  font-size: 12px;
  color: var(--pb-text-muted);
  border-bottom: 1px solid var(--pb-line);
}

.gantt-edit-notice {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  color: var(--pb-danger-text);
  background: var(--pb-danger-bg);
}

.gantt-edit-notice .notice-close {
  margin-left: auto;
  padding: 0 var(--pb-space-1);
  border: 0;
  background: none;
  color: inherit;
  cursor: pointer;
}

.gantt-body {
  position: relative;
  flex: 1 1 auto;
  min-height: 0;
  display: grid;
  grid-template-columns: 300px minmax(0, 1fr);
}

.gantt-body.loading {
  opacity: 0.6;
}

/* 768px 未満は「破綻しない」水準に留める（2.4）。行の欄を狭め、時間軸に幅を残す。
   **ヘッダの道具は詰めて、見出しの「ガント」を残す**（選択肢の中身で軸は読める） */
@media (max-width: 767px) {
  .gantt-body {
    grid-template-columns: 160px minmax(0, 1fr);
  }

  .gantt-group .filter-label {
    display: none;
  }

  .gantt-seg button {
    padding: 3px 6px;
  }
}

/* ── 行（ツリー）── */
.gantt-tree {
  display: flex;
  flex-direction: column;
  min-height: 0;
  border-right: 1px solid var(--pb-border);
  overflow: hidden;
  background: var(--pb-bg);
}

.gantt-thdr {
  flex: none;
  box-sizing: border-box;
  border-bottom: 1px solid var(--pb-border);
  background: var(--pb-surface);
  font-size: 11px;
  color: var(--pb-text-muted);
}

.gantt-thdr-row {
  box-sizing: border-box;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 10px;
  border-bottom: 1px solid var(--pb-line);
  white-space: nowrap;
  overflow: hidden;
}

.gantt-thdr-row:last-child {
  border-bottom: 0;
}

.gantt-thdr-row b {
  font-weight: 600;
  color: var(--pb-text);
}

.mono {
  font-family: var(--pb-font-mono);
  font-size: 10.5px;
}

.gantt-trows {
  position: relative;
  flex: 1 1 auto;
  min-height: 0;
  overflow: hidden;
}

.gantt-tr {
  position: absolute;
  left: 0;
  right: 0;
  top: 0;
  height: 28px;
  box-sizing: border-box;
  display: flex;
  align-items: center;
  gap: 6px;
  padding-right: 8px;
  cursor: pointer;
  white-space: nowrap;
  font-size: 13px;
}

.gantt-tr.hov {
  background: color-mix(in srgb, var(--pb-hover) 70%, transparent);
}

.gantt-tr.hov::after,
.gantt-tr.sel::after {
  content: '';
  position: absolute;
  right: 0;
  top: 5px;
  bottom: 5px;
  width: 3px;
  border-radius: 2px 0 0 2px;
  background: var(--g-bar);
}

.gantt-tr.sel {
  background: color-mix(in srgb, var(--pb-accent) 14%, transparent);
  box-shadow: inset 2px 0 0 var(--pb-accent);
}

.gantt-tgroup {
  cursor: default;
  background: var(--pb-surface);
  font-weight: 600;
  padding-left: 6px;
}

.gantt-tgroup .count {
  font-weight: 400;
  font-size: 12px;
  color: var(--pb-text-muted);
}

.caret {
  width: 16px;
  height: 16px;
  flex: none;
  border: 0;
  padding: 0;
  background: none;
  color: var(--pb-text-muted);
  font-size: 10px;
  cursor: pointer;
}

.caret-sp {
  width: 16px;
  flex: none;
}

.st {
  width: 14px;
  flex: none;
  text-align: center;
  font-size: 11px;
  color: var(--pb-text-muted);
}

.st.in_progress {
  color: var(--pb-accent);
}

.id {
  flex: none;
  font-family: var(--pb-font-mono);
  font-size: 11.5px;
  color: var(--pb-text-muted);
  font-variant-numeric: tabular-nums;
}

.ttl {
  flex: 1 1 auto;
  min-width: 4em;
  overflow: hidden;
  text-overflow: ellipsis;
}

.od {
  flex: none;
  font-size: 12px;
  color: var(--pb-danger-text);
}

/* 完了は行ごと沈める（5.14。8.2） */
.gantt-tr.done .ttl,
.gantt-tr.done .id {
  color: var(--pb-text-muted);
}

.gantt-tr.done .ttl {
  opacity: 0.8;
}

.gantt-empty {
  margin: var(--pb-space-4);
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ── 時間軸 ── */
.gantt-chart {
  position: relative;
  min-width: 0;
  min-height: 0;
  overflow: auto;
  overscroll-behavior: contain;
}

.gantt-canvas {
  position: relative;
  min-height: 100%;
}

/* **SVG は見えている範囲に張り付ける**（sticky）。中身は見えている範囲だけを描き直す */
.gantt-svg {
  position: sticky;
  top: 0;
  left: 0;
  display: block;
}

/* ── 浮かせた詳細ペイン（5.14「詳細ペイン」）── */
.gantt-detail {
  position: absolute;
  top: 6px;
  right: 6px;
  bottom: 6px;
  z-index: 5;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--g-pane-bg);
  /* 裏の土日祝の色が面に差さないよう、ぼかしと一緒に彩度を落とす */
  -webkit-backdrop-filter: blur(14px) saturate(0.4);
  backdrop-filter: blur(14px) saturate(0.4);
  border: 1px solid var(--g-pane-line);
  border-radius: 8px;
  box-shadow: var(--g-pane-shadow);
}

.gantt-detail > :deep(.detail) {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
}

/* 詳細の見出しと本文の地を透かす（面はペインが持つ） */
.gantt-detail :deep(.detail-header) {
  background: transparent;
}

/* ダークではペインの中だけ文字と線の色を差し替える（5.14 の表） */
:global([data-theme='dark']) .gantt-detail {
  --pb-4: oklch(0.34 0.004 var(--pb-hue));
  --pb-6: oklch(0.37 0.004 var(--pb-hue));
  --pb-7: oklch(0.44 0.005 var(--pb-hue));
  --pb-8: oklch(0.52 0.006 var(--pb-hue));
  --pb-11: oklch(0.68 0.006 var(--pb-hue));
  --pb-12: oklch(0.93 0.004 var(--pb-hue));
  --pb-hover: var(--pb-4);
  --pb-line: var(--pb-6);
  --pb-border: var(--pb-7);
  --pb-focus: var(--pb-8);
  --pb-text-muted: var(--pb-11);
  --pb-text: var(--pb-12);
  color: var(--pb-12);
}

.gantt-grip {
  position: absolute;
  left: -1px;
  top: 0;
  bottom: 0;
  width: 6px;
  cursor: col-resize;
  z-index: 2;
}

.gantt-grip:hover,
.gantt-grip.dragging {
  background: color-mix(in srgb, var(--pb-focus) 60%, transparent);
}

/* ── SVG の中身（`lib/gantt/render.ts` が描く。見本の CSS を移したもの）── */
.gantt-svg :deep(.c-sat) { fill: var(--g-sat); }
.gantt-svg :deep(.c-sun) { fill: var(--g-sun); }
.gantt-svg :deep(.c-hol) { fill: url(#g-hol); }
.gantt-svg :deep(.c-holbg) { fill: var(--g-sun); }
.gantt[data-cal='other'] .gantt-svg :deep(.c-sat),
.gantt[data-cal='other'] .gantt-svg :deep(.c-sun) { fill: var(--g-wkend); }
.gantt-svg :deep(.hol-line) { stroke: var(--g-hol-line); stroke-width: 1; }
.gantt-svg :deep(.spr) { fill: none; stroke: var(--g-neon); stroke-width: 1.25; opacity: 0.45; filter: var(--g-glow); }
.gantt-svg :deep(.spr.plan) { stroke-dasharray: 5 4; opacity: 0.85; }
.gantt-svg :deep(.spr.act) { stroke: var(--g-neon-act); stroke-width: 1.5; opacity: 1; filter: var(--g-glow-act); }
.gantt-svg :deep(.sprl) { fill: var(--pb-text-muted); font: 10.5px var(--pb-font-mono); }
.gantt-svg :deep(.sprl.act) { fill: var(--g-neon-act); font-weight: 600; }
.gantt-svg :deep(.rowhi) { fill: color-mix(in srgb, var(--pb-4) 70%, transparent); }
.gantt-svg :deep(.rowsel) { fill: color-mix(in srgb, var(--pb-9) 13%, transparent); }
.gantt-svg :deep(.rowgrp) { fill: color-mix(in srgb, var(--pb-2) 80%, transparent); }
.gantt-svg :deep(.g-min) { stroke: var(--pb-6); stroke-width: 1; opacity: var(--g-min-op); }
.gantt-svg :deep(.g-sub) { stroke: var(--pb-6); stroke-width: 1; stroke-dasharray: 1 3; opacity: var(--g-sub-op); }
.gantt-svg :deep(.g-maj) { stroke: var(--pb-7); stroke-width: 1; }
.gantt-svg :deep(.g-row) { stroke: var(--pb-6); stroke-width: 1; opacity: var(--g-rule-op); }
.gantt-svg :deep(.g-cross) { stroke: var(--pb-8); stroke-width: 1; fill: none; opacity: 0.7; }
.gantt-svg :deep(.g-vmid) { stroke: var(--pb-8); stroke-width: 1; stroke-dasharray: 1 3; }
.gantt-svg :deep(.g-now) { stroke: var(--g-bar); stroke-width: 1.25; filter: var(--g-bar-glow); }
.gantt-svg :deep(.leader) { stroke: var(--g-bar); stroke-width: 1; stroke-dasharray: 1 3; opacity: 0.9; }
.gantt-svg :deep(.h-bg) { fill: var(--pb-2); }
.gantt-svg :deep(.h-line) { stroke: var(--pb-7); stroke-width: 1; }
.gantt-svg :deep(.h-lane) { stroke: var(--pb-6); stroke-width: 1; opacity: var(--g-rule-op); }
.gantt-svg :deep(.h-vbg) { fill: var(--pb-1); }
.gantt-svg :deep(.h-maj) { fill: var(--pb-12); font: 600 11px var(--pb-font-mono); }
.gantt-svg :deep(.h-min) { fill: var(--pb-11); font: 10.5px var(--pb-font-mono); }
.gantt-svg :deep(.h-tick) { stroke: var(--pb-7); stroke-width: 1; }
.gantt-svg :deep(.h-tmaj) { stroke: var(--pb-8); stroke-width: 1; }
.gantt-svg :deep(.h-v) { fill: var(--pb-11); font: 10px var(--pb-font-mono); }
.gantt-svg :deep(.h-vtick) { stroke: var(--pb-8); stroke-width: 1; }
.gantt-svg :deep(.h-now) { fill: var(--g-bar); }
.gantt-svg :deep(.h-nowt) { fill: var(--g-bar); font: 600 10px var(--pb-font-mono); }
.gantt-svg :deep(.now-box) { fill: var(--pb-2); stroke: var(--g-bar); stroke-width: 1; }

.gantt-svg :deep(.b) { stroke: var(--g-bar-line); stroke-width: 1; }
.gantt-svg :deep(.b:not(.done)) { filter: var(--g-bar-glow); }
.gantt-svg :deep(.b.todo) { fill: url(#g-hatch); }
.gantt-svg :deep(.b.in_progress) { fill: var(--g-bar-fill); }
.gantt-svg :deep(.b.review) { fill: var(--g-bar-fill); stroke-dasharray: 3 2; }
.gantt-svg :deep(.b.done) { fill: var(--g-done-fill); stroke: var(--g-done-line); }
.gantt-svg :deep(.br) { stroke: var(--pb-11); stroke-width: 1; fill: none; }
.gantt-svg :deep(.ms) { stroke: var(--g-bar-line); stroke-width: 1.5; fill: none; filter: var(--g-bar-glow); }
.gantt-svg :deep(.ms.done) { stroke: var(--g-done-line); filter: none; }
.gantt-svg :deep(.ms-tail) { stroke: var(--g-bar); stroke-width: 1; stroke-dasharray: 2 2; opacity: 0.75; }
.gantt-svg :deep(.ms-tail.done) { stroke: var(--g-done-line); }
.gantt-svg :deep(.ms-dot) { fill: var(--g-bar); }
.gantt-svg :deep(.ms-dot.done) { fill: var(--g-done-line); }
.gantt-svg :deep(.hatch) { stroke: var(--g-bar-line); stroke-width: 1; opacity: var(--g-hatch-op); }
.gantt-svg :deep(.lbl) { fill: var(--pb-11); font: 10.5px var(--pb-font-mono); }
.gantt-svg :deep(.lbl .lid) { fill: var(--pb-12); font-weight: 600; }
.gantt-svg :deep(.lbl.done),
.gantt-svg :deep(.lbl.done .lid) { fill: var(--pb-11); font-weight: 400; opacity: 0.8; }
.gantt-svg :deep(.lbl .lsub) { fill: var(--pb-11); font-weight: 400; }
.gantt-svg :deep(.od) { fill: var(--pb-danger-text); font-size: 12px; }
.gantt-svg :deep(.edge) { fill: color-mix(in srgb, var(--pb-1) 90%, transparent); stroke: var(--pb-7); stroke-width: 1; }
.gantt-svg :deep(.edge-t) { fill: var(--pb-11); font: 10px var(--pb-font-mono); }
.gantt-svg :deep(.edge-g:hover .edge) { stroke: var(--g-bar); }
.gantt-svg :deep(.edge-g:hover .edge-t) { fill: var(--pb-12); }

.gantt-svg :deep(.d) { fill: none; stroke: var(--g-dep); stroke-width: 1; }
.gantt-svg :deep(.d-h) { fill: var(--g-dep); }
.gantt-svg :deep(.d-s) { fill: var(--pb-1); stroke: var(--g-dep); stroke-width: 1; }
.gantt-svg :deep(.dep.hi .d) { stroke: var(--g-dep-hi); stroke-width: 1.5; }
.gantt-svg :deep(.dep.hi .d-h) { fill: var(--g-dep-hi); }
.gantt-svg :deep(.dep.hi .d-s) { stroke: var(--g-dep-hi); }
.gantt-svg :deep(.dep.ai .d) { stroke: var(--pb-ai); stroke-dasharray: 4 3; }
.gantt-svg :deep(.dep.blk .d) { stroke-width: 1.8; stroke-dasharray: 8 3 2 3; }
.gantt-svg :deep(.dep.blk.hi .d) { stroke-width: 2.3; }
.gantt-svg :deep(.d-stop) { stroke: var(--g-dep); stroke-width: 3; stroke-linecap: square; }
.gantt-svg :deep(.dep.hi .d-stop) { stroke: var(--g-dep-hi); }
.gantt-svg :deep(.dep.ai .d-h) { fill: var(--pb-ai); }
.gantt-svg :deep(.dep.ai .d-s) { stroke: var(--pb-ai); }
.gantt-svg :deep(.d-tagr) { fill: var(--pb-1); stroke: var(--pb-7); stroke-width: 1; }
.gantt-svg :deep(.d-tagt) { fill: var(--pb-11); font: 9.5px var(--pb-font-mono); }
.gantt-svg :deep(.dep.ai .d-tagr) { stroke: var(--pb-ai-border); stroke-dasharray: 2 1.5; }
.gantt-svg :deep(.dep.ai .d-tagt) { fill: var(--pb-ai-text); }

/* 編集（5.14「編集」）。影・応答待ち・取っ手・ドラッグの札・引いている線・違反 */
.gantt-svg :deep(.ghost) { opacity: 0.3; }
.gantt-svg :deep(.bar.pend .b),
.gantt-svg :deep(.bar.pend .ms) { stroke-dasharray: 3 2; stroke-width: 1.5; }
.gantt-svg :deep(.hdl) { fill: var(--pb-1); stroke: var(--g-dep-hi); stroke-width: 1.2; cursor: crosshair; }
.gantt-svg :deep(.hdl.hot) { fill: var(--g-bar); stroke: var(--g-bar); }
.gantt-svg :deep(.dtag) { fill: color-mix(in srgb, var(--pb-1) 94%, transparent); stroke: var(--g-bar); stroke-width: 1; }
.gantt-svg :deep(.dtag-t) { fill: var(--pb-12); font: 10.5px var(--pb-font-mono); }
.gantt-svg :deep(.dtag.no) { stroke: var(--pb-danger); }
.gantt-svg :deep(.dtag-t.no) { fill: var(--pb-danger-text); }
.gantt-svg :deep(.ldrag) { stroke: var(--g-bar); stroke-width: 1.25; stroke-dasharray: 4 3; }
.gantt-svg :deep(.ldrag.no) { stroke: var(--pb-danger); }
.gantt-svg :deep(.lring) { fill: none; stroke: var(--g-bar); stroke-width: 1.5; }
.gantt-svg :deep(.lring.no) { stroke: var(--pb-danger); }
.gantt-svg :deep(.dep.bad .d) { stroke: var(--pb-danger); }
.gantt-svg :deep(.dep.bad .d-h) { fill: var(--pb-danger); }
.gantt-svg :deep(.dep.bad .d-s) { stroke: var(--pb-danger); }
.gantt-svg :deep(.dep.bad .d-stop) { stroke: var(--pb-danger); }
.gantt-svg :deep(.dep.bad .d-tagr) { stroke: var(--pb-danger-border); }
.gantt-svg :deep(.dep.bad .d-tagt) { fill: var(--pb-danger-text); }

.gantt-svg :deep(.roll) { stroke: var(--pb-11); stroke-width: 1; fill: none; }
.gantt-svg :deep(.roll-h) { fill: var(--pb-11); }
.gantt-svg :deep(.roll.done) { stroke: var(--g-done-line); }
.gantt-svg :deep(.roll-h.done) { fill: var(--g-done-line); }
.gantt-svg :deep(.roll-bg) { fill: var(--pb-1); }
.gantt-svg :deep(.cur-v) { stroke: var(--g-bar); stroke-width: 1; stroke-dasharray: 2 3; opacity: 0.75; }
.gantt-svg :deep(.tip) { fill: color-mix(in srgb, var(--pb-1) 92%, transparent); stroke: color-mix(in srgb, var(--g-bar) 55%, var(--pb-7)); stroke-width: 1; }
.gantt-svg :deep(.tip-id) { fill: var(--g-bar); font: 600 11px var(--pb-font-mono); }
.gantt-svg :deep(.tip-t) { fill: var(--pb-12); font-size: 11.5px; }
.gantt-svg :deep(.tip-d) { fill: var(--pb-11); font: 10.5px var(--pb-font-mono); }
.gantt-svg :deep(.curr) { fill: var(--g-bar); }
.gantt-svg :deep(.curr-t) { fill: var(--pb-1); font: 600 10px var(--pb-font-mono); }
</style>
