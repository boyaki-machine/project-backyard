# 開発・ビルドの入口（Design.md 4.1 / 4.2）
#
# 各ターゲットは実装手順の進行に合わせて追加していく。
# 現在は Phase 1 手順2（マイグレーションの適用）までに必要なものだけを定義している。

COMPOSE := docker compose -f deploy/base/compose.yaml

# ── バージョン（Design.md 11.1）────────────────────────────────
# VERSION ファイルが正。ビルド番号は develop へのマージ回数と一致し、
# make version-check で git 側の実測と突き合わせる。
VERSION       := $(shell cat $(CURDIR)/VERSION)
VERSION_MAJOR := $(word 1,$(subst ., ,$(VERSION)))
VERSION_MINOR := $(word 2,$(subst ., ,$(VERSION)))
VERSION_BUILD := $(word 3,$(subst ., ,$(VERSION)))

# develop へのマージ回数。マージコミットのみを数えるので、
# develop 上の直接コミットではビルド番号が動かない。
MERGES_ON_DEVELOP = $(shell git rev-list --count --first-parent --merges develop 2>/dev/null)

# bump-* は feature ブランチ上で「これからマージする」前提で走らせるため +1 する。
NEXT_BUILD = $$(( $(MERGES_ON_DEVELOP) + 1 ))

# 手順13以降の make build / build-release.sh が使う（Design.md 4.5）。
LDFLAGS := -s -w -X main.version=$(VERSION)

# マイグレーションは DDL を実行するため pb_owner で接続する（DbDesign.md 3.4）。
# パスワードは secret ファイルから recipe 内で読む。Makefile にも argv にも残さない。
DB_PASSWORD_FILE := $(CURDIR)/deploy/dev/secrets/db_password
GOOSE_DBSTRING_OWNER = postgres://pb_owner:$$(cat $(DB_PASSWORD_FILE))@127.0.0.1:5432/pb?sslmode=disable

.PHONY: up down psql migrate version version-check bump-build bump-minor bump-major release-tag

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

# ── バージョン操作（Design.md 11.1）────────────────────────────

## 現在のバージョンを表示する
version:
	@echo "v$(VERSION)  (major=$(VERSION_MAJOR) minor=$(VERSION_MINOR) build=$(VERSION_BUILD))"
	@echo "develop へのマージ回数（git 実測）: $(MERGES_ON_DEVELOP)"

## VERSION のビルド番号が develop のマージ回数と一致するか検証する（マージ後に実行）
version-check:
	@if [ "$(VERSION_BUILD)" != "$(MERGES_ON_DEVELOP)" ]; then \
		echo "NG: VERSION のビルド番号 $(VERSION_BUILD) が develop のマージ回数 $(MERGES_ON_DEVELOP) と一致しない"; \
		echo "    feature ブランチ上で make bump-build / bump-minor / bump-major を実行してからマージすること"; \
		exit 1; \
	fi
	@echo "OK: v$(VERSION)（develop へのマージ $(MERGES_ON_DEVELOP) 回）"

## ビルド番号のみ上げる（hotfix・文書修正など、機能が変わらないマージ）
bump-build:
	@printf '%s.%s.%s\n' '$(VERSION_MAJOR)' '$(VERSION_MINOR)' "$(NEXT_BUILD)" > $(CURDIR)/VERSION
	@echo "v$(VERSION) -> v$$(cat $(CURDIR)/VERSION)"

## マイナーを上げる（機能追加の feature マージ）
bump-minor:
	@printf '%s.%s.%s\n' '$(VERSION_MAJOR)' "$$(( $(VERSION_MINOR) + 1 ))" "$(NEXT_BUILD)" > $(CURDIR)/VERSION
	@echo "v$(VERSION) -> v$$(cat $(CURDIR)/VERSION)"

## メジャーを上げる（開発者が機能まとまりで判断。マイナーは 0 に戻る）
bump-major:
	@printf '%s.0.%s\n' "$$(( $(VERSION_MAJOR) + 1 ))" "$(NEXT_BUILD)" > $(CURDIR)/VERSION
	@echo "v$(VERSION) -> v$$(cat $(CURDIR)/VERSION)"

## リリースタグを打つ（develop 上で実行。push は手動）
release-tag: version-check
	@git tag -a "v$(VERSION)" -m "Release v$(VERSION)"
	@echo "タグ v$(VERSION) を作成した。push は git push origin v$(VERSION) で手動で行う"
