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
 *
 * `lead` と `subtitle` は手順13b で足した。ユーザー詳細（5.6.2）が
 * 「戻る・名前・メール」を出すためで、**2.5 が定める 48px 1行を崩さない**
 * ようにタイトルの左右へ寄せる（利用者の判断、2026-08-21。5.6.2 の
 * ワイヤーは2行だが、機能の議論が固まる前に描いたものである）。
 */
defineProps<{ title: string }>()
</script>

<template>
  <header class="page-header" :class="{ 'with-subtitle': $slots.subtitle }">
    <!-- タイトルの左（戻る導線など）。無ければ何も出ない -->
    <div v-if="$slots.lead" class="lead"><slot name="lead" /></div>

    <h1 class="page-title">{{ title }}</h1>

    <!-- タイトルに従属する情報（メールなど）。見出しの一部にはしない（9.2） -->
    <div v-if="$slots.subtitle" class="subtitle"><slot name="subtitle" /></div>

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

.lead {
  display: flex;
  flex: none;
  align-items: center;
}

.page-title {
  /* 従属情報が無いときは、ここが伸びてアクションを右端へ押す */
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 18px;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* 従属情報があるときは、余りを受け取る役をそちらへ渡す。
   タイトルは内容の幅に収まり、狭ければ縮む（どちらも省略記号で切れる） */
.with-subtitle .page-title {
  flex: 0 1 auto;
}

.subtitle {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--pb-text-muted);
  font-size: 13px;
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
