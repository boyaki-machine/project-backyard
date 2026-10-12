<script lang="ts">
import { uiText } from '../locales/ui'
/**
 * グループ化中のセクションから開いたときの初期値（`GuiDesign.md` 5.4.3）。
 *
 * **`<script setup>` は export を持てない**ので、型だけを通常の script ブロックへ
 * 出している（同じ SFC の2ブロック構成は Vue の標準の書き方）。
 */
export interface NewTicketDefaults {
  parent_seq?: number
  /**
   * エピック欄の初期値（5.4.3「親チケットとエピック」）。**`parent_seq` と
   * 同時には渡さない**——保存されるのは `parent_seq` の1列で、親チケットが勝つ。
   */
  epic_seq?: number
  tag_ids?: string[]
  assignee_id?: string
  /**
   * 「終日」の初期値。省くと終日（5.4.3）。タイムラインから開くときは `false`（5.15「チケットを作る」）
   */
  all_day?: boolean
  /** 予定の初期値（エポックミリ秒）。タイムラインのドラッグで作るときに渡す */
  start_at?: number
  due_at?: number
}
</script>

<script setup lang="ts">
/**
 * 新規チケット（`GuiDesign.md` 5.4.3）。
 *
 * **状態の欄を出さない。** ワークフローの入口はサーバが決める（`ApiDesign.md` 9.3）。
 * 任意のステータスで作成できると 9.6 の遷移検証を素通りできてしまうため、
 * 作成後に遷移させる経路だけを残してある。
 *
 * **選択肢はすべて呼び出し側から受け取る。** バックログが既に持っている
 * もの（メンバー・タグ・スプリント・一覧のチケット）で足り、モーダルが
 * 開くたびに往復を増やさない（5.4.3 の「選択肢の出どころ」）。
 *
 * `SprintModal`（5.9.5）と同じく、**送信そのものは呼び出し側が行う。**
 * ここは値を組み立てて `save` で渡すだけである。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import ActorSelect from './ActorSelect.vue'
import type { ActorSelectOption } from './ActorSelect.vue'
import type { Tag } from '../api/tags'
import {
  backlogTicketTypes,
  newTicketBodyTemplate,
  priorityLabels,
  priorityOrder,
  ticketTypeIcons,
  ticketTypeLabels,
} from '../api/tickets'
import type { CreateTicketRequest, Ticket, TicketPriority, TicketType } from '../api/tickets'
import type { ProjectMember } from '../api/projects'
import { instantOfLocalInput, localInputOf, planDate, planInstant } from '../lib/datetime'
import { useProjectStore } from '../stores/project'

const props = defineProps<{
  /** ID を完全形で出すために要る（`GuiDesign.md` 5.4「ID列」） */
  projectKey: string
  members: ProjectMember[]
  tags: Tag[]
  /**
   * 親チケットの選択肢。バックログがいま表示しているチケット（5.4.3）。
   * **エピックは混ざっていても候補に出さない**（エピック欄で選ぶ）。
   */
  candidates: Ticket[]
  /**
   * エピック欄の選択肢（5.4.3「親チケットとエピック」）。
   * `GET /tickets?type=epic` の結果で、**空なら欄ごと出さない**。
   */
  epics?: Ticket[]
  /**
   * 新規エピックとして開く（5.4.3「新規エピック」）。種別をエピックに固定し、
   * **親チケットとエピックの欄を出さない**——エピックの入れ子を画面から作らない。
   */
  epicMode?: boolean
  defaults?: NewTicketDefaults
  /**
   * 親を `defaults.parent_seq`（エピックなら `defaults.epic_seq`）に固定する
   * （`GuiDesign.md` 5.5、手順17c）。
   *
   * チケット詳細の「子チケットを追加」から開くときに真にする。**「なし」を
   * 選べる状態のままにすると、子を作るつもりで開いたのにトップレベルの
   * チケットができる**——欄は出したまま、選び直せない形にする。
   */
  lockParent?: boolean
  busy?: boolean
  /** サーバが返した検証エラー（`details[].field` → メッセージ） */
  fieldErrors?: Record<string, string>
}>()

