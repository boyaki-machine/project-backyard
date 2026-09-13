<script setup lang="ts">
/**
 * 自分の設定 `/me`（`GuiDesign.md` 5.8）。
 *
 * 3つのセクションを持つ。
 *
 *   基本情報      ログインID・表示名・メールアドレス・言語・タイムゾーン
 *   デザイン      テーマ・色相（8.11）
 *   セキュリティ  パスワード・多要素認証（TOTP。pb-103）・パスキー（pb-104）
 *
 * **結果はセクションごとに出す**（6.4）。トーストは使わない。デザインだけは
 * 見た目が即座に変わることが結果の表示を兼ねるため、成功時の文言を出さない。
 *
 * **「ログインID」と「メールアドレス」は Phase 1 ではどちらも `email` を指す**
 * （`ApiDesign.md` 4.2）。ログインIDは読み取り専用で、メールアドレス欄の変更に
 * 追随する。2行に分けてあるのは、将来ログインのIDと連絡先を別々に登録できる
 * ようにするためであり、そのとき画面の形を変えずに済む（利用者の判断、2026-08-22）。
 */
import { computed, defineAsyncComponent, onMounted, ref, watch } from 'vue'

import { ApiError } from '../api/client'
import * as meApi from '../api/me'
import type { UpdateMeRequest } from '../api/me'
import * as mfaApi from '../api/mfa'
import type { ConfirmedTotp, MfaOverview, TotpCredential } from '../api/mfa'
import * as passkeysApi from '../api/passkeys'
import type { Passkey } from '../api/passkeys'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import MeTabs from '../components/MeTabs.vue'
import PageHeader from '../components/PageHeader.vue'
import PasskeyRegisterModal from '../components/PasskeyRegisterModal.vue'
import RecoveryCodesDialog from '../components/RecoveryCodesDialog.vue'
import { formatDateTime } from '../lib/datetime'
import { IP_ADDRESS_REASON, openedByIPAddress, passkeySupported } from '../lib/passkey'
import { useAuthStore } from '../stores/auth'
import type { Hue, ThemePreference } from '../stores/ui'
import { useUiStore } from '../stores/ui'

const auth = useAuthStore()
const ui = useUiStore()

/**
 * 登録ダイアログは遅延読み込みにする（`Development.md` 7.1）。
 *
 * **`qrcode` が初期バンドルを 38KB 増やしていた**（実測。575.50 → 613.65 kB）。
 * QR を描くのは「認証アプリを追加」を押したときだけなので、その瞬間まで
 * 取り込まない。**この画面が唯一の利用者**なので、静的な import を残さなければ
 * チャンクは分かれる。
 */
const TotpRegisterModal = defineAsyncComponent(
  () => import('../components/TotpRegisterModal.vue'),
)

/** 表示名の上限（`ApiDesign.md` 6.2 と同じ。DBの CHECK 制約に合わせる） */
const MAX_DISPLAY_NAME = 60
/** メールアドレスの上限（RFC 5321 4.5.3.1.3。サーバ側 emailMaxLen と同じ） */
const MAX_EMAIL = 254

// ── 基本情報 ────────────────────────────────────────────────

const displayName = ref('')
const email = ref('')
const locale = ref('ja')
const timezone = ref('Asia/Tokyo')

const savingProfile = ref(false)
const profileSaved = ref(false)
const profileError = ref<ApiError | null>(null)

/**
 * サーバの値をフォームへ写す。
 *
 * **保存の成否にかかわらず、ストアが変わったら必ず引き直す。** 保存に
 * 成功した直後は `PATCH /me` の応答が `setSession` を通ってここへ戻る
 * （`GuiDesign.md` 6.4「サーバ応答後に反映する」）。
 */
function loadFromStore() {
  const actor = auth.actor
  if (!actor) return
  displayName.value = actor.display_name
  email.value = actor.email ?? ''
  locale.value = actor.locale ?? 'ja'
  timezone.value = actor.timezone ?? 'Asia/Tokyo'
}
watch(() => auth.actor, loadFromStore, { immediate: true })

/** ログインID。Phase 1 はメールアドレスと同じ値（`ApiDesign.md` 4.2） */
const loginId = computed(() => auth.actor?.email ?? '')

