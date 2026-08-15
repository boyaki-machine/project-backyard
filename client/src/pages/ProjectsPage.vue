<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, useTemplateRef } from 'vue'
import { useRouter } from 'vue-router'

import EmptyState from '../components/EmptyState.vue'
import PageHeader from '../components/PageHeader.vue'
import type { ProjectListItem, ProjectSort } from '../api/projects'
import { useAuthStore } from '../stores/auth'
import { useProjectStore } from '../stores/project'

/**
 * プロジェクト一覧（GuiDesign.md 5.2）。ログイン後の初期画面。
 *
 * **素のリスト表示とする。** 検索・カード表示・表示切替・ピン留めはいずれも
 * 実装しない（5.2、設計原則3）。
 *
 * 4状態（読み込み中・空・エラー・正常）をすべて持つ（6.2）。
 *
 * 新規作成モーダル（5.2.1）は手順10b。ここでは `+ 新規プロジェクト` が
 * 3.2 の `/projects?new=1` へ遷移するところまでを作る。
 */
const auth = useAuthStore()
const store = useProjectStore()
const router = useRouter()

/** 副次アクションの `⋯`（2.5）。今は「アーカイブを表示」だけが入る */
const menuOpen = ref(false)

const canCreate = computed(() => auth.can('project.create'))

/**
 * 列の定義。`sort` を持つ列だけがソート可能である。
 *
 * **「完了」だけソートできない。** `ApiDesign.md` 5.1 の `sort` は
 * `name` / `key` / `updated_at` / `ticket_count` / `progress` の5値で、
 * `closed_count` を含まないため。
 */
const columns: { label: string; sort?: ProjectSort; numeric?: boolean }[] = [
  { label: '名前', sort: 'name' },
  { label: 'タスク', sort: 'ticket_count', numeric: true },
  { label: '完了', numeric: true },
  { label: '進捗', sort: 'progress', numeric: true },
  { label: '最終更新', sort: 'updated_at' },
]

const rowLinks = useTemplateRef<HTMLAnchorElement[]>('rowLink')

/** 読み込み中に出すスケルトンの行数。前回の件数に合わせて高さの変化を抑える */
const skeletonRows = computed(() => Math.min(Math.max(store.items.length, 5), 10))

const rangeStart = computed(() => (store.page - 1) * store.perPage + 1)
const rangeEnd = computed(() => Math.min(store.page * store.perPage, store.total))

function ariaSort(sort?: ProjectSort): 'ascending' | 'descending' | 'none' | undefined {
  if (sort === undefined) return undefined
  if (store.sort !== sort) return 'none'
  return store.order === 'asc' ? 'ascending' : 'descending'
}

/** ソート不可の列（「完了」）から呼ばれても何もしない */
function sortBy(sort?: ProjectSort) {
  if (sort === undefined) return
  store.toggleSort(sort)
}

/** 進捗は数値のみを出す。プログレスバーは置かない（5.2） */
function percent(progress: number): string {
  return `${Math.round(progress * 100)}%`
}

/**
 * 日時の表示（`2026-08-11 09:12`）。
 *
 * API は ISO8601 UTC で返す（`ApiDesign.md` 2.2）。ここでは端末のローカル時刻へ
 * 直して出す。`app_user.timezone` の反映は自分の設定（手順17）で扱う。
 */
function formatDateTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

/**
 * 行クリック（5.2「行全体がリンク。Ctrl/⌘+クリックで新規タブ」）。
 *
 * 名前のリンクを直接押した場合はブラウザ既定の動作に任せる（`@click.stop`）。
 * こちらへ来るのは、行の余白やセルを押したときである。
 */
function openRow(item: ProjectListItem, e: MouseEvent) {
  const path = `/p/${item.key}`
  if (e.metaKey || e.ctrlKey || e.shiftKey) {
    window.open(router.resolve(path).href, '_blank', 'noopener')
    return
  }
  void router.push(path)
}

