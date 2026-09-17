<script setup lang="ts">
/**
 * 状態のドロップダウン（`GuiDesign.md` 5.5「状態のドロップダウン」）。
 *
 * **`<select>` を使わない。** `reason` が「進行中から完了へは直接進められません」
 * のように長く、`<option>` に入れると幅で切れる。`UserActionsMenu.vue` と同じ
 * `<Teleport>` ＋ `position: fixed` にして、祖先の `overflow` に切られないようにする。
 *
 * **遷移できない先も出す**（`ApiDesign.md` 9.7）。設計原則4「権限で見えないを作る」は
 * メニュー項目についての規則であり、ここには当てはまらない——ステータスは業務上の
 * 到達点で、存在ごと隠すと**なぜ完了にできないのかが分からなくなる**。
 *
 * **選択肢は開いたときに取る**（9.7）。詳細応答には入っていない。
 *
 * **選ばれた項目を伝えるところまで**を持ち、`POST .../transition` は呼び出し側が
 * 投げる。**呼び出し側は選ばれた時点で送る**（`GuiDesign.md` 5.5「遷移にコメントを
 * 添えない」。pb-55）——手順18b では確認モーダルを挟んでいたが、状態変更の頻度に
 * 対してダイアログが重く、廃止した。**この部品の役割は変わっていない。**
 *
 * **5.4 のバックログ一覧でも使う**（pb-63）。**2つ目を作らない**——一覧に要るものは
 * 5.5 とまったく同じ（開いたときに `GET .../transitions` を引き、`allowed` と `reason` を
 * 出す）で、別に書くと**同じ判定が2か所に散る**。違うのは行の高さだけなので `dense` を
 * 足した。
 *
 * **「全項目が不可」の扱いは残す**（5.5）。0026 で再オープン（`done → in_progress`）を
 * 足すまでは、`simple` ワークフロー（`DbDesign.md` 7.4）で完了したチケットが必ず
 * この状態になっていた。**いまは既定の3テンプレートでは起きない**が、**遷移は
 * ワークフローのデータでプロジェクトごとに違いうる**ので、扱いは消さない。
 */
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef } from 'vue'

import { statusMarks } from '../api/tickets'
import type { TicketStatus, TicketTransitionOption } from '../api/tickets'

const props = defineProps<{
  /** いまのステータス。ボタンの表示になる */
  current: TicketStatus
  /** 遷移できるか（`ticket.transition`）。持たないときは開かない */
  canTransition: boolean
  /** 送信中。二重に送らせない */
  busy: boolean
  /**
   * 一覧の行に置くときの密度（5.4。pb-63）。
   *
   * **バックログの表は行が詰まっている。** 5.5 のメタ欄と同じ 28px にすると
   * **全行が 6px 高くなり**、一画面に入る件数が減る。`dense` では 5.4 の
   * ステータスバッジと同じ `line-height: 22px` に揃える（8.7）。
   */
  dense?: boolean
}>()

const emit = defineEmits<{
  /** 開いた。呼び出し側が `GET .../transitions` を投げる */
  open: []
  /**
   * 遷移できる項目が選ばれた。**キーではなく項目そのものを渡す。**
   * かつては確認モーダルの文面（「`<現在の名前>` から `<遷移先の名前>` へ」）に
   * `name` が要ったためだが、**モーダルを廃止したいまも項目ごと渡す**（pb-55）
   * ——受け側がエラー文言や記録に名前を使えるほうが縛りが少ない。
   */
  select: [item: TicketTransitionOption]
}>()

const open = ref(false)
const loading = ref(false)
const items = ref<TicketTransitionOption[]>([])
const loadError = ref('')

const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const panel = useTemplateRef<HTMLElement>('panel')

/**
 * パネルの位置。開いた時点のボタンの実測位置から決める（`UserActionsMenu` と同じ）。
 * **上へ出すときは `top` ではなく `bottom` で置く**——下端をボタンに揃えておく
 */
const pos = ref<{ top?: number; bottom?: number; left: number; width: number; maxHeight?: number }>(
  { top: 0, left: 0, width: 0 },
)

/** 60vh は `.panel` の上限。余白に合わせて縮めるときも超えない */
const panelStyle = computed(() => ({
  top: pos.value.top === undefined ? undefined : `${pos.value.top}px`,
  bottom: pos.value.bottom === undefined ? undefined : `${pos.value.bottom}px`,
  left: `${pos.value.left}px`,
  minWidth: `${pos.value.width}px`,
  maxHeight: pos.value.maxHeight === undefined ? undefined : `min(60vh, ${pos.value.maxHeight}px)`,
}))

const allBlocked = computed(
  () => items.value.length > 0 && items.value.every((i) => !i.allowed),
)

/**
 * **項目に2行目（`reason`）が付き、件数も開いてから届くので高さが推定できない。**
 * `UserActionsMenu` のように推定せず、いったん下へ描いて実寸を測り、下に入らず
 * 上のほうが広ければ上へ出す（5.5「位置」。pb-92）。最大高は出した側の余白まで
 * 縮め、一覧の最終行でも項目を画面の外に出さない。**中身が変わるたびに呼ぶ**
 */
async function place(): Promise<void> {
  const el = trigger.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const width = Math.max(r.width, 260)
  pos.value = { top: r.bottom + 4, left: r.left, width }
  await nextTick()
  if (!panel.value) return
  const height = panel.value.offsetHeight
  // 4 はボタンとの隙間、8 は画面の端に残す余白
  const below = window.innerHeight - r.bottom - 12
  const above = r.top - 12
  pos.value =
    height <= below || below >= above
      ? { top: r.bottom + 4, left: r.left, width, maxHeight: below }
      : { bottom: window.innerHeight - r.top + 4, left: r.left, width, maxHeight: above }
}

