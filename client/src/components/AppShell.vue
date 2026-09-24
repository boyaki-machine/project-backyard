<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'

import PendingConfirmBar from './PendingConfirmBar.vue'
import SideMenu from './SideMenu.vue'
import SideMenuToggle from './SideMenuToggle.vue'
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

/** `[` でメニューを折りたたむ（GuiDesign.md 9.1） */
function onKeydown(e: KeyboardEvent) {
  if (e.key !== '[' || e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return
  const el = e.target as HTMLElement | null
  // 入力中は横取りしない
  if (el && (el.isContentEditable || ['INPUT', 'TEXTAREA', 'SELECT'].includes(el.tagName))) return
  e.preventDefault()
  ui.toggleMenu()
}

onMounted(() => {
  window.addEventListener('keydown', onKeydown)
  pendingStore.start()
})
onUnmounted(() => {
  window.removeEventListener('keydown', onKeydown)
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

    <SideMenu v-if="!ui.narrow || ui.overlayOpen" class="pane" />
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
