/**
 * エージェントの表示に関わる小さな助け（`GuiDesign.md` 5.8.2）。
 *
 * **既定スコープの写しも持たない**。`GET /agent-scopes`（`ApiDesign.md` 4.5.9）
 * から引く——写しは権限を足すたびに2回続けて腐った。戻すと
 * `me_agents_scopes_test.go` が落ちる。
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