/** 変更された項目だけを送る（部分更新。`ApiDesign.md` 4.2） */
const profileChanges = computed<UpdateMeRequest>(() => {
  const actor = auth.actor
  const body: UpdateMeRequest = {}
  if (!actor) return body
  if (displayName.value !== actor.display_name) body.display_name = displayName.value
  if (email.value !== (actor.email ?? '')) body.email = email.value
  if (locale.value !== (actor.locale ?? '')) body.locale = locale.value as 'ja'
  if (timezone.value !== (actor.timezone ?? '')) body.timezone = timezone.value
  return body
})

const profileDirty = computed(() => Object.keys(profileChanges.value).length > 0)

/** 2.5 の `details` を入力欄に紐づける */
function profileDetail(field: string) {
  return profileError.value?.detailFor(field)
}

async function saveProfile() {
  if (!profileDirty.value || savingProfile.value) return
  savingProfile.value = true
  profileError.value = null
  profileSaved.value = false
  try {
    // 応答は `GET /me` と同一構造なので、そのままストアへ流し込める。
    auth.setSession(await meApi.updateMe(profileChanges.value))
    profileSaved.value = true
  } catch (e: unknown) {
    profileError.value = asApiError(e)
  } finally {
    savingProfile.value = false
  }
}

/**
 * 「✓ 保存しました」を出すか。
 *
 * **入力を監視して消す作りにしない。** 保存の成功時にはサーバ応答が
 * `loadFromStore` を通ってフォームへ戻ってくるため、監視が発火して
 * 出したばかりの結果を消すことがある（パスワード側で実際に起きた）。
 * 「フォームがサーバの値と一致している間だけ出す」と書けば、
 * 打ち始めた瞬間に消えるという意図が発火の順序に依らず満たせる。
 */
const showProfileSaved = computed(() => profileSaved.value && !profileDirty.value)

// ── デザイン（8.11）─────────────────────────────────────────

const designError = ref<ApiError | null>(null)

/**
 * テーマ・色相を切り替える。
 *
 * **見た目を先に変え、その後で保存する**（8.11「再読み込みを要しない」）。
 * 保存に失敗しても見た目は戻さない——押した直後に元へ戻ると、操作が
 * 効かなかったのか壊れたのかが分からない。失敗はこのセクションに出す。
 */
async function chooseTheme(next: ThemePreference) {
  ui.setTheme(next)
  await persistDesign({ theme: next })
}

async function chooseHue(next: Hue) {
  ui.setHue(next)
  await persistDesign({ hue: next })
}

async function persistDesign(body: UpdateMeRequest) {
  designError.value = null
  try {
    auth.setSession(await meApi.updateMe(body))
  } catch (e: unknown) {
    designError.value = asApiError(e)
  }
}

// ── セキュリティ（パスワード。`ApiDesign.md` 4.3）──────────────

/** 新しいパスワードの最小長（`Design.md` 6.3）。サーバ側と同じ値を持つ */
const MIN_PASSWORD = 12

const currentPassword = ref('')
const newPassword = ref('')
const changingPassword = ref(false)
const passwordChanged = ref(false)
const passwordError = ref<ApiError | null>(null)

const canChangePassword = computed(
  () => currentPassword.value !== '' && newPassword.value !== '' && !changingPassword.value,
)

function passwordDetail(field: string) {
  return passwordError.value?.detailFor(field)
}

async function changePassword() {
  if (!canChangePassword.value) return
  changingPassword.value = true
  passwordError.value = null
  passwordChanged.value = false
  try {
    await meApi.changePassword({
      current_password: currentPassword.value,
      new_password: newPassword.value,
    })
    // 204 なので本体が返らない。**`must_change_password` の解除を画面へ
    // 反映するには読み直しが要る**（下りないとガードが `/me` に留め続ける）。
    await auth.refresh()
    currentPassword.value = ''
    newPassword.value = ''
    passwordChanged.value = true
  } catch (e: unknown) {
    passwordError.value = asApiError(e)
  } finally {
    changingPassword.value = false
  }
}

/**
 * 「✓ パスワードを変更しました」を出すか。
 *
 * 成功時に両欄を空へ戻すので、**空である間だけ**出す。入力を監視して
 * 消す作りにすると、その「空へ戻す」自体が発火して結果が即座に消える。
 */
