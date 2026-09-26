<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * チケットの履歴（`GuiDesign.md` 5.5「履歴」、`ApiDesign.md` 9.13.2）。手順19b。
 *
 * **詳細ペインから切り出してある**（6.1）。`GET .../activity` を自分で呼び、
 * 開閉と追記読み込みを持つ自己完結の単位だからで、`TicketComments.vue` と
 * 同じ判断による。
 *
 * **既定は畳んであり、開いたときに初めて呼ぶ**（5.5）。詳細を開くだけでは
 * 呼ばない——起動時の2本（本体とコメント）に3本目を足さないためである
 * （`ApiDesign.md` 8章）。
 *
 * **日時は絶対表記である**（5.3 / 5.5）。相対表記は
 * 文化によって読みやすさが分かれる。**`app_user.timezone` の反映を目で
 * 確かめられるのはこの画面**で、`timestamptz` を大量に並べる場所である（7.5）。
 *
 * **要約文は組み立てない。** `lib/activity.ts` の `activitySummary` を通す
 * ——ダッシュボードの「最近の動き」と同じ文になる必要がある（5.5）。
 */
import { computed, ref, watch } from 'vue'

import Avatar from './Avatar.vue'
import { ApiError } from '../api/client'
import * as dashboardApi from '../api/dashboard'
import type { Activity } from '../api/dashboard'
import type { ProjectMember, Workflow } from '../api/projects'
import { activityDetail, activitySummary, actorLabel, ticketLabel } from '../lib/activity'
import type { ActivityLabelContext } from '../lib/activity'
import { formatDateTime } from '../lib/datetime'
import { statusLabel } from '../lib/catalogLabels'

const props = defineProps<{
  projectKey: string
  seq: number
  /** ステータスキーを表示名へ直すのに使う（`GET /projects/:key` の `workflow`） */
  workflow?: Workflow | null
  /** `assignee_id`（ULID）を表示名へ直すのに使う。**スプリントは直せない**（5.5） */
  members?: ProjectMember[]
}>()

/** 1回に読む件数（9.13.2 の既定と同じ） */
const PER_PAGE = 20

const open = ref(false)
const items = ref<Activity[]>([])
const page = ref(1)
const totalPages = ref(1)
const total = ref(0)
const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')

/** 一度でも読んだか。畳んで開き直したときに読み直さないためのしるし */
const loaded = ref(false)

const hasMore = computed(() => page.value < totalPages.value)

/**
 * 値を名前へ直す手がかり（`lib/activity.ts`）。
 *
 * **スプリント表を渡していない。** 詳細ペインはスプリントの選択肢を持つが、
 * 5.5 は履歴でスプリント名を出さないと決めている——`activity.ts` 側で
 * 「スプリントを変更」に落ちる。
 */
const labelContext = computed<ActivityLabelContext>(() => ({
  projectKey: props.projectKey,
  statusNames: Object.fromEntries(
    (props.workflow?.statuses ?? []).map((s) => [s.key, statusLabel(s.key, s.name)]),
  ),
  actorNames: Object.fromEntries(
    (props.members ?? []).map((m) => [m.actor_id, m.display_name]),
  ),
}))

function toMessage(e: unknown): string {
  return e instanceof ApiError ? e.message : uiText("通信に失敗しました")
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  items.value = []
  page.value = 1
  try {
    const res = await dashboardApi.listActivity(props.projectKey, {
      entity: dashboardApi.ticketEntity(props.seq),
      page: 1,
      per_page: PER_PAGE,
    })
    items.value = res.items
    total.value = res.total
    totalPages.value = res.total_pages
    loaded.value = true
  } catch (e) {
    loadError.value = toMessage(e)
  } finally {
    loading.value = false
  }
}

/** 続きを**後ろへ**足す（並びは新しい順なので、次ページはより古い） */
async function loadMore(): Promise<void> {
  if (loadingMore.value || !hasMore.value) return
  loadingMore.value = true
  loadError.value = ''
  try {
    const next = page.value + 1
    const res = await dashboardApi.listActivity(props.projectKey, {
      entity: dashboardApi.ticketEntity(props.seq),
      page: next,
      per_page: PER_PAGE,
    })
    items.value = [...items.value, ...res.items]
    page.value = next
    total.value = res.total
    totalPages.value = res.total_pages
  } catch (e) {
    loadError.value = toMessage(e)
  } finally {
    loadingMore.value = false
  }
}

function toggle(): void {
  open.value = !open.value
  if (open.value && !loaded.value) void load()
}

/**
 * チケットが変わったら畳み直す。
 *
 * **`immediate` を付けない。** 付けると宣言前の `const` に触れて TDZ で
 * 落ちるうえ、ここでやりたいのは「切り替わったら畳む」
 * だけで、初期状態は `ref` の既定値そのものである。
 */
watch(
  () => [props.projectKey, props.seq],
  () => {
    open.value = false
    loaded.value = false
    items.value = []
    loadError.value = ''
  },
)

defineExpose({ reload: load })
</script>

