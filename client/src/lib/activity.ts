/**
 * `activity` の1行を日本語の1文にする（`ApiDesign.md` 9.13.2）。
 *
 * **要約文の正本はここ1か所である**（`GuiDesign.md` 5.5）。ダッシュボードの
 * 「最近の動き」（5.3）とチケット詳細の「履歴」（5.5）が同じ関数を通る。
 * **同じ出来事が2つの画面で違う文になると、同じことだと読めなくなる。**
 *
 * ## 値域
 *
 * `action` は `create` / `update` / `delete` / `transition` の4値、`field` は
 * 9.13.2 の表の18種類（`create` / `delete` は `null`）。**表に無い `field` が
 * 来ても文にする**——サーバが先に増えても画面が空行を出さないようにするため、
 * 既定の文（「〜を更新」）へ落とす。
 *
 * ## 値の解決
 *
 * `old_value` / `new_value` は `text` のまま返る（9.13.2）。
 *
 * | `field` | 中身 | 画面が名前に直せるか |
 * |---|---|---|
 * | `status_key` | ワークフローのキー | **直せる**（`GET /projects/:key` の `workflow.statuses[]`） |
 * | `assignee_id` | アクターの ULID | **メンバー表があれば直せる**（同 `members[]`） |
 * | `sprint_id` | スプリントの ULID | **直せない**（一覧を別に取っていない。5.5） |
 * | `parent_id` | 親の `seq` | **直せる**（`<key>-<seq>` に組む） |
 * | `body_md` | 値を持たない（`null`） | — |
 *
 * 直せない値は**文から落とす**（「スプリントを変更」）。ULID をそのまま出すと、
 * 26桁の英数字が行の大半を占めて何が起きたか読めなくなる。
 */
import type { Activity } from '../api/dashboard'
import { priorityLabels, ticketTypeLabels } from '../api/tickets'
import type { TicketPriority, TicketType } from '../api/tickets'
import { uiText } from '../locales/ui'

/**
 * 値を名前に直すための手がかり。**持っているものだけ渡す。**
 *
 * ダッシュボード（5.3）はワークフローとメンバーを持ち、チケット詳細（5.5）も
 * 同じものを持つ。どちらもスプリント表は持たない。
 */
export interface ActivityLabelContext {
  /** プロジェクトキー。`parent_id`（`seq`）を完全形に組むのに使う */
  projectKey: string
  /** ワークフローのステータス（キー → 表示名） */
  statusNames?: Record<string, string>
  /** メンバー（アクターの ULID → 表示名） */
  actorNames?: Record<string, string>
}

/** チケットの完全形 ID（`my-app-31`）。`seq` が無ければ「削除されたチケット」 */
export function ticketLabel(projectKey: string, seq: number | null | undefined): string {
  return seq === null || seq === undefined ? uiText('削除されたチケット') : `${projectKey}-${seq}`
}

/** 実行者の表示名。`actor` は `null` になりうる（9.13.2） */
export function actorLabel(a: Activity['actor']): string {
  return a === null || a === undefined ? uiText('削除されたユーザー') : a.display_name
}

/** 値が空か（`null` / 空文字）。サーバは削除を `new_value: null` で表す */
function blank(v: string | null | undefined): boolean {
  return v === null || v === undefined || v === ''
}

/**
 * 子資源（コメント・DoD・リンク・外部参照）の増減を読む。
 *
 * サーバは `old_value` / `new_value` に**要約**を入れ、**どちらが空かで
 * 追加・変更・削除を表す**（`comments.go` / `dod.go` / `links.go` /
 * `references.go` の `record*Change`）。
 */
function childChange(a: Activity): 'added' | 'edited' | 'removed' {
  if (blank(a.old_value)) return 'added'
  if (blank(a.new_value)) return 'removed'
  return 'edited'
}

/**
 * `old → new` の形。片方しか無ければ在るほうだけ出す。
 *
 * **値は「」で囲む。** 種別・優先度・ステータスがそうしているのに数と日付だけ
 * 裸にすると、`の期限を2026-09-30 に変更` のように**前の語と値が地続きになって
 * どこからが値か読めない**（実機のスクリーンショットで判明。手順19b）。
 */
function transition(field: string, oldValue: string, newValue: string): string {
  if (oldValue === '' && newValue === '') return uiText('{field}を変更', { field })
  if (oldValue === '') return uiText('{field}を「{newValue}」に変更', { field, newValue })
  if (newValue === '') return uiText('{field}を「{oldValue}」から未設定に変更', { field, oldValue })
  return uiText('{field}を「{oldValue}」から「{newValue}」に変更', { field, oldValue, newValue })
}

/** 表示用に値を整える。空なら「なし」 */
function shown(v: string | null | undefined): string {
  return blank(v) ? '' : (v as string)
}

function statusName(ctx: ActivityLabelContext, key: string | null | undefined): string {
  if (blank(key)) return ''
  return ctx.statusNames?.[key as string] ?? (key as string)
}