const showPasswordChanged = computed(
  () => passwordChanged.value && currentPassword.value === '' && newPassword.value === '',
)

// ── 多要素認証（MFA。`ApiDesign.md` 4.6）─────────────────────

/**
 * 登録済みの第2要素。
 *
 * **押した操作ごとにその場で反映する**（`GuiDesign.md` 5.8「保存の単位」）。
 * `[ 保存 ]` を通さないのは、登録がダイアログの中で完結し、削除は確認ダイアログが
 * 承認を兼ねるためである。
 */
const mfa = ref<MfaOverview | null>(null)
const mfaError = ref<ApiError | null>(null)
const mfaNotice = ref('')

/** 登録できる件数の上限（サーバ側 maxMFACredentialsPerUser と同じ値） */
const MAX_TOTP = 5

const registerOpen = ref(false)
/** 1回だけ表示するリカバリコード。null なら出さない */
const shownCodes = ref<string[] | null>(null)
/** 削除の確認中の認証器 */
const deleting = ref<TotpCredential | null>(null)
const deleteBusy = ref(false)
const regenerating = ref(false)

async function loadMfa() {
  mfaError.value = null
  try {
    mfa.value = await mfaApi.getMfa()
  } catch (e: unknown) {
    mfaError.value = asApiError(e)
  }
}
onMounted(loadMfa)

const totpItems = computed(() => mfa.value?.totp ?? [])
const atTotpLimit = computed(() => totpItems.value.length >= MAX_TOTP)
/** リカバリコードの行を出すか。**認証器が0件なら作れない**（4.6.5 が 409） */
const showRecoveryRow = computed(() => totpItems.value.length > 0)

/**
 * 登録が確定した。
 *
 * **初回だけリカバリコードが返る**（`ApiDesign.md` 4.6.3）。返ったときは
 * 1回表示のダイアログへ渡し、登録ダイアログは閉じる。
 */
async function onRegistered(result: ConfirmedTotp) {
  registerOpen.value = false
  mfaNotice.value = `✓ 「${result.credential.name}」を登録しました`
  if (result.recovery_codes && result.recovery_codes.length > 0) {
    shownCodes.value = result.recovery_codes
  }
  await loadMfa()
}

/**
 * 削除の確認本文。**最後の1件かどうかで変える**（`GuiDesign.md` 5.8「削除」）。
 *
 * 「MFA が無効になります」だけでは、利用者にとって何が緩むのかが読めない。
 */
const deleteMessage = computed(() => {
  if (!deleting.value) return ''
  const name = deleting.value.name
  if (totpItems.value.length > 1) {
    return `「${name}」で作ったコードは使えなくなります。\n他の認証アプリはそのまま使えます。`
  }
  return (
    `「${name}」を削除すると多要素認証が無効になり、` +
    '次のログインからパスワードだけで入れるようになります。\n' +
    'リカバリコードもあわせて削除されます。'
  )
})

async function confirmDelete() {
  const target = deleting.value
  if (!target || deleteBusy.value) return
  deleteBusy.value = true
  mfaError.value = null
  try {
    await mfaApi.deleteTotp(target.id)
    mfaNotice.value = `✓ 「${target.name}」を削除しました`
    deleting.value = null
    await loadMfa()
  } catch (e: unknown) {
    mfaError.value = asApiError(e)
  } finally {
    deleteBusy.value = false
  }
}

/** リカバリコードを作り直す。**既存は未使用のものも含めて全部無効になる** */
async function regenerateCodes() {
  if (regenerating.value) return
  regenerating.value = true
  mfaError.value = null
  mfaNotice.value = ''
  try {
    const { recovery_codes } = await mfaApi.regenerateRecoveryCodes()
    shownCodes.value = recovery_codes
    await loadMfa()
  } catch (e: unknown) {
    mfaError.value = asApiError(e)
  } finally {
    regenerating.value = false
  }
}

// ── パスキー（`ApiDesign.md` 4.7。pb-104）─────────────────────

