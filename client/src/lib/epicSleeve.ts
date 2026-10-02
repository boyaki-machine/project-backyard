/**
 * エピックの袖章（`GuiDesign.md` 5.4.4）。
 *
 * 所属エピックを、行の左端の幅 8px に描く「色 × 線の組」で示す。
 * 色は8色（`--pb-epic-0`〜`7`）、線の組は4通りで、**32通り**を見分ける。
 * バックログ（DOM の背景）とガント（SVG の rect）が同じ線の並びを使うので、
 * 並びはここに1つだけ持ち、描き方の違いは呼ぶ側の関数で吸収する。
 */
import { computed, ref, watch, type ComputedRef, type Ref } from 'vue'
import * as ticketsApi from '../api/tickets'
import type { Ticket } from '../api/tickets'

/** 袖章の全体の幅（px）。4通りとも同じ */
export const SLEEVE_WIDTH = 8
const COLORS = 8

/**
 * 線の組ごとの線分（左端からの位置と幅、px）。順は割り当ての順
 * （太1本 → 細2本 → 太＋細 → 細＋太）。
 */
const KINDS: readonly (readonly [number, number])[][] = [
  [[0, 8]],
  [[0, 2], [6, 2]],
  [[0, 4], [6, 2]],
  [[0, 2], [4, 4]],
]
/** 33件目からの灰の細線1本 */
const OVERFLOW: (readonly [number, number])[] = [[0, 2]]

export interface Sleeve {
  /** 色の CSS 値（`var(--pb-epic-N)`、33件目からは `var(--pb-text-muted)`） */
  color: string
  /** 線分（左端からの位置と幅、px） */
  segments: readonly (readonly [number, number])[]
}

/** 所属エピック（最も近い祖先1件） */
export type EpicRef = Pick<Ticket, 'seq' | 'title'>

/**
 * 番号の昇順に並べたときの順位から袖章を決める。
 * **完了・棚に戻ったエピックも順位に数える**——数えないと、閉じるたびに後ろの袖章が入れ替わる。
 */
export function sleeveForRank(rank: number): Sleeve {
  if (rank >= COLORS * KINDS.length) return { color: 'var(--pb-text-muted)', segments: OVERFLOW }
  return { color: `var(--pb-epic-${rank % COLORS})`, segments: KINDS[Math.floor(rank / COLORS)]! }
}

/**
 * エピックの番号 → 袖章。`allEpics` は完了も含む全エピック（並びは問わない）。
 * 一覧に無いエピック（取得の上限を超えた・取得の後に作られた）は、順位が分からないので
 * 33件目以降と同じ灰にする。
 */
export function sleeveMap(allEpics: readonly Pick<Ticket, 'seq'>[]): (epicSeq: number) => Sleeve {
  const rank = new Map([...allEpics].map((e) => e.seq).sort((a, b) => a - b).map((seq, i) => [seq, i]))
  return (epicSeq) => sleeveForRank(rank.get(epicSeq) ?? COLORS * KINDS.length)
}

/** DOM の背景に描く（`background-size: 8px 100%` と組み合わせる） */
export function sleeveBackground(s: Sleeve): string {
  const stops: string[] = []
  let x = 0
  for (const [at, w] of s.segments) {
    if (at > x) stops.push(`transparent ${x}px ${at}px`)
    stops.push(`${s.color} ${at}px ${at + w}px`)
    x = at + w
  }
  stops.push(`transparent ${x}px`)
  return `linear-gradient(90deg, ${stops.join(', ')})`
}

/**
 * 所属するエピックの全件を、袖章の割り当てのために取る（`GuiDesign.md` 5.4.4）。
 * 絞り込みの候補（`retired` を送らない）とは別の1本である。失敗しても画面は止めない。
 */
export async function listAllEpics(key: string): Promise<Ticket[]> {
  try {
    return (await ticketsApi.listTickets(key, { type: 'epic', retired: 'true', sort: 'seq', order: 'asc' })).items
  } catch {
    return []
  }
}

/**
 * チケットごとの所属エピック（最も近い祖先1件）。
 *
 * **検索や絞り込みで親が一覧から外れても所属を失わない**（5.4）。手持ちの一覧と全エピックから
 * 祖先をたどり、欠けた親だけ既存のチケット詳細 API で補う。取得できない親については推測しない。
 */
export function useEpicOfTicket(
  tickets: Readonly<Ref<Ticket[]>>,
  allEpics: Readonly<Ref<Ticket[]>>,
  projectKey: Readonly<Ref<string>>,
): ComputedRef<Map<number, EpicRef>> {
  /** 一覧に無い親 → その所属エピック（親がエピックならそれ自身） */
  const parentEpics = ref(new Map<number, EpicRef | null>())
  let fetchSeq = 0

  watch([tickets, allEpics], async () => {
    const mine = ++fetchSeq
    parentEpics.value = new Map()
    const key = projectKey.value
    const present = new Set([...tickets.value, ...allEpics.value].map((t) => t.seq))
    const missing = [...new Set(tickets.value.map((t) => t.parent_seq))]
      .filter((seq): seq is number => seq !== null && !present.has(seq))
    const resolved = await Promise.all(missing.map(async (seq) => {
      try {
        const parent = await ticketsApi.getTicket(key, seq)
        return [seq, parent.type === 'epic' ? parent : parent.epic] as const
      } catch {
        return [seq, null] as const
      }
    }))
    if (mine === fetchSeq && key === projectKey.value) parentEpics.value = new Map(resolved)
  })

  return computed(() => {
    const present = new Map([...tickets.value, ...allEpics.value].map((t) => [t.seq, t]))
    const out = new Map<number, EpicRef>()
    for (const ticket of tickets.value) {
      let parent = ticket.parent_seq
      const seen = new Set<number>([ticket.seq])
      for (let depth = 0; parent !== null && depth < 32 && !seen.has(parent); depth++) {
        seen.add(parent)
        const ancestor = present.get(parent)
        const epic = ancestor?.type === 'epic' ? ancestor : parentEpics.value.get(parent)
        if (epic) { out.set(ticket.seq, epic); break }
        if (!ancestor) break
        parent = ancestor.parent_seq
      }
    }
    return out
  })
}
