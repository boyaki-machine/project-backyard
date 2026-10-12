/**
 * チケットを見る視点のどれを出すか（`GuiDesign.md` 3.2 / 5.13 / 5.14 / 5.15）。
 *
 * `/p/:key/backlog`・`/p/:key/search`・`/p/:key/gantt`・`/p/:key/tickets/:seq` と、
 * プラグインのビュー（`plugins/registry.ts` が持つパス）は同じ入れ物（`TicketViewsPage`）に来て、
 * **パスと `from` で一覧を選ぶ**。
 * **`from` を解釈できない共有 URL（`/p/:key/tickets/31`）はバックログの上に開く**（3.2）。
 *
 * **判定の正本をここに1つ置く。** 入れ物と、集中モードの「画面を移ったら解除」
 * （2.3.2）の2か所が同じ規則を使う——写しを持つと、詳細を開いただけで集中モードが
 * 解ける、のような食い違いが起きる。
 */
import { viewPluginOf, viewPlugins } from '../plugins/registry'

/** 本体の視点と、プラグインの視点（`plugin:<id>`） */
export type TicketView = 'backlog' | 'search' | 'gantt' | `plugin:${string}`

export function ticketViewOf(path: string, from: unknown): TicketView {
  if (path.endsWith('/search') || from === 'search') return 'search'
  if (path.endsWith('/gantt') || from === 'gantt') return 'gantt'
  const plugin = viewPluginOf(path, from)
  if (plugin) return `plugin:${plugin.id}`
  return 'backlog'
}

const segments = ['backlog', 'search', 'gantt', ...viewPlugins.map((p) => p.route.segment)]
const viewPathPattern = new RegExp(`^/p/[^/]+/(${segments.join('|')}|tickets/\\d+)$`)

/** 入れ物に来るパスのどれかか */
export function isTicketViewPath(path: string): boolean {
  return viewPathPattern.test(path)
}
