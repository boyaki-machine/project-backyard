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
 * `ApiDesign.md` 4.5.3 の許可リストは「この10件 ∪ `doc.edit`」の11件である。
 *
 * **写しを置いているのは、4.5.3 の `scopes` が絶対指定だからである**——
 * 画面が表せるのは「既定に `doc.edit` を足す」だが、API はそのトークンが持つ
 * 権限を全部並べて受け取る。省略時は既定が入るので、**チェックが外れている
 * ときはこの定数を使わない**（`scopes` ごと送らない）。
 *
 * **ずれると「チェックを付けたほうが狭くなる」**（pb-90。2026-09-09）。
 * サーバが1件やめると、ここに残った古いキーが許可リスト外になり **422 で落ちる**
 * （気づける）。サーバが1件足すと、**`doc.edit` つきで発行したトークンだけが
 * その1件を持たない**——発行は成功し、画面にも何も出ないので、
 * **エージェントが 403 を踏むまで誰も気づかない**。
 *
 * **実際に2回続けて起きた。** `ticket.reference.edit`（0027／pb-68、2026-09-08）と
 * `ticket.self_edit`（0029／pb-75、2026-09-09）で、どちらも権限を足した本人が
 * 写しを直していない。**利用者が「チェックを付けたほうが広がる認識なのに、
 * 外さないとチケットの本文が書き換えられない」と指摘して表に出た。**
 *
 * **いまは `me_agents_scopes_test.go` が一致を検査している。** ずれたまま
 * マージできない。**この定数を触ったら、サーバ側も一緒に見ること。**
 *
 * **写しそのものを無くす案は別に持っている**（pb-93）。本ファイルは
 * クライアント種別（`GET /agent-client-kinds`）とロール（旧 `lib/roles.ts`）を
 * 同じ理由でサーバ側へ寄せており、**既定スコープの写しだけが残っている。**
 */
export const AGENT_DEFAULT_SCOPES = [
  'agent.run',
  'comment.create',
  'doc.view',
  'project.view',
  'ticket.assign',
  'ticket.create',
  'ticket.reference.edit',
  'ticket.self_edit',
  'ticket.transition',
  'ticket.view',
] as const