/** `j` / `k` で行を上下移動する（GuiDesign.md 9.1）。`Enter` はリンクの既定動作 */
function onKeydown(e: KeyboardEvent) {
  if (e.key !== 'j' && e.key !== 'k') return
  if (e.metaKey || e.ctrlKey || e.altKey) return
  const el = e.target as HTMLElement | null
  // 入力中は横取りしない（AppShell の `[` と同じ扱い）
  if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return

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

function toggleArchived() {
  store.setIncludeArchived(!store.includeArchived)
  menuOpen.value = false
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  void store.fetch()
})
onUnmounted(() => window.removeEventListener('keydown', onKeydown))

/**
 * 権限エラーは 403 ページへ送る（6.2）。
 *
 * ルーターガードは `project.view` を見て通しているので、ここへ来るのは
 * ガード通過後に権限が変わった場合である。
 */
async function retry() {
  await store.fetch()
  if (store.error?.status === 403) await router.replace('/403')
}
</script>

<template>
  <div class="page">
    <PageHeader title="プロジェクト">
      <template #actions>
        <div class="more" @keydown.esc="menuOpen = false">
          <button
            type="button"
            class="icon-button"
            aria-label="その他の操作"
            :aria-expanded="menuOpen"
            @click="menuOpen = !menuOpen"
          >
            ⋯
          </button>
          <div v-if="menuOpen" class="dropdown">
            <button
              type="button"
              class="item"
              :aria-pressed="store.includeArchived"
              @click="toggleArchived"
            >
              <span class="mark" aria-hidden="true">{{ store.includeArchived ? '✓' : '' }}</span>
              アーカイブを表示
            </button>
          </div>
        </div>

        <RouterLink
          v-if="canCreate"
          class="primary"
          :to="{ path: '/projects', query: { new: '1' } }"
        >
          + 新規プロジェクト
        </RouterLink>
      </template>
    </PageHeader>

    <div class="page-body">
      <!-- エラー（6.2）。原因はサーバが返した message をそのまま出す（ApiDesign.md 2.5） -->
      <EmptyState
        v-if="store.error"
        title="プロジェクト一覧を取得できませんでした"
        :description="store.error.message"
      >
        <template #action>
          <button type="button" class="primary" @click="retry">再試行</button>
        </template>
      </EmptyState>

      <table v-else-if="store.loading || !store.isEmpty" class="table">
        <thead>
          <tr>
            <th
              v-for="col in columns"
              :key="col.label"
              scope="col"
              :class="{ num: col.numeric }"
              :aria-sort="ariaSort(col.sort)"
            >
              <button v-if="col.sort" type="button" class="sort" @click="sortBy(col.sort)">
                {{ col.label }}
                <span class="caret" aria-hidden="true">
                  {{ store.sort === col.sort ? (store.order === 'asc' ? '▴' : '▾') : '' }}
                </span>
              </button>
              <span v-else>{{ col.label }}</span>
            </th>
          </tr>
        </thead>

        <!-- 読み込み中はスケルトン。スピナーではなく実際の行の形を模す（6.2） -->
        <tbody v-if="store.loading" aria-busy="true">
          <tr v-for="n in skeletonRows" :key="n" class="skeleton-row">
            <td v-for="col in columns" :key="col.label" :class="{ num: col.numeric }">
              <span class="skeleton"></span>
            </td>
          </tr>
        </tbody>

        <tbody v-else>
          <tr v-for="p in store.items" :key="p.id" class="row" @click="openRow(p, $event)">
            <td>
              <span class="name-line">
                <!-- 実体の <a> を自前で描くのは、j/k の移動で focus() を呼ぶため
                     （RouterLink の ref はコンポーネントであって要素ではない）。
                     custom の navigate は修飾キー付きのクリックを素通しするので、
                     Ctrl/⌘+クリックでの新規タブはブラウザ既定の動作になる -->
                <RouterLink v-slot="{ href, navigate }" :to="`/p/${p.key}`" custom>
                  <a ref="rowLink" class="name" :href="href" @click.stop="navigate">
                    {{ p.name }}
                  </a>
                </RouterLink>
                <span class="key">{{ p.key }}</span>
                <span v-if="p.status === 'archived'" class="archived">アーカイブ済み</span>
              </span>
              <span v-if="p.description" class="description">{{ p.description }}</span>
            </td>
            <td class="num">{{ p.ticket_count }}</td>
            <td class="num">{{ p.closed_count }}</td>
            <td class="num">{{ percent(p.progress) }}</td>
            <td class="updated">{{ formatDateTime(p.updated_at) }}</td>
          </tr>
        </tbody>
      </table>

      <!-- 空（5.2）。`project.create` を持たない利用者にはボタンを出さない -->
      <EmptyState
        v-else-if="canCreate"
        title="プロジェクトがありません"
        description="最初のプロジェクトを作成して、チケットの管理を始めましょう"
      >
        <template #action>
          <RouterLink class="primary" :to="{ path: '/projects', query: { new: '1' } }">
            + 新規プロジェクト
          </RouterLink>
        </template>
      </EmptyState>
      <EmptyState
        v-else
        title="参加しているプロジェクトがありません"
        description="管理者に招待を依頼してください"
      />

      <div v-if="!store.error && store.totalPages > 1" class="pager">
        <span class="range">{{ rangeStart }}〜{{ rangeEnd }} / 全 {{ store.total }} 件</span>
        <button
          type="button"
          class="page-button"
          :disabled="store.page <= 1"
          @click="store.goToPage(store.page - 1)"
        >
          ◀ 前
        </button>
        <span class="page-number">{{ store.page }} / {{ store.totalPages }}</span>
        <button
          type="button"
          class="page-button"
          :disabled="store.page >= store.totalPages"
          @click="store.goToPage(store.page + 1)"
        >
          次 ▶
        </button>
      </div>
    </div>
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

