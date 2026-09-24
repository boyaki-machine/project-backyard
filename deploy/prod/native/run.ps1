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

# **待受はここで固定しない**。PB の既定値が `127.0.0.1:8080` で、
# この端末からだけ届く形になっている。**固定しないので、画面の「待受アドレス」から
# 変えられる**——端末の外へ出すときは MANUAL.md「3.10 端末の外へ出す」を読むこと。

if ($args.Count -eq 0) {
    $pbArgs = @('serve')
} else {
    $pbArgs = $args
}
& (Join-Path $PSScriptRoot 'pb.exe') @pbArgs
exit $LASTEXITCODE
