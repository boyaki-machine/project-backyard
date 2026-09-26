<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * ユーザー詳細・編集（`GuiDesign.md` 5.6.2）。必要権限は `user.manage`。
 *
 * 表示は `GET /admin/users/:id` の1本で足りる（`ApiDesign.md` 6.3）。本体・
 * 認証手段・プロジェクトごとの権限・有効なセッションが同じ応答に入っている
 * （設計方針3）。**ストアは増やさない**——この画面だけが使う状態である（7.1）。
 *
 * **操作の結果はトーストではなく、操作した場所に出す**（6.4）。この画面には
 * 変更できるものが5ブロックあるので、**結果はそれぞれのブロックの中**に出す。
 * ヘッダの `[⋯]` から始めた操作も、状態が載っているブロックへ結果を返す
 * （無効化なら「基本情報」、リセットなら「認証手段」）。
 *
 * **`kind='user'` のアクターだけが対象**である（6.3）。エージェントは 404 に
 * なる。エージェントの詳細画面は未実装で、列構成ごと設計する。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ConfirmDialog from '../components/ConfirmDialog.vue'
import DeleteUserDialog from '../components/DeleteUserDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import GeneratedPasswordDialog from '../components/GeneratedPasswordDialog.vue'
import MembershipModal from '../components/MembershipModal.vue'
import type { ProjectChoice } from '../components/MembershipModal.vue'
import PageHeader from '../components/PageHeader.vue'
import UserActionsMenu from '../components/UserActionsMenu.vue'
import type { ActionItem } from '../components/UserActionsMenu.vue'
import { ApiError } from '../api/client'
import * as projectsApi from '../api/projects'
import * as usersApi from '../api/users'
import type { UserDetail } from '../api/users'
import { formatDate, formatDateTime } from '../lib/datetime'
import { useRolesStore } from '../stores/roles'
import { useAuthStore } from '../stores/auth'

/** 入力の上限（`ApiDesign.md` 6.2 の検証表と同じ。正本はサーバ側） */
const MAX_DISPLAY_NAME = 60
const MAX_EMAIL = 254

/**
 * `[+ 追加]` の候補を引く件数（13a の引き継ぎ）。
 *
 * アドミニストレータには全件見えるので、200件を超える環境は
 * 想定しない。超えたら検索付きの選択に変える。
 */
const PROJECT_CANDIDATES_PER_PAGE = 200

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

const userId = computed(() => {
  const v = route.params.id
  return typeof v === 'string' ? v : ''
})

const user = ref<UserDetail | null>(null)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

/** 自分自身か。ロール変更・無効化・削除はここで押せなくする（5.6.2） */
const isSelf = computed(() => user.value !== null && auth.actor?.id === user.value.id)

/** 想定外の例外も 2.5 の形に揃える。画面は `ApiError` だけを扱えばよくなる */
function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({
        status: 0,
        code: 'internal_error',
        message: uiText("予期しないエラーが発生しました"),
      })
}

// ── 取得 ──────────────────────────────────────────────────────
async function load(): Promise<void> {
  loading.value = true
  loadError.value = null
  try {
    user.value = await usersApi.getUser(userId.value)
    resetBasicForm()
    roleDraft.value = user.value.system_role
  } catch (e: unknown) {
    const err = toApiError(e)
    // ルーターガードと同じ規則で倒す（7.2）。エージェントを指す ID でもここへ来る
    if (err.status === 404) {
      await router.replace('/404')
      return
    }
    if (err.status === 403) {
      await router.replace('/403')
      return
    }
    loadError.value = err
    user.value = null
  } finally {
    loading.value = false
  }
}

/**
 * ロールの選択肢と表示名（`ApiDesign.md` 7.1）。**画面は対応表を持たない**
 * （`GuiDesign.md` 5.6。正本は `DbDesign.md` 7.3 のシード）。
 *
 * この画面は `user.manage` を要するので全件を読める。
 */
const rolesStore = useRolesStore()
const systemRoles = computed(() => rolesStore.systemRoles)
const projectRoles = computed(() => rolesStore.projectRoles)

onMounted(() => {
  void load()
  void rolesStore.ensureRoles('all')
})

// 一覧から別の行を開くとコンポーネントは再生成されないことがある（同じルート）。
// `:id` だけが変わる場合に備えて取り直す。
watch(userId, () => {
  clearResults()
  void load()
})

/** 応答を1か所で受ける。`version` を含むすべての表示が同時に新しくなる */
function apply(next: UserDetail): void {
  user.value = next
  resetBasicForm()
  roleDraft.value = next.system_role
}

function clearResults(): void {
  editing.value = false
  basicError.value = null
  basicSaved.value = false
  roleError.value = null
  roleSaved.value = false
  membershipError.value = null
  membershipNotice.value = null
  credentialError.value = null
  credentialNotice.value = null
  sessionError.value = null
  sessionNotice.value = null
}

// ── 基本情報（5.6.2）────────────────────────────────────────
//
// **`[編集]` はブロックの操作であり、行の操作ではない。** 押すと表示名とメールの
// 両方が編集可能になり、ブロック内に `[保存]` と `[キャンセル]` が出る。
// 状態は `[⋯]` の「無効化」、ロールは「システムロール」ブロックが担う。
const editing = ref(false)
const displayName = ref('')
const email = ref('')
const savingBasic = ref(false)
const basicError = ref<ApiError | null>(null)
const basicSaved = ref(false)

