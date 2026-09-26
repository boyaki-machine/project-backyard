<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * 関連チケットの追加（`GuiDesign.md` 5.5「関連チケット」「追加のモーダル」）。手順18b。
 *
 * **編集は無い。** `ApiDesign.md` 9.10.1 が `PATCH` を持たない——一意制約が
 * `(source, target, link_type)` である以上、**`link_type` の変更は別の行になるのと
 * 同じ**であり、消して作り直すのと変わらない。
 *
 * **選択肢は4つで、API の `link_type` は3つである。** `blocks` が向きで
 * 「自先行 / 自後行」に割れるため——**`自後行` は API に無い**（`POST .../links` は
 * このチケットを常に `source` にする）。**どちらの向きも同じモーダルから作れる
 * ことのほうが、実装の対称性より優先する**ので、`自後行` は呼び出し側が
 * **相手のチケットに対して `blocks` を作る**形で実現する。
 *
 * **送信そのものは呼び出し側が行う**（`ReferenceModal` と同じ形）。サーバの
 * 検証エラー（409 `already_exists` / 422 `self_link`）を出す責務は呼び出し側にあり、
 * ここは「入力を集めて確定を伝える」までを持つ。
 */
import { computed, ref } from 'vue'

import Modal from './Modal.vue'
import { linkChoices } from '../api/links'
import type { LinkChoice } from '../api/links'
import { ticketTypeIcons, ticketTypeLabels } from '../api/tickets'
import { statusLabel } from '../lib/catalogLabels'
import type { Ticket } from '../api/tickets'

const props = defineProps<{
  /** 相手の候補。**自分自身は呼び出し側で落としてある**（5.5） */
  candidates: Ticket[]
  /** 一覧に出す完全形IDのため */
  projectKey: string
  busy?: boolean
  /** サーバが返した検証エラー（`details[].field` → メッセージ） */
  fieldErrors?: Record<string, string>
  /** 欄に紐づかない誤り（409 `already_exists` など）。モーダルの中に留める */
  formError?: string
}>()

const emit = defineEmits<{
  close: []
  save: [body: { targetSeq: number; choice: LinkChoice['value'] }]
}>()

const targetSeq = ref<number | null>(null)
const choice = ref<LinkChoice['value']>('relates')
const touched = ref(false)

/** 候補の絞り込み。**200件から目で探させない** */
const filter = ref('')

const filtered = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (q === '') return props.candidates
  return props.candidates.filter(
    (c) =>
      c.title.toLowerCase().includes(q) ||
      String(c.seq).includes(q) ||
      `${props.projectKey}-${c.seq}`.toLowerCase().includes(q),
  )
})

const targetError = computed(() =>
  targetSeq.value === null ? uiText("関連づけるチケットを選んでください") : null,
)

const canSave = computed(() => targetError.value === null && props.busy !== true)

function errorFor(field: string): string | undefined {
  return props.fieldErrors?.[field]
}

function submit(): void {
  touched.value = true
  if (!canSave.value || targetSeq.value === null) return
  emit('save', { targetSeq: targetSeq.value, choice: choice.value })
}
</script>

