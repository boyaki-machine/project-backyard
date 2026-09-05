<script setup lang="ts">
/**
 * エージェントタブ `/me/agents`（`GuiDesign.md` 5.8.2）。
 *
 * **この画面が表すのは「自分の端末で動くクライアントを PB に登録し、そのクライアントが
 * 名乗るための資格情報を、自分の手で受け取る場所」である。** 管理台帳ではない
 * ——プロジェクト横断で見て止めるのは `/admin/users` のエージェントタブ（5.6）。
 *
 * **登録するのが本人なのは、トークンが1回だけ全文表示されるため**であり、
 * プロジェクト管理者が受け取って転送する経路を作らない
 * （`Requirements.md` 10.9.1 系統B、`ApiDesign.md` 4.5）。
 *
 * **表ではなくカードで並べる**（5.8.2）。1件が11項目と厚く、件数は1〜4件で、
 * 表にすると 900px で必ず横スクロールするためである。
 *
 * **結果は一覧の直上に出す**（6.4）。登録・発行・失効・無効化で同じ欄を使い回す。
 */
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../api/client'
import * as meApi from '../api/me'
import type { IssuedAgentToken, MyAgent } from '../api/me'
import AgentFormModal from '../components/AgentFormModal.vue'
import Avatar from '../components/Avatar.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import IssuedAgentTokenDialog from '../components/IssuedAgentTokenDialog.vue'
import MeTabs from '../components/MeTabs.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import UserActionsMenu from '../components/UserActionsMenu.vue'
import type { ActionItem } from '../components/UserActionsMenu.vue'
import { AGENT_DEFAULT_SCOPES, clientKindLabel } from '../lib/agents'
import type { AgentClientKind } from '../api/me'
import { formatDate, formatDateTime } from '../lib/datetime'

/** 有効期限の選択肢（`GuiDesign.md` 5.8.1 と同じ3つ。値域は 1〜365） */
const EXPIRY_CHOICES = [30, 90, 365] as const

/**
 * クライアント種別のカタログ（`ApiDesign.md` 4.5.7）。
 *
 * **表示名を画面が持たない**ため、一覧を描くのにこれが要る（`GuiDesign.md` 5.8.2）。
 * **引けなくても一覧は出す**——`clientKindLabel` がキーをそのまま返すので、
 * 種別の欄が空になるより読める。
 */
const clientKinds = ref<AgentClientKind[]>([])

const items = ref<MyAgent[]>([])
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

/** 一覧の直上に出す結果（6.4）。登録・発行・失効・無効化で使い回す */
const notice = ref('')
const actionError = ref<ApiError | null>(null)

/**
 * 無効なエージェントを出すか（5.8.2）。
 *
 * **無効化しても行は消えない**ので、使わなくなったものが視界に溜まり続ける。
 * **既定で畳み、件数を添えて開けるようにする**（利用者の指摘、2026-09-02）。
 *
 * **手順26a で削除（`ApiDesign.md` 4.5.4）が入ったが、畳むのはやめない**——
 * 「いま止めたいが記録は残したい」と「消したい」は別の要求であり、
 * 無効化のまま置く選択が残る（`GuiDesign.md` 5.8.2）。
 */
const showInactive = ref(false)

const inactiveCount = computed(() => items.value.filter((a) => !a.is_active).length)

const visibleItems = computed(() =>
  showInactive.value ? items.value : items.value.filter((a) => a.is_active),
)

