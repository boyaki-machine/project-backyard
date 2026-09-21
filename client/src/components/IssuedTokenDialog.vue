<script setup lang="ts">
/**
 * 発行したアクセストークンの1回表示（`GuiDesign.md` 5.8.1）。
 *
 * **`token` は発行の応答でのみ返る**（`ApiDesign.md` 4.4.2）。DB には SHA-256 の
 * ハッシュしか残らず、再表示する API は無い。そのため
 *
 * - 再表示できない旨を明記する
 * - コピーボタンを置く
 * - **コピーに失敗しても値は画面に出したままにする**（手で写せるように）
 *
 * **`GeneratedPasswordDialog`（5.6.1）と部品を分けてある。** あちらは管理者が
 * **他人に渡す**ための画面で、末尾に「本人に伝えてください」が付き、出す項目も
 * メールアドレスと初期パスワードである。こちらは自分のトークンで、読み手も
 * 出す項目も違う。**共有するのはコピーの作法だけ**（`lib/clipboard.ts`）。
 */
import { ref, useTemplateRef } from 'vue'

import type { IssuedAccessToken } from '../api/me'
import { formatDate } from '../lib/datetime'
import type { CopyState } from '../lib/clipboard'
import { copySecret } from '../lib/clipboard'
import Modal from './Modal.vue'

const props = defineProps<{ token: IssuedAccessToken }>()

const emit = defineEmits<{ close: [] }>()

const tokenEl = useTemplateRef<HTMLElement>('tokenEl')
const copied = ref<CopyState>('idle')

async function copy() {
  copied.value = await copySecret(props.token.token, tokenEl.value)
}
</script>

<template>
  <Modal :title="$ui('アクセストークンを発行しました')" @close="emit('close')">
    <div class="body">
      <!-- warning は「面」で表す（8.4.1）。文字色はベースのまま -->
      <p class="warn">{{ $ui('⚠ このトークンはこの画面でしか確認できません。閉じると再表示できません。') }}</p>

      <dl class="fields">
        <dt>{{ $ui('名前') }}</dt>
        <dd>{{ token.name }}</dd>
        <dt>{{ $ui('有効期限') }}</dt>
        <dd>{{ token.expires_at ? formatDate(token.expires_at) : $ui("無期限") }}</dd>
        <dt>{{ $ui('トークン') }}</dt>
        <dd>
          <div class="token-line">
            <code ref="tokenEl" class="token">{{ token.token }}</code>
            <button type="button" class="secondary" @click="copy">{{ $ui('コピー') }}</button>
          </div>
          <!-- 状態を色だけで示さない（9.2）ので記号か文言を必ず添える -->
          <p v-if="copied === 'ok'" class="note" role="status">{{ $ui('✓ コピーしました') }}</p>
          <p v-else-if="copied === 'manual'" class="note" role="status"> {{ $ui('自動でコピーできませんでした。選択した状態にしたので ⌘C（Ctrl+C）でコピーしてください。') }} </p>
        </dd>
      </dl>

      <p class="hint">{{ $ui('ⓘ CLI やスクリプトからは Authorization: Bearer <トークン> の形で送ります。') }}</p>
    </div>

    <template #footer>
      <button type="button" class="primary" @click="emit('close')">{{ $ui('閉じる') }}</button>
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

.token-line {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}

/* 等幅で出して `l` と `1`、`0` と `O` を見分けられるようにする。
   **折り返して全文を見せる。** トークンは 50文字前後あり、横スクロールの箱に
   収めると末尾が隠れる——コピーに失敗したときの受け皿が「手で写す」である以上、
   見えていない部分があってはならない。区切り文字を持たない乱数なので
   `break-all` が要る（`overflow-wrap` では改行位置が見つからない）。 */
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

/* 折り返すと縦に伸びるので、ボタンは上端に揃える */
.token-line {
  align-items: flex-start;
}

.token-line .secondary {
  margin-top: 1px;
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
