<script setup lang="ts">
/**
 * 一覧と詳細の境界（`GuiDesign.md` 6.1 / 2.2.1「詳細ペイン」）。
 *
 * **押し込む形であり、重ねない。** 詳細が一覧の上に乗るオーバーレイは、
 * 覆われた列が「読めないのに在る」状態になり、**クリックできる範囲と
 * 見える範囲が食い違う**（2.2.1 の表）。
 *
 * **並ぶかどうかは窓の幅ではなく、この部品が受け取った幅で決まる**（2.4）。
 * 同じ窓幅でもメインメニューの折りたたみで使える幅が 184px 変わるため、
 * 窓を基準にすると「畳んだのに並ばない」が起きる。閾値 960px は
 * 一覧の 450px に詳細の下限 510px を足したものである。
 *
 * **保存するのは詳細ペインの幅**（`pb.detail_pane_w`。`pb.theme` /
 * `pb.menu_collapsed` と同じ置き場）。保存値が無いあいだは一覧が下限に張り付き、
 * 詳細が余りを取る——2.2.1 の「メニューを畳めば詳細が 750px から 934px へ広がる」
 * はこの状態の話である。**利用者が幅を決めたら、そちらを優先する**
 * （決めた値をメニューの開閉で書き換えない。2.2.1 と同じ考え）。
 */
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'

const props = withDefaults(
  defineProps<{
    /** 右のペインを出すか。`false` なら左が全幅になり、境界も出ない */
    open: boolean
    /** 幅を覚える `localStorage` のキー（2.2.1 は `pb.detail_pane_w`） */
    storageKey: string
    /** 左（一覧）の下限。2.2.1 の「一覧は約450pxへ縮む」 */
    minPrimary?: number
    /** 右（詳細）の下限。2.4 の「5.5 のメタ情報が2列で収まる下限」 */
    minSecondary?: number
  }>(),
  { minPrimary: 450, minSecondary: 510 },
)

const root = useTemplateRef<HTMLDivElement>('root')

/** この部品が実際に持っている幅。**窓幅ではない**（2.4） */
const hostWidth = ref(0)

/** 保存された詳細の幅。`null` は「利用者がまだ決めていない」 */
const savedWidth = ref<number | null>(null)

/**
 * 並べられるか（2.4）。**下限どうしの和で決める。**
 * 満たさないときは詳細が全幅になり、一覧は隠れる。
 */
const sideBySide = computed(
  () => hostWidth.value >= props.minPrimary + props.minSecondary,
)

/**
 * 詳細ペインの幅。
 *
 * **保存値が無ければ「一覧を下限にして残り全部」**（2.2.1 の既定 750px 前後）。
 * 保存値があれば、両側の下限に収まる範囲へ丸めて使う——窓を狭めたときに
 * 保存値をそのまま使うと、一覧が下限を割って列が潰れる。
 */
const secondaryWidth = computed(() => {
  if (!sideBySide.value) return hostWidth.value
  const max = hostWidth.value - props.minPrimary
  if (savedWidth.value === null) return max
  return Math.min(Math.max(savedWidth.value, props.minSecondary), max)
})

function readSaved(): void {
  try {
    const raw = localStorage.getItem(props.storageKey)
    const n = raw === null ? Number.NaN : Number(raw)
    savedWidth.value = Number.isFinite(n) && n > 0 ? n : null
  } catch {
    // 読めなくても既定の幅で開ける。幅は失われてよい情報である
    savedWidth.value = null
  }
}

function writeSaved(px: number): void {
  savedWidth.value = px
  try {
    localStorage.setItem(props.storageKey, String(Math.round(px)))
  } catch {
    // プライベートモード等。保存できなくても、この場のドラッグは効いている
  }
}

// ── 境界のドラッグ ───────────────────────────────────────────
//
// **HTML5 の D&D ではなくポインタ操作である。** 掴んでいる間ずっと幅を
// 追随させたいので、`dragover` の粒度では足りない。`setPointerCapture` を
// 使うと、ポインタが枠の外へ出ても離すまで追える。

const dragging = ref(false)

function onPointerDown(e: PointerEvent): void {
  if (!sideBySide.value) return
  const el = e.currentTarget as HTMLElement
  el.setPointerCapture(e.pointerId)
  dragging.value = true
  e.preventDefault()
}

function onPointerMove(e: PointerEvent): void {
  if (!dragging.value || root.value === null) return
  const r = root.value.getBoundingClientRect()
  // 右端からの距離がそのまま詳細の幅になる。下限で丸めるのは computed の側
  writeSaved(r.right - e.clientX)
}

function onPointerUp(e: PointerEvent): void {
  if (!dragging.value) return
  const el = e.currentTarget as HTMLElement
  el.releasePointerCapture(e.pointerId)
  dragging.value = false
}