async function load() {
  loading.value = true
  loadError.value = null
  try {
    // **カタログの失敗で一覧を落とさない。** 表示名が引けないだけで、
    // エージェントそのものは出せる。
    const [agents, kinds] = await Promise.all([
      meApi.listAgents(),
      meApi.listAgentClientKinds().catch(() => ({ items: [] as AgentClientKind[] })),
    ])
    items.value = agents.items
    clientKinds.value = kinds.items
  } catch (e: unknown) {
    loadError.value = asApiError(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ── 登録・編集（`ApiDesign.md` 4.5.2 / 4.5.4）────────────────

const showForm = ref(false)
/** 編集の相手。`null` なら登録 */
const formTarget = ref<MyAgent | null>(null)
const formBusy = ref(false)
const formError = ref<ApiError | null>(null)

function openCreate() {
  formTarget.value = null
  formError.value = null
  showForm.value = true
}

function openEdit(agent: MyAgent) {
  formTarget.value = agent
  formError.value = null
  showForm.value = true
}

async function submitForm(payload: {
  display_name: string
  project_key: string
  client_kind: string
  model_name: string
  model_version: string
  token_env_suffix: string
}) {
  formBusy.value = true
  formError.value = null
  try {
    if (formTarget.value) {
      // **送るのは変えられる3項目だけ**（4.5.4）。project_key と client_kind は
      // 変更不可なので、モーダルも読み取り専用にしてある。
      // **client_kind も送る**（`ApiDesign.md` 4.5.4、0020 から変更可能）。
      // project_key だけが変えられない。
      const updated = await meApi.updateAgent(formTarget.value.id, {
        display_name: payload.display_name,
        client_kind: payload.client_kind,
        model_name: payload.model_name,
        model_version: payload.model_version,
        // **空文字は送らない。** サーバの COALESCE は空文字を「値の指定」として
        // 扱うため、未設定へ戻す意味にはならない（4.5.4）。**空にする経路は
        // Phase 2 では作らない。**
        ...(payload.token_env_suffix === ''
          ? {}
          : { token_env_suffix: payload.token_env_suffix }),
      })
      showForm.value = false
      notice.value = `✓ エージェント「${updated.display_name}」を更新しました`
      actionError.value = null
      await load()
    } else {
      const created = await meApi.createAgent({
        display_name: payload.display_name,
        project_key: payload.project_key,
        client_kind: payload.client_kind,
        ...(payload.model_name === '' ? {} : { model_name: payload.model_name }),
        ...(payload.model_version === '' ? {} : { model_version: payload.model_version }),
        // **空なら送らない**（未設定として作られ、token_env_name が id へ倒れる）。
        ...(payload.token_env_suffix === ''
          ? {}
          : { token_env_suffix: payload.token_env_suffix }),
      })
      showForm.value = false
      notice.value = `✓ エージェント「${created.display_name}」を登録しました`
      actionError.value = null
      await load()
      // **登録が成功したら、続けて発行モーダルを開く**（`ApiDesign.md` 4.5.2 が
      // 「登録直後の token は null で、画面は続けて発行を呼ぶ」と定める）。
      // **キャンセルしても行は残る**ので、あとから発行できる。
      openIssue(created)
    }
  } catch (e: unknown) {
    formError.value = asApiError(e)
  } finally {
    formBusy.value = false
  }
}

// ── トークンの発行（`ApiDesign.md` 4.5.3）────────────────────

const issueTarget = ref<MyAgent | null>(null)
const issuing = ref(false)
const issueError = ref<ApiError | null>(null)
const newExpiresInDays = ref<number>(90)
/**
 * 憲章の編集（`doc.edit`）を許すか（`ApiDesign.md` 4.5.3。手順26a）。
 *
 * **既定は外す。** `Design.md` 6.5 が「載せるかはそのエージェントが誰に
 * 付いているかで決まる」と定めており、押さなければ従来どおりの8件になる。
 */
const allowDocEdit = ref(false)

/** 1回だけ出す発行結果。閉じると二度と出せない（4.5.3） */
const issued = ref<IssuedAgentToken | null>(null)
/** 発行結果に添えるエージェント（プロジェクト名と名前を出すため） */
const issuedAgent = ref<MyAgent | null>(null)

/**
 * 選んだ日数から算出した期日（5.8.2）。
 *
 * **「90日」ではなくカレンダーと突き合わせられる形も出す。** サーバも
 * 「発行時刻 + N 日」で計算するので、押すまでに日付が変わらなければ同じ値になる。
 */
const newExpiryDate = computed(() => {
  const d = new Date()
  d.setDate(d.getDate() + newExpiresInDays.value)
  return formatDate(d.toISOString())
})

function openIssue(agent: MyAgent) {
  issueTarget.value = agent
  newExpiresInDays.value = 90
  // **毎回外す。** 前に発行したときの選択を引き継ぐと、憲章を編集できる
  // トークンが気づかないうちに配られる。
  allowDocEdit.value = false
  issueError.value = null
}

async function issueToken() {
  const target = issueTarget.value
  if (!target || issuing.value) return
  issuing.value = true
  issueError.value = null
  try {
    // **`scopes` は押されたときだけ送る**（4.5.3）。省略すると
    // `Design.md` 6.5 の既定8件が入るので、既定の写しを画面に持たない
    // ——持つと 6.5 が変わったときに片方だけ古くなる。
    const token = await meApi.issueAgentToken(target.id, {
      expires_in_days: newExpiresInDays.value,
      ...(allowDocEdit.value ? { scopes: [...AGENT_DEFAULT_SCOPES, 'doc.edit'] } : {}),
    })
    issueTarget.value = null
    // **先に平文を出す。** 一覧の読み直しが失敗しても、二度と出せない値を
    // 落とさないためである（5.8.1 と同じ判断）。
    issuedAgent.value = target
    issued.value = token
    notice.value = `✓ 「${target.display_name}」のトークンを発行しました`
    actionError.value = null
    await load()
  } catch (e: unknown) {
    issueError.value = asApiError(e)
  } finally {
    issuing.value = false
  }
}

// ── トークンの失効（`ApiDesign.md` 4.5.5）────────────────────

const revokeTarget = ref<MyAgent | null>(null)
const revoking = ref(false)

const revokeMessage = computed(() => {
  const a = revokeTarget.value
  if (!a) return ''
  return (
    `「${a.display_name}」のトークンを失効させます。復元はできません。\n` +
    'このエージェントは次のリクエストから 401 になります。'
  )
})

async function revokeToken() {
  const target = revokeTarget.value
  if (!target?.token || revoking.value) return
  revoking.value = true
  actionError.value = null
  try {
    await meApi.revokeAgentToken(target.id, target.token.id)
    revokeTarget.value = null
    notice.value = `✓ 「${target.display_name}」のトークンを失効させました`
    await load()
  } catch (e: unknown) {
    actionError.value = asApiError(e)
    notice.value = ''
    revokeTarget.value = null
  } finally {
    revoking.value = false
  }
}

// ── 無効化・有効化（`ApiDesign.md` 4.5.4）────────────────────

const deactivateTarget = ref<MyAgent | null>(null)
const togglingActive = ref(false)

const deactivateMessage = computed(() => {
  const a = deactivateTarget.value
  if (!a) return ''
  return (
    `「${a.display_name}」を無効化します。\n` +
    'このエージェントのトークンも同時に失効し、次のリクエストから 401 になります。\n' +
    'あとから有効化できますが、トークンは発行し直しになります。'
  )
})

async function setActive(agent: MyAgent, active: boolean) {
  if (togglingActive.value) return
  togglingActive.value = true
  actionError.value = null
  try {
    await meApi.updateAgent(agent.id, { is_active: active })
    deactivateTarget.value = null
    notice.value = active
      ? `✓ 「${agent.display_name}」を有効化しました`
      : `✓ 「${agent.display_name}」を無効化しました`
    await load()
    // 無効化した行が畳まれて消えるので、開いて見せる
    if (!active) showInactive.value = true
  } catch (e: unknown) {
    actionError.value = asApiError(e)
    notice.value = ''
    deactivateTarget.value = null
  } finally {
    togglingActive.value = false
  }
}

// ── 削除（`ApiDesign.md` 4.5.4。手順26a）─────────────────────

const deleteTarget = ref<MyAgent | null>(null)
const deleting = ref(false)

/**
 * 確認の本文（`GuiDesign.md` 5.8.2）。
 *
 * **消えるものと残るものを両方書く**（6.3 の破壊的操作の作法）。片方だけ書くと、
 * 利用者は書かれていないほうを自分の期待で埋める——「コメントも消える」と読んで
 * ためらうか、「トークンは残る」と読んで止め損ねるかのどちらかになる。
 */
const deleteMessage = computed(() => {
  const a = deleteTarget.value
  if (!a) return ''
  return (
    `「${a.display_name}」を削除します。この操作は取り消せません。\n` +
    '消えるもの：登録と、発行済みのトークン。\n' +
    '残るもの：このエージェントが書いたコメント（書き手は「削除されたエージェント」になります）と、監査ログ。'
  )
})

async function deleteAgent() {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  actionError.value = null
  try {
    await meApi.deleteAgent(target.id)
    deleteTarget.value = null
    notice.value = `✓ 「${target.display_name}」を削除しました`
    await load()
  } catch (e: unknown) {
    actionError.value = asApiError(e)
    notice.value = ''
    deleteTarget.value = null
  } finally {
    deleting.value = false
  }
}

// ── `[⋯]` の項目（5.8.2）─────────────────────────────────────

function menuItems(agent: MyAgent): ActionItem[] {
  return [
    {
      key: 'reissue',
      label: 'トークンを再発行',
      disabled: !agent.is_active,
      reason: '無効なエージェントには発行できません',
    },
    {
      key: 'revoke',
      label: 'トークンを失効',
      danger: true,
      disabled: agent.token === null,
      reason: '有効なトークンがありません',
    },
    { key: 'edit', label: '編集' },
    agent.is_active
      ? { key: 'deactivate', label: '無効化', danger: true }
      : { key: 'activate', label: '有効化' },
    // **削除は下段に置く**（5.8.2）。5.6 が人について「無効化を既定の導線にし、
    // 削除は `⋯` の下段」と定めているのと同じ形である。
    { key: 'delete', label: '削除', danger: true, separated: true },
  ]
}

function onMenuSelect(agent: MyAgent, key: string) {
  if (key === 'reissue') openIssue(agent)
  else if (key === 'revoke') revokeTarget.value = agent
  else if (key === 'edit') openEdit(agent)
  else if (key === 'deactivate') deactivateTarget.value = agent
  else if (key === 'activate') void setActive(agent, true)
  else if (key === 'delete') deleteTarget.value = agent
}

// ── 小さな助け ──────────────────────────────────────────────

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: '通信に失敗しました' })
}