function resetBasicForm(): void {
  displayName.value = user.value?.display_name ?? ''
  email.value = user.value?.email ?? ''
}

function startEdit(): void {
  resetBasicForm()
  basicError.value = null
  basicSaved.value = false
  editing.value = true
}

function cancelEdit(): void {
  resetBasicForm()
  basicError.value = null
  editing.value = false
}

const nameError = computed(() => {
  const v = displayName.value.trim()
  if (v === '') return uiText("表示名を入力してください")
  if (v.length > MAX_DISPLAY_NAME) return uiText("{value0}文字以内で入力してください", { value0: MAX_DISPLAY_NAME })
  return null
})

const emailError = computed(() => {
  const v = email.value.trim()
  if (v === '') return uiText("メールアドレスを入力してください")
  if (v.length > MAX_EMAIL) return uiText("{value0}文字以内で入力してください", { value0: MAX_EMAIL })
  // 形式の正本はサーバ側。ここは押す前に気づかせるための最小の確認にとどめる
  if (!v.includes('@')) return uiText("メールアドレスの形式で入力してください")
  return null
})

const basicChanged = computed(() => {
  const u = user.value
  if (u === null) return false
  return displayName.value.trim() !== u.display_name || email.value.trim() !== u.email
})

const canSaveBasic = computed(
  () =>
    basicChanged.value &&
    nameError.value === null &&
    emailError.value === null &&
    !savingBasic.value,
)

// 入力欄に紐づくエラー（2.5 の details）
const nameDetail = computed(() => basicError.value?.detailFor('display_name'))
const emailDetail = computed(() => basicError.value?.detailFor('email'))

/** 入力欄に紐づかないエラーだけをボタンの近くに出す（同じ文言を二重に見せない） */
const generalBasicError = computed(() => {
  const e = basicError.value
  if (!e) return null
  if (e.detailFor('display_name') || e.detailFor('email')) return null
  return e.message
})

/** 楽観ロックの 409 だけ再取得を促す（2.8）。他の 409 は message で足りる */
function isVersionConflict(e: ApiError | null): boolean {
  return e?.status === 409 && e.code === 'conflict'
}

async function saveBasic(): Promise<void> {
  const u = user.value
  if (u === null || !canSaveBasic.value) return

  savingBasic.value = true
  basicError.value = null
  basicSaved.value = false
  try {
    // 変更されたフィールドだけを送る（6.4 の部分更新）
    const body: usersApi.UpdateUserRequest = {}
    if (displayName.value.trim() !== u.display_name) body.display_name = displayName.value.trim()
    if (email.value.trim() !== u.email) body.email = email.value.trim()

    const res = await usersApi.updateUser(u.id, u.version, body)
    apply(res)
    editing.value = false
    basicSaved.value = true

    // 自分自身を編集した場合、メニューに出ている自分の名前が古くなる
    if (isSelf.value) {
      try {
        await auth.refresh()
      } catch {
        // 表示が古いままになるだけ。保存そのものは済んでいる
      }
    }
  } catch (e: unknown) {
    basicError.value = toApiError(e)
  } finally {
    savingBasic.value = false
  }
}

// ── システムロール（5.6.2）──────────────────────────────────
const roleDraft = ref<'operator' | 'administrator'>('operator')
const savingRole = ref(false)
const roleError = ref<ApiError | null>(null)
const roleSaved = ref(false)

const roleChanged = computed(
  () => user.value !== null && roleDraft.value !== user.value.system_role,
)

watch(roleDraft, () => {
  roleSaved.value = false
})

async function saveRole(): Promise<void> {
  const u = user.value
  if (u === null || !roleChanged.value || savingRole.value || isSelf.value) return

  savingRole.value = true
  roleError.value = null
  roleSaved.value = false
  try {
    const res = await usersApi.updateUser(u.id, u.version, { system_role: roleDraft.value })
    apply(res)
    roleSaved.value = true
  } catch (e: unknown) {
    roleError.value = toApiError(e)
  } finally {
    savingRole.value = false
  }
}

// ── プロジェクトごとの権限（5.6.2 / `ApiDesign.md` 6.8）──────────
//
// 一覧は表、追加はモーダル。ロールの `▾` は**選んだ時点で送る**（6.8 は冪等）。
// ワイヤーに保存ボタンが無く、行ごとに保存を持つと「システムロール」ブロックの
// `[保存]` と意味が混ざるためである。
const membershipBusy = ref<string | null>(null)
const membershipError = ref<ApiError | null>(null)
const membershipNotice = ref<string | null>(null)

const addOpen = ref(false)
const candidates = ref<ProjectChoice[]>([])
const candidatesLoading = ref(false)
const addBusy = ref(false)
const addError = ref<ApiError | null>(null)

const memberships = computed(() => user.value?.project_memberships ?? [])

