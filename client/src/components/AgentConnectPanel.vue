<script setup lang="ts">
import { uiText } from '../locales/ui'
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
import { computed, onMounted, ref, watch } from 'vue'

import * as setupApi from '../api/agentSetup'
import type { AgentConnect } from '../api/agentSetup'
import { ApiError } from '../api/client'
import { copySecret, type CopyState } from '../lib/clipboard'
import { renderMarkdown } from '../lib/markdown'

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
/** Codex は HTTPS 直結と、ローカル stdio ブリッジを選べる。 */
const transport = ref('direct')
const bridgeOS = ref('')
const bridgeArch = ref('')
/** 節ごとのコピー結果（`GuiDesign.md` 6.4。トーストを使わない） */
const copied = ref<Record<string, CopyState>>({})

/** 接続設定は1枚か0枚（4.5.8.3） */
const file = computed(() => setup.value?.files?.[0] ?? null)

/**
 * Claude Desktop だけ、置き場も渡し方も他と違う。
 *
 * **画面に種別の対応表は持たない**という 5.8.2 の作法は表示名の話であり、
 * ここで見ているのは**手順そのものが違う**という事実である。他の種別は
 * 「作業フォルダの直下へ置き、履歴管理から外す」だが、**Desktop の設定は
 * 作業フォルダの外にあり、トークンもその中に書く。** 同じ文言を出すと嘘になる。
 *
 * **3つ目が現れたら、この事実はサーバが持つべきである**（`ApiDesign.md` 4.5.8.1 に
 * 項目を足す）。2つのうち1つに留まるうちは、画面の分岐で足りる。
 */
const CLIENT_KIND_CLAUDE_DESKTOP = 'claude_desktop'
const isDesktop = computed(
  () => setup.value?.agent.client_kind === CLIENT_KIND_CLAUDE_DESKTOP,
)
const isCodex = computed(() => setup.value?.agent.client_kind === 'codex')

const readyForSetup = computed(() => transport.value !== 'bridge' || (!!bridgeOS.value && !!bridgeArch.value))
const zipHref = computed(() => setupApi.agentConnectZipURL(props.agentId, transport.value, bridgeOS.value, bridgeArch.value))

/**
 * 「HTTPS の証明書を信頼させる」手順（`ApiDesign.md` 4.5.8.1b、`GuiDesign.md` 5.8.2）。
 *
 * **本文はサーバが返す。** 種別ごとの違いが種別の数だけあり、画面の分岐で持つと zip の
 * `PB-README.md` と同じ文を2か所に書くことになる（pb-202。上の「3つ目が現れたら」の実現）。
 * **本文は日本語だけである**——手引き（zip）と同じ扱い。
 */
const caTrustHtml = computed(() => renderMarkdown(setup.value?.ca_trust.body_md ?? ''))

/** 実機で確かめたか（4.5.8.1b）。**状態を色だけで示さない**（9.2）ので記号と文言で出す */
const caTrustLabel = computed(() => {
  switch (setup.value?.ca_trust.verification) {
    case 'verified':
      return uiText('✓ 実機で確認済み')
    case 'partial':
      return uiText('△ 一部だけ実機で確認')
    default:
      return uiText('? 実機では未確認')
  }
})

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

