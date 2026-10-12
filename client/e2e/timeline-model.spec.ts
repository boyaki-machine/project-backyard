import { expect, test } from '@playwright/test'

import {
  assignLanes,
  barStateOf,
  baselineStamps,
  canCompact,
  canExtendToNow,
  changedSeqs,
  collisions,
  deviationOf,
  deviationTag,
  expectOf,
  extendToNowDue,
  fsMarks,
  layoutRows,
  baselineRows,
  emptyStateOf,
  rowSourceItems,
  mergeNewer,
  mergeTimelineEpics,
  readSnapSeconds,
  readTimelineEpics,
  widthClass,
} from '../src/plugins/event_view/model'
import type { Bar, TlEpic, TlLink } from '../src/plugins/event_view/model'
import { axisRange, clock, isWallMultiple, tickScale, wallTicks, zoomAt, ZOOMS } from '../src/plugins/event_view/time'
import { readViewEnabled, mergeViews } from '../src/api/projects'

/**
 * タイムラインの純粋な関数（`GuiDesign.md` 5.15。pb-298）。ブラウザを使わない。
 */

const MIN = 60_000
const T = (h: number, m = 0, s = 0) => Date.UTC(2026, 9, 24, h - 9, m, s) // Asia/Tokyo の壁時計

const st = (category: 'todo' | 'in_progress' | 'review' | 'done') => ({ key: category, name: category, category })

function bar(seq: number, s: number | null, e: number | null, chk: boolean, epic = 1): Bar {
  return { seq, title: `t${seq}`, epic, s, e, chk, ps: false, pe: false, state: 'todo', over: 0, closedAt: null }
}

test.describe('状態の見え方', () => {
  const now = T(11, 42)
  test('未着手・開始待ち・進行中・押している・完了', () => {
    expect(barStateOf({ status: st('todo'), start_at: T(12), due_at: T(13) }, now)).toEqual({ state: 'todo', over: 0 })
    expect(barStateOf({ status: st('todo'), start_at: T(11, 30), due_at: T(12) }, now)).toEqual({ state: 'wait', over: 12 * MIN })
    expect(barStateOf({ status: st('in_progress'), start_at: T(11), due_at: T(12) }, now)).toEqual({ state: 'prog', over: 0 })
    expect(barStateOf({ status: st('review'), start_at: T(11), due_at: T(11, 30) }, now)).toEqual({ state: 'late', over: 12 * MIN })
    // 未完了で終了を過ぎたものは、未着手でも押している
    expect(barStateOf({ status: st('todo'), start_at: T(10), due_at: T(11) }, now).state).toBe('late')
    expect(barStateOf({ status: st('done'), start_at: T(10), due_at: T(11) }, now)).toEqual({ state: 'done', over: 0 })
  })
})

test.describe('レーンの割り当て', () => {
  test('メインは 0、サブは重なる分だけ 1 から足す', () => {
    const { lane, lanes } = assignLanes([
      bar(1, T(10), T(11), true),
      bar(2, T(10, 30), T(11, 30), true), // メインどうしは重なっても 0（衝突として描く）
      bar(3, T(9), T(10), false),
      bar(4, T(9, 30), T(10, 30), false),
      bar(5, T(10), T(11), false), // 3 が空いたレーン 1 に入る
      bar(6, T(9, 45), T(9, 50), false),
    ])
    expect(lane.get('t1')).toBe(0)
    expect(lane.get('t2')).toBe(0)
    expect(lane.get('t3')).toBe(1)
    expect(lane.get('t4')).toBe(2)
    expect(lane.get('t6')).toBe(3)
    expect(lane.get('t5')).toBe(1)
    expect(lanes).toBe(4)
  })

  test('サブが無ければ1レーン。消えたものは別のキーで同じ番号と並ぶ', () => {
    expect(assignLanes([bar(1, T(10), T(11), true)]).lanes).toBe(1)
    const g = { ...bar(1, T(10), T(11), false), gone: true }
    const { lane, lanes } = assignLanes([bar(1, T(10), T(11), true), g])
    expect(lane.get('t1')).toBe(0)
    expect(lane.get('g1')).toBe(1)
    expect(lanes).toBe(2)
  })

  test('行の高さはレーンの数で決まり、バーの無い行も出す', () => {
    const epics: TlEpic[] = [
      { seq: 1, title: 'A', status: st('todo'), sort_key: null, closed_at: null },
      { seq: 2, title: 'B', status: st('todo'), sort_key: null, closed_at: null },
    ]
    const rows = layoutRows(epics, [bar(10, T(10), T(11), true, 1), bar(11, T(10), T(11), false, 1)])
    expect(rows.map((r) => [r.lanes, r.height, r.top])).toEqual([
      [2, 2 * 46 + 10, 0],
      [1, 46 + 10, 102],
    ])
  })
})

