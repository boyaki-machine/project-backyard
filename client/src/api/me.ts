/**
 * 自分自身に関するエンドポイント（`ApiDesign.md` 4.2 / 4.3）。
 *
 * `GET /me` は `api/auth.ts` にある——起動時の復元で使うものであり、
 * 「自分の設定」画面の持ち物ではないためである。
 *
 * 型は `docs/design/openapi.yaml` の生成物をそのまま使う。ここで別名を定義し直さない。
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

export function putAvatar(image: Blob): Promise<Session> {
  return api.putBlob<Session>('/me/avatar', image)
}

export function deleteAvatar(): Promise<Session> {
  return api.del<Session>('/me/avatar')
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

// ── エージェント（`ApiDesign.md` 4.5）──────────────────────────
//
// **`/me` 配下なので本ファイルに置く。** `api/agents.ts` を別に作らないのは、
// これらが「自分のエージェント」の操作であり、他人のエージェントを扱う経路
// （`/admin/users` のエージェントタブ。`ApiDesign.md` 6.1）とは別物だからである。

/** `GET /me/agents` が返す1件（`ApiDesign.md` 4.5.1）。**平文のトークンを含まない** */
export type MyAgent = components['schemas']['MyAgent']

/** 1件につき1本の有効なトークン（4.5.1）。無ければ `null` */
export type AgentToken = components['schemas']['AgentToken']

/** `POST /me/agents/:id/tokens` の 201（4.5.3）。**`token` を持つ唯一の形** */
export type IssuedAgentToken = components['schemas']['IssuedAgentToken']

export type CreateAgentRequest = components['schemas']['CreateAgentRequest']
export type UpdateAgentRequest = components['schemas']['UpdateAgentRequest']
export type CreateAgentTokenRequest = components['schemas']['CreateAgentTokenRequest']

/** `GET /agent-client-kinds` が返す1件（`ApiDesign.md` 4.5.7） */
export type AgentClientKind = components['schemas']['AgentClientKind']

/**
 * クライアント種別のカタログ（`ApiDesign.md` 4.5.7）。
 *
 * **必要権限は無い**（認証済みであればよい）。`sort_order` の昇順で返るので、
 * **画面は並べ替えない。**
 *
 * **画面に対応表を持たせないために在る**——値域は今後も増える
 * （`DbDesign.md` 8.2.1.1）ので、写しを置くと必ず腐る。
 */
export function listAgentClientKinds(): Promise<{ items: AgentClientKind[] }> {
  return api.get<{ items: AgentClientKind[] }>('/agent-client-kinds')
}

/** `GET /agent-scopes` の応答（`ApiDesign.md` 4.5.9） */
export type AgentScopes = components['schemas']['AgentScopes']

/**
 * エージェント用トークンの既定スコープと、既定に足せる権限（`ApiDesign.md` 4.5.9）。
 *
 * **画面に既定の写しを持たせないために在る。** 発行の `scopes` は絶対指定なので
 * （4.5.3）、「既定に `doc.edit` を足す」を送るには既定の中身が要る。写しは
 * 権限を足すたびに2回続けて腐った。
 */
export function getAgentScopes(): Promise<AgentScopes> {
  return api.get<AgentScopes>('/agent-scopes')
}

/**
 * 自分のエージェントを一覧する（`ApiDesign.md` 4.5.1）。
 *
 * **他人のものは返らない。** `created_at` の降順で、ページネーションも `ETag` も
 * 持たないので、返った配列がそのまま全件である。
 *
 * **無効化されたエージェントも返る**（`is_active: false`）。行は消えないため、
 * 畳むかどうかは画面が決める（`GuiDesign.md` 5.8.2）。
 */
export function listAgents(): Promise<{ items: MyAgent[] }> {
  return api.get<{ items: MyAgent[] }>('/me/agents')
}

/**
 * エージェントを登録する（`ApiDesign.md` 4.5.2）。
 *
 * **トークンは同時に発行されない。** 応答の `token` は必ず `null` で、画面は
 * 続けて `issueAgentToken` を呼ぶ（4.5.2 が定める）。
 *
 * `project_key` は**自分がメンバーであるプロジェクト**に限る。それ以外は 422
 * （`details[].field` が `project_key`、`code` が `not_found`）。
 */
export function createAgent(body: CreateAgentRequest): Promise<MyAgent> {
  return api.post<MyAgent>('/me/agents', body)
}

/**
 * エージェントを更新する（`ApiDesign.md` 4.5.4）。**送った項目だけが変わる。**
 *
 * `project_key` は変えられない（そのエージェントが行った仕事はプロジェクトに
 * 属するため）。**`client_kind` は変えられる**——値域が今後も増えるので、
 * `other` で登録した人が、PB がその種別に対応した日に移れる必要がある。
 *
 * **`client_kind` か `display_name` を変えると重複しうる**（キーは所有者・
 * プロジェクト・クライアント種別・表示名の4つ組）。重複は 409 `already_exists`。
 *
 * **`is_active: false` にすると、そのエージェントのトークンも失効する。**
 */
export function updateAgent(id: string, body: UpdateAgentRequest): Promise<MyAgent> {
  return api.patch<MyAgent>(`/me/agents/${encodeURIComponent(id)}`, body)
}

/**
 * エージェント用トークンを発行する（`ApiDesign.md` 4.5.3）。
 *
 * **`token`（平文）はこの応答でしか得られない。** 呼び出し側は1回だけ画面に出し、
 * 再取得できないことを明記すること（`GuiDesign.md` 5.8.2）。
 *
 * **有効なトークンは1件につき1本。** 既に在れば暗黙に失効させたうえで発行する
 * ——画面は押す前にその旨を出す。無効化されたエージェントには 409。
 *
 * **`scopes` は省略できる**（4.5.3。手順26a で「受け取らない」から改めた）。
 * 省略すると `Design.md` 6.5 の既定8件。渡すときは**許可リストの中だけ**で、
 * 既定に足せるのは `doc.edit`（`pb_put_doc` が要求する権限）の1件である。
 *
 * **所有者の権限との積になる**ので、`doc.edit` を選んでも所有者が
 * `project_admin` でなければ実効権限には入らない。
 */
export function issueAgentToken(
  id: string,
  body: CreateAgentTokenRequest,
): Promise<IssuedAgentToken> {
  return api.post<IssuedAgentToken>(`/me/agents/${encodeURIComponent(id)}/tokens`, body)
}

/**
 * エージェント用トークンを失効させる（`ApiDesign.md` 4.5.5）。
 *
 * **冪等。** 既に失効済みでも 204 が返る。自分のものでなければ 404。
 */
export function revokeAgentToken(id: string, tokenID: string): Promise<void> {
  return api.del<void>(
    `/me/agents/${encodeURIComponent(id)}/tokens/${encodeURIComponent(tokenID)}`,
  )
}

/**
 * エージェントを削除する（`ApiDesign.md` 4.5.4）。
 *
 * **物理削除である。** `agent` と発行済みトークンが CASCADE で消え、
 * **そのエージェントの資格情報は1本残らず消える。**
 *
 * **そのエージェントが書いたコメントは残る**——書き手が
 * 「削除されたエージェント」へ付け替わり、アバターは角丸四角のままである
 * （`GuiDesign.md` 8.4.2）。監査ログも残る。
 *
 * **無効化（`updateAgent({is_active:false})`）とは別の操作である。**
 * 「いま止めたいが記録は残したい」が無効化、「配った資格情報ごと消したい」が削除。
 */
export function deleteAgent(id: string): Promise<void> {
  return api.del<void>(`/me/agents/${encodeURIComponent(id)}`)
}
