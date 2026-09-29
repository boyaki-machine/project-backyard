/**
 * チケット間リンクのエンドポイント（`ApiDesign.md` 9.10.1）。手順18b。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 *
 * **指す先が PB の中か外かで節が分かれている**（9.10）。外を指すのは
 * `references.ts`（9.10.2、手順17c）で、こちらは**同じプロジェクトの別の
 * チケット**だけを指す。
 *
 * **一覧を呼ぶ画面は無い。** 詳細応答（9.5.1）の `links` に同じものが入る
 * （`references.ts` / `dod.ts` と同じ位置づけ）。
 *
 * **`GET` は双方向を1本で返す。** 当該チケットが `source` である行
 * （`outgoing`）と `target` である行（`incoming`）の両方が入り、
 * **`ticket` に入るのは常に相手であって自分ではない。**
 */
import { api } from './client'
import type { components } from './schema'
import { uiText } from '../locales/ui'

export type TicketLink = components['schemas']['TicketLink']
export type TicketLinkList = components['schemas']['TicketLinkList']
export type TicketLinkTicketRef = components['schemas']['TicketLinkTicketRef']
export type CreateTicketLinkRequest = components['schemas']['CreateTicketLinkRequest']

export type LinkType = TicketLink['link_type']
export type LinkDirection = TicketLink['direction']

/**
 * 行に出すラベル（`GuiDesign.md` 5.5「`link_type` の日本語ラベル」）。
 *
 * **`blocks` だけ主語を書く**。**行に描かれるのは
 * 常に相手**なので、「先行」とだけ書くと**その行のチケットが先行だと読める**
 * ——実際に先行なのはこのチケットのほうである。`自` の1文字が主語を固定する。
 *
 * `relates` と `duplicates` に主語が要らないのは、**向きで意味が反転しない**
 * ためである（重複はどちらを閉じても片方が片づく同値の関係）。
 */
export function linkLabel(type: LinkType, direction: LinkDirection): string {
  if (type === 'blocks') return direction === 'outgoing' ? uiText('自先行') : uiText('自後行')
  if (type === 'duplicates') return uiText('重複')
  if (type === 'relates') return uiText('関連')
  // `FS`〜`SF` は画面から作れないが、MCP が積んだ行は一覧に混ざりうる。
  // **キーをそのまま出す**——ガント（`GuiDesign.md` 5.14）の線の札も `SS` / `FF` の
  // 表記であり、訳を当てると見比べられなくなる。
  return type
}

/** チップに添える注釈（`title`）。**主語がどちらかを全文で示す** */
export function linkLabelTitle(type: LinkType, direction: LinkDirection): string {
  if (type === 'blocks') {
    return direction === 'outgoing'
      ? uiText('自先行 = このチケットが先行。相手がこのチケットの完了を待つ')
      : uiText('自後行 = このチケットが後行。このチケットが相手の完了を待つ')
  }
  if (type === 'duplicates') return uiText('重複 = 同じことを指している')
  if (type === 'relates') return uiText('関連 = 関わりがある')
  return uiText('{type}（ガント用の依存。画面からは作れない）', { type })
}

/**
 * 追加のモーダルに出す選択肢（`GuiDesign.md` 5.5「追加のモーダル」）。
 *
 * **画面が作れるのは `relates` / `duplicates` / `blocks` の3種だけである**。
 * `FS` / `SS` / `FF` / `SF` と `lag_days` は
 * ガントの依存線のためのもので、ガントは閲覧だけを実装した段階である（線を
 * 引く編集は pb-221）——**線を見ながら作れない値を、ここで人に選ばせない。** **API は7種すべて
 * 受け続ける**（MCP とエージェントが先に積むのは妨げない）。
 *
 * **`自後行` は API に無い。** `POST .../links` は**このチケットが常に
 * `source`** になるので、`incoming` の行を直接は作れない——画面は
 * `自後行` を選ばれたら**相手のチケットに対して `blocks` を作る**
 * （`createLink` の呼び先を入れ替える）。**どちらの向きも同じモーダルから
 * 作れることのほうが、実装の対称性より優先する。**
 */
export interface LinkChoice {
  /** 画面の値。`blocks` だけ向きで2つに割れる */
  value: 'relates' | 'duplicates' | 'blocks_out' | 'blocks_in'
  label: string
  hint: string
}

export const linkChoices: LinkChoice[] = [
  { value: 'relates', get label() { return uiText('関連') }, get hint() { return uiText('関わりがある') } },
  { value: 'duplicates', get label() { return uiText('重複') }, get hint() { return uiText('同じことを指している') } },
  {
    value: 'blocks_out',
    get label() { return uiText('自先行') },
    get hint() { return uiText('このチケットが先行。相手がこのチケットの完了を待つ') },
  },
  {
    value: 'blocks_in',
    get label() { return uiText('自後行') },
    get hint() { return uiText('このチケットが後行。このチケットが相手の完了を待つ') },
  },
]

function base(key: string, seq: number): string {
  return `/projects/${encodeURIComponent(key)}/tickets/${seq}/links`
}

/**
 * リンクの一覧（9.10.1）。必要権限は `ticket.view`。
 *
 * `direction`（`outgoing` → `incoming`）、同じ向きの中は
 * `link_type` → 相手の `seq` の昇順で返る。
 */
export function listLinks(key: string, seq: number): Promise<TicketLinkList> {
  return api.get<TicketLinkList>(base(key, seq))
}

/**
 * リンクを1件足す（9.10.1）。必要権限は `ticket.edit`。
 *
 * **`seq` のチケットが常に `source` になる。** `自後行` を作るときは、
 * 呼び出し側が `seq` に**相手**を、`target_seq` に**自分**を渡す
 * （`TicketDetailPane` の `saveLink`）。
 *
 * **同じ `(source, target, link_type)` は 409 `already_exists`**。
 * **自分自身は 422 `self_link`**、相手が居なければ 422 `not_found`。
 *
 * **`lag_days` は既定の `0` を明示して送る。** 生成された型が必須にしている
 * ためで、**`FS`〜`SF` のときしか意味を持たない**——画面はその4種を作らない。
 */
export function createLink(
  key: string,
  seq: number,
  body: CreateTicketLinkRequest,
): Promise<TicketLink> {
  return api.post<TicketLink>(base(key, seq), body)
}

/**
 * リンクを消す（9.10.1）。`204`。
 *
 * **`direction` を問わない。** `incoming` の行——相手のチケットが `source` で
 * ある行——も、このチケットのエンドポイントから消せる。画面が両方を同じ
 * リストに並べる以上、**片方だけ消せないと「消せない行」が混ざる。**
 */
export function deleteLink(key: string, seq: number, id: string): Promise<void> {
  return api.del<void>(`${base(key, seq)}/${encodeURIComponent(id)}`)
}
