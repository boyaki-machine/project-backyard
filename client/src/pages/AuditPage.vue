<script setup lang="ts">
import { onMounted, ref } from 'vue'

import PageHeader from '../components/PageHeader.vue'
import * as auditApi from '../api/audit'
import type { AuditFilters, AuditLogItem } from '../api/audit'
import { formatDateTime, instantOfLocalInput, isoOf } from '../lib/datetime'
import { uiText } from '../locales/ui'

const fromInput = ref('')
const toInput = ref('')
const actionInput = ref('')
const actorInput = ref('')
const resultInput = ref('')
const searchInput = ref('')
const applied = ref<AuditFilters>({})
const items = ref<AuditLogItem[]>([])
const page = ref(1)
const total = ref(0)
const totalPages = ref(0)
const loading = ref(false)
const loaded = ref(false)
const exporting = ref(false)
const error = ref('')
const filterError = ref('')
const expanded = ref<string | null>(null)

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = await auditApi.listAuditLogs(applied.value, page.value)
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
  toInput.value = ''
  actionInput.value = ''
  actorInput.value = ''
  resultInput.value = ''
  searchInput.value = ''
  applyFilters()
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
  return [item.target_type, item.target_id].filter(Boolean).join(' / ') || '—'
}

function toggleDetail(id: string) {
  expanded.value = expanded.value === id ? null : id
}

