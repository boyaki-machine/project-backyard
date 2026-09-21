<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * エージェント連携セットアップ `/p/:key/settings/agents`（`GuiDesign.md` 5.11）。
 *
 * **リポジトリにコミットする配置ファイルを作る場である**（`Requirements.md` 10.9.1
 * の系統A）。**1リポジトリにつき1回**の作業で、成果物は全参加者に届く。
 *
 * **資格情報も個人の設定も扱わない。** 接続設定（`.mcp.json` など）は履歴管理の
 * 対象外なので（10.8.1）、各人が `/me/agents` で受け取る（手順28b）。
 *
 * **プロジェクト設定のタブではない。** 独立したルートを持ち、必要権限も違う
 * （`project.edit` ではなく `agent.register`。3.2）。
 */
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import * as setupApi from '../api/agentSetup'
import type { AgentSetup, AgentSetupFile } from '../api/agentSetup'
import { ApiError } from '../api/client'
import * as meApi from '../api/me'
import type { AgentClientKind } from '../api/me'
import EmptyState from '../components/EmptyState.vue'
import PageHeader from '../components/PageHeader.vue'
import { copySecret, type CopyState } from '../lib/clipboard'

const route = useRoute()
const router = useRouter()
const projectKey = computed(() => String(route.params.key))

/**
 * 選択肢は `has_setup_template` が真の種別だけ（`ApiDesign.md` 4.5.7）。
 *
 * **選んだ先に何も出ない選択肢を置かない**（5.11）。`/me/agents` の登録モーダルは
 * 全種別を出すので、**絞るのは画面ごとに違う**——サーバは値を返すだけにしてある。
 */
const kinds = ref<AgentClientKind[]>([])
const selected = ref<string[]>([])

const setup = ref<AgentSetup | null>(null)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)
const kindsError = ref<ApiError | null>(null)

/** 本文を開いている行（既定は畳む。5.11「本文は畳んで出す」） */
const expanded = ref<Set<string>>(new Set())
/** 行ごとのコピー結果（`GuiDesign.md` 6.4。トーストを使わない） */
const copied = ref<Record<string, CopyState>>({})

const templateKinds = computed(() => kinds.value.filter((k) => k.has_setup_template))
const hasSelection = computed(() => selected.value.length > 0)

const zipHref = computed(() =>
  hasSelection.value ? setupApi.agentSetupZipURL(projectKey.value, selected.value) : '',
)

async function loadKinds() {
  kindsError.value = null
  try {
    kinds.value = (await meApi.listAgentClientKinds()).items
    // **既定で Claude Code を選んでおかない。** 何も選ばれていない状態から
    // 始めるほうが、「自分が使うものに印を付ける」という操作が伝わる。
  } catch (e: unknown) {
    kindsError.value = asApiError(e)
  }
}

async function loadSetup() {
  if (!hasSelection.value) {
    setup.value = null
    return
  }
  loading.value = true
  loadError.value = null
  try {
    setup.value = await setupApi.getAgentSetup(projectKey.value, selected.value)
    expanded.value = new Set()
    copied.value = {}
  } catch (e: unknown) {
    loadError.value = asApiError(e)
    setup.value = null
  } finally {
    loading.value = false
  }
}

onMounted(loadKinds)
// **選択を変えたら取り直す。** 生成は安く、押すボタンを1つ増やすほうが手間である。
watch(selected, loadSetup, { deep: true })
watch(projectKey, () => {
  selected.value = []
  setup.value = null
  void loadKinds()
})

function toggleKind(key: string) {
  const i = selected.value.indexOf(key)
  if (i >= 0) selected.value.splice(i, 1)
  else selected.value.push(key)
}

