<script lang="ts">
/** メニュー項目1件。`<script setup>` は export を持てないのでここに置く */
export interface ActionItem {
  key: string
  label: string
  /** 取り消せない操作。文字を danger にする（8.4.3 / 8.6） */
  danger?: boolean
  /** 押せない状態。消さずに出す（5.6.2） */
  disabled?: boolean
  /** 押せない理由。`disabled` のときだけ出す */
  reason?: string
}
</script>

<script setup lang="ts">
/**
 * 行と見出しの操作メニュー `[⋯]`（`GuiDesign.md` 5.6 / 5.6.2）。
 *
 * **項目は呼び出し側が配列で渡す。** 一覧と詳細で項目が違い、詳細のメニューには
 * 「詳細画面に無い操作」や関連情報とのひも付きが今後増えていくため（利用者の
 * 判断、2026-08-21）、部品側に項目を焼き付けない。
 *
 * **`⋯` の記号自体は暫定の表示**である（同じく利用者の判断。5.6 のワイヤーの
 * `[⋯]` はメニューアイコンのプレースホルダにあたる）。アイコンへ差し替える
 * ときはここ1か所を直す。
 *
 * **押せない項目も消さずに出す**（5.6.2）。自分自身へのロール変更・無効化・
 * 削除がこれにあたり、押せない理由を項目の下に添える。**在るはずの操作が
 * 黙って消えるより、押せない理由が読めるほうがよい。**
 *
 * 一覧の `[⋯]` は横スクロールする表の中にあり、詳細の `[⋯]` は
 * スクロール領域の外にある。**どちらでも同じ位置に開く**よう、パネルは
 * `<Teleport>` で body へ出し、ボタンの実測位置に `fixed` で置く。
 * 祖先の `overflow` に切り取られないようにするためである。
 */
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef } from 'vue'

const props = withDefaults(
  defineProps<{
    items: ActionItem[]
    /** ボタンの読み上げ名。「<誰> の操作メニュー」を呼び出し側が組み立てる */
    label: string
    /** 一覧の行に置くときは小さくする */
    compact?: boolean
  }>(),
  { compact: false },
)

const emit = defineEmits<{ select: [key: string] }>()

const open = ref(false)
const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const panel = useTemplateRef<HTMLElement>('panel')

/** パネルの位置。開いた時点のボタンの実測位置から決める */
const pos = ref({ top: 0, right: 0 })

/** パネルの推定の高さ。画面下端に収まらなければ上向きに開く */
function place(): void {
  const el = trigger.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const estimated = props.items.length * 34 + 8
  const below = window.innerHeight - r.bottom
  pos.value = {
    // 下に入らなければボタンの上へ出す（一覧の最終行で画面外に出さない）
    top: below < estimated && r.top > estimated ? r.top - estimated - 4 : r.bottom + 4,
    right: Math.max(window.innerWidth - r.right, 4),
  }
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
  // 開いたら先頭の押せる項目へ移る（9.2）
  panel.value?.querySelector<HTMLElement>('button:not([disabled])')?.focus()
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
 * スクロールしたら閉じる。
 *
 * `fixed` で置いているため、追随させるには毎フレーム測り直すことになる。
 * **メニューは開いている時間が短い**ので、位置を追うより閉じるほうが素直で、
 * 表の下に置き去りのパネルが浮くこともない。
 */
function attach(): void {
  document.addEventListener('pointerdown', onDocumentPointerDown, true)
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('scroll', close, true)
  window.addEventListener('resize', close)
}

function detach(): void {
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('scroll', close, true)
  window.removeEventListener('resize', close)
}

onBeforeUnmount(detach)

function choose(item: ActionItem): void {
  if (item.disabled) return
  open.value = false
  detach()
  emit('select', item.key)
}

const panelStyle = computed(() => ({
  top: `${pos.value.top}px`,
  right: `${pos.value.right}px`,
}))
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="trigger"
    :class="{ compact }"
    :aria-label="label"
    :aria-expanded="open"
    aria-haspopup="menu"
    @click.stop="toggle"
  >
    <span aria-hidden="true">⋯</span>
  </button>

  <Teleport to="body">
    <div
      v-if="open"
      ref="panel"
      class="panel"
      role="menu"
      :style="panelStyle"
      @click.stop
    >
      <template v-for="item in items" :key="item.key">
        <button
          type="button"
          role="menuitem"
          class="item"
          :class="{ danger: item.danger }"
          :disabled="item.disabled"
          @click="choose(item)"
        >
          {{ item.label }}
        </button>
        <!-- 押せない理由は項目の下に置く（5.6.2）。項目そのものは消さない -->
        <p v-if="item.disabled && item.reason" class="reason">{{ item.reason }}</p>
      </template>
    </div>
  </Teleport>
</template>

<style scoped>
.trigger {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: var(--pb-text-muted);
  font-size: 16px;
  line-height: 1;
  cursor: pointer;
}

.trigger.compact {
  width: 28px;
  height: 28px;
}

.trigger:hover,
.trigger[aria-expanded='true'] {
  border-color: var(--pb-border);
  background: var(--pb-hover);
  color: var(--pb-text);
}

/* body へ teleport するので scoped の親子関係に頼らない。
   z-index はページヘッダ（5）とモーダル（100）の間に置く */
.panel {
  position: fixed;
  z-index: 50;
  min-width: 200px;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  box-shadow: var(--pb-shadow-2);
}

.item {
  display: block;
  width: 100%;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 0;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  white-space: nowrap;
  cursor: pointer;
}

.item:hover:not(:disabled) {
  background: var(--pb-hover);
}

/* 破壊的な項目は文字だけ danger にする。面で使うのは確認ダイアログの
   実行ボタンだけである（8.6） */
.item.danger {
  color: var(--pb-danger-text);
}

.item:disabled {
  color: var(--pb-text-muted);
  cursor: default;
}

.reason {
  margin: 0 0 var(--pb-space-1);
  padding: 0 var(--pb-space-3);
  color: var(--pb-text-muted);
  font-size: 12px;
  line-height: 1.5;
  white-space: normal;
}
</style>
