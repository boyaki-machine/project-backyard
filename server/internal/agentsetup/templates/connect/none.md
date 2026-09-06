# {{.ProjectName}} — 接続に必要な値

このファイルは Project Backyard が「{{.DisplayName}}」のために生成しました。

**PB は {{.ClientDisplayName}} 向けの設定ファイルを持っていません。**
下の値を使って、お使いのクライアントの作法で設定してください。

| | |
|---|---|
| 接続先（MCP） | `{{.MCPURL}}` |
| トランスポート | Streamable HTTP（**`POST` だけ**。SSE ストリームを持ちません） |
| 認証 | `Authorization: Bearer <トークン>` |
| 環境変数（推奨） | `{{.TokenEnvName}}` |
| プロジェクト | {{.ProjectName}}（`{{.ProjectKey}}`） |

## トークンを環境変数へ置く

```
{{.ExportLine}}
```

`~/.zshrc` か direnv（`.envrc`）に追記し、**発行時に一度だけ表示された値**を入れます。
控えていない場合は、PB の `自分の設定 → エージェント` で**再発行**してください。

**設定ファイルにトークンの実体を書かないでください。** 変数を参照する形にします。
**`.envrc` は履歴管理から外します。**

## 手で確かめる

```
curl -s {{.MCPURL}} \
  -H "Authorization: Bearer ${{"$"}}{{.TokenEnvName}}" \
  -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list"}'
```

ツールの一覧が返れば繋がっています。**ツールの名前と引数は PB が接続時に返す**ので、
どこかに書き写す必要はありません。

**PB の画面（自分の設定 → エージェント）に「接続済み」のチェックが付きます。**
