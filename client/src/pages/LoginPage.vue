<script setup lang="ts">
import { uiText } from '../locales/ui'
import { computed, nextTick, onMounted, ref, useTemplateRef } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import type { MfaChallenge } from '../api/auth'
import { ApiError } from '../api/client'
import * as passkeysApi from '../api/passkeys'
import {
  ipAddressReason,
  getPasskey,
  isPasskeyCancelled,
  openedByIPAddress,
  passkeySupported,
} from '../lib/passkey'
import type { PasskeyOptionsEnvelope } from '../lib/passkey'
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
 *
 * **パスキーのボタンはパスワードの欄の下に置く**（5.1.2）。
 * パスキーは第2要素ではないので、5.1.1 のコード入力には出さない。
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
const codeInput = useTemplateRef<HTMLInputElement>('codeInput')

onMounted(() => emailInput.value?.focus())

/**
 * 第2要素の挑戦（5.1.1）。
 *
 * **同じカードを差し替える。** `/login/mfa` のようなルートを作らないのは、
 * 挑戦トークンがメモリにあるだけで**再読み込みで失われる**ためである——
 * URL を持たせると、開き直せるように見えて実際には最初からやり直しになる。
 *
 * **保存もしない**（`localStorage` に置かない）。パスワードを通した直後の
 * 状態であり、次のタブや次回の起動へ持ち越す意味が無い。
 */
const challenge = ref<MfaChallenge | null>(null)
const code = ref('')
/** リカバリコードで入るモードか（挑戦が `recovery_code` を許すときだけ出す） */
const useRecovery = ref(false)

const canUseRecovery = computed(
  () => challenge.value?.methods.includes('recovery_code') === true,
)

const codeDetail = computed(() => error.value?.detailFor('code'))

/** 第2要素を確認する（`ApiDesign.md` 3.4） */
async function submitCode() {
  const c = challenge.value
  if (!c || submitting.value || code.value.trim() === '') return
  submitting.value = true
  error.value = null
  try {
    const value = code.value.trim()
    await auth.completeMfa(
      c.mfa_token,
      useRecovery.value ? { recoveryCode: value } : { code: value },
    )
    await goAfterLogin()
  } catch (e: unknown) {
    error.value = asApiError(e)
    code.value = ''
  } finally {
    submitting.value = false
  }
}

/**
 * メールアドレスの入力へ戻る。
 *
 * **必ず置く。** 認証アプリを開いたら消えていた、という場面で
 * **行き止まりにしないため**である（5.1.1）。挑戦は捨てる。
 */
function backToPassword() {
  challenge.value = null
  code.value = ''
  useRecovery.value = false
  error.value = null
  password.value = ''
  void nextTick(() => emailInput.value?.focus())
}

async function goAfterLogin() {
  // 未認証で保護ページへ来ていた場合はそこへ戻る（5.1 のリダイレクト復帰）。
  const back = safeRedirect(route.query[REDIRECT_QUERY])
  await router.replace(back ?? '/projects')
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({
    status: 0,
    code: 'internal_error',
    message: uiText("予期しないエラーが発生しました"),
  })
}

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

// ── パスキーでログイン（5.1.2）──────────────────────

/**
 * パスキーのボタンを出すか。**WebAuthn の無いブラウザでは出さない**（5.1.2）。
 * パスワードで入れるので行き止まりにはならない。
 */
const showPasskey = passkeySupported()

/** IP アドレスで開いているか。**ボタンは押せなくし、理由を添える**（`Design.md` 6.8.3） */
const passkeyBlocked = openedByIPAddress()

/** パスキーの確認中か。ボタンの文言だけを切り替える（送信の抑止は `submitting`） */
const passkeyBusy = ref(false)

/**
 * パスキーでログインする（`ApiDesign.md` 3.5 → `navigator.credentials.get()` → 3.6）。
 *
 * **メールアドレスの入力を見ない。** 端末がパスキーを選ばせる（`Design.md` 6.8.1）。
 * 確認中は `submitting` を立て、**パスワード側の送信も押せなくする**——
 * 2つのログインを並行させない。
 */
async function loginWithPasskey() {
  if (submitting.value || passkeyBlocked) return
  submitting.value = true
  passkeyBusy.value = true
  error.value = null
  try {
    const options = await passkeysApi.startPasskeyLogin()
    const credential = await getPasskey(options.options as PasskeyOptionsEnvelope)
    await auth.loginWithPasskey(credential)
    await goAfterLogin()
  } catch (e: unknown) {
    // **端末のダイアログを閉じたときは何も出さない**（5.1.2）
    if (!isPasskeyCancelled(e)) error.value = asApiError(e)
  } finally {
    submitting.value = false
    passkeyBusy.value = false
  }
}

