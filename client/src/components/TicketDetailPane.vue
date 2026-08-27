<script setup lang="ts">
/**
 * チケット詳細（`GuiDesign.md` 5.5）。
 *
 * **バックログの右に開く3枚目のペインである**（2.2.1）。画面を置き換えない。
 * 1件ごとに画面が入れ替わると、戻るたびにスクロール位置と視線の位置を作り直す
 * ことになるためで、**一覧に居たまま次の行をクリックすれば詳細が差し替わる。**
 *
 * **レイアウトは1カラム**（5.5。2カラムから本改訂で変更した）。ペインの幅が
 * 750px 前後に制限されるため、右サイドバーにメタ情報を寄せる形は成り立たない。
 * メタ情報は最上部の**2列のラベル＋値のグリッド**へ横に流す。
 *
 * **上から読む順序**は「何のチケットか」→「いまどうなっているか」→「何をするのか」
 * →「何が下にぶら下がるか」である（5.5）。
 *
 * **画面全体を編集モードにしない**（5.5「編集の単位」）。選べるものは選んだ時点で
 * `PATCH`、文字・数値・日付はその領域をクリックして編集モードに入る。
 *
 * **まだ出さないセクションは見出しごと出さない**（5.5）——DoD・関連チケット・
 * コメント（18）、履歴（19）。空の枠を置くと「実装済みで中身が無い」に見え、
 * `comment_count` が実数を返すぶん誤解が強くなる。
 *
 * **コードと参考リンクは手順17c で足した**（5.5、`ApiDesign.md` 9.10.2）。
 * 0件のときの扱いは2つで違う——**コードは見出しごと出さず**（画面から追加できず、
 * 空の枠は何もできない箱になる）、**参考リンクは常に出す**（`[+ 追加]` が
 * このセクションへの唯一の入口で、隠すと機能へ到達できない）。
 */
import { computed, nextTick, ref, useTemplateRef, watch } from 'vue'

import ConfirmDialog from './ConfirmDialog.vue'
import EmptyState from './EmptyState.vue'
import MarkdownEditor from './MarkdownEditor.vue'
import NewTicketModal from './NewTicketModal.vue'
import ReferenceModal from './ReferenceModal.vue'
import StatusDropdown from './StatusDropdown.vue'
import UserActionsMenu from './UserActionsMenu.vue'
import type { ActionItem } from './UserActionsMenu.vue'
import { ApiError } from '../api/client'
import type { ProjectMember } from '../api/projects'
import * as referencesApi from '../api/references'
import { codeSummary, docSummary } from '../api/references'
import type { TicketReference } from '../api/references'
import type { Sprint } from '../api/sprints'
import type { Tag } from '../api/tags'
import * as ticketsApi from '../api/tickets'
import {
  priorityLabels,
  priorityOrder,
  ticketTypeIcons,
  ticketTypeLabels,
} from '../api/tickets'
import type {
  CreateTicketRequest,
  Ticket,
  TicketDetail,
  TicketPriority,
  TicketType,
  UpdateTicketRequest,
} from '../api/tickets'
import { formatPlainDate } from '../lib/datetime'
import { renderMarkdown } from '../lib/markdown'
import { isWebUrl } from '../lib/url'
import { useAuthStore } from '../stores/auth'

const props = defineProps<{
  projectKey: string
  seq: number
  /**
   * 担当の選択肢（5.4.3 と同じく `GET /projects/:key` の `members[]`）。
   *
   * **`ProjectMember` をそのまま受ける**（手順17c で narrow な自前の型から
   * 広げた）。子チケットの追加が `NewTicketModal` を開き、あちらは
   * `ProjectMember` を要求する——同じ配列をそのまま渡すので、写しの型を
   * 挟むと通らない。
   */
  members: ProjectMember[]
  tags: Tag[]
  sprints: Sprint[]
  /**
   * 親の選択肢（5.5「編集の単位」）。**いま一覧に出ているチケット**から選ぶ。
   * バックログは最大200件を既に手元に持っており、追加の往復を要しない。
   */
  candidates: Ticket[]
}>()

const emit = defineEmits<{
  close: []
  /** 変更が確定した。一覧側が該当行だけ差し替える（取り直さない） */
  updated: [ticket: TicketDetail]
  deleted: [seq: number, title: string]
}>()

const auth = useAuthStore()

const canEdit = computed(() => auth.canInProject(props.projectKey, 'ticket.edit'))
const canAssign = computed(() => auth.canInProject(props.projectKey, 'ticket.assign'))
const canTransition = computed(() => auth.canInProject(props.projectKey, 'ticket.transition'))
const canDelete = computed(() => auth.canInProject(props.projectKey, 'ticket.delete'))
/** 子チケットの追加（5.5）。作成そのものは 9.3 なので `ticket.create` を見る */
const canCreate = computed(() => auth.canInProject(props.projectKey, 'ticket.create'))

// ── 取得 ─────────────────────────────────────────────────────

const ticket = ref<TicketDetail | null>(null)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)
const busy = ref(false)

/** 応答の追い越しを防ぐ通し番号（一覧と同じ対策）。行を続けて叩くと起きる */
let fetchSeq = 0

function toApiError(e: unknown): ApiError {
  return e instanceof ApiError
    ? e
    : new ApiError({
        status: 0,
        code: 'internal_error',
        message: '予期しないエラーが発生しました',
      })
}

async function load(): Promise<void> {
  const mine = ++fetchSeq
  loading.value = true
  loadError.value = null
  try {
    const res = await ticketsApi.getTicket(props.projectKey, props.seq)
    if (mine !== fetchSeq) return
    ticket.value = res
  } catch (e) {
    if (mine !== fetchSeq) return
    loadError.value = toApiError(e)
    ticket.value = null
  } finally {
    if (mine === fetchSeq) loading.value = false
  }
}

// ── 保存（`ApiDesign.md` 9.5.2）───────────────────────────────

/**
 * 編集中の欄。**同時に1つだけ**——5.5 は「別の領域をクリックしてフォーカスを
 * 外す」を保存と定めるので、2つ開くことがそもそも起きない。
 */
type EditField =
  | 'title'
  | 'body_md'
  | 'estimate_point'
  | 'estimate_hours'
  | 'actual_hours'
  | 'start_date'
  | 'due_date'

const editing = ref<EditField | null>(null)
const draft = ref('')
/** 失敗した欄と、その直下に出すサーバの `message`（5.5 / 6.4） */
const fieldError = ref<{ field: string; message: string } | null>(null)

const editorRef = useTemplateRef<InstanceType<typeof MarkdownEditor>>('editorRef')
const inputRef = useTemplateRef<HTMLInputElement>('inputRef')

/** いまの値を編集用の文字列にする。**数値の `null` は空文字**（未設定と 0 を分ける） */
function currentText(field: EditField): string {
  const t = ticket.value
  if (t === null) return ''
  const v = t[field]
  return v === null || v === undefined ? '' : String(v)
}

