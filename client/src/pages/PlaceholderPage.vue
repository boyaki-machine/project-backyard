<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import PageHeader from '../components/PageHeader.vue'
import { useAuthStore } from '../stores/auth'

/**
 * 未実装・設計未確定の画面に出すプレースホルダ（GuiDesign.md 6.5）。
 *
 * 表示内容はルート定義の meta.placeholder から受け取る。
 * 画面ごとにファイルを作らない。
 */
const route = useRoute()
const auth = useAuthStore()

const placeholder = computed(() => route.meta.placeholder)

/**
 * ページヘッダの見出し。
 *
 * ダッシュボードだけは「<プロジェクト名> ダッシュボード」になる（GuiDesign.md 5.3）。
 * プロジェクト名は `GET /me` の `projects[]` から引く。メンバーでない
 * アドミニストレータでは名前が無いため、5.3 のとおりキーで代替する。
 */
const heading = computed(() => {
  const p = placeholder.value
  if (!p) return ''
  if (p.projectHeading === undefined) return p.title
  const key = typeof route.params.key === 'string' ? route.params.key : ''
  return `${auth.projectByKey(key)?.name ?? key} ${p.projectHeading}`
})

/** 定義側のパス（/p/:key）を出す。実際のURLではなくルートの形を示すため */
const routePath = computed(() => route.matched[route.matched.length - 1]?.path ?? route.path)
</script>

<template>
  <div v-if="placeholder" class="page">
    <PageHeader :title="heading" />

    <div class="page-body">
      <div class="card">
        <p class="lead"><span class="icon" aria-hidden="true">▤</span> このページは未実装です</p>
        <p class="plan-title">{{ placeholder.title }}のページ予定</p>

        <section v-if="placeholder.planned.length > 0" class="planned">
          <h2 class="planned-title">予定している内容</h2>
          <ul class="planned-list">
            <li v-for="item in placeholder.planned" :key="item">{{ item }}</li>
          </ul>
        </section>

        <dl class="meta">
          <dt>設計文書</dt>
          <dd>{{ placeholder.docRef }}</dd>
          <dt>ルート</dt>
          <dd><code>{{ routePath }}</code></dd>
          <dt>状態</dt>
          <dd>{{ placeholder.status }}</dd>
        </dl>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* 配色は --pb-text-muted を基調とし、--pb-warning / --pb-ai を使わない（GuiDesign.md 6.5 規約） */
.page {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.page-body {
  flex: 1;
  overflow: auto;
  padding: var(--pb-space-6);
}

.card {
  max-width: 720px;
  padding: var(--pb-space-6);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: var(--pb-text-muted);
}

.lead {
  font-size: 16px;
  font-weight: 600;
}

.icon {
  margin-right: var(--pb-space-2);
}

.plan-title {
  margin-top: var(--pb-space-2);
}

.planned {
  margin-top: var(--pb-space-4);
  padding: var(--pb-space-4);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

.planned-title {
  font-size: 14px;
  font-weight: 600;
}

.planned-list {
  margin-top: var(--pb-space-2);
  padding-left: var(--pb-space-4);
  list-style: disc;
}

.meta {
  display: grid;
  grid-template-columns: 96px 1fr;
  gap: var(--pb-space-1) var(--pb-space-4);
  margin-top: var(--pb-space-6);
  font-size: 13px;
}

.meta dt {
  font-weight: 600;
}

.meta dd {
  margin: 0;
}
</style>
