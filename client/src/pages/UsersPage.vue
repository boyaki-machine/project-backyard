<script setup lang="ts">
/**
 * アカウント / 権限管理（`GuiDesign.md` 5.6）。必要権限は `user.manage`。
 *
 * **人間とエージェントを同じ一覧に並べる**（`DbDesign.md` 6.2 の `actor` 統合設計）。
 * 種別で意味を持たない列は「—」で埋め、行の形は変えない。
 *
 * タブは「ユーザー」「ロールと権限」の2つ。**タブはURLを持たない**（3.2 の
 * ルーティング表が持つのは `/admin/users` の1行だけである）。
 * 「ロールと権限」の中身は `GET /roles` / `GET /permissions`（手順14）が要るため、
 * いまは 6.5 と同じ体裁で予定を出す。
 *
 * 一覧の状態はこの画面だけが使うので、ストアを増やさずここに持つ（7.1）。
 *
 * **操作の結果はトーストではなく、操作した場所に出す**（6.4）。
 */
import { computed, onMounted, onUnmounted, ref, useTemplateRef, watch } from 'vue'
import { useRouter } from 'vue-router'

import AddUserModal from '../components/AddUserModal.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DeleteUserDialog from '../components/DeleteUserDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import GeneratedPasswordDialog from '../components/GeneratedPasswordDialog.vue'
import PageHeader from '../components/PageHeader.vue'
import UserActionsMenu from '../components/UserActionsMenu.vue'
import type { ActionItem } from '../components/UserActionsMenu.vue'
import { ApiError } from '../api/client'
import * as usersApi from '../api/users'
import type {
  CreatedUser,
  SortOrder,
  UserActiveFilter,
  UserListItem,
  UserSort,
} from '../api/users'
import { formatDate, formatDateTime } from '../lib/datetime'
import RolePermissionMatrix from '../components/RolePermissionMatrix.vue'
import { useRolesStore } from '../stores/roles'
import { useAuthStore } from '../stores/auth'

/**
 * 検索の debounce。`ApiDesign.md` 5.2 の `check-key` と同じ値にそろえる。
 * アプリの中に「待つ長さ」の数字を2つ作らないため。
 */
const SEARCH_DEBOUNCE_MS = 400

/** 1ページの件数。`ApiDesign.md` 2.6 / 6.1 のサーバ既定と同じ値を明示する */
const PER_PAGE = 25

const router = useRouter()
const auth = useAuthStore()

/**
 * ロールの表示名（`GuiDesign.md` 5.6）。**画面は対応表を持たない**——
 * 正本は `DbDesign.md` 7.3 のシードで、`GET /roles` から取る（手順14 で
 * `lib/roles.ts` を廃止した）。
 *
 * この画面は `user.manage` を要するので全件（`all`）を読める。読み込みが
 * 終わるまではキーがそのまま出る。
 */
const rolesStore = useRolesStore()

type Tab = 'users' | 'agents' | 'roles'
const tab = ref<Tab>('users')

// ── 一覧の状態（4状態は 6.2 の規約）───────────────────────────
const items = ref<UserListItem[]>([])
const page = ref(1)
const perPage = ref(PER_PAGE)
const total = ref(0)
const totalPages = ref(0)
const loading = ref(false)
const loaded = ref(false)
const error = ref<ApiError | null>(null)

/** 既定は表示名の昇順（`ApiDesign.md` 6.1）。**名簿は昇順で読む**ため一覧の中で唯一 asc */
const sort = ref<UserSort>('display_name')
const order = ref<SortOrder>('asc')

// ── 検索と絞り込み（5.6。件数によらず常時表示する）──────────────
const q = ref('')
const isActive = ref<UserActiveFilter>('all')

/** サーバへ送っている検索語。debounce の途中は `q` と食い違う */
const appliedQ = ref('')

const filtered = computed(() => appliedQ.value !== '' || isActive.value !== 'all')

// ── 追加（5.6.1）──────────────────────────────────────────────
const addOpen = ref(false)
/** 生成パスワードの1回表示。`manual` では `generated_password` が null で出さない */
const created = ref<CreatedUser | null>(null)
/**
 * 6.4「結果は操作した場所に出す」。表の直上に残す。
 *
 * 追加だけでなく `[⋯]` の操作の結果もここへ出す。**同じ場所に2つ目の通知欄を
 * 作らない。** 詳細画面で削除したときの結果もここへ着地する（画面が消えるため、
 * 操作した場所ではなく**次に着地する場所**へ渡す）。
 */
const notice = ref<string | null>(null)

/**
 * 応答の追い越しを防ぐ通し番号。
 *
 * 検索を打ちながら列ヘッダを押すと複数のリクエストが並ぶ。遅れて届いた
 * 古い応答で新しい結果を上書きすると、画面の条件と中身がずれる。
 */
let seq = 0