async function startEdit(field: EditField): Promise<void> {
  if (!canEdit.value || busy.value) return
  // 別の欄を開いていたら、そちらは保存してから移る（5.5「別の領域をクリック」）
  if (editing.value !== null && editing.value !== field) {
    await commitEdit()
    if (editing.value !== null) return // 保存に失敗して留まっている
  }
  editing.value = field
  draft.value = currentText(field)
  fieldError.value = null
  await nextTick()
  if (field === 'body_md') editorRef.value?.focus()
  else inputRef.value?.focus()
}

/** `Esc`：編集前の値へ戻す（5.5）。**別の領域のクリックは保存であって取消ではない** */
function cancelEdit(): void {
  editing.value = null
  draft.value = ''
  fieldError.value = null
}

/**
 * 編集中の値を**必ず文字列として**取り出す。
 *
 * **`v-model` は `type="number"` の入力を自動で数値へ変換する**（Vue 3 の
 * `vModelText` が `el.type === 'number'` で数値化する）。TypeScript の型は
 * `Ref<string>` のままなので**コンパイルでは捕まらず**、`draft.value.trim()` が
 * 実行時に `TypeError` になる——しかも `commitEdit` の中で投げるので、
 * **画面は編集モードのまま何も起きない**（実際に踏んだ）。
 */
function draftText(): string {
  return String(draft.value)
}

/** 数値欄の値。空は `null`（未設定）。数にならないものはサーバの 422 に任せない */
function numberOrNull(text: string): number | null | undefined {
  const s = text.trim()
  if (s === '') return null
  const n = Number(s)
  return Number.isFinite(n) ? n : undefined
}

/**
 * 編集中の欄を確定する。
 *
 * **値が変わっていなければ送らない**（5.5）。フォーカスが外れるたびに `PATCH`
 * すると `version` が無駄に進み、次の編集が 409 になる。
 */
async function commitEdit(): Promise<void> {
  const field = editing.value
  if (field === null || ticket.value === null) return
  const raw = draftText()
  if (raw === currentText(field)) {
    cancelEdit()
    return
  }

  const patch: UpdateTicketRequest = {}
  if (field === 'title') {
    patch.title = raw.trim()
  } else if (field === 'body_md') {
    patch.body_md = raw === '' ? null : raw
  } else if (field === 'start_date' || field === 'due_date') {
    // **`new Date()` を通さない**（`date` 列。`input[type=date]` の値がそのまま
    // `YYYY-MM-DD` である）。通すと UTC より西の地域で前日へずれる
    patch[field] = raw === '' ? null : raw
  } else {
    const n = numberOrNull(raw)
    if (n === undefined) {
      fieldError.value = { field, message: '数値で入力してください' }
      return
    }
    patch[field] = n
  }

  await save(patch, field)
}

/**
 * 選べるものは**選んだ時点で `PATCH`**（5.5「編集の単位」）。
 * 失敗したら値を戻す必要はない——応答が来るまで画面の値は変えていない。
 */
async function selectField(patch: UpdateTicketRequest, field: string): Promise<void> {
  await save(patch, field)
}

/**
 * `PATCH` を1回送る。
 *
 * **`If-Match` は毎回、直前の応答が返した `version` を使う**（9.5.2）。項目ごとに
 * 個別に送るので、**応答の `version` を1か所で受けて持ち回る。**
 *
 * 失敗したら**編集モードのまま留め、その欄の直下にサーバの `message` を出す。
 * 入力値を捨てない**（5.5 / 6.4）。409 / 422 / `not_stageable` のいずれも同じ扱いで、
 * `error.details` に該当の欄があればそちらの文言を優先する（2.5）。
 */
async function save(patch: UpdateTicketRequest, field: string): Promise<void> {
  const current = ticket.value
  if (current === null) return
  busy.value = true
  fieldError.value = null
  try {
    const next = await ticketsApi.updateTicket(props.projectKey, current.seq, current.version, patch)
    ticket.value = next
    cancelEdit()
    emit('updated', next)
  } catch (e) {
    const err = toApiError(e)
    const detail = err.details.find((d) => d.field === field) ?? err.details[0]
    fieldError.value = { field, message: detail?.message ?? err.message }
  } finally {
    busy.value = false
  }
}

// ── 状態の遷移（9.6 / 9.7）───────────────────────────────────

const statusRef = useTemplateRef<InstanceType<typeof StatusDropdown>>('statusRef')

/** **開いたときに呼ぶ**（9.7）。詳細応答には入っていない */
async function loadTransitions(): Promise<void> {
  const t = ticket.value
  if (t === null) return
  statusRef.value?.setLoading()
  try {
    const res = await ticketsApi.listTransitions(props.projectKey, t.seq)
    statusRef.value?.setItems(res.items)
  } catch (e) {
    statusRef.value?.setError(toApiError(e).message)
  }
}

/**
 * 遷移させる（9.6）。**`comment` は送らない**（5.5）——投稿したコメントを
 * 表示する場所が手順18 まで無く、**送ったのに見えない**状態になる。
 */
async function transition(to: string): Promise<void> {
  const t = ticket.value
  if (t === null) return
  busy.value = true
  fieldError.value = null
  try {
    const next = await ticketsApi.transitionTicket(props.projectKey, t.seq, { to })
    ticket.value = next
    emit('updated', next)
  } catch (e) {
    fieldError.value = { field: 'status', message: toApiError(e).message }
  } finally {
    busy.value = false
  }
}

// ── 削除（9.5.3 / 6.3）───────────────────────────────────────

const confirmDelete = ref(false)

/**
 * `[⋯]` の項目（5.5「`[⋯]` メニューの項目」）。
 *
 * **追加の2つは、セクションの見出し右と二重に置く**（利用者の判断、2026-08-27）。
 * 見出し右はそのセクションを読んでいる最中の導線、`[⋯]` は**セクションが画面に
 * 出ていなくても届く**導線で、役割が違う。**子チケットは0件だとセクションごと
 * 消える**ので、`[⋯]` が無いと最初の1件を作れない。
 */
const actionItems = computed<ActionItem[]>(() => [
  {
    key: 'add-child',
    label: '子チケットを追加',
    disabled: !canCreate.value,
    reason: canCreate.value ? undefined : 'チケットを作成する権限がありません',
  },
  {
    key: 'add-reference',
    label: '参考リンクを追加',
    disabled: !canEdit.value,
    reason: canEdit.value ? undefined : 'チケットを編集する権限がありません',
  },
  {
    key: 'delete',
    label: 'このチケットを削除',
    danger: true,
    disabled: !canDelete.value,
    reason: canDelete.value ? undefined : 'チケットを削除する権限がありません',
  },
])

