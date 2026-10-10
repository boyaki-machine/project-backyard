<script setup lang="ts">
/**
 * 計画と評価の履歴（`GuiDesign.md` 5.5「自律実行」、pb-274）。
 *
 * **計画の版**：左に版の一覧、右に選んだ版。右では比べる版を選び、骨格（JSON を整形したもの）と
 * 説明の差分を行ごとに出す。PM は前の版へ差し戻せ（6.2）、評価が合格させた構造の変更には
 * 異議を出せる（6.4。pb-275。出せるかはサーバが `objectable` で返す）。
 *
 * **評価の記録**：新しい順に、対象・往復・合否・観点ごとの判定・評価者と main の種類とモデル。
 *
 * 差し戻し・異議は、右の下で理由を書いて確かめてから送る（モーダルを重ねない）。
 * 送ったら一覧を読み直し、親に `changed` を伝える（自律実行の状態も変わる）。
 */
import { computed, onMounted, ref, watch } from 'vue'

import Modal from './Modal.vue'
import {
  evaluationAspectLabels,
  evaluationVerdictLabels,
  listDelegationEvaluations,
  listDelegationPlans,
  objectDelegationPlan,
  planClassLabels,
  planStatusLabels,
  revertDelegationPlan,
  type EvaluationHistoryItem,
  type PlanHistoryItem,
} from '../api/delegation'
import { formatDateTime } from '../lib/datetime'
import { lineDiff } from '../lib/lineDiff'
import { delegationErrorText } from '../composables/useDelegationStatus'
import { uiText } from '../locales/ui'

const props = defineProps<{
  projectKey: string
  seq: number
  /** delegation.manage を持つか（差し戻し・異議） */
  canManage: boolean
  /** 自律実行が終了しているか（終了したら操作を出さない） */
  ended: boolean
}>()

const emit = defineEmits<{ close: []; changed: [] }>()

const tab = ref<'plans' | 'evaluations'>('plans')
const plans = ref<PlanHistoryItem[]>([])
const evaluations = ref<EvaluationHistoryItem[]>([])
const loading = ref(true)
const loadError = ref<string | null>(null)

