<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * チケットのコメント（`GuiDesign.md` 5.5「コメント」、`ApiDesign.md` 9.8）。手順18b。
 *
 * **詳細ペインから切り出してある。** チケットの子資源でこれだけが**別の
 * エンドポイントとページングを持つ**ため、取得・続きの読み込み・投稿欄・返信を
 * 自分で抱える自己完結の単位になる。完了条件と関連チケットは詳細応答
 * （9.5.1）の `dod` / `links` を親が所有しているので切り出していない
 * ——切り出すと配列の持ち主が2か所に割れる。
 *
 * **`order=desc` で取り、描くときに反転する**（5.5）。サーバの既定は `asc` だが、
 * その1ページ目は**最も古い50件**になり、120件あると最新のコメントが3ページ目に
 * 沈む——**議論は新しいほうから読めなければならない。**
 *
 * **返信は段を付けず、両向きのリンクで示す**（利用者の判断、2026-08-27）。
 * 深さの上限・親が削除されたときの並び・折りたたみの既定を決めずにインデントを
 * 入れると、返信が続いた時点で 750px のペインで本文が読めなくなる。
 *
 * **見出しの件数は親が持つ `comment_count` である**（`deleted_at IS NULL`）。
 * ここが持つ `total` は**削除済みも数える**ので答えが違う（9.8。意図的）。
 */
import { computed, defineAsyncComponent, nextTick, ref, useTemplateRef, watch } from 'vue'

import Avatar from './Avatar.vue'
import ConfirmDialog from './ConfirmDialog.vue'
/** 型だけを取り込む（実行時の import を生まないので、下の分割を打ち消さない） */
import type MarkdownEditorComponent from './MarkdownEditor.vue'
import { ApiError } from '../api/client'
import * as commentsApi from '../api/comments'
import {
  commentKindLabels,
  commentKindOptions,
  defaultCommentKind,
} from '../api/comments'
import type { CommentKind, TicketComment } from '../api/comments'
import { formatDateTime } from '../lib/datetime'
import { renderMarkdown } from '../lib/markdown'
import { useAuthStore } from '../stores/auth'

/**
 * **編集器は使うときに読み込む**（`GuiDesign.md` 5.10、`fix/split-markdown-chunk`）。
 *
 * **`MarkdownEditor` の利用者は3つある**——チケットの説明欄（`TicketDetailPane`）、
 * ここ（コメントの投稿と編集）、Docs（5.10）。**1つでも同期で取り込んでいると
 * CodeMirror は初回チャンクに残る**ので、3つとも動的にする（ビルドが
 * `INEFFECTIVE_DYNAMIC_IMPORT` で教えてくれる）。
 */
const MarkdownEditor = defineAsyncComponent(() => import('./MarkdownEditor.vue'))

const props = defineProps<{
  projectKey: string
  seq: number
}>()

const emit = defineEmits<{
  /**
   * 読めるコメントの件数が動いた。**親の `comment_count` を足し引きする**
   * ——見出しに使うのは詳細応答のこの値であって、ここの `total` ではない。
   */
  'count-delta': [delta: number]
}>()

const auth = useAuthStore()

const canCreate = computed(() => auth.canInProject(props.projectKey, 'comment.create'))
const canEditOwn = computed(() => auth.canInProject(props.projectKey, 'comment.edit_own'))
const canDeleteAny = computed(() =>
  auth.canInProject(props.projectKey, 'comment.delete_any'),
)

const myActorId = computed(() => auth.actor?.id ?? null)

// ── 取得（9.8）─────────────────────────────────────────────

/** **古い順に持つ**（描く順そのもの）。取得は `desc` なので入れるときに反転する */
const items = ref<TicketComment[]>([])
const page = ref(1)
const totalPages = ref(1)
const total = ref(0)
const loading = ref(false)
const loadingMore = ref(false)
const loadError = ref('')
const busy = ref(false)

const PER_PAGE = 50

const hasMore = computed(() => page.value < totalPages.value)

function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
}

