<script setup lang="ts">
import { uiNumber, uiText } from '../locales/ui'
/**
 * DB タブ（`GuiDesign.md` 5.12.2）。
 *
 * **設計の正本は `ApiDesign.md` 11.10。** 読み取り専用で、変更の操作を持たない。
 *
 * **定期的に引かない。** 件数は全表の `count(*)` なので、行が増えるほど1回が
 * 重くなる。開いたときと [再読み込み] のときだけ引き、**いつの値かを横に出す。**
 *
 * **下半分はバックアップと復元である**。上半分（状態）は読み取り専用、
 * 下半分は画面を変える操作なので、**区切りで分ける。**
 */
import { computed, onMounted, ref } from 'vue'

import RestoreBackupDialog from '../components/RestoreBackupDialog.vue'
import { ApiError } from '../api/client'
import * as settingsApi from '../api/settings'
import type { DatabaseStatus, RestoreResult } from '../api/settings'
import { readBackupMeta } from '../lib/backupArchive'
import type { BackupMeta } from '../lib/backupArchive'
import { formatDateTime } from '../lib/datetime'

const status = ref<DatabaseStatus | null>(null)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

async function load() {
  loading.value = true
  loadError.value = null
  try {
    status.value = await settingsApi.getDatabaseStatus()
  } catch (e) {
    // **引き直しに失敗しても、前の値は消さない。** 取得時刻が古いまま残るので、
    // 古い値であることは読める。
    loadError.value =
      e instanceof ApiError
        ? e
        : new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
  } finally {
    loading.value = false
  }
}

onMounted(load)

/** 接続先。**Unix ソケットはディレクトリのパスなので、ポートを括弧で添える** */
const endpoint = computed(() => {
  const c = status.value?.connection
  if (!c) return ''
  if (c.host.startsWith('/')) return uiText("{value0}（ポート {value1}）", { value0: c.host, value1: c.port })
  if (c.host.includes(':')) return `[${c.host}]:${c.port}`
  return `${c.host}:${c.port}`
})

/** マイグレーション番号はファイル名と同じ4桁で出す（`0038_…sql`） */
const migration = computed(() =>
  status.value ? String(status.value.migration_version).padStart(4, '0') : '',
)

/** 稼働時間。**取得した時刻から数える**——表示している値と同じ時点にそろえる */
const uptime = computed(() => {
  const s = status.value
  if (!s) return ''
  const ms = s.fetched_at - s.server.started_at
  if (!(ms >= 0)) return ''
  const min = Math.floor(ms / 60000)
  const days = Math.floor(min / 1440)
  const hours = Math.floor((min % 1440) / 60)
  if (days > 0) return uiText("{value0}日{value1}時間", { value0: days, value1: hours })
  if (hours > 0) return uiText("{value0}時間{value1}分", { value0: hours, value1: min % 60 })
  return uiText("{value0}分", { value0: min })
})

function formatCount(n: number): string {
  return uiNumber(n)
}

// ── バックアップと復元（`ApiDesign.md` 11.11〜11.12）────────────

const backupUrl = settingsApi.backupUrl()

/**
 * 書き出し中か。**押したら終わるまで押せなくする。**
 *
 * `Content-Length` が返らないので進捗は出せない（11.11）。**押した直後に
 * 何も変わらないと、押せていないと読まれる。**
 */
const dumping = ref(false)

function startDump() {
  dumping.value = true
  // **落とし始めたかは分からない**（`<a>` の遷移は検知できない）。**大きいと
  // 時間がかかると先に書いてあるので、一定時間で戻す。**
  window.setTimeout(() => {
    dumping.value = false
  }, 4000)
}

const fileInput = ref<HTMLInputElement | null>(null)
const chosen = ref<File | null>(null)
const chosenMeta = ref<BackupMeta | null>(null)
const confirming = ref(false)
const restoring = ref(false)
const restoreError = ref<string | null>(null)
const restored = ref<RestoreResult | null>(null)

async function chooseFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0] ?? null
  chosen.value = file
  chosenMeta.value = null
  restored.value = null
  restoreError.value = null
  if (!file) return
  // **押す前に中身を読んで見せる**（5.12.2）。読めなくても誤りにしない。
  chosenMeta.value = await readBackupMeta(file)
}

function openConfirm() {
  if (!chosen.value) return
  restoreError.value = null
  confirming.value = true
}

