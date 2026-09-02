<script setup lang="ts">
/**
 * 発行したエージェント用トークンの1回表示（`GuiDesign.md` 5.8.2）。
 *
 * **`token` は発行の応答でのみ返る**（`ApiDesign.md` 4.5.3）。DB には SHA-256 の
 * ハッシュしか残らず、再表示する API は無い。**5.8.1 の作法に従う**——再表示
 * できない旨を明記し、コピーボタンを置き、**コピーに失敗しても値は画面に残す。**
 *
 * **`IssuedTokenDialog`（5.8.1）と部品を分けてある。** あちらは CLI 用の
 * `api` トークンで、出すのは名前と有効期限だけである。こちらは**対象プロジェクトの
 * 限定**と**このトークンでできること**を持つ。共有するのはコピーの作法だけ
 * （`lib/clipboard.ts`）。
 *
 * **「積」という語を画面に出さない。** 実効権限は
 * `( 所有者のシステムロール ∪ 所有者のプロジェクトロール ) ∩ トークンのスコープ`
 * だが、これを利用者に読ませない——**「対象はこのプロジェクトだけ」と
 * 「あなたにできないことはできない」の2文で同じことが言える**（利用者の指摘、
 * 2026-09-02）。
 */
import { computed, onMounted, ref, useTemplateRef } from 'vue'

import type { IssuedAgentToken, MyAgent } from '../api/me'
import * as rolesApi from '../api/roles'
import type { CopyState } from '../lib/clipboard'
import { copySecret } from '../lib/clipboard'
import { formatDate } from '../lib/datetime'
import Modal from './Modal.vue'

const props = defineProps<{
  agent: MyAgent
  token: IssuedAgentToken
}>()

const emit = defineEmits<{ close: [] }>()

const tokenEl = useTemplateRef<HTMLElement>('tokenEl')
const copied = ref<CopyState>('idle')

async function copy() {
  copied.value = await copySecret(props.token.token, tokenEl.value)
}

// ── 権限を日本語にする ──────────────────────────────────────
//
// **`GET /permissions` の `description` を引く**（`ApiDesign.md` 7.2）。
// **画面に対応表を焼き込まない**——手順24a が「旧語彙で発行すると実効権限が
// 0件になる」という形で、写しが腐る失敗を踏んだばかりである。
//
// **このために 7.2 の必要権限を `user.manage` から外した**（2026-09-02）。
// 本画面の必要権限は「本人」であり、`user.manage` を持たない利用者が開く。

/** 権限キー → 説明。引けなければ空のまま（キーをそのまま出す） */
const descriptions = ref<Record<string, string>>({})

onMounted(async () => {
  try {
    const res = await rolesApi.getPermissions()
    const map: Record<string, string> = {}
    for (const p of res.items) map[p.key] = p.description
    descriptions.value = map
  } catch {
    // **引けなくても発行結果は出す。** 平文はこの一度しか存在しないので、
    // 権限の説明が無いことを理由にダイアログを出さない選択肢は無い（5.8.2）。
  }
})

/** 画面に出す行。説明が引けなければ権限キーそのもの */
const scopeLabels = computed(() =>
  props.token.scopes.map((key) => descriptions.value[key] ?? key),
)
</script>

<template>
  <Modal title="エージェント用トークンを発行しました" @close="emit('close')">
    <div class="body">
      <!-- warning は「面」で表す（8.4.1）。文字色はベースのまま -->
      <p class="warn">⚠ このトークンはこの画面でしか確認できません。閉じると再表示できません。</p>

      <dl class="fields">
        <dt>エージェント</dt>
        <dd>{{ agent.display_name }}</dd>
        <dt>プロジェクト</dt>
        <dd>{{ agent.project.name }}</dd>
        <dt>有効期限</dt>
        <dd>{{ token.expires_at ? formatDate(token.expires_at) : '無期限' }}</dd>
        <dt>トークン</dt>
        <dd>
          <div class="token-line">
            <code ref="tokenEl" class="token">{{ token.token }}</code>
            <button type="button" class="secondary" @click="copy">コピー</button>
          </div>
          <!-- 状態を色だけで示さない（9.2）ので記号か文言を必ず添える -->
          <p v-if="copied === 'ok'" class="note" role="status">✓ コピーしました</p>
          <p v-else-if="copied === 'manual'" class="note" role="status">
            自動でコピーできませんでした。選択した状態にしたので ⌘C（Ctrl+C）でコピーしてください。
          </p>
        </dd>
      </dl>

      <section class="scopes">
        <h3 class="scopes-title">このトークンでできること</h3>
        <!-- **補間のまわりに空白を置かない**（6.6）。全角のあいだに空きが出る -->
        <p class="hint">ⓘ 対象は{{ agent.project.name }}だけです。他のプロジェクトには届きません。</p>
        <p class="hint">ⓘ 下のうち、あなた自身にできないことは、エージェントにもできません。</p>
        <ul class="scope-list">
          <li v-for="(label, i) in scopeLabels" :key="token.scopes[i]">{{ label }}</li>
        </ul>
      </section>
    </div>

    <template #footer>
      <button type="button" class="primary" @click="emit('close')">閉じる</button>
    </template>
  </Modal>
</template>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
}

.warn {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border-left: 3px solid var(--pb-warning);
  background: var(--pb-warning-bg);
  color: var(--pb-text);
}

.fields {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  margin: 0;
}

dt {
  color: var(--pb-text-muted);
  font-size: 13px;
}

dd {
  margin: 0 0 var(--pb-space-3);
}

dd:last-child {
  margin-bottom: 0;
}

/* 折り返すと縦に伸びるので、ボタンは上端に揃える（5.8.1 と同じ） */
.token-line {
  display: flex;
  align-items: flex-start;
  gap: var(--pb-space-2);
}

.token-line .secondary {
  margin-top: 1px;
}

/* 等幅で出して `l` と `1`、`0` と `O` を見分けられるようにする。
   **折り返して全文を見せる**——コピーに失敗したときの受け皿が「手で写す」で
   ある以上、見えていない部分があってはならない。区切り文字を持たない乱数なので
   `break-all` が要る（5.8.1 と同じ判断）。 */
.token {
  flex: 1;
  min-width: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 14px;
  line-height: 1.6;
  word-break: break-all;
  user-select: all;
}

.scopes {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

.scopes-title {
  font-size: 13px;
  font-weight: 600;
}

/* **2列のグリッド。** 8件を縦に並べるとダイアログが伸びる。
   数を書いてあるので `auto-fit` にしない（手順17c の再発防止） */
.scope-list {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 0 var(--pb-space-4);
  margin: var(--pb-space-1) 0 0;
  padding-left: var(--pb-space-4);
  font-size: 13px;
}

/* 狭い窓では1列へ落とす。2列だと1項目が折り返して組が読めなくなる */
@media (max-width: 560px) {
  .scope-list {
    grid-template-columns: 1fr;
  }
}

.note {
  margin: var(--pb-space-1) 0 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
