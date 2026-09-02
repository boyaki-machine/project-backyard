<script setup lang="ts">
/**
 * 「自分の設定」のタブ（`GuiDesign.md` 5.8）。
 *
 * **タブの切替が URL の遷移になる**（`/me` と `/me/tokens` と `/me/agents`。3.2）。
 * プロジェクト設定（5.9）の一般／メンバーとは違う点である。
 *
 * **3つ目のエージェントタブは手順24b で足した**（5.8.2）。**ユーザーメニュー
 * （4.2）には足していない**——登録は参画のときに1回起きる作業であり、常時出る
 * メニューを1行増やす価値が薄い（利用者の判断、2026-09-02）。
 *
 * **部品に切り出したのは、2つの画面で完全に同じでなければならないため。**
 * 写しを2つ持つと、片方だけ直したときに「同じタブなのに見た目が違う」という
 * 分かりにくい崩れになる。判断を含まない静的な markup なので、抽出しても
 * 呼び出し側から読めなくなることがない。
 */
defineProps<{
  /** いま開いているタブ */
  current: 'general' | 'tokens' | 'agents'
}>()
</script>

<template>
  <nav class="tabs" aria-label="設定の種類">
    <RouterLink
      to="/me"
      class="tab"
      :class="{ selected: current === 'general' }"
      :aria-current="current === 'general' ? 'page' : undefined"
    >
      一般
    </RouterLink>
    <RouterLink
      to="/me/tokens"
      class="tab"
      :class="{ selected: current === 'tokens' }"
      :aria-current="current === 'tokens' ? 'page' : undefined"
    >
      アクセストークン
    </RouterLink>
    <RouterLink
      to="/me/agents"
      class="tab"
      :class="{ selected: current === 'agents' }"
      :aria-current="current === 'agents' ? 'page' : undefined"
    >
      エージェント
    </RouterLink>
  </nav>
</template>

<style scoped>
.tabs {
  display: flex;
  gap: var(--pb-space-1);
  margin-bottom: var(--pb-space-4);
  border-bottom: 1px solid var(--pb-border);
}

.tab {
  padding: var(--pb-space-2) var(--pb-space-4);
  border-bottom: 2px solid transparent;
  color: var(--pb-text-muted);
  font: inherit;
  text-decoration: none;
}

.tab:hover {
  color: var(--pb-text);
}

/* 選択中を色だけで示さない（9.2）。下線の太さでも区別できるようにする */
.tab.selected {
  border-bottom-color: var(--pb-accent);
  color: var(--pb-text);
  font-weight: 600;
}
</style>
