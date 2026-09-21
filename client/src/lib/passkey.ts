/**
 * ブラウザ側のパスキー（WebAuthn）の手続き（`Design.md` 6.8、`GuiDesign.md` 5.1.2 / 5.8）。
 *
 * **サーバの options をそのまま WebAuthn の API に渡す。** JSON からの変換は
 * `PublicKeyCredential.parse*OptionsFromJSON()`、応答の JSON 化は `toJSON()` に任せ、
 * base64url の変換を自前で持たない（`ApiDesign.md` 3.5）。
 */

/** サーバが返す options の外形（`PasskeyOptions.options`） */
export type PasskeyOptionsEnvelope = {
  publicKey: Record<string, unknown>
  mediation?: string
}

/**
 * このブラウザでパスキーを使えるか。
 *
 * **JSON 変換の API まで揃っているかを見る。** `PublicKeyCredential` だけがあって
 * 変換の API が無い古いブラウザでは、押してから失敗することになる。
 */
export function passkeySupported(): boolean {
  return (
    typeof window !== 'undefined' &&
    typeof window.PublicKeyCredential === 'function' &&
    typeof PublicKeyCredential.parseRequestOptionsFromJSON === 'function' &&
    typeof PublicKeyCredential.parseCreationOptionsFromJSON === 'function'
  )
}

/**
 * いま開いているホスト名が IP アドレスか（`Design.md` 6.8.3）。
 *
 * **IP アドレスは RP ID になれない**ので、ボタンを押せなくして理由を出す。
 * サーバも 409 を返すが、押してから断られるより押す前に読めるほうがよい。
 */
export function openedByIPAddress(hostname: string = window.location.hostname): boolean {
  // IPv6 は `[::1]` の形で入る
  if (hostname.startsWith('[') || hostname.includes(':')) return true
  return /^\d{1,3}(\.\d{1,3}){3}$/.test(hostname)
}

/** IP アドレスで開いているときに出す文言（`GuiDesign.md` 5.1.2 / 5.8） */
import { uiText } from '../locales/ui'

export function ipAddressReason(): string {
  return uiText('IP アドレスで開いた画面ではパスキーを使えません')
}

/**
 * 利用者が端末のダイアログを閉じたか。
 *
 * **このときは何も出さない**（`GuiDesign.md` 5.1.2）。自分で取り消した操作に
 * 赤い帯を出さない。期限切れも同じ `NotAllowedError` で来る。
 */
export function isPasskeyCancelled(e: unknown): boolean {
  return e instanceof DOMException && (e.name === 'NotAllowedError' || e.name === 'AbortError')
}

/**
 * 同じ認証器が既に登録されているか（`excludeCredentials` に当たった）。
 *
 * ブラウザが登録の前に断ると `InvalidStateError` が来る（`ApiDesign.md` 4.7.2）。
 */
export function isPasskeyAlreadyRegistered(e: unknown): boolean {
  return e instanceof DOMException && e.name === 'InvalidStateError'
}

/** `navigator.credentials.get()` を呼び、応答を JSON にして返す（ログイン） */
export async function getPasskey(options: PasskeyOptionsEnvelope): Promise<unknown> {
  const publicKey = PublicKeyCredential.parseRequestOptionsFromJSON(
    options.publicKey as unknown as PublicKeyCredentialRequestOptionsJSON,
  )
  const credential = await navigator.credentials.get({ publicKey })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new Error(uiText('パスキーを取得できませんでした'))
  }
  return credential.toJSON()
}

/** `navigator.credentials.create()` を呼び、応答を JSON にして返す（登録） */
export async function createPasskey(options: PasskeyOptionsEnvelope): Promise<unknown> {
  const publicKey = PublicKeyCredential.parseCreationOptionsFromJSON(
    options.publicKey as unknown as PublicKeyCredentialCreationOptionsJSON,
  )
  const credential = await navigator.credentials.create({ publicKey })
  if (!(credential instanceof PublicKeyCredential)) {
    throw new Error(uiText('パスキーを作成できませんでした'))
  }
  return credential.toJSON()
}
