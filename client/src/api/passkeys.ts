/**
 * パスキーのエンドポイント（`ApiDesign.md` 3.5 / 3.6 / 4.7）。
 *
 * 画面は `GuiDesign.md` 5.1.2（ログイン）と 5.8（自分の設定のセキュリティ）。
 *
 * **options と credential は WebAuthn の JSON 表現のまま受け渡す。** PB の命名規約
 * （snake_case）に変換しない——ブラウザの API が受け取る形そのものである（3.5）。
 * 変換は `lib/passkey.ts` がブラウザの API に任せる。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
 */
import { api } from './client'
import type { Session } from './auth'
import type { components } from './schema'

/** `POST /auth/passkey/options` と `POST /me/passkeys/options` の応答 */
export type PasskeyOptions = components['schemas']['PasskeyOptions']

/** 登録済みのパスキー1件。**公開鍵も credential_id も含まない**（4.7.1） */
export type Passkey = components['schemas']['Passkey']

/** `GET /me/passkeys` の応答 */
export type PasskeyList = components['schemas']['PasskeyList']

/**
 * パスキーでのログインを始める（`ApiDesign.md` 3.5）。
 *
 * **メールアドレスを送らない。** 挑戦は誰に対しても同じ形で作られる。
 */
export function startPasskeyLogin(): Promise<PasskeyOptions> {
  return api.post<PasskeyOptions>('/auth/passkey/options')
}

/**
 * パスキーの応答でログインする（`ApiDesign.md` 3.6）。
 *
 * 成功するとセッション Cookie が発行され、応答は `GET /me` と同じ構造になる。
 * **TOTP を登録していても第2要素は求められない**（`Design.md` 6.8.2）。
 */
export function loginPasskey(credential: unknown): Promise<Session> {
  return api.post<Session>('/auth/login/passkey', { credential })
}

/** 登録済みのパスキーを読む（`ApiDesign.md` 4.7.1） */
export function listPasskeys(): Promise<PasskeyList> {
  return api.get<PasskeyList>('/me/passkeys')
}

/**
 * パスキーの登録を始める（`ApiDesign.md` 4.7.2）。
 *
 * 2回続けて呼ぶと1回目の挑戦は捨てられる（登録の挑戦は1人1件まで）。
 */
export function startPasskeyRegistration(): Promise<PasskeyOptions> {
  return api.post<PasskeyOptions>('/me/passkeys/options')
}

/**
 * 認証器の応答で登録を確定させる（`ApiDesign.md` 4.7.3）。
 *
 * **名前の重複と件数は、サーバが挑戦を消費する前に確かめる**ので、409 のあとは
 * 名前だけを直して同じ `credential` を送り直せる。
 */
export function registerPasskey(name: string, credential: unknown): Promise<Passkey> {
  return api.post<Passkey>('/me/passkeys', { name, credential })
}

/**
 * パスキーを削除する（`ApiDesign.md` 4.7.4）。
 *
 * **端末の中のパスキーは消えない。** 呼び出し側は `listPasskeys` を読み直す。
 */
export function deletePasskey(id: string): Promise<void> {
  return api.del<void>(`/me/passkeys/${encodeURIComponent(id)}`)
}
