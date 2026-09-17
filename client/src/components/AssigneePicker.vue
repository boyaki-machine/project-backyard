<script setup lang="ts">
/**
 * 担当のピッカー（`GuiDesign.md` 5.4「一覧で担当を選ぶ」。pb-64）。
 *
 * **`StatusDropdown` と形を揃える。** `<Teleport>` ＋ `position: fixed` で祖先の
 * `overflow` に切られないようにし、開いた時点のボタンの実測位置から場所を決める。
 * **流用はしない**——あちらは遷移という別の語彙（`allowed` / `reason` / サーバに
 * 問い合わせる選択肢）を持っており、混ぜると両方が曇る。
 *
 * **候補はプロジェクトのメンバーである**（`GET /projects/:key` の `members[]`。
 * `ApiDesign.md` 5.2）。**API を足さない**——絞り込みはこの手元の配列に対して
 * 行う。プロジェクトのメンバーが、問い合わせを分けるほど多くなることはない。
 *
 * **インクリメンタルサーチを持つ**（利用者の要望、2026-09-07）。**打つたびに
 * 絞る**のであって、確定を待たない。
 *
 * **「未割当」を候補の先頭に置く。** 担当を外す操作は付ける操作と同じ頻度で
 * 起こり、**別の場所（詳細ペイン）へ行かないと外せない状態を作らない。**
 */
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef } from 'vue'

import type { ProjectMember } from '../api/projects'

const props = defineProps<{
  /** いまの担当。`null` は未割当 */
  current: { id: string; kind: string; display_name: string } | null
  /** 候補（`GET /projects/:key` の `members[]`） */
  members: ProjectMember[]
  /** 担当を変えられるか（`ticket.assign`）。持たないときは開かない */
  canAssign: boolean
  /** 送信中。二重に送らせない */
  busy: boolean
}>()

const emit = defineEmits<{
  /** 選ばれた。`null` は未割当にする */
  select: [actorId: string | null]
}>()

const open = ref(false)
const query = ref('')

const trigger = useTemplateRef<HTMLButtonElement>('trigger')
// **パネルの参照は高さを測るためだけに持つ**（`place`）。`StatusDropdown` はパネルへ
// フォーカスを移すが、こちらは開いた直後に検索欄へ入れる
const panel = useTemplateRef<HTMLElement>('panel')
const search = useTemplateRef<HTMLInputElement>('search')

/**
 * パネルの位置。開いた時点のボタンの実測位置から決める（`StatusDropdown` と同じ）。
 * **上へ出すときは `top` ではなく `bottom` で置く**——絞り込みで候補が減っても、
 * 下端がボタンに揃ったまま離れない
 */
const pos = ref<{ top?: number; bottom?: number; left: number; width: number; maxHeight?: number }>(
  { top: 0, left: 0, width: 0 },
)

/** 候補の欄の上限（40vh）は `.options` が持つので、ここではパネル全体を余白に収めるだけ */
const panelStyle = computed(() => ({
  top: pos.value.top === undefined ? undefined : `${pos.value.top}px`,
  bottom: pos.value.bottom === undefined ? undefined : `${pos.value.bottom}px`,
  left: `${pos.value.left}px`,
  minWidth: `${pos.value.width}px`,
  maxHeight: pos.value.maxHeight === undefined ? undefined : `${pos.value.maxHeight}px`,
}))

/**
 * 絞り込んだ候補。**表示名で前方一致ではなく部分一致**にする——姓で引く人と
 * 名で引く人がいて、どちらも当たるほうが手数が少ない。**大文字小文字を無視する。**
 */
const filtered = computed<ProjectMember[]>(() => {
  const q = query.value.trim().toLocaleLowerCase()
  if (q === '') return props.members
  return props.members.filter((m) => m.display_name.toLocaleLowerCase().includes(q))
})

/** 種別の記号。一覧の担当セルと同じ（5.4「実行者を担当と同じセルに置く」） */
function mark(kind: string): string {
  return kind === 'agent' ? '🤖' : '👤'
}

/**
 * **高さが推定できない**（候補の件数で変わる）ので、いったん下へ描いて実寸を測り、
 * 下に入らず上のほうが広ければ上へ出す。最大高は出した側の余白まで縮める
 * （5.4「一覧で担当を選ぶ」の「位置」。`StatusDropdown` と同じ判断。pb-92）。
 * **絞り込みでは置き直さない**——打っている最中にパネルが跳ねない
 */
async function place(): Promise<void> {
  const el = trigger.value
  if (!el) return
  const r = el.getBoundingClientRect()
  const width = Math.max(r.width, 220)
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
  query.value = ''
  detach()
  trigger.value?.focus()
}