function toggleExpanded(path: string) {
  const next = new Set(expanded.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  expanded.value = next
}

/**
 * 畳んだときに見せる先頭3行（5.11）。
 *
 * **手順ファイルは40〜60行ある。** 全文を並べると `[ 一式をダウンロード ]` が
 * 画面外へ出る。
 */
function preview(content: string): string {
  return content.split('\n').slice(0, 3).join('\n')
}

async function copyFile(file: AgentSetupFile) {
  // **コピーに失敗しても内容は画面に残す**（`lib/clipboard.ts` の作法）。
  const el = document.getElementById(`pb-file-${cssID(file.path)}`)
  copied.value = { ...copied.value, [file.path]: await copySecret(file.content, el) }
}

/** パスを DOM の id に使える形へ畳む（`/` と `.` を `-` にする） */
function cssID(path: string): string {
  return path.replace(/[^a-zA-Z0-9]+/g, '-')
}

function kindLabel(key: string): string {
  return kinds.value.find((k) => k.key === key)?.display_name ?? key
}

/** 追記のファイルかどうか（`mode` が `create` でない。`ApiDesign.md` 5.7.1） */
function isAppend(file: AgentSetupFile): boolean {
  return file.mode !== 'create'
}

function modeLabel(file: AgentSetupFile): string {
  if (file.mode === 'append') return uiText("⚠ 追記")
  if (file.mode === 'merge') return uiText("⚠ 統合")
  return uiText("新規")
}

/**
 * マーカーで囲まれた常時コンテキストか（`Requirements.md` 10.8.8）。
 *
 * **`.gitignore` も `append` だがマーカーを持たない。** 貼る前・貼った後の
 * チェックはマーカーの中身を差し替える話なので、**持つものにだけ出す**
 * （描画して気づいた。持たない行に出すと「〜 の間だけが」が空欄で並ぶ）。
 */
function hasMarker(file: AgentSetupFile): boolean {
  return Boolean(file.marker_begin) && Boolean(file.marker_end)
}

/**
 * 追記・統合の行に添える説明（5.11）。
 *
 * **貼る前・貼った後のチェックは、生成物にも埋めてある**（10.8.8）。
 * **画面にだけ書くと、zip で落として後日展開した人には届かない。**
 * **生成物にだけ書くと、貼り終えた後にしか読まれない。**
 */
function appendHint(file: AgentSetupFile): string {
  if (file.mode === 'merge') {
    return uiText("既に {value0} があるなら、permissions.allow にこの中身を足してください（丸ごと置き換えると既存の設定が消えます）。", { value0: file.path })
  }
  if (hasMarker(file)) {
    return uiText("既存の {value0} の末尾へ貼ります。{value1} 〜 {value2} の間だけが PB の管理範囲です。", { value0: file.path, value1: file.marker_begin, value2: file.marker_end })
  }
  return uiText("既存の {value0} の末尾へ貼ります。同じ行が既にあれば足しません。", { value0: file.path })
}

function backToSettings() {
  void router.push(`/p/${projectKey.value}/settings`)
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
}
</script>

<template>
  <div class="page">
    <!-- 戻る導線は actions スロットへ置く（PageHeader は名前付きスロットしか描かない） -->
    <PageHeader :title="$ui('エージェント連携セットアップ')">
      <template #actions>
        <button type="button" class="secondary" @click="backToSettings"> {{ $ui('← プロジェクト設定へ') }} </button>
      </template>
    </PageHeader>

    <div class="page-body">
      <section class="block">
        <p class="hint"> {{ $ui('ⓘ リポジトリに置くファイルを生成します。1リポジトリにつき1回の作業で、成果物は コミットして全員で共有します。参加する人ごとの接続設定とトークンは、各自の') }} <RouterLink to="/me/agents">{{ $ui('自分の設定 → エージェント') }}</RouterLink> {{ $ui('で受け取ります。') }} </p>

        <h2 class="step">{{ $ui('1. 使うクライアントに印を付ける') }}</h2>
        <p v-if="kindsError" class="alert" role="alert">
          {{ kindsError.message }}
          <button type="button" class="secondary" @click="loadKinds">{{ $ui('再試行') }}</button>
        </p>
        <div v-else class="kinds">
          <label v-for="k in templateKinds" :key="k.key" class="kind">
            <input
              type="checkbox"
              :checked="selected.includes(k.key)"
              @change="toggleKind(k.key)"
            />
            <span>{{ k.display_name }}</span>
          </label>
        </div>
        <p class="hint">{{ $ui('両方（または3つとも）を使う人がいるなら、まとめて印を付けてください。') }}</p>

        <template v-if="setup">
          <h2 class="step">{{ $ui('2. 接続先') }}</h2>
          <p class="endpoint"><code>{{ setup.base_url }}/mcp/{{ setup.project.key }}</code></p>
          <p class="hint"> {{ $ui('エージェントはこの URL へ接続します。この値は PB を開いているアドレスから 組み立てています。') }} </p>
        </template>

        <h2 class="step">{{ $ui('3. 置くファイル') }}</h2>

        <p v-if="loadError" class="alert" role="alert">
          {{ loadError.message }}
          <button type="button" class="secondary" @click="loadSetup">{{ $ui('再試行') }}</button>
        </p>

        <EmptyState
          v-else-if="!hasSelection"
          :title="$ui('クライアントを選んでください')"
          :description="$ui('印を付けると、そのクライアント向けの配置ファイルが並びます。')"
        />

        <p v-else-if="loading" class="hint">{{ $ui('生成しています…') }}</p>

        <template v-else-if="setup">
          <div class="files-head">
            <span class="count">{{ setup.files.length }}{{ $ui('件') }}</span>
            <a class="primary download" :href="zipHref" download>{{ $ui('⬇ 一式をダウンロード') }}</a>
          </div>

          <ul class="files">
            <li v-for="f in setup.files" :key="f.path" class="file">
              <div class="file-head">
                <code class="path">{{ f.path }}</code>
                <span class="mode" :class="{ warn: isAppend(f) }">{{ modeLabel(f) }}</span>
                <span v-if="f.client_kind" class="kind-tag">{{ kindLabel(f.client_kind) }}</span>
                <span class="spacer" />
                <button type="button" class="secondary" @click="copyFile(f)">{{ $ui('コピー') }}</button>
              </div>

              <p v-if="isAppend(f)" class="hint">{{ appendHint(f) }}</p>
              <!-- 利用者の要望（2026-09-06）。生成物にも同じ2行を埋めてある。
                   **マーカーを持つ行にだけ出す**——差し替えの話なので、
                   `.gitignore` のような素の追記には当たらない -->
              <p v-if="hasMarker(f)" class="warn-note"> {{ $ui('⚠ 貼る前に') }} <code>git pull</code> {{ $ui('して、リポジトリに新しい版が入っていないか 確かめてください') }}<br /> {{ $ui('⚠ 貼った後に') }} <code>git diff</code> {{ $ui('でこのブロックを見て、他の人の更新を 潰していないか確かめてください') }} </p>

              <p v-if="copied[f.path] === 'ok'" class="ok" role="status">{{ $ui('✓ コピーしました') }}</p>
              <p v-else-if="copied[f.path] === 'manual'" class="hint" role="status"> {{ $ui('コピーできませんでした。下の内容を選択して ⌘C でコピーしてください。') }} </p>

              <pre :id="`pb-file-${cssID(f.path)}`" class="content">{{
                expanded.has(f.path) ? f.content : preview(f.content)
              }}</pre>
              <button type="button" class="secondary more" @click="toggleExpanded(f.path)">
                {{ expanded.has(f.path) ? $ui("畳む") : $ui("全文を見る") }}
              </button>
            </li>
          </ul>

          <h2 class="step">{{ $ui('4. コミットする') }}</h2>
          <p class="hint">
            <code>.gitignore</code> {{ $ui('の追記を反映してから、生成したファイルをコミットして ください。接続設定（') }}<code>.mcp.json</code> {{ $ui('など）は含まれません——各自の環境なので 履歴に入れません。') }} </p>
        </template>
      </section>
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

.step {
  margin-top: var(--pb-space-3);
  font-size: 14px;
  font-weight: 600;
}

.kinds {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-4);
}

