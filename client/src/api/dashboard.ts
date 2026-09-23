/**
 * ダッシュボードのエンドポイント（`ApiDesign.md` 9.13）。
 *
 * 消費者は2つある——プロジェクトダッシュボード（`GuiDesign.md` 5.3）と、
 * チケット詳細の「履歴」セクション（同 5.5）。後者は `entity` を付けるだけで、
 * 応答の形は変わらない。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 */
import { api } from './client'
import type { components } from './schema'

export type ProjectStats = components['schemas']['ProjectStats']
export type Activity = components['schemas']['Activity']
export type ActivityList = components['schemas']['ActivityList']

/** `activity.action` の4値（9.13.2、`DbDesign.md` 6.8 の CHECK） */
export type ActivityAction = Activity['action']

/**
 * プロジェクトの集計（9.13.1）。必要権限は `project.view`。
 *
 * **`ETag` を返さない**（9.13.1）。ページャを持たず、材料を採る走査が本体の
 * 集計とほぼ同じなので、付けても DB の仕事が減らないためである。
 *
 * **エピックはどの項目にも入らない**（手順19b）。4枚のカードは押すと
 * バックログをそのカテゴリで絞って開くので、除かないと数が一致しない。
 */
export function getProjectStats(key: string): Promise<ProjectStats> {
  return api.get<ProjectStats>(`/projects/${encodeURIComponent(key)}/stats`)
}

/** 業務履歴のクエリ（9.13.2）。並び順は `occurred_at DESC, id DESC` で固定 */
export interface ListActivityQuery {
  /** `ticket:31` の形。省略時はプロジェクト全体（ダッシュボードの「最近の動き」） */
  entity?: string
  /** **単一値のみ。** 9.2.1 のフィルタ群と違いカンマ区切りの OR を受け付けない */
  action?: ActivityAction
  page?: number
  per_page?: number
}

/**
 * 業務履歴（9.13.2）。必要権限は `project.view`。
 *
 * **`sort` / `order` は送れない。** 並び順は固定で、`order` を付けると 422 になる。
 *
 * **存在しない `seq` を指しても 404 にはならず、空の一覧が 200 で返る**——
 * `entity` は資源の指定ではなくフィルタだからである。
 */
export function listActivity(key: string, query: ListActivityQuery = {}): Promise<ActivityList> {
  const params = new URLSearchParams()
  for (const [k, v] of Object.entries(query)) {
    if (v !== undefined && v !== '') params.set(k, String(v))
  }
  const qs = params.toString()
  return api.get<ActivityList>(
    `/projects/${encodeURIComponent(key)}/activity${qs === '' ? '' : `?${qs}`}`,
  )
}

/** チケット1件の履歴を引くための `entity` 値（9.13.2） */
export function ticketEntity(seq: number): string {
  return `ticket:${seq}`
}
