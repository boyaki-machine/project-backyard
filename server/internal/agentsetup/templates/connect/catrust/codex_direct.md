**ローカル CA・社内 CA の証明書では、接続方式を「ローカル stdio ブリッジ」に選び直してください**
（画面の「接続方式」で選び、zip を取り直します）。ブリッジは OS の信頼ストアを読みます。

直接接続のまま試すなら、CA の証明書を `CODEX_CA_CERTIFICATE` で渡し、新しいシェルで
{{.ClientDisplayName}} を起動し直します。

```
export CODEX_CA_CERTIFICATE="$(mkcert -CAROOT)/rootCA.pem"
```

**確かめたこと：ありません。** 公式はこの変数が「ログイン・HTTPS・WebSocket」に効くとしており、
MCP の接続に効くとは書いていません。自己署名の証明書では、OS に登録しても直接接続では
受け付けられませんでした。
