/**
 * プロジェクトのエンドポイント（`ApiDesign.md` 5.1〜5.3）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 */
import { api } from './client'
import type { components } from './schema'

export type ProjectList = components['schemas']['ProjectList']
export type ProjectListItem = components['schemas']['ProjectListItem']
export type ProjectDetail = components['schemas']['ProjectDetail']
export type CheckKeyResult = components['schemas']['CheckKeyResult']
export type CreateProjectRequest = components['schemas']['CreateProjectRequest']

/** ワークフローテンプレート（`DbDesign.md` 7.4）。既定は `simple` */
export type WorkflowTemplate = CreateProjectRequest['workflow_template']

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

/**
 * プロジェクトキーの利用可否（`ApiDesign.md` 5.2）。
 *
 * 判定の順は 形式 → 予約語 → 既存。**使えない場合も 200 で返る**（エラーではない）。
 *
 * 形式と予約語の判定はサーバだけが持つ。**フロントに正規表現や予約語一覧を
 * 複製しない**（二重管理になり、片方だけ変わると食い違う）。
 *
 * この結果は作成時の重複検出には使わない（5.3。競合検出はDBの `UNIQUE` 制約）。
 */
export function checkProjectKey(key: string): Promise<CheckKeyResult> {
  return api.get<CheckKeyResult>(`/projects/check-key?key=${encodeURIComponent(key)}`)
}

/**
 * プロジェクトの作成（`ApiDesign.md` 5.3）。必要権限は `project.create`。
 *
 * 応答は `GET /projects/:key` と同形式（5.4）。キー重複は 409 `already_exists`。
 */
export function createProject(body: CreateProjectRequest): Promise<ProjectDetail> {
  return api.post<ProjectDetail>('/projects', body)
}
