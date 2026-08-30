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
 * **保存するのは `secondary` 側の幅**（2.2.1 は `pb.detail_pane_w`。`pb.theme` /
 * `pb.menu_collapsed` と同じ置き場）。保存値が無いあいだは `primary` が下限に張り付き、
 * `secondary` が余りを取る——2.2.1 の「メニューを畳めば詳細が 750px から 934px へ広がる」
 * はこの状態の話である。**利用者が幅を決めたら、そちらを優先する**
 * （決めた値をメニューの開閉で書き換えない。2.2.1 と同じ考え）。
 *
 * **スロットの役割は位置ではなく振る舞いで決まる。** `primary` が可変（余りを取る）、
 * `secondary` が固定（幅を保存する）である。**`side` はその固定側を左右どちらに
 * 置くかだけを決める**（`GuiDesign.md` 6.1）。Docs（5.10）はこれを2枚入れ子にして
 * 3ペインを作る——外側は木を固定して本文が余りを取り、内側は編集器を固定して
 * 可視化が余りを取る。
 *
 * **`side` / `collapseTo` / `defaultSecondary` は追加のみで、既定は従来の挙動である。**
 * 渡さない限りバックログ（2.2.1）の動きは1つも変わらない。
 */
import { computed, onBeforeUnmount, onMounted, ref, useTemplateRef } from 'vue'

const props = withDefaults(
  defineProps<{
    /** `secondary` を出すか。`false` なら `primary` が全幅になり、境界も出ない */
    open: boolean
    /** 幅を覚える `localStorage` のキー（2.2.1 は `pb.detail_pane_w`） */
    storageKey: string
    /** 可変側の下限。2.2.1 の「一覧は約450pxへ縮む」 */
    minPrimary?: number
    /** 固定側の下限。2.4 の「5.5 のメタ情報が2列で収まる下限」 */
    minSecondary?: number
    /**
     * 固定側（`secondary`）をどちらに置くか。
     *
     * `'end'`（既定）＝右。`'start'`＝左（Docs の文書ツリーと編集ペイン。5.10）。
     * **DOM の順序ごと入れ替える**——`order` や `row-reverse` で見た目だけ入れ替えると、
     * **タブ移動の順序が見た目と食い違う**（9.2）。
     */
    side?: 'start' | 'end'
    /**
     * 並べられない幅になったとき、**どちらを残すか**。
     *
     * 既定は `'secondary'`（2.2.1 の「詳細が全幅になり、一覧は隠れる」）。
     * Docs の外側は `'primary'` を使う——**残すべきは本文であって木ではない**。
     * 逆にすると、狭い窓で文書を1件も読めなくなる。
     */
    collapseTo?: 'primary' | 'secondary'
    /**
     * 保存値がまだ無いときの固定側の幅。
     *
     * 省略すると「`primary` を下限に張り付けて残り全部」（2.2.1 の既定）。
     * **Docs は木が 840px で開いてしまうため実数を渡す**（5.10 のワイヤーの 240px）。
     */
    defaultSecondary?: number
  }>(),
  { minPrimary: 450, minSecondary: 510, side: 'end', collapseTo: 'secondary', defaultSecondary: undefined },
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
  const wanted = savedWidth.value ?? props.defaultSecondary
  if (wanted === undefined) return max
  return Math.min(Math.max(wanted, props.minSecondary), max)
})

/**
 * 描くペイン。**並べられないときに残す側は `collapseTo` が決める。**
 *
 * `v-if` で消すのは、DOM に残すと `j` / `k` のフォーカス移動が見えない行へ入るため
 * （2.2.1）。**残らなかった側は全幅になる**——`primary` は `flex: 1 1 auto`、
 * `secondary` は `.full` が引き受ける。
 */
const showPrimary = computed(
  () => !props.open || sideBySide.value || props.collapseTo === 'primary',
)
const showSecondary = computed(
  () => props.open && (sideBySide.value || props.collapseTo === 'secondary'),
)

/** 並ぶときだけ幅を指定する。並ばないときは `.full` が全幅を取る */
const secondaryStyle = computed(() =>
  sideBySide.value ? { width: `${secondaryWidth.value}px` } : undefined,
)

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
  // 固定側の端からの距離がそのまま固定側の幅になる。下限で丸めるのは computed の側。
  // **`side` で基準の端が変わる**——右に置いたなら右端から、左に置いたなら左端から測る
  writeSaved(props.side === 'start' ? e.clientX - r.left : r.right - e.clientX)
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
  // **キーの向きは画面の向きに合わせる。** 固定側が左にあるとき、右キーで
  // 広がらないと「押した向きと動く向きが逆」になる
  const grow = props.side === 'start' ? 'ArrowRight' : 'ArrowLeft'
  const shrink = props.side === 'start' ? 'ArrowLeft' : 'ArrowRight'
  if (e.key === grow) writeSaved(secondaryWidth.value + step)
  else if (e.key === shrink) writeSaved(secondaryWidth.value - step)
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
  <div ref="root" class="split" :class="{ dragging, 'side-start': side === 'start' }">
    <!-- 固定側を左に置く形（Docs の文書ツリー・編集ペイン。5.10）。
         **DOM の順序ごと入れ替える**——`order` で見た目だけ動かすと
         タブ移動が見た目と食い違う（9.2） -->
    <template v-if="showSecondary && side === 'start'">
      <div class="split-secondary" :class="{ full: !sideBySide }" :style="secondaryStyle">
        <slot name="secondary" />
      </div>
      <!-- 並ばないときは境界も出さない。動かせないものを掴めるように見せない -->
      <div
        v-if="sideBySide"
        class="divider"
        role="separator"
        aria-orientation="vertical"
        aria-label="ペインの境界。左右キーで幅を変えられます"
        tabindex="0"
        @pointerdown="onPointerDown"
        @pointermove="onPointerMove"
        @pointerup="onPointerUp"
        @pointercancel="onPointerUp"
        @keydown="onKeydown"
      ></div>
    </template>

    <div v-if="showPrimary" class="split-primary">
      <slot name="primary" />
    </div>

    <template v-if="showSecondary && side === 'end'">
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

      <div class="split-secondary" :class="{ full: !sideBySide }" :style="secondaryStyle">
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

/* 固定側を左に置く形（`side="start"`）。**線と負の余白の向きだけが裏返る。**
   線は `split-secondary` の border が引くので、そちらを右へ移し、帯はその上に
   左向きの負の余白で重ねる（`z-index` は上の規則がそのまま効く） */
.side-start .split-secondary {
  border-right: 1px solid var(--pb-line);
  border-left: none;
}

.side-start .split-secondary.full {
  border-right: none;
}

.side-start .divider {
  margin-right: 0;
  margin-left: -9px;
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
