<script setup lang="ts">
/**
 * プロジェクト設定（`GuiDesign.md` 5.9）。必要権限は `project.edit`。
 *
 * タブは「一般」「メンバー」「タグ」「スプリント」の4つ。**タブはURLを持たない**
 * （3.2 のルーティング表が持つのは `/p/:key/settings` の1行だけである）。
 *
 * 一般とメンバーは `GET /projects/:key` の1本で足りる（5.4）。ワークフローも
 * メンバーも自分の実効権限も同じ応答に入っている。**タグとスプリントは別の
 * エンドポイント**（`ApiDesign.md` 9.11 / 9.12）で、**そのタブを最初に開いた
 * ときに取りに行く**——一般タブしか見ない利用者に、使わない2本を払わせない。
 *
 * **操作の結果はトーストではなく、操作した場所に出す**（6.4）。タグとスプリントは
 * それぞれのタブの見出しの下に1つ欄を持ち、追加・改名・削除の結果を使い回す。
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import PageHeader from '../components/PageHeader.vue'
import RepositoryModal from '../components/RepositoryModal.vue'
import SprintModal from '../components/SprintModal.vue'
import { ApiError } from '../api/client'
import * as projectsApi from '../api/projects'
import type { ProjectDetail, ProjectRepository, UpdateProjectRequest } from '../api/projects'
import * as tagsApi from '../api/tags'
import type { Tag } from '../api/tags'
import * as sprintsApi from '../api/sprints'
import { sprintStatusLabels } from '../api/sprints'
import type { CreateSprintRequest, Sprint } from '../api/sprints'
import { formatDate, formatPlainDate } from '../lib/datetime'
import { isWebUrl } from '../lib/url'
import { useRolesStore } from '../stores/roles'
import { useAuthStore } from '../stores/auth'
import { useProjectStore } from '../stores/project'

/** 入力の上限（`ApiDesign.md` 5.3 の検証表と `GuiDesign.md` 5.9.1）。正本はサーバ側 */
const MAX_NAME = 100
const MAX_DESCRIPTION = 1000
/** リポジトリの上限（5.9.1）。1件ごとの検証は `RepositoryModal` が持つ */
const MAX_REPOSITORIES = 10

const auth = useAuthStore()
const store = useProjectStore()
const route = useRoute()
const router = useRouter()

type Tab = 'general' | 'members' | 'tags' | 'sprints'
const tab = ref<Tab>('general')

const projectKey = computed(() => {
  const k = route.params.key
  return typeof k === 'string' ? k : ''
})

// ── 入力（サーバの値から作り、保存のたびに作り直す）──────────────────
const name = ref('')
const description = ref('')
const repositories = ref<ProjectRepository[]>([])

const saving = ref(false)
const saveError = ref<ApiError | null>(null)
const saved = ref(false)

const confirmOpen = ref(false)
const archiving = ref(false)
const archiveError = ref<ApiError | null>(null)

function resetForm(p: ProjectDetail): void {
  name.value = p.name
  description.value = p.description ?? ''
  repositories.value = projectsApi.readRepositories(p.settings)
}

/** 想定外の例外も 2.5 の形に揃える。画面は `ApiError` だけを扱えばよくなる */
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
  await store.fetchCurrent(projectKey.value)

  // ルーターガードと同じ規則で倒す（7.2）。ガードを通った後に権限が変わった、
  // あるいはプロジェクトが消えた場合にここへ来る。
  const err = store.currentError
  if (err?.status === 404) {
    await router.replace('/404')
    return
  }
  if (err?.status === 403) {
    await router.replace('/403')
    return
  }
  if (store.current) resetForm(store.current)
}

onMounted(() => {
  void load()
  // ロールの表示名（メンバータブ）。**プロジェクト管理者でも読める範囲**だけを
  // 要求する（`ApiDesign.md` 7.1。`scope` 無しは `user.manage` が要る）。
  void rolesStore.ensureRoles('project')
})

// プロジェクト切替は「同じ画面種別を維持する」（4.4）ため、この画面のまま
// `:key` だけが変わる。コンポーネントは再生成されないので自分で取り直す。
watch(projectKey, () => {
  tab.value = 'general'
  saveError.value = null
  saved.value = false
  archiveError.value = null
  // **タグとスプリントは持ち越さない。** プロジェクトごとに違う値であり、
  // 取得済みフラグを残すと切替先で前のプロジェクトのタグが出る。
  resetTagsAndSprints()
  void load()
})

onUnmounted(() => store.clearCurrent())

// ── タグ（5.9.4。`ApiDesign.md` 9.11）────────────────────────────

/** プロジェクト切替時に、タグとスプリントの状態を捨てる */
function resetTagsAndSprints(): void {
  tags.value = []
  tagsLoaded.value = false
  tagsError.value = null
  tagResult.value = ''
  newTagName.value = null
  renamingTagId.value = null
  deletingTag.value = null

  sprints.value = []
  sprintsLoaded.value = false
  sprintsError.value = null
  sprintResult.value = ''
  editingSprint.value = null
  deletingSprint.value = null
}

//
// **一覧を1つ持ち、操作のたびに取り直さない。** 応答が1件分返るので手元を
// 差し替えれば足り、往復を1回減らせる。並べ替えだけは複数行が動くので
// 取り直す（原子的でないため、失敗したときにサーバの実際の順序へ戻す）。

const tags = ref<Tag[]>([])
const tagsLoaded = ref(false)
const tagsLoading = ref(false)
const tagsError = ref<ApiError | null>(null)
/** 追加・改名・削除・並べ替えの結果を1つの欄で使い回す（6.4） */
const tagResult = ref('')
const tagBusy = ref(false)

/** 追加行の入力。null なら行を出さない */
const newTagName = ref<string | null>(null)
const newTagError = ref<string | null>(null)

/** 改名中のタグ ID と入力値。null なら誰も編集していない */
const renamingTagId = ref<string | null>(null)
const renamingTagName = ref('')
const renameTagError = ref<string | null>(null)

/** 削除確認の対象 */
const deletingTag = ref<Tag | null>(null)