async function fetchUsers(): Promise<void> {
  const mine = ++seq
  loading.value = true
  error.value = null
  try {
    const res = await usersApi.listUsers({
      // **タブが種別を決める**（5.6）。エージェントは別タブで、実装は Phase 2。
      // Phase 1 にエージェントは1件も存在しないが、`all` ではなく `user` を送るのは
      // タブの意味と一致させるためで、Phase 2 で増えても混ざらない。
      kind: 'user',
      is_active: isActive.value,
      q: appliedQ.value === '' ? undefined : appliedQ.value,
      sort: sort.value,
      order: order.value,
      page: page.value,
      per_page: perPage.value,
    })
    if (mine !== seq) return
    items.value = res.items
    page.value = res.page
    perPage.value = res.per_page
    total.value = res.total
    totalPages.value = res.total_pages
    loaded.value = true
  } catch (e: unknown) {
    if (mine !== seq) return
    error.value =
      e instanceof ApiError
        ? e
        : new ApiError({
            status: 0,
            code: 'internal_error',
            message: '予期しないエラーが発生しました',
          })
    // 古い一覧を新しい条件の結果として見せない（6.2）
    items.value = []
  } finally {
    if (mine === seq) loading.value = false
  }
}

/** 条件を変えたら必ず1ページ目へ戻す。3ページ目のまま絞ると空に見える */
function reload(): void {
  page.value = 1
  void fetchUsers()
}

let timer: ReturnType<typeof setTimeout> | undefined

watch(q, (next) => {
  clearTimeout(timer)
  timer = setTimeout(() => {
    if (appliedQ.value === next.trim()) return
    appliedQ.value = next.trim()
    reload()
  }, SEARCH_DEBOUNCE_MS)
})

watch(isActive, () => reload())

function clearFilters(): void {
  clearTimeout(timer)
  q.value = ''
  appliedQ.value = ''
  isActive.value = 'all'
  reload()
}

// ── 列とソート ────────────────────────────────────────────────
//
// 種別以外のすべての列で並べ替えられる（`ApiDesign.md` 6.1）。ロールと状態は
// 手順12c で加わった。**並びの意味づけはサーバが持つ**——ロールは
// `role.sort_order` の順、状態は昇順で無効が先である。
//
// **すべての列が既定幅と下限を持つ**（5.6）。「余りを1列に渡す」作りにすると、
// 窓が狭いときにその列だけが 0 まで潰れる（実際そうなっていた。窓 900px で
// メールが1文字も見えなかった）。表の幅は幅の合計で、領域を超えたら横スクロールする。
interface Column {
  key: string
  label: string
  sort?: UserSort
  className?: string
  /** 既定の幅。読み直すとここへ戻る（保存しない） */
  width: number
  /** ドラッグで縮められる下限。ここを割ると内容が読めなくなる */
  min: number
  /**
   * 幅調整の対象外にする（手順13b）。
   *
   * **操作列（`[⋯]`）だけが該当する。** 内容の長さに依存しないので広げる動機が
   * なく、可変にすると窓を広げるたびにメニューの周りだけ空きが増える。
   * 5.6 の4つの規則は、この列を除いた並びの上でそのまま成り立つ——余りを
   * 受け取るのは変わらず「作成」である。
   */
  fixed?: boolean
}

const COLUMNS: Column[] = [
  { key: 'kind', label: '', className: 'kind', width: 40, min: 32 },
  { key: 'name', label: '名前', sort: 'display_name', className: 'name-col', width: 240, min: 100 },
  { key: 'email', label: 'メール', sort: 'email', className: 'email', width: 260, min: 100 },
  { key: 'role', label: 'ロール', sort: 'system_role', className: 'role', width: 150, min: 90 },
  { key: 'status', label: '状態', sort: 'is_active', className: 'status', width: 70, min: 56 },
  { key: 'last', label: '最終ログイン', sort: 'last_login_at', className: 'datetime', width: 150, min: 110 },
  { key: 'created', label: '作成', sort: 'created_at', className: 'date', width: 110, min: 90 },
  // `⋯` の記号は暫定の表示（利用者の判断、2026-08-21。5.6 のワイヤーの `[⋯]` は
  // メニューアイコンのプレースホルダである）。見出しの文字は持たない
  { key: 'actions', label: '', className: 'actions-col', width: 44, min: 44, fixed: true },
]

/** 幅調整に参加する列。**操作列を除いた並び**が 5.6 の規則の対象になる */
const FLEX_COLUMNS = COLUMNS.filter((c) => c.fixed !== true)

/** 余りを受け取る列（5.6）。操作列を足した後も「作成」のままである */
const LAST_FLEX = FLEX_COLUMNS[FLEX_COLUMNS.length - 1]!

/**
 * つまみを出す列。
 *
 * **右隣と融通する**ので、右隣が無い列と、右隣が幅調整の対象外である列には
 * 出さない（5.6「最後の列につまみは無い」）。
 */
function resizable(col: Column): boolean {
  const i = FLEX_COLUMNS.findIndex((c) => c.key === col.key)
  return i >= 0 && i < FLEX_COLUMNS.length - 1
}

/**
 * 列ごとの現在の幅。**保存しない**（`GuiDesign.md` 5.6）。
 *
 * 読み直せば既定へ戻り、それが幅を戻す手段でもある。専用のリセット操作は
 * 置かない（原則3）。
 */
const widths = ref<Record<string, number>>(
  Object.fromEntries(COLUMNS.map((c) => [c.key, c.width])),
)

/** 表の幅は列幅の合計。領域より広ければ、表だけが横スクロールする */
const tableWidth = computed(() =>
  COLUMNS.reduce((sum, c) => sum + (widths.value[c.key] ?? c.width), 0),
)

