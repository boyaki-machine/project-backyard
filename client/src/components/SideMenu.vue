<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import ProjectSwitcher from './ProjectSwitcher.vue'
import SideMenuToggle from './SideMenuToggle.vue'
import UserMenu from './UserMenu.vue'
import { useAuthStore } from '../stores/auth'
import { useUiStore } from '../stores/ui'

/**
 * メインメニュー（GuiDesign.md 2.2 / 4章）。
 *
 * 上から ブランド行／グローバル／プロジェクト／管理／（余白）／ユーザー行。
 * 旧ヘッダの機能は最上行（ロゴ→ホーム）と最下行（ユーザーメニュー）が引き受ける（2.2）。
 *
 * 出し分けは 4.3 の表に従う。**オペレータには「管理」セクションの見出しごと
 * 表示しない**（押せないメニューによる混乱を避ける）。
 *
 * 「P2」「P3」の項目（ボード・ガント・承認キュー・システム設定）は
 * Phase 1 では表示しない（4.1）。チケットの件数バッジは、供給する API が
 * 手順16 のため出していない。
 */
const auth = useAuthStore()
const ui = useUiStore()
const route = useRoute()

/** 選択中のプロジェクト。URL の :key から決まる（3.2 の /p/:key/...） */
const projectKey = computed(() => {
  const key = route.params.key
  return typeof key === 'string' && key !== '' ? key : null
})

/** プロジェクト領域全体：プロジェクト選択中、かつ project.view（4.3） */
const showProject = computed(
  () => projectKey.value !== null && auth.canInProject(projectKey.value, 'project.view'),
)

/** 管理セクション：user.manage / auditlog.view のいずれかを持つ場合に見出しごと（4.3） */
const showAdmin = computed(() => auth.can('user.manage') || auth.can('auditlog.view'))
</script>

<template>
  <nav class="menu" :class="{ collapsed: ui.menuCollapsed }" aria-label="メインメニュー">
    <div class="brand">
      <SideMenuToggle />
      <RouterLink v-if="!ui.menuCollapsed" class="brand-name" to="/projects">
        Project Backyard
      </RouterLink>
    </div>

    <div class="sections">
      <RouterLink class="item" to="/projects" title="プロジェクト一覧">
        <span class="icon" aria-hidden="true">⌂</span>
        <span v-if="!ui.menuCollapsed" class="label">プロジェクト一覧</span>
      </RouterLink>

      <template v-if="showProject && projectKey">
        <ProjectSwitcher v-if="!ui.menuCollapsed" :current-key="projectKey" />
        <div v-else class="rail-sep" aria-hidden="true"></div>

        <RouterLink class="item" :to="`/p/${projectKey}`" title="ダッシュボード">
          <span class="icon" aria-hidden="true">▤</span>
          <span v-if="!ui.menuCollapsed" class="label">ダッシュボード</span>
        </RouterLink>
        <RouterLink class="item" :to="`/p/${projectKey}/tickets`" title="チケット">
          <span class="icon" aria-hidden="true">☑</span>
          <span v-if="!ui.menuCollapsed" class="label">チケット</span>
        </RouterLink>
        <RouterLink
          v-if="auth.canInProject(projectKey, 'project.edit')"
          class="item"
          :to="`/p/${projectKey}/settings`"
          title="プロジェクト設定"
        >
          <span class="icon" aria-hidden="true">⚙</span>
          <span v-if="!ui.menuCollapsed" class="label">プロジェクト設定</span>
        </RouterLink>
      </template>

      <template v-if="showAdmin">
        <p v-if="!ui.menuCollapsed" class="section">─── 管理 ────────</p>
        <div v-else class="rail-sep" aria-hidden="true"></div>

        <RouterLink
          v-if="auth.can('user.manage')"
          class="item"
          to="/admin/users"
          title="アカウント / 権限"
        >
          <span class="icon" aria-hidden="true">⚇</span>
          <span v-if="!ui.menuCollapsed" class="label">アカウント / 権限</span>
        </RouterLink>
        <RouterLink
          v-if="auth.can('auditlog.view')"
          class="item"
          to="/admin/audit"
          title="監査ログ"
        >
          <span class="icon" aria-hidden="true">⛨</span>
          <span v-if="!ui.menuCollapsed" class="label">監査ログ</span>
        </RouterLink>
      </template>
    </div>

    <UserMenu />
  </nav>
</template>

<style scoped>
.menu {
  display: flex;
  flex-direction: column;
  width: var(--pb-menu-w);
  height: 100%;
  border-right: 1px solid var(--pb-line);
  background: var(--pb-surface);
}

.menu.collapsed {
  width: var(--pb-menu-w-collapsed);
}

.brand {
  display: flex;
  flex: none;
  align-items: center;
  height: var(--pb-pageheader-h);
  border-bottom: 1px solid var(--pb-line);
}

.brand-name {
  overflow: hidden;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.sections {
  flex: 1;
  /* 折りたたみ時（56px）に横スクロールバーが出ないよう、横方向は切る */
  overflow-x: hidden;
  overflow-y: auto;
  padding: var(--pb-space-2) 0;
}

.item {
  display: flex;
  align-items: center;
  height: var(--pb-row-h);
  color: var(--pb-text);
}

.item:hover {
  background: var(--pb-hover);
}

/* 現在地（vue-router が付ける） */
.item.router-link-active {
  background: var(--pb-active);
  font-weight: 600;
}

.icon {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: var(--pb-menu-w-collapsed);
  color: var(--pb-text-muted);
}

.label {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.section {
  padding: var(--pb-space-3) var(--pb-space-3) var(--pb-space-1);
  overflow: hidden;
  color: var(--pb-text-muted);
  font-size: 13px;
  white-space: nowrap;
}

/* 折りたたみ時は見出しの文字を出せないので、区切り線だけを残す */
.rail-sep {
  margin: var(--pb-space-2) var(--pb-space-2);
  border-top: 1px solid var(--pb-line);
}
</style>
