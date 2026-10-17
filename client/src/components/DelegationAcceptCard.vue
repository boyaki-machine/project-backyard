<script setup lang="ts">
/**
 * 成果の受け入れ（GuiDesign.md 5.5「自律実行」、Delegation.md 4.3「受け入れ」。pb-340）。
 *
 * 成果の評価に合格した後に PM がすることを、1枚のカードに順に並べる。
 * 1 成果を確かめる → 2 マージを承認する → 3 子を完了にしてから根を完了にする（受け入れ）。
 *
 * **新しい API は使わない。** 子をまとめて完了にするのは、子ごとに遷移先（9.7）を読み、
 * 完了へ直接進めるものだけを1件ずつ進める（バックログの複数選択と同じ型。5.4）。
 * 進められない子は理由を添えて残し、開いて進めてもらう。根の完了も同じく遷移先を読み、
 * 押せないときはサーバの理由（検証7〜9）をそのまま出す。
 */
import { computed, ref, watch } from 'vue'

import { type DelegationStatus } from '../api/delegation'
import { listTransitions, transitionTicket, type TicketTransitionOption } from '../api/tickets'
import { delegationErrorText } from '../composables/useDelegationStatus'
import { uiText } from '../locales/ui'

type Child = { seq: number; title: string; status: { category: string } }

const props = defineProps<{
  projectKey: string
  seq: number
  status: DelegationStatus
  children: Child[]
  canTransition: boolean
}>()

const emit = defineEmits<{ changed: [] }>()

const openChildren = computed(() => props.children.filter((c) => c.status.category !== 'done'))

/** マージの確認待ち（8.1 の2）が開いているか、答えたか */
const mergeState = computed<'open' | 'answered' | 'none'>(() => {
  if ((props.status.open_confirmations ?? []).some((c) => c.reason === 'irreversible_external')) return 'open'
  if ((props.status.recent_answers ?? []).some((a) => a.reason === 'irreversible_external')) return 'answered'
  return 'none'
})

const busy = ref(false)
const error = ref<string | null>(null)
/** まとめて完了にした結果。完了へ直接進められなかった子は理由つきで残す */
const closed = ref<number | null>(null)
const skipped = ref<{ seq: number; title: string; reason: string }[]>([])

/** 根の完了へ進む先。押せないときの理由も持つ（9.7） */
const rootDone = ref<TicketTransitionOption | null>(null)

function doneOption(items: TicketTransitionOption[]): TicketTransitionOption | null {
  return items.find((o) => o.category === 'done') ?? null
}

async function loadRoot(): Promise<void> {
  try {
    rootDone.value = doneOption((await listTransitions(props.projectKey, props.seq)).items ?? [])
  } catch (e) {
    error.value = delegationErrorText(e)
  }
}

watch(
  () => [props.seq, props.children.map((c) => `${c.seq}:${c.status.category}`).join(','), props.status.open_confirmations?.length],
  () => void loadRoot(),
  { immediate: true },
)

async function closeChildren(): Promise<void> {
  busy.value = true
  error.value = null
  closed.value = null
  skipped.value = []
  let n = 0
  try {
    for (const c of openChildren.value) {
      const to = doneOption((await listTransitions(props.projectKey, c.seq)).items ?? [])
      if (!to?.allowed || !to.key) {
        skipped.value.push({ seq: c.seq, title: c.title, reason: to?.reason ?? uiText('完了へ直接は進められません') })
        continue
      }
      await transitionTicket(props.projectKey, c.seq, { to: to.key })
      n++
    }
  } catch (e) {
    error.value = delegationErrorText(e)
  } finally {
    closed.value = n
    busy.value = false
    if (n > 0) emit('changed')
  }
}

async function accept(): Promise<void> {
  const to = rootDone.value
  if (!to?.allowed || !to.key) return
  busy.value = true
  error.value = null
  try {
    await transitionTicket(props.projectKey, props.seq, { to: to.key })
    emit('changed')
  } catch (e) {
    error.value = delegationErrorText(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <article class="card accept">
    <header class="card-head">
      <span class="card-title">{{ $ui('成果の受け入れ') }}</span>
      <span class="verdict" data-verdict="pass">{{ $ui('評価：合格') }}</span>
    </header>
    <ol class="steps">
      <li>
        <span class="step-name">{{ $ui('成果を確かめる') }}</span>
        <span class="muted">{{ $ui('コメント欄の完了レポートに、ブランチ・変えたもの・確かめ方が書いてあります。マージの前に動かして見たいなら、マージの確認待ちでそう答えてください') }}</span>
      </li>
      <li>
        <span class="step-name">{{ $ui('マージを承認する') }}</span>
        <span v-if="mergeState === 'open'" class="attention" data-merge="open">{{ $ui('下の「後戻りできない操作」に回答してください') }}</span>
        <span v-else-if="mergeState === 'answered'" class="muted" data-merge="answered">{{ $ui('✓ 回答しました') }}</span>
        <span v-else class="muted" data-merge="none">{{ $ui('main からマージの確認待ちが届いたら、ここで回答します') }}</span>
      </li>
      <li>
        <span class="step-name">{{ $ui('受け入れる') }}</span>
        <span class="muted">{{ $ui('子を完了にしてから、根を完了にします。根の完了が受け入れで、自律実行は終わります（実行モードは「自律実行」のまま記録に残ります）') }}</span>
        <div v-if="canTransition" class="actions">
          <button
            v-if="openChildren.length > 0"
            type="button"
            class="secondary"
            data-action="close-children"
            :disabled="busy"
            @click="closeChildren"
          >{{ $ui('子をまとめて完了にする（{value0}件）', { value0: openChildren.length }) }}</button>
          <button
            type="button"
            class="primary"
            data-action="accept"
            :disabled="busy || !rootDone?.allowed"
            @click="accept"
          >{{ $ui('受け入れる（根を完了にする）') }}</button>
        </div>
        <p v-if="canTransition && rootDone && !rootDone.allowed && rootDone.reason" class="muted" data-role="accept-reason">
          {{ rootDone.reason }}
        </p>
        <p v-if="closed !== null && closed > 0" class="muted" data-role="closed">{{ $ui('子 {value0} 件を完了にしました', { value0: closed }) }}</p>
        <ul v-if="skipped.length > 0" class="skipped" data-role="skipped">
          <li v-for="s in skipped" :key="s.seq">
            <RouterLink :to="`/p/${projectKey}/tickets/${s.seq}`">#{{ s.seq }} {{ s.title }}</RouterLink>
            <span class="muted">{{ $ui('（開いて進めてください：{value0}）', { value0: s.reason }) }}</span>
          </li>
        </ul>
      </li>
    </ol>
    <p v-if="error" class="field-error" role="alert">{{ error }}</p>
  </article>
</template>

<style scoped>
/* 確認待ちのカード（DelegationConfirmations.vue）と同じ面にする——人の対応が要るものの並び */
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

.steps {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  margin: 0;
  padding-left: 1.4em;
  list-style: decimal;
}

.steps > li::marker {
  font-weight: 600;
}

/* 番号（::marker）は list-item にしか付かないので、中身を block で縦に積む */
.steps > li {
  display: list-item;
}

.steps > li > span {
  display: block;
}

.step-name {
  font-weight: 600;
}

.muted {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.attention {
  color: var(--pb-warning-text);
  font-size: 12px;
}

.actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: var(--pb-space-2);
  margin-top: var(--pb-space-1);
}

.skipped {
  margin: 0;
  padding-left: 1em;
  font-size: 12px;
}
</style>
