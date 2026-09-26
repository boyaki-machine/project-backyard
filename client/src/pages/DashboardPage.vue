<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * プロジェクトダッシュボード（`GuiDesign.md` 5.3）。必要権限は `project.view`。手順19b。
 *
 * プロジェクト選択直後の着地点。**統計と当面の作業に割り切る**——AI要約は
 * 置かない（5.3.1）。
 *
 * **この画面だけが起動時に5本のAPIを呼ぶ**（`ApiDesign.md` 8章の例外）。
 * 互いに独立で並列に投げ、**1本落ちてもそのブロックだけをエラーにする**
 * （6.2 の4状態をブロック単位で持つ）。5本目の `GET /projects/:key` は
 * **表示名の解決**に要る——`activity` は `status_key` と ULID しか返さない
 * （9.13.2）。
 *
 * **すべての導線はバックログへ飛び、同じ条件で絞った一覧を出す**（5.3）。
 * ダッシュボードが出した件数と押した先の件数が一致することが要件で、
 * サーバ側は `stats` からエピックを除き（9.13.1）、`overdue` / `stale` を
 * `GET /tickets` のパラメータとして持つ（9.2.1）。
 *
 * **日時は絶対表記**。相対表記は文化によって
 * 読みやすさが分かれる。
 */
import { computed, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import Avatar from '../components/Avatar.vue'
import EmptyState from '../components/EmptyState.vue'
import NewTicketModal from '../components/NewTicketModal.vue'
import PageHeader from '../components/PageHeader.vue'
import StatCard from '../components/StatCard.vue'
import { ApiError } from '../api/client'
import * as dashboardApi from '../api/dashboard'
import type { Activity, ProjectStats } from '../api/dashboard'
import * as tagsApi from '../api/tags'
import type { Tag } from '../api/tags'
import * as ticketsApi from '../api/tickets'
import {
  backlogTicketTypes,
  statusCategoryLabels,
  statusCategoryOrder,
  statusMarks,
} from '../api/tickets'
import type { CreateTicketRequest, StatusCategory, Ticket } from '../api/tickets'
import { activitySummary, actorLabel, ticketLabel } from '../lib/activity'
import type { ActivityLabelContext } from '../lib/activity'
import { formatDateTime, formatPlainDate, todayPlainDate } from '../lib/datetime'
import { statusLabel } from '../lib/catalogLabels'
import { useAuthStore } from '../stores/auth'
import { useProjectStore } from '../stores/project'

const props = defineProps<{ projectKey: string }>()

const auth = useAuthStore()
const projectStore = useProjectStore()
const router = useRouter()

/** 「自分の担当」「期限が近い」に出す件数（5.3 の表） */
const LIST_PER_PAGE = 5
/** 「最近の動き」の1回ぶん（5.3 の表） */
const ACTIVITY_PER_PAGE = 10

/**
 * チケットの2ブロックが送る種別（5.3）。**エピックを含まない。**
 *
 * 理由は3つとも同じ根である——①エピックはグルーピング専用で「やるべき仕事」
 * ではない（5.4）②`stats` もエピックを数えない（`ApiDesign.md` 9.13.1）
 * ③**「すべて見る →」の行き先であるバックログがエピックを行として出さない**
 * ので、送らないとブロックの件数と押した先の件数が食い違う。
 *
 * **実測で 2 対 0 / 5 対 3 のずれが出た**（demo の seed。手順19b の検証）。
 * 開発PM に割り当てられた未完了2件はどちらもエピックだった。
 */
const BLOCK_TYPES = backlogTicketTypes.join(',')

// ── 権限（7.3）─────────────────────────────────────────────
//
// **ルートの必要権限は `project.view` である**（3.2）。チケットを読む権限を
// 持たない利用者には、チケットの2ブロックと集計カードの導線を出さない。

const canViewTickets = computed(() => auth.canInProject(props.projectKey, 'ticket.view'))
const canCreate = computed(() => auth.canInProject(props.projectKey, 'ticket.create'))

/**
 * ページヘッダの見出し（5.3）。**プロジェクト名 ＋ 画面名**。
 *
 * 名前はセッションから引く（`GET /me` の `projects[]`）。**メンバーでない
 * アドミニストレータは持たないので、そのときはキーで代替する**（5.3）。
 */
const heading = computed(() => {
  const name = auth.projectByKey(props.projectKey)?.name ?? props.projectKey
  return uiText("{value0} ダッシュボード", { value0: name })
})

// ── ブロックごとの状態（6.2）───────────────────────────────

function toMessage(e: unknown): string {
  return e instanceof ApiError ? e.message : uiText("通信に失敗しました")
}

const stats = ref<ProjectStats | null>(null)
const statsError = ref('')
const statsLoading = ref(false)

const mine = ref<Ticket[]>([])
const mineTotal = ref(0)
const mineError = ref('')
const mineLoading = ref(false)

const due = ref<Ticket[]>([])
const dueTotal = ref(0)
const dueError = ref('')
const dueLoading = ref(false)

const activity = ref<Activity[]>([])
const activityTotal = ref(0)
const activityPage = ref(1)
const activityTotalPages = ref(1)
const activityError = ref('')
const activityLoading = ref(false)
const activityLoadingMore = ref(false)

const hasMoreActivity = computed(() => activityPage.value < activityTotalPages.value)

/**
 * 表示名を解決する手がかり（`lib/activity.ts`）。
 *
 * **スプリント表は渡さない**（5.5 と同じ）。ダッシュボードは
 * `GET /sprints` を呼ばず、`activity.ts` 側で
 * 「スプリントを変更」に落ちる。
 */
const labelContext = computed<ActivityLabelContext>(() => ({
  projectKey: props.projectKey,
  statusNames: Object.fromEntries(
    (projectStore.current?.workflow?.statuses ?? []).map((s) => [s.key, statusLabel(s.key, s.name)]),
  ),
  actorNames: Object.fromEntries(
    (projectStore.current?.members ?? []).map((m) => [m.actor_id, m.display_name]),
  ),
}))

// ── 取得（5本。並列で投げる）───────────────────────────────

async function loadStats(): Promise<void> {
  statsLoading.value = true
  statsError.value = ''
  try {
    stats.value = await dashboardApi.getProjectStats(props.projectKey)
  } catch (e) {
    statsError.value = toMessage(e)
    stats.value = null
  } finally {
    statsLoading.value = false
  }
}

async function loadMine(): Promise<void> {
  if (!canViewTickets.value) return
  mineLoading.value = true
  mineError.value = ''
  try {
    const res = await ticketsApi.listTickets(props.projectKey, {
      assignee: 'me',
      open: 'true',
      type: BLOCK_TYPES,
      per_page: LIST_PER_PAGE,
    })
    mine.value = res.items
    mineTotal.value = res.total
  } catch (e) {
    mineError.value = toMessage(e)
    mine.value = []
  } finally {
    mineLoading.value = false
  }
}

async function loadDue(): Promise<void> {
  if (!canViewTickets.value) return
  dueLoading.value = true
  dueError.value = ''
  try {
    const res = await ticketsApi.listTickets(props.projectKey, {
      due_within: '7d',
      open: 'true',
      type: BLOCK_TYPES,
      // **期限の近い順に出す**（5.3 のワイヤーが「明日 → 3日後 → 5日後」の順）。
      // 既定の `sort_key` は人が手で並べた順で、期限とは関係しない。
      sort: 'due_date',
      order: 'asc',
      per_page: LIST_PER_PAGE,
    })
    due.value = res.items
    dueTotal.value = res.total
  } catch (e) {
    dueError.value = toMessage(e)
    due.value = []
  } finally {
    dueLoading.value = false
  }
}

async function loadActivity(): Promise<void> {
  activityLoading.value = true
  activityError.value = ''
  activityPage.value = 1
  try {
    const res = await dashboardApi.listActivity(props.projectKey, {
      page: 1,
      per_page: ACTIVITY_PER_PAGE,
    })
    activity.value = res.items
    activityTotal.value = res.total
    activityTotalPages.value = res.total_pages
  } catch (e) {
    activityError.value = toMessage(e)
    activity.value = []
  } finally {
    activityLoading.value = false
  }
}

/**
 * 「以前の動きを読む」（5.3）。**リンクではなくブロック内で足す**——
 * `activity` の全件を並べる画面が 3.2 に無く、出来事の流れはバックログの
 * どのフィルタにも翻訳できない。
 */
async function loadMoreActivity(): Promise<void> {
  if (activityLoadingMore.value || !hasMoreActivity.value) return
  activityLoadingMore.value = true
  activityError.value = ''
  try {
    const next = activityPage.value + 1
    const res = await dashboardApi.listActivity(props.projectKey, {
      page: next,
      per_page: ACTIVITY_PER_PAGE,
    })
    activity.value = [...activity.value, ...res.items]
    activityPage.value = next
    activityTotal.value = res.total
    activityTotalPages.value = res.total_pages
  } catch (e) {
    activityError.value = toMessage(e)
  } finally {
    activityLoadingMore.value = false
  }
}

/**
 * 5本を並列に投げる。**`allSettled` を使う**——1本の失敗で他を捨てない。
 * 各 `load*` が自分のエラーを抱えるので、ここでは待つだけでよい。
 */
function loadAll(): void {
  void Promise.allSettled([
    loadStats(),
    loadMine(),
    loadDue(),
    loadActivity(),
    projectStore.fetchCurrent(props.projectKey),
  ])
}

watch(() => props.projectKey, loadAll, { immediate: true })

// ── 導線（5.3「ブロックの導線」）───────────────────────────
//
// **どれもバックログへ飛び、同じ条件で絞った一覧を出す。** クエリ名は
// `ApiDesign.md` 9.2.1 と同じで、バックログのフィルタ行がそのまま読む
// （`GuiDesign.md` 5.4「状態と期限のフィルタ」）。

function backlogTo(query: Record<string, string>): {
  path: string
  query: Record<string, string>
} {
  return { path: `/p/${props.projectKey}/backlog`, query }
}

const cards = computed(() =>
  statusCategoryOrder.map((c: StatusCategory) => ({
    category: c,
    label: statusCategoryLabels[c],
    value: stats.value?.by_category[c] ?? 0,
  })),
)

/**
 * 「要対応」の3行（5.3）。**チケット名を出さず件数で述べる**——`stats` は
 * 件数しか返さず、1件だけ名前を出すと残りが何件あるか読めない。
 *
 * **閾値の 14 を画面に書かない**（5.3）。`stale.threshold_days` から文も
 * リンクも組み立てるので、サーバが変えれば追従する。
 */
const attention = computed(() => {
  const s = stats.value
  if (s === null) return []
  const rows: { key: string; text: string; to: ReturnType<typeof backlogTo> }[] = []
  if (s.stale.count > 0) {
    rows.push({
      key: 'stale',
      text: uiText("{value0}件のチケットが{value1}日以上更新されていません", { value0: s.stale.count, value1: s.stale.threshold_days }),
      to: backlogTo({ stale: `${s.stale.threshold_days}d` }),
    })
  }
  if (s.overdue > 0) {
    rows.push({
      key: 'overdue',
      text: uiText("{value0}件のチケットが期限を超過しています", { value0: s.overdue }),
      to: backlogTo({ overdue: 'true' }),
    })
  }
  if (s.unassigned > 0) {
    rows.push({
      key: 'unassigned',
      text: uiText("{value0}件のチケットに担当者が割り当てられていません", { value0: s.unassigned }),
      to: backlogTo({ assignee: 'none', open: 'true' }),
    })
  }
  return rows
})

/** 期限超過か（5.4 と同じ判定。`date` 列なので `todayPlainDate` と文字列で比べる） */
function isOverdue(t: Ticket): boolean {
  return t.due_date !== null && t.due_date < todayPlainDate() && t.closed_at === null
}

function ticketTo(seq: number): string {
  return `/p/${props.projectKey}/tickets/${seq}`
}

// ── 新規チケット（5.3 のヘッダ。5.4.3 のモーダルを使う）────────
//
// **語彙（タグ・エピック）は押したときに取りに行く。** 起動時の
// 5本に足さない——作らない利用者に2本を払わせないためで、プロジェクト設定が
// タブを開いたときにタグを取るのと同じ判断（5.9）。

const showNewModal = ref(false)
const vocabLoading = ref(false)
const tags = ref<Tag[]>([])
/**
 * エピック欄の選択肢（5.4.3「親チケットとエピック」）。**ダッシュボードは
 * 一覧を持たない**ので、親チケット欄は出ない（候補が0件）。
 */
const epics = ref<Ticket[]>([])
const busy = ref(false)
const newFieldErrors = ref<Record<string, string>>({})
const createError = ref('')

const members = computed(() => projectStore.current?.members ?? [])

async function openNewModal(): Promise<void> {
  showNewModal.value = true
  newFieldErrors.value = {}
  createError.value = ''
  if (tags.value.length > 0 || epics.value.length > 0) return
  vocabLoading.value = true
  try {
    const [t, c] = await Promise.all([
      tagsApi.listTags(props.projectKey),
      ticketsApi.listTickets(props.projectKey, { type: 'epic' }),
    ])
    tags.value = t.items
    epics.value = c.items
  } catch {
    // **語彙が取れなくてもモーダルは閉じない。** タイトルと種別だけで作れる
    // （`ApiDesign.md` 9.3 の必須はタイトルのみ）ので、選択肢が空のまま出す。
    tags.value = []
    epics.value = []
  } finally {
    vocabLoading.value = false
  }
}

/**
 * 作成する。**成功したらそのチケットの詳細へ移る**（バックログの右に開く）。
 *
 * ダッシュボードに留まっても、増えるのは集計の数字だけで**作ったものが
 * 見えない**。作った直後にやることは中身を書くことである。
 */
async function createTicket(body: CreateTicketRequest): Promise<void> {
  busy.value = true
  newFieldErrors.value = {}
  try {
    const created = await ticketsApi.createTicket(props.projectKey, body)
    showNewModal.value = false
    await router.push(ticketTo(created.seq))
  } catch (e) {
    const err = e instanceof ApiError ? e : null
    if (err !== null && err.status === 422) {
      const fields: Record<string, string> = {}
      for (const d of err.details) fields[d.field] = d.message
      newFieldErrors.value = fields
    } else {
      showNewModal.value = false
      createError.value = toMessage(e)
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <div class="dash">
    <PageHeader :title="heading">
      <template #actions>
        <button v-if="canCreate" type="button" class="primary" @click="openNewModal"> {{ $ui('+ 新規チケット') }} </button>
      </template>
    </PageHeader>

    <div class="dash-body">
      <p v-if="createError" class="dash-error" role="alert">{{ createError }}</p>

      <!-- 集計カード（5.3）。**押すとバックログをその区分で絞って開く** -->
      <section class="dash-cards" :aria-label="$ui('ステータス集計')">
        <p v-if="statsError" class="dash-error" role="alert">
          {{ statsError }}
          <button type="button" class="dash-retry" @click="loadStats">{{ $ui('再試行') }}</button>
        </p>
        <template v-else>
          <StatCard
            v-for="c in cards"
            :key="c.category"
            class="dash-card"
            :label="c.label"
            :value="c.value"
            :to="canViewTickets ? backlogTo({ status_category: c.category }) : undefined"
            :link-label="$ui('{value0}のチケットをバックログで見る', { value0: c.label })"
          />
        </template>
      </section>

      <div class="dash-columns">
        <!-- 自分の担当（5.3）─────────────────────────────── -->
        <section v-if="canViewTickets" class="dash-block" :aria-label="$ui('自分の担当')">
          <div class="dash-block-head">
            <h2 class="dash-block-title">{{ $ui('自分の担当') }}</h2>
          </div>

          <p v-if="mineLoading" class="dash-note">{{ $ui('読み込み中…') }}</p>
          <p v-else-if="mineError" class="dash-note dash-error" role="alert">
            {{ mineError }}
            <button type="button" class="dash-retry" @click="loadMine">{{ $ui('再試行') }}</button>
          </p>
          <EmptyState v-else-if="mine.length === 0" :title="$ui('担当しているチケットはありません')" />
          <ul v-else class="dash-list">
            <li v-for="t in mine" :key="t.id" class="dash-row">
              <RouterLink class="dash-row-link" :to="ticketTo(t.seq)">
                <span class="dash-id">{{ projectKey }}-{{ t.seq }}</span>
                <span class="dash-title">{{ t.title }}</span>
                <span class="dash-status">
                  <span aria-hidden="true">{{ statusMarks[t.status.category] }}</span>
                  {{ statusLabel(t.status.key, t.status.name) }}
                </span>
              </RouterLink>
            </li>
          </ul>

          <RouterLink
            v-if="mine.length > 0"
            class="dash-more-link"
            :to="backlogTo({ assignee: 'me', open: 'true' })"
          > {{ $ui('すべて見る（') }}{{ mineTotal }}{{ $ui('件） →') }} </RouterLink>
        </section>

        <!-- 期限が近い（5.3）───────────────────────────── -->
        <section v-if="canViewTickets" class="dash-block" :aria-label="$ui('期限が近い')">
          <div class="dash-block-head">
            <h2 class="dash-block-title">{{ $ui('期限が近い') }}</h2>
          </div>

          <p v-if="dueLoading" class="dash-note">{{ $ui('読み込み中…') }}</p>
          <p v-else-if="dueError" class="dash-note dash-error" role="alert">
            {{ dueError }}
            <button type="button" class="dash-retry" @click="loadDue">{{ $ui('再試行') }}</button>
          </p>
          <EmptyState v-else-if="due.length === 0" :title="$ui('7日以内に期限のチケットはありません')" />
          <ul v-else class="dash-list">
            <li v-for="t in due" :key="t.id" class="dash-row">
              <RouterLink class="dash-row-link" :to="ticketTo(t.seq)">
                <span class="dash-id">{{ projectKey }}-{{ t.seq }}</span>
                <span class="dash-title">{{ t.title }}</span>
                <!-- 期限超過のみ danger（8.7 の例外）。**年を省かない**（5.4） -->
                <span class="dash-due" :class="{ overdue: isOverdue(t) }">
                  <span v-if="isOverdue(t)" aria-hidden="true">⚠ </span>
                  {{ t.due_date === null ? '—' : formatPlainDate(t.due_date) }}
                </span>
              </RouterLink>
            </li>
          </ul>

          <RouterLink
            v-if="due.length > 0"
            class="dash-more-link"
            :to="backlogTo({ due_within: '7d', open: 'true' })"
          > {{ $ui('すべて見る（') }}{{ dueTotal }}{{ $ui('件） →') }} </RouterLink>
        </section>
      </div>

      <!-- 最近の動き（5.3）。**リンクを持たず、ここで続きを足す** -->
      <section class="dash-block" :aria-label="$ui('最近の動き')">
        <div class="dash-block-head">
          <h2 class="dash-block-title">{{ $ui('最近の動き') }}</h2>
        </div>

        <p v-if="activityLoading" class="dash-note">{{ $ui('読み込み中…') }}</p>
        <p v-else-if="activityError" class="dash-note dash-error" role="alert">
          {{ activityError }}
          <button type="button" class="dash-retry" @click="loadActivity">{{ $ui('再試行') }}</button>
        </p>
        <EmptyState v-else-if="activity.length === 0" :title="$ui('まだ動きがありません')" />
        <template v-else>
          <ol class="dash-activity">
            <li v-for="a in activity" :key="a.id" class="dash-act">
              <Avatar
                class="dash-act-avatar"
                :name="actorLabel(a.actor)"
                :kind="a.actor?.kind ?? 'system'"
                :size="20"
              />
              <p class="dash-act-line">
                <span class="dash-act-actor">{{ actorLabel(a.actor) }}</span>
                <RouterLink
                  v-if="a.entity_seq !== null"
                  class="dash-act-target"
                  :to="ticketTo(a.entity_seq)"
                  >{{ ticketLabel(projectKey, a.entity_seq) }}</RouterLink
                >
                <span v-else class="dash-act-target">{{ ticketLabel(projectKey, null) }}</span>
                <span>{{ activitySummary(a, labelContext) }}</span>
              </p>
              <time class="dash-act-time" :datetime="a.occurred_at">{{
                formatDateTime(a.occurred_at)
              }}</time>
            </li>
          </ol>

          <button
            v-if="hasMoreActivity"
            type="button"
            class="dash-more"
            :disabled="activityLoadingMore"
            @click="loadMoreActivity"
          >
            {{
              activityLoadingMore
                ? $ui("読み込み中…")
                : $ui("以前の動きを読む（残り {value0} 件）", { value0: activityTotal - activity.length })
            }}
          </button>
        </template>
      </section>

      <!-- 要対応（5.3）。**件数の文で述べ、押すとバックログで絞る** -->
      <section v-if="stats !== null && attention.length > 0" class="dash-block" :aria-label="$ui('要対応')">
        <div class="dash-block-head">
          <h2 class="dash-block-title">{{ $ui('要対応') }}</h2>
        </div>
        <ul class="dash-attention">
          <li v-for="row in attention" :key="row.key" class="dash-att">
            <span class="dash-att-text"><span aria-hidden="true">⚠</span> {{ row.text }}</span>
            <RouterLink v-if="canViewTickets" class="dash-more-link" :to="row.to"> {{ $ui('確認する →') }} </RouterLink>
          </li>
        </ul>
      </section>
    </div>

    <NewTicketModal
      v-if="showNewModal"
      :project-key="projectKey"
      :members="members"
      :tags="tags"
      :candidates="[]"
      :epics="epics"
      :busy="busy || vocabLoading"
      :field-errors="newFieldErrors"
      @close="showNewModal = false"
      @save="createTicket"
    />
  </div>
</template>

<style scoped>
/* **クラス名は画面名を冠する**（6.6）。`scoped` は DOM のクラス名を分けない
   ので、`.card` `.row` のような一般名は検証のセレクタが別画面に当たる */
.dash {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.dash-body {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: var(--pb-space-6);
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-6);
}

/* 集計カード。**4枚で固定する**（5.3 は「常に4つのキーを持つ」）。
   `auto-fit` にしない——狭い窓で枚数が変わると、意味の並びが崩れる */
.dash-cards {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: var(--pb-space-4);
}

/* 900px では4枚が横に並びきらないので2×2にする（2.4 のブレークポイント）。
   **1枚ずつの縦積みにはしない**——4つを見比べるのがこのブロックの用である */
@media (max-width: 900px) {
  .dash-cards {
    grid-template-columns: repeat(2, 1fr);
  }
}

/* 「自分の担当」と「期限が近い」を横に並べる（5.3 のワイヤー）。
   **2列で固定する。** 5.3 が2つのブロックを左右に置くと決めている */
.dash-columns {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--pb-space-4);
}

@media (max-width: 900px) {
  .dash-columns {
    grid-template-columns: minmax(0, 1fr);
  }
}

.dash-block {
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  padding: var(--pb-space-4);
  min-width: 0;
}

.dash-block-head {
  padding-bottom: var(--pb-space-1);
  margin-bottom: var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
}

.dash-block-title {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
}

.dash-note {
  color: var(--pb-text-muted);
  font-size: 13px;
  margin: 0;
}

.dash-error {
  color: var(--pb-danger-text);
}

.dash-retry {
  margin-left: var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  padding: 0 var(--pb-space-2);
  cursor: pointer;
}

.dash-list,
.dash-activity,
.dash-attention {
  list-style: none;
  margin: 0;
  padding: 0;
}

.dash-row-link {
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-2);
  padding: var(--pb-space-2) 0;
  border-bottom: 1px solid var(--pb-line);
  text-decoration: none;
  color: inherit;
  font-size: 13px;
}

.dash-row:last-child .dash-row-link {
  border-bottom: none;
}

.dash-row-link:hover {
  background: var(--pb-hover);
}

.dash-id {
  flex: 0 0 auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--pb-text-muted);
}

/* **1行に収め、あふれたら省略する。** 折り返すと行の高さがそろわず、
   5件の一覧が段組みの中で高さを持ちすぎる */
.dash-title {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dash-status,
.dash-due {
  flex: 0 0 auto;
  color: var(--pb-text-muted);
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

/* 期限超過のみ danger（8.7 の例外。優先度には色を使わない） */
.dash-due.overdue {
  color: var(--pb-danger-text);
}

.dash-more-link {
  display: inline-block;
  margin-top: var(--pb-space-2);
  font-size: 12px;
  color: var(--pb-text-muted);
}

.dash-act {
  display: flex;
  align-items: flex-start;
  gap: var(--pb-space-2);
  padding: var(--pb-space-2) 0;
  border-bottom: 1px solid var(--pb-line);
}

.dash-act:last-child {
  border-bottom: none;
}

.dash-act-avatar {
  flex: 0 0 auto;
  margin-top: 2px;
}

.dash-act-line {
  flex: 1 1 auto;
  min-width: 0;
  margin: 0;
  font-size: 13px;
  line-height: 1.5;
  overflow-wrap: anywhere;
}

.dash-act-actor {
  font-weight: 600;
  margin-right: var(--pb-space-1);
}

.dash-act-target {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  color: var(--pb-text-muted);
  margin-right: var(--pb-space-1);
}

/* **時刻の幅を固定する**（時系列であることが読み取れるように） */
.dash-act-time {
  flex: 0 0 auto;
  font-size: 12px;
  color: var(--pb-text-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
  padding-top: 2px;
}

.dash-more {
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

.dash-more:hover:not(:disabled) {
  background: var(--pb-hover);
}

.dash-att {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--pb-space-4);
  padding: var(--pb-space-2) 0;
  border-bottom: 1px solid var(--pb-line);
  font-size: 13px;
}

.dash-att:last-child {
  border-bottom: none;
}

.dash-att-text {
  min-width: 0;
  overflow-wrap: anywhere;
}

.dash-att-text > span {
  color: var(--pb-warning-text);
  margin-right: var(--pb-space-1);
}

.dash-more-link:hover {
  color: var(--pb-text);
}
</style>
