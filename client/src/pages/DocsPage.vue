<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * Docs（`GuiDesign.md` 5.10）。必要権限は `doc.view`、編集は `doc.edit`。手順22b。
 *
 * プロジェクト文書（憲章）を読み書きする。**規約・価値観・判断の基準を1か所に置き、
 * 全参加者のエージェントが同じものを読む**ための画面である（`Requirements.md` 10.6.2）。
 *
 * **3ペインである**——文書ツリー／編集ペイン／可視化ペイン。
 * **編集ペインは編集モードのときだけ現れ**、書きながら横で描画を確かめられる。
 * 並ばない幅では可視化ペインを畳み、`MarkdownEditor` の既定（ソースの下にプレビュー）
 * へ落とす（5.10「幅が足りないとき」）。
 *
 * **`SplitPane` を2枚入れ子にして作る。** 外側は木を固定して本文が余りを取り、
 * 内側は編集器を固定して可視化が余りを取る。**新しい3分割部品を作らない**——
 * 同型の部品が並ぶと片方に入れた直しがもう片方に入らない。
 *
 * **`MarkdownEditor` は遅延読み込みにする**（5.10）。client の初回チャンクが
 * 980KB になっている原因が CodeMirror であり、**閲覧だけの利用者に編集器を配らない。**
 *
 * **`doc.edit` を持たない人には `[ 編集 ]` `[ + 文書を追加 ]` `[⋯]` を出さない**
 * （設計原則4）。`operator` と `project_member` がこれに当たり（`DbDesign.md` 8.1.4）、
 * **「権限による出し分けの負の側」をこの画面で
 * 実地に確かめられる**（`Design.md` 付録A）。
 *
 * **手順22c で足したもの**：木のドラッグ&ドロップ、`[⋯]` の「移動・改名」「履歴」。
 * **ドラッグの状態はここが持つ**——`DocTree` は自分自身を再帰的に描くので、
 * 部品側に持たせると段をまたいで共有されない。
 */