test.describe('当初計画とのずれ', () => {
  test('1分以上違えばずれ。札は終了を優先する', () => {
    expect(deviationOf({ s: T(10), e: T(11) }, { start_at: T(10), due_at: T(11, 0, 59) })).toBeNull()
    const d = deviationOf({ s: T(10, 5), e: T(11, 10) }, { start_at: T(10), due_at: T(11) })!
    expect(d).toEqual({ ds: 5 * MIN, de: 10 * MIN })
    expect(deviationTag(d)).toEqual({ edge: 'end', minutes: 10, late: true })
    expect(deviationTag({ ds: -5 * MIN, de: 0 })).toEqual({ edge: 'start', minutes: -5, late: false })
  })

  test('版の名前：分まで。同じ分は秒まで。今年でなければ年を付ける', () => {
    const now = T(12)
    const m = baselineStamps(
      [
        { id: 'a', created_at: T(8, 30, 12) },
        { id: 'b', created_at: T(8, 30, 45) },
        { id: 'c', created_at: T(8, 45) },
        { id: 'd', created_at: Date.UTC(2025, 9, 23, 23, 30) },
      ],
      'Asia/Tokyo',
      now,
    )
    expect(m.get('a')).toBe('10/24 08:30:12')
    expect(m.get('b')).toBe('10/24 08:30:45')
    expect(m.get('c')).toBe('10/24 08:45')
    expect(m.get('d')).toBe('2025/10/24 08:30')
  })
})

test.describe('衝突・FS・幅', () => {
  test('チェック付きどうしの重なりだけを返す', () => {
    expect(collisions([bar(1, T(10), T(11), true), bar(2, T(10, 50), T(12), true), bar(3, T(10), T(12), false)])).toEqual([
      { s: T(10, 50), e: T(11) },
    ])
    // 終わりと始まりが同じ時刻は重ならない
    expect(collisions([bar(1, T(10), T(11), true), bar(2, T(11), T(12), true)])).toEqual([])
  })

  test('FS は同じ行だけ。接していれば継ぎ目', () => {
    const links: TlLink[] = [
      { id: 'x', source_seq: 1, target_seq: 2, link_type: 'FS', lag_days: 0, origin: 'human' },
      { id: 'y', source_seq: 2, target_seq: 3, link_type: 'FS', lag_days: 0, origin: 'human' },
      { id: 'z', source_seq: 1, target_seq: 4, link_type: 'FS', lag_days: 0, origin: 'human' },
    ]
    const marks = fsMarks(links, [bar(1, T(10), T(11), true), bar(2, T(11), T(12), true), bar(3, T(12, 30), T(13), true), bar(4, T(13), T(14), true, 9)])
    expect(marks.map((m) => [m.from.seq, m.to.seq, m.joined])).toEqual([
      [1, 2, true],
      [2, 3, false],
    ])
  })

  test('幅による縮め方', () => {
    expect([widthClass(5), widthClass(20), widthClass(60), widthClass(200)]).toEqual(['w-0', 'w-xs', 'w-s', ''])
  })
})

test.describe('［いままで延ばす］・［ここで詰める］', () => {
  const now = T(11, 42, 10)
  test('いままで延ばす：吸着の単位に切り上げ、少なくとも1単位', () => {
    expect(extendToNowDue(T(11, 30), now, 60)).toBe(T(11, 43))
    expect(extendToNowDue(T(11, 42, 50), now, 60)).toBe(T(11, 43, 50))
    const late = { status: st('in_progress'), start_at: T(11), due_at: T(11, 30), all_day: false, due_pinned: false }
    expect(canExtendToNow(late, now)).toBe(true)
    expect(canExtendToNow({ ...late, status: st('todo') }, now)).toBe(false)
    expect(canExtendToNow({ ...late, due_pinned: true }, now)).toBe(false)
  })

  test('ここで詰める：完了・チェック付き・単位以上早く終わった', () => {
    const done = { status: st('done'), due_at: T(11, 45), closed_at: T(11, 35), no_overlap: true, all_day: false }
    expect(canCompact(done, 60)).toBe(true)
    expect(canCompact(done, 900)).toBe(false)
    expect(canCompact({ ...done, no_overlap: false }, 60)).toBe(false)
    expect(canCompact({ ...done, status: st('in_progress') }, 60)).toBe(false)
  })

  test('確定の expect はきっかけを除く', () => {
    const span = { start_at: 0, due_at: 1 }
    expect(
      expectOf(
        [
          { seq: 1, reason: 'target', before: span, after: span, version: 4 },
          { seq: 2, reason: 'ripple', before: span, after: span, version: 7 },
        ],
        1,
      ),
    ).toEqual([{ seq: 2, version: 7 }])
  })
})