function detach(): void {
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', close)
}

/**
 * 閉じるのは**パネルの外側**のスクロールだけ（`GuiDesign.md` 6.1「浮かせたパネルを閉じる規則」。
 * pb-131）。capture で拾うので、候補の欄のスクロールもここへ届く。
 */
function onScroll(e: Event): void {
  if (e.target instanceof Node && panel.value?.contains(e.target)) return
  close()
}

function close(): void {
  if (!open.value) return
  open.value = false
  detach()
  trigger.value?.focus()
}

async function toggle(): Promise<void> {
  if (open.value) {
    close()
    return
  }
  if (!props.canTransition || props.busy) return
  open.value = true
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', close)
  emit('open')
  await place()
  panel.value?.focus()
}

/** 呼び出し側が取得の結果を流し込む。**開いている間だけ意味を持つ** */
function setItems(next: TicketTransitionOption[]): void {
  items.value = next
  loading.value = false
  loadError.value = ''
  if (open.value) void place()
}

function setLoading(): void {
  loading.value = true
  loadError.value = ''
  items.value = []
}

function setError(message: string): void {
  loading.value = false
  loadError.value = message
  items.value = []
  if (open.value) void place()
}

function choose(item: TicketTransitionOption): void {
  if (!item.allowed) return
  close()
  emit('select', item)
}

onBeforeUnmount(detach)

defineExpose({ setItems, setLoading, setError, close })
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="status-trigger"
    :class="[current.category, { plain: !canTransition, dense }]"
    :disabled="!canTransition || busy"
    :aria-expanded="open"
    aria-haspopup="listbox"
    :title="canTransition ? '状態を変える' : '状態を変える権限がありません'"
    @click="toggle"
  >
    <span class="status-mark" aria-hidden="true">{{ statusMarks[current.category] }}</span>
    <span class="status-name">{{ current.name }}</span>
    <span v-if="canTransition" class="caret" aria-hidden="true">▾</span>
  </button>

  <Teleport to="body">
    <template v-if="open">
      <!-- 外側を押したら閉じる。`UserActionsMenu` と同じ透明の膜 -->
      <div class="scrim" @click="close" @contextmenu.prevent="close"></div>
      <div
        ref="panel"
        class="panel"
        role="listbox"
        tabindex="-1"
        :style="panelStyle"
        @keydown.escape="close"
      >
        <p v-if="loading" class="note">読み込み中…</p>
        <p v-else-if="loadError" class="note">{{ loadError }}</p>

        <template v-else>
          <!-- **全項目が不可のときは先頭に1行出す**（5.5）。ドロップダウンは
               開いたままにして、理由が読める状態にする -->
          <p v-if="allBlocked" class="note">いま進められる状態はありません</p>

          <button
            v-for="item in items"
            :key="item.key"
            type="button"
            class="option"
            :class="{ blocked: !item.allowed }"
            role="option"
            :aria-selected="false"
            :aria-disabled="!item.allowed"
            @click="choose(item)"
          >
            <span class="option-name">
              <span class="status-mark" aria-hidden="true">{{ statusMarks[item.category] }}</span>
              {{ item.name }}
            </span>
            <!-- **`reason` はサーバの日本語をそのまま出す**（`ApiDesign.md` 2.5） -->
            <span v-if="!item.allowed && item.reason" class="reason">{{ item.reason }}</span>
          </button>

          <p v-if="items.length === 0" class="note">遷移先がありません</p>
        </template>
      </div>
    </template>
  </Teleport>
</template>

<style scoped>
/* ボタンの見た目はバックログのステータスバッジに揃える（8.7）。
   **同じチケットの状態が一覧と詳細で違う形に見えないようにする** */
.status-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  height: 28px;
  padding: 0 var(--pb-space-2);
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  font-size: 13px;
  cursor: pointer;
}

.status-trigger:hover:not(:disabled) {
  border-color: var(--pb-border);
}

.status-trigger.plain {
  cursor: default;
}

/* 一覧の行に置くとき（5.4。pb-63）。**5.4 のステータスバッジと同じ高さにする**
   ——表の行がこれ1つで 6px 高くなるのを避ける */
.status-trigger.dense {
  height: 22px;
  padding: 0 var(--pb-space-1);
}

.status-trigger.todo {
  border-color: var(--pb-border);
  color: var(--pb-text-muted);
}

.status-trigger.in_progress {
  background: var(--pb-elevated);
  color: var(--pb-text);
}

.status-trigger.in_progress .status-mark {
  color: var(--pb-accent);
}

.status-trigger.review {
  background: var(--pb-hover);
  color: var(--pb-text);
}

.status-trigger.done {
  color: var(--pb-text-muted);
}

.status-name {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.caret {
  color: var(--pb-text-muted);
}

.scrim {
  position: fixed;
  z-index: 40;
  inset: 0;
}

.panel {
  position: fixed;
  z-index: 41;
  max-width: 360px;
  max-height: 60vh;
  overflow-y: auto;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  box-shadow: var(--pb-shadow-2);
}

.option {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: var(--pb-space-2);
  border: none;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.option:hover:not(.blocked) {
  background: var(--pb-hover);
}

/* 不可の項目は淡色にする（5.5）。**消さずに出す** */
.option.blocked {
  color: var(--pb-text-muted);
  cursor: default;
}

.option-name {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* 2行目の理由。**折り返して全文を出す**——1行に詰めて省略記号で切ると、
   出した意味が無くなる */
.reason {
  font-size: 12px;
  line-height: 1.5;
}

.note {
  margin: 0;
  padding: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
