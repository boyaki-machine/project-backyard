<script setup lang="ts">
/**
 * ユーザー追加モーダル（`GuiDesign.md` 5.6.1）。
 *
 * 入力の正本はサーバの検証（`ApiDesign.md` 6.2）である。ここが持つのは
 * **送る前に分かる不足**（必須と最低文字数）だけで、形式の判定は複製しない。
 * メールの重複は 409 で受ける。
 *
 * 既定値は 6.2 とワイヤーフレームに揃える——ロールは `operator`、
 * パスワードは `generate`、初回変更の要求はオン。
 *
 * **モーダルはURLを持たない**（3.2）。開閉は `UsersPage` が持つ。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import { ApiError } from '../api/client'
import * as usersApi from '../api/users'
import type { CreatedUser } from '../api/users'
import { systemRoleLabel } from '../lib/roles'

const emit = defineEmits<{ close: []; created: [user: CreatedUser] }>()

/** 入力の上限（`ApiDesign.md` 6.2 の検証表）。正本はサーバ側 */
const MAX_DISPLAY_NAME = 60
const MAX_EMAIL = 254
/** `Design.md` 6.3 の最低長。サーバも同じ値で弾く */
const MIN_PASSWORD = 12

type SystemRole = 'operator' | 'administrator'
type PasswordMode = 'generate' | 'manual'

const displayName = ref('')
const email = ref('')
const systemRole = ref<SystemRole>('operator')
const passwordMode = ref<PasswordMode>('generate')
const password = ref('')
const mustChangePassword = ref(true)

/** ロールの説明は 5.6.1 の図のまま。表示名は `lib/roles.ts` と共有する */
const roles: { value: SystemRole; description: string }[] = [
  { value: 'operator', description: 'プロジェクトとチケットの閲覧・編集ができます' },
  { value: 'administrator', description: 'ユーザー管理・システム設定を含む全操作' },
]

const submitting = ref(false)
const error = ref<ApiError | null>(null)

/** 入力欄に紐づくエラー（`ApiDesign.md` 2.5 の details）。他の画面と同じ扱い */
const displayNameDetail = computed(() => error.value?.detailFor('display_name'))
const emailDetail = computed(() => error.value?.detailFor('email'))
const passwordDetail = computed(() => error.value?.detailFor('password'))

/** 入力欄に紐づかないエラーだけを上部に出す（同じ文言を二重に見せない） */
const generalError = computed(() => {
  const e = error.value
  if (!e) return null
  if (e.details.length > 0) return null
  return e.message
})

const canSubmit = computed(() => {
  if (submitting.value) return false
  if (displayName.value.trim() === '') return false
  if (email.value.trim() === '') return false
  if (passwordMode.value === 'manual' && password.value.length < MIN_PASSWORD) return false
  return true
})

async function submit() {
  if (!canSubmit.value) return
  submitting.value = true
  error.value = null
  try {
    const user = await usersApi.createUser({
      display_name: displayName.value.trim(),
      email: email.value.trim(),
      system_role: systemRole.value,
      password_mode: passwordMode.value,
      // `generate` のとき送られても サーバは無視する（6.2）が、入力の
      // 残りを送らずに済むならそのほうが素直である。モードを戻したときに
      // 打ち直さずに済むよう、欄の値そのものは消さない。
      password: passwordMode.value === 'manual' ? password.value : undefined,
      must_change_password: mustChangePassword.value,
    })
    emit('created', user)
  } catch (e: unknown) {
    error.value =
      e instanceof ApiError
        ? e
        : new ApiError({
            status: 0,
            code: 'internal_error',
            message: '予期しないエラーが発生しました',
          })
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal title="ユーザーを追加" @close="emit('close')">
    <form id="add-user-form" class="form" @submit.prevent="submit">
      <p v-if="generalError" class="alert" role="alert">{{ generalError }}</p>

      <label class="field">
        <span class="label">表示名 <span class="required">*</span></span>
        <input
          v-model="displayName"
          type="text"
          name="display_name"
          :maxlength="MAX_DISPLAY_NAME"
          :aria-invalid="displayNameDetail !== undefined"
          :disabled="submitting"
        />
        <span v-if="displayNameDetail" class="detail">{{ displayNameDetail.message }}</span>
      </label>

      <label class="field">
        <span class="label">メールアドレス <span class="required">*</span></span>
        <input
          v-model="email"
          type="email"
          name="email"
          :maxlength="MAX_EMAIL"
          autocapitalize="off"
          autocomplete="off"
          spellcheck="false"
          :aria-invalid="emailDetail !== undefined"
          :disabled="submitting"
        />
        <span v-if="emailDetail" class="detail">{{ emailDetail.message }}</span>
      </label>

      <fieldset class="field">
        <legend class="label">システムロール <span class="required">*</span></legend>
        <label v-for="r in roles" :key="r.value" class="choice">
          <input
            v-model="systemRole"
            type="radio"
            name="system_role"
            :value="r.value"
            :disabled="submitting"
          />
          <span class="choice-body">
            <span class="choice-label">{{ systemRoleLabel(r.value) }}</span>
            <span class="choice-description">{{ r.description }}</span>
          </span>
        </label>
      </fieldset>

      <fieldset class="field">
        <legend class="label">初期パスワード</legend>
        <label class="choice">
          <input
            v-model="passwordMode"
            type="radio"
            name="password_mode"
            value="generate"
            :disabled="submitting"
          />
          <span class="choice-body">
            <span class="choice-label">自動生成して表示する</span>
            <span class="choice-description">作成後に一度だけ表示します（再表示できません）</span>
          </span>
        </label>
        <label class="choice">
          <input
            v-model="passwordMode"
            type="radio"
            name="password_mode"
            value="manual"
            :disabled="submitting"
          />
          <span class="choice-body">
            <span class="choice-label">手動で設定する</span>
            <span class="choice-description">{{ MIN_PASSWORD }}文字以上</span>
          </span>
        </label>

        <!-- 伏せ字にしない。**本人へ伝えるために管理者が読み上げる値**であり、
             確認用の再入力欄も 5.6.1 に無い。自動生成の値を平文で見せるのと
             同じ扱いにそろえる -->
        <label v-if="passwordMode === 'manual'" class="field password-field">
          <span class="label">パスワード <span class="required">*</span></span>
          <input
            v-model="password"
            type="text"
            name="password"
            autocapitalize="off"
            autocomplete="off"
            spellcheck="false"
            :aria-invalid="passwordDetail !== undefined"
            :disabled="submitting"
          />
          <span v-if="passwordDetail" class="detail">{{ passwordDetail.message }}</span>
        </label>
      </fieldset>

      <label class="checkbox">
        <input v-model="mustChangePassword" type="checkbox" :disabled="submitting" />
        <span>初回ログイン時にパスワード変更を要求</span>
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="submitting" @click="emit('close')">
        キャンセル
      </button>
      <button type="submit" form="add-user-form" class="primary" :disabled="!canSubmit">
        {{ submitting ? '追加中…' : '追加' }}
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
input[type='email'] {
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
  font-size: 13px;
  color: var(--pb-danger-text);
}

/* ── ラジオ（説明つき）─────────────────────────────────── */
.choice {
  display: flex;
  align-items: flex-start;
  gap: var(--pb-space-2);
  padding: var(--pb-space-1) 0;
  cursor: pointer;
}

.choice input {
  margin-top: 3px;
}

.choice-body {
  display: flex;
  flex-direction: column;
}

.choice-description {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.password-field {
  margin-top: var(--pb-space-2);
  margin-left: var(--pb-space-5);
}

.checkbox {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
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