test.describe('設定の読み書き', () => {
  test('吸着：無い・知らない値は 60', () => {
    expect(readSnapSeconds(undefined)).toBe(60)
    expect(readSnapSeconds({ timeline_snap_seconds: 7 })).toBe(60)
    expect(readSnapSeconds({ timeline_snap_seconds: 300 })).toBe(300)
  })

  test('タイムライン対象エピック：配列でなければ空。保存はいま在るものに詰め直す', () => {
    expect(readTimelineEpics({ timeline_epics: 'x' })).toEqual([])
    expect(readTimelineEpics({ timeline_epics: [3, 'a', 3, 1.5, 2] })).toEqual([3, 2])
    expect(mergeTimelineEpics({ repositories: [1], timeline_epics: [9] }, [5, 3, 99], [3, 5, 7])).toEqual({
      repositories: [1],
      timeline_epics: [3, 5],
    })
  })

  test('views：ガントは既定で出す・タイムラインは既定で出さない。知らないキーを落とさない', () => {
    expect(readViewEnabled({}, 'gantt', true)).toBe(true)
    expect(readViewEnabled({ views: { gantt: false } }, 'gantt', true)).toBe(false)
    expect(readViewEnabled({ views: { timeline: 'yes' } }, 'timeline', false)).toBe(false)
    expect(mergeViews({ a: 1, views: { other: true } }, { timeline: true })).toEqual({ a: 1, views: { other: true, timeline: true } })
  })

  test('他の人の変更：版が変わったものと新しいもの。自分の確定は除く', () => {
    const prev = new Map([
      [1, 1],
      [2, 1],
      [3, 1],
    ])
    const next = [
      { seq: 1, version: 1 },
      { seq: 2, version: 2 },
      { seq: 3, version: 2 },
      { seq: 4, version: 1 },
    ]
    expect(changedSeqs(prev, next, new Map([[3, 2]]))).toEqual([2, 4])
  })
})

test.describe('時間軸', () => {
  test('段階と目盛りの間隔', () => {
    expect(zoomAt(ZOOMS.sec)).toBe('sec')
    expect(zoomAt(ZOOMS.min)).toBe('min')
    expect(zoomAt(ZOOMS.min * 1.5)).toBeNull()
    expect(tickScale(ZOOMS.sec)).toEqual({ minor: 15, major: 30, fine: 5 })
    expect(tickScale(ZOOMS.min)).toEqual({ minor: 600, major: 1800, fine: 120 })
    expect(tickScale(ZOOMS.hour)).toEqual({ minor: 900, major: 3600, fine: 300 })
  })

  test('壁時計で揃える（時差 +5:45 でも現地の :00）', () => {
    const t0 = Date.UTC(2026, 9, 24, 3, 7)
    const ticks = wallTicks('Asia/Kathmandu', t0, t0 + 3 * 3600_000, 3600)
    expect(ticks.map((t) => clock('Asia/Kathmandu', t))).toEqual(['09:00', '10:00', '11:00'])
    expect(isWallMultiple('Asia/Tokyo', T(10, 30), 1800)).toBe(true)
    expect(isWallMultiple('Asia/Tokyo', T(10, 31), 1800)).toBe(false)
    expect(clock('Asia/Tokyo', T(11, 42, 5), true)).toBe('11:42:05')
  })

  test('範囲は前後 30分を足し、「いま」を含める', () => {
    expect(axisRange([T(10), T(12)], T(11))).toEqual({ t0: T(9, 30), t1: T(12, 30) })
    expect(axisRange([T(10), T(12)], T(15))).toEqual({ t0: T(9, 30), t1: T(15, 30) })
    expect(axisRange([], T(11))).toEqual({ t0: T(10), t1: T(12) })
  })
})

