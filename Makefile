# 開発・ビルドの入口（Design.md 4.1 / 4.2）
#
# 各ターゲットは実装手順の進行に合わせて追加していく。

COMPOSE := docker compose -f deploy/base/compose.yaml

# ドッグフーディング用インスタンス（Design.md 4.4）。base に stg を重ねる。
# **compose プロジェクト名が pb-stg に変わる**ので、コンテナ・ネットワーク・
# ボリュームが dev（project-backyard）と名前空間ごと分かれる。
# dev の make dev-reset（down -v）が stg に届かないのはこれによる。
STG_COMPOSE := docker compose -f $(CURDIR)/deploy/base/compose.yaml -f $(CURDIR)/deploy/stg/compose.yaml

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

# make build が使う。deploy/stg/build.sh と deploy/prod/build-release.sh も同じ値を
# 自前で組み立てている（Design.md 4.5）ので、変えるなら3か所を揃える。
LDFLAGS := -s -w -X main.version=$(VERSION)

# マイグレーションは DDL を実行するため pb_owner で接続する（DbDesign.md 3.4）。
# パスワードは secret ファイルから recipe 内で読む。Makefile にも argv にも残さない。
DB_PASSWORD_FILE := $(CURDIR)/deploy/dev/secrets/db_password
GOOSE_DBSTRING_OWNER = postgres://pb_owner:$$(cat $(DB_PASSWORD_FILE))@127.0.0.1:5432/pb?sslmode=disable

# アプリ（および pb admin create）は実行時ロール pb_app で接続する（DbDesign.md 3.4）。
# deploy/dev/secrets/app_database_url はコンテナ内から見た db:5432 を指すため、
# ホストで動かすターゲットでは app_db_password から 127.0.0.1 向けに組み立てる。
APP_DB_PASSWORD_FILE := $(CURDIR)/deploy/dev/secrets/app_db_password
PB_DATABASE_URL_APP = postgres://pb_app:$$(cat $(APP_DB_PASSWORD_FILE))@127.0.0.1:5432/pb?sslmode=disable&application_name=pb

# stg の DB（:5433）。**接続文字列は deploy/stg/secrets/app_database_url に
# 実体があり、そのまま渡せる**（dev と違い stg の PB はコンテナではなく
# ネイティブに動くため、ファイルの中身がホストから見た 127.0.0.1:5433 を指す）。
# goose は *_FILE を解さないので、migrate だけは owner の文字列を組み立てる。
STG_DB_PASSWORD_FILE := $(CURDIR)/deploy/stg/secrets/db_password
STG_APP_DATABASE_URL_FILE := $(CURDIR)/deploy/stg/secrets/app_database_url
STG_GOOSE_DBSTRING_OWNER = postgres://pb_owner:$$(cat $(STG_DB_PASSWORD_FILE))@127.0.0.1:5433/pb?sslmode=disable

.PHONY: up down stop-server restart psql migrate sqlc run admin-create admin-mfa-reset test test-db \
	dev-reset dev-seed dev-info \
	stg-init stg-up stg-down stg-psql stg-migrate stg-build stg-run stg-stop stg-admin-create \
	dev-client gen-api build-client sync-webui build clean-webui release \
	version version-check bump-build bump-minor bump-major release-tag \
	docs-size docs-emphasis css-tokens fmt-check vuln-check

## DB を起動する
# **app は起動しない。** dev では PB 本体を make run でホストから動かしており、app の
# コンテナまで上げると 8080 番でぶつかる。コンテナの一式は make release TARGET=compose。
up:
	$(COMPOSE) up -d db

## コンテナを停止する（pgdata ボリュームは残す）
down:
	$(COMPOSE) down