/** **子チケットの件数を出す**（6.3）。子は消えず、親を失ってトップレベルへ上がる */
const deleteMessage = computed(() => {
  const t = ticket.value
  if (t === null) return ''
  const head = `${fullId.value}「${t.title}」を削除します。元に戻せません。`
  if (t.children.length === 0) return head
  return `${head}\n${t.children.length}件の子チケットは削除されず、親のないチケットになります。`
})

async function runDelete(): Promise<void> {
  const t = ticket.value
  if (t === null) return
  busy.value = true
  try {
    await ticketsApi.deleteTicket(props.projectKey, t.seq)
    confirmDelete.value = false
    emit('deleted', t.seq, t.title)
  } catch (e) {
    confirmDelete.value = false
    fieldError.value = { field: 'delete', message: toApiError(e).message }
  } finally {
    busy.value = false
  }
}

// ── コードと参考リンク（5.5、`ApiDesign.md` 9.10.2）。手順17c ─────

/**
 * セクションは `kind` で分ける。**一覧は1本で返る**ので、ここで振り分ける。
 *
 * サーバが `kind` 昇順・同じ `kind` の中は `sort_order` → `created_at` の
 * 昇順で返す（9.10.2）ので、**画面で並べ替え直さない**。
 */
const codeRefs = computed(() =>
  (ticket.value?.references ?? []).filter((r) => r.kind === 'code'),
)
const docRefs = computed(() => (ticket.value?.references ?? []).filter((r) => r.kind === 'doc'))

/** 追加・編集のモーダル。`null` を渡すと新規、参照を渡すと編集（5.5） */
const refModal = ref<{ target: TicketReference | null } | null>(null)
const refErrors = ref<Record<string, string>>({})
/** 削除の確認（6.3）。**どちらの `kind` も人が入れ直せるとは限らない** */
const refToDelete = ref<TicketReference | null>(null)

function openNewReference(): void {
  refErrors.value = {}
  refModal.value = { target: null }
}

function openEditReference(ref: TicketReference): void {
  refErrors.value = {}
  refModal.value = { target: ref }
}

/**
 * 追加・更新を確定する。
 *
 * **詳細を取り直さない**（6.4「サーバ応答後に反映する」）。応答の1件を手元の
 * 配列へ入れるだけで足りる——**参照の増減は親チケットの `version` も
 * `updated_at` も動かさない**ので（9.10.2）、持ち回っている `version` に
 * 影響しない。
 */
async function saveReference(
  body: { url: string; label: string | null; note: string | null },
): Promise<void> {
  const t = ticket.value
  const modal = refModal.value
  if (t === null || modal === null) return

  busy.value = true
  refErrors.value = {}
  try {
    if (modal.target === null) {
      const created = await referencesApi.createReference(props.projectKey, t.seq, {
        kind: 'doc',
        url: body.url,
        label: body.label,
        note: body.note,
      })
      applyReference(created)
    } else {
      const updated = await referencesApi.updateReference(
        props.projectKey,
        t.seq,
        modal.target.id,
        { url: body.url, label: body.label, note: body.note },
      )
      applyReference(updated)
    }
    refModal.value = null
  } catch (e) {
    const err = toApiError(e)
    if (err.details.length > 0) {
      // 欄に紐づく誤りはモーダルの中へ返す（6.4）
      refErrors.value = Object.fromEntries(err.details.map((d) => [d.field, d.message]))
    } else {
      refModal.value = null
      fieldError.value = { field: 'references', message: err.message }
    }
  } finally {
    busy.value = false
  }
}

async function runDeleteReference(): Promise<void> {
  const t = ticket.value
  const target = refToDelete.value
  if (t === null || target === null) return

  busy.value = true
  try {
    await referencesApi.deleteReference(props.projectKey, t.seq, target.id)
    t.references = t.references.filter((r) => r.id !== target.id)
    emit('updated', t)
  } catch (e) {
    fieldError.value = { field: 'references', message: toApiError(e).message }
  } finally {
    refToDelete.value = null
    busy.value = false
  }
}

/**
 * 応答の1件を手元へ反映する。**並び順の規則はサーバのもの**（9.10.2）なので、
 * 追加のときは同じ規則でその場に差し込む。
 */
function applyReference(ref: TicketReference): void {
  const t = ticket.value
  if (t === null) return
  const i = t.references.findIndex((r) => r.id === ref.id)
  if (i >= 0) {
    t.references[i] = ref
  } else {
    t.references = [...t.references, ref].sort(compareReferences)
  }
  emit('updated', t)
}

/** `kind` 昇順 → `sort_order` → `created_at`（9.10.2 と同じ順序）。 */
function compareReferences(a: TicketReference, b: TicketReference): number {
  if (a.kind !== b.kind) return a.kind < b.kind ? -1 : 1
  if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order
  return a.created_at < b.created_at ? -1 : a.created_at > b.created_at ? 1 : 0
}

/** 削除の確認文（6.3）。**誰も締め出さないので1行でよい** */
const deleteReferenceMessage = computed(() => {
  const r = refToDelete.value
  if (r === null) return ''
  const what = r.kind === 'code' ? 'コード' : '参考リンク'
  const summary = r.kind === 'code' ? codeSummary(r) : docSummary(r)
  const head = `${what}「${summary}」を削除します。元に戻せません。`
  // **code は画面から入れ直せない**（追加の導線を持たない。5.5）
  return r.kind === 'code'
    ? `${head}\nコードは画面から追加できないため、消すと入れ直せません。`
    : head
})

// ── 子チケットの追加（5.5「子チケット」）。手順17c ────────────

/**
 * 5.4.3 と同じモーダルを、**親をこのチケットに固定して**開く。
 *
 * **新しい API は要らない**——`POST /tickets`（9.3）に `parent_seq` を添える
 * だけである。選択肢（メンバー・タグ・スプリント・親の候補）は詳細ペインが
 * 既に props で受け取っているので、開くたびに往復が増えない。
 */
const showNewChild = ref(false)
const newChildErrors = ref<Record<string, string>>({})

function openNewChild(): void {
  newChildErrors.value = {}
  showNewChild.value = true
}

/** 親の候補。**このチケット自身に固定する**ので1件だけ渡す */
const childParentCandidates = computed<Ticket[]>(() => {
  const t = ticket.value
  if (t === null) return []
  const self = props.candidates.find((c) => c.seq === t.seq)
  return self ? [self] : []
})

const newChildDefaults = computed(() => ({ parent_seq: ticket.value?.seq }))

/**
 * 子チケットを作る。
 *
 * **作ったあとは詳細を取り直す**——`children` はサーバが組み立てるもので
 * （9.5.1）、応答は作った子チケットのほうだからである。参照の追加（手元へ
 * 差し込む）とはここが違う。
 */
