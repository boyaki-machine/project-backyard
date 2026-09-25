**手順**

1. 設定ファイル（`claude_desktop_config.json`）の `pb` の `env` に、次の1行が入っていることを確かめます。PB が生成した設定には最初から入っています。それより前に置いた設定には無いので、足します。

   ```
   "NODE_USE_SYSTEM_CA": "1"
   ```

2. {{.ClientDisplayName}} を完全に終了してから、起動し直します。
3. 「PB に参画して」と伝え、繋がることを確かめます。

**うまくいかないとき**

- 古い Node では 1 の行が効かないことがあります。代わりに、CA の証明書を絶対パスで渡します（`$(…)` は展開されません。`mkcert -CAROOT` が出す場所の `rootCA.pem` です）。

  ```
  "NODE_EXTRA_CA_CERTS": "<rootCA.pem の絶対パス>"
  ```

- ログ（macOS では `~/Library/Logs/Claude/mcp-server-pb.log`）に `UNABLE_TO_VERIFY_LEAF_SIGNATURE` が出る：1 の行が効いていません。完全に終了してから起動し直したかも見ます。
- ログに `DEPTH_ZERO_SELF_SIGNED_CERT` が出る：PB の証明書が自己署名です（この手順の対象外）。

**確かめたこと**：橋（`mcp-remote` 0.14.3）を {{.ClientDisplayName}} と同じ起動のしかた（設定の `command`・`args`・`env`）で動かし、1 の行があると繋がり（`initialize` と `tools/list`）、無いと `UNABLE_TO_VERIFY_LEAF_SIGNATURE` で落ちることを確認しました。**{{.ClientDisplayName}} のアプリが設定を読んで繋がるところは未確認です。**