test.describe('保留した一覧と、版の行', () => {
  test('版の新しいほうを採る。一覧に無い行は落とす', () => {
    const cur = [{ seq: 1, version: 5, v: 'mine' }, { seq: 2, version: 1, v: 'old' }, { seq: 9, version: 1, v: 'gone' }]
    const inc = [{ seq: 1, version: 4, v: 'stale' }, { seq: 2, version: 2, v: 'other' }, { seq: 3, version: 1, v: 'new' }]
    expect(mergeNewer(cur, inc).map((t) => t.v)).toEqual(['mine', 'other', 'new'])
  })

  test('対象から外した・消したエピックは足す。絞り込みで外したものは足さない', () => {
    const ep = (seq: number, title: string): TlEpic => ({ seq, title, status: st('todo'), sort_key: null, closed_at: null })
    const items = [
      { epic_seq: 301, epic_title: '体育館', all_day: false },
      { epic_seq: 309, epic_title: '旧ステージ', all_day: false },
      { epic_seq: 302, epic_title: '中庭（版）', all_day: false },
      { epic_seq: 310, epic_title: '終日だけ', all_day: true },
    ]
    const known = [{ seq: 302, title: '中庭ステージ', sort_key: '0|b' }]
    // 対象は 301・303（303 の行は版に無い）
    const r = baselineRows([ep(301, '体育館')], items, [301, 303], [], known)
    expect(r.epics.map((e) => [e.seq, e.title])).toEqual([
      [301, '体育館'],
      [302, '中庭ステージ'],
      [309, '旧ステージ'],
    ])
    expect([...r.outside]).toEqual([302, 309])
    // 絞り込み中は、parent に書かれた対象外の行だけ
    expect(baselineRows([ep(301, '体育館')], items, [301, 303], [301], known).epics.map((e) => e.seq)).toEqual([301])
    expect(baselineRows([ep(301, '体育館')], items, [301, 303], [301, 309], known).epics.map((e) => e.seq)).toEqual([301, 309])
  })
})

