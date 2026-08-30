<script setup lang="ts">
/**
 * 文書の新規作成（`GuiDesign.md` 5.10）。
 *
 * **`[ 別名で保存 ]` の受け皿でもある**（5.10「競合したとき」）。編集中の本文を
 * `initialBody` で受け、親は元の文書と同じものを既定にし、**`slug` と `title` は
 * 空で開く**——`-copy` のような名前を自動で付けない。`slug` は URL になり、
 * 後から直すと共有リンクが切れる（`ApiDesign.md` 10.4）ので、慌てている場面ほど
 * 名前を人に決めさせる。
 *
 * **この部品が API を呼ぶ。** `409 already_exists`（同じ親の下に同じ `slug`）と
 * `422` の `details` を**欄の下に出す**ため（6.4）で、呼び出し側へ投げ返すと
 * モーダルを閉じてから誤りを見せることになる。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import { ApiError } from '../api/client'
import { createDoc, type Doc, type DocTreeItem } from '../api/docs'

const props = withDefaults(
  defineProps<{
    projectKey: string
    /** 親の候補を作るための目次（10.2） */
    tree: DocTreeItem[]
    /** 既定の親。`null` はトップレベル */
    initialParentPath?: string | null
    /** 既定の本文。`[ 別名で保存 ]` は編集中の本文をここへ載せる */
    initialBody?: string
  }>(),
  { initialParentPath: null, initialBody: '' },
)

const emit = defineEmits<{ close: []; created: [doc: Doc] }>()

/** `DbDesign.md` 8.1.1 の CHECK と同じ式。サーバと同じものを画面にも持つ */
const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/
const MAX_TITLE = 200

const title = ref('')
const slug = ref('')
const parentPath = ref<string>(props.initialParentPath ?? '')
const body = ref(props.initialBody)

const touched = ref(false)
const busy = ref(false)

/** サーバが返した欄ごとの誤り（2.5 の `details`）。送るたびに作り直す */
const serverDetails = ref<Record<string, string>>({})
/** 欄に紐づかない誤り。**モーダルの中に留める**（6.4） */
const formError = ref<string | null>(null)

/**
 * 親の候補。**木を深さ優先で平らにし、段数ぶん字下げして見せる。**
 *
 * `<select>` の選択肢に階層は描けないので、全角空白で段を作る。
 */
interface ParentOption {
  path: string
  label: string
}

function flatten(items: DocTreeItem[], depth: number, out: ParentOption[]): void {
  for (const item of items) {
    out.push({ path: item.path, label: `${'　'.repeat(depth)}${item.title}` })
    flatten(item.children, depth + 1, out)
  }
}

const parentOptions = computed<ParentOption[]>(() => {
  const out: ParentOption[] = []
  flatten(props.tree, 0, out)
  return out
})

const titleError = computed(() => {
  if (serverDetails.value.title) return serverDetails.value.title
  const v = title.value.trim()
  if (v === '') return 'タイトルを入力してください'
  if (v.length > MAX_TITLE) return `${MAX_TITLE}文字以内で入力してください`
  return null
})

const slugError = computed(() => {
  if (serverDetails.value.slug) return serverDetails.value.slug
  const v = slug.value.trim()
  if (v === '') return 'スラッグを入力してください'
  if (!SLUG_PATTERN.test(v)) {
    return '英小文字・数字・ハイフンのみ、先頭は英小文字か数字で、64文字以内'
  }
  return null
})

const parentError = computed(() => serverDetails.value.parent_path ?? null)

const canSave = computed(
  () => !busy.value && titleError.value === null && slugError.value === null,
)

async function submit(): Promise<void> {
  touched.value = true
  serverDetails.value = {}
  formError.value = null
  if (!canSave.value) return

  busy.value = true
  try {
    const doc = await createDoc(props.projectKey, {
      title: title.value.trim(),
      slug: slug.value.trim(),
      parent_path: parentPath.value === '' ? null : parentPath.value,
      body_md: body.value,
    })
    emit('created', doc)
  } catch (e) {
    if (e instanceof ApiError) {
      // **409 は欄の誤りとして出す**（2.5.1 の `already_exists` は `details` を
      // 伴わない）。「同じ親の下に同じ slug がある」は slug の欄の話である
      if (e.status === 409) {
        serverDetails.value = { slug: e.message }
      } else if (e.details.length > 0) {
        const next: Record<string, string> = {}
        for (const d of e.details) if (d.field) next[d.field] = d.message
        serverDetails.value = next
      } else {
        formError.value = e.message
      }
    } else {
      formError.value = '文書を作成できませんでした'
    }
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal title="文書を追加" @close="emit('close')">
    <form id="doc-form" class="form" @submit.prevent="submit">
      <p v-if="formError" class="form-error">✕ {{ formError }}</p>

      <label class="field">
        <span class="label">タイトル <span class="required">*</span></span>
        <input
          v-model="title"
          type="text"
          :maxlength="MAX_TITLE"
          :aria-invalid="touched && titleError !== null"
          @blur="touched = true"
        />
        <span v-if="touched && titleError" class="detail">✕ {{ titleError }}</span>
      </label>

      <label class="field">
        <span class="label">スラッグ <span class="required">*</span></span>
        <input
          v-model="slug"
          type="text"
          class="slug"
          placeholder="naming"
          autocapitalize="off"
          autocomplete="off"
          spellcheck="false"
          maxlength="64"
          :aria-invalid="touched && slugError !== null"
          @blur="touched = true"
        />
        <!-- **URL になることを入力の時点で見せる**（`ApiDesign.md` 10.1）。
             後から直すと共有リンクが切れるので、決める前に形を見せる -->
        <span class="hint">
          URL になります：/p/{{ projectKey }}/docs/{{
            parentPath === '' ? '' : `${parentPath}/`
          }}{{ slug.trim() === '' ? '…' : slug.trim() }}
        </span>
        <span v-if="touched && slugError" class="detail">✕ {{ slugError }}</span>
      </label>

      <label class="field">
        <span class="label">親の文書</span>
        <select v-model="parentPath">
          <option value="">（トップレベル）</option>
          <option v-for="o in parentOptions" :key="o.path" :value="o.path">
            {{ o.label }}
          </option>
        </select>
        <span v-if="parentError" class="detail">✕ {{ parentError }}</span>
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')">
        キャンセル
      </button>
      <button type="submit" form="doc-form" class="primary" :disabled="!canSave">
        追加
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

.form-error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
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

input[type='text'],
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

.slug {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}

.hint {
  overflow-wrap: anywhere;
  color: var(--pb-text-muted);
  font-size: 12px;
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

.secondary:hover:not(:disabled) {
  background: var(--pb-hover);
}

button:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