async function load(): Promise<void> {
  loading.value = true
  loadError.value = null
  try {
    const [p, e] = await Promise.all([
      listDelegationPlans(props.projectKey, props.seq),
      listDelegationEvaluations(props.projectKey, props.seq),
    ])
    plans.value = p
    evaluations.value = e
    if (!plans.value.some((x) => x.version === selected.value)) selected.value = plans.value[0]?.version ?? null
  } catch (err) {
    loadError.value = delegationErrorText(err)
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ── 計画の版 ───────────────────────────────────────────────

const selected = ref<number | null>(null)
const compareTo = ref<number | null>(null)
const plan = computed(() => plans.value.find((p) => p.version === selected.value) ?? null)
const latestAccepted = computed(() => plans.value.find((p) => p.status === 'accepted')?.version ?? null)

// 比べる版の既定は、一覧で1つ前（古い側）の版。
watch(selected, (v) => {
  const i = plans.value.findIndex((p) => p.version === v)
  compareTo.value = i >= 0 ? (plans.value[i + 1]?.version ?? null) : null
  action.value = null
  actionError.value = null
})

const base = computed(() => plans.value.find((p) => p.version === compareTo.value) ?? null)
const skeletonDiff = computed(() =>
  plan.value ? lineDiff(base.value ? pretty(base.value.skeleton) : '', pretty(plan.value.skeleton)) : [],
)
const bodyDiff = computed(() => (plan.value ? lineDiff(base.value?.body_md ?? '', plan.value.body_md) : []))

function pretty(v: unknown): string {
  return JSON.stringify(v ?? null, null, 2)
}

function classOf(p: PlanHistoryItem): string | null {
  // 申告と判定の厳しい方（6.3）。
  const order = ['minor', 'structural', 'scope']
  const c = [p.declared_class, p.measured_class].filter((x): x is NonNullable<typeof x> => !!x)
  if (c.length === 0) return null
  return c.sort((a, b) => order.indexOf(b) - order.indexOf(a))[0]
}

// ── 差し戻し・異議 ─────────────────────────────────────────

const action = ref<'revert' | 'object' | null>(null)
const reason = ref('')
const busy = ref(false)
const actionError = ref<string | null>(null)
const notice = ref<string | null>(null)

/**
 * 差し戻しは、反映されたことのある版で、いまの最新の反映済みの版と違う版にだけ出す。
 * 判定待ち・却下の版へ戻すと、評価を通らずに反映されてしまう。同じ中身の版も積まない
 */
const canRevert = computed(
  () =>
    props.canManage &&
    !props.ended &&
    plan.value !== null &&
    plan.value.status === 'accepted' &&
    plan.value.version !== latestAccepted.value,
)
const canObject = computed(() => props.canManage && !props.ended && plan.value?.objectable === true)

function begin(kind: 'revert' | 'object'): void {
  action.value = kind
  reason.value = ''
  actionError.value = null
}

async function submit(): Promise<void> {
  if (!plan.value || !action.value) return
  busy.value = true
  actionError.value = null
  try {
    if (action.value === 'revert') {
      await revertDelegationPlan(props.projectKey, props.seq, plan.value.version, reason.value.trim())
      notice.value = uiText('第{value0}版へ差し戻しました', { value0: plan.value.version })
    } else {
      const r = await objectDelegationPlan(props.projectKey, props.seq, plan.value.version, reason.value.trim())
      notice.value = r.reverted
        ? uiText('異議を出し、前の版へ差し戻しました')
        : uiText('異議を記録しました。後に第{value0}版が反映されているため、差し戻しはしていません', {
            value0: r.later_version ?? '',
          })
    }
    action.value = null
    selected.value = null
    await load()
    emit('changed')
  } catch (err) {
    actionError.value = delegationErrorText(err)
  } finally {
    busy.value = false
  }
}

// ── 評価の記録 ─────────────────────────────────────────────

type Finding = { aspect?: string; verdict?: string; note_md?: string }
function findingsOf(e: EvaluationHistoryItem): Finding[] {
  return Array.isArray(e.findings) ? (e.findings as Finding[]) : []
}
function modelText(m: EvaluationHistoryItem['evaluator_model']): string {
  return [m.client_kind, m.model_name, m.model_version].filter(Boolean).join(' / ') || '—'
}
</script>

<template>
  <Modal size="wide" :title="$ui('計画と評価の履歴')" @close="emit('close')">
    <div class="tabs" role="tablist">
      <button type="button" role="tab" :aria-selected="tab === 'plans'" :class="{ on: tab === 'plans' }" @click="tab = 'plans'">
        {{ $ui('計画の版') }}（{{ plans.length }}）
      </button>
      <button
        type="button"
        role="tab"
        :aria-selected="tab === 'evaluations'"
        :class="{ on: tab === 'evaluations' }"
        @click="tab = 'evaluations'"
      >
        {{ $ui('評価の記録') }}（{{ evaluations.length }}）
      </button>
    </div>
    <p v-if="notice" class="notice" role="status">{{ notice }}</p>
    <p v-if="loading" class="note">{{ $ui('読み込み中…') }}</p>
    <p v-else-if="loadError" class="note error">✕ {{ loadError }}</p>

    <!-- 計画の版 -->
    <div v-else-if="tab === 'plans'" class="layout">
      <ul class="list">
        <li v-for="p in plans" :key="p.version">
          <button
            type="button"
            class="item"
            :class="{ selected: selected === p.version }"
            :aria-current="selected === p.version"
            @click="selected = p.version"
          >
            <span class="item-head">
              <span class="ver">{{ $ui('第{value0}版', { value0: p.version }) }}</span>
              <span class="tag" :data-status="p.status">{{ planStatusLabels[p.status] ?? p.status }}</span>
              <span v-if="classOf(p)" class="tag">{{ planClassLabels[classOf(p)!] }}</span>
            </span>
            <span class="item-sub">
              <template v-if="p.reverts_to">{{ $ui('第{value0}版への差し戻し', { value0: p.reverts_to }) }}</template>
              <template v-else-if="p.latest_verdict">
                {{ $ui('評価：') }}{{ evaluationVerdictLabels[p.latest_verdict] }}
              </template>
              <span v-if="p.pass_objected_at" class="objected">{{ $ui('異議あり') }}</span>
            </span>
            <span class="item-sub muted">{{ formatDateTime(p.submitted_at) }} · {{ p.submitted_by.display_name }}</span>
          </button>
        </li>
      </ul>

      <div class="detail">
        <p v-if="!plan" class="note">{{ $ui('左の一覧から版を選んでください') }}</p>
        <template v-else>
          <div class="detail-head">
            <h3>{{ $ui('第{value0}版', { value0: plan.version }) }}</h3>
            <label class="compare">
              {{ $ui('比べる版') }}
              <select v-model="compareTo">
                <option :value="null">{{ $ui('比べない') }}</option>
                <option v-for="p in plans.filter((x) => x.version !== plan!.version)" :key="p.version" :value="p.version">
                  {{ $ui('第{value0}版', { value0: p.version }) }}
                </option>
              </select>
            </label>
          </div>
          <p class="reason">{{ $ui('変えた理由：') }}{{ plan.reason_md || '—' }}</p>

          <h4>{{ $ui('骨格') }}</h4>
          <pre class="diff"><span v-for="(l, i) in skeletonDiff" :key="i" :data-kind="l.kind">{{ l.kind === 'add' ? '+ ' : l.kind === 'del' ? '- ' : '  ' }}{{ l.text }}
</span></pre>
          <h4>{{ $ui('計画の説明') }}</h4>
          <pre class="diff"><span v-for="(l, i) in bodyDiff" :key="i" :data-kind="l.kind">{{ l.kind === 'add' ? '+ ' : l.kind === 'del' ? '- ' : '  ' }}{{ l.text }}
</span></pre>

          <form v-if="action" class="confirm" @submit.prevent="submit">
            <p class="lead">
              {{
                action === 'revert'
                  ? $ui('第{value0}版を写した新しい版を作り、反映します。作られたチケットは消えません', { value0: plan.version })
                  : $ui('この構造の変更に異議を出します。最新の版なら、前の版へ差し戻します')
              }}
            </p>
            <label class="field">
              <span class="label">{{ $ui('理由（任意）') }}</span>
              <textarea v-model="reason" rows="2"></textarea>
            </label>
            <p v-if="actionError" class="field-error" role="alert">{{ actionError }}</p>
            <div class="actions">
              <button type="button" class="secondary" :disabled="busy" @click="action = null">{{ $ui('キャンセル') }}</button>
              <button type="submit" class="primary" :disabled="busy">
                {{ action === 'revert' ? $ui('差し戻す') : $ui('異議を出す') }}
              </button>
            </div>
          </form>
          <div v-else-if="canRevert || canObject" class="actions">
            <button v-if="canObject" type="button" class="secondary" @click="begin('object')">{{ $ui('異議…') }}</button>
            <button v-if="canRevert" type="button" class="secondary" @click="begin('revert')">
              {{ $ui('この版へ差し戻す…') }}
            </button>
          </div>
        </template>
      </div>
    </div>

    <!-- 評価の記録 -->
    <div v-else class="evals">
      <p v-if="evaluations.length === 0" class="note">{{ $ui('評価の記録はまだありません') }}</p>
      <article v-for="e in evaluations" :key="e.id" class="eval">
        <header>
          <span class="ver">
            {{ e.target_kind === 'plan' ? $ui('計画 第{value0}版', { value0: e.plan_version ?? '' }) : $ui('成果') }}
          </span>
          <span class="tag" :data-verdict="e.verdict">{{ evaluationVerdictLabels[e.verdict] ?? e.verdict }}</span>
          <span class="muted">{{ $ui('{value0}回目', { value0: e.round }) }}</span>
          <span v-if="e.objected_at" class="objected">{{ $ui('異議あり') }}</span>
          <span class="muted right">{{ formatDateTime(e.created_at) }}</span>
        </header>
        <ul v-if="findingsOf(e).length > 0" class="findings">
          <li v-for="(f, i) in findingsOf(e)" :key="i">
            <span class="aspect">{{ evaluationAspectLabels[f.aspect ?? ''] ?? f.aspect }}</span>
            <span :data-verdict="f.verdict">{{ f.verdict === 'pass' ? '✓' : '✕' }}</span>
            <span v-if="f.note_md" class="note-text">{{ f.note_md }}</span>
          </li>
        </ul>
        <dl class="models">
          <dt>{{ $ui('評価') }}</dt>
          <dd>{{ e.evaluator.display_name }}（{{ modelText(e.evaluator_model) }}）</dd>
          <dt>main</dt>
          <dd>{{ modelText(e.main_model) }}</dd>
        </dl>
      </article>
    </div>

    <template #footer>
      <button type="button" class="secondary" @click="emit('close')">{{ $ui('閉じる') }}</button>
    </template>
  </Modal>
</template>

<style scoped>
.tabs {
  display: flex;
  gap: var(--pb-space-2);
  margin-bottom: var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
}

.tabs button {
  padding: var(--pb-space-1) var(--pb-space-3);
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.tabs button.on {
  border-bottom-color: var(--pb-accent);
  color: inherit;
  font-weight: 600;
}

/* 版によって中身の長さが違うので、高さを決め打ち、両側を独立にスクロールする
   （DocRevisionsModal と同じ。6.7） */
.layout {
  display: flex;
  height: 56vh;
  min-height: 280px;
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
}

.list {
  flex: none;
  width: 240px;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  border-right: 1px solid var(--pb-line);
  list-style: none;
}

.item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 0;
  border-bottom: 1px solid var(--pb-line);
  background: none;
  color: inherit;
  font: inherit;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.item.selected {
  background: var(--pb-surface);
}

.item-head,
.item-sub {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-1);
}

.ver {
  font-weight: 600;
}

.tag {
  padding: 0 6px;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  font-size: 12px;
}

.objected {
  color: var(--pb-danger-text);
  font-size: 12px;
}

.muted {
  color: var(--pb-text-muted);
  font-size: 12px;
}

.detail {
  flex: 1 1 auto;
  min-width: 0;
  padding: var(--pb-space-3) var(--pb-space-4);
  overflow-y: auto;
}

.detail-head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-2);
}

