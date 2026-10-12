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
import { lagText } from '../lib/lagText'

export { lagText }

export type TicketLink = components['schemas']['TicketLink']
export type TicketLinkList = components['schemas']['TicketLinkList']
export type TicketLinkTicketRef = components['schemas']['TicketLinkTicketRef']
export type CreateTicketLinkRequest = components['schemas']['CreateTicketLinkRequest']
export type UpdateTicketLinkRequest = components['schemas']['UpdateTicketLinkRequest']

export type LinkType = TicketLink['link_type']
export type LinkDirection = TicketLink['direction']

/**
 * 行に出すラベル（`GuiDesign.md` 5.5「`link_type` の日本語ラベル」）。
 *
 * **`blocks` と依存の4種は主語を書く**。**行に描かれるのは
 * 常に相手**なので、「先行」とだけ書くと**その行のチケットが先行だと読める**
 * ——実際に先行なのはこのチケットのほうである。`自` の1文字が主語を固定する。
 *
 * `relates` と `duplicates` に主語が要らないのは、**向きで意味が反転しない**
 * ためである（重複はどちらを閉じても片方が片づく同値の関係）。
 *
 * **`FS`〜`SF` は記号を訳さない**——ガント（5.14）の線の札も `SS` / `FF` の表記であり、
 * 訳を当てると見比べられなくなる。ずらしがあれば `+2d`・`+10m` を添える（札と同じ書式）。
 */
export function linkLabel(type: LinkType, direction: LinkDirection, lagDays = 0, lagSeconds = 0): string {
  if (type === 'blocks') return direction === 'outgoing' ? uiText('自先行') : uiText('自後行')
  if (type === 'duplicates') return uiText('重複')
  if (type === 'relates') return uiText('関連')
  const side = direction === 'outgoing' ? uiText('自先行') : uiText('自後行')
  const lag = lagText(lagDays, lagSeconds)
  return `${type} ${side}${lag ? ` ${lag}` : ''}`
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
  // 1文字目が先行の端、2文字目が後行の端（S＝開始、F＝終了）。**このチケットの端を言葉で書く**
  const name = (c: string) => (c === 'S' ? uiText('開始') : uiText('終了'))
  const [a, b] = [name(type[0]!), name(type[1]!)]
  return direction === 'outgoing'
    ? uiText('{type} 自先行 = このチケットが先行（このチケットの{a} → 相手の{b}）。ガントかタイムラインで線を引いて作る', { type, a, b })
    : uiText('{type} 自後行 = このチケットが後行（相手の{a} → このチケットの{b}）。ガントかタイムラインで線を引いて作る', { type, a, b })
}

/**
 * 追加のモーダルに出す選択肢（`GuiDesign.md` 5.5「追加のモーダル」）。
 *
 * **このモーダルで作れるのは `relates` / `duplicates` / `blocks` の3種だけである**。
 * `FS` / `SS` / `FF` / `SF` はガントの依存線のためのもので、**ガントの上でバーの端から
 * 端へ引いて作る**（`GuiDesign.md` 5.14「編集」）——線を見ながら作れる場所があるので、
 * **線の無いここで人に選ばせない。** **API は7種すべて受け続ける**（MCP とエージェントも積める）。
 *
 * **`自後行` は API に無い。** `POST .../links` は**このチケットが常に
 * `source`** になるので、`incoming` の行を直接は作れない——画面は
 * `自後行` を選ばれたら**相手のチケットに対して `blocks` を作る**
 * （`createLink` の呼び先を入れ替える）。**どちらの向きも同じモーダルから
 * 作れることのほうが、実装の対称性より優先する。**
 */
export interface LinkChoice {
  /**
   * 画面の値。`blocks` と依存の4種は向きで2つに割れる（`_out`＝このチケットが先行、`_in`＝後行）。
   * 依存の4種はタイムラインから開いたときだけ出す（`timelineLinkChoices`。pb-332）
   */
  value: 'relates' | 'duplicates' | `${'blocks' | DepLinkType}_${'out' | 'in'}`
  label: string
  hint: string
}

/** 依存の4種（連鎖のずらしが従い、ずらしを持つもの） */
export type DepLinkType = 'FS' | 'SS' | 'FF' | 'SF'

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

