<script setup lang="ts">
/**
 * プロジェクト設定のカレンダータブ（`GuiDesign.md` 5.9.6）。
 *
 * 基準タイムゾーン・ガントの吸着・祝日の取得元・休日の一覧の4ブロック。API は `ApiDesign.md` 5.8、
 * 基準タイムゾーンとガントの吸着の保存は `PATCH /projects/:key`（5.5）で、**それぞれ
 * のブロックだけの `[保存]`** を持つ——一般タブの `[保存]` に混ぜると、タイムゾーンを変えた人が
 * 休日の一覧を見ないまま保存する。
 *
 * **取得はボタンだけが契機である**（2026-09-27 利用者の判断）。国を選んでも、
 * 画面を開いても取りに行かない。
 */
import { computed, onMounted, ref, watch } from 'vue'

import Modal from './Modal.vue'
import { ApiError } from '../api/client'
import * as calendarApi from '../api/calendar'
import { googleCalendarSources } from '../api/calendar'
import type { ProjectCalendar, ProjectCalendarDay } from '../api/calendar'
import * as projectsApi from '../api/projects'
import { GANTT_SNAP_CHOICES, mergeGanttSnap, readGanttSnap } from '../api/projects'
import type { GanttSnapMinutes, ProjectDetail } from '../api/projects'
import { formatDateTime } from '../lib/datetime'
import { uiLocaleTag, uiText } from '../locales/ui'

const props = defineProps<{
  projectKey: string
  project: ProjectDetail
  canEdit: boolean
}>()
const emit = defineEmits<{ updated: [project: ProjectDetail] }>()

function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({ status: 0, code: 'internal_error', message: uiText('予期しないエラーが発生しました') })
}

// ── 基準タイムゾーン ─────────────────────────────────────────────
const timezone = ref(props.project.timezone)
const tzSaving = ref(false)
const tzResult = ref('')
const tzError = ref<ApiError | null>(null)

/** 選択肢はブラウザが知っている IANA 名。いまの値が無ければ先頭に足す */
const timezoneOptions = computed(() => {
  const list = typeof Intl.supportedValuesOf === 'function' ? Intl.supportedValuesOf('timeZone') : []
  const all = list.includes('UTC') ? [...list] : ['UTC', ...list]
  return all.includes(props.project.timezone) ? all : [props.project.timezone, ...all]
})

watch(
  () => props.project.timezone,
  (tz) => {
    timezone.value = tz
  },
)

async function saveTimezone(): Promise<void> {
  tzSaving.value = true
  tzResult.value = ''
  tzError.value = null
  try {
    const res = await projectsApi.updateProject(props.projectKey, props.project.version, {
      timezone: timezone.value,
    })
    emit('updated', res)
    tzResult.value = uiText('保存しました')
  } catch (e) {
    tzError.value = toApiError(e)
  } finally {
    tzSaving.value = false
  }
}

// ── ガントの吸着（5.9.6。`project.settings.gantt_snap_minutes`）──────────
const snap = ref<GanttSnapMinutes>(readGanttSnap(props.project.settings))
const snapSaving = ref(false)
const snapResult = ref('')
const snapError = ref<ApiError | null>(null)
const savedSnap = computed(() => readGanttSnap(props.project.settings))

const snapLabels: Record<GanttSnapMinutes, () => string> = {
  1440: () => uiText('日'),
  60: () => uiText('1時間'),
  30: () => uiText('30分'),
  15: () => uiText('15分'),
}

watch(savedSnap, (v) => {
  snap.value = v
})

/**
 * **取得した `settings` の吸着の単位だけを差し替えて全体を送る**（5.5 の丸ごと置き換え）。
 * `If-Match` は取得時の `version`——基準タイムゾーンを保存した後でも、`updated` で
 * 親が持つ `project` が差し替わっているので、その値を使う。
 */
async function saveSnap(): Promise<void> {
  snapSaving.value = true
  snapResult.value = ''
  snapError.value = null
  try {
    const res = await projectsApi.updateProject(props.projectKey, props.project.version, {
      settings: mergeGanttSnap(props.project.settings, snap.value),
    })
    emit('updated', res)
    snapResult.value = uiText('保存しました')
  } catch (e) {
    snapError.value = toApiError(e)
  } finally {
    snapSaving.value = false
  }
}