const emit = defineEmits<{ close: []; save: [body: CreateTicketRequest] }>()

/** 上限（`ApiDesign.md` 9.3）。正本はサーバ側 */
const MAX_TITLE = 200

/**
 * **選択肢はストーリーとタスクの2つ**（`GuiDesign.md` 5.4.3）。
 * エピックは `エピック[…]` のパネルから、`epicMode` で開いて作る。
 */
const types: TicketType[] = props.epicMode ? ['epic'] : backlogTicketTypes

const type = ref<TicketType>(props.epicMode ? 'epic' : 'task')
const title = ref('')

/**
 * **説明欄はテンプレートで始める**（5.4.3）。`placeholder` にしないのは、
 * 1文字打つと見出しごと消えてしまい、書かせたい項目に機能しないためである。
 * 触らずに作成した場合もそのまま本文として送る（初期値であるとはそういう意味）。
 */
const bodyMd = ref(newTicketBodyTemplate())
const priority = ref<TicketPriority | ''>('')
const assigneeId = ref(props.defaults?.assignee_id ?? '')
const assigneeOptions = computed<ActorSelectOption[]>(() => [
  { value: '', label: uiText('未割当') },
  ...props.members.map((m) => ({
    value: m.actor_id, label: m.display_name,
    actor: { id: m.actor_id, kind: m.kind, display_name: m.display_name },
  })),
])
const parentSeq = ref(props.defaults?.parent_seq !== undefined ? String(props.defaults.parent_seq) : '')
const epicSeq = ref(props.defaults?.epic_seq !== undefined ? String(props.defaults.epic_seq) : '')
const tagIds = ref<string[]>([...(props.defaults?.tag_ids ?? [])])
const estimatePoint = ref('')

// ── 予定（5.4.3「終日」、7.5「予定日時の出し方」）──────────────────────
//
// **終日は基準タイムゾーンの日付、時刻付きは見る人の日時**で入力する（詳細ペインと同じ）。
// 入力欄の文字列で持ち、送るときと切り替えるときに瞬間へ直す。
const planTz = useProjectStore().planTimezone
const allDay = ref(props.defaults?.all_day ?? true)

/** 瞬間を、いまの「終日」に合った入力欄の文字列にする */
function inputOf(ms: number | undefined, isEnd: boolean): string {
  if (ms === undefined) return ''
  return allDay.value ? planDate(ms, planTz, isEnd) : localInputOf(ms)
}

/** 入力欄の文字列を瞬間へ戻す（`inputOf` の逆）。空なら null */
function instantOf(raw: string, isEnd: boolean): number | null {
  if (raw === '') return null
  return allDay.value ? planInstant(raw, planTz, isEnd) : instantOfLocalInput(raw)
}

const startDate = ref(inputOf(props.defaults?.start_at, false))
const dueDate = ref(inputOf(props.defaults?.due_at, true))

/**
 * 「終日」の切り替え。**入力済みの値は保つ**——終日→時刻付きは日の境界の時刻（0時）に、
 * 時刻付き→終日はその瞬間を含む日付にする（詳細ペインの `toggleAllDay` と同じ向き）。
 */
function setAllDay(next: boolean): void {
  if (allDay.value === next) return
  const s = instantOf(startDate.value, false)
  const e = instantOf(dueDate.value, true)
  allDay.value = next
  startDate.value = inputOf(s ?? undefined, false)
  dueDate.value = inputOf(e ?? undefined, true)
}

/** 入力済みかどうか。触る前から赤く出さない */
const touched = ref(false)

/** 優先度は高い順に並べる。走査するとき上から強いものを見たい */
const priorities = computed(() => [...priorityOrder].reverse())

const titleError = computed(() => {
  const v = title.value.trim()
  if (v === '') return uiText("タイトルを入力してください")
  if (v.length > MAX_TITLE) return uiText("{value0}文字以内で入力してください", { value0: MAX_TITLE })
  return null
})

/**
 * 期間の前後関係（`DbDesign.md` 6.6 の `ck_ticket_dates`）。
 *
 * **サーバも同じ検証をする。** ここで見るのは往復する前に気づけるように
 * するためであって、こちらを正本にするためではない。
 */
