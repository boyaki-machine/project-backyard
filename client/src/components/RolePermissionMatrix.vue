<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * 権限マトリクス（`GuiDesign.md` 5.6.3）。
 *
 * `Design.md` 6.4.2 の権限カタログ（正本は `DbDesign.md` 7.2 のシード）を
 * そのまま表示する。**Phase 1 は読み取り専用**で、カスタムロールの作成と
 * 権限の編集は Phase 3（`ApiDesign.md` 7.3）。
 *
 * 読み取り専用でも画面を用意するのは、「オペレータに何ができるか」を管理者が
 * 確認できることが運用上必要だからである（5.6.3）。
 *
 * 材料は `GET /roles` と `GET /permissions` の2本（`ApiDesign.md` 7.1 / 7.2）。
 * **マトリクス専用のエンドポイントは作らない**——データが重複し、片方だけ
 * 更新される事故を招くため（7.2 末尾）。
 */
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

import EmptyState from './EmptyState.vue'
import { useRolesStore } from '../stores/roles'

const rolesStore = useRolesStore()

const loading = ref(true)
const failure = ref<string | null>(null)

/** ヘッダ1段目。2段目を貼り付ける位置を**実測**するために参照を持つ */
const headRow = ref<HTMLElement | null>(null)
const panel = ref<HTMLElement | null>(null)

/**
 * ヘッダ2段目の `top` を1段目の実測の高さにする。
 *
 * 定数で持つと、文字寸法・余白・翻訳で行の高さが変わったときに1段目と2段目が
 * 重なるか隙間が開く。**見た目の値をコードに写さない。**
 */
function measureHead() {
  const h = headRow.value?.getBoundingClientRect().height ?? 0
  panel.value?.style.setProperty('--pb-matrix-head-h', `${Math.round(h)}px`)
}

/** 表の列。`role.sort_order` の順は API が保証する（7.1）。画面で並べ替えない */
const roles = computed(() => rolesStore.roles)

/**
 * ヘッダ1段目のグループ（5.6.3）。
 *
 * `role.scope` は権限の効く範囲そのものであり（`Design.md` 6.4.1 の三層構造）、
 * これが読めないと「プロジェクト管理者は `user.manage` を持たない」が
 * インスタンス全体の話に見える。
 *
 * **並びは列の並びから作る。** `scope` の順序を別に決め打つと、`sort_order` を
 * 変えたときに見出しと列がずれる。
 */
const scopeGroups = computed(() => {
  const groups: { scope: string; label: string; count: number; boundary: boolean }[] = []
  for (const role of roles.value) {
    const last = groups[groups.length - 1]
    if (last && last.scope === role.scope) {
      last.count += 1
      continue
    }
    groups.push({
      scope: role.scope,
      label: role.scope === 'system' ? uiText("システム") : uiText("プロジェクト"),
      count: 1,
      // 2つ目以降の群は、左端に縦罫を引いて切れ目を示す
      boundary: groups.length > 0,
    })
  }
  return groups
})

/**
 * 行。`permission.category` ごとに区切る（5.6.3）。
 *
 * 権限は30件あり `ticket.*` だけで7件連続する。`sort_order` は category ごとに
 * 連続する番号帯で採番されているため、順に見て変わり目で切ればよい。
 */
const categories = computed(() => {
  const out: { name: string; items: typeof rolesStore.permissions }[] = []
  for (const p of rolesStore.permissions) {
    const last = out[out.length - 1]
    if (last && last.name === p.category) {
      last.items.push(p)
      continue
    }
    out.push({ name: p.category, items: [p] })
  }
  return out
})

/** 割り当ての索引。行×列で毎回 includes を走らせない（28×5＝140マス） */
const granted = computed(() => {
  const map = new Map<string, Set<string>>()
  for (const role of roles.value) {
    map.set(role.key, new Set(role.permissions))
  }
  return map
})

function has(roleKey: string, permissionKey: string): boolean {
  return granted.value.get(roleKey)?.has(permissionKey) ?? false
}

/**
 * その列が群（`role.scope`）の先頭か。
 *
 * **中央寄せの見出しだけでは、どこまでが「システム」か読めない**。
 * 境目に縦罫を1本引いて示す。先頭の群には引かない
 * ——左隣は権限列で、そちらは既に右罫を持つ。
 */
function isGroupStart(index: number): boolean {
  if (index === 0) return false
  return roles.value[index].scope !== roles.value[index - 1].scope
}