// ── 祝日の取得元 ─────────────────────────────────────────────────
type Choice = 'none' | 'google' | 'file'
const calendar = ref<ProjectCalendar | null>(null)
const choice = ref<Choice>('none')
const googleId = ref(googleCalendarSources[0].id)
const sourceBusy = ref(false)
const sourceResult = ref('')
const sourceError = ref<ApiError | null>(null)
const fileInput = ref<HTMLInputElement | null>(null)

const source = computed(() => calendar.value?.source ?? null)

function applyCalendar(c: ProjectCalendar): void {
  calendar.value = c
  choice.value = c.source?.kind ?? 'none'
  if (c.source?.kind === 'google' && c.source.google_id) googleId.value = c.source.google_id
}

async function loadCalendar(): Promise<void> {
  sourceError.value = null
  try {
    applyCalendar(await calendarApi.getCalendar(props.projectKey))
  } catch (e) {
    sourceError.value = toApiError(e)
  }
}

async function runSource(op: () => Promise<ProjectCalendar>, done: (c: ProjectCalendar) => string): Promise<void> {
  sourceBusy.value = true
  sourceResult.value = ''
  sourceError.value = null
  try {
    const c = await op()
    applyCalendar(c)
    sourceResult.value = done(c)
    await loadDays()
  } catch (e) {
    sourceError.value = toApiError(e)
  } finally {
    sourceBusy.value = false
  }
}

/** ラジオの切り替え。ファイルはファイルを選んだ時点で送る */
function onChoice(next: Choice): void {
  choice.value = next
  if (next === 'none' && source.value) {
    void runSource(() => calendarApi.putSource(props.projectKey, null), () => uiText('祝日の取得元を外しました'))
  } else if (next === 'google' && (source.value?.kind !== 'google' || source.value.google_id !== googleId.value)) {
    void selectGoogle()
  }
}

function selectGoogle(): Promise<void> {
  return runSource(
    () => calendarApi.putSource(props.projectKey, googleId.value),
    () => uiText('取得元を選びました。「祝日を取得」で取り込みます'),
  )
}

function fetchNow(): Promise<void> {
  return runSource(
    () => calendarApi.fetchHolidays(props.projectKey),
    () => uiText('祝日を取得しました'),
  )
}

async function onFile(ev: Event): Promise<void> {
  const input = ev.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  const text = await file.text()
  await runSource(
    () => calendarApi.importIcs(props.projectKey, file.name, text),
    (c) =>
      uiText('{days}日を取り込みました（読み飛ばし {skipped}件）', {
        days: c.imported?.days ?? 0,
        skipped: c.imported?.skipped ?? 0,
      }),
  )
}

/** 待ち時間のあいだは取得できない（`ApiDesign.md` 5.8.3） */
const nextFetchAt = computed(() => {
  const next = source.value?.next_fetch_at
  return next && new Date(next).getTime() > Date.now() ? next : null
})

// ── 休日の一覧 ───────────────────────────────────────────────────
const year = ref(new Date().getFullYear())
const days = ref<ProjectCalendarDay[]>([])
const daysLoading = ref(false)
const daysError = ref<ApiError | null>(null)
const dayBusy = ref(false)

/** 土日だけの日は出さない（毎年同じ並びで一覧が埋まるため）。上書きのある土日は出す */
const listed = computed(() => days.value.filter((d) => d.reason !== 'weekend'))

async function loadDays(): Promise<void> {
  daysLoading.value = true
  daysError.value = null
  try {
    const res = await calendarApi.listDays(props.projectKey, `${year.value}-01-01`, `${year.value + 1}-01-01`)
    days.value = res.days
  } catch (e) {
    daysError.value = toApiError(e)
  } finally {
    daysLoading.value = false
  }
}

watch(year, () => void loadDays())

/** `YYYY-MM-DD` をタイムゾーンに通さずに曜日へ（`GuiDesign.md` 7.5） */
function weekdayOf(day: string): number {
  const [y, m, d] = day.split('-').map(Number)
  return new Date(Date.UTC(y, m - 1, d)).getUTCDay()
}

