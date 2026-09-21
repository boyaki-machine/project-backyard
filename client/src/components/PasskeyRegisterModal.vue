<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * パスキーの登録ダイアログ（`GuiDesign.md` 5.8「パスキーの一覧」）。
 *
 * **名前を先に訊き、端末のダイアログはそのあとに開く。** 端末のダイアログが閉じてから
 * 名前を訊くと、**端末にはパスキーができたのに PB に登録されない**状態で、利用者が
 * キャンセルを選べてしまう（TOTP の登録ダイアログと同じ理由）。
 *
 * **名前の重複は、端末のダイアログを開く前に一覧と突き合わせて止める。**
 */
import { computed, ref } from 'vue'

import { ApiError } from '../api/client'
import * as passkeysApi from '../api/passkeys'
import type { Passkey } from '../api/passkeys'
import { createPasskey, isPasskeyAlreadyRegistered, isPasskeyCancelled } from '../lib/passkey'
import type { PasskeyOptionsEnvelope } from '../lib/passkey'
import Modal from './Modal.vue'

const props = defineProps<{
  /** 登録済みの名前。重複は端末のダイアログを開く前に止める */
  existingNames: string[]
}>()

const emit = defineEmits<{
  close: []
  registered: [passkey: Passkey]
}>()

/** 名前の上限（`user_passkey.name` の CHECK と同じ） */
const MAX_NAME = 60

const name = ref('')
const busy = ref(false)
const error = ref<ApiError | null>(null)

/**
 * 端末が作った応答。**サーバが名前の重複で断ったときに送り直すために持つ**
 * （`ApiDesign.md` 4.7.3 は名前と件数を挑戦の消費より前に確かめる）。
 */
const pending = ref<unknown>(null)

const nameTaken = computed(() => props.existingNames.includes(name.value.trim()))

const nameError = computed(() => {
  if (nameTaken.value) return uiText("その名前は既に使われています。別の名前を指定してください")
  return error.value?.detailFor('name')?.message ?? ''
})

async function register() {
  const n = name.value.trim()
  if (busy.value || n === '' || nameTaken.value) return
  busy.value = true
  error.value = null
  try {
    if (pending.value === null) {
      const options = await passkeysApi.startPasskeyRegistration()
      pending.value = await createPasskey(options.options as PasskeyOptionsEnvelope)
    }
    emit('registered', await passkeysApi.registerPasskey(n, pending.value))
  } catch (e: unknown) {
    // **端末のダイアログを閉じたときは何も出さない**（5.8）
    if (isPasskeyCancelled(e)) return
    error.value = asApiError(e)
    // 名前の重複（409 already_exists）のときだけ、端末の応答を持ち越す。
    // それ以外の失敗では挑戦が消費されているので、次は最初からやり直す。
    if (!(e instanceof ApiError && e.code === 'already_exists' && e.message.includes(uiText("名前")))) {
      pending.value = null
    }
  } finally {
    busy.value = false
  }
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  if (isPasskeyAlreadyRegistered(e)) {
    return new ApiError({
      status: 0,
      code: 'network_error',
      message: uiText("この端末のパスキーは登録済みです"),
    })
  }
  return new ApiError({
    status: 0,
    code: 'network_error',
    message: uiText("パスキーを作成できませんでした"),
  })
}
</script>

<template>
  <Modal :title="$ui('パスキーを追加')" @close="emit('close')">
    <div class="body">
      <label class="field">
        <span class="label">{{ $ui('名前') }} <span class="required">*</span></span>
        <input
          v-model="name"
          type="text"
          name="name"
          :maxlength="MAX_NAME"
          placeholder="MacBook"
          :aria-invalid="nameError !== ''"
          :disabled="busy"
          @keydown.enter.prevent="register"
        />
        <span v-if="nameError" class="detail">{{ nameError }}</span>
        <span v-else class="hint">{{ $ui('どの端末のパスキーかを、あとで見分けるための名前です。') }}</span>
      </label>

      <p class="hint"> {{ $ui('ⓘ [ 次へ ] を押すと、端末がパスキーの作成を求めます。生体認証か PIN で確認してください。') }} </p>

      <!-- details に紐づかない失敗はここに出す（6.4） -->
      <p v-if="error && !error.detailFor('name')" class="alert" role="alert">
        {{ error.message }}
      </p>
    </div>

    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button
        type="button"
        class="primary"
        :disabled="name.trim() === '' || nameTaken || busy"
        @click="register"
      >
        {{ busy ? $ui("確認中…") : $ui("次へ") }}
      </button>
    </template>
  </Modal>
</template>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
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

.required {
  color: var(--pb-danger);
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.detail {
  color: var(--pb-danger);
  font-size: 12px;
}

.alert {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border-left: 3px solid var(--pb-danger);
  background: var(--pb-danger-bg);
  color: var(--pb-text);
}
</style>
