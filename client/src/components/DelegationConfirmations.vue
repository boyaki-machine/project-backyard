<script setup lang="ts">
/**
 * ユーザ確認待ち（GuiDesign.md 5.5「自律実行」、pb-252）。
 *
 * **人の対応が要るものだけを集めたセクション**で、メタ情報の後・説明の前に置く。
 * 対応が要るものが無いときはセクションごと出さない（空の見出しを置かない）。
 *
 * - **計画の承認**：計画中で、最新の計画の版が判定を待っているとき。評価の結果を
 *   並べ、承認は評価が合格したときだけ押せる（Delegation.md 6.1「評価が先、PM が後」）。
 *   却下は評価を待たずにできる
 * - **確認への回答**：開いている確認待ち（ヒアリングを含む）。制約の超過などで
 *   一時停止したものは、答えた後に「再開…」で再開する（答えるだけでは再開しない。8.2）
 *
 * **URL が `#confirmations` で終わるときは、開いたときに一度だけこのセクションまで
 * スクロールする**（確認待ちの通知の遷移先。Delegation.md 8.2）。
 */
import { computed, nextTick, ref, watch } from 'vue'
import { useRoute } from 'vue-router'

import {
  answerConfirmation,
  approveDelegation,
  confirmationReasonLabels,
  evaluationAspectLabels,
  evaluationVerdictLabels,
  rejectDelegation,
  type DelegationStatus,
  type OpenConfirmation,
} from '../api/delegation'
import { delegationErrorText } from '../composables/useDelegationStatus'
import { renderMarkdown } from '../lib/markdown'
import { uiText } from '../locales/ui'
import DelegationTextModal from './DelegationTextModal.vue'

const props = defineProps<{
  projectKey: string
  seq: number
  status: DelegationStatus | null
  canManage: boolean
}>()

const emit = defineEmits<{ changed: [] }>()

const plan = computed(() => {
  const s = props.status
  const p = s?.latest_plan
  if (!s || s.status !== 'planning' || !p || p.status !== 'proposed') return null
  return p
})

const planBody = computed(() => renderMarkdown(plan.value?.body_md ?? ''))
const planPassed = computed(() => plan.value?.evaluation?.verdict === 'pass')

type Finding = { aspect?: string; verdict?: string; note_md?: string }
const findings = computed<Finding[]>(() => (plan.value?.evaluation?.findings ?? []) as Finding[])

const confirmations = computed<OpenConfirmation[]>(() => props.status?.open_confirmations ?? [])
const count = computed(() => (plan.value ? 1 : 0) + confirmations.value.length)

// 通知から来たとき（#confirmations）だけ、チケットごとに一度だけ寄せる。答えた後の
// 読み直しで、また引き戻さないようにする。
const route = useRoute()
const section = ref<HTMLElement | null>(null)
let scrolledSeq: number | null = null
watch(
  [count, () => props.seq],
  async ([n, seq]) => {
    if (n === 0 || scrolledSeq === seq || route.hash !== '#confirmations') return
    scrolledSeq = seq
    await nextTick()
    section.value?.scrollIntoView({ block: 'start' })
  },
  { immediate: true },
)

const busy = ref(false)
const error = ref<string | null>(null)
const rejectOpen = ref(false)
const answering = ref<OpenConfirmation | null>(null)

async function run(op: () => Promise<unknown>, done: () => void): Promise<void> {
  busy.value = true
  error.value = null
  try {
    await op()
    done()
    emit('changed')
  } catch (e) {
    error.value = delegationErrorText(e)
  } finally {
    busy.value = false
  }
}

function approve(): void {
  const p = plan.value
  if (!p?.version) return
  void run(() => approveDelegation(props.projectKey, props.seq, p.version!), () => {})
}

function reject(reason: string): void {
  const p = plan.value
  if (!p?.version) return
  void run(() => rejectDelegation(props.projectKey, props.seq, p.version!, reason), () => {
    rejectOpen.value = false
  })
}

function answer(text: string): void {
  const c = answering.value
  if (!c?.id) return
  void run(() => answerConfirmation(props.projectKey, props.seq, c.id!, text), () => {
    answering.value = null
  })
}

function reasonLabel(reason: string | undefined): string {
  return (reason && confirmationReasonLabels[reason]) || uiText('確認')
}
</script>

