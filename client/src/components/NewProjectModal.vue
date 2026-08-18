<script setup lang="ts">
/**
 * 新規プロジェクト作成モーダル（GuiDesign.md 5.2.1）。
 *
 * **プロジェクトキーは明示入力とし、変更不可とする。** 名前から候補を自動入力
 * するが、フィールドは隠さず利用者が必ず確認・確定する（5.2.1）。
 *
 * キーの検証（形式・予約語・重複）は `GET /projects/check-key` に委ねる
 * （`ApiDesign.md` 5.2、debounce 400ms）。**正規表現も予約語一覧もここに
 * 複製しない。** 二重管理になり、片方だけ変わると食い違うため。
 *
 * 作成時の重複検出は `check-key` の結果ではなくサーバの 409 で受ける
 * （5.3 の TOCTOU 対策）。
 */
import { computed, ref, watch } from 'vue'

import Modal from './Modal.vue'
import { ApiError } from '../api/client'
import * as projectsApi from '../api/projects'
import type { ProjectDetail, WorkflowTemplate } from '../api/projects'

const emit = defineEmits<{ close: []; created: [project: ProjectDetail] }>()

/** `ApiDesign.md` 5.2「debounce 400ms でフロントから呼ぶ」 */
const CHECK_KEY_DEBOUNCE_MS = 400

/** 入力の上限（`ApiDesign.md` 5.3 の検証表）。サーバ側の検証が正本 */
const MAX_NAME = 100
const MAX_KEY = 20
const MAX_DESCRIPTION = 1000

const name = ref('')
const key = ref('')
const description = ref('')
const workflowTemplate = ref<WorkflowTemplate>('simple')

/** ワークフローの選択肢。ラベルは 5.2.1 の図のまま。実体は `DbDesign.md` 7.4 */
const templates: { value: WorkflowTemplate; label: string }[] = [
  { value: 'simple', label: 'シンプル（未着手 / 進行中 / 完了）' },
  { value: 'with_review', label: 'レビュー付き（+ レビュー中）' },
  { value: 'with_approval', label: '承認フロー付き（ウォーターフォール向け）' },
]

/** 利用者がキー欄を自分で触ったか。触った後は名前からの自動入力を止める */
const keyTouched = ref(false)

const submitting = ref(false)
const error = ref<ApiError | null>(null)

/**
 * キーの判定状態。
 *
 * `check-key` は使えない理由を `reason` の enum でしか返さない（文言を持たない）。
 * 表示する文言は下の `KEY_REASON_TEXT` が持つ。**フロントが文言を持つのは
 * ここだけ**であり、サーバが `message` を返す経路では 2.5 のとおりそのまま出す。
 */
type KeyState = 'idle' | 'checking' | 'ok' | 'ng'
const keyState = ref<KeyState>('idle')
const keyReason = ref<string | null>(null)

const KEY_REASON_TEXT: Record<string, string> = {
  invalid_format: '半角英小文字・数字・ハイフン、2〜20文字で入力してください',
  reserved: 'このキーは予約できません',
  already_exists: 'このキーは既に使われています',
}

const keyMessage = computed(() => {
  if (keyState.value === 'ok') return '使用可能です'
  if (keyState.value === 'ng') {
    return keyReason.value !== null
      ? (KEY_REASON_TEXT[keyReason.value] ?? 'このキーは使用できません')
      : 'このキーは使用できません'
  }
  return ''
})

/** 入力欄に紐づくエラー（`ApiDesign.md` 2.5 の details）。ログイン画面と同じ扱い */
const nameDetail = computed(() => error.value?.detailFor('name'))
const keyDetail = computed(() => error.value?.detailFor('key'))
const descriptionDetail = computed(() => error.value?.detailFor('description'))

/** 入力欄に紐づかないエラーだけを上部に出す（同じ文言を二重に見せない） */
const generalError = computed(() => {
  const e = error.value
  if (!e) return null
  if (e.details.length > 0) return null
  return e.message
})

