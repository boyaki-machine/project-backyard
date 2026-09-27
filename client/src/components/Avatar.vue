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
 * **登録画像があれば画像、なければ表示名の先頭1文字を描く。**
 * 画像は `actor_avatar` に保存する（pb-19）。
 *
 * 担当や書き手など、名前と一緒に表示する場所で使う。実行者の 🤖 バッジは
 * チケットの進行状態を示すため、このアバターとは別に扱う。
 */
import { computed, ref, watch } from 'vue'

const props = defineProps<{
  /** `ActorRef.display_name` */
  name: string
  /** `ActorRef.kind`（`user` / `agent` / `system`） */
  kind: string
  /** アクター参照では ID から画像取得口を組み立てる。 */
  id?: string
  /** セッションのように登録状態が分かるときは URL を直接渡す。 */
  url?: string | null
  /** 24px（既定）か 20px。コメントは 24、密な一覧は 20 */
  size?: number
}>()

const isAgent = computed(() => props.kind === 'agent')
const failed = ref(false)
const ready = ref(false)
const source = computed(() => props.kind === 'user' ? (props.url || (props.id ? `/api/v1/actors/${encodeURIComponent(props.id)}/avatar` : null)) : null)
watch(source, () => { failed.value = false; ready.value = false })

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
    <img v-if="source && !failed" :src="source" :class="{ ready }" alt="" aria-hidden="true" @load="ready = true" @error="failed = true" />
    <span v-if="!ready || failed" aria-hidden="true">{{ initial }}</span>
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

.pb-avatar img {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  border-radius: inherit;
  object-fit: cover;
  visibility: hidden;
}

.pb-avatar img.ready { visibility: visible; }

.sr-only {
  position: absolute;
  overflow: hidden;
  clip-path: inset(50%);
  width: 1px;
  height: 1px;
  white-space: nowrap;
}
</style>
