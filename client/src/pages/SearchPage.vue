<script setup lang="ts">
import { uiNumber, uiText } from '../locales/ui'
import { computed, onBeforeUnmount, onMounted, onUnmounted, ref, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AssigneePicker from '../components/AssigneePicker.vue'
import EpicFilter from '../components/EpicFilter.vue'
import MultiSelectFilter from '../components/MultiSelectFilter.vue'
import type { MultiSelectOption } from '../components/MultiSelectFilter.vue'
import PageHeader from '../components/PageHeader.vue'
import SplitPane from '../components/SplitPane.vue'
import StatusDropdown from '../components/StatusDropdown.vue'
import TicketDetailPane from '../components/TicketDetailPane.vue'
import { ApiError } from '../api/client'
import * as tagsApi from '../api/tags'
import type { Tag } from '../api/tags'
import * as ticketsApi from '../api/tickets'
import { priorityLabels, priorityMarks, ticketTypeIcons, ticketTypeLabels } from '../api/tickets'
import type {
  SortOrder,
  Ticket,
  TicketDetail,
  TicketSort,
  TicketTransitionOption,
  TicketType,
} from '../api/tickets'
import {
  addDaysPlainDate,
  formatDate,
  formatPlainDate,
  startOfDayInstant,
  todayPlainDate,
} from '../lib/datetime'
import { statusLabel } from '../lib/catalogLabels'
import { useAuthStore } from '../stores/auth'
import { useProjectStore } from '../stores/project'

/**
 * チケット検索（`GuiDesign.md` 5.13）。
 *
 * **完了してスプリントから外れたものも含めた全チケットから、条件で探す画面である。**
 * バックログ（5.4）が「次に何をやるか」を決める面なのに対し、こちらは過去を含めて
 * 1件に辿り着く面である。**常に `retired=true` を送る。**
 *
 * **表は平らである**。条件で親が落ちると字下げが歯抜けになり
 * （`ApiDesign.md` 9.2.4 は親を補完しない）、手で並べた順は探す手がかりにならない。
 * 二段・ドラッグ・グループ化は持たず、**既定は番号の降順**。**エピックも行に出す。**
 *
 * **表はバックログから切り出さず、この画面に書いた**（同上）。行の見た目と操作は
 * バックログに揃え、部品（`StatusDropdown`・`AssigneePicker`・`TicketDetailPane`・
 * `SplitPane`・`EpicFilter`）を共有する。
 *
 * **条件はすべて URL のクエリに置く**（5.13「URL」）。パラメータ名は `ApiDesign.md` 9.2.1 と
 * 同じで、期間も API と同じ ISO8601 の瞬間で持つ——同じ URL を開いた人は、タイムゾーンが
 * 違っても同じ瞬間で絞る。欄に出すときだけ利用者のタイムゾーンの日付へ戻す。
 *
 * **詳細は `/p/:key/tickets/:seq?from=search&<条件>` で開く**（3.2）。`TicketViewsPage` が
 * `from` を見てこの画面を出し続けるので、**行を押しても再マウントされない。**
 */
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const projectStore = useProjectStore()

const projectKey = computed(() => {
  const key = route.params.key
  return typeof key === 'string' ? key : ''
})

const canEdit = computed(() => auth.canInProject(projectKey.value, 'ticket.edit'))
/** 一覧から状態を変えるのに要る（`ApiDesign.md` 9.6。5.4 と同じ） */
const canTransition = computed(() => auth.canInProject(projectKey.value, 'ticket.transition'))
/** 一覧から担当を変えるのに要る（9.5.2 は `ticket.edit` に加えてこれを要求する） */
const canAssign = computed(
  () => canEdit.value && auth.canInProject(projectKey.value, 'ticket.assign'),
)

// ── URL クエリ ───────────────────────────────────────────────

function queryValue(name: string): string {
  const v = route.query[name]
  return typeof v === 'string' ? v : ''
}

function queryList(name: string): string[] {
  return queryValue(name)
    .split(',')
    .map((s) => s.trim())
    .filter((s) => s !== '')
}

/**
 * 検索の条件（`from` を除いたクエリ）。詳細を開くときも閉じるときもこれを持ち回る。
 *
 * **`from` を落とすのは、これが「一覧の条件」だからである。** `from` は詳細の後ろに
 * どの一覧を残すかを表すだけで（3.2）、検索の条件ではない。
 */
const searchQuery = computed<Record<string, string>>(() => {
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(route.query)) {
    if (k !== 'from' && typeof v === 'string' && v !== '') out[k] = v
  }
  return out
})

const isPristine = computed(() => Object.keys(searchQuery.value).length === 0)

/**
 * クエリを書き換える。**空の値はキーごと落とす。**
 *
 * **パスは今のまま**——詳細を開いている間に条件を変えても、詳細は閉じない。
 * `replace` を使うのは、条件の操作1つずつが履歴に積まれると「戻る」で画面から
 * 出られなくなるためである（5.4 の `setQuery` と同じ）。
 */