test.describe('表示する行の規則（版・タイムライン対象エピック・parent の組み合わせ）', () => {
  // 実在するエピック 301・302・303（並びもこの順）。309 は消した。310 は終日の行しか無い
  const known = [
    { seq: 301, title: '体育館', sort_key: '0|a' },
    { seq: 302, title: '中庭', sort_key: '0|b' },
    { seq: 303, title: '本部', sort_key: '0|c' },
  ]
  const item = (epic: number, title: string, all_day = false) => ({ epic_seq: epic, epic_title: title, all_day })
  const versions: Record<string, ReturnType<typeof item>[]> = {
    V1: [item(301, '体育館'), item(302, '中庭'), item(309, '旧ステージ'), item(310, '終日だけ', true)],
    V2: [item(301, '体育館'), item(303, '本部')],
    VE: [],
  }
  /** サーバの `epics[]`：タイムライン対象エピック ∩ parent ∩ 実在（ApiDesign 9.2.7） */
  const server = (target: number[], parent: number[]): TlEpic[] =>
    known
      .filter((k) => target.includes(k.seq) && (parent.length === 0 || parent.includes(k.seq)))
      .map((k) => ({ seq: k.seq, title: k.title, status: st('todo'), sort_key: k.sort_key, closed_at: null }))

  type Case = {
    name: string
    target: number[]
    parent?: number[]
    saved: string[] // 保存した版（古い順）
    view: { mode: 'none' } | { mode: 'overlay'; id?: string } | { mode: 'step'; k: number }
    rows: number[]
    kinds?: Record<number, 'outside' | 'missing'>
    empty: 'select' | 'band' | null
  }
  const cases: Case[] = [
    { name: '対象全部・版0件', target: [301, 302, 303], saved: [], view: { mode: 'overlay' }, rows: [301, 302, 303], empty: null },
    { name: '対象空・版0件', target: [], saved: [], view: { mode: 'overlay' }, rows: [], empty: 'select' },
    { name: '対象空・版を重ねない', target: [], saved: ['V1', 'V2'], view: { mode: 'none' }, rows: [], empty: 'select' },
    { name: '対象空・最後の版を重ねる', target: [], saved: ['V1', 'V2'], view: { mode: 'overlay' }, rows: [301, 303], kinds: { 301: 'outside', 303: 'outside' }, empty: 'band' },
    { name: '対象空・行が0件の版を重ねる', target: [], saved: ['VE'], view: { mode: 'overlay' }, rows: [], empty: 'select' },
    { name: '対象空・送る：最初の版（前の版なし）', target: [], saved: ['V1', 'V2'], view: { mode: 'step', k: 0 }, rows: [301, 302, 309], kinds: { 301: 'outside', 302: 'outside', 309: 'missing' }, empty: 'band' },
    { name: '対象空・送る：2番目（前の版あり）', target: [], saved: ['V1', 'V2'], view: { mode: 'step', k: 1 }, rows: [301, 302, 303, 309], empty: 'band' },
    { name: '対象空・送る：最後の「いま」は前の版で比べる', target: [], saved: ['V1', 'V2'], view: { mode: 'step', k: 2 }, rows: [301, 303], empty: 'band' },
    { name: '対象一部・消したエピックと対象外の版の行', target: [301], saved: ['V1'], view: { mode: 'overlay' }, rows: [301, 302, 309], kinds: { 302: 'outside', 309: 'missing' }, empty: null },
    { name: '対象一部・行が0件の版', target: [301], saved: ['VE'], view: { mode: 'overlay' }, rows: [301], empty: null },
    { name: '設定に不在の番号が残る', target: [301, 303, 309], saved: ['V1'], view: { mode: 'overlay' }, rows: [301, 303, 302, 309], kinds: { 302: 'outside', 309: 'missing' }, empty: null },
    { name: '設定に不在の番号・送る', target: [301, 303, 309], saved: ['V1', 'V2'], view: { mode: 'step', k: 0 }, rows: [301, 303, 302, 309], kinds: { 309: 'missing' }, empty: null },
    { name: 'parent で外した行は版にあっても出さない', target: [301, 303, 309], parent: [301], saved: ['V1'], view: { mode: 'overlay' }, rows: [301], empty: null },
    { name: 'parent で外した行は送るときも出さない', target: [], parent: [301], saved: ['V1', 'V2'], view: { mode: 'step', k: 1 }, rows: [301], empty: 'band' },
    { name: 'parent が不在の番号を指す・版にその行あり', target: [301, 303, 309], parent: [309], saved: ['V1'], view: { mode: 'overlay' }, rows: [309], kinds: { 309: 'missing' }, empty: null },
    { name: 'parent が不在の番号を指す・版を重ねない', target: [301, 303, 309], parent: [309], saved: ['V1'], view: { mode: 'none' }, rows: [], empty: null },
    { name: 'parent が対象外の実在エピックを指す', target: [301], parent: [302], saved: ['V1'], view: { mode: 'overlay' }, rows: [302], kinds: { 302: 'outside' }, empty: null },
    { name: '対象全部・送る：最初の版で消したエピックも読める', target: [301, 302, 303], saved: ['V1', 'V2'], view: { mode: 'step', k: 0 }, rows: [301, 302, 303, 309], kinds: { 309: 'missing' }, empty: null },
    { name: '対象全部・版なし（重ねない）', target: [301, 302, 303], saved: ['V1'], view: { mode: 'none' }, rows: [301, 302, 303], empty: null },
    { name: '重ねる版を明示（古い版）', target: [301], saved: ['V1', 'V2'], view: { mode: 'overlay', id: 'V1' }, rows: [301, 302, 309], empty: null },
  ]

  for (const c of cases) {
    test(c.name, () => {
      const parent = c.parent ?? []
      const latest = c.saved[c.saved.length - 1] ?? null
      const step = c.view.mode === 'step' ? c.view.k : null
      const selected = c.view.mode === 'overlay' ? (c.view.id ?? latest) : null
      const items = rowSourceItems(step, c.saved, selected, (id) => versions[id])
      const r = baselineRows(server(c.target, parent), items, c.target, parent, known)
      expect(r.epics.map((e) => e.seq)).toEqual(c.rows)
      for (const [seq, kind] of Object.entries(c.kinds ?? {})) expect(r.kinds.get(Number(seq))).toBe(kind)
      // いまの行には印を付けない
      for (const e of server(c.target, parent)) expect(r.kinds.has(e.seq)).toBe(false)
      // 消したエピックの見出しは版に保存した題名
      if (c.rows.includes(309)) expect(r.epics.find((e) => e.seq === 309)!.title).toBe('旧ステージ')
      expect(r.epics.some((e) => e.seq === 310)).toBe(false)
      expect(emptyStateOf(true, c.target.length, r.epics.length)).toBe(c.empty)
    })
  }

  test('設定を読む前は空状態を出さない', () => {
    expect(emptyStateOf(false, 0, 0)).toBeNull()
  })
})
