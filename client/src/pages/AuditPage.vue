<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import PageHeader from '../components/PageHeader.vue'
import * as auditApi from '../api/audit'
import type { AuditFilters, AuditLogItem } from '../api/audit'
import { formatAuditDateTime, instantOfLocalInput, isoOf } from '../lib/datetime'
import { uiText } from '../locales/ui'

const fromInput = ref('')
const categoryInput = ref<AuditFilters['category']>()
const toInput = ref('')
const actionInput = ref('')
const actorInput = ref('')
const resultInput = ref('')
const searchInput = ref('')
const applied = ref<AuditFilters>({})
const items = ref<AuditLogItem[]>([])
const page = ref(1)
const perPage = ref(50)
const total = ref(0)
const totalPages = ref(0)
const loading = ref(false)
const loaded = ref(false)
const exporting = ref(false)
const error = ref('')
const filterError = ref('')
const expanded = ref<string | null>(null)
const columnWidths = ref([225, 155, 195, 300, 100])
const tableWidth = computed(() => columnWidths.value.reduce((sum, width) => sum + width, 0))
let resizing: { index: number; startX: number; width: number } | null = null

function startResize(index: number, event: PointerEvent) {
  resizing = { index, startX: event.clientX, width: columnWidths.value[index]! }
  window.addEventListener('pointermove', moveResize)
  window.addEventListener('pointerup', stopResize, { once: true })
  event.preventDefault()
}

function moveResize(event: PointerEvent) {
  if (!resizing) return
  const next = [...columnWidths.value]
  next[resizing.index] = Math.max(85, resizing.width + event.clientX - resizing.startX)
  columnWidths.value = next
}

function stopResize() {
  resizing = null
  window.removeEventListener('pointermove', moveResize)
}

function nudgeWidth(index: number, delta: number) {
  const next = [...columnWidths.value]
  next[index] = Math.max(85, next[index]! + delta)
  columnWidths.value = next
}

function changePerPage() {
  page.value = 1
  void load()
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await auditApi.listAuditLogs(applied.value, page.value, perPage.value)
    items.value = data.items
    total.value = data.total
    totalPages.value = data.total_pages
    loaded.value = true
    expanded.value = null
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : uiText('監査ログを取得できませんでした')
    items.value = []
    total.value = 0
    totalPages.value = 0
    loaded.value = false
  } finally {
    loading.value = false
  }
}

function applyFilters() {
  const from = fromInput.value ? instantOfLocalInput(fromInput.value) : undefined
  const to = toInput.value ? instantOfLocalInput(toInput.value) : undefined
  if ((fromInput.value && from === null) || (toInput.value && to === null) ||
      (from != null && to != null && from >= to)) {
    filterError.value = uiText('終了日時は開始日時より後にしてください')
    return
  }
  filterError.value = ''
  applied.value = {
    from_at: from ?? undefined,
    to_at: to ?? undefined,
    category: categoryInput.value,
    action: actionInput.value.trim() || undefined,
    actor: actorInput.value.trim() || undefined,
    result: resultInput.value === 'success' || resultInput.value === 'failure' ? resultInput.value : undefined,
    q: searchInput.value.trim() || undefined,
  }
  page.value = 1
  void load()
}

function resetFilters() {
  fromInput.value = ''
  categoryInput.value = undefined
  toInput.value = ''
  actionInput.value = ''
  actorInput.value = ''
  resultInput.value = ''
  searchInput.value = ''
  applyFilters()
}

function selectCategory(category: AuditFilters['category']) {
  categoryInput.value = category
  applied.value = { ...applied.value, category }
  page.value = 1
  void load()
}

function movePage(to: number) {
  if (to < 1 || to > totalPages.value || loading.value) return
  page.value = to
  void load()
}

async function exportCSV() {
  exporting.value = true
  error.value = ''
  try {
    const blob = await auditApi.exportAuditLogs(applied.value)
    const url = URL.createObjectURL(blob)
    const link = document.createElement('a')
    link.href = url
    link.download = 'pb-audit.csv'
    document.body.append(link)
    link.click()
    link.remove()
    setTimeout(() => URL.revokeObjectURL(url), 1000)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : uiText('CSVを出力できませんでした')
  } finally {
    exporting.value = false
  }
}

function detailText(item: AuditLogItem): string {
  return JSON.stringify(item.detail, null, 2)
}

function targetText(item: AuditLogItem): string {
  return item.target_label || [item.target_type, item.target_id].filter(Boolean).join(' / ') || '—'
}

function changedField(item: AuditLogItem): string | null {
  if (item.category !== 'ticket') return null
  const field = (item.detail as Record<string, unknown>).field
  return typeof field === 'string' ? field : null
}

function toggleDetail(id: string) {
  expanded.value = expanded.value === id ? null : id
}

onMounted(load)
onBeforeUnmount(stopResize)
</script>

