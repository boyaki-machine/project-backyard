<script setup lang="ts">
/**
 * 実行モードが「自律実行」のときに、実行モードの下に開く項目
 * （GuiDesign.md 5.5「自律実行」、pb-252）。
 *
 * 2つの姿を持つ。
 *
 * - **始める前**（`setupOpen`）：mainエージェント・評価エージェント・制約を入れて
 *   「自律実行を始める」。実行モードの選択肢で「自律実行」を選ぶと開く
 * - **始めた後**（実行モードが `delegated`）：状態・エージェント・制約・エージェント
 *   ステイタス・参考の使用状況・エージェントの呼び方・一時停止と再開
 *
 * **画面に設計の語を出さない**（委任の根・委任範囲など。pb-252 で PM と合意）。
 * 計画の承認とユーザ確認待ちへの回答は、ここではなく DelegationConfirmations に置く
 * ——説明の前に独立したセクションとして出す（人の対応が要るものは目に付く場所に）。
 */
import { computed, onMounted, ref } from 'vue'

import {
  agentCallPhrase,
  constraintsOf,
  metricsOf,
  createDelegation,
  delegationStatusLabels,
  pauseDelegation,
  resumeDelegation,
  withdrawDelegation,
  type DelegationConstraints,
  type DelegationStatus,
} from '../api/delegation'
import { listAgents, type MyAgent } from '../api/me'
import { delegationErrorText } from '../composables/useDelegationStatus'
import { uiNumber, uiText } from '../locales/ui'
import DelegationConstraintsEditor from './DelegationConstraintsEditor.vue'
import DelegationResumeModal from './DelegationResumeModal.vue'
import DelegationTextModal from './DelegationTextModal.vue'
import DelegationWithdrawModal from './DelegationWithdrawModal.vue'
import DelegationHistoryModal from './DelegationHistoryModal.vue'
import DelegationMetricsEditor, { type MetricsDraft } from './DelegationMetricsEditor.vue'
import DelegationSummaryModal from './DelegationSummaryModal.vue'

const props = defineProps<{
  projectKey: string
  seq: number
  /** 実行モードが自律実行（始めた後）か */
  delegated: boolean
  /** 始める前の入力欄を開いているか */
  setupOpen: boolean
  /** delegation.manage を持つか（始める・一時停止・再開） */
  canManage: boolean
  status: DelegationStatus | null
  statusError: string | null
}>()

const emit = defineEmits<{
  /** 始める前の入力を取りやめた */
  cancelSetup: []
  /** 状態が変わった（チケットと自律実行の状態を読み直す） */
  changed: []
}>()

// ── エージェント（PM 自身のもの。/me/agents）──────────────────────

const agents = ref<MyAgent[]>([])
const agentsError = ref<string | null>(null)

onMounted(async () => {
  if (!props.canManage) return
  try {
    agents.value = (await listAgents()).items.filter((a) => a.is_active)
  } catch (e) {
    agentsError.value = delegationErrorText(e)
  }
})

function clientKindOf(actorID: string | undefined): string {
  return agents.value.find((a) => a.id === actorID)?.client_kind ?? ''
}

// ── 始める前 ─────────────────────────────────────────────

const mainID = ref('')
const evaluatorID = ref('')
const constraints = ref<DelegationConstraints>({ budget: {}, stop_conditions: [''] })
const metricsDraft = ref<MetricsDraft>({ metrics: [], report_every_days: '' })
const busy = ref(false)
const actionError = ref<string | null>(null)

const setupProblem = computed(() => {
  if (mainID.value === '' || evaluatorID.value === '') return uiText('mainエージェントと評価エージェントを選んでください')
  if (mainID.value === evaluatorID.value) return uiText('mainエージェントと評価エージェントには別のエージェントを選んでください')
  return null
})

