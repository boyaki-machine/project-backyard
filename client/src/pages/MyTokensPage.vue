<script setup lang="ts">
/**
 * アクセストークン管理 `/me/tokens`（`GuiDesign.md` 5.8.1）。
 *
 * CLI・スクリプトから API を呼ぶための Bearer トークンを、本人が発行・一覧・
 * 失効する（`ApiDesign.md` 4.4）。
 *
 * **扱うのは `api` 種別だけ。** ブラウザのセッションは出ない——本人が自分の
 * セッションを見る・切る画面を持たないと決めている（5.8）。
 *
 * **結果は表の直上に出す**（6.4）。発行と失効で同じ欄を使い回す（5.6 の一覧と
 * 同じ形）。トーストは使わない。
 */
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../api/client'
import * as meApi from '../api/me'
import type { AccessToken, IssuedAccessToken } from '../api/me'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import IssuedTokenDialog from '../components/IssuedTokenDialog.vue'
import MeTabs from '../components/MeTabs.vue'
import Modal from '../components/Modal.vue'
import PageHeader from '../components/PageHeader.vue'
import { formatDate, formatDateTime } from '../lib/datetime'

/** 1人あたりの上限（`ApiDesign.md` 4.4.2）。サーバ側 maxAPITokensPerActor と同じ値 */
const MAX_TOKENS = 5
/** 名前の上限（`ApiDesign.md` 4.4.2） */
const MAX_NAME = 100
/** 有効期限の選択肢（`GuiDesign.md` 5.8.1 のワイヤー） */
const EXPIRY_CHOICES = [30, 90, 365] as const

const items = ref<AccessToken[]>([])
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

/**
 * 表の直上に出す結果（6.4）。発行と失効で使い回す。
 *
 * **入力を監視して消さない。** 一覧を読み直すとこの欄も書き換わるので、
 * 発火の順序に依らないよう「次の操作を始めたら消す」だけにしてある。
 */
const notice = ref('')
const actionError = ref<ApiError | null>(null)

const atLimit = computed(() => items.value.length >= MAX_TOKENS)

async function load() {
  loading.value = true
  loadError.value = null
  try {
    items.value = (await meApi.listTokens()).items
  } catch (e: unknown) {
    loadError.value = asApiError(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ── 発行（`ApiDesign.md` 4.4.2）──────────────────────────────

const issuing = ref(false)
const showIssueModal = ref(false)
const newName = ref('')
const newExpiresInDays = ref<number>(90)
const issueError = ref<ApiError | null>(null)

/** 1回だけ出す発行結果。閉じると二度と出せない（4.4.2） */
const issued = ref<IssuedAccessToken | null>(null)

/**
 * 選んだ日数から算出した期日。
 *
 * **「90日」ではなくカレンダーと突き合わせられる形も出す**（5.8.1）。
 * サーバも「発行時刻 + N 日」で計算するので、押すまでの間に日付が変わらない
 * かぎり同じ値になる。
 */
const newExpiryDate = computed(() => {
  const d = new Date()
  d.setDate(d.getDate() + newExpiresInDays.value)
  return formatDate(d.toISOString())
})

function openIssueModal() {
  if (atLimit.value) return
  newName.value = ''
  newExpiresInDays.value = 90
  issueError.value = null
  showIssueModal.value = true
}

function issueDetail(field: string) {
  return issueError.value?.detailFor(field)
}

const canIssue = computed(() => newName.value.trim() !== '' && !issuing.value)

async function issueToken() {
  if (!canIssue.value) return
  issuing.value = true
  issueError.value = null
  try {
    // **スコープは送らない**（`ApiDesign.md` 4.4.2）。Phase 1 は常に
    // 「絞り込みなし」で発行する。選択UIはトークンの使われ方が決まってから。
    const token = await meApi.createToken({
      name: newName.value.trim(),
      expires_in_days: newExpiresInDays.value,
    })
    showIssueModal.value = false
    // **先に平文を出す。** 一覧の読み直しが失敗しても、二度と出せない値を
    // 落とさないためである。
    issued.value = token
    notice.value = `✓ アクセストークン「${token.name}」を発行しました`
    actionError.value = null
    await load()
  } catch (e: unknown) {
    issueError.value = asApiError(e)
  } finally {
    issuing.value = false
  }
}

// ── 失効（`ApiDesign.md` 4.4.3）──────────────────────────────

const revoking = ref(false)
/** 確認ダイアログの対象。null なら閉じている（6.3） */
const revokeTarget = ref<AccessToken | null>(null)

const revokeMessage = computed(() => {
  const t = revokeTarget.value
  if (!t) return ''
  return (
    `「${t.name}」を失効させます。復元はできません。\n` +
    'このトークンを使っている CLI やスクリプトは、次のリクエストから 401 になります。'
  )
})

async function revoke() {
  const target = revokeTarget.value
  if (!target || revoking.value) return
  revoking.value = true
  actionError.value = null
  try {
    await meApi.revokeToken(target.id)
    revokeTarget.value = null
    notice.value = `✓ アクセストークン「${target.name}」を失効させました`
    await load()
  } catch (e: unknown) {
    actionError.value = asApiError(e)
    notice.value = ''
    revokeTarget.value = null
  } finally {
    revoking.value = false
  }
}

// ── 小さな助け ──────────────────────────────────────────────

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: '通信に失敗しました' })
}
</script>

