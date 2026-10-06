<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import PageHeader from '../components/PageHeader.vue'
import { getAgentUsage, listAgentRuns } from '../api/agentUsage'
import type { AgentUsage, AgentUsageRunList } from '../api/agentUsage'
import { useAuthStore } from '../stores/auth'
import { uiNumber, uiText, uiLocaleTag } from '../locales/ui'

const route = useRoute()
const auth = useAuthStore()
const key = computed(() => String(route.params.key))
const canRead = computed(() => auth.canInProject(key.value, 'ticket.view'))
const from = ref('')
const to = ref('')
const summary = ref<AgentUsage | null>(null)
const runs = ref<AgentUsageRunList | null>(null)
const loading = ref(false)
const error = ref('')
let request = 0
const maxTokens = computed(() => Math.max(1, ...summary.value?.daily.map(day => Math.max(day.run_tokens, day.evaluation_tokens)) ?? []))
function measured(sum: number, count: number, missing: number): string { return count > 0 && count === missing ? uiText('未計測') : uiNumber(sum) }
function value(n: number | null): string { return n === null ? uiText('未計測') : uiNumber(n) }
const resultSources: Record<string, string> = { completed: '完了の報告', partial: '一部完了の報告', blocked: '進められない報告' }
function resultLabel(status: string): string { return uiText(resultSources[status] ?? status) }
async function fetch(page = 1, initial = false): Promise<void> {
  const mine = ++request
  if (!canRead.value) { summary.value = null; runs.value = null; loading.value = false; return }
  loading.value = true; error.value = ''
  const project = key.value
  const period = initial ? {} : { from_date: from.value, to_date: to.value }
  try {
    const [usage, details] = await Promise.all([getAgentUsage(project, period), listAgentRuns(project, { ...period, page })])
    if (mine !== request) return
    summary.value = usage; runs.value = details; from.value = usage.from_date; to.value = usage.to_date
  } catch (err) {
    if (mine !== request) return
    summary.value = null; runs.value = null
    error.value = err instanceof Error ? err.message : uiText('実績を取得できませんでした')
  } finally { if (mine === request) loading.value = false }
}
watch([key, canRead], () => { from.value = ''; to.value = ''; void fetch(1, true) }, { immediate: true })
</script>

