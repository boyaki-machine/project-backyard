<script lang="ts">
/**
 * ドロップ先（`GuiDesign.md` 5.10「木の操作」）。**行を3つに割る。**
 *
 * | ポインタの位置 | 値 | 結果 |
 * |---|---|---|
 * | 行の上 1/4 | `before` | その行の**前の兄弟**へ |
 * | 行の下 1/4 | `after` | その行の**後ろの兄弟**へ |
 * | 行の中央 1/2 | `inside` | **その行の子**へ（末尾） |
 *
 * **`<script setup>` は export を持てない**ので、型はここに置く
 * （`UserActionsMenu.vue` の `ActionItem` と同じ形）。
 */
export type DocDropZone = 'before' | 'after' | 'inside'

/** いま目印を出している場所。木の中で同時に1つしか持たない */
export interface DocDropHint {
  path: string
  zone: DocDropZone
}
</script>

<script setup lang="ts">
/**
 * 文書ツリー（`GuiDesign.md` 5.10「木の操作」）。
 *
 * **レイアウトも幅も `localStorage` も持たない。** `items` と選択中の `path` を
 * props で受け、選択と開閉と `[⋯]` を emit するだけである。5.10 が「文書ツリーの
 * 置き場（未確定）」として、**この木をメインメニューの Docs 配下へ移す可能性**を
 * 残しているため——移すときに木そのものを書き換えずに済む形にしてある。
 *
 * **ドラッグの状態も持たない**（手順22c）。**この部品は自分自身を再帰的に描く**ので、
 * 各段は別のコンポーネントインスタンスになり、**内部に持った状態は段をまたいで
 * 共有されない**。掴んでいる行と目印は呼び出し側（`DocsPage`）が1つだけ持ち、
 * props で全段へ配る。
 *
 * **自分自身を再帰的に描く。** 階層の深さに上限が無い（`ApiDesign.md` 10.1）ので、
 * 段数を数え打ちしない。インデントは `depth` から作る。
 *
 * **クラス名は `doc-tree-` を冠する**（6.6）。`scoped` は見た目を分けても DOM の
 * クラス名は分けないので、`.node` のような一般名は検証のセレクタが別画面に当たる。
 */
import type { DocTreeItem } from '../api/docs'
import UserActionsMenu, { type ActionItem } from './UserActionsMenu.vue'

const props = withDefaults(
  defineProps<{
    items: DocTreeItem[]
    /** 選択中の文書のパス。`null` はどれも選んでいない（`/p/:key/docs`） */
    selectedPath: string | null
    /** 畳んでいる文書のパス。**畳んだものだけを持つので既定は全展開**（5.10） */
    collapsed: ReadonlySet<string>
    /** リンク先を組み立てるためのプロジェクトキー */
    projectKey: string
    /** `doc.edit` を持つか。持たない人には `[⋯]` とドラッグを出さない（設計原則4） */
    canEdit: boolean
    /** `[⋯]` の項目。呼び出し側が渡す（部品側に項目を焼き付けない。6.1） */
    actions: ActionItem[]
    /** いま掴んでいる文書のパス。`null` はドラッグ中でない */
    draggingPath?: string | null
    /** いま目印を出す場所。木の中で同時に1つ */
    dropHint?: DocDropHint | null
    /** 再帰の深さ。インデントの段数になる */
    depth?: number
  }>(),
  { draggingPath: null, dropHint: null, depth: 0 },
)

const emit = defineEmits<{
  /** 開閉の切り替え。**状態は呼び出し側が持つ**（`localStorage` はページの仕事） */
  toggle: [path: string]
  /** `[⋯]` の選択。どの文書に対する操作かを添える */
  action: [payload: { key: string; item: DocTreeItem }]
  /** 掴んだ。`null` はドラッグの終わり（`dragend`） */
  dragging: [item: DocTreeItem | null]
  /** 目印の置き場が変わった。`null` は落とせない場所に来た */
  hint: [hint: DocDropHint | null]
  /** 落とした。**実際に動かすのは呼び出し側**（目次を持っているのはあちら） */
  drop: [payload: { item: DocTreeItem; zone: DocDropZone }]
}>()

/**
 * その行へ落とせるか（5.10）。
 *
 * **自分自身と自分の子孫へは落とせない。** 10.4 が `cycle` の 422 を返す組み合わせ
 * であり、画面の側で先に塞ぐ。**判定は `path` の前方一致で足りる**——`path` は
 * `slug` を根から連ねたもので（10.1）、`a/b` の子孫は必ず `a/b/` で始まる。
 * **区切りの `/` まで含めて比べる**こと。`startsWith('a/b')` だけでは兄弟の
 * `a/bc` にも当たる。
 */
function droppable(item: DocTreeItem): boolean {
  const from = props.draggingPath
  if (from === null) return false
  return item.path !== from && !item.path.startsWith(`${from}/`)
}

/** ドラッグ中に「落とせない」と見せる部分木（5.10）。掴んだ行そのものを含む */
function undroppable(item: DocTreeItem): boolean {
  return props.draggingPath !== null && !droppable(item)
}

/**
 * ポインタが行のどこを指しているか（5.10 の表）。
 *
 * **上 1/4・下 1/4・中央 1/2。** 5.4 の `sideOf`（半分で割る）をそのまま写せない
 * ——あちらは親子を変えないので行き先が2つしかないが、**木は「兄弟」と「子」の
 * 2軸を1回のドロップで決める**（5.10）ため、中央を子に割り当てる必要がある。
 */
