/**
 * アプリケーション設定のエンドポイント（`ApiDesign.md` 11.1 / 11.2）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない
 * （`roles.ts` と同じ方針）。
 */
import { api } from './client'
import type { components } from './schema'

export type SettingList = components['schemas']['SettingList']
export type Setting = components['schemas']['Setting']

/**
 * 実効値の出どころ（11.1 の `source`）。**並びは優先順と同じ**である
 * （`Design.md` 10.3）——上にあるものが下を押さえる。
 */
export type SettingSource = Setting['source']

/**
 * サーバ全体の設定と、各設定の実効値がどこから来たか（`ApiDesign.md` 11.1）。
 *
 * **必要権限は `system.settings`。** 持たなければ 403 になる。
 */
export function getSettings(): Promise<SettingList> {
  return api.get<SettingList>('/admin/settings')
}

/**
 * 変更のある設定をまとめて書く（`ApiDesign.md` 11.2）。
 *
 * **`value` を `null` にすると行を消し、既定値へ戻す。** 「既定に戻す」ための
 * 別のエンドポイントは無い——設定を消すことと既定へ戻すことは同じ状態である。
 *
 * **配列で送るのは、保存ボタンが1回だからである。** 1件ずつにすると監査ログが
 * 分かれ、途中で失敗したときに画面と DB がずれる。
 */
export function putSettings(
  items: { key: string; value: string | null }[],
): Promise<SettingList> {
  return api.put<SettingList>('/admin/settings', { items })
}