import { computed, defineAsyncComponent, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import Avatar from '../components/Avatar.vue'
import ConfirmDialog from '../components/ConfirmDialog.vue'
import DocFormModal from '../components/DocFormModal.vue'
import DocRevisionsModal from '../components/DocRevisionsModal.vue'
import DocTree, { type DocDropHint, type DocDropZone } from '../components/DocTree.vue'
import EmptyState from '../components/EmptyState.vue'
import PageHeader from '../components/PageHeader.vue'
import SplitPane from '../components/SplitPane.vue'
import UserActionsMenu, { type ActionItem } from '../components/UserActionsMenu.vue'
import { ApiError } from '../api/client'
import * as docsApi from '../api/docs'
import type { Doc, DocTreeItem } from '../api/docs'
import { formatDateTime } from '../lib/datetime'
import { renderMarkdown } from '../lib/markdown'
import { useAuthStore } from '../stores/auth'

/**
 * **編集器は使うときに読み込む**（5.10）。`import()` にすると Vite が
 * CodeMirror を別チャンクへ切り出すので、閲覧だけの利用者は取得しない。
 */
const MarkdownEditor = defineAsyncComponent(() => import('../components/MarkdownEditor.vue'))

const props = defineProps<{ projectKey: string }>()

const auth = useAuthStore()
const route = useRoute()
const router = useRouter()

/** 木の開閉を覚える置き場（7.1）。**畳んだものだけを持つので既定は全展開** */
const COLLAPSED_KEY = 'pb.docs_tree_collapsed'

const canEdit = computed(() => auth.canInProject(props.projectKey, 'doc.edit'))

// ── 目次（10.2）────────────────────────────────────────────

const tree = ref<DocTreeItem[]>([])
const treeLoading = ref(true)
const treeError = ref<string | null>(null)

async function loadTree(): Promise<void> {
  treeLoading.value = true
  treeError.value = null
  try {
    // **`?outline=1` を付けない**（10.2）。見出し一覧はエージェントが「どの章を
    // 読むか」を決めるための情報で、この画面は使わない
    tree.value = (await docsApi.listDocs(props.projectKey)).items
  } catch (e) {
    treeError.value = e instanceof ApiError ? e.message : uiText("文書の一覧を取得できませんでした")
  } finally {
    treeLoading.value = false
  }
}

/** 木を平らにして `path` で引けるようにする。祖先の展開と子の数え上げに要る */
interface FlatDoc {
  item: DocTreeItem
  depth: number
}

const flatDocs = computed<FlatDoc[]>(() => {
  const out: FlatDoc[] = []
  const walk = (items: DocTreeItem[], depth: number): void => {
    for (const item of items) {
      out.push({ item, depth })
      walk(item.children, depth + 1)
    }
  }
  walk(tree.value, 0)
  return out
})

const byPath = computed(() => new Map(flatDocs.value.map((f) => [f.item.path, f.item])))

/**
 * 親のパス。**`path` から末尾の `slug` を落として作る**——目次（10.2）は
 * `parent_path` を返さないが、`path` は `slug` を根から連ねたものなので（10.1）、
 * 最後の区切りで切れば親が出る。`null` はトップレベル。
 */
function parentPathOf(path: string): string | null {
  const cut = path.lastIndexOf('/')
  return cut < 0 ? null : path.slice(0, cut)
}

/** その親が持つ子の並び。`null` はトップレベル（＝木の根） */
function childrenOf(parentPath: string | null): DocTreeItem[] {
  if (parentPath === null) return tree.value
  return byPath.value.get(parentPath)?.children ?? []
}

/** `id` で木を引く。**移動のあと `path` が変わった行を追う**のに要る */
function findById(id: string): DocTreeItem | null {
  return flatDocs.value.find((f) => f.item.id === id)?.item ?? null
}

/** その文書と子孫の合計件数 - 1（＝子孫の数）。削除の確認に出す（6.3） */
function descendantCount(item: DocTreeItem): number {
  let n = 0
  const walk = (items: DocTreeItem[]): void => {
    for (const child of items) {
      n += 1
      walk(child.children)
    }
  }
  walk(item.children)
  return n
}

// ── 木の開閉（7.1）──────────────────────────────────────────

const collapsed = ref<Set<string>>(new Set())

function readCollapsed(): void {
  try {
    const raw = localStorage.getItem(COLLAPSED_KEY)
    const all = raw === null ? {} : (JSON.parse(raw) as Record<string, string[]>)
    collapsed.value = new Set(all[props.projectKey] ?? [])
  } catch {
    // 読めなくても全展開で開ける。開閉は失われてよい情報である
    collapsed.value = new Set()
  }
}

function writeCollapsed(): void {
  try {
    const raw = localStorage.getItem(COLLAPSED_KEY)
    const all = raw === null ? {} : (JSON.parse(raw) as Record<string, string[]>)
    all[props.projectKey] = [...collapsed.value]
    localStorage.setItem(COLLAPSED_KEY, JSON.stringify(all))
  } catch {
    // プライベートモード等。保存できなくても、この場の開閉は効いている
  }
}

function toggleCollapsed(path: string): void {
  if (collapsed.value.has(path)) collapsed.value.delete(path)
  else collapsed.value.add(path)
  // Set の中身を変えても参照は同じなので、明示的に作り直して追随させる
  collapsed.value = new Set(collapsed.value)
  writeCollapsed()
}

/**
 * 開いた文書の祖先を必ず展開する（5.10）。
 *
 * **共有された URL を開くと木も展開される**——畳んだままだと、選択中の行が
 * 木に見えない状態になる。
 */
function expandAncestors(path: string): void {
  const parts = path.split('/')
  let changed = false
  for (let i = 1; i < parts.length; i += 1) {
    const ancestor = parts.slice(0, i).join('/')
    if (collapsed.value.delete(ancestor)) changed = true
  }
  if (!changed) return
  collapsed.value = new Set(collapsed.value)
  writeCollapsed()
}

// ── 本文（10.3）────────────────────────────────────────────

/** URL が持つ現在のパス（5.10「URL は本文側が持つ」）。`null` は未選択 */
const currentPath = computed(() => {
  const p = route.params.path
  const value = Array.isArray(p) ? p.join('/') : p
  return typeof value === 'string' && value !== '' ? value : null
})

const doc = ref<Doc | null>(null)
const docLoading = ref(false)
const docError = ref<string | null>(null)

async function loadDoc(path: string | null): Promise<void> {
  cancelEdit()
  if (path === null) {
    doc.value = null
    docError.value = null
    return
  }
  docLoading.value = true
  docError.value = null
  try {
    doc.value = await docsApi.getDoc(props.projectKey, path)
    expandAncestors(path)
  } catch (e) {
    doc.value = null
    docError.value = e instanceof ApiError ? e.message : uiText("文書を取得できませんでした")
  } finally {
    docLoading.value = false
  }
}

/** いま開いている文書の子。可視化ペインの末尾に「配下の文書」として出す（5.10） */
const children = computed<DocTreeItem[]>(() => {
  if (doc.value === null) return []
  return byPath.value.get(doc.value.path)?.children ?? []
})

// ── 編集（10.4）────────────────────────────────────────────

const editing = ref(false)
const draft = ref('')
const changeReason = ref('')
const saving = ref(false)
const saveError = ref<ApiError | null>(null)

/**
 * 保存した版。**結果表示を入力の監視で消さない**ため、
 * 「サーバの値と一致している間だけ出す」と宣言的に書く（6.4）。
 */
const savedVersion = ref<number | null>(null)
const packModeSaving = ref(false)
const packModeError = ref('')

async function changePackMode(event: Event): Promise<void> {
  if (!doc.value || !canEdit.value || packModeSaving.value) return
  const value = (event.target as HTMLSelectElement).value as Doc['pack_mode']
  if (value === doc.value.pack_mode) return
  packModeSaving.value = true
  packModeError.value = ''
  try {
    doc.value = await docsApi.updateDoc(props.projectKey, doc.value.path, doc.value.version, { pack_mode: value })
    await loadTree()
  } catch (e) {
    packModeError.value = e instanceof ApiError ? e.message : uiText('掲載方法を変更できませんでした')
    ;(event.target as HTMLSelectElement).value = doc.value.pack_mode
  } finally {
    packModeSaving.value = false
  }
}

const conflict = computed(() => saveError.value?.status === 409)

/**
 * 可視化ペインに出す HTML。**編集中は保存済みの本文ではなく `draft` を描く**
 * ——5.10 が編集ペインを独立させた理由が「**書きながら横で描画を確かめられる**」
 * だからである。保存するまで描画が変わらないなら、3枚目は要らない。
 *
 * **デバウンスを挟まない。** `MarkdownEditor` は `preview` が真のとき既に毎打鍵で
 * 描画しており（可視化ペインが畳まれる幅では今日それが動いている）、こちらだけ
 * 遅らせると**同じ画面に2つの流儀が並ぶ**。描画の実測は 10KB の本文で約 1ms、
 * `GuiDesign.md` 相当の 160K字でも約 7ms である（`markdown-it`）。
 */
const bodyHtml = computed(() => {
  if (editing.value) return renderMarkdown(draft.value)
  return doc.value === null ? '' : renderMarkdown(doc.value.body_md)
})

function startEdit(): void {
  if (doc.value === null || !canEdit.value) return
  draft.value = doc.value.body_md
  changeReason.value = ''
  saveError.value = null
  savedVersion.value = null
  editing.value = true
}

function cancelEdit(): void {
  editing.value = false
  draft.value = ''
  changeReason.value = ''
  saveError.value = null
}

async function save(): Promise<void> {
  if (doc.value === null || saving.value) return
  saving.value = true
  saveError.value = null
  try {
    // **`If-Match` には直前の応答が返した `version` を使う**（2.8 / 10.4）。
    // **`change_reason` は空なら送らない**——10.4 は「リビジョンを作らない更新で
    // 送っても捨てる」と定めるが、空文字を送る意味は無い
    const reason = changeReason.value.trim()
    const next = await docsApi.updateDoc(props.projectKey, doc.value.path, doc.value.version, {
      body_md: draft.value,
      ...(reason === '' ? {} : { change_reason: reason }),
    })
    doc.value = next
    savedVersion.value = next.version
    editing.value = false
    changeReason.value = ''
    // 目次の `updated_at` と `version` も動いている（10.2）ので取り直す
    await loadTree()
  } catch (e) {
    // **編集内容を捨てない**（5.10）。409 / 422 のいずれでも `draft` はそのまま
    saveError.value =
      e instanceof ApiError
        ? e
        : new ApiError({ status: 0, code: 'internal_error', message: uiText("保存できませんでした") })
  } finally {
    saving.value = false
  }
}

/** 409 のあと、サーバの最新を取り込む。**自分の編集は破棄される**（5.10） */
async function reloadLatest(): Promise<void> {
  if (doc.value === null) return
  const path = doc.value.path
  docLoading.value = true
  try {
    doc.value = await docsApi.getDoc(props.projectKey, path)
    draft.value = doc.value.body_md
    saveError.value = null
  } catch (e) {
    docError.value = e instanceof ApiError ? e.message : uiText("文書を取得できませんでした")
  } finally {
    docLoading.value = false
  }
}

// ── 追加・削除（10.4）──────────────────────────────────────

const showCreate = ref(false)
/** 作成モーダルの初期値。`[ 別名で保存 ]` は編集中の本文をここへ載せる（5.10） */
const createParent = ref<string | null>(null)
const createBody = ref('')

function openCreate(parentPath: string | null = null, body = ''): void {
  createParent.value = parentPath
  createBody.value = body
  showCreate.value = true
}

/** `[ 別名で保存 ]`（5.10）。**`slug` と `title` は空で開く**——名前は人が決める */
function saveAsCopy(): void {
  if (doc.value === null) return
  openCreate(doc.value.parent_path, draft.value)
}

async function onCreated(created: Doc): Promise<void> {
  showCreate.value = false
  cancelEdit()
  await loadTree()
  await router.push(`/p/${props.projectKey}/docs/${created.path}`)
}

const deleteTarget = ref<DocTreeItem | null>(null)
const deleting = ref(false)

const deleteMessage = computed(() => {
  const target = deleteTarget.value
  if (target === null) return ''
  const n = descendantCount(target)
  const head = uiText("「{value0}」を削除します。", { value0: target.title })
  const scope =
    n === 0
      ? uiText("この文書と、その変更履歴が消えます。")
      : uiText("この文書と配下の {value0} 件、およびそれぞれの変更履歴が消えます。", { value0: n })
  return uiText("{value0}\n{value1}\n取り消せません。", { value0: head, value1: scope })
})

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (target === null || deleting.value) return
  deleting.value = true
  try {
    await docsApi.deleteDoc(props.projectKey, target.path)
    const removedCurrent =
      currentPath.value !== null &&
      (currentPath.value === target.path || currentPath.value.startsWith(`${target.path}/`))
    deleteTarget.value = null
    await loadTree()
    // **消えた文書を開いたままにしない。** 部分木ごと消えるので、配下を開いて
    // いた場合も一覧へ戻す（10.4）
    if (removedCurrent) await router.push(`/p/${props.projectKey}/docs`)
  } catch (e) {
    docError.value = e instanceof ApiError ? e.message : uiText("文書を削除できませんでした")
    deleteTarget.value = null
  } finally {
    deleting.value = false
  }
}

