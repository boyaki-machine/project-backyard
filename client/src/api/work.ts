import { api } from './client'
import type { components } from './schema'
export type WorkAllocation = components['schemas']['WorkAllocation']
export type WorkSession = components['schemas']['WorkSession']
export type WorkSummary = components['schemas']['WorkSummary']
export type WorkList = components['schemas']['WorkSessionList']
export interface WorkEvent {
 request_id: string; action: 'correct' | 'restore_auto' | 'finish'; note: string; incomplete?: boolean
 reported_active_ms?: number; tokens?: number; result?: string; allocations?: WorkAllocation[]
}
const path = (key: string, seq: number) => `/projects/${encodeURIComponent(key)}/tickets/${seq}/work-sessions`
export const listWork = (key: string, seq: number, page = 1) => api.get<WorkList>(`${path(key, seq)}?page=${page}&per_page=10`)
export const recordWork = (key: string, seq: number, id: string, event: WorkEvent) => api.post<WorkSession>(`${path(key, seq)}/${encodeURIComponent(id)}/events`, event)
