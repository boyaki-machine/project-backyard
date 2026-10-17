<script setup lang="ts">
/**
 * チケットを見る視点の入れ物（`GuiDesign.md` 3.2 / 5.13 / 5.15）。
 *
 * `/p/:key/backlog`・`/p/:key/search`・`/p/:key/gantt`・`/p/:key/tickets/:seq` と
 * プラグインのビュー（`plugins/registry.ts`）のルートがこのコンポーネントを指し、
 * **パスと `from` を見て、バックログ・検索・ガント・プラグインを出し分ける。**
 *
 * **入れ物を挟むのは、詳細を開閉しても一覧を再マウントさせないためである。**
 * バックログは手順17b から2本のルートを1つのコンポーネントに向け、行を押しても
 * 一覧の取得結果とスクロール位置が残るようにしてきた。検索が加わって「どの一覧の
 * 上に詳細を開くか」を選ぶ必要が出たので、選ぶ役をここに置く。
 *
 * **`from` を解釈できない共有 URL（`/p/:key/tickets/31`）はバックログの上に開く**
 * （3.2）——`from` は「後ろにどの一覧を残すか」だけを表し、視点そのものはパスで分ける。
 *
 * **ページは遅延読み込みにする**（5.15「プラグインの境界」）。バックログを開くだけで
 * ガントやタイムラインのコードを読み込まない。
 */
import { computed, defineAsyncComponent } from 'vue'
import type { Component } from 'vue'
import { useRoute } from 'vue-router'

import { ticketViewOf } from '../lib/ticketViews'
import { viewPlugins } from '../plugins/registry'

const BacklogPage = defineAsyncComponent(() => import('./BacklogPage.vue'))
const GanttPage = defineAsyncComponent(() => import('./GanttPage.vue'))
const SearchPage = defineAsyncComponent(() => import('./SearchPage.vue'))
const pluginPages = new Map<string, Component>(
  viewPlugins.map((p) => [`plugin:${p.id}`, defineAsyncComponent(p.route.page)]),
)

const route = useRoute()

/** どの一覧を出すか（判定の正本は `lib/ticketViews.ts`）。ガント・プラグインの詳細は `from` で本体の上に浮かせて開く */
const view = computed(() => ticketViewOf(route.path, route.query.from))
const pluginPage = computed(() => pluginPages.get(view.value) ?? null)
</script>

<template>
  <SearchPage v-if="view === 'search'" />
  <GanttPage v-else-if="view === 'gantt'" />
  <component :is="pluginPage" v-else-if="pluginPage" :key="view" />
  <BacklogPage v-else />
</template>
