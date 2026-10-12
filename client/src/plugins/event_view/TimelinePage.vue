<script setup lang="ts">
/**
 * タイムライン（`GuiDesign.md` 5.15。pb-226 / pb-298）。
 *
 * イベント当日の進行を、**エピックを行にして1行に複数のバーを並べて見る**。
 * プラグイン `event_view` のページで、本体との接点は `plugins/registry.ts` だけである。
 * 使うのは公開された REST と、認証・選択中のプロジェクト・本体の部品（詳細ペイン）。
 *
 * - 取得は起動時と**5秒ごと**（`If-None-Match`。304 なら何もしない）。3回続けて失敗したら警告の面
 * - **ドラッグ中・試算の確認中に取れた結果は、操作が終わるまで画面に当てない**
 * - 端のドラッグ → 離して試算（`dry_run`）→ 影 → `[確定]`。計算はサーバ（`ApiDesign.md` 9.17）
 * - **画面は自動で流さない**。「いま」の線だけが動く
 * - 状態はすべて URL のクエリ（`parent`・`zoom`・`baseline`・担当・状態・タグ）
 */
import { computed, nextTick, onMounted, onUnmounted, ref, shallowRef, useTemplateRef, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ConfirmDialog from '../../components/ConfirmDialog.vue'
import EmptyState from '../../components/EmptyState.vue'
import Modal from '../../components/Modal.vue'
import PageHeader from '../../components/PageHeader.vue'
import TicketDetailPane from '../../components/TicketDetailPane.vue'
import UserActionsMenu from '../../components/UserActionsMenu.vue'
import { ApiError } from '../../api/client'
import * as projectsApi from '../../api/projects'
import * as tagsApi from '../../api/tags'
import type { Tag } from '../../api/tags'
import { statusCategoryLabels, statusCategoryOrder } from '../../api/tickets'
import type { TicketDetail } from '../../api/tickets'
import { statusLabel } from '../../lib/catalogLabels'
import { currentTimezone } from '../../lib/datetime'
import { sleeveBackground } from '../../lib/epicSleeve'
import { uiNumber, uiText } from '../../locales/ui'
import { useAuthStore } from '../../stores/auth'
import { useProjectStore } from '../../stores/project'
import * as tlApi from './api'
import EpicChecklist from './EpicChecklist.vue'
import { useEpicChoices } from './epics'
import { baselineFullName, baselineName, fmtDur, fmtSigned, shortTitle, stateLabel } from './format'
import {
  BAR_H,
  LANE_H,
  ROW_PAD,
  barOf,
  baselineRows,
  emptyStateOf,
  rowSourceItems,
  baselineStamps,
  canCompact,
  canExtendToNow,
  changeOf,
  changedSeqs,
  collisions,
  deviationOf,
  deviationTag,
  expectOf,
  extendToNowDue,
  fsMarks,
  keyOf,
  laneTop,
  layoutRows,
  mergeNewer,
  mergeTimelineEpics,
  planBarOf,
  readSnapSeconds,
  readTimelineEpics,
  widthClass,
} from './model'
import type {
  Bar,
  Baseline,
  BaselineItem,
  BaselineSummary,
  RescheduleChange,
  RescheduleRequest,
  RescheduleResult,
  RowLayout,
  TlEpic,
  TlLink,
  TlList,
  TlTicket,
} from './model'
import {
  MIN,
  PPS_MAX,
  PPS_MIN,
  SEC,
  ZOOMS,
  ZOOM_ORDER,
  axisRange,
  clock,
  isWallMultiple,
  monthDay,
  snapTo,
  tickScale,
  wallTicks,
  zoomAt,
  zoomOf,
} from './time'
import type { ZoomKey } from './time'

const route = useRoute()
const router = useRouter()
const projectStore = useProjectStore()
const auth = useAuthStore()

const projectKey = computed(() => {
  const key = route.params.key
  return typeof key === 'string' ? key : ''
})

/** 行の見出しの幅・時間軸の高さ（5.15） */
const LW = 210
const AH = 44
/** 片方だけの予定（`[┄` / `┄]`）の幅 */
const HALF_W = 40
/** 他の人の変更を光らせる時間・通知の行に残す時間 */
const FLASH_MS = 2400
const OTHER_MS = 20_000
const POLL_MS = 5000
const LIMIT = 5000

// ── URL クエリ ───────────────────────────────────────────────

function queryValue(name: string): string {
  const v = route.query[name]
  return typeof v === 'string' ? v : ''
}

/** タイムライン自身のクエリ（`from` を除く）。詳細の URL へ持ち回る */
const tlQuery = computed<Record<string, string>>(() => {
  const q: Record<string, string> = {}
  for (const [k, v] of Object.entries(route.query)) {
    if (k !== 'from' && typeof v === 'string' && v !== '') q[k] = v
  }
  return q
})

/** クエリを書き換える。空の値はキーごと落とす。**詳細を開いていてもパスを変えない** */
function setQuery(patch: Record<string, string>): void {
  const next: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...route.query, ...patch })) {
    if (typeof v === 'string' && v !== '') next[k] = v
  }
  void router.replace({ path: route.path, query: next })
}

const rowFilter = computed<number[]>(() =>
  queryValue('parent')
    .split(',')
    .map((v) => Number(v))
    .filter((n) => Number.isInteger(n) && n > 0),
)

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

const filterCount = computed(
  () => (queryValue('assignee') !== '' ? 1 : 0) + (queryValue('tag') !== '' ? 1 : 0) + (stateValue.value !== '' ? 1 : 0),
)

function clearFilters(): void {
  setQuery({ assignee: '', tag: '', status: '', status_category: '', open: '' })
}

const fetchQuery = computed<tlApi.TimelineQuery>(() => ({
  parent: queryValue('parent'),
  assignee: queryValue('assignee'),
  status: queryValue('status'),
  status_category: queryValue('status_category'),
  open: queryValue('open'),
  tag: queryValue('tag'),
}))
const fetchKey = computed(() => JSON.stringify(fetchQuery.value))

// ── 権限と設定 ───────────────────────────────────────────────

/** 端のドラッグと通知の行の操作（`ticket.edit` か `ticket.plan.edit`） */
const canPlan = computed(
  () => auth.canInProject(projectKey.value, 'ticket.edit') || auth.canInProject(projectKey.value, 'ticket.plan.edit'),
)
/** `[計画を保存]`・`[載せるエピック ▾]`・版を消す */
const canProjectEdit = computed(() => auth.canInProject(projectKey.value, 'project.edit'))

const settings = computed(() => projectStore.current?.settings)
const snapSec = computed(() => readSnapSeconds(settings.value))
const timelineEpics = computed(() => readTimelineEpics(settings.value))
const baseTz = computed(() => projectStore.planTimezone)
const viewTz = computed(() => currentTimezone() ?? Intl.DateTimeFormat().resolvedOptions().timeZone)
const members = computed(() => projectStore.current?.members ?? [])
const statuses = computed(() => projectStore.current?.workflow?.statuses ?? [])

const tags = ref<Tag[]>([])
const epicChoices = useEpicChoices()
const sleeveOf = computed(() => epicChoices.sleeve.value)

function sleeveStyle(seq: number): Record<string, string> {
  return { background: sleeveBackground(sleeveOf.value(seq)) }
}

// ── 取得と定期取得 ───────────────────────────────────────────

const tickets = shallowRef<TlTicket[]>([])
const epics = shallowRef<TlEpic[]>([])
const links = shallowRef<TlLink[]>([])
const baselinesInfo = ref<TlList['baselines'] | null>(null)
const total = ref(0)
const loading = ref(false)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
/** 最後に取得に成功した時刻（`⟳ 3秒前`）と、続けて失敗した回数 */
const lastFetchAt = ref<number | null>(null)
const failCount = ref(0)
let etag: string | null = null
let fetchSeq = 0
/** 操作中に取れた結果（操作が終わってから当てる） */
let deferred: TlList | null = null
/** 自分が書いた版（定期取得で「他の人の変更」として光らせない） */
const myVersions = new Map<number, number>()

function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({ status: 0, code: 'internal_error', message: uiText('予期しないエラーが発生しました') })
}

async function load(initial: boolean): Promise<void> {
  const key = projectKey.value
  if (key === '') return
  const mine = ++fetchSeq
  if (initial) {
    loading.value = true
    error.value = null
    etag = null
  }
  try {
    const res = await tlApi.getTimeline(key, fetchQuery.value, etag)
    if (mine !== fetchSeq) return
    failCount.value = 0
    lastFetchAt.value = Date.now()
    if (res.status === 304) return
    etag = res.etag
    if (!initial && isBusy()) {
      deferred = res.data
      return
    }
    apply(res.data, !initial)
  } catch (e) {
    if (mine !== fetchSeq) return
    if (initial) error.value = toApiError(e)
    else failCount.value++
  } finally {
    if (mine === fetchSeq && initial) loading.value = false
  }
}

function apply(data: TlList, detect: boolean): void {
  // 保留していた古い一覧が、その後に確定した行（新しい版）を上書きしないよう、版の新しいほうを採る
  const items = mergeNewer(tickets.value, data.items)
  if (detect && loaded.value) {
    const before = new Map(tickets.value.map((t) => [t.seq, t]))
    const changed = changedSeqs(new Map(tickets.value.map((t) => [t.seq, t.version])), items, myVersions)
    if (changed.length > 0) noteOthers(changed, before, items)
  }
  tickets.value = items
  epics.value = data.epics ?? []
  links.value = data.links ?? []
  total.value = data.total
  const b = data.baselines ?? null
  const prev = baselinesInfo.value
  baselinesInfo.value = b
  if (b && prev && (b.latest_id !== prev.latest_id || b.count !== prev.count)) void loadBaselineList()
  loaded.value = true
}

/** 操作中か（ドラッグ中・試算の確認中・確定の応答待ち） */
function isBusy(): boolean {
  return drag.value !== null || trial.value !== null || committing.value
}

function flushDeferred(): void {
  if (deferred === null || isBusy()) return
  const d = deferred
  deferred = null
  apply(d, true)
}

let pollTimer: ReturnType<typeof setInterval> | undefined

function poll(): void {
  if (document.hidden || loading.value || !loaded.value) return
  void load(false)
}

function onVisibility(): void {
  if (!document.hidden) poll()
}

// ── 他の人の変更 ─────────────────────────────────────────────

const flash = shallowRef<Map<number, { at: number; who: string }>>(new Map())
const other = ref<{ who: string; text: string; at: number } | null>(null)
let otherTimer: ReturnType<typeof setTimeout> | undefined
let flashTimer: ReturnType<typeof setTimeout> | undefined

function noteOthers(seqs: number[], before: Map<number, TlTicket>, after: TlTicket[]): void {
  const by = new Map(after.map((t) => [t.seq, t]))
  const at = Date.now()
  const next = new Map(flash.value)
  for (const seq of seqs) next.set(seq, { at, who: by.get(seq)?.last_changed_by?.display_name ?? '' })
  flash.value = next
  clearTimeout(flashTimer)
  flashTimer = setTimeout(() => {
    const keep = new Map([...flash.value].filter(([, f]) => Date.now() - f.at < FLASH_MS))
    flash.value = keep
  }, FLASH_MS + 50)

  const first = by.get(seqs[0]!)
  if (!first) return
  const title = shortTitle(first.title)
  const c = changeOf(before.get(first.seq), first)
  let text: string
  switch (c.kind) {
    case 'due':
      text = uiText('{value0} の終了を {value1} に', { value0: title, value1: clock(viewTz.value, c.at) })
      break
    case 'start':
      text = uiText('{value0} の開始を {value1} に', { value0: title, value1: clock(viewTz.value, c.at) })
      break
    case 'status':
      text = uiText('{value0} を{value1}に', { value0: title, value1: c.name })
      break
    case 'title':
      text = uiText('{value0} の題名を変更', { value0: title })
      break
    case 'added':
      text = uiText('{value0} を追加', { value0: title })
      break
    default:
      text = uiText('{value0} を変更', { value0: title })
  }
  if (seqs.length > 1) text = uiText('{value0} ほか{value1}件', { value0: text, value1: seqs.length - 1 })
  other.value = { who: first.last_changed_by?.display_name ?? '', text, at }
  clearTimeout(otherTimer)
  otherTimer = setTimeout(() => (other.value = null), OTHER_MS)
}

// ── 当初計画の版 ─────────────────────────────────────────────

const baselineList = ref<BaselineSummary[]>([])
const baselineListLoaded = ref(false)
const baselineCache = shallowRef<Map<string, Baseline>>(new Map())

async function loadBaselineList(): Promise<void> {
  try {
    baselineList.value = (await tlApi.listBaselines(projectKey.value)).items
  } catch {
    baselineList.value = []
  } finally {
    baselineListLoaded.value = true
  }
}

const stamps = computed(() => baselineStamps(baselineList.value, baseTz.value, Date.now()))
function nameOf(b: BaselineSummary): string {
  return baselineName(stamps.value.get(b.id) ?? '', b)
}
function fullNameOf(b: BaselineSummary): string {
  return baselineFullName(stamps.value.get(b.id) ?? '', b)
}

