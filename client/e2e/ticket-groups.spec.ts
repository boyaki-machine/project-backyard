import { expect, test } from '@playwright/test'
import type { Ticket } from '../src/api/tickets'
import { bucketize, groupsOf, type GroupVocabulary } from '../src/lib/ticketGroups'

function ticket(seq: number, title: string, parent_seq: number | null = null): Ticket {
  return { seq, title, parent_seq } as Ticket
}

function vocabulary(tickets: Ticket[], epics: Ticket[] = []): GroupVocabulary {
  return { projectKey: 'demo', tickets, epics, tags: [], sprints: [], members: [], statuses: [] }
}

test('parent grouping preserves labels, vocabulary order, unknown parents and refreshed titles', () => {
  const parent = ticket(20, '日本語の親')
  const v = vocabulary([parent, ticket(30, '')], [ticket(10, 'エピック')])
  const rows = [ticket(1, '子', 20), ticket(2, '子', 999), ticket(3, '子', 10),
    ticket(4, '根'), ticket(5, '子', 20), ticket(6, '子', 30)]
  const grouped = bucketize(rows, t => t, 'parent', v)
  expect(grouped.map(g => [g.key, g.label, g.rows.map(t => t.seq)])).toEqual([
    ['top', 'トップレベル', [4]], ['10', 'エピック', [3]],
    ['20', '日本語の親', [1, 5]], ['30', '', [6]], ['999', 'demo-999', [2]],
  ])
  for (const group of grouped) expect(groupsOf(group.rows[0]!, 'parent', v)[0]!.label).toBe(group.label)
  expect(groupsOf(rows[0]!, 'parent', vocabulary([parent], [ticket(20, '別の表示名')]))[0]!.label).toBe(parent.title)
  parent.title = '更新された名前'
  expect(bucketize(rows, t => t, 'parent', v).find(g => g.key === '20')!.label).toBe(parent.title)
})

test('parent grouping reads vocabulary a bounded number of times for 2000 distinct parents', () => {
  let reads = 0
  const count = 2000
  const parents = Array.from({ length: count }, (_, i) => ({
    get seq() { reads++; return i + 1 }, title: `親${i + 1}`, parent_seq: null,
  } as Ticket))
  const children = parents.map((_, i) => ticket(count + i + 1, '子', i + 1))
  const grouped = bucketize(children, t => t, 'parent', vocabulary(parents))
  expect(reads).toBeLessThanOrEqual(count * 6)
  expect(grouped).toHaveLength(count)
  expect(grouped[0]!.label).toBe('親1')
  expect(grouped.at(-1)!.rows[0]!.seq).toBe(count * 2)
})
