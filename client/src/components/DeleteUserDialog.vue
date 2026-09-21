<script setup lang="ts">
/**
 * ユーザー削除の確認（`GuiDesign.md` 6.3）。
 *
 * 6.3 は破壊的操作ごとに確認方法を定めており、**ユーザー削除だけが
 * 「ユーザー名の入力」を要求する**。Yes/No で足りる操作は `ConfirmDialog`
 * を使う（手順11b で作った）。ここは同じ位置づけの別部品である。
 *
 * 何が起きるかを列挙するのは、`ApiDesign.md` 6.5 が**物理削除**と定めるため。
 * 無効化（`is_active: false`）との違いが読めないと、取り返しのつかない側を
 * 選んでしまう。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'

const props = withDefaults(
  defineProps<{
    displayName: string
    email: string
    /** 実行中。ボタンを止めて二重送信を防ぐ */
    busy?: boolean
    /** サーバが返した `message`（409 など）。そのまま出す（`ApiDesign.md` 2.5） */
    errorMessage?: string | null
  }>(),
  { busy: false, errorMessage: null },
)

const emit = defineEmits<{ confirm: []; cancel: [] }>()

const typed = ref('')

/**
 * 表示名と**完全に一致**したときだけ押せる。
 *
 * 前後の空白だけは落とす（コピー＆ペーストで紛れ込むため）。それ以外を
 * 緩めると、確認の意味が無くなる。
 */
const matched = computed(() => typed.value.trim() === props.displayName.trim())
</script>

<template>
  <Modal :title="$ui('ユーザーを削除')" @close="emit('cancel')">
    <div class="body">
      <p class="lead">
        <strong>{{ displayName }}</strong>（{{ email }}{{ $ui('）を削除します。') }} </p>

      <!-- warning は「面」で表す（8.4.1）。文字色はベースのまま -->
      <p class="warn">{{ $ui('⚠ この操作は取り消せません。無効化するだけなら「無効化」を使ってください。') }}</p>

      <ul class="effects">
        <li>{{ $ui('ログイン情報・アクセストークン・プロジェクトの権限が消えます') }}</li>
        <li>{{ $ui('担当していたチケットは残り、担当者が未設定になります') }}</li>
        <li>{{ $ui('書き込んだコメントは残り、投稿者が「削除されたユーザー」になります') }}</li>
      </ul>

      <label class="field">
        <span class="label"> {{ $ui('確認のため、表示名') }} <code>{{ displayName }}</code> {{ $ui('を入力してください') }} </span>
        <input
          v-model="typed"
          type="text"
          name="confirm_display_name"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          :disabled="busy"
        />
      </label>

      <p v-if="errorMessage" class="error" role="alert">✕ {{ errorMessage }}</p>
    </div>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('cancel')"> {{ $ui('キャンセル') }} </button>
      <button
        type="button"
        class="danger"
        :disabled="busy || !matched"
        @click="emit('confirm')"
      > {{ $ui('削除する') }} </button>
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

.effects {
  margin: 0;
  padding-left: var(--pb-space-6);
  color: var(--pb-text-muted);
  font-size: 13px;
  list-style: disc;
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.label code {
  color: var(--pb-text);
}

input {
  height: 32px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

input:disabled {
  background: var(--pb-surface);
}

.error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
