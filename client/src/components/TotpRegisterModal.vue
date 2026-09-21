<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * 認証アプリの登録ダイアログ（`GuiDesign.md` 5.8「登録ダイアログ」）。
 *
 * **名前・QR・コードを1枚に収める。** 段に分けない——QR を読ませてから名前を
 * 訊くと、**アプリ側に登録が済んだあとで利用者がキャンセルを選べてしまい**、
 * どちらが本当の状態か分からなくなる。
 *
 * **`[ 登録 ]` を押すまで、この認証器は効かない**（`ApiDesign.md` 4.6.3）。
 * ダイアログを閉じただけなら認証に何も影響しないので、閉じる操作に確認を挟まない。
 *
 * **QR はここで描く。** サーバから画像を受け取らないので、画像のキャッシュや
 * 履歴に共有秘密が残る経路が無い（`ApiDesign.md` 4.6.2）。
 */
import { ref, useTemplateRef, watch } from 'vue'
import QRCode from 'qrcode'

import { ApiError } from '../api/client'
import * as mfaApi from '../api/mfa'
import type { ConfirmedTotp, StartedTotpRegistration } from '../api/mfa'
import type { CopyState } from '../lib/clipboard'
import { copySecret } from '../lib/clipboard'
import Modal from './Modal.vue'

const emit = defineEmits<{
  close: []
  /** 確定した。初回なら `recovery_codes` が入る（`ApiDesign.md` 4.6.3） */
  registered: [result: ConfirmedTotp]
}>()

/** 名前の上限（`user_mfa_credential.name` の CHECK と同じ） */
const MAX_NAME = 60

const name = ref('')
const code = ref('')

/** 登録開始の応答。**押すまで送らない**ので、初期は null */
const started = ref<StartedTotpRegistration | null>(null)
const starting = ref(false)
const confirming = ref(false)
const error = ref<ApiError | null>(null)

const secretEl = useTemplateRef<HTMLElement>('secretEl')
const copied = ref<CopyState>('idle')

/** QR を描いた data URI。**描けなかったら手入力に落とす**（下の分岐） */
const qrDataUri = ref('')
const qrFailed = ref(false)

/**
 * 名前を決めて QR を出す。
 *
 * **2回押すと1回目の登録は捨てられる**（サーバ側で未確定の行を置き換える。
 * `ApiDesign.md` 4.6.2）。画面もそれに合わせて、出ている QR を差し替える。
 */
async function start() {
  if (starting.value || name.value.trim() === '') return
  starting.value = true
  error.value = null
  try {
    started.value = await mfaApi.startTotp(name.value.trim())
  } catch (e: unknown) {
    error.value = asApiError(e)
  } finally {
    starting.value = false
  }
}

/** コードを照合して確定させる */
async function confirm() {
  const reg = started.value
  if (!reg || confirming.value || code.value.trim() === '') return
  confirming.value = true
  error.value = null
  try {
    emit('registered', await mfaApi.confirmTotp(reg.id, code.value.trim()))
  } catch (e: unknown) {
    error.value = asApiError(e)
    // **やり直しが要る失敗では、出ている QR を捨てる。** サーバ側が未確定の
    // 行を捨てているので（4.6.3 の5回失敗）、画面に残すと読めない QR になる。
    if (error.value.detailFor('code')?.code === 'attempts_exceeded') {
      started.value = null
      code.value = ''
    }
  } finally {
    confirming.value = false
  }
}

async function copy() {
  if (!started.value) return
  copied.value = await copySecret(started.value.secret, secretEl.value)
}

/** QR を描く。**失敗しても手入力の経路が残る** */
watch(started, async (reg) => {
  qrDataUri.value = ''
  qrFailed.value = false
  if (!reg) return
  try {
    qrDataUri.value = await QRCode.toDataURL(reg.otpauth_uri, {
      width: 180,
      margin: 1,
      // **白地・黒模様の固定である。** テーマに合わせて反転させると、
      // 読み取り機が明暗を取り違えることがある。
      color: { dark: '#000000', light: '#ffffff' },
    })
  } catch {
    qrFailed.value = true
  }
})

function detail(field: string) {
  return error.value?.detailFor(field)
}

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
}
</script>