/**
 * 表示領域の幅に総幅を合わせる（`GuiDesign.md` 5.6）。
 *
 * **余りは最後の列が受け取る**（表の右に空きを作らない）。狭くなったぶんも
 * 最後の列から削り、下限に達したらそれ以上は削らない——そのときだけ総幅が
 * 表示領域を超え、表が横スクロールする。
 *
 * 1px 未満のずれでは動かさない。スクロールバーの出入りで領域幅が微妙に
 * 揺れると、調整と再計測が交互に走り続けるため。
 */
const scroller = useTemplateRef<HTMLElement>('scroller')

function fitToContainer(): void {
  const el = scroller.value
  if (el === null) return
  const available = el.clientWidth
  if (available <= 0) return

  // 操作列は固定幅なので、その幅を差し引いた残りを配る
  const others = COLUMNS.filter((c) => c.key !== LAST_FLEX.key).reduce(
    (sum, c) => sum + (widths.value[c.key] ?? c.width),
    0,
  )
  const next = Math.max(LAST_FLEX.min, available - others)
  if (Math.abs(next - (widths.value[LAST_FLEX.key] ?? LAST_FLEX.width)) < 1) return
  widths.value[LAST_FLEX.key] = next
}

/**
 * 列幅のドラッグ（`GuiDesign.md` 5.6）。
 *
 * **右隣の列と融通する。総幅は変わらない。** 動く列を1つに絞ると結果が
 * 予測できるためで、全列へ按分すると1つ広げたつもりが表全体の見た目を変える。
 * 右隣が下限に達したらそこで止まる。
 *
 * **キーボードでは操作できない**——9.2 の例外として明記してある（利用者の判断）。
 * 幅は表示上の都合であり、列の内容は横スクロールで到達できる。
 */
let dragging:
  | { key: string; nextKey: string; startX: number; startWidth: number; startNextWidth: number
      min: number; nextMin: number }
  | null = null

function onResizeStart(col: Column, e: MouseEvent): void {
  const i = FLEX_COLUMNS.findIndex((c) => c.key === col.key)
  const neighbour = i < 0 ? undefined : FLEX_COLUMNS[i + 1]
  // 最後の列と操作列にはつまみを出していない（融通する相手がいない）
  if (neighbour === undefined) return

  dragging = {
    key: col.key,
    nextKey: neighbour.key,
    startX: e.clientX,
    startWidth: widths.value[col.key] ?? col.width,
    startNextWidth: widths.value[neighbour.key] ?? neighbour.width,
    min: col.min,
    nextMin: neighbour.min,
  }
  // ドラッグ中に行や見出しの文字が選択されるのを止める。
  // scoped CSS は body に効かないので、クラスではなく style を直接触る
  document.body.style.userSelect = 'none'
  document.body.style.cursor = 'col-resize'
  window.addEventListener('mousemove', onResizeMove)
  window.addEventListener('mouseup', onResizeEnd)
}

function onResizeMove(e: MouseEvent): void {
  const d = dragging
  if (d === null) return
  // 両側の下限で挟む。**始点からの差分で計算する**ので、途中で下限に当たって
  // 止まった後に戻しても、掴んだ位置との関係がずれない
  const min = d.min - d.startWidth
  const max = d.startNextWidth - d.nextMin
  const dx = Math.round(Math.min(Math.max(e.clientX - d.startX, min), max))
  widths.value[d.key] = d.startWidth + dx
  widths.value[d.nextKey] = d.startNextWidth - dx
}

function onResizeEnd(): void {
  dragging = null
  document.body.style.userSelect = ''
  document.body.style.cursor = ''
  window.removeEventListener('mousemove', onResizeMove)
  window.removeEventListener('mouseup', onResizeEnd)
}

function ariaSort(target?: UserSort): 'ascending' | 'descending' | 'none' | undefined {
  if (target === undefined) return undefined
  if (sort.value !== target) return 'none'
  return order.value === 'asc' ? 'ascending' : 'descending'
}

/**
 * ソート列を選ぶ。同じ列なら向きを反転する。
 *
 * 列を切り替えたときの初期の向きは、**日時だけが降順**で他は昇順。
 * 一覧ストア（`stores/project.ts`）と同じ規則である（文字列は昇順、日時は
 * 新しい順）。**状態は昇順＝無効が先**——並べ替える動機は「無効な利用者を
 * 探す」ことが多く、探したいものが上に来る（`ApiDesign.md` 6.1）。
 */
const DESC_FIRST: UserSort[] = ['last_login_at', 'created_at']

function sortBy(target?: UserSort): void {
  if (target === undefined) return
  if (sort.value === target) {
    order.value = order.value === 'asc' ? 'desc' : 'asc'
  } else {
    sort.value = target
    order.value = DESC_FIRST.includes(target) ? 'desc' : 'asc'
  }
  reload()
}

// ── 行 ────────────────────────────────────────────────────────
const rowLinks = useTemplateRef<HTMLAnchorElement[]>('rowLink')

const skeletonRows = computed(() => Math.min(Math.max(items.value.length, 5), 10))

const rangeStart = computed(() => (page.value - 1) * perPage.value + 1)
const rangeEnd = computed(() => Math.min(page.value * perPage.value, total.value))

const isEmpty = computed(() => loaded.value && items.value.length === 0)

/** 人間とエージェントを同じ一覧に並べる（5.6、`DbDesign.md` 6.2） */
function kindIcon(k: string): string {
  return k === 'agent' ? '🤖' : '👤'
}

/** 状態を色だけで示さない（9.2）。文字で出す */
function activeLabel(active: boolean): string {
  return active ? '有効' : '無効'
}

