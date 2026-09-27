<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import Modal from './Modal.vue'
import { uiText } from '../locales/ui'

const props = defineProps<{ file: File; busy: boolean }>()
const emit = defineEmits<{ close: []; save: [image: Blob] }>()
const canvas = useTemplateRef<HTMLCanvasElement>('canvas')
const x = ref(50)
const y = ref(50)
const zoom = ref(1)
const dragging = ref(false)
const error = ref('')
const ready = ref(false)
let source: ImageBitmap | null = null
let activePointer: number | null = null
let lastPointerX = 0
let lastPointerY = 0

function clamp(value: number): number {
  return Math.max(0, Math.min(100, value))
}

function moveImage(dx: number, dy: number, width: number, height: number): void {
  if (!source || width <= 0 || height <= 0) return
  const side = Math.min(source.width, source.height) / zoom.value
  const horizontalTravel = source.width - side
  const verticalTravel = source.height - side
  if (horizontalTravel > 0) x.value = clamp(x.value - dx * side / width / horizontalTravel * 100)
  if (verticalTravel > 0) y.value = clamp(y.value - dy * side / height / verticalTravel * 100)
}

function onPointerDown(event: PointerEvent): void {
  if (!ready.value || activePointer !== null || event.button !== 0) return
  const target = event.currentTarget as HTMLCanvasElement
  target.focus()
  target.setPointerCapture(event.pointerId)
  activePointer = event.pointerId
  lastPointerX = event.clientX
  lastPointerY = event.clientY
  dragging.value = true
}

function onPointerMove(event: PointerEvent): void {
  if (activePointer !== event.pointerId || !canvas.value) return
  const rect = canvas.value.getBoundingClientRect()
  moveImage(event.clientX - lastPointerX, event.clientY - lastPointerY, rect.width, rect.height)
  lastPointerX = event.clientX
  lastPointerY = event.clientY
}

function endDrag(event: PointerEvent): void {
  if (activePointer !== event.pointerId) return
  activePointer = null
  dragging.value = false
  const target = event.currentTarget as HTMLCanvasElement
  if (target.hasPointerCapture(event.pointerId)) target.releasePointerCapture(event.pointerId)
}

function onArrowKey(event: KeyboardEvent): void {
  const direction: Record<string, [number, number]> = {
    ArrowLeft: [-8, 0], ArrowRight: [8, 0], ArrowUp: [0, -8], ArrowDown: [0, 8],
  }
  const delta = direction[event.key]
  if (!delta || !ready.value) return
  event.preventDefault()
  moveImage(delta[0], delta[1], 256, 256)
}

function draw(): void {
  if (!source || !canvas.value) return
  const ctx = canvas.value.getContext('2d')
  if (!ctx) return
  const side = Math.min(source.width, source.height) / zoom.value
  const left = (source.width - side) * x.value / 100
  const top = (source.height - side) * y.value / 100
  ctx.clearRect(0, 0, 256, 256)
  ctx.drawImage(source, left, top, side, side, 0, 0, 256, 256)
}

onMounted(async () => {
  try {
    source = await createImageBitmap(props.file)
    if (source.width > 8192 || source.height > 8192) {
      error.value = uiText('画像の縦横は8192ピクセル以下にしてください')
      return
    }
    ready.value = true
    draw()
  } catch {
    error.value = uiText('画像を開けませんでした')
  }
})
watch([x, y, zoom], draw)
onBeforeUnmount(() => source?.close())

function save(): void {
  canvas.value?.toBlob((blob) => {
    if (!blob) { error.value = uiText('画像を作成できませんでした'); return }
    if (blob.size > 1024 * 1024) { error.value = uiText('画像が1 MiBを超えました'); return }
    emit('save', blob)
  }, 'image/png')
}
</script>

<template>
  <Modal :title="$ui('アイコンを切り抜く')" @close="emit('close')">
    <p id="avatar-crop-help">{{ $ui('画像をドラッグして位置を調整してください。矢印キーでも移動できます。') }}</p>
    <canvas ref="canvas" width="256" height="256" tabindex="0" :aria-label="$ui('アイコンのプレビュー')"
      aria-describedby="avatar-crop-help" :class="{ dragging }"
      @pointerdown="onPointerDown" @pointermove="onPointerMove" @pointerup="endDrag"
      @pointercancel="endDrag" @lostpointercapture="endDrag" @keydown="onArrowKey" />
    <label>{{ $ui('拡大') }} <input v-model.number="zoom" type="range" min="1" max="3" step="0.05" /></label>
    <p v-if="error" role="alert">{{ error }}</p>
    <div class="actions">
      <button type="button" @click="emit('close')">{{ $ui('キャンセル') }}</button>
      <button type="button" class="primary" :disabled="busy || !!error || !ready" @click="save">
        {{ busy ? $ui('保存中…') : $ui('登録') }}
      </button>
    </div>
  </Modal>
</template>

<style scoped>
canvas { display: block; width: min(256px, 100%); height: auto; margin: 16px auto; border-radius: 50%; cursor: grab; touch-action: none; }
canvas.dragging { cursor: grabbing; }
label { display: flex; align-items: center; gap: 12px; margin: 8px 0; }
input { flex: 1; }
.actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
</style>
