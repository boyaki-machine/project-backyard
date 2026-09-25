**手順**

1. 何もしなくて構いません。{{.ClientDisplayName}}（ネイティブ版）は、既定で OS の信頼ストアを読みます（`CLAUDE_CODE_CERT_STORE` の既定が `bundled,system`）。
2. {{.ClientDisplayName}} を起動して参画の手順を実行し、繋がることを確かめます。

**うまくいかないとき**

- `CLAUDE_CODE_CERT_STORE` を自分で設定している場合は、値に `system` が入っているかを見ます。

**確かめたこと**：mkcert v1.4.4 のローカル CA で作った証明書の PB へ、`NODE_EXTRA_CA_CERTS` を設定していない Claude Code 2.1.281 から MCP で繋がりました。
