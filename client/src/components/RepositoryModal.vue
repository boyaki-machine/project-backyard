<script setup lang="ts">
/**
 * リポジトリの追加・編集（`GuiDesign.md` 5.9.1）。
 *
 * **確定しても画面上の一覧を書き換えるだけで、サーバへは送らない。**
 * 永続化は一般タブの `[保存]` 1つに集約する（5.9.1）。
 *
 * 入力の検証をここに閉じ込めているのは、**不正な行が一覧へ入らないようにする**
 * ためである。一覧側で弾く作りにすると、空行が1つあるだけで他の項目
 * （名前・説明）の保存まで止まる（手順11b の実機確認で起きた）。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import type { ProjectRepository } from '../api/projects'

const props = defineProps<{
  /** 編集対象。新規追加のときは空の値を渡す */
  repository: ProjectRepository
  /** 新規か編集か。見出しと確定ボタンの文言だけが変わる */
  isNew: boolean
}>()

const emit = defineEmits<{ close: []; save: [repository: ProjectRepository] }>()

/** 上限（5.9.1）。`settings` はサーバが検証しないため画面が持つ */
const MAX_URL = 1000
const MAX_NAME = 100
const MAX_DESCRIPTION = 200

// props をそのまま編集しない。キャンセルで元へ戻せるように複製して持つ。
const url = ref(props.repository.url)
const name = ref(props.repository.name ?? '')
const description = ref(props.repository.description ?? '')

/** 入力済みかどうか。触る前から赤く出さない */
const touched = ref(false)

const urlError = computed(() => {
  const v = url.value.trim()
  if (v === '') return 'URLを入力してください'
  if (v.length > MAX_URL) return `${MAX_URL}文字以内で入力してください`
  return null
})

const canSave = computed(() => urlError.value === null)

function submit() {
  touched.value = true
  if (!canSave.value) return
  const next: ProjectRepository = { url: url.value.trim() }
  // 空文字は保存しない。次に読んだとき「設定された空の名前」と区別できないため
  if (name.value.trim() !== '') next.name = name.value.trim()
  if (description.value.trim() !== '') next.description = description.value.trim()
  emit('save', next)
}
</script>

<template>
  <Modal :title="isNew ? 'リポジトリを追加' : 'リポジトリを編集'" @close="emit('close')">
    <form id="repository-form" class="form" @submit.prevent="submit">
      <label class="field">
        <span class="label">URL <span class="required">*</span></span>
        <input
          v-model="url"
          type="text"
          class="url"
          placeholder="https://… または git@host:org/repo.git"
          autocapitalize="off"
          autocomplete="off"
          spellcheck="false"
          :maxlength="MAX_URL"
          :aria-invalid="touched && urlError !== null"
          @blur="touched = true"
        />
        <!-- 状態を色だけで示さない（9.2）ので記号を添える -->
        <span v-if="touched && urlError" class="detail">✕ {{ urlError }}</span>
      </label>

      <label class="field">
        <span class="label">表示名</span>
        <input v-model="name" type="text" :maxlength="MAX_NAME" />
      </label>

      <label class="field">
        <span class="label">説明</span>
        <input v-model="description" type="text" :maxlength="MAX_DESCRIPTION" />
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">キャンセル</button>
      <button type="submit" form="repository-form" class="primary" :disabled="!canSave">
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

.url {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

.primary,
.secondary {
  display: inline-flex;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border-radius: var(--pb-radius);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}

.primary {
  border: 1px solid var(--pb-accent);
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.primary:hover:not(:disabled) {
  border-color: var(--pb-accent-hover);
  background: var(--pb-accent-hover);
}

.secondary {
  border: 1px solid var(--pb-border);
  background: var(--pb-surface);
  color: inherit;
}

.secondary:hover {
  background: var(--pb-hover);
}

button:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