/** 重ねる版。URL の `baseline`（`id` か `none`）。**省くと最後に保存した版** */
const selectedBaselineId = computed<string | null>(() => {
  const v = queryValue('baseline')
  if (v === 'none') return null
  if (v !== '' && baselineList.value.some((b) => b.id === v)) return v
  return baselineList.value[0]?.id ?? null
})
const selectedBaseline = computed(() => baselineList.value.find((b) => b.id === selectedBaselineId.value) ?? null)

// 消された版の `id` なら、最後に保存した版で開き直す（5.15「行のフィルタと URL」）
watch([baselineList, () => route.query.baseline], () => {
  const v = queryValue('baseline')
  if (!baselineListLoaded.value || v === '' || v === 'none') return
  if (!baselineList.value.some((b) => b.id === v)) setQuery({ baseline: '' })
})

const fetchingBaselines = new Set<string>()

async function ensureBaseline(id: string | null): Promise<void> {
  if (id === null || baselineCache.value.has(id) || fetchingBaselines.has(id)) return
  fetchingBaselines.add(id)
  try {
    const b = await tlApi.getBaseline(projectKey.value, id)
    baselineCache.value = new Map(baselineCache.value).set(id, b)
  } catch {
    // 重ねられないだけ。版の一覧を取り直して食い違いを直す
    void loadBaselineList()
  } finally {
    fetchingBaselines.delete(id)
  }
}

watch(selectedBaselineId, (id) => void ensureBaseline(id), { immediate: true })

/** 版の中身（`ticket_seq` → 行） */
function itemsOf(id: string | null): Map<number, BaselineItem> | null {
  if (id === null) return null
  const b = baselineCache.value.get(id)
  return b ? new Map(b.items.map((i) => [i.ticket_seq, i])) : null
}

// 版を送る（5.15「当初計画の版」）。古い順に1つずつ、最後は「いま」
const step = ref<number | null>(null)
const versionsAsc = computed(() => [...baselineList.value].reverse())

function startStep(): void {
  if (versionsAsc.value.length === 0) return
  cancelTrial()
  openMenu.value = null
  step.value = 0
}
function moveStep(d: number): void {
  if (step.value === null) return
  step.value = Math.max(0, Math.min(versionsAsc.value.length, step.value + d))
}
function endStep(): void {
  step.value = null
}

watch([step, versionsAsc], () => {
  if (step.value === null) return
  if (step.value > versionsAsc.value.length) step.value = versionsAsc.value.length
  const k = step.value
  void ensureBaseline(versionsAsc.value[k]?.id ?? null)
  void ensureBaseline(versionsAsc.value[k - 1]?.id ?? null)
})

// 版の保存と削除

const saveOpen = ref(false)
const saveLabel = ref('')
const saving = ref(false)
const saveError = ref('')

function openSave(): void {
  saveLabel.value = ''
  saveError.value = ''
  saveOpen.value = true
}

async function saveBaseline(): Promise<void> {
  saving.value = true
  saveError.value = ''
  try {
    const label = saveLabel.value.trim()
    const b = await tlApi.createBaseline(projectKey.value, label === '' ? null : label)
    saveOpen.value = false
    await loadBaselineList()
    setResult('ok', uiText('{value0} の版を保存しました（{value1}件）', { value0: nameOf(b), value1: uiNumber(b.item_count) }))
  } catch (e) {
    saveError.value = toApiError(e).message
  } finally {
    saving.value = false
  }
}

const deleting = ref<BaselineSummary | null>(null)
const deleteBusy = ref(false)

async function confirmDelete(): Promise<void> {
  const b = deleting.value
  if (!b) return
  deleteBusy.value = true
  // 名前は消す前に決める（一覧を取り直すと、同じ分の判定と日時が引けなくなる）
  const name = fullNameOf(b)
  try {
    await tlApi.deleteBaseline(projectKey.value, b.id)
    const cache = new Map(baselineCache.value)
    cache.delete(b.id)
    baselineCache.value = cache
    if (queryValue('baseline') === b.id) setQuery({ baseline: '' })
    await loadBaselineList()
    setResult('ok', uiText('{value0} の版を消しました', { value0: name }))
  } catch (e) {
    setResult('error', toApiError(e).message)
  } finally {
    deleteBusy.value = false
    deleting.value = null
  }
}

// ── 載せるエピック ─────────────────────────────────────────

const epicDraft = ref<number[]>([])
const epicSaving = ref(false)
const epicError = ref('')

function openEpicPicker(): void {
  epicDraft.value = [...timelineEpics.value]
  epicError.value = ''
  openMenu.value = 'epics'
}

async function saveEpics(): Promise<void> {
  const p = projectStore.current
  if (!p) return
  epicSaving.value = true
  epicError.value = ''
  try {
    const next = mergeTimelineEpics(p.settings, epicDraft.value, epicChoices.all.value.map((t) => t.seq))
    const res = await projectsApi.updateProject(p.key, p.version, { settings: next })
    projectStore.setCurrent(res)
    openMenu.value = null
    await load(true)
  } catch (e) {
    epicError.value = toApiError(e).message
  } finally {
    epicSaving.value = false
  }
}

// ── 見出しの道具（開いているパネルは1つだけ）──────────────────

const openMenu = ref<'rows' | 'filter' | 'baseline' | 'epics' | null>(null)
const toolsEl = useTemplateRef<HTMLElement>('toolsEl')

function toggleMenu(m: 'rows' | 'filter' | 'baseline' | 'epics'): void {
  if (m === 'epics' && openMenu.value !== 'epics') {
    openEpicPicker()
    return
  }
  openMenu.value = openMenu.value === m ? null : m
}

function onDocumentPointerDown(e: PointerEvent): void {
  if (openMenu.value === null) return
  const t = e.target as HTMLElement
  if (toolsEl.value?.contains(t) || t.closest?.('[role="dialog"], [role="menu"], .actions-panel')) return
  openMenu.value = null
}

/** 行の候補：載せるエピック（`sort_key` の順） */
const rowChoices = computed(() => {
  const chosen = new Set(timelineEpics.value)
  return epicChoices.choices.value.filter((c) => chosen.has(c.seq))
})
const rowSelected = computed(() => (rowFilter.value.length > 0 ? rowFilter.value : rowChoices.value.map((c) => c.seq)))
const rowFiltered = computed(() => rowFilter.value.length > 0 && rowFilter.value.length < rowChoices.value.length)
const rowSummary = computed(() => {
  if (!rowFiltered.value) return ''
  const names = rowChoices.value.filter((c) => rowFilter.value.includes(c.seq)).map((c) => c.title)
  if (names.length === 0) return ''
  return names.length > 1 ? uiText('{value0}ほか{value1}', { value0: names[0], value1: names.length - 1 }) : names[0]!
})

function setRows(seqs: number[]): void {
  const all = rowChoices.value.map((c) => c.seq)
  const picked = all.filter((s) => seqs.includes(s))
  setQuery({ parent: picked.length === 0 || picked.length === all.length ? '' : picked.join(',') })
}

// ── 時刻・拡大縮小・スクロール ─────────────────────────────────

const now = ref(Date.now())
const tickNow = ref(Date.now())
const pps = ref(ZOOMS[zoomOf(route.query.zoom)])
const withSec = computed(() => pps.value >= 1)
const scroller = useTemplateRef<HTMLDivElement>('scroller')
const scrollLeft = ref(0)
const bodyW = ref(800)

const liveBars = computed<Bar[]>(() => tickets.value.map((t) => barOf(t, now.value)).filter((b): b is Bar => b !== null))

/**
 * 版の表示で読む行（`model.ts` の `baselineRows`）。重ねる版、版を送るときはその版と前の版。
 * **対象から外したエピック・消したエピックも、版に保存した行の情報で出す。**
 */
const rowItems = computed<BaselineItem[]>(() =>
  rowSourceItems<BaselineItem>(
    step.value,
    versionsAsc.value.map((v) => v.id),
    step.value === null ? selectedBaselineId.value : null,
    (id) => baselineCache.value.get(id)?.items,
  ),
)
/**
 * 行を決める材料がそろったか。いまの行・エピックの候補・重ねる版の中身のどれかが未着のあいだは、
 * 版の行を「無いエピック」と取り違えたり、空状態を一瞬出したりしないよう、版の行を足さない
 */
const rowsReady = computed(
  () =>
    loaded.value &&
    epicChoices.loaded.value &&
    baselineListLoaded.value &&
    (step.value !== null || selectedBaselineId.value === null || baselineCache.value.has(selectedBaselineId.value)),
)
const rowSet = computed(() => baselineRows(epics.value, rowsReady.value ? rowItems.value : [], timelineEpics.value, rowFilter.value, epicChoices.all.value))

/** 表示する中身。当日（いまの予定）か、版を送っているあいだはその版 */
const display = computed<{ mode: 'day' | 'step'; bars: Bar[]; base: Map<number, BaselineItem> | null; gone: Bar[] }>(() => {
  if (step.value !== null) {
    const vs = versionsAsc.value
    const k = step.value
    const cur = k >= vs.length
    const items = cur ? null : itemsOf(vs[k]?.id ?? null)
    const bars = cur ? liveBars.value : [...(items?.values() ?? [])].filter((i) => !i.all_day).map((i) => planBarOf(i))
    const base = k > 0 ? itemsOf(vs[Math.min(k, vs.length) - 1]?.id ?? null) : null
    const present = new Set(bars.map((b) => b.seq))
    const gone = base ? [...base.values()].filter((i) => !i.all_day && !present.has(i.ticket_seq)).map((i) => planBarOf(i, true)) : []
    return { mode: 'step', bars, base, gone }
  }
  const base = itemsOf(selectedBaselineId.value)
  // 消えたもの：版にあっていまは無いチケットと、対象から外した行の計画（5.15「当初計画の版」）
  const present = new Set(liveBars.value.map((b) => b.seq))
  const outside = rowSet.value.outside
  const gone = base
    ? [...base.values()]
        .filter((i) => !i.all_day && (!i.exists || (outside.has(i.epic_seq) && !present.has(i.ticket_seq))))
        .map((i) => planBarOf(i, true))
    : []
  return { mode: 'day', bars: liveBars.value, base, gone }
})

const range = computed(() => {
  const times: number[] = []
  for (const b of [...display.value.bars, ...display.value.gone]) {
    if (b.s !== null) times.push(b.s)
    if (b.e !== null) times.push(b.e)
  }
  return axisRange(times, now.value)
})

function X(t: number): number {
  return (t - range.value.t0) * (pps.value / SEC)
}
const trackW = computed(() => Math.max(0, X(range.value.t1)))

function onScroll(): void {
  scrollLeft.value = scroller.value?.scrollLeft ?? 0
}

/** 1秒の幅を変える。**`anchorPx`（本体の左端からの位置）の下の時刻を動かさない** */
async function setPps(next: number, anchorPx: number): Promise<void> {
  const el = scroller.value
  const clamped = Math.min(PPS_MAX, Math.max(PPS_MIN, next))
  if (!el) {
    pps.value = clamped
    return
  }
  const t = range.value.t0 + ((el.scrollLeft + anchorPx) / pps.value) * SEC
  pps.value = clamped
  await nextTick()
  el.scrollLeft = X(t) - anchorPx
  onScroll()
}

/** 段階のボタンの中心：「いま」が見えていれば「いま」、そうでなければ本体の中央 */
function levelAnchor(): number {
  const x = X(now.value) - scrollLeft.value
  return x >= 0 && x <= bodyW.value ? x : bodyW.value / 2
}

function pickZoom(k: ZoomKey): void {
  setQuery({ zoom: k === 'min' ? '' : k })
}

watch(
  () => route.query.zoom,
  (z) => void setPps(ZOOMS[zoomOf(z)], levelAnchor()),
)

const currentZoom = computed(() => zoomAt(pps.value))

function onWheel(e: WheelEvent): void {
  if (!(e.ctrlKey || e.metaKey)) return
  e.preventDefault()
  const el = scroller.value
  if (!el) return
  const px = e.clientX - el.getBoundingClientRect().left - LW
  void setPps(pps.value * Math.exp(-e.deltaY * 0.002), Math.max(0, px))
}

/** `[いま]`：「いま」を本体の幅の 30% の位置へ送る */
function toNow(): void {
  scrollToTime(now.value)
}

function scrollToTime(t: number): void {
  const el = scroller.value
  if (!el) return
  el.scrollLeft = Math.max(0, X(t) - bodyW.value * 0.3)
  onScroll()
}

const zoomText = computed(() => {
  const p = pps.value
  if (p >= 1) return uiText('1秒＝{value0}px', { value0: +p.toFixed(2) })
  if (p * 60 >= 6) return uiText('1分＝{value0}px', { value0: +(p * 60).toFixed(1) })
  return uiText('1時間＝{value0}px', { value0: Math.round(p * 3600) })
})

// ── 時間軸の目盛り ───────────────────────────────────────────

const visible = computed(() => {
  const x0 = Math.max(0, scrollLeft.value - 80)
  const x1 = Math.min(trackW.value, scrollLeft.value + bodyW.value + 80)
  const toT = (x: number) => range.value.t0 + (x / pps.value) * SEC
  return { x0, x1, t0: toT(x0), t1: toT(x1) }
})