function zoneOf(e: DragEvent): DocDropZone {
  const el = e.currentTarget as HTMLElement | null
  if (el === null) return 'inside'
  const r = el.getBoundingClientRect()
  const y = e.clientY - r.top
  if (y < r.height / 4) return 'before'
  if (y > (r.height * 3) / 4) return 'after'
  return 'inside'
}

/**
 * **受け取れる相手の上でだけ `preventDefault()` する**（6.8）。
 *
 * HTML の D&D は「既定の動作を止めた要素」だけがドロップ先になる規約なので、
 * これで**落とせない行の上ではカーソルが禁止の形になる**。すべて受け取ってから
 * 弾くと、落とせるように見えて何も起きない。
 */
function onDragOver(e: DragEvent, item: DocTreeItem): void {
  if (!droppable(item)) {
    emit('hint', null)
    return
  }
  e.preventDefault()
  emit('hint', { path: item.path, zone: zoneOf(e) })
}

function onDrop(e: DragEvent, item: DocTreeItem): void {
  if (!droppable(item)) return
  emit('drop', { item, zone: zoneOf(e) })
}

/** 目印を出すか。`dropHint` は木全体で1つなので、線も面も同時に1つしか出ない */
function hints(item: DocTreeItem, zone: DocDropZone): boolean {
  const h = props.dropHint
  return h !== null && h.path === item.path && h.zone === zone
}
</script>

<template>
  <ul class="doc-tree-list">
    <li v-for="item in items" :key="item.id" class="doc-tree-item">
      <!-- **掴むのは行全体で、`⠿` のハンドルを置かない**（5.10）。ツリーペインの
           下限は 200px で、行は既に `▸` 20px と `[⋯]` 28px を確保しているため、
           ハンドルを足すとタイトルの実効幅が約 112px（日本語で8字ほど）になる。
           **木に出ているのは `title`** であり、主役を削ってまで置く掴みしろではない -->
      <div
        class="doc-tree-node"
        :class="{
          'doc-tree-selected': selectedPath === item.path,
          'doc-tree-dragging': draggingPath === item.path,
          'doc-tree-undroppable': undroppable(item),
          'doc-tree-drop-before': hints(item, 'before'),
          'doc-tree-drop-after': hints(item, 'after'),
          'doc-tree-drop-inside': hints(item, 'inside'),
        }"
        :style="{ paddingLeft: `${depth * 16}px` }"
        :draggable="canEdit"
        @dragstart="emit('dragging', item)"
        @dragend="emit('dragging', null)"
        @dragover="onDragOver($event, item)"
        @drop.prevent="onDrop($event, item)"
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
             コピーもできる。URL は本文側が持つ（5.10）。

             **`draggable="false"` を必ず付ける**（5.10）。`<a>` は既定で
             ドラッグ可能で、放置すると**タイトルを掴んだ瞬間に URL の
             ドラッグが始まり**、行のドラッグが一度も起きない -->
        <RouterLink
          class="doc-tree-link"
          :to="`/p/${projectKey}/docs/${item.path}`"
          :title="item.title"
          :draggable="false"
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
        :dragging-path="draggingPath"
        :drop-hint="dropHint"
        :depth="depth + 1"
        @toggle="(path: string) => emit('toggle', path)"
        @action="(payload) => emit('action', payload)"
        @dragging="(item2) => emit('dragging', item2)"
        @hint="(hint) => emit('hint', hint)"
        @drop="(payload) => emit('drop', payload)"
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

/* `[⋯]` はホバーと焦点のときだけ**見せる**。**場所は常に取る**——
   `visibility` は箱を残すので、ホバーのたびにタイトルの幅が動くことがない
   （`display: none` にすると 28px ぶん左右に揺れる）。
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

/* ── ドラッグ&ドロップ（5.10「木の操作」）───────────────────── */

/* 掴んでいる行。**見た目だけを変える**——`dragstart` を機に要素を差し込むと
   Chrome がドラッグを取り消す（6.8） */
.doc-tree-dragging {
  opacity: 0.4;
}

/* 落とせない部分木（自分自身と自分の子孫。5.10）。
   `cursor` はドラッグ中に効かないので、**薄さで「ここではない」を伝える** */
.doc-tree-undroppable {
  opacity: 0.35;
}

/* **兄弟へ入るときは線、子へ入るときは面**（5.10）。
   線だけでは `sort_order` しか表せず、`parent_path` が変わることが読めない */
.doc-tree-drop-before {
  box-shadow: inset 0 2px 0 0 var(--pb-accent);
}

.doc-tree-drop-after {
  box-shadow: inset 0 -2px 0 0 var(--pb-accent);
}

/* 面は塗りつぶす。**線と並べたときに一目で別物と読める強さ**にする
   ——2px の線と淡い背景色では、どちらに入るのかが手元で判別できない */
.doc-tree-drop-inside,
.doc-tree-drop-inside:hover {
  background: var(--pb-accent);
}

.doc-tree-drop-inside .doc-tree-link,
.doc-tree-drop-inside .doc-tree-twisty {
  color: var(--pb-on-accent);
}
</style>
