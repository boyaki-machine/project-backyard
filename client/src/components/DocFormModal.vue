<script setup lang="ts">
/**
 * 文書の新規作成と、移動・改名（`GuiDesign.md` 5.10）。
 *
 * **1つの部品が2つのモードを持つ。** 扱う欄が `title` / `slug` / `parent_path` の
 * 3つで完全に同じだからである（6.1）——**`slug` と `title` はどちらも「名前」で
 * あり、隣に並べたほうが取り違えない**（5.10）。`doc` を渡すと移動・改名になる。
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
import {
  createDoc,
  updateDoc,
  type Doc,
  type DocTreeItem,
  type PatchDocRequest,
} from '../api/docs'

const props = withDefaults(
  defineProps<{
    projectKey: string
    /** 親の候補を作るための目次（10.2） */
    tree: DocTreeItem[]
    /**
     * 移動・改名の対象。`null` は新規作成。
     *
     * **目次の行をそのまま受ける。** `version` を持っているので（10.2）、
     * `If-Match` のために本文を取り直さずに済む——木を1回取れば `PATCH` できる。
     */
    doc?: DocTreeItem | null
    /** 既定の親。`null` はトップレベル（新規作成のときだけ効く） */
    initialParentPath?: string | null
    /** 既定の本文。`[ 別名で保存 ]` は編集中の本文をここへ載せる */
    initialBody?: string
  }>(),
  { doc: null, initialParentPath: null, initialBody: '' },
)

const emit = defineEmits<{ close: []; created: [doc: Doc]; saved: [doc: Doc] }>()

/** `DbDesign.md` 8.1.1 の CHECK と同じ式。サーバと同じものを画面にも持つ */
const SLUG_PATTERN = /^[a-z0-9][a-z0-9-]{0,63}$/
const MAX_TITLE = 200

const editing = computed(() => props.doc !== null)

/**
 * 対象の現在の親のパス。**`path` から末尾の `slug` を落として作る**——
 * 目次（10.2）は `parent_path` を返さないが、`path` は `slug` を根から
 * 連ねたものなので（10.1）、区切りの最後で切れば親が出る。
 */
const currentParentPath = computed<string | null>(() => {
  const d = props.doc
  if (d === null) return null
  const cut = d.path.lastIndexOf('/')
  return cut < 0 ? null : d.path.slice(0, cut)
})

const title = ref(props.doc?.title ?? '')
const slug = ref(props.doc?.slug ?? '')
const parentPath = ref<string>(
  (props.doc !== null ? currentParentPath.value : props.initialParentPath) ?? '',
)
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
 *
 * **移動・改名のときは自分自身と自分の子孫を外す**（5.10 の「自分の子孫へは
 * 落とせない」と同じ理由）。10.4 は `cycle` の 422 を返すが、**選べないものを
 * 出さないほうが先である**（設計原則4）。**部分木ごと枝を刈る**ので、
 * 深さ優先の再帰の途中で降りるのをやめればよい。
 */
interface ParentOption {
  path: string
  label: string
}

function flatten(items: DocTreeItem[], depth: number, out: ParentOption[]): void {
  for (const item of items) {
    if (props.doc !== null && item.id === props.doc.id) continue
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

/**
 * サーバの誤りを欄へ配る。
 *
 * **409 を2つに割る**（10.6）。`already_exists` は「同じ親の下に同じ `slug` が
 * ある」で `slug` 欄の話だが、**`conflict` は `If-Match` の不一致**であって
 * どの欄の誤りでもない。**`POST` は `conflict` を返さない**ので新規作成の
 * ときは前者しか来ないが、`PATCH` は両方を返しうる。
 */
function applyError(e: unknown): void {
  if (!(e instanceof ApiError)) {
    formError.value = editing.value ? '文書を更新できませんでした' : '文書を作成できませんでした'
    return
  }
  if (e.code === 'already_exists') {
    serverDetails.value = { slug: e.message }
  } else if (e.details.length > 0) {
    const next: Record<string, string> = {}
    for (const d of e.details) if (d.field) next[d.field] = d.message
    serverDetails.value = next
  } else {
    formError.value = e.message
  }
}

async function submit(): Promise<void> {
  touched.value = true
  serverDetails.value = {}
  formError.value = null
  if (!canSave.value) return

  busy.value = true
  try {
    if (props.doc === null) {
      const doc = await createDoc(props.projectKey, {
        title: title.value.trim(),
        slug: slug.value.trim(),
        parent_path: parentPath.value === '' ? null : parentPath.value,
        body_md: body.value,
      })
      emit('created', doc)
    } else {
      // **変わった欄だけ送る。** 送らない欄は `PATCH` が触らない（10.4）ので、
      // 何も変わっていなければ `version` を動かさずに閉じられる
      const nextParent = parentPath.value === '' ? null : parentPath.value
      const patch: PatchDocRequest = {}
      if (title.value.trim() !== props.doc.title) patch.title = title.value.trim()
      if (slug.value.trim() !== props.doc.slug) patch.slug = slug.value.trim()
      if (nextParent !== currentParentPath.value) patch.parent_path = nextParent

      if (Object.keys(patch).length === 0) {
        emit('close')
        return
      }
      const doc = await updateDoc(
        props.projectKey,
        props.doc.path,
        props.doc.version,
        patch,
      )
      emit('saved', doc)
    }
  } catch (e) {
    applyError(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Modal :title="editing ? '移動・改名' : '文書を追加'" @close="emit('close')">
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
        <!-- **部分木ごと動くことを先に伝える**（10.4）。畳んでいると、
             一緒に動く範囲が画面から見えない（削除の確認と同じ考え方。6.3） -->
        <span v-if="editing && doc && doc.children.length > 0" class="hint">
          配下の文書も一緒に移動し、それぞれの URL が変わります
        </span>
        <span v-if="parentError" class="detail">✕ {{ parentError }}</span>
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')">
        キャンセル
      </button>
      <button type="submit" form="doc-form" class="primary" :disabled="!canSave">
        {{ editing ? '保存' : '追加' }}
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

button:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
