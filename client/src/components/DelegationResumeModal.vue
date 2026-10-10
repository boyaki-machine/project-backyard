<script setup lang="ts">
/**
 * 自律実行の再開（GuiDesign.md 5.5「自律実行」、pb-252）。
 *
 * **再開のときに制約を直せる。** 一時停止のあいだはエージェントが範囲の変更を出せない
 * ので、PM が制約を直す口はここにしかない（Delegation.md 12.1）。**直したときだけ
 * 送る**——変えていない制約を送ると、範囲の版が無駄に1つ進む。
 *
 * 計画中へ戻る委任では、制約を直せない（サーバが 422 で拒む。計画を却下して
 * 出し直させる）。そのときは入力欄を出さず、理由を出す。
 */
import { computed, ref } from 'vue'

import { constraintsOf, type DelegationConstraints } from '../api/delegation'
import DelegationConstraintsEditor from './DelegationConstraintsEditor.vue'
import Modal from './Modal.vue'

const props = defineProps<{
  scope: unknown
  /** 承認済の範囲があるか（無ければ計画中へ戻り、制約を直せない） */
  canEditConstraints: boolean
  busy?: boolean
  error?: string | null
}>()

const emit = defineEmits<{
  close: []
  submit: [body: { budget?: DelegationConstraints['budget']; stop_conditions?: string[]; reason_md?: string }]
}>()

const original = constraintsOf(props.scope)
const draft = ref<DelegationConstraints>(constraintsOf(props.scope))
const reason = ref('')

const cleaned = computed(() => draft.value.stop_conditions.map((c) => c.trim()).filter((c) => c !== ''))

function submit(): void {
  const body: { budget?: DelegationConstraints['budget']; stop_conditions?: string[]; reason_md?: string } = {}
  if (props.canEditConstraints) {
    if (JSON.stringify(draft.value.budget) !== JSON.stringify(original.budget)) body.budget = draft.value.budget
    if (JSON.stringify(cleaned.value) !== JSON.stringify(original.stop_conditions)) body.stop_conditions = cleaned.value
  }
  if (reason.value.trim() !== '') body.reason_md = reason.value.trim()
  emit('submit', body)
}
</script>

<template>
  <Modal :title="$ui('自律実行を再開しますか？')" @close="emit('close')">
    <form id="delegation-resume-form" class="form" @submit.prevent="submit">
      <DelegationConstraintsEditor v-if="canEditConstraints" v-model="draft" :disabled="busy" />
      <p v-else class="lead">
        {{ $ui('計画の承認前に止まったため、計画中に戻ります。制約を直したいときは、計画を却下して出し直してもらってください') }}
      </p>
      <label class="field">
        <span class="label">{{ $ui('一言（任意）') }}</span>
        <textarea v-model="reason" rows="2" :placeholder="$ui('エージェントへの申し送りがあれば')"></textarea>
      </label>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    </form>
    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button type="submit" form="delegation-resume-form" class="primary" :disabled="busy">{{ $ui('再開する') }}</button>
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

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
}

.label {
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