async function load() {
  loading.value = true
  failure.value = null
  try {
    // **どちらも user.manage を要する。** このタブは `/admin/users` の中にあり、
    // 画面自体が同じ権限で守られているので 403 は通常起きない。
    await Promise.all([rolesStore.ensureRoles('all'), rolesStore.ensurePermissions()])
    // ロールの取得はストアが例外を握りつぶすため、失敗は error で受け取る。
    if (rolesStore.error) failure.value = rolesStore.error
  } catch (e) {
    failure.value = e instanceof Error ? e.message : uiText("権限カタログを取得できませんでした")
  } finally {
    loading.value = false
  }
}

onMounted(async () => {
  await load()
  // 表が描かれてから測る。読み込み中は thead が存在しない。
  await nextTick()
  measureHead()
  window.addEventListener('resize', measureHead)
})

onBeforeUnmount(() => window.removeEventListener('resize', measureHead))
</script>

<template>
  <div ref="panel" class="matrix-panel">
    <!-- 5.6.3 のワイヤーどおり。Phase 1 が参照のみであることを最初に伝える -->
    <p class="notice">
      <span aria-hidden="true">ⓘ</span> {{ $ui('組み込みロールの権限は Phase 1 では変更できません（参照のみ）') }} </p>

    <p v-if="loading" class="loading" role="status">{{ $ui('読み込み中…') }}</p>

    <EmptyState
      v-else-if="failure"
      :title="$ui('権限カタログを取得できませんでした')"
      :description="failure"
    >
      <template #action>
        <button type="button" class="primary" @click="load">{{ $ui('再試行') }}</button>
      </template>
    </EmptyState>

    <!-- 権限列は左端に固定し、横スクロールするのはロール列だけにする（5.6.3）。
         ヘッダ行も上端に固定する。どちらも「✓ が何の権限・どのロールか」を
         見失わせないためである -->
    <div v-else class="matrix-scroll">
      <table class="matrix">
        <thead>
          <tr ref="headRow">
            <th rowspan="2" scope="col" class="perm corner">{{ $ui('権限') }}</th>
            <th
              v-for="g in scopeGroups"
              :key="g.scope"
              scope="colgroup"
              class="group"
              :class="{ 'group-start': g.boundary }"
              :colspan="g.count"
            >
              {{ g.label }}
            </th>
          </tr>
          <tr>
            <th
              v-for="(r, i) in roles"
              :key="r.key"
              scope="col"
              class="role"
              :class="{ 'group-start': isGroupStart(i) }"
            >
              {{ r.display_name }}
            </th>
          </tr>
        </thead>

        <tbody>
          <template v-for="c in categories" :key="c.name">
            <tr class="category-row">
              <th scope="row" class="perm category">{{ c.name }}</th>
              <td
                v-for="(r, i) in roles"
                :key="r.key"
                class="category-fill"
                :class="{ 'group-start': isGroupStart(i) }"
              ></td>
            </tr>
            <tr v-for="p in c.items" :key="p.key" class="perm-row">
              <th scope="row" class="perm">
                <span class="perm-key">{{ p.key }}</span>
                <span class="perm-desc">{{ p.description }}</span>
              </th>
              <!-- 記号だけにしない。読み上げ用の文字を添える（9.2） -->
              <td
                v-for="(r, i) in roles"
                :key="r.key"
                class="mark"
                :class="{ 'group-start': isGroupStart(i) }"
              >
                <span class="mark-glyph" aria-hidden="true">{{ has(r.key, p.key) ? '✓' : '—' }}</span>
                <span class="visually-hidden">{{ has(r.key, p.key) ? $ui("あり") : $ui("なし") }}</span>
              </td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
  </div>
</template>

<style scoped>
.matrix-panel {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
}

