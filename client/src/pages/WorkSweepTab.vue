<script setup lang="ts">
import { uiText } from '../locales/ui'
/**
 * 作業記録タブ（`GuiDesign.md` 5.12.3）。
 *
 * **設計の正本は `Design.md` 8.5.7 と `ApiDesign.md` 11.14。** 閉じられないまま残った
 * 作業記録（浮き）を全プロジェクトから数え、確認のうえで閉じる。
 *
 * **開いたときは数えるだけである。** 閉じるのは [片付ける] を押して確認したときだけで、
 * サーバが押した時点で判定し直す。
 *
 * **閾値の設定 `work_stale_hours` もこのタブで扱う**——TLS の設定を TLS 証明書タブで
 * 扱うのと同じで、関係するものを2つのタブに分けない。
 */
import { computed, onMounted, ref } from 'vue'

import ConfirmDialog from '../components/ConfirmDialog.vue'
import { ApiError } from '../api/client'
import * as settingsApi from '../api/settings'
import type { Setting, WorkSweep, WorkSweepItem } from '../api/settings'
import { formatDateTime } from '../lib/datetime'

const SETTING_KEY = 'work_stale_hours'

const sweep = ref<WorkSweep | null>(null)
const setting = ref<Setting | null>(null)
const loading = ref(false)
const loadError = ref<ApiError | null>(null)

/** 閾値の編集中の値。未編集なら null */
const draftHours = ref<string | null>(null)
const savingSetting = ref(false)

const confirming = ref(false)
const running = ref(false)
/** 表の直上に出す結果（`GuiDesign.md` 6.4） */
const notice = ref('')
const actionError = ref<ApiError | null>(null)

function asApiError(e: unknown): ApiError {
  if (e instanceof ApiError) return e
  return new ApiError({ status: 0, code: 'network_error', message: uiText("通信に失敗しました") })
}

async function load() {
  loading.value = true
  loadError.value = null
  try {
    const [s, list] = await Promise.all([settingsApi.getWorkSweep(), settingsApi.getSettings()])
    sweep.value = s
    setting.value = list.items.find((x) => x.key === SETTING_KEY) ?? null
    draftHours.value = null
  } catch (e) {
    loadError.value = asApiError(e)
  } finally {
    loading.value = false
  }
}
onMounted(load)

const currentHours = computed(() => draftHours.value ?? setting.value?.value ?? '')
const settingDirty = computed(() => draftHours.value !== null && draftHours.value !== setting.value?.value)

function hoursLabel(v: string): string {
  return v === '0' ? uiText("時間で判定しない（日次の処理も止める）") : uiText("{value0}時間", { value0: v })
}

/** 固定されている理由。**どこで変えるのかを必ず添える**（5.12 と同じ） */
const pinnedReason = computed(() => {
  const s = setting.value
  if (!s || s.editable) return ''
  switch (s.source) {
    case 'secret_file':
      return uiText("{value0}_FILE が指すファイルで固定されています", { value0: s.env_key })
    case 'config_file':
      return uiText("設定ファイルの {value0} で固定されています", { value0: s.config_file_key })
    default:
      return uiText("{value0} で固定されています", { value0: s.env_key })
  }
})

async function saveSetting() {
  if (draftHours.value === null) return
  savingSetting.value = true
  notice.value = ''
  actionError.value = null
  try {
    await settingsApi.putSettings([{ key: SETTING_KEY, value: draftHours.value }])
    notice.value = uiText("浮きとみなす時間を保存しました。")
    await load()
  } catch (e) {
    actionError.value = asApiError(e)
  } finally {
    savingSetting.value = false
  }
}

function reasonLabel(it: WorkSweepItem): string {
  if (it.reason === 'ticket_closed') return uiText("チケットが完了")
  return uiText("最後の活動から{value0}時間", { value0: sweep.value?.threshold_hours ?? '' })
}

function triggerLabel(trigger: string): string {
  return trigger === 'daily' ? uiText("日次の処理") : uiText("画面から")
}

async function runSweep() {
  const s = sweep.value
  if (!s || s.items.length === 0) return
  running.value = true
  notice.value = ''
  actionError.value = null
  try {
    const res = await settingsApi.runWorkSweep(s.items.map((it) => it.work_id))
    sweep.value = res
    notice.value =
      res.skipped_count > 0
        ? uiText("{value0}件を閉じました（{value1}件は浮きでなくなっていたので閉じていません）。", { value0: res.closed_ids.length, value1: res.skipped_count })
        : uiText("{value0}件を閉じました。", { value0: res.closed_ids.length })
  } catch (e) {
    actionError.value = asApiError(e)
  } finally {
    running.value = false
    confirming.value = false
  }
}
</script>

