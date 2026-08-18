<script setup lang="ts">
/**
 * ユーザー / 権限管理（`GuiDesign.md` 5.6）。必要権限は `user.manage`。
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
import EmptyState from '../components/EmptyState.vue'
import GeneratedPasswordDialog from '../components/GeneratedPasswordDialog.vue'
import PageHeader from '../components/PageHeader.vue'
import { ApiError } from '../api/client'
import * as usersApi from '../api/users'
import type {
  CreatedUser,
  SortOrder,
  UserActiveFilter,
  UserKindFilter,
  UserListItem,
  UserSort,
} from '../api/users'
import { formatDate, formatDateTime } from '../lib/datetime'
import { userRoleLabel } from '../lib/roles'

/**
 * 検索の debounce。`ApiDesign.md` 5.2 の `check-key` と同じ値にそろえる。
 * アプリの中に「待つ長さ」の数字を2つ作らないため。
 */
const SEARCH_DEBOUNCE_MS = 400

/** 1ページの件数。`ApiDesign.md` 2.6 / 6.1 のサーバ既定と同じ値を明示する */
const PER_PAGE = 25

const router = useRouter()

type Tab = 'users' | 'roles'
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
const kind = ref<UserKindFilter>('all')
const isActive = ref<UserActiveFilter>('all')

/** サーバへ送っている検索語。debounce の途中は `q` と食い違う */
const appliedQ = ref('')

const filtered = computed(
  () => appliedQ.value !== '' || kind.value !== 'all' || isActive.value !== 'all',
)

// ── 追加（5.6.1）──────────────────────────────────────────────
const addOpen = ref(false)
/** 生成パスワードの1回表示。`manual` では `generated_password` が null で出さない */
const created = ref<CreatedUser | null>(null)
/** 6.4「結果は操作した場所に出す」。表の直上に残す */
const createdNotice = ref<string | null>(null)

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
      kind: kind.value,
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

watch([kind, isActive], () => reload())

function clearFilters(): void {
  clearTimeout(timer)
  q.value = ''
  appliedQ.value = ''
  kind.value = 'all'
  isActive.value = 'all'
  reload()
}

// ── 列とソート ────────────────────────────────────────────────
//
// ソートできるのは `ApiDesign.md` 6.1 の `sort` が受ける4つだけである。
// **ロールと状態はAPIが受けない**ので、押せる見た目にしない。
const columns: { label: string; sort?: UserSort; className?: string }[] = [
  { label: '', className: 'kind' },
  { label: '名前', sort: 'display_name', className: 'name-col' },
  { label: 'メール', sort: 'email', className: 'email' },
  { label: 'ロール', className: 'role' },
  { label: '状態', className: 'status' },
  { label: '最終ログイン', sort: 'last_login_at', className: 'datetime' },
  { label: '作成', sort: 'created_at', className: 'date' },
]

function ariaSort(target?: UserSort): 'ascending' | 'descending' | 'none' | undefined {
  if (target === undefined) return undefined
  if (sort.value !== target) return 'none'
  return order.value === 'asc' ? 'ascending' : 'descending'
}

/**
 * ソート列を選ぶ。同じ列なら向きを反転する。
 *
 * 列を切り替えたときの初期の向きは、名前・メールが昇順、日時が降順。
 * 一覧ストア（`stores/project.ts`）と同じ規則にそろえてある。
 */