/**
 * 登録済みのパスキー。
 *
 * **多要素認証の下に、別の項目として置く**（`GuiDesign.md` 5.8）。パスキーは
 * 第2要素ではなく、パスワードの代わりである——MFA の表に混ぜると、登録した
 * 利用者が「ログインのときにパスキーも求められる」と読む。
 */
const passkeys = ref<Passkey[]>([])
const passkeyError = ref<ApiError | null>(null)
const passkeyNotice = ref('')
const passkeyRegisterOpen = ref(false)
/** 削除の確認中のパスキー */
const deletingPasskey = ref<Passkey | null>(null)
const passkeyDeleteBusy = ref(false)

/** 登録できる件数の上限（サーバ側 passkey.MaxPerUser と同じ値） */
const MAX_PASSKEYS = 5

/** WebAuthn の無いブラウザか。追加は押せないが、**一覧と削除は出す**（5.8） */
const passkeyUnsupported = !passkeySupported()
/** IP アドレスで開いているか（`Design.md` 6.8.3） */
const passkeyOnIPAddress = openedByIPAddress()
/** いま開いているホスト名。`rp_id` と比べて「このアドレスでは使えません」を出す */
const currentHost = window.location.hostname

/**
 * 追加を押せない理由。押せるなら空文字。
 *
 * **在るはずの操作が黙って消えるより、押せない理由が読めるほうがよい**（5.8.1 の作法）。
 */
const passkeyAddBlockedReason = computed(() => {
  if (passkeyUnsupported) return 'このブラウザはパスキーに対応していません'
  if (passkeyOnIPAddress) return IP_ADDRESS_REASON
  if (passkeys.value.length >= MAX_PASSKEYS) {
    return `登録できるのは${MAX_PASSKEYS}件までです。追加するには、いずれかを削除してください。`
  }
  return ''
})

async function loadPasskeys() {
  passkeyError.value = null
  try {
    passkeys.value = (await passkeysApi.listPasskeys()).items
  } catch (e: unknown) {
    passkeyError.value = asApiError(e)
  }
}
onMounted(loadPasskeys)

async function onPasskeyRegistered(created: Passkey) {
  passkeyRegisterOpen.value = false
  passkeyNotice.value = `✓ 「${created.name}」を登録しました`
  await loadPasskeys()
}

/**
 * 削除の確認本文（`GuiDesign.md` 5.8「パスキーの削除」）。
 *
 * **最後の1件かどうかで変えない。** 全部消してもパスワードで入れる。
 * **端末の中のパスキーは消えない**ことを必ず書く（`ApiDesign.md` 4.7.4）。
 */
const passkeyDeleteMessage = computed(() => {
  if (!deletingPasskey.value) return ''
  return (
    `「${deletingPasskey.value.name}」では PB にログインできなくなります。\n` +
    '端末の中のパスキーは消えません。不要なら端末の設定から削除してください。'
  )
})

async function confirmPasskeyDelete() {
  const target = deletingPasskey.value
  if (!target || passkeyDeleteBusy.value) return
  passkeyDeleteBusy.value = true
  passkeyError.value = null
  try {
    await passkeysApi.deletePasskey(target.id)
    passkeyNotice.value = `✓ 「${target.name}」を削除しました`
    deletingPasskey.value = null
    await loadPasskeys()
  } catch (e: unknown) {
    passkeyError.value = asApiError(e)
  } finally {
    passkeyDeleteBusy.value = false
  }
}

// ── 小さな助け ──────────────────────────────────────────────

/**
 * 選べるタイムゾーン。
 *
 * **一覧を自分で持たない。** IANA のデータベースは更新されるもので、
 * 画面に写しを置くと古くなる（サーバ側も `time.LoadLocation` に委ねている）。
 * `Intl.supportedValuesOf` が無い環境では、いま設定されている値だけを出す
 * ——選び直せないが、表示が空になって「消えた」ように見えるよりはよい。
 */
const timezones = computed(() => {
  const supported = Intl.supportedValuesOf?.('timeZone') ?? []
  const list = supported.length > 0 ? [...supported] : ['UTC']
  if (timezone.value !== '' && !list.includes(timezone.value)) list.unshift(timezone.value)
  return list
})

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: '通信に失敗しました' })
}
</script>

