<script setup lang="ts">
/**
 * 自律実行の制約の入力（GuiDesign.md 5.5「自律実行」、pb-252）。
 *
 * 始めるとき（DelegationPanel）と、再開のとき（DelegationResumeModal）で同じ部品を使う。
 *
 * **画面で扱うのは PB が数えられる4つと、止まる条件だけである。** トークンの上限は
 * 出さない——エージェントが自分の消費を正確に知れず、上限として機能しないため
 * （pb-252 で PM と合意）。`budget` の他のキー（`tokens` など）は触らずに持ち回る。
 *
 * 止まる条件は複数持てる（追加・削除できるリスト）。**空の行は保存の前に落とす**
 * ——サーバは空の条件を 422 で拒む。
 */
import { computed } from 'vue'

import type { DelegationConstraints } from '../api/delegation'

const model = defineModel<DelegationConstraints>({ required: true })

defineProps<{ disabled?: boolean }>()

type CountKey = 'tickets' | 'depth' | 'rounds'

function countOf(key: CountKey): string {
  const v = model.value.budget[key]
  return v === undefined || v === null ? '' : String(v)
}

/** 空欄は上限なし（キーごと消す）。1未満・小数は入力の段階で弾かず、サーバの 422 に任せる */
function setCount(key: CountKey, raw: string): void {
  const budget = { ...model.value.budget }
  const n = raw.trim() === '' ? NaN : Number(raw)
  if (Number.isNaN(n)) delete budget[key]
  else budget[key] = n
  model.value = { ...model.value, budget }
}

const dueOn = computed({
  get: () => model.value.budget.due_on ?? '',
  set: (v: string) => {
    const budget = { ...model.value.budget }
    if (v === '') delete budget.due_on
    else budget.due_on = v
    model.value = { ...model.value, budget }
  },
})

function setStop(i: number, v: string): void {
  const list = [...model.value.stop_conditions]
  list[i] = v
  model.value = { ...model.value, stop_conditions: list }
}

function addStop(): void {
  model.value = { ...model.value, stop_conditions: [...model.value.stop_conditions, ''] }
}

function removeStop(i: number): void {
  const list = model.value.stop_conditions.filter((_, j) => j !== i)
  model.value = { ...model.value, stop_conditions: list }
}
</script>

<template>
  <fieldset class="constraints" :disabled="disabled">
    <legend class="legend">{{ $ui('制約') }}</legend>
    <div class="limits">
      <label class="limit">
        <span class="name">{{ $ui('子チケットの数') }}</span>
        <input
          type="number"
          min="1"
          inputmode="numeric"
          :value="countOf('tickets')"
          :placeholder="$ui('上限なし')"
          @input="setCount('tickets', ($event.target as HTMLInputElement).value)"
        />
        <span class="unit">{{ $ui('件まで') }}</span>
      </label>
      <label class="limit">
        <span class="name">{{ $ui('子チケットの階層') }}</span>
        <input
          type="number"
          min="1"
          inputmode="numeric"
          :value="countOf('depth')"
          :placeholder="$ui('上限なし')"
          @input="setCount('depth', ($event.target as HTMLInputElement).value)"
        />
        <span class="unit">{{ $ui('段まで') }}</span>
      </label>
      <label class="limit">
        <span class="name">{{ $ui('期日') }}</span>
        <input v-model="dueOn" type="date" />
      </label>
      <label class="limit">
        <span class="name">{{ $ui('評価のやり直し') }}</span>
        <input
          type="number"
          min="1"
          inputmode="numeric"
          :value="countOf('rounds')"
          placeholder="3"
          @input="setCount('rounds', ($event.target as HTMLInputElement).value)"
        />
        <span class="unit">{{ $ui('回まで') }}</span>
      </label>
    </div>
    <p class="hint">{{ $ui('空欄は上限なしです（評価のやり直しは3回）。超えると PB が自律実行を一時停止し、あなたに確認を求めます') }}</p>

    <div class="stops">
      <span class="name">{{ $ui('止まる条件') }}</span>
      <ul class="stop-list">
        <li v-for="(c, i) in model.stop_conditions" :key="i" class="stop">
          <input
            type="text"
            :value="c"
            maxlength="500"
            :aria-label="$ui('止まる条件 {value0}', { value0: i + 1 })"
            :placeholder="$ui('例：模試の判定が2回続けて下がったとき')"
            @input="setStop(i, ($event.target as HTMLInputElement).value)"
          />
          <button
            type="button"
            class="icon"
            :aria-label="$ui('止まる条件 {value0} を消す', { value0: i + 1 })"
            @click="removeStop(i)"
          >
            ✕
          </button>
        </li>
      </ul>
      <button
        v-if="model.stop_conditions.length < 20"
        type="button"
        class="link"
        @click="addStop"
      >
        {{ $ui('+ 止まる条件を足す') }}
      </button>
      <p class="hint">{{ $ui('エージェントが読んで守ります。当たったら作業を止めて、あなたに確認を求めます') }}</p>
    </div>
  </fieldset>
</template>

<style scoped>
.constraints {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-2);
  min-width: 0;
  padding: 0;
  margin: 0;
  border: none;
}

.legend {
  padding: 0;
  margin-bottom: var(--pb-space-1);
  color: var(--pb-text-muted);
  font-size: 13px;
}

.limits {
  display: flex;
  flex-wrap: wrap;
  gap: var(--pb-space-2) var(--pb-space-4);
}

.limit {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
  font-size: 13px;
  white-space: nowrap;
}

.limit input[type='number'] {
  width: 7em;
}

.name {
  color: var(--pb-text-muted);
  font-size: 13px;
}

.unit {
  color: var(--pb-text-muted);
}

.stops {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  min-width: 0;
}

.stop-list {
  display: flex;
  flex-direction: column;
  gap: var(--pb-space-1);
  padding: 0;
  margin: 0;
  list-style: none;
}

.stop {
  display: flex;
  align-items: center;
  gap: var(--pb-space-1);
  min-width: 0;
}

.stop input {
  flex: 1;
  min-width: 0;
}

.icon {
  flex: none;
  padding: 0 var(--pb-space-1);
  border: none;
  background: none;
  color: var(--pb-text-muted);
  cursor: pointer;
}

.link {
  align-self: flex-start;
  padding: 0;
  border: none;
  background: none;
  color: var(--pb-accent);
  font-size: 13px;
  cursor: pointer;
}

.hint {
  margin: 0;
  color: var(--pb-text-muted);
  font-size: 12px;
}

input {
  padding: var(--pb-space-1) var(--pb-space-2);
  border: 1px solid var(--pb-border);
  border-radius: var(--pb-radius);
  background: var(--pb-bg);
  color: inherit;
  font: inherit;
  font-size: 13px;
}
</style>
