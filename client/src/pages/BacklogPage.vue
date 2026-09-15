<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useTemplateRef, watch } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import AssigneePicker from '../components/AssigneePicker.vue'
import EmptyState from '../components/EmptyState.vue'
import EpicFilter from '../components/EpicFilter.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import NewTicketModal from '../components/NewTicketModal.vue'
import type { NewTicketDefaults } from '../components/NewTicketModal.vue'
import PageHeader from '../components/PageHeader.vue'
import SplitPane from '../components/SplitPane.vue'
import StatusDropdown from '../components/StatusDropdown.vue'
import SprintStartModal from '../components/SprintStartModal.vue'
import TicketDetailPane from '../components/TicketDetailPane.vue'
import { ApiError } from '../api/client'
import * as sprintsApi from '../api/sprints'
import type { Sprint, StartSprintRequest } from '../api/sprints'
import * as tagsApi from '../api/tags'
import type { Tag } from '../api/tags'
import * as ticketsApi from '../api/tickets'
import {
  backlogTicketTypes,
  priorityLabels,
  priorityMarks,
  priorityOrder,
  statusCategoryLabels,
  statusCategoryOrder,
  ticketTypeIcons,
  ticketTypeLabels,
} from '../api/tickets'
import type {
  CreateTicketRequest,
  MoveTicketRequest,
  Ticket,
  TicketDetail,
  TicketPriority,
  TicketSort,
  TicketTransitionOption,
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
 *
 * **チケット詳細（5.5）もこの画面が持つ**（手順17b）。詳細は画面を置き換えず、
 * **一覧の右に3枚目のペインとして開く**（2.2.1）。`/p/:key/backlog` と
 * `/p/:key/tickets/:seq` は**同じコンポーネントを指す**ルートで、行をクリックしても
 * この画面は再マウントされない——だから**一覧の取得結果・折りたたみ・スクロール
 * 位置がそのまま残る**（5.4「戻したときに保つもの」）。
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
/**
 * スプリントの開始・終了は `project.edit`（`ApiDesign.md` 9.12.1 / 9.12.2）。
 *
 * **持っていない人にはボタンを出さない**（`GuiDesign.md` 2.4 の権限による出し分け）。
 */
const canRunSprint = computed(() => auth.canInProject(projectKey.value, 'project.edit'))
const canEdit = computed(() => auth.canInProject(projectKey.value, 'ticket.edit'))
/** 一覧から状態を変えるのに要る（`ApiDesign.md` 9.6。pb-63） */
const canTransition = computed(() => auth.canInProject(projectKey.value, 'ticket.transition'))
/** 一覧から担当を変えるのに要る（9.5.2 は `ticket.edit` に加えてこれを要求する。pb-64） */
const canAssign = computed(
  () => canEdit.value && auth.canInProject(projectKey.value, 'ticket.assign'),
)

// ── 詳細ペイン（2.2.1 / 5.5）─────────────────────────────────

/**
 * 開いているチケットの `seq`。**URL が持つ**（3.2）。
 *
 * `/p/:key/tickets/31?status=in_progress&group=tag` の形で、**バックログの
 * フィルタをそのまま持ち回る**。詳細を開いた瞬間にクエリを捨てるとフィルタが
 * 共有できなくなり、逆にバックログの URL のまま詳細を表すとチケット単体の
 * リンクが作れなくなる（3.2「URL設計の判断」）。
 */
const detailSeq = computed<number | null>(() => {
  const raw = route.params.seq
  const n = typeof raw === 'string' ? Number(raw) : Number.NaN
  return Number.isInteger(n) && n > 0 ? n : null
})

/**
 * 詳細を開いている間、一覧は約450pxへ縮み**列が3つになる**
 * （5.4「詳細を開いているときの一覧」）。
 *
 * **落ちる列はいずれも詳細側に出ているもの**で、残すのは「次にどれを開くか」を
 * 決めるのに要るものだけ。`状態` を残すのは、この作業が**進捗を順に確認して
 * いく**ものだからである（2.2.1）。
 *
 * **並ぶかどうかは `SplitPane` が幅で決める**（2.4）。並ばない幅では詳細が全幅に
 * なって一覧が隠れるので、そのときこの値が何であっても表は描かれない。
 */
const shrunk = computed(() => detailSeq.value !== null)

/** 縮小中のフィルタ行は `[絞り込み ▾]` の1行に畳む（5.4）。押すとその場で開く */
const filtersOpen = ref(false)

/** 一覧の現在のフィルタを保ったまま行き先を作る。**クエリを落とさない**（3.2） */
function withQuery(path: string): { path: string; query: typeof route.query } {
  return { path, query: route.query }
}

/**
 * 詳細を閉じて一覧を全幅へ戻す（5.4「一覧へ戻る導線」）。
 *
 * **URL も `/p/:key/backlog?<フィルタ>` へ戻す。** `replace` を使うのは、
 * 開く・閉じるが履歴に積まれると「戻る」でバックログから出られなくなるため
 * ——フィルタの操作（`setQuery`）と同じ扱いである。
 */
function closeDetail(): void {
  void router.replace(withQuery(`/p/${projectKey.value}/backlog`))
}

/**
 * 詳細で変更が確定した。**一覧の該当行だけ差し替える**（5.4「戻したときに保つもの」の
 * 「詳細で変更した内容を反映した行」）。
 *
 * **取り直さない。** 編集のたびに並びが組み直されると、いま見ている行が動いて
 * 「次の行をクリックする」作業が壊れる。並び順が実際に変わるのは取り直した
 * ときで、それは利用者が明示的にソートやフィルタを触ったときでよい。
 */
function onDetailUpdated(next: TicketDetail): void {
  // **`TicketDetail` は `Ticket` に6項目を足したもの**（9.5.1）なので、そのまま
  // 一覧の行として使える。`sort_key` / `staged_at` はサーバの値をそのまま採る
  // ——`PATCH` では動かないが（9.5.2 の `use_move_endpoint`）、他の誰かが
  // `move` していれば応答のほうが新しい
  tickets.value = tickets.value.map((t) => (t.seq === next.seq ? { ...t, ...next } : t))
}

/**
 * 詳細ペインで**行が増えた**（子チケットの作成。5.5「子チケット」）。
 *
 * **取り直す。** `onDetailUpdated` の「差し替えるだけで取り直さない」を
 * ここへ写せない——**新しい行は結果集合に一度も入っていない**ので、
 * いまのフィルタに合致するか、`sort_key` の並びのどこへ入るかを手元で
 * 決められない。手元へ足すと、**絞り込みに合わない行が出たうえで
 * 下部の総件数と食い違う**（9.2.2 / 5.4）。
 *
 * **`onDetailDeleted` が既に同じ判断をしている。** 行が増減したときは取り直す、
 * 行の中身が変わっただけのときは差し替える、という切り分けである（pb-15）。
 *
 * **結果の一言は出さない。** 操作したのは詳細ペインであり、作られた子は
 * あちらの「子チケット」の節に現れる（6.4「操作結果は操作した場所に出す」）。
 */
function onDetailCreated(): void {
  void loadTickets()
}

/** 削除された（9.5.3）。**画面が消える操作なので、結果は着地する一覧へ渡す**（6.4） */
function onDetailDeleted(seq: number, title: string): void {
  tickets.value = tickets.value.filter((t) => t.seq !== seq)
  total.value = Math.max(total.value - 1, 0)
  result.value = `✓ ${projectKey.value}-${seq}「${title}」を削除しました`
  closeDetail()
  // **子は消えず親を失ってトップレベルへ上がる**（9.5.3）。手元の行では
  // 親子の付け替えが起きているので、そこだけ取り直す
  void loadTickets()
}

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

/**
 * 「状態」の箱が持つ3系列のクエリ名（5.4「状態と期限のフィルタ」。手順19b）。
 *
 * **1つの箱で3つのクエリを出し入れする。** どれも「進み具合で絞る」という
 * 同じ軸の値であり、同時に2つを選ぶ意味がない。**選ぶたびに他の2つを消す**
 * ——消さないと `?open=true&status_category=done` のような、画面のどこにも
 * 表れない組み合わせが URL に残る。
 */
const STATE_KEYS = ['status', 'status_category', 'open', 'stale'] as const

/**
 * 「期限」の箱が持つクエリ名（同上）。
 *
 * `overdue` と `due_within` は**別の条件**である（`ApiDesign.md` 9.2.1）——
 * `due_within=0d` は「今日以前」で今日締切を含み、`overdue`（`due_date < 今日`）
 * と1日ぶんずれる。
 */
const DUE_KEYS = ['overdue', 'due_within'] as const

/**
 * 状態フィルタが「完了」を指しているか（5.4「状態と期限のフィルタ」。pb-5 / pb-6）。
 *
 * **これが `retired=true` を送る唯一の条件である。** スプリントを終えて棚に
 * 戻ったチケットは既定で一覧から外れるので（`ApiDesign.md` 9.2.1）、
 * **完了を明示的に選んだときだけ戻す**——検索画面（`/p/:key/search`、3.2 の
 * プレースホルダ）ができるまでの逃げ道である。
 *
 * **「すべて」では送らない。** 素の状態のバックログは「これからやるべき仕事」を
 * 並べる面であり、終わった仕事が混ざると走査の妨げになる。
 *
 * 3系列のどれで完了を選んでも効かせる（`open=false` / `status_category=done` /
 * **完了カテゴリのステータスキー**）。**キーからカテゴリを引くのに
 * ワークフローを読む**——`done` という名前のキーを決め打ちにすると、
 * ワークフローを差し替えたプロジェクトで効かなくなる。
 */
const wantsRetired = computed(() => {
  if (queryValue('open') === 'false') return true
  if (splitQuery('status_category').includes('done')) return true
  const keys = splitQuery('status')
  if (keys.length === 0) return false
  const statuses = projectStore.current?.workflow?.statuses ?? []
  return keys.some((k) => statuses.find((st) => st.key === k)?.category === 'done')
})

/** カンマ区切りのクエリを配列にする（空は落とす） */
function splitQuery(name: string): string[] {
  return queryValue(name)
    .split(',')
    .filter((v) => v !== '')
}

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

/**
 * 「状態」の箱の現在値。**選択肢の `value` は `<クエリ名>:<値>`** で、
 * 空文字が「すべて」である。URL から復元するので、共有されたリンクを開いても
 * 箱に選択が出る（5.4「フィルタとグループ化の保持」）。
 */
const stateValue = computed<string>(() => {
  for (const k of STATE_KEYS) {
    const v = queryValue(k)
    if (v !== '') return `${k}:${v}`
  }
  return ''
})

/** 「期限」の箱の現在値。同じ形式 */
const dueValue = computed<string>(() => {
  for (const k of DUE_KEYS) {
    const v = queryValue(k)
    if (v !== '') return `${k}:${v}`
  }
  return ''
})

/**
 * 排他の箱を1つ選ぶ。**同じ群の他のキーを空にしてから**新しい値を入れる。
 *
 * `setQuery` は空文字のキーを落とすので、これで URL には常に1つだけ残る。
 */
function setExclusive(keys: readonly string[], selected: string): void {
  const patch: Record<string, string> = {}
  for (const k of keys) patch[k] = ''
  if (selected !== '') {
    const at = selected.indexOf(':')
    patch[selected.slice(0, at)] = selected.slice(at + 1)
  }
  setQuery(patch)
}

/**
 * 「放置」の選択肢が使う日数（5.4）。**閾値の正本ではない**——正本は
 * サーバ（`ApiDesign.md` 9.13.1 の `threshold_days`）で、ダッシュボードは
 * 応答の値をそのままリンクへ載せる。ここは「素の状態から選ぶときの既定」
 * であり、5.3 のワイヤーの文言に合わせてある。
 */
const STALE_DEFAULT_DAYS = 14

/**
 * いま選ばれている `stale` の日数。**URL の値から読む**ので、ダッシュボード
 * から `?stale=30d` で来ればラベルも「30日以上更新なし」になる。
 */
const staleDays = computed(() => {
  const m = /^(\d+)d$/.exec(queryValue('stale'))
  return m === null ? STALE_DEFAULT_DAYS : Number(m[1])
})

/** 素の状態か。`[解除]` を出すかどうかの判定に使う */
const isPristine = computed(
  () =>
    FILTER_KEYS.every((k) => filters.value[k] === '') &&
    stateValue.value === '' &&
    dueValue.value === '' &&
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

/** スプリントの開始ダイアログ（5.4「開始のダイアログ」。pb-6） */
const showSprintStart = ref(false)
/** スプリントの終了確認（5.4「終了の確認」） */
const confirmSprintFinish = ref(false)
/** 開始ダイアログへ返す検証エラー（`details[].field` → メッセージ） */
const sprintStartErrors = ref<Record<string, string>>({})

/**
 * いま進行中のスプリント（`ApiDesign.md` 9.12.1 は同時に1本だけを許す）。
 *
 * **一覧から拾う。** 語彙として既に `GET /sprints` を引いているので、
 * 進行中を知るために往復を増やさない。
 */
const activeSprint = computed(() => sprints.value.find((s) => s.status === 'active') ?? null)

/**
 * 進行中のスプリントの期間表示（`8/05 — 8/21`）。
 *
 * **年を出さない。** ここは「いま回っている期間」であり、5.4 の期限列とは
 * 事情が違う（あちらは来年の期限が今年に見えると判断を誤るので年を出す）。
 * 見出しの1行に収める必要があり、両端が揃っていれば月日で足りる。
 */
const activeSprintPeriod = computed(() => {
  const sp = activeSprint.value
  if (!sp) return ''
  const md = (d: string | null | undefined) => (d ? d.slice(5).replace('-', '/') : '')
  const from = md(sp.start_date)
  const to = md(sp.end_date)
  if (from === '' && to === '') return ''
  return `${from} — ${to}`
})

/**
 * オンステージ段に出ている行数（開始ダイアログに出す参考値）。
 *
 * **サーバの判定とは別物である。** あちらは `staged_at` を持つ行の部分木を
 * DB で取る（9.12.1）。ここはフィルタが掛かった手元の表示から数えるので、
 * **絞り込み中は実際の対象より少なく出る。**
 */
const onstageCount = computed(() => staged.value.staged.length)

/**
 * 終了したときの内訳（5.4「終了の確認」）。**押す前に数で出す。**
 *
 * **数えるのはスプリントの対象であって、オンステージ段の行ではない**
 * （実機で判明、2026-09-09）。手元の行から数えていたときは、
 *
 * - **オンステージから降りた対象が漏れる**（対象に入ったあとバックログ段へ
 *   戻した行）。実際に「完了した 0 件」と出しながら1件が消えた
 * - フィルタで絞っている間は、絞られた行が数から落ちる
 *
 * サーバが `ticket_count` / `closed_count` を返しているので（`ApiDesign.md`
 * 9.12）、**そちらを正本にする。**
 */
const finishPreview = computed(() => {
  const sp = activeSprint.value
  const total = sp?.ticket_count ?? 0
  const closed = sp?.closed_count ?? 0
  return { total, closed, remaining: total - closed }
})

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
      // 「状態」の箱が出す3系列（5.4。手順19b）。**排他なので同時に入らない** ——
      // `setExclusive` が他を消すが、共有された URL に複数書かれていても
      // サーバは AND で解釈するだけで壊れない。
      status_category: queryValue('status_category'),
      open: queryValue('open') === 'true' ? 'true' : queryValue('open') === 'false' ? 'false' : undefined,
      stale: queryValue('stale'),
      // **完了を明示的に選んだときだけ棚に戻ったものを出す**（5.4。pb-5 / pb-6）
      retired: wantsRetired.value ? 'true' : undefined,
      // 「期限」の箱が出す2系列（同上）
      overdue: queryValue('overdue') === 'true' ? 'true' : undefined,
      due_within: queryValue('due_within'),
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

/**
 * エピックの語彙だけを取り直す（5.4.3「新規エピック」。pb-14）。作ったエピックを
 * フィルタの選択肢に出すためで、タグとスプリントは変わっていない。
 * **失敗しても一覧は止めない**（`loadVocabulary` と同じ扱い）。
 */
async function reloadEpics(): Promise<void> {
  try {
    epics.value = (await ticketsApi.listTickets(projectKey.value, { type: 'epic' })).items
  } catch {
    // 選択肢が古いままになるだけで、作成そのものは成功している
  }
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
 * **上げた行の配下は、オンステージ段に親の下として出す**（5.4「配下の行き先」。
 * 利用者の判断、2026-09-06。pb-46）。**バックログ段には出さない**——「行は
 * 片方の段にしか出ない」という大原則を保つためで、両方に出すと上下を
 * 見比べる作業が復活する。
 *
 * **段を決めるのは親であり、子は親と一緒に運ばれる**（9.4.1）。配下の行は
 * `staged_at` が `NULL` のままオンステージ段に現れるので、**`staged_at` だけで
 * 振り分けてはいけない**。
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
    // **`carried` は「オンステージの行の部分木」である。** `staged_at` は
    // `NULL` のままなので、この判定を先に置かないとバックログ段へ落ちる
    if (carried.has(t.seq) || t.staged_at !== null) staged.push(t)
    else backlog.push(t)
  }
  return { staged, backlog }
}

const staged = computed(() => splitByStage(tickets.value))

/**
 * **その行がいまどちらの段に出ているか。** `staged_at` で判定してはいけない
 * （pb-46）——オンステージへ上げた行の配下は `staged_at` が `NULL` のまま
 * オンステージ段に現れるためで、**段を決めるのは親である**（9.4.1）。
 */
const stagedSeqs = computed(() => new Set(staged.value.staged.map((t) => t.seq)))

function shownInStage(t: Ticket): boolean {
  return stagedSeqs.value.has(t.seq)
}

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
  // 二段（5.4）。**両方の段が親子のインデントと折りたたみを持つ**（pb-46）。
  // オンステージ段には親と一緒に運ばれた配下が来るので、バックログ段と
  // 同じ条件でツリーに組み直す（グループ化が「なし」かつ `sort_key` の昇順）。
  if (twoTier.value) {
    return [
      {
        key: 'staged',
        label: 'オンステージ',
        rows: treeMode.value ? buildTree(staged.value.staged) : flatRows(staged.value.staged),
        stage: true,
      },
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
 * 行クリックで**右の詳細ペインを開く**（5.4「行クリック」）。画面は遷移せず、
 * 一覧は縮んで残る（2.2.1）。`Ctrl/⌘+クリック` で新規タブ。
 *
 * **文字を選択しただけのときは遷移しない。** ID をなぞってコピーしようと
 * すると mouseup のあとに click が来るため、そのまま遷移してしまう。
 *
 * **`replace` を使う。** 行をたどるたびに履歴が積まれると、「戻る」で
 * 見終わったチケットを逆順に開き直すことになる。フィルタの操作（`setQuery`）と
 * 同じ扱いにそろえる。
 */
function openRow(t: Ticket, e: MouseEvent): void {
  if ((window.getSelection()?.toString() ?? '') !== '') return
  const to = withQuery(`/p/${projectKey.value}/tickets/${t.seq}`)
  if (e.metaKey || e.ctrlKey || e.shiftKey) {
    window.open(router.resolve(to).href, '_blank', 'noopener')
    return
  }
  void router.replace(to)
}

// ── 並べ替えと段の行き来（ドラッグ。`ApiDesign.md` 9.4）───────

/**
 * 掴めるのは**ソートが `sort_key` の昇順のとき**だけである（5.4）。
 * 他の並びでは、画面上の位置と `after_seq` の意味が一致しない。
 *
 * **縮小中も掴める**（5.4「掴みしろ」。pb-7）。以前は `!shrunk` を
 * 条件に持っていたが、**詳細を開いたまま消化順を組み替える**のは実際に起きる
 * 作業で、そのたびに全幅へ戻すことになっていた。
 */
const canReorder = computed(
  () => canEdit.value && sort.value === 'sort_key' && order.value === 'asc',
)

/**
 * 掴みしろを行そのものに置くか（5.4「掴みしろ」）。
 *
 * **全幅でも縮小中でも行全体を掴む**（利用者の要望、2026-09-07。pb-71）。
 * かつては縮小中だけがこの形だった——450px では `⠿` の列を持てないためで、
 * **全幅では `⠿` の28px だけが掴みしろ**だった。実運用では**幅の広いほうが
 * 狙いにくい**と分かった。行が長いほど `⠿` は遠く、並べ替えのたびに左端まで
 * ポインタを運ぶことになる。
 *
 * **`⠿` の列は残す**（全幅のとき）。ヘッダが「`sort_key` へ戻す」ボタンを
 * 兼ねており（5.4）、列ごと落とすとその導線が失われる。**掴める範囲が
 * 広がるだけである。**
 */
const grabWholeRow = canReorder

/**
 * ポインタが**操作を持つセル**（状態・担当）の上にあるか（5.4「掴みしろ」。pb-71）。
 *
 * **`draggable="false"` を子孫に置いても親のドラッグは止まらない**（実測、
 * 2026-09-07）。`dragstart` はドラッグ元——つまり `draggable` な `<tr>`——で
 * 発火するので、`<td>` に `@dragstart.stop.prevent` を書いても**イベントの経路に
 * 入らず一度も呼ばれない**。属性 `draggable="false"` も、Chrome は上位の
 * `draggable="true"` まで遡るため効かない。
 *
 * **そこで、ポインタがそのセルに入っている間だけ行を掴めなくする。** 掴めるのは
 * ポインタが載っている場所だけなので、値は1つで足りる。
 *
 * **要素を差し込むわけではない**ので、`dragstart` の直後に位置がずれて Chrome が
 * ドラッグを取り消す問題（5.4「掴んだ瞬間に要素を差し込まない」）には当たらない。
 */
const overActionCell = ref(false)

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
type DropSide = 'before' | 'after' | 'inside' | 'first' | 'last'

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
    // **インデントされた行を根の並びへ落とすとルートになる**（5.4。pb-70）。
    // 親を外すことと位置決めが `move` 1本で決まるので、線を出してよい。
    // **兄弟の中での並べ替えは従来どおり**——同じ親を持つ行の上下へは、
    // 親を触らずに落とせる。
    if (sourceRow.parentKey !== null) {
      if (row.parentKey === null) return canUnparentInto(section)
      return sourceRow.parentKey === row.parentKey && shownInStage(source) === section.stage
    }
    // **同じ段の中なら、表示上の親が同じ行どうし**（オンステージ段にも
    // インデントされた行が出るようになったので、根だけとは限らない。pb-46）
    if (shownInStage(source) === section.stage) {
      return sourceRow.parentKey === row.parentKey
    }
    return isStageable(source) && row.parentKey === null
  }
  return sectionsOf(source).some((s) => s.key === section.key)
}

/**
 * 段そのもの（見出し＝先頭、空の枠＝末尾）へ落としてよいか。
 *
 * 空の段には基準にできる行が無いので、この落とし場所が無いと最初の1件を
 * 上げられない（`ApiDesign.md` 9.4.1 が `position` を段の中で解釈する理由）。
 *
 * **表示上の根と、インデントされた行とで意味が違う**（5.4「ドロップ先の見せ方」）。
 *
 * | 掴んでいる行 | 落としたときに起きること |
 * |---|---|
 * | 表示上の根 | その段の先頭／末尾へ動く（`move`）。段をまたいでもよい |
 * | インデントされた行 | **ルートになる**（`PATCH parent_seq: null`。pb-70）。**バックログ段だけ** |
 *
 * **インデントされた行に `position` を送らない。** その行は親の下で兄弟の端へ
 * 動くだけで、「その段の先頭／末尾へ」という表示と食い違う。**ルート化は
 * 位置を約束しない別の操作**なので、目印も線ではなく枠で出す。
 */
function canDropOnSection(sourceSeq: number | null, section: Section): boolean {
  if (sourceSeq === null || section.stage === undefined) return false
  const sourceRow = rowIndex.value.get(sourceSeq)
  if (sourceRow === undefined) return false
  if (sourceRow.parentKey !== null) return canUnparentInto(section)
  if (shownInStage(sourceRow.ticket) === section.stage) return true
  return isStageable(sourceRow.ticket)
}

/**
 * インデントされた行をこの段へ落として**ルートにできる**か（5.4。pb-70）。
 *
 * **バックログ段だけである。** サーバは `parent_seq: null` と `staged` を同時に
 * 受け取れるが（`ApiDesign.md` 9.4.2）、**画面の落とし先は絞る**——ルートにする
 * のと段へ上げるのは別の判断であり、1回のドラッグに2つ込めると誤操作が戻し
 * にくい。**ルートになった行は `staged_at` が `NULL` のままなので、必ずバック
 * ログへ出る**（配下は親と一緒に運ばれていただけ。5.4「配下の行き先」）
 * ——落とし先と着地が一致する。
 */
function canUnparentInto(section: Section): boolean {
  return section.stage === false
}

/**
 * ポインタが行のどこを指しているか（5.4「ドロップ先の見せ方」）。
 *
 * **上 1/4・下 1/4・中央 1/2。** 上下は兄弟（`sort_key`）、中央は子
 * （`parent_seq`）で、**1回のドロップで2軸のどちらを動かすかを決める**（pb-16）。
 * 半分で割る形では兄弟しか表せない。**5.10 の文書ツリーが先に採った形**で、
 * `DocTree.vue` の `zoneOf` と同じ割り方である。
 *
 * **掴んだ行がどこから来たかに依らない。** 「越えた向き」で決める方式は、
 * 行の下 1/4 を指しても上に入ることがあり、線を出した意味がなくなる。
 */
function sideOf(e: DragEvent): 'before' | 'after' | 'inside' {
  const el = e.currentTarget as HTMLElement | null
  if (el === null) return 'after'
  const r = el.getBoundingClientRect()
  const y = e.clientY - r.top
  if (y < r.height / 4) return 'before'
  if (y > (r.height * 3) / 4) return 'after'
  return 'inside'
}

/**
 * その行の**子にして**よいか（5.4「ドロップ先の見せ方」。pb-16）。
 *
 * **サーバが弾く条件を、そのまま画面の規則にする**——落とせない相手の上では
 * 面を出さず、カーソルを禁止の形にする。
 *
 * | 落とせない相手 | 根拠 |
 * |---|---|
 * | 自分自身と自分の子孫 | `parent_cycle`（`ApiDesign.md` 9.5.2） |
 * | オンステージの行を、エピック以外の子にする | `not_stageable`（9.5.2 / 9.4.1） |
 * | いま親である行 | 送っても何も変わらない |
 *
 * **段をまたいでもよい。** バックログの行をオンステージの行の子にすると、
 * その行は親と一緒に運ばれてオンステージ段に出る（5.4「配下の行き先」）。
 */
function canDropInto(sourceSeq: number | null, row: Row): boolean {
  if (sourceSeq === null || sourceSeq === row.ticket.seq) return false
  const sourceRow = rowIndex.value.get(sourceSeq)
  if (sourceRow === undefined) return false
  const source = sourceRow.ticket
  if (source.parent_seq === row.ticket.seq) return false
  // **オンステージの行は、エピック以外の子になれない。** 配下は親と一緒に
  // 運ばれるので、子を個別に段へ置く操作は意味を持たない（9.4.1）
  if (source.staged_at !== null && row.ticket.type !== 'epic') return false
  return !isDescendant(row.ticket.seq, sourceSeq)
}

/**
 * `seq` が `ancestorSeq` の子孫か。**自分の子孫を親にすると輪になる**
 * （`parent_cycle`。9.5.2）。サーバもアプリ層で検出するが、ここで見るのは
 * ドラッグ中にその部分木をドロップ不可として見せるためである。
 */
function isDescendant(seq: number, ancestorSeq: number): boolean {
  const bySeq = new Map(tickets.value.map((t) => [t.seq, t]))
  let cur = bySeq.get(seq)
  const seen = new Set<number>()
  while (cur?.parent_seq != null && !seen.has(cur.seq)) {
    if (cur.parent_seq === ancestorSeq) return true
    seen.add(cur.seq)
    cur = bySeq.get(cur.parent_seq)
  }
  return false
}

/**
 * 受け取れる相手の上でだけ `preventDefault()` する。
 *
 * HTML の D&D は「既定の動作を止めた要素」だけがドロップ先になる規約なので、
 * これで**落とせない行の上ではカーソルが禁止の形になる**。すべて受け取って
 * から弾くと、落とせるように見えて何も起きない。
 */
function onDragOverRow(e: DragEvent, row: Row, section: Section): void {
  const side = sideOf(e)
  const ok =
    side === 'inside'
      ? canDropInto(draggingSeq.value, row)
      : canDropOn(draggingSeq.value, row, section)
  if (!ok) {
    dropHint.value = null
    return
  }
  e.preventDefault()
  dropHint.value = { key: section.key, seq: row.ticket.seq, side }
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
function hintsRow(row: Row, section: Section, side: 'before' | 'after' | 'inside'): boolean {
  const h = dropHint.value
  return h !== null && h.key === section.key && h.seq === row.ticket.seq && h.side === side
}

function hintsSection(section: Section, side: 'first' | 'last'): boolean {
  const h = dropHint.value
  return h !== null && h.key === section.key && h.seq === null && h.side === side
}


function moveMessage(t: Ticket, stagedChange: boolean | undefined, unparented: boolean): string {
  const id = `${fullId(t)}「${t.title}」`
  // **ルート化を先に言う。** 同じ操作で段も動きうるが、利用者が意図したのは
  // 親を外すことである（5.4。pb-70）
  if (unparented) return `✓ ${id}をルートにしました`
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
  unparented = false,
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
    result.value = moveMessage(source, stagedChange, unparented)
  } catch (e) {
    tickets.value = before
    error.value = toApiError(e)
    await loadTickets()
  } finally {
    busy.value = false
  }
}

/**
 * 段が変わるか。変わらないときは `undefined`（`move` に `staged` を送らない）。
 *
 * **`staged_at` ではなく「いま出ている段」で比べる**（pb-46）。配下の行は
 * `staged_at` が `NULL` のままオンステージ段に居るので、`staged_at` で比べると
 * **同じ段の中で動かしただけなのに `staged: true` を送り**、親を持つ行なので
 * 422 `not_stageable` になる（9.4.1）。
 */
function stageChangeOf(source: Ticket, section: Section): boolean | undefined {
  if (section.stage === undefined) return undefined
  return shownInStage(source) === section.stage ? undefined : section.stage
}

/** 段の見た目を先に変えるための行。正しい値はサーバ応答で上書きする */
function optimisticRow(source: Ticket, stagedChange: boolean | undefined): Ticket {
  if (stagedChange === undefined) return source
  return { ...source, staged_at: stagedChange ? new Date().toISOString() : null }
}

/**
 * その行の子にする（5.4「ドロップ先の見せ方」の中央 1/2。pb-16）。
 *
 * **送るのは `PATCH` の `parent_seq` だけで、`move` は呼ばない。**
 * 新しい親の下での位置は、続けて並べ替えれば決められる。2本続けて送ると
 * **片方だけ成功した状態**が残りうる。
 *
 * **取り直す。** 親子が変わると木の組み方が変わり、`has_children` も動く。
 * 手元で組み替えるより、サーバの答えを1回もらうほうが確かである。
 */
async function dropInto(source: Ticket, parent: Ticket): Promise<void> {
  busy.value = true
  result.value = ''
  error.value = null
  try {
    await ticketsApi.updateTicket(projectKey.value, source.seq, source.version, {
      parent_seq: parent.seq,
    })
    await loadTickets()
    result.value = `✓ ${fullId(source)}「${source.title}」を ${fullId(parent)}「${parent.title}」の子にしました`
  } catch (err) {
    error.value = toApiError(err)
    await loadTickets()
  } finally {
    busy.value = false
  }
}

/**
 * 行の上へ落とす。**ポインタが指した位置がそのまま行き先になる**
 * （5.4「ドロップ先の見せ方」）——上 1/4 と下 1/4 が兄弟、**中央 1/2 が子**。
 * 出した目印と着地を一致させるためで、掴んだ行がどこから来たかには依らない。
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

  if (side === 'inside') {
    if (!canDropInto(seq, row)) return
    const source = tickets.value.find((t) => t.seq === seq)
    if (source === undefined) return
    await dropInto(source, row.ticket)
    return
  }

  if (!canDropOn(seq, row, section)) return

  const from = tickets.value.findIndex((t) => t.seq === seq)
  if (from < 0) return
  const source = tickets.value[from]!
  const stagedChange = stageChangeOf(source, section)

  // **インデントされた行を根の並びへ落としたらルートにする**（5.4。pb-70）。
  // **親と位置を `move` 1本で送る**（`ApiDesign.md` 9.4.2）——2本に分けると
  // 「ルートにはなったが位置は元のまま」が残りうる。
  const unparenting = rowIndex.value.get(seq!)?.parentKey != null && row.parentKey === null

  const body: MoveTicketRequest =
    side === 'before' ? { before_seq: row.ticket.seq } : { after_seq: row.ticket.seq }
  if (stagedChange !== undefined) body.staged = stagedChange
  if (unparenting) body.parent_seq = null

  // **掴んだ行を抜いてから相手の位置を数え直す。** 抜く前の添字で挿入すると、
  // 下へ動かしたときだけ1つ手前に入る。
  const next = [...tickets.value]
  next.splice(from, 1)
  const to = next.findIndex((t) => t.seq === row.ticket.seq)
  if (to < 0) return
  const insertAt = side === 'before' ? to : to + 1

  // 位置も段も親も変わらないなら送らない。**`version` を無駄に上げない**
  // （`If-Match` を使う画面が 409 になる。9.4）
  if (insertAt === from && stagedChange === undefined && !unparenting) return

  // **ルート化するときは手元で並べ替えない。** 親子が変わると木の組み方と
  // インデントが動き、`has_children` も動く。**手元で組み替えるより、
  // サーバの答えを1回もらうほうが確かである**（`dropInto` と同じ判断）。
  if (unparenting) {
    await runMove(source, body, null, stagedChange, true)
    return
  }

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
  const sourceIsChild = seq !== null && rowIndex.value.get(seq)?.parentKey !== null
  endDrag()
  if (!canDropOnSection(seq, section)) return

  const source = rowIndex.value.get(seq!)?.ticket
  if (source === undefined || section.stage === undefined) return

  const stagedChange = stageChangeOf(source, section)
  const body: MoveTicketRequest = { position }
  if (stagedChange !== undefined) body.staged = stagedChange
  // **インデントされた行はルートにして、その段の先頭／末尾へ置く**（5.4。pb-70）。
  // 親と位置が `move` 1本で決まるので、見出しの線と着地が一致する。
  if (sourceIsChild) body.parent_seq = null

  // 段の中の先頭・末尾は手元で正しい位置を作れない——`position` は段の中で
  // 解釈される（9.4.1）のに `sort_key` は二段で1本だからで、取り直して合わせる。
  await runMove(source, body, null, stagedChange, sourceIsChild)
}

// ── 一覧から状態を変える（5.4「一覧で状態を変える」。pb-63）──────

/**
 * 行ごとの `StatusDropdown`（5.5 の部品をそのまま使う）。
 *
 * **開いているのは常に1つだけ**（`StatusDropdown` は外側を押すと閉じる）だが、
 * **どの行が開いたかは押されるまで分からない**ので、行ごとに参照を持つ。
 * `v-for` の中では関数 ref を使う——`useTemplateRef` は配列で返り、`seq` から
 * 引けない。
 *
 * **`onUnmounted` で消さなくてよい。** Vue は要素が外れるとき `null` を渡して
 * 呼び直すので、下の `setStatusRef` が自分で消す。
 */
const statusRefs = new Map<number, InstanceType<typeof StatusDropdown>>()

function setStatusRef(seq: number, el: Element | ComponentPublicInstance | null): void {
  if (el === null) statusRefs.delete(seq)
  else statusRefs.set(seq, el as InstanceType<typeof StatusDropdown>)
}

/**
 * **開いたときに引く**（`ApiDesign.md` 9.7）。一覧の応答には入っていない。
 *
 * **行ごとに引く。** 遷移できる先はチケットの現在地と担当で変わるので
 * （9.6 の検証6）、一覧を取ったときにまとめて引いても使い回せない。
 */
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

/**
 * 遷移させる（9.6）。**選んだ時点で送る**——確認は挟まない。
 *
 * 5.5 が pb-55 で確認モーダルを廃止しており（「状態変更は頻度が高く、毎回
 * ダイアログを挟むのは現実的でない」）、**一覧はさらに頻度が高い。**
 *
 * **応答をそのまま行へ差し替える。** `onDetailUpdated` と同じ判断で、
 * 行の中身が変わっただけなら取り直さない（pb-15）。**フィルタから外れる行が
 * 残ることはある**——`status=todo` で絞っている最中に進行中へ変えた場合で、
 * これは詳細ペインから変えたときと同じ振る舞いである。
 *
 * **結果の一言は出さない。** 変えた行の表示がその場で変わるので、
 * 操作したことは見えている（6.4「操作結果は操作した場所に出す」）。
 */
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

// ── 一覧から担当を変える（5.4「一覧で担当を選ぶ」。pb-64）────────

/**
 * 担当を差し替える（`ApiDesign.md` 9.5.2）。**選んだ時点で送る**——状態と同じで、
 * 確認は挟まない。
 *
 * **`If-Match` は行が持つ `version` を使う**（2.8）。一覧の行は `PATCH` の応答と
 * `move` の応答で更新されており、他人が同時に変えていれば 409 が返る——**黙って
 * 上書きしない。**
 *
 * **応答をその行へ差し替える**（`onDetailUpdated` と同じ判断。pb-15）。行の増減が
 * 起きないので取り直さない。**担当で絞り込み中に外れる行が残ることはある**が、
 * これは状態を変えたときと同じ振る舞いである。
 */
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

// ── 新規チケット（5.4.3）───────────────────────────────────

const showNewModal = ref(false)
const newDefaults = ref<NewTicketDefaults>({})
/** 新規エピックとして開いているか（5.4.3「新規エピック」。pb-14） */
const newEpicMode = ref(false)
const newFieldErrors = ref<Record<string, string>>({})

/**
 * 親チケットの選択肢（5.4.3）。**いま一覧に出ているチケット**から選ぶ。
 *
 * **エピックで絞り込み中は「そのエピック配下かつ未完了」に絞る**——絞り込んで
 * 作業しているときに、視野の外のチケットを親に選べても選ぶ理由がない。
 * **エピックは入れない**——エピック欄で選ぶ（5.4.3「親チケットとエピック」。pb-14）。
 * 以前は、選択中のエピック自身をここへ足していた。
 */
const parentCandidates = computed<Ticket[]>(() => {
  if (epicSeqs.value.length === 0) return tickets.value
  return tickets.value.filter((t) => t.closed_at === null)
})

/**
 * グループ化中にセクション内から作成した場合、**その軸の値を初期値に入れる**（5.4.3）。
 *
 * 状態の軸だけは初期値を持たない——ワークフローの入口はサーバが決めるため、
 * モーダルに状態の欄そのものが無い（9.3）。
 *
 * **エピックを1つだけ選んでいるときは、そのエピックがエピック欄の初期値になる**（5.4）。
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
    if (group.value === 'parent') {
      // エピックのセクションなら、エピック欄に入れる（親チケット欄の候補にエピックは無い。5.4.3）
      const seq = Number(sectionKey)
      if (epicSeqSet.value.has(seq)) defaults.epic_seq = seq
      else defaults.parent_seq = seq
    }
    if (group.value === 'tag') defaults.tag_ids = [sectionKey]
    // **スプリントの軸だけ初期値を持たない**（pb-6）。9.3 が `sprint_id` を
    // 受け付けなくなったためで、所属はスプリントを開始したときに決まる。
    // 状態の軸が初期値を持たないのと同じ形である。
    if (group.value === 'assignee') defaults.assignee_id = sectionKey
  }
  if (
    defaults.parent_seq === undefined &&
    defaults.epic_seq === undefined &&
    epicSeqs.value.length === 1
  ) {
    defaults.epic_seq = epicSeqs.value[0]
  }
  newEpicMode.value = false
  newDefaults.value = defaults
  newFieldErrors.value = {}
  result.value = ''
  showNewModal.value = true
}

/**
 * 新規エピック（5.4.3「新規エピック」。pb-14）。`エピック[…]` のパネルから開く。
 *
 * **同じモーダルを種別エピックに固定して使う。** 初期値は持たない——絞り込み中の
 * エピックを親に入れると、エピックの入れ子ができる。
 */
function openNewEpicModal(): void {
  newEpicMode.value = true
  newDefaults.value = {}
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
    // **エピックは行に出ない**ので、取り直すのは語彙のほう（5.4.3「新規エピック」）
    if (created.type === 'epic') await reloadEpics()
    else await loadTickets()
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

// ── スプリントの運用（5.4「スプリントを開始・終了する」。pb-6）──────

/**
 * スプリントを始める（`ApiDesign.md` 9.12.1）。
 *
 * **対象を送らない。** 「オンステージ段に出ている行」の判定は `staged_at` だけ
 * では決まらない（子は親と一緒に運ばれる）ので、サーバが部分木ごと取る。
 *
 * **成功したら語彙と一覧の両方を取り直す。** 語彙は見出しに出す進行中の
 * スプリントのため、一覧はチケットの `sprint` 欄が変わったためである。
 */
async function runStartSprint(body: StartSprintRequest): Promise<void> {
  busy.value = true
  sprintStartErrors.value = {}
  try {
    const sprint = await sprintsApi.startSprint(projectKey.value, body)
    showSprintStart.value = false
    await Promise.all([loadVocabulary(), loadTickets()])
    result.value = `✓ スプリント「${sprint.name}」を開始しました（対象 ${sprint.ticket_count} 件）`
  } catch (e) {
    const err = toApiError(e)
    if (err.status === 422) {
      const fields: Record<string, string> = {}
      for (const d of err.details) fields[d.field] = d.message
      sprintStartErrors.value = fields
    } else {
      // 409（進行中が既にある）と 403 はモーダルを閉じて画面上部へ出す。
      // **409 は他の人が同時に始めたときに来る**——画面はボタンを出さない
      // ことで先に防いでいるが、手元の語彙が古ければすり抜ける。
      showSprintStart.value = false
      error.value = err
      if (err.status === 409) await loadVocabulary()
    }
  } finally {
    busy.value = false
  }
}

/**
 * スプリントを終える（`ApiDesign.md` 9.12.2）。
 *
 * **終わったら一覧を引き直す。** 外れる行と残る行が同時に決まるので、
 * 手元で差分を当てずにサーバの答えを採る（5.4「終了の確認」）。
 */
async function runFinishSprint(): Promise<void> {
  const sprint = activeSprint.value
  if (!sprint) return
  busy.value = true
  try {
    const done = await sprintsApi.finishSprint(projectKey.value, sprint.id)
    confirmSprintFinish.value = false
    await Promise.all([loadVocabulary(), loadTickets()])
    result.value = `✓ スプリント「${done.name}」を終了しました（完了 ${done.closed_count} / ${done.ticket_count} 件）`
  } catch (e) {
    confirmSprintFinish.value = false
    error.value = toApiError(e)
    await loadVocabulary()
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
//
// **`fullPath` を見てはいけない。** 詳細ペインを開くとパスが
// `/p/:key/tickets/:seq` へ変わるので、**行をクリックするたびに一覧を
// 取り直すことになる**（5.4「戻したときに保つもの」が保つと定めている
// スクロール位置と選択が、そのたびに作り直される）。見るのはクエリだけでよい。
//
// **`route.query` そのものを見てもいけない。** navigation のたびに新しい
// オブジェクトが作られるので、参照で比べる `watch` は中身が同じでも発火する
// ——`fullPath` を見るのと結果が変わらない。**正規化した文字列**にして比べる。
const queryKey = computed(() =>
  Object.entries(route.query)
    .filter((e): e is [string, string] => typeof e[1] === 'string')
    .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
    .map(([k, v]) => `${k}=${v}`)
    .join('&'),
)

watch(queryKey, () => {
  if (route.params.key === undefined) return
  loadCollapsed()
  void loadTickets()
})

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
  <!-- 一覧と詳細のマスター・ディテール（2.2.1）。**押し込む形で、重ねない。**
       並ぶかどうかは窓の幅ではなく**コンテンツペインの幅**で決まる（2.4） -->
  <SplitPane :open="shrunk" storage-key="pb.detail_pane_w" class="page">
    <template #primary>
      <!-- **縮小中はこのヘッダの左側全体が「全幅へ戻す」の当たり所になる**
           （5.4「一覧へ戻る導線」）。新しい帯を差し込まず、既にある 48px を使う
           ——足すと行の縦位置がずれる。**全幅のときはボタンにしない**
           （押しても何も起きないものをボタンに見せない） -->
      <PageHeader
        title="バックログ"
        :title-action-label="shrunk ? 'バックログを全幅に戻す' : undefined"
        :title-action-icon="shrunk ? '⤢' : undefined"
        @title-click="closeDetail"
      >
        <template #actions>
          <!-- 縮小中はラベルが入らないのでアイコンのみにし、`aria-label` を付ける（9.2） -->
          <button
            v-if="canCreate"
            type="button"
            class="primary"
            :class="{ 'icon-only': shrunk }"
            :aria-label="shrunk ? '新規チケット' : undefined"
            :title="shrunk ? '新規チケット' : undefined"
            @click="openNewModal()"
          >
            {{ shrunk ? '+' : '+ 新規チケット' }}
          </button>
        </template>
      </PageHeader>

    <div class="page-body" :class="{ shrunk }">
      <!-- 縮小中はフィルタ行を1行に畳む（5.4「詳細を開いているときの一覧」）。
           押すとその場で開く -->
      <button
        v-if="shrunk"
        type="button"
        class="filters-toggle"
        :aria-expanded="filtersOpen"
        @click="filtersOpen = !filtersOpen"
      >
        絞り込み
        <span class="caret" aria-hidden="true">{{ filtersOpen ? '▾' : '▸' }}</span>
        <span v-if="!isPristine" class="filters-dot" aria-label="絞り込み中">●</span>
      </button>

      <!-- フィルタ行（5.4）。条件は URL のクエリに載る -->
      <div v-if="!shrunk || filtersOpen" class="filters">
        <!-- 状態（5.4「状態と期限のフィルタ」。手順19b）。
             **1つの箱で3系列を出し入れする**——進み具合（`open` / `stale`）・
             区分（`status_category`）・ステータス（`status`）。同じ軸の値なので
             箱を分けず、`optgroup` の見出しで区別する。**見出しが無いと、
             `simple` / `with_review` で区分とステータスが同じ語になって読めない** -->
        <label class="filter">
          <span class="filter-label">状態</span>
          <select
            :value="stateValue"
            @change="setExclusive(STATE_KEYS, ($event.target as HTMLSelectElement).value)"
          >
            <option value="">すべて</option>
            <optgroup label="進み具合">
              <option value="open:true">未完了</option>
              <option value="open:false">完了</option>
              <option :value="`stale:${staleDays}d`">{{ staleDays }}日以上更新なし</option>
            </optgroup>
            <optgroup label="区分">
              <option
                v-for="c in statusCategoryOrder"
                :key="c"
                :value="`status_category:${c}`"
              >
                {{ statusCategoryLabels[c] }}
              </option>
            </optgroup>
            <optgroup label="ステータス">
              <option v-for="s in statuses" :key="s.key" :value="`status:${s.key}`">
                {{ s.name }}
              </option>
            </optgroup>
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

        <!-- 期限（5.4「状態と期限のフィルタ」。手順19b）。
             **「期限超過」は `due_within=0d` ではない**——あちらは「今日以前」で
             今日が期限のものを含み、`stats.overdue` と1日ぶんずれる（9.2.1） -->
        <label class="filter">
          <span class="filter-label">期限</span>
          <select
            :value="dueValue"
            @change="setExclusive(DUE_KEYS, ($event.target as HTMLSelectElement).value)"
          >
            <option value="">すべて</option>
            <option value="overdue:true">期限超過</option>
            <option value="due_within:0d">今日まで</option>
            <option value="due_within:7d">7日以内</option>
            <option value="due_within:30d">30日以内</option>
          </select>
        </label>

        <!-- エピックだけは複数選択（5.4）。URL 上の実体は `parent` である -->
        <div class="filter">
          <span class="filter-label" aria-hidden="true">エピック</span>
          <EpicFilter
            :epics="epics"
            :selected="epicSeqs"
            :project-key="projectKey"
            :can-create="canCreate"
            @update="setQuery({ parent: $event.join(',') })"
            @create="openNewEpicModal"
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
            <td v-for="c in shrunk ? 3 : 7" :key="c"><span class="skeleton"></span></td>
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

            <!-- スプリントの運用（5.4「スプリントを開始・終了する」。pb-6）。
                 **オンステージ段の見出しにだけ出す**——対象がオンステージに
                 載っているもの全部だからで、別の画面で選び直させると同じ
                 集合を2回作ることになる。
                 **進行中があるときは開始のボタンを出さない**（押せない
                 ボタンを出すより、状態が1つに見えるほうがよい）。
                 **縮小中は畳む**——450px では段の名前が「オンステ…」と切れる
                 （実機で判明）。フィルタ行を畳み、列を3つに落とすのと同じ
                 方針で、一覧に残すのは「次にどれを開くか」に要るものだけ -->
            <template v-if="canRunSprint && section.stage === true && !shrunk">
              <span v-if="activeSprint" class="sprint-active">
                <span class="sprint-name">{{ activeSprint.name }}</span>
                <span v-if="activeSprintPeriod" class="sprint-period">
                  {{ activeSprintPeriod }}
                </span>
              </span>
              <button
                v-if="activeSprint"
                type="button"
                class="secondary small"
                :disabled="busy"
                @click="confirmSprintFinish = true"
              >
                スプリントを終了
              </button>
              <button
                v-else
                type="button"
                class="secondary small"
                :disabled="busy"
                @click="showSprintStart = true"
              >
                スプリントを開始
              </button>
            </template>
          </div>

          <template v-if="!collapsed.has(section.key)">
            <table v-if="section.rows.length > 0" class="table">
              <thead>
                <tr>
                  <!-- 縮小中は `⠿` 列を出さない（5.4「詳細を開いているときの一覧」）
                       ——幅が無く、詳細を見ている間の操作でもない -->
                  <th
                    v-if="!shrunk"
                    scope="col"
                    class="grip-col"
                    :aria-sort="ariaSort('sort_key')"
                  >
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
                  <!-- **`table-layout: fixed` は先頭行のセルで列幅が決まる。**
                       縮小中の追加幅は `<td>` ではなくここへ書かないと効かない -->
                  <th
                    scope="col"
                    class="id-col"
                    :class="{ 'with-gutter': shrunk }"
                    :aria-sort="ariaSort('seq')"
                  >
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
                  <!-- 優先・担当・期限は縮小中に落とす（5.4）。**いずれも詳細側に
                       出ているもの**で、残すのは「次にどれを開くか」を決めるのに
                       要るものだけでよい -->
                  <th
                    v-if="!shrunk"
                    scope="col"
                    class="priority-col"
                    :aria-sort="ariaSort('priority')"
                  >
                    <button type="button" class="sort" @click="sortBy('priority')">
                      優先
                      <span class="caret" aria-hidden="true">{{
                        sort === 'priority' ? (order === 'asc' ? '▴' : '▾') : ''
                      }}</span>
                    </button>
                  </th>
                  <!-- 担当だけソートできない（`ApiDesign.md` 9.2.1 の sort に無い） -->
                  <th v-if="!shrunk" scope="col" class="assignee-col">担当</th>
                  <th v-if="!shrunk" scope="col" class="due-col" :aria-sort="ariaSort('due_date')">
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
                  :draggable="grabWholeRow && !overActionCell"
                  @dragstart="grabWholeRow && !overActionCell && (draggingSeq = row.ticket.seq)"
                  @dragend="endDrag()"
                  :class="{
                    grabbable: grabWholeRow,
                    dragging: draggingSeq === row.ticket.seq,
                    'drop-before': hintsRow(row, section, 'before'),
                    'drop-after': hintsRow(row, section, 'after'),
                    'drop-inside': hintsRow(row, section, 'inside'),
                    selected: detailSeq === row.ticket.seq,
                  }"
                  :aria-selected="detailSeq === row.ticket.seq"
                  @click="openRow(row.ticket, $event)"
                  @dragover="onDragOverRow($event, row, section)"
                  @drop.prevent="dropOnRow($event, row, section)"
                >
                  <td v-if="!shrunk" class="grip-col">
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

                  <!-- **完全形で出す**（5.4「ID列」）。`-31` は負の数に見える。
                       **縮小中は折りたたみと種別アイコンをこのセルへ寄せる**
                       ——落ちるのは `⠿`（並べ替え）の列であって、ツリーの開閉と
                       種別の区別まで失うと 5.4 の「階層表示」が効かなくなる -->
                  <td class="id-col" :class="{ 'with-gutter': shrunk }">
                    <span v-if="shrunk" class="grip-line">
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
                    <code class="seq">{{ fullId(row.ticket) }}</code>
                  </td>

                  <td class="title-col">
                    <span class="title-line" :style="{ paddingLeft: `${row.depth * 20}px` }">
                      <span v-if="row.depth > 0" class="branch" aria-hidden="true">└</span>
                      <!-- **クエリを持ち回る**（3.2）。ここだけ落とすと、
                           タイトルを押したときにフィルタが消えて、行の
                           どこを押したかで結果が変わる -->
                      <RouterLink
                        v-slot="{ href, navigate }"
                        :to="withQuery(`/p/${projectKey}/tickets/${row.ticket.seq}`)"
                        custom
                      >
                        <!-- **`<a>` は既定でドラッグ可能**なので落とす（5.4 / 5.10）。
                             放置すると掴んだ瞬間に URL のドラッグが始まり、
                             行のドラッグが一度も起きない -->
                        <a
                          ref="rowLink"
                          class="title"
                          :href="href"
                          draggable="false"
                          @click.stop="navigate"
                        >
                          {{ row.ticket.title }}
                        </a>
                      </RouterLink>
                      <!-- **縮小中の期限超過は `⚠` だけをタイトルの後ろに出す**
                           （5.4「詳細を開いているときの一覧」）。日付は詳細側に出ている -->
                      <span
                        v-if="shrunk && isOverdue(row.ticket)"
                        class="overdue"
                        :title="`期限超過（${row.ticket.due_date}）`"
                        >⚠</span
                      >
                      <!-- タグは枠線＋文字（8.6）。色は使わない。
                           **縮小中は出さない**（5.4「詳細を開いているときの一覧」）
                           ——450px ではタイトルが省略記号で切れたうえにタグが
                           枠の途中で切れ、**読めないのに在る**状態になる（2.2.1 が
                           「重ねる」案を却下したのと同じ理由）。タグは詳細側に出ている -->
                      <span
                        v-for="tag in shrunk ? [] : row.ticket.tags"
                        :key="tag.id"
                        class="tag"
                        >{{ tag.name }}</span
                      >
                    </span>
                  </td>

                  <!-- **一覧から状態を変えられる**（5.4「一覧で状態を変える」。pb-63）。
                       5.5 と同じ `StatusDropdown` を `dense` で置く——遷移できない先も
                       理由つきで出る規則（9.7）ごと共有される。**`ticket.transition` を
                       持たないときは部品側が押せないボタンにする**ので、出し分けを
                       ここに書かない -->
                  <!-- **`@click.stop` が要る。** `<tr>` の `openRow` が同時に走ると
                       詳細ペインが開き、**一覧が 450px へ縮んでパネルだけ元の位置に
                       取り残される**（実機で判明）。状態セルは状態の操作に使う場所
                       であって、詳細を開く場所ではない -->
                  <td
                    class="status-col"
                    @click.stop
                    @mouseenter="overActionCell = true"
                    @mouseleave="overActionCell = false"
                  >
                    <StatusDropdown
                      :ref="(el) => setStatusRef(row.ticket.seq, el)"
                      dense
                      :current="row.ticket.status"
                      :can-transition="canTransition"
                      :busy="busy"
                      @open="loadRowTransitions(row.ticket.seq)"
                      @select="transitionRow(row.ticket, $event)"
                    />
                  </td>

                  <!-- 優先度は色を使わず記号のみ。中は無表示（8.7） -->
                  <td v-if="!shrunk" class="priority-col">
                    <span
                      v-if="row.ticket.priority"
                      class="priority"
                      :title="priorityLabels[row.ticket.priority]"
                      >{{ priorityMarks[row.ticket.priority] }}</span
                    >
                  </td>

                  <!-- 担当と実行者を1つのセルに入れる（5.4「実行者を担当と同じ
                       セルに置く」。手順26b）。**中を flex にする**——担当の名前を
                       縮ませ、実行者の 🤖 は縮ませないためで、素の text node の
                       ままだと 🤖 がセルの外へ押し出されて消える（実機で判明） -->
                  <td
                    v-if="!shrunk"
                    class="assignee-col"
                    @click.stop
                    @mouseenter="overActionCell = true"
                    @mouseleave="overActionCell = false"
                  >
                    <span class="assignee-cell">
                      <!-- **一覧から担当を選べる**（5.4「一覧で担当を選ぶ」。pb-64）。
                           `ticket.assign` を持たないときは押せない表示になるので、
                           出し分けをここに書かない -->
                      <AssigneePicker
                        :current="row.ticket.assignee"
                        :members="members"
                        :can-assign="canAssign"
                        :busy="busy"
                        @select="assignRow(row.ticket, $event)"
                      />
                      <!-- **一覧では 🤖 だけを出す**（名前は詳細ペインが出す）。
                           130px の列に名前2つは入らない -->
                      <span
                        v-if="row.ticket.working_agent"
                        class="working-agent"
                        :title="`${row.ticket.working_agent.display_name} が処理しています`"
                        >🤖</span
                      >
                    </span>
                  </td>

                  <td v-if="!shrunk" class="due-col">
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
             **上下の件数の合計と一致する**——伏せる行が無くなったため
             （5.4「配下の行き先」。pb-46） -->
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
        :project-key="projectKey"
        :members="members"
        :tags="tags"
        :candidates="parentCandidates"
        :epics="epics"
        :epic-mode="newEpicMode"
        :defaults="newDefaults"
        :busy="busy"
        :field-errors="newFieldErrors"
        @close="showNewModal = false"
        @save="createTicket"
      />

      <!-- スプリントの開始（5.4「開始のダイアログ」。pb-6） -->
      <SprintStartModal
        v-if="showSprintStart"
        :onstage-count="onstageCount"
        :busy="busy"
        :field-errors="sprintStartErrors"
        @close="showSprintStart = false"
        @start="runStartSprint"
      />

      <!-- スプリントの終了（5.4「終了の確認」）。**「外れます」と書き、
           「削除されます」と書かない**——チケットは消えず、状態フィルタで
           完了を選べば戻ってくる。取り返しがつかないと読める言葉を、
           取り返しのつく操作に使わない（6.3）。だから `danger` も付けない -->
      <ConfirmDialog
        v-if="confirmSprintFinish && activeSprint"
        title="スプリントを終了しますか？"
        :message="`${activeSprint.name} を終了します。対象の ${finishPreview.total} 件のうち、完了しているのは ${finishPreview.closed} 件です。完了したものは一覧から外れ、未完了の ${finishPreview.remaining} 件はオンステージに残ります。`"
        confirm-label="終了する"
        :busy="busy"
        @cancel="confirmSprintFinish = false"
        @confirm="runFinishSprint"
      />
    </template>

    <!-- チケット詳細（5.5）。**`seq` が変わっても再マウントしない**——
         同じペインが差し替わるだけで、一覧はそのまま残る（2.2.1） -->
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
.page {
  height: 100%;
}

.page-body {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: var(--pb-space-6);
}

/* ── 縮小中（詳細ペインを開いているとき。5.4）───────────── */

/* 畳んだフィルタ行（`[絞り込み ▾]` の1行）。**新しい帯は足していない**
   ——フィルタ行そのものを1行に置き換えている */
.filters-toggle {
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

.filters-toggle:hover {
  background: var(--pb-hover);
}

/* 畳んでいても「絞り込みが効いている」ことは読めるようにする。
   **色ではなく点の有無で示す**（8.6） */
.filters-dot {
  color: var(--pb-text-muted);
  font-size: 10px;
}

/* 縮小中は左右の余白を詰める。450px のうち 48px を余白に使うと、
   タイトル列の取り分がそのぶん減る（3列の合計は変わらないので、
   削れるのはタイトルだけである） */
.page-body.shrunk {
  padding: var(--pb-space-4) var(--pb-space-3);
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

/* **行そのものが掴みしろである**（5.4「掴みしろ」。pb-7 → pb-71 で全幅にも広げた）。
   **行クリックで詳細が開く**ことは変わらないので `pointer` を上書きしない
   ——`grab` は「掴める」を足すのであって、「押せない」を意味しない */
.row.grabbable {
  cursor: grab;
}

.row.grabbable:active {
  cursor: grabbing;
}

/* **開いている行は選択状態として背景を変える**（5.4「行クリック」）。
   色ではなく面の輝度で示す（8.2 / 8.6）。hover より一段強くして、
   マウスが別の行に載っていても「いま開いているのはこれ」が読めるようにする */
.row.selected,
.row.selected:hover {
  background: var(--pb-active);
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

/* **中央 1/2 は面で塗る**（5.4「ドロップ先の見せ方」。pb-16）。
   線が「行と行の**間**」、面が「行**そのもの**」を指す。
   **線と並べたときに一目で別物と読める強さ**にする——2px の線と淡い背景色
   では、どちらに入るのかが手元で判別できない（5.10 と同じ判断） */
.row.drop-inside td {
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.row.drop-inside td :is(a, code, .seq, .title, .muted, .branch, .type-icon, .overdue) {
  color: var(--pb-on-accent);
}

/* **面の上ではバッジの地を抜く。** 状態・優先・タグは自前の背景と枠を持って
   いるので、面を敷いただけでは**白地のバッジが面に溶けて読めなくなる**
   （スクリーンショットで判明）。枠と文字だけを残して面に載せる */
.row.drop-inside td :is(.status, .tag, .priority) {
  background: transparent;
  border-color: var(--pb-on-accent);
  color: var(--pb-on-accent);
}

.row.drop-inside td .status-mark {
  color: var(--pb-on-accent);
}

/* 見出しは「その段の先頭へ」なので、線は見出しの下端に引く */
.section-head.drop-first {
  box-shadow: inset 0 -2px 0 0 var(--pb-accent);
}

/* 進行中のスプリント（5.4「スプリントを開始・終了する」。pb-6）。
   **見出しの1行に収める**——名前が長いときは名前のほうを省略し、
   期間は縮ませない（日付が切れると読めない）。 */
.sprint-active {
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-2);
  min-width: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.sprint-name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: var(--pb-text);
}

.sprint-period {
  flex: none;
  font-variant-numeric: tabular-nums;
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

/* 縮小中の `[+ 新規チケット]` はアイコンのみにする（5.4「一覧へ戻る導線」）。
   ラベルが入らない幅なので、`aria-label` を付けて形だけ残す（9.2） */
.primary.icon-only {
  width: 28px;
  padding: 0;
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

/* 縮小中はここへ折りたたみと種別アイコンが入る（`⠿` 列を落とすため）。
   `▾` 16px ＋ 種別 16px ＋ すきま 4px を足した幅にする */
.id-col.with-gutter {
  width: 152px;
}

.id-col.with-gutter .grip-line {
  margin-right: var(--pb-space-1);
}

/* 縮小中の期限超過は `⚠` だけを出す（5.4）。日付は詳細側にある */
.title-line .overdue {
  flex: none;
}

.status-col {
  width: 104px;
}

.priority-col {
  width: 56px;
}

/* `👤 開発メンバー` が 1440px で切れない幅。**狭い窓ではタイトルを優先する**が、
   それは `.title` の下限（`min-width`）が担うので、ここは固定値のままにする
   ——メディアクエリを1つ足すより、下限を1か所に置くほうが読める。

   **実行者（🤖）も同じセルに入る**（5.4。手順26b）。列を足さないのは、8列が既に
   横幅の上限であることと、実行者を読みたい場面が「担当は誰か」を読む場面と
   同じだからである */
/* **`▾` のぶん 16px 広げた**（5.4「一覧で担当を選ぶ」。pb-64）。130px は
   `👤開発メンバー` でほぼ埋まる幅で、キャレットを足すと名前が省略記号で切れる
   ——**押せることを示す記号のために、読みたい情報を削らない。**
   広げたぶんはタイトル列（可変）から取る */
.assignee-col {
  width: 146px;
}

/* 担当と実行者を1行に収める。**縮むのは担当の名前だけで、🤖 は縮まない**
   （`flex: none`）——「誰か」より先に「エージェントが入っている」が読めればよく、
   名前の全文は詳細ペイン（5.5）が出す。

   **素の text node のままでは 🤖 がセルの外へ押し出されて消えた**（実機で判明）。
   セルに overflow があるため、はみ出した要素は幅を持ったまま見えなくなる
   ——`getBoundingClientRect()` が 0 にならないので、実測では気づけない */
.assignee-cell {
  display: flex;
  align-items: baseline;
  gap: 4px;
  min-width: 0;
}

.working-agent {
  flex: none;
  color: var(--pb-text-muted);
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

/* ステータスのバッジは `StatusDropdown` が持つ（5.4「一覧で状態を変える」。pb-63）。
   **輝度差＋記号で表す規則（8.7）ごとあちらへ移した**——同じ見た目を2か所に
   置くと、片方だけ直る。ここに残っていた `.status` 系は使い手を失ったので消した */

/* 担当の名前と種別の記号は `AssigneePicker` が持つ（5.4「一覧で担当を選ぶ」。
   pb-64）。ここに残っていた `.assignee-name` / `.actor-mark` は使い手を失った */
.priority {
  color: var(--pb-text-muted);
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
