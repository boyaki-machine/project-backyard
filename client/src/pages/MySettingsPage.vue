<script setup lang="ts">
/**
 * 自分の設定 `/me`（`GuiDesign.md` 5.8）。
 *
 * 3つのセクションを持つ。
 *
 *   基本情報      ログインID・表示名・メールアドレス・言語・タイムゾーン
 *   デザイン      テーマ・色相（8.11）
 *   セキュリティ  パスワード（将来 MFA・パスキーもここへ）
 *
 * **結果はセクションごとに出す**（6.4）。トーストは使わない。デザインだけは
 * 見た目が即座に変わることが結果の表示を兼ねるため、成功時の文言を出さない。
 *
 * **「ログインID」と「メールアドレス」は Phase 1 ではどちらも `email` を指す**
 * （`ApiDesign.md` 4.2）。ログインIDは読み取り専用で、メールアドレス欄の変更に
 * 追随する。2行に分けてあるのは、将来ログインのIDと連絡先を別々に登録できる
 * ようにするためであり、そのとき画面の形を変えずに済む（利用者の判断、2026-08-22）。
 */
import { computed, ref, watch } from 'vue'

import { ApiError } from '../api/client'
import * as meApi from '../api/me'
import type { UpdateMeRequest } from '../api/me'
import PageHeader from '../components/PageHeader.vue'
import { useAuthStore } from '../stores/auth'
import type { Hue, ThemePreference } from '../stores/ui'
import { useUiStore } from '../stores/ui'

const auth = useAuthStore()
const ui = useUiStore()

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
      <!-- タブは `/me` と `/me/tokens` の2ルートに対応する（3.2）。
           プロジェクト設定（5.9）と違い、切替が URL の遷移になる -->
      <nav class="tabs" aria-label="設定の種類">
        <RouterLink to="/me" class="tab selected" aria-current="page">一般</RouterLink>
        <RouterLink to="/me/tokens" class="tab">アクセストークン</RouterLink>
      </nav>

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
        </form>
      </div>
    </div>
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

/* ── タブ（5.8）────────────────────────────────────────── */
.tabs {
  display: flex;
  gap: var(--pb-space-1);
  margin-bottom: var(--pb-space-4);
  border-bottom: 1px solid var(--pb-border);
}

.tab {
  padding: var(--pb-space-2) var(--pb-space-4);
  border-bottom: 2px solid transparent;
  color: var(--pb-text-muted);
  font: inherit;
  text-decoration: none;
}

.tab:hover {
  color: var(--pb-text);
}

/* 選択中を色だけで示さない（9.2）。下線の太さでも区別できるようにする */
.tab.selected {
  border-bottom-color: var(--pb-accent);
  color: var(--pb-text);
  font-weight: 600;
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
</style>