/** 既に権限を持つプロジェクトは候補から外す（変更は行の `▾` で行う） */
async function openAdd(): Promise<void> {
  addError.value = null
  addOpen.value = true
  candidatesLoading.value = true
  try {
    const res = await projectsApi.listProjects({
      per_page: PROJECT_CANDIDATES_PER_PAGE,
      sort: 'key',
      order: 'asc',
      // アーカイブ済みにも権限は与えられる（6.3 は両方を返す）
      status: 'all',
    })
    const taken = new Set(memberships.value.map((m) => m.project_key))
    candidates.value = res.items
      .filter((p) => !taken.has(p.key))
      .map((p) => ({ key: p.key, name: p.name }))
  } catch (e: unknown) {
    addError.value = toApiError(e)
    candidates.value = []
  } finally {
    candidatesLoading.value = false
  }
}

async function addMembership(value: { projectKey: string; role: string }): Promise<void> {
  const u = user.value
  if (u === null || addBusy.value) return

  addBusy.value = true
  addError.value = null
  try {
    await usersApi.putMembership(u.id, value.projectKey, value.role)
    addOpen.value = false
    membershipError.value = null
    membershipNotice.value = uiText("{value0} の権限を追加しました", { value0: value.projectKey })
    // 応答は1件ぶんなので、一覧と `version` を合わせるために取り直す
    await reloadDetail()
  } catch (e: unknown) {
    addError.value = toApiError(e)
  } finally {
    addBusy.value = false
  }
}

async function changeMembershipRole(projectKey: string, role: string): Promise<void> {
  const u = user.value
  if (u === null) return

  membershipBusy.value = projectKey
  membershipError.value = null
  membershipNotice.value = null
  try {
    await usersApi.putMembership(u.id, projectKey, role)
    membershipNotice.value = uiText("{value0} のロールを {value1} に変更しました", { value0: projectKey, value1: rolesStore.roleLabel(role) })
    await reloadDetail()
  } catch (e: unknown) {
    membershipError.value = toApiError(e)
    // 送れなかったときは画面の値をサーバの値へ戻す（ずれた状態を見せない。6.4）
    await reloadDetail()
  } finally {
    membershipBusy.value = null
  }
}

async function removeMembership(projectKey: string): Promise<void> {
  const u = user.value
  if (u === null) return

  membershipBusy.value = projectKey
  membershipError.value = null
  membershipNotice.value = null
  try {
    await usersApi.deleteMembership(u.id, projectKey)
    membershipNotice.value = uiText("{value0} の権限を削除しました", { value0: projectKey })
    await reloadDetail()
  } catch (e: unknown) {
    membershipError.value = toApiError(e)
  } finally {
    membershipBusy.value = null
  }
}

/** 結果表示を消さずに本体だけ取り直す（`load` は画面ごと作り直してしまう） */
async function reloadDetail(): Promise<void> {
  try {
    apply(await usersApi.getUser(userId.value))
  } catch {
    // 取り直せなくても、操作そのものの結果は上で伝えてある
  }
}

// ── 認証手段（5.6.2 / `ApiDesign.md` 6.6）───────────────────────
const credentialError = ref<ApiError | null>(null)
const credentialNotice = ref<string | null>(null)
const resetting = ref(false)
const resetConfirmOpen = ref(false)
/** 生成されたパスワード。**この応答でしか手に入らない**（6.6） */
const generated = ref<string | null>(null)

/** 認証プロバイダは `local` のみ（`DbDesign.md` 7.1 のシード） */
const localIdentity = computed(
  () => user.value?.identities.find((i) => i.provider_type === 'local') ?? null,
)

async function runPasswordReset(): Promise<void> {
  const u = user.value
  if (u === null || resetting.value) return

  resetting.value = true
  credentialError.value = null
  credentialNotice.value = null
  try {
    const res = await usersApi.resetUserPassword(u.id)
    generated.value = res.generated_password
    credentialNotice.value = uiText("パスワードをリセットし、すべてのセッションを失効しました")
    resetConfirmOpen.value = false
    // `password_updated_at` とセッション一覧が変わる（6.6）
    await reloadDetail()
  } catch (e: unknown) {
    credentialError.value = toApiError(e)
    resetConfirmOpen.value = false
  } finally {
    resetting.value = false
  }
}

// ── 第2要素の解除（5.6.2 / `ApiDesign.md` 6.9）──────────
//
// **6.6 のパスワードリセットと同じブロックに置くが、別の操作である。**
// 締め出しの原因が2つある——パスワードを忘れた人と、認証アプリを失った人は
// 別の人で、まとめて直すと必要のない資格情報まで作り替える。
const mfaResetting = ref(false)
const mfaConfirmOpen = ref(false)

/** 確定済みの認証器の件数（6.3 の `mfa_credential_count`） */
const mfaCount = computed(() => user.value?.mfa_credential_count ?? 0)

async function runMfaReset(): Promise<void> {
  const u = user.value
  if (u === null || mfaResetting.value) return

  mfaResetting.value = true
  credentialError.value = null
  credentialNotice.value = null
  try {
    await usersApi.resetUserMfa(u.id)
    credentialNotice.value = uiText("第2要素を解除しました。次のログインからパスワードだけで入れます")
    mfaConfirmOpen.value = false
    // `mfa_credential_count` が変わる（6.3）
    await reloadDetail()
  } catch (e: unknown) {
    credentialError.value = toApiError(e)
    mfaConfirmOpen.value = false
  } finally {
    mfaResetting.value = false
  }
}

