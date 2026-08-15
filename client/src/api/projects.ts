/**
 * プロジェクトのエンドポイント（`ApiDesign.md` 5.1〜5.3）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 *
 * 手順10a で使うのは一覧のみ。`check-key`（5.2）と作成（5.3）は
 * 新規作成モーダルとともに手順10b で足す。
 */
import { api } from './client'
import type { components } from './schema'

export type ProjectList = components['schemas']['ProjectList']
export type ProjectListItem = components['schemas']['ProjectListItem']

/** `status` の3値（`ApiDesign.md` 5.1）。未指定は `active` */
export type ProjectStatusFilter = 'active' | 'archived' | 'all'

/** ソート可能な列（5.1）。`closed_count` は**含まれない** */
export type ProjectSort = 'name' | 'key' | 'updated_at' | 'ticket_count' | 'progress'

export type SortOrder = 'asc' | 'desc'

export interface ListProjectsQuery {
  status?: ProjectStatusFilter
  sort?: ProjectSort
  order?: SortOrder
  page?: number
  per_page?: number
}

/**
 * プロジェクト一覧（`ApiDesign.md` 5.1）。
 *
 * 自分がメンバーであるプロジェクトが返る（アドミニストレータは全件）。
 * 件数・進捗も同じ応答に含まれるため、行ごとの追加リクエストは要らない。
 *
 * 既定値はサーバが持つ（`status=active` / `sort=updated_at` / `order=desc` /
 * `page=1` / `per_page=25`）。未指定の項目は送らない。
 */
export function listProjects(query: ListProjectsQuery = {}): Promise<ProjectList> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined) params.set(k, String(v))
  }
  const qs = params.toString()
  return api.get<ProjectList>(`/projects${qs === '' ? '' : `?${qs}`}`)
}
