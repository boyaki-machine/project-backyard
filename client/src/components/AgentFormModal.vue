<script setup lang="ts">
/**
 * エージェントの登録・編集モーダル（`GuiDesign.md` 5.8.2）。
 *
 * **1つの部品が2つのモードを持つ**（5.10 の `DocFormModal` と同じ形）。同じ欄を
 * 使い、編集では**プロジェクトとクライアント種別を読み取り専用にする**
 * ——`ApiDesign.md` 4.5.4 が変更不可と定めており、**その2つが「どのクライアントが
 * どのプロジェクトにつないでいるか」という1件の同一性そのもの**だからである。
 *
 * **プロジェクトの選択肢は「自分がメンバーであるもの」に限る。** `GET /projects` は
 * アドミニストレータには非メンバーのプロジェクトも返す（`my_role` が `null`。
 * `ApiDesign.md` 5.1）が、`POST /me/agents` はメンバーでないと 422 になる。
 * **選べるのに必ず失敗する項目を出さない。**
 */
import { computed, onMounted, ref } from 'vue'

import { ApiError } from '../api/client'
import type { MyAgent } from '../api/me'
import * as projectsApi from '../api/projects'
import { CLIENT_KINDS, type ClientKind } from '../lib/agents'
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
  submit: [payload: { display_name: string; project_key: string; client_kind: ClientKind; model_name: string; model_version: string }]
  close: []
}>()

/** `ApiDesign.md` 4.5.2。`actor.display_name` の CHECK（1〜60）に揃える */
const MAX_NAME = 60
/** 同 4.5.2。`agent` 表に CHECK は無く、アプリ側が持つ */
const MAX_MODEL = 100

const isEdit = computed(() => props.agent !== null)

const displayName = ref(props.agent?.display_name ?? '')
const projectKey = ref(props.agent?.project.key ?? '')
const clientKind = ref<ClientKind>(props.agent?.client_kind ?? 'claude_code')
const modelName = ref(props.agent?.model_name ?? '')
const modelVersion = ref(props.agent?.model_version ?? '')

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
  })
}
</script>

<template>
  <Modal :title="isEdit ? 'エージェントを編集' : 'エージェントを登録'" @close="emit('close')">
    <form id="agent-form" class="agent-form" @submit.prevent="submit">
      <label class="field">
        <span class="label">名前 <span class="required">*</span></span>
        <input
          v-model="displayName"
          type="text"
          name="display_name"
          :maxlength="MAX_NAME"
          placeholder="私の Claude Code"
          :aria-invalid="detail('display_name') !== undefined"
          :disabled="busy"
        />
        <span v-if="detail('display_name')" class="detail">{{ detail('display_name')?.message }}</span>
        <span v-else class="hint">どの端末のどのクライアントかが分かる名前を付けてください。</span>
      </label>

      <!-- 編集では変えられない（4.5.4）。値は読めるように残す -->
      <div class="field">
        <span class="label">プロジェクト <span v-if="!isEdit" class="required">*</span></span>
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
          <span v-else-if="noProjects" class="detail">
            参加しているプロジェクトがありません。プロジェクトに参加してから登録してください。
          </span>
          <span v-else class="hint">参加しているプロジェクトから選びます。あとで変更できません。</span>
        </template>
      </div>

      <div class="field">
        <span class="label">クライアント <span v-if="!isEdit" class="required">*</span></span>
        <p v-if="isEdit" class="fixed">
          {{ CLIENT_KINDS.find((k) => k.value === agent?.client_kind)?.label }}
        </p>
        <template v-else>
          <div class="choices-row">
            <label v-for="k in CLIENT_KINDS" :key="k.value">
              <input
                type="radio"
                name="client_kind"
                :value="k.value"
                :checked="clientKind === k.value"
                :disabled="busy"
                @change="clientKind = k.value"
              />
              <span>{{ k.label }}</span>
            </label>
          </div>
          <span class="hint">あとで変更できません。別のクライアントは別に登録します。</span>
        </template>
      </div>

      <label class="field">
        <span class="label">モデル名</span>
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
        <span class="label">モデルバージョン</span>
        <input
          v-model="modelVersion"
          type="text"
          name="model_version"
          :maxlength="MAX_MODEL"
          :disabled="busy"
        />
        <span v-if="detail('model_version')" class="detail">{{ detail('model_version')?.message }}</span>
      </label>

      <p v-if="projectsError" class="alert" role="alert">{{ projectsError.message }}</p>
      <p
        v-if="error && !detail('display_name') && !detail('project_key') && !detail('model_name') && !detail('model_version')"
        class="alert"
        role="alert"
      >
        {{ error.message }}
      </p>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('close')">
        キャンセル
      </button>
      <button type="submit" form="agent-form" class="primary" :disabled="!canSubmit">
        {{ busy ? (isEdit ? '保存中…' : '登録中…') : isEdit ? '保存' : '登録' }}
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

/* 編集で変えられない項目（4.5.4）。入力欄に見せない */
.fixed {
  margin: 0;
  padding: var(--pb-space-1) 0;
}

/* ラジオを横1行に並べる（5.8.1 の発行モーダルと同じ形）。
   `.field` の `flex-direction: column` をそのまま受けると縦積みになる */
.choices-row {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-1) var(--pb-space-4);
  align-items: center;
}

.choices-row label {
  display: inline-flex;
  align-items: center;
  gap: var(--pb-space-1);
  cursor: pointer;
  white-space: nowrap;
}

.choices-row input[type='radio'] {
  width: auto;
  height: auto;
  margin: 0;
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
