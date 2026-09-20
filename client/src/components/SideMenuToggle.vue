<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { useUiStore } from '../stores/ui'

/**
 * メニューの折りたたみトグル（GuiDesign.md 2.3）。
 *
 * **メインメニュー自身の最上行に置く**（案A）。折りたたんでもアイコンレール
 * 56px が残るため、ボタンは常に同じ位置にあり「戻し方が分からない」が
 * 構造的に起きない。768px 未満でのみ浮遊表示になる（2.4、案B）。
 */
const ui = useUiStore()
const { t } = useI18n()
</script>

<template>
  <button
    type="button"
    class="toggle"
    :aria-label="t('menu.main.toggle')"
    :aria-expanded="ui.narrow ? ui.overlayOpen : !ui.menuCollapsed"
    @click="ui.toggleMenu()"
  >
    ☰
  </button>
</template>

<style scoped>
.toggle {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: var(--pb-menu-w-collapsed);
  height: var(--pb-pageheader-h);
  border: 0;
  background: none;
  cursor: pointer;
}

.toggle:hover {
  background: var(--pb-hover);
}
</style>