/**
 * 副題の `クライアント ・ プロジェクト ・ モデル名`。
 *
 * **モデル名は無ければ項目ごと落とす**（`—` を置かない。5.8.2）。
 * 名前が無いこと自体は異常ではない。
 */
function subtitle(agent: MyAgent): string {
  const parts = [clientKindLabel(clientKinds.value, agent.client_kind), agent.project.name]
  if (agent.model_name) parts.push(agent.model_name)
  if (agent.model_version) parts.push(agent.model_version)
  return parts.join(' ・ ')
}
</script>

<template>
  <div class="page">
    <PageHeader title="自分の設定" />

    <div class="page-body">
      <MeTabs current="agents" />

      <section class="block">
        <div class="block-head">
          <h2 class="block-title">エージェント</h2>
          <button type="button" class="primary" :disabled="loading" @click="openCreate">
            + 登録
          </button>
        </div>

        <!-- **画面に出す日本語は1行に収める**（GuiDesign.md 6.6）。
             HTML は改行を半角空白にするので、途中で折ると全角のあいだに空きが出る -->
        <p class="hint">
          ⓘ 自分の端末で動くクライアント（Claude Code / VS Code）を登録し、その資格情報を発行します。エージェントはあなたの権限の範囲で動きます。
        </p>

        <!-- 結果は操作した場所に出す（6.4）。登録・発行・失効・無効化で共通 -->
        <p v-if="actionError" class="alert" role="alert">{{ actionError.message }}</p>
        <p v-else-if="notice" class="ok" role="status">{{ notice }}</p>

        <p v-if="loadError" class="alert" role="alert">
          {{ loadError.message }}
          <button type="button" class="secondary" @click="load">再試行</button>
        </p>

        <div v-else-if="loading" class="cards" aria-busy="true">
          <div v-for="n in 2" :key="n" class="card skeleton-card">
            <span class="skeleton"></span>
            <span class="skeleton short"></span>
          </div>
        </div>

        <EmptyState
          v-else-if="items.length === 0"
          title="エージェントはまだ登録されていません"
          description="自分の端末で動くクライアントを登録すると、そのための資格情報を発行できます。"
        />

        <template v-else>
          <div class="cards">
            <article
              v-for="a in visibleItems"
              :key="a.id"
              class="card"
              :class="{ inactive: !a.is_active }"
            >
              <div class="card-head">
                <!-- **エージェントなので角丸四角**（8.4.2）。ここで初めて描かれる -->
                <Avatar :name="a.display_name" kind="agent" :size="24" />
                <h3 class="agent-name">{{ a.display_name }}</h3>
                <!-- 状態を色だけで示さない（9.2） -->
                <span v-if="!a.is_active" class="state-off">無効</span>
                <UserActionsMenu
                  :items="menuItems(a)"
                  :label="`${a.display_name} の操作メニュー`"
                  compact
                  @select="(k) => onMenuSelect(a, k)"
                />
              </div>

              <p class="agent-meta">{{ subtitle(a) }}</p>
              <p class="agent-meta">登録 {{ formatDateTime(a.created_at) }}</p>
              <!-- **環境変数名を出す**（手順28a）。ここに出さないと、登録のときに
                   決めた名前を後から確かめる場所が無い。**`export` 行そのものと
                   接続設定は手順28b** -->
              <p class="agent-meta">
                環境変数 <code class="env-name">{{ a.token_env_name }}</code>
              </p>

              <div class="token-box">
                <template v-if="a.token">
                  <div class="token-head">
                    <code class="prefix">{{ a.token.token_prefix }}</code>
                    <span v-if="a.token.status === 'active'">● 有効</span>
                    <span v-else class="expired">期限切れ</span>
                  </div>
                  <p class="token-meta">
                    発行 {{ formatDateTime(a.token.issued_at) }} ・ 最終利用
                    {{ a.token.last_used_at ? formatDateTime(a.token.last_used_at) : '—' }}
                  </p>
                  <p class="token-meta">
                    有効期限 {{ a.token.expires_at ? formatDate(a.token.expires_at) : '無期限' }}
                  </p>
                </template>
                <div v-else class="token-head">
                  <span class="token-label">トークン</span>
                  <span class="none">未発行</span>
                  <!-- **未発行のときだけ主ボタンで出す**（5.8.2）。登録直後の行は
                       必ずここに来るので、次の一歩を `[⋯]` に隠さない -->
                  <button
                    type="button"
                    class="primary issue-button"
                    :disabled="!a.is_active"
                    @click="openIssue(a)"
                  >
                    トークンを発行
                  </button>
                </div>
              </div>
            </article>
          </div>

          <!-- **無効なものは既定で畳む**（5.8.2）。0件ならこの行ごと出さない -->
          <label v-if="inactiveCount > 0" class="show-inactive">
            <input v-model="showInactive" type="checkbox" />
            <span>無効にしたエージェントも表示（{{ inactiveCount }}件）</span>
          </label>
        </template>
      </section>
    </div>

    <!-- ── 登録・編集（5.8.2）──────────────────────────────── -->
    <AgentFormModal
      v-if="showForm"
      :agent="formTarget"
      :busy="formBusy"
      :error="formError"
      @submit="submitForm"
      @close="showForm = false"
    />

    <!-- ── 発行モーダル（5.8.2）────────────────────────────── -->
    <Modal
      v-if="issueTarget"
      title="エージェント用トークンを発行"
      @close="issueTarget = null"
    >
      <form id="issue-agent-token" class="issue-form" @submit.prevent="issueToken">
        <p class="target">
          {{ issueTarget.display_name }}（{{ issueTarget.project.name }}）
        </p>

        <fieldset class="field choices">
          <legend class="label">有効期限</legend>
          <div class="choices-row">
            <label v-for="d in EXPIRY_CHOICES" :key="d">
              <input
                type="radio"
                name="expires_in_days"
                :value="d"
                :checked="newExpiresInDays === d"
                :disabled="issuing"
                @change="newExpiresInDays = d"
              />
              <span>{{ d }}日</span>
            </label>
          </div>
          <span v-if="issueError?.detailFor('expires_in_days')" class="detail">
            {{ issueError.detailFor('expires_in_days')?.message }}
          </span>
          <span v-else class="hint">{{ newExpiryDate }} まで有効です。</span>
        </fieldset>

        <!-- **追加の権限は `doc.edit` の1件だけ**（4.5.3 の許可リスト。手順26a）。
             既定は外す——`Design.md` 6.5 が「載せるかはそのエージェントが誰に
             付いているかで決まる」と定めており、押さなければ従来どおりの8件になる。
             **権限キーを画面に出さない**（24b で決めた形。発行結果も日本語で出す） -->
        <fieldset class="field">
          <legend class="label">追加の権限</legend>
          <label class="check">
            <input v-model="allowDocEdit" type="checkbox" :disabled="issuing" />
            <span>プロジェクト文書の編集を許す</span>
          </label>
          <span class="hint">憲章を書き換えられるようになります。</span>
        </fieldset>

        <!-- **再発行が既存を暗黙に失効させることを、押す前に出す**（4.5.3）。
             利用者は「再発行した」としか認識しないため、書かないと動いていた
             端末が黙って 401 になる -->
        <p v-if="issueTarget.token" class="warn">
          ⚠ いま有効なトークン（{{ issueTarget.token.token_prefix }}）は失効します。
        </p>

        <p v-if="issueError && !issueError.detailFor('expires_in_days')" class="alert" role="alert">
          {{ issueError.message }}
        </p>
      </form>

      <template #footer>
        <button type="button" class="secondary" :disabled="issuing" @click="issueTarget = null">
          キャンセル
        </button>
        <button type="submit" form="issue-agent-token" class="primary" :disabled="issuing">
          {{ issuing ? '発行中…' : '発行' }}
        </button>
      </template>
    </Modal>

    <!-- ── 削除の確認（4.5.4。手順26a）───────────────────── -->
    <ConfirmDialog
      v-if="deleteTarget"
      title="エージェントを削除"
      :message="deleteMessage"
      confirm-label="削除する"
      danger
      :busy="deleting"
      @confirm="deleteAgent"
      @cancel="deleteTarget = null"
    />

    <!-- ── 1回だけの表示（4.5.3）──────────────────────────── -->
    <IssuedAgentTokenDialog
      v-if="issued && issuedAgent"
      :agent="issuedAgent"
      :token="issued"
      @close="issued = null"
    />

    <!-- ── 失効の確認（6.3）──────────────────────────────── -->
    <ConfirmDialog
      v-if="revokeTarget"
      title="トークンを失効"
      :message="revokeMessage"
      confirm-label="失効させる"
      danger
      :busy="revoking"
      @confirm="revokeToken"
      @cancel="revokeTarget = null"
    />

    <!-- ── 無効化の確認（6.3）────────────────────────────── -->
    <ConfirmDialog
      v-if="deactivateTarget"
      title="エージェントを無効化"
      :message="deactivateMessage"
      confirm-label="無効化する"
      danger
      :busy="togglingActive"
      @confirm="setActive(deactivateTarget, false)"
      @cancel="deactivateTarget = null"
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

