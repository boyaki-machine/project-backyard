<script lang="ts">
export interface ActorSelectOption {
  value: string
  label: string
  actor?: { id: string; kind: string; display_name: string }
}
</script>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef } from 'vue'
import Avatar from './Avatar.vue'

const props = withDefaults(defineProps<{
  value: string
  options: ActorSelectOption[]
  label: string
  disabled?: boolean
  variant?: 'default' | 'compact' | 'modal'
}>(), { variant: 'default' })
const emit = defineEmits<{ change: [value: string] }>()

const open = ref(false)
const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const panel = useTemplateRef<HTMLElement>('panel')
const position = ref({ top: 0, left: 0, width: 220, maxHeight: 280 })
const selected = computed(() => props.options.find((o) => o.value === props.value))

function close(): void {
  if (!open.value) return
  open.value = false
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', close)
  trigger.value?.focus()
}

function onScroll(event: Event): void {
  if (event.target instanceof Node && panel.value?.contains(event.target)) return
  close()
}

async function toggle(): Promise<void> {
  if (open.value) { close(); return }
  if (props.disabled || !trigger.value) return
  const rect = trigger.value.getBoundingClientRect()
  const width = Math.min(Math.max(rect.width, 220), window.innerWidth - 16)
  const below = window.innerHeight - rect.bottom - 8
  const above = rect.top - 8
  const height = Math.min(props.options.length * 32 + 8, 280)
  const useAbove = below < height && above > below
  position.value = {
    top: useAbove ? Math.max(8, rect.top - Math.min(height, above) - 4) : rect.bottom + 4,
    left: Math.max(8, Math.min(rect.left, window.innerWidth - width - 8)),
    width,
    maxHeight: Math.max(40, Math.min(280, useAbove ? above : below)),
  }
  open.value = true
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', close)
  await nextTick()
  const buttons = panel.value?.querySelectorAll<HTMLButtonElement>('[role="option"]')
  ;(Array.from(buttons ?? []).find((button) => button.getAttribute('aria-selected') === 'true') ?? buttons?.[0])?.focus()
}

function choose(value: string): void {
  close()
  if (value !== props.value) emit('change', value)
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape' || event.key === 'Tab') {
    event.preventDefault()
    event.stopPropagation()
    close()
    return
  }
  if (!['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) return
  const buttons = Array.from(panel.value?.querySelectorAll<HTMLButtonElement>('[role="option"]') ?? [])
  if (buttons.length === 0) return
  event.preventDefault()
  const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
  const next = event.key === 'Home' ? 0 : event.key === 'End' ? buttons.length - 1
    : event.key === 'ArrowDown' ? (index + 1) % buttons.length : (index - 1 + buttons.length) % buttons.length
  buttons[next]?.focus()
}

onBeforeUnmount(close)
</script>

<template>
  <div class="actor-select" :class="variant">
    <button ref="trigger" type="button" class="actor-select-trigger" :disabled="disabled"
      :aria-label="`${label}: ${selected?.label ?? '—'}`" :aria-expanded="open" aria-haspopup="listbox" @click="toggle">
      <span class="actor-select-value">
        <Avatar v-if="selected?.actor" :name="selected.actor.display_name" :kind="selected.actor.kind"
          :id="selected.actor.id" :size="20" aria-hidden="true" />
        <span>{{ selected?.label ?? '—' }}</span>
      </span>
      <span aria-hidden="true">▾</span>
    </button>
    <Teleport to="body">
      <template v-if="open">
        <div class="actor-select-scrim" :class="variant" @pointerdown="close"></div>
        <div ref="panel" class="actor-select-panel" :class="variant" role="listbox"
          :aria-label="label" :style="{ top: `${position.top}px`, left: `${position.left}px`, width: `${position.width}px`, maxHeight: `${position.maxHeight}px` }"
          @keydown="onKeydown">
          <button v-for="o in options" :key="o.value" type="button" class="actor-select-option"
            role="option" :aria-selected="o.value === value" @click="choose(o.value)">
            <Avatar v-if="o.actor" :name="o.actor.display_name" :kind="o.actor.kind" :id="o.actor.id" :size="20" aria-hidden="true" />
            <span>{{ o.label }}</span>
          </button>
        </div>
      </template>
    </Teleport>
  </div>
</template>

<style scoped>
.actor-select { flex: 1 1 0; min-width: 0; width: 100%; }
.actor-select-trigger {
  display: flex; align-items: center; justify-content: space-between; gap: 8px;
  width: 100%; height: 32px; padding: 0 8px; border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius); background: var(--pb-bg); color: inherit; font: inherit;
  text-align: left; cursor: pointer;
}
.actor-select.compact .actor-select-trigger { height: 28px; font-size: 13px; }
.actor-select.modal .actor-select-trigger { height: 36px; padding: 0 12px; }
.actor-select-trigger:disabled { cursor: default; opacity: .65; }
.actor-select-value { display: inline-flex; align-items: center; gap: 6px; min-width: 0; overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.actor-select-value > span:last-child { overflow: hidden; text-overflow: ellipsis; }
.actor-select-scrim { position: fixed; z-index: 40; inset: 0; }
.actor-select-panel {
  position: fixed; z-index: 41; overflow-y: auto; box-sizing: border-box;
  padding: 4px; border: 1px solid var(--pb-border); border-radius: var(--pb-radius);
  background: var(--pb-bg); box-shadow: var(--pb-shadow-2);
}
.actor-select-scrim.modal { z-index: 110; }
.actor-select-panel.modal { z-index: 111; }
.actor-select-option {
  display: flex; align-items: center; gap: 8px; width: 100%; min-height: 32px;
  padding: 4px 8px; border: 0; border-radius: var(--pb-radius);
  background: none; color: inherit; font: inherit; text-align: left; cursor: pointer;
}
.actor-select-option:hover, .actor-select-option:focus-visible, .actor-select-option[aria-selected='true'] { background: var(--pb-hover); }
</style>