const scale = computed(() => tickScale(pps.value))
const NOW_TAG_W = 64

const axisTicks = computed(() => {
  const { t0, t1 } = visible.value
  const sc = scale.value
  const nx = X(now.value)
  const near = (x: number, w: number) => step.value === null && x + w > nx - NOW_TAG_W / 2 - 4 && x < nx + NOW_TAG_W / 2 + 4
  const ticks: { x: number; major: boolean }[] = []
  const minors: { x: number; text: string }[] = []
  const majors: { x: number; text: string; left: boolean }[] = []
  for (const t of wallTicks(viewTz.value, t0, t1, sc.minor)) {
    const x = X(t)
    const major = isWallMultiple(viewTz.value, t, sc.major)
    ticks.push({ x, major })
    if (major) {
      const text = clock(viewTz.value, t, sc.major < 60)
      const w = text.length * 7 + 8
      if (!near(x, w)) majors.push({ x, text, left: false })
      else if (!near(x - w - 8, w)) majors.push({ x, text, left: true })
    } else {
      const text = clock(viewTz.value, t, sc.minor < 60)
      if (!near(x, text.length * 6.5 + 6)) minors.push({ x, text })
    }
  }
  return { ticks, minors, majors }
})

const gridLines = computed(() => {
  const { t0, t1 } = visible.value
  const sc = scale.value
  return wallTicks(viewTz.value, t0, t1, sc.fine).map((t) => ({
    x: X(t) - visible.value.x0,
    cls: isWallMultiple(viewTz.value, t, sc.major) ? 'M' : isWallMultiple(viewTz.value, t, sc.minor) ? 'm' : 'f',
  }))
})

const cornerDate = computed(() => monthDay(viewTz.value, now.value))
const cornerZone = computed(() =>
  baseTz.value === viewTz.value
    ? uiText('基準 {value0}', { value0: baseTz.value })
    : uiText('表示 {value0}（基準 {value1}）', { value0: viewTz.value, value1: baseTz.value }),
)

// ── 行とバー ─────────────────────────────────────────────────

const rows = computed<RowLayout[]>(() => layoutRows(rowSet.value.epics, display.value.bars, display.value.gone))
const rowsH = computed(() => rows.value.reduce((h, r) => h + r.height, 0))

/** ドラッグ中の予定（掴んでいる間はバーだけが動く） */
const override = shallowRef<Map<number, { s: number | null; e: number | null }>>(new Map())
/** 試算の応答を待っているバー（破線で描く） */
const pending = shallowRef<Set<number>>(new Set())

const detailSeq = computed<number | null>(() => {
  const raw = route.params.seq
  const n = typeof raw === 'string' ? Number(raw) : Number.NaN
  return Number.isInteger(n) && n > 0 ? n : null
})

/** 試算で動くバー（もとのバーを沈める） */
const movingSeqs = computed(() => new Set(activeChanges.value.map((c) => c.seq)))

interface BarView {
  bar: Bar
  key: string
  x: number
  w: number
  y: number
  cls: string[]
  tip: string
  label: string
  rest: string
  restDanger: boolean
  isNew: boolean
  dev: { text: string; up: boolean; title: string } | null
  handleS: boolean
  handleE: boolean
  flashDelay: number | null
  who: string
  inset: number
}

function timeRange(s: number | null, e: number | null): string {
  const f = (t: number) => clock(viewTz.value, t, withSec.value)
  if (s !== null && e !== null) return `${f(s)}–${f(e)}`
  return s !== null ? `${f(s)}–` : e !== null ? `–${f(e)}` : ''
}

function barView(b: Bar, r: RowLayout): BarView {
  const o = override.value.get(b.seq)
  const s = o ? o.s : b.s
  const e = o ? o.e : b.e
  const half = s === null ? 'e' : e === null ? 's' : null
  let x: number
  let w: number
  if (half === 's') {
    x = X(s!)
    w = HALF_W
  } else if (half === 'e') {
    x = X(e!) - HALF_W
    w = HALF_W
  } else {
    x = X(s!)
    w = Math.max(3, X(e!) - x)
  }
  const y = laneTop(r.lane.get(keyOf(b)) ?? 0)
  const mode = display.value.mode
  const base = display.value.base
  const bi = base?.get(b.seq)
  const isNew = base !== null && bi === undefined
  const dv = bi ? deviationOf({ s, e }, bi) : null
  let dev: BarView['dev'] = null
  if (dv) {
    const tag = deviationTag(dv)
    const v = `${tag.minutes > 0 ? '+' : tag.minutes < 0 ? '−' : '±'}${Math.abs(tag.minutes)}`
    dev = {
      text: tag.edge === 'end' ? uiText('終{value0}分', { value0: v }) : uiText('始{value0}分', { value0: v }),
      up: tag.late,
      title: uiText('当初計画から 始{value0} 終{value1}', { value0: fmtSigned(dv.ds), value1: fmtSigned(dv.de) }),
    }
  }
  let rest = ''
  let restDanger = false
  switch (b.state) {
    case 'todo':
    case 'plan':
      rest = timeRange(s, e)
      break
    case 'wait':
    case 'late':
      rest = `+${fmtDur(b.over)}`
      restDanger = true
      break
    case 'prog':
      rest = e !== null ? uiText('残り {value0}', { value0: fmtDur(e - now.value) }) : ''
      break
    case 'done':
      rest = uiText('{value0} 終了', { value0: clock(viewTz.value, b.closedAt ?? e ?? s ?? 0) })
      break
  }
  const editable = mode === 'day' && canPlan.value && !b.gone && s !== null && e !== null && trial.value === null && !committing.value
  const cat = b.ticket?.status.category
  const handleE = editable && b.state !== 'done' && !b.pe
  const handleS = editable && cat === 'todo' && !b.ps
  const f = mode === 'day' ? flash.value.get(b.seq) : undefined
  const flashAge = f ? Date.now() - f.at : Infinity
  const label = stateLabel(b.state)
  const tipParts = [
    b.title,
    `${timeRange(s, e)}${s !== null && e !== null ? `（${fmtDur(e - s)}）` : ''} ${label}`,
    b.chk ? '' : uiText('時間重複チェック無し'),
    b.ps ? uiText('開始のアンカー') : '',
    b.pe ? uiText('終了のアンカー') : '',
    dev?.title ?? '',
  ].filter((p) => p !== '')
  const cls = [
    'bar',
    `st-${b.state}`,
    b.chk ? 'chk' : 'free',
    widthClass(w),
    half ? `half-${half}` : '',
    detailSeq.value === b.seq ? 'sel' : '',
    movingSeqs.value.has(b.seq) ? 'tr-from' : '',
    pending.value.has(b.seq) ? 'pending' : '',
    flashAge < FLASH_MS ? 'flash' : '',
    handleE || handleS ? 'editable' : '',
  ].filter((c) => c !== '')
  // 見えている範囲の左端より前から始まるバーは、文字を見えている左端へ寄せる
  const inset = Math.max(0, Math.min(scrollLeft.value - x, w - 60))
  return {
    bar: b,
    key: keyOf(b),
    x,
    w,
    y,
    cls,
    tip: tipParts.join('\n'),
    label,
    rest,
    restDanger,
    isNew,
    dev,
    handleS,
    handleE,
    flashDelay: flashAge < FLASH_MS ? flashAge : null,
    who: flashAge < FLASH_MS ? (f?.who ?? '') : '',
    inset,
  }
}

interface RowView {
  row: RowLayout
  bars: BarView[]
  frames: { key: string; x: number; w: number; y: number; title: string }[]
  gone: { key: string; x: number; w: number; y: number; title: string; lx: number; lw: number }[]
  coll: { x: number; w: number }[]
  fs: { key: string; joined: boolean; x: number; w: number; y: number; title: string }[]
  lateExt: { key: string; x: number; w: number; y: number; title: string }[]
  ghosts: GhostView[]
  nChk: number
  nFree: number
}

interface GhostView {
  key: string
  x: number
  w: number
  y: number
  cls: string
  label: string
}

function spanX(s: number | null, e: number | null): { x: number; w: number } {
  const a = s ?? e ?? 0
  const z = e ?? s ?? 0
  const x = X(a)
  return { x, w: Math.max(3, X(z) - x) }
}

const rowViews = computed<RowView[]>(() =>
  rows.value.map((r) => {
    const base = display.value.base
    const frames: RowView['frames'] = []
    const bars = r.bars.map((b) => barView(b, r))
    if (base) {
      for (const v of bars) {
        const bi = base.get(v.bar.seq)
        if (!bi || !v.dev) continue
        const { x, w } = spanX(bi.start_at, bi.due_at)
        frames.push({ key: v.key, x, w, y: v.y, title: uiText('版の位置 {value0}', { value0: timeRange(bi.start_at, bi.due_at) }) })
      }
    }
    const gone = r.gone.map((g) => {
      const { x, w } = spanX(g.s, g.e)
      // 文字は影の幅に収め、見えている範囲の左端より前から始まる影は文字を左端へ寄せる（バーと同じ）
      const inset = Math.max(0, Math.min(scrollLeft.value - x, w - 60))
      return { key: keyOf(g), x, w, y: laneTop(r.lane.get(keyOf(g)) ?? 0), title: g.title, lx: x + 4 + inset, lw: Math.max(0, w - 8 - inset) }
    })
    const mode = display.value.mode
    const coll = mode === 'day' ? collisions(r.bars).map((c) => ({ x: X(c.s), w: Math.max(2, X(c.e) - X(c.s)) })) : []
    const fs =
      mode === 'day'
        ? fsMarks(links.value, r.bars).map((m) => {
            const y = laneTop(r.lane.get(keyOf(m.to)) ?? 0) + BAR_H / 2
            const title = uiText('FS：{value0} → {value1}（隙間を空けない）', { value0: m.from.title, value1: m.to.title })
            if (m.joined) return { key: `${m.from.seq}-${m.to.seq}`, joined: true, x: X(m.to.s!), w: 0, y, title }
            return { key: `${m.from.seq}-${m.to.seq}`, joined: false, x: X(m.from.e!) + 2, w: Math.max(0, X(m.to.s!) - X(m.from.e!) - 4), y, title }
          })
        : []
    const lateExt = bars
      .filter((v) => v.bar.state === 'late' && v.bar.e !== null)
      .map((v) => ({
        key: v.key,
        x: X(v.bar.e!),
        w: Math.max(0, X(now.value) - X(v.bar.e!)),
        y: v.y + BAR_H + 3,
        title: uiText('押している：{value0}', { value0: fmtDur(v.bar.over) }),
      }))
    return {
      row: r,
      bars,
      frames,
      gone,
      coll,
      fs,
      lateExt,
      ghosts: ghostsFor(r),
      nChk: r.bars.filter((b) => b.chk).length,
      nFree: r.bars.filter((b) => !b.chk).length,
    }
  }),
)

// ── 連鎖のずらし（試算の影 → 確定）──────────────────────────

interface Trial {
  seq: number
  title: string
  op: 'end' | 'start' | 'now' | 'compact'
  version: number
  body: RescheduleRequest
  from: number | null
  to: number | null
  result: RescheduleResult | null
  stale: boolean
  showAlt: boolean
}

const trial = shallowRef<Trial | null>(null)
const committing = ref(false)
/** 操作の結果（通知の行。6.4。トーストにしない） */
const result = ref<{ kind: 'ok' | 'error'; text: string } | null>(null)
let resultTimer: ReturnType<typeof setTimeout> | undefined

function setResult(kind: 'ok' | 'error', text: string): void {
  result.value = { kind, text }
  clearTimeout(resultTimer)
  if (kind === 'ok') resultTimer = setTimeout(() => (result.value = null), 8000)
}

const activeChanges = computed<RescheduleChange[]>(() => {
  const t = trial.value
  if (!t?.result) return []
  if (t.showAlt && t.result.alternative) return t.result.alternative.changes
  return t.result.changes
})

function ticketOf(seq: number): TlTicket | undefined {
  return tickets.value.find((t) => t.seq === seq)
}

async function startTrial(seq: number, op: Trial['op'], edge?: { start_at?: number; due_at?: number }): Promise<void> {
  const t = ticketOf(seq)
  if (!t) return
  const body: RescheduleRequest =
    op === 'compact'
      ? { op: 'compact', on_block: 'reject', dry_run: true }
      : { op: 'move', on_block: 'reject', dry_run: true, ...edge }
  const isStart = op === 'start'
  const tr: Trial = {
    seq,
    title: t.title,
    op,
    version: t.version,
    body,
    from: isStart ? t.start_at : t.due_at,
    to: isStart ? (edge?.start_at ?? null) : (edge?.due_at ?? null),
    result: null,
    stale: false,
    showAlt: false,
  }
  result.value = null
  trial.value = tr
  pending.value = new Set([seq])
  await runTrial(tr)
}