async function start(): Promise<void> {
  if (setupProblem.value) return
  busy.value = true
  actionError.value = null
  try {
    const stops = constraints.value.stop_conditions.map((c) => c.trim()).filter((c) => c !== '')
    const scope: Record<string, unknown> = { budget: constraints.value.budget }
    if (stops.length > 0) scope.stop_conditions = stops
    // 指標と要約の周期（4.2・8.3。pb-277）。名前が空の行は落とし、空の単位・目標は送らない
    const metrics = metricsDraft.value.metrics
      .filter((m) => m.name.trim() !== '')
      .map((m) => ({
        key: m.key,
        name: m.name.trim(),
        direction: m.direction,
        ...(m.unit?.trim() ? { unit: m.unit.trim() } : {}),
        ...(m.target !== undefined && m.target !== null ? { target: m.target } : {}),
      }))
    if (metrics.length > 0) scope.metrics = metrics
    if (String(metricsDraft.value.report_every_days).trim() !== '') {
      scope.report_every_days = Number(metricsDraft.value.report_every_days)
    }
    await createDelegation(props.projectKey, props.seq, {
      main_actor_id: mainID.value,
      evaluator_actor_id: evaluatorID.value,
      scope,
    })
    emit('changed')
  } catch (e) {
    actionError.value = delegationErrorText(e)
  } finally {
    busy.value = false
  }
}

// ── 始めた後 ─────────────────────────────────────────────

const state = computed(() => props.status?.status)
const current = computed(() => constraintsOf(props.status?.scope))

/** 制約の上限を1行に（設定されたものだけ）。トークンは出さない（pb-252） */
const limitsLine = computed(() => {
  const b = current.value.budget
  const parts: string[] = []
  if (b.tickets) parts.push(uiText('子チケットの数 {value0}件まで', { value0: uiNumber(b.tickets) }))
  if (b.depth) parts.push(uiText('子チケットの階層 {value0}段まで', { value0: b.depth }))
  if (b.due_on) parts.push(uiText('期日 {value0}', { value0: b.due_on }))
  parts.push(uiText('評価のやり直し {value0}回まで', { value0: b.rounds ?? 3 }))
  return parts.join(' ・ ')
})

const usageLine = computed(() => {
  const u = props.status?.budget
  if (!u) return ''
  let tokens = uiText('申告されたトークン {value0}', { value0: uiNumber(u.tokens_total) })
  if (u.tokens_missing > 0) tokens += uiText('（申告なし {value0}件）', { value0: u.tokens_missing })
  return [
    uiText('子チケット {value0}件', { value0: uiNumber(u.tickets) }),
    uiText('階層 {value0}段', { value0: u.max_depth }),
    tokens,
  ].join(' ・ ')
})

/**
 * エージェントステイタス。**PM が開くと役割は「読むだけ」になる**ので、
 * main の次の一手（`main_next_move`）を出す。止まっているときは、止まっている
 * ことを先に言う（エージェントは何もしていない）。
 */
const agentStatus = computed(() => {
  const s = props.status
  if (!s) return ''
  if (s.status === 'paused') return uiText('一時停止中です。再開を待っています')
  if (s.status === 'ended') return uiText('終了しました')
  if ((s.open_confirmations ?? []).some((c) => c.reason !== 'hearing')) {
    return uiText('あなたの回答を待っています（下のユーザ確認待ち）')
  }
  // **計画中の次の一手はサーバでは近似で、計画の状態を見ない**（pb-251 の finding）。
  // 計画が出ているなら、誰を待っているかを画面の側で言う
  const p = s.latest_plan
  if (s.status === 'planning' && p?.status === 'proposed') {
    if (!p.evaluation) return uiText('評価エージェントが計画を評価するのを待っています')
    if (p.evaluation.verdict === 'pass') return uiText('あなたの計画の承認を待っています（下のユーザ確認待ち）')
  }
  return s.main_next_move ?? ''
})

const showCall = ref(false)
const copied = ref<string | null>(null)