function cancelConfirm() {
  confirming.value = false
}

async function runRestore(ownerUser: string, ownerPassword: string) {
  const file = chosen.value
  if (!file) return
  restoring.value = true
  restoreError.value = null
  try {
    const res = await settingsApi.restoreBackup(ownerUser, ownerPassword, file)
    restored.value = res
    confirming.value = false
    clearChosen()
    if (!res.session_kept) {
      // **セッションを維持できなかった**（11.12）。この画面はもう自分の権限を
      // 確かめられないので、ログイン画面へ送る。
      window.location.assign('/login')
      return
    }
    // **上半分を引き直す。** 件数も接続もすべて変わっている。
    await load()
  } catch (e) {
    restoreError.value =
      e instanceof ApiError ? e.message : uiText("取り込みに失敗しました。もう一度お試しください")
  } finally {
    restoring.value = false
  }
}

function clearChosen() {
  chosen.value = null
  chosenMeta.value = null
  if (fileInput.value) fileInput.value.value = ''
}

/** 突き合わせで食い違った表かどうか */
function isMismatched(name: string): boolean {
  return restored.value?.mismatched.includes(name) ?? false
}

/**
 * 大きさ。**1024 で割る**（`pg_size_pretty` と同じ割り方。`GuiDesign.md` 5.12.2）。
 * 1 KB 未満はバイトのまま、それ以上は小数1桁で出す。
 */
