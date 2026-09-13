# PB のスキーマを進める（make release が同梱した）。使い方は MANUAL.md の3章。
#
#   .\migrate.ps1           未適用のマイグレーションをすべて適用する（goose up）
#   .\migrate.ps1 status    適用状況を表示する
#
# 接続先は migrate.conf に、pb_owner のパスワードは secrets\pgpass に置く。
# **パスワードを環境変数の値にもコマンドライン引数にも出さない。** 渡すのは
# passfile の位置（PGPASSFILE）だけで、中身は goose（が使う pgx）が自分で読む。
#
# **Windows の実機ではまだ動かしていない**（MANUAL.md「3.12 Windows で使う」）。

$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot

$conf = Join-Path $PSScriptRoot 'migrate.conf'
if (-not (Test-Path -LiteralPath $conf)) {
    Write-Error 'migrate.conf が無い。'
}

# コメントと空行を除いた最初の行を、接続先として読む。
$url = Get-Content -LiteralPath $conf |
    ForEach-Object { $_.Trim() } |
    Where-Object { $_ -ne '' -and -not $_.StartsWith('#') } |
    Select-Object -First 1
if (-not $url) {
    Write-Error 'migrate.conf に接続先が書かれていない。'
}

# **接続先にパスワードを書かせない。** 書くと goose の引数に載り、他のプロセスから見える。
if ($url -match '://[^/@]*:[^/@]*@') {
    Write-Error 'migrate.conf の接続先にパスワードが含まれている。パスワードは secrets\pgpass に書くこと（MANUAL.md「3.4 秘密を置く」）。'
}

$pgpass = Join-Path $PSScriptRoot 'secrets\pgpass'
if (-not (Test-Path -LiteralPath $pgpass)) {
    Write-Error 'secrets\pgpass が無い。MANUAL.md「3.4 秘密を置く」を読むこと。'
}
$env:PGPASSFILE = $pgpass

if ($args.Count -eq 0) {
    $gooseArgs = @('up')
} else {
    $gooseArgs = $args
}
& (Join-Path $PSScriptRoot 'goose.exe') -dir (Join-Path $PSScriptRoot 'migrations') postgres $url @gooseArgs
exit $LASTEXITCODE
