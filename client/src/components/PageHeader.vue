<script setup lang="ts">
/**
 * ページヘッダ（GuiDesign.md 2.5）。
 *
 * すべてのページが持つ2ブロックのうちの1つ。48px・sticky で、
 * 画面タイトル（左）と主要アクション（右）を置く。副次アクションは
 * 呼び出し側が `⋯` へ集約する。
 *
 * **アプリ共通のヘッダ行は無い**（2.1）。ここの `<h1>` がそのページの
 * 唯一の見出しになる（9.2）。
 */
defineProps<{ title: string }>()
</script>

<template>
  <header class="page-header">
    <h1 class="page-title">{{ title }}</h1>
    <div class="actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style scoped>
.page-header {
  position: sticky;
  z-index: 5;
  top: 0;
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--pb-space-3);
  height: var(--pb-pageheader-h);
  padding: 0 var(--pb-space-6);
  border-bottom: 1px solid var(--pb-line);
  background: var(--pb-bg);
}

.page-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 18px;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--pb-space-2);
}
</style>