<template>
  <div class="tab">
    <div class="toolbar">
      <span v-if="sweep" class="muted fetched">{{ formatDateTime(sweep.checked_at) }} {{ $ui('に数えた') }}</span>
      <button type="button" class="secondary small" :disabled="loading" @click="load"> {{ $ui('数え直す') }} </button>
    </div>

    <p v-if="loadError" class="error" role="alert">{{ loadError.message }}</p>
    <p v-if="loading && !sweep" class="muted">{{ $ui('読み込み中…') }}</p>

    <section v-if="setting" class="block" data-testid="work-stale-setting">
      <h3>{{ $ui('浮きとみなす時間') }}</h3>
      <div v-if="setting.editable" class="setting-row">
        <select :value="currentHours" :aria-label="$ui('浮きとみなす時間')" @change="draftHours = ($event.target as HTMLSelectElement).value">
          <option v-for="v in setting.allowed ?? []" :key="v" :value="v">{{ hoursLabel(v) }}</option>
        </select>
        <button type="button" class="small" :disabled="!settingDirty || savingSetting" @click="saveSetting"> {{ $ui('保存') }} </button>
      </div>
      <p v-else>{{ hoursLabel(setting.value ?? '') }} <span class="muted">{{ pinnedReason }}</span></p>
      <p class="muted desc">{{ $ui('最後の活動からこの時間を過ぎた開いたままの作業記録を、日次の処理で不完全として閉じます。配分先のチケットがすべて完了した記録は、時間に関わらず対象です') }}</p>
    </section>

    <template v-if="sweep">
      <p class="last-run" data-testid="work-sweep-last-run">
        <template v-if="sweep.last_run">
          {{ $ui('最後の実行：') }}{{ formatDateTime(sweep.last_run.ran_at) }} {{ triggerLabel(sweep.last_run.trigger) }}<template v-if="sweep.last_run.actor_label">（{{ sweep.last_run.actor_label }}）</template> {{ $ui('{value0}件を閉じた', { value0: sweep.last_run.closed_count }) }}
        </template>
        <template v-else>{{ $ui('まだ実行されていません') }}</template>
      </p>

      <p v-if="notice" class="notice" role="status">{{ notice }}</p>
      <p v-if="actionError" class="error" role="alert">{{ actionError.message }}</p>

      <section class="block">
        <h3>{{ $ui('浮き（{value0}件）', { value0: sweep.items.length }) }}</h3>
        <p v-if="sweep.items.length === 0" class="muted" data-testid="work-sweep-empty">{{ $ui('浮きはありません') }}</p>
        <ul v-else class="items" data-testid="work-sweep-items">
          <li v-for="it in sweep.items" :key="it.work_id">
            <div class="line">
              <RouterLink :to="`/p/${it.project_key}/tickets/${it.ticket_seq}`" class="ticket">{{ it.project_key }} #{{ it.ticket_seq }}</RouterLink>
              <span class="title">{{ it.ticket_title }}</span>
              <span class="actor">{{ it.actor.display_name }}</span>
            </div>
            <div class="line muted">
              <span>{{ $ui('開始') }} {{ formatDateTime(it.started_at) }}</span>
              <span>{{ $ui('最後の活動') }} {{ formatDateTime(it.last_active_at) }}</span>
              <span class="reason">{{ reasonLabel(it) }}</span>
            </div>
          </li>
        </ul>
        <div v-if="sweep.items.length > 0" class="actions">
          <button type="button" class="danger" :disabled="running" @click="confirming = true">
            {{ $ui('{value0}件を片付ける', { value0: sweep.items.length }) }}
          </button>
        </div>
      </section>
    </template>

    <ConfirmDialog
      v-if="confirming"
      :title="$ui('浮きを片付けますか？')"
      :message="$ui('不完全として終了と同じ扱いで閉じます。最後の区間は作業時間に数えません。閉じた記録は開き直せません')"
      :confirm-label="$ui('片付ける')"
      danger
      :busy="running"
      @confirm="runSweep"
      @cancel="confirming = false"
    />
  </div>
</template>

<style scoped>
.tab {
  /* スクロールは親（AppSettingsPage の .page-body）が持つ */
  padding-block: var(--pb-space-3);
}
.toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: var(--pb-space-3);
  margin-bottom: var(--pb-space-3);
}
.fetched {
  font-size: 12px;
}
.muted {
  color: var(--pb-text-muted);
}
.error {
  color: var(--pb-danger-text);
  margin: 0 0 var(--pb-space-3);
}
.notice {
  margin: 0 0 var(--pb-space-3);
}
.block {
  margin-bottom: var(--pb-space-6);
}
.block h3 {
  font-size: 14px;
  margin: 0 0 var(--pb-space-2);
  border-bottom: 1px solid var(--pb-border);
  padding-bottom: var(--pb-space-1);
}
.setting-row {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  flex-wrap: wrap;
}
.desc {
  font-size: 12px;
  margin: var(--pb-space-1) 0 0;
}
.last-run {
  margin: 0 0 var(--pb-space-3);
}
.items {
  list-style: none;
  margin: 0;
  padding: 0;
}
.items li {
  padding: var(--pb-space-2) 0;
  border-bottom: 1px solid var(--pb-line);
}
.items li:last-child {
  border-bottom: 0;
}
.line {
  display: flex;
  flex-wrap: wrap;
  column-gap: var(--pb-space-3);
  row-gap: var(--pb-space-1);
  align-items: baseline;
  min-width: 0;
}
.line.muted {
  font-size: 12px;
}
.title {
  flex: 1 1 12em;
  min-width: 0;
  overflow-wrap: anywhere;
}
.ticket,
.actor {
  white-space: nowrap;
}
.actions {
  display: flex;
  justify-content: flex-end;
  margin-top: var(--pb-space-3);
}
</style>
