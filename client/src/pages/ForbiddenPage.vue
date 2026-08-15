<script setup lang="ts">
import { RouterLink } from 'vue-router'
</script>

<template>
  <!-- 権限が無いルートへ入ろうとしたときの遷移先（GuiDesign.md 7.2 手順3） -->
  <div class="error-page">
    <p class="code">403</p>
    <h1 class="title">このページを表示する権限がありません</h1>
    <p class="detail">必要な権限が付与されていない可能性があります。管理者に確認してください。</p>
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