async function load(): Promise<void> {
  loading.value = true
  loadError.value = ''
  items.value = []
  page.value = 1
  try {
    const res = await commentsApi.listComments(props.projectKey, props.seq, {
      page: 1,
      per_page: PER_PAGE,
      order: 'desc',
    })
    items.value = [...res.items].reverse()
    total.value = res.total
    totalPages.value = res.total_pages
  } catch (e) {
    loadError.value = toApiError(e).message
  } finally {
    loading.value = false
  }
}

/**
 * 以前のコメントを読む。**前へ足す**——`desc` の次ページはより古い50件である。
 *
 * **これで「返信先」のリンクが解決することがある。** 親は返信より古いので、
 * 後のページに居ることがあるためで、逆は起こらない（`parentOf` の注記）。
 */
async function loadMore(): Promise<void> {
  if (!hasMore.value || loadingMore.value) return
  loadingMore.value = true
  loadError.value = ''
  try {
    const next = page.value + 1
    const res = await commentsApi.listComments(props.projectKey, props.seq, {
      page: next,
      per_page: PER_PAGE,
      order: 'desc',
    })
    items.value = [...[...res.items].reverse(), ...items.value]
    page.value = next
    total.value = res.total
    totalPages.value = res.total_pages
  } catch (e) {
    loadError.value = toApiError(e).message
  } finally {
    loadingMore.value = false
  }
}

// ── 返信の索引（5.5「返信」）───────────────────────────────

const byId = computed(() => {
  const m = new Map<string, TicketComment>()
  for (const c of items.value) m.set(c.id, c)
  return m
})

/**
 * 親 → 返信。**これは常に完全である**——返信は必ず親より新しいので、
 * 親が読み込まれているならその返信も読み込まれている（`desc` で取るため）。
 */
const repliesOf = computed(() => {
  const m = new Map<string, TicketComment[]>()
  for (const c of items.value) {
    if (c.in_reply_to === null) continue
    const list = m.get(c.in_reply_to)
    if (list === undefined) m.set(c.in_reply_to, [c])
    else list.push(c)
  }
  return m
})

/**
 * 返信 → 親。**`undefined` になることがある**——親は返信より古いので、
 * まだ読み込んでいないページに居ることがある。そのときはリンクにせず
 * 「以前のコメント」とだけ出す（5.5）。
 */
function parentOf(c: TicketComment): TicketComment | undefined {
  return c.in_reply_to === null ? undefined : byId.value.get(c.in_reply_to)
}

/** 飛んだ先を一瞬だけ強調する。**位置だけ動かすと、どれに着いたか分からない** */
const highlightId = ref<string | null>(null)
let highlightTimer: ReturnType<typeof setTimeout> | null = null

function jumpTo(id: string): void {
  const el = document.getElementById(`comment-${id}`)
  if (el === null) return
  el.scrollIntoView({ block: 'center', behavior: 'smooth' })
  highlightId.value = id
  if (highlightTimer !== null) clearTimeout(highlightTimer)
  highlightTimer = setTimeout(() => {
    highlightId.value = null
    highlightTimer = null
  }, 1500)
}

// ── 投稿（9.8）─────────────────────────────────────────────

/**
 * 投稿欄は**閉じているとき1行の `textarea`**（5.5）。開くと `MarkdownEditor` と
 * `kind` の選択と `[投稿]` が出る。**常設すると「プレビュー／まだ何も書かれて
 * いません」の枠が居座り、縦を1つ余分に食う**（原則1）。
 */
const composerOpen = ref(false)
const draft = ref('')
const draftKind = ref<CommentKind>(defaultCommentKind)
const replyTo = ref<TicketComment | null>(null)
const postError = ref('')

const composerRef = useTemplateRef<InstanceType<typeof MarkdownEditorComponent>>('composerRef')

const canPost = computed(() => draft.value.trim() !== '' && !busy.value)

function openComposer(): void {
  if (!canCreate.value) return
  composerOpen.value = true
}

function closeComposer(): void {
  composerOpen.value = false
  draft.value = ''
  draftKind.value = defaultCommentKind
  replyTo.value = null
  postError.value = ''
}

