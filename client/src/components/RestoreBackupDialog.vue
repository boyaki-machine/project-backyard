<script setup lang="ts">
/**
 * バックアップの取り込みの確認（`GuiDesign.md` 5.12.2 / 6.3）。
 *
 * **6.3 でいちばん重い操作である。** 全員がログアウトし、設定も証明書も書庫の
 * 時点へ戻り、取り込みのあいだ PB は誰からも使えない。**だから DB 名の入力を
 * 要求する**（ユーザー削除が表示名を求めるのと同じ理由で、締め出される側が居る）。
 *
 * **書庫の中身を、押す前に読んで見せる。** ファイル名は利用者が変えられるので、
 * どれを戻そうとしているのかが分からないまま押させない。
 *
 * **`pb_owner` の資格情報をここで尋ねる。** PB はこれを持たない（`DbDesign.md` 3.4）。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import { formatDateTime } from '../lib/datetime'

const props = withDefaults(
  defineProps<{
    /** 選ばれたファイル */
    file: File
    /** 書庫の `meta.json` から読んだ値。読めていなければ null */
    meta: { migration_version: number; created_at: string; pb_version: string } | null
    /** 確認のために入力させる DB 名 */
    databaseName: string
    /** 実行中。ボタンを止めて二重送信を防ぐ */
    busy?: boolean
    /** サーバが返した `message`。そのまま出す（`ApiDesign.md` 2.5） */
    errorMessage?: string | null
  }>(),
  { busy: false, errorMessage: null },
)

const emit = defineEmits<{
  confirm: [ownerUser: string, ownerPassword: string]
  cancel: []
}>()

const ownerUser = ref('pb_owner')
const ownerPassword = ref('')
const typed = ref('')

/** DB 名と完全に一致し、資格情報が埋まっているときだけ押せる */
const ready = computed(
  () =>
    typed.value.trim() === props.databaseName.trim() &&
    ownerUser.value.trim() !== '' &&
    ownerPassword.value !== '',
)

function confirm() {
  if (!ready.value) return
  emit('confirm', ownerUser.value.trim(), ownerPassword.value)
  // **パスワードを画面に残さない。** 送ったら消す。
  ownerPassword.value = ''
}
</script>

<template>
  <Modal :title="$ui('バックアップを取り込む')" @close="emit('cancel')">
    <div class="body">
      <p class="lead">
        <code class="file">{{ file.name }}</code>
      </p>
      <p v-if="meta" class="meta muted">
        {{ formatDateTime(Date.parse(meta.created_at)) }} {{ $ui('に書き出し ／ データ形式の版') }} {{ String(meta.migration_version).padStart(4, '0') }} ／ PB {{ meta.pb_version }}
      </p>
      <p v-else class="meta error-text"> {{ $ui('✕ このファイルから書き出しの情報を読めませんでした。PB が書き出したファイルを選んでください') }} </p>

      <!-- warning は「面」で表す（8.4.1）。文字色はベースのまま -->
      <p class="warn">{{ $ui('⚠ いまの PB のデータは、すべて置き換わります。') }}</p>

      <ul class="effects">
        <li>{{ $ui('ログインしている他の利用者とエージェントは、全員ログアウトします') }}</li>
        <li>{{ $ui('待ち受け・TLS 証明書などの設定も、この時点に戻ります') }}</li>
        <li>{{ $ui('取り込みのあいだ、PB は誰からも使えません') }}</li>
      </ul>

      <fieldset class="creds">
        <legend>{{ $ui('DB のオーナー権限が要ります') }}</legend>
        <label class="field">
          <span class="label">{{ $ui('ロール') }}</span>
          <input
            v-model="ownerUser"
            type="text"
            name="owner_user"
            autocomplete="off"
            autocapitalize="off"
            spellcheck="false"
            :disabled="busy"
          />
        </label>
        <label class="field">
          <span class="label">{{ $ui('パスワード') }}</span>
          <!-- **ブラウザに覚えさせない**（5.12.2）。送ったら欄からも消す -->
          <input
            v-model="ownerPassword"
            type="password"
            name="owner_password"
            autocomplete="new-password"
            :disabled="busy"
          />
        </label>
      </fieldset>

      <label class="field">
        <span class="label"> {{ $ui('取り込むには、データベース名') }} <code>{{ databaseName }}</code> {{ $ui('を入力してください') }} </span>
        <input
          v-model="typed"
          type="text"
          name="confirm_database"
          autocomplete="off"
          autocapitalize="off"
          spellcheck="false"
          :disabled="busy"
        />
      </label>

      <p v-if="errorMessage" class="error-text" role="alert">✕ {{ errorMessage }}</p>
    </div>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('cancel')"> {{ $ui('やめる') }} </button>
      <button type="button" class="danger" :disabled="busy || !ready" @click="confirm"> {{ $ui('取り込む') }} </button>
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
.file {
  font-family: var(--pb-font-mono);
  overflow-wrap: anywhere;
}
.meta {
  margin: calc(-1 * var(--pb-space-3)) 0 0;
  font-size: 13px;
}
.muted {
  color: var(--pb-text-muted);
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
.creds {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
  margin: 0;
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
}
.creds legend {
  padding-inline: var(--pb-space-2);
  font-size: 13px;
  font-weight: 600;
}
.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
}
.field .label {
  font-size: 13px;
}
.error-text {
  margin: 0;
  color: var(--pb-danger-text);
}
code {
  font-family: var(--pb-font-mono);
}
</style>