.block {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
  max-width: 880px;
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

/* ── カード（5.8.2）───────────────────────────────────────── */

.cards {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
}

/* **クラス名は具体的にする**（6.6）。`.card` は画面をまたいで衝突しうるが、
   `scoped` の外へ漏れないうえ、この画面には1種類しかカードが無い */
.card {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

/* **無効な行は背面へ下げる**（5.6 と同じ）。文字を1段落とすだけで、
   独自の色を作らない（8.3 の12段スケールに「彩度だけを下げる段」が無い） */
.card.inactive {
  color: var(--pb-text-muted);
}

.card-head {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}

.agent-name {
  flex: 1;
  min-width: 0;
  font-size: 14px;
  font-weight: 600;
  word-break: break-word;
}

.state-off {
  flex: none;
  font-size: 13px;
}

.agent-meta {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
  word-break: break-word;
}

/* 接続設定が読む環境変数の名前（4.5.1 の token_env_name）。
   **未設定なら id へ倒れた形が出る**ので、長くなっても折り返させる */
.env-name {
  font-size: 12px;
  overflow-wrap: anywhere;
}

/* ── トークンの箱 ─────────────────────────────────────────── */

.token-box {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  margin-top: var(--pb-space-2);
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
}

.token-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-2) var(--pb-space-3);
  font-size: 13px;
}