const canSubmit = computed(
  () =>
    !submitting.value &&
    name.value.trim() !== '' &&
    key.value !== '' &&
    keyState.value !== 'ng',
)

/**
 * 名前からキーの候補を作る（5.2.1「英数小文字＋ハイフン化」）。
 *
 * 日本語の名前では結果が空になる。その場合はキー欄を空のままにして、
 * 利用者が自分で決める（フィールドを隠さないのが 5.2.1 の趣旨）。
 */
function slugify(source: string): string {
  return source
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+/, '')
    .slice(0, MAX_KEY)
    .replace(/-+$/, '')
}

watch(name, (next) => {
  if (keyTouched.value) return
  key.value = slugify(next)
})

/**
 * `check-key` の debounce。
 *
 * `seq` は応答の追い越し対策。続けて入力すると複数のリクエストが並ぶため、
 * 遅れて届いた古い応答で新しい判定を上書きさせない（一覧ストアと同じ方式）。
 */
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0

watch(key, (next) => {
  clearTimeout(timer)
  // キーを打ち直したら、前回の送信で付いたキー欄のエラーは消す
  if (keyDetail.value) error.value = null

  if (next === '') {
    seq++
    keyState.value = 'idle'
    keyReason.value = null
    return
  }

  keyState.value = 'checking'
  timer = setTimeout(() => {
    void check(next)
  }, CHECK_KEY_DEBOUNCE_MS)
})

