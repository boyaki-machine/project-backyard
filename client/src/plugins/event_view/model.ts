/**
 * タイムラインの描画の材料（`GuiDesign.md` 5.15）。**純粋な関数だけを置く**（単体テストの対象）。
 *
 * - 行はエピック1つ、バーはその配下のチケット（9.2.7 の `epic_seq`）
 * - **本番（主レーン）**：時間重複チェック付きは1本目のレーン。**裏（副レーン）**：チェックの無いものは
 *   2本目以降に、重なる分だけレーンを足して全部入れる（先に始まるものから空いているレーンへ）
 * - 状態：未着手・開始待ち・進行中・押している・完了（版を送るときは「計画」）
 * - 当初計画：ずれ（1分以上）・計画外・消えたもの
 *
 * 文言は持たない（画面が `uiText` で組み立てる）。
 */
import type { components } from '../../api/schema'
import { MIN, SEC } from './time'

export type TlTicket = components['schemas']['Ticket']
export type TlEpic = components['schemas']['TimelineEpic']
export type TlLink = components['schemas']['TicketGanttLink']
export type TlList = components['schemas']['TicketList']
export type BaselineSummary = components['schemas']['ScheduleBaselineSummary']
export type Baseline = components['schemas']['ScheduleBaseline']
export type BaselineItem = components['schemas']['ScheduleBaselineItem']
export type RescheduleRequest = components['schemas']['RescheduleRequest']
export type RescheduleResult = components['schemas']['RescheduleResult']
export type RescheduleChange = components['schemas']['RescheduleChange']

/** バーの見え方。`plan` は版を送るときの「計画」の見え方 */
export type BarState = 'todo' | 'wait' | 'prog' | 'late' | 'done' | 'plan'

export interface Bar {
  seq: number
  title: string
  epic: number
  /** 開始・終了（どちらか一方は `null` でありうる。片方だけのものは `[┄` / `┄]` で描く） */
  s: number | null
  e: number | null
  chk: boolean
  ps: boolean
  pe: boolean
  state: BarState
  /** 押している・開始待ちの量（ms）。それ以外は 0 */
  over: number
  /** 完了した時刻（完了の補足と `[ここで詰める]` に使う） */
  closedAt: number | null
  /** 版にあって、いまは無い（消えたもの） */
  gone?: boolean
  ticket?: TlTicket
}

/** 区分（`todo` / `in_progress` / `review` / `done`）と「いま」から見え方を決める（5.15「状態の色」） */
export function barStateOf(t: Pick<TlTicket, 'status' | 'start_at' | 'due_at'>, now: number): { state: BarState; over: number } {
  const cat = t.status.category
  if (cat === 'done') return { state: 'done', over: 0 }
  // 押している：未完了で、終了を過ぎた（ガントの期限超過と同じ式）
  if (t.due_at !== null && t.due_at < now) return { state: 'late', over: now - t.due_at }
  if (cat === 'todo') {
    // 開始待ち：未着手で、開始を過ぎた
    if (t.start_at !== null && t.start_at < now) return { state: 'wait', over: now - t.start_at }
    return { state: 'todo', over: 0 }
  }
  return { state: 'prog', over: 0 }
}

/** チケットをバーにする。**時刻付きで、開始か終了を持つものだけ**（終日・予定なしは `null`） */
export function barOf(t: TlTicket, now: number): Bar | null {
  if (t.all_day || (t.start_at === null && t.due_at === null)) return null
  const { state, over } = barStateOf(t, now)
  return {
    seq: t.seq,
    title: t.title,
    epic: t.epic_seq ?? 0,
    s: t.start_at,
    e: t.due_at,
    chk: t.no_overlap,
    ps: t.start_pinned && t.start_at !== null,
    pe: t.due_pinned && t.due_at !== null,
    state,
    over,
    closedAt: t.closed_at,
    ticket: t,
  }
}

/** 版の行をバーにする（版を送るときの「計画」、消えたものの影） */
export function planBarOf(b: BaselineItem, gone = false): Bar {
  return {
    seq: b.ticket_seq,
    title: b.title,
    epic: b.epic_seq,
    s: b.start_at,
    e: b.due_at,
    chk: b.no_overlap,
    ps: b.start_pinned && b.start_at !== null,
    pe: b.due_pinned && b.due_at !== null,
    state: 'plan',
    over: 0,
    closedAt: null,
    gone,
  }
}