function goToPage(next: number): void {
  if (next < 1 || next > totalPages.value || next === page.value) return
  page.value = next
  void fetchUsers()
}

/**
 * 行クリック（3.1 の遷移図「アカウント/権限管理 ──▶ ユーザー詳細・編集」）。
 *
 * 名前のリンクを直接押した場合はブラウザ既定の動作に任せる（`@click.stop`）。
 * こちらへ来るのは、行の余白やセルを押したときである。
 */
function openRow(item: UserListItem, e: MouseEvent): void {
  const path = `/admin/users/${item.id}`
  if (e.metaKey || e.ctrlKey || e.shiftKey) {
    window.open(router.resolve(path).href, '_blank', 'noopener')
    return
  }
  void router.push(path)
}

// ── 行の操作メニュー（5.6 の `[⋯]`）──────────────────────────
//
// 5項目とも `ApiDesign.md` 6.4〜6.7 のAPIを呼ぶ。**押せない項目は消さずに
// `disabled` で出す**（5.6.2。自分自身へのロール変更・無効化・削除）。
//
// 「編集」は詳細画面へ送る。表示名とメールの編集は詳細の「基本情報」ブロックが
// 担っており（5.6.2）、一覧に2つ目の編集の入口を作らない。

type PendingAction = 'password-reset' | 'revoke-sessions' | 'toggle-active' | 'delete'

/** リセットで生成されたパスワード。**この応答でしか手に入らない**（6.6） */
const resetResult = ref<{ user: UserListItem; password: string } | null>(null)

/** 操作の対象。ダイアログはこの値を見て開く */
const target = ref<UserListItem | null>(null)
const pending = ref<PendingAction | null>(null)
const actionBusy = ref(false)
const actionError = ref<ApiError | null>(null)

/** ダイアログが開いているか。`j` / `k` を横取りしないために見る（9.2） */
const dialogOpen = computed(
  () => addOpen.value || created.value !== null || pending.value !== null,
)

function menuItems(u: UserListItem): ActionItem[] {
  const self = auth.actor?.id === u.id
  return [
    { key: 'edit', label: '編集' },
    { key: 'password-reset', label: 'パスワードをリセット' },
    { key: 'revoke-sessions', label: 'セッションを全失効' },
    {
      key: 'toggle-active',
      label: u.is_active ? '無効化' : '有効化',
      disabled: self && u.is_active,
      reason: '自分自身は無効化できません',
    },
    { key: 'delete', label: '削除', danger: true, disabled: self, reason: '自分自身は削除できません' },
  ]
}

function onMenuSelect(u: UserListItem, key: string): void {
  notice.value = null
  actionError.value = null

  if (key === 'edit') {
    void router.push(`/admin/users/${u.id}`)
    return
  }
  target.value = u
  // 有効化だけ確認を挟まない（失うものが無い）。他は取り消せないか本人に影響が出る
  if (key === 'toggle-active' && !u.is_active) {
    pending.value = 'toggle-active'
    void runAction()
    return
  }
  pending.value = key as PendingAction
}

function closeAction(): void {
  pending.value = null
  target.value = null
  actionError.value = null
}

/**
 * 選んだ操作を実行する。
 *
 * **一覧の行は `version` を持たない**（`ApiDesign.md` 6.1）。`PATCH` は
 * `If-Match` が必須なので、状態を変える操作だけ詳細を1回引いてから送る。
 * 引いた時点の値で送るため、他の誰かが先に更新していれば 409 で止まる。
 */
async function runAction(): Promise<void> {
  const u = target.value
  const action = pending.value
  if (u === null || action === null || actionBusy.value) return

  actionBusy.value = true
  actionError.value = null
  try {
    if (action === 'password-reset') {
      const res = await usersApi.resetUserPassword(u.id)
      resetResult.value = { user: u, password: res.generated_password }
      notice.value = `${u.display_name} のパスワードをリセットしました`
    } else if (action === 'revoke-sessions') {
      await usersApi.revokeUserSessions(u.id)
      notice.value = `${u.display_name} のセッションをすべて失効しました`
    } else if (action === 'toggle-active') {
      const detail = await usersApi.getUser(u.id)
      await usersApi.updateUser(detail.id, detail.version, { is_active: !detail.is_active })
      notice.value = `${u.display_name} を${detail.is_active ? '無効化' : '有効化'}しました`
    } else {
      await usersApi.deleteUser(u.id)
      notice.value = `${u.display_name} を削除しました`
    }
    closeAction()
    void fetchUsers()
  } catch (e: unknown) {
    const err =
      e instanceof ApiError
        ? e
        : new ApiError({
            status: 0,
            code: 'internal_error',
            message: '予期しないエラーが発生しました',
          })
    actionError.value = err
    // 削除は入力欄のあるダイアログの中で伝える。他はダイアログを閉じて表の上に出す
    if (action !== 'delete') {
      pending.value = null
      target.value = null
    }
  } finally {
    actionBusy.value = false
  }
}

