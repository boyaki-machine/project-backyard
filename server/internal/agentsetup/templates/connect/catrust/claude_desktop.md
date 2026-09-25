**{{.ClientDisplayName}} が起動する橋（`mcp-remote`）は Node で動き、Node は既定では OS の信頼ストアを読みません。** 設定ファイルの `env` に、次のどちらかを足します。

OS の信頼ストアを読ませる（古い Node では効かないことがあります。そのときは次の方法を使います）：

```
"NODE_USE_SYSTEM_CA": "1"
```

CA の証明書を渡す（Node の版を問わない）。**`$(…)` は展開されないので、絶対パスを書きます**（`mkcert -CAROOT` が出す場所の `rootCA.pem`）：

```
"NODE_EXTRA_CA_CERTS": "<rootCA.pem の絶対パス>"
```

足したら、{{.ClientDisplayName}} を完全に終了してから起動し直します。

**確かめたこと**：Node 24.14 単体で、どちらの設定も効くことを確認しました。**`mcp-remote` を経由した接続と、{{.ClientDisplayName}} からの起動は未確認です。**
