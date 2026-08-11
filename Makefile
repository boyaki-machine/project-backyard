# 開発・ビルドの入口（Design.md 4.1 / 4.2）
#
# 各ターゲットは実装手順の進行に合わせて追加していく。
# 現在は Phase 1 手順1（DB の起動とロール分離）までに必要なものだけを定義している。

COMPOSE := docker compose -f deploy/base/compose.yaml

.PHONY: up down psql

## DB を起動する
# TODO(手順13以降): deploy/Dockerfile 作成後、`up -d` に戻して app も起動対象にする
up:
	$(COMPOSE) up -d db

## コンテナを停止する（pgdata ボリュームは残す）
down:
	$(COMPOSE) down

## DBコンソールを開く
psql:
	$(COMPOSE) exec db psql -U pb_owner -d pb
