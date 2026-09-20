<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
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
 * 「P2」「P3」の項目（カンバン・ガント・進捗分析・承認キュー・認証プロバイダ）は
 * まだ表示しない（4.1）。**アプリケーション設定は pb-2 で実装したので出す。**
 * **チケットを見る視点は5つあるが（4.1.1）、実装済みなのはバックログと
 * チケット検索（5.13。pb-66）**である。
 *
 * **Docs は手順22b で出した**（4.3 の表。必要権限は `doc.view`）。**`doc.view` は
 * `project_viewer` まで全ロールが持つ**ので、プロジェクトに到達できる人には
 * 常に見える——出し分けが効くのは項目ではなく画面の中のボタンである
 * （`DbDesign.md` 8.1.4、`GuiDesign.md` 5.10）。
 *
 * チケットの未完了件数バッジ（4.1 の `[12]`）は出していない。供給は
 * `GET /tickets?assignee=me&open=true&per_page=1` で可能だが、メニューは
 * 全画面に出るのでプロジェクトを開くたびに1往復増える。**軽い供給源
 * （手順19 の `stats`）が入ってから足す**（利用者の判断、2026-08-23）。
 */
const auth = useAuthStore()
const ui = useUiStore()
const route = useRoute()
const { t } = useI18n()

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
const showAdmin = computed(
  () => auth.can('user.manage') || auth.can('auditlog.view') || auth.can('system.settings'),
)
</script>

<template>
  <nav
    class="menu"
    :class="{ collapsed: !ui.narrow && ui.menuCollapsed }"
    :aria-label="t('menu.main.label')"
  >
    <div class="brand">
      <SideMenuToggle />
      <RouterLink v-if="ui.narrow || !ui.menuCollapsed" class="brand-name" to="/projects">
        Project Backyard
      </RouterLink>
    </div>

    <div class="sections">
      <RouterLink class="item" to="/projects" :title="t('menu.main.projects')">
        <span class="icon" aria-hidden="true">⌂</span>
        <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.projects') }}</span>
      </RouterLink>

      <template v-if="showProject && projectKey">
        <ProjectSwitcher v-if="ui.narrow || !ui.menuCollapsed" :current-key="projectKey" />
        <div v-else class="rail-sep" aria-hidden="true"></div>

        <RouterLink class="item" :to="`/p/${projectKey}`" :title="t('menu.main.dashboard')">
          <span class="icon" aria-hidden="true">▤</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.dashboard') }}</span>
        </RouterLink>
        <RouterLink
          v-if="auth.canInProject(projectKey, 'ticket.view')"
          class="item"
          :to="`/p/${projectKey}/backlog`"
          :title="t('menu.main.backlog')"
        >
          <span class="icon" aria-hidden="true">≡</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.backlog') }}</span>
        </RouterLink>
        <!-- チケット検索（4.1 / 5.13。pb-66）。**バックログの次に置く**——4.1 の図は間に
             カンバンとガントを挟むが、どちらもまだ出していない。必要権限は同じ `ticket.view`（4.3） -->
        <RouterLink
          v-if="auth.canInProject(projectKey, 'ticket.view')"
          class="item"
          :to="`/p/${projectKey}/search`"
          :title="t('menu.main.search')"
        >
          <span class="icon" aria-hidden="true">⌕</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.search') }}</span>
        </RouterLink>
        <!-- Docs（4.3 の表。手順22b）。**プロジェクト設定より上に置く**——
             4.1 の並びが「視点 → Docs → 設定」で、設定は最後に来る -->
        <RouterLink
          v-if="auth.canInProject(projectKey, 'doc.view')"
          class="item"
          :to="`/p/${projectKey}/docs`"
          :title="t('menu.main.docs')"
        >
          <span class="icon" aria-hidden="true">▣</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.docs') }}</span>
        </RouterLink>
        <RouterLink
          v-if="auth.canInProject(projectKey, 'project.edit')"
          class="item"
          :to="`/p/${projectKey}/settings`"
          :title="t('menu.main.projectSettings')"
        >
          <span class="icon" aria-hidden="true">⚙</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.projectSettings') }}</span>
        </RouterLink>
      </template>

      <template v-if="showAdmin">
        <p v-if="ui.narrow || !ui.menuCollapsed" class="section">─── {{ t('menu.main.administration') }} ────────</p>
        <div v-else class="rail-sep" aria-hidden="true"></div>

        <RouterLink
          v-if="auth.can('user.manage')"
          class="item"
          to="/admin/users"
          :title="t('menu.main.accounts')"
        >
          <span class="icon" aria-hidden="true">⚇</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.accounts') }}</span>
        </RouterLink>
        <RouterLink
          v-if="auth.can('auditlog.view')"
          class="item"
          to="/admin/audit"
          :title="t('menu.main.auditLog')"
        >
          <span class="icon" aria-hidden="true">⛨</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.auditLog') }}</span>
        </RouterLink>
        <RouterLink
          v-if="auth.can('system.settings')"
          class="item"
          to="/admin/settings"
          :title="t('menu.main.appSettings')"
        >
          <span class="icon" aria-hidden="true">⚙</span>
          <span v-if="ui.narrow || !ui.menuCollapsed" class="label">{{ t('menu.main.appSettings') }}</span>
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