<template>
  <Modal :title="$ui('関連チケットを追加')" @close="emit('close')">
    <form id="ticket-link-form" class="form" @submit.prevent="submit">
      <div class="field">
        <span class="label">{{ $ui('関係') }} <span class="required">*</span></span>
        <!-- **ラジオで出す。** 4つしかなく、`hint` の1行を各項目に添えたい
             ——`<option>` に入れると幅で切れる（5.5 の状態ドロップダウンと同じ理由） -->
        <div class="choices">
          <label v-for="c in linkChoices" :key="c.value" class="choice">
            <input v-model="choice" type="radio" name="link-choice" :value="c.value" />
            <span class="choice-body">
              <span class="choice-label">{{ c.label }}</span>
              <span class="choice-hint">{{ c.hint }}</span>
            </span>
          </label>
        </div>
        <span v-if="errorFor('link_type')" class="detail">✕ {{ errorFor('link_type') }}</span>
      </div>

      <div class="field">
        <span class="label">{{ $ui('相手のチケット') }} <span class="required">*</span></span>
        <input
          v-model="filter"
          type="text"
          class="filter"
          :placeholder="$ui('ID・タイトルで絞り込む')"
          autocapitalize="off"
          autocomplete="off"
        />
        <div class="candidates" role="listbox" :aria-label="$ui('関連づけるチケット')">
          <label v-for="c in filtered" :key="c.seq" class="candidate">
            <input v-model="targetSeq" type="radio" name="link-target" :value="c.seq" />
            <span class="type-icon" :title="ticketTypeLabels[c.type]" aria-hidden="true">
              {{ ticketTypeIcons[c.type] }}
            </span>
            <code class="candidate-id">{{ projectKey }}-{{ c.seq }}</code>
            <span class="candidate-title">{{ c.title }}</span>
            <span class="candidate-status">{{ statusLabel(c.status.key, c.status.name) }}</span>
          </label>
          <p v-if="filtered.length === 0" class="empty">
            {{ candidates.length === 0 ? $ui("関連づけられるチケットがありません") : $ui("一致するチケットがありません") }}
          </p>
        </div>
        <span v-if="touched && targetError" class="detail">✕ {{ targetError }}</span>
        <span v-else-if="errorFor('target_seq')" class="detail">
          ✕ {{ errorFor('target_seq') }}
        </span>
        <span v-else class="hint">{{ $ui('いま一覧に出ているチケットから選びます') }}</span>
      </div>

      <!-- 欄に紐づかない誤り（409 `already_exists`）。**モーダルを閉じずにここへ出す**
           ——閉じてしまうと選び直しがやり直しになる（6.4） -->
      <p v-if="formError" class="form-error" role="alert">{{ formError }}</p>
    </form>

    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button type="submit" form="ticket-link-form" class="primary" :disabled="!canSave"> {{ $ui('追加') }} </button>
    </template>
  </Modal>
</template>

<style scoped>
.form {
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
  color: var(--pb-danger-text);
}

/* ── 関係の選択 ───────────────────────────────────────────── */

/* **2列に畳む。** 4つを縦に積むと候補一覧が下へ押し出される。
   モーダルの幅は固定なので、**基準値の合計＋gap がその幅に入ることを確かめてある**
   （6.7。1fr 2つなので器の幅に従う） */
.choices {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--pb-space-2);
}

.choice {
  display: flex;
  align-items: flex-start;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  cursor: pointer;
}

.choice:hover {
  background: var(--pb-hover);
}

.choice:has(input:checked) {
  border-color: var(--pb-accent);
}

.choice-body {
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 0;
}

.choice-label {
  font-size: 13px;
  font-weight: 600;
}

.choice-hint {
  color: var(--pb-text-muted);
  font-size: 11px;
  line-height: 1.4;
}

/* ── 相手の候補 ───────────────────────────────────────────── */

.filter {
  width: 100%;
  height: 34px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

/* **高さを固定してこの中だけスクロールさせる。** 200件がモーダルを
   画面外まで伸ばさないようにする */
.candidates {
  overflow-y: auto;
  max-height: 260px;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

.candidate {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
  cursor: pointer;
}

.candidate:last-of-type {
  border-bottom: none;
}

.candidate:hover {
  background: var(--pb-hover);
}

.candidate:has(input:checked) {
  background: var(--pb-active);
}

.type-icon {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.candidate-id {
  flex: none;
  color: var(--pb-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}

/* **主役には `flex: 1 1 auto` と `min-width` を添える**（6.7）。
   添えないと隣の `flex: none` が残って、タイトルのほうが先に0幅まで潰れる */
.candidate-title {
  flex: 1 1 auto;
  overflow: hidden;
  min-width: 6em;
  font-size: 13px;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.candidate-status {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.empty {
  margin: 0;
  padding: var(--pb-space-4);
  color: var(--pb-text-muted);
  font-size: 13px;
  text-align: center;
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

.hint {
  color: var(--pb-text-muted);
  font-size: 12px;
}

.form-error {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-danger-bg);
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