<template>
  <Modal :title="$ui('認証アプリを追加')" @close="emit('close')">
    <div class="body">
      <!-- 名前は最初に決める（QR を読ませたあとに訊かない） -->
      <label class="field">
        <span class="label">{{ $ui('名前') }} <span class="required">*</span></span>
        <input
          v-model="name"
          type="text"
          name="name"
          :maxlength="MAX_NAME"
          placeholder="iPhone"
          :aria-invalid="detail('name') !== undefined"
          :disabled="started !== null || starting"
        />
        <span v-if="detail('name')" class="detail">{{ detail('name')?.message }}</span>
        <span v-else class="hint">{{ $ui('どの端末の認証アプリかを、あとで見分けるための名前です。') }}</span>
      </label>

      <div v-if="started === null" class="actions-inline">
        <button
          type="button"
          class="primary"
          :disabled="name.trim() === '' || starting"
          @click="start"
        >
          {{ starting ? $ui("準備中…") : $ui("QRコードを出す") }}
        </button>
      </div>

      <template v-else>
        <div class="qr-row">
          <div class="qr-box">
            <!-- alt を空にする。**この画像は装飾ではなく秘密**なので、
                 読み上げに値そのものを流さない（下の文字列が代替である） -->
            <img v-if="qrDataUri" :src="qrDataUri" alt="" class="qr" width="180" height="180" />
            <p v-else-if="qrFailed" class="qr-fallback"> {{ $ui('QRコードを描けませんでした。下の文字列を手で入力してください。') }} </p>
            <p v-else class="qr-fallback">{{ $ui('QRコードを準備しています…') }}</p>
          </div>

          <div class="qr-text">
            <p class="hint">{{ $ui('認証アプリでこのQRコードを読み取ってください。') }}</p>
            <p class="hint">{{ $ui('読み取れないときは、この文字列を手で入力します。') }}</p>
            <div class="secret-line">
              <code ref="secretEl" class="secret">{{ started.secret }}</code>
              <button type="button" class="secondary" @click="copy">{{ $ui('コピー') }}</button>
            </div>
            <!-- 状態を色だけで示さない（9.2） -->
            <p v-if="copied === 'ok'" class="note" role="status">{{ $ui('✓ コピーしました') }}</p>
            <p v-else-if="copied === 'manual'" class="note" role="status"> {{ $ui('自動でコピーできませんでした。選択した状態にしたので ⌘C（Ctrl+C）でコピーしてください。') }} </p>
          </div>
        </div>

        <label class="field">
          <span class="label">{{ $ui('表示された6桁のコード') }} <span class="required">*</span></span>
          <input
            v-model="code"
            type="text"
            name="code"
            inputmode="numeric"
            autocomplete="one-time-code"
            maxlength="8"
            :aria-invalid="detail('code') !== undefined"
            :disabled="confirming"
          />
          <span v-if="detail('code')" class="detail">{{ detail('code')?.message }}</span>
          <span v-else class="hint"> {{ $ui('ⓘ 登録を終えるには、いまアプリに出ているコードが必要です。') }} </span>
        </label>
      </template>

      <!-- details に紐づかない失敗（409 など）はここに出す（6.4） -->
      <p v-if="error && !detail('name') && !detail('code')" class="alert" role="alert">
        {{ error.message }}
      </p>
    </div>

    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button
        type="button"
        class="primary"
        :disabled="started === null || code.trim() === '' || confirming"
        @click="confirm"
      >
        {{ confirming ? $ui("登録中…") : $ui("登録") }}
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

.hint,
.note {
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

.actions-inline {
  display: flex;
  justify-content: flex-start;
}

/* QR と説明を横に並べる。**狭いと縦へ落とす**（2.4 のブレークポイント） */
.qr-row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-4);
  align-items: flex-start;
}

.qr-box {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 196px;
  min-height: 196px;
  padding: var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  /* **白地を固定する。** ダークテーマでも QR の明暗を反転させない */
  background: #ffffff;
}

.qr {
  display: block;
}

.qr-fallback {
  margin: 0;
  padding: var(--pb-space-2);
  color: #333333;
  font-size: 12px;
  text-align: center;
}

.qr-text {
  flex: 1 1 240px;
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  min-width: 0;
}

.secret-line {
  display: flex;
  gap: var(--pb-space-2);
  align-items: center;
  min-width: 0;
}

.secret {
  flex: 1 1 auto;
  overflow-wrap: anywhere;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  line-height: 1.6;
  word-break: break-all;
}
</style>
