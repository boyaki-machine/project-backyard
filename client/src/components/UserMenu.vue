<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'

import { useAuthStore } from '../stores/auth'
import { useUiStore } from '../stores/ui'
import type { ThemePreference } from '../stores/ui'
import { APP_VERSION } from '../version'

/**
 * メニュー最下部のユーザー行とドロップダウン（GuiDesign.md 4.2）。
 *
 * 旧ヘッダのユーザーメニュー・テーマ切替・ログアウトがここに集約されている（2.2）。
 * 上方向へ開く。折りたたみ時はアバターのみになる。
 *
 * テーマは 8.11 が定める3値（ライト／ダーク／システムに従う）をそのまま出す。
 * 4.2 の図は「ライト / ダーク」の2つだが、それでは既定の「システムに従う」を
 * 選び直せない。色相（ブルー／グリーン）の切替は /me の画面（手順17）。
 */
const auth = useAuthStore()
const ui = useUiStore()
const router = useRouter()

const open = ref(false)
const loggingOut = ref(false)

const themes: { value: ThemePreference; label: string }[] = [
  { value: 'light', label: 'ライト' },
  { value: 'dark', label: 'ダーク' },
  { value: 'system', label: 'システム' },
]

async function logout() {
  if (loggingOut.value) return
  loggingOut.value = true
  try {
    await auth.logout()
    await router.replace('/login')
  } finally {
    loggingOut.value = false
    open.value = false
  }
}
</script>

<template>
  <div class="user" @keydown.esc="open = false">
    <div v-if="open" class="dropdown">
      <RouterLink class="item" to="/me" @click="open = false">
        <span class="icon" aria-hidden="true">⚙</span> 自分の設定
      </RouterLink>
      <RouterLink class="item" to="/me/tokens" @click="open = false">
        <span class="icon" aria-hidden="true">🔑</span> アクセストークン
      </RouterLink>

      <div class="sep"></div>

      <div class="theme">
        <p class="theme-label"><span class="icon" aria-hidden="true">◑</span> テーマ</p>
        <span class="choices" role="group" aria-label="テーマ">
          <button
            v-for="t in themes"
            :key="t.value"
            type="button"
            class="choice"
            :aria-pressed="ui.theme === t.value"
            @click="ui.setTheme(t.value)"
          >
            {{ t.label }}
          </button>
        </span>
      </div>

      <div class="sep"></div>

      <p class="version"><span class="icon" aria-hidden="true">ⓘ</span> PB v{{ APP_VERSION }}</p>
      <button type="button" class="item" :disabled="loggingOut" @click="logout">
        <span class="icon" aria-hidden="true">⏻</span> ログアウト
      </button>
    </div>

    <button
      type="button"
      class="row"
      :aria-expanded="open"
      :aria-label="`${auth.actor?.display_name ?? ''} のメニュー`"
      @click="open = !open"
    >
      <span class="avatar" aria-hidden="true">👤</span>
      <span v-if="!ui.menuCollapsed" class="name">{{ auth.actor?.display_name }}</span>
      <span v-if="!ui.menuCollapsed" class="caret" aria-hidden="true">{{ open ? '▾' : '▴' }}</span>
    </button>
  </div>
</template>

<style scoped>
.user {
  position: relative;
  border-top: 1px solid var(--pb-line);
}

.row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  width: 100%;
  height: var(--pb-row-h);
  padding: 0 var(--pb-space-3);
  border: 0;
  background: none;
  cursor: pointer;
  text-align: left;
}

.row:hover {
  background: var(--pb-hover);
}

.avatar {
  flex: none;
}

.name {
  flex: 1;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* 上方向へ開く（4.2）。折りたたみ時は右へはみ出して開く */
.dropdown {
  position: absolute;
  z-index: 10;
  right: var(--pb-space-2);
  bottom: calc(var(--pb-row-h) + var(--pb-space-1));
  left: var(--pb-space-2);
  min-width: 220px;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  box-shadow: var(--pb-shadow-2);
}

.item {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  width: 100%;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 0;
  border-radius: var(--pb-radius);
  background: none;
  cursor: pointer;
  text-align: left;
}

.item:hover {
  background: var(--pb-hover);
}

.icon {
  flex: none;
  width: 1.2em;
  color: var(--pb-text-muted);
}

.sep {
  margin: var(--pb-space-1) 0;
  border-top: 1px solid var(--pb-line);
}

/* メニュー幅（240px）に収めるため、ラベルと選択肢を2行に分ける */
.theme {
  padding: var(--pb-space-1) var(--pb-space-2);
}

.theme-label {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  white-space: nowrap;
}

.choices {
  display: flex;
  gap: 2px;
  margin-top: var(--pb-space-1);
  padding-left: calc(1.2em + var(--pb-space-2));
}

.choice {
  padding: 2px var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-size: 12px;
  white-space: nowrap;
  cursor: pointer;
}

.choice[aria-pressed='true'] {
  border-color: var(--pb-accent);
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.version {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  padding: var(--pb-space-1) var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
