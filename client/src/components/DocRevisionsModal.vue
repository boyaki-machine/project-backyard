<script setup lang="ts">
/**
 * 文書の履歴（`GuiDesign.md` 5.10「表示と編集」／`ApiDesign.md` 10.5）。手順22c。
 *
 * **モーダルにする理由**（5.10）——10.5 は 20件のページャを持ち、版を選ぶと本文が
 * 丸ごと入れ替わる。可視化ペインを乗っ取ると「いま読んでいるのが現在の版か過去の
 * 版か」が読めなくなり、`[ 編集 ]` と競合する。**閉じれば現在の版に戻る。**
 *
 * **中は左に一覧、右に本文の2ペイン**で、`Modal` の幅の変種を使う（6.1）。
 * 5.10「出さないもの」が**差分ビューを Phase 3 からここへ回す**と定めており、
 * この場所には後から左右に並べるものが増える。
 *
 * **一覧は `body_md` を持たない**（10.5）ので、1件を選んだときに
 * `.../_revisions/:no` をもう1回叩く。20件ぶんの Markdown を先に取らない。
 *
 * **最新の版には `[ この版に戻す ]` を出さない**（5.10）。リビジョンは内容が
 * 変わったときだけ積まれるので（10.4）**最新の版＝いまの本文**であり、戻しても
 * `version` が +1 するだけになる。**押せる操作が何も起こさない状態を作らない。**
 */
import { computed, onMounted, ref } from 'vue'

import Avatar from './Avatar.vue'
import Modal from './Modal.vue'
import { ApiError } from '../api/client'
import {
  getDocRevision,
  listDocRevisions,
  updateDoc,
  type Doc,
  type DocRevision,
  type DocRevisionItem,
  type DocTreeItem,
} from '../api/docs'
import { formatDateTime } from '../lib/datetime'
import { renderMarkdown } from '../lib/markdown'

const props = defineProps<{
  projectKey: string
  /**
   * 対象の文書。**目次の行をそのまま受ける**（10.2）——`version` を持っているので、
   * `[ この版に戻す ]` の `If-Match` のために本文を取り直さずに済む。
   */
  doc: DocTreeItem
  /** `doc.edit` を持つか。持たない人に `[ この版に戻す ]` を出さない（設計原則4） */
  canEdit: boolean
}>()

const emit = defineEmits<{ close: []; reverted: [doc: Doc] }>()

/** 10.5 の既定。**サーバの既定と同じ値を明示する**（`UsersPage` と同じ方針） */
const PER_PAGE = 20

// ── 一覧（10.5）──────────────────────────────────────────────

const items = ref<DocRevisionItem[]>([])
const page = ref(1)
const total = ref(0)
const totalPages = ref(1)
const listLoading = ref(true)
const listError = ref<string | null>(null)

/**
 * 最新の版の番号。**1ページ目の先頭から取る**——10.5 は `revision_no` の降順に
 * 固定なので、1ページ目の先頭が必ず最新である。**`total` から導かない**：
 * 「番号が連番で欠けない」は 10.5 が約束していない性質で、そこに寄りかかると
 * 将来リビジョンを間引く仕様が入ったときに静かに壊れる。
 */
const latestNo = ref<number | null>(null)

async function loadList(): Promise<void> {
  listLoading.value = true
  listError.value = null
  try {
    const res = await listDocRevisions(props.projectKey, props.doc.path, {
      page: page.value,
      per_page: PER_PAGE,
    })
    items.value = res.items
    page.value = res.page
    total.value = res.total
    totalPages.value = res.total_pages
    if (res.page === 1 && res.items.length > 0) latestNo.value = res.items[0]!.revision_no
  } catch (e) {
    listError.value = e instanceof ApiError ? e.message : '履歴を取得できませんでした'
    items.value = []
  } finally {
    listLoading.value = false
  }
}

function goToPage(next: number): void {
  if (next < 1 || next > totalPages.value) return
  page.value = next
  void loadList()
}