async function startReply(c: TicketComment): Promise<void> {
  if (!canCreate.value || c.deleted_at !== null) return
  replyTo.value = c
  composerOpen.value = true
  postError.value = ''
  await nextTick()
  composerRef.value?.focus()
}

async function post(): Promise<void> {
  const body = draft.value.trim()
  if (body === '' || busy.value) return
  busy.value = true
  postError.value = ''
  try {
    const created = await commentsApi.createComment(props.projectKey, props.seq, {
      body_md: body,
      kind: draftKind.value,
      // **返信でないときはキーごと送らない。** `null` を送っても同じだが、
      // 送らないほうが 9.8 の既定に素直である
      ...(replyTo.value === null ? {} : { in_reply_to: replyTo.value.id }),
    })
    // **末尾に足す**——古い順に持っており、投稿したものが最も新しい
    items.value = [...items.value, created]
    total.value += 1
    emit('count-delta', 1)
    closeComposer()
  } catch (e) {
    postError.value = toApiError(e).message
  } finally {
    busy.value = false
  }
}

// ── 編集（9.8 の `PATCH`）──────────────────────────────────

const editingId = ref<string | null>(null)
const editDraft = ref('')
const editKind = ref<CommentKind>(defaultCommentKind)
const editError = ref('')

function canEdit(c: TicketComment): boolean {
  return (
    c.deleted_at === null &&
    canEditOwn.value &&
    myActorId.value !== null &&
    c.author.id === myActorId.value
  )
}

/**
 * 削除できるか（9.8）。**必要権限は OR である**——`comment.delete_any`、
 * または `comment.edit_own` かつ自分のもの。
 */
function canDelete(c: TicketComment): boolean {
  if (c.deleted_at !== null) return false
  if (canDeleteAny.value) return true
  return (
    canEditOwn.value && myActorId.value !== null && c.author.id === myActorId.value
  )
}

function startEdit(c: TicketComment): void {
  if (!canEdit(c)) return
  editingId.value = c.id
  editDraft.value = c.body_md ?? ''
  editKind.value = c.kind
  editError.value = ''
}

function cancelEdit(): void {
  editingId.value = null
  editDraft.value = ''
  editError.value = ''
}

async function commitEdit(): Promise<void> {
  const id = editingId.value
  const body = editDraft.value.trim()
  if (id === null || body === '' || busy.value) return
  const target = items.value.find((c) => c.id === id)
  if (target === undefined) return

  // **変わっていないなら送らない**（5.5「編集の単位」と同じ規則）
  if (body === (target.body_md ?? '') && editKind.value === target.kind) {
    cancelEdit()
    return
  }

  busy.value = true
  editError.value = ''
  try {
    const updated = await commentsApi.updateComment(props.projectKey, props.seq, id, {
      body_md: body,
      kind: editKind.value,
    })
    replaceItem(updated)
    cancelEdit()
  } catch (e) {
    // **編集モードのまま留め、入力値を捨てない**（6.4）
    editError.value = toApiError(e).message
  } finally {
    busy.value = false
  }
}

function replaceItem(next: TicketComment): void {
  const i = items.value.findIndex((c) => c.id === next.id)
  if (i >= 0) items.value[i] = next
}

// ── 削除（9.8。論理削除）───────────────────────────────────

const toDelete = ref<TicketComment | null>(null)
const deleteError = ref('')

const deleteMessage = computed(() => {
  const c = toDelete.value
  if (c === null) return ''
  const mine = myActorId.value !== null && c.author.id === myActorId.value
  const head = uiText("このコメントを削除します。本文は画面から読めなくなります。")
  return mine ? head : uiText("{value0}\n{value1} さんが書いたコメントです。", { value0: head, value1: c.author.display_name })
})