<template>
  <div class="page">
    <PageHeader title="自分の設定" />

    <div class="page-body">
      <MeTabs current="tokens" />

      <section class="block">
        <div class="block-head">
          <h2 class="block-title">アクセストークン</h2>
          <button type="button" class="primary" :disabled="atLimit || loading" @click="openIssueModal">
            + 発行
          </button>
        </div>

        <p class="hint">
          ⓘ CLI やスクリプトから API を呼ぶためのトークンです。Authorization: Bearer &lt;トークン&gt; で送ります。
        </p>
        <!-- 押せない理由をその場に出す（5.6.2 と同じ。黙って消さない） -->
        <p v-if="atLimit" class="hint">
          {{ MAX_TOKENS }}本まで発行できます。新しく発行するには、いずれかを失効させてください。
        </p>

        <!-- 結果は操作した場所に出す（6.4）。発行と失効で同じ欄を使い回す -->
        <p v-if="actionError" class="alert" role="alert">{{ actionError.message }}</p>
        <p v-else-if="notice" class="ok" role="status">{{ notice }}</p>

        <p v-if="loadError" class="alert" role="alert">
          {{ loadError.message }}
          <button type="button" class="secondary" @click="load">再試行</button>
        </p>

        <EmptyState
          v-else-if="!loading && items.length === 0"
          title="アクセストークンはまだありません"
          description="CLI やスクリプトから API を呼ぶときに発行します。"
        />

        <div v-else class="table-scroll">
          <table class="tokens">
            <thead>
              <tr>
                <th class="name">名前</th>
                <th class="prefix">接頭辞</th>
                <th class="when">発行</th>
                <th class="when">最終利用</th>
                <th class="when">有効期限</th>
                <th class="status">状態</th>
                <th class="actions"><span class="sr-only">操作</span></th>
              </tr>
            </thead>
            <tbody v-if="loading" aria-busy="true">
              <tr v-for="n in 3" :key="n" class="skeleton-row">
                <td v-for="c in 7" :key="c"><span class="skeleton"></span></td>
              </tr>
            </tbody>
            <tbody v-else>
              <tr v-for="t in items" :key="t.id">
                <td class="name">{{ t.name }}</td>
                <td class="prefix"><code>{{ t.token_prefix }}</code></td>
                <td class="when">{{ formatDateTime(t.issued_at) }}</td>
                <td class="when">{{ t.last_used_at ? formatDateTime(t.last_used_at) : '—' }}</td>
                <td class="when">{{ t.expires_at ? formatDate(t.expires_at) : '無期限' }}</td>
                <!-- 状態を色だけで示さない（9.2）ので記号か文言を必ず添える -->
                <td class="status">
                  <span v-if="t.status === 'active'">● 有効</span>
                  <span v-else class="expired">期限切れ</span>
                </td>
                <td class="actions">
                  <button type="button" class="secondary" @click="revokeTarget = t">失効</button>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>

    <!-- ── 発行モーダル（5.8.1）──────────────────────────────── -->
    <Modal v-if="showIssueModal" title="アクセストークンを発行" @close="showIssueModal = false">
      <form id="issue-token" class="issue-form" @submit.prevent="issueToken">
        <label class="field">
          <span class="label">名前 <span class="required">*</span></span>
          <input
            v-model="newName"
            type="text"
            name="name"
            :maxlength="MAX_NAME"
            placeholder="CLI (MacBook)"
            :aria-invalid="issueDetail('name') !== undefined"
            :disabled="issuing"
          />
          <span v-if="issueDetail('name')" class="detail">{{ issueDetail('name')?.message }}</span>
          <span v-else class="hint">どの端末・どの用途かが分かる名前を付けてください。</span>
        </label>

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
          <span v-if="issueDetail('expires_in_days')" class="detail">
            {{ issueDetail('expires_in_days')?.message }}
          </span>
          <span v-else class="hint">{{ newExpiryDate }} まで有効です。</span>
        </fieldset>

        <p v-if="issueError && !issueDetail('name') && !issueDetail('expires_in_days')"
           class="alert" role="alert">
          {{ issueError.message }}
        </p>
      </form>

      <template #footer>
        <button type="button" class="secondary" :disabled="issuing" @click="showIssueModal = false">
          キャンセル
        </button>
        <button type="submit" form="issue-token" class="primary" :disabled="!canIssue">
          {{ issuing ? '発行中…' : '発行' }}
        </button>
      </template>
    </Modal>

    <!-- ── 1回だけの表示（4.4.2）──────────────────────────────── -->
    <IssuedTokenDialog v-if="issued" :token="issued" @close="issued = null" />

    <!-- ── 失効の確認（6.3）───────────────────────────────────── -->
    <ConfirmDialog
      v-if="revokeTarget"
      title="アクセストークンを失効"
      :message="revokeMessage"
      confirm-label="失効させる"
      danger
      :busy="revoking"
      @confirm="revoke"
      @cancel="revokeTarget = null"
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