const dateError = computed(() => {
  const s = instantOf(startDate.value, false)
  const e = instantOf(dueDate.value, true)
  if (s === null || e === null) return null
  // 終日は半開区間の終わり（翌日の0時）なので同じ日は通る。時刻付きは同時刻を許さない（9.3.1）
  if (allDay.value ? s > e : s >= e) {
    return allDay.value ? uiText("期限は開始日以降の日付を指定してください") : uiText("終了は開始より後の日時を指定してください")
  }
  return null
})

const estimateError = computed(() => {
  if (estimatePoint.value === '') return null
  const n = Number(estimatePoint.value)
  if (!Number.isFinite(n) || n < 0) return uiText("0以上の数値を入力してください")
  return null
})

const canSave = computed(
  () => titleError.value === null && dateError.value === null && estimateError.value === null,
)

function toggleTag(id: string): void {
  tagIds.value = tagIds.value.includes(id)
    ? tagIds.value.filter((v) => v !== id)
    : [...tagIds.value, id]
}

/**
 * 親の候補ラベル。**一覧と同じ完全形 `my-app-12 DB設計` の形で出す**（5.4「ID列」）。
 *
 * 接尾だけの `-12` は負の数に見える（実機で判明）。
 * 組み立てはフロントが行う（`ApiDesign.md` 9.1。サーバは組み立てない）。
 */
function candidateLabel(t: Ticket): string {
  return `${props.projectKey}-${t.seq} ${t.title}`
}

// ── 親チケットとエピック（5.4.3）──────────────────────
//
// **保存されるのは `parent_seq` の1列だけ**で、2つの欄はそこから導く。
// エピックを選べるのは親チケットが無いときだけ——配下のツリーは親と一緒に
// エピックへ属する（`ApiDesign.md` 9.2.1 の `parent` は部分木で絞る）。

const parentOptions = computed(() => props.candidates.filter((c) => c.type !== 'epic'))

/**
 * 親チケット欄を出すか。**候補が0件なら出さない**（ダッシュボードは一覧を持たない）。
 * 固定されているときは値があれば出す——エピックに固定したときは値が無いので出さない。
 */
const showParent = computed(
  () =>
    !props.epicMode &&
    (parentSeq.value !== '' || (!props.lockParent && parentOptions.value.length > 0)),
)

const epicOptions = computed(() => props.epics ?? [])

/** エピック欄を出すか。**エピックが1件も無ければ出さない**（5.4.3） */
const showEpic = computed(
  () => !props.epicMode && (epicSeq.value !== '' || epicOptions.value.length > 0),
)

/**
 * `POST` なので、**値が無い項目はキーごと落とす**（`ApiDesign.md` 9.3）。
 *
 * `PATCH`（9.5.2）と違い `null` に「消す」の意味は無く、送らないことが
 * 「指定しない」である。空文字を送ると型の検証に落ちる。
 */
function submit(): void {
  touched.value = true
  if (!canSave.value) return

  const body: CreateTicketRequest = { type: type.value, title: title.value.trim() }
  if (bodyMd.value.trim() !== '') body.body_md = bodyMd.value
  if (priority.value !== '') body.priority = priority.value
  if (assigneeId.value !== '') body.assignee_id = assigneeId.value
  // 親チケットが勝つ。エピック欄はそのとき選べない状態で、値だけが残っている（5.4.3）
  if (!props.epicMode) {
    if (parentSeq.value !== '') body.parent_seq = Number(parentSeq.value)
    else if (epicSeq.value !== '') body.parent_seq = Number(epicSeq.value)
  }
  if (tagIds.value.length > 0) body.tag_ids = [...tagIds.value]
  if (estimatePoint.value !== '') body.estimate_point = Number(estimatePoint.value)
  // 終日は基準タイムゾーンの0時（期限は締切日の翌日の0時）、時刻付きは見る人の日時（pb-217・pb-327）
  const s = instantOf(startDate.value, false)
  const e = instantOf(dueDate.value, true)
  if (s !== null) body.start_at = s
  if (e !== null) body.due_at = e
  // 予定が無くても「終日を外した」は送る。後から時刻を入れたとき時刻付きのままにする
  if (s !== null || e !== null || !allDay.value) body.all_day = allDay.value

  emit('save', body)
}
</script>