async function runTrial(tr: Trial): Promise<void> {
  try {
    const res = await tlApi.reschedule(projectKey.value, tr.seq, tr.version, tr.body)
    if (trial.value !== tr) return
    const target = res.changes.find((c) => c.seq === tr.seq)
    if (target) {
      tr.from = tr.op === 'start' ? target.before.start_at : target.before.due_at
      tr.to = tr.op === 'start' ? target.after.start_at : target.after.due_at
    }
    trial.value = { ...tr, result: res }
  } catch (e) {
    if (trial.value !== tr) return
    trial.value = null
    setResult('error', toApiError(e).message)
    flushDeferred()
  } finally {
    pending.value = new Set()
    override.value = new Map()
  }
}

function cancelTrial(): void {
  if (trial.value === null || committing.value) return
  trial.value = null
  pending.value = new Set()
  override.value = new Map()
  flushDeferred()
}

async function commitTrial(useAlt = false): Promise<void> {
  const tr = trial.value
  const res = tr?.result
  if (!tr || !res || committing.value) return
  if (useAlt && !res.alternative) return
  if (!useAlt && res.result !== 'ok') return
  if (useAlt) trial.value = { ...tr, showAlt: true }
  const changes = useAlt ? res.alternative!.changes : res.changes
  const body: RescheduleRequest = {
    ...tr.body,
    dry_run: false,
    on_block: useAlt ? 'shrink' : 'reject',
    expect: expectOf(changes, tr.seq),
  }
  committing.value = true
  try {
    const done = await tlApi.reschedule(projectKey.value, tr.seq, tr.version, body)
    const items = done.items ?? []
    for (const it of items) myVersions.set(it.seq, it.version)
    if (items.length > 0) {
      const by = new Map(items.map((i) => [i.seq, i]))
      tickets.value = tickets.value.map((t) => (by.has(t.seq) ? { ...t, ...by.get(t.seq)! } : t))
    }
    trial.value = null
    setResult('ok', uiText('確定しました（{value0}件を動かしました）', { value0: done.changes.length }))
  } catch (e) {
    const err = toApiError(e)
    if (err.status === 409) {
      // 他の人が先に動かした（`stale_plan`）か、きっかけの版が違う。取り直して試算し直し、もう一度確認を求める
      await reloadNow()
      const fresh = ticketOf(tr.seq)
      if (fresh) {
        const again: Trial = { ...tr, version: fresh.version, result: null, stale: true, showAlt: false }
        trial.value = again
        committing.value = false
        await runTrial(again)
        if (trial.value) trial.value = { ...trial.value, stale: true }
        return
      }
    }
    trial.value = null
    setResult('error', err.message)
  } finally {
    committing.value = false
    flushDeferred()
  }
}

/** 試算し直す前に、いまの状態を取り直して当てる（定期取得の結果は待たない） */
async function reloadNow(): Promise<void> {
  try {
    const res = await tlApi.getTimeline(projectKey.value, fetchQuery.value, null)
    if (res.status === 200) {
      etag = res.etag
      deferred = null
      apply(res.data, true)
    }
  } catch {
    // 取り直せなくても、試算し直しの応答で確かめる
  }
}

function ghostsFor(r: RowLayout): GhostView[] {
  const t = trial.value
  if (!t?.result) return []
  const out: GhostView[] = []
  const alt = t.showAlt && t.result.alternative !== null
  for (const c of activeChanges.value) {
    const bar = r.bars.find((b) => b.seq === c.seq)
    if (!bar) continue
    const y = laneTop(r.lane.get(keyOf(bar)) ?? 0)
    const x = X(c.after.start_at)
    const w = Math.max(3, X(c.after.due_at) - x)
    const ds = c.after.start_at - c.before.start_at
    const de = c.after.due_at - c.before.due_at
    let label: string
    if (c.seq === t.seq) {
      label =
        t.op === 'start'
          ? uiText('開始 {value0}', { value0: clock(viewTz.value, c.after.start_at, withSec.value) })
          : uiText('終了 {value0}', { value0: clock(viewTz.value, c.after.due_at, withSec.value) })
    } else if (ds === de) {
      label = fmtSigned(ds)
    } else {
      label = [ds !== 0 ? uiText('始{value0}', { value0: fmtSigned(ds) }) : '', de !== 0 ? uiText('終{value0}', { value0: fmtSigned(de) }) : '']
        .filter((p) => p !== '')
        .join(' ')
    }
    out.push({ key: `g${c.seq}`, x, w, y, cls: `${c.seq === t.seq ? 'src' : ''} ${alt ? 'sug' : ''}`, label })
  }
  const block = t.result.block
  if (t.result.result === 'blocked' && block && !alt) {
    const bar = r.bars.find((b) => b.seq === block.seq)
    if (bar) {
      const { x, w } = spanX(bar.s, bar.e)
      const label =
        block.code === 'overlaps_previous'
          ? uiText('前は動かしません')
          : uiText('{value0} 足りません', { value0: fmtDur(block.short_ms) })
      out.push({ key: `b${block.seq}`, x, w, y: laneTop(r.lane.get(keyOf(bar)) ?? 0), cls: 'block', label })
    }
  }
  return out
}

/** 影が画面の外にあれば、その時刻（`[13:20 の影へ →]`） */
const farGhost = computed<number | null>(() => {
  const t = trial.value
  if (!t?.result) return null
  let at: number | null = null
  for (const c of activeChanges.value) at = Math.max(at ?? 0, c.after.start_at)
  if (at === null) return null
  const x = X(at)
  return x < scrollLeft.value || x > scrollLeft.value + bodyW.value - 40 ? at : null
})

/** 試算の通知の中身 */
const trialNotice = computed(() => {
  const t = trial.value
  if (!t) return null
  const what = t.op === 'start' ? uiText('開始') : uiText('終了')
  const opName = t.op === 'now' ? uiText('［いままで延ばす］') : t.op === 'compact' ? uiText('［ここで詰める］') : ''
  const head =
    t.from !== null && t.to !== null
      ? uiText('{value0} の{value1} {value2} → {value3}（{value4}）', {
          value0: shortTitle(t.title),
          value1: what,
          value2: clock(viewTz.value, t.from, withSec.value),
          value3: clock(viewTz.value, t.to, withSec.value),
          value4: fmtSigned(t.to - t.from),
        })
      : shortTitle(t.title)
  if (!t.result) return { kind: 'pending' as const, opName, head, tail: uiText('試算しています…'), alt: '' }
  if (t.result.result === 'ok') {
    const moved = t.result.changes.filter((c) => c.seq !== t.seq)
    const names = moved.map((c) => shortTitle(ticketOf(c.seq)?.title ?? `pb-${c.seq}`, 8)).join('・')
    const tk = ticketOf(t.seq)
    const tail = !tk?.no_overlap
      ? uiText('時間重複チェック無しなので、ほかは動きません')
      : moved.length > 0
        ? uiText('後ろの{value0}件が動きます（{value1}）', { value0: moved.length, value1: names })
        : t.to !== null && t.from !== null && t.to < t.from
          ? uiText('後ろは動きません（隙間が空きます）')
          : uiText('隙間で吸収し、後ろは動きません')
    return { kind: 'ok' as const, opName, head, tail, alt: '' }
  }
  const msg = t.result.block?.message ?? ''
  let alt = ''
  if (t.result.alternative) {
    const shrunk = t.result.alternative.changes.filter((c) => c.reason === 'shrink' && c.seq !== t.seq)
    alt = shrunk
      .map((c) =>
        uiText('{value0} を {value1}', {
          value0: shortTitle(ticketOf(c.seq)?.title ?? `pb-${c.seq}`, 8),
          value1: fmtDur(c.after.due_at - c.after.start_at),
        }),
      )
      .join('、')
    if (alt === '') alt = uiText('縮めて')
  }
  return { kind: 'blocked' as const, opName, head, tail: msg, alt }
})

// ── ドラッグ ─────────────────────────────────────────────────

interface Drag {
  seq: number
  edge: 's' | 'e'
  x0: number
  orig: number
  cur: number
  s: number
  e: number
}

const drag = shallowRef<Drag | null>(null)

function onHandleDown(ev: PointerEvent, v: BarView, edge: 's' | 'e'): void {
  if (ev.button !== 0 || v.bar.s === null || v.bar.e === null) return
  const el = ev.currentTarget as HTMLElement
  el.setPointerCapture(ev.pointerId)
  const orig = edge === 'e' ? v.bar.e : v.bar.s
  drag.value = { seq: v.bar.seq, edge, x0: ev.clientX, orig, cur: orig, s: v.bar.s, e: v.bar.e }
  result.value = null
  const move = (m: PointerEvent) => {
    const d = drag.value
    if (!d) return
    const raw = d.orig + ((m.clientX - d.x0) / pps.value) * SEC
    let next = snapTo(raw, m.altKey ? 1 : snapSec.value)
    if (d.edge === 'e') next = Math.max(next, d.s + SEC)
    else next = Math.min(next, d.e - SEC)
    if (next === d.cur) return
    drag.value = { ...d, cur: next }
    override.value = new Map([[d.seq, d.edge === 'e' ? { s: d.s, e: next } : { s: next, e: d.e }]])
  }
  const up = () => {
    el.removeEventListener('pointermove', move)
    el.removeEventListener('pointerup', up)
    el.removeEventListener('pointercancel', cancel)
    const d = drag.value
    drag.value = null
    if (!d || d.cur === d.orig) {
      override.value = new Map()
      flushDeferred()
      return
    }
    void startTrial(d.seq, d.edge === 'e' ? 'end' : 'start', d.edge === 'e' ? { due_at: d.cur } : { start_at: d.cur })
  }
  const cancel = () => {
    el.removeEventListener('pointermove', move)
    el.removeEventListener('pointerup', up)
    el.removeEventListener('pointercancel', cancel)
    drag.value = null
    override.value = new Map()
    flushDeferred()
  }
  el.addEventListener('pointermove', move)
  el.addEventListener('pointerup', up)
  el.addEventListener('pointercancel', cancel)
}

function onKeydown(e: KeyboardEvent): void {
  const target = e.target as HTMLElement | null
  if (target?.closest?.('input, textarea, select, [contenteditable="true"], [role="dialog"]')) return
  if (trial.value === null) return
  if (e.key === 'Escape') {
    e.preventDefault()
    e.stopPropagation()
    cancelTrial()
  } else if (e.key === 'Enter' && trial.value.result?.result === 'ok') {
    e.preventDefault()
    void commitTrial()
  }
}

// ── 選んだバーの操作（`[いままで延ばす]`・`[ここで詰める]`）──────────

const selectedTicket = computed(() => (detailSeq.value === null ? undefined : ticketOf(detailSeq.value)))
const selectedAction = computed(() => {
  const t = selectedTicket.value
  if (!t || !canPlan.value || step.value !== null || trial.value !== null) return null
  if (canExtendToNow(t, now.value)) {
    return { op: 'now' as const, text: uiText('{value0}：押している {value1}', { value0: shortTitle(t.title), value1: fmtDur(now.value - t.due_at!) }) }
  }
  if (canCompact(t, snapSec.value)) {
    return { op: 'compact' as const, text: uiText('{value0}：{value1} 早く終わりました', { value0: shortTitle(t.title), value1: fmtDur(t.due_at! - t.closed_at!) }) }
  }
  return null
})

function runSelectedAction(): void {
  const a = selectedAction.value
  const t = selectedTicket.value
  if (!a || !t) return
  if (a.op === 'now') void startTrial(t.seq, 'now', { due_at: extendToNowDue(t.due_at!, Date.now(), snapSec.value) })
  else void startTrial(t.seq, 'compact')
}

// ── 詳細ペイン（5.14「詳細ペイン」と同じ作り）──────────────────

const detailLinkQuery = computed(() => ({ ...tlQuery.value, from: 'timeline' }))

function openDetail(seq: number, e?: MouseEvent): void {
  const to = { path: `/p/${projectKey.value}/tickets/${seq}`, query: detailLinkQuery.value }
  if (e && (e.metaKey || e.ctrlKey || e.shiftKey)) {
    window.open(router.resolve(to).href, '_blank', 'noopener')
    return
  }
  void router.replace(to)
}

function closeDetail(): void {
  void router.replace({ path: `/p/${projectKey.value}/timeline`, query: tlQuery.value })
}

/** 詳細で直した値は、その行だけ差し替えて描き直す */
function onDetailUpdated(next: TicketDetail): void {
  myVersions.set(next.seq, next.version)
  tickets.value = tickets.value.map((t) => (t.seq === next.seq ? { ...t, ...next } : t))
}

function onDetailDeleted(): void {
  closeDetail()
  void load(true)
}

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
  const move = (ev: PointerEvent) => {
    paneWidth.value = Math.min(PANE_MAX, Math.max(PANE_MIN, Math.round(w0 + (x0 - ev.clientX))))
  }
  const up = () => {
    grip.removeEventListener('pointermove', move)
    grip.removeEventListener('pointerup', up)
    try {
      localStorage.setItem(PANE_KEY, String(paneWidth.value))
    } catch {
      // 幅は失われてよい情報である
    }
  }
  grip.addEventListener('pointermove', move)
  grip.addEventListener('pointerup', up)
}

// ── 見出しの文言 ─────────────────────────────────────────────

