<script setup lang="ts">
/**
 * DB タブ（`GuiDesign.md` 5.12.2）。pb-110。
 *
 * **設計の正本は `ApiDesign.md` 11.10。** 読み取り専用で、変更の操作を持たない。
 *
 * **定期的に引かない。** 件数は全表の `count(*)` なので、行が増えるほど1回が
 * 重くなる。開いたときと [再読み込み] のときだけ引き、**いつの値かを横に出す。**
 *
 * **下半分はバックアップと復元である**（pb-147）。上半分（状態）は読み取り専用、
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
        : new ApiError({ status: 0, code: 'network_error', message: '通信に失敗しました' })
  } finally {
    loading.value = false
  }
}

onMounted(load)

/** 接続先。**Unix ソケットはディレクトリのパスなので、ポートを括弧で添える** */
const endpoint = computed(() => {
  const c = status.value?.connection
  if (!c) return ''
  if (c.host.startsWith('/')) return `${c.host}（ポート ${c.port}）`
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
  const ms = Date.parse(s.fetched_at) - Date.parse(s.server.started_at)
  if (!(ms >= 0)) return ''
  const min = Math.floor(ms / 60000)
  const days = Math.floor(min / 1440)
  const hours = Math.floor((min % 1440) / 60)
  if (days > 0) return `${days}日${hours}時間`
  if (hours > 0) return `${hours}時間${min % 60}分`
  return `${min}分`
})

function formatCount(n: number): string {
  return n.toLocaleString('ja-JP')
}