<template>
  <div class="page">
    <PageHeader title="自分の設定" />

    <div class="page-body">
      <MeTabs current="general" />

      <div class="blocks">
        <!-- ── 基本情報 ──────────────────────────────────── -->
        <form class="block" @submit.prevent="saveProfile">
          <h2 class="block-title">基本情報</h2>

          <div class="field">
            <span class="label">ログインID</span>
            <p class="static-value">{{ loginId }}</p>
            <p class="hint">現在はメールアドレスと同じ値です。</p>
          </div>

          <label class="field">
            <span class="label">表示名 <span class="required">*</span></span>
            <input
              v-model="displayName"
              type="text"
              name="display_name"
              :maxlength="MAX_DISPLAY_NAME"
              :aria-invalid="profileDetail('display_name') !== undefined"
              :disabled="savingProfile"
            />
            <span v-if="profileDetail('display_name')" class="detail">
              {{ profileDetail('display_name')?.message }}
            </span>
          </label>

          <label class="field">
            <span class="label">メールアドレス <span class="required">*</span></span>
            <input
              v-model="email"
              type="text"
              name="email"
              :maxlength="MAX_EMAIL"
              :aria-invalid="profileDetail('email') !== undefined"
              :disabled="savingProfile"
            />
            <span v-if="profileDetail('email')" class="detail">
              {{ profileDetail('email')?.message }}
            </span>
            <span v-else class="hint">変更するとログインIDも同じ値になります。</span>
          </label>

          <div class="pair">
            <label class="field">
              <span class="label">言語</span>
              <select v-model="locale" name="locale" :disabled="savingProfile">
                <option value="ja">日本語</option>
              </select>
              <span v-if="profileDetail('locale')" class="detail">
                {{ profileDetail('locale')?.message }}
              </span>
            </label>

            <label class="field">
              <span class="label">タイムゾーン</span>
              <select v-model="timezone" name="timezone" :disabled="savingProfile">
                <option v-for="tz in timezones" :key="tz" :value="tz">{{ tz }}</option>
              </select>
              <span v-if="profileDetail('timezone')" class="detail">
                {{ profileDetail('timezone')?.message }}
              </span>
            </label>
          </div>

          <!-- 結果は操作した場所に出す（6.4）。トーストは使わない -->
          <div class="actions">
            <p v-if="profileError" class="alert" role="alert">{{ profileError.message }}</p>
            <p v-else-if="showProfileSaved" class="ok" role="status">✓ 保存しました</p>
            <span v-else class="spacer"></span>

            <button type="submit" class="primary" :disabled="!profileDirty || savingProfile">
              {{ savingProfile ? '保存中…' : '保存' }}
            </button>
          </div>
        </form>

        <!-- ── デザイン（8.11）───────────────────────────── -->
        <section class="block">
          <h2 class="block-title">デザイン</h2>

          <fieldset class="field choices">
            <legend class="label">テーマ</legend>
            <div class="choices-row">
              <label v-for="t in (['light', 'dark', 'system'] as ThemePreference[])" :key="t">
                <input
                  type="radio"
                  name="theme"
                  :value="t"
                  :checked="ui.theme === t"
                  @change="chooseTheme(t)"
                />
                <span>{{ { light: 'ライト', dark: 'ダーク', system: 'システムに従う' }[t] }}</span>
              </label>
            </div>
          </fieldset>

          <fieldset class="field choices">
            <legend class="label">色相</legend>
            <div class="choices-row">
              <label v-for="h in (['blue', 'green'] as Hue[])" :key="h">
                <input
                  type="radio"
                  name="hue"
                  :value="h"
                  :checked="ui.hue === h"
                  @change="chooseHue(h)"
                />
                <span>{{ { blue: 'ブルー', green: 'グリーン' }[h] }}</span>
              </label>
            </div>
          </fieldset>

          <!-- 成功時は何も出さない。見た目が変わることが結果の表示である -->
          <p v-if="designError" class="alert" role="alert">{{ designError.message }}</p>
        </section>

        <!-- ── セキュリティ ──────────────────────────────── -->
        <form class="block" @submit.prevent="changePassword">
          <h2 class="block-title">セキュリティ</h2>

          <!-- 要パスワード変更（`ApiDesign.md` 3.1）。変更するまで他の画面へ
               行けないので、なぜここに留まるのかをその場に書く -->
          <p v-if="auth.mustChangePassword" class="warn">
            ⚠ パスワードの変更が必要です。変更するまで他の画面には移動できません。
          </p>

          <h3 class="sub-title">パスワード</h3>

          <div class="pair">
            <label class="field">
              <span class="label">現在のパスワード</span>
              <input
                v-model="currentPassword"
                type="password"
                name="current_password"
                autocomplete="current-password"
                :aria-invalid="passwordDetail('current_password') !== undefined"
                :disabled="changingPassword"
              />
              <span v-if="passwordDetail('current_password')" class="detail">
                {{ passwordDetail('current_password')?.message }}
              </span>
            </label>

            <label class="field">
              <span class="label">新しいパスワード</span>
              <input
                v-model="newPassword"
                type="password"
                name="new_password"
                autocomplete="new-password"
                :aria-invalid="passwordDetail('new_password') !== undefined"
                :disabled="changingPassword"
              />
              <span v-if="passwordDetail('new_password')" class="detail">
                {{ passwordDetail('new_password')?.message }}
              </span>
              <span v-else class="hint">{{ MIN_PASSWORD }}文字以上。</span>
            </label>
          </div>

          <p class="hint">ⓘ 変更すると、他のセッションはすべてログアウトされます。</p>

          <div class="actions">
            <p v-if="passwordError" class="alert" role="alert">{{ passwordError.message }}</p>
            <p v-else-if="showPasswordChanged" class="ok" role="status">
              ✓ パスワードを変更しました
            </p>
            <span v-else class="spacer"></span>

            <button type="submit" class="primary" :disabled="!canChangePassword">
              {{ changingPassword ? '変更中…' : '変更' }}
            </button>
          </div>

          <!-- ── 多要素認証（pb-103。`GuiDesign.md` 5.8）──────────
               **パスワードと同じセクションに置く。** どれも「どうやって自分で
               あることを示すか」であり、利用者がログインの固さを見るときに
               1か所で足りるほうがよい -->
          <hr class="divider" />

          <div class="sub-block">
            <div class="sub-head">
              <h3 class="sub-title">多要素認証（MFA）</h3>
              <button
                type="button"
                class="secondary"
                :disabled="atTotpLimit"
                @click="registerOpen = true"
              >
                + 認証アプリを追加
              </button>
            </div>

            <p class="hint">
              ⓘ ログインのときに、パスワードに加えて認証アプリの6桁のコードを求めます。
            </p>
            <!-- **在るはずの操作が黙って消えるより、押せない理由が読めるほうがよい**
                 （5.8.1 と同じ作法） -->
            <p v-if="atTotpLimit" class="hint">
              登録できるのは{{ MAX_TOTP }}件までです。追加するには、いずれかを削除してください。
            </p>

            <table v-if="totpItems.length > 0" class="mfa-table">
              <thead>
                <tr>
                  <th scope="col">名前</th>
                  <th scope="col">登録</th>
                  <th scope="col">最終利用</th>
                  <th scope="col"><span class="sr-only">操作</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="c in totpItems" :key="c.id">
                  <td>{{ c.name }}</td>
                  <td>{{ formatDateTime(c.created_at) }}</td>
                  <td>{{ c.last_used_at ? formatDateTime(c.last_used_at) : '—' }}</td>
                  <td class="row-actions">
                    <button type="button" class="secondary" @click="deleting = c">削除</button>
                  </td>
                </tr>
              </tbody>
            </table>
            <p v-else class="empty">登録されていません。</p>

            <!-- リカバリコード。**認証器が0件のときは行そのものを出さない**
                 （作れないため。`ApiDesign.md` 4.6.5 が 409） -->
            <div v-if="showRecoveryRow" class="recovery">
              <div class="recovery-head">
                <span class="label">リカバリコード</span>
                <!-- **残り0本と「1本も作っていない」を区別して出す**（4.6.1） -->
                <span v-if="mfa?.recovery_codes" class="recovery-count">
                  残り {{ mfa.recovery_codes.remaining }} 本
                </span>
                <span v-else class="recovery-count warn-text">⚠ リカバリコードがありません</span>
                <button
                  type="button"
                  class="secondary"
                  :disabled="regenerating"
                  @click="regenerateCodes"
                >
                  {{ mfa?.recovery_codes ? '作り直す' : '作成' }}
                </button>
              </div>
              <p class="hint">
                ⚠ 認証アプリを使えなくなったときは、このコードでログインします。
                作り直すと、いまのコードはすべて使えなくなります。
              </p>
            </div>

            <!-- 結果は MFA の表の直上ではなくこの領域に出す（6.4）。
                 パスワードの結果欄と混ざらないようにする -->
            <p v-if="mfaError" class="alert" role="alert">{{ mfaError.message }}</p>
            <p v-else-if="mfaNotice" class="ok" role="status">{{ mfaNotice }}</p>
          </div>

          <!-- ── パスキー（pb-104。`GuiDesign.md` 5.8）───────────
               **項目そのものは出す**（利用者の判断、2026-09-13）。MFA と
               パスキーは別のものであり、MFA を登録した利用者が「パスキーも
               設定した」と誤解するのを防ぐのは、項目が並んでいることである -->
          <hr class="divider" />

          <div class="sub-block">
            <div class="sub-head">
              <h3 class="sub-title">パスキー</h3>
              <button
                type="button"
                class="secondary"
                :disabled="passkeyAddBlockedReason !== ''"
                @click="passkeyRegisterOpen = true"
              >
                + パスキーを追加
              </button>
            </div>

            <p class="hint">
              ⓘ パスワードの代わりに、この端末の生体認証や PIN でログインできます。
            </p>
            <p v-if="passkeyAddBlockedReason" class="hint">{{ passkeyAddBlockedReason }}</p>

            <table v-if="passkeys.length > 0" class="mfa-table">
              <thead>
                <tr>
                  <th scope="col">名前</th>
                  <th scope="col">登録</th>
                  <th scope="col">最終利用</th>
                  <th scope="col">同期</th>
                  <th scope="col"><span class="sr-only">操作</span></th>
                </tr>
              </thead>
              <tbody>
                <template v-for="pk in passkeys" :key="pk.id">
                  <tr>
                    <td>{{ pk.name }}</td>
                    <td>{{ formatDateTime(pk.created_at) }}</td>
                    <td>{{ pk.last_used_at ? formatDateTime(pk.last_used_at) : '—' }}</td>
                    <!-- 状態を色だけで示さない（9.2） -->
                    <td>{{ pk.backed_up ? '✓' : '—' }}</td>
                    <td class="row-actions">
                      <button type="button" class="secondary" @click="deletingPasskey = pk">
                        削除
                      </button>
                    </td>
                  </tr>
                  <!-- **登録したホスト名と違えば使えない**（`Design.md` 6.8.3）。削除は押せる -->
                  <tr v-if="pk.rp_id !== currentHost">
                    <td colspan="5" class="warn-text">
                      ⚠ このアドレスでは使えません（{{ pk.rp_id }} で登録）
                    </td>
                  </tr>
                </template>
              </tbody>
            </table>
            <p v-else class="empty">登録されていません。</p>

            <!-- 結果はパスキーの項目の中に出す（6.4）。MFA の結果欄は使わない -->
            <p v-if="passkeyError" class="alert" role="alert">{{ passkeyError.message }}</p>
            <p v-else-if="passkeyNotice" class="ok" role="status">{{ passkeyNotice }}</p>
          </div>
        </form>
      </div>
    </div>

    <TotpRegisterModal
      v-if="registerOpen"
      @close="registerOpen = false"
      @registered="onRegistered"
    />

    <PasskeyRegisterModal
      v-if="passkeyRegisterOpen"
      :existing-names="passkeys.map((pk) => pk.name)"
      @close="passkeyRegisterOpen = false"
      @registered="onPasskeyRegistered"
    />

    <ConfirmDialog
      v-if="deletingPasskey"
      title="パスキーを削除"
      :message="passkeyDeleteMessage"
      confirm-label="削除する"
      danger
      :busy="passkeyDeleteBusy"
      @confirm="confirmPasskeyDelete"
      @cancel="deletingPasskey = null"
    />

    <RecoveryCodesDialog
      v-if="shownCodes"
      :codes="shownCodes"
      @close="shownCodes = null"
    />

    <ConfirmDialog
      v-if="deleting"
      title="認証アプリを削除しますか？"
      :message="deleteMessage"
      confirm-label="削除"
      danger
      :busy="deleteBusy"
      @confirm="confirmDelete"
      @cancel="deleting = null"
    />
  </div>
