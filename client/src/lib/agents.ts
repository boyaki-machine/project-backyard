/**
 * エージェントの表示に関わる小さな助け（`GuiDesign.md` 5.8.2）。
 *
 * **クライアント種別の対応表は持たない**（0020 で参照テーブルにした）。
 * `GET /agent-client-kinds`（`ApiDesign.md` 4.5.7）が `display_name` を返す
 * ——**`GET /roles` が表示名を返すようになった時点で `lib/roles.ts` を廃止したのと
 * 同じ形である**（`GuiDesign.md` 5.6）。**値域は今後も増える**ので
 * （`DbDesign.md` 8.2.1.1）、写しを置くと必ず腐る。
 */
import type { AgentClientKind } from '../api/me'

/**
 * カタログから表示名を引く。
 *
 * **引けなければキーをそのまま返す。** カタログの取得に失敗した瞬間に
 * 種別の欄が空になるより、`claude_code` と出るほうが読める。
 */
export function clientKindLabel(
  kinds: readonly AgentClientKind[],
  key: string,
): string {
  return kinds.find((k) => k.key === key)?.display_name ?? key
}

/**
 * エージェント用トークンの既定スコープ（`Design.md` 6.5、手順26a）。
 *
 * **これは写しである。正本はサーバの `agentDefaultScopes`**（`me_agents.go`）で、
 * `ApiDesign.md` 4.5.3 の許可リストは「この8件 ∪ `doc.edit`」である。
 *
 * **写しを置いているのは、4.5.3 の `scopes` が絶対指定だからである**——
 * 画面が表せるのは「既定に `doc.edit` を足す」だが、API はそのトークンが持つ
 * 権限を全部並べて受け取る。省略時は既定が入るので、**チェックが外れている
 * ときはこの定数を使わない**（`scopes` ごと送らない）。
 *
 * **ずれたときの出方**——サーバが1件やめると、ここに残った古いキーが許可リスト外に
 * なり **422 で落ちる**（気づける）。サーバが1件足すと、`doc.edit` つきで発行した
 * トークンだけがその1件を持たない（**気づきにくい**）。
 * **`scopes` を「足すもの」だけ受ける形（`add_scopes`）にすれば写しは消える**が、
 * 10.9.1 系統B の「閲覧用＝read only」で絞る用途が手順28 に控えているため、
 * 絶対指定のまま置いている（`docs/PROGRESS.md` の引き継ぎに起票）。
 */
export const AGENT_DEFAULT_SCOPES = [
  'agent.run',
  'comment.create',
  'doc.view',
  'project.view',
  'ticket.assign',
  'ticket.create',
  'ticket.transition',
  'ticket.view',
] as const
