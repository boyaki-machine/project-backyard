<script setup lang="ts">
/**
 * プロジェクト設定（`GuiDesign.md` 5.9）。必要権限は `project.edit`。
 *
 * タブは「一般」「メンバー」の2つ。**タブはURLを持たない**（3.2 のルーティング表が
 * 持つのは `/p/:key/settings` の1行だけである）。
 *
 * 表示は `GET /projects/:key` の1本で足りる（5.4）。ワークフローもメンバーも
 * 自分の実効権限も同じ応答に入っている。
 *
 * **操作の結果はトーストではなく、操作した場所に出す**（6.4）。
 */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import ConfirmDialog from '../components/ConfirmDialog.vue'
import EmptyState from '../components/EmptyState.vue'
import PageHeader from '../components/PageHeader.vue'
import RepositoryModal from '../components/RepositoryModal.vue'
import { ApiError } from '../api/client'
import * as projectsApi from '../api/projects'
import type { ProjectDetail, ProjectRepository, UpdateProjectRequest } from '../api/projects'
import { formatDate } from '../lib/datetime'
import { useAuthStore } from '../stores/auth'
import { useProjectStore } from '../stores/project'

/** 入力の上限（`ApiDesign.md` 5.3 の検証表と `GuiDesign.md` 5.9.1）。正本はサーバ側 */
const MAX_NAME = 100
const MAX_DESCRIPTION = 1000
/** リポジトリの上限（5.9.1）。1件ごとの検証は `RepositoryModal` が持つ */
const MAX_REPOSITORIES = 10

/**
 * プロジェクトロールの表示名（`DbDesign.md` 7.3 の `display_name`）。
 *
 * `GET /projects/:key` はロールをキーでしか返さない。**`GET /roles`（手順16）を
 * 実装した時点でこの表を捨てる。** それまでは画面が持つ。
 */
const ROLE_LABELS: Record<string, string> = {
  project_admin: 'プロジェクト管理者',
  project_member: 'メンバー',
  project_viewer: '閲覧者',
}

const auth = useAuthStore()
const store = useProjectStore()
const route = useRoute()
const router = useRouter()

type Tab = 'general' | 'members'
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

onMounted(() => void load())

// プロジェクト切替は「同じ画面種別を維持する」（4.4）ため、この画面のまま
// `:key` だけが変わる。コンポーネントは再生成されないので自分で取り直す。
watch(projectKey, () => {
  tab.value = 'general'
  saveError.value = null
  saved.value = false
  archiveError.value = null
  void load()
})

onUnmounted(() => store.clearCurrent())

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

/**
 * ブラウザで開けるURLか。
 *
 * `git@host:org/repo.git` はクリックしても何も起きないので**リンクにしない**
 * （5.9.1）。文字列として見せ、コピーできる状態にとどめる。
 */
function isWebUrl(url: string): boolean {
  return /^https?:\/\//i.test(url.trim())
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
function roleLabel(role: string): string {
  return ROLE_LABELS[role] ?? role
}

/** 人間とエージェントを同じ一覧に並べる（設計原則5、`DbDesign.md` 6.2） */
function kindIcon(kind: string): string {
  return kind === 'agent' ? '🤖' : kind === 'system' ? '⚙' : '👤'
}
</script>

<template>
  <div class="page">
    <PageHeader title="プロジェクト設定" />

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
        <div v-else class="blocks" role="tabpanel">
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
            <p class="hint">ⓘ メンバーの追加・変更は「ユーザー / 権限」から行います。</p>
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
  </div>
</template>

<style scoped>
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

.primary,
.secondary {
  display: inline-flex;
  flex: none;
  align-items: center;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border-radius: var(--pb-radius);
  font-weight: 600;
  white-space: nowrap;
  cursor: pointer;
}

.primary {
  border: 1px solid var(--pb-accent);
  background: var(--pb-accent);
  color: var(--pb-on-accent);
}

.primary:hover:not(:disabled) {
  border-color: var(--pb-accent-hover);
  background: var(--pb-accent-hover);
}

.secondary {
  border: 1px solid var(--pb-border);
  background: var(--pb-surface);
  color: inherit;
}

.secondary:hover:not(:disabled) {
  background: var(--pb-hover);
}

.secondary.small {
  height: 28px;
  font-size: 13px;
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
</style>
