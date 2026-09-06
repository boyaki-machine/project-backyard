<script setup lang="ts">
/**
 * 接続パネル（`GuiDesign.md` 5.8.2「接続パネル」、手順28b）。
 *
 * **エージェント1件が PB に繋がるまでに、人が手を動かすもの一式**を出す
 * （`Requirements.md` 10.9.1 の系統B）。**カードの中に畳んで置く**——モーダルでは
 * 接続設定の全文が読めず（既定 520px）、`wide` は「中で左右に並べるもの」に限る規約が
 * ある（6.1）。別ルートにすると 3.2 の表に行が増え、1〜4件の画面で往復させる値打ちが薄い。
 *
 * **出すのは「MCP が使える状態になるまで」だけである。** 作業の材料をどこから
 * どう用意するかは PB が知らない（リポジトリ型・配布型・MCP 型がある）ので、
 * **プロジェクトの文書「エージェントの参画情報」への深リンクだけ**を置く（手順28c）。
 *
 * **接続確認はここに出さない。** `token.last_used_at` はカード側（トークンの箱）が
 * 持っており、**畳んでいるときも見えている必要がある**——「繋がったか確かめに戻る」
 * 場面では、パネルを開く動機がない。
 */
import { computed, onMounted, ref } from 'vue'

import * as setupApi from '../api/agentSetup'
import type { AgentConnect } from '../api/agentSetup'
import { ApiError } from '../api/client'
import { copySecret, type CopyState } from '../lib/clipboard'

const props = defineProps<{
  /** 対象のエージェント（`ApiDesign.md` 4.5.1 の id） */
  agentId: string
  /** トークンが未発行か。**3 の節の文言が変わる** */
  hasToken: boolean
  /** 文書へのリンク先（プロジェクトキー） */
  projectKey: string
}>()

const setup = ref<AgentConnect | null>(null)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

/** 本文を開いているか（既定は畳む。5.11 と同じ作法） */
const expanded = ref(false)
/** 節ごとのコピー結果（`GuiDesign.md` 6.4。トーストを使わない） */
const copied = ref<Record<string, CopyState>>({})

/** 接続設定は1枚か0枚（4.5.8.3） */
const file = computed(() => setup.value?.files?.[0] ?? null)

const zipHref = computed(() => setupApi.agentConnectZipURL(props.agentId))

/**
 * 作業材料の取り方を書く文書の `slug`（`DbDesign.md` 8.1.2、`GuiDesign.md` 5.8.2）。
 *
 * **文書ツリーの根へ落とさない。** そうすると憲章5件の中からどれを読むのかを
 * 参加者に当てさせることになる。**接続の手順の1行目でそれをやらせるのは、
 * この画面が「MCP が使える状態になるまで」を完成品にした意図と合わない。**
 *
 * **有無を判定しない。** 複製はプロジェクト作成時にしか走らないので、それ以前の
 * プロジェクトには無い。**押しても Docs 画面の左の木は残る**ので行き止まりに
 * ならず、**判定を足すと同じ事実を API と画面の2か所から出すことになる。**
 */
const ONBOARDING_DOC_SLUG = 'agent-onboarding'

const onboardingDocHref = computed(() => `/p/${props.projectKey}/docs/${ONBOARDING_DOC_SLUG}`)

async function load() {
  loading.value = true
  loadError.value = null
  try {
    setup.value = await setupApi.getAgentConnect(props.agentId)
  } catch (e: unknown) {
    loadError.value =
      e instanceof ApiError
        ? e
        : new ApiError({ status: 0, code: 'network_error', message: '通信に失敗しました' })
  } finally {
    loading.value = false
  }
}
// **開いた時点で読む。** 親は畳んでいる間このコンポーネントを描かない。
onMounted(load)

async function copy(key: string, value: string, elementID: string) {
  // **コピーに失敗しても内容は画面に残す**（`lib/clipboard.ts` の作法）
  const el = document.getElementById(elementID)
  copied.value = { ...copied.value, [key]: await copySecret(value, el) }
}

/**
 * 畳んだときに見せる先頭3行（5.11 と同じ）。
 *
 * **接続設定は10〜30行ある。** 全文を並べるとカードが画面外まで伸びる。
 */
function preview(content: string): string {
  return content.split('\n').slice(0, 3).join('\n')
}
</script>

