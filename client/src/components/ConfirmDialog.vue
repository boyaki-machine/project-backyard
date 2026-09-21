<script setup lang="ts">
/**
 * 破壊的操作の確認ダイアログ（`GuiDesign.md` 6.3）。
 *
 * 6.3 は操作ごとに確認方法を定める。**Yes/No で足りるもの**（チケット削除・
 * プロジェクトのアーカイブ・トークン失効）がこれである。ユーザー削除だけは
 * 名前の入力を要求するため、別の部品になる（手順13）。
 *
 * 見た目とフォーカスの扱いは `Modal.vue` に委ねる。ここが足すのは
 * 「実行」ボタンの意味づけ（`danger`）と、処理中の二重押下の防止だけである。
 */
import Modal from './Modal.vue'

withDefaults(
  defineProps<{
    title: string
    /** 本文。何が起きるかを1〜2文で書く */
    message: string
    /** 実行ボタンの文言。「はい」ではなく操作名にする（押す前に何が起きるか読めるように） */
    confirmLabel: string
    /** 取り消せない操作か。true なら実行ボタンを danger で出す（8.4.3） */
    danger?: boolean
    /** 実行中。ボタンを止めて二重送信を防ぐ */
    busy?: boolean
  }>(),
  { danger: false, busy: false },
)

const emit = defineEmits<{ confirm: []; cancel: [] }>()
</script>

<template>
  <Modal :title="title" @close="emit('cancel')">
    <p class="message">{{ message }}</p>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('cancel')"> {{ $ui('キャンセル') }} </button>
      <button
        type="button"
        :class="danger ? 'danger' : 'primary'"
        :disabled="busy"
        @click="emit('confirm')"
      >
        {{ confirmLabel }}
      </button>
    </template>
  </Modal>
</template>

<style scoped>
.message {
  margin: 0;
  line-height: 1.7;
  white-space: pre-line;
}

button:disabled {
  cursor: default;
  opacity: 0.6;
}
</style>