.token-label {
  color: var(--pb-text-muted);
}

.prefix {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.none,
.expired {
  color: var(--pb-text-muted);
}

/* 主ボタンは右端へ寄せる。折り返しても行の頭に来ない */
.issue-button {
  margin-left: auto;
}

.token-meta {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.show-inactive {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-2);
  color: var(--pb-text-muted);
  cursor: pointer;
  font-size: 13px;
}

.show-inactive input {
  margin: 0;
}

/* ── 読み込み中 ───────────────────────────────────────────── */

.skeleton-card {
  gap: var(--pb-space-2);
}

.skeleton {
  display: block;
  width: 100%;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.skeleton.short {
  width: 40%;
}

/* ── 発行モーダル ─────────────────────────────────────────── */

.issue-form {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
}

.target {
  margin: 0;
  font-weight: 600;
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

/* **`fieldset` の既定の枠と余白は消す。** 消さないと、この欄だけが枠線の箱に
   なり、隣の「有効期限」と別の種類の入力に見える（手順26a でスクリーンショットを
   見て気づいた。自動検証17件は全 PASS だった） */
fieldset.field {
  padding: 0;
  border: 0;
  margin: 0;
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ラジオを横1行に並べる（5.8.1 と同じ）。枠と余白の打ち消しは `fieldset.field` */

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

.choices input[type='radio'] {
  width: auto;
  height: auto;
  margin: 0;
}

/* 追加の権限のチェックボックス（5.8.2。手順26a）。**ラジオと同じ組み方**にする
   ——`gap` を置かないと記号と文字がくっつき、押せる範囲も読み取りにくくなる */
.check {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  cursor: pointer;
}

.check input[type='checkbox'] {
  width: auto;
  height: auto;
  margin: 0;
}

/* warning は「面」で表す（8.4.1）。文字色はベースのまま */
.warn {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border-left: 3px solid var(--pb-warning);
  background: var(--pb-warning-bg);
  color: var(--pb-text);
  font-size: 13px;
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

/* ── 結果（6.4）───────────────────────────────────────────── */

.alert {
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

.ok {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