function dayLabel(day: string): string {
  const [y, m, d] = day.split('-').map(Number)
  const wd = new Intl.DateTimeFormat(uiLocaleTag(), { weekday: 'short', timeZone: 'UTC' }).format(
    new Date(Date.UTC(y, m - 1, d)),
  )
  return `${day.slice(5, 7)}/${day.slice(8, 10)}（${wd}）`
}

function nameOf(d: ProjectCalendarDay): string {
  return d.override?.name || d.events.map((e) => e.name).join('・')
}

function kindOf(d: ProjectCalendarDay): string {
  if (d.override) return uiText('手動')
  if (d.events.some((e) => e.kind === 'holiday')) return uiText('祝日')
  return uiText('行事')
}

/** 上書きが無いときの判定（`DbDesign.md` 6.23 の表の2〜4段） */
function baseHoliday(d: ProjectCalendarDay): boolean {
  const wd = weekdayOf(d.day)
  return d.events.some((e) => e.kind === 'holiday') || wd === 0 || wd === 6
}

async function runDay(op: () => Promise<unknown>): Promise<void> {
  dayBusy.value = true
  daysError.value = null
  try {
    await op()
    await loadDays()
  } catch (e) {
    daysError.value = toApiError(e)
  } finally {
    dayBusy.value = false
  }
}

/** 休日扱いの切り替え。**取得元の判定に戻るなら上書きを外す** */
function toggle(d: ProjectCalendarDay): Promise<void> {
  const next = !d.is_holiday
  if (d.override && next === baseHoliday(d)) {
    return runDay(() => calendarApi.deleteDay(props.projectKey, d.day))
  }
  return runDay(() =>
    calendarApi.putDay(props.projectKey, d.day, { is_holiday: next, name: d.override?.name ?? undefined }),
  )
}

function clearOverride(d: ProjectCalendarDay): Promise<void> {
  return runDay(() => calendarApi.deleteDay(props.projectKey, d.day))
}

// 日を追加するモーダル
const addOpen = ref(false)
const addDate = ref('')
const addName = ref('')
const addHoliday = ref(true)

function openAdd(): void {
  addDate.value = `${year.value}-01-01`
  addName.value = ''
  addHoliday.value = true
  addOpen.value = true
}

async function submitAdd(): Promise<void> {
  if (!/^\d{4}-\d{2}-\d{2}$/u.test(addDate.value)) return
  addOpen.value = false
  const target = Number(addDate.value.slice(0, 4))
  await runDay(() =>
    calendarApi.putDay(props.projectKey, addDate.value, {
      is_holiday: addHoliday.value,
      name: addName.value.trim() || undefined,
    }),
  )
  if (target !== year.value) year.value = target
}

onMounted(() => {
  void loadCalendar()
  void loadDays()
})
</script>