.notice {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.loading {
  color: var(--pb-text-muted);
  font-size: 13px;
}

/*
 * **横スクロールするのは表の中だけ**（5.6 と同じ原則。ページ全体は横に流れない）。
 *
 * max-height を置くのは、ヘッダ行の `position: sticky` を効かせるためである。
 * overflow-x だけを auto にしても、CSS では overflow-y が auto に計算され、
 * この要素自身がスクロール容器になる。高さの上限が無いと縦には決してスクロール
 * しないため、`top: 0` の固定が働かない。vh で持つのは、画面の高さに追従させて
 * 特定の窓幅に依存した数値を置かないため。
 */
.matrix-scroll {
  /*
   * **枠は表の幅にそろえる。** flex の子は既定で引き伸ばされ、表より広い箱に
   * なって右側に空白が残る（1440px で実測）。カテゴリ行の背景も行の罫線も表の
   * 幅で終わるため、枠だけが伸びていると壊れて見える。
   *
   * 5.6 の一覧のように最後の列で余りを吸わせないのは、ロール列を広げても
   * `✓` が離れて読みにくくなるだけで、得るものが無いためである（5.6.3）。
   */
  align-self: start;
  width: max-content;
  max-width: 100%;
  max-height: 70vh;
  overflow: auto;
  /*
   * **`contain: paint` が無いと、文書全体が横に7px流れる**（900px で実測）。
   * はみ出しているのは中身のない空白で、入れ子のスクロール容器が自前の縦
   * スクロールバーを持つときに生じる。`GuiDesign.md` 5.6 は「ページ全体は
   * 横に流れない」と定めており、これを満たすために要る。
   *
   * 宣言の意味そのものも実態に合っている——この要素の子孫は、この矩形の外へ
   * 描画しない（`overflow: auto` で切っている）。
   */
  contain: paint;
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
}

.matrix {
  border-collapse: separate;
  border-spacing: 0;
  background: var(--pb-surface);
  font-size: 13px;
}

/*
 * **ここで指定した値は、クラスだけのセレクタでは上書きできない**（詳細度 0,1,1）。
 * 中央寄せや重なりの順序をクラスに書くときは `.matrix th.role` のように
 * セレクタを伸ばすこと。書き忘れると DOM 上は正しいまま見た目だけがずれ、
 * 「要素があるか」を見る検証では拾えない（GuiDesign.md 5.6.3 の実装で3回起きた）。
 */
.matrix th,
.matrix td {
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
  text-align: left;
  white-space: nowrap;
}

/* ── ヘッダ（2段）──────────────────────────────── */

.matrix thead th {
  position: sticky;
  z-index: 2;
  background: var(--pb-surface);
  font-weight: 600;
}

.matrix thead tr:first-child th {
  top: 0;
}

/* 2段目は1段目の**実測の高さ**だけ下に貼り付く。文字寸法や余白を変えても
   ずれないよう、値は onMounted で測って CSS 変数に入れる（下の measureHead） */
.matrix thead tr:nth-child(2) th {
  top: var(--pb-matrix-head-h, 0px);
}

.matrix th.group {
  text-align: center;
  color: var(--pb-text-muted);
  font-size: 12px;
  /*
   * **見出しの下罫を一段強くする**（`--pb-line` → `--pb-border`）。ラベルが
   * 自分の列を覆っていることを、線の伸びる範囲そのもので示す。
   */
  border-bottom: 1px solid var(--pb-border);
}

/*
 * 群（`role.scope`）の境目に縦罫を1本入れる。
 *
 * **1本だけにする。** 全列に引くと格子になり、一覧（`GuiDesign.md` 5.6）や
 * メンバー表（5.9.2）が横罫だけで組まれているのと見た目が揃わない。読みたいのは
 * 「どこまでがシステムか」であって、隣り合う列の区切りではない。
 *
 * ヘッダから本体まで通しで引く。ヘッダだけだと、下までスクロールしたときに
 * 切れ目が分からなくなる（ヘッダ行を固定したのと同じ理由）。
 */
.matrix th.group-start,
.matrix td.group-start {
  border-left: 1px solid var(--pb-border);
}

.matrix th.role {
  text-align: center;
  min-width: 132px;
}

/* ── 権限列（左端に固定）──────────────────────── */

.perm {
  position: sticky;
  left: 0;
  z-index: 1;
  min-width: 240px;
  background: var(--pb-surface);
  /* 横スクロールしたときに、下を通る内容との境目を出す */
  border-right: 1px solid var(--pb-line);
}

/*
 * 角は縦横どちらの固定より前に出す。
 *
 * **セレクタを `.matrix thead th.corner` まで伸ばすこと。** `.corner` だけでは
 * 上の `.matrix thead th { z-index: 2 }`（詳細度 0,1,2）に負け、角も 2 になる。
 * 同じ値どうしでは後から現れるロール名の見出しが上に描かれ、**横スクロール
 * したときにロール名が固定した権限列の上へはみ出す**（900px で実測）。
 */
.matrix thead th.corner {
  z-index: 3;
  vertical-align: bottom;
}

.perm-key,
.perm-desc {
  display: block;
}

.perm-key {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
}

.perm-desc {
  color: var(--pb-text-muted);
  font-size: 12px;
  font-weight: 400;
}

/* ── カテゴリの小見出し ───────────────────────── */

.category-row th,
.category-row td {
  background: var(--pb-elevated);
  color: var(--pb-text-muted);
  font-size: 12px;
  font-weight: 600;
}

.category {
  /* 固定した権限列の背景は、カテゴリ行でも行と同じ色にする */
  background: var(--pb-elevated);
}

/* ── マス ────────────────────────────────────── */

.matrix td.mark {
  text-align: center;
  color: var(--pb-text);
}

.mark-glyph {
  font-size: 14px;
}

.perm-row:hover th,
.perm-row:hover td {
  background: var(--pb-hover);
}

.visually-hidden {
  position: absolute;
  overflow: hidden;
  clip-path: inset(50%);
  width: 1px;
  height: 1px;
  white-space: nowrap;
}
</style>
