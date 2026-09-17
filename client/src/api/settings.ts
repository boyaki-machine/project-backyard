/**
 * アプリケーション設定のエンドポイント（`ApiDesign.md` 11.1 / 11.2）。
 *
 * 型は `docs/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない
 * （`roles.ts` と同じ方針）。
 */
import { api, BASE_PATH } from './client'
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

export type TLSCertificateList = components['schemas']['TLSCertificateList']
export type TLSCertificate = components['schemas']['TLSCertificate']

/** 証明書の状態（11.4 の `status`）。**サーバが決める**——画面が日付から組み立てない。 */
export type CertificateStatus = TLSCertificate['status']

/**
 * 登録済みの TLS 証明書（`ApiDesign.md` 11.4）。
 *
 * **`private_key` は含まれない。** 暗号化して保持しており、この応答にも
 * 他のどの応答にも現れない。
 */
export function getCertificates(): Promise<TLSCertificateList> {
  return api.get<TLSCertificateList>('/admin/tls/certificates')
}

/**
 * 証明書を登録する（`ApiDesign.md` 11.5）。
 *
 * **貼った秘密鍵は二度と表示されない。** 呼び出し側はその旨を画面に出すこと。
 */
export function uploadCertificate(certPem: string, keyPem: string): Promise<TLSCertificate> {
  return api.post<TLSCertificate>('/admin/tls/certificates', {
    cert_pem: certPem,
    key_pem: keyPem,
  })
}

/**
 * 証明書を消す（`ApiDesign.md` 11.6）。
 *
 * **最後の有効な証明書は 409 になる。** 消せると画面から復旧できなくなるため。
 */
export function deleteCertificate(id: string): Promise<void> {
  return api.del<void>(`/admin/tls/certificates/${encodeURIComponent(id)}`)
}

/** 確認を待っている設定変更（`ApiDesign.md` 11.8） */
export type PendingConfirmation = components['schemas']['PendingConfirmation']

/**
 * 確認を待っている設定変更を引く（`ApiDesign.md` 11.9）。
 *
 * **設定一覧と分けた軽い口である。** どの画面にいても未確認を出すために
 * 定期的に引くので、全設定の一覧を運ばない（pb-107）。
 *
 * **必要権限は `system.settings`。** 持たなければ 403 になるので、
 * 呼び出し側が権限を見てから叩くこと。
 */
export function getPendingSettings(): Promise<{
  pending_confirmation: PendingConfirmation | null
}> {
  return api.get('/admin/settings/pending')
}

/**
 * 締め出されうる設定変更を確定する（`ApiDesign.md` 11.8）。
 *
 * **確定すると以後は元へ戻らない。** 押されなければ期限で元の値へ戻る
 * （`Design.md` 10.3）。
 *
 * **新しい設定を通って届いていないと 409 になる。** `tls_enabled` を有効に
 * したなら、**https で開き直してから押す**必要がある。
 */
export function confirmSettings(): Promise<void> {
  return api.post<void>('/admin/settings/confirm')
}

/**
 * 証明書を取り出す URL（`ApiDesign.md` 11.7）。
 *
 * **`<a href>` で開く。** Cookie 認証なので追加のヘッダが要らず、サーバが付ける
 * `Content-Disposition` がそのままブラウザの保存に乗る。**画面が Blob を
 * 組み立てない。**
 *
 * **この口は循環を断つためにある**——自己署名証明書では、その証明書を持って
 * いないクライアントが PB へ繋げない（pb-100）。
 *
 * **落ちてくるのは zip である**（pb-108）。**`.crt` をそのまま返すとブラウザが
 * 「不審なファイル」として拒み、200 が返っているので失敗がどこにも残らない。**
 */
export function certificateZipUrl(id: string): string {
  return `${BASE_PATH}/admin/tls/certificates/${encodeURIComponent(id)}/certificate.zip`
}

export type DatabaseStatus = components['schemas']['DatabaseStatus']
export type DatabaseTable = components['schemas']['DatabaseTable']

/**
 * DB の接続状態と統計（`ApiDesign.md` 11.10。pb-110）。
 *
 * **件数は全表の `count(*)` である。** 行が増えるほど重くなるので、定期的に
 * 引かない——タブを開いたときと [再読み込み] のときだけ呼ぶ（`GuiDesign.md` 5.12.2）。
 *
 * **必要権限は `system.settings`。** パスワードは応答に含まれない。
 */
export function getDatabaseStatus(): Promise<DatabaseStatus> {
  return api.get<DatabaseStatus>('/admin/database')
}