<template>
  <div class="blocks">
    <section class="block">
      <h2 class="block-title">{{ $ui('基準タイムゾーン') }}</h2>
      <div class="row">
        <select v-model="timezone" class="tz" :disabled="!canEdit || tzSaving" :aria-label="$ui('基準タイムゾーン')">
          <option v-for="tz in timezoneOptions" :key="tz" :value="tz">{{ tz }}</option>
        </select>
        <span class="spacer"></span>
        <button
          v-if="canEdit"
          type="button"
          class="primary"
          :disabled="tzSaving || timezone === project.timezone"
          @click="saveTimezone"
        >{{ $ui('保存') }}</button>
      </div>
      <p class="hint">
        ⓘ {{ $ui('期限超過の判定と、終日の予定の区切りに使います。変えても既存の予定の日時は動きません') }}
      </p>
      <p v-if="tzResult" class="ok" role="status">✓ {{ tzResult }}</p>
      <p v-if="tzError" class="alert" role="alert">✕ {{ tzError.message }}</p>
    </section>

    <section class="block">
      <h2 class="block-title">{{ $ui('ガントの吸着') }}</h2>
      <div class="row">
        <select v-model.number="snap" class="snap" :disabled="!canEdit || snapSaving" :aria-label="$ui('ガントの吸着')">
          <option v-for="m in GANTT_SNAP_CHOICES" :key="m" :value="m">{{ snapLabels[m]() }}</option>
        </select>
        <span class="spacer"></span>
        <button
          v-if="canEdit"
          type="button"
          class="primary"
          :disabled="snapSaving || snap === savedSnap"
          @click="saveSnap"
        >{{ $ui('保存') }}</button>
      </div>
      <p class="hint">
        ⓘ {{ $ui('ガントでバーを動かすときに揃える単位です。区切りは基準タイムゾーンの時刻で、Alt（Mac では Option）を押している間は外れます') }}
      </p>
      <p v-if="snapResult" class="ok" role="status">✓ {{ snapResult }}</p>
      <p v-if="snapError" class="alert" role="alert">✕ {{ snapError.message }}</p>
    </section>

    <section class="block">
      <h2 class="block-title">{{ $ui('祝日の取得元') }}</h2>
      <fieldset class="choices" :disabled="!canEdit || sourceBusy">
        <legend class="sr-only">{{ $ui('祝日の取得元') }}</legend>
        <label class="choice">
          <input type="radio" name="cal-source" :checked="choice === 'none'" @change="onChoice('none')" />
          {{ $ui('使わない') }}
        </label>
        <label class="choice">
          <input type="radio" name="cal-source" :checked="choice === 'google'" @change="onChoice('google')" />
          {{ $ui('Google の祝日カレンダー') }}
          <select
            v-model="googleId"
            class="country"
            :disabled="choice !== 'google'"
            :aria-label="$ui('国')"
            @change="selectGoogle"
          >
            <option v-for="c in googleCalendarSources" :key="c.id" :value="c.id">{{ $ui(c.label) }}</option>
          </select>
        </label>
        <label class="choice">
          <input type="radio" name="cal-source" :checked="choice === 'file'" @change="onChoice('file')" />
          {{ $ui('ファイルから取り込む') }}
          <button
            type="button"
            class="secondary small"
            :disabled="choice !== 'file'"
            @click="fileInput?.click()"
          >{{ $ui('ファイルを選択') }}</button>
          <input ref="fileInput" type="file" accept=".ics,text/calendar" hidden @change="onFile" />
        </label>
      </fieldset>

      <div v-if="source" class="row status">
        <p class="statusline">
          <span>{{ source.name ?? $ui('（名前なし）') }}</span>
          <span class="sep">・</span>
          <span>{{ $ui('祝日 {n}件', { n: source.holiday_count }) }}</span>
          <span class="sep">・</span>
          <span>{{ $ui('行事 {n}件', { n: source.observance_count }) }}</span>
          <span class="sep">・</span>
          <span v-if="source.fetched_at">{{ $ui('最終取得 {at}', { at: formatDateTime(source.fetched_at) }) }}</span>
          <span v-else>{{ $ui('未取得') }}</span>
        </p>
        <span class="spacer"></span>
        <button
          v-if="canEdit && source.kind === 'google'"
          type="button"
          class="secondary"
          :disabled="sourceBusy || nextFetchAt !== null"
          @click="fetchNow"
        >{{ $ui('祝日を取得') }}</button>
      </div>
      <p v-if="nextFetchAt" class="hint">{{ $ui('次は {at} 以降に取得できます', { at: formatDateTime(nextFetchAt) }) }}</p>
      <p v-if="source?.last_error" class="warn" role="alert">{{ source.last_error }}</p>
      <p v-if="sourceResult" class="ok" role="status">✓ {{ sourceResult }}</p>
      <p v-if="sourceError" class="alert" role="alert">✕ {{ sourceError.message }}</p>
    </section>

    <section class="block">
      <div class="block-head">
        <h2 class="block-title">{{ $ui('休日の一覧') }}</h2>
        <div class="year">
          <button type="button" class="secondary small" :aria-label="$ui('前の年')" @click="year--">◀</button>
          <span class="year-num">{{ year }}</span>
          <button type="button" class="secondary small" :aria-label="$ui('次の年')" @click="year++">▶</button>
        </div>
        <span class="spacer"></span>
        <button v-if="canEdit" type="button" class="secondary" :disabled="dayBusy" @click="openAdd">
          {{ $ui('+ 日を追加') }}
        </button>
      </div>
      <p v-if="daysError" class="alert" role="alert">✕ {{ daysError.message }}</p>
      <div v-if="daysLoading && days.length === 0" class="loading" aria-busy="true">
        <span class="skeleton"></span>
        <span class="skeleton short"></span>
      </div>
      <p v-else-if="listed.length === 0" class="hint">{{ $ui('この年の祝日・行事・上書きはありません') }}</p>
      <table v-else class="table">
        <thead>
          <tr>
            <th scope="col">{{ $ui('日付') }}</th>
            <th scope="col">{{ $ui('名前') }}</th>
            <th scope="col">{{ $ui('種別') }}</th>
            <th scope="col">{{ $ui('休日扱い') }}</th>
            <th scope="col"><span class="sr-only">{{ $ui('操作') }}</span></th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="d in listed" :key="d.day" :class="{ observance: !d.is_holiday }">
            <td class="date">{{ dayLabel(d.day) }}</td>
            <td>{{ nameOf(d) }}</td>
            <td class="kind">{{ kindOf(d) }}</td>
            <td>
              <input
                type="checkbox"
                :checked="d.is_holiday"
                :disabled="!canEdit || dayBusy"
                :aria-label="$ui('{day} を休日にする', { day: d.day })"
                @change="toggle(d)"
              />
            </td>
            <td class="actions-cell">
              <button
                v-if="canEdit && d.override"
                type="button"
                class="secondary small"
                :disabled="dayBusy"
                @click="clearOverride(d)"
              >{{ $ui('上書きを外す') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </section>

    <Modal v-if="addOpen" :title="$ui('日を追加')" @close="addOpen = false">
      <form class="add-form" @submit.prevent="submitAdd">
        <label class="field">
          <span class="label">{{ $ui('日付') }}</span>
          <input v-model="addDate" type="date" required />
        </label>
        <label class="field">
          <span class="label">{{ $ui('名前') }}</span>
          <input v-model="addName" type="text" maxlength="200" :placeholder="$ui('創立記念日')" />
        </label>
        <fieldset class="choices">
          <legend class="sr-only">{{ $ui('休日扱い') }}</legend>
          <label class="choice"><input v-model="addHoliday" type="radio" :value="true" /> {{ $ui('休日にする') }}</label>
          <label class="choice"><input v-model="addHoliday" type="radio" :value="false" /> {{ $ui('平日にする') }}</label>
        </fieldset>
        <div class="actions">
          <button type="button" class="secondary" @click="addOpen = false">{{ $ui('キャンセル') }}</button>
          <button type="submit" class="primary">{{ $ui('追加') }}</button>
        </div>
      </form>
    </Modal>
  </div>
</template>

<style scoped>
.blocks {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
  max-width: 880px;
}

.block {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
  padding: var(--pb-space-4);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
}

.block-title {
  margin: 0;
  font-size: 14px;
  font-weight: 600;
}

.block-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-3);
}