/** レーンの割り当てに使う区間（片方だけのものは、その時刻の1点） */
export function spanOf(b: Pick<Bar, 's' | 'e'>): { a: number; b: number } {
  const a = b.s ?? b.e ?? 0
  const z = b.e ?? b.s ?? 0
  return { a: Math.min(a, z), b: Math.max(a, z) }
}

/**
 * レーンを割り当てる。**チェック付き（本番）は 0、チェックの無い（裏）は 1 から**、
 * 先に始まるものから空いているレーンへ詰め、足りなければレーンを足す（半開区間）。
 */
export function assignLanes(bars: readonly Pick<Bar, 'seq' | 's' | 'e' | 'chk' | 'gone'>[]): { lane: Map<string, number>; lanes: number } {
  const lane = new Map<string, number>()
  const ends: number[] = []
  const free = bars
    .filter((b) => !b.chk)
    .map((bar) => ({ bar, ...spanOf(bar) }))
    .sort((x, y) => x.a - y.a || x.b - y.b || x.bar.seq - y.bar.seq)
  for (const f of free) {
    let k = ends.findIndex((end) => end <= f.a)
    if (k < 0) {
      k = ends.length
      ends.push(0)
    }
    // 1点のもの（片方だけ）は幅を持たないが、同じ時刻の次のものと重ねないよう 1ms を足す
    ends[k] = Math.max(f.b, f.a + 1)
    lane.set(keyOf(f.bar), k + 1)
  }
  for (const b of bars) if (b.chk) lane.set(keyOf(b), 0)
  return { lane, lanes: 1 + ends.length }
}

/** レーンの表のキー（いまのバーと消えたものは同じ番号でありうるので分ける） */
export function keyOf(b: Pick<Bar, 'seq' | 'gone'>): string {
  return `${b.gone ? 'g' : 't'}${b.seq}`
}

/** 行の寸法（5.15「行とレーン」）。1レーン 46px ＋ 上下 5px、バーは 36px */
export const LANE_H = 46
export const ROW_PAD = 5
export const BAR_H = 36

export function laneTop(lane: number): number {
  return ROW_PAD + lane * LANE_H + (LANE_H - BAR_H) / 2
}

export interface RowLayout {
  epic: TlEpic
  bars: Bar[]
  gone: Bar[]
  lane: Map<string, number>
  lanes: number
  top: number
  height: number
}

/** 行を並べる。`epics` の順（`sort_key` の昇順）。バーが無くても行を出す */
export function layoutRows(epics: readonly TlEpic[], bars: readonly Bar[], gone: readonly Bar[] = []): RowLayout[] {
  const rows: RowLayout[] = []
  let top = 0
  for (const epic of epics) {
    const mine = bars.filter((b) => b.epic === epic.seq)
    const lost = gone.filter((b) => b.epic === epic.seq)
    const { lane, lanes } = assignLanes([...mine, ...lost])
    const height = lanes * LANE_H + ROW_PAD * 2
    rows.push({ epic, bars: mine, gone: lost, lane, lanes, top, height })
    top += height
  }
  return rows
}

/** 当初計画とのずれ（ms）。**開始か終了が1分以上違えば**ずれとする。ずれていなければ `null` */
export interface Deviation {
  ds: number
  de: number
}

export function deviationOf(cur: Pick<Bar, 's' | 'e'>, base: Pick<BaselineItem, 'start_at' | 'due_at'>): Deviation | null {
  const ds = cur.s !== null && base.start_at !== null ? cur.s - base.start_at : 0
  const de = cur.e !== null && base.due_at !== null ? cur.e - base.due_at : 0
  return Math.abs(ds) >= MIN || Math.abs(de) >= MIN ? { ds, de } : null
}

/**
 * ズレの札の中身。**終了が1分以上ずれていれば終了、そうでなければ開始の差**（分、符号付き）。
 * `edge` は `end` / `start`、`late` は遅れ（正）か。
 */