// ── パスキーの全削除（5.6.2 / `ApiDesign.md` 6.10）──────
//
// **乗っ取りの疑いがあるときの口である**（`Design.md` 6.8.6）。パスキーは
// パスワード無しで入れる鍵なので、6.6 のリセットも 6.9 の解除もそれを消さない。
// **この1つで乗っ取りが直ったと読ませない**——確認の本文に、リセットと失効が
// 別の操作であることを書く（5.6.2）。
const passkeyResetting = ref(false)
const passkeyConfirmOpen = ref(false)

/** 登録済みのパスキーの件数（6.3 の `passkey_count`） */
const passkeyCount = computed(() => user.value?.passkey_count ?? 0)

async function runPasskeyReset(): Promise<void> {
  const u = user.value
  if (u === null || passkeyResetting.value) return

  passkeyResetting.value = true
  credentialError.value = null
  credentialNotice.value = null
  try {
    await usersApi.resetUserPasskeys(u.id)
    credentialNotice.value = uiText("パスキーをすべて削除しました。パスワードでは引き続きログインできます")
    passkeyConfirmOpen.value = false
    // `passkey_count` が変わる（6.3）
    await reloadDetail()
  } catch (e: unknown) {
    credentialError.value = toApiError(e)
    passkeyConfirmOpen.value = false
  } finally {
    passkeyResetting.value = false
  }
}

// ── 有効なセッション（5.6.2 / `ApiDesign.md` 6.7）────────────────
//
// **一覧は参照のみ。** 行ごとの `[失効]` は出さない（13a の判断）。管理者が
// 他人の1セッションを選んで切る場面は考えにくく、怪しいセッションが1つ
// あるなら全部を切るのが実務の動きである。
const sessionError = ref<ApiError | null>(null)
const sessionNotice = ref<string | null>(null)
const revoking = ref(false)
const revokeConfirmOpen = ref(false)

const sessions = computed(() => user.value?.sessions ?? [])

async function runRevokeSessions(): Promise<void> {
  const u = user.value
  if (u === null || revoking.value) return

  revoking.value = true
  sessionError.value = null
  sessionNotice.value = null
  try {
    const before = sessions.value.length
    await usersApi.revokeUserSessions(u.id)
    sessionNotice.value =
      before === 0
        ? uiText("有効なセッションはありませんでした")
        : uiText("{value0}件のセッションを失効しました", { value0: before })
    revokeConfirmOpen.value = false
    await reloadDetail()
  } catch (e: unknown) {
    sessionError.value = toApiError(e)
    revokeConfirmOpen.value = false
  } finally {
    revoking.value = false
  }
}

// ── 状態の切り替えと削除（5.6.2 の `[⋯]`）──────────────────────
//
// **無効化は管理者が当該ユーザーのメニューから行う**。
// 無効なユーザーでは同じ項目が「有効化」になる——`ApiDesign.md` 6.4 は
// `is_active` を両方向に変えられると定めており、画面から戻せないと無効化が
// 事実上の不可逆操作になる。
const activeBusy = ref(false)
const activeConfirmOpen = ref(false)

const isActive = computed(() => user.value?.is_active ?? false)

async function runToggleActive(): Promise<void> {
  const u = user.value
  if (u === null || activeBusy.value) return

  activeBusy.value = true
  basicError.value = null
  basicSaved.value = false
  try {
    const res = await usersApi.updateUser(u.id, u.version, { is_active: !u.is_active })
    apply(res)
    basicSaved.value = true
    activeConfirmOpen.value = false
  } catch (e: unknown) {
    basicError.value = toApiError(e)
    activeConfirmOpen.value = false
  } finally {
    activeBusy.value = false
  }
}

const deleteOpen = ref(false)
const deleting = ref(false)
const deleteError = ref<ApiError | null>(null)

function closeDelete(): void {
  deleteOpen.value = false
  deleteError.value = null
}

async function runDelete(): Promise<void> {
  const u = user.value
  if (u === null || deleting.value) return

  deleting.value = true
  deleteError.value = null
  try {
    await usersApi.deleteUser(u.id)
    // 操作した場所（この画面）が消えるので、**着地する一覧**へ結果を渡す（6.4）。
    // URL には残さない（再読み込みで通知が復活しないように）
    await router.replace({
      path: '/admin/users',
      state: { notice: uiText("{value0} を削除しました", { value0: u.display_name }) },
    })
  } catch (e: unknown) {
    deleteError.value = toApiError(e)
  } finally {
    deleting.value = false
  }
}

// ── ヘッダの `[⋯]`（5.6.2）───────────────────────────────────
//
// **編集は入れない。** 同じ画面の「基本情報」ブロックの `[編集]` が担う（5.6.2）。
// ここに置くのは、この画面のブロックに現れない操作である。
const menuItems = computed<ActionItem[]>(() => [
  { key: 'password-reset', label: uiText("パスワードをリセット") },
  { key: 'revoke-sessions', label: uiText("セッションを全失効") },
  {
    key: 'reset-mfa',
    label: uiText("多要素認証を解除"),
    // **登録が0件なら押せない。** 黙って消さず、理由を添える（5.6.2 の作法）
    disabled: mfaCount.value === 0,
    reason: uiText("登録がないため解除できません"),
  },
  {
    key: 'reset-passkeys',
    label: uiText("パスキーを全削除"),
    disabled: passkeyCount.value === 0,
    reason: uiText("登録がないため削除できません"),
  },
  {
    key: 'toggle-active',
    label: isActive.value ? uiText("無効化") : uiText("有効化"),
    disabled: isSelf.value && isActive.value,
    reason: uiText("自分自身は無効化できません"),
  },
  {
    key: 'delete',
    label: uiText("削除"),
    danger: true,
    disabled: isSelf.value,
    reason: uiText("自分自身は削除できません"),
  },
])