.detail-head h3 {
  margin: 0;
  font-size: 15px;
}

.compare {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  font-size: 13px;
}

.detail h4 {
  margin: var(--pb-space-3) 0 var(--pb-space-1);
  font-size: 13px;
}

.reason {
  margin: var(--pb-space-2) 0 0;
  font-size: 13px;
  overflow-wrap: anywhere;
}

.diff {
  margin: 0;
  padding: var(--pb-space-2);
  overflow-x: auto;
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.diff [data-kind='add'] {
  display: block;
  background: var(--pb-4);
}

.diff [data-kind='del'] {
  display: block;
  background: var(--pb-danger-bg);
}

.diff [data-kind='same'] {
  display: block;
  color: var(--pb-text-muted);
}

.confirm {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  margin-top: var(--pb-space-3);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
}

.lead {
  margin: 0;
  font-size: 13px;
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

textarea {
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

.actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--pb-space-2);
  margin-top: var(--pb-space-3);
}

.confirm .actions {
  margin-top: 0;
}

.field-error,
.error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}

.note {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.notice {
  margin: 0 0 var(--pb-space-2);
  font-size: 13px;
}

.evals {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
  max-height: 56vh;
  overflow-y: auto;
}

.eval {
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  font-size: 13px;
}

.eval header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-2);
}

.right {
  margin-left: auto;
}

.findings {
  margin: var(--pb-space-2) 0 0;
  padding: 0;
  list-style: none;
}

.findings li {
  display: flex;
  gap: var(--pb-space-2);
}

.aspect {
  color: var(--pb-text-muted);
}

.note-text {
  overflow-wrap: anywhere;
}

.models {
  display: grid;
  grid-template-columns: auto 1fr;
  gap: 2px var(--pb-space-2);
  margin: var(--pb-space-2) 0 0;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.models dd {
  margin: 0;
  overflow-wrap: anywhere;
}

/* 幅が無ければ一覧と中身を縦に積む（DocRevisionsModal と同じ。6.7） */
@media (max-width: 720px) {
  .layout {
    flex-direction: column;
    height: auto;
  }

  .list {
    width: auto;
    max-height: 30vh;
    border-right: 0;
    border-bottom: 1px solid var(--pb-line);
  }
}
</style>
