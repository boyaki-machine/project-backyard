/**
 * 第2要素のエンドポイント（`ApiDesign.md` 4.6 / 3.4）。
 *
 * 画面は `GuiDesign.md` 5.8 のセキュリティセクションと 5.1.1。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 */
import { api } from './client'
import type { Session } from './auth'
import type { components } from './schema'

/** `GET /me/mfa` の応答（`ApiDesign.md` 4.6.1） */
export type MfaOverview = components['schemas']['MfaOverview']

/** 確定済みの認証器1件。**共有秘密を含まない** */
export type TotpCredential = components['schemas']['TotpCredential']

/** `POST /me/mfa/totp` の 201。**`secret` と `otpauth_uri` を持つ唯一の形** */
export type StartedTotpRegistration = components['schemas']['StartedTotpRegistration']

/** `POST /me/mfa/totp/{id}/confirm` の 200 */
export type ConfirmedTotp = components['schemas']['ConfirmedTotp']

/** `POST /auth/login` が第2要素を要求したときの応答（`ApiDesign.md` 3.1） */
export type MfaChallenge = components['schemas']['MfaChallenge']

/**
 * 登録済みの第2要素とリカバリコードの残数を読む（`ApiDesign.md` 4.6.1）。
 *
 * **確定していない登録は返らない。** `recovery_codes` が `null` なのは
 * 「1本も作っていない」で、`remaining: 0` の「作って全部使った」とは別の状態である。
 */
export function getMfa(): Promise<MfaOverview> {
  return api.get<MfaOverview>('/me/mfa')
}

/**
 * 認証アプリの登録を始める（`ApiDesign.md` 4.6.2）。
 *
 * **この時点では登録されていない。** `confirmTotp` が通るまで認証に影響しない。
 * 2回続けて呼ぶと1回目の登録は捨てられる（途中の行は1人1件まで）。
 */
export function startTotp(name: string): Promise<StartedTotpRegistration> {
  return api.post<StartedTotpRegistration>('/me/mfa/totp', { name })
}

/**
 * コードを照合して登録を確定させる（`ApiDesign.md` 4.6.3）。
 *
 * **初回だけ `recovery_codes` が入る。** 2件目以降の認証器では返らない。
 */
export function confirmTotp(id: string, code: string): Promise<ConfirmedTotp> {
  return api.post<ConfirmedTotp>(`/me/mfa/totp/${encodeURIComponent(id)}/confirm`, { code })
}

/**
 * 認証器を削除する（`ApiDesign.md` 4.6.4）。
 *
 * **最後の1件を消すとリカバリコードも消える。** 呼び出し側は `getMfa` を
 * 読み直して表示を更新する（204 なので応答に状態が乗らない）。
 */
export function deleteTotp(id: string): Promise<void> {
  return api.del<void>(`/me/mfa/totp/${encodeURIComponent(id)}`)
}

/**
 * リカバリコードを作り直す（`ApiDesign.md` 4.6.5）。
 *
 * **既存は未使用のものも含めて全部無効になる。** 平文はこの応答でしか出ない。
 */
export function regenerateRecoveryCodes(): Promise<{ recovery_codes: string[] }> {
  return api.post<{ recovery_codes: string[] }>('/me/mfa/recovery-codes', {})
}

/**
 * ログインの第2要素を確認する（`ApiDesign.md` 3.4）。
 *
 * **`code` と `recovery_code` はどちらか一方だけを渡す**（両方は 422）。
 * 成功するとセッション Cookie が発行され、応答は `GET /me` と同じ構造になる。
 */
export function loginMfa(
  body: { mfa_token: string; code?: string; recovery_code?: string },
): Promise<Session> {
  return api.post<Session>('/auth/login/mfa', body)
}