async function createChild(body: CreateTicketRequest): Promise<void> {
  const t = ticket.value
  if (t === null) return

  busy.value = true
  newChildErrors.value = {}
  try {
    await ticketsApi.createTicket(props.projectKey, { ...body, parent_seq: t.seq })
    showNewChild.value = false
    await load()
    if (ticket.value !== null) emit('updated', ticket.value)
  } catch (e) {
    const err = toApiError(e)
    if (err.details.length > 0) {
      newChildErrors.value = Object.fromEntries(err.details.map((d) => [d.field, d.message]))
    } else {
      showNewChild.value = false
      fieldError.value = { field: 'children', message: err.message }
    }
  } finally {
    busy.value = false
  }
}

/** `[⋯]` の項目を振り分ける（5.5）。 */
function onAction(key: string): void {
  switch (key) {
    case 'add-child':
      openNewChild()
      break
    case 'add-reference':
      openNewReference()
      break
    case 'delete':
      confirmDelete.value = true
      break
  }
}

// ── 表示のための小さな関数 ───────────────────────────────────

/** チケットIDは**完全形**で出す（5.4「ID列」）。`-31` は負の数に見える */
const fullId = computed(() =>
  ticket.value === null ? '' : `${props.projectKey}-${ticket.value.seq}`,
)

const body = computed(() => renderMarkdown(ticket.value?.body_md ?? ''))

/** 親の候補。**自分自身は落とす**。子孫はサーバの 422 `parent_cycle` に任せる（5.5） */
const parentOptions = computed(() =>
  props.candidates.filter((c) => c.seq !== props.seq),
)

/** タグの付け外し（9.5.2 の `tag_ids` は**丸ごと置き換える**） */
const tagIds = computed(() => new Set((ticket.value?.tags ?? []).map((t) => t.id)))

const unusedTags = computed(() => props.tags.filter((t) => !tagIds.value.has(t.id)))

const showTagPicker = ref(false)

async function toggleTag(id: string, on: boolean): Promise<void> {
  const next = new Set(tagIds.value)
  if (on) next.add(id)
  else next.delete(id)
  showTagPicker.value = false
  await selectField({ tag_ids: [...next] }, 'tag_ids')
}

function actorMark(kind: string): string {
  return kind === 'agent' ? '🤖' : '👤'
}

/** `—` は「未設定」の意。数値は 0 と未設定を取り違えないよう、単位を添える */
function num(v: number | null | undefined, unit: string): string {
  return v === null || v === undefined ? '—' : `${v} ${unit}`
}

const priorityOptions = [...priorityOrder].reverse() as TicketPriority[]
const typeOptions: TicketType[] = ['epic', 'story', 'task']

// ── 起動と追随 ───────────────────────────────────────────────

/**
 * **`seq` が変わったら取り直す。** 一覧の別の行をクリックしても、子チケットの
 * 行をクリックしても、同じペインが差し替わるだけで再マウントはされない。
 *
 * **この `watch` はスクリプトの末尾に置く。** `immediate: true` は setup の
 * その場でコールバックを走らせるので、上に置くと `cancelEdit()` がまだ
 * 宣言されていない `const`（`editing` / `draft` / `fieldError`）に触れて
 * **TDZ の `ReferenceError` になる**。Vue はそれを console.error に流すだけで
 * 画面は空のまま出るため、**症状は「詳細が真っ白」になり原因が読めない**
 * （実際に踏んだ。関数宣言は巻き上がるが、参照する `const` は巻き上がらない）。
 */
watch(
  () => [props.projectKey, props.seq],
  () => {
    cancelEdit()
    void load()
  },
  { immediate: true },
)

function errorFor(field: string): string {
  return fieldError.value !== null && fieldError.value.field === field
    ? fieldError.value.message
    : ''
}
</script>