function onMenuSelect(key: string): void {
  if (key === 'password-reset') resetConfirmOpen.value = true
  else if (key === 'revoke-sessions') revokeConfirmOpen.value = true
  else if (key === 'reset-mfa') mfaConfirmOpen.value = true
  else if (key === 'reset-passkeys') passkeyConfirmOpen.value = true
  else if (key === 'toggle-active') {
    // 無効化だけ確認を挟む。有効化は失うものが無い（6.3 と同じ考え方）
    if (isActive.value) activeConfirmOpen.value = true
    else void runToggleActive()
  } else if (key === 'delete') deleteOpen.value = true
}
</script>

<template>
  <div class="page">
    <PageHeader :title="user ? `👤 ${user.display_name}` : $ui('ユーザー詳細')">
      <template #lead>
        <!-- 戻り先は一覧で固定する。`router.back()` は直リンクで開いたとき
             どこへ戻るか分からない -->
        <RouterLink class="back" to="/admin/users" :aria-label="$ui('アカウント / 権限へ戻る')">
          <span aria-hidden="true">←</span>
        </RouterLink>
      </template>

      <template v-if="user" #subtitle>{{ user.email }}</template>

      <template v-if="user" #actions>
        <UserActionsMenu
          :items="menuItems"
          :label="$ui('{value0} の操作メニュー', { value0: user.display_name })"
          @select="onMenuSelect"
        />
      </template>
    </PageHeader>

    <div class="page-body">
      <!-- エラー（6.2）。原因はサーバが返した message をそのまま出す（2.5） -->
      <EmptyState
        v-if="loadError"
        :title="$ui('ユーザーを取得できませんでした')"
        :description="loadError.message"
      >
        <template #action>
          <button type="button" class="primary" @click="load">{{ $ui('再試行') }}</button>
        </template>
      </EmptyState>

      <!-- 読み込み中はスケルトン。実際のブロックの形を模す（6.2） -->
      <div v-else-if="loading && !user" class="blocks" aria-busy="true">
        <div v-for="n in 4" :key="n" class="block">
          <span class="skeleton title"></span>
          <span class="skeleton"></span>
          <span class="skeleton short"></span>
        </div>
      </div>

      <div v-else-if="user" class="blocks">
        <!-- ── 基本情報 ─────────────────────────────────────── -->
        <section class="block">
          <div class="block-head">
            <h2 class="block-title">{{ $ui('基本情報') }}</h2>
            <button
              v-if="!editing"
              type="button"
              class="secondary small"
              @click="startEdit"
            > {{ $ui('編集') }} </button>
          </div>

          <!-- 表示（5.6.2 の5項目） -->
          <dl v-if="!editing" class="fields">
            <dt>{{ $ui('表示名') }}</dt>
            <dd>{{ user.display_name }}</dd>

            <dt>{{ $ui('メール') }}</dt>
            <dd>{{ user.email }}</dd>

            <!-- 状態を色だけで示さない（9.2）。記号と文字の両方を出す -->
            <dt>{{ $ui('状態') }}</dt>
            <dd :class="{ inactive: !user.is_active }">
              <span aria-hidden="true">{{ user.is_active ? '●' : '○' }}</span>
              {{ user.is_active ? $ui("有効") : $ui("無効") }}
            </dd>

            <dt>{{ $ui('最終ログイン') }}</dt>
            <dd>{{ user.last_login_at === null ? '—' : formatDateTime(user.last_login_at) }}</dd>

            <dt>{{ $ui('作成') }}</dt>
            <dd>{{ formatDate(user.created_at) }}</dd>
          </dl>

          <!-- 編集（5.6.2）。表示名とメールの両方を編集可能にする -->
          <form v-else class="form" @submit.prevent="saveBasic">
            <label class="field">
              <span class="label">{{ $ui('表示名') }} <span class="required">*</span></span>
              <input
                v-model="displayName"
                type="text"
                name="display_name"
                :maxlength="MAX_DISPLAY_NAME"
                :aria-invalid="nameDetail !== undefined || nameError !== null"
                :disabled="savingBasic"
              />
              <span v-if="nameDetail" class="detail">{{ nameDetail.message }}</span>
              <span v-else-if="nameError" class="detail">✕ {{ nameError }}</span>
            </label>

            <label class="field">
              <span class="label">{{ $ui('メールアドレス') }} <span class="required">*</span></span>
              <input
                v-model="email"
                type="email"
                name="email"
                autocapitalize="off"
                autocomplete="off"
                spellcheck="false"
                :maxlength="MAX_EMAIL"
                :aria-invalid="emailDetail !== undefined || emailError !== null"
                :disabled="savingBasic"
              />
              <span v-if="emailDetail" class="detail">{{ emailDetail.message }}</span>
              <span v-else-if="emailError" class="detail">✕ {{ emailError }}</span>
              <span class="hint"> {{ $ui('変更するとログインに使うアドレスも変わります（本人に伝えてください）。') }} </span>
            </label>

            <div class="actions-row">
              <button
                type="button"
                class="secondary"
                :disabled="savingBasic"
                @click="cancelEdit"
              > {{ $ui('キャンセル') }} </button>
              <button type="submit" class="primary" :disabled="!canSaveBasic">{{ $ui('保存') }}</button>
            </div>
          </form>

          <!-- 結果は操作した場所に出す（6.4） -->
          <p v-if="basicSaved" class="ok" role="status">{{ $ui('✓ 保存しました') }}</p>
          <p v-if="generalBasicError" class="error" role="alert">
            ✕ {{ generalBasicError }}
            <button
              v-if="isVersionConflict(basicError)"
              type="button"
              class="secondary small"
              @click="load"
            > {{ $ui('最新の内容を取得') }} </button>
          </p>
        </section>

        <!-- ── システムロール ───────────────────────────────── -->
        <section class="block">
          <h2 class="block-title">{{ $ui('システムロール') }}</h2>

          <div class="choices" role="radiogroup" :aria-label="$ui('システムロール')">
            <label v-for="r in systemRoles" :key="r.key" class="choice">
              <input
                v-model="roleDraft"
                type="radio"
                name="system_role"
                :value="r.key"
                :disabled="savingRole || isSelf"
              />
              <span class="choice-body">
                <span class="choice-label">{{ rolesStore.roleLabel(r.key) }}</span>
                <span class="choice-description">{{ rolesStore.roleDescription(r.key) }}</span>
              </span>
            </label>
          </div>

          <div class="actions-row">
            <!-- 押せない理由を添える（5.6.2）。項目そのものは消さない -->
            <span v-if="isSelf" class="hint">{{ $ui('自分自身のロールは変更できません') }}</span>
            <button
              type="button"
              class="primary"
              :disabled="!roleChanged || savingRole || isSelf"
              @click="saveRole"
            > {{ $ui('保存') }} </button>
          </div>

          <p v-if="roleSaved" class="ok" role="status">{{ $ui('✓ 保存しました') }}</p>
          <p v-if="roleError" class="error" role="alert">
            ✕ {{ roleError.message }}
            <button
              v-if="isVersionConflict(roleError)"
              type="button"
              class="secondary small"
              @click="load"
            > {{ $ui('最新の内容を取得') }} </button>
          </p>
        </section>

        <!-- ── プロジェクトごとの権限 ───────────────────────── -->
        <section class="block">
          <div class="block-head">
            <h2 class="block-title">{{ $ui('プロジェクトごとの権限') }}</h2>
            <button type="button" class="secondary small" @click="openAdd">{{ $ui('+ 追加') }}</button>
          </div>

          <!-- 日本語は行を折り返すとその位置に空白が入る。1行に収める -->
          <p v-if="memberships.length === 0" class="hint"> {{ $ui('プロジェクトの権限がありません。アドミニストレータはメンバーでなくてもすべてのプロジェクトを見られます。') }} </p>

          <table v-else class="table">
            <thead>
              <tr>
                <th scope="col">{{ $ui('プロジェクト') }}</th>
                <th scope="col" class="role-col">{{ $ui('ロール') }}</th>
                <th scope="col" class="joined">{{ $ui('参加') }}</th>
                <th scope="col"><span class="visually-hidden">{{ $ui('操作') }}</span></th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="m in memberships" :key="m.project_id">
                <td>
                  <RouterLink class="project" :to="`/p/${m.project_key}`">
                    {{ m.project_key }}
                  </RouterLink>
                  <span class="project-name">{{ m.project_name }}</span>
                </td>
                <td class="role-col">
                  <!-- 選んだ時点で送る（6.8 は冪等） -->
                  <select
                    :value="m.role"
                    :disabled="membershipBusy !== null"
                    :aria-label="$ui('{value0} のロール', { value0: m.project_key })"
                    @change="
                      changeMembershipRole(
                        m.project_key,
                        ($event.target as HTMLSelectElement).value,
                      )
                    "
                  >
                    <option v-for="r in projectRoles" :key="r.key" :value="r.key">
                      {{ rolesStore.roleLabel(r.key) }}
                    </option>
                  </select>
                </td>
                <td class="joined">{{ formatDate(m.joined_at) }}</td>
                <td class="row-actions">
                  <button
                    type="button"
                    class="secondary small"
                    :disabled="membershipBusy !== null"
                    @click="removeMembership(m.project_key)"
                  > {{ $ui('削除') }} </button>
                </td>
              </tr>
            </tbody>
          </table>

          <p v-if="membershipNotice" class="ok" role="status">✓ {{ membershipNotice }}</p>
          <p v-if="membershipError" class="error" role="alert">✕ {{ membershipError.message }}</p>
        </section>

        <!-- ── 認証手段（`DbDesign.md` 6.2 の user_identity）───── -->
        <section class="block">
          <h2 class="block-title">{{ $ui('認証手段') }}</h2>

          <!-- **1つの表にまとめる。** 別の table に分けると列幅が独立して決まり、
               2行の「最終更新」と「登録済み」が縦に揃わない -->
          <table class="table">
            <tbody>
              <tr v-if="localIdentity">
                <td>{{ $ui('ローカルパスワード') }}</td>
                <td class="muted"> {{ $ui('最終更新') }} {{
                    localIdentity.password_updated_at === null
                      ? '—'
                      : formatDate(localIdentity.password_updated_at)
                  }}
                </td>
                <td class="row-actions">
                  <button
                    type="button"
                    class="secondary small"
                    :disabled="resetting"
                    @click="resetConfirmOpen = true"
                  > {{ $ui('リセット') }} </button>
                </td>
              </tr>
              <tr v-else>
                <td>{{ $ui('ローカルパスワード') }}</td>
                <td class="muted">{{ $ui('設定されていません') }}</td>
                <td class="row-actions"></td>
              </tr>

              <!-- 第2要素（`DbDesign.md` 6.18）。**user_identity ではない**が、
                   管理者が「この人はどうやってログインするか」を1か所で読むために
                   同じブロックへ置く。**出すのは件数だけ** -->
              <tr>
                <td>{{ $ui('多要素認証（TOTP）') }}</td>
                <td class="muted">
                  {{ mfaCount === 0 ? $ui("登録されていません") : $ui("{value0}件 登録済み", { value0: mfaCount }) }}
                </td>
                <td class="row-actions">
                  <!-- **黙って消さず、押せない理由を添える**（5.6.2 の作法） -->
                  <button
                    type="button"
                    class="secondary small"
                    :disabled="mfaCount === 0 || mfaResetting"
                    :title="mfaCount === 0 ? $ui('登録がないため解除できません') : undefined"
                    @click="mfaConfirmOpen = true"
                  > {{ $ui('解除') }} </button>
                </td>
              </tr>

              <!-- パスキー（`DbDesign.md` 6.19）。第2要素と同じく
                   **user_identity ではない**が、同じブロックに**件数だけ**を出す -->
              <tr>
                <td>{{ $ui('パスキー') }}</td>
                <td class="muted">
                  {{ passkeyCount === 0 ? $ui("登録されていません") : $ui("{value0}件 登録済み", { value0: passkeyCount }) }}
                </td>
                <td class="row-actions">
                  <button
                    type="button"
                    class="secondary small"
                    :disabled="passkeyCount === 0 || passkeyResetting"
                    :title="passkeyCount === 0 ? $ui('登録がないため削除できません') : undefined"
                    @click="passkeyConfirmOpen = true"
                  > {{ $ui('全削除') }} </button>
                </td>
              </tr>
            </tbody>
          </table>

          <p class="hint">{{ $ui('（OIDC/SAML 連携は構想）') }}</p>

          <p v-if="credentialNotice" class="ok" role="status">✓ {{ credentialNotice }}</p>
          <p v-if="credentialError" class="error" role="alert">✕ {{ credentialError.message }}</p>
        </section>

        <!-- ── 有効なセッション ─────────────────────────────── -->
        <section class="block">
          <h2 class="block-title">{{ $ui('有効なセッション') }}</h2>

          <p v-if="sessions.length === 0" class="hint">{{ $ui('有効なセッションはありません。') }}</p>

          <table v-else class="table">
            <tbody>
              <!-- 行ごとの失効は持たない（5.6.2）。一覧は参照のみ -->
              <tr v-for="s in sessions" :key="s.id">
                <td>{{ s.client_info ?? $ui("不明なクライアント") }}</td>
                <td class="muted"> {{ $ui('最終利用') }} {{ s.last_used_at === null ? '—' : formatDateTime(s.last_used_at) }}
                </td>
                <td class="muted"> {{ $ui('期限') }} {{ s.expires_at === null ? $ui("無期限") : formatDateTime(s.expires_at) }}
                </td>
              </tr>
            </tbody>
          </table>

          <div class="actions-row">
            <button
              type="button"
              class="secondary"
              :disabled="revoking"
              @click="revokeConfirmOpen = true"
            > {{ $ui('すべてのセッションを失効') }} </button>
          </div>

          <p v-if="sessionNotice" class="ok" role="status">✓ {{ sessionNotice }}</p>
          <p v-if="sessionError" class="error" role="alert">✕ {{ sessionError.message }}</p>
        </section>
      </div>
    </div>

    <!-- ── 確認と結果のダイアログ ───────────────────────────── -->
    <ConfirmDialog
      v-if="resetConfirmOpen"
      :title="$ui('パスワードをリセット')"
      :message="$ui('{value0} のパスワードを新しく生成します。\n現在のパスワードは使えなくなり、有効なセッションはすべて失効します。', { value0: user?.display_name ?? '' })"
      :confirm-label="$ui('リセットする')"
      danger
      :busy="resetting"
      @confirm="runPasswordReset"
      @cancel="resetConfirmOpen = false"
    />

    <ConfirmDialog
      v-if="mfaConfirmOpen"
      :title="$ui('多要素認証を解除')"
      :message="$ui('{value0} の第2要素の保護が外れ、次のログインからパスワードだけで入れるようになります。\n登録済みの認証アプリとリカバリコードはすべて削除されます。\nパスワードとセッションには影響しません。', { value0: user?.display_name ?? '' })"
      :confirm-label="$ui('解除する')"
      danger
      :busy="mfaResetting"
      @confirm="runMfaReset"
      @cancel="mfaConfirmOpen = false"
    />

    <ConfirmDialog
      v-if="passkeyConfirmOpen"
      :title="$ui('パスキーを全削除')"
      :message="$ui('{value0} の登録済みのパスキーをすべて削除します。対象はパスキーでログインできなくなりますが、パスワードでは入れます。\n乗っ取りを疑っているなら、パスワードのリセットとセッションの失効も別に行ってください。', { value0: user?.display_name ?? '' })"
      :confirm-label="$ui('全削除する')"
      danger
      :busy="passkeyResetting"
      @confirm="runPasskeyReset"
      @cancel="passkeyConfirmOpen = false"
    />

    <ConfirmDialog
      v-if="revokeConfirmOpen"
      :title="$ui('すべてのセッションを失効')"
      :message="$ui('{value0} のログイン中のセッションとアクセストークンをすべて失効します。\n本人は次のリクエストからログインし直す必要があります。', { value0: user?.display_name ?? '' })"
      :confirm-label="$ui('失効する')"
      danger
      :busy="revoking"
      @confirm="runRevokeSessions"
      @cancel="revokeConfirmOpen = false"
    />

    <ConfirmDialog
      v-if="activeConfirmOpen"
      :title="$ui('ユーザーを無効化')"
      :message="$ui('{value0} を無効化します。\n本人は次のリクエストからログインできなくなります。有効なセッションは失効しません（必要なら別に失効してください）。', { value0: user?.display_name ?? '' })"
      :confirm-label="$ui('無効化する')"
      danger
      :busy="activeBusy"
      @confirm="runToggleActive"
      @cancel="activeConfirmOpen = false"
    />

    <DeleteUserDialog
      v-if="deleteOpen && user"
      :display-name="user.display_name"
      :email="user.email"
      :busy="deleting"
      :error-message="deleteError?.message ?? null"
      @confirm="runDelete"
      @cancel="closeDelete"
    />

    <MembershipModal
      v-if="addOpen"
      :candidates="candidates"
      :loading="candidatesLoading"
      :busy="addBusy"
      :error-message="addError?.message ?? null"
      @submit="addMembership"
      @cancel="addOpen = false"
    />

    <!-- 生成されたパスワードはこの1回しか出せない（6.6） -->
    <GeneratedPasswordDialog
      v-if="generated && user"
      :title="$ui('パスワードをリセットしました')"
      :lead-suffix="$ui('のパスワードを再発行しました。')"
      :footer-note="
        isSelf
          ? $ui('このパスワードで入り直し、次回ログイン後に新しいものへ変更してください。')
          : undefined
      "
      :display-name="user.display_name"
      :email="user.email"
      :password="generated"
      @close="generated = null"
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

