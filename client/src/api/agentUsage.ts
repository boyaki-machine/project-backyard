import { api } from './client'
import type { components } from './schema'
export type AgentUsage = components['schemas']['AgentUsage']
export type AgentUsageRunList = components['schemas']['AgentUsageRunList']
export interface UsageQuery { from_date?: string; to_date?: string; client_kind?: string; model_name?: string; model_version?: string; page?: number; per_page?: number }
function queryString(query: UsageQuery): string {
  const params = new URLSearchParams()
  for (const [key, value] of Object.entries(query)) if (value !== undefined && value !== '') params.set(key, String(value))
  return params.toString()
}
export function getAgentUsage(key: string, query: UsageQuery = {}): Promise<AgentUsage> {
  return api.get(`/projects/${encodeURIComponent(key)}/agent-usage?${queryString(query)}`)
}
export function listAgentRuns(key: string, query: UsageQuery = {}): Promise<AgentUsageRunList> {
  return api.get(`/projects/${encodeURIComponent(key)}/agent-usage/runs?${queryString(query)}`)
}
