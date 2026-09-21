<script setup lang="ts">
import { uiText } from '../locales/ui'
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
 * **まだ出さないセクションは見出しごと出さない**（5.5）——Phase 1 で残っている
 * のは履歴（19）だけである。空の枠を置くと「実装済みで中身が無い」に見え、
 * `comment_count` が実数を返すぶん誤解が強くなる。
 *
 * **完了条件・関連チケット・コメントは手順18b で足した**（5.5、`ApiDesign.md`
 * 9.9 / 9.10.1 / 9.8）。**新しいエンドポイントは1本も要らなかった**——18a で
 * すべて通してある。完了条件と関連チケットは詳細応答（9.5.1）の `dod` / `links`
 * をここが所有し、**コメントだけが別の `GET` とページングを持つ**ので
 * `TicketComments.vue` に切り出した。
 *
 * **コードと参考リンクは手順17c で足した**（5.5、`ApiDesign.md` 9.10.2）。
 * 0件のときの扱いは2つで違う——**コードは見出しごと出さず**（画面から追加できず、
 * 空の枠は何もできない箱になる）、**参考リンクは常に出す**（`[+ 追加]` が
 * このセクションへの唯一の入口で、隠すと機能へ到達できない）。
 */
import { computed, defineAsyncComponent, nextTick, ref, useTemplateRef, watch } from 'vue'

import ConfirmDialog from './ConfirmDialog.vue'
import EmptyState from './EmptyState.vue'
/**
 * **型だけを取り込む。** `import type` は実行時の import を生まないので、
 * 下の `defineAsyncComponent` によるチャンク分割を打ち消さない。
 */
import type MarkdownEditorComponent from './MarkdownEditor.vue'
import NewTicketModal from './NewTicketModal.vue'
import type { NewTicketDefaults } from './NewTicketModal.vue'
import ReferenceModal from './ReferenceModal.vue'
import StatusDropdown from './StatusDropdown.vue'
import TicketActivity from './TicketActivity.vue'
import TicketComments from './TicketComments.vue'
import TicketLinkModal from './TicketLinkModal.vue'
import UserActionsMenu from './UserActionsMenu.vue'
import type { ActionItem } from './UserActionsMenu.vue'
import { ApiError } from '../api/client'
import * as dodApi from '../api/dod'
import type { TicketDoDItem } from '../api/dod'
import * as linksApi from '../api/links'
import { linkLabel, linkLabelTitle } from '../api/links'
import type { LinkChoice, TicketLink } from '../api/links'
import type { ProjectMember, Workflow } from '../api/projects'
import * as referencesApi from '../api/references'
import { codeSummary, docSummary } from '../api/references'
import type { TicketReference } from '../api/references'
import type { Tag } from '../api/tags'
import * as ticketsApi from '../api/tickets'
import {
  executionModeLabels,
  executionModeOptions,
  priorityLabels,
  priorityOrder,
  readinessLabels,
  readinessOptions,
  scopeKeys,
  scopeText,
  ticketTypeIcons,
  ticketTypeLabels,
  unknownScopeEntries,
  withScopeLines,
} from '../api/tickets'
import type { ScopeKey } from '../api/tickets'
import type {
  CreateTicketRequest,
  Ticket,
  TicketDetail,
  TicketPriority,
  TicketTransitionOption,
  TicketType,
  UpdateTicketRequest,
} from '../api/tickets'
import { formatPlainDate } from '../lib/datetime'
import { renderMarkdown } from '../lib/markdown'
import { isWebUrl } from '../lib/url'
import { useAuthStore } from '../stores/auth'

/**
 * **編集器は使うときに読み込む**（`GuiDesign.md` 5.10、`fix/split-markdown-chunk`）。
 *
 * CodeMirror が client の初回チャンクを 980KB にしている原因で、**説明欄は
 * 詳細ペインを開いて編集を始めたときにしか要らない。** Docs（5.10）も同じ形で
 * 読み込む——**片方が同期で取り込んでいる限りチャンクは分かれない**ので、
 * 2つある利用者の両方をここで揃える。
 */
const MarkdownEditor = defineAsyncComponent(() => import('./MarkdownEditor.vue'))

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
  /**
   * プロジェクトのワークフロー（`GET /projects/:key` の `workflow`）。
   *
   * **履歴（5.5）が `status_key` を表示名に直すのに使う。** `activity` は
   * キーしか返さない（`ApiDesign.md` 9.13.2）。状態のドロップダウンは
   * `GET .../transitions` を開いたときに引くので、こちらは使わない。
   */
  workflow?: Workflow | null
  /**
   * 親の選択肢（5.5「編集の単位」）。**いま一覧に出ているチケット**から選ぶ。
   * バックログは最大200件を既に手元に持っており、追加の往復を要しない。
   * **エピックは混ざっていても候補に出さない**（5.5「エピック欄」。pb-14）。
   */
  candidates: Ticket[]
  /**
   * エピック欄の選択肢（5.5「エピック欄」。pb-14）。バックログが持つ
   * `GET /tickets?type=epic` の語彙で、追加の往復を要しない。
   */
  epics?: Ticket[]
}>()

