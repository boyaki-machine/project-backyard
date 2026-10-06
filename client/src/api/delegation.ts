/**
 * 自律実行（委任）のエンドポイント（`docs/design/Delegation.md`）。pb-252。
 *
 * 画面（`GuiDesign.md` 5.5「自律実行」）は、状態を `delegation-status` の1本で
 * 読み、操作のたびにそれを読み直す。**委任・計画・評価・確認待ちはそれぞれ GET を
 * 持たない**（書き込みの口だけ）——集約の1本が画面の材料をまとめて返す。
 *
 * **画面に設計の語を出さない**（pb-252 で PM と合意）。委任の根・委任範囲は出さず、
 * 状態や操作は下の `delegationStatusLabels` などの語で出す。
 */
import { api } from './client'
import type { components } from './schema'
import { uiText } from '../locales/ui'

export type DelegationStatus = components['schemas']['DelegationStatus']
export type Delegation = components['schemas']['Delegation']
export type DelegationBudget = components['schemas']['DelegationBudget']
export type DelegationState = NonNullable<DelegationStatus['status']>
export type OpenConfirmation = NonNullable<DelegationStatus['open_confirmations']>[number]
export type PlanHistoryItem = components['schemas']['DelegationPlanHistoryItem']
export type EvaluationHistoryItem = components['schemas']['DelegationEvaluationHistoryItem']
export type DelegationMetric = components['schemas']['DelegationMetric']
export type DelegationMetricValue = components['schemas']['DelegationMetricValue']
export type DelegationSummary = components['schemas']['DelegationSummary']

/** 状態の語（pb-252 の合意）。 */
export const delegationStatusLabels: Record<DelegationState, string> = {
  get planning() { return uiText('計画中') },
  get approved() { return uiText('実行中') },
  get paused() { return uiText('一時停止') },
  get ended() { return uiText('終了') },
}

/** ユーザ確認待ちの理由の語（`Delegation.md` 8.1）。 */
export const confirmationReasonLabels: Record<string, string> = {
  get scope_change() { return uiText('範囲の外への変更') },
  get irreversible_external() { return uiText('後戻りできない操作') },
  get charter_change() { return uiText('憲章の変更') },
  get sensitive_area() { return uiText('機微な領域') },
  get budget_exceeded() { return uiText('制約を超えたため一時停止しました') },
  get evaluation_unable() { return uiText('評価エージェントが判断できませんでした') },
  get checkpoint() { return uiText('チェックポイント') },
  get hearing() { return uiText('ヒアリング') },
}

/** 評価の観点の語（`Delegation.md` 7.2）。 */
export const evaluationAspectLabels: Record<string, string> = {
  get purpose_and_dod() { return uiText('目的・完了条件') },
  get values() { return uiText('価値観') },
}

/** 評価の判定の語。 */
export const evaluationVerdictLabels: Record<string, string> = {
  get pass() { return uiText('合格') },
  get fail() { return uiText('不合格') },
  get unable() { return uiText('判断できない') },
}

/** 計画の版の状態の語（`Delegation.md` 4.4・6.2。pb-274）。 */
export const planStatusLabels: Record<string, string> = {
  get proposed() { return uiText('判定待ち') },
  get accepted() { return uiText('反映済み') },
  get rejected() { return uiText('却下') },
  get superseded() { return uiText('置き換え済み') },
  get reverted() { return uiText('差し戻し済み') },
}

/** 計画の変更の段階の語（`Delegation.md` 6.3）。 */
export const planClassLabels: Record<string, string> = {
  get minor() { return uiText('軽微') },
  get structural() { return uiText('構造') },
  get scope() { return uiText('範囲') },
}

/** 範囲（scope）のうち、画面が読み書きする制約（`Delegation.md` 4.2・12.1）。 */
export interface DelegationConstraints {
  budget: DelegationBudget
  stop_conditions: string[]
}

/** scope から制約を読む。読めない項目は空にする。 */
export function constraintsOf(scope: unknown): DelegationConstraints {
  const s = (scope ?? {}) as { budget?: DelegationBudget; stop_conditions?: unknown }
  const stops = Array.isArray(s.stop_conditions)
    ? s.stop_conditions.filter((c): c is string => typeof c === 'string')
    : []
  return { budget: { ...(s.budget ?? {}) }, stop_conditions: stops }
}

/**
 * エージェントをセッションに呼ぶ一言（pb-252。文面は pb-278・pb-279 と揃える）。
 * **Claude Code だけがスラッシュコマンドを持つ**。他の種別は一文で頼む
 * （PB の文書「エージェントの参画情報」）。最初も、強制終了の後の呼び直しも同じ一言。
 */