function setQuery(patch: Record<string, string>): void {
  const next: Record<string, string> = {}
  for (const [k, v] of Object.entries({ ...route.query, ...patch })) {
    if (typeof v === 'string' && v !== '') next[k] = v
  }
  void router.replace({ path: route.path, query: next })
}

/** `[解除]`：条件と並べ替えを素へ戻す（5.13）。**詳細を開いていれば `from` は残す** */
function clearAll(): void {
  clearTimeout(keywordTimer)
  keywordTimer = undefined
  keywordPending = false
  void router.replace({ path: route.path, query: shrunk.value ? { from: 'search' } : {} })
}

const statusKeys = computed(() => queryList('status'))
const types = computed(() => queryList('type'))
const assignees = computed(() => queryList('assignee'))
const epicSeqs = computed(() =>
  queryList('parent')
    .map(Number)
    .filter((n) => Number.isInteger(n) && n > 0),
)

// ── 並べ替え ─────────────────────────────────────────────────

const SORTS: TicketSort[] = ['seq', 'title', 'status', 'priority', 'due_date', 'closed_at']

/** **既定は番号の降順**（5.13）。新しいチケットから並ぶ */
const sort = computed<TicketSort>(() => {
  const v = queryValue('sort') as TicketSort
  return SORTS.includes(v) ? v : 'seq'
})

const order = computed<SortOrder>(() => {
  const v = queryValue('order')
  if (v === 'asc' || v === 'desc') return v
  return sort.value === 'seq' && queryValue('sort') === '' ? 'desc' : 'asc'
})

/**
 * 列の見出しの並べ替え。同じ列なら向きを反転する。
 *
 * 列を切り替えたときの初期の向きは、**番号・期限・完了日は降順**（新しいもの・近いものから）、
 * 文字と状態と優先は昇順から始める。
 */
function sortBy(next: TicketSort): void {
  if (sort.value === next) {
    setQuery({ sort: next, order: order.value === 'asc' ? 'desc' : 'asc' })
    return
  }
  const desc = next === 'seq' || next === 'due_date' || next === 'closed_at'
  setQuery({ sort: next, order: desc ? 'desc' : 'asc' })
}

function ariaSort(col: TicketSort): 'ascending' | 'descending' | 'none' {
  if (sort.value !== col) return 'none'
  return order.value === 'asc' ? 'ascending' : 'descending'
}

function caret(col: TicketSort): string {
  if (sort.value !== col) return ''
  return order.value === 'asc' ? '▴' : '▾'
}

// ── キーワード ───────────────────────────────────────────────

/** 打つのが止まってから送るまで（5.13。`ApiDesign.md` 5.2 の `check-key` と同じ間隔） */
const KEYWORD_DEBOUNCE_MS = 400

const keyword = ref(queryValue('q'))
let keywordTimer: ReturnType<typeof setTimeout> | undefined
/** 打ってからまだ URL へ載せていない。**その間は URL の値で欄を上書きしない** */
let keywordPending = false
/** 日本語の変換中。**変換中は送らない**（5.13） */
let composing = false

function commitKeyword(): void {
  clearTimeout(keywordTimer)
  keywordTimer = undefined
  keywordPending = false
  // 空白だけの語はサーバでも指定なしと同じ（9.2.1）。URL を汚さないようにキーごと落とす
  const v = keyword.value.trim() === '' ? '' : keyword.value
  if (v !== queryValue('q')) setQuery({ q: v })
}

function scheduleKeyword(): void {
  if (composing) return
  clearTimeout(keywordTimer)
  keywordTimer = setTimeout(commitKeyword, KEYWORD_DEBOUNCE_MS)
}

function onKeywordInput(): void {
  keywordPending = true
  scheduleKeyword()
}

function onCompositionStart(): void {
  composing = true
}

function onCompositionEnd(): void {
  composing = false
  scheduleKeyword()
}

/**
 * `Enter` は待たずに送る。**変換を確定する `Enter` では送らない**（5.5「`Enter` で
 * 確定するか」と同じ判定）。
 */
function onKeywordKeydown(e: KeyboardEvent): void {
  if (e.key !== 'Enter' || e.isComposing || e.keyCode === 229) return
  e.preventDefault()
  commitKeyword()
}

// `[解除]` や「戻る」で URL が外から変わったら欄を合わせる
watch(
  () => queryValue('q'),
  (next) => {
    if (!keywordPending && next !== keyword.value) keyword.value = next
  },
)

onBeforeUnmount(() => clearTimeout(keywordTimer))

// ── 期間（着手日・完了日）────────────────────────────────────

/**
 * 期間の欄に出す日付。**URL の瞬間を、利用者のタイムゾーンの日付へ戻す**（7.5 の
 * `formatDate`）。`before` は「翌日の0時未満」で持っているので、1日戻して出す。
 */