// ── 移動したあと URL を追う（10.4）────────────────────────

/**
 * 移動・改名で `path` が変わったら、開いている文書の URL を追随させる。
 *
 * **部分木ごと動く**（10.4）ので、**開いているのが動いた文書の子孫でも**
 * URL は変わる。追わないと、次に読み込んだときに 404 になる。
 *
 * **`replace` を使う。** 移動は履歴に積む「遷移」ではなく、いま見ているものの
 * 住所が変わっただけである——`push` にすると戻るボタンで古い URL（もう無い）へ戻る。
 *
 * @returns 実際に遷移したか。**呼び出し側が本文の取り直しを重ねないため**に返す
 *   （遷移すれば `currentPath` の watch が `loadDoc` を呼ぶ）
 */
async function followMoved(id: string, oldPath: string): Promise<boolean> {
  const cur = currentPath.value
  if (cur === null) return false
  if (cur !== oldPath && !cur.startsWith(`${oldPath}/`)) return false

  const moved = findById(id)
  if (moved === null) {
    await router.replace(`/p/${props.projectKey}/docs`)
    return true
  }
  const next = `${moved.path}${cur.slice(oldPath.length)}`
  if (next === cur) return false
  await router.replace(`/p/${props.projectKey}/docs/${next}`)
  return true
}

