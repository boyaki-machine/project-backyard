<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * アクターのアバター（`GuiDesign.md` 6.1 / 8.4.2）。手順18b で実体化した。
 *
 * **人＝円、エージェント＝角丸四角。色は使わない**（8.4.2「恒常的に表示される
 * 要素からは色を外す」）。担当者列やコメント欄に常時並ぶものは**属性**であり、
 * 紫（`--pb-ai`）は「**未確認の**AI出力」という**状態**に予約してある。
 * 形で区別すれば、紫の総量が「レビューが追いついていない量」を表し続ける。
 *
 * **中身は表示名の先頭1文字である。** 画像を持たない——`actor` に
 * アバター画像の列が無く（`DbDesign.md` 6.1）、Phase 1 に置き場も無い。
 *
 * **`ticket.ts` の `actorMark`（🤖 / 👤）とは役割が違う。** あちらは1行の中で
 * 担当を示す記号で、こちらは**書き手を面として示す**もの。コメントは書き手が
 * 読みの単位なので、行頭に箱が要る。
 */
import { computed } from 'vue'

const props = defineProps<{
  /** `ActorRef.display_name` */
  name: string
  /** `ActorRef.kind`（`user` / `agent` / `system`） */
  kind: string
  /** 24px（既定）か 20px。コメントは 24、密な一覧は 20 */
  size?: number
}>()

const isAgent = computed(() => props.kind === 'agent')

/**
 * 先頭1文字。**サロゲートペアで割らない**——絵文字や一部の漢字は
 * `charAt(0)` だと壊れた半端な文字になる。
 */
const initial = computed(() => {
  const trimmed = props.name.trim()
  if (trimmed === '') return '?'
  return [...trimmed][0] ?? '?'
})

const px = computed(() => props.size ?? 24)

/** **形が唯一の区別である**ため、読み上げにも言葉で残す（8.4.2） */
const title = computed(
  () => uiText("{value0}（{value1}）", { value0: props.name, value1: isAgent.value ? uiText("エージェント") : uiText("人") }),
)
</script>

<template>
  <span
    class="pb-avatar"
    :class="{ agent: isAgent }"
    :style="{ width: `${px}px`, height: `${px}px`, fontSize: `${Math.round(px * 0.5)}px` }"
    :title="title"
  >
    <span aria-hidden="true">{{ initial }}</span>
    <span class="sr-only">{{ title }}</span>
  </span>
</template>

<style scoped>
/* **クラス名は部品名を冠する**（6.6）。`.avatar` は画面をまたいで衝突しうる */
.pb-avatar {
  display: inline-flex;
  flex: none;
  align-items: center;
  justify-content: center;
  border: 1px solid var(--pb-border);
  /* **人は円。** 8.4.2 の「形状で区別する」 */
  border-radius: 50%;
  background: var(--pb-surface);
  color: var(--pb-text-muted);
  font-weight: 600;
  line-height: 1;
  user-select: none;
  /* **`.sr-only` の位置の基準になる**（手順19b）。付けないと読み上げ用の
     `position: absolute` が初期包含ブロックを基準に置かれ、**スクロール領域の
     外へはみ出して文書そのものを縦に伸ばす**。症状は「アプリ全体が上へ
     スクロールして下に余白が出る」で、`AppShell` の `.content` が
     `overflow: hidden` でも防げない（絶対配置は祖先の overflow を無視する）。
     ダッシュボードの「最近の動き」と履歴でアバターが縦に並んで表面化した
     ——1440px で文書が 46〜69px 伸びるのを実測。 */
  position: relative;
}

/* **エージェントは角丸四角。** 色は変えない */
.pb-avatar.agent {
  border-radius: 6px;
}

.sr-only {
  position: absolute;
  overflow: hidden;
  clip-path: inset(50%);
  width: 1px;
  height: 1px;
  white-space: nowrap;
}
</style>
