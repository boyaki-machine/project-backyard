/**
 * タイムラインが使う口（`ApiDesign.md` 9.2.7・9.17）。**公開された REST だけを使う**
 * （`GuiDesign.md` 5.15「プラグインの境界」）——型は `openapi.yaml` から生成した `schema.d.ts`。
 */
import { api } from '../../api/client'
import type { ConditionalResult } from '../../api/client'
import type { Baseline, BaselineSummary, RescheduleRequest, RescheduleResult, TlList } from './model'
import type { components } from '../../api/schema'

const base = (key: string) => `/projects/${encodeURIComponent(key)}`

/** タイムラインの取得の条件（URL のクエリから作る。9.2.1 と同じ形） */
export interface TimelineQuery {
  parent?: string
  assignee?: string
  status?: string
  status_category?: string
  open?: string
  tag?: string
}

export function timelinePath(key: string, q: TimelineQuery): string {
  const params = new URLSearchParams({ view: 'timeline' })
  for (const [k, v] of Object.entries(q)) if (typeof v === 'string' && v !== '') params.set(k, v)
  return `${base(key)}/tickets?${params.toString()}`
}

/** 起動時と5秒ごとの定期取得。`If-None-Match` で `304`（9.2.5） */
export function getTimeline(key: string, q: TimelineQuery, etag: string | null): Promise<ConditionalResult<TlList>> {
  return api.getConditional<TlList>(timelinePath(key, q), etag)
}

/** 連鎖のずらし（9.17.2）。`If-Match` はきっかけの `version` */
export function reschedule(key: string, seq: number, version: number, body: RescheduleRequest): Promise<RescheduleResult> {
  return api.post<RescheduleResult>(`${base(key)}/tickets/${seq}/reschedule`, body, {
    headers: { 'If-Match': `"${version}"` },
  })
}

export function listBaselines(key: string): Promise<components['schemas']['ScheduleBaselineList']> {
  return api.get(`${base(key)}/schedule-baselines`)
}

/** 版の中身。**書き換わらないので、画面は1回取れば取り直さない**（9.17.3） */
export function getBaseline(key: string, id: string): Promise<Baseline> {
  return api.get<Baseline>(`${base(key)}/schedule-baselines/${encodeURIComponent(id)}`)
}

export function createBaseline(key: string, label: string | null): Promise<BaselineSummary> {
  return api.post<BaselineSummary>(`${base(key)}/schedule-baselines`, { label })
}

export function deleteBaseline(key: string, id: string): Promise<void> {
  return api.del<void>(`${base(key)}/schedule-baselines/${encodeURIComponent(id)}`)
}

/**
 * 依存を1本作る（`ApiDesign.md` 9.10.1。5.15「依存を引く」。pb-332）。`seq` が先行（`source`）。
 * ずらしはドラッグでは入れない（0。詳細の関連チケットの行で変える）
 */
export function createDependency(key: string, seq: number, targetSeq: number, linkType: 'FS' | 'SS' | 'FF' | 'SF'): Promise<components['schemas']['TicketLink']> {
  return api.post(`${base(key)}/tickets/${seq}/links`, { target_seq: targetSeq, link_type: linkType, lag_days: 0, lag_seconds: 0 })
}