<template>
  <Modal :title="epicMode ? $ui('新規エピック') : $ui('新規チケット')" @close="emit('close')">
    <form id="new-ticket-form" class="form" @submit.prevent="submit">
      <div class="row">
        <label class="field type">
          <span class="label">{{ $ui('種別') }} <span class="required">*</span></span>
          <!-- 新規エピックでは変えられない（5.4.3「新規エピック」） -->
          <select v-model="type" :disabled="epicMode">
            <option v-for="t in types" :key="t" :value="t">
              {{ ticketTypeIcons[t] }} {{ ticketTypeLabels[t] }}
            </option>
          </select>
        </label>

        <label class="field grow">
          <span class="label">{{ $ui('タイトル') }} <span class="required">*</span></span>
          <input
            v-model="title"
            type="text"
            :maxlength="MAX_TITLE"
            :aria-invalid="touched && titleError !== null"
            @blur="touched = true"
          />
          <!-- 状態を色だけで示さない（9.2）ので記号を添える -->
          <span v-if="touched && titleError" class="detail">✕ {{ titleError }}</span>
          <span v-else-if="fieldErrors?.title" class="detail">✕ {{ fieldErrors.title }}</span>
        </label>
      </div>

      <label class="field">
        <span class="label">{{ $ui('説明') }}</span>
        <!-- Markdown のライブプレビューはチケット詳細（5.5）の役目。
             ここは素の textarea に留める -->
        <textarea v-model="bodyMd" rows="7" :placeholder="$ui('Markdown で書けます')"></textarea>
      </label>

      <div class="row">
        <label class="field grow">
          <span class="label">{{ $ui('優先度') }}</span>
          <select v-model="priority">
            <option value="">{{ $ui('未設定') }}</option>
            <option v-for="p in priorities" :key="p" :value="p">{{ priorityLabels[p] }}</option>
          </select>
        </label>

        <div class="field grow">
          <span class="label">{{ $ui('担当') }}</span>
          <!-- 選択肢はプロジェクトのメンバーに限る（9.3 は非メンバーを 422 で弾く） -->
          <ActorSelect :value="assigneeId" :options="assigneeOptions" :label="$ui('担当')"
            variant="modal" :disabled="busy" @change="assigneeId = $event" />
        </div>
      </div>

      <div v-if="showParent || showEpic" class="row">
        <label v-if="showParent" class="field grow">
          <span class="label">{{ $ui('親チケット') }}</span>
          <!-- **固定するときは「なし」を出さない。** 出したまま選べなくすると
               「選べるのに選べない」に見える（5.5、手順17c） -->
          <select v-model="parentSeq" :disabled="lockParent">
            <option v-if="!lockParent" value="">{{ $ui('なし') }}</option>
            <option v-for="c in parentOptions" :key="c.seq" :value="String(c.seq)">
              {{ candidateLabel(c) }}
            </option>
          </select>
          <span v-if="parentSeq !== '' && fieldErrors?.parent_seq" class="detail">✕ {{ fieldErrors.parent_seq }}</span>
        </label>

        <label v-if="showEpic" class="field grow">
          <span class="label">{{ $ui('エピック') }}</span>
          <!-- **親チケットがあるときは選べない**（5.4.3「親チケットとエピック」）。
               値は捨てずに持っておき、親を「なし」に戻したら元の選択が見える -->
          <select v-if="parentSeq !== ''" disabled>
            <option>{{ $ui('親チケットに従う') }}</option>
          </select>
          <select v-else v-model="epicSeq" :disabled="lockParent">
            <option v-if="!lockParent" value="">{{ $ui('なし') }}</option>
            <option v-for="e in epicOptions" :key="e.seq" :value="String(e.seq)">
              {{ ticketTypeIcons.epic }} {{ candidateLabel(e) }}
            </option>
          </select>
          <span v-if="parentSeq === '' && fieldErrors?.parent_seq" class="detail">✕ {{ fieldErrors.parent_seq }}</span>
        </label>
      </div>

      <!-- タグは複数付く（`ticket_tag` は多対多）。ここから新規作成はできない
           ——定義はプロジェクト設定のタグタブ（5.9.4） -->
      <fieldset v-if="tags.length > 0" class="field tags">
        <legend class="label">{{ $ui('タグ') }}</legend>
        <div class="tag-list">
          <label v-for="t in tags" :key="t.id" class="tag-choice">
            <input
              type="checkbox"
              :checked="tagIds.includes(t.id)"
              :aria-label="t.name"
              @change="toggleTag(t.id)"
            />
            <span class="tag-name">{{ t.name }}</span>
          </label>
        </div>
      </fieldset>

      <div class="row">
        <label class="field estimate">
          <span class="label">{{ $ui('見積（ポイント）') }}</span>
          <input
            v-model="estimatePoint"
            type="number"
            min="0"
            step="0.5"
            :aria-invalid="estimateError !== null"
          />
          <span v-if="estimateError" class="detail">✕ {{ estimateError }}</span>
        </label>

        <!-- 終日の切り替え（5.4.3）。外すと開始・期限が日時の入力欄になる（pb-327） -->
        <label class="all-day">
          <input type="checkbox" :checked="allDay" @change="setAllDay(($event.target as HTMLInputElement).checked)" />
          {{ $ui('終日') }}
        </label>
      </div>

      <div class="row">
        <label class="field grow">
          <span class="label">{{ allDay ? $ui('開始日') : $ui('開始') }}</span>
          <!-- 種類ごとに作り直す（key）。同じ要素の type を変えると、値が先に入って古い type で捨てられる -->
          <input :key="allDay ? 'date' : 'time'" v-model="startDate" :type="allDay ? 'date' : 'datetime-local'" />
        </label>

        <label class="field grow">
          <span class="label">{{ allDay ? $ui('期限') : $ui('終了') }}</span>
          <input :key="allDay ? 'date' : 'time'" v-model="dueDate" :type="allDay ? 'date' : 'datetime-local'" :aria-invalid="dateError !== null" />
        </label>
      </div>
      <span v-if="dateError" class="detail">✕ {{ dateError }}</span>
      <span v-else-if="fieldErrors?.due_at" class="detail">✕ {{ fieldErrors.due_at }}</span>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')"> {{ $ui('キャンセル') }} </button>
      <button
        type="submit"
        form="new-ticket-form"
        class="primary"
        :disabled="!canSave || busy"
      > {{ $ui('作成') }} </button>
    </template>
  </Modal>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
}

