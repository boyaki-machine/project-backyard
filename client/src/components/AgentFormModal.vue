<script setup lang="ts">
/**
 * エージェントの登録・編集モーダル（`GuiDesign.md` 5.8.2）。
 *
 * **1つの部品が2つのモードを持つ**（5.10 の `DocFormModal` と同じ形）。同じ欄を
 * 使い、編集では**プロジェクトだけを読み取り専用にする**——`ApiDesign.md` 4.5.4 が
 * 変更不可と定めており、**そのエージェントが行った仕事はプロジェクトに属する**からである。
 * **クライアント種別は編集できる**（0020 で変更可能にした）——値域が今後も増えるので、
 * `その他・OSS 等` で登録した人が、PB がその種別に対応した日に移れる必要がある。
 *
 * **クライアント種別は `<select>` で出す**（ラジオではない）。値域が5つを超えて
 * 増えていくため（`DbDesign.md` 8.2.1.1）、ラジオを1行に並べる形では早晩あふれる。
 * **選択肢と表示名は `GET /agent-client-kinds` から取る**——画面は対応表を持たない。
 *
 * **プロジェクトの選択肢は「自分がメンバーであるもの」に限る。** `GET /projects` は
 * アドミニストレータには非メンバーのプロジェクトも返す（`my_role` が `null`。
 * `ApiDesign.md` 5.1）が、`POST /me/agents` はメンバーでないと 422 になる。
 * **選べるのに必ず失敗する項目を出さない。**
 */
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../api/client'
import * as meApi from '../api/me'
import type { AgentClientKind, MyAgent } from '../api/me'
import * as projectsApi from '../api/projects'
import Modal from './Modal.vue'

const props = defineProps<{
  /** 編集する相手。`null` なら登録 */
  agent: MyAgent | null
  /** 送信中（呼び出し側が API を持つ） */
  busy: boolean
  /** 送信で起きた誤り。項目ごとの `details` をここから引く */
  error: ApiError | null
}>()

const emit = defineEmits<{
  submit: [
    payload: {
      display_name: string
      project_key: string
      client_kind: string
      model_name: string
      model_version: string
      token_env_suffix: string
    },
  ]
  close: []
}>()

/** `ApiDesign.md` 4.5.2。`actor.display_name` の CHECK（1〜60）に揃える */
const MAX_NAME = 60
/** 同 4.5.2。`agent` 表に CHECK は無く、アプリ側が持つ */
const MAX_MODEL = 100

/**
 * 環境変数名の接尾（`ApiDesign.md` 4.5.2 の `token_env_suffix`）。
 *
 * **接頭の `PB_TOKEN_` は PB が付ける。** 接頭ごと入力させると `PATH` や `HOME` を
 * 作れてしまう（`DbDesign.md` 8.2.1）。CHECK は `^[A-Z][A-Z0-9_]{0,40}$`。
 */
const ENV_SUFFIX_PREFIX = 'PB_TOKEN_'
const MAX_ENV_SUFFIX = 41

const isEdit = computed(() => props.agent !== null)

const displayName = ref(props.agent?.display_name ?? '')
const projectKey = ref(props.agent?.project.key ?? '')
const clientKind = ref<string>(props.agent?.client_kind ?? '')
const modelName = ref(props.agent?.model_name ?? '')
const modelVersion = ref(props.agent?.model_version ?? '')
const tokenEnvSuffix = ref(props.agent?.token_env_suffix ?? '')

/**
 * 表示名から環境変数名の候補を作る。
 *
 * **英数字を大文字化し、それ以外を `_` に畳む。** `私の Claude Code` なら
 * `CLAUDE_CODE`、`ノートPC` なら **空**——**日本語だけの名前では候補が作れない**ので、
 * そのときは入力を促す（`GuiDesign.md` 5.8.2）。
 *
 * **候補はあくまで初期値である。** 「どの端末か」は PB が知らない情報なので、
 * 利用者が打ち直せる（`ApiDesign.md` 4.5.1 が `display_name` を「本人が思い出す
 * ための手がかり」と定めているのと同じ理由）。
 */
function suggestEnvSuffix(name: string): string {
  const s = name
    .toUpperCase()
    .replace(/[^A-Z0-9]+/g, '_')
    .replace(/^_+|_+$/g, '')
  // 先頭は英字でなければならない（CHECK が `^[A-Z]`）。数字始まりは候補にしない。
  if (!/^[A-Z]/.test(s)) return ''
  return s.slice(0, MAX_ENV_SUFFIX)
}

/**
 * 名前を打ち終えたら候補を入れる。
 *
 * **既に何か入っていれば触らない。** 利用者が打った値を上書きしない。
 * **編集では候補を入れない**——既存の変数名を勝手に変えると、`~/.zshrc` を
 * 直すまで繋がらなくなる。
 */