let loadSeq = 0
async function load() {
  const current = ++loadSeq
  if (!readyForSetup.value) {
    loading.value = false
    loadError.value = null
    return
  }
  loading.value = true
  loadError.value = null
  try {
    const result = await setupApi.getAgentConnect(props.agentId, transport.value, bridgeOS.value, bridgeArch.value)
    if (current === loadSeq) setup.value = result
  } catch (e: unknown) {
    if (current === loadSeq) {
      loadError.value =
        e instanceof ApiError
          ? e
          : new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
    }
  } finally {
    if (current === loadSeq) loading.value = false
  }
}
// **開いた時点で読む。** 親は畳んでいる間このコンポーネントを描かない。
onMounted(load)
watch([transport, bridgeOS, bridgeArch], () => { expanded.value = false; void load() })

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
  <section class="panel" :aria-label="$ui('接続の手順')">
    <p v-if="loadError" class="alert" role="alert">
      {{ loadError.message }}
      <button type="button" class="secondary" @click="load">{{ $ui('再試行') }}</button>
    </p>

    <p v-else-if="loading" class="hint">{{ $ui('読み込んでいます…') }}</p>

    <template v-else-if="setup">
      <!-- 1. 作業フォルダ ────────────────────────────────────
           **PB は材料の取り方を知らない**（`Requirements.md` 10.9.1）。
           リポジトリ型・配布型・MCP 型があり、指定するのはプロジェクト管理者で、
           置き場は PB の文書である。ここは深リンクだけを出す（手順28c） -->
      <h4 class="step">{{ $ui('1. 作業フォルダを用意する') }}</h4>
      <p class="hint"> {{ $ui('材料の取り方（リポジトリの clone、配布物の展開など）はプロジェクトの文書にあります。') }} <RouterLink :to="onboardingDocHref">{{ $ui('エージェントの参画情報を開く') }}</RouterLink>
      </p>

      <!-- 2. 接続設定 ─────────────────────────────────────── -->
      <h4 class="step">{{ $ui('2. 接続設定を置く') }}</h4>

      <template v-if="isCodex">
        <fieldset class="transport">
          <legend>{{ $ui('接続方式') }}</legend>
          <label><input v-model="transport" type="radio" value="direct" /> {{ $ui('HTTPS へ直接接続（公開 CA）') }}</label>
          <label><input v-model="transport" type="radio" value="bridge" /> {{ $ui('ローカル stdio ブリッジを使う（自己署名・社内 CA）') }}</label>
        </fieldset>
        <div v-if="transport === 'bridge'" class="bridge-platform">
          <label>{{ $ui('端末のOS') }}
            <select v-model="bridgeOS" :aria-label="$ui('端末のOS')">
              <option value="">{{ $ui('選択してください') }}</option>
              <option value="darwin">macOS</option>
              <option value="windows">Windows</option>
              <option value="linux">Linux</option>
            </select>
          </label>
          <label>{{ $ui('CPUアーキテクチャ') }}
            <select v-model="bridgeArch" :aria-label="$ui('CPUアーキテクチャ')">
              <option value="">{{ $ui('選択してください') }}</option>
              <option value="amd64">amd64 (x64)</option>
              <option value="arm64">arm64</option>
            </select>
          </label>
        </div>
        <p v-if="transport === 'bridge'" class="warn-note"> {{ $ui('ⓘ ローカル CA・社内 CA・自己署名の証明書を使うローカル PB では、この方式を使います。ブリッジも TLS 検証を行うので、証明書を OS の信頼ストアへ登録するか、') }}<code>PB_MCP_CA_FILE</code> {{ $ui('で発行元 CA を指定します。 配置・証明書登録または CA 指定・設定・後始末の詳細は、この zip の') }} <code>PB-README.md</code> {{ $ui('にあります。') }} </p>
        <!-- **直接接続の断定を弱めた**（pb-202）。自己署名で受け付けなかったのは観測だが、
             ローカル CA と CODEX_CA_CERTIFICATE での直接接続は誰も確かめていない -->
        <p v-else class="hint"> {{ $ui('公開 CA の証明書では直接接続できます。ローカル CA・社内 CA・自己署名の証明書では、ローカル stdio ブリッジを選びます（自己署名の証明書は、OS の信頼ストアへ登録しても直接接続では受け付けられませんでした。ローカル CA での直接接続は実機で確かめていません）。詳しい手順は、この zip の') }} <code>PB-README.md</code> {{ $ui('にあります。') }} </p>
      </template>

      <template v-if="readyForSetup">
      <template v-if="file">
        <div class="file-head">
          <code class="path">{{ file.path }}</code>
          <!-- 状態を色だけで示さない（9.2）。`mode` は常に merge（4.5.8.4） -->
          <span class="mode warn">{{ $ui('⚠ 統合') }}</span>
          <span class="spacer" />
          <button type="button" class="secondary" @click="copy('file', file.content, 'pb-cnx-file')"> {{ $ui('コピー') }} </button>
          <a class="secondary download" :href="zipHref" download>⬇ zip</a>
        </div>

        <!-- **Desktop は作業フォルダを持たない**。設定はアプリのメニューから
             開き、履歴管理とは関係しない。同じ文言を出すと嘘になる -->
        <template v-if="isDesktop">
          <p class="hint">
            {{ setup.agent.client_display_name }} {{ $ui('の') }} <strong>Settings &gt; Developer &gt; Edit Config</strong> {{ $ui('から') }} <code>{{ file.path }}</code> {{ $ui('を開いて、中の') }} <code>mcpServers</code> {{ $ui('に') }} <code>pb</code> {{ $ui('の項を足します。') }} </p>
          <p class="warn-note"> {{ $ui('⚠ 設定 > コネクタ（カスタムコネクタ）からは繋がりません。あちらの接続は あなたの端末ではなく') }} <strong>{{ $ui('Anthropic のクラウドから') }}</strong>{{ $ui('届くため、 手元の PB には到達せず https も必要になります') }}<br />
            ⚠ <code>"command": "npx"</code> {{ $ui('のままでは起動しません。GUI アプリはシェルの') }} <code>PATH</code> {{ $ui('を継承しないので、') }}<strong>{{ $ui('貼り替える欄が2つ') }}</strong>{{ $ui('あります （') }}<code>which npx</code> {{ $ui('と') }} <code>dirname $(which node)</code>）<br /> {{ $ui('⚠ 既に他の MCP サーバを登録している場合は、丸ごと置き換えないでください') }}<br /> {{ $ui('ⓘ 橋（') }}<code>mcp-remote</code>{{ $ui('）が PB と往復することは実測しました。') }} {{ setup.agent.client_display_name }} {{ $ui('自身がこの設定を読んで起動するところは') }} <strong>{{ $ui('まだ確かめられていません') }}</strong>
          </p>
        </template>
        <template v-else>
          <p class="hint"> {{ $ui('1 のフォルダの直下に') }} <code>{{ file.path }}</code> {{ $ui('として置きます。') }} </p>
          <!-- **既存を壊しうることを、押す前に出す。** zip では別名で入るが、
               コピーして貼る人には別名という手当てが効かない -->
          <p class="warn-note"> {{ $ui('ⓘ これは各自の環境です。履歴管理には入れません（') }}<code>.gitignore</code> {{ $ui('に入っています）') }}<br /> {{ $ui('⚠ 既に') }} <code>{{ file.path }}</code> {{ $ui('がある場合は、丸ごと置き換えないでください。 中の該当キーに') }} <code>pb</code> {{ $ui('の項だけを足します') }}<br /> {{ $ui('⚠ zip の中は') }} <code>{{ file.path }}</code> {{ $ui('ではなく別名です。展開してから元の名前へ戻します') }} </p>
        </template>

        <p v-if="copied.file === 'ok'" class="ok" role="status">{{ $ui('✓ コピーしました') }}</p>
        <p v-else-if="copied.file === 'manual'" class="hint" role="status"> {{ $ui('コピーできませんでした。下の内容を選択して ⌘C でコピーしてください。') }} </p>

        <pre id="pb-cnx-file" class="content">{{
          expanded ? file.content : preview(file.content)
        }}</pre>
        <button type="button" class="secondary more" @click="expanded = !expanded">
          {{ expanded ? $ui("畳む") : $ui("全文を見る") }}
        </button>
      </template>

      <!-- **配置ファイルを持たない種別**（4.5.8.3）。カードは消さず、
           自分で書くのに要る値を出す -->
      <template v-else>
        <p class="warn-note"> {{ $ui('ⓘ PB は') }} {{ setup.agent.client_display_name }} {{ $ui('向けの接続設定を持っていません。 下の値を使って、お使いのクライアントの作法で設定してください。') }} </p>
        <dl class="values">
          <dt>{{ $ui('接続先') }}</dt>
          <dd><code>{{ setup.mcp_url }}</code></dd>
          <dt>{{ $ui('認証') }}</dt>
          <dd><code>{{ $ui('Authorization: Bearer <トークン>') }}</code></dd>
          <dt>{{ $ui('環境変数') }}</dt>
          <dd><code>{{ setup.agent.token_env_name }}</code></dd>
        </dl>
        <a class="secondary download" :href="zipHref" download>{{ $ui('⬇ 手引きを zip で落とす') }}</a>
      </template>

      <!-- 2 の末尾：証明書を信頼させる ─────────────────────────
           **常に畳んで出す**（pb-202）。接続先が http でも、後から HTTPS にする人が先に読める。
           **番号を振らない**——振り直すと手引き（zip）の節番号と食い違う -->
      <details class="ca-trust">
        <summary>
          {{ $ui('HTTPS の証明書を信頼させる（ローカル CA・社内 CA のとき）') }}
          <span class="verification" :class="setup.ca_trust.verification">{{ caTrustLabel }}</span>
        </summary>
        <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
        <div class="markdown-body ca-trust-body" v-html="caTrustHtml"></div>
      </details>

      <!-- 3. 環境変数 ─────────────────────────────────────
           **`export_line` が null の種別では節ごと落とす**（4.5.8.2）。
           意味のない行を出すと、利用者は書かれていない前提を自分の期待で埋める -->
      <template v-if="setup.export_line">
        <h4 class="step">{{ $ui('3. トークンを環境変数へ置く') }}</h4>
        <div class="file-head">
          <code id="pb-cnx-export" class="export">{{ setup.export_line }}</code>
          <span class="spacer" />
          <button
            type="button"
            class="secondary"
            @click="copy('export', setup.export_line, 'pb-cnx-export')"
          > {{ $ui('コピー') }} </button>
        </div>
        <p v-if="copied.export === 'ok'" class="ok" role="status">{{ $ui('✓ コピーしました') }}</p>
        <p v-else-if="copied.export === 'manual'" class="hint" role="status"> {{ $ui('コピーできませんでした。上の行を選択して ⌘C でコピーしてください。') }} </p>
        <p v-if="transport === 'bridge' && bridgeOS === 'windows'" class="hint">{{ $ui('PowerShell で値を設定し、同じ PowerShell から Codex を起動します。') }}</p>
        <p v-else class="hint">
          <code>~/.zshrc</code> {{ $ui('か direnv に追記し、値を差し替えます。') }} </p>
        <!-- **平文はここに出せない**（`Requirements.md` 10.10.1）。
             失った場合の復旧経路は再発行である -->
        <p v-if="hasToken" class="hint"> {{ $ui('ⓘ 値は発行時に一度だけ表示されます。控えていない場合は [⋯] → トークンを再発行 で取り直します。') }} </p>
        <p v-else class="warn-note"> {{ $ui('⚠ このエージェントはトークンが未発行です。先に [ トークンを発行 ] を押してください。') }} </p>
      </template>
      <template v-else>
        <h4 class="step">{{ $ui('3. トークンを渡す') }}</h4>
        <!-- **同じ null でも渡し方が違う**（4.5.8.2）。Copilot はクライアントが
             入力を求め、Desktop は設定ファイルの env に平文で書く -->
        <p v-if="isDesktop" class="hint">
          {{ setup.agent.client_display_name }} {{ $ui('は環境変数を使いません（GUI アプリにシェルの環境は届きません）。 上の設定の') }} <code>env</code> {{ $ui('の') }} <code>{{ setup.agent.token_env_name }}</code> {{ $ui('に、発行時に一度だけ表示された値を貼ります。') }} </p>
        <p v-else class="hint">
          {{ setup.agent.client_display_name }} {{ $ui('は環境変数を使いません。初回の接続時に入力を求められるので、発行時に一度だけ表示された値を貼ります。') }} </p>
        <p v-if="isDesktop" class="warn-note"> {{ $ui('⚠ トークンの平文が設定ファイルに残ります。このファイルは共有しないでください') }} </p>
        <p v-if="!hasToken" class="warn-note"> {{ $ui('⚠ このエージェントはトークンが未発行です。先に [ トークンを発行 ] を押してください。') }} </p>
      </template>

      <!-- 4. 起動 ────────────────────────────────────────── -->
      <h4 class="step">{{ $ui('4. エージェントを起動して参画の手順を実行する') }}</h4>
      <p v-if="isDesktop" class="hint">
        <strong>{{ $ui('窓を閉じるだけでは足りません。') }}</strong>
        {{ setup.agent.client_display_name }} {{ $ui('を完全に終了してから起動し直し、「PB に参画して」と伝えます。') }} </p>
      <p class="hint"> {{ $ui('繋がると、上のトークンの欄に「接続済み」のチェックが付きます。') }} </p>
      <p v-if="isDesktop" class="hint"> {{ $ui('ⓘ 起動しないときは、原因が') }} {{ setup.agent.client_display_name }} {{ $ui('側に出ません。 同梱の手引き（zip）に切り分けの表があります。') }} </p>
      </template>
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

