<script lang="ts">
/**
 * グループ化中のセクションから開いたときの初期値（`GuiDesign.md` 5.4.3）。
 *
 * **`<script setup>` は export を持てない**ので、型だけを通常の script ブロックへ
 * 出している（同じ SFC の2ブロック構成は Vue の標準の書き方）。
 */
export interface NewTicketDefaults {
  parent_seq?: number
  tag_ids?: string[]
  assignee_id?: string
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

const props = defineProps<{
  /** ID を完全形で出すために要る（`GuiDesign.md` 5.4「ID列」） */
  projectKey: string
  members: ProjectMember[]
  tags: Tag[]
  /** 親の選択肢。バックログがいま表示しているチケット（5.4.3） */
  candidates: Ticket[]
  defaults?: NewTicketDefaults
  /**
   * 親を `defaults.parent_seq` に固定する（`GuiDesign.md` 5.5、手順17c）。
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
 * エピックはフィルタであり、ここからは作らない。
 */
const types: TicketType[] = backlogTicketTypes

const type = ref<TicketType>('task')
const title = ref('')

/**
 * **説明欄はテンプレートで始める**（5.4.3）。`placeholder` にしないのは、
 * 1文字打つと見出しごと消えてしまい、書かせたい項目に機能しないためである。
 * 触らずに作成した場合もそのまま本文として送る（初期値であるとはそういう意味）。
 */
const bodyMd = ref(newTicketBodyTemplate)
const priority = ref<TicketPriority | ''>('')
const assigneeId = ref(props.defaults?.assignee_id ?? '')
const parentSeq = ref(props.defaults?.parent_seq !== undefined ? String(props.defaults.parent_seq) : '')
const tagIds = ref<string[]>([...(props.defaults?.tag_ids ?? [])])
const estimatePoint = ref('')
const startDate = ref('')
const dueDate = ref('')

/** 入力済みかどうか。触る前から赤く出さない */
const touched = ref(false)

/** 優先度は高い順に並べる。走査するとき上から強いものを見たい */
const priorities = computed(() => [...priorityOrder].reverse())

const titleError = computed(() => {
  const v = title.value.trim()
  if (v === '') return 'タイトルを入力してください'
  if (v.length > MAX_TITLE) return `${MAX_TITLE}文字以内で入力してください`
  return null
})

/**
 * 期間の前後関係（`DbDesign.md` 6.6 の `ck_ticket_dates`）。
 *
 * **サーバも同じ検証をする。** ここで見るのは往復する前に気づけるように
 * するためであって、こちらを正本にするためではない。
 */
const dateError = computed(() => {
  if (startDate.value === '' || dueDate.value === '') return null
  if (startDate.value > dueDate.value) return '期限は開始日以降の日付を指定してください'
  return null
})

const estimateError = computed(() => {
  if (estimatePoint.value === '') return null
  const n = Number(estimatePoint.value)
  if (!Number.isFinite(n) || n < 0) return '0以上の数値を入力してください'
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
 * 接尾だけの `-12` は負の数に見える（実機で判明。利用者の指摘、2026-08-23）。
 * 組み立てはフロントが行う（`ApiDesign.md` 9.1。サーバは組み立てない）。
 */
function candidateLabel(t: Ticket): string {
  return `${props.projectKey}-${t.seq} ${t.title}`
}

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
  if (parentSeq.value !== '') body.parent_seq = Number(parentSeq.value)
  if (tagIds.value.length > 0) body.tag_ids = [...tagIds.value]
  if (estimatePoint.value !== '') body.estimate_point = Number(estimatePoint.value)
  if (startDate.value !== '') body.start_date = startDate.value
  if (dueDate.value !== '') body.due_date = dueDate.value

  emit('save', body)
}
</script>

<template>
  <Modal title="新規チケット" @close="emit('close')">
    <form id="new-ticket-form" class="form" @submit.prevent="submit">
      <div class="row">
        <label class="field type">
          <span class="label">種別 <span class="required">*</span></span>
          <select v-model="type">
            <option v-for="t in types" :key="t" :value="t">
              {{ ticketTypeIcons[t] }} {{ ticketTypeLabels[t] }}
            </option>
          </select>
        </label>

        <label class="field grow">
          <span class="label">タイトル <span class="required">*</span></span>
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
        <span class="label">説明</span>
        <!-- Markdown のライブプレビューはチケット詳細（5.5）の役目。
             ここは素の textarea に留める -->
        <textarea v-model="bodyMd" rows="7" placeholder="Markdown で書けます"></textarea>
      </label>

      <div class="row">
        <label class="field grow">
          <span class="label">優先度</span>
          <select v-model="priority">
            <option value="">未設定</option>
            <option v-for="p in priorities" :key="p" :value="p">{{ priorityLabels[p] }}</option>
          </select>
        </label>

        <label class="field grow">
          <span class="label">担当</span>
          <!-- 選択肢はプロジェクトのメンバーに限る（9.3 は非メンバーを 422 で弾く） -->
          <select v-model="assigneeId">
            <option value="">未割当</option>
            <option v-for="m in members" :key="m.actor_id" :value="m.actor_id">
              {{ m.kind === 'agent' ? '🤖' : '👤' }} {{ m.display_name }}
            </option>
          </select>
        </label>
      </div>

      <div class="row">
        <label class="field grow">
          <span class="label">親チケット</span>
          <!-- **固定するときは「なし」を出さない。** 出したまま選べなくすると
               「選べるのに選べない」に見える（5.5、手順17c） -->
          <select v-model="parentSeq" :disabled="lockParent">
            <option v-if="!lockParent" value="">なし</option>
            <option v-for="c in candidates" :key="c.seq" :value="String(c.seq)">
              {{ candidateLabel(c) }}
            </option>
          </select>
          <span v-if="fieldErrors?.parent_seq" class="detail">✕ {{ fieldErrors.parent_seq }}</span>
        </label>

      </div>

      <!-- タグは複数付く（`ticket_tag` は多対多）。ここから新規作成はできない
           ——定義はプロジェクト設定のタグタブ（5.9.4） -->
      <fieldset v-if="tags.length > 0" class="field tags">
        <legend class="label">タグ</legend>
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
          <span class="label">見積（ポイント）</span>
          <input
            v-model="estimatePoint"
            type="number"
            min="0"
            step="0.5"
            :aria-invalid="estimateError !== null"
          />
          <span v-if="estimateError" class="detail">✕ {{ estimateError }}</span>
        </label>

        <label class="field grow">
          <span class="label">開始日</span>
          <input v-model="startDate" type="date" />
        </label>

        <label class="field grow">
          <span class="label">期限</span>
          <input v-model="dueDate" type="date" :aria-invalid="dateError !== null" />
        </label>
      </div>
      <span v-if="dateError" class="detail">✕ {{ dateError }}</span>
      <span v-else-if="fieldErrors?.due_date" class="detail">✕ {{ fieldErrors.due_date }}</span>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')">
        キャンセル
      </button>
      <button
        type="submit"
        form="new-ticket-form"
        class="primary"
        :disabled="!canSave || busy"
      >
        作成
      </button>
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

/* 見積・開始日・期限は1行に収める。基準値を 160px のままにすると3つ目が
   折り返し、期限だけが幅いっぱいで下に残る（パネルは 520px） */
.estimate {
  flex: 0 0 130px;
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