/**
 * キーボードでも動かせるようにする（9.2「すべての操作要素がキーボードで
 * 到達可能」）。**5.6 の列幅つまみを例外にした理由はここには無い**——
 * 詳細ペインを畳めないと、一覧の列が狭いまま到達できない情報が出る。
 */
function onKeydown(e: KeyboardEvent): void {
  if (!sideBySide.value) return
  const step = e.shiftKey ? 64 : 16
  if (e.key === 'ArrowLeft') writeSaved(secondaryWidth.value + step)
  else if (e.key === 'ArrowRight') writeSaved(secondaryWidth.value - step)
  else return
  e.preventDefault()
}

// ── 幅の観測 ─────────────────────────────────────────────────

let observer: ResizeObserver | null = null

onMounted(() => {
  readSaved()
  if (root.value === null) return
  hostWidth.value = root.value.getBoundingClientRect().width
  // **窓の resize では足りない**——メインメニューの折りたたみは窓を変えずに
  // この部品の幅を 184px 動かす（2.4）
  observer = new ResizeObserver((entries) => {
    const w = entries[0]?.contentRect.width
    if (w !== undefined) hostWidth.value = w
  })
  observer.observe(root.value)
})

onBeforeUnmount(() => {
  observer?.disconnect()
  observer = null
})

defineExpose({ sideBySide })
</script>

<template>
  <div ref="root" class="split" :class="{ dragging }">
    <!-- 一覧。**並ばないときは隠す**（2.4）。`v-if` で消すのは、DOM に
         残すと `j` / `k` のフォーカス移動が見えない行へ入るため -->
    <div v-if="!open || sideBySide" class="split-primary">
      <slot name="primary" />
    </div>

    <template v-if="open">
      <!-- 並ばないときは境界も出さない。動かせないものを掴めるように見せない -->
      <div
        v-if="sideBySide"
        class="divider"
        role="separator"
        aria-orientation="vertical"
        aria-label="一覧と詳細の境界。左右キーで幅を変えられます"
        tabindex="0"
        @pointerdown="onPointerDown"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
        @pointercancel="onPointerUp"
        @keydown="onKeydown"
      ></div>

      <div
        class="split-secondary"
        :class="{ full: !sideBySide }"
        :style="sideBySide ? { width: `${secondaryWidth}px` } : undefined"
      >
        <slot name="secondary" />
      </div>
    </template>
  </div>
</template>

<style scoped>
.split {
  display: flex;
  min-width: 0;
  height: 100%;
}

/* **クラス名を `primary` / `secondary` にしてはいけない。** `base.css` が
   ボタンの正本として同じ名前を持っており（手順13b）、**scoped スタイルは
   見た目を分けても DOM のクラス名は分けない**ので、一覧ペインが
   `--pb-accent` の面で塗り潰される。実際に踏んだ（スクリーンショットで発覚。
   自動検証は全 PASS のままだった）。

   **主役は余りを受け取る側にする。** `flex` を書かないと `0 1 auto` になり、
   幅を持つ詳細が残ったまま一覧だけが 0 まで潰れる（手順16d-b の教訓） */
.split-primary {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  height: 100%;
}

.split-secondary {
  display: flex;
  flex: none;
  flex-direction: column;
  min-width: 0;
  height: 100%;
  border-left: 1px solid var(--pb-line);
}

/* 並ばないとき（この部品の幅が 960px 未満）は詳細が全幅を取る。
   **`:not([style])` で見分けない**——属性が出るかどうかは描画側の都合で、
   状態の判定に使うと静かにずれる */
.split-secondary.full {
  flex: 1 1 auto;
  border-left: none;
}

/* 掴む帯。**線は 1px だが当たり判定は 9px**——1px の線は狙って掴めない。
   線そのものは `split-secondary` の border が引いているので、ここは負の余白で重ねる。

   **`z-index` を必ず添える。** 負の余白で重ねると、DOM 順で後ろにある
   `split-secondary` が上に載り、**帯を一度も掴めない**（実際に踏んだ。
   `getBoundingClientRect()` は妥当な箱を返すので実測では拾えず、
   実マウスで回して初めて分かった） */
.divider {
  position: relative;
  z-index: 1;
  flex: none;
  width: 9px;
  margin-right: -9px;
  cursor: col-resize;
  touch-action: none;
}

.divider:hover,
.divider:focus-visible {
  background: var(--pb-hover);
}

/* 掴んでいる間はページ全体で文字を選ばせない。掴んだまま動かすと、
   境界の左右にある一覧と本文が反転して読めなくなる */
.split.dragging {
  user-select: none;
}

.split.dragging .divider {
  background: var(--pb-active);
}
</style>
