<script setup lang="ts">
/**
 * 自律実行をやめる（GuiDesign.md 5.5「自律実行」、pb-281）。
 *
 * **戻す先の実行モードを選ぶ。既定は選ばれていない**——PM が「誰が仕上げるか」を
 * 決め直す操作なので、黙って1つを選んだ状態にしない。理由は任意で、根のコメントに残る。
 */
import { ref } from 'vue'

import { executionModeLabels, executionModeOptions, type TicketExecutionMode } from '../api/tickets'
import Modal from './Modal.vue'

defineProps<{
  busy?: boolean
  error?: string | null
}>()

const emit = defineEmits<{
  close: []
  submit: [body: { execution_mode: Exclude<TicketExecutionMode, 'delegated'>; reason_md: string }]
}>()

const modes = executionModeOptions.filter(
  (m): m is Exclude<TicketExecutionMode, 'delegated'> => m !== 'delegated',
)
const mode = ref<Exclude<TicketExecutionMode, 'delegated'> | null>(null)
const reason = ref('')

function submit(): void {
  if (!mode.value) return
  emit('submit', { execution_mode: mode.value, reason_md: reason.value.trim() })
}
</script>

<template>
  <Modal :title="$ui('自律実行をやめますか？')" @close="emit('close')">
    <form id="delegation-withdraw-form" class="form" @submit.prevent="submit">
      <p class="lead">{{ $ui('子チケットは残ります。回答していないユーザ確認待ちは取り下げます') }}</p>
      <fieldset class="modes">
        <legend class="label">{{ $ui('戻す先の実行モード') }}</legend>
        <label v-for="m in modes" :key="m" class="mode">
          <input v-model="mode" type="radio" name="withdraw-mode" :value="m" />
          {{ executionModeLabels[m] }}
        </label>
      </fieldset>
      <label class="field">
        <span class="label">{{ $ui('理由（任意）') }}</span>
        <textarea v-model="reason" rows="3"></textarea>
      </label>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    </form>
    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button type="submit" form="delegation-withdraw-form" class="primary" :disabled="busy || !mode">
        {{ $ui('自律実行をやめる') }}
      </button>
    </template>
  </Modal>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
}

.lead {
  margin: 0;
  font-size: 13px;
}

.modes {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  margin: 0;
  padding: 0;
  border: 0;
}

.mode {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  font-size: 13px;
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
}

.label {
  padding: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

textarea {
  width: 100%;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
  resize: vertical;
}

.field-error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