<template>
  <section class="panel" aria-label="接続の手順">
    <p v-if="loadError" class="alert" role="alert">
      {{ loadError.message }}
      <button type="button" class="secondary" @click="load">再試行</button>
    </p>

    <p v-else-if="loading" class="hint">読み込んでいます…</p>

    <template v-else-if="setup">
      <!-- 1. 作業フォルダ ────────────────────────────────────
           **PB は材料の取り方を知らない**（`Requirements.md` 10.9.1）。
           リポジトリ型・配布型・MCP 型があり、指定するのはプロジェクト管理者で、
           置き場は PB の文書である。ここは深リンクだけを出す（手順28c） -->
      <h4 class="step">1. 作業フォルダを用意する</h4>
      <p class="hint">
        材料の取り方（リポジトリの clone、配布物の展開など）はプロジェクトの文書にあります。
        <RouterLink :to="onboardingDocHref">エージェントの参画情報を開く</RouterLink>
      </p>

      <!-- 2. 接続設定 ─────────────────────────────────────── -->
      <h4 class="step">2. 接続設定を置く</h4>

      <template v-if="file">
        <div class="file-head">
          <code class="path">{{ file.path }}</code>
          <!-- 状態を色だけで示さない（9.2）。`mode` は常に merge（4.5.8.4） -->
          <span class="mode warn">⚠ 統合</span>
          <span class="spacer" />
          <button type="button" class="secondary" @click="copy('file', file.content, 'pb-cnx-file')">
            コピー
          </button>
          <a class="secondary download" :href="zipHref" download>⬇ zip</a>
        </div>

        <p class="hint">
          1 のフォルダの直下に <code>{{ file.path }}</code> として置きます。
        </p>
        <!-- **既存を壊しうることを、押す前に出す。** zip では別名で入るが、
             コピーして貼る人には別名という手当てが効かない -->
        <p class="warn-note">
          ⓘ これは各自の環境です。履歴管理には入れません（<code>.gitignore</code> に入っています）<br />
          ⚠ 既に <code>{{ file.path }}</code> がある場合は、丸ごと置き換えないでください。
          中の該当キーに <code>pb</code> の項だけを足します<br />
          ⚠ zip の中は <code>{{ file.path }}</code> ではなく別名です。展開してから元の名前へ戻します
        </p>

        <p v-if="copied.file === 'ok'" class="ok" role="status">✓ コピーしました</p>
        <p v-else-if="copied.file === 'manual'" class="hint" role="status">
          コピーできませんでした。下の内容を選択して ⌘C でコピーしてください。
        </p>

        <pre id="pb-cnx-file" class="content">{{
          expanded ? file.content : preview(file.content)
        }}</pre>
        <button type="button" class="secondary more" @click="expanded = !expanded">
          {{ expanded ? '畳む' : '全文を見る' }}
        </button>
      </template>

      <!-- **配置ファイルを持たない種別**（4.5.8.3）。カードは消さず、
           自分で書くのに要る値を出す -->
      <template v-else>
        <p class="warn-note">
          ⓘ PB は {{ setup.agent.client_display_name }} 向けの接続設定を持っていません。
          下の値を使って、お使いのクライアントの作法で設定してください。
        </p>
        <dl class="values">
          <dt>接続先</dt>
          <dd><code>{{ setup.mcp_url }}</code></dd>
          <dt>認証</dt>
          <dd><code>Authorization: Bearer &lt;トークン&gt;</code></dd>
          <dt>環境変数</dt>
          <dd><code>{{ setup.agent.token_env_name }}</code></dd>
        </dl>
        <a class="secondary download" :href="zipHref" download>⬇ 手引きを zip で落とす</a>
      </template>

      <!-- 3. 環境変数 ─────────────────────────────────────
           **`export_line` が null の種別では節ごと落とす**（4.5.8.2）。
           意味のない行を出すと、利用者は書かれていない前提を自分の期待で埋める -->
      <template v-if="setup.export_line">
        <h4 class="step">3. トークンを環境変数へ置く</h4>
        <div class="file-head">
          <code id="pb-cnx-export" class="export">{{ setup.export_line }}</code>
          <span class="spacer" />
          <button
            type="button"
            class="secondary"
            @click="copy('export', setup.export_line, 'pb-cnx-export')"
          >
            コピー
          </button>
        </div>
        <p v-if="copied.export === 'ok'" class="ok" role="status">✓ コピーしました</p>
        <p v-else-if="copied.export === 'manual'" class="hint" role="status">
          コピーできませんでした。上の行を選択して ⌘C でコピーしてください。
        </p>
        <p class="hint">
          <code>~/.zshrc</code> か direnv に追記し、値を差し替えます。
        </p>
        <!-- **平文はここに出せない**（`Requirements.md` 10.10.1）。
             失った場合の復旧経路は再発行である -->
        <p v-if="hasToken" class="hint">
          ⓘ 値は発行時に一度だけ表示されます。控えていない場合は [⋯] → トークンを再発行 で取り直します。
        </p>
        <p v-else class="warn-note">
          ⚠ このエージェントはトークンが未発行です。先に [ トークンを発行 ] を押してください。
        </p>
      </template>
      <template v-else>
        <h4 class="step">3. トークンを渡す</h4>
        <p class="hint">
          {{ setup.agent.client_display_name }} は環境変数を使いません。初回の接続時に入力を求められるので、発行時に一度だけ表示された値を貼ります。
        </p>
        <p v-if="!hasToken" class="warn-note">
          ⚠ このエージェントはトークンが未発行です。先に [ トークンを発行 ] を押してください。
        </p>
      </template>

      <!-- 4. 起動 ────────────────────────────────────────── -->
      <h4 class="step">4. エージェントを起動して参画の手順を実行する</h4>
      <p class="hint">
        繋がると、上のトークンの欄に「接続済み」のチェックが付きます。
      </p>
    </template>
  </section>
</template>

<style scoped>
.panel {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  margin-top: var(--pb-space-2);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

/* カードの中の入れ子なので、5.11 の見出しより1段小さくする */
.step {
  margin-top: var(--pb-space-2);
  font-size: 13px;
  font-weight: 600;
}

.step:first-of-type {
  margin-top: 0;
}

.file-head {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
}

.path,
.export {
  font-size: 12px;
}

/* 状態を色だけで示さない（9.2）。文言を必ず添える */
.mode.warn {
  color: var(--pb-warning-text);
  font-size: 12px;
  font-weight: 600;
}

.spacer {
  flex: 1;
}

/* ダウンロードは <a download> にする（4.5.8.5）。行の高さだけボタンに揃える */
.download {
  display: inline-flex;
  align-items: center;
  height: 28px;
  padding: 0 var(--pb-space-3);
  text-decoration: none;
  font-size: 12px;
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
  max-height: 50vh;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  font-size: 12px;
  line-height: 1.6;
  white-space: pre;
}

.values {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: var(--pb-space-1) var(--pb-space-3);
  font-size: 12px;
}

.values dt {
  color: var(--pb-text-muted);
}
</style>
