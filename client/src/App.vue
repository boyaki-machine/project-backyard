<script setup lang="ts">
import { computed } from 'vue'
import { RouterView, useRoute } from 'vue-router'

import AppShell from './components/AppShell.vue'
import { useAuthStore } from './stores/auth'

/**
 * ルート直下のコンポーネント。
 *
 * 認証済みの画面は AppShell（メインメニュー＋コンテンツペイン。GuiDesign.md 2.2）
 * で包む。**ログイン画面はメインメニューを表示しない**（5.1 のレイアウト例外）。
 * 未認証で見える画面（/login・ガードが弾いた先の /403 /404）も同様に素で描く。
 */
const route = useRoute()
const auth = useAuthStore()

const withShell = computed(() => auth.isAuthenticated && route.path !== '/login')
</script>

<template>
  <AppShell v-if="withShell" />
  <RouterView v-else />
</template>
