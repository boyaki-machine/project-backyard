<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { getTicketTrends, type TicketTrends, type TrendPeriod } from '../api/dashboard'
import { uiNumber, uiText } from '../locales/ui'

const props = defineProps<{ projectKey: string }>()
const period = ref<TrendPeriod>('7d')
const trends = ref<TicketTrends | null>(null)
const loading = ref(false)
const error = ref('')
const selectedDate = ref('')
let requestVersion = 0
async function load(): Promise<void> {
  const version = ++requestVersion
  loading.value = true
  error.value = ''
  trends.value = null
  selectedDate.value = ''
  try {
    const result = await getTicketTrends(props.projectKey, period.value)
    if (version === requestVersion) trends.value = result
  } catch {
    if (version === requestVersion) error.value = uiText('チケットの状態別推移を取得できませんでした')
  } finally {
    if (version === requestVersion) loading.value = false
  }
}
watch([() => props.projectKey, period], load, { immediate: true })
const hasUnknown = computed(() => trends.value?.daily.some(d => d.unknown > 0) ?? false)
const columns = computed(() => [
  { key: 'done' as const, label: uiText('消化済み') },
  { key: 'in_progress' as const, label: uiText('着手中（レビュー含む）') },
  { key: 'todo' as const, label: uiText('未着手') },
  { key: 'new_todo' as const, label: uiText('うち期間内追加') },
  ...(hasUnknown.value ? [{ key: 'unknown' as const, label: uiText('不明') }] : []),
])
type Day = TicketTrends['daily'][number]
const layers = computed(() => [
  { key: 'done', label: uiText('消化済み'), value: (d: Day) => d.done },
  { key: 'in_progress', label: uiText('着手中（レビュー含む）'), value: (d: Day) => d.in_progress },
  { key: 'todo', label: uiText('未着手（期間前から）'), value: (d: Day) => d.todo - d.new_todo },
  { key: 'new_todo', label: uiText('未着手（期間内に追加）'), value: (d: Day) => d.new_todo },
  ...(hasUnknown.value ? [{ key: 'unknown', label: uiText('不明'), value: (d: Day) => d.unknown }] : []),
])
const chartElement = ref<HTMLDivElement | null>(null)
const chartWidth = ref(720)
let chartObserver: ResizeObserver | undefined
watch(chartElement, element => {
  chartObserver?.disconnect()
  if (!element) return
  chartWidth.value = Math.max(280, element.clientWidth)
  chartObserver = new ResizeObserver(() => { chartWidth.value = Math.max(280, element.clientWidth) })
  chartObserver.observe(element)
}, { flush: 'post' })
onBeforeUnmount(() => { chartObserver?.disconnect(); ++requestVersion })
const maximum = computed(() => Math.max(1, ...(trends.value?.daily.map(d => d.done + d.in_progress + d.todo + d.unknown) ?? [])))
const ticks = computed(() => [...new Set([maximum.value, Math.floor(maximum.value / 2), 0])])
// Keep the whole period visible; long periods use fewer date labels.
const x = (index: number) => 48 + index / Math.max(1, (trends.value?.days ?? 7) - 1) * (chartWidth.value - 78)
const y = (count: number) => 180 - count / maximum.value * 150
const cumulative = (d: Day, layer: number) => layers.value.slice(0, layer + 1).reduce((sum, s) => sum + s.value(d), 0)
const paths = computed(() => layers.value.map((s, layer) => {
  const daily = trends.value?.daily ?? []
  const top = daily.map((d, i) => `${x(i)},${y(cumulative(d, layer))}`)
  const bottom = daily.map((d, i) => `${x(i)},${y(layer === 0 ? 0 : cumulative(d, layer - 1))}`).reverse()
  return { ...s, line: top.join(' '), area: [...top, ...bottom].join(' ') }
}))
const labels = computed(() => {
  const all = trends.value?.daily ?? []
  const intervals = chartWidth.value < 500 ? 2 : 4
  const stride = Math.ceil((all.length - 1) / intervals)
  return all.flatMap((d, i) => i === 0 || i === all.length - 1 ||
    (i % stride === 0 && i < all.length - Math.ceil(stride / 2)) ? [{ ...d, index: i }] : [])
})
const selected = computed(() => trends.value?.daily.find(d => d.date === selectedDate.value) ?? trends.value?.daily.at(-1))
const dayLabel = (d: Day) => `${d.date}${d.is_partial ? uiText('（途中）') : ''} · ${columns.value.map(c => `${c.label} ${uiNumber(d[c.key])}`).join(' · ')}`
function moveDay(index: number, event: KeyboardEvent) {
  const next = event.key === 'ArrowRight' ? index + 1 : event.key === 'ArrowLeft' ? index - 1 : event.key === 'Home' ? 0 : event.key === 'End' ? (trends.value?.days ?? 1) - 1 : index
  if (next === index) return
  event.preventDefault()
  const targets = (event.currentTarget as SVGElement).parentElement?.querySelectorAll<SVGElement>('.dash-trends-point')
  targets?.[Math.max(0, Math.min((targets?.length ?? 1) - 1, next))]?.focus()
}
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
  <section class="dash-trends" :aria-label="$ui('チケットの状態別推移')">
    <div class="dash-trends-head">
      <h2>{{ $ui('チケットの状態別推移') }}</h2>
      <label><span>{{ $ui('集計期間') }}</span>
        <select v-model="period">
          <option value="7d">{{ $ui('7日') }}</option>
          <option value="4w">{{ $ui('4週間') }}</option>
          <option value="3m">{{ $ui('3ヶ月') }}</option>
          <option value="6m">{{ $ui('半年') }}</option>
        </select>
      </label>
    </div>
    <p v-if="loading" role="status">{{ $ui('読み込み中…') }}</p>
    <p v-else-if="error" role="alert">{{ error }} <button type="button" @click="load">{{ $ui('再試行') }}</button></p>
    <template v-else-if="trends">
      <p class="dash-trends-period">{{ trends.start_date }}〜{{ trends.end_date }} · {{ trends.timezone }}</p>
      <p class="dash-trends-note">{{ $ui('最新時点の件数') }}</p>
      <dl class="dash-trends-totals">
        <div v-for="c in columns" :key="c.key"><dt>{{ c.label }}</dt><dd>{{ $ui('{value0}件', { value0: uiNumber(trends.latest[c.key]) }) }}</dd></div>
      </dl>
      <ul class="dash-trends-legend"><li v-for="s in layers" :key="s.key"><span class="dash-trends-swatch" :class="s.key" aria-hidden="true" />{{ s.label }}</li></ul>
      <div ref="chartElement" class="dash-trends-chart">
        <svg :viewBox="`0 0 ${chartWidth} 216`" role="group" :aria-label="$ui('日別の状態別件数グラフ')">
          <defs>
            <pattern id="dash-trends-hatch" width="5" height="5" patternUnits="userSpaceOnUse"><path d="M-1 1 L1 -1 M0 5 L5 0 M4 6 L6 4" class="dash-trends-hatch" /></pattern>
          </defs>
          <g v-for="tick in ticks" :key="tick"><line x1="48" :x2="chartWidth - 30" :y1="y(tick)" :y2="y(tick)" class="dash-trends-grid" /><text x="42" :y="y(tick) + 4" text-anchor="end">{{ uiNumber(tick) }}</text></g>
          <g v-for="s in paths" :key="s.key" :data-series="s.key">
            <polygon :points="s.area" :class="['dash-trends-area', s.key]" />
            <polyline :points="s.line" :class="['dash-trends-line', s.key]" />
          </g>
          <g>
            <circle v-for="(day, index) in trends.daily" :key="day.date" class="dash-trends-point"
              :data-date="day.date" :data-count="day.done + day.in_progress + day.todo + day.unknown"
              :cx="x(index)" :cy="y(day.done + day.in_progress + day.todo + day.unknown)" r="4"
              :tabindex="day.date === (selectedDate || trends.end_date) ? 0 : -1" :aria-label="dayLabel(day)" @focus="selectedDate = day.date" @mouseenter="selectedDate = day.date" @click="selectedDate = day.date" @keydown="moveDay(index, $event)">
              <title>{{ dayLabel(day) }}</title>
            </circle>
          </g>
          <text v-for="day in labels" :key="day.date" :x="x(day.index)" y="202" :text-anchor="day.index === 0 ? 'start' : day.index === trends.days - 1 ? 'end' : 'middle'">{{ day.date.slice(5) }}{{ day.is_partial ? '*' : '' }}</text>
        </svg>
      </div>
      <p v-if="selected" class="dash-trends-selected" aria-live="polite">{{ dayLabel(selected) }}</p>
      <details class="dash-trends-details"><summary>{{ $ui('日別の数値を表示') }}</summary>
        <div class="dash-trends-scroll" tabindex="0" :aria-label="$ui('日別の状態別件数')"><table>
          <caption>{{ $ui('日別の状態別件数') }}</caption>
          <thead><tr><th scope="col">{{ $ui('日付') }}</th><th v-for="c in columns" :key="c.key" scope="col">{{ c.label }}</th></tr></thead>
          <tbody><tr v-for="day in trends.daily" :key="day.date"><th scope="row">{{ day.date }}{{ day.is_partial ? $ui('（途中）') : '' }}</th><td v-for="c in columns" :key="c.key">{{ uiNumber(day[c.key]) }}</td></tr></tbody>
        </table></div>
      </details>
      <p v-if="hasUnknown" class="dash-trends-note">{{ $ui('履歴から状態を判定できないチケットは「不明」に含めています。') }}</p>
      <p class="dash-trends-note">{{ $ui('未着手の新規追加分は、選択期間中に作成されたチケットです。') }}</p>
      <p class="dash-trends-note">{{ $ui('現在あるストーリー・タスクを集計。削除済みは含みません。過去の状態は履歴から復元し、未記録の変更は検出できません。削除・種別・ワークフローの変更で過去の値も変わります。') }}</p>
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
.dash-trends-period, .dash-trends-note, .dash-trends-selected { font-size: 12px; color: var(--pb-text-muted); overflow-wrap: anywhere; }
.dash-trends-totals { display: flex; gap: var(--pb-space-6); flex-wrap: wrap; margin: var(--pb-space-4) 0; }
.dash-trends-totals dt { font-size: 12px; }
.dash-trends-totals dd { margin: var(--pb-space-1) 0 0; font-size: 20px; font-weight: 600; }
.dash-trends-legend { display: flex; gap: var(--pb-space-3); flex-wrap: wrap; padding: 0; list-style: none; font-size: 12px; }
.dash-trends-legend li { display: flex; gap: var(--pb-space-2); align-items: center; }
.dash-trends-swatch { width: 12px; height: 12px; border: 1px solid var(--pb-text); box-sizing: border-box; }
.dash-trends-swatch.done { background: var(--pb-text); }
.dash-trends-swatch.in_progress { background: var(--pb-text-muted); }
.dash-trends-swatch.todo { background: var(--pb-elevated); }
.dash-trends-swatch.new_todo { background: repeating-linear-gradient(135deg, transparent 0 2px, var(--pb-text) 2px 3px); }
.dash-trends-swatch.unknown { border-style: dashed; }
.dash-trends-chart svg { display: block; width: 100%; height: 216px; font-size: 12px; }
.dash-trends-chart text { fill: var(--pb-text-muted); }
.dash-trends-grid { stroke: var(--pb-line); }
.dash-trends-area.done { fill: var(--pb-text); fill-opacity: .65; }
.dash-trends-area.in_progress { fill: var(--pb-text-muted); fill-opacity: .45; }
.dash-trends-area.todo { fill: var(--pb-elevated); }
.dash-trends-area.new_todo { fill: url(#dash-trends-hatch); }
.dash-trends-area.unknown { fill: var(--pb-surface); }
.dash-trends-line { fill: none; stroke: var(--pb-text); stroke-width: 1; vector-effect: non-scaling-stroke; }
.dash-trends-line.unknown { stroke-dasharray: 4 3; }
.dash-trends-hatch { stroke: var(--pb-text-muted); stroke-width: 1; }
.dash-trends-point { fill: transparent; stroke: transparent; cursor: pointer; }
.dash-trends-point:hover, .dash-trends-point:focus { fill: var(--pb-text); stroke: var(--pb-surface); outline: none; }
.dash-trends-scroll { overflow-x: auto; max-width: 100%; }
.dash-trends-details summary { cursor: pointer; font-size: 13px; }
.dash-trends-details table { border-collapse: collapse; min-width: 560px; width: 100%; margin-top: var(--pb-space-3); font-size: 12px; }
.dash-trends-details caption { text-align: left; padding-bottom: var(--pb-space-2); }
.dash-trends-details th, .dash-trends-details td { padding: var(--pb-space-2); border-bottom: 1px solid var(--pb-line); text-align: right; white-space: nowrap; }
.dash-trends-details th:first-child { text-align: left; }
</style>