// ── バックアップと復元（`ApiDesign.md` 11.11〜11.12。pb-147）────────────

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
      e instanceof ApiError ? e.message : '取り込みに失敗しました。もう一度お試しください'
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
      <span v-if="status" class="muted fetched">{{ formatDateTime(status.fetched_at) }} に取得</span>
      <button type="button" class="secondary small" :disabled="loading" @click="load">
        再読み込み
      </button>
    </div>

    <p v-if="loadError" class="error" role="alert">{{ loadError.message }}</p>
    <p v-if="loading && !status" class="muted">読み込み中…</p>

    <template v-if="status">
      <section class="block">
        <h3>接続</h3>
        <dl class="kv">
          <dt>接続先</dt>
          <dd><code>{{ endpoint }}</code></dd>
          <dt>データベース</dt>
          <dd><code>{{ status.connection.database }}</code></dd>
          <dt>ユーザー</dt>
          <dd><code>{{ status.connection.user }}</code></dd>
          <dt>暗号化</dt>
          <dd>{{ status.connection.tls ? 'あり（TLS）' : 'なし' }}</dd>
        </dl>
      </section>

      <section class="block">
        <h3>サーバ</h3>
        <dl class="kv">
          <dt>PostgreSQL</dt>
          <dd>{{ status.server.version }}</dd>
          <dt>起動</dt>
          <!-- **1行に書く。** 改行すると括弧の前に空白が入る（`GuiDesign.md` 6.7） -->
          <dd>{{ formatDateTime(status.server.started_at) }}<span v-if="uptime" class="muted nowrap">（稼働 {{ uptime }}）</span></dd>
          <dt>マイグレーション</dt>
          <dd><code>{{ migration }}</code></dd>
        </dl>
      </section>

      <section class="block">
        <h3>セッション</h3>
        <dl class="kv">
          <dt>この DB への接続</dt>
          <dd>{{ formatCount(status.sessions.database) }}<span class="muted nowrap">（サーバ全体の上限 {{ formatCount(status.server.max_connections) }}）</span></dd>
          <dt>うち PB</dt>
          <dd>{{ formatCount(status.sessions.pb) }}</dd>
          <!-- **「このプロセスの」と書く。** 複数のプロセスでは引くたびに別の値になりうる（5.12.2） -->
          <dt>このプロセスのプール</dt>
          <dd>使用中 {{ status.pool.acquired }} ・ 待機 {{ status.pool.idle }} ・ 上限 {{ status.pool.max }}</dd>
        </dl>
      </section>

      <section class="block">
        <h3>容量</h3>
        <dl class="kv">
          <dt>DB の大きさ</dt>
          <dd>{{ formatBytes(status.size_bytes) }}</dd>
        </dl>

        <div class="table-scroll">
          <table class="tables">
            <thead>
              <tr>
                <th class="name">表</th>
                <th class="num">件数</th>
                <th class="num">大きさ</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="t in status.tables" :key="t.name">
                <td class="name">
                  <code>{{ t.name }}</code>
                  <!-- **落とさずに理由を出す**——一覧から消すと、表があることに誰も気づかない -->
                  <span v-if="t.rows === null" class="muted note">読む権限がありません</span>
                </td>
                <td class="num">{{ t.rows === null ? '—' : formatCount(t.rows) }}</td>
                <td class="num">{{ formatBytes(t.size_bytes) }}</td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- ── バックアップと復元（5.12.2。pb-147）──────────────── -->
      <section class="block ops">
        <h3>バックアップ</h3>

        <p class="lead">
          PB 全体を1つのファイルに書き出します。利用者・エージェント・暗号鍵も含まれます。
          <strong>ファイルそのものを秘密として扱ってください。</strong>
        </p>
        <p class="muted small">
          データが多いと時間がかかります。進捗は出ません（ファイルの大きさが最後まで分からないためです）。
        </p>
        <p class="actions">
          <a class="button secondary" :href="backupUrl" :aria-disabled="dumping" @click="startDump">
            {{ dumping ? '書き出しています…' : '書き出す' }}
          </a>
        </p>

        <hr class="sep" />

        <p class="lead">
          書き出したファイルから、PB 全体をその時点へ戻します。
          <strong>いまのデータはすべて置き換わります。</strong>
        </p>
        <p class="actions file-row">
          <input
            ref="fileInput"
            type="file"
            accept=".gz,.tgz,application/gzip"
            :disabled="restoring"
            @change="chooseFile"
          />
          <button type="button" class="danger small" :disabled="!chosen || restoring" @click="openConfirm">
            取り込む…
          </button>
        </p>

        <p v-if="restoring" class="restoring" role="status">
          取り込んでいます。このページを閉じないでください
        </p>
        <p v-else-if="restoreError" class="error" role="alert">✕ {{ restoreError }}</p>

        <!-- 突き合わせ（11.12）。**expected と rows を両方出す** -->
        <div v-if="restored" class="result">
          <p class="lead">
            {{ formatDateTime(restored.backup.created_at) }} の書き出し（版
            {{ String(restored.backup.migration_version).padStart(4, '0') }}）を取り込み、版
            {{ String(restored.migration_version).padStart(4, '0') }} まで進めました。
            <strong v-if="restored.mismatched.length === 0">
              全 {{ restored.tables.length }} 表の件数が一致しています
            </strong>
            <strong v-else class="bad">
              {{ restored.mismatched.length }} 表の件数が食い違っています
            </strong>
          </p>
          <p class="muted small">
            この数は取り込んだ直後のものです。いまの件数は上の「容量」で見てください（このあと
            マイグレーションが行を足すことがあります）。
          </p>
          <div class="table-scroll">
            <table class="tables">
              <thead>
                <tr>
                  <th class="name">表</th>
                  <th class="num">書庫</th>
                  <th class="num">取り込み後</th>
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
 * 節ごとに値の始まる位置がずれる（pb-110 のスクリーンショットで見つけた）。
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

/* ── バックアップと復元（pb-147）────────────────────────── */

/*
 * **状態を出す欄と、操作する欄を区切りで分ける**（5.12.2）。DB タブは pb-110 では
 * 読み取り専用だったので、どこから先が画面を変える操作かが見て分かる必要がある。
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
.file-row input[type='file'] {
  font-size: 13px;
  min-width: 0;
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