const calls = computed(() => {
  const s = props.status
  if (!s) return []
  return [
    {
      role: uiText('mainエージェント'),
      name: s.main?.display_name ?? '',
      phrase: agentCallPhrase(clientKindOf(s.main?.id), 'main', props.seq),
    },
    {
      role: uiText('評価エージェント'),
      name: s.evaluator?.display_name ?? '',
      phrase: agentCallPhrase(clientKindOf(s.evaluator?.id), 'evaluator', props.seq),
    },
  ]
})

async function copy(text: string): Promise<void> {
  try {
    await navigator.clipboard.writeText(text)
    copied.value = text
  } catch {
    copied.value = null
  }
}

const pauseOpen = ref(false)
const resumeOpen = ref(false)
const withdrawOpen = ref(false)
const historyOpen = ref(false)
const summaryOpen = ref(false)
/** 指標の名前と要約の周期（4.2・8.3。pb-277） */
const metricNames = computed(() => metricsOf(props.status?.scope).map((m) => m.name))
const reportEveryDays = computed(() => Number((props.status?.scope as { report_every_days?: number } | null)?.report_every_days ?? 0))

const canEditConstraintsOnResume = computed(() => {
  const s = props.status
  if (!s || !s.scope_version) return false
  const p = s.latest_plan
  const pendingScope = p?.status === 'proposed' && (p.declared_class === 'scope' || p.measured_class === 'scope')
  return !pendingScope
})

async function pause(reason: string): Promise<void> {
  busy.value = true
  actionError.value = null
  try {
    await pauseDelegation(props.projectKey, props.seq, reason)
    pauseOpen.value = false
    emit('changed')
  } catch (e) {
    actionError.value = delegationErrorText(e)
  } finally {
    busy.value = false
  }
}

async function withdraw(body: Parameters<typeof withdrawDelegation>[2]): Promise<void> {
  busy.value = true
  actionError.value = null
  try {
    await withdrawDelegation(props.projectKey, props.seq, body)
    withdrawOpen.value = false
    emit('changed')
  } catch (e) {
    actionError.value = delegationErrorText(e)
  } finally {
    busy.value = false
  }
}

