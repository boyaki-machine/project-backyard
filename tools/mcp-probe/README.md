# mcp-probe — MCP クライアントの挙動を確かめる検証用サーバ

pb-158 のための道具である。**PB 本体ではない。** PB が「状態の変化（評価の完了など）をエージェントに知らせる」方式を選ぶために、
主要なクライアントが通知と長い待ち受けをどう扱うかを、同じ相手で比べる（`docs/design/Delegation.md` 10章）。

- 新旧2つの方式を話す。旧方式は `initialize` のハンドシェイクを行う版（2025-11-25 以前）、新方式はリクエストごとに `_meta` を付け、`server/discover` を持つ版（2026-07-28）
- **届いたリクエストをすべて JSON Lines で記録する。** どの方式で話してきたか、何を購読したか、待ち受けを何秒で切ったかが分かる
- 認証は無い。**localhost でだけ動かす**

## 動かす

```bash
cd tools/mcp-probe
go run . -addr 127.0.0.1:8765 -log /tmp/probe.jsonl
```

| 口 | 用途 |
|---|---|
| `POST /mcp`（`GET /mcp` は旧方式の SSE） | MCP の口 |
| `POST /admin/event?msg=<文>&kinds=resource,message,tools` | **状態の変化を起こす。** 待ち受けを起こし、開いているストリームへ通知を流す（`kinds` を省くと3種類すべて） |
| `GET /admin/state` | 出来事の数・開いているストリーム・待ち受けの数 |

| ツール | 動き |
|---|---|
| `probe_wait` | 状態が変わるか `timeout_seconds`（既定60、最大3600）が過ぎるまで応答を返さない。`since` に前回の `counter` を渡す。`progress_interval_seconds` を渡し、呼び出しに `progressToken` があれば、その間隔で進捗の通知を流す |
| `probe_status` | いまの `counter` と最後の出来事 |

リソース `probe://state` を1つ持つ。更新されると `notifications/resources/updated` を出す（購読されたときだけ）。

## 検証の手順（人が行うもの）

**確かめたいのは「エージェントが待機している（人の入力待ち）ときに通知が届くと、モデルが自分から動き出すか」である**（pb-158 の検証項目2）。
ヘッドレス実行では確かめられないので、対話の画面で行う。

1. プローブを起動する（上記）
2. クライアントにプローブを繋ぐ（下の表）。**PB の接続とは別の名前（`probe`）で足す**
3. クライアントで「`probe_status` を1回呼んで、結果を教えて。そのあとは何もせずに待って」と頼む。応答が終わり、入力待ちになるのを見る
4. 別の端末から出来事を起こす：`curl -X POST 'http://127.0.0.1:8765/admin/event?msg=評価が完了した'`
5. **クライアントの画面を1分見る。** 何か表示が出たか、モデルが発話を始めたか、何も起きないかを記録する
6. 記録（`/tmp/probe.jsonl`）を pb-158 に渡す。`"method"` と `"era"` の行で、どの方式で話し、何を購読したかが分かる

| クライアント | 繋ぎ方 |
|---|---|
| Claude Code | 作業フォルダの `.mcp.json` に `{"mcpServers":{"probe":{"type":"http","url":"http://127.0.0.1:8765/mcp"}}}` |
| Claude Desktop | `Settings > Developer > Edit Config` に、PB と同じ形で橋（`mcp-remote`）を書く。URL を `http://127.0.0.1:8765/mcp` にし、ヘッダは要らない（`Requirements.md` 10.8.4.2） |
| Codex | `~/.codex/config.toml` に `[mcp_servers.probe]` と `url = "http://127.0.0.1:8765/mcp"` |
| VS Code の Copilot | `.vscode/mcp.json` に `{"servers":{"probe":{"type":"http","url":"http://127.0.0.1:8765/mcp"}}}` |

**Claude Desktop の通知は橋を経由する。** 通知が届かなかったとき、届かなかったのがクライアントなのか橋なのかを、記録の `stream` の行で切り分ける
（橋が `GET` か `subscriptions/listen` でストリームを張っていなければ、橋で止まっている）。
