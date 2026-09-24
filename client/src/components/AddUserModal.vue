<script setup lang="ts">
import { uiText } from '../locales/ui'
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
import { computed, onMounted, ref } from 'vue'

import Modal from './Modal.vue'
import { ApiError } from '../api/client'
import * as usersApi from '../api/users'
import type { CreatedUser } from '../api/users'
import { useRolesStore } from '../stores/roles'

const emit = defineEmits<{ close: []; created: [user: CreatedUser] }>()

/** 入力の上限（`ApiDesign.md` 6.2 の検証表）。正本はサーバ側 */
const MAX_DISPLAY_NAME = 60
const MAX_EMAIL = 254
/** `Design.md` 6.3 の最低長。サーバも同じ値で弾く */
const MIN_PASSWORD = 12

type PasswordMode = 'generate' | 'manual'

const displayName = ref('')
const email = ref('')
/**
 * 送るのは `CreateUserRequest.system_role`（`ApiDesign.md` 6.2）で、値域は
 * `operator` / `administrator` の2つ。**選択肢そのものは `GET /roles` から
 * 来る**（下の roles）が、送信の型は openapi の生成物に合わせる。
 *
 * カスタムのシステムロール（構想）を作れるようにするなら、6.2 の enum を
 * 広げる改訂が先に要る。ここで `string` へ緩めて先回りしない。
 */
type SystemRoleKey = NonNullable<usersApi.CreateUserRequest['system_role']>
const systemRole = ref<SystemRoleKey>('operator')
const passwordMode = ref<PasswordMode>('generate')
const password = ref('')
const mustChangePassword = ref(true)

/**
 * 選択肢は `GET /roles` が返すシステムロール（`ApiDesign.md` 7.1）。
 *
 * **表示名も説明も、並びも DBのシードが正本**である（`DbDesign.md` 7.3）。
 * 手順14 より前はこのファイルが独自の配列を持っていたが、`lib/roles.ts` の
 * 対応表もろとも廃止した——同じものが3か所にあると、片方だけ直る。
 *
 * このモーダルは `user.manage` を要する画面から開くので、全件を読める。
 */
const rolesStore = useRolesStore()
const roles = computed(() => rolesStore.systemRoles)

onMounted(() => {
  void rolesStore.ensureRoles('all')
})

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
            message: uiText("予期しないエラーが発生しました"),
          })
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <Modal :title="$ui('ユーザーを追加')" @close="emit('close')">
    <form id="add-user-form" class="form" @submit.prevent="submit">
      <p v-if="generalError" class="alert" role="alert">{{ generalError }}</p>

      <label class="field">
        <span class="label">{{ $ui('表示名') }} <span class="required">*</span></span>
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
        <span class="label">{{ $ui('メールアドレス') }} <span class="required">*</span></span>
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
        <legend class="label">{{ $ui('システムロール') }} <span class="required">*</span></legend>
        <label v-for="r in roles" :key="r.key" class="choice">
          <input
            v-model="systemRole"
            type="radio"
            name="system_role"
            :value="r.key"
            :disabled="submitting"
          />
          <span class="choice-body">
            <span class="choice-label">{{ r.display_name }}</span>
            <span class="choice-description">{{ r.description }}</span>
          </span>
        </label>
      </fieldset>

      <fieldset class="field">
        <legend class="label">{{ $ui('初期パスワード') }}</legend>
        <label class="choice">
          <input
            v-model="passwordMode"
            type="radio"
            name="password_mode"
            value="generate"
            :disabled="submitting"
          />
          <span class="choice-body">
            <span class="choice-label">{{ $ui('自動生成して表示する') }}</span>
            <span class="choice-description">{{ $ui('作成後に一度だけ表示します（再表示できません）') }}</span>
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
            <span class="choice-label">{{ $ui('手動で設定する') }}</span>
            <span class="choice-description">{{ MIN_PASSWORD }}{{ $ui('文字以上') }}</span>
          </span>
        </label>

        <!-- 伏せ字にしない。**本人へ伝えるために管理者が読み上げる値**であり、
             確認用の再入力欄も 5.6.1 に無い。自動生成の値を平文で見せるのと
             同じ扱いにそろえる -->
        <label v-if="passwordMode === 'manual'" class="field password-field">
          <span class="label">{{ $ui('パスワード') }} <span class="required">*</span></span>
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
        <span>{{ $ui('初回ログイン時にパスワード変更を要求') }}</span>
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="submitting" @click="emit('close')"> {{ $ui('キャンセル') }} </button>
      <button type="submit" form="add-user-form" class="primary" :disabled="!canSubmit">
        {{ submitting ? $ui("追加中…") : $ui("追加") }}
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
  margin-left: var(--pb-space-6);
}

.checkbox {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  cursor: pointer;
}
</style>
