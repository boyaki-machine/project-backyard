<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useAuthStore } from '../stores/auth'

/**
 * プロジェクト切替（GuiDesign.md 4.4）。
 *
 * 一覧の出どころは `GET /me` の `projects[]`（`ApiDesign.md` 4.1）である。
 * `GET /projects`（手順9）はまだ無いが、切替に必要な key と name は
 * `/me` が返しており、手順8の時点で作れる。
 *
 * 切替時は**同じ画面種別を維持する**（4.4）。チケット一覧を見ていたら
 * 切替先でもチケット一覧になる。
 */
const props = defineProps<{ currentKey: string }>()

const auth = useAuthStore()
const router = useRouter()

const open = ref(false)
const filter = ref('')

const current = computed(() => auth.projectByKey(props.currentKey))

/** 絞り込み入力は 10件を超えたときにのみ出す（4.4、設計原則3） */
const showFilter = computed(() => auth.projects.length > 10)

const visible = computed(() => {
  const q = filter.value.trim().toLowerCase()
  if (q === '') return auth.projects
  return auth.projects.filter(
    (p) => p.key.toLowerCase().includes(q) || p.name.toLowerCase().includes(q),
  )
})

function toggle() {
  open.value = !open.value
  if (open.value) filter.value = ''
}

function close() {
  open.value = false
}

/**
 * 同じ画面種別を保ったまま切り替える。
 *
 * 切替先で当該画面の権限が無ければダッシュボードへ落とす（4.4）。
 */
function switchTo(key: string) {
  close()
  if (key === props.currentKey) return

  const suffix = router.currentRoute.value.path.replace(`/p/${props.currentKey}`, '')
  const permission = router.currentRoute.value.meta.permission
  const keepable = suffix !== '' && (permission === undefined || auth.canInProject(key, permission))
  void router.push(`/p/${key}${keepable ? suffix : ''}`)
}
</script>

<template>
  <div class="switcher" @keydown.esc="close">
    <button type="button" class="current" :aria-expanded="open" @click="toggle">
      <span class="rule" aria-hidden="true">───</span>
      <span class="name">{{ current?.key ?? currentKey }}</span>
      <span class="caret" aria-hidden="true">{{ open ? '▴' : '▾' }}</span>
    </button>

    <div v-if="open" class="dropdown">
      <input
        v-if="showFilter"
        v-model="filter"
        class="filter"
        type="search"
        placeholder="絞り込み"
        aria-label="プロジェクトを絞り込む"
      />

      <ul v-if="visible.length > 0" class="list">
        <li v-for="p in visible" :key="p.key">
          <button type="button" class="item" @click="switchTo(p.key)">
            <span class="mark" aria-hidden="true">{{ p.key === currentKey ? '●' : '○' }}</span>
            {{ p.name }}
          </button>
        </li>
      </ul>
      <!-- 空状態を後回しにしない（GuiDesign.md 6.2）。所属していないプロジェクトを
           見ているアドミニストレータでは、/me の projects[] が空になりうる -->
      <p v-else class="empty">
        {{ auth.projects.length === 0 ? '所属しているプロジェクトはありません' : '該当なし' }}
      </p>

      <div class="footer">
        <RouterLink class="item" to="/projects" @click="close">
          <span class="mark" aria-hidden="true">⌂</span> プロジェクト一覧
        </RouterLink>
        <!-- 新規作成モーダルは手順10。ここでは 3.2 の /projects?new=1 へ送るに留める -->
        <RouterLink
          v-if="auth.can('project.create')"
          class="item"
          :to="{ path: '/projects', query: { new: '1' } }"
          @click="close"
        >
          <span class="mark" aria-hidden="true">+</span> 新規プロジェクト
        </RouterLink>
      </div>
    </div>
  </div>
</template>

<style scoped>
.switcher {
  position: relative;
}

.current {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  width: 100%;
  height: var(--pb-row-h);
  padding: 0 var(--pb-space-3);
  border: 0;
  background: none;
  color: var(--pb-text-muted);
  cursor: pointer;
  text-align: left;
}

.current:hover {
  background: var(--pb-hover);
}

.name {
  flex: 1;
  overflow: hidden;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.rule,
.caret {
  flex: none;
}

.dropdown {
  position: absolute;
  z-index: 10;
  top: calc(var(--pb-row-h) - 2px);
  right: var(--pb-space-2);
  left: var(--pb-space-2);
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  box-shadow: var(--pb-shadow-2);
}

.filter {
  width: 100%;
  height: 32px;
  margin-bottom: var(--pb-space-1);
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

.list {
  max-height: 240px;
  overflow-y: auto;
  list-style: none;
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

.mark {
  flex: none;
  width: 1em;
  color: var(--pb-text-muted);
}

.empty {
  padding: var(--pb-space-1) var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.footer {
  margin-top: var(--pb-space-1);
  padding-top: var(--pb-space-1);
  border-top: 1px solid var(--pb-line);
}
</style>