const emit = defineEmits<{
  close: []
  /** 変更が確定した。一覧側が該当行だけ差し替える（取り直さない） */
  updated: [ticket: TicketDetail]
  /**
   * **行が増えた。** `updated` と分けているのは、受け側の扱いが違うからである
   * ——`updated` は既にある行の差し替えで済むが、**新しい行は一覧の結果集合に
   * 一度も入っていない**ので、フィルタに合うか・`sort_key` のどこに入るかを
   * 一覧側では決められない。`deleted` が取り直しているのと同じ理由（pb-15）。
   */
  created: [ticket: TicketDetail]
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
        message: uiText("予期しないエラーが発生しました"),
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
  | 'readiness_note'
  | ScopeField

/** スコープ境界はキーごとに1欄（5.5「スコープ境界」。pb-45） */
type ScopeField = `scope.${ScopeKey}`

function isScopeField(field: EditField): field is ScopeField {
  return field.startsWith('scope.')
}

function scopeKeyOf(field: ScopeField): ScopeKey {
  return field.slice('scope.'.length) as ScopeKey
}

const editing = ref<EditField | null>(null)
const draft = ref('')
/** 失敗した欄と、その直下に出すサーバの `message`（5.5 / 6.4） */
const fieldError = ref<{ field: string; message: string } | null>(null)

const editorRef = useTemplateRef<InstanceType<typeof MarkdownEditorComponent>>('editorRef')
const inputRef = useTemplateRef<HTMLInputElement>('inputRef')
/** `v-for` の中に置くので配列で返る。開いているのは常に1つだけ */
const scopeRef = useTemplateRef<HTMLTextAreaElement[]>('scopeRef')

/** いまの値を編集用の文字列にする。**数値の `null` は空文字**（未設定と 0 を分ける） */
function currentText(field: EditField): string {
  const t = ticket.value
  if (t === null) return ''
  if (isScopeField(field)) return scopeText(t.scope, scopeKeyOf(field))
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
  else if (isScopeField(field)) scopeRef.value?.[0]?.focus()
  else inputRef.value?.focus()
}

/**
 * `Enter` で確定する（5.5「編集の単位」）。**IME の変換確定を確定と読み違えない。**
 *
 * 日本語入力では、変換候補を確定する `Enter` も `keydown` として飛んでくる。
 * その keydown は `isComposing: true`（`keyCode` は 229）で来るが、**Vue の
 * `.enter` 修飾子はこれを区別しない**。素直に書くと、**変換を確定したつもりの
 * `Enter` が欄の確定として処理され、変換前の文字列のまま編集モードを抜ける**
 * （pb-42。利用者の報告、2026-09-05）。
 *
 * **`.prevent` を修飾子で付けてはいけない。** あちらはハンドラより先に
 * `preventDefault()` を呼ぶので、**変換確定そのものを止めてしまう**。
 * 変換中でないと分かってから止める。
 *
 * `keyCode === 229` も見るのは、`isComposing` を立てない実装が残っているため
 * （古い WebKit）。**どちらか一方でも真なら変換中として扱う。**
 */
function onEnterCommit(e: KeyboardEvent, commit: () => void): void {
  if (e.isComposing || e.keyCode === 229) return
  e.preventDefault()
  commit()
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

  // スコープ境界は**整えた後で**比べる。空行や前後の空白だけを足した編集で
  // `PATCH` を送らない（5.5「スコープ境界」）
  if (isScopeField(field)) {
    const key = scopeKeyOf(field)
    const next = withScopeLines(ticket.value.scope, key, raw)
    if (scopeText(next, key) === currentText(field)) {
      cancelEdit()
      return
    }
    await save({ scope: next }, field)
    return
  }

  if (raw === currentText(field)) {
    cancelEdit()
    return
  }

  const patch: UpdateTicketRequest = {}
  if (field === 'title') {
    patch.title = raw.trim()
  } else if (field === 'readiness_note') {
    patch.readiness_note = raw.trim() === '' ? null : raw.trim()
  } else if (field === 'body_md') {
    patch.body_md = raw === '' ? null : raw
  } else if (field === 'start_date' || field === 'due_date') {
    // **`new Date()` を通さない**（`date` 列。`input[type=date]` の値がそのまま
    // `YYYY-MM-DD` である）。通すと UTC より西の地域で前日へずれる
    patch[field] = raw === '' ? null : raw
  } else {
    const n = numberOrNull(raw)
    if (n === undefined) {
      fieldError.value = { field, message: uiText("数値で入力してください") }
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
  // **編集中の欄があれば先に保存する**（5.5「別の領域をクリックしてフォーカスを
  // 外す」＝保存。pb-43）。`startEdit` が既に持っている作法を、選ぶ側にも広げる
  // ——広げないと、**タグを1つ足しただけで説明の下書きが消える**。
  //
  // **先の保存が失敗したらそこで止める。** 失敗を踏み越えて別の変更を通すと、
  // 利用者は何が保存されたのか読めなくなる。
  if (editing.value !== null && editing.value !== field) {
    await commitEdit()
    if (editing.value !== null) return
  }
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
    // **閉じるのは、いま編集している欄を保存したときだけ**（pb-43）。
    // 以前は成功のたびに閉じていたので、**関係のない欄の `PATCH` が成功しただけで
    // 編集中の下書きが（保存もされずに）消えていた**。
    if (editing.value === field) cancelEdit()
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
 * 遷移させる（9.6）。**選んだ時点で送る**（5.5「状態のドロップダウン」。pb-55）。
 *
 * **確認モーダルは廃止した。** 手順18b で「なぜ動かしたかを残せる場所を同じ操作の
 * 中に置く価値が、クリック1つより大きい」として足したものだが、**実運用では逆
 * だった**——状態変更は頻度が高く、毎回ダイアログを挟むのは現実的でない
 * （利用者の判断、2026-09-06）。「選べるものは選んだ時点で `PATCH`」という
 * 5.5 の原則へ戻した。
 *
 * **履歴は `activity` が持つ**（9.8）。誰がどの状態からどの状態へ変えたかは
 * 残るので、ダイアログを外しても記録は失われない。
 *
 * **API の `comment` は任意のまま残っている**（9.6）。MCP の `pb_transition_task`
 * は今までどおりコメントを添えられる——**画面が使わなくなっただけである。**
 */
async function transition(to: TicketTransitionOption): Promise<void> {
  const t = ticket.value
  if (t === null) return
  busy.value = true
  fieldError.value = null
  try {
    const next = await ticketsApi.transitionTicket(props.projectKey, t.seq, { to: to.key })
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
    label: uiText("子チケットを追加"),
    disabled: !canCreate.value,
    reason: canCreate.value ? undefined : uiText("チケットを作成する権限がありません"),
  },
  {
    key: 'add-reference',
    label: uiText("参考リンクを追加"),
    disabled: !canEdit.value,
    reason: canEdit.value ? undefined : uiText("チケットを編集する権限がありません"),
  },
  {
    key: 'delete',
    label: uiText("このチケットを削除"),
    danger: true,
    disabled: !canDelete.value,
    reason: canDelete.value ? undefined : uiText("チケットを削除する権限がありません"),
  },
])

/** **子チケットの件数を出す**（6.3）。子は消えず、親を失ってトップレベルへ上がる */
const deleteMessage = computed(() => {
  const t = ticket.value
  if (t === null) return ''
  const head = uiText("{value0}「{value1}」を削除します。元に戻せません。", { value0: fullId.value, value1: t.title })
  if (t.children.length === 0) return head
  return uiText("{value0}\n{value1}件の子チケットは削除されず、親のないチケットになります。", { value0: head, value1: t.children.length })
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
  const what = r.kind === 'code' ? uiText("コード") : uiText("参考リンク")
  const summary = r.kind === 'code' ? codeSummary(r) : docSummary(r)
  const head = uiText("{value0}「{value1}」を削除します。元に戻せません。", { value0: what, value1: summary })
  // **code は画面から入れ直せない**（追加の導線を持たない。5.5）
  return r.kind === 'code'
    ? uiText("{value0}\nコードは画面から追加できないため、消すと入れ直せません。", { value0: head })
    : head
})

// ── 完了条件（5.5、`ApiDesign.md` 9.9）。手順18b ──────────────

/**
 * **詳細応答（9.5.1）の `dod` をそのまま使う。** `GET .../dod` は呼ばない
 * ——同じ一覧が入っており、画面を開いた時点で見えているものだからである
 * （8章の「起動時1〜2本」）。サーバが `sort_order` → `created_at` の昇順で
 * 返すので、**画面で並べ替え直さない。**
 */
const dodItems = computed(() => ticket.value?.dod ?? [])

/** 追加欄。**セクション末尾に常設する**——列挙するときは続けて何件も打つ（5.5） */
const newDoD = ref('')
const dodError = ref('')

/** 本文の編集。**その場で編集に入る**（「編集の単位」の文字欄と同じ） */
const dodEditingId = ref<string | null>(null)
const dodDraft = ref('')

/** 削除の確認（6.3）。物理削除で戻せない */
const dodToDelete = ref<TicketDoDItem | null>(null)

async function addDoD(): Promise<void> {
  const t = ticket.value
  const body = newDoD.value.trim()
  if (t === null || body === '' || busy.value) return
  busy.value = true
  dodError.value = ''
  try {
    // **`sort_order` は送らない**——省くと末尾に置かれ（9.9）、画面は
    // 並べ替えを持たない。`type` と `is_satisfied` は**生成された型が
    // 必須にしている**（既定値を持つ項目は省略可にならない）ので、
    // **サーバの既定と同じ値を明示して送る**——挙動は省いたときと変わらない
    const created = await dodApi.createDoD(props.projectKey, t.seq, {
      type: 'manual',
      body,
      is_satisfied: false,
    })
    t.dod = [...t.dod, created]
    newDoD.value = ''
    emit('updated', t)
  } catch (e) {
    dodError.value = toApiError(e).message
  } finally {
    busy.value = false
  }
}

function startDoDEdit(item: TicketDoDItem): void {
  if (!canEdit.value) return
  dodEditingId.value = item.id
  dodDraft.value = item.body
  dodError.value = ''
}

function cancelDoDEdit(): void {
  dodEditingId.value = null
  dodDraft.value = ''
}

async function commitDoDEdit(): Promise<void> {
  const id = dodEditingId.value
  const body = dodDraft.value.trim()
  const item = dodItems.value.find((d) => d.id === id)
  if (id === null || item === undefined || busy.value) return
  // **変わっていないなら送らない**（5.5「編集の単位」）
  if (body === '' || body === item.body) {
    cancelDoDEdit()
    return
  }
  await patchDoD(id, { body })
  cancelDoDEdit()
}

/**
 * チェックの付け外し。**付けた時点で `PATCH`**（選択式と同じ扱い）。
 *
 * **サーバが `satisfied_at` と `satisfied_by` を同時に動かす**ので、
 * 応答の1件をそのまま入れ替える（手元で組み立てない）。
 */
async function toggleDoD(item: TicketDoDItem, next: boolean): Promise<void> {
  await patchDoD(item.id, { is_satisfied: next })
}

async function patchDoD(
  id: string,
  patch: Parameters<typeof dodApi.updateDoD>[3],
): Promise<void> {
  const t = ticket.value
  if (t === null || busy.value) return
  busy.value = true
  dodError.value = ''
  try {
    const updated = await dodApi.updateDoD(props.projectKey, t.seq, id, patch)
    const i = t.dod.findIndex((d) => d.id === id)
    if (i >= 0) t.dod[i] = updated
    emit('updated', t)
  } catch (e) {
    dodError.value = toApiError(e).message
  } finally {
    busy.value = false
  }
}

async function runDeleteDoD(): Promise<void> {
  const t = ticket.value
  const target = dodToDelete.value
  if (t === null || target === null) return
  busy.value = true
  dodError.value = ''
  try {
    await dodApi.deleteDoD(props.projectKey, t.seq, target.id)
    t.dod = t.dod.filter((d) => d.id !== target.id)
    emit('updated', t)
  } catch (e) {
    dodError.value = toApiError(e).message
  } finally {
    dodToDelete.value = null
    busy.value = false
  }
}

// ── 関連チケット（5.5、`ApiDesign.md` 9.10.1）。手順18b ───────

/**
 * **詳細応答の `links` をそのまま使う**（`dod` と同じ理由）。
 *
 * **双方向が1本で返る。** `direction`（`outgoing` → `incoming`）、同じ向きの
 * 中は `link_type` → 相手の `seq` の昇順で、**画面で並べ替え直さない。**
 * **`ticket` に入るのは常に相手であって自分ではない。**
 */
const links = computed(() => ticket.value?.links ?? [])

const showLinkModal = ref(false)
const linkFieldErrors = ref<Record<string, string>>({})
/** 欄に紐づかない誤り（409 `already_exists`）。**モーダルの中に留める**（6.4） */
const linkFormError = ref('')
const linkError = ref('')
const linkToDelete = ref<TicketLink | null>(null)

/** 相手の候補。**自分自身は落とす**——サーバも 422 `self_link` で弾く（5.5） */
const linkCandidates = computed(() =>
  props.candidates.filter((c) => c.seq !== props.seq),
)

function openLinkModal(): void {
  linkFieldErrors.value = {}
  linkFormError.value = ''
  showLinkModal.value = true
}

/**
 * リンクを1件足す（9.10.1）。
 *
 * **`自後行` は API に無い。** `POST .../links` は**呼んだチケットが常に
 * `source`** になるので、`incoming` の行は直接作れない——**相手のチケットに
 * 対して `blocks` を作る**ことで同じ関係になる（5.5「追加のモーダル」）。
 *
 * その場合、応答は**相手側から見た `outgoing` の行**なので手元へは入れられない。
 * **詳細を取り直す**——この1つの場合だけ、往復が1回増える。
 */
async function saveLink(body: { targetSeq: number; choice: LinkChoice['value'] }): Promise<void> {
  const t = ticket.value
  if (t === null) return
  busy.value = true
  linkFieldErrors.value = {}
  linkFormError.value = ''
  try {
    if (body.choice === 'blocks_in') {
      await linksApi.createLink(props.projectKey, body.targetSeq, {
        target_seq: t.seq,
        link_type: 'blocks',
        // 既定と同じ値。**`FS`〜`SF` のときしか意味を持たない**（9.10.1）
        lag_days: 0,
      })
      await load()
    } else {
      const created = await linksApi.createLink(props.projectKey, t.seq, {
        target_seq: body.targetSeq,
        link_type: body.choice === 'blocks_out' ? 'blocks' : body.choice,
        lag_days: 0,
      })
      t.links = [...t.links, created].sort(compareLinks)
      emit('updated', t)
    }
    showLinkModal.value = false
  } catch (e) {
    const err = toApiError(e)
    if (err.details.length > 0) {
      linkFieldErrors.value = Object.fromEntries(
        err.details.map((d) => [d.field, d.message]),
      )
    } else {
      // 409 `already_exists` はここに来る。**閉じずにモーダルの中へ出す**
      linkFormError.value = err.message
    }
  } finally {
    busy.value = false
  }
}

/** `direction`（`outgoing` → `incoming`）→ `link_type` → 相手の `seq`（9.10.1 と同じ順序） */
function compareLinks(a: TicketLink, b: TicketLink): number {
  if (a.direction !== b.direction) return a.direction === 'outgoing' ? -1 : 1
  if (a.link_type !== b.link_type) return a.link_type < b.link_type ? -1 : 1
  return a.ticket.seq - b.ticket.seq
}

async function runDeleteLink(): Promise<void> {
  const t = ticket.value
  const target = linkToDelete.value
  if (t === null || target === null) return
  busy.value = true
  linkError.value = ''
  try {
    // **`direction` を問わず消せる**（9.10.1）
    await linksApi.deleteLink(props.projectKey, t.seq, target.id)
    t.links = t.links.filter((l) => l.id !== target.id)
    emit('updated', t)
  } catch (e) {
    linkError.value = toApiError(e).message
  } finally {
    linkToDelete.value = null
    busy.value = false
  }
}

/**
 * 削除の確認文（6.3）。**`incoming` は相手の詳細へ行かないと作り直せない**
 * ——`POST .../links` はこのチケットを常に `source` にするためである。
 */
const deleteLinkMessage = computed(() => {
  const l = linkToDelete.value
  if (l === null) return ''
  const label = linkLabel(l.link_type, l.direction)
  const head = uiText("「{value0}」の関連（{value1}-{value2}「{value3}」）を解除します。", { value0: label, value1: props.projectKey, value2: l.ticket.seq, value3: l.ticket.title })
  return l.direction === 'incoming'
    ? uiText("{value0}\n相手のチケットが起点の関連なので、戻すには {value1}-{value2} を開く必要があります。", { value0: head, value1: props.projectKey, value2: l.ticket.seq })
    : head
})

// ── コメント（5.5、`ApiDesign.md` 9.8）。手順18b ──────────────

/**
 * 見出しの `(4)` に使う数を足し引きする。
 *
 * **`comment_count` は詳細応答が持つ**（`deleted_at IS NULL` で数える）。
 * `TicketComments` が持つ `total` は**削除済みも数える**ので答えが違う——
 * **同じ表を数えて違う答えを返すのは意図的である**（9.8）。取り直さずに
 * 足し引きするのは、コメントの増減が親チケットの `version` を動かさないため。
 */
function onCommentCountDelta(delta: number): void {
  const t = ticket.value
  if (t === null) return
  t.comment_count = Math.max(0, t.comment_count + delta)
  emit('updated', t)
}

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

/**
 * 親の候補。**このチケット自身に固定する**ので1件だけ渡す。
 * **エピックなら親チケット欄には渡さず、エピック欄を固定する**（5.4.3「親チケットとエピック」。pb-14）
 */
const childParentCandidates = computed<Ticket[]>(() => {
  const t = ticket.value
  if (t === null || t.type === 'epic') return []
  const self = props.candidates.find((c) => c.seq === t.seq)
  return self ? [self] : []
})

/** エピック欄の候補。**エピックの子を作るときだけ**、このチケット自身を1件渡す */
const childEpicCandidates = computed<Ticket[]>(() =>
  ticket.value?.type === 'epic' ? [ticket.value] : [],
)

const newChildDefaults = computed<NewTicketDefaults>(() => {
  const t = ticket.value
  if (t === null) return {}
  return t.type === 'epic' ? { epic_seq: t.seq } : { parent_seq: t.seq }
})

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
    const child = await ticketsApi.createTicket(props.projectKey, { ...body, parent_seq: t.seq })
    showNewChild.value = false
    await load()
    // **`updated` ではなく `created` を流す**（pb-15）。親の行を差し替えるだけでは
    // **作った子が一覧に一度も現れない**——バックログは詳細を開いたまま子を
    // 増やせるので、リロードするまで見えない状態が残っていた。
    emit('created', child)
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

/**
 * 子チケットの担当を、一覧の行から変える（5.5「子チケット」。pb-60）。
 *
 * **`children` は `version` を持たない**（`ApiDesign.md` 9.5.1 の `TicketChild` は
 * `seq` / `title` / `type` / `status` / `assignee` だけ）。`PATCH` は `If-Match` が
 * 必須なので（2.8）、**子の詳細を1回引いて `version` を得てから送る。**
 * `TicketChild` に `version` を足す案は採らなかった——**画面の都合でスキーマを
 * 広げるより、往復を1回増やすほうが安い。**
 *
 * **終わったら親の詳細を取り直す。** `children` はサーバが組み立てるものなので、
 * 応答（子のほう）を手元へ差し込んでも親の一覧は古いままである。
 */
async function setChildAssignee(childSeq: number, assigneeId: string | null): Promise<void> {
  if (!canEdit.value || busy.value) return
  busy.value = true
  fieldError.value = null
  try {
    const cur = await ticketsApi.getTicket(props.projectKey, childSeq)
    await ticketsApi.updateTicket(props.projectKey, childSeq, cur.version, {
      assignee_id: assigneeId,
    })
    await load()
    if (ticket.value !== null) emit('updated', ticket.value)
  } catch (e) {
    const err = toApiError(e)
    fieldError.value = { field: 'children', message: err.details[0]?.message ?? err.message }
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

/**
 * 親の候補。**自分自身は落とす**。子孫はサーバの 422 `parent_cycle` に任せる（5.5）。
 * **エピックも落とす**——エピック欄で選ぶ（5.5「エピック欄」。pb-14）。
 */
const parentOptions = computed(() =>
  props.candidates.filter((c) => c.seq !== props.seq && c.type !== 'epic'),
)

/**
 * 親の欄に出す親。**親がエピックなら「親なし」と出す**——エピックはエピック欄が
 * 出す（5.5「エピック欄」。pb-14）。保存されている `parent_seq` は同じ1列である。
 */
const parentTicket = computed(() => {
  const p = ticket.value?.parent
  return p && p.type !== 'epic' ? p : null
})

/**
 * `parent_seq` の失敗を**どちらの欄の下に出すか**。親とエピックは同じ1列を送るので、
 * サーバの `details[].field` だけでは決まらない——最後に触った欄の下に出す。
 */
const parentErrorAt = ref<'parent' | 'epic'>('parent')

/**
 * 親の選択（5.5「親は選択式である」。pb-48）。
 *
 * **`<select>` をやめて、絞り込みのできる一覧にした。** チケットが増えると
 * `<option>` を目で探すのが現実的でなくなる（利用者の報告、2026-09-06）。
 * **候補の出どころは変えていない**——いま一覧に出ているチケット（最大200件）で、
 * 追加の往復を要しない。
 */
const parentPickerOpen = ref(false)
const parentQuery = ref('')

/**
 * いま候補に出ていない現在の親も一覧へ入れる。**落とすと、開いた瞬間に
 * 現在値が消えて見える**（オンステージの配下などは候補に来ない）。
 */
const parentChoices = computed(() => {
  const list = [...parentOptions.value]
  const cur = parentTicket.value
  if (cur && !list.some((c) => c.seq === cur.seq)) {
    list.unshift({ seq: cur.seq, title: cur.title } as (typeof list)[number])
  }
  return list
})

/**
 * 絞り込み。**番号でもタイトルでも当たる**——`my-app-31` の完全形と、
 * `31` のような数字だけの入力の両方を拾う。大文字小文字は区別しない。
 */
const parentMatches = computed(() => {
  const q = parentQuery.value.trim().toLowerCase()
  if (q === '') return parentChoices.value
  return parentChoices.value.filter((c) =>
    `${props.projectKey}-${c.seq} ${c.title}`.toLowerCase().includes(q),
  )
})

function openParentPicker(): void {
  if (!canEdit.value || busy.value) return
  parentQuery.value = ''
  parentPickerOpen.value = true
  void nextTick(() => parentSearchRef.value?.focus())
}

async function pickParent(seq: number | null): Promise<void> {
  parentPickerOpen.value = false
  if ((parentTicket.value?.seq ?? null) === seq) return // 変わらないなら送らない（5.5）
  parentErrorAt.value = 'parent'
  // **親を外しても、祖先のエピックには残す**（5.5「エピック欄」。利用者の判断、
  // 2026-09-15）。親を外す操作で、エピックという属性まで失わせない
  await selectField({ parent_seq: seq ?? ticket.value?.epic?.seq ?? null }, 'parent_seq')
}

// ── エピック欄（5.5「エピック欄」。pb-14）─────────────────────

/**
 * エピックを選べるか。**親（エピック以外）が無いときだけ**——配下のツリーは親と
 * 一緒にエピックへ属する（`ApiDesign.md` 9.2.1 の `parent` は部分木で絞る）。
 */
const epicSelectable = computed(() => parentTicket.value === null)

/** 語彙に無い現在値も入れる。落とすと、選ばれている値が「なし」に見える */
const epicChoices = computed(() => {
  const list = [...(props.epics ?? [])]
  const cur = ticket.value?.epic
  if (cur && !list.some((e) => e.seq === cur.seq)) {
    list.unshift({ seq: cur.seq, title: cur.title } as (typeof list)[number])
  }
  return list
})

async function pickEpic(value: string): Promise<void> {
  const seq = value === '' ? null : Number(value)
  if ((ticket.value?.epic?.seq ?? null) === seq) return // 変わらないなら送らない（5.5）
  parentErrorAt.value = 'epic'
  await selectField({ parent_seq: seq }, 'parent_seq')
}

const parentSearchRef = useTemplateRef<HTMLInputElement>('parentSearchRef')

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
  <section class="detail" :aria-label="$ui('チケット詳細')">
    <!-- 48px のページヘッダ（2.5）。**`<h1>` にはしない**——このペインは画面を
         置き換えないので、`<h1>` は一覧側の「バックログ」1つのままにする（9.2） -->
    <header class="detail-header">
      <template v-if="ticket">
        <span class="type-icon" :title="ticketTypeLabels[ticket.type]" aria-hidden="true">
          {{ ticketTypeIcons[ticket.type] }}
        </span>
        <!-- **ID の右にタイトルを並べる**（5.5。pb-10）。1段目は領域が余っており、
             2段目を畳めば1行ぶんの縦が本文へ回る。**ID は残す**——5.4「ID列」が
             完全形を出すと定めており、詳細から消えると照合できなくなる -->
        <h2 class="detail-id">{{ fullId }}</h2>
        <template v-if="editing === 'title'">
          <input
            ref="inputRef"
            v-model="draft"
            class="title-input"
            type="text"
            maxlength="200"
            :aria-label="$ui('タイトル')"
            @keydown.escape="cancelEdit"
            @keydown.enter="onEnterCommit($event, commitEdit)"
            @blur="commitEdit"
          />
        </template>
        <button
          v-else
          type="button"
          class="title-view"
          :class="{ readonly: !canEdit }"
          :disabled="!canEdit"
          :title="canEdit ? $ui('クリックしてタイトルを編集：{value0}', { value0: ticket.title }) : ticket.title"
          @click="startEdit('title')"
        >
          {{ ticket.title }}
        </button>
      </template>
      <h2 v-else class="detail-id">{{ $ui('チケット') }}</h2>

      <div class="header-actions">
        <UserActionsMenu
          v-if="ticket"
          :items="actionItems"
          :label="$ui('{value0} の操作メニュー', { value0: fullId })"
          compact
          @select="onAction"
        />
        <button type="button" class="icon-button" :aria-label="$ui('詳細を閉じる')" @click="emit('close')">
          ✕
        </button>
      </div>
    </header>

    <div class="detail-body">
      <!-- エラー（6.2）。原因はサーバが返した message をそのまま出す -->
      <EmptyState
        v-if="loadError"
        :title="$ui('チケットを取得できませんでした')"
        :description="loadError.message"
      >
        <template #action>
          <button type="button" class="primary" @click="load">{{ $ui('再試行') }}</button>
        </template>
      </EmptyState>

      <div v-else-if="loading && ticket === null" class="skeleton-block" aria-busy="true">
        <span v-for="n in 6" :key="n" class="skeleton"></span>
      </div>

      <template v-else-if="ticket">
        <!-- タイトルはヘッダ段へ移した（5.5。pb-10）。**失敗の文言だけは本文側に
             残す**——48px のヘッダに複数行を入れると段の高さが動く（6.4） -->
        <p v-if="errorFor('title')" class="field-error" role="alert">{{ errorFor('title') }}</p>

        <!-- メタ情報（5.5）。**2列のラベル＋値のグリッド** -->
        <dl class="meta">
          <div class="meta-item">
            <dt>{{ $ui('状態') }}</dt>
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
            <dt>{{ $ui('担当') }}</dt>
            <dd>
              <!-- **`ticket.assign` が要る**（9.5.2）。持たないときは値だけ出す -->
              <select
                v-if="canEdit && canAssign"
                :value="ticket.assignee?.id ?? ''"
                :disabled="busy"
                :aria-label="$ui('担当')"
                @change="
                  selectField(
                    { assignee_id: ($event.target as HTMLSelectElement).value || null },
                    'assignee_id',
                  )
                "
              >
                <option value="">{{ $ui('未割当') }}</option>
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

          <!-- 実行者（5.5「実行者の行」。手順26b）。**居るときだけ1行出す**
               ——大半のチケットで空になる欄に、最上部の1行を常時使わない。
               **出せるのは「消す」だけ**で、立てるのはエージェント自身である
               （9.6 の遷移の副作用）。ピッカーを置くと、人が指名した実行者と
               実際に動いたエージェントが食い違う状態を作れてしまう。 -->
          <div v-if="ticket.working_agent" class="meta-item wide">
            <dt>{{ $ui('実行者') }}</dt>
            <dd>
              <span>
                <span aria-hidden="true">{{ actorMark(ticket.working_agent.kind) }}</span>
                {{ ticket.working_agent.display_name }}
              </span>
              <!-- **確認モーダルを出さない**（6.3）。失われるのは自己申告の1欄で、
                   次の遷移で立ち直る -->
              <button
                v-if="canEdit && canAssign"
                type="button"
                class="ref-action"
                :disabled="busy"
                @click="selectField({ working_agent_id: null }, 'working_agent_id')"
              > {{ $ui('解除') }} </button>
              <p v-if="errorFor('working_agent_id')" class="field-error" role="alert">
                {{ errorFor('working_agent_id') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>{{ $ui('優先度') }}</dt>
            <dd>
              <select
                v-if="canEdit"
                :value="ticket.priority ?? ''"
                :disabled="busy"
                :aria-label="$ui('優先度')"
                @change="
                  selectField(
                    { priority: (($event.target as HTMLSelectElement).value || null) as never },
                    'priority',
                  )
                "
              >
                <option value="">{{ $ui('未設定') }}</option>
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
            <dt>{{ $ui('種別') }}</dt>
            <dd>
              <!-- **オンステージのチケットを `epic` にすると 422**（`not_stageable`）。
                   サーバが弾いた理由をそのまま欄の下に出す（9.5.2） -->
              <select
                v-if="canEdit"
                :value="ticket.type"
                :disabled="busy"
                :aria-label="$ui('種別')"
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
            <dt>{{ $ui('親') }}</dt>
            <dd class="parent-cell">
              <!-- **選択式である**（5.5）。親の実体は `parent_seq` の数値で、
                   番号を手で打たせる形はどの画面にも無い。**絞り込みができる
                   一覧にした**（pb-48）——`<option>` を目で探せる件数を超えた -->
              <template v-if="canEdit">
                <button
                  type="button"
                  class="parent-trigger"
                  :disabled="busy"
                  aria-haspopup="listbox"
                  :aria-expanded="parentPickerOpen"
                  :aria-label="$ui('親チケット')"
                  @click="parentPickerOpen ? (parentPickerOpen = false) : openParentPicker()"
                >
                  <span class="parent-current">{{
                    parentTicket
                      ? `${projectKey}-${parentTicket.seq} ${parentTicket.title}`
                      : $ui("親なし")
                  }}</span>
                  <span class="caret" aria-hidden="true">▾</span>
                </button>

                <div v-if="parentPickerOpen" class="parent-panel">
                  <!-- **入力欄を先頭に置き、開いた直後にフォーカスを当てる。**
                       開いてから探す場所を探させない -->
                  <input
                    ref="parentSearchRef"
                    v-model="parentQuery"
                    type="text"
                    class="parent-search"
                    :placeholder="$ui('番号かタイトルで絞り込む')"
                    :aria-label="$ui('親チケットを絞り込む')"
                    @keydown.escape="parentPickerOpen = false"
                  />
                  <ul class="parent-list" role="listbox">
                    <li>
                      <button
                        type="button"
                        class="parent-option"
                        :class="{ current: !parentTicket }"
                        role="option"
                        :aria-selected="!parentTicket"
                        @click="pickParent(null)"
                      > {{ $ui('親なし') }} </button>
                    </li>
                    <li v-for="c in parentMatches" :key="c.seq">
                      <button
                        type="button"
                        class="parent-option"
                        :class="{ current: parentTicket?.seq === c.seq }"
                        role="option"
                        :aria-selected="parentTicket?.seq === c.seq"
                        @click="pickParent(c.seq)"
                      >
                        <code class="parent-seq">{{ projectKey }}-{{ c.seq }}</code>
                        {{ c.title }}
                      </button>
                    </li>
                    <!-- **0件でも黙って空にしない**（6.2） -->
                    <li v-if="parentMatches.length === 0" class="parent-empty"> {{ $ui('一致するチケットがありません') }} </li>
                  </ul>
                </div>
              </template>
              <span v-else-if="parentTicket">
                {{ projectKey }}-{{ parentTicket.seq }} {{ parentTicket.title }}
              </span>
              <span v-else class="muted">—</span>

              <!-- 親の詳細を同じペインで開く。親がエピックならエピック欄が出す -->
              <RouterLink
                v-if="parentTicket"
                class="jump"
                :to="`/p/${projectKey}/tickets/${parentTicket.seq}`"
                :aria-label="$ui('親チケット {value0}-{value1} を開く', { value0: projectKey, value1: parentTicket.seq })"
                >↗</RouterLink
              >
              <p v-if="parentErrorAt === 'parent' && errorFor('parent_seq')" class="field-error" role="alert">
                {{ errorFor('parent_seq') }}
              </p>
            </dd>
          </div>

          <div class="meta-item">
            <dt>{{ $ui('スプリント') }}</dt>
            <dd>
              <!-- **読み取り専用である**（5.5「スプリントは選べない」。pb-6）。
                   スプリントは「チケットにあらかじめ付ける属性」ではなく
                   「いまどの期間で消化しようとしているか」であり、決まるのは
                   オンステージ段でスプリントを開始した瞬間である（5.4）。
                   `PATCH` もこの欄を受け付けない（ApiDesign.md 9.5.2）。 -->
              <span>{{ ticket.sprint?.name ?? '—' }}</span>
            </dd>
          </div>

          <!-- エピック（5.5「エピック欄」。pb-14）。**保存するのは `parent_seq` だけ**で、
               親（エピック以外）が無いときだけ選べる。親があれば配下のツリーは親と一緒に
               エピックへ属するので、祖先のエピック（9.5.1 の `epic`）を出すだけにする。
               **エピック自身には出さない**——入れ子を画面から作らない。
               **2列ぶんを使う**——名前に「親チケットに従う」を添えると半分の幅では切れる -->
          <div v-if="ticket.type !== 'epic'" class="meta-item wide">
            <dt>{{ $ui('エピック') }}</dt>
            <dd class="epic-cell">
              <select
                v-if="canEdit && epicSelectable"
                :value="ticket.epic ? String(ticket.epic.seq) : ''"
                :disabled="busy"
                :aria-label="$ui('エピック')"
                @change="pickEpic(($event.target as HTMLSelectElement).value)"
              >
                <option value="">{{ $ui('なし') }}</option>
                <option v-for="e in epicChoices" :key="e.seq" :value="String(e.seq)">
                  {{ ticketTypeIcons.epic }} {{ projectKey }}-{{ e.seq }} {{ e.title }}
                </option>
              </select>
              <span v-else-if="ticket.epic">{{ ticketTypeIcons.epic }} {{ projectKey }}-{{ ticket.epic.seq }} {{ ticket.epic.title }}</span>
              <span v-else class="muted">{{ $ui('なし') }}</span>
              <span v-if="!epicSelectable" class="muted">{{ $ui('（親チケットに従う）') }}</span>
              <RouterLink
                v-if="ticket.epic"
                class="jump"
                :to="`/p/${projectKey}/tickets/${ticket.epic.seq}`"
                :aria-label="$ui('エピック {value0}-{value1} を開く', { value0: projectKey, value1: ticket.epic.seq })"
                >↗</RouterLink
              >
              <p v-if="parentErrorAt === 'epic' && errorFor('parent_seq')" class="field-error" role="alert">
                {{ errorFor('parent_seq') }}
              </p>
            </dd>
          </div>

          <!-- 実行モード（5.5「実行モード」。pb-65）。**このチケットにエージェントが
               手を出してよいか**を決める欄で、エージェントは着手前にこれを読む。
               **「未設定」を持たない**——列が `NOT NULL` で、`null` を送ると 422（9.5.2） -->
          <div class="meta-item wide">
            <dt>{{ $ui('実行モード') }}</dt>
            <dd>
              <select
                v-if="canEdit"
                :value="ticket.execution_mode"
                :disabled="busy"
                :aria-label="$ui('実行モード')"
                @change="
                  selectField(
                    { execution_mode: ($event.target as HTMLSelectElement).value as never },
                    'execution_mode',
                  )
                "
              >
                <option v-for="m in executionModeOptions" :key="m" :value="m">
                  {{ executionModeLabels[m] }}
                </option>
              </select>
              <span v-else>{{ executionModeLabels[ticket.execution_mode] }}</span>
              <p v-if="errorFor('execution_mode')" class="field-error" role="alert">
                {{ errorFor('execution_mode') }}
              </p>
            </dd>
          </div>

          <!-- Readiness（5.5「Readiness」。pb-45）。**いま着手してよいか**の信号で、
               パックの「2. 実行の前提」に理由と一緒に出る。**実行モードと違い
               未判定（`null`）を持つ**。理由が自由文なので2列ぶんを使う -->
          <div class="meta-item wide">
            <dt>Readiness</dt>
            <dd>
              <select
                v-if="canEdit"
                :value="ticket.readiness ?? ''"
                :disabled="busy"
                aria-label="Readiness"
                @change="
                  selectField(
                    { readiness: (($event.target as HTMLSelectElement).value || null) as never },
                    'readiness',
                  )
                "
              >
                <option value="">{{ $ui('未判定') }}</option>
                <option v-for="r in readinessOptions" :key="r" :value="r">
                  {{ readinessLabels[r] }}
                </option>
              </select>
              <span v-else>{{ ticket.readiness ? readinessLabels[ticket.readiness] : $ui("未判定") }}</span>
              <p v-if="errorFor('readiness')" class="field-error" role="alert">
                {{ errorFor('readiness') }}
              </p>
              <div class="readiness-note">
                <input
                  v-if="editing === 'readiness_note'"
                  ref="inputRef"
                  v-model="draft"
                  type="text"
                  :aria-label="$ui('Readiness の理由')"
                  @keydown.escape="cancelEdit"
                  @keydown.enter="onEnterCommit($event, commitEdit)"
                  @blur="commitEdit"
                />
                <button
                  v-else
                  type="button"
                  class="value-view"
                  :disabled="!canEdit"
                  :title="canEdit ? $ui('クリックして理由を編集') : ''"
                  @click="startEdit('readiness_note')"
                >
                  <span v-if="ticket.readiness_note">{{ ticket.readiness_note }}</span>
                  <span v-else class="muted">{{ $ui('理由なし') }}</span>
                </button>
              </div>
              <p v-if="errorFor('readiness_note')" class="field-error" role="alert">
                {{ errorFor('readiness_note') }}
              </p>
            </dd>
          </div>

          <!-- タグは2列ぶんを使う。数が読めないので1列に押し込むと折り返しが荒れる -->
          <div class="meta-item wide">
            <dt>{{ $ui('タグ') }}</dt>
            <dd class="tag-cell">
              <span v-for="t in ticket.tags" :key="t.id" class="tag">
                {{ t.name }}
                <button
                  v-if="canEdit"
                  type="button"
                  class="tag-remove"
                  :disabled="busy"
                  :aria-label="$ui('タグ {value0} を外す', { value0: t.name })"
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
                  :title="unusedTags.length === 0 ? $ui('付けられるタグがありません') : $ui('タグを付ける')"
                  :aria-label="$ui('タグを付ける')"
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
            <dt>{{ $ui('見積') }}</dt>
            <dd>
              <input
                v-if="editing === 'estimate_point'"
                ref="inputRef"
                v-model="draft"
                type="number"
                min="0"
                step="0.5"
                :aria-label="$ui('見積（ポイント）')"
                @keydown.escape="cancelEdit"
                @keydown.enter="onEnterCommit($event, commitEdit)"
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
            <dt>{{ $ui('見積（時間）') }}</dt>
            <dd>
              <input
                v-if="editing === 'estimate_hours'"
                ref="inputRef"
                v-model="draft"
                type="number"
                min="0"
                step="0.5"
                :aria-label="$ui('見積（時間）')"
                @keydown.escape="cancelEdit"
                @keydown.enter="onEnterCommit($event, commitEdit)"
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
            <dt>{{ $ui('実績') }}</dt>
            <dd>
              <input
                v-if="editing === 'actual_hours'"
                ref="inputRef"
                v-model="draft"
                type="number"
                min="0"
                step="0.5"
                :aria-label="$ui('実績（時間）')"
                @keydown.escape="cancelEdit"
                @keydown.enter="onEnterCommit($event, commitEdit)"
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
            <dt>{{ $ui('開始') }}</dt>
            <dd>
              <!-- **`date` 列であって時刻を持たない**（9.2.2）。`input[type=date]` の
                   値がそのまま `YYYY-MM-DD` なので、`new Date()` を通さない -->
              <input
                v-if="editing === 'start_date'"
                ref="inputRef"
                v-model="draft"
                type="date"
                :aria-label="$ui('開始日')"
                @keydown.escape="cancelEdit"
                @keydown.enter="onEnterCommit($event, commitEdit)"
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
            <dt>{{ $ui('期限') }}</dt>
            <dd>
              <input
                v-if="editing === 'due_date'"
                ref="inputRef"
                v-model="draft"
                type="date"
                :aria-label="$ui('期限')"
                @keydown.escape="cancelEdit"
                @keydown.enter="onEnterCommit($event, commitEdit)"
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
          <h3 class="block-title">{{ $ui('説明') }}</h3>
          <template v-if="editing === 'body_md'">
            <MarkdownEditor ref="editorRef" v-model="draft" @cancel="cancelEdit" />
            <div class="block-actions">
              <button type="button" class="secondary" :disabled="busy" @click="cancelEdit"> {{ $ui('取消') }} </button>
              <button type="button" class="primary" :disabled="busy" @click="commitEdit"> {{ $ui('保存') }} </button>
            </div>
          </template>
          <template v-else>
            <button
              type="button"
              class="body-view"
              :class="{ readonly: !canEdit }"
              :disabled="!canEdit"
              :title="canEdit ? $ui('クリックして説明を編集') : ''"
              @click="startEdit('body_md')"
            >
              <!-- eslint-disable-next-line vue/no-v-html -- lib/markdown.ts の dompurify を通っている -->
              <span v-if="body" class="markdown-body" v-html="body"></span>
              <span v-else class="muted">{{ $ui('説明はまだありません') }}</span>
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
            <h3 class="block-title">{{ $ui('子チケット (') }}{{ ticket.children.length }})</h3>
            <!-- **見出し右の追加**（5.5）。0件だとこのセクションごと消えるので、
                 最初の1件は `[⋯]` から作る -->
            <button
              v-if="canCreate"
              type="button"
              class="block-add"
              @click="openNewChild"
            > {{ $ui('+ 追加') }} </button>
          </div>
          <ul class="children">
            <li v-for="c in ticket.children" :key="c.seq" class="child-row">
              <!-- **行クリックでその子の詳細を開く**（同じペインが差し替わる）。
                   **担当のセルはリンクの外に出す**（pb-60）——`<select>` をリンクの
                   中に置くと、開こうとしただけで子の詳細へ飛ぶ -->
              <RouterLink class="child" :to="`/p/${projectKey}/tickets/${c.seq}`">
                <span class="type-icon" :title="ticketTypeLabels[c.type]" aria-hidden="true">
                  {{ ticketTypeIcons[c.type] }}
                </span>
                <code class="child-id">{{ projectKey }}-{{ c.seq }}</code>
                <span class="child-title">{{ c.title }}</span>
                <span class="child-status">{{ c.status.name }}</span>
              </RouterLink>
              <span class="child-assignee">
                <!-- 選択肢は親自身の担当欄と同じ語彙（プロジェクトのメンバー＋未割当） -->
                <select
                  v-if="canEdit"
                  :value="c.assignee?.id ?? ''"
                  :disabled="busy"
                  :aria-label="$ui('{value0}-{value1} の担当', { value0: projectKey, value1: c.seq })"
                  @change="
                    setChildAssignee(
                      c.seq,
                      ($event.target as HTMLSelectElement).value || null,
                    )
                  "
                >
                  <option value="">{{ $ui('未割当') }}</option>
                  <!-- **メンバーの識別子は `actor_id`**（`id` ではない） -->
                  <option v-for="m in members" :key="m.actor_id" :value="m.actor_id">
                    {{ actorMark(m.kind) }} {{ m.display_name }}
                  </option>
                </select>
                <template v-else-if="c.assignee">
                  <span aria-hidden="true">{{ actorMark(c.assignee.kind) }}</span>
                  {{ c.assignee.display_name }}
                </template>
                <span v-else class="muted">—</span>
              </span>
            </li>
          </ul>
        </section>

        <!-- コード（5.5「コードと参考リンク」）。**画面に追加を置かない**——
             この欄はエージェントの作業記録で、人が手で書くものではない。
             **0件なら見出しごと出さない**（何もできない空の箱になるため） -->
        <section v-if="codeRefs.length > 0" class="block">
          <h3 class="block-title">{{ $ui('コード') }}</h3>
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
              > {{ $ui('削除') }} </button>
            </li>
          </ul>
        </section>

        <!-- 参考リンク（5.5）。**0件でも常に出す**——`[+ 追加]` がこのセクションへの
             唯一の入口であり、隠すと機能へ到達できない -->
        <section class="block">
          <div class="block-head">
            <h3 class="block-title">{{ $ui('参考リンク') }}</h3>
            <button
              v-if="canEdit"
              type="button"
              class="block-add"
              @click="openNewReference"
            > {{ $ui('+ 追加') }} </button>
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
                <button type="button" class="ref-action" @click="openEditReference(r)"> {{ $ui('編集') }} </button>
                <button type="button" class="ref-action ref-danger" @click="refToDelete = r"> {{ $ui('削除') }} </button>
              </template>
            </li>
          </ul>
          <p v-else class="ref-empty">{{ $ui('参考リンクはまだありません') }}</p>
          <p v-if="errorFor('references')" class="field-error" role="alert">
            {{ errorFor('references') }}
          </p>
        </section>

        <!-- スコープ境界（5.5「スコープ境界」。pb-45）。**空（`{}`）でも見出しを出す**——
             ここから入力できるので、隠すと設定できることに気づけない。
             **知らないキーは落とさず、読み取り専用で出す**（`withScopeLines` が丸ごと写す） -->
        <section class="block">
          <h3 class="block-title">{{ $ui('スコープ境界') }}</h3>
          <dl class="scope-list">
            <div v-for="s in scopeKeys" :key="s.key" class="scope-row">
              <dt>{{ s.label }}</dt>
              <dd>
                <template v-if="editing === `scope.${s.key}`">
                  <textarea
                    ref="scopeRef"
                    v-model="draft"
                    class="scope-input"
                    rows="4"
                    :placeholder="$ui('1行に1件')"
                    :aria-label="s.label"
                    @keydown.escape="cancelEdit"
                  ></textarea>
                  <div class="block-actions">
                    <button type="button" class="secondary" :disabled="busy" @click="cancelEdit"> {{ $ui('取消') }} </button>
                    <button type="button" class="primary" :disabled="busy" @click="commitEdit"> {{ $ui('保存') }} </button>
                  </div>
                </template>
                <button
                  v-else
                  type="button"
                  class="scope-view"
                  :class="{ readonly: !canEdit }"
                  :disabled="!canEdit"
                  :title="canEdit ? $ui('クリックして{value0}を編集', { value0: s.label }) : ''"
                  @click="startEdit(`scope.${s.key}`)"
                >
                  <template v-if="scopeText(ticket.scope, s.key) !== ''">
                    <code
                      v-for="(line, i) in scopeText(ticket.scope, s.key).split('\n')"
                      :key="i"
                      class="scope-item"
                      >{{ line }}</code
                    >
                  </template>
                  <span v-else class="muted">{{ $ui('なし') }}</span>
                </button>
                <p v-if="errorFor(`scope.${s.key}`)" class="field-error" role="alert">
                  {{ errorFor(`scope.${s.key}`) }}
                </p>
              </dd>
            </div>
            <div v-for="[k, v] in unknownScopeEntries(ticket.scope)" :key="k" class="scope-row">
              <dt>{{ k }}</dt>
              <dd>
                <code class="scope-item">{{ v }}</code>
                <span class="muted">{{ $ui('（画面からは編集できない）') }}</span>
              </dd>
            </div>
          </dl>
        </section>

        <!-- 完了条件（5.5「完了条件（DoD）」）。**0件でも見出しを出す**——
             追加の入口がこのセクションの中にあり、隠すと到達できない -->
        <section class="block">
          <h3 class="block-title">{{ $ui('完了条件 (DoD)') }}</h3>
          <ul v-if="dodItems.length > 0" class="dod-list">
            <li v-for="d in dodItems" :key="d.id" class="dod-row">
              <!-- **付け外した時点で `PATCH`**（選択式と同じ扱い。5.5） -->
              <input
                :id="`dod-${d.id}`"
                type="checkbox"
                class="dod-check"
                :checked="d.is_satisfied"
                :disabled="!canEdit || busy"
                :aria-label="$ui('完了条件「{value0}」を満たした', { value0: d.body })"
                @change="toggleDoD(d, ($event.target as HTMLInputElement).checked)"
              />

              <!-- 本文をクリックするとその場で編集に入る（「編集の単位」） -->
              <template v-if="dodEditingId === d.id">
                <input
                  v-model="dodDraft"
                  type="text"
                  class="dod-input"
                  :aria-label="$ui('完了条件の本文')"
                  :disabled="busy"
                  @keydown.enter="onEnterCommit($event, commitDoDEdit)"
                  @keydown.escape="cancelDoDEdit"
                  @blur="commitDoDEdit"
                />
              </template>
              <button
                v-else-if="canEdit"
                type="button"
                class="dod-body editable"
                :class="{ satisfied: d.is_satisfied }"
                @click="startDoDEdit(d)"
              >
                {{ d.body }}
              </button>
              <span v-else class="dod-body" :class="{ satisfied: d.is_satisfied }">
                {{ d.body }}
              </span>

              <button
                v-if="canEdit"
                type="button"
                class="ref-action ref-danger"
                @click="dodToDelete = d"
              > {{ $ui('削除') }} </button>
            </li>
          </ul>
          <p v-else class="ref-empty">{{ $ui('完了条件はまだありません') }}</p>

          <!-- **追加はモーダルにしない**（5.5）。列挙するときは続けて何件も打つ -->
          <form v-if="canEdit" class="dod-add" @submit.prevent="addDoD">
            <input
              v-model="newDoD"
              type="text"
              class="dod-input"
              :placeholder="$ui('完了条件を追加…')"
              :aria-label="$ui('完了条件を追加')"
              :disabled="busy"
            />
            <button type="submit" class="secondary" :disabled="newDoD.trim() === '' || busy"> {{ $ui('追加') }} </button>
          </form>
          <p v-if="dodError" class="field-error" role="alert">{{ dodError }}</p>
        </section>

        <!-- 関連チケット（5.5「関連チケット」）。**双方向を1つのリストに混ぜる**
             ——サーバが `outgoing` と `incoming` の両方を1本で返す（9.10.1） -->
        <section class="block">
          <div class="block-head">
            <h3 class="block-title">{{ $ui('関連チケット') }}</h3>
            <button v-if="canEdit" type="button" class="block-add" @click="openLinkModal"> {{ $ui('+ 追加') }} </button>
          </div>
          <ul v-if="links.length > 0" class="rel-list">
            <li v-for="l in links" :key="l.id" class="rel-row">
              <!-- **`blocks` だけ主語を書く**（5.5）。行に出ているのは常に相手なので、
                   「先行」だけだとその行のチケットが先行だと読める -->
              <span
                class="rel-kind"
                :title="linkLabelTitle(l.link_type, l.direction)"
              >
                {{ linkLabel(l.link_type, l.direction) }}
              </span>
              <!-- 行クリックでその相手の詳細を開く（子チケットと同じ） -->
              <RouterLink class="rel-main" :to="`/p/${projectKey}/tickets/${l.ticket.seq}`">
                <span
                  class="type-icon"
                  :title="ticketTypeLabels[l.ticket.type]"
                  aria-hidden="true"
                >
                  {{ ticketTypeIcons[l.ticket.type] }}
                </span>
                <code class="child-id">{{ projectKey }}-{{ l.ticket.seq }}</code>
                <span class="child-title">{{ l.ticket.title }}</span>
                <span class="child-status">{{ l.ticket.status.name }}</span>
              </RouterLink>
              <button
                v-if="canEdit"
                type="button"
                class="ref-action ref-danger"
                @click="linkToDelete = l"
              > {{ $ui('削除') }} </button>
            </li>
          </ul>
          <p v-else class="ref-empty">{{ $ui('関連チケットはまだありません') }}</p>
          <p v-if="linkError" class="field-error" role="alert">{{ linkError }}</p>
        </section>

        <!-- コメント（5.5「コメント」）。**見出しの数は `comment_count`**
             （`deleted_at IS NULL`）で、一覧の `total` ではない（9.8） -->
        <section class="block">
          <h3 class="block-title">{{ $ui('コメント (') }}{{ ticket.comment_count }})</h3>
          <TicketComments
            :project-key="projectKey"
            :seq="ticket.seq"
            @count-delta="onCommentCountDelta"
          />
        </section>

        <!-- 履歴（5.5「履歴」）。**件数を見出しに出さない**——開くまで
             読み込まないので、畳んだ状態では数を知らない。数を出すために
             起動時に1本足すのは、このセクションを遅延にした判断と矛盾する -->
        <section class="block">
          <TicketActivity
            :project-key="projectKey"
            :seq="ticket.seq"
            :workflow="workflow"
            :members="members"
          >
            <template #title><h3 class="block-title bare">{{ $ui('履歴') }}</h3></template>
          </TicketActivity>
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
      :title="refToDelete.kind === 'code' ? $ui('コードを削除しますか？') : $ui('参考リンクを削除しますか？')"
      :message="deleteReferenceMessage"
      :confirm-label="$ui('削除する')"
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
      :candidates="childParentCandidates"
      :epics="childEpicCandidates"
      :defaults="newChildDefaults"
      lock-parent
      :busy="busy"
      :field-errors="newChildErrors"
      @close="showNewChild = false"
      @save="createChild"
    />

    <ConfirmDialog
      v-if="confirmDelete"
      :title="$ui('チケットを削除しますか？')"
      :message="deleteMessage"
      :confirm-label="$ui('削除する')"
      danger
      :busy="busy"
      @cancel="confirmDelete = false"
      @confirm="runDelete"
    />

    <TicketLinkModal
      v-if="showLinkModal"
      :project-key="projectKey"
      :candidates="linkCandidates"
      :busy="busy"
      :field-errors="linkFieldErrors"
      :form-error="linkFormError"
      @close="showLinkModal = false"
      @save="saveLink"
    />

    <ConfirmDialog
      v-if="dodToDelete"
      :title="$ui('完了条件を削除しますか？')"
      :message="$ui('「{value0}」を削除します。元に戻せません。', { value0: dodToDelete.body })"
      :confirm-label="$ui('削除する')"
      danger
      :busy="busy"
      @cancel="dodToDelete = null"
      @confirm="runDeleteDoD"
    />

    <ConfirmDialog
      v-if="linkToDelete"
      :title="$ui('関連を解除しますか？')"
      :message="deleteLinkMessage"
      :confirm-label="$ui('解除する')"
      danger
      :busy="busy"
      @cancel="linkToDelete = null"
      @confirm="runDeleteLink"
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
  /* **ID は縮まない**（5.4「ID列」は完全形を出すと定めている）。
     余りを取って省略記号で切れるのはタイトルのほうである（pb-10） */
  flex: none;
  font-size: 15px;
  font-weight: 600;
  white-space: nowrap;
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

/* ── タイトル（ヘッダ段。5.5。pb-10）─────────────────────── */

/* 押せる領域だが、読むときはただの見出しに見せる（クリックで編集に入る）。
   **余りを取り、長いタイトルは省略記号で切る**——48px の段に収めるので
   折り返さない。全文は `title` 属性と、編集に入ったときの入力欄が出す */
.title-view {
  flex: 1;
  min-width: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font-size: 15px;
  font-weight: 600;
  line-height: 1.5;
  text-align: left;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  cursor: text;
}

.title-view:hover:not(:disabled) {
  border-color: var(--pb-border);
}

.title-view.readonly {
  cursor: default;
}

.title-input {
  flex: 1;
  min-width: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-focus);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  font-size: 15px;
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

/* ── 親の選択（5.5。pb-48）─────────────────────────────────
   **絞り込みの一覧は、そのセルの中に絶対配置で開く。** `<Teleport>` を使わない
   ——このペインは自分でスクロールするので、body へ出すと**スクロールに追随せず
   置き去りになる**（StatusDropdown は fixed で追随を自前で持っている） */
.parent-cell {
  position: relative;
}

.parent-trigger {
  display: flex;
  flex: 1;
  min-width: 0;
  align-items: center;
  gap: var(--pb-space-1);
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.parent-trigger:disabled {
  cursor: default;
  opacity: 0.6;
}

/* **1行に収める**（5.5 と同じ規則）。長いタイトルは省略記号で切る */
.parent-current {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.parent-panel {
  position: absolute;
  z-index: 20;
  top: calc(100% + 2px);
  left: 0;
  width: 100%;
  min-width: 260px;
  padding: var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  box-shadow: var(--pb-shadow-2);
}

.parent-search {
  width: 100%;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  font-size: 13px;
}

/* **一覧側だけスクロールさせる。** 入力欄は常に見えている */
.parent-list {
  max-height: 240px;
  margin-top: var(--pb-space-2);
  overflow-y: auto;
  list-style: none;
}

.parent-option {
  display: block;
  width: 100%;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: none;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font-size: 13px;
  text-align: left;
  cursor: pointer;
}

.parent-option:hover {
  background: var(--pb-hover);
}

/* いま選ばれているものは面の輝度で示す（色を使わない。8.2 / 8.6） */
.parent-option.current {
  background: var(--pb-active);
}

.parent-seq {
  margin-right: var(--pb-space-1);
  color: var(--pb-text-muted);
}

.parent-empty {
  padding: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.parent-cell .jump,
.epic-cell .jump {
  flex: none;
  color: var(--pb-text-muted);
}

.parent-cell .jump:hover,
.epic-cell .jump:hover {
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

/* 下線を子側（`TicketActivity` の `.ta-head`）が引くとき用。
   **`.block-head .block-title` と同じ効果を、親を選べない場所で得る**
   ——スロットの中身は親のスコープでコンパイルされるので、子の
   `scoped` セレクタからは `:slotted` 無しに届かない */
.block-title.bare {
  padding-bottom: 0;
  margin-bottom: 0;
  border-bottom: none;
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

/* ── Readiness の理由とスコープ境界（5.5。pb-45）──────────────── */

/* 理由は値の下の行に回す。値と同じ行に並べると、自由文が選択肢を押し縮める */
.readiness-note {
  display: flex;
  flex: 1 1 100%;
  min-width: 0;
}

.scope-list {
  display: grid;
  gap: var(--pb-space-2);
  margin: 0;
}

.scope-row dt {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.scope-row dd {
  margin: 0;
}

/* **1件を1行に出す。** 横に流すと、空白を含む1件と2件の区別がつかない
   （`client/src/ docs/…` が1つのパスに読める。実機で見た） */
.scope-view {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  width: 100%;
  min-height: 28px;
  padding: var(--pb-space-1);
  border: 1px solid transparent;
  border-radius: var(--pb-radius);
  background: none;
  color: inherit;
  font: inherit;
  font-size: 13px;
  text-align: left;
  cursor: text;
}

.scope-view:hover:not(:disabled) {
  border-color: var(--pb-border);
}

.scope-view.readonly {
  cursor: default;
}

.scope-item {
  overflow-wrap: anywhere;
}

.scope-input {
  display: block;
  box-sizing: border-box;
  width: 100%;
  padding: var(--pb-space-1);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font-family: var(--pb-font-mono);
  font-size: 13px;
  resize: vertical;
}

/* ── 子チケット ───────────────────────────────────────────── */

.children {
  list-style: none;
}

/* **行は「リンク部分」と「担当のセル」の2つに割れている**（pb-60）。
   担当を変える操作がリンクの遷移と食い合わないようにするため */
.child-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
}

.child {
  display: flex;
  flex: 1;
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

/* ── 完了条件（5.5「完了条件（DoD）」）。手順18b ─────────────── */

.dod-list {
  list-style: none;
}

.dod-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border-radius: var(--pb-radius);
}

.dod-row:hover {
  background: var(--pb-hover);
}

.dod-check {
  flex: none;
  margin: 0;
}

/* **主役には `flex: 1 1 auto` と `min-width` を添える**（6.7）。
   添えないと `flex: none` のチェックと `[削除]` が残って本文が先に潰れる */
.dod-body {
  flex: 1 1 auto;
  min-width: 6em;
  padding: 0;
  border: none;
  background: none;
  color: inherit;
  font: inherit;
  font-size: 13px;
  text-align: left;
  /* **折り返す。** 完了条件は文であって識別子ではないので、省略すると読めない */
  overflow-wrap: anywhere;
}

.dod-body.editable {
  cursor: text;
}

.dod-body.editable:hover {
  text-decoration: underline dotted;
}

/* 満たしたものは淡くする。**取り消し線は引かない**——条件は「消えた」のではなく
   「満たされた」であり、後から読み返す対象として残る */
.dod-body.satisfied {
  color: var(--pb-text-muted);
}

.dod-input {
  flex: 1 1 auto;
  min-width: 6em;
  height: 30px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
}

.dod-add {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  margin-top: var(--pb-space-2);
  /* 一覧の行と左端をそろえる（行は `padding` を持つ） */
  padding: 0 var(--pb-space-2);
}

/* ── 関連チケット（5.5「関連チケット」）。手順18b ─────────────── */

.rel-list {
  list-style: none;
}

.rel-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border-radius: var(--pb-radius);
}

.rel-row:hover {
  background: var(--pb-hover);
}

/* 関係のチップ。**枠線＋文字で表す**（8.6。有彩色を使わない）。
   **幅をそろえる**——4種のラベルが2〜3文字で、そろえないと ID の列が段違いになる */
.rel-kind {
  flex: none;
  min-width: 3.6em;
  padding: 1px var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: 999px;
  color: var(--pb-text-muted);
  font-size: 11px;
  text-align: center;
  white-space: nowrap;
  cursor: help;
}

/* 行の本体。**`.child-*` の見た目をそのまま使う**（子チケットと同じ並びなので、
   別々に書くと同じものが2通りに見える） */
.rel-main {
  display: flex;
  flex: 1 1 auto;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 8em;
  overflow: hidden;
  color: inherit;
  text-decoration: none;
}

.rel-main:hover .child-title {
  text-decoration: underline;
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
