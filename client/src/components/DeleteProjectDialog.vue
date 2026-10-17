<script setup lang="ts">
/**
 * プロジェクト削除の確認（`GuiDesign.md` 5.9.3 / 6.3）。
 *
 * 6.3 は**プロジェクトキーの入力**を要求する。ユーザー削除の表示名（`DeleteUserDialog`）と
 * 同じ作りで、キーと完全に一致したときだけ押せる。**サーバも同じキーを照合する**
 * （`ApiDesign.md` 5.6.1）ので、入力した文字列をそのまま送る。
 *
 * 何が消えて何が残るかを列挙するのは、取り消せない操作だからである。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'

const props = withDefaults(
  defineProps<{
    projectKey: string
    name: string
    /** 実行中。ボタンを止めて二重送信を防ぐ */
    busy?: boolean
    /** サーバが返した `message`（409 / 422 など）。そのまま出す（`ApiDesign.md` 2.5） */
    errorMessage?: string | null
  }>(),
  { busy: false, errorMessage: null },
)

const emit = defineEmits<{ confirm: [typed: string]; cancel: [] }>()

const typed = ref('')

/** 前後の空白だけは落とす（コピー＆ペーストで紛れ込むため）。サーバも同じ扱い */
const matched = computed(() => typed.value.trim() === props.projectKey)
</script>

<template>
  <Modal :title="$ui('プロジェクトを削除')" @close="emit('cancel')">
    <div class="body">
      <p class="lead">
        <strong>{{ name }}</strong>（{{ projectKey }}{{ $ui('）を削除します。') }}
      </p>

      <!-- warning は「面」で表す（8.4.1）。文字色はベースのまま -->
      <p class="warn">{{ $ui('⚠ この操作は取り消せません。') }}</p>

      <ul class="effects">
        <li>{{ $ui('チケット・コメント・文書・スプリント・タグ・作業記録がすべて消えます') }}</li>
        <li>{{ $ui('このプロジェクト専用のエージェントとそのトークンも消えます') }}</li>
        <li>{{ $ui('監査ログは残ります') }}</li>
      </ul>

      <label class="field">
        <span class="label"> {{ $ui('確認のため、プロジェクトキー') }} <code>{{ projectKey }}</code> {{ $ui('を入力してください') }} </span>
        <input
          v-model="typed"
          type="text"
          name="confirm_project_key"
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
        @click="emit('confirm', typed)"
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