async function submit() {
  if (submitting.value) return
  submitting.value = true
  error.value = null
  try {
    const next = await auth.login(email.value, password.value)
    if (next) {
      // **第2要素が要る**（`ApiDesign.md` 3.1）。まだログインしていないので
      // 遷移せず、同じカードをコード入力へ差し替える。
      challenge.value = next
      await nextTick()
      codeInput.value?.focus()
      return
    }
    await goAfterLogin()
  } catch (e: unknown) {
    error.value = asApiError(e)
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="login">
    <!-- ── 第2要素の入力（5.1.1）──────────────────────────
         **同じカードを差し替える。** ルートを増やさない -->
    <form v-if="challenge" class="card" @submit.prevent="submitCode">
      <h1 class="brand">Project Backyard</h1>

      <p v-if="generalError" class="alert" role="alert">{{ generalError }}</p>

      <label class="field">
        <span class="label">{{ useRecovery ? $ui("リカバリコード") : $ui("確認コード") }}</span>
        <input
          ref="codeInput"
          v-model="code"
          type="text"
          name="code"
          :inputmode="useRecovery ? 'text' : 'numeric'"
          :autocomplete="useRecovery ? 'off' : 'one-time-code'"
          :maxlength="useRecovery ? 16 : 8"
          :aria-invalid="codeDetail !== undefined"
          :disabled="submitting"
        />
        <span v-if="codeDetail" class="detail">{{ codeDetail.message }}</span>
        <span v-else class="hint">
          {{
            useRecovery
              ? $ui("保存しておいたリカバリコードを1本入力します。")
              : $ui("ⓘ 認証アプリに表示されている6桁を入力します。")
          }}
        </span>
      </label>

      <button type="submit" class="submit" :disabled="submitting || code.trim() === ''">
        {{ submitting ? $ui("確認中…") : $ui("確認") }}
      </button>

      <!-- **出せない選択肢を出さない**（`methods` に無ければ導線も出さない） -->
      <button
        v-if="canUseRecovery"
        type="button"
        class="link"
        @click="useRecovery = !useRecovery"
      >
        {{ useRecovery ? $ui("認証アプリのコードを使う") : $ui("リカバリコードを使う") }}
      </button>

      <!-- **行き止まりにしない**（5.1.1） -->
      <button type="button" class="link" @click="backToPassword"> {{ $ui('← メールアドレスから入力') }} </button>
    </form>

    <form v-else class="card" @submit.prevent="submit">
      <h1 class="brand">Project Backyard</h1>

      <p v-if="generalError" class="alert" role="alert">{{ generalError }}</p>

      <label class="field">
        <span class="label">{{ $ui('メールアドレス') }}</span>
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
        <span class="label">{{ $ui('パスワード') }}</span>
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
            :aria-label="showPassword ? $ui('パスワードを隠す') : $ui('パスワードを表示する')"
            :aria-pressed="showPassword"
            @click="showPassword = !showPassword"
          >
            👁
          </button>
        </span>
        <span v-if="passwordDetail" class="detail">{{ passwordDetail.message }}</span>
      </label>

      <button type="submit" class="submit" :disabled="submitting">
        {{ submitting && !passkeyBusy ? $ui("ログイン中…") : $ui("ログイン") }}
      </button>

      <!-- ── パスキー（5.1.2）────────────────────────
           **パスワードの欄より上に置かない。** WebAuthn の無いブラウザでは出さない -->
      <template v-if="showPasskey">
        <div class="or" aria-hidden="true">{{ $ui('または') }}</div>
        <button
          type="button"
          class="passkey"
          :disabled="submitting || passkeyBlocked"
          @click="loginWithPasskey"
        >
          {{ passkeyBusy ? $ui("確認中…") : $ui("🔑 パスキーでログイン") }}
        </button>
        <span v-if="passkeyBlocked" class="hint">{{ ipAddressReason() }}</span>
      </template>
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

/* カード内の補助文言と、段を戻る導線（5.1.1） */
.hint {
  color: var(--pb-text-muted);
  font-size: 12px;
}

.link {
  padding: 0;
  border: 0;
  background: none;
  color: var(--pb-text-muted);
  font-size: 13px;
  text-align: center;
  cursor: pointer;
}

.link:hover {
  color: var(--pb-text);
  text-decoration: underline;
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

/* 「または」の区切り（5.1 の図） */
.or {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 12px;
}

.or::before,
.or::after {
  content: '';
  flex: 1 1 auto;
  border-top: 1px solid var(--pb-line);
}

/* パスワードの送信より一段控えめにする。大半の利用者はパスワードで入る（5.1.2） */
.passkey {
  height: 36px;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: var(--pb-text);
  font-weight: 600;
  cursor: pointer;
}

.passkey:hover:not(:disabled) {
  background: var(--pb-bg);
}

.passkey:disabled {
  cursor: default;
  opacity: 0.7;
}

.version {
  font-size: 13px;
  color: var(--pb-text-muted);
}
</style>