export function deviationTag(d: Deviation): { edge: 'start' | 'end'; minutes: number; late: boolean } {
  const end = Math.abs(d.de) >= MIN
  const v = end ? d.de : d.ds
  return { edge: end ? 'end' : 'start', minutes: Math.round(v / MIN), late: v > 0 }
}

/** 時間重複チェック付きどうしの重なり（5.15「衝突」）。重なった区間を返す */
export function collisions(bars: readonly Bar[]): { s: number; e: number }[] {
  const chk = bars.filter((b) => b.chk && b.s !== null && b.e !== null).sort((a, b) => a.s! - b.s!)
  const out: { s: number; e: number }[] = []
  for (let i = 1; i < chk.length; i++) {
    for (let j = 0; j < i; j++) {
      const s = Math.max(chk[j]!.s!, chk[i]!.s!)
      const e = Math.min(chk[j]!.e!, chk[i]!.e!)
      if (e > s) out.push({ s, e })
    }
  }
  return out
}

/** FS の印（5.15「FS」）。**同じ行の、両端が描かれているものだけ** */
export interface FsMark {
  from: Bar
  to: Bar
  /** 先行の終わりと後続の始まりが接している（継ぎ目に ◆） */
  joined: boolean
}

export function fsMarks(links: readonly TlLink[], bars: readonly Bar[]): FsMark[] {
  const by = new Map(bars.map((b) => [b.seq, b]))
  const out: FsMark[] = []
  for (const l of links) {
    if (l.link_type !== 'FS') continue
    const a = by.get(l.source_seq)
    const b = by.get(l.target_seq)
    if (!a || !b || a.epic !== b.epic || a.e === null || b.s === null) continue
    out.push({ from: a, to: b, joined: b.s - a.e <= 0 })
  }
  return out
}

/** バーの幅による縮め方（5.15「バー（2行固定）」） */
export type WidthClass = 'w-0' | 'w-xs' | 'w-s' | ''

export function widthClass(w: number): WidthClass {
  if (w < 12) return 'w-0'
  if (w < 34) return 'w-xs'
  if (w < 96) return 'w-s'
  return ''
}

/**
 * `[いままで延ばす]` の新しい終了：「いま」を吸着の単位に切り上げ、**少なくとも1単位延ばす**
 */
export function extendToNowDue(due: number, now: number, snapSec: number): number {
  const u = snapSec * SEC
  return Math.max(Math.ceil(now / u) * u, due + u)
}

/** `[いままで延ばす]` を出すか：進行中で、終了を過ぎている（押している） */
export function canExtendToNow(t: Pick<TlTicket, 'status' | 'due_at' | 'start_at' | 'all_day' | 'due_pinned'>, now: number): boolean {
  const cat = t.status.category
  return (cat === 'in_progress' || cat === 'review') && !t.all_day && t.due_at !== null && t.start_at !== null && t.due_at < now && !t.due_pinned
}

/**
 * `[ここで詰める]` を出すか：完了で、時間重複チェック付きで、完了時刻が予定の終了より
 * 吸着の単位以上早い
 */
export function canCompact(t: Pick<TlTicket, 'status' | 'due_at' | 'closed_at' | 'no_overlap' | 'all_day'>, snapSec: number): boolean {
  return (
    t.status.category === 'done' &&
    t.no_overlap &&
    !t.all_day &&
    t.due_at !== null &&
    t.closed_at !== null &&
    t.due_at - t.closed_at >= snapSec * SEC
  )
}

/** 試算で動く行（きっかけを除く）を、確定の `expect` に入れる形にする */
export function expectOf(changes: readonly RescheduleChange[], target: number): { seq: number; version: number }[] {
  return changes.filter((c) => c.seq !== target).map((c) => ({ seq: c.seq, version: c.version }))
}

/** 吸着の単位（秒）。`1`・`10`・`30`・`60`・`300`・`900`。**無い・知らない値は 60**（`DbDesign.md` 6.4） */
export const SNAP_CHOICES = [1, 10, 30, 60, 300, 900] as const