async function runDelete(): Promise<void> {
  const c = toDelete.value
  if (c === null) return
  busy.value = true
  deleteError.value = ''
  try {
    await commentsApi.deleteComment(props.projectKey, props.seq, c.id)
    // **論理削除なので行は残る**（9.8）。`204` は本体を返さないので、
    // サーバが返すのと同じ形へ手元で倒す——**画面に出るのは
    // 「削除されました」の1行だけ**で、`deleted_at` の値そのものは出さない
    replaceItem({ ...c, body_md: null, deleted_at: new Date().toISOString() })
    emit('count-delta', -1)
    toDelete.value = null
  } catch (e) {
    toDelete.value = null
    deleteError.value = toApiError(e).message
  } finally {
    busy.value = false
  }
}

// ── 表示のための小さな関数 ───────────────────────────────────

function rendered(c: TicketComment): string {
  return renderMarkdown(c.body_md ?? '')
}

/** 編集されたか。**秒精度で返る**ので、同じ秒の更新は「編集済み」に見えない */
function isEdited(c: TicketComment): boolean {
  return c.deleted_at === null && c.updated_at !== c.created_at
}

/** 返信先のチップに出す1行。**誰のどの類型か**が分かれば足りる */
function briefOf(c: TicketComment): string {
  return `${commentKindLabels[c.kind]} ${c.author.display_name} ${formatDateTime(c.created_at)}`
}

// ── 起動と追随 ───────────────────────────────────────────────

/**
 * **この `watch` はスクリプトの末尾に置く**（`GuiDesign.md` 7.4）。
 * `immediate: true` は setup のその場でコールバックを走らせるので、上に置くと
 * まだ宣言されていない `const`（ref）に触れて TDZ の `ReferenceError` になる。
 */
watch(
  () => [props.projectKey, props.seq],
  () => {
    closeComposer()
    cancelEdit()
    toDelete.value = null
    deleteError.value = ''
    void load()
  },
  { immediate: true },
)

defineExpose({ reload: load })
</script>