.kind {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  font-size: 13px;
}

/* 等幅は base.css が code / pre に当てている。ここで font-family を書かない */
.endpoint {
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-size: 13px;
}

.files-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-3);
}

.count {
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ダウンロードは <a download> にする（`ApiDesign.md` 5.7.2）。
   ボタンと同じ見た目にするため、行の高さだけ揃える */
.download {
  display: inline-flex;
  align-items: center;
  text-decoration: none;
}

.files {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
  list-style: none;
}

.file {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
}

.file-head {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
}

.path {
  font-size: 13px;
}

/* 状態を色だけで示さない（9.2）。文言を必ず添える */
.mode {
  color: var(--pb-text-muted);
  font-size: 12px;
}

.mode.warn {
  color: var(--pb-warning-text);
  font-weight: 600;
}

.kind-tag {
  color: var(--pb-text-muted);
  font-size: 12px;
}

.spacer {
  flex: 1;
}

/* warning は文字ではなく面で表す（`GuiDesign.md` 8.4.1） */
.warn-note {
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-warning-border);
  border-radius: var(--pb-radius);
  background: var(--pb-warning-bg);
  font-size: 12px;
  line-height: 1.6;
}

/* 行の中の従属操作なので、主ボタンより小さくして左へ寄せる */
.more {
  align-self: flex-start;
  height: 26px;
  font-size: 12px;
  font-weight: 400;
}

/* 広い内容はこの箱の中だけで横スクロールさせる（6.7） */
.content {
  overflow-x: auto;
  max-height: 60vh;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre;
}
</style>
