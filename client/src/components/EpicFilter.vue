<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * エピックの複数選択フィルタ（`GuiDesign.md` 5.4「エピックをフィルタにする」）。
 *
 * **エピックは行として出さず、ここに集約する。** グルーピング専用であり
 * （`DbDesign.md` 6.6）、行として並ぶと「やるべき仕事」の数に混ざって、
 * 次に何をやるかを決める走査の妨げになる。
 *
 * **絞り込みの実体は `parent`（部分木）であって種別ではない**（`ApiDesign.md`
 * 9.2.1）。選んだ `seq` をカンマで並べると OR になる。組み立ては呼び出し側
 * （バックログ画面）が URL のクエリで行い、ここは選択の値だけを扱う。
 *
 * **`<select multiple>` を使わない。** 縦に伸びてフィルタ行の高さがそろわず、
 * 画面の縦を食う（原則1）。`UserActionsMenu`（5.6）と同じく `<Teleport>` で
 * body へ出し、ボタンの実測位置に `fixed` で置く——フィルタ行の祖先が
 * `overflow: auto` を持つため、その中に描くと切り取られる。
 *
 * **エピック自身もチケットである**ので、各行に詳細への導線を置く（5.4「編集」）。
 * **手順17b で実画面になった**——`↗` は右の詳細ペインを開く（2.2.1）。
 * **クエリを持ち回る**（3.2）ので、開いてもエピックの絞り込みは外れない。
 *
 * **末尾に `[+ 新規エピック]` を置く**（5.4「新規作成」。pb-14）。ここでは入力させず、
 * 呼び出し側が新規チケットのモーダルを種別エピックで開く——このパネルは外側を押す・
 * スクロールすると閉じるので、打ちかけのタイトルが消える。
 */
import { computed, nextTick, onBeforeUnmount, ref, useTemplateRef } from 'vue'

import { ticketTypeIcons } from '../api/tickets'
import type { Ticket } from '../api/tickets'

/** パネルの幅。位置決めにも使うので1か所に置く */
const PANEL_W = 340

const props = defineProps<{
  /** `GET /tickets?type=epic` の結果（5.4「フィルタ」の語彙の取得） */
  epics: Ticket[]
  /** 選択中の `seq`。URL のクエリ `parent` から復元される */
  selected: number[]
  projectKey: string
  /** `[+ 新規エピック]` を出すか（`ticket.create`。5.4「新規作成」） */
  canCreate?: boolean
  /**
   * `↗` の行き先に足すクエリ（pb-66）。チケット検索は `{ from: 'search' }` を渡す
   * ——**落とすと、押した先の詳細の後ろがバックログに変わる**（`GuiDesign.md` 3.2）
   */
  linkQuery?: Record<string, string>
}>()

const emit = defineEmits<{ update: [seqs: number[]]; create: [] }>()

const open = ref(false)
const trigger = useTemplateRef<HTMLButtonElement>('trigger')
const panel = useTemplateRef<HTMLElement>('panel')

/**
 * ラベル（5.4 のワイヤーの `[2件選択 ▾]`）。
 *
 * **1件でもタイトルを出さない。** 選ぶたびに幅が変わると、隣のフィルタの
 * 位置が動いて押し間違える。
 */
const label = computed(() =>
  props.selected.length === 0 ? uiText("すべて") : uiText("{value0}件選択", { value0: props.selected.length }),
)

/** パネルの位置。開いた時点のボタンの実測位置から決める */
const pos = ref({ top: 0, left: 0 })

function place(): void {
  const el = trigger.value
  if (!el) return
  const r = el.getBoundingClientRect()
  // 行の数に `[+ 新規エピック]` の1行を足す
  const estimated = (Math.max(props.epics.length, 1) + (props.canCreate ? 1 : 0)) * 34 + 8
  const below = window.innerHeight - r.bottom
  pos.value = {
    // 下に入らなければボタンの上へ出す（狭い窓で画面外へ落とさない）
    top: below < estimated && r.top > estimated ? r.top - estimated - 4 : r.bottom + 4,
    // 右端からはみ出すときだけ左へずらす
    left: Math.max(Math.min(r.left, window.innerWidth - PANEL_W - 4), 4),
  }
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
  place()
  open.value = true
  await nextTick()
  attach()
  panel.value?.querySelector<HTMLElement>('input[type="checkbox"]')?.focus()
}

function onDocumentPointerDown(e: PointerEvent): void {
  const t = e.target as Node | null
  if (t && (panel.value?.contains(t) || trigger.value?.contains(t))) return
  close()
}

function onKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape') {
    e.preventDefault()
    close()
  }
}

/**
 * 外側のスクロールと窓の大きさの変化で閉じる（`GuiDesign.md` 6.1「浮かせたパネルを閉じる規則」）。
 */