export function readSnapSeconds(settings: Record<string, unknown> | undefined): number {
  const v = settings?.['timeline_snap_seconds']
  return SNAP_CHOICES.find((c) => c === v) ?? 60
}

/** 載せるエピック。**無い・配列でないときは空**、数でないものは読み飛ばす（`DbDesign.md` 6.4） */
export function readTimelineEpics(settings: Record<string, unknown> | undefined): number[] {
  const v = settings?.['timeline_epics']
  if (!Array.isArray(v)) return []
  return [...new Set(v.filter((x): x is number => Number.isInteger(x) && x > 0))]
}

/**
 * 載せるエピックを差し替えた `settings`。**保存するとき、いま在るエピックだけに詰め直す**
 * （`DbDesign.md` 6.4）。知らないキーは落とさない（丸ごと置き換えなので）。
 */
export function mergeTimelineEpics(
  settings: Record<string, unknown>,
  selected: readonly number[],
  existing: readonly number[],
): Record<string, unknown> {
  const live = new Set(existing)
  return { ...settings, timeline_epics: [...new Set(selected)].filter((s) => live.has(s)).sort((a, b) => a - b) }
}

/**
 * 版の名前のうち日時の部分（`DbDesign.md` 6.32「名前」、5.15「当初計画の版」）。
 *
 * **保存した日時をプロジェクトの基準タイムゾーンで分まで**（`10/24 08:30`）。**同じ分に保存した版が
 * 2つ以上あれば、それらだけ秒まで**。年が今年でなければ年を前に付ける。番号は付けない。
 */
export function baselineStamps(list: readonly Pick<BaselineSummary, 'id' | 'created_at'>[], tz: string, now: number): Map<string, string> {
  const fmt = new Intl.DateTimeFormat('en-US', {
    timeZone: tz,
    hourCycle: 'h23',
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
  const partsOf = (ms: number) => {
    const o: Record<string, string> = {}
    for (const p of fmt.formatToParts(ms)) o[p.type] = p.value
    return o
  }
  const thisYear = partsOf(now)['year']
  const parts = list.map((b) => ({ id: b.id, p: partsOf(b.created_at) }))
  const minuteKey = (p: Record<string, string>) => `${p['year']}-${p['month']}-${p['day']} ${p['hour']}:${p['minute']}`
  const count = new Map<string, number>()
  for (const { p } of parts) count.set(minuteKey(p), (count.get(minuteKey(p)) ?? 0) + 1)
  const out = new Map<string, string>()
  for (const { id, p } of parts) {
    const year = p['year'] === thisYear ? '' : `${p['year']}/`
    const sec = (count.get(minuteKey(p)) ?? 0) > 1 ? `:${p['second']}` : ''
    out.set(id, `${year}${p['month']}/${p['day']} ${p['hour']}:${p['minute']}${sec}`)
  }
  return out
}

/** 定期取得で変わったバー。**前回から `version` が変わったもの（自分の確定を除く）と、新しく現れたもの** */
export function changedSeqs(
  prev: ReadonlyMap<number, number>,
  next: readonly Pick<TlTicket, 'seq' | 'version'>[],
  mine: ReadonlyMap<number, number>,
): number[] {
  const out: number[] = []
  for (const t of next) {
    const before = prev.get(t.seq)
    if (before === t.version) continue
    if (mine.get(t.seq) === t.version) continue
    out.push(t.seq)
  }
  return out
}

/** 他の人の変更の中身（通知の行の右端に出す） */
export type ChangeKind =
  | { kind: 'due'; at: number }
  | { kind: 'start'; at: number }
  | { kind: 'status'; name: string }
  | { kind: 'title' }
  | { kind: 'added' }
  | { kind: 'other' }

export function changeOf(before: TlTicket | undefined, after: TlTicket): ChangeKind {
  if (!before) return { kind: 'added' }
  if (before.due_at !== after.due_at && after.due_at !== null) return { kind: 'due', at: after.due_at }
  if (before.start_at !== after.start_at && after.start_at !== null) return { kind: 'start', at: after.start_at }
  if (before.status.key !== after.status.key) return { kind: 'status', name: after.status.name }
  if (before.title !== after.title) return { kind: 'title' }
  return { kind: 'other' }
}