function sortBy(target?: UserSort): void {
  if (target === undefined) return
  if (sort.value === target) {
    order.value = order.value === 'asc' ? 'desc' : 'asc'
  } else {
    sort.value = target
    order.value = target === 'display_name' || target === 'email' ? 'asc' : 'desc'
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
 * 行クリック（3.1 の遷移図「ユーザー/権限管理 ──▶ ユーザー詳細・編集」）。
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

/** `j` / `k` で行を上下移動する（9.1）。`Enter` はリンクの既定動作 */
function onKeydown(e: KeyboardEvent): void {
  if (e.key !== 'j' && e.key !== 'k') return
  if (e.metaKey || e.ctrlKey || e.altKey) return
  // モーダルが開いている間は背後の一覧へフォーカスを移さない（9.2 のトラップ）
  if (addOpen.value || created.value !== null) return
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

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  void fetchUsers()
})
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  clearTimeout(timer)
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
  createdNotice.value = `${user.display_name} を追加しました`
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
    <PageHeader title="ユーザー / 権限">
      <template #actions>
        <button type="button" class="primary" @click="addOpen = true">+ ユーザー追加</button>
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
            <span class="filter-label">種別</span>
            <select v-model="kind">
              <option value="all">すべて</option>
              <option value="user">ユーザー</option>
              <option value="agent">エージェント</option>
            </select>
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
        <p v-if="createdNotice" class="notice" role="status">
          <span aria-hidden="true">✓</span> {{ createdNotice }}
          <button type="button" class="notice-close" aria-label="閉じる" @click="createdNotice = null">
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

        <table v-else-if="loading || !isEmpty" class="table">
          <thead>
            <tr>
              <th
                v-for="(col, i) in columns"
                :key="col.label + i"
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
              </th>
            </tr>
          </thead>

          <!-- 読み込み中はスケルトン。実際の行の形を模す（6.2） -->
          <tbody v-if="loading" aria-busy="true">
            <tr v-for="n in skeletonRows" :key="n" class="skeleton-row">
              <td v-for="(col, i) in columns" :key="col.label + i" :class="col.className">
                <span class="skeleton"></span>
              </td>
            </tr>
          </tbody>

          <tbody v-else>
            <tr v-for="u in items" :key="u.id" class="row" @click="openRow(u, $event)">
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
              <td class="role">{{ userRoleLabel(u.kind, u.system_role) }}</td>
              <td class="status">{{ activeLabel(u.is_active) }}</td>
              <td class="datetime">
                {{ u.last_login_at === null ? '—' : formatDateTime(u.last_login_at) }}
              </td>
              <td class="date">{{ formatDate(u.created_at) }}</td>
            </tr>
          </tbody>
        </table>

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

      <!-- ── ロールと権限タブ（5.6.3）──────────────────────── -->
      <!-- 中身は `GET /roles` / `GET /permissions`（`ApiDesign.md` 7.1 / 7.2）が要る。
           3.2 のルーティング表に無いタブなので PlaceholderPage は使えないが、
           空白にせず予定を出すのは 6.5 と同じ考え方である -->
      <div v-else class="placeholder" role="tabpanel">
        <p class="placeholder-title">ロールと権限のページ予定</p>
        <p class="placeholder-doc">GuiDesign.md 5.6.3</p>
        <ul class="placeholder-list">
          <li>権限カタログとロールの対応表（Design.md 6.4.2、正本は DbDesign.md 7.2 のシード）</li>
          <li>オペレータ / アドミニストレータ / PJ管理者 / PJメンバー の4列</li>
          <li>Phase 1 は参照のみ。カスタムロールの作成と権限の編集は Phase 3</li>
        </ul>
        <p class="placeholder-status">Phase 1・手順14で実装（docs/PROGRESS.md）</p>
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

.primary {
  border: 1px solid var(--pb-accent);
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.primary:hover {
  border-color: var(--pb-accent-hover);
  background: var(--pb-accent-hover);
}

.secondary {
  border: 1px solid var(--pb-border);
  background: var(--pb-surface);
  color: inherit;
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
/* table-layout: fixed の理由は ProjectsPage と同じ。長いメールで表が
   横へ伸びるのを止め、残りの幅を名前の列へ渡す */
.table {
  width: 100%;
  border-collapse: collapse;
  table-layout: fixed;
}

th {
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-border);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
  text-align: left;
}

/* 幅を指定しないのはメールの列だけで、そこが余りを受け取る。
   **長くなりうるのはメール**（254文字。`ApiDesign.md` 6.2）で、表示名は
   60文字までかつ実際には短い。名前に余りを渡すと右側が間延びする */
th.kind {
  width: 40px;
  padding-right: 0;
}

th.name-col {
  width: 280px;
}

th.role {
  width: 150px;
}

th.status {
  width: 70px;
}

th.datetime {
  width: 150px;
}

th.date {
  width: 110px;
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
