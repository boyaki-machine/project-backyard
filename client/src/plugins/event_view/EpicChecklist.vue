<script setup lang="ts">
/**
 * タイムライン対象エピックの候補（`GuiDesign.md` 5.15「タイムライン対象エピック」・5.9.1「表示するビュー」）。
 *
 * 袖章を添えたチェックの並び（`sort_key` の順）。**棚に戻ったものは既定で畳む。**
 * タイムラインのヘッダの `[タイムライン対象エピック ▾]` と、プロジェクト設定の両方が使う。
 */
import { computed, ref } from 'vue'

import { sleeveBackground } from '../../lib/epicSleeve'
import type { Sleeve } from '../../lib/epicSleeve'

export interface EpicChoice {
  seq: number
  title: string
  retired: boolean
}

const props = defineProps<{
  choices: EpicChoice[]
  selected: number[]
  sleeve: (seq: number) => Sleeve
  disabled?: boolean
}>()

const emit = defineEmits<{ update: [selected: number[]] }>()

const showRetired = ref(false)
const active = computed(() => props.choices.filter((c) => !c.retired))
const retired = computed(() => props.choices.filter((c) => c.retired))
const chosen = computed(() => new Set(props.selected))

function toggle(seq: number, on: boolean): void {
  const next = new Set(props.selected)
  if (on) next.add(seq)
  else next.delete(seq)
  emit('update', [...next])
}

function sleeveStyle(seq: number): Record<string, string> {
  return { background: sleeveBackground(props.sleeve(seq)) }
}
</script>

<template>
  <div class="tl-epics">
    <p v-if="choices.length === 0" class="tl-epics-empty">{{ $ui('エピックがありません') }}</p>
    <label v-for="c in active" :key="c.seq" class="tl-epic">
      <input type="checkbox" :checked="chosen.has(c.seq)" :disabled="disabled" @change="toggle(c.seq, ($event.target as HTMLInputElement).checked)" />
      <span class="tl-sw" :style="sleeveStyle(c.seq)" aria-hidden="true"></span>
      <span class="tl-epic-title" :title="c.title">{{ c.title }}</span>
      <span class="tl-epic-id">pb-{{ c.seq }}</span>
    </label>
    <template v-if="retired.length > 0">
      <button type="button" class="tl-epics-more" :aria-expanded="showRetired" @click="showRetired = !showRetired">
        {{ showRetired ? '▾' : '▸' }} {{ $ui('棚に戻ったもの（{value0}）', { value0: retired.length }) }}
      </button>
      <template v-if="showRetired">
        <label v-for="c in retired" :key="c.seq" class="tl-epic">
          <input type="checkbox" :checked="chosen.has(c.seq)" :disabled="disabled" @change="toggle(c.seq, ($event.target as HTMLInputElement).checked)" />
          <span class="tl-sw" :style="sleeveStyle(c.seq)" aria-hidden="true"></span>
          <span class="tl-epic-title muted" :title="c.title">{{ c.title }}</span>
          <span class="tl-epic-id">pb-{{ c.seq }}</span>
        </label>
      </template>
    </template>
  </div>
</template>

<style scoped>
.tl-epics {
  display: grid;
  gap: 2px;
  max-height: 320px;
  overflow-y: auto;
}

.tl-epic {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  padding: 3px 4px;
  border-radius: 4px;
  font-size: 13px;
  cursor: pointer;
  min-width: 0;
}

.tl-epic:hover {
  background: var(--pb-hover);
}

.tl-sw {
  flex: none;
  width: 8px;
  height: 16px;
}

.tl-epic-title {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.tl-epic-title.muted,
.tl-epic-id,
.tl-epics-empty {
  color: var(--pb-text-muted);
}

.tl-epic-id {
  flex: none;
  font-family: var(--pb-font-mono);
  font-size: 11px;
}

.tl-epics-empty {
  margin: 0;
  font-size: 13px;
}

.tl-epics-more {
  justify-self: start;
  padding: 2px 4px;
  border: 0;
  background: none;
  color: var(--pb-text-muted);
  font-size: 12px;
  cursor: pointer;
}
</style>
