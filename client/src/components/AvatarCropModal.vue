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
const error = ref('')
const ready = ref(false)
let source: ImageBitmap | null = null

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
    <p>{{ $ui('表示する範囲を調整してください') }}</p>
    <canvas ref="canvas" width="256" height="256" :aria-label="$ui('アイコンのプレビュー')" />
    <label>{{ $ui('左右') }} <input v-model.number="x" type="range" min="0" max="100" /></label>
    <label>{{ $ui('上下') }} <input v-model.number="y" type="range" min="0" max="100" /></label>
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
canvas { display: block; width: min(256px, 100%); height: auto; margin: 16px auto; border-radius: 50%; }
label { display: flex; align-items: center; gap: 12px; margin: 8px 0; }
input { flex: 1; }
.actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
</style>