function attach(): void {
  document.addEventListener('pointerdown', onDocumentPointerDown, true)
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', close)
}

function detach(): void {
  document.removeEventListener('pointerdown', onDocumentPointerDown, true)
  window.removeEventListener('keydown', onKeydown)
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', close)
}

onBeforeUnmount(detach)

/**
 * 選択の付け外し。**パネルは閉じない**——複数選ぶための部品であり、
 * 1件ごとに閉じると選び直しのたびに開き直すことになる。
 */
function choose(seq: number): void {
  const next = props.selected.includes(seq)
    ? props.selected.filter((v) => v !== seq)
    : [...props.selected, seq]
  emit('update', next)
}

/** 新規エピック。**パネルを閉じてから**呼び出し側へ渡す——モーダルの下にパネルを残さない */
function startCreate(): void {
  close()
  emit('create')
}

const panelStyle = computed(() => ({
  top: `${pos.value.top}px`,
  left: `${pos.value.left}px`,
}))
</script>

<template>
  <button
    ref="trigger"
    type="button"
    class="trigger"
    :aria-label="$ui('エピック：{value0}', { value0: label })"
    :aria-expanded="open"
    aria-haspopup="true"
    @click.stop="toggle"
  >
    <span class="trigger-label">{{ label }}</span>
    <span class="caret" aria-hidden="true">▾</span>
  </button>

  <Teleport to="body">
    <div v-if="open" ref="panel" class="epic-panel" :style="panelStyle" @click.stop>
      <p v-if="epics.length === 0" class="empty">{{ $ui('エピックはまだありません') }}</p>
      <div v-for="e in epics" :key="e.seq" class="epic-row">
        <label class="epic-choice">
          <input
            type="checkbox"
            :checked="selected.includes(e.seq)"
            :aria-label="e.title"
            @change="choose(e.seq)"
          />
          <span class="epic-icon" aria-hidden="true">{{ ticketTypeIcons.epic }}</span>
          <span class="epic-title" :title="e.title">{{ e.title }}</span>
        </label>
        <!-- エピック自身もチケットである（5.4「編集」）。詳細へ抜ける導線を置く -->
        <RouterLink
          class="epic-open"
          :to="{
            path: `/p/${projectKey}/tickets/${e.seq}`,
            query: { ...$route.query, ...(linkQuery ?? {}) },
          }"
          :aria-label="$ui('{value0}-{value1} {value2} の詳細を開く', { value0: projectKey, value1: e.seq, value2: e.title })"
          :title="$ui('{value0}-{value1} の詳細を開く', { value0: projectKey, value1: e.seq })"
        >
          ↗
        </RouterLink>
      </div>
      <!-- 新規エピック（5.4「新規作成」。pb-14）。選択肢と混ざらないよう罫線で区切る -->
      <div v-if="canCreate" class="epic-create">
        <button type="button" class="epic-create-button" @click="startCreate">{{ $ui('+ 新規エピック') }}</button>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
/* フィルタ行の `select` と同じ見え方にそろえる（並んだときに高さが揃う） */
.trigger {
  display: inline-flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-2);
  width: 180px;
  height: 32px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.trigger:hover,
.trigger[aria-expanded='true'] {
  background: var(--pb-hover);
}

.trigger-label {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.caret {
  flex: none;
  color: var(--pb-text-muted);
}

/* body へ teleport するので scoped の親子関係に頼らない。
   z-index はページヘッダ（5）とモーダル（100）の間に置く（`UserActionsMenu` と同じ） */
.epic-panel {
  position: fixed;
  z-index: 50;
  width: 340px;
  max-height: 60vh;
  overflow-y: auto;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-elevated);
  box-shadow: var(--pb-shadow-2);
}

.epic-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
}

.epic-choice {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-2);
  border-radius: var(--pb-radius);
  cursor: pointer;
}

.epic-choice:hover {
  background: var(--pb-hover);
}

.epic-icon {
  flex: none;
  color: var(--pb-text-muted);
}

.epic-title {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.epic-open {
  flex: none;
  padding: 0 var(--pb-space-2);
  color: var(--pb-text-muted);
  line-height: 28px;
  text-decoration: none;
}

.epic-open:hover {
  color: var(--pb-text);
  text-decoration: underline;
}

/* 文言は**テンプレート側で行を折り返さない**（折り返した位置に空白が入る）。
   ここでの折り返しは描画上のもので、それは構わない */
.empty {
  margin: 0;
  padding: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.epic-create {
  margin-top: var(--pb-space-1);
  padding-top: var(--pb-space-1);
  border-top: 1px solid var(--pb-border);
}

.epic-create-button {
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

.epic-create-button:hover {
  background: var(--pb-hover);
}
</style>
