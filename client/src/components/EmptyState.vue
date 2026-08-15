<script setup lang="ts">
/**
 * 空状態（GuiDesign.md 6.2）。
 *
 * 「一言＋次の行動を促すボタン」を出す。次の行動が無い利用者
 * （プロジェクト一覧における `project.create` を持たない人）には
 * 説明文だけを出し、ボタンを置かない（5.2）。
 *
 * エラー状態もこの形で出す。原因（サーバが返した message）と
 * 再試行ボタンを同じ配置に置けるため。
 */
defineProps<{ title: string; description?: string }>()
</script>

<template>
  <div class="empty">
    <p class="title">{{ title }}</p>
    <p v-if="description" class="description">{{ description }}</p>
    <div v-if="$slots.action" class="action">
      <slot name="action" />
    </div>
  </div>
</template>

<style scoped>
.empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--pb-space-2);
  padding: var(--pb-space-6);
  text-align: center;
}

.title {
  font-size: 16px;
  font-weight: 600;
}

.description {
  color: var(--pb-text-muted);
}

.action {
  margin-top: var(--pb-space-4);
}
</style>
