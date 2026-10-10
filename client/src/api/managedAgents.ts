import { api } from './client'
import type { components } from './schema'
export type ManagedAgent = components['schemas']['ManagedAgent']
export type ManagedAgentList = components['schemas']['ManagedAgentList']
export function listManagedAgents(key:string,page=1):Promise<ManagedAgentList>{return api.get(`/projects/${encodeURIComponent(key)}/agents?page=${page}`)}
export function disableManagedAgent(key:string,id:string):Promise<void>{return api.post(`/projects/${encodeURIComponent(key)}/agents/${encodeURIComponent(id)}/disable`,{})}
