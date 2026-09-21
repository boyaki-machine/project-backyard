<script setup lang="ts">
import { uiText } from '../locales/ui'
/** バックログの予定期間フィルタ（GuiDesign.md 5.4、pb-8）。 */
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef, watch } from 'vue'

const props = defineProps<{ from: string; to: string }>()
const emit = defineEmits<{ update: [value: { from: string; to: string }] }>()

const open = ref(false)
const draftFrom = ref(props.from)
const draftTo = ref(props.to)
const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const panel = useTemplateRef<HTMLElement>('panel')
const pos = ref({ top: 0, left: 0 })

watch(
  () => [props.from, props.to] as const,
  ([from, to]) => {
    if (open.value) return
    draftFrom.value = from
    draftTo.value = to
  },
)

const label = computed(() => {
  if (props.from === '' && props.to === '') return uiText("指定なし")
  if (props.from !== '' && props.to !== '') return `${props.from}〜${props.to}`
  return props.from !== '' ? uiText("{value0}以降", { value0: props.from }) : uiText("{value0}まで", { value0: props.to })
})

const rangeInvalid = computed(
  () => draftFrom.value !== '' && draftTo.value !== '' && draftFrom.value > draftTo.value,
)

function place(): void {
  const el = trigger.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const width = 330
  const height = 190
  pos.value = {
    top: window.innerHeight - r.bottom < height && r.top > height ? r.top - height - 4 : r.bottom + 4,
    left: Math.max(Math.min(r.left, window.innerWidth - width - 4), 4),
  }
}

async function toggle(): Promise<void> {
  if (open.value) {
    close()
    return
  }
  draftFrom.value = props.from
  draftTo.value = props.to
  place()
  open.value = true
  await nextTick()
  attach()
  panel.value?.querySelector<HTMLInputElement>('input')?.focus()
}

function close(): void {
  if (!open.value) return
  open.value = false
  detach()
  trigger.value?.focus()
}

function apply(): void {
  if (rangeInvalid.value) return
  emit('update', { from: draftFrom.value, to: draftTo.value })
  close()
}

function clear(): void {
  draftFrom.value = ''
  draftTo.value = ''
  emit('update', { from: '', to: '' })
  close()
}

function onDocumentPointerDown(e: PointerEvent): void {
  const target = e.target as Node | null
  if (target && (panel.value?.contains(target) || trigger.value?.contains(target))) return
  close()
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key !== 'Escape') return
  e.preventDefault()
  close()
}

function onScroll(e: Event): void {
  if (e.target instanceof Node && panel.value?.contains(e.target)) return
  close()
}

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

const panelStyle = computed(() => ({ top: `${pos.value.top}px`, left: `${pos.value.left}px` }))
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="trigger"
    :title="label"
    :aria-label="$ui('予定期間：{value0}', { value0: label })"
    :aria-expanded="open"
    aria-haspopup="dialog"
    @click="toggle"
  >
  <span class="trigger-label">{{ label }}</span><span aria-hidden="true">▾</span>
  </button>

  <Teleport to="body">
    <section
      v-if="open"
      ref="panel"
      class="planned-panel"
      :style="panelStyle"
      role="dialog"
      :aria-label="$ui('予定期間を指定')"
    >
      <label>{{ $ui('開始日') }}<input v-model="draftFrom" type="date" :aria-label="$ui('予定期間の開始日')" /></label>
      <label>{{ $ui('終了日') }}<input v-model="draftTo" type="date" :aria-label="$ui('予定期間の終了日')" /></label>
      <p v-if="rangeInvalid" class="error" role="alert">{{ $ui('終了日は開始日以降にしてください') }}</p>
      <div class="actions">
        <button
          type="button"
          class="secondary"
          :disabled="props.from === '' && props.to === ''"
          @click="clear"
        > {{ $ui('解除') }} </button>
        <button type="button" class="primary" :disabled="rangeInvalid" @click="apply">{{ $ui('適用') }}</button>
      </div>
    </section>
  </Teleport>
</template>

<style scoped>
.trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-2);
  width: 100%;
  height: 32px;
  min-width: 0;
  padding: 0 var(--pb-space-2);
  overflow: hidden;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  cursor: pointer;
}
.trigger:hover,
.trigger[aria-expanded='true'] {
  background: var(--pb-hover);
}
.trigger-label {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}
.planned-panel {
  position: fixed;
  z-index: 1000;
  display: grid;
  width: 330px;
  padding: var(--pb-space-4);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  box-shadow: var(--pb-shadow-2);
  gap: var(--pb-space-3);
}
.planned-panel label {
  display: grid;
  grid-template-columns: 64px 1fr;
  align-items: center;
  gap: var(--pb-space-2);
}
.planned-panel input {
  height: 32px;
  min-width: 0;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}
.actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--pb-space-2);
}
.error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
