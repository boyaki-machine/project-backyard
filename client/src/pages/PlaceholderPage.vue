<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import PageHeader from '../components/PageHeader.vue'
import { useAuthStore } from '../stores/auth'
import { uiText } from '../locales/ui'

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
  if (p.projectHeading === undefined) return uiText(p.title)
  const key = typeof route.params.key === 'string' ? route.params.key : ''
  return `${auth.projectByKey(key)?.name ?? key} ${uiText(p.projectHeading)}`
})

</script>

<template>
  <div v-if="placeholder" class="page">
    <PageHeader :title="heading" />

    <div class="page-body">
      <div class="card">
        <p class="lead"><span class="icon" aria-hidden="true">▤</span> {{ $ui('このページは未実装です') }}</p>
        <p class="plan-title">{{ $ui('{value0}のページ予定', { value0: $ui(placeholder.title) }) }}</p>

        <section v-if="placeholder.planned.length > 0" class="planned">
          <h2 class="planned-title">{{ $ui('予定している内容') }}</h2>
          <ul class="planned-list">
            <li v-for="item in placeholder.planned" :key="item">{{ $ui(item) }}</li>
          </ul>
        </section>

        <dl class="meta">
          <dt>{{ $ui('状態') }}</dt>
          <dd>{{ $ui(placeholder.status) }}</dd>
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
