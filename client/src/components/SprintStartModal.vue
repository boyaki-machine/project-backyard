<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * スプリントを開始する（`GuiDesign.md` 5.4「スプリントを開始・終了する」）。
 *
 * **`SprintModal`（5.9.5）とは役割が違う。** あちらは**定義**——名前と期間を
 * 作る・直す・消すためのもので、プロジェクト設定のタブが出す。こちらは**運用**で、
 * バックログのオンステージ段が出す。
 *
 * **`status` を持たない。** 開始したスプリントは必ず `active` であり、選ばせる
 * 意味がない（`ApiDesign.md` 9.12.1）。
 *
 * **対象を選ばせない。** 対象は「オンステージに載っているもの全部」で、
 * それは既に画面に並んでいる。ここでは**件数だけ**を出して、押す前に規模が
 * 分かるようにする——**判定はサーバが部分木ごと行う**ので、この数は
 * 手元の表示から作った参考値である。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import type { StartSprintRequest } from '../api/sprints'

const props = defineProps<{
  /** オンステージ段に出ている行数（配下を含む。エピックを除く） */
  onstageCount: number
  /** 送信中。二重送信を防ぐ */
  busy?: boolean
  /** サーバが返した検証エラー（`details[].field` → メッセージ） */
  fieldErrors?: Record<string, string>
}>()

const emit = defineEmits<{ close: []; start: [body: StartSprintRequest] }>()

/** 上限（`ApiDesign.md` 9.12）。正本はサーバ側 */
const MAX_NAME = 50

/**
 * **名前に既定値を入れない**（`GuiDesign.md` 5.4）。連番を推測して `Sprint 4` と
 * 埋めると、命名規約が違うプロジェクトで毎回消させることになる。
 */
const name = ref('')
const goal = ref('')

/** **開始日の既定は今日**（5.4）。いま始めるのだから、その日が入っていてよい */
const startDate = ref(todayISO())
const endDate = ref('')

const touched = ref(false)

/**
 * 端末の日付で「今日」を作る。
 *
 * **`toISOString()` を使わない。** あれは UTC へ寄せるので、日本時間の朝9時より
 * 前に開くと前日になる。`date` 列は時刻を持たない（`ApiDesign.md` 9.12）ので、
 * 見ている人の暦の今日をそのまま入れる。
 */
function todayISO(): string {
  const d = new Date()
  const m = `${d.getMonth() + 1}`.padStart(2, '0')
  const day = `${d.getDate()}`.padStart(2, '0')
  return `${d.getFullYear()}-${m}-${day}`
}

const nameError = computed(() => {
  const v = name.value.trim()
  if (v === '') return uiText("スプリント名を入力してください")
  if (v.length > MAX_NAME) return uiText("{value0}文字以内で入力してください", { value0: MAX_NAME })
  return null
})

/**
 * 期間の前後関係（`DbDesign.md` 6.9 の `ck_sprint_dates`）。
 * サーバも同じ検証をする——ここで見るのは往復する前に気づけるようにするため。
 */
const dateError = computed(() => {
  if (startDate.value === '' || endDate.value === '') return null
  if (startDate.value > endDate.value) return uiText("終了日は開始日以降の日付を指定してください")
  return null
})

const canStart = computed(() => nameError.value === null && dateError.value === null)

function submit() {
  touched.value = true
  if (!canStart.value) return
  emit('start', {
    name: name.value.trim(),
    goal: goal.value.trim() === '' ? null : goal.value.trim(),
    start_date: startDate.value === '' ? null : startDate.value,
    end_date: endDate.value === '' ? null : endDate.value,
  })
}
</script>

<template>
  <Modal :title="$ui('スプリントを開始')" @close="emit('close')">
    <form id="sprint-start-form" class="form" @submit.prevent="submit">
      <label class="field">
        <span class="label">{{ $ui('スプリント名') }} <span class="required">*</span></span>
        <input
          v-model="name"
          type="text"
          :maxlength="MAX_NAME"
          :aria-invalid="touched && nameError !== null"
          @blur="touched = true"
        />
        <!-- 状態を色だけで示さない（GuiDesign.md 9.2）ので記号を添える -->
        <span v-if="touched && nameError" class="detail">✕ {{ nameError }}</span>
        <span v-else-if="fieldErrors?.name" class="detail">✕ {{ fieldErrors.name }}</span>
      </label>

      <label class="field">
        <span class="label">{{ $ui('ゴール') }}</span>
        <input v-model="goal" type="text" :placeholder="$ui('このスプリントで達成したいこと')" />
      </label>

      <div class="dates">
        <label class="field">
          <span class="label">{{ $ui('開始日') }}</span>
          <input v-model="startDate" type="date" />
        </label>
        <label class="field">
          <span class="label">{{ $ui('終了日') }}</span>
          <input v-model="endDate" type="date" :aria-invalid="dateError !== null" />
        </label>
      </div>
      <span v-if="dateError" class="detail">✕ {{ dateError }}</span>
      <span v-else-if="fieldErrors?.end_date" class="detail">✕ {{ fieldErrors.end_date }}</span>

      <!--
        件数は押す前に規模が分かるようにするためのもの。**0件でも開始できる**
        （GuiDesign.md 5.4）——期間を先に切ってから積む進め方がある。
      -->
      <p class="scope">
        <template v-if="onstageCount > 0"> {{ $ui('オンステージの') }} <strong>{{ onstageCount }}</strong> {{ $ui('件が対象になります。') }} </template>
        <template v-else> {{ $ui('オンステージは空です。対象が無いままスプリントを始められます。') }} </template>
      </p>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')"> {{ $ui('キャンセル') }} </button>
      <button
        type="submit"
        form="sprint-start-form"
        class="primary"
        :disabled="!canStart || busy"
      > {{ $ui('開始') }} </button>
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
input[type='date'] {
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

.scope {
  margin: 0;
  padding: var(--pb-space-3);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