<template>
  <section class="detail" aria-label="チケット詳細">
    <!-- 48px のページヘッダ（2.5）。**`<h1>` にはしない**——このペインは画面を
         置き換えないので、`<h1>` は一覧側の「バックログ」1つのままにする（9.2） -->
    <header class="detail-header">
      <template v-if="ticket">
        <span class="type-icon" :title="ticketTypeLabels[ticket.type]" aria-hidden="true">
          {{ ticketTypeIcons[ticket.type] }}
        </span>
        <h2 class="detail-id">{{ fullId }}</h2>
      </template>
      <h2 v-else class="detail-id">チケット</h2>

      <div class="header-actions">
        <UserActionsMenu
          v-if="ticket"
          :items="actionItems"
          :label="`${fullId} の操作メニュー`"
          compact
          @select="onAction"
        />
        <button type="button" class="icon-button" aria-label="詳細を閉じる" @click="emit('close')">
          ✕
        </button>
      </div>
    </header>

    <div class="detail-body">
      <!-- エラー（6.2）。原因はサーバが返した message をそのまま出す -->
      <EmptyState
        v-if="loadError"
        title="チケットを取得できませんでした"
        :description="loadError.message"
      >
        <template #action>
          <button type="button" class="primary" @click="load">再試行</button>
        </template>
      </EmptyState>

      <div v-else-if="loading && ticket === null" class="skeleton-block" aria-busy="true">
        <span v-for="n in 6" :key="n" class="skeleton"></span>
      </div>

      <template v-else-if="ticket">
        <!-- タイトル。**クリックで編集**（5.5。`[編集]` ボタンは置かない） -->
        <div class="title-block">
          <template v-if="editing === 'title'">
            <input
              ref="inputRef"
              v-model="draft"
              class="title-input"
              type="text"
              maxlength="200"
              aria-label="タイトル"
              @keydown.escape="cancelEdit"
              @keydown.enter.prevent="commitEdit"
              @blur="commitEdit"
            />
          </template>
          <button
            v-else
            type="button"
            class="title-view"
            :class="{ readonly: !canEdit }"
            :disabled="!canEdit"
            :title="canEdit ? 'クリックしてタイトルを編集' : ''"
            @click="startEdit('title')"
          >
            {{ ticket.title }}
          </button>
          <p v-if="errorFor('title')" class="field-error" role="alert">{{ errorFor('title') }}</p>
        </div>

        <!-- メタ情報（5.5）。**2列のラベル＋値のグリッド** -->
        <dl class="meta">
          <div class="meta-item">
            <dt>状態</dt>
            <dd>
              <StatusDropdown
                ref="statusRef"
                :current="ticket.status"
                :can-transition="canTransition"
                :busy="busy"
                @open="loadTransitions"
                @select="transition"
              />
              <p v-if="errorFor('status')" class="field-error" role="alert">
                {{ errorFor('status') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>担当</dt>
            <dd>
              <!-- **`ticket.assign` が要る**（9.5.2）。持たないときは値だけ出す -->
              <select
                v-if="canEdit && canAssign"
                :value="ticket.assignee?.id ?? ''"
                :disabled="busy"
                aria-label="担当"
                @change="
                  selectField(
                    { assignee_id: ($event.target as HTMLSelectElement).value || null },
                    'assignee_id',
                  )
                "
              >
                <option value="">未割当</option>
                <option v-for="m in members" :key="m.actor_id" :value="m.actor_id">
                  {{ actorMark(m.kind) }} {{ m.display_name }}
                </option>
              </select>
              <span v-else-if="ticket.assignee">
                <span aria-hidden="true">{{ actorMark(ticket.assignee.kind) }}</span>
                {{ ticket.assignee.display_name }}
              </span>
              <span v-else class="muted">—</span>
              <p v-if="errorFor('assignee_id')" class="field-error" role="alert">
                {{ errorFor('assignee_id') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>優先度</dt>
            <dd>
              <select
                v-if="canEdit"
                :value="ticket.priority ?? ''"
                :disabled="busy"
                aria-label="優先度"
                @change="
                  selectField(
                    { priority: (($event.target as HTMLSelectElement).value || null) as never },
                    'priority',
                  )
                "
              >
                <option value="">未設定</option>
                <option v-for="p in priorityOptions" :key="p" :value="p">
                  {{ priorityLabels[p] }}
                </option>
              </select>
              <span v-else>{{ ticket.priority ? priorityLabels[ticket.priority] : '—' }}</span>
              <p v-if="errorFor('priority')" class="field-error" role="alert">
                {{ errorFor('priority') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>種別</dt>
            <dd>
              <!-- **オンステージのチケットを `epic` にすると 422**（`not_stageable`）。
                   サーバが弾いた理由をそのまま欄の下に出す（9.5.2） -->
              <select
                v-if="canEdit"
                :value="ticket.type"
                :disabled="busy"
                aria-label="種別"
                @change="
                  selectField(
                    { type: ($event.target as HTMLSelectElement).value as never },
                    'type',
                  )
                "
              >
                <option v-for="t in typeOptions" :key="t" :value="t">
                  {{ ticketTypeIcons[t] }} {{ ticketTypeLabels[t] }}
                </option>
              </select>
              <span v-else>{{ ticketTypeLabels[ticket.type] }}</span>
              <p v-if="errorFor('type')" class="field-error" role="alert">{{ errorFor('type') }}</p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>親</dt>
            <dd class="parent-cell">
              <!-- **選択式である**（5.5）。親の実体は `parent_seq` の数値で、
                   番号を手で打たせる形はどの画面にも無い -->
              <select
                v-if="canEdit"
                :value="ticket.parent?.seq ?? ''"
                :disabled="busy"
                aria-label="親チケット"
                @change="
                  selectField(
                    {
                      parent_seq: ($event.target as HTMLSelectElement).value
                        ? Number(($event.target as HTMLSelectElement).value)
                        : null,
                    },
                    'parent_seq',
                  )
                "
              >
                <option value="">親なし</option>
                <!-- いま一覧に出ていない親（オンステージの配下など）も選択肢に
                     残す。落とすと、開いた瞬間に現在値が消えて見える -->
                <option
                  v-if="ticket.parent && !parentOptions.some((c) => c.seq === ticket!.parent!.seq)"
                  :value="ticket.parent.seq"
                >
                  {{ projectKey }}-{{ ticket.parent.seq }} {{ ticket.parent.title }}
                </option>
                <option v-for="c in parentOptions" :key="c.seq" :value="c.seq">
                  {{ projectKey }}-{{ c.seq }} {{ c.title }}
                </option>
              </select>
              <span v-else-if="ticket.parent">
                {{ projectKey }}-{{ ticket.parent.seq }} {{ ticket.parent.title }}
              </span>
              <span v-else class="muted">—</span>

              <!-- 親の詳細を同じペインで開く -->
              <RouterLink
                v-if="ticket.parent"
                class="jump"
                :to="`/p/${projectKey}/tickets/${ticket.parent.seq}`"
                :aria-label="`親チケット ${projectKey}-${ticket.parent.seq} を開く`"
                >↗</RouterLink
              >
              <p v-if="errorFor('parent_seq')" class="field-error" role="alert">
                {{ errorFor('parent_seq') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>スプリント</dt>
            <dd>
              <!-- **選択肢はプロジェクトのスプリント。ここから新規作成はできない**
                   （定義は 5.9.5） -->
              <select
                v-if="canEdit"
                :value="ticket.sprint?.id ?? ''"
                :disabled="busy"
                aria-label="スプリント"
                @change="
                  selectField(
                    { sprint_id: ($event.target as HTMLSelectElement).value || null },
                    'sprint_id',
                  )
                "
              >
                <option value="">スプリント未設定</option>
                <option v-for="s in sprints" :key="s.id" :value="s.id">{{ s.name }}</option>
              </select>
              <span v-else>{{ ticket.sprint?.name ?? '—' }}</span>
              <p v-if="errorFor('sprint_id')" class="field-error" role="alert">
                {{ errorFor('sprint_id') }}
              </p>
            </dd>
          </div>

          <!-- タグは2列ぶんを使う。数が読めないので1列に押し込むと折り返しが荒れる -->
          <div class="meta-item wide">
            <dt>タグ</dt>
            <dd class="tag-cell">
              <span v-for="t in ticket.tags" :key="t.id" class="tag">
                {{ t.name }}
                <button
                  v-if="canEdit"
                  type="button"
                  class="tag-remove"
                  :disabled="busy"
                  :aria-label="`タグ ${t.name} を外す`"
                  @click="toggleTag(t.id, false)"
                >
                  ✕
                </button>
              </span>
              <span v-if="ticket.tags.length === 0 && !canEdit" class="muted">—</span>

              <!-- **選択肢はプロジェクトのタグのみ。ここから新規作成はできない**
                   （定義は 5.9.4） -->
              <span v-if="canEdit" class="tag-picker">
                <button
                  type="button"
                  class="tag-add"
                  :disabled="busy || unusedTags.length === 0"
                  :title="unusedTags.length === 0 ? '付けられるタグがありません' : 'タグを付ける'"
                  aria-label="タグを付ける"
                  @click="showTagPicker = !showTagPicker"
                >
                  +
                </button>
                <span v-if="showTagPicker" class="tag-menu">
                  <button
                    v-for="t in unusedTags"
                    :key="t.id"
                    type="button"
                    class="tag-option"
                    @click="toggleTag(t.id, true)"
                  >
                    {{ t.name }}
                  </button>
                </span>
              </span>
              <p v-if="errorFor('tag_ids')" class="field-error" role="alert">
                {{ errorFor('tag_ids') }}
              </p>
            </dd>
          </div>

          <!-- 見積は3つ（5.5）。**実績だけが左に1つで並ぶ**のは、開始と期限を
               同じ行に残すためである -->
          <div class="meta-item">
            <dt>見積</dt>
            <dd>
              <input
                v-if="editing === 'estimate_point'"
                ref="inputRef"
                v-model="draft"
                type="number"
                min="0"
                step="0.5"
                aria-label="見積（ポイント）"
                @keydown.escape="cancelEdit"
                @keydown.enter.prevent="commitEdit"
                @blur="commitEdit"
              />
              <button
                v-else
                type="button"
                class="value-view"
                :disabled="!canEdit"
                @click="startEdit('estimate_point')"
              >
                {{ num(ticket.estimate_point, 'pt') }}
              </button>
              <p v-if="errorFor('estimate_point')" class="field-error" role="alert">
                {{ errorFor('estimate_point') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>見積（時間）</dt>
            <dd>
              <input
                v-if="editing === 'estimate_hours'"
                ref="inputRef"
                v-model="draft"
                type="number"
                min="0"
                step="0.5"
                aria-label="見積（時間）"
                @keydown.escape="cancelEdit"
                @keydown.enter.prevent="commitEdit"
                @blur="commitEdit"
              />
              <button
                v-else
                type="button"
                class="value-view"
                :disabled="!canEdit"
                @click="startEdit('estimate_hours')"
              >
                {{ num(ticket.estimate_hours, 'h') }}
              </button>
              <p v-if="errorFor('estimate_hours')" class="field-error" role="alert">
                {{ errorFor('estimate_hours') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>実績</dt>
            <dd>
              <input
                v-if="editing === 'actual_hours'"
                ref="inputRef"
                v-model="draft"
                type="number"
                min="0"
                step="0.5"
                aria-label="実績（時間）"
                @keydown.escape="cancelEdit"
                @keydown.enter.prevent="commitEdit"
                @blur="commitEdit"
              />
              <button
                v-else
                type="button"
                class="value-view"
                :disabled="!canEdit"
                @click="startEdit('actual_hours')"
              >
                {{ num(ticket.actual_hours, 'h') }}
              </button>
              <p v-if="errorFor('actual_hours')" class="field-error" role="alert">
                {{ errorFor('actual_hours') }}
              </p>
            </dd>
          </div>

          <!-- 実績の右は空ける。開始と期限を同じ行に残すため（5.5） -->
          <div class="meta-item spacer" aria-hidden="true"></div>

          <div class="meta-item">
            <dt>開始</dt>
            <dd>
              <!-- **`date` 列であって時刻を持たない**（9.2.2）。`input[type=date]` の
                   値がそのまま `YYYY-MM-DD` なので、`new Date()` を通さない -->
              <input
                v-if="editing === 'start_date'"
                ref="inputRef"
                v-model="draft"
                type="date"
                aria-label="開始日"
                @keydown.escape="cancelEdit"
                @keydown.enter.prevent="commitEdit"
                @blur="commitEdit"
              />
              <button
                v-else
                type="button"
                class="value-view"
                :disabled="!canEdit"
                @click="startEdit('start_date')"
              >
                {{ ticket.start_date ? formatPlainDate(ticket.start_date) : '—' }}
              </button>
              <p v-if="errorFor('start_date')" class="field-error" role="alert">
                {{ errorFor('start_date') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>期限</dt>
            <dd>
              <input
                v-if="editing === 'due_date'"
                ref="inputRef"
                v-model="draft"
                type="date"
                aria-label="期限"
                @keydown.escape="cancelEdit"
                @keydown.enter.prevent="commitEdit"
                @blur="commitEdit"
              />
              <button
                v-else
                type="button"
                class="value-view"
                :disabled="!canEdit"
                @click="startEdit('due_date')"
              >
                {{ ticket.due_date ? formatPlainDate(ticket.due_date) : '—' }}
              </button>
              <p v-if="errorFor('due_date')" class="field-error" role="alert">
                {{ errorFor('due_date') }}
              </p>
            </dd>
          </div>
        </dl>

        <!-- 説明（5.5「説明欄」）。読み取り時はレンダリング結果、クリックで
             ソース＋プレビューへ入る -->
        <section class="block">
          <h3 class="block-title">説明</h3>
          <template v-if="editing === 'body_md'">
            <MarkdownEditor ref="editorRef" v-model="draft" @cancel="cancelEdit" />
            <div class="block-actions">
              <button type="button" class="secondary" :disabled="busy" @click="cancelEdit">
                取消
              </button>
              <button type="button" class="primary" :disabled="busy" @click="commitEdit">
                保存
              </button>
            </div>
          </template>
          <template v-else>
            <button
              type="button"
              class="body-view"
              :class="{ readonly: !canEdit }"
              :disabled="!canEdit"
              :title="canEdit ? 'クリックして説明を編集' : ''"
              @click="startEdit('body_md')"
            >
              <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
              <span v-if="body" class="markdown-body" v-html="body"></span>
              <span v-else class="muted">説明はまだありません</span>
            </button>
          </template>
          <p v-if="errorFor('body_md')" class="field-error" role="alert">
            {{ errorFor('body_md') }}
          </p>
        </section>

        <!-- 子チケット（5.5「子チケット」）。**直下の子だけで、孫は含めない。**
             **子が無いときはセクションごと出さない**（空の見出しを置かない） -->
        <section v-if="ticket.children.length > 0" class="block">
          <div class="block-head">
            <h3 class="block-title">子チケット ({{ ticket.children.length }})</h3>
            <!-- **見出し右の追加**（5.5）。0件だとこのセクションごと消えるので、
                 最初の1件は `[⋯]` から作る -->
            <button
              v-if="canCreate"
              type="button"
              class="block-add"
              @click="openNewChild"
            >
              + 追加
            </button>
          </div>
          <ul class="children">
            <li v-for="c in ticket.children" :key="c.seq">
              <!-- **行クリックでその子の詳細を開く**（同じペインが差し替わる） -->
              <RouterLink class="child" :to="`/p/${projectKey}/tickets/${c.seq}`">
                <span class="type-icon" :title="ticketTypeLabels[c.type]" aria-hidden="true">
                  {{ ticketTypeIcons[c.type] }}
                </span>
                <code class="child-id">{{ projectKey }}-{{ c.seq }}</code>
                <span class="child-title">{{ c.title }}</span>
                <span class="child-status">{{ c.status.name }}</span>
                <span class="child-assignee">
                  <template v-if="c.assignee">
                    <span aria-hidden="true">{{ actorMark(c.assignee.kind) }}</span>
                    {{ c.assignee.display_name }}
                  </template>
                  <span v-else class="muted">—</span>
                </span>
              </RouterLink>
            </li>
          </ul>
        </section>

        <!-- コード（5.5「コードと参考リンク」）。**画面に追加を置かない**——
             この欄はエージェントの作業記録で、人が手で書くものではない。
             **0件なら見出しごと出さない**（何もできない空の箱になるため） -->
        <section v-if="codeRefs.length > 0" class="block">
          <h3 class="block-title">コード</h3>
          <ul class="refs">
            <li v-for="r in codeRefs" :key="r.id" class="ref-row">
              <!-- `url` があればリンク。**ブラウザで開けるものだけ**（5.5） -->
              <a
                v-if="isWebUrl(r.url)"
                class="ref-main ref-link"
                :href="r.url ?? undefined"
                target="_blank"
                rel="noopener noreferrer"
              >
                <code class="ref-code">{{ codeSummary(r) }}</code>
                <span class="ref-external" aria-hidden="true">↗</span>
              </a>
              <span v-else class="ref-main">
                <code class="ref-code">{{ codeSummary(r) }}</code>
              </span>
              <span v-if="r.label" class="ref-label">{{ r.label }}</span>
              <button
                v-if="canEdit"
                type="button"
                class="ref-action ref-danger"
                @click="refToDelete = r"
              >
                削除
              </button>
            </li>
          </ul>
        </section>

        <!-- 参考リンク（5.5）。**0件でも常に出す**——`[+ 追加]` がこのセクションへの
             唯一の入口であり、隠すと機能へ到達できない -->
        <section class="block">
          <div class="block-head">
            <h3 class="block-title">参考リンク</h3>
            <button
              v-if="canEdit"
              type="button"
              class="block-add"
              @click="openNewReference"
            >
              + 追加
            </button>
          </div>
          <ul v-if="docRefs.length > 0" class="refs">
            <li v-for="r in docRefs" :key="r.id" class="ref-row">
              <a
                v-if="isWebUrl(r.url)"
                class="ref-main ref-link"
                :href="r.url ?? undefined"
                target="_blank"
                rel="noopener noreferrer"
              >
                <span class="ref-text">{{ docSummary(r) }}</span>
                <span class="ref-external" aria-hidden="true">↗</span>
              </a>
              <!-- SSH 形式などブラウザから開けないものは**文字列として出す**
                   （コピーできる状態にとどめる。5.5 / 5.9.1） -->
              <span v-else class="ref-main">
                <span class="ref-text">{{ docSummary(r) }}</span>
              </span>
              <span v-if="r.note" class="ref-label">{{ r.note }}</span>
              <template v-if="canEdit">
                <button type="button" class="ref-action" @click="openEditReference(r)">
                  編集
                </button>
                <button type="button" class="ref-action ref-danger" @click="refToDelete = r">
                  削除
                </button>
              </template>
            </li>
          </ul>
          <p v-else class="ref-empty">参考リンクはまだありません</p>
          <p v-if="errorFor('references')" class="field-error" role="alert">
            {{ errorFor('references') }}
          </p>
        </section>

        <p v-if="errorFor('children')" class="field-error" role="alert">
          {{ errorFor('children') }}
        </p>
        <p v-if="errorFor('delete')" class="field-error" role="alert">{{ errorFor('delete') }}</p>
      </template>
    </div>

    <ReferenceModal
      v-if="refModal"
      :reference="refModal.target"
      :busy="busy"
      :field-errors="refErrors"
      @close="refModal = null"
      @save="saveReference"
    />

    <ConfirmDialog
      v-if="refToDelete"
      :title="refToDelete.kind === 'code' ? 'コードを削除しますか？' : '参考リンクを削除しますか？'"
      :message="deleteReferenceMessage"
      confirm-label="削除する"
      danger
      :busy="busy"
      @cancel="refToDelete = null"
      @confirm="runDeleteReference"
    />

    <NewTicketModal
      v-if="showNewChild && ticket"
      :project-key="projectKey"
      :members="members"
      :tags="tags"
      :sprints="sprints"
      :candidates="childParentCandidates"
      :defaults="newChildDefaults"
      lock-parent
      :busy="busy"
      :field-errors="newChildErrors"
      @close="showNewChild = false"
      @save="createChild"
    />

    <ConfirmDialog
      v-if="confirmDelete"
      title="チケットを削除しますか？"
      :message="deleteMessage"
      confirm-label="削除する"
      danger
      :busy="busy"
      @cancel="confirmDelete = false"
      @confirm="runDelete"
    />
  </section>
</template>

<style scoped>
.detail {
  display: flex;
  flex-direction: column;
  min-width: 0;
  height: 100%;
}

/* 2.5 のページヘッダと同じ 48px・sticky。**新しい帯を作らない** */
.detail-header {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--pb-space-2);
  height: var(--pb-pageheader-h);
  padding: 0 var(--pb-space-4);
  border-bottom: 1px solid var(--pb-line);
  background: var(--pb-bg);
}

.detail-id {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: 15px;
  font-weight: 600;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.header-actions {
  display: flex;
  flex: none;
  align-items: center;
  gap: var(--pb-space-1);
}

.icon-button {
  width: 28px;
  height: 28px;
  padding: 0;
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: var(--pb-text-muted);
  cursor: pointer;
}

.icon-button:hover {
  border-color: var(--pb-border);
  color: var(--pb-text);
}

/* スクロールはこの中だけで起きる（2.5）。ヘッダは常時見える */
.detail-body {
  flex: 1;
  min-width: 0;
  overflow: auto;
  padding: var(--pb-space-4);
}

/* ── タイトル ─────────────────────────────────────────────── */

.title-block {
  margin-bottom: var(--pb-space-4);
}

/* 押せる領域だが、読むときはただの見出しに見せる（クリックで編集に入る） */
.title-view {
  display: block;
  width: 100%;
  padding: var(--pb-space-1) var(--pb-space-2);
  margin-left: calc(var(--pb-space-2) * -1);
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font-size: 18px;
  font-weight: 600;
  line-height: 1.5;
  text-align: left;
  cursor: text;
}

.title-view:hover:not(:disabled) {
  border-color: var(--pb-border);
}

.title-view.readonly {
  cursor: default;
}

.title-input {
  width: 100%;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-focus);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-size: 18px;
  font-weight: 600;
}

/* ── メタ情報（5.5 の2列グリッド）───────────────────────── */

/* **2列である**（5.5「2列のラベル＋値のグリッド」）。
   `auto-fit` にすると 750px のペインで3列になり、**見積・実績と開始・期限の
   組が行をまたいで割れる**（実際に踏んだ。スクリーンショットで発覚）。
   ペインが 510px を下回る幅は `SplitPane` が作らないので、2列で固定してよい */
.meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: var(--pb-space-2) var(--pb-space-4);
  margin: 0 0 var(--pb-space-4);
}

.meta-item {
  display: flex;
  align-items: baseline;
  gap: var(--pb-space-2);
  min-width: 0;
}

/* タグは行いっぱいを使う。数が読めないので1列に押し込むと折り返しが荒れる */
.meta-item.wide {
  grid-column: 1 / -1;
}

.meta-item.spacer {
  min-height: 0;
}

.meta dt {
  flex: none;
  width: 5.5em;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.meta dd {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: var(--pb-space-1);
  min-width: 0;
  margin: 0;
  flex-wrap: wrap;
}

/* **`flex: 1 1 0` と `min-width: 0` を必ず添える。** `max-width: 100%` だけだと
   選択肢の文字数ぶんの幅を主張し、**隣に置いた `↗` が次の行へ落ちる**
   （親の欄で実際に起きた）。縮む側にしておけば1行に収まる */
.meta select,
.meta input {
  flex: 1 1 0;
  height: 28px;
  min-width: 0;
  max-width: 100%;
  padding: 0 var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
}

/* 数値・日付の読み取り表示。タイトルと同じく「押せるが読むときは地の文」 */
.value-view {
  padding: 0 var(--pb-space-1);
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  font-size: 13px;
  cursor: text;
}

.value-view:hover:not(:disabled) {
  border-color: var(--pb-border);
}

.value-view:disabled {
  cursor: default;
}

.parent-cell .jump {
  flex: none;
  color: var(--pb-text-muted);
}

.parent-cell .jump:hover {
  color: var(--pb-text);
}

/* ── タグ ─────────────────────────────────────────────────── */

.tag-cell {
  flex-wrap: wrap;
  gap: var(--pb-space-1);
}

/* 枠線＋文字（8.6）。**色は使わない** */
.tag {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 0 var(--pb-space-1) 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  color: var(--pb-text-muted);
  font-size: 12px;
  line-height: 20px;
}

.tag-remove,
.tag-add {
  padding: 0 2px;
  border: none;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}

.tag-remove:hover:not(:disabled),
.tag-add:hover:not(:disabled) {
  color: var(--pb-text);
}

.tag-add {
  height: 22px;
  padding: 0 var(--pb-space-2);
  border: 1px dashed var(--pb-border);
  border-radius: var(--pb-radius);
}

.tag-picker {
  position: relative;
  display: inline-flex;
}

/* **`<Teleport>` にしない。** この小さなパネルはスクロール領域の中にあり、
   祖先に `overflow: hidden` を持つ箱が無い（`.detail-body` は `auto`）ので、
   絶対配置で足りる。位置の実測を持ち込むほうが壊れやすい */
.tag-menu {
  position: absolute;
  z-index: 10;
  top: calc(100% + 4px);
  left: 0;
  display: flex;
  flex-direction: column;
  max-height: 220px;
  min-width: 140px;
  overflow-y: auto;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  box-shadow: var(--pb-shadow-2);
}

.tag-option {
  padding: var(--pb-space-1) var(--pb-space-2);
  border: none;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.tag-option:hover {
  background: var(--pb-hover);
}

/* ── 区切りのあるブロック ─────────────────────────────────── */

.block + .block {
  margin-top: var(--pb-space-4);
}

.block-title {
  padding-bottom: var(--pb-space-1);
  margin-bottom: var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
}

.block-actions {
  display: flex;
  justify-content: flex-end;
  gap: var(--pb-space-2);
  margin-top: var(--pb-space-2);
}

.body-view {
  display: block;
  width: 100%;
  min-height: 48px;
  padding: var(--pb-space-2);
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  text-align: left;
  cursor: text;
}

.body-view:hover:not(:disabled) {
  border-color: var(--pb-border);
}

.body-view.readonly {
  cursor: default;
}

/* ── 子チケット ───────────────────────────────────────────── */

.children {
  list-style: none;
}

.child {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border-radius: var(--pb-radius);
}

.child:hover {
  background: var(--pb-hover);
}

.child-id {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 12px;
}

/* **主役には `flex: 1 1 auto` と `min-width` を必ず添える**（手順16d-b の教訓）。
   書かないと `0 1 auto` になり、`flex: none` の隣を残したままタイトルが潰れる */
.child-title {
  flex: 1 1 auto;
  min-width: 6em;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.child-status,
.child-assignee {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 12px;
  white-space: nowrap;
}

/* ── コードと参考リンク（5.5）。手順17c ─────────────────────── */

/* 見出しの右に操作を置くブロック（子チケット・参考リンク）。
   **下線は `.block-title` ではなくこちらに引く**——`h3` に引くと線が
   見出しの幅で切れ、右の `[+ 追加]` がブロックの外に見える */
.block-head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: var(--pb-space-2);
  padding-bottom: var(--pb-space-1);
  margin-bottom: var(--pb-space-2);
  border-bottom: 1px solid var(--pb-line);
}

.block-head .block-title {
  padding-bottom: 0;
  margin-bottom: 0;
  border-bottom: none;
}

.block-add {
  flex: none;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 12px;
  line-height: 22px;
  white-space: nowrap;
  cursor: pointer;
}

.block-add:hover {
  background: var(--pb-hover);
  color: inherit;
}

.refs {
  list-style: none;
}

.ref-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border-radius: var(--pb-radius);
}

.ref-row:hover {
  background: var(--pb-hover);
}

/* **主役には `flex: 1 1 auto` と `min-width` を必ず添える**（`GuiDesign.md` 6.7）。
   書かないと `0 1 auto` になり、`flex: none` の操作を残したまま本体が潰れる */
.ref-main {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: var(--pb-space-1);
  min-width: 8em;
  overflow: hidden;
}

.ref-link {
  color: inherit;
  text-decoration: none;
}

.ref-link:hover {
  text-decoration: underline;
}

.ref-code {
  overflow: hidden;
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 12px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.ref-text {
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.ref-external {
  flex: none;
  color: var(--pb-text-muted);
  font-size: 11px;
}

/* ラベル・メモは補助。**本体より先に縮む**ように `0 1 auto` にしてある */
.ref-label {
  flex: 0 1 auto;
  overflow: hidden;
  color: var(--pb-text-muted);
  font-size: 12px;
  white-space: nowrap;
  text-overflow: ellipsis;
}

/* **行の操作は `[編集]` / `[削除]` の文字列**（5.5、利用者の判断 2026-08-27）。
   行そのものがリンクなので、行クリックに編集の意味を持たせられない */
.ref-action {
  flex: none;
  padding: 0 var(--pb-space-1);
  border: none;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 12px;
  white-space: nowrap;
  cursor: pointer;
}

.ref-action:hover {
  color: inherit;
  text-decoration: underline;
}

.ref-danger:hover {
  color: var(--pb-danger-text);
}

.ref-empty {
  padding: var(--pb-space-1) var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}

/* ── 共通 ─────────────────────────────────────────────────── */

.type-icon {
  flex: none;
  color: var(--pb-text-muted);
}

.muted {
  color: var(--pb-text-muted);
}

/* 失敗はその欄の直下に出す（5.5 / 6.4）。**入力値を捨てない** */
.field-error {
  width: 100%;
  margin: 2px 0 0;
  color: var(--pb-danger-text);
  font-size: 12px;
}

.skeleton-block {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-3);
}

.skeleton {
  display: block;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.skeleton:nth-child(odd) {
  width: 70%;
}
</style>