/* ── ページヘッダのアクション ───────────────────────────── */
.more {
  position: relative;
}

.icon-button {
  width: 32px;
  height: 32px;
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  cursor: pointer;
}

.icon-button:hover {
  border-color: var(--pb-border);
  background: var(--pb-hover);
}

.dropdown {
  position: absolute;
  z-index: 10;
  top: calc(100% + var(--pb-space-1));
  right: 0;
  min-width: 200px;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  box-shadow: var(--pb-shadow-2);
}

.item {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  width: 100%;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 0;
  border-radius: var(--pb-radius);
  background: none;
  cursor: pointer;
  text-align: left;
  white-space: nowrap;
}

.item:hover {
  background: var(--pb-hover);
}

.mark {
  flex: none;
  width: 1em;
  color: var(--pb-text-muted);
}

.primary {
  display: inline-flex;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-accent);
  border-radius: var(--pb-radius);
  background: var(--pb-accent);
  color: var(--pb-on-accent);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}

.primary:hover {
  border-color: var(--pb-accent-hover);
  background: var(--pb-accent-hover);
}

/* ── 一覧 ──────────────────────────────────────────────── */
.table {
  width: 100%;
  border-collapse: collapse;
}

th {
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-border);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
  text-align: left;
}

th.num {
  text-align: right;
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
  border-bottom: 1px solid var(--pb-line);
  vertical-align: top;
}

td.num {
  text-align: right;
  /* 桁を揃える。等幅にすると行ごとに数字の幅が変わらない */
  font-variant-numeric: tabular-nums;
}

.updated {
  color: var(--pb-text-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.row {
  cursor: pointer;
}

.row:hover {
  background: var(--pb-hover);
}

.name-line {
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-2);
}

.name {
  color: var(--pb-text);
  font-weight: 600;
  text-decoration: none;
}

.name:hover {
  text-decoration: underline;
}

/* キーは URL・チケット番号・MCP のパスに出るため、名前の隣に併記する */
.key {
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* 状態を色だけで示さない（9.2）。アーカイブは文字で出す */
.archived {
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  color: var(--pb-text-muted);
  font-size: 12px;
}

.description {
  display: block;
  margin-top: var(--pb-space-1);
  overflow: hidden;
  color: var(--pb-text-muted);
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* ── 読み込み中（6.2）───────────────────────────────────── */
.skeleton {
  display: block;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.skeleton-row td:first-child .skeleton {
  width: 40%;
}

.skeleton-row td.num .skeleton {
  width: 32px;
  margin-left: auto;
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
</style>