// ── 選んだ1件（10.5）────────────────────────────────────────

const selectedNo = ref<number | null>(null)
const revision = ref<DocRevision | null>(null)
const bodyLoading = ref(false)
const bodyError = ref<string | null>(null)

async function select(no: number): Promise<void> {
  selectedNo.value = no
  revision.value = null
  bodyLoading.value = true
  bodyError.value = null
  revertError.value = null
  try {
    revision.value = await getDocRevision(props.projectKey, props.doc.path, no)
  } catch (e) {
    bodyError.value = e instanceof ApiError ? e.message : '版の本文を取得できませんでした'
  } finally {
    bodyLoading.value = false
  }
}

const bodyHtml = computed(() =>
  revision.value === null ? '' : renderMarkdown(revision.value.body_md),
)

const isLatest = computed(
  () => selectedNo.value !== null && selectedNo.value === latestNo.value,
)

// ── 戻す（10.5）─────────────────────────────────────────────

/**
 * いまの文書の `version`。**戻すたびに更新する**——モーダルを開いたまま2回
 * 戻すと、1回目の `PATCH` で `version` が +1 しており、開いたときの値では
 * `409 conflict` になる。
 */
const currentVersion = ref(props.doc.version)

const reverting = ref(false)
const revertError = ref<string | null>(null)

/**
 * `[ この版に戻す ]`（10.5）。**専用のエンドポイントは無い**ので、取得した
 * `title` と `body_md` を `PATCH` で書き戻す。**書き戻しも新しい版として積まれ、
 * 履歴は消えない。**
 *
 * **戻すのは `title` と `body_md` の2つ**（10.5、利用者の判断 2026-08-30）。
 * 10.4 がリビジョンを積む条件を「`title` か `body_md` が変わったとき」と定めて
 * いる以上、版の中身はこの2つで1組である。`slug` と `parent_path` は戻さない
 * ——文書の場所は内容ではなく構造で、「移動・改名」が扱う。
 */
async function revert(): Promise<void> {
  const rev = revision.value
  if (rev === null || reverting.value) return
  reverting.value = true
  revertError.value = null
  try {
    const next = await updateDoc(props.projectKey, props.doc.path, currentVersion.value, {
      title: rev.title,
      body_md: rev.body_md,
      // **戻したことを履歴に残す。** `change_reason` は「その版の中身を説明する」
      // ものであり（10.5）、書き戻しの中身はまさに「#N の内容」である。
      // 空のまま積むと、手で編集した版と見分けがつかない
      change_reason: `#${rev.revision_no} の内容に戻した`,
    })
    currentVersion.value = next.version
    emit('reverted', next)
    // 版が1つ増えたので、1ページ目から取り直す（新しい版が先頭に来る）
    page.value = 1
    await loadList()
    await select(rev.revision_no)
  } catch (e) {
    revertError.value = e instanceof ApiError ? e.message : 'この版に戻せませんでした'
  } finally {
    reverting.value = false
  }
}

onMounted(async () => {
  await loadList()
  // **開いた直後に右のペインを空にしない。** 最新の版を選んでおくと、
  // 「いまの本文」から読み始められる（戻すボタンは出ない＝押せる操作が
  // 何も起こさない状態にならない）
  if (items.value.length > 0) await select(items.value[0]!.revision_no)
})
</script>

