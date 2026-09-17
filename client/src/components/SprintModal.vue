<script setup lang="ts">
/**
 * スプリントの追加・編集（`GuiDesign.md` 5.9.5）。
 *
 * **タグ（5.9.4）と作りが違う。** タグは編集できるのが名前1つなので行内で
 * 直接いじるが、スプリントは `name` / `goal` / 期間 / `status` の5項目あり、
 * 表の行には入らない。ワイヤーのボタン名がそれぞれ `[名前を変更]` と
 * `[編集]` に分かれているのも同じ理由による。
 *
 * **リポジトリのモーダル（5.9.1）とも違い、確定でサーバへ送る。**
 * あちらは `project.settings` の jsonb を一般タブの `[保存]` にまとめる
 * 作りだが、スプリントは独立したエンドポイントを持つ（`ApiDesign.md` 9.12）。
 * 送信そのものは呼び出し側が行い、ここは値を組み立てて渡すだけである。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import { sprintStatusLabels } from '../api/sprints'
import type { CreateSprintRequest, Sprint, SprintStatus } from '../api/sprints'

const props = defineProps<{
  /** 編集対象。新規追加のときは null */
  sprint: Sprint | null
  /** 送信中。二重送信を防ぐ */
  busy?: boolean
  /** サーバが返した検証エラー（`details[].field` → メッセージ） */
  fieldErrors?: Record<string, string>
}>()

const emit = defineEmits<{ close: []; save: [body: CreateSprintRequest] }>()

/** 上限（`ApiDesign.md` 9.12）。正本はサーバ側 */
const MAX_NAME = 50

const isNew = computed(() => props.sprint === null)

// props をそのまま編集しない。キャンセルで元へ戻せるように複製して持つ。
const name = ref(props.sprint?.name ?? '')
const goal = ref(props.sprint?.goal ?? '')
const startDate = ref(props.sprint?.start_date ?? '')
const endDate = ref(props.sprint?.end_date ?? '')
const status = ref<SprintStatus>(props.sprint?.status ?? 'planned')

/** 入力済みかどうか。触る前から赤く出さない */
const touched = ref(false)

const statusOptions = Object.entries(sprintStatusLabels) as [SprintStatus, string][]

const nameError = computed(() => {
  const v = name.value.trim()
  if (v === '') return 'スプリント名を入力してください'
  if (v.length > MAX_NAME) return `${MAX_NAME}文字以内で入力してください`
  return null
})

/**
 * 期間の前後関係（`DbDesign.md` 6.9 の `ck_sprint_dates`）。
 *
 * **サーバも同じ検証をする。** ここで見るのは、往復する前に気づけるように
 * するためであって、こちらを正本にするためではない。
 */
const dateError = computed(() => {
  if (startDate.value === '' || endDate.value === '') return null
  if (startDate.value > endDate.value) return '終了日は開始日以降の日付を指定してください'
  return null
})

const canSave = computed(() => nameError.value === null && dateError.value === null)

function submit() {
  touched.value = true
  if (!canSave.value) return

  // **空文字は null で送る。** キーを落とすと PATCH では「据え置き」に
  // なってしまい、欄を空にする操作が効かない（`ApiDesign.md` 9.12）。
  emit('save', {
    name: name.value.trim(),
    goal: goal.value.trim() === '' ? null : goal.value.trim(),
    start_date: startDate.value === '' ? null : startDate.value,
    end_date: endDate.value === '' ? null : endDate.value,
    status: status.value,
  })
}
</script>

<template>
  <Modal :title="isNew ? 'スプリントを追加' : 'スプリントを編集'" @close="emit('close')">
    <form id="sprint-form" class="form" @submit.prevent="submit">
      <label class="field">
        <span class="label">スプリント名 <span class="required">*</span></span>
        <input
          v-model="name"
          type="text"
          :maxlength="MAX_NAME"
          :aria-invalid="touched && nameError !== null"
          @blur="touched = true"
        />
        <!-- 状態を色だけで示さない（9.2）ので記号を添える -->
        <span v-if="touched && nameError" class="detail">✕ {{ nameError }}</span>
        <span v-else-if="fieldErrors?.name" class="detail">✕ {{ fieldErrors.name }}</span>
      </label>

      <label class="field">
        <span class="label">ゴール</span>
        <input v-model="goal" type="text" placeholder="このスプリントで達成したいこと" />
      </label>

      <div class="dates">
        <label class="field">
          <span class="label">開始日</span>
          <input v-model="startDate" type="date" />
        </label>
        <label class="field">
          <span class="label">終了日</span>
          <input
            v-model="endDate"
            type="date"
            :aria-invalid="dateError !== null"
          />
        </label>
      </div>
      <span v-if="dateError" class="detail">✕ {{ dateError }}</span>
      <span v-else-if="fieldErrors?.end_date" class="detail">✕ {{ fieldErrors.end_date }}</span>

      <label class="field">
        <span class="label">状態</span>
        <select v-model="status">
          <option v-for="[key, label] in statusOptions" :key="key" :value="key">
            {{ label }}
          </option>
        </select>
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')">
        キャンセル
      </button>
      <button type="submit" form="sprint-form" class="primary" :disabled="!canSave || busy">
        {{ isNew ? '追加' : '更新' }}
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

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.required {
  color: var(--pb-danger-text);
}

/* 開始日と終了日を横に並べる。狭い窓では縦に折り返す */
.dates {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-4);
}

.dates .field {
  flex: 1 1 160px;
}

input[type='text'],
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

input[aria-invalid='true'] {
  border-color: var(--pb-danger-border);
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
