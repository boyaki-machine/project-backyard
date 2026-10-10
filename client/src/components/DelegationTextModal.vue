<script setup lang="ts">
/**
 * 自律実行の操作に一言を添えるモーダル（GuiDesign.md 5.5「自律実行」、pb-252）。
 * 計画の却下（理由は必須）、ユーザ確認待ちへの回答（必須）、一時停止（任意）で使う。
 *
 * 書いた文は委任を回しているチケットのコメントに残り、エージェントが読む
 * （却下の理由は、main が計画を出し直す材料になる）。
 */
import { computed, ref } from 'vue'

import { renderMarkdown } from '../lib/markdown'
import Modal from './Modal.vue'

const props = defineProps<{
  title: string
  /** 何をするのかの説明（1〜2文。画面の固定文） */
  lead?: string
  /**
   * エージェントが書いた問い（Markdown。pb-280）。**整形して出す**——番号付きの問いや
   * 強調を1段落に潰すと読めない。lead と両方あれば、lead の下に置く
   */
  questionMd?: string
  label: string
  placeholder?: string
  required?: boolean
  submitLabel: string
  busy?: boolean
  error?: string | null
}>()

const emit = defineEmits<{ close: []; submit: [text: string] }>()

const text = ref('')
const question = computed(() => renderMarkdown(props.questionMd ?? ''))

function submit(): void {
  const t = text.value.trim()
  if (props.required && t === '') return
  emit('submit', t)
}
</script>

<template>
  <Modal :title="title" @close="emit('close')">
    <form id="delegation-text-form" class="form" @submit.prevent="submit">
      <p v-if="lead" class="lead">{{ lead }}</p>
      <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
      <div v-if="question" class="markdown-body question" v-html="question"></div>
      <label class="field">
        <span class="label">{{ label }}</span>
        <textarea v-model="text" rows="4" :placeholder="placeholder" :required="required"></textarea>
      </label>
      <p v-if="error" class="field-error" role="alert">{{ error }}</p>
    </form>
    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button
        type="submit"
        form="delegation-text-form"
        class="primary"
        :disabled="busy || (required && text.trim() === '')"
      >
        {{ submitLabel }}
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

.question {
  max-height: 40vh;
  padding: var(--pb-space-2) var(--pb-space-3);
  overflow: auto;
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  font-size: 13px;
  overflow-wrap: anywhere;
}

.question :deep(p),
.question :deep(ol),
.question :deep(ul) {
  margin: 0 0 var(--pb-space-2);
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