<template>
  <div class="page">
    <PageHeader :title="$ui('監査ログ')">
      <template #actions>
        <button type="button" class="export-button" :disabled="exporting || loading" @click="exportCSV">
          {{ exporting ? $ui('出力中…') : $ui('CSVエクスポート') }}
        </button>
      </template>
    </PageHeader>

    <div class="body">
      <form class="filters" @submit.prevent="applyFilters">
        <label class="filter-field">{{ $ui('開始日時') }}
          <input v-model="fromInput" type="datetime-local" />
        </label>
        <label class="filter-field">{{ $ui('終了日時') }}
          <input v-model="toInput" type="datetime-local" />
        </label>
        <label class="filter-field">{{ $ui('操作') }}
          <input v-model="actionInput" type="search" :placeholder="$ui('例: login')" />
        </label>
        <label class="filter-field">{{ $ui('実行者') }}
          <input v-model="actorInput" type="search" :placeholder="$ui('名前で検索')" />
        </label>
        <label class="filter-field">{{ $ui('結果') }}
          <select v-model="resultInput">
            <option value="">{{ $ui('すべて') }}</option>
            <option value="success">{{ $ui('成功') }}</option>
            <option value="failure">{{ $ui('失敗') }}</option>
          </select>
        </label>
        <label class="filter-field search-label">{{ $ui('検索ワード') }}
          <input v-model="searchInput" type="search" :placeholder="$ui('実行者・操作・対象を検索')" />
        </label>
        <div class="filter-actions">
          <button type="submit" :disabled="loading">{{ $ui('絞り込む') }}</button>
          <button type="button" :disabled="loading" @click="resetFilters">{{ $ui('クリア') }}</button>
        </div>
      </form>
      <div class="categories" role="group" :aria-label="$ui('履歴の種別')">
        <button v-for="option in [
          { value: undefined, label: $ui('すべて') },
          { value: 'ticket', label: $ui('チケット') },
          { value: 'project', label: $ui('プロジェクト') },
          { value: 'application', label: $ui('アプリケーション') },
          { value: 'security', label: $ui('認証・アカウント') },
        ] as const" :key="option.label" type="button" :class="{ active: categoryInput === option.value }" :aria-pressed="categoryInput === option.value" @click="selectCategory(option.value)">{{ option.label }}</button>
      </div>
      <p v-if="filterError" class="error" role="alert">{{ filterError }}</p>
      <p v-if="error" class="error" role="alert">{{ error }} <button type="button" @click="load">{{ $ui('再試行') }}</button></p>
      <p v-if="loading" class="status">{{ $ui('読み込んでいます…') }}</p>
      <p v-else-if="loaded && total === 0" class="status">{{ $ui('該当する監査ログはありません') }}</p>

      <div v-if="items.length" class="table-wrap">
        <table :style="{ width: `${tableWidth}px` }">
          <colgroup><col v-for="(width, index) in columnWidths" :key="index" :style="{ width: `${width}px` }" /></colgroup>
          <thead><tr>
            <th v-for="(heading, index) in [$ui('日時'), $ui('実行者'), $ui('操作'), $ui('対象'), $ui('結果')]" :key="index" scope="col">
              {{ heading }}
              <span class="resize-handle" role="separator" tabindex="0" :aria-label="`${heading} ${$ui('列の幅を調整')}`" aria-orientation="vertical" @pointerdown="startResize(index, $event)" @keydown.left.prevent="nudgeWidth(index, -10)" @keydown.right.prevent="nudgeWidth(index, 10)" />
            </th>
          </tr></thead>
          <tbody>
            <template v-for="item in items" :key="item.id">
              <tr class="record" :class="{ selected: expanded === item.id }" tabindex="0" :aria-expanded="expanded === item.id" @click="toggleDetail(item.id)" @keydown.enter.self="toggleDetail(item.id)" @keydown.space.self.prevent="toggleDetail(item.id)">
                <td><time :datetime="isoOf(item.occurred_at)">{{ formatAuditDateTime(item.occurred_at) }}</time></td>
                <td><RouterLink v-if="item.actor_kind === 'user' && item.actor_id" :to="`/admin/users/${item.actor_id}`" @click.stop>{{ item.actor_name || '—' }}</RouterLink><template v-else>{{ item.actor_name || '—' }}</template></td>
                <td><code>{{ item.action }}</code><small v-if="changedField(item)" class="field-name">{{ changedField(item) }}</small></td>
                <td class="target"><span :title="targetText(item)">{{ targetText(item) }}</span></td>
                <td>{{ item.result === 'success' ? $ui('成功') : $ui('失敗') }}</td>
              </tr>
              <tr v-if="expanded === item.id" class="detail-row"><td colspan="5">
                <dl class="details">
                  <dt>IP</dt><dd>{{ item.ip || '—' }}</dd>
                  <dt>User-Agent</dt><dd>{{ item.user_agent || '—' }}</dd>
                  <dt>Request ID</dt><dd>{{ item.request_id || '—' }}</dd>
                  <dt>Token ID</dt><dd>{{ item.token_id || '—' }}</dd>
                  <dt>{{ $ui('対象種別') }}</dt><dd>{{ item.target_type || '—' }}</dd>
                  <dt>{{ $ui('対象ID') }}</dt><dd>{{ item.target_id || '—' }}</dd>
                  <dt>{{ $ui('対象名') }}</dt><dd>{{ item.target_label || '—' }}</dd>
                  <dt>detail</dt><dd><pre>{{ detailText(item) }}</pre></dd>
                </dl>
              </td></tr>
            </template>
          </tbody>
        </table>
      </div>
      <div v-if="loaded && total > 0" class="pager">
        <span>{{ total }}{{ $ui('件') }} · {{ page }} / {{ totalPages }}</span>
        <label class="page-size">{{ $ui('表示件数') }} <select v-model.number="perPage" @change="changePerPage"><option v-for="size in [50, 100, 200, 400]" :key="size" :value="size">{{ size }}</option></select></label>
        <button type="button" :disabled="page <= 1 || loading" @click="movePage(page - 1)">{{ $ui('前へ') }}</button>
        <button type="button" :disabled="page >= totalPages || loading" @click="movePage(page + 1)">{{ $ui('次へ') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page { display: flex; flex-direction: column; height: 100%; min-width: 0; }
.body { flex: 1; overflow: auto; padding: var(--pb-space-6); }
.filters { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: var(--pb-space-2) var(--pb-space-3); padding: var(--pb-space-3); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); background: linear-gradient(135deg, var(--pb-surface), var(--pb-bg)); }
.filter-field { display: flex; align-items: center; gap: var(--pb-space-2); min-width: 0; white-space: nowrap; font-size: 12px; color: var(--pb-text-muted); }
.filters input, .filters select { flex: 1; min-width: 0; height: 32px; padding: 0 var(--pb-space-2); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); background: var(--pb-surface); color: var(--pb-text); font: inherit; }
.filters input:focus-visible, .filters select:focus-visible { outline: 2px solid var(--pb-accent); outline-offset: 1px; }
.search-label { grid-column: span 2; }
.filter-actions { display: flex; justify-content: flex-end; align-items: center; gap: var(--pb-space-2); }
.categories { display: flex; flex-wrap: wrap; gap: var(--pb-space-1); margin-top: var(--pb-space-3); }
.categories button { font-size: 12px; padding: var(--pb-space-1) var(--pb-space-3); }
.categories button.active { border-color: var(--pb-accent); color: var(--pb-accent); background: var(--pb-surface); }
button { cursor: pointer; padding: var(--pb-space-2) var(--pb-space-3); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); background: var(--pb-surface); color: var(--pb-text); white-space: nowrap; }
button:disabled { cursor: default; opacity: .5; }
.export-button { font-size: 13px; }
.error { color: var(--pb-danger); margin-top: var(--pb-space-3); }
.status { padding: var(--pb-space-6); color: var(--pb-text-muted); }
.table-wrap { overflow-x: auto; margin-top: var(--pb-space-4); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); }
table { min-width: 100%; table-layout: fixed; border-collapse: collapse; background: var(--pb-surface); font-size: 13px; }
th, td { padding: var(--pb-space-3); border-bottom: 1px solid var(--pb-line); text-align: left; vertical-align: top; overflow: hidden; }
th { position: relative; color: var(--pb-text-muted); font-weight: 600; }
.resize-handle { position: absolute; right: 0; top: 0; bottom: 0; width: 9px; cursor: col-resize; touch-action: none; }
.resize-handle:hover, .resize-handle:focus-visible { background: var(--pb-line); outline: none; }
.record { cursor: pointer; }
.record:hover, .record:focus-visible, .selected { background: var(--pb-bg); }
.record time { text-decoration: underline; }
.target span { display: block; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.field-name { display: block; margin-top: var(--pb-space-1); color: var(--pb-text-muted); }
.details { display: grid; grid-template-columns: 100px minmax(0, 1fr); gap: var(--pb-space-2); margin: 0; overflow-wrap: anywhere; }
.details dt { color: var(--pb-text-muted); }
.details dd { margin: 0; min-width: 0; }
pre { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; }
.pager { display: flex; justify-content: flex-end; align-items: center; gap: var(--pb-space-2); padding: var(--pb-space-4) 0; }
.page-size { display: inline-flex; align-items: center; gap: var(--pb-space-1); margin: 0 var(--pb-space-2); }
.page-size select { padding: var(--pb-space-1); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); background: var(--pb-surface); color: var(--pb-text); }
@media (max-width: 1100px) { .filters { grid-template-columns: repeat(2, minmax(0, 1fr)); } .search-label { grid-column: auto; } }
@media (max-width: 760px) { .body { padding: var(--pb-space-3); } .pager { flex-wrap: wrap; } }
@media (max-width: 560px) { .filters { grid-template-columns: minmax(0, 1fr); } .filter-actions { justify-content: flex-start; } }
</style>
