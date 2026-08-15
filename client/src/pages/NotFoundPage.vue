<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <!--
    存在しないURL、および「在るが到達できない」プロジェクトの遷移先。
    403 ではなく 404 に倒すのは Design.md 6.4.5 の方針による（GuiDesign.md 7.2 手順2）。
  -->
  <div class="error-page">
    <p class="code">404</p>
    <h1 class="title">ページが見つかりません</h1>
    <p class="detail">URLが変更されたか、アクセスできないページです。</p>
    <RouterLink class="back" to="/projects">プロジェクト一覧へ戻る</RouterLink>
  </div>
</template>

<style scoped>
.error-page {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--pb-space-2);
  height: 100%;
  padding: var(--pb-space-6);
  color: var(--pb-text-muted);
  text-align: center;
}

.code {
  font-size: 32px;
  font-weight: 600;
  font-variant-numeric: tabular-nums;
}

.title {
  font-size: 18px;
  font-weight: 600;
  color: var(--pb-text);
}

.back {
  margin-top: var(--pb-space-4);
  color: var(--pb-accent);
}

.back:hover {
  color: var(--pb-accent-hover);
  text-decoration: underline;
}
</style>
