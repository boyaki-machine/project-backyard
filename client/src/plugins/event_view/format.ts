/**
 * タイムラインの文言の組み立て（`GuiDesign.md` 5.15）。数と時刻は `model.ts` / `time.ts` が作り、
 * ここで訳した文に入れる。
 */
import { uiText } from '../../locales/ui'
import type { BarState, BaselineSummary } from './model'
import { HOUR, MIN, SEC } from './time'

/** 長さ（`8分`・`1時間20分`・`45秒`） */
export function fmtDur(ms: number): string {
  const d = Math.round(Math.abs(ms) / SEC)
  if (d < 60) return uiText('{value0}秒', { value0: d })
  const h = Math.floor((d * SEC) / HOUR)
  const m = Math.floor(((d * SEC) % HOUR) / MIN)
  if (h === 0) return uiText('{value0}分', { value0: m })
  if (m === 0) return uiText('{value0}時間', { value0: h })
  return uiText('{value0}時間{value1}分', { value0: h, value1: m })
}

/** 符号付きの長さ（`+20分`・`−5分`） */
export function fmtSigned(ms: number): string {
  if (Math.abs(ms) < SEC) return '±0'
  return `${ms > 0 ? '+' : '−'}${fmtDur(ms)}`
}

/** 状態の札 */
export function stateLabel(state: BarState): string {
  switch (state) {
    case 'todo':
      return uiText('未着手')
    case 'wait':
      return uiText('開始待ち')
    case 'prog':
      return uiText('進行中')
    case 'late':
      return uiText('押している')
    case 'done':
      return uiText('完了')
    case 'plan':
      return uiText('計画')
  }
}

/** 版の名前（`10/24 08:30 保存 当日朝`）。`stamp` は `model.ts` の `baselineStamps` */
export function baselineName(stamp: string, b: Pick<BaselineSummary, 'label'>): string {
  const head = uiText('{value0} 保存', { value0: stamp })
  return b.label ? `${head} ${b.label}` : head
}

/** 保存した人を添えた名前（`10/24 08:30 保存 当日朝（鈴木）`） */
export function baselineFullName(stamp: string, b: Pick<BaselineSummary, 'label' | 'created_by'>): string {
  return uiText('{value0}（{value1}）', { value0: baselineName(stamp, b), value1: b.created_by.display_name })
}

/** 題名を短く切る（通知の行に入れるとき） */
export function shortTitle(s: string, n = 14): string {
  const chars = [...s]
  return chars.length > n ? `${chars.slice(0, n).join('')}…` : s
}