</template>

<style scoped>
.page {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.page-body {
  flex: 1;
  overflow: auto;
  padding: var(--pb-space-6);
}

/* ── ブロック ──────────────────────────────────────────── */
.blocks {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
  max-width: 880px;
}

.block {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
  padding: var(--pb-space-4);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
}

.block-title {
  font-size: 14px;
  font-weight: 600;
}

.sub-title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
}

/* ── 入力（プロジェクト設定と同じ形にそろえる）──────────────── */
.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

/* 2欄を横に並べる。**狭いと縦へ落とす**——余りを1列に渡す作りにすると、
   窓が狭いときにその列だけ潰れる（手順12b で実際に起きた） */
.pair {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
  gap: var(--pb-space-4);
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.required {
  color: var(--pb-danger-text);
}

.static-value {
  margin: 0;
  font-weight: 600;
}

input[type='text'],
input[type='password'],
select {
  width: 100%;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

input:disabled,
select:disabled {
  opacity: 0.6;
}

/* ラジオの並び。`fieldset` の既定の枠と余白は消す。
   **選択肢を横1行に並べる**（5.8 のワイヤーが `( ○ ライト ● ダーク … )`）。
   `.field` の `flex-direction: column` をそのまま受けると縦積みになる */
.choices {
  padding: 0;
  border: 0;
}

.choices-row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-1) var(--pb-space-4);
  align-items: center;
}

