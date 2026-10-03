<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { getTicketTrends, type TicketTrends } from '../api/dashboard'
import { uiNumber, uiText } from '../locales/ui'

const props = defineProps<{ projectKey: string }>()
const days = ref<7 | 28>(7)
const trends = ref<TicketTrends | null>(null)
const loading = ref(false)
const error = ref('')
let requestVersion = 0

// A slower response from an old project/period must never overwrite the new period.
async function load(): Promise<void> {
  const version = ++requestVersion
  const key = props.projectKey
  const period = days.value
  loading.value = true
  error.value = ''
  trends.value = null
  try {
    const result = await getTicketTrends(key, period)
    if (version === requestVersion) trends.value = result
  } catch {
    if (version === requestVersion) error.value = uiText('期間内の増加・消化を取得できませんでした')
  } finally {
    if (version === requestVersion) loading.value = false
  }
}
watch([() => props.projectKey, days], load, { immediate: true })

const series = computed(() => [
  { key: 'created' as const, label: uiText('新規') },
  { key: 'completed' as const, label: uiText('完了') },
  { key: 'reopened' as const, label: uiText('再オープン') },
])
const maximum = computed(() => Math.max(1, ...(trends.value?.daily.flatMap(d => [d.created, d.completed, d.reopened]) ?? [])))
const ticks = computed(() => [...new Set([maximum.value, Math.floor(maximum.value / 2), 0])])
const chartWidth = computed(() => Math.max(400, (trends.value?.days ?? 7) * 40 + 60))
const y = (count: number) => 180 - count / maximum.value * 150
const updatedAt = computed(() => {
  if (!trends.value) return ''
  const parts = new Intl.DateTimeFormat('en-CA', {
    timeZone: trends.value.timezone, year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
  }).formatToParts(trends.value.as_of)
  const part = (type: string) => parts.find(p => p.type === type)?.value ?? ''
  return `${part('year')}-${part('month')}-${part('day')} ${part('hour')}:${part('minute')}`
})
</script>

<template>
  <section class="dash-trends" :aria-label="$ui('期間内の増加・消化')">
    <div class="dash-trends-head">
      <h2>{{ $ui('期間内の増加・消化') }}</h2>
      <label>
        <span>{{ $ui('集計期間') }}</span>
        <select v-model="days">
          <option :value="7">{{ $ui('直近7日') }}</option>
          <option :value="28">{{ $ui('直近28日') }}</option>
        </select>
      </label>
    </div>
    <p v-if="loading" role="status">{{ $ui('読み込み中…') }}</p>
    <p v-else-if="error" role="alert">
      {{ error }} <button type="button" @click="load">{{ $ui('再試行') }}</button>
    </p>
    <template v-else-if="trends">
      <p class="dash-trends-period">{{ trends.start_date }}〜{{ trends.end_date }} · {{ trends.timezone }}</p>
      <dl class="dash-trends-totals">
        <div v-for="s in series" :key="s.key">
          <dt><span class="dash-trends-swatch" :class="s.key" aria-hidden="true" />{{ s.label }}</dt>
          <dd>{{ $ui('{value0}回', { value0: uiNumber(trends.totals[s.key]) }) }}</dd>
        </div>
      </dl>
      <div class="dash-trends-scroll" tabindex="0" :aria-label="$ui('日別の増加・消化グラフ')">
        <svg :width="chartWidth" height="216" :viewBox="`0 0 ${chartWidth} 216`" role="img" :aria-label="$ui('日別の増加・消化グラフ')">
          <defs>
            <pattern id="dash-trends-hatch" width="4" height="4" patternUnits="userSpaceOnUse">
              <path d="M-1 1 L1 -1 M0 4 L4 0 M3 5 L5 3" class="dash-trends-hatch" />
            </pattern>
          </defs>
          <g v-for="tick in ticks" :key="tick">
            <line x1="38" :x2="chartWidth - 8" :y1="y(tick)" :y2="y(tick)" class="dash-trends-grid" />
            <text x="32" :y="y(tick) + 4" text-anchor="end">{{ uiNumber(tick) }}</text>
          </g>
          <g v-for="(day, index) in trends.daily" :key="day.date">
            <rect v-for="(s, offset) in series" :key="s.key" :class="['dash-trends-bar', s.key]"
              :data-date="day.date" :data-series="s.key" :data-count="day[s.key]"
              :x="44 + index * 40 + offset * 9" :y="y(day[s.key])" width="7" :height="180 - y(day[s.key])">
              <title>{{ day.date }} {{ s.label }} {{ $ui('{value0}回', { value0: uiNumber(day[s.key]) }) }}</title>
            </rect>
            <text :x="56 + index * 40" y="202" text-anchor="middle">{{ day.date.slice(5) }}{{ day.is_partial ? '*' : '' }}</text>
          </g>
        </svg>
      </div>
      <details class="dash-trends-details">
        <summary>{{ $ui('日別の数値を表示') }}</summary>
        <div class="dash-trends-scroll" tabindex="0" :aria-label="$ui('日別の増加・消化の数値')">
          <table>
            <caption>{{ $ui('日別の増加・消化の数値') }}</caption>
            <thead><tr>
              <th scope="col">{{ $ui('日付') }}</th>
              <th v-for="s in series" :key="s.key" scope="col">{{ s.label }}</th>
              <th v-if="trends.totals.unclassified_transitions > 0" scope="col">{{ $ui('分類できない状態変更') }}</th>
            </tr></thead>
            <tbody><tr v-for="day in trends.daily" :key="day.date">
              <th scope="row">{{ day.date }}{{ day.is_partial ? $ui('（途中）') : '' }}</th>
              <td v-for="s in series" :key="s.key">{{ uiNumber(day[s.key]) }}</td>
              <td v-if="trends.totals.unclassified_transitions > 0">{{ uiNumber(day.unclassified_transitions) }}</td>
            </tr></tbody>
          </table>
        </div>
      </details>
      <p v-if="trends.totals.unclassified_transitions > 0" class="dash-trends-note">
        {{ $ui('{value0}回の状態変更を分類できないため、完了・再オープンに含めていません', { value0: uiNumber(trends.totals.unclassified_transitions) }) }}
      </p>
      <p class="dash-trends-note">{{ $ui('現在あるストーリー・タスクの、記録された操作を集計。削除済み・未記録の操作は含みません。削除・種別・ワークフローの変更で過去の値も変わる場合があります。') }}</p>
      <p class="dash-trends-note">{{ $ui('今日の値は途中です（*）。更新時点：{value0}', { value0: updatedAt }) }} · {{ trends.timezone }}</p>
    </template>
  </section>