## :8080 を掴んでいるサーバを止める（コンテナではないので down では落ちない）
# make run は go run → 実バイナリの親子構成であり、親だけを殺すと子が
# ポートを掴んだまま残る（Development.md 3.1「止め方」）。PID で確実に止める。
# -sTCP:LISTEN を必ず付ける。付けないと ESTABLISHED も拾い、8080 へ接続中の
# ブラウザや curl の PID まで kill してしまう。
stop-server:
	@pids=$$(lsof -ti tcp:8080 -sTCP:LISTEN 2>/dev/null); \
	if [ -n "$$pids" ]; then \
		echo "サーバを停止する（PID: $${pids}）"; kill $$pids; sleep 1; \
	else \
		echo ":8080 を掴んでいるプロセスは無い"; \
	fi

## 作り直して起動し直す（停止 → ビルド → DB起動 → サーバ起動）
# 画面を直したあとに1コマンドで確かめるためのもの。**client の変更は
# make build を通さないと反映されない**（embed。Design.md 3.4）ため、
# サーバを再起動するだけでは古い画面が出続ける。
restart: stop-server down build up run

## DBコンソールを開く
psql:
	$(COMPOSE) exec db psql -U pb_owner -d pb

## マイグレーションを適用する（goose v3。前進のみ。DbDesign.md 5.3）
# goose と sqlc のバージョンは server/tools/go.mod の tool ディレクティブで固定している。
# ツールを別モジュールに隔離しているのは、server/go.mod にツールの推移依存
# （indirect 80件超）を持ち込まないため（DbDesign.md 5.1）。
# @ を付けて実行するのは、パスワードを含むコマンドをエコーさせないため。
migrate:
	@cd server/tools && GOOSE_DRIVER=postgres GOOSE_DBSTRING="$(GOOSE_DBSTRING_OWNER)" \
		go tool goose -dir ../migrations up

## sqlc でクエリからGoコードを生成する（Design.md 3.2）
# 生成物 server/internal/store/gen/ はコミットする（Design.md 4.6）。
# パスは server/sqlc.yaml からの相対で解決される。
sqlc:
	cd server/tools && go tool sqlc -f ../sqlc.yaml generate

## APIサーバをローカル起動する
# 待受を 127.0.0.1 に固定するのは、ホストで直接動かす場合の公開範囲を
# Design.md 6.6 の既定（127.0.0.1 のみ）に合わせるため。
# コンテナ内では 0.0.0.0:8080 で待ち受け、公開範囲は compose の ports で制御する。
# @ を付けて実行するのは、パスワードを含むコマンドをエコーさせないため。
# PB_HEALTH_SHOW_VERSION を開発時だけ true にするのは、起動しているバイナリの
# バージョンを /healthcheck で確かめられるようにするため。既定は false（ApiDesign.md 2.11）。
run:
	@cd server && PB_BIND=127.0.0.1:8080 PB_HEALTH_SHOW_VERSION=true \
		PB_DATABASE_URL="$(PB_DATABASE_URL_APP)" \
		go run -ldflags "-X main.version=$(VERSION)" ./cmd/pb serve

## 初期管理者を対話的に作成する（DbDesign.md 7.5）
# シードに管理者を含めないため、初回起動時に一度だけ実行する。
# @ を付けて実行するのは、パスワードを含むコマンドをエコーさせないため。
admin-create:
	@cd server && PB_DATABASE_URL="$(PB_DATABASE_URL_APP)" go run ./cmd/pb admin create

## 第2要素（TOTP・リカバリコード）を解除する（Design.md 6.7.5）
# **画面から解除できなくなった人のための口である。** 管理者が1人だけの構成で、
# その人が認証アプリとリカバリコードの両方を失うと、画面側の口（ApiDesign.md 6.9）は
# 誰も呼べない。使い方: make admin-mfa-reset EMAIL=tanaka@example.com
admin-mfa-reset:
	@test -n "$(EMAIL)" || (echo "EMAIL を指定してください（例: make admin-mfa-reset EMAIL=a@example.com）" && exit 1)
	@cd server && PB_DATABASE_URL="$(PB_DATABASE_URL_APP)" \
		go run ./cmd/pb admin mfa-reset --email "$(EMAIL)"

## テストを実行する
# **整形の検査を先に通す**。gofmt は go test が見ないので、
# 打つ人がいなければ発火しない。起票から4日間、誰も気づかないまま
# 別のチケットが偶然直した、という経緯が根拠である。
test: fmt-check
	@cd server && go test ./...

