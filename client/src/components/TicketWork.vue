<script setup lang="ts">
import { ref, watch } from 'vue'
import { uiText } from '../locales/ui'
import { listWork, recordWork } from '../api/work'
import type { WorkList, WorkSession, WorkEvent } from '../api/work'
const props = defineProps<{ projectKey: string; seq: number; version: number; canCorrect: boolean; canFinish: boolean }>()
const emit = defineEmits<{ updated: [] }>()
const data = ref<WorkList | null>(null)
const page = ref(1)
const error = ref('')
const busy = ref(false)
const editing = ref<WorkSession | null>(null)
const minutes = ref<string | number>('')
const tokens = ref<string | number>('')
const allocations = ref('')
const note = ref('')
const incomplete = ref(false)
let generation = 0
async function load() {
 const current = ++generation
 try { const result = await listWork(props.projectKey, props.seq, page.value); if (current === generation) { data.value = result; error.value = '' } }
 catch { if (current === generation) error.value = uiText('作業記録を取得できませんでした') }
}
watch(() => [props.projectKey, props.seq], () => { page.value = 1; data.value = null; editing.value = null; void load() }, { immediate: true })
watch(() => props.version, () => { void load() })
watch(page, () => { void load() })
const hours = (ms: number) => (ms / 3600000).toFixed(3)
function begin(row: WorkSession) {
 editing.value = row; minutes.value = String((row.reported_active_ms ?? row.active_ms) / 60000)
 tokens.value = row.tokens === null ? '' : String(row.tokens)
 allocations.value = row.allocations.map(a => `${a.seq}:${a.share}`).join(', ')
 note.value = ''; incomplete.value = row.incomplete
}
async function send(row: WorkSession, event: Omit<WorkEvent, 'request_id'>) {
 if (busy.value) return
 busy.value = true; error.value = ''
 try { await recordWork(props.projectKey, event.action === 'restore_auto' ? props.seq : row.ticket_seq, row.id, { ...event, request_id: crypto.randomUUID() }); editing.value = null; await load(); emit('updated') }
 catch { error.value = uiText('作業記録を更新できませんでした。権限と入力値を確認してください') }
 finally { busy.value = false }
}
function correct() {
 const row = editing.value; if (!row || !note.value.trim()) return
 const ms = Math.round(Number(minutes.value) * 60000)
 const tokenValue = String(tokens.value).trim() === '' ? undefined : Number(tokens.value)
 const shares = allocations.value.split(',').map(part => { const [seq, share] = part.trim().split(':'); return { seq: Number(seq), share: Number(share) } })
 if (String(minutes.value).trim() === '' || !Number.isSafeInteger(ms) || ms < 0 || ms > Math.max(row.active_ms + row.waiting_ms, (row.ended_at ?? row.changed_at) - row.started_at) || (tokenValue !== undefined && (!Number.isSafeInteger(tokenValue) || tokenValue < 0)) || shares.some(a => !Number.isInteger(a.seq) || a.seq < 1 || !Number.isFinite(a.share) || a.share <= 0)) {
  error.value = uiText('時間・トークン・配分を確認してください'); return
 }
 void send(row, { action: 'correct', note: note.value, reported_active_ms: ms, ...(tokenValue === undefined ? {} : { tokens: tokenValue }), allocations: shares, incomplete: incomplete.value })
}
async function restore(row: WorkSession) {
 if (window.confirm(uiText('手入力の実績を自動算出値に戻しますか？'))) await send(row, { action: 'restore_auto', note: uiText('手入力を自動算出へ復帰') })
}
async function finish(row: WorkSession) {
 const reason = window.prompt(uiText('中断した作業を終了する理由を入力してください'))
 if (reason?.trim()) await send(row, { action: 'abandon', incomplete: true, note: reason })
}
</script>
<template>
 <section class="work" :aria-label="$ui('作業実績')">
  <h3>{{ $ui('作業実績') }}</h3>
  <p class="muted">{{ $ui('申告した待機を除くPB計時です。思考時間の実測ではありません。Pointは変更量から算出し、材料不足なら未算出です。') }}</p>
  <p v-if="error" role="alert">{{ error }}</p>
  <template v-if="data">
   <p v-if="!data.summary.sessions_count" class="muted">{{ $ui('作業記録はありません。過去の実績は根拠を確認して実績欄へ入力してください。') }}</p>
   <template v-else>
    <p v-if="data.summary.archived_count" class="muted">{{ $ui('明細整理済み {count}件。実績値と累積集計は保持しています。', { count: data.summary.archived_count }) }}</p>
    <p v-if="data.summary.point_reason" class="muted">{{ $ui('Pointは未算出です。運営ノートの設定と累積変更量を確認してください。') }}</p>
    <dl class="summary">
     <div><dt>{{ $ui('算出時間') }}</dt><dd>{{ data.summary.suggested_hours === null ? $ui('未計測') : `${data.summary.suggested_hours.toFixed(3)} h` }} · {{ data.summary.manual_hours ? $ui('手入力を保持') : $ui('自動算出') }}</dd></div>
     <div><dt>{{ $ui('算出Point') }}</dt><dd>{{ data.summary.suggested_point ?? $ui('未計測') }} · {{ data.summary.point_version }} · {{ data.summary.manual_point ? $ui('手入力を保持') : $ui('自動算出') }}</dd></div>
     <div><dt>{{ $ui('待機時間') }}</dt><dd>{{ hours(data.summary.waiting_ms) }} h</dd></div>
     <div><dt>{{ $ui('トークン') }}</dt><dd>{{ data.summary.tokens }} · {{ $ui('未計測 {count}件', { count: data.summary.tokens_missing }) }}</dd></div>
    </dl>
    <p>{{ $ui('未終了 {open}件・不完全 {incomplete}件', { open: data.summary.open_count, incomplete: data.summary.incomplete_count }) }}</p>
    <p v-if="data.summary.open_count || data.summary.incomplete_count" class="muted">{{ $ui('未終了の時間は集計されていません。不完全な記録を含む実績は参考値です。') }}</p>
    <article v-for="row in data.items" :key="row.id" class="record">
     <p><strong>{{ row.state === 'finished' ? $ui('終了済み') : row.state === 'waiting' ? $ui('待機中') : $ui('計測中') }}</strong> · {{ row.client_kind ?? $ui('人・種別未登録') }} / {{ row.model_name ?? '—' }} / {{ row.model_version ?? '—' }} <span v-if="row.incomplete"> · {{ $ui('不完全') }}</span></p>
     <p class="muted">{{ new Date(row.started_at).toLocaleString() }} → {{ row.ended_at === null ? '—' : new Date(row.ended_at).toLocaleString() }}</p>
     <p>{{ $ui('稼働時間') }}: {{ row.time_missing ? `${hours(row.active_ms)} h · ${$ui('不完全')}` : `${hours(row.policy.time_source === 'pb' ? row.active_ms : (row.reported_active_ms ?? row.active_ms))} h` }} ({{ row.reported_active_ms === null ? $ui('PB計時') : $ui('取得元の申告') }}) · {{ $ui('待機時間') }}: {{ hours(row.waiting_ms) }} h · {{ $ui('トークン') }}: {{ row.tokens ?? $ui('未計測') }}</p>
     <p>{{ $ui('配分') }}: {{ row.allocations.map(a => `#${a.seq} ${Math.round(a.share * 100)}%`).join(', ') }}</p>
     <p v-if="row.time_missing" class="muted">{{ $ui('時間に欠測があります。確認済みの区間だけを集計しています。') }}</p>
     <p v-if="row.outcome">{{ $ui('実行結果') }}: {{ row.outcome==='failed'?$ui('失敗'):row.outcome==='abandoned'?$ui('放棄'):$ui('成功') }}</p>
     <p class="note">{{ row.note }}</p>
     <div class="actions">
      <button v-if="canCorrect && row.state === 'finished'" type="button" :disabled="busy" @click="begin(row)">{{ $ui('記録を訂正') }}</button>
      <button v-if="canCorrect && row.state === 'finished' && (data.summary.manual_hours || data.summary.manual_point)" type="button" :disabled="busy" @click="restore(row)">{{ $ui('自動算出に戻す') }}</button>
      <button v-if="canFinish && row.state !== 'finished'" type="button" :disabled="busy" @click="finish(row)">{{ $ui('不完全として終了') }}</button>
     </div>
     <form v-if="editing?.id === row.id" @submit.prevent="correct">
      <label>{{ $ui('稼働時間（分）') }}<input v-model="minutes" type="number" min="0" step="any" required /></label>
      <label>{{ $ui('トークン（空欄は変更しない）') }}<input v-model="tokens" type="number" min="0" step="1" /></label>
      <label>{{ $ui('配分（番号:比率、カンマ区切り）') }}<input v-model="allocations" required /></label>
      <label>{{ $ui('訂正理由・取得元') }}<textarea v-model="note" maxlength="2000" required /></label>
      <label class="check"><input v-model="incomplete" type="checkbox" />{{ $ui('不完全な記録') }}</label>
      <div class="actions"><button type="submit" :disabled="busy">{{ $ui('保存') }}</button><button type="button" @click="editing = null">{{ $ui('キャンセル') }}</button></div>
     </form>
    </article>
    <nav v-if="data.total > data.per_page" class="actions" :aria-label="$ui('作業記録のページ')"><button :disabled="page === 1" @click="page--">{{ $ui('前へ') }}</button><span>{{ page }} / {{ Math.ceil(data.total / data.per_page) }}</span><button :disabled="page * data.per_page >= data.total" @click="page++">{{ $ui('次へ') }}</button></nav>
   </template>
  </template>
 </section>