/** `j` / `k` で行を上下移動する（9.1）。`Enter` はリンクの既定動作 */
function onKeydown(e: KeyboardEvent): void {
  if (e.key !== 'j' && e.key !== 'k') return
  if (e.metaKey || e.ctrlKey || e.altKey) return
  // モーダルが開いている間は背後の一覧へフォーカスを移さない（9.2 のトラップ）
  if (dialogOpen.value || resetResult.value !== null) return
  const el = e.target as HTMLElement | null
  // 入力中は横取りしない。**検索欄に `j` を打てなくなる**（AppShell の `[` と同じ扱い）
  if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return

  const links = rowLinks.value
  if (!links || links.length === 0) return
  e.preventDefault()

  const currentIndex = links.findIndex((a) => a === document.activeElement)
  const next =
    currentIndex === -1
      ? 0
      : Math.min(Math.max(currentIndex + (e.key === 'j' ? 1 : -1), 0), links.length - 1)
  links[next]?.focus()
}

/**
 * 表示領域の幅を見張る（5.6「ウィンドウ幅が変わったら最後の列が吸収する」）。
 *
 * `window` の resize ではなく要素を見るのは、**メニューの折りたたみ（`[`）でも
 * 表示領域が変わる**ためである。ウィンドウ幅は変わらないので resize は起きない。
 */
let observer: ResizeObserver | null = null

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  observer = new ResizeObserver(() => fitToContainer())

  // 詳細画面で削除したときの結果を受け取る（6.4。操作した画面が消えるため、
  // 着地するこの一覧へ渡している）。**読んだら履歴から消す**——再読み込みで
  // 古い通知が復活しないようにする
  const state = window.history.state as { notice?: string } | null
  if (state?.notice) {
    notice.value = state.notice
    window.history.replaceState({ ...state, notice: undefined }, '')
  }

  void fetchUsers()
  // ロール列の表示名に要る。**一覧の取得と直列にしない**——表示名が無くても
  // キーで行は読めるので、一覧を待たせる理由が無い。
  void rolesStore.ensureRoles('all')
})

// 表は読み込み中・空・エラーで付け外しされる。**現れたときに監視を張り直す**
// （`scroller` が null の間は張れない）。あわせて幅も合わせ直す。
watch(scroller, (el) => {
  observer?.disconnect()
  if (el !== null) {
    observer?.observe(el)
    fitToContainer()
  }
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  clearTimeout(timer)
  observer?.disconnect()
  observer = null
  // ドラッグ中に画面を離れても、body の style と listener を残さない
  if (dragging !== null) onResizeEnd()
})

/**
 * 追加できたとき。
 *
 * `generate` なら初期パスワードのダイアログを出す（5.6.1）。`manual` は
 * `generated_password` が `null` で返るのでダイアログを出さず、結果だけを
 * 一覧の上に残す（6.4）。**どちらも一覧を取り直す**。
 */
function onCreated(user: CreatedUser): void {
  addOpen.value = false
  notice.value = `${user.display_name} を追加しました`
  if (user.generated_password !== null) created.value = user
  // 追加したユーザーが見えるように、絞り込みは触らず現在の条件で取り直す
  void fetchUsers()
}

async function retry(): Promise<void> {
  await fetchUsers()
  if (error.value?.status === 403) await router.replace('/403')
}
</script>

