<script setup lang="ts">
import { computed, onMounted, onUnmounted, useTemplateRef, watch } from 'vue'
import { useRoute } from 'vue-router'

import PendingConfirmBar from './PendingConfirmBar.vue'
import SideMenu from './SideMenu.vue'
import SideMenuToggle from './SideMenuToggle.vue'
import { isTicketViewPath, ticketViewOf } from '../lib/ticketViews'
import { usePendingStore } from '../stores/pending'
import { useUiStore } from '../stores/ui'

/**
 * メインメニューとコンテンツペインの2枚構成（GuiDesign.md 2.2）。
 *
 * **アプリ共通のヘッダ行を持たない**（2.1）。ページタイトルと主要アクションは
 * コンテンツペイン内のページヘッダが持つ（2.5）。
 *
 * スクロールはコンテンツ領域内のみで起きる。メニューは常時表示される。
 */
const ui = useUiStore()

/**
 * 未確認の設定変更の監視。
 *
 * **アプリの枠で始める。** どの画面にいても帯が出る必要があり、**設定画面に
 * 置くと、そこを離れた瞬間に見えなくなる**。
 */
const pendingStore = usePendingStore()

function isTyping(el: EventTarget | null): boolean {
  const t = el as HTMLElement | null
  return !!t && (t.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(t.tagName))
}

/**
 * `[` でメニューを折りたたむ、`Shift + [` で集中モードを切り替える（GuiDesign.md 9.1 / 2.3.2）。
 *
 * **`Shift + [` は `e.key === '{'` で拾う。** US 配列でも JIS 配列でも同じ値になる
 * （JIS の `[` は US の `]` の位置にあり、`e.code` で見ると配列で食い違う）。
 */
function onKeydown(e: KeyboardEvent) {
  if (e.metaKey || e.ctrlKey || e.altKey) return
  if (e.key === 'Escape') {
    if (ui.focusMode) onEscape(e)
    return
  }
  if (e.key !== '[' && e.key !== '{') return
  // 入力中は横取りしない
  if (isTyping(e.target)) return
  if (e.key === '{') {
    // 768px 未満では集中モードに入らない（2.3.2）
    if (ui.narrow) return
    e.preventDefault()
    ui.toggleFocus()
    return
  }
  if (e.shiftKey) return
  e.preventDefault()
  ui.toggleMenu()
}

/**
 * **`Esc` はほかの部品が先に使う**（2.3.2）——モーダル・浮かせたパネル・ガントのドラッグの
 * 取り消しは、使ったら `preventDefault` する。**リスナの登録順は部品を開いた順で決まり、
 * この枠より後のことがある**（モーダルは開いたときに登録する）ので、1拍遅らせて、
 * 誰も使わなかった `Esc` だけで解除する。
 */
function onEscape(e: KeyboardEvent) {
  if (isTyping(e.target)) return
  setTimeout(() => {
    if (e.defaultPrevented || !ui.focusMode) return
    if (document.querySelector('[role="dialog"][aria-modal="true"]')) return
    ui.exitFocus()
  })
}

// ── 集中モードの左端（2.3.2）─────────────────────────────────

/** 左端から何 px までを「寄せた」とみなすか */
const EDGE_PX = 8
/** 左端にとどまってから出すまで。素早く通り過ぎた・行を押しに行った、を拾わない */
const DWELL_MS = 200
/** メニューから離れてから隠すまで */
const LEAVE_MS = 300

let dwellTimer: ReturnType<typeof setTimeout> | undefined
let leaveTimer: ReturnType<typeof setTimeout> | undefined
let lastEdge = { x: Infinity, buttons: 0 }

function clearDwell() {
  clearTimeout(dwellTimer)
  dwellTimer = undefined
}

function clearLeave() {
  clearTimeout(leaveTimer)
  leaveTimer = undefined
}

const peek = useTemplateRef<HTMLElement>('peek')

/**
 * 左端 8px に 200ms とどまったら重ねて出す。**ボタンを押している間は出さない**
 * （ガントのドラッグで左端へ寄せたとき）。要素を置かず `document` で測るので、
 * 左端のツリーの行や開閉ボタンの当たりを奪わない。
 */
function onDocumentPointerMove(e: PointerEvent) {
  if (!ui.focusMode) return
  if (ui.focusPeek) {
    trackPeekLeave(e)
    return
  }
  lastEdge = { x: e.clientX, buttons: e.buttons }
  if (e.clientX > EDGE_PX || e.buttons !== 0) {
    clearDwell()
    return
  }
  if (dwellTimer !== undefined) return
  dwellTimer = setTimeout(() => {
    dwellTimer = undefined
    if (ui.focusMode && lastEdge.x <= EDGE_PX && lastEdge.buttons === 0) ui.focusPeek = true
  }, DWELL_MS)
}

