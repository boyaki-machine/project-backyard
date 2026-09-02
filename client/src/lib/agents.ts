/**
 * エージェントの表示に関わる小さな決めごと（`GuiDesign.md` 5.8.2）。
 *
 * **`client_kind` の表示名は 5.8.2 の表が正本である。** `GET /permissions` の
 * `description` のような供給源が API に無く、**画面が対応表を持たざるをえない**
 * ——`agent.client_kind` は `DbDesign.md` 8.2.1 の CHECK が持つ3値で、
 * 表示名を持つ列は無い。
 *
 * **`lib/roles.ts` を廃止したのとは事情が違う。** あちらは `GET /roles` が
 * `display_name` を返すようになったので写しを消せた（`GuiDesign.md` 5.6）。
 * こちらは返す相手がいない。**値域が3つでDBの CHECK に固定されている**ので、
 * 増えるときはマイグレーションを伴い、ここも一緒に直る。
 */
import type { MyAgent } from '../api/me'

/** `agent.client_kind` の値域（`DbDesign.md` 8.2.1 の CHECK と同じ順・同じ綴り） */
export type ClientKind = MyAgent['client_kind']

/**
 * 登録モーダルの選択肢（`GuiDesign.md` 5.8.2）。
 *
 * **並びは CHECK と同じにする。** 既定は先頭の `claude_code`。
 */
export const CLIENT_KINDS: readonly { value: ClientKind; label: string }[] = [
  { value: 'claude_code', label: 'Claude Code' },
  { value: 'copilot', label: 'VS Code + Copilot' },
  { value: 'other', label: 'その他' },
] as const

/**
 * `client_kind` を画面の表示名にする。
 *
 * **知らない値はそのまま返す。** DBの CHECK が値域を守っているので起こらないが、
 * 空文字にすると「クライアント種別が消えた」という読みにくい壊れ方になる。
 */
export function clientKindLabel(kind: string): string {
  return CLIENT_KINDS.find((k) => k.value === kind)?.label ?? kind
}