/*
 * **値の列を 0 まで縮められるようにする**（pb-204）。`auto 1fr` では 1fr の最小幅が中身
 * （折り返せない URL）になり、狭い幅で箱の外へはみ出したうえ、見出しの列が1文字ずつ縦に折れた。
 */
.values {
  display: grid;
  grid-template-columns: max-content minmax(0, 1fr);
  gap: var(--pb-space-1) var(--pb-space-3);
  font-size: 12px;
}

.values dt {
  color: var(--pb-text-muted);
  white-space: nowrap;
}

/* 長い値はこの箱の中だけで横スクロールさせる（6.7） */
.values dd {
  min-width: 0;
  overflow-x: auto;
  white-space: nowrap;
}

.transport {
  display: flex;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
  border: 0;
  padding: 0;
  font-size: 12px;
}

.transport legend { font-weight: 600; }

.bridge-platform {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-3);
}
.bridge-platform label {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  font-size: 12px;
}

/* 2 の末尾の畳んだ節。毎回読むものではない（TLS タブの .trust と同じ作法） */
.ca-trust summary {
  cursor: pointer;
  font-size: 12px;
  font-weight: 600;
}

/* 状態を色だけで示さない（9.2）。記号と文言が主で、面は補助 */
.verification {
  margin-left: var(--pb-space-2);
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  font-weight: 400;
  white-space: nowrap;
}

.verification.unverified {
  border-color: var(--pb-warning-border);
  background: var(--pb-warning-bg);
}

.ca-trust-body {
  margin-top: var(--pb-space-2);
  font-size: 12px;
  line-height: 1.6;
}

/* 広いコードはこの箱の中だけで横スクロールさせる（6.7） */
.ca-trust-body :deep(pre) {
  overflow-x: auto;
}
</style>