/** 窓の外へ出たら待ちをやめる（別の画面へマウスを運んだとき） */
function onDocumentLeave() {
  clearDwell()
  lastEdge = { x: Infinity, buttons: 0 }
}

/**
 * 重ねたメニューの外へ出たら 300ms 後に隠し、内へ戻れば待ちをやめる。
 * **`pointerenter` / `pointerleave` に頼らない**——メニューは静止したポインタの下へ
 * スライドして現れるので、ブラウザは「入った」を記録せず、離れても `pointerleave` が
 * 起きない（pb-233）。外で動き続けても待ちは延ばさない。
 */
function trackPeekLeave(e: PointerEvent) {
  if (e.target instanceof Node && peek.value?.contains(e.target)) {
    clearLeave()
    return
  }
  if (leaveTimer !== undefined) return
  leaveTimer = setTimeout(() => {
    leaveTimer = undefined
    ui.focusPeek = false
  }, LEAVE_MS)
}

// ── 画面を移ったら解除する（2.3.2）──────────────────────────

const route = useRoute()

/**
 * 画面＝プロジェクトと、見えている一覧の種類。**ガントで詳細を開く
 * （`/p/:key/tickets/:seq?from=gantt`）のは、ガントが出たままなので同じ画面**である。
 */
const screen = computed(() => {
  const key = route.params.key
  if (typeof key === 'string' && isTicketViewPath(route.path)) return `${key}:${ticketViewOf(route.path, route.query.from)}`
  return route.path
})

watch(screen, () => ui.exitFocus())

watch(
  () => ui.focusMode,
  (on) => {
    if (on) return
    clearDwell()
    clearLeave()
  },
)

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  document.addEventListener('pointermove', onDocumentPointerMove)
  document.documentElement.addEventListener('mouseleave', onDocumentLeave)
  pendingStore.start()
})
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
  document.removeEventListener('pointermove', onDocumentPointerMove)
  document.documentElement.removeEventListener('mouseleave', onDocumentLeave)
  clearDwell()
  clearLeave()
  pendingStore.stop()
})
</script>

<template>
  <div class="shell" :class="{ narrow: ui.narrow }">
    <!-- 768px 未満ではメニューをオーバーレイにするため、レールが残らない。
         この幅でのみ浮遊ボタンを置く（GuiDesign.md 2.4） -->
    <div v-if="ui.narrow && !ui.overlayOpen" class="floating">
      <SideMenuToggle />
    </div>

    <SideMenu v-if="(!ui.narrow || ui.overlayOpen) && !ui.focusMode" class="pane" />
    <!-- 集中モード（2.3.2）：メニューは 0px。左端に寄せたときだけ重ねて出す -->
    <Transition name="peek">
      <div
        v-if="ui.focusMode && ui.focusPeek"
        ref="peek"
        class="peek"
      >
        <SideMenu />
      </div>
    </Transition>
    <div v-if="ui.narrow && ui.overlayOpen" class="scrim" @click="ui.closeOverlay()"></div>

    <main class="content" :class="{ 'with-bar': pendingStore.pending }">
      <RouterView />
    </main>

    <!--
      **未確認の設定変更は全画面に出す**。設定画面の中だけに置くと、
      そこを離れた瞬間に確認ボタンが消える。
    -->
    <PendingConfirmBar />
  </div>
</template>

<style scoped>
.shell {
  display: flex;
  height: 100%;
}

.pane {
  flex: none;
}

.content {
  flex: 1;
  min-width: 0;
  height: 100%;
  overflow: hidden;
}

/*
 * **帯のぶんだけ下を空ける**。空けないと、スクロール枠を持つ画面
 * （文書のツリー、設定の貼り付け欄）で**最下部が帯の裏に隠れる。**
 */
.content.with-bar {
  padding-bottom: 6rem;
}

/* 768px 未満：メニューはオーバーレイ。タイトル行との衝突は許容する（2.4） */
.narrow .pane {
  position: fixed;
  z-index: 30;
  top: 0;
  bottom: 0;
  left: 0;
  box-shadow: var(--pb-shadow-2);
}

/* 集中モードで重ねて出すメニュー（2.3.2）。左からスライドする */
.peek {
  position: fixed;
  z-index: 30;
  top: 0;
  bottom: 0;
  left: 0;
  display: flex;
  box-shadow: var(--pb-shadow-2);
}

.peek-enter-active,
.peek-leave-active {
  transition: transform 150ms ease-out;
}

.peek-enter-from,
.peek-leave-to {
  transform: translateX(-100%);
}

@media (prefers-reduced-motion: reduce) {
  .peek-enter-active,
  .peek-leave-active {
    transition: none;
  }
}

.floating {
  position: fixed;
  z-index: 20;
  top: 0;
  left: 0;
}

.scrim {
  position: fixed;
  z-index: 25;
  inset: 0;
  background: oklch(0 0 0 / 0.3);
}
</style>