async function resume(body: Parameters<typeof resumeDelegation>[2]): Promise<void> {
  busy.value = true
  actionError.value = null
  try {
    await resumeDelegation(props.projectKey, props.seq, body)
    resumeOpen.value = false
    emit('changed')
  } catch (e) {
    actionError.value = delegationErrorText(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <!-- 始める前 -->
  <div v-if="!delegated && setupOpen" class="delegation">
    <p class="lead">
      {{ $ui('mainエージェントが計画を立てて作業を進め、評価エージェントがその計画と成果を確かめます。目的はこのチケットの説明と完了条件、触ってよい場所はスコープ境界です') }}
    </p>
    <div class="row">
      <label class="pick">
        <span class="name">{{ $ui('mainエージェント') }}</span>
        <select v-model="mainID" :disabled="busy">
          <option value="">{{ $ui('選んでください') }}</option>
          <option v-for="a in agents" :key="a.id" :value="a.id">{{ a.display_name }}</option>
        </select>
      </label>
      <label class="pick">
        <span class="name">{{ $ui('評価エージェント') }}</span>
        <select v-model="evaluatorID" :disabled="busy">
          <option value="">{{ $ui('選んでください') }}</option>
          <option v-for="a in agents" :key="a.id" :value="a.id">{{ a.display_name }}</option>
        </select>
      </label>
    </div>
    <p class="hint">
      {{ $ui('選べるのは、あなたが登録したエージェントです。評価エージェントのトークンには、発行のときに評価の権限（delegation.evaluate）を足してください') }}
    </p>
    <p v-if="agentsError" class="field-error" role="alert">{{ agentsError }}</p>
    <DelegationConstraintsEditor v-model="constraints" :disabled="busy" />
    <DelegationMetricsEditor v-model="metricsDraft" :disabled="busy" />
    <p v-if="actionError" class="field-error" role="alert">{{ actionError }}</p>
    <div class="actions">
      <button type="button" class="secondary" :disabled="busy" @click="emit('cancelSetup')">{{ $ui('取りやめる') }}</button>
      <button
        type="button"
        class="primary"
        :disabled="busy || setupProblem !== null"
        :title="setupProblem ?? ''"
        @click="start"
      >
        {{ $ui('自律実行を始める') }}
      </button>
    </div>
  </div>

  <!-- 始めた後 -->
  <div v-else-if="delegated" class="delegation">
    <p v-if="statusError" class="field-error" role="alert">{{ statusError }}</p>
    <template v-if="status">
      <dl class="items">
        <div class="item">
          <dt>{{ $ui('状態') }}</dt>
          <dd>
            <span class="state" :data-state="state">{{ state ? delegationStatusLabels[state] : '' }}</span>
          </dd>
        </div>
        <div class="item">
          <dt>{{ $ui('エージェント') }}</dt>
          <dd class="agents">
            <span>main 🤖 {{ status.main?.display_name }}</span>
            <span>{{ $ui('評価') }} 🤖 {{ status.evaluator?.display_name }}</span>
          </dd>
        </div>
        <div class="item">
          <dt>{{ $ui('制約') }}</dt>
          <dd class="stack">
            <span>{{ limitsLine }}</span>
            <ul v-if="current.stop_conditions.length > 0" class="stops">
              <li v-for="(c, i) in current.stop_conditions" :key="i">
                {{ $ui('止まる条件：') }}{{ c }}
              </li>
            </ul>
          </dd>
        </div>
        <div class="item">
          <dt>{{ $ui('エージェントステイタス') }}</dt>
          <dd>{{ agentStatus }}</dd>
        </div>
        <div v-if="usageLine" class="item">
          <dt>{{ $ui('参考') }}</dt>
          <dd class="muted">{{ usageLine }}</dd>
        </div>
        <div v-if="metricNames.length > 0 || reportEveryDays > 0" class="item">
          <dt>{{ $ui('指標と要約') }}</dt>
          <dd>
            <span class="muted">
              {{ metricNames.join(' ・ ') }}<template v-if="metricNames.length > 0 && reportEveryDays > 0"> ／ </template>
              <template v-if="reportEveryDays > 0">{{ $ui('{value0}日ごとに要約', { value0: reportEveryDays }) }}</template>
            </span>
            <button type="button" class="link" @click="summaryOpen = true">{{ $ui('周期の要約を見る') }}</button>
          </dd>
        </div>
        <div v-if="status?.latest_plan" class="item">
          <dt>{{ $ui('履歴') }}</dt>
          <dd>
            <span class="muted">{{ $ui('計画 第{value0}版まで', { value0: status.latest_plan.version }) }}</span>
            <button type="button" class="link" @click="historyOpen = true">{{ $ui('計画と評価の履歴を見る') }}</button>
          </dd>
        </div>
        <div class="item">
          <dt>{{ $ui('エージェントの呼び方') }}</dt>
          <dd class="stack">
            <button type="button" class="link" :aria-expanded="showCall" @click="showCall = !showCall">
              {{ showCall ? $ui('閉じる') : $ui('表示する') }}
            </button>
            <template v-if="showCall">
              <p class="hint">
                {{ $ui('PB はエージェントを自分では起動しません。それぞれのエージェントのセッションを開き、次の一言を伝えてください。途中で止めた後も、同じ一言で続きから再開します') }}
              </p>
              <ul class="calls">
                <li v-for="c in calls" :key="c.role" class="call">
                  <span class="name">{{ c.role }}（{{ c.name }}）</span>
                  <code class="phrase">{{ c.phrase }}</code>
                  <button type="button" class="secondary small" @click="copy(c.phrase)">
                    {{ copied === c.phrase ? $ui('コピーしました') : $ui('コピー') }}
                  </button>
                </li>
              </ul>
            </template>
          </dd>
        </div>
      </dl>
      <p v-if="actionError" class="field-error" role="alert">{{ actionError }}</p>
      <div v-if="canManage && state !== 'ended'" class="actions">
        <button type="button" class="secondary" :disabled="busy" @click="withdrawOpen = true">
          {{ $ui('自律実行をやめる…') }}
        </button>
        <button
          v-if="state === 'paused'"
          type="button"
          class="primary"
          :disabled="busy"
          @click="resumeOpen = true"
        >
          {{ $ui('再開…') }}
        </button>
        <button v-else type="button" class="secondary" :disabled="busy" @click="pauseOpen = true">
          {{ $ui('一時停止') }}
        </button>
      </div>
    </template>
  </div>

  <DelegationTextModal
    v-if="pauseOpen"
    :title="$ui('自律実行を一時停止しますか？')"
    :lead="$ui('止めているあいだ、エージェントはこのチケットの木に書き込めません。再開はあなたが行います')"
    :label="$ui('理由（任意）')"
    :submit-label="$ui('一時停止する')"
    :busy="busy"
    :error="actionError"
    @close="pauseOpen = false"
    @submit="pause"
  />
  <DelegationSummaryModal
    v-if="summaryOpen"
    :project-key="projectKey"
    :seq="seq"
    :can-record="canManage && state !== 'ended'"
    @close="summaryOpen = false"
    @changed="emit('changed')"
  />
  <DelegationHistoryModal
    v-if="historyOpen"
    :project-key="projectKey"
    :seq="seq"
    :can-manage="canManage"
    :ended="state === 'ended'"
    @close="historyOpen = false"
    @changed="emit('changed')"
  />
  <DelegationWithdrawModal
    v-if="withdrawOpen"
    :busy="busy"
    :error="actionError"
    @close="withdrawOpen = false"
    @submit="withdraw"
  />
  <DelegationResumeModal
    v-if="resumeOpen && status"
    :scope="status.scope"
    :can-edit-constraints="canEditConstraintsOnResume"
    :busy="busy"
    :error="actionError"
    @close="resumeOpen = false"
    @submit="resume"
  />
</template>

<style scoped>
.delegation {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-2) 0 var(--pb-space-2) var(--pb-space-3);
  margin-top: var(--pb-space-1);
  border-left: 2px solid var(--pb-line);
}

.lead {
  margin: 0;
  font-size: 13px;
}

.row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-2) var(--pb-space-4);
}

