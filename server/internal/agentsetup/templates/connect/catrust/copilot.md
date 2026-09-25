**VS Code は `http.systemCertificates`（既定で有効）で OS の信頼ストアを読みます。** 設定を変えていなければ、
手当ては要らない見込みです。繋がらないときは、VS Code の設定でこの項目が有効かを見てください。

**確かめたこと：ありません。** VS Code の MCP では、自己署名の証明書で接続に失敗する報告があります
（microsoft/vscode#248245）。