function fillEnvSuffixSuggestion() {
  if (isEdit.value || tokenEnvSuffix.value !== '') return
  tokenEnvSuffix.value = suggestEnvSuffix(displayName.value)
}

// ── クライアント種別のカタログ（`ApiDesign.md` 4.5.7）──────

const clientKinds = ref<AgentClientKind[]>([])
const kindsError = ref<ApiError | null>(null)

async function loadClientKinds() {
  try {
    clientKinds.value = (await meApi.listAgentClientKinds()).items
    // **既定は一覧の先頭**（`sort_order` の昇順で返る）。画面は並べ替えない。
    if (clientKind.value === '' && clientKinds.value.length > 0) {
      clientKind.value = clientKinds.value[0].key
    }
  } catch (e: unknown) {
    kindsError.value = e instanceof ApiError ? e : null
  }
}
onMounted(loadClientKinds)

// ── プロジェクトの選択肢 ────────────────────────────────────

const projects = ref<projectsApi.ProjectListItem[]>([])
const projectsLoading = ref(false)
const projectsError = ref<ApiError | null>(null)

async function loadProjects() {
  // 編集では変えられないので読まない（`ApiDesign.md` 4.5.4）
  if (isEdit.value) return
  projectsLoading.value = true
  projectsError.value = null
  try {
    // **`per_page` は上限の 200**（`ApiDesign.md` 2.6）。既定の 25 だと
    // 参加プロジェクトが多い人で1ページ目しか選べない。
    // **`status` は既定の `active` のまま**——アーカイブ済みに
    // エージェントをつなぐ動機が無い（5.8.2）。
    const res = await projectsApi.listProjects({ per_page: 200, sort: 'name', order: 'asc' })
    // **`my_role` が `null` の行を落とす。** アドミニストレータの一覧には
    // 非メンバーのプロジェクトが混ざる（5.1）。
    projects.value = res.items.filter((p) => p.my_role !== null)
    if (projectKey.value === '' && projects.value.length > 0) {
      projectKey.value = projects.value[0].key
    }
  } catch (e: unknown) {
    projectsError.value = e instanceof ApiError ? e : null
  } finally {
    projectsLoading.value = false
  }
}
onMounted(loadProjects)

const noProjects = computed(
  () => !isEdit.value && !projectsLoading.value && projects.value.length === 0,
)

// ── 検証と送信 ──────────────────────────────────────────────

function detail(field: string) {
  return props.error?.detailFor(field)
}

const canSubmit = computed(
  () =>
    displayName.value.trim() !== '' &&
    clientKind.value !== '' &&
    (isEdit.value || projectKey.value !== '') &&
    !props.busy,
)

function submit() {
  if (!canSubmit.value) return
  emit('submit', {
    display_name: displayName.value.trim(),
    project_key: projectKey.value,
    client_kind: clientKind.value,
    model_name: modelName.value.trim(),
    model_version: modelVersion.value.trim(),
    token_env_suffix: tokenEnvSuffix.value.trim().toUpperCase(),
  })
}
</script>

