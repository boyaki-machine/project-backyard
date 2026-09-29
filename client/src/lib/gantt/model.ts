/**
 * ガントの行と帯の材料（`GuiDesign.md` 5.14「行の並び」「帯（予定の描き分け）」）。
 *
 * **二段を持たず1本のツリーにする。** 並びはサーバが返した `sort_key` の昇順のまま、
 * 親子はバックログ（5.4）と同じく `parent_seq` で組み直す。親が結果に居ない子
 * （エピックの直下など）は根として並ぶ。グループ化したときは平らに並べる（5.4.1）。
 */
import type { StatusCategory, Ticket, TicketGanttLink } from '../../api/tickets'
import { bucketize } from '../ticketGroups'
import type { GroupAxis, GroupVocabulary } from '../ticketGroups'

export interface GItem {
  seq: number
  title: string
  ticket: Ticket
  cat: StatusCategory
  done: boolean
  /** 期限超過（未完了で `due_at <= いま`）。5.4 と同じ式 */
  overdue: boolean
  /** 予定の開始・終わり（半開区間。エポックミリ秒） */
  s: number | null
  e: number | null
  allDay: boolean
  /** 自分の予定を持たない親の、配下の期間（寸法線で描く） */
  roll: { s: number; e: number } | null
}

export type GRow =
  | { kind: 'group'; key: string; label: string; count: number; collapsed: boolean }
  | { kind: 'ticket'; item: GItem; depth: number; hasKids: boolean; collapsed: boolean }

/** インデントは5段までで打ち切る（5.4）。それ以深は同じ深さに置く */
const MAX_DEPTH = 5

export function toItems(tickets: Ticket[], now: number): Map<number, GItem> {
  const items = new Map<number, GItem>()
  for (const t of tickets) {
    const done = t.closed_at !== null
    items.set(t.seq, {
      seq: t.seq,
      title: t.title,
      ticket: t,
      cat: t.status.category,
      done,
      overdue: !done && t.due_at !== null && t.due_at <= now,
      s: t.start_at,
      e: t.due_at,
      allDay: t.all_day,
      roll: null,
    })
  }
  // 配下の期間。**畳んでいても求める**——畳んだときこそ配下の広がりが要る（5.14）
  const kids = childrenOf(tickets)
  for (const it of items.values()) {
    if (it.s !== null || it.e !== null) continue
    if (!kids.has(it.seq)) continue
    let s = Infinity
    let e = -Infinity
    const seen = new Set<number>([it.seq])
    const walk = (seq: number): void => {
      for (const k of kids.get(seq) ?? []) {
        if (seen.has(k.seq)) continue
        seen.add(k.seq)
        const a = k.start_at ?? k.due_at
        const b = k.due_at ?? k.start_at
        if (a !== null && b !== null) {
          s = Math.min(s, a)
          e = Math.max(e, b)
        }
        walk(k.seq)
      }
    }
    walk(it.seq)
    if (s < e) it.roll = { s, e }
  }
  return items
}

/** 結果の中での親子（親が結果に居ない子は含めない） */
function childrenOf(tickets: Ticket[]): Map<number, Ticket[]> {
  const present = new Set(tickets.map((t) => t.seq))
  const map = new Map<number, Ticket[]>()
  for (const t of tickets) {
    if (t.parent_seq === null || !present.has(t.parent_seq)) continue
    const list = map.get(t.parent_seq)
    if (list) list.push(t)
    else map.set(t.parent_seq, [t])
  }
  return map
}

type TicketRow = Extract<GRow, { kind: 'ticket' }>

/** `parent_seq` からツリーを組む（5.4 の `buildTree` と同じ規則） */
function treeRows(tickets: Ticket[], items: Map<number, GItem>, collapsed: Set<number>): TicketRow[] {
  const kids = childrenOf(tickets)
  const present = new Set(tickets.map((t) => t.seq))
  const roots = tickets.filter((t) => t.parent_seq === null || !present.has(t.parent_seq))
  const rows: TicketRow[] = []
  const reached = new Set<number>()
  const markReached = (list: Ticket[]): void => {
    for (const t of list) {
      if (reached.has(t.seq)) continue
      reached.add(t.seq)
      markReached(kids.get(t.seq) ?? [])
    }
  }
  const walk = (list: Ticket[], depth: number): void => {
    for (const t of list) {
      if (reached.has(t.seq)) continue
      reached.add(t.seq)
      const k = kids.get(t.seq)
      const isCollapsed = k !== undefined && collapsed.has(t.seq)
      rows.push({ kind: 'ticket', item: items.get(t.seq)!, depth: Math.min(depth, MAX_DEPTH), hasKids: k !== undefined, collapsed: isCollapsed })
      if (k === undefined) continue
      if (isCollapsed) markReached(k)
      else walk(k, depth + 1)
    }
  }
  walk(roots, 0)
  // 親子が輪になっていると根から辿り着けない。行を落とさない（5.4 と同じ救済）
  for (const t of tickets) {
    if (!reached.has(t.seq)) rows.push({ kind: 'ticket', item: items.get(t.seq)!, depth: 0, hasKids: false, collapsed: false })
  }
  return rows
}

export function buildRows(
  tickets: Ticket[],
  items: Map<number, GItem>,
  axis: GroupAxis,
  vocabulary: GroupVocabulary,
  treeCollapsed: Set<number>,
  groupCollapsed: Set<string>,
): GRow[] {
  if (axis === '') return treeRows(tickets, items, treeCollapsed)
  const flat = tickets.map((t): TicketRow => ({ kind: 'ticket', item: items.get(t.seq)!, depth: 0, hasKids: false, collapsed: false }))
  const out: GRow[] = []
  for (const g of bucketize(flat, (r) => r.item.ticket, axis, vocabulary)) {
    const isCollapsed = groupCollapsed.has(g.key)
    out.push({ kind: 'group', key: g.key, label: g.label, count: g.rows.length, collapsed: isCollapsed })
    if (!isCollapsed) out.push(...g.rows)
  }
  return out
}

/** 行の中でのチケットの位置。**同じチケットが複数のグループに出るときは最初の行**（タグ） */
export function rowIndexOf(rows: GRow[]): Map<number, number> {
  const map = new Map<number, number>()
  rows.forEach((r, i) => {
    if (r.kind === 'ticket' && !map.has(r.item.seq)) map.set(r.item.seq, i)
  })
  return map
}

/** 描く範囲（自分の予定。無ければ配下の期間） */
export function spanOf(it: GItem): { s: number | null; e: number | null; roll: boolean } | null {
  if (it.s !== null || it.e !== null) return { s: it.s, e: it.e, roll: false }
  if (it.roll !== null) return { s: it.roll.s, e: it.roll.e, roll: true }
  return null
}

/**
 * 時間軸の範囲（5.14「時間軸の範囲」）。予定（配下の期間を含む）の最も早い時点から
 * 最も遅い時点までに、前後 90日の余白を足す。**「今」は必ず含める。**
 */
export function timeRange(items: Iterable<GItem>, now: number): { t0: number; t1: number } {
  const DAY = 86_400_000
  let lo = now
  let hi = now
  for (const it of items) {
    for (const v of [it.s, it.e, it.roll?.s ?? null, it.roll?.e ?? null]) {
      if (v === null) continue
      if (v < lo) lo = v
      if (v > hi) hi = v
    }
  }
  return { t0: lo - 90 * DAY, t1: hi + 90 * DAY }
}

export type GanttLink = TicketGanttLink