const pollAge = computed(() => {
  if (lastFetchAt.value === null) return ''
  return uiText('⟳ {value0}秒前', { value0: Math.max(0, Math.round((tickNow.value - lastFetchAt.value) / SEC)) })
})
const pollWarn = computed(() => failCount.value >= 3)
const truncated = computed(() => total.value > tickets.value.length)
const otherVisible = computed(() => (other.value && tickNow.value - other.value.at < OTHER_MS ? other.value : null))

/** 空状態（5.15「何も選んでいないとき」）。版の行があれば行は出し、上に促す帯を置く */
const emptyState = computed(() => emptyStateOf(projectStore.current !== null && rowsReady.value, timelineEpics.value.length, rowSet.value.epics.length))
const noEpics = computed(() => emptyState.value === 'select')

const stepName = computed(() => {
  if (step.value === null) return ''
  const v = versionsAsc.value[step.value]
  return v ? fullNameOf(v) : uiText('いま（当日の予定）')
})

const zoomLabelSources: Record<ZoomKey, string> = { sec: '秒', min: '分', hour: '時' }

const baselineChip = computed(() =>
  selectedBaseline.value ? nameOf(selectedBaseline.value) : uiText('重ねない'),
)

function baselineMenuItems(): { key: string; label: string; danger: boolean }[] {
  return [{ key: 'delete', label: uiText('この版を消す'), danger: true }]
}

// ── 起動と追随 ───────────────────────────────────────────────

let clockTimer: ReturnType<typeof setInterval> | undefined
let resizeObserver: ResizeObserver | null = null
let placed = false

function startAll(): void {
  placed = false
  loaded.value = false
  tickets.value = []
  epics.value = []
  links.value = []
  baselineList.value = []
  baselineListLoaded.value = false
  baselineCache.value = new Map()
  baselinesInfo.value = null
  step.value = null
  trial.value = null
  myVersions.clear()
  void projectStore.fetchCurrent(projectKey.value)
  void epicChoices.load(projectKey.value)
  void tagsApi
    .listTags(projectKey.value)
    .then((r) => (tags.value = r.items))
    .catch(() => (tags.value = []))
  void loadBaselineList()
  void load(true)
}

onMounted(() => {
  loadPaneWidth()
  window.addEventListener('keydown', onKeydown, true)
  document.addEventListener('pointerdown', onDocumentPointerDown)
  document.addEventListener('visibilitychange', onVisibility)
  startAll()
  pollTimer = setInterval(poll, POLL_MS)
  clockTimer = setInterval(() => {
    const t = Date.now()
    tickNow.value = t
    // 「いま」の線は、秒の段階では毎秒、それ以外は毎分だけ描き直す
    if (withSec.value || Math.floor(t / MIN) !== Math.floor(now.value / MIN)) now.value = t
  }, SEC)
  resizeObserver = new ResizeObserver(() => {
    bodyW.value = Math.max(100, (scroller.value?.clientWidth ?? 900) - LW)
  })
  if (scroller.value) resizeObserver.observe(scroller.value)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown, true)
  document.removeEventListener('pointerdown', onDocumentPointerDown)
  document.removeEventListener('visibilitychange', onVisibility)
  clearInterval(pollTimer)
  clearInterval(clockTimer)
  clearTimeout(otherTimer)
  clearTimeout(flashTimer)
  clearTimeout(resultTimer)
  resizeObserver?.disconnect()
  projectStore.clearCurrent()
})

// 最初に取れたら「いま」を `[いま]` と同じ位置に置く
watch(loaded, async (v) => {
  if (!v || placed) return
  placed = true
  await nextTick()
  bodyW.value = Math.max(100, (scroller.value?.clientWidth ?? 900) - LW)
  toNow()
})

watch(fetchKey, () => {
  if (route.params.key === undefined) return
  cancelTrial()
  void load(true)
})

watch(projectKey, (key, prev) => {
  if (key === '' || key === prev) return
  startAll()
})
</script>