<template>
  <section v-if="count > 0" id="confirmations" ref="section" class="block confirmations">
    <h3 class="block-title">{{ $ui('ユーザ確認待ち（{value0}）', { value0: count }) }}</h3>

    <article v-if="plan" class="card">
      <header class="card-head">
        <span class="card-title">{{ $ui('計画の承認 ─ 計画 第{value0}版', { value0: plan.version ?? 0 }) }}</span>
        <span v-if="plan.evaluation" class="verdict" :data-verdict="plan.evaluation.verdict">
          {{ $ui('評価：') }}{{ evaluationVerdictLabels[plan.evaluation.verdict ?? ''] ?? '' }}
        </span>
        <span v-else class="verdict pending">{{ $ui('評価：まだです') }}</span>
      </header>
      <ul v-if="findings.length > 0" class="findings">
        <li v-for="(f, i) in findings" :key="i">
          <span class="aspect">{{ evaluationAspectLabels[f.aspect ?? ''] ?? f.aspect }}</span>
          <span :data-verdict="f.verdict">{{ f.verdict === 'pass' ? '✓' : '✕' }}</span>
          <span v-if="f.note_md" class="note">{{ f.note_md }}</span>
        </li>
      </ul>
      <p v-if="plan.reason_md" class="muted">{{ $ui('変えた理由：') }}{{ plan.reason_md }}</p>
      <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
      <div v-if="planBody" class="markdown-body plan-body" v-html="planBody"></div>
      <div v-if="canManage" class="actions">
        <span v-if="!planPassed" class="muted">{{ $ui('承認できるのは、評価が合格した後です') }}</span>
        <button type="button" class="secondary" :disabled="busy" @click="rejectOpen = true">{{ $ui('却下…') }}</button>
        <button type="button" class="primary" :disabled="busy || !planPassed" @click="approve">{{ $ui('承認') }}</button>
      </div>
    </article>

    <article v-for="c in confirmations" :key="c.id" class="card">
      <header class="card-head">
        <span class="card-title">{{ reasonLabel(c.reason) }}</span>
        <span v-if="c.raised_by?.display_name" class="muted">🤖 {{ c.raised_by.display_name }}</span>
      </header>
      <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
      <div class="markdown-body question" v-html="renderMarkdown(c.question_md ?? '')"></div>
      <div v-if="canManage" class="actions">
        <button type="button" class="primary" :disabled="busy" @click="answering = c">{{ $ui('回答する…') }}</button>
      </div>
    </article>

    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </section>

  <DelegationTextModal
    v-if="rejectOpen"
    :title="$ui('計画を却下しますか？')"
    :lead="$ui('mainエージェントが理由を読んで、計画を出し直します')"
    :label="$ui('却下の理由')"
    :placeholder="$ui('どこを直してほしいか')"
    required
    :submit-label="$ui('却下する')"
    :busy="busy"
    :error="error"
    @close="rejectOpen = false"
    @submit="reject"
  />
  <DelegationTextModal
    v-if="answering"
    :title="reasonLabel(answering.reason)"
    :question-md="answering.question_md"
    :label="$ui('回答')"
    required
    :submit-label="$ui('回答する')"
    :busy="busy"
    :error="error"
    @close="answering = null"
    @submit="answer"
  />
</template>

<style scoped>
/* 見出しは詳細ペインの `.block-title` と同じ見た目（あちらは scoped で子に効かない） */
.block-title {
  padding-bottom: var(--pb-space-1);
  margin: 0 0 var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
}

.confirmations {
  margin-top: var(--pb-space-4);
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
}

.card {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-warning-border);
  border-radius: var(--pb-radius);
  background: var(--pb-warning-bg);
  font-size: 13px;
}

.card-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--pb-space-1) var(--pb-space-3);
}

.card-title {
  font-weight: 600;
}

.verdict[data-verdict='fail'],
[data-verdict='fail'] {
  color: var(--pb-danger-text);
}

.findings {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-1) var(--pb-space-4);
  padding: 0;
  margin: 0;
  list-style: none;
}

.findings li {
  display: flex;
  gap: var(--pb-space-1);
  min-width: 0;
}

.aspect,
.muted {
  color: var(--pb-text-muted);
}

.muted {
  margin: 0;
}

.note {
  overflow-wrap: anywhere;
}

.plan-body {
  max-height: 16em;
  overflow: auto;
}

.question {
  overflow-wrap: anywhere;
}

.question :deep(p),
.question :deep(ul) {
  margin: 0 0 var(--pb-space-1);
}

.actions {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--pb-space-2);
}

.field-error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