<template>
  <div class="page">
    <PageHeader title="アカウント / 権限">
      <template #actions>
        <!-- 追加できるのはユーザータブだけ。エージェント（Phase 2）とロールと権限
             （Phase 1 は参照のみ。5.6.3）では、押しても行き先が無い -->
        <button v-if="tab === 'users'" type="button" class="primary" @click="addOpen = true">
          + ユーザー追加
        </button>
      </template>
    </PageHeader>

    <div class="page-body">
      <div class="tabs" role="tablist">
        <button
          type="button"
          role="tab"
          class="tab"
          :class="{ selected: tab === 'users' }"
          :aria-selected="tab === 'users'"
          @click="tab = 'users'"
        >
          ユーザー
        </button>
        <button
          type="button"
          role="tab"
          class="tab"
          :class="{ selected: tab === 'agents' }"
          :aria-selected="tab === 'agents'"
          @click="tab = 'agents'"
        >
          エージェント
        </button>
        <button
          type="button"
          role="tab"
          class="tab"
          :class="{ selected: tab === 'roles' }"
          :aria-selected="tab === 'roles'"
          @click="tab = 'roles'"
        >
          ロールと権限
        </button>
      </div>

      <!-- ── ユーザータブ（5.6）───────────────────────────── -->
      <div v-if="tab === 'users'" role="tabpanel">
        <!-- 検索と絞り込みは常時表示する（5.6）。件数で出し入れしない -->
        <div class="filters">
          <label class="search">
            <span class="visually-hidden">名前・メールで検索</span>
            <input
              v-model="q"
              type="search"
              name="q"
              placeholder="名前・メールで検索"
              autocapitalize="off"
              autocomplete="off"
              spellcheck="false"
            />
          </label>

          <label class="filter">
            <span class="filter-label">状態</span>
            <select v-model="isActive">
              <option value="all">すべて</option>
              <option value="true">有効</option>
              <option value="false">無効</option>
            </select>
          </label>
        </div>

        <!-- 追加の結果は操作した場所（一覧の直上）に出す（6.4） -->
        <p v-if="notice" class="notice" role="status">
          <span aria-hidden="true">✓</span> {{ notice }}
          <button type="button" class="notice-close" aria-label="閉じる" @click="notice = null">
            ✕
          </button>
        </p>

        <!-- `[⋯]` の操作が失敗したとき。一覧そのものは出したままにする -->
        <p v-if="actionError" class="alert" role="alert">
          <span aria-hidden="true">✕</span> {{ actionError.message }}
          <button
            type="button"
            class="notice-close"
            aria-label="閉じる"
            @click="actionError = null"
          >
            ✕
          </button>
        </p>

        <!-- エラー（6.2）。原因はサーバの message をそのまま出す（`ApiDesign.md` 2.5） -->
        <EmptyState
          v-if="error"
          title="ユーザー一覧を取得できませんでした"
          :description="error.message"
        >
          <template #action>
            <button type="button" class="primary" @click="retry">再試行</button>
          </template>
        </EmptyState>

        <!-- 列幅の合計が領域を超えたら、**表だけ**が横スクロールする（5.6）。
             ページ全体を横に流さない -->
        <div v-else-if="loading || !isEmpty" ref="scroller" class="table-scroll">
          <table class="table" :style="{ width: `${tableWidth}px` }">
            <colgroup>
              <col v-for="col in COLUMNS" :key="col.key" :style="{ width: `${widths[col.key]}px` }" />
            </colgroup>
            <thead>
              <tr>
                <th
                  v-for="col in COLUMNS"
                  :key="col.key"
                  scope="col"
                  :class="col.className"
                  :aria-sort="ariaSort(col.sort)"
                >
                  <button v-if="col.sort" type="button" class="sort" @click="sortBy(col.sort)">
                    {{ col.label }}
                    <span class="caret" aria-hidden="true">
                      {{ sort === col.sort ? (order === 'asc' ? '▴' : '▾') : '' }}
                    </span>
                  </button>
                  <span v-else>{{ col.label }}</span>

                  <!-- 幅を変えるつまみ。**ドラッグのみ**（9.2 の例外として明記済み）。
                       見出しのソートを誘発しないよう、クリックはここで止める。
                       右隣と融通するので、最後の列と操作列には出さない（5.6） -->
                  <span
                    v-if="resizable(col)"
                    class="resizer"
                    aria-hidden="true"
                    @mousedown.stop.prevent="onResizeStart(col, $event)"
                    @click.stop
                  ></span>
                </th>
              </tr>
            </thead>

            <!-- 読み込み中はスケルトン。実際の行の形を模す（6.2） -->
            <tbody v-if="loading" aria-busy="true">
              <tr v-for="n in skeletonRows" :key="n" class="skeleton-row">
                <td v-for="col in COLUMNS" :key="col.key" :class="col.className">
                  <span class="skeleton"></span>
                </td>
              </tr>
            </tbody>

            <tbody v-else>
              <!-- 無効なユーザーは背面へ下げる（8.2）。状態列の「無効」という
                   文字は残す（9.2） -->
              <tr
                v-for="u in items"
                :key="u.id"
                class="row"
                :class="{ inactive: !u.is_active }"
                @click="openRow(u, $event)"
              >
                <td class="kind">
                  <!-- 種別は記号だけにしない。読み上げ用の文字を添える（9.2） -->
                  <span aria-hidden="true">{{ kindIcon(u.kind) }}</span>
                  <span class="visually-hidden">{{
                    u.kind === 'agent' ? 'エージェント' : 'ユーザー'
                  }}</span>
                </td>
                <td class="name-col">
                  <!-- 実体の <a> を自前で描くのは、j/k の移動で focus() を呼ぶため
                       （ProjectsPage と同じ理由）。custom の navigate は修飾キー付きの
                       クリックを素通しするので、Ctrl/⌘+クリックは新規タブになる -->
                  <RouterLink v-slot="{ href, navigate }" :to="`/admin/users/${u.id}`" custom>
                    <a ref="rowLink" class="name" :href="href" @click.stop="navigate">
                      {{ u.display_name }}
                    </a>
                  </RouterLink>
                </td>
                <td class="email">{{ u.email ?? '—' }}</td>
                <td class="role">{{ rolesStore.userRoleLabel(u.kind, u.system_role) }}</td>
                <td class="status">{{ activeLabel(u.is_active) }}</td>
                <td class="datetime">
                  {{ u.last_login_at === null ? '—' : formatDateTime(u.last_login_at) }}
                </td>
                <td class="date">{{ formatDate(u.created_at) }}</td>
                <!-- 行クリック（詳細へ遷移）を誘発しない。メニュー側でも止めている -->
                <td class="actions-col" @click.stop>
                  <UserActionsMenu
                    :items="menuItems(u)"
                    :label="`${u.display_name} の操作メニュー`"
                    compact
                    @select="onMenuSelect(u, $event)"
                  />
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 空（6.2）。絞り込みの結果かどうかで次の行動が変わる -->
        <EmptyState
          v-else-if="filtered"
          title="条件に一致するユーザーがいません"
          description="検索語や絞り込みを変えてください"
        >
          <template #action>
            <button type="button" class="secondary" @click="clearFilters">条件をクリア</button>
          </template>
        </EmptyState>
        <EmptyState
          v-else
          title="ユーザーがいません"
          description="最初のユーザーを追加してください"
        >
          <template #action>
            <button type="button" class="primary" @click="addOpen = true">+ ユーザー追加</button>
          </template>
        </EmptyState>

        <!-- 件数は常に、ページャは2ページ以上のときだけ出す（5.6 のフッタ） -->
        <div v-if="!error && loaded" class="pager">
          <span v-if="totalPages > 1" class="range">
            {{ rangeStart }}〜{{ rangeEnd }} / 全 {{ total }} 件
          </span>
          <span v-else class="range">{{ total }}件</span>

          <template v-if="totalPages > 1">
            <button
              type="button"
              class="page-button"
              :disabled="page <= 1"
              @click="goToPage(page - 1)"
            >
              ◀ 前
            </button>
            <span class="page-number">{{ page }} / {{ totalPages }}</span>
            <button
              type="button"
              class="page-button"
              :disabled="page >= totalPages"
              @click="goToPage(page + 1)"
            >
              次 ▶
            </button>
          </template>
        </div>
      </div>

      <!-- ── エージェントタブ（5.6。実装は Phase 2）────────────── -->
      <!-- `agent` テーブルは Phase 2 のマイグレーションで作られるため、Phase 1 には
           1件も存在しない。人間と持つ情報が違うのでタブを分けてある -->
      <div v-else-if="tab === 'agents'" class="placeholder" role="tabpanel">
        <p class="placeholder-title">エージェントのページ予定</p>
        <p class="placeholder-doc">GuiDesign.md 5.6 / ApiDesign.md 6.1</p>
        <ul class="placeholder-list">
          <li>登録済みエージェントの一覧（名前・クライアント種別・モデル・プロジェクト・信頼度）</li>
          <li>人間とは持つ情報が違うため、ユーザータブとは別の列構成にする</li>
          <li>行の操作メニューも異なる（パスワードのリセットは無い）</li>
        </ul>
        <p class="placeholder-status">Phase 2 で実装（agent テーブルの作成後）</p>
      </div>

      <!-- ── ロールと権限タブ（5.6.3）──────────────────────── -->
      <!-- 材料は `GET /roles` と `GET /permissions`（`ApiDesign.md` 7.1 / 7.2）。
           3.2 のルーティング表に無いタブなので、URL は `/admin/users` のまま -->
      <div v-else role="tabpanel">
        <RolePermissionMatrix />
      </div>
    </div>

    <AddUserModal v-if="addOpen" @close="addOpen = false" @created="onCreated" />

    <GeneratedPasswordDialog
      v-if="created && created.generated_password"
      :display-name="created.display_name"
      :email="created.email"
      :password="created.generated_password"
      @close="created = null"
    />

    <!-- ── `[⋯]` の確認（6.3）─────────────────────────────── -->
    <ConfirmDialog
      v-if="pending === 'password-reset' && target"
      title="パスワードをリセット"
      :message="`${target.display_name} のパスワードを新しく生成します。\n現在のパスワードは使えなくなり、有効なセッションはすべて失効します。`"
      confirm-label="リセットする"
      danger
      :busy="actionBusy"
      @confirm="runAction"
      @cancel="closeAction"
    />

    <ConfirmDialog
      v-if="pending === 'revoke-sessions' && target"
      title="すべてのセッションを失効"
      :message="`${target.display_name} のログイン中のセッションとアクセストークンをすべて失効します。\n本人は次のリクエストからログインし直す必要があります。`"
      confirm-label="失効する"
      danger
      :busy="actionBusy"
      @confirm="runAction"
      @cancel="closeAction"
    />

    <ConfirmDialog
      v-if="pending === 'toggle-active' && target"
      title="ユーザーを無効化"
      :message="`${target.display_name} を無効化します。\n本人は次のリクエストからログインできなくなります。`"
      confirm-label="無効化する"
      danger
      :busy="actionBusy"
      @confirm="runAction"
      @cancel="closeAction"
    />

    <!-- 削除だけは名前の入力を求める（6.3） -->
    <DeleteUserDialog
      v-if="pending === 'delete' && target"
      :display-name="target.display_name"
      :email="target.email ?? ''"
      :busy="actionBusy"
      :error-message="actionError?.message ?? null"
      @confirm="runAction"
      @cancel="closeAction"
    />

    <!-- リセットで生成された値は、この1回しか出せない（6.6） -->
    <GeneratedPasswordDialog
      v-if="resetResult"
      title="パスワードをリセットしました"
      lead-suffix="のパスワードを再発行しました。"
      :footer-note="
        auth.actor?.id === resetResult.user.id
          ? 'このパスワードで入り直し、次回ログイン後に新しいものへ変更してください。'
          : undefined
      "
      :display-name="resetResult.user.display_name"
      :email="resetResult.user.email ?? ''"
      :password="resetResult.password"
      @close="resetResult = null"
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