// ── 木のドラッグ&ドロップ（5.10「木の操作」）──────────────

/**
 * 掴んでいる行と、目印の置き場。**この2つをページが1つずつ持つ**——
 * `DocTree` は再帰で自分自身を描くので、部品側の状態は段をまたいで共有されない。
 */
const draggingItem = ref<DocTreeItem | null>(null)
const dropHint = ref<DocDropHint | null>(null)

/** 並べ替えの誤り。**木のそばに出す**（操作した場所に結果を出す。6.4） */
const moveError = ref<string | null>(null)
const moving = ref(false)

function onDragging(item: DocTreeItem | null): void {
  draggingItem.value = item
  if (item === null) dropHint.value = null
}

/**
 * 落とした（5.10 の表）。
 *
 * | ゾーン | 行き先 |
 * |---|---|
 * | `before` / `after` | 相手と**同じ親**の中で、相手の前／後ろ |
 * | `inside` | **相手の子**の末尾 |
 *
 * **移動先の兄弟の並びを組み立てて `moveDoc` へ渡す。** `10, 20, 30, …` の
 * 振り直しと「変わった行だけ送る」はあちらが持つ（`api/docs.ts`）。
 *
 * **楽観更新はしない。**親子が変わると部分木の
 * `path` が丸ごと付け替わるので（10.4）、手元で正しい姿を作るとサーバの規則を
 * 二重に持つことになる。`PATCH` を送ってから目次を取り直す。
 */
async function onDrop(payload: { item: DocTreeItem; zone: DocDropZone }): Promise<void> {
  // **掴んでいた行を先に控える。** `onDragging(null)` を通すと消えてしまう
  const moved = draggingItem.value
  const target = payload.item
  onDragging(null)
  if (moved === null || moving.value) return

  const toParentPath = payload.zone === 'inside' ? target.path : parentPathOf(target.path)
  const fromParentPath = parentPathOf(moved.path)

  // 移動する行を除いた移動先の並び。**別の親から来たときは元から入っていない**
  const base = childrenOf(toParentPath).filter((c) => c.id !== moved.id)
  let siblings: DocTreeItem[]
  if (payload.zone === 'inside') {
    siblings = [...base, moved]
  } else {
    const at = base.findIndex((c) => c.id === target.id)
    if (at < 0) return
    const insertAt = payload.zone === 'before' ? at : at + 1
    siblings = [...base.slice(0, insertAt), moved, ...base.slice(insertAt)]
  }

  const oldPath = moved.path
  moving.value = true
  moveError.value = null
  try {
    const sent = await docsApi.moveDoc(props.projectKey, {
      moved,
      fromParentPath,
      toParentPath,
      siblings,
    })
    if (sent === 0) return
    // **子にしたなら、その親を必ず開く。** 畳んだ節点へ落とすと、
    // 動いた行がどこへ行ったのか画面から見えなくなる
    if (payload.zone === 'inside' && collapsed.value.delete(target.path)) {
      collapsed.value = new Set(collapsed.value)
      writeCollapsed()
    }
    await loadTree()
    await followMoved(moved.id, oldPath)
  } catch (e) {
    // **原子的ではない**（`ApiDesign.md` 12.2 のタグと同じ）。途中まで反映された
    // 状態が実際に起こりうるので、取り直していまの姿を見せる
    moveError.value = e instanceof ApiError ? e.message : uiText("文書を移動できませんでした")
    await loadTree()
  } finally {
    moving.value = false
  }
}

