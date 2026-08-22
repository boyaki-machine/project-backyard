<script lang="ts">
/** 候補のプロジェクト。`<script setup>` は export を持てないのでここに置く */
export interface ProjectChoice {
  key: string
  name: string
}
</script>

<script setup lang="ts">
/**
 * プロジェクト権限の付与（`GuiDesign.md` 5.6.2 の「プロジェクトごとの権限」）。
 *
 * **一覧は表、追加はモーダル**——手順11b のリポジトリ（5.9.1）と同じ形にする。
 * 一覧に行を直接生やすと、確定していない行が既存の権限に混じって見える。
 *
 * 候補は `GET /projects?per_page=200&sort=key&order=asc` を呼び出し側が引く
 * （13a の引き継ぎ）。**既に権限を持つプロジェクトは候補から外して渡す**——
 * ロールの変更は一覧の行でできるので、ここに出すと入口が2つになる。
 */
import { computed, onMounted, ref } from 'vue'

import Modal from './Modal.vue'
import { useRolesStore } from '../stores/roles'

const props = withDefaults(
  defineProps<{
    candidates: ProjectChoice[]
    /** 候補の取得中 */
    loading?: boolean
    /** 送信中。ボタンを止めて二重送信を防ぐ */
    busy?: boolean
    /** サーバが返した `message`。そのまま出す（`ApiDesign.md` 2.5） */
    errorMessage?: string | null
  }>(),
  { loading: false, busy: false, errorMessage: null },
)

const emit = defineEmits<{
  submit: [value: { projectKey: string; role: string }]
  cancel: []
}>()

const projectKey = ref('')
/** 既定は「メンバー」。**与えすぎない側から始める**（最小権限） */
const role = ref('project_member')

const canSubmit = computed(
  () => !props.busy && !props.loading && projectKey.value !== '' && role.value !== '',
)

function submit(): void {
  if (!canSubmit.value) return
  emit('submit', { projectKey: projectKey.value, role: role.value })
}

/**
 * 選択肢は `GET /roles?scope=project`（`ApiDesign.md` 7.1）。表示名・説明・
 * 並びとも `DbDesign.md` 7.3 のシードが正本である。
 *
 * このモーダルは `user.manage` を要する画面（ユーザー詳細）から開くので、
 * 呼び出し側が全件を読み込んでいる。
 */
const rolesStore = useRolesStore()
const projectRoles = computed(() => rolesStore.projectRoles)
const roleDescription = computed(() => rolesStore.roleDescription(role.value))

onMounted(() => {
  void rolesStore.ensureRoles('all')
})
</script>

<template>
  <Modal title="プロジェクトの権限を追加" @close="emit('cancel')">
    <form class="body" @submit.prevent="submit">
      <label class="field">
        <span class="label">プロジェクト <span class="required">*</span></span>
        <select v-model="projectKey" name="project_key" :disabled="busy || loading">
          <option value="" disabled>
            {{ loading ? '読み込み中…' : 'プロジェクトを選択' }}
          </option>
          <option v-for="p in candidates" :key="p.key" :value="p.key">
            {{ p.key }} — {{ p.name }}
          </option>
        </select>
        <span v-if="!loading && candidates.length === 0" class="hint">
          追加できるプロジェクトがありません（すべてに権限が設定されています）。
        </span>
      </label>

      <label class="field">
        <span class="label">ロール <span class="required">*</span></span>
        <select v-model="role" name="role" :disabled="busy">
          <option v-for="r in projectRoles" :key="r.key" :value="r.key">
            {{ r.display_name }}
          </option>
        </select>
        <span class="hint">{{ roleDescription }}</span>
      </label>

      <p v-if="errorMessage" class="error" role="alert">✕ {{ errorMessage }}</p>
    </form>

    <template #footer>
      <button type="button" class="secondary" :disabled="busy" @click="emit('cancel')">
        キャンセル
      </button>
      <button type="button" class="primary" :disabled="!canSubmit" @click="submit">追加</button>
    </template>
  </Modal>
</template>

<style scoped>
.body {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-4);
}

.field {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
}

.label {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.required {
  color: var(--pb-danger-text);
}

select {
  height: 32px;
  padding: 0 var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
}

.hint {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.error {
  margin: 0;
  color: var(--pb-danger-text);
  font-size: 13px;
}
</style>
