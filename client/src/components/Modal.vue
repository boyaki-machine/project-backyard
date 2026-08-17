<script setup lang="ts">
/**
 * モーダル（GuiDesign.md 6.1 のオーバーレイ）。
 *
 * 9.2 が求める3点をここに集約する。個々のモーダルに書かせない。
 *
 * - フォーカストラップ（Tab / Shift+Tab が中で循環する）
 * - 閉じたら元の要素へフォーカスを戻す
 * - `Esc` で閉じる（9.1）
 *
 * **モーダルはURLを持たない**（3.2）。開閉の状態は呼び出し側が持ち、
 * `close` を受けて `v-if` を外す。唯一の例外である `/projects?new=1` も、
 * クエリを扱うのは ProjectsPage 側である。
 */
import { nextTick, onBeforeUnmount, onMounted, useTemplateRef } from 'vue'

defineProps<{ title: string }>()

const emit = defineEmits<{ close: [] }>()

const panel = useTemplateRef<HTMLElement>('panel')

/** 開く前にフォーカスされていた要素。閉じたときここへ戻す（9.2） */
let opener: HTMLElement | null = null

/**
 * フォーカスを当てられる要素。`disabled` は除く。
 *
 * 送信中に入力欄が `disabled` になるとその要素は候補から外れるが、
 * 呼び出しのたびに数え直すので追随する。
 */
const FOCUSABLE =
  'a[href], button:not([disabled]), input:not([disabled]), textarea:not([disabled]), select:not([disabled]), [tabindex]:not([tabindex="-1"])'

function focusables(root: ParentNode | null = panel.value): HTMLElement[] {
  if (!root) return []
  return Array.from(root.querySelectorAll<HTMLElement>(FOCUSABLE))
}

function onKeydown(e: KeyboardEvent) {
  if (e.key === 'Escape') {
    e.preventDefault()
    emit('close')
    return
  }
  if (e.key !== 'Tab') return

  const items = focusables()
  if (items.length === 0) return

  const first = items[0]!
  const last = items[items.length - 1]!
  const active = document.activeElement

  // 端に来たら反対側へ回す。モーダルの外へ Tab で出られないようにする（9.2）
  if (e.shiftKey && (active === first || !panel.value?.contains(active))) {
    e.preventDefault()
    last.focus()
  } else if (!e.shiftKey && active === last) {
    e.preventDefault()
    first.focus()
  }
}

onMounted(async () => {
  opener = document.activeElement instanceof HTMLElement ? document.activeElement : null
  window.addEventListener('keydown', onKeydown)
  await nextTick()
  // 本文の先頭（ふつうは最初の入力欄）へ置く。DOM 順の先頭は見出しの ✕ だが、
  // 開いた直後に「閉じる」へフォーカスがあるのは操作の起点として不自然
  const body = panel.value?.querySelector('.body') ?? null
  ;(focusables(body)[0] ?? focusables()[0])?.focus()
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeydown)
  opener?.focus()
})
</script>

<template>
  <!-- 背景のクリックでは閉じない。入力途中の内容を1クリックで失わせないため
       （6.3 が破壊的操作に確認を求めるのと同じ考え方）。閉じる手段は
       ✕ / キャンセル / Esc の3つ -->
  <div class="overlay">
    <div
      ref="panel"
      class="panel"
      role="dialog"
      aria-modal="true"
      :aria-label="title"
    >
      <header class="head">
        <h2 class="title">{{ title }}</h2>
        <button type="button" class="close" aria-label="閉じる" @click="emit('close')">✕</button>
      </header>

      <div class="body">
        <slot />
      </div>

      <footer v-if="$slots.footer" class="foot">
        <slot name="footer" />
      </footer>
    </div>
  </div>
</template>

<style scoped>
.overlay {
  position: fixed;
  z-index: 100;
  inset: 0;
  display: flex;
  align-items: flex-start;
  justify-content: center;
  padding: var(--pb-space-6);
  overflow: auto;
  background: oklch(0.245 0.038 var(--pb-hue) / 0.32);
}

.panel {
  display: flex;
  flex-direction: column;
  width: 100%;
  max-width: 520px;
  margin: auto;
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  box-shadow: var(--pb-shadow-2);
}

.head {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--pb-space-3);
  height: var(--pb-pageheader-h);
  padding: 0 var(--pb-space-4);
  border-bottom: 1px solid var(--pb-line);
}

.title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 16px;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.close {
  flex: none;
  width: 32px;
  height: 32px;
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: var(--pb-text-muted);
  cursor: pointer;
}

.close:hover {
  border-color: var(--pb-border);
  background: var(--pb-hover);
}

.body {
  padding: var(--pb-space-4);
}

.foot {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: flex-end;
  gap: var(--pb-space-2);
  padding: var(--pb-space-3) var(--pb-space-4);
  border-top: 1px solid var(--pb-line);
}
</style>
