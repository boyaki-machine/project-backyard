#!/usr/bin/env bash
#
# PB を起動する（make release が同梱した）。使い方は MANUAL.md の3章。
#
#   ./run.sh                          サーバを起動する（pb serve）
#   ./run.sh admin create             初期管理者を作る（サーバと同じ設定で DB へ繋ぐ）
#   nohup ./run.sh > pb.log 2>&1 &    背景で起動し、ログをファイルへ書く
#
# **pb を直に叩かず、このスクリプトから起動する。** 設定ファイルと秘密の位置、
# 待受をここで渡しているため。
set -euo pipefail

# **自分のあるディレクトリへ移る。** 下のパスは一式からの相対なので、
# どこから呼ばれても同じファイルを指すようにする。
cd "$(dirname "${BASH_SOURCE[0]}")"

if [ ! -f ./secrets/app_database_url ]; then
	echo "エラー: secrets/app_database_url が無い。MANUAL.md「3.4 秘密を置く」を読むこと。" >&2
	exit 1
fi

# 設定ファイル。何も書かなくても動く（MANUAL.md「3.9 設定を変える」）。
export PB_CONFIG_FILE=./pb.yaml

# pb_app での接続文字列。パスワードを含むので、値ではなくファイルの位置を渡す。
export PB_DATABASE_URL_FILE=./secrets/app_database_url

# **待受は、この端末からだけ届く形に固定する。** PB の既定値はすべてのアドレス
# （0.0.0.0）で、そのままでは同じネットワークの他の端末から平文で届いてしまう。
# この行があるかぎり、画面の「待受アドレス」は「環境変数で固定」と表示される。
# 端末の外へ出すときは、MANUAL.md「3.10 端末の外へ出す」を読んでからこの行を直す。
export PB_BIND=127.0.0.1:8080

if [ "$#" -eq 0 ]; then
	set -- serve
fi
exec ./pb "$@"
