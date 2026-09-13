# PB を起動する（make release が同梱した）。使い方は MANUAL.md の3章。
#
#   .\run.ps1                 サーバを起動する（pb serve）
#   .\run.ps1 admin create    初期管理者を作る（サーバと同じ設定で DB へ繋ぐ）
#
# 実行ポリシーで止められたときは:
#   powershell -ExecutionPolicy Bypass -File .\run.ps1
#
# **Windows の実機ではまだ動かしていない**（MANUAL.md「3.12 Windows で使う」）。
# pb.exe を直に叩かず、このスクリプトから起動する。設定ファイルと秘密の位置、
# 待受をここで渡しているため。

$ErrorActionPreference = 'Stop'

# 自分のあるディレクトリへ移る。パスはすべてここからの絶対パスで渡す。
Set-Location -LiteralPath $PSScriptRoot

$databaseUrlFile = Join-Path $PSScriptRoot 'secrets\app_database_url'
if (-not (Test-Path -LiteralPath $databaseUrlFile)) {
    Write-Error 'secrets\app_database_url が無い。MANUAL.md「3.4 秘密を置く」を読むこと。'
}

# 設定ファイル。何も書かなくても動く（MANUAL.md「3.9 設定を変える」）。
$env:PB_CONFIG_FILE = Join-Path $PSScriptRoot 'pb.yaml'

# pb_app での接続文字列。パスワードを含むので、値ではなくファイルの位置を渡す。
$env:PB_DATABASE_URL_FILE = $databaseUrlFile

# **待受は、この端末からだけ届く形に固定する。** PB の既定値はすべてのアドレス
# （0.0.0.0）で、そのままでは同じネットワークの他の端末から平文で届いてしまう。
# この行があるかぎり、画面の「待受アドレス」は「環境変数で固定」と表示される。
# 端末の外へ出すときは、MANUAL.md「3.10 端末の外へ出す」を読んでからこの行を直す。
$env:PB_BIND = '127.0.0.1:8080'

if ($args.Count -eq 0) {
    $pbArgs = @('serve')
} else {
    $pbArgs = $args
}
& (Join-Path $PSScriptRoot 'pb.exe') @pbArgs
exit $LASTEXITCODE
