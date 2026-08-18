/**
 * プロジェクトのエンドポイント（`ApiDesign.md` 5.1〜5.6）。
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
export type UpdateProjectRequest = components['schemas']['UpdateProjectRequest']
export type ProjectMember = components['schemas']['ProjectMember']
export type Workflow = components['schemas']['Workflow']

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

/**
 * プロジェクトの詳細（`ApiDesign.md` 5.4）。必要権限は `project.view`。
 *
 * **メンバーでなければ 404**（403 ではない。`Design.md` 6.4.5）。ワークフローも
 * メンバーも自分の実効権限も同じ応答に入るため、設定画面はこの1本で描ける。
 */
export function getProject(key: string): Promise<ProjectDetail> {
  return api.get<ProjectDetail>(`/projects/${encodeURIComponent(key)}`)
}

/**
 * プロジェクトの更新（`ApiDesign.md` 5.5）。必要権限は `project.edit`。
 *
 * **送るのは変更したフィールドだけ**（部分更新）。`version` は取得時の値を
 * `If-Match` に載せる。食い違えば 409 で、成功すると `version` が +1 される。
 *
 * `settings` だけは**丸ごと置き換え**になる。呼び出し側は取得した `settings` を
 * 保持し、変更するキーだけ差し替えて全体を渡すこと（`mergeRepositories`）。
 */
export function updateProject(
  key: string,
  version: number,
  body: UpdateProjectRequest,
): Promise<ProjectDetail> {
  return api.patch<ProjectDetail>(`/projects/${encodeURIComponent(key)}`, body, {
    // RFC 9110 8.8.3 の entity-tag。引用符で囲む（`ApiDesign.md` 5.5 の例）
    headers: { 'If-Match': `"${version}"` },
  })
}

/**
 * アーカイブ / 解除（`ApiDesign.md` 5.6）。必要権限は `project.archive`。
 *
 * 本文は取らず、`If-Match` も要らない（2.8）。**冪等**で、既にその状態なら
 * 何も変えずに 200 が返る。応答は 5.4 と同形式なので、`status` と `version` を
 * 1往復で更新できる。
 */
export function archiveProject(key: string): Promise<ProjectDetail> {
  return api.post<ProjectDetail>(`/projects/${encodeURIComponent(key)}/archive`)
}

export function unarchiveProject(key: string): Promise<ProjectDetail> {
  return api.post<ProjectDetail>(`/projects/${encodeURIComponent(key)}/unarchive`)
}

/**
 * `project.settings` の `repositories`（`DbDesign.md` 6.4）。
 *
 * **Phase 1 が `settings` に定義する唯一のキー**である。PBがこのURLを使って
 * 自動で何かを行うことはない。用途は画面のリンクと、MCP経由でエージェントが
 * プロジェクト情報として受け取ることの2つ（`GuiDesign.md` 5.9.1）。
 */
export interface ProjectRepository {
  url: string
  name?: string
  description?: string
}

/**
 * `settings` から `repositories` を取り出す。
 *
 * **サーバは `settings` の中身を検証しない**（JSONオブジェクトであることだけを
 * 見る。`ApiDesign.md` 5.5）。つまりここへ来る値は何でもありうるので、
 * 期待する形の要素だけを拾い、それ以外は黙って捨てる。画面が壊れるより、
 * 表示できるものを表示するほうがよい。
 */
export function readRepositories(settings: ProjectDetail['settings']): ProjectRepository[] {
  const raw = settings['repositories']
  if (!Array.isArray(raw)) return []
  const items: ProjectRepository[] = []
  for (const e of raw) {
    if (typeof e !== 'object' || e === null) continue
    const rec = e as Record<string, unknown>
    if (typeof rec['url'] !== 'string' || rec['url'] === '') continue
    items.push({
      url: rec['url'],
      name: typeof rec['name'] === 'string' ? rec['name'] : undefined,
      description: typeof rec['description'] === 'string' ? rec['description'] : undefined,
    })
  }
  return items
}

/**
 * `repositories` だけを差し替えた `settings` を作る。
 *
 * **`PATCH` の `settings` は丸ごと置き換わる**（5.5）。取得した `settings` を
 * 土台にしないと、画面が知らないキー（Phase 2 の設定など）が消える。
 *
 * 空の項目は落とし、`name` / `description` は値があるときだけ入れる
 * （空文字を保存すると、次に読んだとき「設定された空の名前」と区別できない）。
 */
export function mergeRepositories(
  settings: ProjectDetail['settings'],
  repositories: ProjectRepository[],
): ProjectDetail['settings'] {
  const cleaned = repositories
    .map((r) => ({
      url: r.url.trim(),
      name: r.name?.trim() ?? '',
      description: r.description?.trim() ?? '',
    }))
    .filter((r) => r.url !== '')
    .map((r) => {
      const out: ProjectRepository = { url: r.url }
      if (r.name !== '') out.name = r.name
      if (r.description !== '') out.description = r.description
      return out
    })
  return { ...settings, repositories: cleaned }
}
