<script setup lang="ts">
/**
 * 未読の通知の数（`GuiDesign.md` 5.16）。**`danger` の面に数字**で、0件なら何も出さない。
 *
 * 未読数に限り `danger` を面で使う（8.4）。
 *
 * - `narrow`：幅の無い置き場（折りたたみのレール・浮遊する ☰）。99+ で止める
 * - `overlay`：アイコンの右上に重ねる（2.3 の図のバッジ）。親が `position: relative` を持つ
 */
import { computed } from 'vue'

import { COUNT_CAP, COUNT_CAP_NARROW, formatUnreadCount } from '../stores/notifications'

const props = defineProps<{ count: number; narrow?: boolean; overlay?: boolean }>()

const text = computed(() => formatUnreadCount(props.count, props.narrow ? COUNT_CAP_NARROW : COUNT_CAP))
</script>

<template>
  <span v-if="text" class="unread-count" :class="{ overlay }" aria-hidden="true">{{ text }}</span>
</template>

<style scoped>
.unread-count {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  min-width: 18px;
  height: 18px;
  padding: 0 5px;
  border-radius: 9px;
  background: var(--pb-danger);
  /* danger の面の上の文字。ライトでは白、ダークでは濃い色（赤が明るくなるため） */
  color: var(--pb-on-accent);
  font: 600 11px/1 var(--pb-font-mono);
  white-space: nowrap;
}

.unread-count.overlay {
  position: absolute;
  top: -10px;
  right: -20px;
  transform: scale(0.85);
  box-shadow: 0 0 0 2px var(--pb-surface);
}
</style>
