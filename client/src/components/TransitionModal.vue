<script setup lang="ts">
/**
 * 状態を変えるときの確認とコメント。手順18b。
 *
 * **いまはどの画面からも使っていない**（`GuiDesign.md` 5.5「遷移にコメントを
 * 添えない」。pb-55）。状態変更の頻度に対してダイアログが重く、チケット詳細から
 * 外した。**部品は消していない**——同じ形の確認が要る場所が他にも出うるためで、
 * `ApiDesign.md` 9.6 の `comment` もサーバ側に残っている。
 *
 * **`ApiDesign.md` 9.6 は `comment` を任意で受け、同じトランザクションで
 * `kind='progress'` のコメントを作る**——遷移とコメントが必ず対になる。
 * 手順17b では投稿先の表示場所が無かったので送っていなかった。
 *
 * **これは「選択できるものは選んだ時点で `PATCH`」の例外である**（5.5「編集の単位」）。
 * 状態だけは担当や優先度と違って**業務上の到達点が動く**ので、なぜ動かしたかを
 * 残せる場所を同じ操作の中に置く価値が、クリック1つより大きい。
 * **書かない人は `[変更する]` を押すだけで抜けられる。**
 *
 * **`MarkdownEditor` を使わない。** 遷移の理由は1〜2行で足り、ソースと
 * プレビューの2枚をモーダルに積むと縦が要る（原則1）。書き込まれた本文は
 * コメント欄では Markdown として描かれる。
 */
import { ref } from 'vue'

import Modal from './Modal.vue'

defineProps<{
  /** いまの状態の表示名 */
  fromName: string
  /** 移る先の表示名 */
  toName: string
  busy?: boolean
}>()

const emit = defineEmits<{
  close: []
  /** `comment` は空なら `null`。**空文字を送らない**（本文の無いコメントが生まれる） */
  confirm: [comment: string | null]
}>()

const comment = ref('')

function submit(): void {
  const c = comment.value.trim()
  emit('confirm', c === '' ? null : c)
}
</script>

<template>
  <Modal :title="$ui('状態を変更しますか？')" @close="emit('close')">
    <form id="transition-form" class="form" @submit.prevent="submit">
      <p class="summary">
        <span class="from">{{ fromName }}</span>
        <span class="arrow" aria-hidden="true">→</span>
        <span class="to">{{ toName }}</span> {{ $ui('へ変更します。') }} </p>

      <label class="field">
        <span class="label">{{ $ui('コメント（任意）') }}</span>
        <textarea
          v-model="comment"
          class="comment"
          rows="3"
          :placeholder="$ui('なぜこの状態にしたかを残せます')"
        ></textarea>
        <span class="hint"> {{ $ui('書くと「経過」のコメントとして下の一覧に並びます。空のままでも変更できます') }} </span>
      </label>
    </form>

    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button type="submit" form="transition-form" class="primary" :disabled="busy"> {{ $ui('変更する') }} </button>
    </template>
  </Modal>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
}

.summary {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--pb-space-2);
  margin: 0;
  font-size: 13px;
}

.from,
.to {
  font-weight: 600;
}

.arrow {
  color: var(--pb-text-muted);
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.comment {
  width: 100%;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
  resize: vertical;
}

.hint {
  color: var(--pb-text-muted);
  font-size: 12px;
}
</style>