/* ヘッダの戻る導線（5.6.2）。48px の中に収める（2.5） */
.back {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: var(--pb-radius);
  color: var(--pb-text-muted);
  font-size: 16px;
}

.back:hover {
  background: var(--pb-hover);
  color: var(--pb-text);
}

/* ── ブロック（プロジェクト設定と同じ形にそろえる）─────────── */
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

.block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-3);
}

.block-title {
  font-size: 14px;
  font-weight: 600;
}

/* ── 表示（定義リスト）───────────────────────────────── */
.fields {
  display: grid;
  grid-template-columns: 140px 1fr;
  gap: var(--pb-space-2) var(--pb-space-3);
  margin: 0;
  align-items: baseline;
}

dt {
  color: var(--pb-text-muted);
  font-size: 13px;
}

dd {
  margin: 0;
  min-width: 0;
  overflow-wrap: anywhere;
}

/* 無効なユーザーは輝度の階層で一段下げる（8.2 / 5.6 と同じ扱い） */
dd.inactive {
  color: var(--pb-text-muted);
}

/* ── 入力 ─────────────────────────────────────────────── */
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

input:disabled {
  opacity: 0.6;
}

select {
  height: 32px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

select:disabled {
  opacity: 0.6;
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
  line-height: 1.7;
}

/* ── ロールの選択（追加モーダルと同じ形）─────────────────── */
.choices {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
}

.choice {
  display: flex;
  align-items: flex-start;
  gap: var(--pb-space-2);
  cursor: pointer;
}

.choice-body {
  display: flex;
  flex-direction: column;
}

.choice-label {
  font-weight: 600;
}

.choice-description {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.choice:has(input:disabled) {
  cursor: default;
  opacity: 0.6;
}

/* ── 表 ───────────────────────────────────────────────── */
.table {
  width: 100%;
  border-collapse: collapse;
}

th {
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-border);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
  text-align: left;
}

td {
  padding: var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
}

tr:last-child td {
  border-bottom: 0;
}

.role-col {
  width: 200px;
}

.joined {
  width: 110px;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.row-actions {
  width: 80px;
  text-align: right;
}

.muted {
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* キーは等幅で出す（8.10）。名前は従属情報として一段下げる */
.project {
  color: var(--pb-accent);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}

.project-name {
  margin-left: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ── 操作と結果（6.4）─────────────────────────────────── */
.actions-row {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--pb-space-3);
}

.actions-row .hint {
  flex: 1;
}

.ok {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.error {
  display: flex;
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

/* ── 読み込み中（6.2）─────────────────────────────────── */
.skeleton {
  display: block;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.skeleton.title {
  width: 30%;
}

.skeleton.short {
  width: 60%;
}

/* 読み上げ専用（9.2） */
.visually-hidden {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}
</style>