function sinceDate(name: string): string {
  const v = queryValue(name)
  return v === '' ? '' : formatDate(v)
}

function beforeDate(name: string): string {
  const v = queryValue(name)
  return v === '' ? '' : (addDaysPlainDate(formatDate(v), -1) ?? '')
}

/** 期間の始まりの日付を「その日の0時以上」として載せる（`ApiDesign.md` 9.2.1 の半開区間） */
function setSince(name: string, date: string): void {
  setQuery({ [name]: date === '' ? '' : (startOfDayInstant(date) ?? '') })
}

/** 期間の終わりの日付を「翌日の0時未満」として載せる。**その日を含める**ためである */
function setBefore(name: string, date: string): void {
  const next = date === '' ? null : addDaysPlainDate(date, 1)
  setQuery({ [name]: next === null ? '' : (startOfDayInstant(next) ?? '') })
}

// ── 詳細ペイン（2.2.1 / 5.5）─────────────────────────────────

/** 開いているチケットの `seq`。**URL が持つ**（3.2） */
const detailSeq = computed<number | null>(() => {
  const raw = route.params.seq
  const n = typeof raw === 'string' ? Number(raw) : Number.NaN
  return Number.isInteger(n) && n > 0 ? n : null
})

/** 詳細を開いている間、一覧は約450pxへ縮み、列が3つになる（5.13。5.4 と同じ） */
const shrunk = computed(() => detailSeq.value !== null)

/** 縮小中の条件は `[検索条件 ▾]` の1行に畳む（5.13。5.4 と同じ） */
const conditionsOpen = ref(false)

/** 詳細を開く行き先。**`from=search` を足し、検索の条件を持ち回る**（3.2） */
function detailTo(seq: number): { path: string; query: Record<string, string> } {
  return {
    path: `/p/${projectKey.value}/tickets/${seq}`,
    query: { ...searchQuery.value, from: 'search' },
  }
}

/** 詳細を閉じて全幅へ戻す。URL も `/p/:key/search?<条件>` へ戻す（5.4 の `closeDetail` と同じ） */
function closeDetail(): void {
  void router.replace({ path: `/p/${projectKey.value}/search`, query: searchQuery.value })
}

/**
 * 行クリックで右に詳細を開く。`Ctrl/⌘+クリック` で新規タブ。
 * **文字を選択しただけのときは開かない**（5.4 の `openRow` と同じ）。
 */
function openRow(t: Ticket, e: MouseEvent): void {
  if ((window.getSelection()?.toString() ?? '') !== '') return
  const to = detailTo(t.seq)
  if (e.metaKey || e.ctrlKey || e.shiftKey) {
    window.open(router.resolve(to).href, '_blank', 'noopener')
    return
  }
  void router.replace(to)
}

/** 詳細で変更が確定した。**該当行だけ差し替え、取り直さない**（5.4 と同じ判断） */
function onDetailUpdated(next: TicketDetail): void {
  tickets.value = tickets.value.map((t) => (t.seq === next.seq ? { ...t, ...next } : t))
}

/** 行が増えた（子チケットの作成）。**条件に合うかを手元で決められないので取り直す** */
function onDetailCreated(): void {
  void loadTickets()
}

/** 削除された（9.5.3）。結果は着地する一覧へ出す（6.4） */
function onDetailDeleted(seq: number, title: string): void {
  tickets.value = tickets.value.filter((t) => t.seq !== seq)
  total.value = Math.max(total.value - 1, 0)
  result.value = uiText("✓ {value0}-{value1}「{value2}」を削除しました", { value0: projectKey.value, value1: seq, value2: title })
  closeDetail()
  void loadTickets()
}

// ── データ ───────────────────────────────────────────────────

const tickets = ref<Ticket[]>([])
const total = ref(0)
const perPage = ref(200)
const loading = ref(false)
const loaded = ref(false)
const error = ref<ApiError | null>(null)
const result = ref('')
const busy = ref(false)

const tags = ref<Tag[]>([])
/** エピックの選択肢。**棚に戻ったエピックも含める**（探す面なので過去も選べる） */
const epics = ref<Ticket[]>([])

const statuses = computed(() => projectStore.current?.workflow?.statuses ?? [])
const members = computed(() => projectStore.current?.members ?? [])

/** 応答の追い越しを防ぐ通し番号（5.4 と同じ対策）。キーワードを打つたびに取り直すので要る */
let fetchSeq = 0

function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({
        status: 0,
        code: 'internal_error',
        message: uiText("予期しないエラーが発生しました"),
      })
}

/**
 * 出す文言。**422 は項目ごとの理由を並べる**——「番号の範囲が逆」のような入力の誤りは、
 * 汎用の文言だけでは何を直せばよいか読めない（6.4）。
 */
const errorMessage = computed(() => {
  const e = error.value
  if (e === null) return ''
  return e.details.length > 0 ? e.details.map((d) => d.message).join(' / ') : e.message
})

