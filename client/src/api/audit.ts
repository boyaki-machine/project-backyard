import { api } from './client'
import type { components } from './schema'

export type AuditLogList = components['schemas']['AuditLogList']
export type AuditLogItem = components['schemas']['AuditLogItem']

export interface AuditFilters {
  from_at?: number
  to_at?: number
  action?: string
  actor?: string
  result?: 'success' | 'failure'
  q?: string
}

export function queryString(filters: AuditFilters): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(filters)) {
    if (value !== undefined && value !== '') params.set(key, String(value))
  }
  return params.toString()
}

export function listAuditLogs(filters: AuditFilters, page: number, perPage: number): Promise<AuditLogList> {
  const params = queryString(filters)
  return api.get<AuditLogList>(`/admin/audit?${params ? `${params}&` : ''}page=${page}&per_page=${perPage}`)
}

/** 一覧と同じ絞り込みの全件。CSV は JSON ではないため共通の api.get を通さない。 */
export async function exportAuditLogs(filters: AuditFilters): Promise<Blob> {
  const qs = queryString(filters)
  return api.getBlob(`/admin/audit.csv${qs ? `?${qs}` : ''}`)
}