/** ドラッグ中のタグ ID（`⠿` の並べ替え） */
const draggingTagId = ref<string | null>(null)

async function loadTags(): Promise<void> {
  tagsLoading.value = true
  tagsError.value = null
  try {
    tags.value = (await tagsApi.listTags(projectKey.value)).items
    tagsLoaded.value = true
  } catch (e) {
    tagsError.value = toApiError(e)
  } finally {
    tagsLoading.value = false
  }
}

function startAddTag(): void {
  newTagName.value = ''
  newTagError.value = null
  tagResult.value = ''
}

async function submitNewTag(): Promise<void> {
  const name = (newTagName.value ?? '').trim()
  if (name === '') {
    newTagError.value = 'タグ名を入力してください'
    return
  }
  tagBusy.value = true
  newTagError.value = null
  try {
    const created = await tagsApi.createTag(projectKey.value, { name })
    // 応答は1件。並び（sort_order 昇順・同値は name 昇順）を手元でも保つ。
    tags.value = [...tags.value, created].sort(compareTags)
    newTagName.value = null
    tagResult.value = `✓ タグ「${created.name}」を追加しました`
  } catch (e) {
    const err = toApiError(e)
    newTagError.value = err.message
  } finally {
    tagBusy.value = false
  }
}

function startRenameTag(tag: Tag): void {
  renamingTagId.value = tag.id
  renamingTagName.value = tag.name
  renameTagError.value = null
  tagResult.value = ''
}

async function submitRenameTag(tag: Tag): Promise<void> {
  const name = renamingTagName.value.trim()
  if (name === '') {
    renameTagError.value = 'タグ名を入力してください'
    return
  }
  if (name === tag.name) {
    renamingTagId.value = null
    return
  }
  tagBusy.value = true
  renameTagError.value = null
  try {
    const updated = await tagsApi.updateTag(projectKey.value, tag.id, { name })
    tags.value = tags.value.map((t) => (t.id === updated.id ? updated : t)).sort(compareTags)
    renamingTagId.value = null
    tagResult.value = `✓ タグ「${updated.name}」に変更しました`
  } catch (e) {
    renameTagError.value = toApiError(e).message
  } finally {
    tagBusy.value = false
  }
}

async function confirmDeleteTag(): Promise<void> {
  const tag = deletingTag.value
  if (tag === null) return
  tagBusy.value = true
  try {
    await tagsApi.deleteTag(projectKey.value, tag.id)
    tags.value = tags.value.filter((t) => t.id !== tag.id)
    deletingTag.value = null
    tagResult.value = `✓ タグ「${tag.name}」を削除しました`
  } catch (e) {
    tagsError.value = toApiError(e)
    deletingTag.value = null
  } finally {
    tagBusy.value = false
  }
}

/** 一覧の並び（`ApiDesign.md` 9.11）。サーバと同じ規則を手元でも使う */
function compareTags(a: Tag, b: Tag): number {
  if (a.sort_order !== b.sort_order) return a.sort_order - b.sort_order
  return a.name.localeCompare(b.name, 'ja')
}

/**
 * `⠿` のドラッグで並べ替える（5.9.4、`ApiDesign.md` 9.11.1）。
 *
 * **画面を先に動かし、サーバへは変わった行だけ送る。** 失敗したら一覧を
 * 取り直して戻す——この操作は原子的ではなく、途中まで反映された状態が
 * 実際に起こりうるためである。
 */
async function dropTag(targetId: string): Promise<void> {
  const sourceId = draggingTagId.value
  draggingTagId.value = null
  if (sourceId === null || sourceId === targetId) return

  const from = tags.value.findIndex((t) => t.id === sourceId)
  const to = tags.value.findIndex((t) => t.id === targetId)
  if (from < 0 || to < 0) return

  const next = [...tags.value]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  const before = tags.value
  tags.value = next

  tagBusy.value = true
  try {
    const sent = await tagsApi.reorderTags(projectKey.value, next)
    if (sent > 0) await loadTags()
    tagResult.value = '✓ 並び順を変更しました'
  } catch (e) {
    tags.value = before
    tagsError.value = toApiError(e)
    await loadTags()
  } finally {
    tagBusy.value = false
  }
}

// ── スプリント（5.9.5。`ApiDesign.md` 9.12）──────────────────────

const sprints = ref<Sprint[]>([])
const sprintsLoaded = ref(false)
const sprintsLoading = ref(false)
const sprintsError = ref<ApiError | null>(null)
const sprintResult = ref('')
const sprintBusy = ref(false)

/** モーダルの状態。`'new'` は追加、Sprint は編集、null は閉じている */
const editingSprint = ref<Sprint | 'new' | null>(null)
const sprintFieldErrors = ref<Record<string, string>>({})

/** 削除確認の対象 */
const deletingSprint = ref<Sprint | null>(null)

async function loadSprints(): Promise<void> {
  sprintsLoading.value = true
  sprintsError.value = null
  try {
    sprints.value = (await sprintsApi.listSprints(projectKey.value)).items
    sprintsLoaded.value = true
  } catch (e) {
    sprintsError.value = toApiError(e)
  } finally {
    sprintsLoading.value = false
  }
}

function openSprintModal(target: Sprint | 'new'): void {
  editingSprint.value = target
  sprintFieldErrors.value = {}
  sprintResult.value = ''
}

async function saveSprint(body: CreateSprintRequest): Promise<void> {
  const target = editingSprint.value
  if (target === null) return

  sprintBusy.value = true
  sprintFieldErrors.value = {}
  try {
    if (target === 'new') {
      await sprintsApi.createSprint(projectKey.value, body)
      sprintResult.value = `✓ スプリント「${body.name}」を追加しました`
    } else {
      await sprintsApi.updateSprint(projectKey.value, target.id, body)
      sprintResult.value = `✓ スプリント「${body.name}」を更新しました`
    }
    editingSprint.value = null
    // 並びが start_date に依るので、作成・更新のたびに取り直す。
    // 1件分の応答を差し込むだけでは、日付を変えたときに位置がずれる。
    await loadSprints()
  } catch (e) {
    const err = toApiError(e)
    if (err.status === 422 && err.details) {
      const map: Record<string, string> = {}
      for (const d of err.details) {
        if (d.field) map[d.field] = d.message
      }
      sprintFieldErrors.value = map
    } else {
      sprintsError.value = err
      editingSprint.value = null
    }
  } finally {
    sprintBusy.value = false
  }
}

