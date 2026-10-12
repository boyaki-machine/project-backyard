<script setup lang="ts">
/**
 * 一覧の行に出す自律実行の印（`GuiDesign.md` 5.4「自律実行の印」、`Delegation.md` 8.2。pb-276）。
 *
 * **PM が開いていないチケットでも、人の対応が要ることに気づけるようにする。**
 * 人の対応が要るとき（回答していないユーザ確認待ちがある・一時停止している）だけ黄の面にし、
 * それ以外は目立たせない。子孫の行は小さな灰の文字で「自律実行の中」とだけ出す。
 * 縮小中（詳細を開いている）は、対応が要るときの記号だけを出す。
 * 完了レポートを出したのに完了していない子は「報告済み」を足す（pb-307）。PM がチェックポイントで
 * まとめて完了にする対象で、急ぎではないので灰のまま。急ぎの対応が重なるときと縮小中は出さない。
 */
import { computed } from 'vue'

import { uiText } from '../locales/ui'

const props = defineProps<{
  mark: { root_seq: number; is_root: boolean; status: string; open_confirmations: number; reported: boolean }
  projectKey: string
  /** 詳細を開いて一覧が縮んでいるか */
  shrunk: boolean
}>()

const paused = computed(() => props.mark.status === 'paused')
const attention = computed(() => paused.value || props.mark.open_confirmations > 0)

const text = computed(() => {
  const parts = [props.mark.is_root ? uiText('自律実行') : uiText('自律実行の中')]
  if (paused.value) parts.push(uiText('一時停止'))
  if (props.mark.open_confirmations > 0) {
    parts.push(uiText('確認待ち {value0}', { value0: props.mark.open_confirmations }))
  }
  // 急ぎの対応が重なるときは出さない。そちらを先に片付けてもらい、狭い幅で印を切らせない
  if (props.mark.reported && !attention.value) parts.push(uiText('報告済み'))
  return parts.join(' · ')
})

const title = computed(() =>
  props.mark.is_root
    ? text.value
    : uiText('{value0} が自律実行を回しています', { value0: `${props.projectKey}-${props.mark.root_seq}` }) +
      (attention.value || props.mark.reported ? `（${text.value}）` : ''),
)
</script>

<template>
  <span v-if="shrunk" v-show="attention" class="mark-symbol" :title="title" :aria-label="title">
    {{ paused ? '⏸' : '❓' }}
  </span>
  <span
    v-else
    class="mark"
    :class="{ attention, root: mark.is_root }"
    :title="title"
  >{{ text }}</span>
</template>

<style scoped>
.mark {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 11px;
  white-space: nowrap;
}

.mark.root {
  padding: 0 6px;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
}

.mark.attention {
  padding: 0 6px;
  border: 1px solid var(--pb-warning-border);
  border-radius: var(--pb-radius);
  background: var(--pb-warning-bg);
  color: var(--pb-on-warning);
}

.mark-symbol {
  flex: none;
  font-size: 12px;
}
</style>