// ── `[⋯]` の項目 ───────────────────────────────────────────

const docActions = computed<ActionItem[]>(() => [
  { key: 'move', label: uiText("移動・改名") },
  { key: 'history', label: uiText("履歴") },
  { key: 'delete', label: uiText("削除"), danger: true },
])

/** 「移動・改名」の対象。**目次の行を渡す**——`version` を持つので `If-Match` に足りる */
const moveTarget = ref<DocTreeItem | null>(null)

/**
 * 「履歴」の対象。**行そのものではなく `id` を持ち、木から引き直す。**
 *
 * 履歴モーダルは**開いたまま文書を変える**（`[ この版に戻す ]`）唯一の場所で、
 * 戻すと `title` が変わりうる（10.5 は `title` も書き戻す）。行の写しを持つと
 * **モーダルの見出しが戻す前のタイトルのまま残る**（実測で発覚）。
 * 移動・改名のほうは保存と同時に閉じるので、この作りは要らない。
 */
const historyTargetId = ref<string | null>(null)
const historyTarget = computed<DocTreeItem | null>(() =>
  historyTargetId.value === null ? null : findById(historyTargetId.value),
)

function runAction(key: string, item: DocTreeItem): void {
  if (key === 'move') moveTarget.value = item
  else if (key === 'history') historyTargetId.value = item.id
  else if (key === 'delete') deleteTarget.value = item
}

function onTreeAction(payload: { key: string; item: DocTreeItem }): void {
  runAction(payload.key, payload.item)
}

function onHeaderAction(key: string): void {
  if (doc.value === null) return
  const item = byPath.value.get(doc.value.path)
  if (item !== undefined) runAction(key, item)
}

/** 移動・改名が通った（10.4）。**`path` が変わっていれば URL を追う** */
async function onDocSaved(saved: Doc): Promise<void> {
  const target = moveTarget.value
  moveTarget.value = null
  if (target === null) return
  const oldPath = target.path
  await loadTree()
  const navigated = await followMoved(saved.id, oldPath)
  // 遷移したなら watch が本文を取り直す。していないなら、開いているのが
  // 当人のときだけ手元で差し替える（タイトルだけ変えた場合がこれにあたる）
  if (!navigated && currentPath.value === saved.path) doc.value = saved
}

/** `[ この版に戻す ]` が通った（10.5）。**新しい版として積まれている** */
async function onReverted(next: Doc): Promise<void> {
  await loadTree()
  if (currentPath.value === next.path) doc.value = next
}

// ── 3ペインの幅（5.10「幅が足りないとき」）──────────────────

const bodyPane = ref<InstanceType<typeof SplitPane> | null>(null)

/**
 * 可視化ペインが独立して並んでいるか。
 *
 * **並んでいないときは `MarkdownEditor` の内蔵プレビューへ戻す**——あちらが
 * 唯一の描画になるため。`SplitPane` が `sideBySide` を公開している（2.4 の
 * 「並ぶかどうかはコンテンツペインの幅で決める」を持っているのはあの部品である）。
 * **マウント前は `true` として扱う**——初回描画で内蔵プレビューを出してから
 * 消すより、出さないでおいて必要なら出すほうがちらつかない。
 */
const previewPaneShown = computed(() => bodyPane.value?.sideBySide ?? true)

// ── 読み込み ───────────────────────────────────────────────

watch(
  () => props.projectKey,
  () => {
    readCollapsed()
    void loadTree()
  },
  { immediate: true },
)

watch(currentPath, (path) => void loadDoc(path), { immediate: true })
</script>