<template>
  <div class="tl-page" :data-mode="display.mode">
    <PageHeader :title="$ui('タイムライン')">
      <template #actions>
        <div ref="toolsEl" class="tl-tools">
          <!-- 行（見る人ごとの絞り込み。URL の parent） -->
          <div class="tl-dd">
            <button type="button" class="tl-chip" :class="{ on: rowFiltered }" :aria-expanded="openMenu === 'rows'" @click="toggleMenu('rows')">
              <span class="k">{{ $ui('表示するエピック') }}</span> {{ rowSelected.length }}/{{ rowChoices.length }} ▾
            </button>
            <div v-if="openMenu === 'rows'" class="tl-panel">
              <p class="ph">{{ $ui('見る行（エピック）を選ぶ。URL に残ります') }}</p>
              <label v-for="c in rowChoices" :key="c.seq" class="tl-check">
                <input
                  type="checkbox"
                  :checked="rowSelected.includes(c.seq)"
                  @change="setRows(($event.target as HTMLInputElement).checked ? [...rowSelected, c.seq] : rowSelected.filter((s) => s !== c.seq))"
                />
                <span class="sw" :style="sleeveStyle(c.seq)" aria-hidden="true"></span>
                <span class="tl-check-title">{{ c.title }}</span>
              </label>
              <div class="pf">
                <button type="button" class="secondary small" @click="setRows([])">{{ $ui('全行') }}</button>
              </div>
            </div>
          </div>
          <span v-if="rowSummary" class="tl-rowsum" :title="rowSummary">{{ rowSummary }}</span>

          <div class="tl-dd">
            <button type="button" class="tl-chip" :class="{ on: filterCount > 0 }" :aria-expanded="openMenu === 'filter'" @click="toggleMenu('filter')">
              {{ $ui('絞り込み') }}<template v-if="filterCount > 0">（{{ filterCount }}）</template> ▾
            </button>
            <div v-if="openMenu === 'filter'" class="tl-panel tl-filter">
              <label class="filter">
                <span class="filter-label">{{ $ui('担当') }}</span>
                <select :value="queryValue('assignee')" @change="setQuery({ assignee: ($event.target as HTMLSelectElement).value })">
                  <option value="">{{ $ui('すべて') }}</option>
                  <option value="me">{{ $ui('自分') }}</option>
                  <option value="none">{{ $ui('未割当') }}</option>
                  <option v-for="m in members" :key="m.actor_id" :value="m.actor_id">{{ m.display_name }}</option>
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
                    <option v-for="c in statusCategoryOrder" :key="c" :value="`status_category:${c}`">{{ statusCategoryLabels[c] }}</option>
                  </optgroup>
                  <optgroup :label="$ui('ステータス')">
                    <option v-for="s in statuses" :key="s.key" :value="`status:${s.key}`">{{ statusLabel(s.key, s.name) }}</option>
                  </optgroup>
                </select>
              </label>
              <label class="filter">
                <span class="filter-label">{{ $ui('タグ') }}</span>
                <select :value="queryValue('tag')" @change="setQuery({ tag: ($event.target as HTMLSelectElement).value })">
                  <option value="">{{ $ui('すべて') }}</option>
                  <option value="none">{{ $ui('未分類') }}</option>
                  <option v-for="t in tags" :key="t.id" :value="t.id">{{ t.name }}</option>
                </select>
              </label>
              <button type="button" class="secondary small" :disabled="filterCount === 0" @click="clearFilters">{{ $ui('解除') }}</button>
            </div>
          </div>

          <div class="tl-seg" role="group" :aria-label="$ui('拡大縮小の段階')">
            <button v-for="k in ZOOM_ORDER" :key="k" type="button" :aria-pressed="currentZoom === k" @click="pickZoom(k)">
              {{ $ui(zoomLabelSources[k]) }}
            </button>
          </div>
          <button type="button" class="secondary small" @click="toNow">{{ $ui('いま') }}</button>
          <span class="tl-vsep" aria-hidden="true"></span>

          <div class="tl-dd">
            <button type="button" class="tl-chip" :class="{ on: selectedBaseline !== null }" :title="baselineChip" :aria-expanded="openMenu === 'baseline'" @click="toggleMenu('baseline')">
              <span class="k">{{ $ui('当初計画') }}</span> {{ baselineChip }} ▾
            </button>
            <div v-if="openMenu === 'baseline'" class="tl-panel right tl-versions">
              <p class="ph">{{ $ui('重ねる版（保存した日時の新しい順）') }}</p>
              <label class="tl-check">
                <input type="radio" name="tl-baseline" :checked="selectedBaselineId === null" @change="setQuery({ baseline: 'none' }); openMenu = null" />
                {{ $ui('重ねない') }}
              </label>
              <p v-if="baselineList.length === 0" class="tl-hint">{{ $ui('保存した版はまだありません') }}</p>
              <div v-for="b in baselineList" :key="b.id" class="tl-vrow">
                <label class="tl-check">
                  <input
                    type="radio"
                    name="tl-baseline"
                    :checked="selectedBaselineId === b.id"
                    @change="setQuery({ baseline: b.id === baselineList[0]?.id ? '' : b.id }); openMenu = null"
                  />
                  <span class="tl-check-title">{{ nameOf(b) }}</span>
                  <span class="tl-who">{{ b.created_by.display_name }}</span>
                </label>
                <UserActionsMenu
                  v-if="canProjectEdit"
                  compact
                  :items="baselineMenuItems()"
                  :label="$ui('{value0} の版の操作', { value0: nameOf(b) })"
                  @select="deleting = b"
                />
              </div>
              <div class="pf">
                <button type="button" class="secondary small" :disabled="baselineList.length === 0" @click="startStep">
                  {{ $ui('版を順に送る（最初の版 → いま）') }}
                </button>
              </div>
            </div>
          </div>
          <button
            type="button"
            class="secondary small"
            :aria-pressed="step !== null"
            :disabled="baselineList.length === 0"
            @click="step === null ? startStep() : endStep()"
          >
            {{ $ui('版を送る') }}
          </button>
          <button v-if="canProjectEdit" type="button" class="secondary small" @click="openSave">{{ $ui('計画を保存') }}</button>
          <div v-if="canProjectEdit" class="tl-dd">
            <button type="button" class="tl-chip" :aria-expanded="openMenu === 'epics'" @click="toggleMenu('epics')">{{ $ui('載せるエピック') }} ▾</button>
            <div v-if="openMenu === 'epics'" class="tl-panel right tl-epic-panel">
              <p class="ph">{{ $ui('タイムラインの行にするエピック（プロジェクトの設定）') }}</p>
              <EpicChecklist :choices="epicChoices.choices.value" :selected="epicDraft" :sleeve="sleeveOf" :disabled="epicSaving" @update="epicDraft = $event" />
              <p v-if="epicError" class="tl-err" role="alert">{{ epicError }}</p>
              <div class="pf">
                <button type="button" class="primary small" :disabled="epicSaving" @click="saveEpics">{{ epicSaving ? $ui('保存中…') : $ui('保存') }}</button>
                <button type="button" class="secondary small" :disabled="epicSaving" @click="openMenu = null">{{ $ui('キャンセル') }}</button>
              </div>
            </div>
          </div>
          <span class="tl-poll mono" :class="{ warn: pollWarn }" :title="$ui('5秒ごとに取り直します')">{{ pollAge }}</span>
        </div>
      </template>
    </PageHeader>

    <!-- 3回続けて取得に失敗したら、警告の面（8.4.1） -->
    <p v-if="pollWarn" class="tl-warn" role="alert">{{ $ui('最新の状態を取得できていません') }}</p>

    <!-- 通知の行（6.4。トーストにしない） -->
    <div class="tl-notice" :class="{ trial: trialNotice && trialNotice.kind !== 'blocked', fail: trialNotice?.kind === 'blocked' || result?.kind === 'error', step: step !== null && !trialNotice }" role="status">
      <template v-if="trialNotice">
        <span class="lbl">{{ trialNotice.kind === 'blocked' ? $ui('解けません') : $ui('試算') }}{{ trialNotice.opName }}</span>
        <span class="msg" :title="`${trialNotice.head}${trialNotice.tail}`">
          <span v-if="trial?.stale" class="dz">{{ $ui('他の人の変更を受けて試算し直しました。') }}</span>
          <b>{{ trialNotice.head }}</b><template v-if="trialNotice.kind === 'blocked'">：{{ trialNotice.tail }}。{{ $ui('既定では書きません') }}<template v-if="trialNotice.alt">{{ $ui('——縮めれば収まります') }}</template></template><template v-else>。{{ trialNotice.tail }}</template>
        </span>
        <button v-if="farGhost !== null" type="button" class="secondary small" @click="scrollToTime(farGhost)">
          {{ $ui('{value0} の影へ →', { value0: clock(viewTz, farGhost) }) }}
        </button>
        <button v-if="trialNotice.kind === 'ok'" type="button" class="primary small" :disabled="committing" @click="commitTrial()">{{ $ui('確定') }}</button>
        <button v-if="trialNotice.kind === 'blocked' && trialNotice.alt" type="button" class="secondary small" :disabled="committing" @click="commitTrial(true)">
          {{ $ui('{value0} に縮めて確定', { value0: trialNotice.alt }) }}
        </button>
        <button type="button" class="secondary small" :disabled="committing" @click="cancelTrial">{{ $ui('取り消す') }}</button>
      </template>
      <template v-else-if="step !== null">
        <span class="lbl">{{ $ui('版を送る {value0}/{value1}', { value0: step + 1, value1: versionsAsc.length + 1 }) }}</span>
        <button type="button" class="secondary small sq" :disabled="step <= 0" :aria-label="$ui('前の版')" @click="moveStep(-1)">◀</button>
        <button type="button" class="secondary small sq" :disabled="step >= versionsAsc.length" :aria-label="$ui('次の版')" @click="moveStep(1)">▶</button>
        <span class="msg">
          {{ stepName }}
          <span class="muted">{{ step > 0 ? $ui('点線の枠＝前の版の位置（ずれたものだけ）') : $ui('最初の版') }}</span>
        </span>
        <button type="button" class="secondary small" @click="endStep">{{ $ui('終える') }}</button>
      </template>
      <template v-else-if="result">
        <span class="lbl">{{ result.kind === 'ok' ? $ui('結果') : $ui('失敗') }}</span>
        <span class="msg" :title="result.text">{{ result.text }}</span>
        <button type="button" class="tl-x" :aria-label="$ui('閉じる')" @click="result = null">✕</button>
      </template>
      <template v-else-if="selectedAction">
        <span class="msg">{{ selectedAction.text }}</span>
        <button type="button" class="primary small" @click="runSelectedAction">
          {{ selectedAction.op === 'now' ? $ui('いままで延ばす') : $ui('ここで詰める') }}
        </button>
      </template>
      <template v-else-if="truncated">
        <span class="msg">{{ $ui('{value0}件を超えています。行を絞り込むと残りを表示できます', { value0: uiNumber(LIMIT) }) }}</span>
      </template>
      <span v-if="otherVisible" class="other" :title="otherVisible.text">
        ⟳ {{ $ui('他の人の変更') }} <mark v-if="otherVisible.who">{{ otherVisible.who }}</mark> {{ otherVisible.text }}
      </span>
    </div>

    <EmptyState v-if="error" :title="$ui('チケットを取得できませんでした')" :description="error.message">
      <template #action>
        <button type="button" class="secondary" @click="load(true)">{{ $ui('再試行') }}</button>
      </template>
    </EmptyState>

    <EmptyState
      v-else-if="noEpics"
      :title="$ui('タイムラインに載せるエピックを選んでください')"
      :description="canProjectEdit ? undefined : $ui('プロジェクト管理者に依頼してください')"
    >
      <template v-if="canProjectEdit" #action>
        <button type="button" class="primary" @click="openEpicPicker">{{ $ui('載せるエピックを選ぶ') }}</button>
      </template>
    </EmptyState>

    <p v-if="!error && emptyState === 'band'" class="tl-select-band" role="status">
      <span>{{ $ui('タイムラインに載せるエピックを選んでください') }}</span>
      <span class="muted">{{ $ui('保存した版の計画だけを出しています') }}</span>
      <button v-if="canProjectEdit" type="button" class="secondary small" @click="openEpicPicker">{{ $ui('載せるエピックを選ぶ') }}</button>
      <span v-else class="muted">{{ $ui('プロジェクト管理者に依頼してください') }}</span>
    </p>

    <div v-show="!error && !noEpics" class="tl-body" :class="{ loading: loading && !loaded }">
      <div ref="scroller" class="tl-scroller" @scroll="onScroll" @wheel="onWheel">
        <div class="tl-canvas" :style="{ width: `${LW + trackW}px` }">
          <div class="tl-axis" :style="{ height: `${AH}px` }">
            <div class="tl-corner" :style="{ width: `${LW}px` }">
              <b>{{ cornerDate }}</b><br />{{ cornerZone }}
            </div>
            <div class="tl-axtrack" :style="{ width: `${trackW}px` }">
              <i v-for="(t, i) in axisTicks.ticks" :key="`t${i}`" class="ax-tk" :class="{ M: t.major }" :style="{ left: `${t.x}px` }"></i>
              <span v-for="(t, i) in axisTicks.minors" :key="`n${i}`" class="ax-min" :style="{ left: `${t.x}px` }">{{ t.text }}</span>
              <span v-for="(t, i) in axisTicks.majors" :key="`j${i}`" class="ax-maj" :class="{ lft: t.left }" :style="{ left: `${t.x}px` }">{{ t.text }}</span>
              <span v-if="step === null" class="nowtag" :style="{ left: `${X(now)}px` }">{{ clock(viewTz, now, withSec) }}</span>
            </div>
          </div>

          <svg
            class="tl-grid"
            :width="Math.max(0, visible.x1 - visible.x0)"
            :height="Math.max(rowsH, 80)"
            :style="{ left: `${LW + visible.x0}px`, top: `${AH}px` }"
            aria-hidden="true"
          >
            <line v-for="(g, i) in gridLines" :key="i" :class="g.cls" :x1="g.x + 0.5" :x2="g.x + 0.5" y1="0" :y2="Math.max(rowsH, 80)" />
          </svg>

          <div v-for="rv in rowViews" :key="rv.row.epic.seq" class="tl-row" :class="{ outside: rowSet.outside.has(rv.row.epic.seq) }" :style="{ height: `${rv.row.height}px` }">
            <div class="tl-rlabel" :style="{ width: `${LW}px`, height: `${rv.row.height}px` }">
              <span class="sleeve" :style="sleeveStyle(rv.row.epic.seq)" aria-hidden="true"></span>
              <div class="rn" :title="rv.row.epic.title">{{ rv.row.epic.title }}</div>
              <div class="rm"><span v-if="rowSet.kinds.get(rv.row.epic.seq) === 'outside'" class="tl-outside" :title="$ui('載せるエピックに入っていない行です。保存した版の計画だけを出しています')">{{ $ui('対象外') }}</span><span v-else-if="rowSet.kinds.get(rv.row.epic.seq) === 'missing'" class="tl-outside" :title="$ui('このエピックはもうありません。保存した版の計画だけを出しています')">{{ $ui('無いエピック') }}</span>pb-{{ rv.row.epic.seq }} · {{ $ui('本番 {value0}', { value0: rv.nChk }) }}<template v-if="rv.nFree"> · {{ $ui('裏 {value0}', { value0: rv.nFree }) }}</template></div>
              <template v-if="rv.row.lanes > 1">
                <span class="lanetag" :style="{ top: `${ROW_PAD + LANE_H / 2 - 5}px` }">{{ $ui('本番') }}</span>
                <span class="lanetag" :style="{ top: `${ROW_PAD + LANE_H * 1.5 - 5}px` }">{{ $ui('裏') }}{{ rv.row.lanes > 2 ? `×${rv.row.lanes - 1}` : '' }}</span>
              </template>
            </div>
            <div class="tl-track" :style="{ width: `${trackW}px`, height: `${rv.row.height}px` }">
              <div v-if="rv.row.lanes > 1" class="subzone" :style="{ top: `${ROW_PAD + LANE_H}px`, height: `${(rv.row.lanes - 1) * LANE_H}px` }"></div>
              <div
                v-for="f in rv.frames"
                :key="`f${f.key}`"
                class="base"
                :style="{ left: `${f.x}px`, width: `${f.w}px`, top: `${f.y}px`, height: `${BAR_H}px` }"
                :title="f.title"
              ></div>
              <template v-for="g in rv.gone" :key="`x${g.key}`">
                <div class="base gone" :style="{ left: `${g.x}px`, width: `${g.w}px`, top: `${g.y}px`, height: `${BAR_H}px` }" :title="$ui('版にあったが、いまは無い：{value0}', { value0: g.title })"></div>
                <div class="gonelbl" :style="{ left: `${g.lx}px`, maxWidth: `${g.lw}px`, top: `${g.y + BAR_H / 2 - 8}px` }" :title="$ui('消えた：{value0}', { value0: g.title })">{{ $ui('消えた：{value0}', { value0: g.title }) }}</div>
              </template>
              <div
                v-for="(c, i) in rv.coll"
                :key="`c${i}`"
                class="coll"
                :style="{ left: `${c.x}px`, width: `${c.w}px`, top: `${laneTop(0) - 3}px`, height: `${BAR_H + 6}px` }"
                :title="$ui('時間重複チェック付きどうしが重なっています')"
              ></div>
              <div
                v-for="v in rv.bars"
                :key="v.key"
                :class="v.cls"
                :style="{ left: `${v.x}px`, width: `${v.w}px`, top: `${v.y}px`, height: `${BAR_H}px`, animationDelay: v.flashDelay !== null ? `-${v.flashDelay}ms` : undefined }"
                :title="v.tip"
                :data-seq="v.bar.seq"
                @click="v.bar.gone || display.mode === 'step' ? undefined : openDetail(v.bar.seq, $event)"
              >
                <span class="in" :style="v.inset > 0 ? { marginLeft: `${v.inset}px`, maxWidth: `${v.w - v.inset}px` } : undefined">
                  <span class="l1"><span v-if="v.isNew" class="newtag">{{ $ui('計画外') }}</span>{{ v.bar.title }}</span>
                  <span class="l2"><span class="sl">{{ v.label }}</span><span class="rest" :class="{ dz: v.restDanger }">{{ v.rest }}</span><span v-if="v.dev" class="dv" :class="{ up: v.dev.up }" :title="v.dev.title">{{ v.dev.text }}</span></span>
                </span>
                <i v-if="v.bar.ps" class="pin s" aria-hidden="true"></i>
                <i v-if="v.bar.pe" class="pin e" aria-hidden="true"></i>
                <i v-if="v.handleE" class="hE" :title="$ui('ドラッグで終了を動かす')" @pointerdown.stop.prevent="onHandleDown($event, v, 'e')" @click.stop></i>
                <i v-if="v.handleS" class="hS" :title="$ui('ドラッグで開始を動かす')" @pointerdown.stop.prevent="onHandleDown($event, v, 's')" @click.stop></i>
              </div>
              <template v-for="v in rv.bars" :key="`w${v.key}`">
                <span v-if="v.who" class="who" :style="{ left: `${v.x}px`, top: `${v.y - 2}px`, animationDelay: `-${v.flashDelay}ms` }">{{ v.who }}</span>
              </template>
              <div
                v-for="l in rv.lateExt"
                :key="`l${l.key}`"
                class="lateext"
                :style="{ left: `${l.x}px`, width: `${l.w}px`, top: `${l.y}px` }"
                :title="l.title"
              ></div>
              <template v-for="m in rv.fs" :key="`fs${m.key}`">
                <i v-if="m.joined" class="fs j" :style="{ left: `${m.x}px`, top: `${m.y}px` }" :title="m.title"></i>
                <i v-else class="fs ln" :style="{ left: `${m.x}px`, width: `${m.w}px`, top: `${m.y}px` }" :title="m.title"></i>
              </template>
              <template v-for="g in rv.ghosts" :key="g.key">
                <div class="ghost" :class="g.cls" :style="{ left: `${g.x}px`, width: `${g.w}px`, top: `${g.y}px`, height: `${BAR_H}px` }"></div>
                <span class="gl" :class="g.cls" :style="{ left: `${g.x}px`, top: `${g.y - 2}px` }">{{ g.label }}</span>
              </template>
            </div>
          </div>
          <p v-if="loaded && rowViews.length === 0" class="tl-empty">{{ $ui('表示する行がありません') }}</p>

          <div
            v-if="step === null"
            class="nowline"
            :style="{ left: `${LW + X(now)}px`, top: `${AH}px`, height: `${Math.max(rowsH, 80)}px` }"
            aria-hidden="true"
          ></div>
        </div>
      </div>

      <aside v-if="detailSeq !== null" class="tl-detail" :style="{ width: `${paneWidth}px` }">
        <div class="tl-grip" :title="$ui('ドラッグで幅を変える')" @pointerdown="startPaneResize"></div>
        <TicketDetailPane
          :project-key="projectKey"
          :seq="detailSeq"
          :members="members"
          :tags="tags"
          :workflow="projectStore.current?.workflow ?? null"
          :candidates="tickets"
          :epics="epicChoices.all.value"
          :link-query="detailLinkQuery"
          timeline-fields
          @close="closeDetail"
          @updated="onDetailUpdated"
          @created="load(true)"
          @deleted="onDetailDeleted"
        />
      </aside>
    </div>

    <div class="tl-status">
      <span class="legend">
        {{ $ui('凡例') }}
        <span><i class="t"></i>{{ $ui('未着手') }}</span>
        <span><i class="p"></i>{{ $ui('進行中') }}</span>
        <span><i class="l"></i>{{ $ui('押している') }}</span>
        <span><i class="d"></i>{{ $ui('完了') }}</span>
        <span><i class="s"></i>{{ $ui('当初計画') }}</span>
      </span>
      <span v-if="canPlan">{{ $ui('右端をドラッグ：終了を動かす（試算→確定）') }}</span>
      <span v-if="canPlan"><kbd>Enter</kbd> {{ $ui('確定') }} <kbd>Esc</kbd> {{ $ui('取り消す') }}</span>
      <span><kbd>⌘/Ctrl</kbd>{{ $ui('＋ホイール 拡大縮小') }}</span>
      <span class="r mono">{{ zoomText }}</span>
    </div>

    <Modal v-if="saveOpen" :title="$ui('計画を保存')" @close="saveOpen = false">
      <form class="tl-save" @submit.prevent="saveBaseline">
        <p class="tl-hint">{{ $ui('行を絞っていても、載せるエピックの全体を保存します。版の名前は保存した日時で決まります。') }}</p>
        <label class="field">
          <span class="label">{{ $ui('添え書き（任意）') }}</span>
          <input v-model="saveLabel" type="text" maxlength="100" :placeholder="$ui('例：リハーサル後')" :disabled="saving" />
        </label>
        <p v-if="saveError" class="tl-err" role="alert">{{ saveError }}</p>
      </form>
      <template #footer>
        <button type="button" class="secondary" :disabled="saving" @click="saveOpen = false">{{ $ui('キャンセル') }}</button>
        <button type="button" class="primary" :disabled="saving" @click="saveBaseline">{{ saving ? $ui('保存中…') : $ui('保存') }}</button>
      </template>
    </Modal>

    <ConfirmDialog
      v-if="deleting"
      :title="$ui('版を消す')"
      :message="$ui('{value0}の版を消します。元に戻せません', { value0: fullNameOf(deleting) })"
      :confirm-label="$ui('消す')"
      danger
      :busy="deleteBusy"
      @confirm="confirmDelete"
      @cancel="deleting = null"
    />
  </div>