## 整形されていない Go のファイルが無いことを見る
# **パイプ越しに判定しない。** `gofmt -l . | ...` の $? は常にパイプの
# 最後のコマンドのものになり、判定が常に成功する。
# 出力を変数に取ってから中身の有無で見る。
fmt-check:
	@out=$$(cd server && gofmt -l ./cmd ./internal ./tools); \
	if [ -n "$$out" ]; then \
		echo "NG: gofmt が未整形と報告した"; \
		echo "$$out" | sed 's|^|    server/|'; \
		echo "    直す: cd server && gofmt -w <ファイル>"; \
		exit 1; \
	fi; \
	echo "OK: gofmt は未整形を報告しない"

## 依存の既知脆弱性を照合する
# **govulncheck の版は server/tools/go.mod の tool ディレクティブで固定する**
# ——goose・sqlc と同じ型である。go install でグローバルに入れると版が揃わず、
# 走らせる人によって結果が変わる。
#
# **-C で server を指す。** ツールは tools モジュールが持ち、解析の対象は
# 本体のモジュールなので、この2つは別である。
#
# **「呼んでいない」ものは既定では出ない。** govulncheck は到達可能性を見るので、
# import しているだけの脆弱性は件数の要約にしか現れない（全部見るなら -show verbose）。
#
# **片方が落ちても、もう片方は走らせる。** 素直に2行並べると Go 側で止まって
# client を一度も見ないまま終わる——**残りが何件あるかを知りたいのに、
# 最初の1件で調査が打ち切られる。** 両方の結果を出してから、どちらかが
# 落ちていれば非ゼロで返す（CI を置く日にそのまま関門として使える）。
vuln-check:
	@rc=0; \
	echo "── Go ──"; \
	(cd server/tools && go tool govulncheck -C .. ./...) || rc=1; \
	echo "── client ──"; \
	(cd client && npm audit) || rc=1; \
	if [ $$rc -ne 0 ]; then echo "NG: 未対処の脆弱性がある"; else echo "OK: 既知の脆弱性は無い"; fi; \
	exit $$rc

## 実DBを使う結合テストを実行する（Development.md 6.1）
# PB_TEST_DATABASE_URL が無いとテスト側が SKIP するため、通常の make test では
# 走らない。**秘密を argv にも Makefile にも残さない**ため、接続文字列は
# recipe 内で app_db_password から組み立て、@ でエコーを抑止する。
#
# 対象を -run Integration に絞るのは、結合テストの命名規約がこれであるため
# （Development.md 6.1）。RUN= で individual なテストへ絞れる。
#   make test-db RUN=TestMeTokensIntegration
#
# **パッケージを足したら並びにも足す。** dbstat は、pb_app から見える
# カタログと統計を確かめるので、実 DB でしか意味を持たない。backup も同じで、
# **書き出しは pb_app、取り込みは pb_owner** の2つのロールで確かめる必要がある
# （DbDesign.md 9.1.1）。オーナーの接続文字列も渡すのはそのためである。
RUN ?= Integration
test-db:
	@cd server && PB_TEST_DATABASE_URL="$(PB_DATABASE_URL_APP)" \
		PB_TEST_DATABASE_URL_OWNER="$(GOOSE_DBSTRING_OWNER)" \
		go test ./internal/httpapi/... ./internal/dbstat/... ./internal/backup/... \
			-run '$(RUN)' -count=1 -v

# ── 開発用デモデータ（DbDesign.md 7.6）────────────────────────
# 本番シード（マイグレーション 0010）とは別物。開発端末でしか使わない。

DEV_SEED_FILE := $(CURDIR)/deploy/dev/seed/dev-data.yaml

## 開発用：DBを作り直してデモデータを投入する（docker compose down -v を含む）
# 確認と待ち合わせを含む手続きなので deploy/dev/reset.sh に置いている（DbDesign.md 7.6.6）。
dev-reset:
	@COMPOSE="$(COMPOSE)" bash $(CURDIR)/deploy/dev/reset.sh