<template>
  <!-- 外側：文書ツリー（固定・左）と本文（余りを取る）。**残すのは本文のほうである**
       ——並ばない幅で木だけが残ると、文書を1件も読めなくなる（5.10） -->
  <SplitPane
    class="page"
    :open="true"
    side="start"
    collapse-to="primary"
    storage-key="pb.docs_tree_w"
    :min-primary="360"
    :min-secondary="200"
    :default-secondary="240"
  >
    <template #secondary>
      <nav class="docs-tree-pane" :aria-label="$ui('文書ツリー')">
        <div class="docs-tree-scroll">
          <p v-if="treeLoading" class="docs-tree-note">{{ $ui('読み込み中…') }}</p>
          <p v-else-if="treeError" class="docs-tree-note docs-tree-error">✕ {{ treeError }}</p>
          <DocTree
            v-else-if="tree.length > 0"
            :items="tree"
            :selected-path="currentPath"
            :collapsed="collapsed"
            :project-key="projectKey"
            :can-edit="canEdit"
            :actions="docActions"
            :dragging-path="draggingItem?.path ?? null"
            :drop-hint="dropHint"
            @toggle="toggleCollapsed"
            @action="onTreeAction"
            @dragging="onDragging"
            @hint="dropHint = $event"
            @drop="onDrop"
          />
        </div>

        <!-- `doc.edit` を持たない人には出さない（設計原則4）。ワイヤーどおり木の下 -->
        <div v-if="canEdit" class="docs-tree-foot">
          <!-- 並べ替えの誤りは木のそばに出す（6.4）。**目次は取り直してあるので、
               画面に出ているのは失敗した後のいまの姿である** -->
          <p v-if="moveError" class="docs-tree-note docs-tree-error">✕ {{ moveError }}</p>
          <button type="button" class="secondary docs-add" @click="openCreate(null)"> {{ $ui('+ 文書を追加') }} </button>
        </div>
      </nav>
    </template>

    <template #primary>
      <!-- **タイトル行は編集ペインと可視化ペインにまたがる**（5.10）。
           2つの最上部に別々に出すと同じ文字が2回並ぶ -->
      <PageHeader :title="doc?.title ?? 'Docs'">
        <template #actions>
          <template v-if="doc !== null && canEdit">
            <template v-if="editing">
              <button type="button" class="secondary" :disabled="saving" @click="cancelEdit"> {{ $ui('取消') }} </button>
              <button type="button" class="primary" :disabled="saving" @click="save">
                {{ saving ? $ui("保存中…") : $ui("保存") }}
              </button>
            </template>
            <template v-else>
              <button type="button" class="secondary" @click="startEdit">{{ $ui('編集') }}</button>
              <UserActionsMenu
                :items="docActions"
                :label="$ui('{value0} の操作メニュー', { value0: doc.title })"
                @select="onHeaderAction"
              />
            </template>
          </template>
        </template>
      </PageHeader>

      <!-- 内側：編集ペイン（固定・左）と可視化ペイン（余りを取る）。
           **編集していないときは可視化だけになる**（`open` が false） -->
      <SplitPane
        ref="bodyPane"
        class="docs-body"
        :open="editing"
        side="start"
        storage-key="pb.docs_editor_w"
        :min-primary="360"
        :min-secondary="360"
        :default-secondary="480"
      >
        <template #secondary>
          <section class="docs-editor" :aria-label="$ui('編集')">
            <div class="docs-editor-scroll">
              <!-- 競合（5.10）。**入力欄の内容は保持したまま**その場に出す（6.4） -->
              <div v-if="conflict" class="docs-conflict">
                <p class="docs-conflict-message">⚠ {{ saveError?.message }}</p>
                <div class="docs-conflict-actions">
                  <button type="button" class="secondary" @click="reloadLatest"> {{ $ui('最新を読み込む') }} </button>
                  <button type="button" class="secondary" @click="saveAsCopy"> {{ $ui('別名で保存') }} </button>
                </div>
              </div>
              <p v-else-if="saveError" class="docs-save-error">✕ {{ saveError.message }}</p>

              <!-- **可視化ペインが並んでいるときは内蔵プレビューを出さない**（5.10） -->
              <MarkdownEditor
                v-model="draft"
                :preview="!previewPaneShown"
                @cancel="cancelEdit"
              />
            </div>

            <!-- 更新理由は編集ペインの最下部（5.10）。本文とは線で分ける -->
            <label class="docs-reason">
              <span class="docs-reason-label">{{ $ui('更新理由（任意）') }}</span>
              <input
                v-model="changeReason"
                type="text"
                maxlength="200"
                :placeholder="$ui('ブランチ命名にチケット番号を入れる')"
              />
            </label>
          </section>
        </template>

        <template #primary>
          <section class="docs-preview" :aria-label="$ui('本文')">
            <p v-if="docLoading" class="docs-note">{{ $ui('読み込み中…') }}</p>
            <p v-else-if="docError" class="docs-note docs-tree-error">✕ {{ docError }}</p>

            <!-- 文書が1件も無いとき（5.10「空状態」）。**`doc.edit` を持たない人には
                 一言だけを出し、ボタンを置かない**（設計原則4） -->
            <EmptyState
              v-else-if="tree.length === 0 && !treeLoading"
              :title="$ui('文書がありません')"
              :description="
                canEdit
                  ? $ui('最初の文書を作成して、このプロジェクトの規約や判断の基準を書き始めましょう')
                  : $ui('このプロジェクトにはまだ文書がありません。編集できる人が作成するのを待ってください')
              "
            >
              <template v-if="canEdit" #action>
                <button type="button" class="primary" @click="openCreate(null)"> {{ $ui('+ 文書を追加') }} </button>
              </template>
            </EmptyState>

            <!-- どの文書も選んでいない（5.10）。木全体を目次として出す -->
            <div v-else-if="doc === null" class="docs-outline">
              <p class="docs-outline-lead"> {{ $ui('左の文書ツリーから選ぶか、下の一覧から開いてください。') }} </p>
              <ul class="docs-outline-list">
                <li v-for="f in flatDocs" :key="f.item.id" class="docs-outline-item">
                  <RouterLink
                    class="docs-outline-link"
                    :to="`/p/${projectKey}/docs/${f.item.path}`"
                    :style="{ paddingLeft: `${f.depth * 16}px` }"
                  >
                    <span class="docs-outline-title">{{ f.item.title }}</span>
                    <span class="docs-outline-path">{{ f.item.path }}</span>
                  </RouterLink>
                  <span class="docs-outline-date">{{ formatDateTime(f.item.updated_at) }}</span>
                </li>
              </ul>
            </div>

            <article v-else class="docs-article">
              <label class="docs-pack-mode">
                <span>{{ $ui('コンテキストパックへの掲載') }}</span>
                <select :value="doc.pack_mode" :disabled="!canEdit || packModeSaving || editing" @change="changePackMode">
                  <option value="full">{{ $ui('全文') }}</option>
                  <option value="outline">{{ $ui('目次だけ') }}</option>
                  <option value="none">{{ $ui('載せない') }}</option>
                </select>
              </label>
              <p v-if="packModeError" class="docs-save-error" role="alert">{{ packModeError }}</p>
              <p class="docs-meta">
                <span class="docs-meta-label">{{ $ui('更新') }}</span>
                <span>{{ formatDateTime(doc.updated_at) }}</span>
                <!-- **`updated_by` は `null` になりうる**（10.3。`ON DELETE SET NULL`）
                     ——文書は書いた人が消えても内容が生き続ける -->
                <template v-if="doc.updated_by">
                  <Avatar
                    :name="doc.updated_by.display_name"
                    :kind="doc.updated_by.kind"
                    :size="20"
                  />
                  <span>{{ doc.updated_by.display_name }}</span>
                </template>
                <span v-else class="docs-meta-none">—</span>
              </p>

              <!-- 保存の結果は操作した場所に出す（6.4）。**サーバの値と一致して
                   いる間だけ出す**ので、次の編集を始めた時点で自然に消える -->
              <p v-if="savedVersion === doc.version" class="docs-saved">{{ $ui('✓ 保存しました') }}</p>

              <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
              <div v-if="bodyHtml" class="markdown-body" v-html="bodyHtml"></div>
              <p v-else class="docs-note">{{ $ui('まだ何も書かれていません') }}</p>

              <!-- 配下の文書（5.10）。**画面が目次から生成する。本文に書かせない**
                   ——手で書いたリンクは移動・改名の直後に必ず古くなる -->
              <section v-if="children.length > 0" class="docs-children">
                <h2 class="docs-children-title">{{ $ui('配下の文書') }}</h2>
                <ul class="docs-children-list">
                  <li v-for="child in children" :key="child.id">
                    <RouterLink :to="`/p/${projectKey}/docs/${child.path}`">
                      → {{ child.title }}
                    </RouterLink>
                  </li>
                </ul>
              </section>
            </article>
          </section>
        </template>
      </SplitPane>
    </template>
  </SplitPane>

  <DocFormModal
    v-if="showCreate"
    :project-key="projectKey"
    :tree="tree"
    :initial-parent-path="createParent"
    :initial-body="createBody"
    @close="showCreate = false"
    @created="onCreated"
  />

  <!-- 「移動・改名」（5.10）。**同じ部品の別モード**——扱う欄が
       `title` / `slug` / `parent_path` の3つで完全に同じである（6.1） -->
  <DocFormModal
    v-if="moveTarget"
    :project-key="projectKey"
    :tree="tree"
    :doc="moveTarget"
    @close="moveTarget = null"
    @saved="onDocSaved"
  />

  <!-- 「履歴」（5.10 / `ApiDesign.md` 10.5）。閉じれば現在の版に戻る -->
  <DocRevisionsModal
    v-if="historyTarget"
    :project-key="projectKey"
    :doc="historyTarget"
    :can-edit="canEdit"
    @close="historyTargetId = null"
    @reverted="onReverted"
  />

  <ConfirmDialog
    v-if="deleteTarget"
    :title="$ui('文書を削除')"
    :message="deleteMessage"
    :confirm-label="$ui('削除')"
    danger
    :busy="deleting"
    @cancel="deleteTarget = null"
    @confirm="confirmDelete"
  />
