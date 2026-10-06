<script setup lang="ts">
/**
 * 指標と要約の周期の入力（GuiDesign.md 5.5「自律実行」、`Delegation.md` 4.2・8.3。pb-277）。
 *
 * **始めるときだけ使う。** 指標は PM が承認する制約の一部で、始めた後に変えるのは範囲の変更
 * （main が計画の版で出し、PM が承認する）。key は画面に出さず、足した順に `m1` `m2` … を振る
 * （サーバは英小文字・数字・_ だけを受ける）。名前が空の行は保存の前に落とす。
 */
import { computed } from 'vue'

import type { DelegationMetric } from '../api/delegation'

export interface MetricsDraft {
  metrics: DelegationMetric[]
  /** 空文字は周期を持たない */
  report_every_days: string
}

const model = defineModel<MetricsDraft>({ required: true })

defineProps<{ disabled?: boolean }>()

/** 周期も新しいオブジェクトで返す（prop の中身を直に書き換えない） */
const period = computed({
  get: () => model.value.report_every_days,
  set: (v: string | number) => {
    model.value = { ...model.value, report_every_days: String(v ?? '') }
  },
})

function update(i: number, patch: Partial<DelegationMetric>): void {
  const list = model.value.metrics.map((m, j) => (j === i ? { ...m, ...patch } : m))
  model.value = { ...model.value, metrics: list }
}

function setTarget(i: number, raw: string): void {
  const n = raw.trim() === '' ? undefined : Number(raw)
  update(i, { target: n === undefined || Number.isNaN(n) ? undefined : n })
}

function add(): void {
  const used = new Set(model.value.metrics.map((m) => m.key))
  let n = model.value.metrics.length + 1
  while (used.has(`m${n}`)) n++
  model.value = { ...model.value, metrics: [...model.value.metrics, { key: `m${n}`, name: '', direction: 'higher' }] }
}

function remove(i: number): void {
  model.value = { ...model.value, metrics: model.value.metrics.filter((_, j) => j !== i) }
}
</script>

<template>
  <fieldset class="metrics" :disabled="disabled">
    <legend class="legend">{{ $ui('指標と要約') }}</legend>
    <p class="hint">{{ $ui('目的に近づいているかの測り方です。値はあなたか main エージェントが入れ、周期ごとの要約に推移が出ます') }}</p>
    <ul class="list">
      <li v-for="(m, i) in model.metrics" :key="m.key" class="row">
        <input
          type="text"
          maxlength="100"
          :value="m.name"
          :aria-label="$ui('指標 {value0} の名前', { value0: i + 1 })"
          :placeholder="$ui('例：模試の偏差値')"
          @input="update(i, { name: ($event.target as HTMLInputElement).value })"
        />
        <label class="small">
          {{ $ui('目標') }}
          <input
            type="number"
            :value="m.target ?? ''"
            :aria-label="$ui('指標 {value0} の目標', { value0: i + 1 })"
            @input="setTarget(i, ($event.target as HTMLInputElement).value)"
          />
        </label>
        <input
          type="text"
          class="unit"
          maxlength="20"
          :value="m.unit ?? ''"
          :aria-label="$ui('指標 {value0} の単位', { value0: i + 1 })"
          :placeholder="$ui('単位')"
          @input="update(i, { unit: ($event.target as HTMLInputElement).value })"
        />
        <select
          :value="m.direction"
          :aria-label="$ui('指標 {value0} の良い向き', { value0: i + 1 })"
          @change="update(i, { direction: ($event.target as HTMLSelectElement).value as 'higher' | 'lower' })"
        >
          <option value="higher">{{ $ui('高いほど良い') }}</option>
          <option value="lower">{{ $ui('低いほど良い') }}</option>
        </select>
        <button type="button" class="icon" :aria-label="$ui('指標 {value0} を消す', { value0: i + 1 })" @click="remove(i)">✕</button>
      </li>
    </ul>
    <button v-if="model.metrics.length < 10" type="button" class="link" @click="add">{{ $ui('+ 指標を足す') }}</button>
    <label class="period">
      <span class="name">{{ $ui('要約の周期') }}</span>
      <input
        v-model="period"
        type="number"
        min="1"
        max="365"
        inputmode="numeric"
        :placeholder="$ui('なし')"
      />
      <span class="unit-text">{{ $ui('日ごと') }}</span>
    </label>
    <p class="hint">{{ $ui('周期が過ぎると PB が要約を作り、あなたに確認を求めます。回答するまでエージェントは止まります') }}</p>
  </fieldset>
</template>

<style scoped>
.metrics {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  margin: 0;
  padding: 0;
  border: 0;
}

.legend {
  padding: 0;
  font-size: 13px;
  font-weight: 600;
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 12px;
}

.list {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--pb-space-2);
}

.row > input[type='text']:first-child {
  flex: 1 1 160px;
  min-width: 0;
}

.small {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
  font-size: 13px;
}

.small input {
  width: 72px;
}

.unit {
  width: 64px;
}

.period {
  display: flex;
  align-items: center;
  gap: var(--pb-space-2);
  font-size: 13px;
}

.period input {
  width: 72px;
}

.name {
  color: var(--pb-text-muted);
}

input,
select {
  padding: 2px var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
}

.icon,
.link {
  border: 0;
  background: none;
  color: var(--pb-text-muted);
  font: inherit;
  font-size: 13px;
  cursor: pointer;
}

.link {
  align-self: flex-start;
  padding: 0;
}
</style>
