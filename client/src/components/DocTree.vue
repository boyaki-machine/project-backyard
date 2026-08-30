<script setup lang="ts">
/**
 * 文書ツリー（`GuiDesign.md` 5.10「木の操作」）。
 *
 * **レイアウトも幅も `localStorage` も持たない。** `items` と選択中の `path` を
 * props で受け、選択と開閉と `[⋯]` を emit するだけである。5.10 が「文書ツリーの
 * 置き場（未確定）」として、**この木をメインメニューの Docs 配下へ移す可能性**を
 * 残しているため——移すときに木そのものを書き換えずに済む形にしてある。
 *
 * **自分自身を再帰的に描く。** 階層の深さに上限が無い（`ApiDesign.md` 10.1）ので、
 * 段数を数え打ちしない。インデントは `depth` から作る。
 *
 * **クラス名は `doc-tree-` を冠する**（6.6）。`scoped` は見た目を分けても DOM の
 * クラス名は分けないので、`.node` のような一般名は検証のセレクタが別画面に当たる。
 */
import type { DocTreeItem } from '../api/docs'
import UserActionsMenu, { type ActionItem } from './UserActionsMenu.vue'

withDefaults(
  defineProps<{
    items: DocTreeItem[]
    /** 選択中の文書のパス。`null` はどれも選んでいない（`/p/:key/docs`） */
    selectedPath: string | null
    /** 畳んでいる文書のパス。**畳んだものだけを持つので既定は全展開**（5.10） */
    collapsed: ReadonlySet<string>
    /** リンク先を組み立てるためのプロジェクトキー */
    projectKey: string
    /** `doc.edit` を持つか。持たない人には `[⋯]` を出さない（設計原則4） */
    canEdit: boolean
    /** `[⋯]` の項目。呼び出し側が渡す（部品側に項目を焼き付けない。6.1） */
    actions: ActionItem[]
    /** 再帰の深さ。インデントの段数になる */
    depth?: number
  }>(),
  { depth: 0 },
)

const emit = defineEmits<{
  /** 開閉の切り替え。**状態は呼び出し側が持つ**（`localStorage` はページの仕事） */
  toggle: [path: string]
  /** `[⋯]` の選択。どの文書に対する操作かを添える */
  action: [payload: { key: string; item: DocTreeItem }]
}>()
</script>

<template>
  <ul class="doc-tree-list">
    <li v-for="item in items" :key="item.id" class="doc-tree-item">
      <div
        class="doc-tree-node"
        :class="{ 'doc-tree-selected': selectedPath === item.path }"
        :style="{ paddingLeft: `${depth * 16}px` }"
      >
        <!-- 開閉。**子がいる行にだけ出し、場所は常に取っておく**（バックログの
             `tree-toggle` と同じ理由——行ごとにタイトルの左端がずれない） -->
        <button
          v-if="item.children.length > 0"
          type="button"
          class="doc-tree-twisty"
          :aria-expanded="!collapsed.has(item.path)"
          :aria-label="`${item.title} の配下を開閉する`"
          @click.stop="emit('toggle', item.path)"
        >
          {{ collapsed.has(item.path) ? '▸' : '▾' }}
        </button>
        <span v-else class="doc-tree-twisty-spacer" aria-hidden="true"></span>

        <!-- **`RouterLink` にする。** 中クリックで別タブに開け、リンクの
             コピーもできる。URL は本文側が持つ（5.10） -->
        <RouterLink
          class="doc-tree-link"
          :to="`/p/${projectKey}/docs/${item.path}`"
          :title="item.title"
        >
          {{ item.title }}
        </RouterLink>

        <!-- **`UserActionsMenu` を span で包む。** あの部品は `<button>` と
             `<Teleport>` の**2つの根を持つ**ので、Vue は class の受け渡し
             （フォールスルー）を行わない——直接 class を付けても消え、
             `[⋯]` が全行に出たままになる（実機のスクリーンショットで発覚。
             Vue の警告は `console.warn` なので検証の `console.error` 監視では拾えない） -->
        <span v-if="canEdit" class="doc-tree-actions">
          <UserActionsMenu
            compact
            :items="actions"
            :label="`${item.title} の操作メニュー`"
            @select="(key: string) => emit('action', { key, item })"
          />
        </span>
      </div>

      <DocTree
        v-if="item.children.length > 0 && !collapsed.has(item.path)"
        :items="item.children"
        :selected-path="selectedPath"
        :collapsed="collapsed"
        :project-key="projectKey"
        :can-edit="canEdit"
        :actions="actions"
        :depth="depth + 1"
        @toggle="(path: string) => emit('toggle', path)"
        @action="(payload) => emit('action', payload)"
      />
    </li>
  </ul>
</template>

<style scoped>
.doc-tree-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.doc-tree-node {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
  height: var(--pb-row-h);
  padding-right: var(--pb-space-2);
}

.doc-tree-node:hover {
  background: var(--pb-hover);
}

/* 選択中は面の輝度で示す（8.2 / 8.6）。hover より一段強くして、
   マウスが別の行に載っていても「いま開いているのはこれ」が読める */
.doc-tree-selected,
.doc-tree-selected:hover {
  background: var(--pb-active);
}

.doc-tree-twisty,
.doc-tree-twisty-spacer {
  display: flex;
  flex: none;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
}

.doc-tree-twisty {
  border: 0;
  border-radius: var(--pb-radius);
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  cursor: pointer;
}

.doc-tree-twisty:hover {
  background: var(--pb-hover);
  color: var(--pb-text);
}

/* **主役には `flex: 1 1 auto` と `min-width` を添える**（6.7）。
   `overflow: hidden` だけでは `0 1 auto` のままで、隣の `[⋯]`（`flex: none`）が
   残ったままタイトルのほうが先に0幅まで潰れる */
.doc-tree-link {
  flex: 1 1 auto;
  min-width: 3em;
  overflow: hidden;
  color: var(--pb-text);
  white-space: nowrap;
  text-overflow: ellipsis;
}

.doc-tree-selected .doc-tree-link {
  font-weight: 600;
}

/* `[⋯]` はホバーと焦点のときだけ出す。**場所は常に取らない**——木の行は
   タイトルが主役で、常時出すと狭いペインでタイトルが 32px 削られる。
   `:focus-within` を必ず添える（キーボードで到達したときに見えないと押せない。9.2） */
.doc-tree-actions {
  display: inline-flex;
  flex: none;
  visibility: hidden;
}

.doc-tree-node:hover .doc-tree-actions,
.doc-tree-node:focus-within .doc-tree-actions {
  visibility: visible;
}
</style>