<template>
  <div class="tc">
    <p v-if="loading" class="tc-note">{{ $ui('読み込み中…') }}</p>
    <p v-else-if="loadError" class="tc-note tc-error" role="alert">
      {{ loadError }}
      <button type="button" class="tc-retry" @click="load">{{ $ui('再試行') }}</button>
    </p>

    <template v-else>
      <!-- **続きは前へ足す**（`desc` の次ページはより古い50件）。
           これで「返信先」のリンクが解決することがある -->
      <button
        v-if="hasMore"
        type="button"
        class="tc-more"
        :disabled="loadingMore"
        @click="loadMore"
      >
        {{ loadingMore ? $ui("読み込み中…") : $ui("以前のコメントを読む（残り {value0} 件）", { value0: total - items.length }) }}
      </button>

      <ol v-if="items.length > 0" class="tc-list">
        <li
          v-for="c in items"
          :id="`comment-${c.id}`"
          :key="c.id"
          class="tc-item"
          :class="{ highlight: highlightId === c.id, removed: c.deleted_at !== null }"
        >
          <Avatar class="tc-avatar" :name="c.author.display_name" :kind="c.author.kind" />

          <div class="tc-body">
            <div class="tc-head">
              <span class="tc-author">{{ c.author.display_name }}</span>
              <span class="tc-kind">{{ commentKindLabels[c.kind] }}</span>
              <span class="tc-time">{{ formatDateTime(c.created_at) }}</span>
              <span v-if="isEdited(c)" class="tc-edited">{{ $ui('（編集済み）') }}</span>

              <span class="tc-actions">
                <button
                  v-if="canCreate && c.deleted_at === null"
                  type="button"
                  class="tc-action"
                  @click="startReply(c)"
                > {{ $ui('返信') }} </button>
                <button
                  v-if="canEdit(c) && editingId !== c.id"
                  type="button"
                  class="tc-action"
                  @click="startEdit(c)"
                > {{ $ui('編集') }} </button>
                <button
                  v-if="canDelete(c)"
                  type="button"
                  class="tc-action tc-danger"
                  @click="toDelete = c"
                > {{ $ui('削除') }} </button>
              </span>
            </div>

            <!-- 返信 → 親（5.5）。**親が未読み込みならリンクにしない** -->
            <p v-if="c.in_reply_to !== null" class="tc-reply-to">
              <span aria-hidden="true">↩</span>
              <template v-if="parentOf(c)"> {{ $ui('返信先:') }} <button type="button" class="tc-jump" @click="jumpTo(c.in_reply_to)">
                  {{ briefOf(parentOf(c)!) }}
                </button>
              </template>
              <template v-else> {{ $ui('返信先:') }} <span class="tc-muted">{{ $ui('以前のコメント') }}</span>
              </template>
            </p>

            <!-- 編集中（9.8 の `PATCH` は `body_md` と `kind` だけを受ける） -->
            <template v-if="editingId === c.id">
              <MarkdownEditor v-model="editDraft" @cancel="cancelEdit" />
              <div class="tc-editbar">
                <label class="tc-kind-pick">
                  <span class="tc-kind-label">{{ $ui('類型') }}</span>
                  <select v-model="editKind">
                    <option v-for="k in commentKindOptions" :key="k" :value="k">
                      {{ commentKindLabels[k] }}
                    </option>
                  </select>
                </label>
                <span class="tc-spacer"></span>
                <button type="button" class="secondary" @click="cancelEdit">{{ $ui('キャンセル') }}</button>
                <button
                  type="button"
                  class="primary"
                  :disabled="editDraft.trim() === '' || busy"
                  @click="commitEdit"
                > {{ $ui('保存') }} </button>
              </div>
              <p v-if="editError" class="tc-field-error" role="alert">{{ editError }}</p>
            </template>

            <!-- 削除済みは本文の代わりに1行（9.8。行そのものは残す） -->
            <p v-else-if="c.deleted_at !== null" class="tc-removed-note">{{ $ui('削除されました') }}</p>

            <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
            <div v-else class="markdown-body tc-text" v-html="rendered(c)"></div>

            <!-- 親 → 返信（5.5）。**常に完全である**（返信は親より新しい） -->
            <p v-if="repliesOf.get(c.id)?.length" class="tc-replies"> {{ $ui('返信') }} {{ repliesOf.get(c.id)!.length }}{{ $ui('件:') }} <button
                v-for="r in repliesOf.get(c.id)!"
                :key="r.id"
                type="button"
                class="tc-jump"
                @click="jumpTo(r.id)"
              >
                ↓{{ r.author.display_name }}
              </button>
            </p>
          </div>
        </li>
      </ol>

      <p v-else class="tc-empty">{{ $ui('コメントはまだありません') }}</p>

      <p v-if="deleteError" class="tc-field-error" role="alert">{{ deleteError }}</p>

      <!-- ── 投稿欄（5.5「投稿・編集・削除」）───────────────── -->
      <div v-if="canCreate" class="tc-composer">
        <!-- 返信先のチップ。`×` で通常の投稿に戻る -->
        <p v-if="replyTo" class="tc-replying">
          <span aria-hidden="true">↩</span> {{ $ui('返信先:') }} {{ briefOf(replyTo) }}
          <button
            type="button"
            class="tc-chip-clear"
            :aria-label="$ui('返信をやめる')"
            @click="replyTo = null"
          >
            ✕
          </button>
        </p>

        <!-- 閉じているときは1行。**フォーカスで開く**（5.5） -->
        <textarea
          v-if="!composerOpen"
          class="tc-seed"
          rows="1"
          :placeholder="$ui('コメントを書く…')"
          @focus="openComposer"
        ></textarea>

        <template v-else>
          <MarkdownEditor ref="composerRef" v-model="draft" @cancel="closeComposer" />
          <div class="tc-editbar">
            <label class="tc-kind-pick">
              <span class="tc-kind-label">{{ $ui('類型') }}</span>
              <select v-model="draftKind">
                <option v-for="k in commentKindOptions" :key="k" :value="k">
                  {{ commentKindLabels[k] }}
                </option>
              </select>
            </label>
            <span class="tc-spacer"></span>
            <button type="button" class="secondary" @click="closeComposer">{{ $ui('キャンセル') }}</button>
            <button type="button" class="primary" :disabled="!canPost" @click="post"> {{ $ui('投稿') }} </button>
          </div>
          <p v-if="postError" class="tc-field-error" role="alert">{{ postError }}</p>
        </template>
      </div>
    </template>

    <ConfirmDialog
      v-if="toDelete"
      :title="$ui('コメントを削除しますか？')"
      :message="deleteMessage"
      :confirm-label="$ui('削除する')"
      danger
      :busy="busy"
      @cancel="toDelete = null"
      @confirm="runDelete"
    />
  </div>