.pick {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
}

.pick select {
  min-width: 0;
  max-width: 16em;
}

.name {
  color: var(--pb-text-muted);
  font-size: 13px;
  white-space: nowrap;
}

.items {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  margin: 0;
}

.item {
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-2);
  min-width: 0;
  font-size: 13px;
}

.item dt {
  flex: none;
  width: 11.5em;
  white-space: nowrap;
  color: var(--pb-text-muted);
}

.item dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.agents {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-1) var(--pb-space-4);
}

.stack {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: var(--pb-space-1);
}

.stops,
.calls {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  padding: 0;
  margin: 0;
  list-style: none;
}

.call {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-1) var(--pb-space-2);
}

.phrase {
  padding: 0 var(--pb-space-1);
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
  font-family: var(--pb-font-mono);
  font-size: 12px;
}

.state {
  font-weight: 600;
}

.state[data-state='paused'] {
  color: var(--pb-warning-text);
}

.muted,
.hint {
  color: var(--pb-text-muted);
}

.hint {
  margin: 0;
  font-size: 12px;
}

.link {
  padding: 0;
  border: none;
  background: none;
  color: var(--pb-accent);
  font-size: 13px;
  cursor: pointer;
}

.small {
  padding: 0 var(--pb-space-2);
  font-size: 12px;
}

.actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--pb-space-2);
}

.field-error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}

/* ペインが狭いときは見出しを上に積む（5.14 のガントの浮かせたペインは 320〜560px） */
@container (max-width: 479px) {
  .item {
    flex-direction: column;
    gap: 0;
  }
}
</style>
