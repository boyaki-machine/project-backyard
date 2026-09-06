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
