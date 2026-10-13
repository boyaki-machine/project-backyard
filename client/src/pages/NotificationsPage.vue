<script setup lang="ts">
/**
 * 通知（`GuiDesign.md` 5.16）。自分宛ての通知を読み、遷移先へ移る。
 *
 * **この画面は使い手を知らない。** 題名・本文・遷移先は通知が持ち、ここは並べるだけである。
 *
 * **遷移する場所と、既読にするだけの場所を分ける**（5.16「押したときの振る舞い」）。
 * 行のどこを押しても遷移すると、読むつもりで押した利用者が画面ごと移されてしまう。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import {
  deleteNotification,
  listNotifications,
  readAllNotifications,
  setNotificationRead,
  type Notification,
  type NotificationFilter,
} from '../api/notifications'
import EmptyState from '../components/EmptyState.vue'
import PageHeader from '../components/PageHeader.vue'
import { currentTimezone, formatDate, isoOf } from '../lib/datetime'
import { uiLocaleTag, uiText } from '../locales/ui'
import { formatUnreadCount, useNotificationStore } from '../stores/notifications'

/** 1回に引く件数。末尾の [さらに表示] で続きを足す（5.16「一覧」） */
const PER_PAGE = 50

const route = useRoute()
const router = useRouter()
const store = useNotificationStore()

const filter = computed<NotificationFilter>(() => (route.query.filter === 'unread' ? 'unread' : 'all'))
const items = ref<Notification[]>([])
const total = ref(0)
const page = ref(1)
const loading = ref(false)
const error = ref<string | null>(null)

const hasMore = computed(() => items.value.length < total.value)

async function load(reset: boolean) {
  loading.value = true
  error.value = null
  try {
    const next = reset ? 1 : page.value + 1
    const res = await listNotifications(filter.value, next, PER_PAGE)
    items.value = reset ? res.items : [...items.value, ...res.items]
    total.value = res.total
    page.value = next
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : uiText('通知を読み込めませんでした')
  } finally {
    loading.value = false
  }
}

onMounted(() => void load(true))
watch(filter, () => void load(true))
// **未読数が増えたら先頭のページを引き直す**（5.16「未読数の取り直し」）
watch(
  () => store.unread,
  (now, before) => {
    if (now > before) void load(true)
  },
)

function setFilter(next: NotificationFilter) {
  void router.replace({ query: { ...route.query, filter: next === 'unread' ? 'unread' : undefined } })
}

// ── 日付の区切りと時刻 ─────────────────────────────────

/** 区切りの日付。**自分のタイムゾーン**で切る（5.16） */
function dayLabel(ms: number): string {
  const weekday = new Intl.DateTimeFormat(uiLocaleTag(), {
    weekday: 'short',
    timeZone: currentTimezone() ?? undefined,
  }).format(new Date(ms))
  return uiText('{date}（{weekday}）', { date: formatDate(ms), weekday })
}

function timeOf(ms: number): string {
  return new Intl.DateTimeFormat(uiLocaleTag(), {
    hour: '2-digit',
    minute: '2-digit',
    hour12: false,
    timeZone: currentTimezone() ?? undefined,
  }).format(new Date(ms))
}

const groups = computed(() => {
  const out: { day: string; rows: Notification[] }[] = []
  for (const n of items.value) {
    const day = dayLabel(n.created_at)
    const last = out[out.length - 1]
    if (last && last.day === day) last.rows.push(n)
    else out.push({ day, rows: [n] })
  }
  return out
})

// ── 操作 ─────────────────────────────────────────────

/** 1件を差し替える。**絞り込みが未読なら、既読にした行もその場では消さない**——押した行が消えると、隣を押し違える */
function replace(next: Notification) {
  items.value = items.value.map((n) => (n.id === next.id ? next : n))
}

async function setRead(n: Notification, read: boolean) {
  if ((n.read_at !== null) === read) return
  try {
    replace(await setNotificationRead(n.id, read))
    store.unread = Math.max(0, store.unread + (read ? -1 : 1))
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : uiText('通知を更新できませんでした')
  }
}

/** 遷移先のリンク：**既読にしてから移る** */
async function open(n: Notification) {
  await setRead(n, true)
  await router.push(n.link)
}

/** リンクと操作以外の、行の中：既読にするだけ。**未読へは戻さない** */
function onRowClick(n: Notification, event: MouseEvent) {
  if ((event.target as HTMLElement).closest('a, button')) return
  void setRead(n, true)
}

async function remove(n: Notification) {
  try {
    await deleteNotification(n.id)
    items.value = items.value.filter((x) => x.id !== n.id)
    total.value = Math.max(0, total.value - 1)
    if (n.read_at === null) store.unread = Math.max(0, store.unread - 1)
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : uiText('通知を削除できませんでした')
  }
}

async function readAll() {
  try {
    await readAllNotifications()
    store.unread = 0
    await load(true)
  } catch (e: unknown) {
    error.value = e instanceof Error ? e.message : uiText('通知を更新できませんでした')
  }
}
</script>