</template>

<style scoped>
/* **クラス名は部品名を冠する**（6.6）。`.item` / `.head` / `.actions` は
   画面をまたいで衝突する——`base.css` の正本や `SideMenu` と当たった前例がある */
.tc {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
  min-width: 0;
}

.tc-note {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.tc-error {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  color: var(--pb-danger-text);
}

.tc-retry,
.tc-more {
  padding: var(--pb-space-1) var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: inherit;
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.tc-more {
  align-self: flex-start;
}

.tc-more:hover:not(:disabled),
.tc-retry:hover {
  background: var(--pb-hover);
}

.tc-list {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}

.tc-item {
  display: flex;
  gap: var(--pb-space-3);
  min-width: 0;
  padding: var(--pb-space-1);
  border-radius: var(--pb-radius);
  /* 飛んだ先の強調が「点いて消える」ように見えるようにする */
  transition: background-color 0.4s ease;
}

.tc-item.highlight {
  background: var(--pb-active);
}

.tc-avatar {
  margin-top: 2px;
}

.tc-body {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  gap: var(--pb-space-1);
  /* **主役には `min-width` を添える**（6.7）。添えないと `flex: none` の
     アバターが残って本文のほうが先に潰れる */
  min-width: 6em;
}

.tc-head {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--pb-space-2);
  min-width: 0;
}

.tc-author {
  font-size: 13px;
  font-weight: 600;
}

/* 類型のチップ。**枠線＋文字で表す**（8.6。有彩色は使わない） */
.tc-kind {
  flex: none;
  padding: 1px var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: 999px;
  color: var(--pb-text-muted);
  font-size: 11px;
}

.tc-time,
.tc-edited {
  color: var(--pb-text-muted);
  font-size: 12px;
}

/* 操作は行の右端へ寄せる。**折り返しても右に残る** */
.tc-actions {
  display: flex;
  gap: var(--pb-space-2);
  margin-left: auto;
}

.tc-action {
  padding: 0;
  border: none;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.tc-action:hover {
  color: var(--pb-text);
  text-decoration: underline;
}

.tc-danger:hover {
  color: var(--pb-danger-text);
}

/* ── 返信の両向きリンク（5.5「返信」）───────────────────── */

.tc-reply-to,
.tc-replies {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--pb-space-1);
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.tc-jump {
  padding: 0;
  border: none;
  background: none;
  color: var(--pb-accent);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.tc-jump:hover {
  text-decoration: underline;
}

.tc-muted {
  color: var(--pb-text-muted);
}

.tc-text {
  min-width: 0;
  font-size: 13px;
}

.tc-removed-note {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
  font-style: italic;
}

.tc-item.removed .tc-author {
  font-weight: 400;
}

.tc-empty {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ── 投稿欄 ───────────────────────────────────────────────── */

.tc-composer {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  min-width: 0;
  padding-top: var(--pb-space-3);
  border-top: 1px solid var(--pb-line);
}

.tc-replying {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
  align-self: flex-start;
  margin: 0;
  padding: 2px var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: 999px;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.tc-chip-clear {
  padding: 0 2px;
  border: none;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 11px;
  cursor: pointer;
}

.tc-chip-clear:hover {
  color: var(--pb-danger-text);
}

/* 閉じているときの1行。**開いたら `MarkdownEditor` に置き換わる** */
.tc-seed {
  width: 100%;
  min-height: 36px;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
  resize: none;
}

.tc-editbar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
}

.tc-kind-pick {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}

.tc-kind-label {
  color: var(--pb-text-muted);
  font-size: 12px;
}

.tc-kind-pick select {
  height: 30px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
}

.tc-spacer {
  flex: 1 1 auto;
}

.tc-field-error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
