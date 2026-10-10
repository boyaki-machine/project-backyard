<script setup lang="ts">
/**
 * 周期の要約と、指標の値の入力（GuiDesign.md 5.5「自律実行」、`Delegation.md` 8.3。pb-277）。
 *
 * **要約は PB が記録から組み立てたもの**（`summary_md`）をそのまま出す。期間は直近の周期
 * （前回の要約の確認から、いままで）。値を入れると要約を読み直す。
 */
import { computed, onMounted, ref } from 'vue'

import Modal from './Modal.vue'
import { getDelegationSummary, recordDelegationMetric, type DelegationSummary } from '../api/delegation'
import { delegationErrorText } from '../composables/useDelegationStatus'
import { renderMarkdown } from '../lib/markdown'

const props = defineProps<{
  projectKey: string
  seq: number
  /** 値を入れられるか（人で、自律実行が終わっていない） */
  canRecord: boolean
}>()

const emit = defineEmits<{ close: []; changed: [] }>()

const summary = ref<DelegationSummary | null>(null)
const loadError = ref<string | null>(null)
const html = computed(() => renderMarkdown(summary.value?.summary_md ?? ''))

async function load(): Promise<void> {
  try {
    summary.value = await getDelegationSummary(props.projectKey, props.seq)
    loadError.value = null
    if (!metric.value) metric.value = summary.value.metrics[0]?.key ?? ''
  } catch (e) {
    loadError.value = delegationErrorText(e)
  }
}
onMounted(load)

const metric = ref('')
/** **v-model は type="number" の値を数に直す**（Vue）。空のときは '' のまま来るので両方を受ける */
const value = ref<string | number>('')
const valueText = computed(() => String(value.value ?? '').trim())
const note = ref('')
const busy = ref(false)
const error = ref<string | null>(null)

async function record(): Promise<void> {
  const n = Number(valueText.value)
  if (!metric.value || valueText.value === '' || Number.isNaN(n)) return
  busy.value = true
  error.value = null
  try {
    await recordDelegationMetric(props.projectKey, props.seq, metric.value, n, note.value.trim())
    value.value = ''
    note.value = ''
    await load()
    emit('changed')
  } catch (e) {
    error.value = delegationErrorText(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :title="$ui('周期の要約')" @close="emit('close')">
    <p v-if="loadError" class="error">✕ {{ loadError }}</p>
    <p v-else-if="!summary" class="muted">{{ $ui('読み込み中…') }}</p>
    <template v-else>
      <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
      <div class="markdown-body summary" v-html="html"></div>
      <form v-if="canRecord && summary.metrics.length > 0" class="record" @submit.prevent="record">
        <span class="label">{{ $ui('指標の値を入れる') }}</span>
        <div class="row">
          <select v-model="metric" :aria-label="$ui('指標')">
            <option v-for="m in summary.metrics" :key="m.key" :value="m.key">{{ m.name }}</option>
          </select>
          <input v-model="value" type="number" step="any" :aria-label="$ui('値')" :placeholder="$ui('値')" />
          <input v-model="note" type="text" maxlength="2000" class="note" :aria-label="$ui('メモ')" :placeholder="$ui('どう測ったか（任意）')" />
          <button type="submit" class="primary" :disabled="busy || valueText === ''">{{ $ui('入れる') }}</button>
        </div>
        <p v-if="error" class="error" role="alert">{{ error }}</p>
      </form>
    </template>
    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('閉じる') }}</button>
    </template>
  </Modal>
</template>

<style scoped>
.summary {
  max-height: 50vh;
  overflow-y: auto;
  font-size: 13px;
}

.record {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  margin-top: var(--pb-space-3);
  padding-top: var(--pb-space-3);
  border-top: 1px solid var(--pb-line);
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-2);
}

.row input[type='number'] {
  width: 96px;
}

.note {
  flex: 1 1 160px;
  min-width: 0;
}

input,
select {
  padding: 2px var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
}

.muted {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