## 開発用：デモデータのみ投入する（冪等）
# PB_ALLOW_DEV_SEED をここで与えるのは、make が開発端末専用の入口だからである
# （DbDesign.md 7.6.3 の安全装置その1）。配布したバイナリを本番で直接叩いた場合は
# 環境変数が無いので止まり、あっても接続先ホストの検査（安全装置その2）が残る。
# @ を付けて実行するのは、パスワードを含むコマンドをエコーさせないため。
#
# **最後に放置のチケットを1件作る**（DbDesign.md 7.6.4、Development.md 8.6 と同じ SQL）。
# updated_at はトリガが now() で上書きし、止められるのはテーブルの所有者だけなので、
# pb_app で動く seed 本体ではなく、ここで pb_owner として振る。**打つたびに振り直す**
# （触って放置でなくなっても、dev-seed を打てば戻る）。対象はタイトルで指すので、
# dev-data.yaml でタイトルを変えたらここも直す。
DEV_STALE_TICKET := アーカイブの冪等性が怪しい

dev-seed:
	@cd server && PB_ALLOW_DEV_SEED=1 PB_DATABASE_URL="$(PB_DATABASE_URL_APP)" \
		go run ./cmd/pb dev seed --file "$(DEV_SEED_FILE)"
	@echo "==> 放置のチケットを1件作る（$(DEV_STALE_TICKET)。updated_at を20日前へ）"
	@$(COMPOSE) exec -T db psql -U pb_owner -d pb -v ON_ERROR_STOP=1 -c \
		"ALTER TABLE ticket DISABLE TRIGGER trg_ticket_updated; \
		 UPDATE ticket SET updated_at = now() - interval '20 days' \
		  WHERE title = '$(DEV_STALE_TICKET)' AND project_id = (SELECT id FROM project WHERE key = 'demo'); \
		 ALTER TABLE ticket ENABLE TRIGGER trg_ticket_updated;"

## 開発用：URL とデモアカウント一覧を表示する
# DBには接続しない。パスワードや URL を探す時間をなくすためのもの（DbDesign.md 7.6.6）。
dev-info:
	@cd server && go run ./cmd/pb dev info --file "$(DEV_SEED_FILE)"

# ── ドッグフーディング用インスタンス（Design.md 4.4）──────────
# PB 自身のプロジェクト管理に使う、壊れないインスタンス。
# **dev（:8080 / :5432 / project-backyard）とは compose プロジェクトごと分かれる**
# ので、make dev-reset の down -v は stg の pb-stg_pgdata に届かない。
#
# 画面は **http://localhost:8081** で開く。Cookie はポートを区別しないため、
# 127.0.0.1 で開くと dev のログインセッションと上書きし合う（Design.md 4.4）。

## stg：初回セットアップ（秘密の生成 → 設定 → DB起動 → migrate）
# 生成・待ち合わせ・条件分岐を含む手続きなので deploy/stg/init.sh に置いている。
# 何度実行しても壊れない（既にあるものは作り直さない）。
stg-init:
	@COMPOSE="$(STG_COMPOSE)" bash $(CURDIR)/deploy/stg/init.sh

## stg：DBを起動する
stg-up:
	$(STG_COMPOSE) up -d db

## stg：DBのコンテナを破棄する（pb-stg_pgdata ボリュームは残る）
# **通常は実行しない。** restart: always による自動起動も、コンテナごと消えるため
# 戻らなくなる（make stg-up で作り直す）。容量を空けたいときだけ使う。
stg-down:
	$(STG_COMPOSE) down

## stg：DBコンソールを開く
stg-psql:
	$(STG_COMPOSE) exec db psql -U pb_owner -d pb

## stg：マイグレーションを適用する
# DDL を実行するため pb_owner で接続する（DbDesign.md 3.4）。
# @ を付けて実行するのは、パスワードを含むコマンドをエコーさせないため。
stg-migrate:
	@cd $(CURDIR)/server/tools && GOOSE_DRIVER=postgres GOOSE_DBSTRING="$(STG_GOOSE_DBSTRING_OWNER)" \
		go tool goose -dir ../migrations up