/** アクターの ULID を表示名へ。**解決できなければ空**（ULID を出さない） */
function actorName(ctx: ActivityLabelContext, id: string | null | undefined): string {
  if (blank(id)) return ''
  return ctx.actorNames?.[id as string] ?? ''
}

function typeName(v: string | null | undefined): string {
  if (blank(v)) return ''
  return ticketTypeLabels[v as TicketType] ?? (v as string)
}

function priorityName(v: string | null | undefined): string {
  if (blank(v)) return ''
  return priorityLabels[v as TicketPriority] ?? (v as string)
}

/**
 * 1行の要約文。**チケットの完全形 ID は含めない**——呼び出し側が行の先頭に
 * 別要素として置き、リンクにできるようにするためである（5.3 / 5.5）。
 *
 * 返る文は「〜を作成」「〜を『進行中』に変更」のように**述部だけ**である。
 */
export function activitySummary(a: Activity, ctx: ActivityLabelContext): string {
  if (a.action === 'create') return uiText('を作成')
  if (a.action === 'delete') return uiText('を削除')

  // 遷移（9.6）。**status_key は必ずワークフローに在る**ので表示名に直せる。
  if (a.action === 'transition' || a.field === 'status_key') {
    const to = statusName(ctx, a.new_value)
    return to === '' ? uiText('の状態を変更') : uiText('を「{value}」に変更', { value: to })
  }

  switch (a.field) {
    case 'title':
      return uiText('のタイトルを変更')
    case 'body_md':
      // 値を持たない（9.5.2）。「説明が変わった」ことだけが残る。
      return uiText('の説明を変更')
    case 'type': {
      const to = typeName(a.new_value)
      return to === '' ? uiText('の種別を変更') : uiText('の種別を「{value}」に変更', { value: to })
    }
    case 'priority': {
      const to = priorityName(a.new_value)
      return to === '' ? uiText('の優先度を変更') : uiText('の優先度を「{value}」に変更', { value: to })
    }
    case 'assignee_id': {
      // ULID をそのまま出さない。解決できたときだけ名前を添える。
      const to = actorName(ctx, a.new_value)
      if (blank(a.new_value)) return uiText('の担当を未設定に変更')
      return to === '' ? uiText('の担当を変更') : uiText('の担当を {value} に変更', { value: to })
    }
    case 'sprint_id':
      // **スプリント表を持つ画面が無い**（5.5）。値は出さない。
      return blank(a.new_value) ? uiText('のスプリントを未設定に変更') : uiText('のスプリントを変更')
    case 'parent_id': {
      // parent_id には `seq` が入る（`tickets_update.go` の int4StrPtr）。
      if (blank(a.new_value)) return uiText('の親を未設定に変更')
      return uiText('の親を {value} に変更', { value: `${ctx.projectKey}-${shown(a.new_value)}` })
    }
    case 'estimate_point':
      return transition(uiText('見積(pt)'), shown(a.old_value), shown(a.new_value))
    case 'estimate_hours':
      return transition(uiText('見積(時間)'), shown(a.old_value), shown(a.new_value))
    case 'actual_hours':
      return transition(uiText('実績(時間)'), shown(a.old_value), shown(a.new_value))
    case 'start_date':
      return transition(uiText('開始日'), shown(a.old_value), shown(a.new_value))
    case 'due_date':
      return transition(uiText('期限'), shown(a.old_value), shown(a.new_value))
    case 'comment':
      return { added: uiText('にコメントを追加'), edited: uiText('のコメントを編集'), removed: uiText('のコメントを削除') }[
        childChange(a)
      ]
    case 'dod':
      return {
        added: uiText('に完了条件を追加'),
        edited: uiText('の完了条件を変更'),
        removed: uiText('の完了条件を削除'),
      }[childChange(a)]
    case 'link':
      return {
        added: uiText('に関連チケットを追加'),
        edited: uiText('の関連チケットを変更'),
        removed: uiText('の関連チケットを削除'),
      }[childChange(a)]
    case 'reference.code':
      return { added: uiText('にコードを追加'), edited: uiText('のコードを変更'), removed: uiText('のコードを削除') }[
        childChange(a)
      ]
    case 'reference.doc':
      return {
        added: uiText('に参考リンクを追加'),
        edited: uiText('の参考リンクを変更'),
        removed: uiText('の参考リンクを削除'),
      }[childChange(a)]
    default:
      // 9.13.2 の表に無い field。**空行にせず既定の文へ落とす。**
      return uiText('を更新')
  }
}

/**
 * 変更の中身をもう1行で出すための補足（履歴セクションが使う）。
 *
 * 要約文が値を含まないもの（コメント・DoD・リンク・外部参照）について、
 * **サーバが積んだ要約をそのまま見せる**。`comments.go` の
 * `commentSummaryOf` などが作った `<kind>: <本文の先頭40字>` の形である。
 */
export function activityDetail(a: Activity): string | null {
  switch (a.field) {
    case 'comment':
    case 'dod':
    case 'link':
    case 'reference.code':
    case 'reference.doc':
      break
    default:
      return null
  }
  const shownValue = childChange(a) === 'removed' ? a.old_value : a.new_value
  return blank(shownValue) ? null : (shownValue as string)
}
