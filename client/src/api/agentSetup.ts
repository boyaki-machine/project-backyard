/**
 * エージェント連携セットアップのエンドポイント（`ApiDesign.md` 5.7、手順28a）。
 *
 * **リポジトリにコミットする配置ファイルを取る口である**（`Requirements.md` 10.9.1
 * の系統A）。**接続設定は返らない**——履歴管理の対象外なので、リポジトリに置くものを
 * 作るこの経路には置き場がない（10.8.1）。各人が `/me/agents` から受け取る（手順28b）。
 *
 * **必要権限は `agent.register`**（`GuiDesign.md` 3.2）。`project.edit` ではない。
 */
import { api, BASE_PATH } from './client'
import type { components } from './schema'

export type AgentSetup = components['schemas']['AgentSetup']
export type AgentSetupFile = components['schemas']['AgentSetupFile']

/**
 * `client` を繰り返し指定したクエリ文字列を組み立てる（5.7.1）。
 *
 * **`URLSearchParams` に同じキーを複数 append する。** サーバは
 * `r.URL.Query()["client"]` で受けるので、`client=a&client=b` の形が要る
 * ——カンマ区切りにすると1件の未知の値として 422 になる。
 */
function clientQuery(clients: readonly string[]): string {
  const params = new URLSearchParams()
  for (const c of clients) params.append('client', c)
  return params.toString()
}

/** 配置ファイル一式（5.7.1）。 */
export function getAgentSetup(key: string, clients: readonly string[]): Promise<AgentSetup> {
  return api.get<AgentSetup>(
    `/projects/${encodeURIComponent(key)}/agent-setup?${clientQuery(clients)}`,
  )
}

/**
 * zip のダウンロード URL（5.7.2）。
 *
 * **`fetch` ではなく `<a download href>` に渡す。** 認証は Cookie が載るので、
 * ブラウザがそのまま落とせる。**`api` を通さないのは、応答が JSON ではないため**で、
 * ベースパスだけ `client.ts` から借りる（写しを置かない）。
 */
export function agentSetupZipURL(key: string, clients: readonly string[]): string {
  return `${BASE_PATH}/projects/${encodeURIComponent(key)}/agent-setup.zip?${clientQuery(clients)}`
}

// ── 系統B：自分の接続設定（`ApiDesign.md` 4.5.8、手順28b）────────

export type AgentConnect = components['schemas']['AgentConnect']

/**
 * そのエージェント1件のための接続設定（4.5.8.1）。
 *
 * **系統A と対になる。** あちらはリポジトリにコミットするファイル（管理者が1回）、
 * こちらは**各人の手元にしか残らないもの**（本人が何度でも）。
 *
 * **`files` は空になりうる**（`has_setup_template` が偽の種別。4.5.8.3）。
 * **エラーではない**ので、画面はカードを消さず「自分で設定するための値」を出す。
 *
 * **接続できたかどうかはここに無い**——`GET /me/agents` の `token.last_used_at` が
 * それを表す（同じ事実を2か所から出さない）。
 */
export function getAgentConnect(agentID: string): Promise<AgentConnect> {
  return api.get<AgentConnect>(`/me/agents/${encodeURIComponent(agentID)}/setup`)
}

/**
 * zip のダウンロード URL（4.5.8.5）。
 *
 * **`fetch` ではなく `<a download href>` に渡す**（系統A と同じ。認証は Cookie）。
 * zip には手引き（`PB-README.md`）と、**改名した**接続設定が入る。
 */
export function agentConnectZipURL(agentID: string): string {
  return `${BASE_PATH}/me/agents/${encodeURIComponent(agentID)}/setup.zip`
}
