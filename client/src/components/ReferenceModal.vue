<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * 参考リンクの追加・編集（`GuiDesign.md` 5.5「コードと参考リンク」）。手順17c。
 *
 * **`doc` だけを扱う。** `kind='code'` は画面に追加の導線を置かない——この欄は
 * エージェントの作業記録であり、人が手で書くものではない（5.5、利用者の判断
 * 2026-08-27）。削除だけは両方に置く。
 *
 * **送信そのものは呼び出し側が行う**（`RepositoryModal` / `SprintModal` と同じ形）。
 * サーバの検証エラーを欄の下へ出す責務は呼び出し側にあり、ここは
 * 「入力を集めて確定を伝える」までを持つ。
 *
 * 入力の検証をここに閉じ込めているのは、**不正な行が送られないようにする**
 * ためである（`RepositoryModal` と同じ理由）。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import type { TicketReference } from '../api/references'

const props = defineProps<{
  /** 編集対象。新規追加のときは `null` */
  reference: TicketReference | null
  busy?: boolean
  /** サーバが返した検証エラー（`details[].field` → メッセージ） */
  fieldErrors?: Record<string, string>
}>()

const emit = defineEmits<{
  close: []
  save: [body: { url: string; label: string | null; note: string | null }]
}>()

/** 上限（`ApiDesign.md` 9.10.2）。正本はサーバ側 */
const MAX_URL = 1000
const MAX_LABEL = 200
const MAX_NOTE = 500

const isNew = computed(() => props.reference === null)

// props をそのまま編集しない。キャンセルで元へ戻せるように複製して持つ。
const url = ref(props.reference?.url ?? '')
const label = ref(props.reference?.label ?? '')
const note = ref(props.reference?.note ?? '')

/** 入力済みかどうか。触る前から赤く出さない */
const touched = ref(false)

const urlError = computed(() => {
  const v = url.value.trim()
  if (v === '') return uiText("URLを入力してください")
  if (v.length > MAX_URL) return uiText("{value0}文字以内で入力してください", { value0: MAX_URL })
  return null
})

const canSave = computed(() => urlError.value === null && props.busy !== true)

function errorFor(field: string): string | undefined {
  return props.fieldErrors?.[field]
}

function submit(): void {
  touched.value = true
  if (!canSave.value) return
  emit('save', {
    url: url.value.trim(),
    // 空文字は送らない。**`null` は「その項目を空にする」**（9.10.2）なので、
    // 編集で消したときにちゃんと消える。
    label: label.value.trim() === '' ? null : label.value.trim(),
    note: note.value.trim() === '' ? null : note.value.trim(),
  })
}
</script>

<template>
  <Modal :title="isNew ? $ui('参考リンクを追加') : $ui('参考リンクを編集')" @close="emit('close')">
    <form id="reference-form" class="form" @submit.prevent="submit">
      <label class="field">
        <span class="label">URL <span class="required">*</span></span>
        <input
          v-model="url"
          type="text"
          class="ref-url"
          placeholder="https://…"
          autocapitalize="off"
          autocomplete="off"
          spellcheck="false"
          :maxlength="MAX_URL"
          :aria-invalid="(touched && urlError !== null) || errorFor('url') !== undefined"
          @blur="touched = true"
        />
        <!-- 状態を色だけで示さない（9.2）ので記号を添える -->
        <span v-if="touched && urlError" class="detail">✕ {{ urlError }}</span>
        <span v-else-if="errorFor('url')" class="detail">✕ {{ errorFor('url') }}</span>
        <span v-else class="hint"> {{ $ui('https:// か http:// で始まるものはリンクになります') }} </span>
      </label>

      <label class="field">
        <span class="label">{{ $ui('ラベル') }}</span>
        <input v-model="label" type="text" :maxlength="MAX_LABEL" :placeholder="$ui('認証設計メモ')" />
        <span v-if="errorFor('label')" class="detail">✕ {{ errorFor('label') }}</span>
        <span v-else class="hint">{{ $ui('省略するとURLをそのまま見せます') }}</span>
      </label>

      <label class="field">
        <span class="label">{{ $ui('メモ') }}</span>
        <input v-model="note" type="text" :maxlength="MAX_NOTE" />
        <span v-if="errorFor('note')" class="detail">✕ {{ errorFor('note') }}</span>
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button type="submit" form="reference-form" class="primary" :disabled="!canSave">
        {{ isNew ? $ui("追加") : $ui("更新") }}
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

input[type='text'] {
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

.ref-url {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

.hint {
  color: var(--pb-text-muted);
  font-size: 12px;
}
</style>