async function loadTickets(): Promise<void> {
  const mine = ++fetchSeq
  loading.value = true
  error.value = null
  try {
    const res = await ticketsApi.listTickets(projectKey.value, {
      // **棚に戻ったものも常に含める**（5.13）
      retired: 'true',
      q: queryValue('q'),
      seq_from: queryValue('seq_from'),
      seq_to: queryValue('seq_to'),
      status: statusKeys.value.join(','),
      type: types.value.join(','),
      assignee: assignees.value.join(','),
      // エピックの条件の実体は部分木（`ApiDesign.md` 9.2.1 の `parent`）
      parent: epicSeqs.value.join(','),
      started_since: queryValue('started_since'),
      started_before: queryValue('started_before'),
      closed_since: queryValue('closed_since'),
      closed_before: queryValue('closed_before'),
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
    // 古い一覧を新しい条件の結果として見せない（6.2）
    tickets.value = []
    total.value = 0
  } finally {
    if (mine === fetchSeq) loading.value = false
  }
}

/**
 * 条件の選択肢に要るもの。**一覧の取得と直列にしない**（5.4 と同じ）。
 * 失敗しても画面は止めず、その条件の選択肢が空になるだけにする。
 */
async function loadVocabulary(): Promise<void> {
  const key = projectKey.value
  const [t, e] = await Promise.allSettled([
    tagsApi.listTags(key),
    ticketsApi.listTickets(key, { type: 'epic', retired: 'true' }),
  ])
  if (t.status === 'fulfilled') tags.value = t.value.items
  if (e.status === 'fulfilled') epics.value = e.value.items
}

/**
 * **検索の条件が変わったときだけ取り直す。** 詳細を開閉してもパスと `from` しか
 * 変わらないので、取り直さない——一覧とスクロール位置が残る（2.2.1）。
 */
const fetchKey = computed(
  () => `${projectKey.value}?${new URLSearchParams(searchQuery.value).toString()}`,
)
watch(fetchKey, () => void loadTickets(), { immediate: true })
watch(projectKey, () => void loadVocabulary(), { immediate: true })

/**
 * 選択中プロジェクト（ワークフローとメンバー。`GuiDesign.md` 7.1）。**バックログと同じく
 * 画面が自分で取り、離れるときに捨てる**——状態と担当の選択肢はここから作る。
 * 取らずにいると、バックログから移ってきたときに `clearCurrent` で空になったまま残り、
 * 選択肢が「選択肢がありません」になる（実機で判明）。
 *
 * **`onMounted` で取る（setup で始めない）。** 同じ入れ物（`TicketViewsPage`）の中で
 * バックログから切り替わるとき、setup で始めた取得の結果を、あとから走るバックログの
 * `clearCurrent` が捨てうる。
 */
onMounted(() => void projectStore.fetchCurrent(projectKey.value))
onUnmounted(() => projectStore.clearCurrent())
watch(projectKey, (key) => void projectStore.fetchCurrent(key))

// ── 条件の選択肢 ─────────────────────────────────────────────

const statusOptions = computed<MultiSelectOption[]>(() =>
  statuses.value.map((s) => ({ value: s.key, label: statusLabel(s.key, s.name) })),
)

/** 種別は3つとも選べる。**エピックも行に出す**（5.13） */
const typeOptions = computed<MultiSelectOption[]>(() =>
  (['epic', 'story', 'task'] as TicketType[]).map((t) => ({
    value: t,
    label: ticketTypeLabels[t],
    icon: ticketTypeIcons[t],
  })),
)

/** 担当は「未割当」も選べる（`ApiDesign.md` 9.2.1 の `none`） */
const assigneeOptions = computed<MultiSelectOption[]>(() => [
  { value: 'none', label: uiText("未割当") },
  ...members.value.map((m) => ({
    value: m.actor_id,
    label: m.display_name,
    icon: m.kind === 'agent' ? '🤖' : '👤',
  })),
])

// ── 行の表示 ─────────────────────────────────────────────────

const today = todayPlainDate()

/** 期限超過（5.4 と同じ）。完了したものは含めない */
function isOverdue(t: Ticket): boolean {
  return t.due_date !== null && t.closed_at === null && t.due_date < today
}

function withComma(n: number): string {
  return uiNumber(n)
}

/** チケットIDは**完全形**で出す（5.4「ID列」） */
function fullId(t: Ticket): string {
  return `${projectKey.value}-${t.seq}`
}

/** 200件で打ち切られたか（`ApiDesign.md` 9.2.3） */
const truncated = computed(() => total.value > perPage.value)

// ── 一覧から状態・担当を変える（5.4「一覧で状態を変える」「一覧で担当を選ぶ」）──

/** 行ごとの `StatusDropdown`。**どの行が開いたかは押されるまで分からない**ので行ごとに持つ */
const statusRefs = new Map<number, InstanceType<typeof StatusDropdown>>()

function setStatusRef(seq: number, el: Element | ComponentPublicInstance | null): void {
  if (el === null) statusRefs.delete(seq)
  else statusRefs.set(seq, el as InstanceType<typeof StatusDropdown>)
}

/** 開いたときに遷移できる先を引く（`ApiDesign.md` 9.7） */
async function loadRowTransitions(seq: number): Promise<void> {
  const dropdown = statusRefs.get(seq)
  dropdown?.setLoading()
  try {
    const res = await ticketsApi.listTransitions(projectKey.value, seq)
    dropdown?.setItems(res.items)
  } catch (e) {
    dropdown?.setError(toApiError(e).message)
  }
}

/** 遷移させる（9.6）。**選んだ時点で送り、応答で行を差し替える**（5.4 と同じ） */
async function transitionRow(ticket: Ticket, to: TicketTransitionOption): Promise<void> {
  busy.value = true
  error.value = null
  try {
    const next = await ticketsApi.transitionTicket(projectKey.value, ticket.seq, { to: to.key })
    onDetailUpdated(next)
  } catch (e) {
    error.value = toApiError(e)
  } finally {
    busy.value = false
  }
}

/** 担当を差し替える（9.5.2）。**`If-Match` は行が持つ `version`**（2.8） */
async function assignRow(ticket: Ticket, actorId: string | null): Promise<void> {
  if ((ticket.assignee?.id ?? null) === actorId) return
  busy.value = true
  error.value = null
  try {
    const next = await ticketsApi.updateTicket(projectKey.value, ticket.seq, ticket.version, {
      assignee_id: actorId,
    })
    onDetailUpdated(next)
  } catch (e) {
    error.value = toApiError(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <!-- 一覧と詳細のマスター・ディテール（2.2.1）。**押し込む形で、重ねない** -->
  <SplitPane :open="shrunk" storage-key="pb.detail_pane_w" class="page">
    <template #primary>
      <!-- **縮小中はヘッダの左側全体が「全幅へ戻す」の当たり所になる**（5.4「一覧へ戻る導線」
           と同じ）。全幅のときはボタンにしない。**新規チケットの口は置かない**（5.13） -->
      <PageHeader
        :title="$ui('チケット検索')"
        :title-action-label="shrunk ? $ui('チケット検索を全幅に戻す') : undefined"
        :title-action-icon="shrunk ? '⤢' : undefined"
        @title-click="closeDetail"
      />

      <div class="page-body" :class="{ shrunk }">
        <!-- 縮小中は条件を1行に畳む（5.13。5.4 と同じ）。押すとその場で開く -->
        <button
          v-if="shrunk"
          type="button"
          class="search-conditions-toggle"
          :aria-expanded="conditionsOpen"
          @click="conditionsOpen = !conditionsOpen"
        > {{ $ui('検索条件') }} <span class="caret" aria-hidden="true">{{ conditionsOpen ? '▾' : '▸' }}</span>
          <span v-if="!isPristine" class="search-conditions-dot" :aria-label="$ui('条件あり')">●</span>
        </button>

        <!-- 検索の条件（5.13）。条件は URL のクエリに載る -->
        <div v-if="!shrunk || conditionsOpen" class="search-conditions">
          <div class="search-conditions-row">
            <label class="search-filter search-keyword">
              <span class="search-filter-label">{{ $ui('キーワード') }}</span>
              <input
                v-model="keyword"
                type="search"
                :placeholder="$ui('タイトル・本文・コメント')"
                @input="onKeywordInput"
                @compositionstart="onCompositionStart"
                @compositionend="onCompositionEnd"
                @keydown="onKeywordKeydown"
              />
            </label>
            <div class="search-filter">
              <span class="search-filter-label" aria-hidden="true">{{ $ui('番号') }}</span>
              <span class="search-range">
                <input
                  type="number"
                  min="1"
                  :aria-label="$ui('番号の下限')"
                  :value="queryValue('seq_from')"
                  @change="setQuery({ seq_from: ($event.target as HTMLInputElement).value })"
                />
                <span aria-hidden="true">〜</span>
                <input
                  type="number"
                  min="1"
                  :aria-label="$ui('番号の上限')"
                  :value="queryValue('seq_to')"
                  @change="setQuery({ seq_to: ($event.target as HTMLInputElement).value })"
                />
              </span>
            </div>
          </div>

          <div class="search-conditions-row">
            <div class="search-filter">
              <span class="search-filter-label" aria-hidden="true">{{ $ui('状態') }}</span>
              <MultiSelectFilter
                :label="$ui('状態')"
                :options="statusOptions"
                :selected="statusKeys"
                @update="setQuery({ status: $event.join(',') })"
              />
            </div>
            <div class="search-filter">
              <span class="search-filter-label" aria-hidden="true">{{ $ui('種別') }}</span>
              <MultiSelectFilter
                :label="$ui('種別')"
                :options="typeOptions"
                :selected="types"
                @update="setQuery({ type: $event.join(',') })"
              />
            </div>
            <!-- エピックは詳細への導線（↗）を持つので `EpicFilter` のまま使う。
                 **↗ にも `from=search` を持たせる**——落とすと、押した先がバックログに変わる -->
            <div class="search-filter">
              <span class="search-filter-label" aria-hidden="true">{{ $ui('エピック') }}</span>
              <EpicFilter
                :epics="epics"
                :selected="epicSeqs"
                :project-key="projectKey"
                :link-query="{ from: 'search' }"
                @update="setQuery({ parent: $event.join(',') })"
              />
            </div>
            <div class="search-filter">
              <span class="search-filter-label" aria-hidden="true">{{ $ui('担当') }}</span>
              <MultiSelectFilter
                :label="$ui('担当')"
                :options="assigneeOptions"
                :selected="assignees"
                @update="setQuery({ assignee: $event.join(',') })"
              />
            </div>
          </div>

          <div class="search-conditions-row">
            <div class="search-filter">
              <span class="search-filter-label" aria-hidden="true">{{ $ui('着手日') }}</span>
              <span class="search-range">
                <input
                  type="date"
                  :aria-label="$ui('着手日の始まり')"
                  :value="sinceDate('started_since')"
                  @change="setSince('started_since', ($event.target as HTMLInputElement).value)"
                />
                <span aria-hidden="true">〜</span>
                <input
                  type="date"
                  :aria-label="$ui('着手日の終わり')"
                  :value="beforeDate('started_before')"
                  @change="setBefore('started_before', ($event.target as HTMLInputElement).value)"
                />
              </span>
            </div>
            <div class="search-filter">
              <span class="search-filter-label" aria-hidden="true">{{ $ui('完了日') }}</span>
              <span class="search-range">
                <input
                  type="date"
                  :aria-label="$ui('完了日の始まり')"
                  :value="sinceDate('closed_since')"
                  @change="setSince('closed_since', ($event.target as HTMLInputElement).value)"
                />
                <span aria-hidden="true">〜</span>
                <input
                  type="date"
                  :aria-label="$ui('完了日の終わり')"
                  :value="beforeDate('closed_before')"
                  @change="setBefore('closed_before', ($event.target as HTMLInputElement).value)"
                />
              </span>
            </div>
            <button
              type="button"
              class="secondary search-clear"
              :disabled="isPristine"
              @click="clearAll"
            > {{ $ui('解除') }} </button>
          </div>
        </div>

        <p v-if="result" class="search-result">{{ result }}</p>
        <p v-if="error" class="search-error" role="alert">✕ {{ errorMessage }}</p>

        <p v-if="loading && !loaded" class="search-muted" aria-busy="true">{{ $ui('読み込み中…') }}</p>
        <template v-else-if="!error">
          <p v-if="tickets.length === 0" class="search-empty">{{ $ui('条件に一致するチケットはありません') }}</p>

          <table v-else class="search-table" :aria-busy="loading">
            <thead>
              <tr>
                <th scope="col" class="search-id-col" :aria-sort="ariaSort('seq')">
                  <button type="button" class="search-sort" @click="sortBy('seq')">
                    ID <span class="caret" aria-hidden="true">{{ caret('seq') }}</span>
                  </button>
                </th>
                <th scope="col" :aria-sort="ariaSort('title')">
                  <button type="button" class="search-sort" @click="sortBy('title')"> {{ $ui('タイトル') }} <span class="caret" aria-hidden="true">{{ caret('title') }}</span>
                  </button>
                </th>
                <th scope="col" class="search-status-col" :aria-sort="ariaSort('status')">
                  <button type="button" class="search-sort" @click="sortBy('status')"> {{ $ui('状態') }} <span class="caret" aria-hidden="true">{{ caret('status') }}</span>
                  </button>
                </th>
                <!-- 優先・担当・期限・完了日は縮小中に落とす（5.13。いずれも詳細側に出ている） -->
                <th v-if="!shrunk" scope="col" class="search-priority-col" :aria-sort="ariaSort('priority')">
                  <button type="button" class="search-sort" @click="sortBy('priority')"> {{ $ui('優先') }} <span class="caret" aria-hidden="true">{{ caret('priority') }}</span>
                  </button>
                </th>
                <!-- 担当は並べ替えられない（`ApiDesign.md` 9.2.1 の `sort` に無い） -->
                <th v-if="!shrunk" scope="col" class="search-assignee-col">{{ $ui('担当') }}</th>
                <th v-if="!shrunk" scope="col" class="search-due-col" :aria-sort="ariaSort('due_date')">
                  <button type="button" class="search-sort" @click="sortBy('due_date')"> {{ $ui('期限') }} <span class="caret" aria-hidden="true">{{ caret('due_date') }}</span>
                  </button>
                </th>
                <th v-if="!shrunk" scope="col" class="search-closed-col" :aria-sort="ariaSort('closed_at')">
                  <button type="button" class="search-sort" @click="sortBy('closed_at')"> {{ $ui('完了日') }} <span class="caret" aria-hidden="true">{{ caret('closed_at') }}</span>
                  </button>
                </th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="t in tickets"
                :key="t.seq"
                class="search-ticket-row"
                :class="{ selected: t.seq === detailSeq }"
                @click="openRow(t, $event)"
              >
                <td class="search-id-col">
                  <span class="search-type-icon" :title="ticketTypeLabels[t.type]">
                    {{ ticketTypeIcons[t.type] }}
                  </span>
                  <code class="search-seq">{{ fullId(t) }}</code>
                </td>

                <td>
                  <span class="search-title-line">
                    <!-- **クエリと `from` を持ち回る**（3.2）。行のどこを押しても同じ行き先にする -->
                    <RouterLink v-slot="{ href, navigate }" :to="detailTo(t.seq)" custom>
                      <a class="search-title" :href="href" @click.stop="navigate">{{ t.title }}</a>
                    </RouterLink>
                    <span
                      v-if="shrunk && isOverdue(t)"
                      class="search-overdue"
                      :title="$ui('期限超過（{value0}）', { value0: t.due_date })"
                      >⚠</span
                    >
                    <!-- タグは枠線＋文字（8.6）。**縮小中は出さない**（5.4 と同じ理由） -->
                    <span v-for="tag in shrunk ? [] : t.tags" :key="tag.id" class="search-tag">{{
                      tag.name
                    }}</span>
                  </span>
                </td>

                <!-- **`@click.stop` が要る**——状態セルは状態の操作に使う場所で、詳細を開く場所ではない -->
                <td class="search-status-col" @click.stop>
                  <StatusDropdown
                    :ref="(el) => setStatusRef(t.seq, el)"
                    dense
                    :current="t.status"
                    :can-transition="canTransition"
                    :busy="busy"
                    @open="loadRowTransitions(t.seq)"
                    @select="transitionRow(t, $event)"
                  />
                </td>

                <td v-if="!shrunk" class="search-priority-col">
                  <span v-if="t.priority" class="search-priority" :title="priorityLabels[t.priority]">{{
                    priorityMarks[t.priority]
                  }}</span>
                </td>

                <td v-if="!shrunk" class="search-assignee-col" @click.stop>
                  <span class="search-assignee-cell">
                    <AssigneePicker
                      :current="t.assignee"
                      :members="members"
                      :can-assign="canAssign"
                      :busy="busy"
                      @select="assignRow(t, $event)"
                    />
                    <span
                      v-if="t.working_agent"
                      class="search-working-agent"
                      :title="$ui('{value0} が処理しています', { value0: t.working_agent.display_name })"
                      >🤖</span
                    >
                  </span>
                </td>

                <td v-if="!shrunk" class="search-due-col">
                  <span v-if="t.due_date" :class="{ 'search-overdue': isOverdue(t) }">
                    <span v-if="isOverdue(t)" aria-hidden="true">⚠ </span>{{ formatPlainDate(t.due_date) }}
                  </span>
                  <span v-else class="search-muted">—</span>
                </td>

                <!-- 完了日は `timestamptz` なので `formatDate`（利用者のタイムゾーン。7.5） -->
                <td v-if="!shrunk" class="search-closed-col">
                  <span v-if="t.closed_at">{{ formatDate(t.closed_at) }}</span>
                  <span v-else class="search-muted">—</span>
                </td>
              </tr>
            </tbody>
          </table>

          <p v-if="tickets.length > 0" class="search-total">
            <template v-if="truncated">
              {{ withComma(total) }}{{ $ui('件中') }} {{ withComma(perPage) }}{{ $ui('件を表示しています。条件で絞り込んでください') }} </template>
            <template v-else>{{ withComma(total) }}{{ $ui('件') }}</template>
          </p>
        </template>
      </div>
    </template>

    <!-- チケット詳細（5.5）。**`seq` が変わっても再マウントしない**（2.2.1） -->
    <template #secondary>
      <TicketDetailPane
        v-if="detailSeq !== null"
        :project-key="projectKey"
        :seq="detailSeq"
        :members="members"
        :tags="tags"
        :workflow="projectStore.current?.workflow ?? null"
        :candidates="tickets"
        :epics="epics"
        @close="closeDetail"
        @updated="onDetailUpdated"
        @created="onDetailCreated"
        @deleted="onDetailDeleted"
      />
    </template>
  </SplitPane>
</template>

<style scoped>
/* **クラス名は画面名を冠する**（6.6）。scoped は DOM のクラス名を分けないので、
   `.table` `.row` のような一般名は検証のセレクタがバックログにも当たる。
   **値はバックログ（`BacklogPage.vue`）に揃える**——見た目のずれは表の切り出しの合図になる（5.13） */
.page {
  height: 100%;
}

.page-body {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: var(--pb-space-6);
  /* 狭い窓で列を落とす判定に使う（下の `@container`） */
  container-type: inline-size;
}

.page-body.shrunk {
  padding: var(--pb-space-4) var(--pb-space-3);
}

/* ── 検索の条件 ─────────────────────────────────────────── */

.search-conditions-toggle {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  height: 28px;
  padding: 0 var(--pb-space-2);
  margin-bottom: var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.search-conditions-toggle:hover {
  background: var(--pb-hover);
}

/* 畳んでいても条件が効いていることは読めるようにする。**色ではなく点の有無で示す**（8.6） */
.search-conditions-dot {
  color: var(--pb-text-muted);
  font-size: 10px;
}

.search-conditions {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
  margin-bottom: var(--pb-space-4);
}

.search-conditions-row {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--pb-space-3);
}

.search-filter {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

.search-filter-label {
  color: var(--pb-text-muted);
  font-size: 13px;
  white-space: nowrap;
}

.search-conditions input {
  height: 32px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

/* キーワードは最も使う条件なので広く取る。狭い窓では行の幅に収める */
.search-keyword input {
  width: 360px;
  max-width: 100%;
}

.search-range {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  color: var(--pb-text-muted);
}

.search-range input[type='number'] {
  width: 88px;
}

.search-range input[type='date'] {
  width: 150px;
}

/* `[解除]` は行の右端へ寄せる。狭い窓では折り返して先頭に来る */
.search-clear {
  height: 32px;
  margin-left: auto;
}

.search-result {
  margin: 0 0 var(--pb-space-3);
}

.search-error {
  margin: 0 0 var(--pb-space-3);
  color: var(--pb-danger-text);
}

.search-empty,
.search-muted {
  color: var(--pb-text-muted);
}

.search-empty {
  margin: 0;
  padding: var(--pb-space-3) var(--pb-space-2);
  border: 1px dashed var(--pb-border);
  font-size: 13px;
}

/* ── 表（値はバックログの `.table` と同じ）──────────────────── */
.search-table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

.search-table th,
.search-table td {
  padding: 0 var(--pb-space-2);
  overflow: hidden;
  border-bottom: 1px solid var(--pb-line);
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

.search-table th {
  height: var(--pb-row-h);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
}

.search-table td {
  height: var(--pb-row-h);
}

.search-sort {
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

.search-ticket-row {
  cursor: pointer;
}

.search-ticket-row:hover {
  background: var(--pb-hover);
}

/* 開いている行は面の輝度で示す（8.2 / 8.6。バックログと同じ） */
.search-ticket-row.selected,
.search-ticket-row.selected:hover {
  background: var(--pb-active);
}

/* ── 列幅 ─────────────────────────────────────────────── */

/* 種別アイコンと完全形の ID が入る幅（バックログの `⠿` 列を持たないぶん、アイコンをここへ入れる） */
.search-id-col {
  width: 140px;
}

.search-status-col {
  width: 104px;
}

.search-priority-col {
  width: 56px;
}

.search-assignee-col {
  width: 146px;
}

.search-due-col {
  width: 120px;
}

/* `2026-09-14` が入る幅 */
.search-closed-col {
  width: 112px;
}

/* **一覧の幅が 900px 未満のときは、優先と期限の列を出さない**（5.13「狭い窓」）。
   全列を残すとタイトル列が潰れる（800px の窓で幅がほぼ0になる）。
   どちらも詳細ペインに出ている項目で、縮小中に落とす列（5.4）と同じ考え方である。
   **窓ではなく一覧の幅で決める**——メニューを畳んだかどうかで一覧の幅が変わるため */
@container (max-width: 899px) {
  .search-priority-col,
  .search-due-col {
    display: none;
  }
}

/* ── セルの中身 ───────────────────────────────────────── */

.search-type-icon {
  margin-right: var(--pb-space-1);
  color: var(--pb-text-muted);
}

.search-seq {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.search-title-line {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-2);
  max-width: 100%;
  overflow: hidden;
}

/* **タイトルはタグより先に縮まない**（バックログと同じ。900px で実測済みの教訓） */
.search-title {
  flex: 1 1 auto;
  min-width: 6em;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.search-title:hover {
  text-decoration: underline;
}

.search-tag {
  flex: none;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  color: var(--pb-text-muted);
  font-size: 12px;
  line-height: 18px;
}

.search-overdue {
  flex: none;
  color: var(--pb-danger-text);
}

.search-priority {
  color: var(--pb-text-muted);
}

/* 担当と実行者を1行に収める。**縮むのは担当の名前だけで、🤖 は縮まない**（バックログと同じ） */
.search-assignee-cell {
  display: flex;
  align-items: baseline;
  gap: 4px;
  min-width: 0;
}

.search-working-agent {
  flex: none;
  color: var(--pb-text-muted);
}

.search-total {
  margin: var(--pb-space-3) 0 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