## stg：動作に必要な一式を deploy/stg/out/ へ出力する
# 出力は丸ごと別のパスへ置いても動く（Design.md 4.4）。
# 別の場所へ出したいときは make stg-build OUT=/path/to/dir
OUT ?= $(CURDIR)/deploy/stg/out
stg-build:
	bash $(CURDIR)/deploy/stg/build.sh "$(OUT)"

## stg：出力した一式を前景で起動する（http://localhost:8081）
# **背景で動かすなら出力先で直接叩く**（Ctrl+C で止める前提の入口はこちら）:
#   nohup deploy/stg/out/run.sh > deploy/stg/out/pb.log 2>&1 &
stg-run:
	$(OUT)/run.sh

## stg：:8081 を掴んでいるサーバを止める
# stop-server（:8080）と同型。-sTCP:LISTEN を必ず付ける。付けないと
# ESTABLISHED も拾い、8081 へ接続中のブラウザや curl の PID まで kill してしまう。
stg-stop:
	@pids=$$(lsof -ti tcp:8081 -sTCP:LISTEN 2>/dev/null); \
	if [ -n "$$pids" ]; then \
		echo "stg のサーバを停止する（PID: $${pids}）"; kill $$pids; sleep 1; \
	else \
		echo ":8081 を掴んでいるプロセスは無い"; \
	fi

## stg：初期管理者を対話的に作成する（DbDesign.md 7.5）
# **デモデータ（make dev-seed）は入れない。** stg のデータは本番相当である
# （Design.md 4.4）。接続文字列は *_FILE で渡し、argv にも環境変数の値にも残さない。
stg-admin-create:
	@cd $(CURDIR)/server && PB_DATABASE_URL_FILE="$(STG_APP_DATABASE_URL_FILE)" \
		go run ./cmd/pb admin create

# ── client とビルド（Design.md 3.4 / 4.2）──────────────────────

## Vite 開発サーバを起動する（:5173。/api と /mcp を :8080 へプロキシ）
# HMR を効かせながら画面を作るときはこちらを使う。API は make run で別に立てる。
dev-client:
	cd client && npm run dev

## docs/design/openapi.yaml から client の型を生成する（Design.md 3.3）
# 生成するのは型だけで、呼び出しは client/src/api/client.ts が持つ。
# openapi.yaml を更新したら実行し、生成物（schema.d.ts）もコミットする。
gen-api:
	cd client && npm run gen:api

## client をビルドする（client/dist を生成）
build-client:
	cd client && npm ci && npm run build

## embed 対象へコピーする（//go:embed は親ディレクトリを辿れない。Design.md 3.4）
# **placeholder.html だけは消さない。** 追跡対象であり、消すと作業ツリーが汚れる。
# rm -rf ではなく find にしているのはそのため。古い成果物は残さず一掃する
# （ハッシュ付きのファイル名は毎ビルド変わるので、残すと binary に溜まり続ける）。
sync-webui: build-client
	mkdir -p server/internal/webui/dist
	find server/internal/webui/dist -mindepth 1 ! -name placeholder.html -delete
	cp -R client/dist/. server/internal/webui/dist/

## client を埋め込んだ単一バイナリを作る（bin/pb）
build: sync-webui
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o ../bin/pb ./cmd/pb

## embed 対象をコミット済みのプレースホルダだけに戻す
# **コミット前の必須手順ではない**（make build は追跡対象を上書きしないため、
# 作業ツリーは汚れない）。ビルド成果物でディスクを塞ぎたくないときに使う。
# git clean は追跡済みの placeholder.html を消さない。
clean-webui:
	git -C $(CURDIR) clean -fdxq server/internal/webui/dist

# ── リリース用の一式（Design.md 4.5）──────────────────────────

# cmdline は、変数がコマンドラインで渡されたときだけその値を返す。
cmdline = $(if $(filter command line,$(origin $(1))),$($(1)))

