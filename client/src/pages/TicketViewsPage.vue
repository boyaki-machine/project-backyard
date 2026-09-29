<script setup lang="ts">
/**
 * チケットを見る視点の入れ物（`GuiDesign.md` 3.2 / 5.13）。
 *
 * `/p/:key/backlog`・`/p/:key/search`・`/p/:key/gantt`・`/p/:key/tickets/:seq` の4本の
 * ルートがこのコンポーネントを指し、**パスと `from` を見て、バックログ・検索・ガントを
 * 出し分ける。**
 *
 * **入れ物を挟むのは、詳細を開閉しても一覧を再マウントさせないためである。**
 * バックログは手順17b から2本のルートを1つのコンポーネントに向け、行を押しても
 * 一覧の取得結果とスクロール位置が残るようにしてきた。検索が加わって「どの一覧の
 * 上に詳細を開くか」を選ぶ必要が出たので、選ぶ役をここに置く。
 *
 * **`from` を解釈できない共有 URL（`/p/:key/tickets/31`）はバックログの上に開く**
 * （3.2）——`from` は「後ろにどの一覧を残すか」だけを表し、視点そのものはパスで分ける。
 */
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import BacklogPage from './BacklogPage.vue'
import GanttPage from './GanttPage.vue'
import SearchPage from './SearchPage.vue'

const route = useRoute()

const showSearch = computed(
  () => route.path.endsWith('/search') || route.query.from === 'search',
)

/** ガント（5.14）。**詳細は `from=gantt` で本体の上に浮かせて開く**（3.2） */
const showGantt = computed(
  () => route.path.endsWith('/gantt') || route.query.from === 'gantt',
)
</script>

<template>
  <SearchPage v-if="showSearch" />
  <GanttPage v-else-if="showGantt" />
  <BacklogPage v-else />
</template>
