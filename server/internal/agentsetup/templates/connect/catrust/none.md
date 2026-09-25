**OS の信頼ストアを読むクライアントなら、手当ては要りません。** 読まないクライアントには、CA の証明書を渡します。

**Node で動くクライアント（Gemini CLI など）は、既定では OS の信頼ストアを読みません。** 起動する前のシェルで、次のどちらかを設定します。

OS の信頼ストアを読ませる（古い Node では効かないことがあります。そのときは次の方法を使います）：

```
export NODE_USE_SYSTEM_CA=1
```

CA の証明書を渡す（Node の版を問わない）：

```
export NODE_EXTRA_CA_CERTS="$(mkcert -CAROOT)/rootCA.pem"
```

**確かめたこと**：Node 24.14 単体で、どちらの設定も効くことを確認しました。**{{.ClientDisplayName}} からの接続は未確認です。**
