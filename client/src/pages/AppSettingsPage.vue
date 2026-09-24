<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * アプリケーション設定 `/admin/settings`（`GuiDesign.md` 5.12）。
 *
 * サーバ全体の設定を、アドミニストレータが確認・変更する。必要権限は
 * `system.settings`。設計の正本は `Design.md` 10.3（設定の3層）と
 * `ApiDesign.md` 11章。
 *
 * **出どころの札がこの画面の中心である。** 出どころが見えないと、設定の誤りが
 * 不具合として読まれる——`cookie_secure` を誤ると全員がログインできなくなるが、
 * 「いまの値がどこから来たか」が出ていなければ、利用者はログインの不具合を疑う。
 *
 * **同じ画面が単一ノードと K8s の両方を扱えるのは、この札があるからである。**
 * 単一ノードでは `[DB]` と `[既定]` の項目を編集でき、K8s では ConfigMap /
 * Secret で固定された項目が読み取り専用で並ぶ。
 */
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../api/client'
import * as settingsApi from '../api/settings'
import { usePendingStore } from '../stores/pending'
import type { Setting, SettingSource } from '../api/settings'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import PageHeader from '../components/PageHeader.vue'
import { formatDateTime } from '../lib/datetime'
import DatabaseTab from './DatabaseTab.vue'
import TlsCertificatesTab from './TlsCertificatesTab.vue'

/**
 * タブ（`GuiDesign.md` 5.12）。**証明書が設定の一覧に収まらないので分けた**
 * ——1件が複数行の情報を持ち、登録と削除という操作を伴う。
 *
 * **DB（5.12.2）は設定ではなく状態である。** 変更の操作を持たない。
 */
type Tab = 'general' | 'tls' | 'database'
const tab = ref<Tab>('general')

const items = ref<Setting[]>([])
const configFilePath = ref<string | null>(null)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

/**
 * 編集中の値。**キーから文字列への対応で持つ。**
 *
 * API が値を型によらず文字列で返すので（11.1）、入力欄も文字列で扱う。
 * `null` は「既定に戻す」を押したことを表す。
 */
const draft = ref<Record<string, string | null>>({})

/** 表の直上に出す結果（`GuiDesign.md` 6.4）。保存の成否で使い回す */
const notice = ref('')
const actionError = ref<ApiError | null>(null)
const saving = ref(false)

/** `cookie_secure` を有効にする操作だけ確認を挟む（5.12） */
const confirmingLockout = ref(false)

/** 出どころの札（`GuiDesign.md` 5.12）。並びは優先順と同じ */
const sourceLabelSources: Record<SettingSource, string> = {
  secret_file: '秘密ファイル',
  config_file: '設定ファイル',
  env: '環境変数',
  database: 'DB',
  default: '既定',
}

function sourceLabel(source: SettingSource): string {
  return uiText(sourceLabelSources[source])
}

/**
 * 未確認の設定変更は**アプリの枠のフッターが持つ**。
 *
 * **このページの中に帯を置かない。** 置くと、**設定を変えたあと別の画面へ
 * 移ったとき、確認ボタンがどこにも無い状態で期限が過ぎる。** 保存したときに
 * フッターへ知らせる。
 */
const pendingStore = usePendingStore()