async function confirmDeleteSprint(): Promise<void> {
  const sprint = deletingSprint.value
  if (sprint === null) return
  sprintBusy.value = true
  try {
    await sprintsApi.deleteSprint(projectKey.value, sprint.id)
    sprints.value = sprints.value.filter((s) => s.id !== sprint.id)
    deletingSprint.value = null
    sprintResult.value = `✓ スプリント「${sprint.name}」を削除しました`
  } catch (e) {
    sprintsError.value = toApiError(e)
    deletingSprint.value = null
  } finally {
    sprintBusy.value = false
  }
}

/**
 * 期間の表示（5.9.5 の `8/05 — 8/18`）。片方だけでも読める形にする。
 *
 * **`formatDate` ではなく `formatPlainDate` を使う。** `start_date` /
 * `end_date` は `date` 列で時刻を持たず、タイムゾーンの変換を通すと
 * UTC より西の地域で前日へずれる（`lib/datetime.ts`）。
 */
function sprintPeriod(s: Sprint): string {
  if (!s.start_date && !s.end_date) return '—'
  const from = s.start_date ? formatPlainDate(s.start_date) : '未定'
  const to = s.end_date ? formatPlainDate(s.end_date) : '未定'
  return `${from} — ${to}`
}

// ── タブの切り替えで初回だけ取りに行く ──────────────────────────
//
// 一般タブしか見ない利用者に、使わない2本を払わせない（5.9）。
watch(tab, (next) => {
  if (next === 'tags' && !tagsLoaded.value && !tagsLoading.value) void loadTags()
  if (next === 'sprints' && !sprintsLoaded.value && !sprintsLoading.value) void loadSprints()
})


// ── 変更の検出 ────────────────────────────────────────────────
const current = computed(() => store.current)

const nameChanged = computed(() => current.value !== null && name.value.trim() !== current.value.name)

const descriptionChanged = computed(
  () => current.value !== null && description.value.trim() !== (current.value.description ?? ''),
)

/**
 * リポジトリの変更判定。
 *
 * 保存するときと同じ整形（空行を落とし、空文字のキーを外す）を通してから
 * 比べる。そうしないと「空の行を足して消した」だけで変更ありになる。
 */
const repositoriesChanged = computed(() => {
  const p = current.value
  if (p === null) return false
  const next = projectsApi.mergeRepositories(p.settings, repositories.value)['repositories']
  return JSON.stringify(next) !== JSON.stringify(projectsApi.readRepositories(p.settings))
})

const dirty = computed(
  () => nameChanged.value || descriptionChanged.value || repositoriesChanged.value,
)

/** 入力し直したら前回の結果表示を消す。古い `✓ 保存しました` を残さない */
watch(dirty, (d) => {
  if (d) saved.value = false
})

// ── 入力の検証（サーバ側の検証が正本。ここは押す前に気づかせるためのもの）──
const nameError = computed(() => {
  const v = name.value.trim()
  if (v === '') return 'プロジェクト名を入力してください'
  if (v.length > MAX_NAME) return `${MAX_NAME}文字以内で入力してください`
  return null
})

/**
 * 保存してよい状態か。
 *
 * **リポジトリ1件ごとの検証は `RepositoryModal` が持つ**（5.9.1）。不正な行を
 * 一覧へ入れない作りにしてあるので、ここで行を見る必要がない。一覧側で弾くと、
 * 1行の不備が名前・説明の保存まで止める（手順11b の実機確認で起きた）。
 */
const valid = computed(
  () => nameError.value === null && description.value.trim().length <= MAX_DESCRIPTION,
)

const canSave = computed(() => dirty.value && valid.value && !saving.value)

// 入力欄に紐づくエラー（2.5 の details）。ログイン画面・作成モーダルと同じ扱い
const nameDetail = computed(() => saveError.value?.detailFor('name'))
const descriptionDetail = computed(() => saveError.value?.detailFor('description'))

/** 入力欄に紐づかないエラーだけを保存ボタンの近くに出す（同じ文言を二重に見せない） */
const generalSaveError = computed(() => {
  const e = saveError.value
  if (!e) return null
  if (e.detailFor('name') || e.detailFor('description')) return null
  return e.message
})

/** 409（2.8）。他の誰かが先に更新した。取り込むかどうかは利用者が決める */
const conflict = computed(() => saveError.value?.status === 409)

// ── 保存 ─────────────────────────────────────────────────────
async function save(): Promise<void> {
  const p = current.value
  if (p === null || !canSave.value) return

  saving.value = true
  saveError.value = null
  saved.value = false

  // 変更されたフィールドだけを送る（5.5 の部分更新）。
  const body: UpdateProjectRequest = {}
  if (nameChanged.value) body.name = name.value.trim()
  if (descriptionChanged.value) {
    // 空文字は「説明を消す」。5.5 は null と未送信を区別する
    const v = description.value.trim()
    body.description = v === '' ? null : v
  }
  if (repositoriesChanged.value) {
    // settings は丸ごと置き換わる。取得した値を土台にして
    // 画面が知らないキーを落とさない（5.5）
    body.settings = projectsApi.mergeRepositories(p.settings, repositories.value)
  }

  try {
    const res = await projectsApi.updateProject(p.key, p.version, body)
    store.setCurrent(res)
    resetForm(res)
    saved.value = true

    // メニューとプロジェクト切替（4.4）が出す名前は `GET /me` 由来である。
    // 取り直さないと、改名しても左のメニューは古い名前のままになる。
    if (body.name !== undefined) {
      try {
        await auth.refresh()
      } catch {
        // 表示が古いままになるだけ。保存そのものは済んでいる
      }
    }
  } catch (e: unknown) {
    saveError.value = toApiError(e)
  } finally {
    saving.value = false
  }
}