export function agentCallPhrase(clientKind: string, role: 'main' | 'evaluator', seq: number): string {
  if (clientKind === 'claude_code') {
    return role === 'main' ? `/pb-delegate-main ${seq}` : `/pb-delegate-eval ${seq}`
  }
  return role === 'main'
    ? uiText('PB のチケット {value0} を自律実行の main として進めて', { value0: seq })
    : uiText('PB のチケット {value0} を評価エージェントとして見て', { value0: seq })
}

function base(key: string, seq: number): string {
  return `/projects/${encodeURIComponent(key)}/tickets/${seq}`
}

export function getDelegationStatus(key: string, seq: number): Promise<DelegationStatus> {
  return api.get<DelegationStatus>(`${base(key, seq)}/delegation-status`)
}

export function createDelegation(
  key: string,
  seq: number,
  body: { main_actor_id: string; evaluator_actor_id: string; scope: Record<string, unknown> },
): Promise<Delegation> {
  return api.post<Delegation>(`${base(key, seq)}/delegation`, body)
}

export function approveDelegation(key: string, seq: number, planVersion: number): Promise<Delegation> {
  return api.post<Delegation>(`${base(key, seq)}/delegation/approve`, { plan_version: planVersion })
}

export function rejectDelegation(
  key: string,
  seq: number,
  planVersion: number,
  reasonMd: string,
): Promise<Delegation> {
  return api.post<Delegation>(`${base(key, seq)}/delegation/reject`, {
    plan_version: planVersion,
    reason_md: reasonMd,
  })
}

export function pauseDelegation(key: string, seq: number, reasonMd: string): Promise<Delegation> {
  return api.post<Delegation>(`${base(key, seq)}/delegation/pause`, { reason_md: reasonMd })
}

export function resumeDelegation(
  key: string,
  seq: number,
  body: { budget?: DelegationBudget; stop_conditions?: string[]; reason_md?: string },
): Promise<Delegation> {
  return api.post<Delegation>(`${base(key, seq)}/delegation/resume`, body)
}

/** 自律実行をやめて、従来の実行モードへ戻す（Delegation.md 4.3「取り下げ」。pb-281） */
export function withdrawDelegation(
  key: string,
  seq: number,
  body: { execution_mode: 'human_only' | 'agent_draft' | 'agent_only'; reason_md?: string },
): Promise<Delegation> {
  return api.post<Delegation>(`${base(key, seq)}/delegation/withdraw`, body)
}

/** 計画の版の一覧（新しい順。pb-274） */
export async function listDelegationPlans(key: string, seq: number): Promise<PlanHistoryItem[]> {
  return (await api.get<{ items: PlanHistoryItem[] }>(`${base(key, seq)}/plans`)).items
}

/** 評価記録の一覧（新しい順。pb-274） */
export async function listDelegationEvaluations(key: string, seq: number): Promise<EvaluationHistoryItem[]> {
  return (await api.get<{ items: EvaluationHistoryItem[] }>(`${base(key, seq)}/evaluations`)).items
}

/** 前の版へ差し戻す（`Delegation.md` 6.2。pb-247） */
export function revertDelegationPlan(key: string, seq: number, version: number, reasonMd: string): Promise<unknown> {
  return api.post(`${base(key, seq)}/plan/${version}/revert`, { reason_md: reasonMd })
}

/** 評価が合格させた構造の変更に異議を出す（`Delegation.md` 6.4。pb-275） */
export function objectDelegationPlan(
  key: string,
  seq: number,
  version: number,
  reasonMd: string,
): Promise<{ reverted: boolean; later_version?: number }> {
  return api.post(`${base(key, seq)}/plan/${version}/object`, { reason_md: reasonMd })
}

/** scope から指標の定義を読む（`Delegation.md` 4.2。pb-277）。読めなければ空 */
export function metricsOf(scope: unknown): DelegationMetric[] {
  const m = (scope as { metrics?: unknown } | null)?.metrics
  return Array.isArray(m) ? (m as DelegationMetric[]) : []
}

/** 指標の値を入れる（pb-277） */
export function recordDelegationMetric(
  key: string,
  seq: number,
  metric: string,
  value: number,
  noteMd: string,
): Promise<DelegationMetricValue> {
  return api.post<DelegationMetricValue>(`${base(key, seq)}/metrics/${encodeURIComponent(metric)}/values`, {
    value,
    note_md: noteMd,
  })
}

/** 周期の要約（期間を省くと直近の周期。pb-277） */
export function getDelegationSummary(key: string, seq: number): Promise<DelegationSummary> {
  return api.get<DelegationSummary>(`${base(key, seq)}/summary`)
}

export function answerConfirmation(
  key: string,
  seq: number,
  confirmationID: string,
  answerMd: string,
): Promise<unknown> {
  return api.post(`${base(key, seq)}/confirmation/${encodeURIComponent(confirmationID)}/answer`, {
    answer_md: answerMd,
  })
}
