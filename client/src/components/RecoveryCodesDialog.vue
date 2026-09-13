<script setup lang="ts">
/**
 * リカバリコードの1回表示（`GuiDesign.md` 5.8「リカバリコードの表示」）。
 *
 * **平文は発行の応答でしか返らない**（`ApiDesign.md` 4.6.3 / 4.6.5）。DB には
 * SHA-256 のハッシュしか残らないので、初期パスワード（5.6.1）とアクセストークン
 * （5.8.1）と同じ作法に従う。
 *
 * - 再表示できない旨を明記する
 * - コピーボタンを置く
 * - **コピーに失敗しても値は画面に出したままにする**（手で写せるように）
 *
 * **「テキストで保存」を足すのはこのダイアログだけである。** 10本は手で写す量では
 * なく、**印刷して金庫に入れる・パスワードマネージャに貼るという使い方が実際に
 * 起きる**（`GuiDesign.md` 5.8）。
 */
import { computed, ref, useTemplateRef } from 'vue'

import type { CopyState } from '../lib/clipboard'
import { copySecret } from '../lib/clipboard'
import Modal from './Modal.vue'

const props = defineProps<{ codes: string[] }>()

const emit = defineEmits<{ close: [] }>()

const codesEl = useTemplateRef<HTMLElement>('codesEl')
const copied = ref<CopyState>('idle')

/** 1行1本の平文。コピーと保存で同じ並びを使う */
const asText = computed(() => props.codes.join('\n'))

async function copy() {
  copied.value = await copySecret(asText.value, codesEl.value)
}

/**
 * テキストファイルとして保存する。
 *
 * **`Blob` と一時的な `a` 要素で行う。** サーバに口を作らない——平文を
 * もう一度サーバへ送る経路を増やさないためである。
 */
function save() {
  const blob = new Blob([asText.value + '\n'], { type: 'text/plain;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = 'pb-recovery-codes.txt'
  a.click()
  URL.revokeObjectURL(url)
}
</script>

<template>
  <Modal title="リカバリコードを保存してください" @close="emit('close')">
    <div class="body">
      <!-- warning は「面」で表す（8.4.1）。文字色はベースのまま -->
      <p class="warn">
        ⚠ この{{ codes.length }}本はこの画面でしか確認できません。閉じると再表示できません。
      </p>

      <ul ref="codesEl" class="codes">
        <li v-for="c in codes" :key="c">{{ c }}</li>
      </ul>

      <div class="actions-inline">
        <button type="button" class="secondary" @click="copy">コピー</button>
        <button type="button" class="secondary" @click="save">テキストで保存</button>
      </div>

      <!-- 状態を色だけで示さない（9.2）ので記号か文言を必ず添える -->
      <p v-if="copied === 'ok'" class="note" role="status">✓ コピーしました</p>
      <p v-else-if="copied === 'manual'" class="note" role="status">
        自動でコピーできませんでした。選択した状態にしたので ⌘C（Ctrl+C）でコピーしてください。
      </p>

      <p class="hint">
        ⓘ 認証アプリを使えなくなったとき、1本で1回ログインできます。使った分は元に戻りません。
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

.warn {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border-left: 3px solid var(--pb-warning);
  background: var(--pb-warning-bg);
  color: var(--pb-text);
}

/* 10本を2〜3列で並べる。**狭いと列が減る**（2.4） */
.codes {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(120px, 1fr));
  gap: var(--pb-space-2);
  margin: 0;
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  list-style: none;
}

.codes li {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 14px;
  letter-spacing: 0.04em;
}

.actions-inline {
  display: flex;
  gap: var(--pb-space-2);
}

.hint,
.note {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 12px;
}
</style>
