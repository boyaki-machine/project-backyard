/**
 * 自分自身に関するエンドポイント（`ApiDesign.md` 4.2 / 4.3）。
 *
 * `GET /me` は `api/auth.ts` にある——起動時の復元で使うものであり、
 * 「自分の設定」画面の持ち物ではないためである。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 */
import { api } from './client'
import type { Session } from './auth'
import type { components } from './schema'

/** `PATCH /me` のリクエスト本体（`ApiDesign.md` 4.2） */
export type UpdateMeRequest = components['schemas']['UpdateMeRequest']

/** `POST /me/password` のリクエスト本体（`ApiDesign.md` 4.3） */
export type ChangePasswordRequest = components['schemas']['ChangePasswordRequest']

/**
 * 自分のプロフィールと見た目の設定を更新する（`ApiDesign.md` 4.2）。
 *
 * **応答は `GET /me` と同じ `Session` である。** 呼び出し側は
 * `auth` ストアへそのまま流し込める（`GuiDesign.md` 6.4「サーバ応答後に反映」）。
 *
 * **送らなかった項目は変わらない。** 部分更新なので、`{ theme: 'dark' }` の
 * ように1項目だけ送ってよい。
 */
export function updateMe(body: UpdateMeRequest): Promise<Session> {
  return api.patch<Session>('/me', body)
}

/**
 * パスワードを変更する（`ApiDesign.md` 4.3）。
 *
 * 成功すると **現在のセッションを除く全セッションが失効する**。204 なので
 * 本体は返らず、`must_change_password` の解除を画面に反映するには
 * `GET /me` を読み直す必要がある。
 */
export function changePassword(body: ChangePasswordRequest): Promise<void> {
  return api.post<void>('/me/password', body)
}

// ── アクセストークン（`ApiDesign.md` 4.4）────────────────────────

/** `GET /me/tokens` が返す1行（`ApiDesign.md` 4.4.1）。**平文を含まない** */
export type AccessToken = components['schemas']['AccessToken']

/** `POST /me/tokens` の 201（`ApiDesign.md` 4.4.2）。**`token` を持つ唯一の形** */
export type IssuedAccessToken = components['schemas']['IssuedAccessToken']

/** `POST /me/tokens` のリクエスト本体 */
export type CreateTokenRequest = components['schemas']['CreateTokenRequest']

/**
 * 自分のアクセストークンを一覧する（`ApiDesign.md` 4.4.1）。
 *
 * **失効済みは返らない。期限切れは返る**（`status` が `expired`）。
 * ページネーションが無いので、返った配列がそのまま全件である。
 */
export function listTokens(): Promise<{ items: AccessToken[] }> {
  return api.get<{ items: AccessToken[] }>('/me/tokens')
}

/**
 * アクセストークンを発行する（`ApiDesign.md` 4.4.2）。
 *
 * **`token`（平文）はこの応答でしか得られない。** 呼び出し側は1回だけ
 * 画面に出し、再取得できないことを明記すること（`GuiDesign.md` 5.8.1）。
 *
 * 失効していないトークンが既に5本あると 409。
 */
export function createToken(body: CreateTokenRequest): Promise<IssuedAccessToken> {
  return api.post<IssuedAccessToken>('/me/tokens', body)
}

/**
 * アクセストークンを失効させる（`ApiDesign.md` 4.4.3）。
 *
 * **冪等。** 既に失効済みでも 204 が返る。自分のものでなければ 404。
 */
export function revokeToken(id: string): Promise<void> {
  return api.del<void>(`/me/tokens/${encodeURIComponent(id)}`)
}
