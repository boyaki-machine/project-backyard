# 開発・ビルドの入口（Design.md 4.1 / 4.2）
#
# 各ターゲットは実装手順の進行に合わせて追加していく。
# 現在は Phase 1 手順2（マイグレーションの適用）までに必要なものだけを定義している。

COMPOSE := docker compose -f deploy/base/compose.yaml

# マイグレーションは DDL を実行するため pb_owner で接続する（DbDesign.md 3.4）。
# パスワードは secret ファイルから recipe 内で読む。Makefile にも argv にも残さない。
DB_PASSWORD_FILE := $(CURDIR)/deploy/dev/secrets/db_password
GOOSE_DBSTRING_OWNER = postgres://pb_owner:$$(cat $(DB_PASSWORD_FILE))@127.0.0.1:5432/pb?sslmode=disable

.PHONY: up down psql migrate

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

## マイグレーションを適用する（goose v3。前進のみ。DbDesign.md 5.3）
# goose のバージョンは server/go.mod の tool ディレクティブで固定している。
# @ を付けて実行するのは、パスワードを含むコマンドをエコーさせないため。
migrate:
	@cd server && GOOSE_DRIVER=postgres GOOSE_DBSTRING="$(GOOSE_DBSTRING_OWNER)" \
		go tool goose -dir migrations up