.choices label {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  cursor: pointer;
  white-space: nowrap;
}

/* 幅いっぱいに広がる input の指定が、ラジオにも当たらないようにする */
.choices input[type='radio'] {
  width: auto;
  height: auto;
  margin: 0;
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* warning は面で表す（8.4.1）。文字はベース色のまま */
.warn {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-warning-border);
  border-radius: var(--pb-radius);
  background: var(--pb-warning-bg);
  color: var(--pb-warning-text);
  font-size: 13px;
}

/* ── 結果とアクション（6.4）────────────────────────────── */
.actions {
  display: flex;
  align-items: center;
  gap: var(--pb-space-3);
}

.alert {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--pb-space-3);
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-danger-bg);
  color: var(--pb-danger-text);
  font-size: 13px;
}

.ok {
  flex: 1;
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.spacer {
  flex: 1;
}

/* ── 多要素認証（pb-103）────────────────────────────────────
   **パスワードと同じセクションの中で、区切り線で分ける。** 別のブロックに
   すると「ログインの固さ」が3か所に散る（`GuiDesign.md` 5.8） */
.divider {
  width: 100%;
  height: 1px;
  margin: 0;
  border: 0;
  background: var(--pb-line);
}

.sub-block {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
}

.sub-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-3);
}

.mfa-table {
  /* **見出しの `.sr-only` の位置の基準になる**（pb-104）。付けないと
     `position: absolute` が初期包含ブロックを基準に置かれ、文書そのものを縦に
     伸ばす——パスキーを1件登録すると、1440×900 で文書が 900 → 1300px に伸びた
     （実測）。症状は「アプリ全体が上へずれて下に余白が出る」で、Avatar.vue の
     手順19b と同じ形である。MFA の表も同じ見出しを持つので、ここで両方を止める。 */
  position: relative;
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.mfa-table th {
  padding: var(--pb-space-1) var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
  color: var(--pb-text-muted);
  font-weight: 500;
  text-align: left;
}

.mfa-table td {
  padding: var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
}

.row-actions {
  text-align: right;
}

.empty {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.recovery {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
}

/* 残数とボタンを横に並べる。**狭いと縦へ落とす**（2.4） */
.recovery-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-3);
}

.recovery-count {
  flex: 1;
  font-size: 13px;
}

/* **色だけで示さない**（9.2）ので、文言側に ⚠ を添えてある */
.warn-text {
  color: var(--pb-warning-text, var(--pb-text));
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
</style>
