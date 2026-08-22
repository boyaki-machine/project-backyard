<script setup lang="ts">
/**
 * 生成されたパスワードの1回表示（`GuiDesign.md` 5.6.1）。
 *
 * **`generated_password` は生成した応答でのみ返る**（`ApiDesign.md` 6.2 の作成、
 * 6.6 のリセット）。再表示する API は無く、監査ログにも残らない。そのため
 *
 * - 再表示できない旨を明記する
 * - コピーボタンを置く（`ApiDesign.md` 4.4.2 のトークン発行と同じ扱い）
 * - **コピーに失敗しても値は画面に出したままにする**（手で写せるように）
 *
 * **作成とリセットで同じ部品を使う**（手順13b）。値の性質——1回だけ・再表示不可・
 * コピーして渡す——が同じで、違うのは見出しと導入の1文だけである。そこだけを
 * 呼び出し側から渡す。
 *
 * `password_mode=manual` のときは呼び出し側が平文を持っているのでこの
 * ダイアログを出さない。開閉の判断は呼び出し側にある。
 */
import { ref, useTemplateRef } from 'vue'

import type { CopyState } from '../lib/clipboard'
import { copySecret } from '../lib/clipboard'
import Modal from './Modal.vue'

withDefaults(
  defineProps<{
    displayName: string
    email: string
    password: string
    /** 見出し。リセットでは「パスワードをリセットしました」になる */
    title?: string
    /** 導入の1文。`<strong>` で囲む表示名の後ろに続く文言 */
    leadSuffix?: string
  }>(),
  { title: '初期パスワード', leadSuffix: 'を追加しました。' },
)

const emit = defineEmits<{ close: [] }>()

const passwordEl = useTemplateRef<HTMLElement>('passwordEl')

/**
 * コピーの結果。
 *
 * **作法は `lib/clipboard.ts` にある**（手順15b で切り出した）。
 * アクセストークンの1回表示（`IssuedTokenDialog`）と同じ扱いにするためで、
 * 「失敗したら選択して ⌘C に委ねる」を2か所で書き分けない。
 */
const copied = ref<CopyState>('idle')

async function copy(password: string) {
  copied.value = await copySecret(password, passwordEl.value)
}
</script>

<template>
  <Modal :title="title" @close="emit('close')">
    <div class="body">
      <p class="lead"><strong>{{ displayName }}</strong> {{ leadSuffix }}</p>

      <!-- warning は「面」で表す（8.4.1）。文字色はベースのまま -->
      <p class="warn">⚠ このパスワードはこの画面でしか確認できません。閉じると再表示できません。</p>

      <dl class="fields">
        <dt>メールアドレス</dt>
        <dd>{{ email }}</dd>
        <dt>初期パスワード</dt>
        <dd>
          <div class="password-line">
            <code ref="passwordEl" class="password">{{ password }}</code>
            <button type="button" class="secondary" @click="copy(password)">コピー</button>
          </div>
          <!-- 状態を色だけで示さない（9.2）ので記号か文言を必ず添える -->
          <p v-if="copied === 'ok'" class="note" role="status">✓ コピーしました</p>
          <p v-else-if="copied === 'manual'" class="note" role="status">
            自動でコピーできませんでした。選択した状態にしたので ⌘C（Ctrl+C）でコピーしてください。
          </p>
        </dd>
      </dl>

      <p class="hint">
        本人には、このパスワードと次回ログイン後に変更する必要があることを伝えてください。
      </p>
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

.lead {
  margin: 0;
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

.password-line {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}

/* 読み上げ・転記しやすい語句連結方式（`ApiDesign.md` 6.2.1）。等幅で出して
   `l` と `1`、`0` と `O` を見分けられるようにする */
.password {
  flex: 1;
  min-width: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  overflow-x: auto;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 15px;
  user-select: all;
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
  line-height: 1.7;
}

.primary,
.secondary {
  display: inline-flex;
  flex: none;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-4);
  border-radius: var(--pb-radius);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}

.primary {
  border: 1px solid var(--pb-accent);
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.primary:hover {
  border-color: var(--pb-accent-hover);
  background: var(--pb-accent-hover);
}

.secondary {
  border: 1px solid var(--pb-border);
  background: var(--pb-surface);
  color: inherit;
}

.secondary:hover {
  background: var(--pb-hover);
}
</style>