</template>
<style scoped>
.work { margin: 1rem 0; overflow-wrap: anywhere; }
h3 { font-size: 1rem; margin-bottom: .5rem; }
p { margin: .5rem 0; }
.muted { color: var(--pb-text-muted); font-size: .85rem; }
.summary { display: grid; grid-template-columns: repeat(auto-fit,minmax(180px,1fr)); gap: .6rem; }
dd { margin: .25rem 0 0; } dt { font-size: .85rem; color: var(--pb-text-muted); }
.record { border-top: 1px solid var(--pb-border); padding: .7rem 0; }
.note { white-space: pre-wrap; }
.actions { display: flex; flex-wrap: wrap; align-items: center; gap: .5rem; }
button { padding: .35rem .6rem; border: 1px solid var(--pb-border); border-radius: var(--pb-radius); background: var(--pb-surface); color: inherit; font: inherit; font-size: .85rem; cursor: pointer; }
button:hover:not(:disabled) { background: var(--pb-hover); }
button:disabled { opacity: .5; cursor: default; }
form { display: grid; gap: .65rem; margin-top: .75rem; }
label { display: grid; gap: .25rem; }
input, textarea { width: 100%; min-width: 0; box-sizing: border-box; padding: .4rem; border: 1px solid var(--pb-border); border-radius: var(--pb-radius); background: var(--pb-surface); color: inherit; font: inherit; }
.check { display: flex; align-items: center; } .check input { width: auto; }
</style>