/* ── 表 ───────────────────────────────────────────────────── */

/* 狭い窓では横スクロールさせる（`GuiDesign.md` 5.6 の規則）。
   余りを1列に渡す作りにしない——その列だけ 0 まで潰れる（手順12b） */
.table-scroll {
  overflow-x: auto;
}

.tokens {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}

.tokens th,
.tokens td {
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
  text-align: left;
  white-space: nowrap;
}

.tokens th {
  color: var(--pb-text-muted);
  font-weight: 600;
}

.tokens tbody tr:last-child td {
  border-bottom: 0;
}

/* 名前だけは伸ばす。他は内容の幅で足りる */
.tokens .name {
  width: 100%;
  min-width: 160px;
  white-space: normal;
  word-break: break-word;
}

.tokens .prefix code {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.tokens .actions {
  text-align: right;
}

.expired {
  color: var(--pb-text-muted);
}

.skeleton {
  display: block;
  width: 100%;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.sr-only {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
  white-space: nowrap;
}

/* ── 発行モーダル ─────────────────────────────────────────── */
.issue-form {
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

.issue-form input[type='text'] {
  width: 100%;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

/* ラジオを横1行に並べる（5.8.1 のワイヤー）。`fieldset` の既定の枠と余白は
   消す。`.field` の `flex-direction: column` をそのまま受けると縦積みになる */
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