<template>
  <div class="ta">
    <!-- **見出しと `[開く ▾]` を同じ行に置く**（5.5 のワイヤー）。見出しは
         呼び出し側から渡す——ブロックの見出しの形は詳細ペインが持っており、
         ここで `h3` を作ると2種類の見出しができる -->
    <div class="ta-head">
      <slot name="title" />
      <button type="button" class="ta-toggle" :aria-expanded="open" @click="toggle">
        <span>{{ open ? $ui("閉じる") : $ui("開く") }}</span>
        <span class="ta-caret" aria-hidden="true">{{ open ? '▾' : '▸' }}</span>
      </button>
    </div>

    <div v-if="open" class="ta-body">
      <p v-if="loading" class="ta-note">{{ $ui('読み込み中…') }}</p>
      <p v-else-if="loadError" class="ta-note ta-error" role="alert">
        {{ loadError }}
        <button type="button" class="ta-retry" @click="load">{{ $ui('再試行') }}</button>
      </p>

      <template v-else>
        <p v-if="items.length === 0" class="ta-note">{{ $ui('まだ履歴がありません') }}</p>

        <ol v-else class="ta-list">
          <li v-for="a in items" :key="a.id" class="ta-item">
            <Avatar
              class="ta-avatar"
              :name="actorLabel(a.actor)"
              :kind="a.actor?.kind ?? 'system'"
              :size="20"
            />
            <div class="ta-main">
              <p class="ta-line">
                <span class="ta-actor">{{ actorLabel(a.actor) }}</span>
                <span class="ta-target">{{ ticketLabel(projectKey, a.entity_seq) }}</span>
                <span class="ta-summary">{{ activitySummary(a, labelContext) }}</span>
              </p>
              <p v-if="activityDetail(a)" class="ta-detail">{{ activityDetail(a) }}</p>
            </div>
            <time class="ta-time" :datetime="a.occurred_at">{{
              formatDateTime(a.occurred_at)
            }}</time>
          </li>
        </ol>

        <button
          v-if="hasMore"
          type="button"
          class="ta-more"
          :disabled="loadingMore"
          @click="loadMore"
        >
          {{ loadingMore ? $ui("読み込み中…") : $ui("以前の履歴を読む（残り {value0} 件）", { value0: total - items.length }) }}
        </button>
      </template>
    </div>
  </div>
</template>

<style scoped>
/* **クラス名は部品名を冠する**（6.6）。`scoped` は DOM のクラス名を分けない */
/* 見出しの右に `[開く ▾]` を置く行。**下線はここに引く**——`h3` に引くと
   線が見出しの幅で切れ、右のボタンがブロックの外に見える（詳細ペインの
   `.block-head` と同じ理由） */
.ta-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--pb-space-2);
  padding-bottom: var(--pb-space-1);
  margin-bottom: var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
}

.ta-toggle {
  flex: none;
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: none;
  color: var(--pb-text-muted);
  font-size: 12px;
  cursor: pointer;
}

.ta-toggle:hover {
  background: var(--pb-hover);
}

.ta-caret {
  font-size: 10px;
}

.ta-body {
  margin-top: var(--pb-space-2);
}

.ta-note {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.ta-error {
  color: var(--pb-danger-text);
}

.ta-retry {
  margin-left: var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  padding: 0 var(--pb-space-2);
  cursor: pointer;
}

.ta-list {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
}

.ta-item {
  display: flex;
  align-items: flex-start;
  gap: var(--pb-space-2);
  padding-bottom: var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
}

.ta-item:last-child {
  border-bottom: none;
  padding-bottom: 0;
}

.ta-avatar {
  flex: 0 0 auto;
  margin-top: 2px;
}

/* **`min-width: 0` が無いと長い要約が縮まず、時刻が枠外へ押し出される**（6.7） */
.ta-main {
  flex: 1 1 auto;
  min-width: 0;
}

.ta-line {
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  /* **日本語は単語境界を持たないので `anywhere` を使う。** `break-word` だと
     長い ID や URL が枠を突き抜ける（6.7） */
  overflow-wrap: anywhere;
}

.ta-actor {
  font-weight: 600;
  margin-right: var(--pb-space-1);
}

/* **右に余白を置く。** 置かないと `demo-18を「進行中」に変更` のように
   ID と述部が地続きになる（ダッシュボードの `.dash-act-target` と揃える
   ——同じ文が2画面で違って見えてはいけない） */
.ta-target {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--pb-text-muted);
  margin-right: var(--pb-space-1);
}

.ta-detail {
  margin: 2px 0 0;
  font-size: 12px;
  color: var(--pb-text-muted);
  overflow-wrap: anywhere;
}

/* **時刻の幅を固定する。** 可変にすると行ごとに右端がそろわず、
   時系列であることが読み取りにくくなる */
.ta-time {
  flex: 0 0 auto;
  font-size: 12px;
  color: var(--pb-text-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  padding-top: 2px;
}

.ta-more {
  margin-top: var(--pb-space-3);
  width: 100%;
  padding: var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: none;
  color: var(--pb-text-muted);
  font-size: 12px;
  cursor: pointer;
}

.ta-more:hover:not(:disabled) {
  background: var(--pb-hover);
}
</style>