</template>

<style scoped>
.page {
  height: 100%;
}

/* ── 文書ツリー ─────────────────────────────────────────── */

.docs-tree-pane {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
  background: var(--pb-surface);
}

.docs-tree-scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-x: hidden;
  overflow-y: auto;
  padding: var(--pb-space-2) 0;
}

.docs-tree-note {
  margin: 0;
  padding: var(--pb-space-3);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.docs-tree-error {
  color: var(--pb-danger-text);
}

.docs-tree-foot {
  flex: none;
  padding: var(--pb-space-2);
  border-top: 1px solid var(--pb-line);
}

/* 誤りの行はボタンの真上に置く。**`.docs-tree-note` の余白は木の中で
   使うためのもの**なので、フッタでは詰める */
.docs-tree-foot .docs-tree-note {
  padding: 0 var(--pb-space-1) var(--pb-space-2);
}

.docs-add {
  width: 100%;
  justify-content: center;
}

/* ── 本文まわり ─────────────────────────────────────────── */

/* **主役には `flex: 1 1 auto` と `min-height` を添える**（6.7 の縦版）。
   ページヘッダ（48px）の下で残りを埋める */
.docs-body {
  flex: 1 1 auto;
  min-height: 0;
}

.docs-editor,
.docs-preview {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

/* **編集器はペインの高さいっぱいに伸ばす。** `MarkdownEditor` の既定は
   `min-height: 180px` / `max-height: 420px`（チケットの説明欄はページの流れの
   中にあり、青天井にすると本文が押し出されるため）。**Docs はペインが編集器
   専用なので、420px で止めると下に広大な余白が残る**（実機のスクリーンショットで
   発覚。座標の実測は「入るか」しか答えない）。
   `:deep()` で子部品の内側へ届かせる——`.cm-editor` の規則は `MarkdownEditor` の
   非 scoped ブロックにあり、こちらの詳細度が1つ上回る */
.docs-editor-scroll {
  display: flex;
  flex: 1 1 auto;
  flex-direction: column;
  min-height: 0;
  overflow-y: auto;
  padding: var(--pb-space-4);
}

.docs-editor-scroll :deep(.md-editor) {
  flex: 1 1 auto;
  min-height: 0;
}

.docs-editor-scroll :deep(.source) {
  flex: 1 1 auto;
  min-height: 0;
}

.docs-editor-scroll :deep(.cm-editor) {
  height: 100%;
  max-height: none;
}

/* 内蔵プレビュー（並ばない幅のときだけ出る）は自然な高さで下に置く */
.docs-editor-scroll :deep(.preview),
.docs-editor-scroll :deep(.preview-label) {
  flex: none;
}

.docs-preview {
  overflow-y: auto;
  padding: var(--pb-space-4) var(--pb-space-6);
}

.docs-article {
  min-width: 0;
}

.docs-pack-mode {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
  margin-bottom: var(--pb-space-3);
  color: var(--pb-text-muted);
}

.docs-pack-mode select {
  min-height: 32px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: var(--pb-text);
}

.docs-meta {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  margin: 0 0 var(--pb-space-3);
  padding-bottom: var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.docs-meta-label {
  color: var(--pb-text-muted);
}

.docs-meta-none {
  color: var(--pb-text-muted);
}

.docs-saved {
  margin: 0 0 var(--pb-space-3);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.docs-note {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ── 更新理由 ───────────────────────────────────────────── */

.docs-reason {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--pb-space-2);
  padding: var(--pb-space-3) var(--pb-space-4);
  border-top: 1px solid var(--pb-line);
}

.docs-reason-label {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.docs-reason input {
  flex: 1 1 auto;
  min-width: 0;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

/* ── 競合（5.10）─────────────────────────────────────────── */

/* **warning は面で表す**（8.4.1）。文字色だけにすると、編集中の画面で埋もれる */
.docs-conflict {
  flex: none;
  margin-bottom: var(--pb-space-3);
  padding: var(--pb-space-3);
  border: 1px solid var(--pb-warning-border);
  border-radius: var(--pb-radius);
  background: var(--pb-warning-bg);
}

.docs-conflict-message {
  margin: 0 0 var(--pb-space-3);
  color: var(--pb-warning-text);
}

.docs-conflict-actions {
  display: flex;
  gap: var(--pb-space-2);
}

.docs-save-error {
  margin: 0 0 var(--pb-space-3);
  color: var(--pb-danger-text);
  font-size: 13px;
}

/* ── 目次の概要（未選択時）───────────────────────────────── */

.docs-outline-lead {
  margin: 0 0 var(--pb-space-4);
  color: var(--pb-text-muted);
}

.docs-outline-list {
  margin: 0;
  padding: 0;
  list-style: none;
}

.docs-outline-item {
  display: flex;
  align-items: center;
  gap: var(--pb-space-3);
  height: var(--pb-row-h);
  border-bottom: 1px solid var(--pb-line);
}

.docs-outline-link {
  display: flex;
  flex: 1 1 auto;
  align-items: baseline;
  gap: var(--pb-space-2);
  min-width: 0;
  overflow: hidden;
  color: var(--pb-text);
  white-space: nowrap;
}

.docs-outline-title {
  overflow: hidden;
  text-overflow: ellipsis;
}

.docs-outline-path {
  flex: none;
  color: var(--pb-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
}

.docs-outline-date {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 12px;
}

/* ── 配下の文書（5.10）──────────────────────────────────── */

.docs-children {
  margin-top: var(--pb-space-6);
  padding-top: var(--pb-space-4);
  border-top: 1px solid var(--pb-line);
}

.docs-children-title {
  margin: 0 0 var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
}

.docs-children-list {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-2) var(--pb-space-4);
  margin: 0;
  padding: 0;
  list-style: none;
}
</style>