async function load() {
  loading.value = true
  loadError.value = null
  try {
    const res = await settingsApi.getSettings()
    items.value = res.items
    configFilePath.value = res.config_file_path
    // **フッターへ知らせる**。ここで読み捨てると、保存直後に帯が出ない。
    void pendingStore.refresh()
    draft.value = {}
  } catch (e: unknown) {
    loadError.value = asApiError(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

/**
 * 2つの節に分ける（5.12）。**分ける軸は `restart_required`** で、利用者にとっての
 * 違いは「いま効くか、再起動が要るか」だけである。
 *
 * **`layer` の 1 / 2 / 3 という番号は画面に出さない。** あれは設計の語彙であって、
 * 設定を変える人が知る必要のある区別ではない（設計原則4）。
 */
const runtimeItems = computed(() =>
  items.value.filter((s) => !s.restart_required && !TLS_TAB_KEYS.has(s.key)),
)
const bootItems = computed(() =>
  items.value.filter((s) => s.restart_required && !TLS_TAB_KEYS.has(s.key)),
)

/**
 * **TLS 証明書タブが扱う設定**。
 *
 * 一般タブには出さない——**関係するものが2つのタブに分かれているのは筋が悪い。**
 * `secret_key` も TLS のためだけの設定なので、あちらで扱う。
 */
const TLS_TAB_KEYS = new Set(['tls_enabled', 'secret_key'])

/** いまの入力値（未編集なら実効値） */
function currentValue(s: Setting): string {
  const d = draft.value[s.key]
  if (d !== undefined) return d ?? (s.default_value ?? '')
  return s.value ?? ''
}

/** サーバの設定キーを正本に、アプリ所有の表示名と説明を現在の言語へ直す。 */
function settingName(s: Setting): string {
  const settingNameSources: Record<string, string> = {
    bind: '待受アドレス',
    secret_key: '秘密の暗号鍵',
    tls_enabled: 'TLS で待ち受ける',
    log_format: 'ログ形式',
    log_level: 'ログレベル',
    health_show_version: 'ヘルスチェックにバージョンを含める',
    cookie_secure: 'Cookie に Secure を付ける',
  }
  return settingNameSources[s.key] ? uiText(settingNameSources[s.key]) : s.display_name
}

function settingDescription(s: Setting): string {
  const settingDescriptionSources: Record<string, string> = {
    bind: 'HTTP を待ち受けるアドレスとポート。切り替えは即時で、期限内に確認しないと元へ戻ります。コンテナで動かしている場合は、公開側の設定（compose の ports や Service の targetPort）も合わせて変えてください',
    secret_key: 'TLS の秘密鍵を暗号化するための鍵。32バイトを base64 で与えます。証明書を登録しないなら不要です',
    tls_enabled: '有効にすると HTTPS で待ち受けます。証明書の登録が別途必要です。切り替えは即時で、期限内に確認しないと元へ戻ります',
    log_format: '標準出力へ書くログの形式。text は開発時に人が読むためのものです',
    log_level: '記録するログの最低レベル。debug では /healthcheck のアクセスログも出ます',
    health_show_version: 'GET /healthcheck の応答にバージョンを入れます。未認証の呼び出し元への情報開示になるため既定は無効です',
    cookie_secure: 'HTTPS で公開する環境では有効にします',
  }
  return settingDescriptionSources[s.key] ? uiText(settingDescriptionSources[s.key]) : s.description
}

/** 編集されたか。**`null`（既定に戻す）も変更として数える** */
function isDirty(s: Setting): boolean {
  const d = draft.value[s.key]
  if (d === undefined) return false
  if (d === null) return s.source === 'database'
  return d !== s.value
}

const changed = computed(() => items.value.filter(isDirty))
const hasChanges = computed(() => changed.value.length > 0)

/**
 * `cookie_secure` を有効にする変更が含まれるか。
 *
 * **確認を挟む基準は「その変更で、この画面へ戻れなくなるか」である**（5.12）。
 * 他の設定は誤っても画面から直せる。
 */
const willLockOut = computed(() =>
  changed.value.some((s) => s.key === 'cookie_secure' && draft.value[s.key] === 'true'),
)

function setValue(s: Setting, value: string) {
  notice.value = ''
  actionError.value = null
  draft.value = { ...draft.value, [s.key]: value }
}

/** 既定に戻す。**`[DB]` の項目にだけ出す**——`[既定]` は押しても何も変わらない */
function resetToDefault(s: Setting) {
  notice.value = ''
  actionError.value = null
  draft.value = { ...draft.value, [s.key]: null }
}

function discard() {
  draft.value = {}
  notice.value = ''
  actionError.value = null
}

function requestSave() {
  if (willLockOut.value) {
    confirmingLockout.value = true
    return
  }
  void save()
}

async function save() {
  confirmingLockout.value = false
  saving.value = true
  notice.value = ''
  actionError.value = null
  try {
    const payload = changed.value.map((s) => ({
      key: s.key,
      value: draft.value[s.key] ?? null,
    }))
    const res = await settingsApi.putSettings(payload)
    items.value = res.items
    configFilePath.value = res.config_file_path
    draft.value = {}
    // **確認が要る設定を変えたなら、押さないと戻ることを書く**。
    // 件数だけでは、**期限があることが伝わらない。**
    notice.value = res.pending_confirmation
      ? uiText("{value0}件の設定を保存しました。", { value0: payload.length }) +
        uiText("画面下の「アクセスできました」を押してください。") +
        uiText("押さないまま300秒が過ぎると、元の設定へ戻ります。")
      : uiText("{value0}件の設定を保存しました。", { value0: payload.length })
  } catch (e: unknown) {
    actionError.value = asApiError(e)
  } finally {
    saving.value = false
  }
}

/**
 * 編集できない理由（5.12）。**環境変数名かキーを必ず添える**——変更できない値に
 * ついて「ではどこで変えるのか」を画面が答えられないと、行き止まりになる。
 */
function pinnedReason(s: Setting): string {
  if (s.restart_required && s.layer === 1) {
    return uiText("起動前に要る設定です。{value0} か設定ファイルの {value1} で与えてください", { value0: s.env_key, value1: s.config_file_key })
  }
  switch (s.source) {
    case 'secret_file':
      return uiText("{value0}_FILE が指すファイルで固定されています", { value0: s.env_key })
    case 'config_file':
      return uiText("設定ファイルの {value0} で固定されています", { value0: s.config_file_key })
    case 'env':
      return uiText("{value0} で固定されています", { value0: s.env_key })
    default:
      return ''
  }
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
}
</script>

<template>
  <div class="page">
    <PageHeader :title="$ui('アプリケーション設定')" />

    <div class="tabs" role="tablist">
      <button
        type="button"
        role="tab"
        :aria-selected="tab === 'general'"
        :class="{ on: tab === 'general' }"
        @click="tab = 'general'"
      > {{ $ui('一般') }} </button>
      <button
        type="button"
        role="tab"
        :aria-selected="tab === 'tls'"
        :class="{ on: tab === 'tls' }"
        @click="tab = 'tls'"
      > {{ $ui('TLS 証明書') }} </button>
      <button
        type="button"
        role="tab"
        :aria-selected="tab === 'database'"
        :class="{ on: tab === 'database' }"
        @click="tab = 'database'"
      >
        DB
      </button>
    </div>

    <div class="page-body">
      <div class="page-inner">
    <TlsCertificatesTab v-if="tab === 'tls'" />
    <DatabaseTab v-else-if="tab === 'database'" />

    <template v-else>
    <p v-if="loading" class="muted">{{ $ui('読み込み中…') }}</p>
    <p v-else-if="loadError" class="error" role="alert">{{ loadError.message }}</p>

    <template v-else>
      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="actionError" class="error" role="alert">{{ actionError.message }}</p>

      <p v-if="configFilePath" class="muted file"> {{ $ui('設定ファイル:') }} <code>{{ configFilePath }}</code>
      </p>

      <section v-for="group in [
        { title: $ui('実行時の設定'), hint: $ui('変更は次のリクエストから効きます'), list: runtimeItems },
        { title: $ui('起動時の設定'), hint: $ui('変更には再起動が要ります'), list: bootItems },
      ]" :key="group.title" class="group">
        <h2 class="section-title">{{ group.title }}</h2>
        <p class="muted hint">{{ group.hint }}</p>

        <div v-for="s in group.list" :key="s.key" class="row">
          <div class="head">
            <span class="name">{{ settingName(s) }}</span>
            <span class="badge" :class="`src-${s.source}`">{{ sourceLabel(s.source) }}</span>
          </div>

          <!-- 秘密は値を出さない（11.1 が value を返さない） -->
          <p v-if="s.secret" class="value masked">●●●●●●●●</p>

          <!-- 編集できるもの -->
          <template v-else-if="s.editable">
            <label class="control">
              <select
                v-if="s.value_type === 'enum'"
                :value="currentValue(s)"
                @change="setValue(s, ($event.target as HTMLSelectElement).value)"
              >
                <option v-for="a in s.allowed ?? []" :key="a" :value="a">{{ a }}</option>
              </select>
              <span v-else-if="s.value_type === 'bool'" class="bool">
                <input
                  type="checkbox"
                  :checked="currentValue(s) === 'true'"
                  @change="
                    setValue(s, ($event.target as HTMLInputElement).checked ? 'true' : 'false')
                  "
                />
                <span>{{ currentValue(s) === 'true' ? $ui("有効") : $ui("無効") }}</span>
              </span>
              <input
                v-else
                type="text"
                :value="currentValue(s)"
                @input="setValue(s, ($event.target as HTMLInputElement).value)"
              />
            </label>
          </template>

          <!-- 編集できないもの。理由と外し方を出す -->
          <template v-else>
            <p class="value">{{ s.value }}</p>
            <p class="muted reason">{{ pinnedReason(s) }}</p>
          </template>

          <p class="desc">{{ settingDescription(s) }}</p>

          <p v-if="s.key === 'cookie_secure' && s.editable" class="warn"> {{ $ui('⚠ http で提供している場合、有効にするとログインできなくなります') }} </p>

          <div class="foot">
            <span v-if="s.updated_at" class="muted">
              {{ formatDateTime(s.updated_at) }}
              <template v-if="s.updated_by">{{ s.updated_by.display_name }} {{ $ui('が変更') }}</template>
            </span>
            <!-- 既定に戻すは [DB] にだけ出す。[既定] は押しても何も変わらない -->
            <button
              v-if="s.source === 'database' && s.editable"
              type="button"
              class="link"
              @click="resetToDefault(s)"
            > {{ $ui('既定に戻す') }} </button>
            <span v-if="isDirty(s)" class="dirty">{{ $ui('変更あり') }}</span>
          </div>
        </div>
      </section>

      <!-- 保存は1つだけ。変更が無いときは押せないようにする（設計原則4） -->
      <div class="actions">
        <button v-if="hasChanges" type="button" class="link" @click="discard">{{ $ui('破棄') }}</button>
        <button type="button" class="primary" :disabled="!hasChanges || saving" @click="requestSave">
          {{ saving ? $ui("保存中…") : $ui("変更を保存") }}
        </button>
      </div>
    </template>
    </template>
      </div>
    </div>

    <Teleport to="body">
      <ConfirmDialog
        v-if="confirmingLockout"
        :title="$ui('Cookie に Secure を付けますか？')"
        :message="$ui('http で提供している場合、この変更でログインできなくなります。復旧するには環境変数 PB_COOKIE_SECURE=false を与えて起動し直す必要があります。')"
        :confirm-label="$ui('有効にする')"
        danger
        :busy="saving"
        @confirm="save"
        @cancel="confirmingLockout = false"
      />
    </Teleport>
  </div>
</template>

<style scoped>

/**
 * **AppShell の .content は overflow:hidden なので、各ページが自前のスクロール枠を
 * 持つ約束である**（UsersPage と同じ形）。置き忘れると画面の下が切れて
 * スクロールもできない。
 */
.page {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
}
/**
 * **スクロールする箱は全幅にする。** ここに max-width を付けると、
 * スクロールバーがコンテンツペインの右端ではなく**内側の右端に出る**
 * （画面の真ん中にスクロールバーが出て見える）。
 * 幅の制限は内側の .page-inner が持つ。
 */
.page-body {
  flex: 1;
  overflow: auto;
  min-height: 0;
}
.page-inner {
  padding: var(--pb-space-4);
  max-width: 44rem;
}
.tabs {
  display: flex;
  flex: none;
  gap: var(--pb-space-1);
  border-bottom: 1px solid var(--pb-border);
  padding-inline: var(--pb-space-4);
}
.tabs button {
  background: none;
  border: none;
  border-bottom: 2px solid transparent;
  padding: var(--pb-space-2) var(--pb-space-3);
  cursor: pointer;
  color: var(--pb-text-muted);
}
.tabs button.on {
  color: var(--pb-text);
  border-bottom-color: var(--pb-accent);
  font-weight: 600;
}
.muted {
  color: var(--pb-text-muted);
}
.file {
  font-size: 12px;
  margin-bottom: var(--pb-space-3);
}
.notice {
  color: var(--pb-text);
}
.error {
  color: var(--pb-danger-text);
}
.group {
  margin-top: var(--pb-space-6);
}
.section-title {
  font-size: 14px;
  font-weight: 600;
  border-bottom: 1px solid var(--pb-border);
  padding-bottom: var(--pb-space-1);
}
.hint {
  font-size: 12px;
  margin: var(--pb-space-1) 0 var(--pb-space-3);
}
.row {
  padding: var(--pb-space-3) 0;
  border-bottom: 1px solid var(--pb-line);
}
.head {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}
.name {
  font-weight: 600;
}
.badge {
  font-size: 11px;
  padding: 0 var(--pb-space-2);
  border-radius: var(--pb-radius);
  border: 1px solid var(--pb-border);
  color: var(--pb-text-muted);
}
.src-database {
  border-color: var(--pb-accent);
  color: var(--pb-accent);
}
.src-default {
  opacity: 0.7;
}
.control {
  display: block;
  margin: var(--pb-space-2) 0;
}
.control select,
.control input[type='text'] {
  min-width: 14rem;
}
.bool {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-2);
}
.value {
  margin: var(--pb-space-2) 0;
  font-family: var(--pb-font-mono);
}
.masked {
  letter-spacing: 0.15em;
}
.desc,
.reason {
  font-size: 12px;
  margin: var(--pb-space-1) 0 0;
}
.desc {
  color: var(--pb-text-muted);
}
.warn {
  font-size: 12px;
  color: var(--pb-warning-text);
  margin: var(--pb-space-1) 0 0;
}
.foot {
  display: flex;
  align-items: center;
  gap: var(--pb-space-3);
  font-size: 12px;
  margin-top: var(--pb-space-2);
}
.dirty {
  color: var(--pb-accent);
}
.actions {
  display: flex;
  justify-content: flex-end;
  align-items: center;
  gap: var(--pb-space-3);
  margin-top: var(--pb-space-4);
}
</style>
