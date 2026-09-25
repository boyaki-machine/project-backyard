**手順**

1. 何もしなくて構わない見込みです。VS Code は `http.systemCertificates`（既定で有効）で OS の信頼ストアを読みます。
2. Copilot をエージェントモードにして参画の手順を実行し、繋がることを確かめます。

**うまくいかないとき**

- VS Code の設定で `http.systemCertificates` が有効かを見ます。

**確かめたこと：ありません。** VS Code の MCP では、自己署名の証明書で接続に失敗する報告があります（microsoft/vscode#248245）。