onMounted(load)
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
        <label>{{ $ui('開始日時') }}
          <input v-model="fromInput" type="datetime-local" />
        </label>
        <label>{{ $ui('終了日時') }}
          <input v-model="toInput" type="datetime-local" />
        </label>
        <label>{{ $ui('操作') }}
          <input v-model="actionInput" type="search" :placeholder="$ui('例: login')" />
        </label>
        <label>{{ $ui('実行者') }}
          <input v-model="actorInput" type="search" :placeholder="$ui('名前で検索')" />
        </label>
        <label>{{ $ui('結果') }}
          <select v-model="resultInput">
            <option value="">{{ $ui('すべて') }}</option>
            <option value="success">{{ $ui('成功') }}</option>
            <option value="failure">{{ $ui('失敗') }}</option>
          </select>
        </label>
        <label class="search-label">{{ $ui('検索ワード') }}
          <input v-model="searchInput" type="search" :placeholder="$ui('実行者・操作・対象を検索')" />
        </label>
        <div class="filter-actions">
          <button type="submit" :disabled="loading">{{ $ui('絞り込む') }}</button>
          <button type="button" :disabled="loading" @click="resetFilters">{{ $ui('クリア') }}</button>
        </div>
      </form>
      <p v-if="filterError" class="error" role="alert">{{ filterError }}</p>
      <p v-if="error" class="error" role="alert">{{ error }} <button type="button" @click="load">{{ $ui('再試行') }}</button></p>
      <p v-if="loading" class="status">{{ $ui('読み込んでいます…') }}</p>
      <p v-else-if="loaded && total === 0" class="status">{{ $ui('該当する監査ログはありません') }}</p>

      <div v-if="items.length" class="table-wrap">
        <table>
          <thead><tr>
            <th scope="col">{{ $ui('日時') }}</th><th scope="col">{{ $ui('実行者') }}</th>
            <th scope="col">{{ $ui('操作') }}</th><th scope="col">{{ $ui('対象') }}</th>
            <th scope="col">{{ $ui('結果') }}</th>
          </tr></thead>
          <tbody>
            <template v-for="item in items" :key="item.id">
              <tr class="record" :class="{ selected: expanded === item.id }" tabindex="0" :aria-expanded="expanded === item.id" @click="toggleDetail(item.id)" @keydown.enter="toggleDetail(item.id)" @keydown.space.prevent="toggleDetail(item.id)">
                <td><time :datetime="isoOf(item.occurred_at)">{{ formatDateTime(item.occurred_at) }}</time></td>
                <td>{{ item.actor_label || '—' }}</td>
                <td><code>{{ item.action }}</code></td>
                <td class="target">{{ targetText(item) }}</td>
                <td>{{ item.result === 'success' ? $ui('成功') : $ui('失敗') }}</td>
              </tr>
              <tr v-if="expanded === item.id" class="detail-row"><td colspan="5">
                <dl class="details">
                  <dt>IP</dt><dd>{{ item.ip || '—' }}</dd>
                  <dt>User-Agent</dt><dd>{{ item.user_agent || '—' }}</dd>
                  <dt>Request ID</dt><dd>{{ item.request_id || '—' }}</dd>
                  <dt>Token ID</dt><dd>{{ item.token_id || '—' }}</dd>
                  <dt>detail</dt><dd><pre>{{ detailText(item) }}</pre></dd>
                </dl>
              </td></tr>
            </template>
          </tbody>
        </table>
      </div>
      <div v-if="loaded && total > 0" class="pager">
        <span>{{ total }}{{ $ui('件') }} · {{ page }} / {{ totalPages }}</span>
        <button type="button" :disabled="page <= 1 || loading" @click="movePage(page - 1)">{{ $ui('前へ') }}</button>
        <button type="button" :disabled="page >= totalPages || loading" @click="movePage(page + 1)">{{ $ui('次へ') }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page { display: flex; flex-direction: column; height: 100%; min-width: 0; }
.body { flex: 1; overflow: auto; padding: var(--pb-space-6); }
.filters { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: var(--pb-space-3); padding: var(--pb-space-4); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); background: var(--pb-surface); }
.filters label { display: flex; flex-direction: column; gap: var(--pb-space-1); min-width: 0; font-size: 13px; color: var(--pb-text-muted); }
.filters input, .filters select { width: 100%; min-width: 0; padding: var(--pb-space-2); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); background: var(--pb-bg); color: var(--pb-text); font: inherit; }
.search-label { grid-column: span 2; }
.filter-actions { display: flex; align-items: end; gap: var(--pb-space-2); }
button { cursor: pointer; padding: var(--pb-space-2) var(--pb-space-3); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); background: var(--pb-surface); color: var(--pb-text); white-space: nowrap; }
button:disabled { cursor: default; opacity: .5; }
.export-button { font-size: 13px; }
.error { color: var(--pb-danger); margin-top: var(--pb-space-3); }
.status { padding: var(--pb-space-6); color: var(--pb-text-muted); }
.table-wrap { overflow-x: auto; margin-top: var(--pb-space-4); border: 1px solid var(--pb-line); border-radius: var(--pb-radius); }
table { width: 100%; min-width: 690px; border-collapse: collapse; background: var(--pb-surface); font-size: 13px; }
th, td { padding: var(--pb-space-3); border-bottom: 1px solid var(--pb-line); text-align: left; vertical-align: top; }
th { color: var(--pb-text-muted); font-weight: 600; }
.record { cursor: pointer; }
.record:hover, .record:focus-visible, .selected { background: var(--pb-bg); }
.record time { text-decoration: underline; }
.target { max-width: 240px; overflow-wrap: anywhere; }
.details { display: grid; grid-template-columns: 100px minmax(0, 1fr); gap: var(--pb-space-2); margin: 0; overflow-wrap: anywhere; }
.details dt { color: var(--pb-text-muted); }
.details dd { margin: 0; min-width: 0; }
pre { margin: 0; white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; }
.pager { display: flex; justify-content: flex-end; align-items: center; gap: var(--pb-space-2); padding: var(--pb-space-4) 0; }
@media (max-width: 760px) { .body { padding: var(--pb-space-3); } .filters { grid-template-columns: repeat(2, minmax(0, 1fr)); } .search-label { grid-column: span 2; } .filter-actions { grid-column: span 2; } }
@media (max-width: 480px) { .filters { grid-template-columns: minmax(0, 1fr); } .search-label, .filter-actions { grid-column: auto; } }
</style>