/** 種別の2文字（S＝開始、F＝終了）を端の名前にする */
function endName(c: string): string {
  return c === 'S' ? uiText('開始') : uiText('終了')
}

/**
 * タイムラインから開いた詳細の選択肢（`GuiDesign.md` 5.5「追加のモーダル」。pb-332）。
 * 3種に、依存の4種の `自先行` / `自後行` の8つを足す。**ずらしは 0 で作り、行のチップで変える。**
 */
export const timelineLinkChoices: LinkChoice[] = [
  ...linkChoices,
  ...(['FS', 'SS', 'FF', 'SF'] as const).flatMap((ty): LinkChoice[] => {
    const [a, b] = [ty[0]!, ty[1]!]
    return [
      {
        value: `${ty}_out`,
        get label() { return uiText('{type} 自先行', { type: ty }) },
        get hint() { return uiText('このチケットの{a}に、相手の{b}をそろえる', { a: endName(a), b: endName(b) }) },
      },
      {
        value: `${ty}_in`,
        get label() { return uiText('{type} 自後行', { type: ty }) },
        get hint() { return uiText('相手の{a}に、このチケットの{b}をそろえる', { a: endName(a), b: endName(b) }) },
      },
    ]
  }),
]

/**
 * 選択肢を API の要求に直す。`_in` は**相手を `source` にして作る**（`自後行` は API に無い）。
 */
export function choiceToLink(value: LinkChoice['value']): { linkType: LinkType; reverse: boolean } {
  if (value === 'relates' || value === 'duplicates') return { linkType: value, reverse: false }
  const [ty, dir] = value.split('_') as [string, string]
  return { linkType: (ty === 'blocks' ? 'blocks' : ty) as LinkType, reverse: dir === 'in' }
}

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
 * ためで、**`FS`〜`SF` のときしか意味を持たない**——ガントのドラッグも常に `0` で作り、
 * 作ったあと詳細の関連チケットの行で変える（`updateLink`。pb-231）。
 */
export function createLink(
  key: string,
  seq: number,
  body: CreateTicketLinkRequest,
): Promise<TicketLink> {
  return api.post<TicketLink>(base(key, seq), body)
}

/** ずらし（`lag_days`）が意味を持つ種別（9.10.1）。`blocks` は持たない */
export const LAG_LINK_TYPES: readonly LinkType[] = ['FS', 'SS', 'FF', 'SF']
/** ずらしの範囲（-365〜365 日。9.10.1） */
export const MAX_LAG_DAYS = 365
/** 秒のずらしの範囲（1日未満。`DbDesign.md` 6.6.3。pb-332） */
export const MAX_LAG_SECONDS = 86399

/**
 * 秒のずらしを `時:分:秒` にする（`0:10:00`・`-0:05:00`）。詳細の「ずらし」の欄（5.5）
 */
export function formatLagClock(seconds: number): string {
  const sign = seconds < 0 ? '-' : ''
  let sec = Math.abs(seconds)
  const h = Math.floor(sec / 3600)
  const m = Math.floor((sec % 3600) / 60)
  sec %= 60
  return `${sign}${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
}

/**
 * `時:分:秒`・`分:秒`・`秒` を読む（先頭に `-` で負）。読めないか1日以上なら `null`。
 * 空なら 0。
 */
export function parseLagClock(raw: string): number | null {
  const t = raw.trim()
  if (t === '') return 0
  const m = /^(-)?(\d+)(?::(\d{1,2}))?(?::(\d{1,2}))?$/.exec(t)
  if (!m) return null
  const parts = [m[2], m[3], m[4]].filter((p): p is string => p !== undefined).map(Number)
  if (parts.slice(1).some((p) => p >= 60)) return null
  let sec = 0
  for (const p of parts) sec = sec * 60 + p
  if (sec > MAX_LAG_SECONDS) return null
  return m[1] ? -sec : sec
}

/**
 * 依存のずらしを変える（9.10.1 の `PATCH`。pb-231・pb-332）。**変えるもの（`lag_days`・`lag_seconds`）だけを送る。**
 * `direction` を問わない（`incoming` の行も、このチケットの口から直せる）。
 */
export function updateLink(key: string, seq: number, id: string, body: UpdateTicketLinkRequest): Promise<TicketLink> {
  return api.patch<TicketLink>(`${base(key, seq)}/${encodeURIComponent(id)}`, body)
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
