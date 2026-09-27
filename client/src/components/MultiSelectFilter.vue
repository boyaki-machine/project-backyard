<script lang="ts">
import { uiText } from '../locales/ui'
/**
 * 選択肢の1件。
 *
 * **`<script setup>` は export を持てない**ので、型だけを通常の script ブロックへ
 * 出している（`NewTicketModal.vue` と同じ2ブロック構成）。
 */
export interface MultiSelectOption {
  value: string
  label: string
  /** 種別など、画像を持たない選択肢の記号 */
  icon?: string
  actor?: { id: string; kind: string; display_name: string }
}
</script>

<script setup lang="ts">
/**
 * 複数選択のフィルタ（`GuiDesign.md` 5.13「複数選択」）。
 *
 * **`EpicFilter`（5.4）と同じ作りにする。** `<select multiple>` を使わない——縦に伸びて
 * 条件の行の高さがそろわず、画面の縦を食う（原則1）。`<Teleport>` で body へ出し、
 * ボタンの実測位置に `fixed` で置く。条件の行の祖先が `overflow: auto` を持つので、
 * その中に描くと切り取られる。
 *
 * **エピックはこの部品を使わない。** エピックは選択肢ごとに詳細への導線（`↗`）を
 * 持つので、`EpicFilter` のまま使う。ここは名前だけの選択肢を扱う。
 *
 * **ラベルは件数だけを出す**（`EpicFilter` と同じ）。選ぶたびに幅が変わると、
 * 隣の条件の位置が動いて押し間違える。
 */
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef } from 'vue'
import Avatar from './Avatar.vue'

/** パネルの幅。位置決めにも使うので1か所に置く */
const PANEL_W = 260

const props = defineProps<{
  /** 条件の名前。ボタンの `aria-label` に使う（見出しは呼び出し側が出す） */
  label: string
  options: MultiSelectOption[]
  /** 選択中の値。URL のクエリから復元される */
  selected: string[]
}>()

const emit = defineEmits<{ update: [values: string[]] }>()

const open = ref(false)
const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const panel = useTemplateRef<HTMLElement>('panel')

const buttonLabel = computed(() =>
  props.selected.length === 0 ? uiText("すべて") : uiText("{value0}件選択", { value0: props.selected.length }),
)

/** パネルの位置。開いた時点のボタンの実測位置から決める */
const pos = ref({ top: 0, left: 0 })

function place(): void {
  const el = trigger.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const estimated = Math.max(props.options.length, 1) * 34 + 8
  const below = window.innerHeight - r.bottom
  pos.value = {
    // 下に入らなければボタンの上へ出す（狭い窓で画面外へ落とさない）
    top: below < estimated && r.top > estimated ? r.top - estimated - 4 : r.bottom + 4,
    // 右端からはみ出すときだけ左へずらす
    left: Math.max(Math.min(r.left, window.innerWidth - PANEL_W - 4), 4),
  }
}

/**
 * 閉じるのは**パネルの外側**のスクロールだけ（`GuiDesign.md` 6.1「浮かせたパネルを閉じる規則」）。
 * capture で拾うので、候補の欄のスクロールもここへ届く。
 */
function onScroll(e: Event): void {
  if (e.target instanceof Node && panel.value?.contains(e.target)) return
  close()
}

function close(): void {
  if (!open.value) return
  open.value = false
  detach()
  trigger.value?.focus()
}

async function toggle(): Promise<void> {
  if (open.value) {
    close()
    return
  }
  place()
  open.value = true
  await nextTick()
  attach()
  panel.value?.querySelector<HTMLElement>('input[type="checkbox"]')?.focus()
}

function onDocumentPointerDown(e: PointerEvent): void {
  const t = e.target as Node | null
  if (t && (panel.value?.contains(t) || trigger.value?.contains(t))) return
  close()
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') {
    e.preventDefault()
    close()
  }
}

/**
 * 外側のスクロールと窓の大きさの変化で閉じる（`GuiDesign.md` 6.1「浮かせたパネルを閉じる規則」）。
 */
function attach(): void {
  document.addEventListener('pointerdown', onDocumentPointerDown, true)
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', close)
}

function detach(): void {
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', close)
}

onBeforeUnmount(detach)

/** 選択の付け外し。**パネルは閉じない**——複数選ぶための部品である */
function choose(value: string): void {
  const next = props.selected.includes(value)
    ? props.selected.filter((v) => v !== value)
    : [...props.selected, value]
  emit('update', next)
}

const panelStyle = computed(() => ({
  top: `${pos.value.top}px`,
  left: `${pos.value.left}px`,
}))
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="multi-select-trigger"
    :aria-label="`${label}：${buttonLabel}`"
    :aria-expanded="open"
    aria-haspopup="true"
    @click.stop="toggle"
  >
    <span class="multi-select-trigger-label">{{ buttonLabel }}</span>
    <span class="caret" aria-hidden="true">▾</span>
  </button>

  <Teleport to="body">
    <div v-if="open" ref="panel" class="multi-select-panel" :style="panelStyle" @click.stop>
      <p v-if="options.length === 0" class="multi-select-empty">{{ $ui('選択肢がありません') }}</p>
      <label v-for="o in options" :key="o.value" class="multi-select-choice">
        <input
          type="checkbox"
          :checked="selected.includes(o.value)"
          :aria-label="o.label"
          @change="choose(o.value)"
        />
        <span v-if="o.icon" class="multi-select-icon" aria-hidden="true">{{ o.icon }}</span>
        <Avatar v-if="o.actor" :name="o.actor.display_name" :kind="o.actor.kind" :id="o.actor.id" :size="20" aria-hidden="true" />
        <span class="multi-select-label" :title="o.label">{{ o.label }}</span>
      </label>
    </div>
  </Teleport>
</template>

<style scoped>
/* **クラス名は部品名を冠する**（6.6）。scoped は DOM のクラス名を分けないので、
   `.trigger` のような一般名は検証のセレクタが `EpicFilter` にも当たる */

/* 条件の行の `select` と同じ見え方にそろえる（並んだときに高さが揃う） */
.multi-select-trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-2);
  width: 180px;
  height: 32px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.multi-select-trigger:hover,
.multi-select-trigger[aria-expanded='true'] {
  background: var(--pb-hover);
}

.multi-select-trigger-label {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.caret {
  flex: none;
  color: var(--pb-text-muted);
}

/* body へ teleport するので scoped の親子関係に頼らない。
   z-index はページヘッダ（5）とモーダル（100）の間に置く（`EpicFilter` と同じ） */
.multi-select-panel {
  position: fixed;
  z-index: 50;
  width: 260px;
  max-height: 60vh;
  overflow-y: auto;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  box-shadow: var(--pb-shadow-2);
}

.multi-select-choice {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-2);
  border-radius: var(--pb-radius);
  cursor: pointer;
}

.multi-select-choice:hover {
  background: var(--pb-hover);
}

.multi-select-icon {
  flex: none;
  color: var(--pb-text-muted);
}

.multi-select-label {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.multi-select-empty {
  margin: 0;
  padding: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
