<script setup lang="ts">
/**
 * プロジェクト設定「表示するビュー」のタイムラインの欄（`GuiDesign.md` 5.9.1）。
 *
 * 吸着の単位（`timeline_snap_seconds`）とタイムライン対象エピック（`timeline_epics`）。
 * **チェックを入れているあいだだけ**本体の設定画面が出す（登録表の `settings.panel`）。
 * 保存は一般タブの `[保存]` 1つ——ここは差し替えた `settings` を `update` で返すだけ。
 */
import { computed, onMounted, watch } from 'vue'

import { useEpicChoices } from './epics'
import EpicChecklist from './EpicChecklist.vue'
import { SNAP_CHOICES, readSnapSeconds, readTimelineEpics } from './model'

const props = defineProps<{
  projectKey: string
  settings: Record<string, unknown>
  disabled?: boolean
}>()

const emit = defineEmits<{ update: [settings: Record<string, unknown>] }>()

const epics = useEpicChoices()
onMounted(() => void epics.load(props.projectKey))
watch(
  () => props.projectKey,
  (k) => void epics.load(k),
)

const snap = computed(() => readSnapSeconds(props.settings))
const selected = computed(() => readTimelineEpics(props.settings))

function setSnap(v: number): void {
  emit('update', { ...props.settings, timeline_snap_seconds: v })
}

/** いま在るエピックだけに詰め直す（`DbDesign.md` 6.4） */
function setEpics(seqs: number[]): void {
  const live = new Set(epics.all.value.map((t) => t.seq))
  const next = epics.loaded.value ? seqs.filter((s) => live.has(s)) : seqs
  emit('update', { ...props.settings, timeline_epics: [...new Set(next)].sort((a, b) => a - b) })
}
</script>

<template>
  <div class="tl-vs">
    <label class="tl-vs-row">
      <span class="tl-vs-label">{{ $ui('吸着') }}</span>
      <select :value="snap" :disabled="disabled" @change="setSnap(Number(($event.target as HTMLSelectElement).value))">
        <option v-for="s in SNAP_CHOICES" :key="s" :value="s">
          {{ s < 60 ? $ui('{value0}秒', { value0: s }) : $ui('{value0}分', { value0: s / 60 }) }}
        </option>
      </select>
    </label>
    <div class="tl-vs-row top">
      <span class="tl-vs-label">{{ $ui('タイムライン対象エピック') }}</span>
      <EpicChecklist :choices="epics.choices.value" :selected="selected" :sleeve="epics.sleeve.value" :disabled="disabled" @update="setEpics" />
    </div>
  </div>
</template>

<style scoped>
.tl-vs {
  display: grid;
  gap: var(--pb-space-2);
  padding-left: 28px;
}

.tl-vs-row {
  display: grid;
  grid-template-columns: 8em minmax(0, 1fr);
  align-items: center;
  gap: var(--pb-space-2);
  font-size: 13px;
}

.tl-vs-row select {
  justify-self: start;
}

.tl-vs-row.top {
  align-items: start;
}

.tl-vs-label {
  color: var(--pb-text-muted);
}
</style>
