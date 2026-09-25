**手順**

1. 画面の「接続方式」で「ローカル stdio ブリッジ」を選び直し、zip を取り直します。ローカル CA・社内 CA の証明書ではブリッジを使います。
2. ブリッジの zip の手引き（`PB-README.md`）に従って置き直します。

**直接接続のまま試すとき（実機未確認）**

- CA の証明書を `CODEX_CA_CERTIFICATE` で渡し、新しいシェルで {{.ClientDisplayName}} を起動し直します。

  ```
  export CODEX_CA_CERTIFICATE="$(mkcert -CAROOT)/rootCA.pem"
  ```

**確かめたこと：ありません。** 公式はこの変数が「ログイン・HTTPS・WebSocket」に効くとしており、MCP の接続に効くとは書いていません。自己署名の証明書では、OS に登録しても直接接続では受け付けられませんでした。