/** 409 のあと、サーバの最新を取り込む。入力中の内容は最新の値で置き換わる */
async function reload(): Promise<void> {
  saveError.value = null
  saved.value = false
  await load()
}

// ── リポジトリ（5.9.1）───────────────────────────────────────
//
// 一覧は表、追加と編集はモーダル。**確定しても画面上の一覧が変わるだけ**で、
// サーバへ送るのは一般タブの `[保存]` である。

/** 編集中の位置。`null` は閉じている、`-1` は新規追加 */
const editingIndex = ref<number | null>(null)
const editingDraft = ref<ProjectRepository>({ url: '' })

const isNewRepository = computed(() => editingIndex.value === -1)

function addRepository(): void {
  if (repositories.value.length >= MAX_REPOSITORIES) return
  editingDraft.value = { url: '' }
  editingIndex.value = -1
}

function editRepository(index: number): void {
  // 複製を渡す。キャンセルで元へ戻せるようにする
  editingDraft.value = { ...repositories.value[index]! }
  editingIndex.value = index
}

function applyRepository(next: ProjectRepository): void {
  if (editingIndex.value === null) return
  if (editingIndex.value === -1) repositories.value.push(next)
  else repositories.value[editingIndex.value] = next
  editingIndex.value = null
}

function removeRepository(index: number): void {
  repositories.value.splice(index, 1)
}

// ── アーカイブ（5.6 / 6.3）───────────────────────────────────
const isArchived = computed(() => current.value?.status === 'archived')

/**
 * `project.archive` を持つか。
 *
 * **`GET /projects/:key` が返した `my_permissions` を見る**（5.4）。auth ストアの
 * 写しではなくサーバがいま返した値なので、ここが最も新しい。
 */
const canArchive = computed(() => current.value?.my_permissions.includes('project.archive') ?? false)

/**
 * `agent.register` を持つか（手順28a）。
 *
 * **エージェント連携セットアップ（5.11）への導線を出し分ける。** この画面の
 * 必要権限は `project.edit` で、セットアップ側は `agent.register` である
 * ——**0010 ではどちらも `project_admin` だけが持つが、同じとは限らない**ので
 * 導線の側でも見る（`GuiDesign.md` 7.3）。
 */
const canSetupAgents = computed(
  () => current.value?.my_permissions.includes('agent.register') ?? false,
)

/** セットアップ画面（`GuiDesign.md` 5.11）。**タブではなく独立したルートである** */
const agentSetupPath = computed(() => `/p/${projectKey.value}/settings/agents`)

/**
 * アーカイブは確認を挟む（6.3）。**解除は挟まない。**
 * 6.3 が確認を求めているのはアーカイブであり、解除は失うものが無い。
 */
function requestArchiveToggle(): void {
  if (isArchived.value) {
    void runArchiveToggle()
    return
  }
  confirmOpen.value = true
}

async function runArchiveToggle(): Promise<void> {
  const p = current.value
  if (p === null) return

  archiving.value = true
  archiveError.value = null
  try {
    // API は冪等なので、二重送信しても状態は壊れない（5.6）
    const res = isArchived.value
      ? await projectsApi.unarchiveProject(p.key)
      : await projectsApi.archiveProject(p.key)
    // 応答は 5.4 と同形式。status と version が1往復で更新される
    store.setCurrent(res)
    resetForm(res)
    confirmOpen.value = false
  } catch (e: unknown) {
    archiveError.value = toApiError(e)
    confirmOpen.value = false
  } finally {
    archiving.value = false
  }
}

// ── メンバータブ（5.9.2）─────────────────────────────────────
//
// ロールの表示名は `GET /roles?scope=project` から取る（`ApiDesign.md` 7.1）。
// **この画面は `project.edit` で開ける**——プロジェクト管理者は `user.manage` を
// 持たないため、`scope` を指定せずに呼ぶと 403 になる。7.1 が `?scope=project`
// だけを開放しているのは、まさにこの画面のためである。
//
// 手順14 より前は `lib/roles.ts` が対応表の写しを持っていた（`GuiDesign.md` 5.6
// 「同じものは全画面で同じ表記にする」）。正本は `DbDesign.md` 7.3 のシード。
const rolesStore = useRolesStore()

function roleLabel(role: string): string {
  return rolesStore.roleLabel(role)
}

/** 人間とエージェントを同じ一覧に並べる（設計原則5、`DbDesign.md` 6.2） */
function kindIcon(kind: string): string {
  return kind === 'agent' ? '🤖' : kind === 'system' ? '⚙' : '👤'
}
</script>

