/**
 * グループ化の軸ごとの振り分け（`GuiDesign.md` 5.4.1）。
 *
 * **バックログのセクションとガントの行グループ（5.14）が同じ規則で分ける。**
 * 軸を視点のあいだで共有する（4.1.1）以上、同じ軸で違う顔ぶれのグループが
 * 出ると、同じ軸だと読めなくなる。規則を1か所に置く。
 */
import type { Sprint } from '../api/sprints'
import type { Tag } from '../api/tags'
import type { Ticket } from '../api/tickets'
import type { ProjectMember, Workflow } from '../api/projects'
import { statusLabel } from './catalogLabels'
import { uiText } from '../locales/ui'

/** グループ化の軸（5.4.1）。空文字が「なし」で、これが既定 */
export type GroupAxis = '' | 'parent' | 'tag' | 'sprint' | 'assignee' | 'status'

export const GROUP_AXES: GroupAxis[] = ['', 'parent', 'tag', 'sprint', 'assignee', 'status']

export function groupAxisLabel(axis: GroupAxis): string {
  switch (axis) {
    case '':
      return uiText('なし')
    case 'parent':
      return uiText('親チケット')
    case 'tag':
      return uiText('タグ')
    case 'sprint':
      return uiText('スプリント')
    case 'assignee':
      return uiText('担当')
    case 'status':
      return uiText('状態')
  }
}

/** 振り分けに要る語彙。いずれも画面が既に手元に持っている */
export interface GroupVocabulary {
  projectKey: string
  /** いま一覧に出ているチケット（親の名前を引く） */
  tickets: Ticket[]
  /** エピックは行に出ないが、親の名前として引ける */
  epics: Ticket[]
  tags: Tag[]
  sprints: Sprint[]
  members: ProjectMember[]
  statuses: Workflow['statuses']
}

/**
 * 親チケットの見出し。
 *
 * **エピックは一覧に出ないが、語彙として手元にある**ので名前を引ける。
 * どちらにも無い場合は完全形の ID で出す（5.4「ID列」）——「不明」と書くより、
 * 詳細を開ける番号のほうが役に立つ。
 */
function parentLabel(seq: number, v: GroupVocabulary): string {
  const parent = v.tickets.find((t) => t.seq === seq) ?? v.epics.find((e) => e.seq === seq)
  return parent ? parent.title : `${v.projectKey}-${seq}`
}

/**
 * 行がどのグループへ入るかを返す。
 *
 * **タグだけは複数返る**——複数タグを持つチケットは各グループに重複して
 * 現れる（5.4.1。「これを避けない」）。
 */
export function groupsOf(
  t: Ticket,
  axis: GroupAxis,
  v: GroupVocabulary,
): { key: string; label: string }[] {
  switch (axis) {
    case 'parent':
      return t.parent_seq === null
        ? [{ key: 'top', label: uiText('トップレベル') }]
        : [{ key: String(t.parent_seq), label: parentLabel(t.parent_seq, v) }]
    case 'tag':
      return t.tags.length === 0
        ? [{ key: 'none', label: uiText('未分類') }]
        : t.tags.map((tag) => ({ key: tag.id, label: tag.name }))
    case 'sprint':
      return [
        t.sprint === null
          ? { key: 'none', label: uiText('スプリント未設定') }
          : { key: t.sprint.id, label: t.sprint.name },
      ]
    case 'assignee':
      return [
        t.assignee === null
          ? { key: 'none', label: uiText('未割当') }
          : { key: t.assignee.id, label: t.assignee.display_name },
      ]
    case 'status':
      return [{ key: t.status.key, label: statusLabel(t.status.key, t.status.name) }]
    default:
      return []
  }
}

/**
 * グループの並び順。**軸の語彙の順に出す**——タグは `sort_order`、
 * スプリントは一覧の順、状態はワークフローの `sort_order` である。
 * 「未分類」「未割当」「スプリント未設定」は末尾に置く。
 */
export function groupOrder(axis: GroupAxis, v: GroupVocabulary): string[] {
  switch (axis) {
    case 'parent':
      return ['top', ...v.epics.map((e) => String(e.seq)), ...v.tickets.map((t) => String(t.seq))]
    case 'tag':
      return [...v.tags.map((t) => t.id), 'none']
    case 'sprint':
      return [...v.sprints.map((s) => s.id), 'none']
    case 'assignee':
      return [...v.members.map((m) => m.actor_id), 'none']
    case 'status':
      return v.statuses.map((s) => s.key)
    default:
      return []
  }
}

/**
 * 行をグループに振り分けて、並び順に返す。
 *
 * 語彙の順に並べ、語彙に無いもの（削除されたメンバーの担当など）は
 * 出現順で後ろへ付ける。**空のグループは出さない。**
 */
export function bucketize<R>(
  rows: R[],
  ticketOf: (row: R) => Ticket,
  axis: GroupAxis,
  v: GroupVocabulary,
): { key: string; label: string; rows: R[] }[] {
  const buckets = new Map<string, { key: string; label: string; rows: R[] }>()
  const seen: string[] = []
  for (const row of rows) {
    for (const { key, label } of groupsOf(ticketOf(row), axis, v)) {
      const bucket = buckets.get(key)
      if (bucket) {
        bucket.rows.push(row)
      } else {
        buckets.set(key, { key, label, rows: [row] })
        seen.push(key)
      }
    }
  }
  const ordered = groupOrder(axis, v).filter((k) => buckets.has(k))
  const orderedSet = new Set(ordered)
  const rest = seen.filter((k) => !orderedSet.has(k))
  return [...ordered, ...rest].map((k) => buckets.get(k)!)
}
