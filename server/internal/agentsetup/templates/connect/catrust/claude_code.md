**{{.ClientDisplayName}}（ネイティブ版）は手当て不要です。** 既定で OS の信頼ストアを読みます（`CLAUDE_CODE_CERT_STORE` の既定が `bundled,system`）。

**確かめたこと**：mkcert v1.4.4 のローカル CA で作った証明書の PB へ、`NODE_EXTRA_CA_CERTS` を設定していない Claude Code 2.1.281 から MCP で繋がりました。