async function check(target: string) {
  const mine = ++seq
  try {
    const res = await projectsApi.checkProjectKey(target)
    if (mine !== seq) return
    keyState.value = res.available ? 'ok' : 'ng'
    keyReason.value = res.reason ?? null
  } catch {
    // 判定できなかっただけで入力は妨げない。作成時にサーバが改めて検証する
    if (mine !== seq) return
    keyState.value = 'idle'
    keyReason.value = null
  }
}

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  error.value = null
  try {
    const project = await projectsApi.createProject({
      key: key.value,
      name: name.value.trim(),
      description: description.value.trim() === '' ? undefined : description.value.trim(),
      workflow_template: workflowTemplate.value,
    })
    emit('created', project)
  } catch (e: unknown) {
    error.value =
      e instanceof ApiError
        ? e
        : new ApiError({
            status: 0,
            code: 'internal_error',
            message: '予期しないエラーが発生しました',
          })
    // 409 で戻ってきたキーは使えない。判定表示も合わせておく
    if (error.value.detailFor('key')) {
      keyState.value = 'ng'
      keyReason.value = null
    }
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal title="新規プロジェクト" @close="emit('close')">
    <form id="new-project-form" class="form" @submit.prevent="submit">
      <p v-if="generalError" class="alert" role="alert">{{ generalError }}</p>

      <label class="field">
        <span class="label">プロジェクト名 <span class="required">*</span></span>
        <input
          v-model="name"
          type="text"
          name="name"
          :maxlength="MAX_NAME"
          :aria-invalid="nameDetail !== undefined"
          :disabled="submitting"
        />
        <span v-if="nameDetail" class="detail">{{ nameDetail.message }}</span>
      </label>

      <label class="field">
        <span class="label">プロジェクトキー <span class="required">*</span></span>
        <input
          v-model="key"
          type="text"
          name="key"
          :maxlength="MAX_KEY"
          autocapitalize="off"
          autocomplete="off"
          spellcheck="false"
          :aria-invalid="keyState === 'ng' || keyDetail !== undefined"
          :disabled="submitting"
          @input="keyTouched = true"
        />
        <!-- 判定は 5.2.1 の `✓ 使用可能です`。状態を色だけで示さない（9.2）ので
             記号を必ず添える -->
        <span
          v-if="keyDetail"
          class="detail"
          role="alert"
        >{{ keyDetail.message }}</span>
        <span
          v-else
          class="check"
          :class="keyState"
          aria-live="polite"
        >
          <template v-if="keyState === 'ok'">✓ {{ keyMessage }}</template>
          <template v-else-if="keyState === 'ng'">✕ {{ keyMessage }}</template>
          <template v-else-if="keyState === 'checking'">確認中…</template>
        </span>
      </label>

      <!-- 変更不可の明示（5.2.1）。用途一覧と警告を常時表示する -->
      <div class="note">
        <p class="note-head">ⓘ 以下で使われる短い識別子です。</p>
        <dl class="uses">
          <dt>チケット番号</dt>
          <dd>my-app-123</dd>
          <dt>URL</dt>
          <dd>/p/my-app/…</dd>
          <dt>エージェント連携</dt>
          <dd>/mcp/my-app</dd>
          <dt>Gitブランチ規約</dt>
          <dd>pb/123</dd>
        </dl>
        <p class="rule">半角英小文字・数字・ハイフン、2〜20文字。</p>
        <!-- warning は「面」で表す（8.4.1）。文字はベース色のまま -->
        <p class="warn">⚠ 作成後は変更できません。</p>
      </div>

      <label class="field">
        <span class="label">説明</span>
        <textarea
          v-model="description"
          name="description"
          rows="3"
          :maxlength="MAX_DESCRIPTION"
          :aria-invalid="descriptionDetail !== undefined"
          :disabled="submitting"
        ></textarea>
        <span v-if="descriptionDetail" class="detail">{{ descriptionDetail.message }}</span>
      </label>

      <fieldset class="field">
        <legend class="label">ワークフロー</legend>
        <label v-for="t in templates" :key="t.value" class="radio">
          <input
            v-model="workflowTemplate"
            type="radio"
            name="workflow_template"
            :value="t.value"
            :disabled="submitting"
          />
          <span>{{ t.label }}</span>
        </label>
      </fieldset>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="submitting" @click="emit('close')">
        キャンセル
      </button>
      <button type="submit" form="new-project-form" class="primary" :disabled="!canSubmit">
        {{ submitting ? '作成中…' : '作成' }}
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

/* 失敗は danger を文字・枠線で表す（8.4.1 / 8.6） */
.alert {
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-danger-bg);
  color: var(--pb-danger-text);
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.label {
  padding: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.required {
  color: var(--pb-danger-text);
}

input[type='text'],
textarea {
  width: 100%;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

input[type='text'] {
  height: 36px;
}

textarea {
  padding: var(--pb-space-2) var(--pb-space-3);
  resize: vertical;
}

input[aria-invalid='true'],
textarea[aria-invalid='true'] {
  border-color: var(--pb-danger-border);
}

.detail {
  font-size: 13px;
  color: var(--pb-danger-text);
}

/* キーの判定行。高さを固定して、出入りで下の内容が動かないようにする */
.check {
  min-height: 18px;
  font-size: 13px;
  color: var(--pb-text-muted);
}

.check.ok {
  color: var(--pb-text);
}

.check.ng {
  color: var(--pb-danger-text);
}

/* ── キーの用途と警告（5.2.1）────────────────────────────── */
.note {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.note-head {
  color: var(--pb-text);
}

.uses {
  display: grid;
  grid-template-columns: max-content 1fr;
  gap: var(--pb-space-1) var(--pb-space-4);
  margin: 0;
}

.uses dt::before {
  content: '・';
}

.uses dd {
  margin: 0;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.rule {
  margin: 0;
}

.warn {
  margin: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border-left: 3px solid var(--pb-warning);
  background: var(--pb-warning-bg);
  color: var(--pb-text);
}

/* ── ワークフロー ──────────────────────────────────────── */
.radio {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  padding: var(--pb-space-1) 0;
  cursor: pointer;
}

/* ── フッタのボタン ────────────────────────────────────── */
.primary,
.secondary {
  height: 32px;
  padding: 0 var(--pb-space-4);
  border-radius: var(--pb-radius);
  font-weight: 600;
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

.primary:disabled,
.secondary:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