async function toggle(): Promise<void> {
  if (open.value) {
    close()
    return
  }
  if (!props.canAssign || props.busy) return
  open.value = true
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', close)
  await place()
  // **開いたら検索欄へ入る。** 絞り込みが主目的の部品なので、開いてから
  // もう一度クリックさせない
  await nextTick()
  search.value?.focus()
}

function choose(actorId: string | null): void {
  close()
  emit('select', actorId)
}

/**
 * 候補が1件に絞れているとき、`Enter` で確定する。
 *
 * **2件以上のときは何もしない。** 先頭を選ぶ作りにすると、**打ち終わる前の
 * 一瞬の先頭**が確定されうる。
 */
function onEnter(): void {
  const only = filtered.value
  if (only.length === 1) choose(only[0]!.actor_id)
}

onBeforeUnmount(detach)

defineExpose({ close })
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="assignee-trigger"
    :class="{ plain: !canAssign }"
    :disabled="!canAssign || busy"
    :aria-expanded="open"
    aria-haspopup="listbox"
    :title="canAssign ? '担当を変える' : '担当を変える権限がありません'"
    @click="toggle"
  >
    <span v-if="current" class="assignee-name">
      <span class="actor-mark" aria-hidden="true">{{ mark(current.kind) }}</span>
      {{ current.display_name }}
    </span>
    <span v-else class="muted">—</span>
    <span v-if="canAssign" class="caret" aria-hidden="true">▾</span>
  </button>

  <Teleport to="body">
    <template v-if="open">
      <!-- 外側を押したら閉じる。`StatusDropdown` と同じ透明の膜 -->
      <div class="scrim" @click="close" @contextmenu.prevent="close"></div>
      <div
        ref="panel"
        class="panel"
        :style="panelStyle"
        @keydown.escape="close"
      >
        <!-- **インクリメンタルサーチ**（5.4）。打つたびに下の一覧が絞られる -->
        <input
          ref="search"
          v-model="query"
          type="text"
          class="search"
          placeholder="名前で絞り込む"
          aria-label="担当を名前で絞り込む"
          @keydown.enter.prevent="onEnter"
        />

        <div class="options" role="listbox">
          <!-- **「未割当」は絞り込みに関わらず先頭に残す。** 外す操作は名前で
               引けない（名前が無い）ので、絞り込みの対象にすると届かなくなる -->
          <button
            type="button"
            class="option"
            role="option"
            :aria-selected="current === null"
            @click="choose(null)"
          >
            <span class="muted">未割当</span>
          </button>

          <button
            v-for="m in filtered"
            :key="m.actor_id"
            type="button"
            class="option"
            role="option"
            :aria-selected="current?.id === m.actor_id"
            @click="choose(m.actor_id)"
          >
            <span class="actor-mark" aria-hidden="true">{{ mark(m.kind) }}</span>
            {{ m.display_name }}
          </button>

          <p v-if="filtered.length === 0" class="note">該当する人がいません</p>
        </div>
      </div>
    </template>
  </Teleport>
</template>

<style scoped>
/* 一覧の担当セルの見た目をそのまま持つ（5.4「実行者を担当と同じセルに置く」）。
   **担当の名前は縮み、記号は縮まない** */
.assignee-trigger {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  min-width: 0;
  max-width: 100%;
  height: 22px;
  padding: 0 var(--pb-space-1);
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.assignee-trigger:hover:not(:disabled) {
  border-color: var(--pb-border);
}

.assignee-trigger.plain {
  cursor: default;
}

.assignee-name {
  overflow: hidden;
  min-width: 0;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* 記号は淡色（5.4 の担当セルと同じ）。**縮まない**——縮むのは名前のほうで、
   記号を先に削ると「担当が付いていない」と読めてしまう */
.actor-mark {
  flex: none;
  color: var(--pb-text-muted);
}

.caret {
  flex: none;
  color: var(--pb-text-muted);
}

.muted {
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
  display: flex;
  max-width: 320px;
  flex-direction: column;
  gap: var(--pb-space-1);
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  box-shadow: var(--pb-shadow-2);
}

.search {
  width: 100%;
  height: 28px;
  box-sizing: border-box;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: var(--pb-text);
  font: inherit;
  font-size: 13px;
}

/* **候補だけをスクロールさせる。** 検索欄ごと流れると、絞り込みの最中に
   打ち込み先が視野から消える */
.options {
  max-height: 40vh;
  overflow-y: auto;
}

.option {
  display: flex;
  width: 100%;
  align-items: center;
  gap: 4px;
  padding: var(--pb-space-2);
  border: none;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.option:hover {
  background: var(--pb-hover);
}

.option[aria-selected='true'] {
  background: var(--pb-elevated);
}

.note {
  margin: 0;
  padding: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}
</style>