</template>

<style scoped>
.tl-page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  position: relative;
}

/* 見出しのパネルを、本体の貼り付いた時間軸より上に出す */
.tl-page > .page-header {
  position: relative;
  z-index: 20;
}

.mono {
  font-family: var(--pb-font-mono);
  font-variant-numeric: tabular-nums;
}

.muted {
  color: var(--pb-text-muted);
}

/* ── 見出しの道具 ── */
.tl-tools {
  display: flex;
  align-items: center;
  gap: 6px;
  flex-wrap: nowrap;
  min-width: 0;
}

.tl-dd {
  position: relative;
  flex: none;
}

.tl-chip {
  font: inherit;
  font-size: 12px;
  color: var(--pb-text);
  background: var(--pb-bg);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  padding: 3px 8px;
  cursor: pointer;
  white-space: nowrap;
}

.tl-chip {
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tl-chip:hover {
  background: var(--pb-hover);
}

.tl-chip.on {
  border-color: var(--pb-accent);
  box-shadow: inset 0 0 0 1px var(--pb-accent);
}

.tl-chip .k {
  color: var(--pb-text-muted);
}

.tl-tools :deep(button.small),
.tl-notice button.small {
  font-size: 12px;
  padding: 3px 10px;
  white-space: nowrap;
}

.tl-tools button[aria-pressed='true'] {
  background: var(--pb-accent);
  border-color: var(--pb-accent);
  color: var(--pb-on-accent);
}

.tl-rowsum {
  font-size: 12px;
  color: var(--pb-text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 180px;
}

.tl-vsep {
  width: 1px;
  height: 20px;
  background: var(--pb-line);
  flex: none;
}

.tl-seg {
  display: inline-flex;
  flex: none;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  overflow: hidden;
}

.tl-seg button {
  font: inherit;
  font-size: 12px;
  padding: 3px 10px;
  border: 0;
  border-left: 1px solid var(--pb-line);
  border-radius: 0;
  background: var(--pb-bg);
  color: var(--pb-text);
  cursor: pointer;
}

.tl-seg button:first-child {
  border-left: 0;
}

.tl-seg button[aria-pressed='true'] {
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.tl-poll {
  font-size: 11px;
  color: var(--pb-text-muted);
  white-space: nowrap;
  min-width: 5.5em;
  text-align: right;
}

.tl-poll.warn {
  color: var(--pb-danger-text);
}

.tl-panel {
  position: absolute;
  top: calc(100% + 4px);
  left: 0;
  z-index: 30;
  display: grid;
  gap: 2px;
  min-width: 300px;
  max-width: 440px;
  padding: var(--pb-space-2);
  background: var(--pb-bg);
  border: 1px solid var(--pb-border);
  border-radius: 8px;
  box-shadow: var(--pb-shadow-2);
  font-size: 13px;
}

.tl-panel.right {
  left: auto;
  right: 0;
}

.tl-versions {
  min-width: 380px;
}

.tl-panel .ph {
  margin: 0 0 4px;
  padding: 2px 4px 6px;
  font-size: 11px;
  color: var(--pb-text-muted);
  border-bottom: 1px solid var(--pb-line);
}

.tl-panel .pf {
  display: flex;
  gap: 6px;
  padding-top: 6px;
  margin-top: 4px;
  border-top: 1px solid var(--pb-line);
}

.tl-filter {
  gap: var(--pb-space-2);
}

.filter {
  display: grid;
  gap: 2px;
}

.filter-label {
  font-size: 12px;
  color: var(--pb-text-muted);
}

.tl-check {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px;
  border-radius: 4px;
  cursor: pointer;
  min-width: 0;
}

.tl-check:hover {
  background: var(--pb-hover);
}

.tl-check-title {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.tl-check .sw {
  width: 8px;
  height: 14px;
  flex: none;
}

.tl-vrow {
  display: flex;
  align-items: center;
  gap: 4px;
}

.tl-vrow .tl-check {
  flex: 1 1 auto;
}

.tl-who {
  flex: none;
  font-size: 11px;
  color: var(--pb-text-muted);
}

.tl-hint {
  margin: 0;
  padding: 4px;
  font-size: 12px;
  color: var(--pb-text-muted);
}

.tl-err {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--pb-danger-text);
}

/* ── 警告の面・通知の行 ── */
.tl-warn {
  margin: 0;
  padding: 4px 16px;
  font-size: 12.5px;
  background: var(--pb-warning-bg);
  border-bottom: 1px solid var(--pb-warning-border);
  color: var(--pb-text);
}

.tl-notice {
  flex: none;
  display: flex;
  align-items: center;
  gap: 10px;
  height: 38px;
  padding: 0 12px 0 16px;
  border-bottom: 1px solid var(--pb-line);
  font-size: 12.5px;
  min-width: 0;
  white-space: nowrap;
  overflow: hidden;
}

.tl-notice .msg {
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
  flex: 0 1 auto;
}

.tl-notice .msg b {
  font-weight: 600;
}

.tl-notice .lbl {
  flex: none;
  font: 600 11px var(--pb-font-mono);
  letter-spacing: 0.04em;
  color: var(--pb-text-muted);
}

.tl-notice.trial {
  background: color-mix(in srgb, var(--pb-accent) 9%, var(--pb-bg));
}

.tl-notice.fail {
  background: color-mix(in srgb, var(--pb-danger) 9%, var(--pb-bg));
}

.tl-notice.fail .lbl,
.dz {
  color: var(--pb-danger-text);
}

.tl-notice.step {
  background: var(--pb-surface);
}

.tl-notice .sq {
  width: 28px;
  padding: 3px 0;
}

.tl-notice .other {
  margin-left: auto;
  color: var(--pb-text-muted);
  font-size: 12px;
  overflow: hidden;
  text-overflow: ellipsis;
  min-width: 0;
  flex: 0 1 auto;
}

.tl-notice .other mark {
  background: var(--pb-warning);
  color: var(--pb-on-warning);
  border-radius: 3px;
  padding: 0 4px;
}

.tl-x {
  padding: 0 4px;
  border: 0;
  background: none;
  color: inherit;
  cursor: pointer;
}

/* ── 本体 ── */
.tl-body {
  position: relative;
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
}

.tl-body.loading {
  opacity: 0.6;
}

.tl-scroller {
  flex: 1 1 auto;
  min-width: 0;
  overflow: auto;
  position: relative;
  overscroll-behavior: contain;
}

.tl-canvas {
  position: relative;
  min-height: 100%;
}

.tl-axis {
  position: sticky;
  top: 0;
  z-index: 6;
  display: flex;
  background: var(--tl-hdr-bg);
  border-bottom: 1px solid var(--pb-border);
}

.tl-corner {
  position: sticky;
  left: 0;
  z-index: 7;
  flex: none;
  padding: 4px 10px 4px 16px;
  overflow: hidden;
  font-size: 11px;
  line-height: 1.35;
  white-space: nowrap;
  text-overflow: ellipsis;
  color: var(--pb-text-muted);
  background: var(--tl-hdr-bg);
  border-right: 1px solid var(--pb-border);
}

.tl-corner b {
  color: var(--pb-text);
  font-weight: 600;
}

.tl-axtrack {
  position: relative;
  flex: none;
  overflow: hidden;
}

.ax-maj {
  position: absolute;
  top: 3px;
  padding-left: 4px;
  font: 600 11px var(--pb-font-mono);
  color: var(--pb-text);
  white-space: nowrap;
}

.ax-maj.lft {
  transform: translateX(-100%);
  padding: 0 4px 0 0;
}

.ax-min {
  position: absolute;
  bottom: 3px;
  padding-left: 3px;
  font: 10.5px var(--pb-font-mono);
  color: var(--pb-text-muted);
  white-space: nowrap;
}

.ax-tk {
  position: absolute;
  bottom: 0;
  width: 1px;
  height: 6px;
  background: var(--pb-7);
}

.ax-tk.M {
  height: 100%;
  background: var(--pb-8);
}

.nowtag {
  position: absolute;
  bottom: 2px;
  z-index: 2;
  transform: translateX(-50%);
  padding: 0 5px;
  border-radius: 3px;
  font: 600 10.5px var(--pb-font-mono);
  color: var(--pb-1);
  background: var(--g-bar);
  white-space: nowrap;
}

.tl-grid {
  position: absolute;
  pointer-events: none;
  overflow: visible;
}

.tl-grid .f {
  stroke: var(--pb-6);
  stroke-width: 1;
  stroke-dasharray: 1 3;
  opacity: var(--g-sub-op);
}

.tl-grid .m {
  stroke: var(--pb-6);
  stroke-width: 1;
  opacity: var(--g-min-op);
}

.tl-grid .M {
  stroke: var(--pb-7);
  stroke-width: 1;
}

.nowline {
  position: absolute;
  z-index: 3;
  width: 0;
  border-left: 1.5px solid var(--g-bar);
  pointer-events: none;
  filter: var(--g-bar-glow);
}

.tl-row {
  display: flex;
  position: relative;
  border-bottom: 1px solid color-mix(in srgb, var(--pb-6) 70%, transparent);
}

.tl-rlabel {
  position: sticky;
  left: 0;
  z-index: 4;
  flex: none;
  padding: 7px 10px 6px 18px;
  overflow: hidden;
  background: var(--pb-bg);
  border-right: 1px solid var(--pb-border);
}

.tl-rlabel .sleeve {
  position: absolute;
  left: 0;
  top: 0;
  bottom: 0;
  width: 8px;
}

.tl-rlabel .rn {
  font-weight: 600;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tl-rlabel .rm {
  font: 10.5px var(--pb-font-mono);
  color: var(--pb-text-muted);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.tl-select-band {
  flex: none;
  display: flex;
  align-items: center;
  gap: var(--pb-space-3);
  margin: 0;
  padding: 4px 16px;
  font-size: 12.5px;
  border-bottom: 1px solid var(--pb-line);
  background: var(--pb-surface);
}

.tl-outside {
  margin-right: 4px;
  padding: 0 3px;
  border: 1px dashed var(--pb-text-muted);
  border-radius: 3px;
  font-size: 9.5px;
}

.tl-row.outside .rn {
  color: var(--pb-text-muted);
  font-style: italic;
}

.tl-rlabel .lanetag {
  position: absolute;
  right: 8px;
  font: 10px var(--pb-font-mono);
  line-height: 1;
  color: var(--pb-text-muted);
  white-space: nowrap;
}

.tl-track {
  position: relative;
  flex: none;
}

.subzone {
  position: absolute;
  left: 0;
  right: 0;
  pointer-events: none;
  background: var(--tl-sub-bg);
}

.tl-empty {
  margin: 0;
  padding: 24px 16px;
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ── バー（高さ 36px・2行固定）── */
.bar {
  --line: var(--tl-todo-line);
  position: absolute;
  box-sizing: border-box;
  border-radius: 4px;
  background: var(--tl-todo-bg);
  border: 1px solid color-mix(in srgb, var(--line) 55%, transparent);
  border-left: 4px solid var(--line);
  color: var(--pb-text);
  cursor: pointer;
  user-select: none;
  -webkit-user-select: none;
}

.bar.st-prog {
  --line: var(--tl-prog-line);
  background: var(--tl-prog-bg);
}

.bar.st-late {
  --line: var(--tl-late-line);
  background: var(--tl-late-bg);
}

.bar.st-wait {
  --line: var(--tl-wait-line);
}

.bar.st-done {
  --line: var(--tl-done-line);
  background: var(--tl-done-bg);
  color: var(--pb-text-muted);
}

.bar.st-plan {
  --line: var(--pb-8);
  background: var(--pb-2);
  border-style: dashed;
  border-left-style: solid;
  cursor: default;
}

/* 時間重複チェックの無いバーは沈める：細い左罫・細字・斜線の地 */
.bar.free {
  border-left-width: 2px;
  background-image: repeating-linear-gradient(-45deg, color-mix(in srgb, var(--line) 16%, transparent) 0 1px, transparent 1px 6px);
}

.bar.free .l1 {
  font-weight: 500;
}

.bar.free.st-prog {
  background-color: color-mix(in srgb, var(--tl-prog-bg) 45%, var(--pb-bg));
}

.bar.free.st-done {
  background-color: color-mix(in srgb, var(--tl-done-bg) 55%, var(--pb-bg));
}

/* 片方だけの予定（`[┄` / `┄]`） */
.bar.half-s {
  border-right-style: dashed;
  border-top-right-radius: 0;
  border-bottom-right-radius: 0;
}

.bar.half-e {
  border-left-style: solid;
  border-top-style: dashed;
  border-bottom-style: dashed;
}

.bar .in {
  position: relative;
  display: block;
  width: fit-content;
  max-width: 100%;
  height: 100%;
  overflow: hidden;
  padding: 2px 7px;
  box-sizing: border-box;
}

.bar .l1,
.bar .l2 {
  display: block;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
  line-height: 15px;
  height: 15px;
}

.bar .l1 {
  font-size: 12.5px;
  font-weight: 600;
}

.bar .l2 {
  font-size: 11px;
}

.bar.w-s .in {
  padding: 2px 4px;
}

.bar.w-s .rest {
  display: none;
}

.bar.w-xs {
  border-left-width: 3px;
}

.bar.w-xs .in {
  width: 100%;
  padding: 9px 0 0;
  text-align: center;
}

.bar.w-xs .l2,
.bar.w-xs .newtag {
  display: none;
}

.bar.w-0 .in {
  display: none;
}

.bar .sl {
  display: inline-block;
  margin-right: 6px;
  padding: 0 5px;
  border-radius: 7px;
  font-size: 10.5px;
  font-weight: 600;
  line-height: 13px;
  background: var(--line);
  color: var(--pb-1);
}

.bar.st-todo .sl,
.bar.st-plan .sl {
  padding: 0 4px;
  background: none;
  color: var(--pb-text-muted);
  border: 1px solid var(--pb-7);
}

.bar .rest {
  color: var(--pb-text-muted);
}

.bar .rest.dz {
  color: var(--pb-danger-text);
  font-weight: 600;
}

.bar .dv {
  margin-left: 6px;
  padding: 0 4px;
  border-radius: 3px;
  font: 600 10.5px var(--pb-font-mono);
  background: color-mix(in srgb, var(--pb-12) 10%, transparent);
}

.bar .dv.up {
  background: color-mix(in srgb, var(--tl-late-line) 28%, transparent);
}

.newtag {
  margin-right: 4px;
  padding: 0 3px;
  border: 1px dashed currentColor;
  border-radius: 3px;
  font: 600 9.5px var(--pb-font-mono);
  color: var(--pb-text-muted);
  vertical-align: 1px;
}

.bar.sel {
  outline: 2px solid var(--pb-accent);
  outline-offset: 2px;
}

.bar.pending {
  border-style: dashed;
}

.bar.tr-from {
  opacity: 0.35;
}

/* 取っ手（端から内側 5px・外側 3px）。掴めない人にはカーソルも変えない */
.bar .hE,
.bar .hS {
  position: absolute;
  top: -2px;
  bottom: -2px;
  z-index: 2;
  width: 8px;
  cursor: ew-resize;
}

.bar .hE {
  right: -3px;
}

.bar .hS {
  left: -3px;
}

.bar .hE::after,
.bar .hS::after {
  content: '';
  position: absolute;
  top: 8px;
  bottom: 8px;
  left: 2px;
  width: 3px;
  border-left: 1px solid var(--pb-11);
  border-right: 1px solid var(--pb-11);
  opacity: 0;
}

.bar:hover .hE::after,
.bar:hover .hS::after {
  opacity: 0.9;
}

/* 他の人の変更（2.4秒、警告の黄で光らせる） */
.bar.flash {
  animation: tl-flash 2.4s ease-out both;
}

@keyframes tl-flash {
  0%,
  35% {
    box-shadow: 0 0 0 3px var(--pb-warning), 0 0 14px var(--pb-warning);
  }
  100% {
    box-shadow: 0 0 0 3px transparent;
  }
}

.who {
  position: absolute;
  z-index: 5;
  transform: translateY(-100%);
  padding: 0 5px;
  border-radius: 3px;
  font-size: 10.5px;
  white-space: nowrap;
  pointer-events: none;
  background: var(--pb-warning);
  color: var(--pb-on-warning);
  animation: tl-whofade 2.4s ease-out both;
}

@keyframes tl-whofade {
  0%,
  60% {
    opacity: 1;
  }
  100% {
    opacity: 0;
  }
}

@media (prefers-reduced-motion: reduce) {
  .bar.flash,
  .who {
    animation-duration: 0.01s;
  }
}

/* アンカー（上下へ 6px はみ出す 3px の杭と、上下の短い横棒） */
.pin {
  position: absolute;
  top: -6px;
  bottom: -6px;
  z-index: 3;
  width: 3px;
  background: var(--pb-12);
  pointer-events: none;
}

.pin::before,
.pin::after {
  content: '';
  position: absolute;
  left: -3px;
  width: 9px;
  height: 2px;
  background: inherit;
}

.pin::before {
  top: 0;
}

.pin::after {
  bottom: 0;
}

.pin.s {
  left: -4px;
}

.pin.e {
  right: -2px;
}

/* FS */
.fs {
  position: absolute;
  z-index: 3;
  pointer-events: auto;
}

.fs.j {
  width: 9px;
  height: 9px;
  transform: translate(-50%, -50%) rotate(45deg);
  background: var(--pb-1);
  border: 1.5px solid var(--g-dep);
}

.fs.ln {
  height: 0;
  border-top: 1.5px solid var(--g-dep);
  transform: translateY(-50%);
}

.fs.ln::after {
  content: '';
  position: absolute;
  right: -1px;
  top: -5px;
  border: 4px solid transparent;
  border-left: 6px solid var(--g-dep);
  border-right: 0;
}

/* 押している：終了から「いま」まで、橙の破線 */
.lateext {
  position: absolute;
  z-index: 2;
  height: 0;
  border-top: 2px dashed var(--tl-late-line);
  pointer-events: none;
}

.lateext::after {
  content: '';
  position: absolute;
  right: -1px;
  top: -7px;
  height: 9px;
  border-right: 2px solid var(--tl-late-line);
}

/* 衝突 */
.coll {
  position: absolute;
  z-index: 2;
  pointer-events: none;
  background: repeating-linear-gradient(-45deg, color-mix(in srgb, var(--pb-danger) 45%, transparent) 0 2px, transparent 2px 6px);
  border: 1px solid var(--pb-danger);
}

/* 当初計画：ずれたバーにだけ、計画の位置に細い破線の枠（外側に 4px 張り出す） */
.base {
  position: absolute;
  box-sizing: content-box;
  margin: -4px 0 0 -4px;
  padding: 4px;
  border: 1px dashed var(--pb-11);
  border-radius: 5px;
  opacity: 0.9;
  pointer-events: none;
}

.tl-page[data-mode='step'] .base {
  border-style: dotted;
  border-width: 1.5px;
}

.base.gone {
  border-color: var(--pb-8);
  background: repeating-linear-gradient(-45deg, color-mix(in srgb, var(--pb-8) 25%, transparent) 0 1px, transparent 1px 6px);
}

.gonelbl {
  overflow: hidden;
  text-overflow: ellipsis;
  position: absolute;
  font-size: 10.5px;
  font-style: italic;
  color: var(--pb-text-muted);
  white-space: nowrap;
  pointer-events: none;
}

/* 試算の影 */
.ghost {
  position: absolute;
  z-index: 4;
  box-sizing: border-box;
  border: 2px dashed var(--pb-10);
  border-radius: 3px;
  background: color-mix(in srgb, var(--pb-9) 20%, transparent);
  box-shadow: 0 0 0 1px color-mix(in srgb, var(--pb-1) 70%, transparent);
  pointer-events: none;
}

.ghost.src {
  border-style: solid;
}

.ghost.block {
  border-color: var(--pb-danger);
  background: color-mix(in srgb, var(--pb-danger) 14%, transparent);
}

.ghost.sug {
  border-color: var(--pb-11);
  border-style: dotted;
  background: color-mix(in srgb, var(--pb-11) 10%, transparent);
}

.gl {
  position: absolute;
  z-index: 5;
  transform: translate(4px, -50%);
  padding: 0 4px;
  border-radius: 3px;
  font: 600 10.5px/14px var(--pb-font-mono);
  color: var(--pb-1);
  background: var(--pb-10);
  white-space: nowrap;
  pointer-events: none;
}

.gl.block {
  background: var(--pb-danger);
}

.gl.sug {
  background: var(--pb-11);
}

/* ── 凡例の行 ── */
.tl-status {
  flex: none;
  display: flex;
  align-items: center;
  gap: 16px;
  height: 26px;
  padding: 0 12px;
  overflow: hidden;
  font-size: 11px;
  white-space: nowrap;
  color: var(--pb-text-muted);
  background: var(--pb-surface);
  border-top: 1px solid var(--pb-line);
}

.tl-status kbd {
  padding: 0 4px;
  border: 1px solid var(--pb-7);
  border-bottom-width: 2px;
  border-radius: 4px;
  font-family: var(--pb-font-mono);
  font-size: 10.5px;
  color: var(--pb-text);
  background: var(--pb-bg);
}

.tl-status .r {
  margin-left: auto;
}

.legend {
  display: flex;
  align-items: center;
  gap: 10px;
}

.legend i {
  display: inline-block;
  width: 18px;
  height: 10px;
  margin-right: 3px;
  vertical-align: -1px;
  border-radius: 2px;
  border-left: 3px solid;
}

.legend i.t {
  background: var(--tl-todo-bg);
  border-color: var(--tl-todo-line);
  outline: 1px solid var(--pb-7);
  outline-offset: -1px;
}

.legend i.p {
  background: var(--tl-prog-bg);
  border-color: var(--tl-prog-line);
}

.legend i.l {
  background: var(--tl-late-bg);
  border-color: var(--tl-late-line);
}

.legend i.d {
  background: var(--tl-done-bg);
  border-color: var(--tl-done-line);
}

.legend i.s {
  background: none;
  border: 1px dashed var(--pb-11);
}

/* ── 浮かせた詳細ペイン（5.14 と同じ作り）── */
.tl-detail {
  position: absolute;
  top: 6px;
  right: 6px;
  bottom: 6px;
  z-index: 8;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  background: var(--g-pane-bg);
  -webkit-backdrop-filter: blur(14px) saturate(0.4);
  backdrop-filter: blur(14px) saturate(0.4);
  border: 1px solid var(--g-pane-line);
  border-radius: 8px;
  box-shadow: var(--g-pane-shadow);
}

.tl-detail > :deep(.detail) {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
}

.tl-detail :deep(.detail-header) {
  background: transparent;
}

.tl-grip {
  position: absolute;
  left: -1px;
  top: 0;
  bottom: 0;
  z-index: 2;
  width: 6px;
  cursor: col-resize;
}

.tl-grip:hover {
  background: color-mix(in srgb, var(--pb-focus) 60%, transparent);
}

.tl-save {
  display: grid;
  gap: var(--pb-space-3);
}

.tl-save .field {
  display: grid;
  gap: 4px;
}

.tl-save .label {
  font-size: 13px;
}
</style>

<style>
/*
 * タイムラインの色（プラグインの中だけ。5.15「状態の色」）。**`.tl-page` の上で宣言し、:root に置かない。**
 * scoped にしないのは、scoped の `:global([data-theme='dark']) .tl-page` が `[data-theme='dark']` だけに
 * 縮み、ページの根で宣言したライトの値に負けるため。
 */
.tl-page {
  --tl-todo-bg: var(--pb-1);
  --tl-todo-line: var(--pb-8);
  --tl-prog-bg: oklch(0.87 0.065 235);
  --tl-prog-line: oklch(0.55 0.14 240);
  --tl-late-bg: oklch(0.89 0.085 70);
  --tl-late-line: oklch(0.68 0.17 52);
  --tl-wait-line: var(--pb-danger);
  --tl-done-bg: oklch(0.93 0.03 165);
  --tl-done-line: oklch(0.70 0.06 165);
  --tl-sub-bg: color-mix(in srgb, var(--pb-11) 7%, transparent);
  --tl-hdr-bg: var(--pb-2);
}

[data-theme='dark'] .tl-page {
  --tl-todo-bg: var(--pb-2);
  --tl-prog-bg: oklch(0.37 0.08 238);
  --tl-prog-line: oklch(0.72 0.13 235);
  --tl-late-bg: oklch(0.40 0.09 58);
  --tl-late-line: oklch(0.79 0.15 62);
  --tl-done-bg: oklch(0.29 0.03 165);
  --tl-done-line: oklch(0.50 0.06 165);
  --tl-sub-bg: color-mix(in srgb, var(--pb-11) 9%, transparent);
}
</style>