</template>

<style scoped>
.dash-trends { border: 1px solid var(--pb-border); border-radius: var(--pb-radius); background: var(--pb-surface); padding: var(--pb-space-4); min-width: 0; }
.dash-trends-head { display: flex; align-items: center; justify-content: space-between; gap: var(--pb-space-3); flex-wrap: wrap; }
.dash-trends-head h2 { margin: 0; font-size: 13px; color: var(--pb-text-muted); }
.dash-trends-head label { display: flex; gap: var(--pb-space-2); align-items: center; font-size: 12px; flex-wrap: wrap; }
.dash-trends-head select { min-height: 32px; max-width: 100%; }
.dash-trends-period, .dash-trends-note { font-size: 12px; color: var(--pb-text-muted); overflow-wrap: anywhere; }
.dash-trends-totals { display: flex; gap: var(--pb-space-6); flex-wrap: wrap; margin: var(--pb-space-4) 0; }
.dash-trends-totals dt { display: flex; align-items: center; gap: var(--pb-space-2); font-size: 12px; }
.dash-trends-totals dd { margin: var(--pb-space-1) 0 0; font-size: 20px; font-weight: 600; }
.dash-trends-swatch { display: inline-block; width: 12px; height: 12px; border: 1px solid var(--pb-text); box-sizing: border-box; }
.dash-trends-swatch.created { background: var(--pb-text-muted); }
.dash-trends-swatch.completed { background: var(--pb-text); }
.dash-trends-swatch.reopened { background: repeating-linear-gradient(135deg, transparent 0 2px, var(--pb-text) 2px 3px); }
.dash-trends-scroll { overflow-x: auto; max-width: 100%; }
.dash-trends-scroll svg { display: block; font-size: 10px; }
.dash-trends-scroll text { fill: var(--pb-text-muted); }
.dash-trends-grid { stroke: var(--pb-line); }
.dash-trends-bar.created { fill: var(--pb-text-muted); }
.dash-trends-bar.completed { fill: var(--pb-text); }
.dash-trends-bar.reopened { fill: url(#dash-trends-hatch); stroke: var(--pb-text); stroke-width: 1px; }
.dash-trends-hatch { stroke: var(--pb-text); stroke-width: 1px; }
.dash-trends-details summary { cursor: pointer; font-size: 13px; }
.dash-trends-details table { border-collapse: collapse; min-width: 400px; width: 100%; margin-top: var(--pb-space-3); font-size: 12px; }
.dash-trends-details caption { text-align: left; padding-bottom: var(--pb-space-2); }
.dash-trends-details th, .dash-trends-details td { padding: var(--pb-space-2); border-bottom: 1px solid var(--pb-line); text-align: right; white-space: nowrap; }
.dash-trends-details th:first-child { text-align: left; }
</style>