.primary,
.secondary {
  display: inline-flex;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border-radius: var(--pb-radius);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}

.primary:hover {
  border-color: var(--pb-accent-hover);
  background: var(--pb-accent-hover);
}

.secondary:hover {
  background: var(--pb-hover);
}

/* ── タブ（5.6。プロジェクト設定と同じ形）──────────────── */
.tabs {
  display: flex;
  gap: var(--pb-space-1);
  margin-bottom: var(--pb-space-4);
  border-bottom: 1px solid var(--pb-border);
}

.tab {
  padding: var(--pb-space-2) var(--pb-space-4);
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  cursor: pointer;
}

.tab:hover {
  color: var(--pb-text);
}

/* 選択中を色だけで示さない（9.2）。下線の太さでも区別できるようにする */
.tab.selected {
  border-bottom-color: var(--pb-accent);
  color: var(--pb-text);
  font-weight: 600;
}

/* ── 検索と絞り込み（5.6）──────────────────────────────── */
.filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-3);
  margin-bottom: var(--pb-space-4);
}

.search {
  flex: none;
}

/* 型セレクタに幅を書くとクラスで上書きできない（詳細度で負ける）。
   入力欄の幅は必ずクラス側で決める */
.search input {
  width: 240px;
  height: 36px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

.filter {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}

.filter-label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.filter select {
  height: 36px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

/* ── 操作結果（6.4）────────────────────────────────────── */
.notice {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  margin-bottom: var(--pb-space-3);
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
}

.notice-close {
  margin-left: auto;
  padding: 0 var(--pb-space-1);
  border: 0;
  background: none;
  color: var(--pb-text-muted);
  cursor: pointer;
}

/* ── 一覧 ──────────────────────────────────────────────── */
/* 列幅の合計が領域を超えたら、**表だけ**を横スクロールさせる（5.6）。
   ページ全体が横に流れると、メニューやページヘッダまでずれる */
.table-scroll {
  overflow-x: auto;
}

/* 幅は <colgroup> が持つ（列ごとの状態）。table-layout: fixed で
   その指定がそのまま効くようにする */
.table {
  border-collapse: collapse;
  table-layout: fixed;
}

th {
  position: relative;
  padding: var(--pb-space-2) var(--pb-space-3);
  overflow: hidden;
  border-bottom: 1px solid var(--pb-border);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
  text-align: left;
  text-overflow: ellipsis;
  white-space: nowrap;
}

th.kind {
  padding-right: 0;
}

/* 列幅を変えるつまみ（5.6）。**ドラッグのみ**（9.2 の例外）。
   掴みやすさのために見た目の線より広く取り、線は ::after で細く描く */
.resizer {
  position: absolute;
  z-index: 1;
  top: 0;
  right: -4px;
  width: 9px;
  height: 100%;
  cursor: col-resize;
}

.resizer::after {
  position: absolute;
  top: 25%;
  left: 4px;
  width: 1px;
  height: 50%;
  background: var(--pb-border);
  content: '';
}

.resizer:hover::after {
  top: 0;
  height: 100%;
  background: var(--pb-accent);
}

/* 最後の列の右端は掴めない（表の外へはみ出す） */
th:last-child .resizer {
  display: none;
}

.sort {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  padding: 0;
  border: 0;
  background: none;
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.caret {
  display: inline-block;
  min-width: 1em;
}

td {
  padding: var(--pb-space-3);
  overflow: hidden;
  border-bottom: 1px solid var(--pb-line);
  text-overflow: ellipsis;
  white-space: nowrap;
  vertical-align: middle;
}

td.kind {
  padding-right: 0;
  text-align: center;
}

td.email,
td.datetime,
td.date {
  color: var(--pb-text-muted);
}

td.datetime,
td.date {
  font-variant-numeric: tabular-nums;
}

.row {
  cursor: pointer;
}

.row:hover {
  background: var(--pb-hover);
}

/* 無効なユーザーは背面へ下げる（8.2）。文字は `--pb-text-muted`（8.3 の11段＝
   文字・弱）で**輝度の階層を一段下げ**、アイコンは彩度を落とす。11段は12段より
   背景に近いが彩度は高いので、「行全体の彩度を下げる」ことは 8.3 の固定スケールでは
   できない。**独自の色は作らない。** 状態列の「無効」という文字は残すので、
   色だけで示していることにもならない（9.2） */
.row.inactive {
  color: var(--pb-text-muted);
}

.row.inactive .name {
  color: inherit;
  font-weight: 500;
}

.row.inactive td.kind {
  filter: grayscale(1);
  opacity: 0.55;
}

.name {
  color: var(--pb-text);
  font-weight: 600;
  text-decoration: none;
}

.name:hover {
  text-decoration: underline;
}

/* ── 読み込み中（6.2）───────────────────────────────────── */
.skeleton {
  display: block;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.skeleton-row td.kind .skeleton {
  width: 20px;
}

.skeleton-row td.status .skeleton {
  width: 32px;
}

/* ── ページング ────────────────────────────────────────── */
.pager {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--pb-space-3);
  margin-top: var(--pb-space-4);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.page-button {
  height: 28px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  cursor: pointer;
}

.page-button:hover:not(:disabled) {
  background: var(--pb-hover);
}

.page-button:disabled {
  cursor: default;
  opacity: 0.5;
}

.page-number {
  font-variant-numeric: tabular-nums;
}

/* ── ロールと権限タブの予定（6.5 と同じ体裁）──────────────── */
.placeholder {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  max-width: 880px;
  padding: var(--pb-space-4);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
}

.placeholder-title {
  margin: 0;
  font-weight: 600;
}

.placeholder-doc,
.placeholder-status {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.placeholder-list {
  margin: 0;
  padding-left: var(--pb-space-5);
  color: var(--pb-text-muted);
  font-size: 13px;
  line-height: 1.8;
}

/* 操作列（5.6 の `[⋯]`）。幅は固定で、余白を詰めてメニューだけを置く */
.actions-col {
  padding: 0;
  text-align: center;
}

/* `[⋯]` の操作が失敗したときの帯。一覧は出したままにする（6.2 の正常状態） */
.alert {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  margin: 0 0 var(--pb-space-3);
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-danger-bg);
  color: var(--pb-danger-text);
  font-size: 13px;
}

/* 読み上げ専用（9.2）。記号だけの列に文字を添えるために使う */
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
</style>