/* 入力（MembershipModal と同じ形） */
select,
input[type='text'],
input[type='date'] {
  height: 32px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-3);
}

.spacer {
  flex: 1;
}

.tz,
.snap {
  min-width: 0;
  max-width: 100%;
}

.choices {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  margin: 0;
  padding: 0;
  border: 0;
}

.choice {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-2);
  font-size: 14px;
}

.statusline {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-1);
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
  font-variant-numeric: tabular-nums;
}

.sep {
  color: var(--pb-text-muted);
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
  line-height: 1.7;
}

/* warning は面で表す（8.4.1）。文字はベース色のまま */
.warn {
  margin: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-warning-border);
  border-radius: var(--pb-radius);
  background: var(--pb-warning-bg);
  font-size: 13px;
}

.alert {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-danger-bg);
  color: var(--pb-danger-text);
  font-size: 13px;
}

.ok {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.year {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}

.year-num {
  font-variant-numeric: tabular-nums;
}

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

td {
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
  font-size: 14px;
}

tr.observance td {
  color: var(--pb-text-muted);
}

.date {
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.kind {
  white-space: nowrap;
}

.actions-cell {
  text-align: right;
  white-space: nowrap;
}

.add-form {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
}

.label {
  font-size: 13px;
  font-weight: 600;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--pb-space-3);
}

.loading {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
}

.skeleton {
  display: block;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.skeleton.short {
  width: 60%;
}

.sr-only {
  position: absolute;
  overflow: hidden;
  width: 1px;
  height: 1px;
  clip-path: inset(50%);
  white-space: nowrap;
}

button:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