<template>
  <Modal size="wide" :title="`履歴：${doc.title}`" @close="emit('close')">
    <div class="rev-layout">
      <!-- ── 左：一覧（10.5）────────────────────────────── -->
      <div class="rev-list-pane">
        <div class="rev-list-scroll">
          <p v-if="listLoading" class="rev-note">読み込み中…</p>
          <p v-else-if="listError" class="rev-note rev-error">✕ {{ listError }}</p>
          <p v-else-if="items.length === 0" class="rev-note">履歴がありません</p>
          <ul v-else class="rev-list">
            <li v-for="r in items" :key="r.revision_no">
              <button
                type="button"
                class="rev-item"
                :class="{ 'rev-item-selected': selectedNo === r.revision_no }"
                :aria-current="selectedNo === r.revision_no"
                @click="select(r.revision_no)"
              >
                <span class="rev-item-head">
                  <span class="rev-no">#{{ r.revision_no }}</span>
                  <span v-if="r.revision_no === latestNo" class="rev-current">現在の版</span>
                  <span class="rev-date">{{ formatDateTime(r.created_at) }}</span>
                </span>
                <span class="rev-item-who">
                  <!-- **`changed_by` は `null` になりうる**（`ON DELETE SET NULL`）
                       ——文書は書いた人が消えても内容が生き続ける（10.3） -->
                  <template v-if="r.changed_by">
                    <Avatar
                      :name="r.changed_by.display_name"
                      :kind="r.changed_by.kind"
                      :size="18"
                    />
                    <span class="rev-name">{{ r.changed_by.display_name }}</span>
                  </template>
                  <span v-else class="rev-name rev-muted">—</span>
                </span>
                <span v-if="r.change_reason" class="rev-reason">{{ r.change_reason }}</span>
              </button>
            </li>
          </ul>
        </div>

        <!-- 件数は常に、ページャは2ページ以上のときだけ出す（`UsersPage` と同じ体裁） -->
        <div v-if="!listError && !listLoading" class="rev-pager">
          <span class="rev-count">{{ total }}件</span>
          <template v-if="totalPages > 1">
            <button
              type="button"
              class="rev-page-button"
              :disabled="page <= 1"
              @click="goToPage(page - 1)"
            >
              ◀ 前
            </button>
            <span class="rev-page-number">{{ page }} / {{ totalPages }}</span>
            <button
              type="button"
              class="rev-page-button"
              :disabled="page >= totalPages"
              @click="goToPage(page + 1)"
            >
              次 ▶
            </button>
          </template>
        </div>
      </div>

      <!-- ── 右：選んだ版の本文（10.5）──────────────────── -->
      <div class="rev-detail-pane">
        <p v-if="bodyLoading" class="rev-note">読み込み中…</p>
        <p v-else-if="bodyError" class="rev-note rev-error">✕ {{ bodyError }}</p>
        <p v-else-if="revision === null" class="rev-note">左の一覧から版を選んでください</p>
        <template v-else>
          <div class="rev-detail-head">
            <h3 class="rev-detail-title">
              #{{ revision.revision_no }} {{ revision.title }}
            </h3>
            <p class="rev-detail-meta">
              <span>{{ formatDateTime(revision.created_at) }}</span>
              <template v-if="revision.changed_by">
                <Avatar
                  :name="revision.changed_by.display_name"
                  :kind="revision.changed_by.kind"
                  :size="20"
                />
                <span>{{ revision.changed_by.display_name }}</span>
              </template>
              <span v-else class="rev-muted">—</span>
            </p>
            <p class="rev-detail-reason">
              更新理由：{{ revision.change_reason ?? '—' }}
            </p>
          </div>
          <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
          <div v-if="bodyHtml" class="markdown-body" v-html="bodyHtml"></div>
          <p v-else class="rev-note">この版には本文がありません</p>
        </template>
      </div>
    </div>

    <template #footer>
      <p v-if="revertError" class="rev-foot-error">✕ {{ revertError }}</p>
      <button type="button" class="secondary" :disabled="reverting" @click="emit('close')">
        閉じる
      </button>
      <!-- **最新の版には出さない**（5.10）。戻しても `version` が +1 するだけになる -->
      <button
        v-if="canEdit && revision !== null && !isLatest"
        type="button"
        class="primary"
        :disabled="reverting"
        @click="revert"
      >
        {{ reverting ? '戻しています…' : 'この版に戻す' }}
      </button>
    </template>
  </Modal>
</template>

<style scoped>
/* **高さを決め打つ。** 版によって本文の長さが違うため、成り行きに任せると
   選ぶたびにモーダルの高さが跳ねる。**両ペインは中で独立にスクロールする** */
