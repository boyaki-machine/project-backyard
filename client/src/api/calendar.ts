/**
 * 暦のエンドポイント（`ApiDesign.md` 5.8）。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う（`tags.ts` と同じ方針）。
 * 読むのは `project.view`、変えるのは `project.edit`。
 *
 * **日（`day`）は `YYYY-MM-DD` の文字列のまま扱う。** その日であって瞬間ではない
 * （`GuiDesign.md` 7.5）。`Date` に通すと UTC より西の地域で前日へずれる。
 */
import { api } from './client'
import type { components } from './schema'

export type ProjectCalendar = components['schemas']['ProjectCalendar']
export type ProjectCalendarSource = components['schemas']['ProjectCalendarSource']
export type ProjectCalendarDay = components['schemas']['ProjectCalendarDay']
export type ProjectCalendarDays = components['schemas']['ProjectCalendarDays']

const base = (key: string) => `/projects/${encodeURIComponent(key)}/calendar`

/** 取得元と取得の状態（5.8.1）。 */
export function getCalendar(key: string): Promise<ProjectCalendar> {
  return api.get<ProjectCalendar>(base(key))
}

/** 取得元を選ぶ・外す（5.8.2）。**選ぶだけで取りに行かない。** */
export function putSource(key: string, googleId: string | null): Promise<ProjectCalendar> {
  return api.put<ProjectCalendar>(`${base(key)}/source`, { google_id: googleId })
}

/** 祝日を取得する（5.8.3）。同じ暦は1時間に1回まで（429）。 */
export function fetchHolidays(key: string): Promise<ProjectCalendar> {
  return api.post<ProjectCalendar>(`${base(key)}/fetch`, undefined)
}

/** `.ics` の本文を取り込む（5.8.4）。 */
export function importIcs(key: string, filename: string, ics: string): Promise<ProjectCalendar> {
  return api.post<ProjectCalendar>(`${base(key)}/import`, { filename, ics })
}

/** 期間 `[from, to)` の休日・行事・上書き（5.8.5）。平日で何も無い日は返らない。 */
export function listDays(key: string, from: string, to: string): Promise<ProjectCalendarDays> {
  const q = new URLSearchParams({ from, to })
  return api.get<ProjectCalendarDays>(`${base(key)}/days?${q.toString()}`)
}

/** 日ごとの上書きを作る・置き換える（5.8.6）。 */
export function putDay(
  key: string,
  day: string,
  body: { is_holiday: boolean; name?: string },
): Promise<ProjectCalendarDay> {
  return api.put<ProjectCalendarDay>(`${base(key)}/days/${encodeURIComponent(day)}`, body)
}

/** 上書きを外す（5.8.6）。無くても成功する。 */
export function deleteDay(key: string, day: string): Promise<void> {
  return api.del<void>(`${base(key)}/days/${encodeURIComponent(day)}`)
}

/**
 * 国の一覧（`GuiDesign.md` 5.9.6）。Google の識別子は 2026-09-27 に取得できることを
 * 確かめたもの。**URL の入力欄は置かない**（`ApiDesign.md` 5.8.2 の SSRF の理由）。
 */
export const googleCalendarSources: ReadonlyArray<{ id: string; label: string }> = [
  { id: 'ja.japanese', label: '日本' },
  { id: 'ja.usa', label: 'アメリカ合衆国' },
  { id: 'ja.uk', label: 'イギリス' },
  { id: 'ja.china', label: '中国' },
  { id: 'ja.south_korea', label: '韓国' },
  { id: 'ja.taiwan', label: '台湾' },
  { id: 'ja.hong_kong', label: '香港' },
  { id: 'ja.philippines', label: 'フィリピン' },
  { id: 'ja.singapore', label: 'シンガポール' },
  { id: 'ja.indian', label: 'インド' },
  { id: 'ja.australian', label: 'オーストラリア' },
  { id: 'ja.canadian', label: 'カナダ' },
  { id: 'ja.german', label: 'ドイツ' },
  { id: 'ja.french', label: 'フランス' },
  { id: 'ja.vietnamese', label: 'ベトナム' },
  { id: 'ja.indonesian', label: 'インドネシア' },
]