<template>
  <div class="page">
    <PageHeader title="プロジェクト設定">
      <template #actions>
        <!-- **タブにしない**（5.9）。独立したルートで必要権限も違うため、
             タブ列に混ぜると「同じ画面の続き」に見える -->
        <RouterLink v-if="canSetupAgents" class="secondary setup-link" :to="agentSetupPath">
          エージェント連携セットアップ →
        </RouterLink>
      </template>
    </PageHeader>

    <div class="page-body">
      <!-- エラー（6.2）。原因はサーバが返した message をそのまま出す -->
      <EmptyState
        v-if="store.currentError && !store.current"
        title="プロジェクトを取得できませんでした"
        :description="store.currentError.message"
      >
        <template #action>
          <button type="button" class="primary" @click="load">再試行</button>
        </template>
      </EmptyState>

      <!-- 読み込み中はスケルトン。実際のブロックの形を模す（6.2） -->
      <div v-else-if="store.currentLoading && !store.current" class="blocks" aria-busy="true">
        <div v-for="n in 3" :key="n" class="block">
          <span class="skeleton title"></span>
          <span class="skeleton"></span>
          <span class="skeleton short"></span>
        </div>
      </div>

      <template v-else-if="store.current">
        <div class="tabs" role="tablist">
          <button
            type="button"
            role="tab"
            class="tab"
            :class="{ selected: tab === 'general' }"
            :aria-selected="tab === 'general'"
            @click="tab = 'general'"
          >
            一般
          </button>
          <button
            type="button"
            role="tab"
            class="tab"
            :class="{ selected: tab === 'members' }"
            :aria-selected="tab === 'members'"
            @click="tab = 'members'"
          >
            メンバー
          </button>
          <button
            type="button"
            role="tab"
            class="tab"
            :class="{ selected: tab === 'tags' }"
            :aria-selected="tab === 'tags'"
            @click="tab = 'tags'"
          >
            タグ
          </button>
          <button
            type="button"
            role="tab"
            class="tab"
            :class="{ selected: tab === 'sprints' }"
            :aria-selected="tab === 'sprints'"
            @click="tab = 'sprints'"
          >
            スプリント
          </button>
        </div>

        <!-- ── 一般タブ（5.9.1）──────────────────────────────── -->
        <div v-if="tab === 'general'" class="blocks" role="tabpanel">
          <form class="block" @submit.prevent="save">
            <h2 class="block-title">基本情報</h2>

            <!-- 名前が先頭。キーは「一意に指すための識別子」であって
                 プロジェクトの一属性にすぎない（5.9.1） -->
            <label class="field">
              <span class="label">プロジェクト名 <span class="required">*</span></span>
              <input
                v-model="name"
                type="text"
                name="name"
                :maxlength="MAX_NAME"
                :aria-invalid="nameDetail !== undefined || nameError !== null"
                :disabled="saving"
              />
              <span v-if="nameDetail" class="detail">{{ nameDetail.message }}</span>
              <span v-else-if="nameError && dirty" class="detail">✕ {{ nameError }}</span>
            </label>

            <div class="field">
              <span class="label">プロジェクトキー</span>
              <p class="static-value">{{ store.current.key }}</p>
              <!-- warning は「面」で表す（8.4.1）。理由は 5.2.1 にある -->
              <p class="warn">⚠ キーは変更できません</p>
            </div>

            <label class="field">
              <span class="label">説明</span>
              <textarea
                v-model="description"
                name="description"
                rows="3"
                :maxlength="MAX_DESCRIPTION"
                :aria-invalid="descriptionDetail !== undefined"
                :disabled="saving"
              ></textarea>
              <span v-if="descriptionDetail" class="detail">{{ descriptionDetail.message }}</span>
            </label>

            <!-- リポジトリ（5.9.1）。PBはこのURLで自動的に何もしない -->
            <div class="field">
              <div class="field-head">
                <span class="label">リポジトリ</span>
                <button
                  type="button"
                  class="secondary small"
                  :disabled="saving || repositories.length >= MAX_REPOSITORIES"
                  @click="addRepository"
                >
                  + 追加
                </button>
              </div>

              <p v-if="repositories.length === 0" class="hint">
                関連するリポジトリを登録できます。画面からリンクで開けるほか、
                エージェントがMCP経由でプロジェクトの情報として受け取ります。
              </p>

              <!-- 一覧は表。編集はモーダル（5.9.1）。入力欄を並べると
                   件数ぶん縦に伸び、他の設定が画面から押し出される -->
              <table v-if="repositories.length > 0" class="table repos">
                <thead>
                  <tr>
                    <th scope="col">URL</th>
                    <th scope="col">表示名</th>
                    <th scope="col">説明</th>
                    <th scope="col"><span class="sr-only">操作</span></th>
                  </tr>
                </thead>
                <tbody>
                  <tr
                    v-for="(repo, i) in repositories"
                    :key="i"
                    class="row"
                    @click="editRepository(i)"
                  >
                    <td class="url-cell">
                      <!-- ブラウザで開けるものだけリンクにする。行クリック（編集）と
                           競合しないよう伝播を止める -->
                      <a
                        v-if="isWebUrl(repo.url)"
                        :href="repo.url.trim()"
                        target="_blank"
                        rel="noopener noreferrer"
                        @click.stop
                      >
                        {{ repo.url }}
                      </a>
                      <span v-else>{{ repo.url }}</span>
                    </td>
                    <td>{{ repo.name || '—' }}</td>
                    <td class="muted">{{ repo.description || '—' }}</td>
                    <td class="actions-cell">
                      <button
                        type="button"
                        class="link-button"
                        :disabled="saving"
                        @click.stop="removeRepository(i)"
                      >
                        削除
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>

              <p v-if="repositories.length >= MAX_REPOSITORIES" class="hint">
                登録できるのは{{ MAX_REPOSITORIES }}件までです。
              </p>
            </div>

            <!-- 結果は操作した場所に出す（6.4）。トーストは使わない -->
            <div class="actions">
              <p v-if="generalSaveError" class="alert" role="alert">
                {{ generalSaveError }}
                <button v-if="conflict" type="button" class="link-button" @click="reload">
                  最新の内容を取得
                </button>
              </p>
              <p v-else-if="saved" class="ok" role="status">✓ 保存しました</p>
              <span v-else class="spacer"></span>

              <button type="submit" class="primary" :disabled="!canSave">
                {{ saving ? '保存中…' : '保存' }}
              </button>
            </div>
          </form>

          <!-- ワークフロー（参照のみ。5.9.1。拡充か削除かは実物を見て決める） -->
          <section class="block">
            <h2 class="block-title">ワークフロー<span class="note">（参照のみ）</span></h2>
            <template v-if="store.current.workflow">
              <p class="static-value">{{ store.current.workflow.name }}</p>
              <p class="statuses">
                <span v-for="(s, i) in store.current.workflow.statuses" :key="s.key">
                  <span v-if="i > 0" class="sep" aria-hidden="true"> ─ </span>{{ s.name }}
                </span>
              </p>
            </template>
            <p v-else class="hint">ワークフローが設定されていません。</p>
          </section>

          <!-- プロジェクトの状態（5.6 / 6.3）。権限が無ければブロックごと出さない -->
          <section v-if="canArchive" class="block">
            <h2 class="block-title">プロジェクトの状態</h2>
            <div class="state-line">
              <!-- 状態を色だけで示さない（9.2）ので文字で出す -->
              <p class="static-value">
                {{ isArchived ? '● アーカイブ済み' : '● 有効' }}
              </p>
              <button
                type="button"
                class="secondary"
                :disabled="archiving"
                @click="requestArchiveToggle"
              >
                {{ isArchived ? 'アーカイブを解除' : 'アーカイブする' }}
              </button>
            </div>
            <p v-if="archiveError" class="alert" role="alert">{{ archiveError.message }}</p>
            <p v-else class="hint">
              アーカイブすると、プロジェクト一覧の既定の表示から外れます。解除もできます。
            </p>
          </section>
        </div>

        <!-- ── メンバータブ（5.9.2）─────────────────────────── -->
        <div v-else-if="tab === 'members'" class="blocks" role="tabpanel">
          <section class="block">
            <table class="table">
              <thead>
                <tr>
                  <th scope="col" class="icon-col"><span class="sr-only">種別</span></th>
                  <th scope="col">名前</th>
                  <th scope="col">メール</th>
                  <th scope="col">ロール</th>
                  <th scope="col">参加日</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="m in store.current.members" :key="m.actor_id">
                  <td class="icon-col">
                    <span :aria-label="m.kind === 'agent' ? 'エージェント' : '利用者'">
                      {{ kindIcon(m.kind) }}
                    </span>
                  </td>
                  <td>{{ m.display_name }}</td>
                  <!-- エージェントとシステムは app_user を持たないため null（5.9.2） -->
                  <td class="muted">{{ m.email ?? '—' }}</td>
                  <td>{{ roleLabel(m.role) }}</td>
                  <td class="date">{{ formatDate(m.joined_at) }}</td>
                </tr>
              </tbody>
            </table>

            <p class="count">{{ store.current.members.length }}件</p>
            <p class="hint">ⓘ メンバーの追加・変更は「アカウント / 権限」から行います。</p>
          </section>
        </div>

        <!-- ── タグタブ（5.9.4）──────────────────────────────── -->
        <div v-else-if="tab === 'tags'" class="blocks" role="tabpanel">
          <section class="block">
            <div class="block-head">
              <h2 class="block-title">タグ</h2>
              <button
                type="button"
                class="secondary"
                :disabled="tagBusy || newTagName !== null"
                @click="startAddTag"
              >
                + 追加
              </button>
            </div>

            <!-- 操作の結果は操作した場所に出す（6.4）。追加・改名・削除・
                 並べ替えで1つの欄を使い回す -->
            <p v-if="tagResult" class="ok" role="status">{{ tagResult }}</p>
            <p v-if="tagsError" class="alert" role="alert">✕ {{ tagsError.message }}</p>

            <div v-if="tagsLoading && !tagsLoaded" class="loading" aria-busy="true">
              <span class="skeleton"></span>
              <span class="skeleton short"></span>
            </div>

            <table v-else-if="tags.length > 0 || newTagName !== null" class="table tags">
              <thead>
                <tr>
                  <th scope="col" class="grip-col"><span class="sr-only">並べ替え</span></th>
                  <th scope="col">名前</th>
                  <th scope="col" class="count-col">使用中</th>
                  <th scope="col" class="actions-col"><span class="sr-only">操作</span></th>
                </tr>
              </thead>
              <tbody>
                <tr
                  v-for="tag in tags"
                  :key="tag.id"
                  :class="{ dragging: draggingTagId === tag.id }"
                  @dragover.prevent
                  @drop.prevent="dropTag(tag.id)"
                >
                  <td class="grip-col">
                    <span
                      class="grip"
                      draggable="true"
                      role="button"
                      :aria-label="`${tag.name} を並べ替える`"
                      @dragstart="draggingTagId = tag.id"
                      @dragend="draggingTagId = null"
                      >⠿</span
                    >
                  </td>
                  <td class="name-col">
                    <template v-if="renamingTagId === tag.id">
                      <form class="inline-edit" @submit.prevent="submitRenameTag(tag)">
                        <input
                          v-model="renamingTagName"
                          type="text"
                          class="tag-name-input"
                          :maxlength="30"
                          :aria-label="`${tag.name} の新しい名前`"
                          @keydown.esc="renamingTagId = null"
                        />
                        <button type="submit" class="primary small" :disabled="tagBusy">
                          変更
                        </button>
                        <button
                          type="button"
                          class="secondary small"
                          :disabled="tagBusy"
                          @click="renamingTagId = null"
                        >
                          取消
                        </button>
                      </form>
                      <span v-if="renameTagError" class="detail">✕ {{ renameTagError }}</span>
                    </template>
                    <template v-else>{{ tag.name }}</template>
                  </td>
                  <td class="count-col">{{ tag.ticket_count }}件</td>
                  <td class="actions-col">
                    <div class="row-actions">
                      <button
                        type="button"
                        class="secondary small"
                        :disabled="tagBusy || renamingTagId === tag.id"
                        @click="startRenameTag(tag)"
                      >
                        名前を変更
                      </button>
                      <button
                        type="button"
                        class="danger small"
                        :disabled="tagBusy"
                        @click="deletingTag = tag"
                      >
                        削除
                      </button>
                    </div>
                  </td>
                </tr>

                <!-- 追加は行内（編集できるのが名前1つなので、モーダルは重い） -->
                <tr v-if="newTagName !== null" class="new-row">
                  <td class="grip-col"></td>
                  <td class="name-col">
                    <form class="inline-edit" @submit.prevent="submitNewTag">
                      <input
                        v-model="newTagName"
                        type="text"
                        class="tag-name-input"
                        :maxlength="30"
                        aria-label="新しいタグの名前"
                        placeholder="タグ名"
                        @keydown.esc="newTagName = null"
                      />
                      <button type="submit" class="primary small" :disabled="tagBusy">追加</button>
                      <button
                        type="button"
                        class="secondary small"
                        :disabled="tagBusy"
                        @click="newTagName = null"
                      >
                        取消
                      </button>
                    </form>
                    <span v-if="newTagError" class="detail">✕ {{ newTagError }}</span>
                  </td>
                  <td class="count-col"></td>
                  <td class="actions-col"></td>
                </tr>
              </tbody>
            </table>

            <EmptyState
              v-else
              title="タグがありません"
              message="タグはチケットを横断的に分類します。[+ 追加] から作成してください。"
            />

            <p class="hint">
              ⓘ タグはチケットを横断的に分類します。「どの大きな仕事の一部か」はチケットの親子関係で表します
            </p>
          </section>
        </div>

        <!-- ── スプリントタブ（5.9.5）───────────────────────── -->
        <div v-else class="blocks" role="tabpanel">
          <section class="block">
            <div class="block-head">
              <h2 class="block-title">スプリント</h2>
              <button
                type="button"
                class="secondary"
                :disabled="sprintBusy"
                @click="openSprintModal('new')"
              >
                + 追加
              </button>
            </div>

            <p v-if="sprintResult" class="ok" role="status">{{ sprintResult }}</p>
            <p v-if="sprintsError" class="alert" role="alert">✕ {{ sprintsError.message }}</p>

            <div v-if="sprintsLoading && !sprintsLoaded" class="loading" aria-busy="true">
              <span class="skeleton"></span>
              <span class="skeleton short"></span>
            </div>

            <table v-else-if="sprints.length > 0" class="table sprints">
              <thead>
                <tr>
                  <th scope="col">名前</th>
                  <th scope="col">期間</th>
                  <th scope="col" class="status-col">状態</th>
                  <th scope="col" class="count-col">進捗</th>
                  <th scope="col" class="actions-col"><span class="sr-only">操作</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="s in sprints" :key="s.id">
                  <td class="name-col">
                    {{ s.name }}
                    <span v-if="s.goal" class="goal">{{ s.goal }}</span>
                  </td>
                  <td class="period">{{ sprintPeriod(s) }}</td>
                  <td class="status-col">{{ sprintStatusLabels[s.status] }}</td>
                  <!-- グラフは出さない（バーンダウンは進捗分析、/p/:key/insights。GuiDesign.md 5.9.5） -->
                  <td class="count-col">{{ s.closed_count }}/{{ s.ticket_count }}</td>
                  <td class="actions-col">
                    <div class="row-actions">
                      <button
                        type="button"
                        class="secondary small"
                        :disabled="sprintBusy"
                        @click="openSprintModal(s)"
                      >
                        編集
                      </button>
                      <button
                        type="button"
                        class="danger small"
                        :disabled="sprintBusy"
                        @click="deletingSprint = s"
                      >
                        削除
                      </button>
                    </div>
                  </td>
                </tr>
              </tbody>
            </table>

            <EmptyState
              v-else
              title="スプリントがありません"
              message="[+ 追加] から作成すると、チケットに割り当てられるようになります。"
            />
          </section>
        </div>
      </template>
    </div>

    <RepositoryModal
      v-if="editingIndex !== null"
      :repository="editingDraft"
      :is-new="isNewRepository"
      @save="applyRepository"
      @close="editingIndex = null"
    />

    <ConfirmDialog
      v-if="confirmOpen"
      title="プロジェクトをアーカイブ"
      :message="`「${store.current?.name ?? ''}」をアーカイブします。\nプロジェクト一覧の既定の表示から外れます。あとで解除できます。`"
      confirm-label="アーカイブする"
      :busy="archiving"
      @confirm="runArchiveToggle"
      @cancel="confirmOpen = false"
    />

    <!-- タグの削除（6.3）。**使用中の件数を出す**——使用中でも消せるが、
         何件から外れるのかを知ったうえで押せるようにする -->
    <ConfirmDialog
      v-if="deletingTag"
      title="タグを削除"
      :message="`タグ「${deletingTag.name}」を削除します。\n${
        deletingTag.ticket_count > 0
          ? `${deletingTag.ticket_count}件のチケットで使われています。チケットは消えず、このタグが外れます。`
          : 'このタグはどのチケットでも使われていません。'
      }`"
      confirm-label="削除する"
      danger
      :busy="tagBusy"
      @confirm="confirmDeleteTag"
      @cancel="deletingTag = null"
    />

    <!-- スプリントの削除（6.3）。**チケットは消えない**旨を明記する -->
    <ConfirmDialog
      v-if="deletingSprint"
      title="スプリントを削除"
      :message="`スプリント「${deletingSprint.name}」を削除します。\n割り当てられている${deletingSprint.ticket_count}件のチケットは消えず、スプリント未設定に戻ります。`"
      confirm-label="削除する"
      danger
      :busy="sprintBusy"
      @confirm="confirmDeleteSprint"
      @cancel="deletingSprint = null"
    />

    <SprintModal
      v-if="editingSprint"
      :key="editingSprint === 'new' ? 'new' : editingSprint.id"
      :sprint="editingSprint === 'new' ? null : editingSprint"
      :busy="sprintBusy"
      :field-errors="sprintFieldErrors"
      @save="saveSprint"
      @close="editingSprint = null"
    />
  </div>
</template>

<style scoped>
/* ボタンと同じ高さに揃える（`base.css` の .secondary は button 前提の指定） */
.setup-link {
  display: inline-flex;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border-radius: var(--pb-radius);
  font-weight: 600;
  white-space: nowrap;
  text-decoration: none;
}

.page {
  display: flex;
  flex-direction: column;
  height: 100%;
}

.page-body {
  flex: 1;
  overflow: auto;
  padding: var(--pb-space-6);
}

/* ── タブ（5.9）────────────────────────────────────────── */
.tabs {
  display: flex;
  gap: var(--pb-space-1);
  margin-bottom: var(--pb-space-4);
  border-bottom: 1px solid var(--pb-border);
}

.tab {
  padding: var(--pb-space-2) var(--pb-space-4);
  border: 0;
  border-bottom: 2px solid transparent;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  cursor: pointer;
}

.tab:hover {
  color: var(--pb-text);
}

/* 選択中を色だけで示さない（9.2）。下線の太さでも区別できるようにする */
.tab.selected {
  border-bottom-color: var(--pb-accent);
  color: var(--pb-text);
  font-weight: 600;
}

/* ── ブロック ──────────────────────────────────────────── */
.blocks {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
  max-width: 880px;
}

.block {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
  padding: var(--pb-space-4);
  border: 1px solid var(--pb-line);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
}

.block-title {
  font-size: 14px;
  font-weight: 600;
}

.note {
  margin-left: var(--pb-space-2);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 400;
}

/* ── 入力（作成モーダルと同じ形にそろえる）────────────────── */
.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

.field-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-3);
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.required {
  color: var(--pb-danger-text);
}

.static-value {
  margin: 0;
  font-weight: 600;
}

input[type='text'],
textarea {
  width: 100%;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

input[type='text'] {
  height: 36px;
}

textarea {
  padding: var(--pb-space-2) var(--pb-space-3);
  resize: vertical;
}

input[aria-invalid='true'],
textarea[aria-invalid='true'] {
  border-color: var(--pb-danger-border);
}

input:disabled,
textarea:disabled {
  opacity: 0.6;
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
  line-height: 1.7;
}

/* warning は面で表す（8.4.1）。文字はベース色のまま */
.warn {
  margin: 0;
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-warning-border);
  border-radius: var(--pb-radius);
  background: var(--pb-warning-bg);
  color: var(--pb-warning-text);
  font-size: 13px;
}

/* ── リポジトリ（5.9.1）───────────────────────────────── */
.repos {
  table-layout: fixed;
}

.repos th:nth-child(1) {
  width: 45%;
}

.repos th:nth-child(2) {
  width: 20%;
}

.repos th:nth-child(4) {
  width: 64px;
}

.repos td {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.url-cell {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}

.url-cell a {
  color: var(--pb-accent);
}

.muted {
  color: var(--pb-text-muted);
}

.actions-cell {
  text-align: right;
}

.link-button {
  padding: 0;
  border: 0;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 13px;
  text-decoration: underline;
  white-space: nowrap;
  cursor: pointer;
}

.link-button:hover:not(:disabled) {
  color: var(--pb-text);
}

.row {
  cursor: pointer;
}

.row:hover {
  background: var(--pb-hover);
}

/* ── 保存 ─────────────────────────────────────────────── */
.actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--pb-space-3);
}

.spacer {
  flex: 1;
}

.alert {
  display: flex;
  flex: 1;
  align-items: center;
  gap: var(--pb-space-3);
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-danger-bg);
  color: var(--pb-danger-text);
  font-size: 13px;
}

.ok {
  flex: 1;
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.state-line {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-3);
}

.statuses {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.sep {
  color: var(--pb-text-muted);
}

button:disabled {
  cursor: default;
  opacity: 0.5;
}

/* ── メンバー（5.9.2）─────────────────────────────────── */
.table {
  width: 100%;
  border-collapse: collapse;
}

th {
  padding: var(--pb-space-2) var(--pb-space-3);
  border-bottom: 1px solid var(--pb-border);
  color: var(--pb-text-muted);
  font-size: 13px;
  font-weight: 600;
  text-align: left;
}

td {
  padding: var(--pb-space-3);
  border-bottom: 1px solid var(--pb-line);
}

.icon-col {
  width: 32px;
}

.date {
  color: var(--pb-text-muted);
  font-variant-numeric: tabular-nums;
  white-space: nowrap;
}

.count {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.sr-only {
  position: absolute;
  overflow: hidden;
  width: 1px;
  height: 1px;
  clip-path: inset(50%);
  white-space: nowrap;
}

/* ── 読み込み中（6.2）─────────────────────────────────── */
.skeleton {
  display: block;
  height: 14px;
  border-radius: var(--pb-radius);
  background: var(--pb-hover);
}

.skeleton.title {
  width: 30%;
}

.skeleton.short {
  width: 60%;
}

/* ── タグ・スプリント（5.9.4 / 5.9.5）──────────────────── */

/* 見出しと [+ 追加] を1行に並べる */
.block-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--pb-space-3);
}

.block-head .block-title {
  margin: 0;
}

.loading {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
}

/* `⠿` のドラッグ列。掴む対象だと分かるようにカーソルを変える */
.grip-col {
  width: 32px;
}

.grip {
  display: inline-block;
  color: var(--pb-text-muted);
  cursor: grab;
  user-select: none;
}

.grip:active {
  cursor: grabbing;
}

tr.dragging {
  opacity: 0.5;
}

.name-col {
  /* 名前を優先して伸ばす。他の列は内容ぶんで足りる */
  width: 100%;
}

/* ゴールは名前の下に小さく添える（別の列にすると横幅を食う） */
.goal {
  display: block;
  margin-top: 2px;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.count-col,
.status-col {
  width: 1%;
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

/* 数える列は右寄せ。桁が揃って読み比べられる */
.count-col {
  text-align: right;
}

.period {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

/*
 * **セルに display:flex を掛けない。** th / td を flex にすると表の
 * レイアウトから外れ、行の区切り線が操作列の手前で切れる（1440px と 900px の
 * どちらでも起きた）。getBoundingClientRect() は妥当な箱を返すので、
 * 実測では拾えない——スクリーンショットを見て気づいた崩れである。
 * 並べるのは中の入れ物の役目にする。
 */
.actions-col {
  width: 1%;
  white-space: nowrap;
}

.row-actions {
  display: flex;
  gap: var(--pb-space-2);
  justify-content: flex-end;
}

/* 行内編集（タグは編集できるのが名前1つなのでモーダルにしない） */
.inline-edit {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
}

/*
 * **要素セレクタに勝てる詳細度で書く。** 上の `input[type='text']` は
 * (0,1,1) で `width: 100%` を持ち、クラス1つ (0,1,0) では負ける。
 * ここは flex の中なので 100% だとボタンを押し出してしまう。
 */
.inline-edit input[type='text'] {
  width: auto;
  flex: 1 1 auto;
  min-width: 0;
  height: 28px;
}

.new-row td {
  background: var(--pb-hover);
}

.danger {
  display: inline-flex;
  flex: none;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-surface);
  color: var(--pb-danger-text);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}

.danger:hover:not(:disabled) {
  background: var(--pb-danger-bg);
}
</style>
