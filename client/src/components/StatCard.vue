<script setup lang="ts">
/**
 * 集計カード（`GuiDesign.md` 6.1 の `StatCard`、5.3 のダッシュボード）。
 *
 * 見出しと数を積んだ箱。**`to` を渡すとリンクになる**——ダッシュボードの4枚は
 * 押すとバックログをそのカテゴリで絞って開く（5.3）。渡さなければただの箱で、
 * **押せないものをボタンに見せない**（`PageHeader` の `titleActionLabel` と
 * 同じ考え方）。
 *
 * **色を使わない**（8.6）。数の大小や意味の重さを色で表さず、大きさだけで示す。
 */
import type { RouteLocationRaw } from 'vue-router'

defineProps<{
  label: string
  value: number
  /** 渡すとリンクになる。押した先で同じ数が出ることが要件（5.3） */
  to?: RouteLocationRaw
  /** リンクの `aria-label`。**押すと何が起きるかを書く** */
  linkLabel?: string
}>()
</script>

<template>
  <component
    :is="to ? 'RouterLink' : 'div'"
    :to="to"
    :aria-label="to ? linkLabel : undefined"
    class="stat-card"
    :class="{ 'is-link': !!to }"
  >
    <span class="stat-card-label">{{ label }}</span>
    <span class="stat-card-value">{{ value }}</span>
  </component>
</template>

<style scoped>
/* **クラス名に部品名を冠する**（6.6）。`scoped` は見た目を分けるが
   DOM のクラス名は分けないので、`.label` `.value` のような一般名を置くと
   検証のセレクタが別の画面の要素に当たる。 */
.stat-card {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  padding: var(--pb-space-4);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  text-decoration: none;
  color: inherit;
}

.stat-card.is-link:hover {
  background: var(--pb-hover);
  border-color: var(--pb-focus);
}

.stat-card.is-link:focus-visible {
  outline: 2px solid var(--pb-accent);
  outline-offset: 2px;
}

.stat-card-label {
  font-size: 13px;
  color: var(--pb-text-muted);
}

.stat-card-value {
  font-size: 28px;
  font-weight: 600;
  line-height: 1.1;
  font-variant-numeric: tabular-nums;
}
</style>