<template>
  <div class="insights-page">
    <PageHeader :title="$ui('進捗分析')" />
    <main class="usage-content">
      <h2>{{ $ui('エージェント実績') }}</h2>
      <p v-if="!canRead" role="status">{{ $ui('実績を読む権限がありません') }}</p>
      <template v-else>
        <form class="period" @submit.prevent="fetch(1)">
          <label>{{ $ui('開始日') }}<input v-model="from" type="date" required /></label>
          <label>{{ $ui('終了日') }}<input v-model="to" type="date" required /></label>
          <button type="submit" :disabled="loading">{{ $ui('更新') }}</button>
          <span v-if="summary">{{ summary.timezone }}</span>
        </form>
        <p>{{ $ui('再提出を含む自己申告の実績です。時間の合計はプロジェクトの経過時間とは異なります。') }}</p>
        <p v-if="loading" role="status">{{ $ui('読み込み中…') }}</p>
        <p v-else-if="error" role="alert">{{ error }}</p>
        <template v-else-if="summary && runs">
          <dl class="totals">
            <div><dt>{{ $ui('実行件数') }}</dt><dd>{{ uiNumber(summary.totals.runs_count) }}</dd></div>
            <div><dt>{{ $ui('対象チケット数') }}</dt><dd>{{ uiNumber(summary.totals.tickets_count) }}</dd></div>
            <div><dt>{{ $ui('実行トークン') }}</dt><dd>{{ measured(summary.totals.run_tokens, summary.totals.runs_count, summary.totals.run_tokens_missing) }}</dd><small>{{ $ui('未計測 {count}件', { count: summary.totals.run_tokens_missing }) }}</small></div>
            <div><dt>{{ $ui('評価トークン') }}</dt><dd>{{ measured(summary.totals.evaluation_tokens, summary.totals.evaluation_count, summary.totals.evaluation_tokens_missing) }}</dd><small>{{ $ui('未計測 {count}件', { count: summary.totals.evaluation_tokens_missing }) }}</small></div>
            <div><dt>{{ $ui('自己申告時間（分）') }}</dt><dd>{{ measured(summary.totals.wall_clock_min, summary.totals.runs_count, summary.totals.wall_clock_min_missing) }}</dd><small>{{ $ui('未計測 {count}件', { count: summary.totals.wall_clock_min_missing }) }}</small></div>
            <div><dt>{{ $ui('ターン数') }}</dt><dd>{{ measured(summary.totals.turns, summary.totals.runs_count, summary.totals.turns_missing) }}</dd><small>{{ $ui('未計測 {count}件', { count: summary.totals.turns_missing }) }}</small></div>
          </dl>
          <p v-if="!summary.totals.runs_count && !summary.totals.evaluation_count">{{ $ui('この期間の記録はありません') }}</p>
          <h3>{{ $ui('日別トークン') }}</h3>
          <div class="table-scroll" tabindex="0" :aria-label="$ui('日別トークン')">
            <table class="daily"><thead><tr><th>{{ $ui('日付') }}</th><th>{{ $ui('実行トークン') }}</th><th>{{ $ui('評価トークン') }}</th><th>{{ $ui('未計測') }}</th></tr></thead>
              <tbody><tr v-for="day in summary.daily" :key="day.date"><th>{{ day.date }}</th><td><span class="bar" :style="{ width: `${day.run_tokens / maxTokens * 100}%` }" />{{ measured(day.run_tokens, day.runs_count, day.run_tokens_missing) }}</td><td><span class="bar evaluation" :style="{ width: `${day.evaluation_tokens / maxTokens * 100}%` }" />{{ measured(day.evaluation_tokens, day.evaluation_count, day.evaluation_tokens_missing) }}</td><td>{{ uiNumber(day.run_tokens_missing + day.evaluation_tokens_missing) }}</td></tr></tbody>
            </table>
          </div>
          <h3>{{ $ui('モデル別') }}</h3>
          <div class="table-scroll" tabindex="0" :aria-label="$ui('モデル別')">
            <table><thead><tr><th>{{ $ui('種別') }}</th><th>{{ $ui('モデル') }}</th><th>{{ $ui('版') }}</th><th>{{ $ui('実行件数') }}</th><th>{{ $ui('実行トークン') }}</th><th>{{ $ui('評価トークン') }}</th><th>{{ $ui('未計測') }}</th></tr></thead>
              <tbody><tr v-for="(model, index) in summary.models" :key="index"><td>{{ model.client_kind ?? $ui('不明') }}</td><td>{{ model.model_name ?? $ui('不明') }}</td><td>{{ model.model_version ?? $ui('不明') }}</td><td>{{ uiNumber(model.runs_count) }}</td><td>{{ measured(model.run_tokens, model.runs_count, model.run_tokens_missing) }}</td><td>{{ measured(model.evaluation_tokens, model.evaluation_count, model.evaluation_tokens_missing) }}</td><td>{{ uiNumber(model.run_tokens_missing + model.evaluation_tokens_missing) }}</td></tr></tbody>
            </table>
          </div>
          <h3>{{ $ui('実行明細') }}</h3>
          <div class="table-scroll" tabindex="0" :aria-label="$ui('実行明細')">
            <table><thead><tr><th>{{ $ui('チケット') }}</th><th>{{ $ui('提出日時') }}</th><th>{{ $ui('結果') }}</th><th>{{ $ui('モデル') }}</th><th>{{ $ui('トークン') }}</th><th>{{ $ui('ターン数') }}</th><th>{{ $ui('自己申告時間（分）') }}</th></tr></thead>
              <tbody><tr v-for="run in runs.items" :key="run.run_id"><td><RouterLink :to="`/p/${encodeURIComponent(key)}/tickets/${run.seq}`">#{{ run.seq }}</RouterLink></td><td>{{ new Date(run.submitted_at).toLocaleString(uiLocaleTag(), { hour12: false }) }}</td><td>{{ resultLabel(run.status) }}</td><td>{{ run.model_name ?? $ui('不明') }} {{ run.model_version }}</td><td>{{ value(run.tokens) }}</td><td>{{ value(run.turns) }}</td><td>{{ value(run.wall_clock_min) }}</td></tr></tbody>
            </table>
          </div>
          <nav class="pager" :aria-label="$ui('実行明細')"><button :disabled="runs.page <= 1 || loading" @click="fetch(runs.page - 1)">{{ $ui('前へ') }}</button><span>{{ runs.page }} / {{ runs.total_pages }} · {{ $ui('{count}件', { count: runs.total }) }}</span><button :disabled="runs.page >= runs.total_pages || loading" @click="fetch(runs.page + 1)">{{ $ui('次へ') }}</button></nav>
        </template>
      </template>
    </main>
  </div>
</template>

<style scoped>
.insights-page { min-width: 0; width: 100%; }
.usage-content { padding: 20px; max-width: 1200px; margin: auto; min-width: 0; }
.period { display: flex; flex-wrap: wrap; align-items: end; gap: 12px; }
.period label { display: flex; flex-direction: column; gap: 4px; }
.period input { width: 150px; }
.totals { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 16px; }
.totals div { padding: 12px; border: 1px solid var(--pb-border); border-radius: 4px; }
.totals dd { margin: 6px 0; font-size: 22px; }
.totals small { color: var(--pb-text-muted); }
.table-scroll { overflow-x: auto; max-width: 100%; }
table { border-collapse: collapse; width: 100%; text-align: left; font-size: 13px; }
th, td { padding: 8px; border-bottom: 1px solid var(--pb-border); white-space: nowrap; }
.daily td { min-width: 160px; }
.bar { display: block; height: 6px; background: var(--pb-text); min-width: 0; }
.bar.evaluation { opacity: .4; }
.pager { display: flex; align-items: center; gap: 12px; margin-top: 12px; flex-wrap: wrap; }
@media (max-width: 600px) { .usage-content { padding: 12px; } .totals { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
</style>
