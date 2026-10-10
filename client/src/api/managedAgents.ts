import { api } from './client'
import type { components } from './schema'
export type ManagedAgent = components['schemas']['ManagedAgent']
export type ManagedAgentList = components['schemas']['ManagedAgentList']
export function listManagedAgents(key:string,page=1):Promise<ManagedAgentList>{return api.get(`/projects/${encodeURIComponent(key)}/agents?page=${page}`)}
export function disableManagedAgent(key:string,id:string):Promise<void>{return api.post(`/projects/${encodeURIComponent(key)}/agents/${encodeURIComponent(id)}/disable`,{})}

export type MonitorState=components['schemas']['MonitorState']
export type MonitorLimits=components['schemas']['MonitorLimits']
export const getMonitorLimits=(key:string)=>api.get<MonitorLimits>(`/projects/${encodeURIComponent(key)}/agent-monitor`)
export const setMonitorLimits=(key:string,limits:MonitorLimits)=>api.put<MonitorLimits>(`/projects/${encodeURIComponent(key)}/agent-monitor`,limits)
export const getMonitor=(id:string,key?:string)=>api.get<MonitorState>(key?`/projects/${encodeURIComponent(key)}/agents/${id}/monitor`:`/me/agents/${id}/monitor`)
export const resetMonitor=(id:string,reason:string,key?:string)=>api.post<MonitorState>(key?`/projects/${encodeURIComponent(key)}/agents/${id}/monitor/reset`:`/me/agents/${id}/monitor`,{reason})
export const saveAttributes=(id:string,trust_level:number,capabilities:string[],key?:string)=>api.patch(key?`/projects/${encodeURIComponent(key)}/agents/${id}/attributes`:`/me/agents/${id}`,{trust_level,capabilities})