.rev-layout {
  display: flex;
  height: 60vh;
  min-height: 280px;
  margin: calc(var(--pb-space-4) * -1);
}

.rev-list-pane {
  display: flex;
  flex: none;
  flex-direction: column;
  width: 260px;
  min-width: 0;
  border-right: 1px solid var(--pb-line);
}

.rev-list-scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
}

.rev-detail-pane {
  flex: 1 1 auto;
  min-width: 0;
  overflow-y: auto;
  padding: var(--pb-space-4);
}

/* 窓が狭いとモーダル自体が縮む。**左右に並べる幅が無くなったら縦に積む**
   ——260px の一覧を残したまま本文を 170px まで潰さない（6.7） */
@media (max-width: 720px) {
  .rev-layout {
    flex-direction: column;
  }

  .rev-list-pane {
    width: auto;
    max-height: 40%;
    border-right: 0;
    border-bottom: 1px solid var(--pb-line);
  }
}

/* ── 一覧 ───────────────────────────────────────────────── */

.rev-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.rev-item {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 100%;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 0;
  border-bottom: 1px solid var(--pb-line);
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.rev-item:hover {
  background: var(--pb-hover);
}

.rev-item-selected,
.rev-item-selected:hover {
  background: var(--pb-active);
}

.rev-item-head {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
}

.rev-no {
  flex: none;
  font-variant-numeric: tabular-nums;
  font-weight: 600;
}

/* 「現在の版」は枠線＋文字（8.6 のバッジ）。面で塗ると選択中と紛れる */
.rev-current {
  flex: none;
  padding: 0 4px;
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  color: var(--pb-text-muted);
  font-size: 11px;
}

.rev-date {
  flex: 1 1 auto;
  min-width: 0;
  overflow: hidden;
  color: var(--pb-text-muted);
  font-size: 12px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.rev-item-who {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
  min-width: 0;
}

.rev-name {
  overflow: hidden;
  color: var(--pb-text-muted);
  font-size: 12px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.rev-muted {
  color: var(--pb-text-muted);
}

/* 更新理由は2行までで打ち切る。**一覧の行の高さを版ごとに変えない** */
.rev-reason {
  display: -webkit-box;
  overflow: hidden;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  color: var(--pb-text-muted);
  font-size: 12px;
}

/* ── ページャ ───────────────────────────────────────────── */

.rev-pager {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--pb-space-2);
  padding: var(--pb-space-2) var(--pb-space-3);
  border-top: 1px solid var(--pb-line);
  color: var(--pb-text-muted);
  font-size: 12px;
}

.rev-count {
  flex: 1 1 auto;
}

.rev-page-button {
  height: 24px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: inherit;
  font: inherit;
  cursor: pointer;
}

.rev-page-button:hover:not(:disabled) {
  background: var(--pb-hover);
}

.rev-page-button:disabled {
  cursor: default;
  opacity: 0.5;
}

.rev-page-number {
  font-variant-numeric: tabular-nums;
}

/* ── 本文 ───────────────────────────────────────────────── */

.rev-detail-head {
  margin-bottom: var(--pb-space-3);
  padding-bottom: var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
}

.rev-detail-title {
  margin: 0 0 var(--pb-space-2);
  font-size: 15px;
}

.rev-detail-meta {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  margin: 0 0 var(--pb-space-1);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.rev-detail-reason {
  margin: 0;
  overflow-wrap: anywhere;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.rev-note {
  margin: 0;
  padding: var(--pb-space-3);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.rev-error {
  color: var(--pb-danger-text);
}

/* 誤りはフッタの左端へ。**ボタンの隣に出す**ので、押した場所で結果が読める（6.4） */
.rev-foot-error {
  flex: 1 1 auto;
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}

.primary,
.secondary {
  display: inline-flex;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border-radius: var(--pb-radius);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}

button:disabled {
  cursor: default;
  opacity: 0.5;
}
</style>
