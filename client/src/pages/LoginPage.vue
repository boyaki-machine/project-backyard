<script setup lang="ts">
import { computed, onMounted, ref, useTemplateRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { ApiError } from '../api/client'
import { REDIRECT_QUERY, safeRedirect } from '../router/guards'
import { useAuthStore } from '../stores/auth'
import { APP_VERSION } from '../version'

/**
 * ログイン画面（GuiDesign.md 5.1）。
 *
 * **メインメニューは表示しない**（レイアウト例外）。App.vue が /login を
 * AppShell の外で描画する。
 *
 * エラー文言はサーバの message をそのまま出す（ApiDesign.md 2.5）。
 * 401（資格情報の誤り）と 423（ロック）の書き分けもサーバ側が持つ。
 *
 * IdP ボタン（GET /auth/providers）は置いていない。Phase 1 の認証手段は
 * local のみで 5.1 も「非表示」としており、API も未実装のため。
 */
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()

const email = ref('')
const password = ref('')
const showPassword = ref(false)
const submitting = ref(false)
const error = ref<ApiError | null>(null)

const emailInput = useTemplateRef<HTMLInputElement>('emailInput')

onMounted(() => emailInput.value?.focus())

/** 入力欄に紐づくエラー（2.5 の details） */
const emailDetail = computed(() => error.value?.detailFor('email'))
const passwordDetail = computed(() => error.value?.detailFor('password'))

/** 入力欄に紐づかないエラーだけをカード上部に出す（同じ文言を二重に見せない） */
const generalError = computed(() => {
  const e = error.value
  if (!e) return null
  if (e.details.length > 0) return null
  return e.message
})

const version = APP_VERSION

async function submit() {
  if (submitting.value) return
  submitting.value = true
  error.value = null
  try {
    await auth.login(email.value, password.value)
    // 未認証で保護ページへ来ていた場合はそこへ戻る（5.1 のリダイレクト復帰）。
    const back = safeRedirect(route.query[REDIRECT_QUERY])
    await router.replace(back ?? '/projects')
  } catch (e: unknown) {
    error.value =
      e instanceof ApiError
        ? e
        : new ApiError({ status: 0, code: 'internal_error', message: '予期しないエラーが発生しました' })
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login">
    <form class="card" @submit.prevent="submit">
      <h1 class="brand">Project Backyard</h1>

      <p v-if="generalError" class="alert" role="alert">{{ generalError }}</p>

      <label class="field">
        <span class="label">メールアドレス</span>
        <input
          ref="emailInput"
          v-model="email"
          type="email"
          name="email"
          autocomplete="username"
          :aria-invalid="emailDetail !== undefined"
          :disabled="submitting"
        />
        <span v-if="emailDetail" class="detail">{{ emailDetail.message }}</span>
      </label>

      <label class="field">
        <span class="label">パスワード</span>
        <span class="password">
          <input
            v-model="password"
            :type="showPassword ? 'text' : 'password'"
            name="password"
            autocomplete="current-password"
            :aria-invalid="passwordDetail !== undefined"
            :disabled="submitting"
          />
          <button
            type="button"
            class="reveal"
            :aria-label="showPassword ? 'パスワードを隠す' : 'パスワードを表示する'"
            :aria-pressed="showPassword"
            @click="showPassword = !showPassword"
          >
            👁
          </button>
        </span>
        <span v-if="passwordDetail" class="detail">{{ passwordDetail.message }}</span>
      </label>

      <button type="submit" class="submit" :disabled="submitting">
        {{ submitting ? 'ログイン中…' : 'ログイン' }}
      </button>
    </form>

    <p class="version">PB v{{ version }}</p>
  </div>
</template>

<style scoped>
.login {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--pb-space-6);
  height: 100%;
  padding: var(--pb-space-6);
}

.card {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
  width: 100%;
  max-width: 360px;
  padding: var(--pb-space-6);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  box-shadow: var(--pb-shadow-1);
}

.brand {
  margin-bottom: var(--pb-space-2);
  font-size: 18px;
  font-weight: 600;
  text-align: center;
}

/* 有彩色は意味に予約されている。失敗は danger（GuiDesign.md 8.6） */
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
}

.label {
  font-size: 13px;
  color: var(--pb-text-muted);
}

input {
  width: 100%;
  height: 36px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

input[aria-invalid='true'] {
  border-color: var(--pb-danger-border);
}

.password {
  position: relative;
  display: block;
}

.password input {
  padding-right: 36px;
}

.reveal {
  position: absolute;
  top: 0;
  right: 0;
  width: 36px;
  height: 36px;
  border: 0;
  background: none;
  cursor: pointer;
}

.detail {
  font-size: 13px;
  color: var(--pb-danger-text);
}

.submit {
  height: 36px;
  margin-top: var(--pb-space-2);
  border: 1px solid var(--pb-accent);
  border-radius: var(--pb-radius);
  background: var(--pb-accent);
  color: var(--pb-on-accent);
  font-weight: 600;
  cursor: pointer;
}

.submit:hover:not(:disabled) {
  border-color: var(--pb-accent-hover);
  background: var(--pb-accent-hover);
}

.submit:disabled {
  cursor: default;
  opacity: 0.7;
}

.version {
  font-size: 13px;
  color: var(--pb-text-muted);
}
</style>