<template>
  <Modal :title="isEdit ? $ui('エージェントを編集') : $ui('エージェントを登録')" @close="emit('close')">
    <form id="agent-form" class="agent-form" @submit.prevent="submit">
      <label class="field">
        <span class="label">{{ $ui('名前') }} <span class="required">*</span></span>
        <input
          v-model="displayName"
          type="text"
          name="display_name"
          :maxlength="MAX_NAME"
          :placeholder="$ui('私の Claude Code')"
          :aria-invalid="detail('display_name') !== undefined"
          :disabled="busy"
          @blur="fillEnvSuffixSuggestion"
        />
        <span v-if="detail('display_name')" class="detail">{{ detail('display_name')?.message }}</span>
        <span v-else class="hint">{{ $ui('どの端末のどのクライアントかが分かる名前を付けてください。') }}</span>
      </label>

      <!-- 編集では変えられない（4.5.4）。値は読めるように残す -->
      <div class="field">
        <span class="label">{{ $ui('プロジェクト') }} <span v-if="!isEdit" class="required">*</span></span>
        <p v-if="isEdit" class="fixed">{{ agent?.project.name }}</p>
        <template v-else>
          <select
            v-model="projectKey"
            name="project_key"
            :disabled="busy || projectsLoading || noProjects"
            :aria-invalid="detail('project_key') !== undefined"
          >
            <option v-for="p in projects" :key="p.key" :value="p.key">{{ p.name }}</option>
          </select>
          <span v-if="detail('project_key')" class="detail">{{ detail('project_key')?.message }}</span>
          <span v-else-if="noProjects" class="detail"> {{ $ui('参加しているプロジェクトがありません。プロジェクトに参加してから登録してください。') }} </span>
          <span v-else class="hint">{{ $ui('参加しているプロジェクトから選びます。あとで変更できません。') }}</span>
        </template>
      </div>

      <!-- **`<select>` で出す**（5.8.2）。値域は今後も増えるのでラジオでは早晩あふれる。
           **選択肢は `GET /agent-client-kinds` から取る**——画面は対応表を持たない -->
      <label class="field">
        <span class="label">{{ $ui('クライアント') }} <span class="required">*</span></span>
        <select
          v-model="clientKind"
          name="client_kind"
          :disabled="busy || clientKinds.length === 0"
          :aria-invalid="detail('client_kind') !== undefined"
        >
          <option v-for="k in clientKinds" :key="k.key" :value="k.key">{{ k.display_name }}</option>
        </select>
        <span v-if="detail('client_kind')" class="detail">{{ detail('client_kind')?.message }}</span>
        <!-- **「VS Code」という語が選択肢に無い**ので、自分の使い方をどれに当てるか迷う。
             5.8.2 の対応表を1行に畳んでその場に出す -->
        <span v-else class="hint"> {{ $ui('VS Code をお使いの場合は、その中で動いているものを選びます（Claude 拡張なら Claude Code、GitHub Copilot なら GitHub Copilot）。') }} </span>
      </label>

      <label class="field">
        <span class="label">{{ $ui('モデル名') }}</span>
        <input
          v-model="modelName"
          type="text"
          name="model_name"
          :maxlength="MAX_MODEL"
          placeholder="claude-opus-5"
          :disabled="busy"
        />
        <span v-if="detail('model_name')" class="detail">{{ detail('model_name')?.message }}</span>
      </label>

      <label class="field">
        <span class="label">{{ $ui('モデルバージョン') }}</span>
        <input
          v-model="modelVersion"
          type="text"
          name="model_version"
          :maxlength="MAX_MODEL"
          :disabled="busy"
        />
        <span v-if="detail('model_version')" class="detail">{{ detail('model_version')?.message }}</span>
      </label>

      <!-- **接尾だけを入力させる**（5.8.2）。接頭は固定文字として左に出し、
           編集させない——`PATH` や `HOME` を作れないようにするためである -->
      <label class="field">
        <span class="label">{{ $ui('環境変数名') }}</span>
        <div class="env-row">
          <span class="env-prefix">{{ ENV_SUFFIX_PREFIX }}</span>
          <input
            v-model="tokenEnvSuffix"
            type="text"
            name="token_env_suffix"
            :maxlength="MAX_ENV_SUFFIX"
            placeholder="MY_LAPTOP"
            :aria-invalid="detail('token_env_suffix') !== undefined"
            :disabled="busy"
          />
        </div>
        <span v-if="detail('token_env_suffix')" class="detail">
          {{ detail('token_env_suffix')?.message }}
        </span>
        <span v-else-if="isEdit" class="hint"> {{ $ui('変えたら接続設定を取り直してください。設定ファイルに古い変数名が残っていると繋がりません。') }} </span>
        <span v-else class="hint"> {{ $ui('トークンを入れる環境変数です。端末が分かる名前にしてください（同じ端末で複数のエージェントを使うときに区別できます）。') }} </span>
      </label>

      <p v-if="kindsError" class="alert" role="alert">{{ kindsError.message }}</p>
      <p v-if="projectsError" class="alert" role="alert">{{ projectsError.message }}</p>
      <p
        v-if="error && !detail('display_name') && !detail('project_key') && !detail('client_kind') && !detail('model_name') && !detail('model_version') && !detail('token_env_suffix')"
        class="alert"
        role="alert"
      >
        {{ error.message }}
      </p>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')"> {{ $ui('キャンセル') }} </button>
      <button type="submit" form="agent-form" class="primary" :disabled="!canSubmit">
        {{ busy ? (isEdit ? $ui("保存中…") : $ui("登録中…")) : isEdit ? $ui("保存") : $ui("登録") }}
      </button>
    </template>
  </Modal>
</template>

<style scoped>
.agent-form {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.required {
  color: var(--pb-danger-text);
}

.agent-form input[type='text'],
.agent-form select {
  width: 100%;
  height: 32px;
  padding: 0 var(--pb-space-3);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
}

/* 接頭は固定文字。**入力欄の一部に見せて、編集できないことを形で示す** */
.env-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  min-width: 0;
}

.env-prefix {
  flex: none;
  color: var(--pb-text-muted);
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
}

/* 編集で変えられない項目（4.5.4）。入力欄に見せない */
.fixed {
  margin: 0;
  padding: var(--pb-space-1) 0;
}

.detail {
  color: var(--pb-danger-text);
  font-size: 13px;
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 13px;
}

.alert {
  margin: 0;
  padding: var(--pb-space-2) var(--pb-space-3);
  border: 1px solid var(--pb-danger-border);
  border-radius: var(--pb-radius);
  background: var(--pb-danger-bg);
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