<template>
  <div class="page">
    <PageHeader :title="$ui('通知')">
      <template #actions>
        <button type="button" :disabled="store.unread === 0" @click="readAll">{{ $ui('すべて既読にする') }}</button>
      </template>
    </PageHeader>

    <div class="filters" role="group" :aria-label="$ui('通知を絞り込む')">
      <button type="button" class="chip" :aria-pressed="filter === 'unread'" @click="setFilter('unread')">
        {{ $ui('未読') }} {{ formatUnreadCount(store.unread) || 0 }}
      </button>
      <button type="button" class="chip" :aria-pressed="filter === 'all'" @click="setFilter('all')">
        {{ $ui('すべて') }}
      </button>
    </div>

    <div class="list">
      <p v-if="error" class="alert" role="alert">{{ error }}</p>

      <EmptyState v-if="!loading && items.length === 0 && !error" :title="$ui('通知はありません')" />

      <section v-for="g in groups" :key="g.day" class="day">
        <h2 class="day-label">{{ g.day }}</h2>
        <article
          v-for="n in g.rows"
          :key="n.id"
          class="row"
          :class="{ read: n.read_at !== null }"
          :data-id="n.id"
          @click="onRowClick(n, $event)"
        >
          <span class="mark" :aria-label="n.read_at === null ? $ui('未読') : $ui('既読')"></span>
          <h3 class="title">{{ n.title }}</h3>
          <time class="time" :datetime="isoOf(n.created_at)">{{ timeOf(n.created_at) }}</time>
          <p v-if="n.body" class="body">{{ n.body.split('\n')[0] }}</p>
          <p class="meta">
            <a class="go" :href="n.link" @click.prevent="open(n)">{{ n.link_label }} →</a>
            <span>{{ $ui('通知元：{name}', { name: n.source.name }) }}</span>
          </p>
          <span class="actions">
            <button type="button" class="link" @click="setRead(n, n.read_at === null)">
              {{ n.read_at === null ? $ui('既読にする') : $ui('未読に戻す') }}
            </button>
            <button type="button" class="link" @click="remove(n)">{{ $ui('削除') }}</button>
          </span>
        </article>
      </section>

      <div v-if="hasMore" class="more">
        <button type="button" :disabled="loading" @click="load(false)">{{ $ui('さらに表示') }}</button>
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

.filters {
  display: flex;
  flex: none;
  gap: var(--pb-space-2);
  padding: var(--pb-space-2) var(--pb-space-4);
  border-bottom: 1px solid var(--pb-line);
}

.chip {
  padding: 1px 10px;
  border: 1px solid var(--pb-line);
  border-radius: 12px;
  background: var(--pb-bg);
  font-size: 13px;
}

.chip[aria-pressed='true'] {
  border-color: var(--pb-accent);
  color: var(--pb-accent);
  font-weight: 600;
}

.list {
  flex: 1;
  overflow: auto;
}

.alert {
  margin: var(--pb-space-2) var(--pb-space-4);
  color: var(--pb-danger-text);
}

.day-label {
  margin: 0;
  padding: 6px var(--pb-space-4) 2px;
  border-bottom: 1px solid var(--pb-line);
  background: var(--pb-surface);
  color: var(--pb-text-muted);
  font-size: 12px;
  font-weight: 400;
}

.row {
  display: grid;
  grid-template-columns: 14px minmax(0, 1fr) auto;
  gap: 4px 10px;
  align-items: start;
  padding: var(--pb-space-2) var(--pb-space-4);
  border-bottom: 1px solid var(--pb-line);
}

.row:hover {
  background: var(--pb-hover);
}

.mark {
  width: 8px;
  height: 8px;
  margin-top: 7px;
  border-radius: 50%;
  background: var(--pb-danger);
}

.read .mark {
  background: transparent;
  box-shadow: inset 0 0 0 1px var(--pb-7);
}

.title {
  overflow: hidden;
  margin: 0;
  font-size: 14px;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.read .title {
  color: var(--pb-text-muted);
  font-weight: 400;
}

.time {
  color: var(--pb-text-muted);
  font: 12px var(--pb-font-mono);
  white-space: nowrap;
}

.body,
.meta {
  grid-column: 2;
  margin: 0;
  color: var(--pb-text-muted);
}

.body {
  overflow: hidden;
  font-size: 13px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  font-size: 12px;
}

/* **遷移する場所はここだけ**。アクセントの色と下線で示す（5.16） */
.go {
  color: var(--pb-accent);
  font-weight: 600;
  text-decoration: underline;
  text-underline-offset: 2px;
}

.read .go {
  font-weight: 400;
}

.actions {
  display: flex;
  grid-row: 2 / span 2;
  grid-column: 3;
  gap: 10px;
  align-self: center;
  opacity: 0;
}

.row:hover .actions,
.row:focus-within .actions {
  opacity: 1;
}

.link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--pb-text-muted);
  font-size: 12px;
  text-decoration: underline;
  cursor: pointer;
}

.link:hover {
  color: var(--pb-text);
}

.more {
  padding: var(--pb-space-3);
  text-align: center;
}

/* 768px 未満は操作を常に出し、時刻と操作を本文の下へ回す（5.16） */
@media (width < 768px) {
  .row {
    grid-template-columns: 14px minmax(0, 1fr);
  }

  .time,
  .actions {
    grid-column: 2;
  }

  .actions {
    grid-row: auto;
    justify-self: start;
    opacity: 1;
  }
}
</style>