.row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-4);
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: none;
}

.grow {
  flex: 1 1 160px;
}

.type {
  flex: 0 0 150px;
}

/* 見積と「終日」を1行に、開始・期限を次の行に並べる。日時の入力欄は日付より
   幅を取るので、見積と同じ行に3つ並べると折り返す（パネルは 520px） */
.estimate {
  flex: 0 0 130px;
}

.all-day {
  display: inline-flex;
  align-items: center;
  align-self: flex-end;
  gap: var(--pb-space-1);
  height: 36px;
  font-size: 13px;
  cursor: pointer;
}

.label {
  padding: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.required {
  color: var(--pb-danger-text);
}

input[type='text'],
input[type='number'],
input[type='date'],
input[type='datetime-local'],
select {
  width: 100%;
  height: 36px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

/* 親を固定したとき（`lockParent`）。**読めることを優先し、薄くしすぎない**
   ——この欄は「何の子を作っているか」を伝える唯一の表示である */
select:disabled {
  background: var(--pb-surface);
  color: var(--pb-text-muted);
  cursor: default;
}

textarea {
  width: 100%;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  resize: vertical;
}

input[aria-invalid='true'] {
  border-color: var(--pb-danger-border);
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

/* タグは枠線＋文字で出す（8.6）。色は使わない */
.tag-list {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-2);
}

.tag-choice {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  height: 28px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  cursor: pointer;
}

.tag-choice:hover {
  background: var(--pb-hover);
}

.tag-name {
  font-size: 13px;
}
</style>