## リリース用の一式を出力する（使い方は deploy/prod/MANUAL.md の2章）
#   make release TARGET=native OS=darwin ARCH=arm64 [OUT=/path/to/dir]
#   make release TARGET=compose ARCH=amd64 [OUT=/path/to/dir] [PUSH=registry.example.com/pb:2.37.0]
#   make release TARGET=k8s ARCH=arm64 [OUT=/path/to/dir] [PUSH=registry.example.com/pb:2.38.0]
# **コマンドラインで渡された値だけを使う。** OUT は stg-build と共有の変数で、既定値が
# deploy/stg/out である——渡さずに叩いて stg の一式を上書きしないため。OS や ARCH は
# 環境変数として定義されている端末があり、それを黙って拾わないため。
# 値の検証と別名の読み替えは build-release.sh が行う。
release:
	@bash $(CURDIR)/deploy/prod/build-release.sh \
		--target "$(call cmdline,TARGET)" --os "$(call cmdline,OS)" --arch "$(call cmdline,ARCH)" \
		$(if $(call cmdline,OUT),--out "$(OUT)") \
		$(if $(call cmdline,PUSH),--push "$(PUSH)")

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

# ── 文書の分量（CLAUDE.md「文書の分量」）────────────────────────

# 毎セッション必ず読む文書。合計にだけ予算を置く（個別の閾値は持たない）。
SESSION_DOCS := CLAUDE.md LEARNINGS.md .claude/commands/pb-step.md
DOCS_BUDGET  := 81920

## 毎セッション読む文書の合計サイズを測る（予算 80KB。超過で非ゼロ終了）
docs-size:
	@total=0; \
	for f in $(SESSION_DOCS); do \
		if [ -f "$(CURDIR)/$$f" ]; then \
			n=$$(wc -c < "$(CURDIR)/$$f" | tr -d ' '); \
		else n=0; fi; \
		total=$$(( total + n )); \
		printf '%8d  %s\n' "$$n" "$$f"; \
	done; \
	printf '%8d  合計（予算 %d = %dKB）\n' "$$total" "$(DOCS_BUDGET)" "$$(( $(DOCS_BUDGET) / 1024 ))"; \
	echo "-- 最長行 Top5 --"; \
	for f in $(SESSION_DOCS); do \
		[ -f "$(CURDIR)/$$f" ] && LC_ALL=C awk -v n="$$f" '{ if (length($$0) > m) m = length($$0) } END { printf "%8d  %s\n", m, n }' "$(CURDIR)/$$f"; \
	done | sort -rn | head -5; \
	if [ "$$total" -gt "$(DOCS_BUDGET)" ]; then \
		echo "NG: 予算を $$(( total - $(DOCS_BUDGET) )) バイト超過している"; \
		echo "    掃除する（pb-step.md 手順7）。閾値の引き上げは利用者だけが判断する"; \
		exit 1; \
	fi; \
	echo "OK: 予算内（残り $$(( $(DOCS_BUDGET) - total )) バイト）"

# ── 閉じない強調記号（Testing.md 7.7）────────────────────────

# 見る文書。差し替えれば任意のファイルを見られる（make docs-emphasis EMPHASIS_DOCS=a.md）
EMPHASIS_DOCS ?= docs/*.md docs/design/*.md docs/history/*.md

## 描画しても ** が残る段落を数える（1件でもあれば非ゼロ終了。client の markdown-it を使う）
docs-emphasis:
	@if [ ! -d "$(CURDIR)/client/node_modules/markdown-it" ]; then \
		echo "NG: client/node_modules に markdown-it が無い。先に cd client && npm ci を打つ"; \
		exit 1; \
	fi
	@node client/scripts/check-emphasis.mjs $(EMPHASIS_DOCS)

# ── 定義されていないデザイントークン（GuiDesign.md 8.5）──────────

## client/src が使う --pb-* がすべて定義されているかを見る（1件でもあれば非ゼロ終了）
css-tokens:
	@node client/scripts/check-tokens.mjs client/src