function formatBytes(n: number): string {
  const units = ['KB', 'MB', 'GB', 'TB']
  if (n < 1024) return `${n} B`
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(1)} ${units[i]}`
}
</script>

<template>
  <div class="tab">
    <div class="toolbar">
      <span v-if="status" class="muted fetched">{{ formatDateTime(status.fetched_at) }} {{ $ui('に取得') }}</span>
      <button type="button" class="secondary small" :disabled="loading" @click="load"> {{ $ui('再読み込み') }} </button>
    </div>

    <p v-if="loadError" class="error" role="alert">{{ loadError.message }}</p>
    <p v-if="loading && !status" class="muted">{{ $ui('読み込み中…') }}</p>

    <template v-if="status">
      <section class="block">
        <h3>{{ $ui('接続') }}</h3>
        <dl class="kv">
          <dt>{{ $ui('接続先') }}</dt>
          <dd><code>{{ endpoint }}</code></dd>
          <dt>{{ $ui('データベース') }}</dt>
          <dd><code>{{ status.connection.database }}</code></dd>
          <dt>{{ $ui('ユーザー') }}</dt>
          <dd><code>{{ status.connection.user }}</code></dd>
          <dt>{{ $ui('暗号化') }}</dt>
          <dd>{{ status.connection.tls ? $ui("あり（TLS）") : $ui("なし") }}</dd>
        </dl>
      </section>

      <section class="block">
        <h3>{{ $ui('サーバ') }}</h3>
        <dl class="kv">
          <dt>PostgreSQL</dt>
          <dd>{{ status.server.version }}</dd>
          <dt>{{ $ui('起動') }}</dt>
          <!-- **1行に書く。** 改行すると括弧の前に空白が入る（`GuiDesign.md` 6.7） -->
          <dd>{{ formatDateTime(status.server.started_at) }}<span v-if="uptime" class="muted nowrap">{{ $ui('（稼働') }} {{ uptime }}）</span></dd>
          <dt>{{ $ui('マイグレーション') }}</dt>
          <dd><code>{{ migration }}</code></dd>
        </dl>
      </section>

      <section class="block">
        <h3>{{ $ui('セッション') }}</h3>
        <dl class="kv">
          <dt>{{ $ui('この DB への接続') }}</dt>
          <dd>{{ formatCount(status.sessions.database) }}<span class="muted nowrap">{{ $ui('（サーバ全体の上限') }} {{ formatCount(status.server.max_connections) }}）</span></dd>
          <dt>{{ $ui('うち PB') }}</dt>
          <dd>{{ formatCount(status.sessions.pb) }}</dd>
          <!-- **「このプロセスの」と書く。** 複数のプロセスでは引くたびに別の値になりうる（5.12.2） -->
          <dt>{{ $ui('このプロセスのプール') }}</dt>
          <dd>{{ $ui('使用中') }} {{ status.pool.acquired }} {{ $ui('・ 待機') }} {{ status.pool.idle }} {{ $ui('・ 上限') }} {{ status.pool.max }}</dd>
        </dl>
      </section>

      <section class="block">
        <h3>{{ $ui('容量') }}</h3>
        <dl class="kv">
          <dt>{{ $ui('DB の大きさ') }}</dt>
          <dd>{{ formatBytes(status.size_bytes) }}</dd>
        </dl>

        <div class="table-scroll">
          <table class="tables">
            <thead>
              <tr>
                <th class="name">{{ $ui('表') }}</th>
                <th class="num">{{ $ui('件数') }}</th>
                <th class="num">{{ $ui('大きさ') }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="t in status.tables" :key="t.name">
                <td class="name">
                  <code>{{ t.name }}</code>
                  <!-- **落とさずに理由を出す**——一覧から消すと、表があることに誰も気づかない -->
                  <span v-if="t.rows === null" class="muted note">{{ $ui('読む権限がありません') }}</span>
                </td>
                <td class="num">{{ t.rows === null ? '—' : formatCount(t.rows) }}</td>
                <td class="num">{{ formatBytes(t.size_bytes) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- ── バックアップと復元（5.12.2）──────────────── -->
      <section class="block ops">
        <h3>{{ $ui('バックアップ') }}</h3>

        <p class="lead"> {{ $ui('PB 全体を1つのファイルに書き出します。利用者・エージェント・暗号鍵も含まれます。') }} <strong>{{ $ui('ファイルそのものを秘密として扱ってください。') }}</strong>
        </p>
        <p class="muted small"> {{ $ui('データが多いと時間がかかります。進捗は出ません（ファイルの大きさが最後まで分からないためです）。') }} </p>
        <p class="actions">
          <a class="button secondary" :href="backupUrl" :aria-disabled="dumping" @click="startDump">
            {{ dumping ? $ui("書き出しています…") : $ui("書き出す") }}
          </a>
        </p>

        <hr class="sep" />

        <p class="lead"> {{ $ui('書き出したファイルから、PB 全体をその時点へ戻します。') }} <strong>{{ $ui('いまのデータはすべて置き換わります。') }}</strong>
        </p>
        <p class="actions file-row">
          <input
            ref="fileInput"
            class="file-input"
            type="file"
            accept=".gz,.tgz,application/gzip"
            :disabled="restoring"
            @change="chooseFile"
          />
          <button type="button" class="secondary small" :disabled="restoring" @click="fileInput?.click()">{{ $ui('ファイルを選択') }}</button>
          <span class="file-name">{{ chosen?.name ?? $ui('選択されていません') }}</span>
          <button type="button" class="danger small" :disabled="!chosen || restoring" @click="openConfirm"> {{ $ui('取り込む…') }} </button>
        </p>

        <p v-if="restoring" class="restoring" role="status"> {{ $ui('取り込んでいます。このページを閉じないでください') }} </p>
        <p v-else-if="restoreError" class="error" role="alert">✕ {{ restoreError }}</p>

        <!-- 突き合わせ（11.12）。**expected と rows を両方出す** -->
        <div v-if="restored" class="result">
          <p class="lead">
            {{ formatDateTime(restored.backup.created_at) }} {{ $ui('の書き出し（版') }} {{ String(restored.backup.migration_version).padStart(4, '0') }}{{ $ui('）を取り込み、版') }} {{ String(restored.migration_version).padStart(4, '0') }} {{ $ui('まで進めました。') }} <strong v-if="restored.mismatched.length === 0"> {{ $ui('全') }} {{ restored.tables.length }} {{ $ui('表の件数が一致しています') }} </strong>
            <strong v-else class="bad">
              {{ restored.mismatched.length }} {{ $ui('表の件数が食い違っています') }} </strong>
          </p>
          <p class="muted small"> {{ $ui('この数は取り込んだ直後のものです。いまの件数は上の「容量」で見てください（このあと マイグレーションが行を足すことがあります）。') }} </p>
          <div class="table-scroll">
            <table class="tables">
              <thead>
                <tr>
                  <th class="name">{{ $ui('表') }}</th>
                  <th class="num">{{ $ui('書庫') }}</th>
                  <th class="num">{{ $ui('取り込み後') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="t in restored.tables" :key="t.name" :class="{ bad: isMismatched(t.name) }">
                  <td class="name"><code>{{ t.name }}</code></td>
                  <td class="num">{{ formatCount(t.expected) }}</td>
                  <td class="num">{{ formatCount(t.rows) }}</td>
                </tr>
              </tbody>
            </table>
          </div>
        </div>
      </section>
    </template>

    <RestoreBackupDialog
      v-if="confirming && chosen && status"
      :file="chosen"
      :meta="chosenMeta"
      :database-name="status.connection.database"
      :busy="restoring"
      :error-message="restoreError"
      @confirm="runRestore"
      @cancel="cancelConfirm"
    />
  </div>
</template>

<style scoped>
.tab {
  /* スクロールは親（AppSettingsPage の .page-body）が持つ */
  padding-block: var(--pb-space-3);
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--pb-space-3);
  margin-bottom: var(--pb-space-3);
}
.fetched {
  font-size: 12px;
}
.muted {
  color: var(--pb-text-muted);
}
.error {
  color: var(--pb-danger-text);
  margin: 0 0 var(--pb-space-3);
}
.block {
  margin-bottom: var(--pb-space-6);
}
.block h3 {
  font-size: 14px;
  margin: 0 0 var(--pb-space-2);
  border-bottom: 1px solid var(--pb-border);
  padding-bottom: var(--pb-space-1);
}

/*
 * 見出しと値の2列。**見出しの幅を節をまたいで固定する。** max-content にすると
 * 節ごとに値の始まる位置がずれる。
 * 11em は最も長い「このプロセスのプール」が1行に収まる幅である。
 */
.kv {
  display: grid;
  grid-template-columns: 11em minmax(0, 1fr);
  column-gap: var(--pb-space-4);
  row-gap: var(--pb-space-1);
  margin: 0 0 var(--pb-space-3);
}
.kv dt {
  font-weight: 600;
}
.kv dd {
  margin: 0;
  overflow-wrap: anywhere;
}
code {
  font-family: var(--pb-font-mono);
}

.table-scroll {
  overflow-x: auto;
}
.tables {
  width: 100%;
  border-collapse: collapse;
  font-size: 13px;
}
.tables th,
.tables td {
  padding: var(--pb-space-1) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
  text-align: left;
  white-space: nowrap;
}
.tables th {
  color: var(--pb-text-muted);
  font-weight: 600;
}
.tables tbody tr:last-child td {
  border-bottom: 0;
}
/* 表の名前だけを伸ばす。数値は内容の幅で足りる */
.tables .name {
  width: 100%;
}
.tables .num {
  text-align: right;
  font-variant-numeric: tabular-nums;
}
.nowrap {
  white-space: nowrap;
}
.note {
  font-size: 12px;
  margin-left: var(--pb-space-2);
}

/* ── バックアップと復元────────────────────────── */

/*
 * **状態を出す欄と、操作する欄を区切りで分ける**（5.12.2）。DB タブの大半は
 * 読み取り専用なので、どこから先が画面を変える操作かが見て分かる必要がある。
 */
.ops {
  border-top: 2px solid var(--pb-border);
  padding-top: var(--pb-space-4);
}
.ops .lead {
  margin: 0 0 var(--pb-space-2);
}
.small {
  font-size: 12px;
}
.actions {
  margin: 0 0 var(--pb-space-4);
}
.file-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-3);
  flex-wrap: wrap;
}
.file-input {
  display: none;
}
.file-name {
  font-size: 13px;
  overflow-wrap: anywhere;
}
.sep {
  border: 0;
  border-top: 1px solid var(--pb-line);
  margin: var(--pb-space-4) 0;
}
/* `<a>` をボタンに見せる。**サーバの Content-Disposition にそのまま乗せる**ため */
a.button {
  display: inline-block;
  text-decoration: none;
}
a.button[aria-disabled='true'] {
  pointer-events: none;
  opacity: 0.6;
}
.restoring {
  margin: 0 0 var(--pb-space-3);
  padding: var(--pb-space-2) var(--pb-space-3);
  border-left: 3px solid var(--pb-warning);
  background: var(--pb-warning-bg);
}
.result {
  margin-top: var(--pb-space-3);
}
.bad {
  color: var(--pb-danger-text);
}
.tables tr.bad td {
  background: var(--pb-danger-bg);
}
</style>
