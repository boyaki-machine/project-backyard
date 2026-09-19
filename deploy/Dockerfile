# PB のコンテナイメージ（Design.md 4.5。pb-123）。
#
#   make release TARGET=docker|compose ARCH=<amd64|arm64>      ← 通常はこちらから作る
#   docker buildx build --platform linux/arm64 -f deploy/Dockerfile .   ← リポジトリ直下で
#
# **ビルドの段は、ビルドする端末の CPU（$BUILDPLATFORM）で動かし、Go のクロスコンパイルで
# 行き先の CPU（$TARGETARCH）向けのバイナリを作る。** 実行の段は COPY だけなので、行き先の
# CPU のエミュレーションが無い端末でも、amd64 と arm64 の両方を作れる。
#
# イメージの中身：/pb（client を embed した単一バイナリ）、/goose（postgres のドライバだけ）、
# /migrations。利用者は uid 65532（nonroot）。秘密ファイルの置き方は MANUAL.md の4章。
#
# **コンテナの healthcheck は持たない**（利用者の判断、2026-09-15）。distroless には
# /healthcheck を叩くコマンドが無く、叩き役も足さない。健全かは外から /healthcheck を見る。

# ── 1. client ─────────────────────────────────────────────
FROM --platform=$BUILDPLATFORM node:24.14.0-bookworm-slim AS client
WORKDIR /src/client
# 依存の取得を先に1層にする。package-lock.json が変わらなければ再利用される。
COPY client/package.json client/package-lock.json ./
RUN npm ci
COPY client/ ./
# client/src/version.ts がリポジトリ直下の VERSION を ../../VERSION で読む。
COPY VERSION /src/VERSION
RUN npm run build

# ── 2. pb と goose ────────────────────────────────────────
FROM --platform=$BUILDPLATFORM golang:1.26.6-bookworm AS server
ARG TARGETOS
ARG TARGETARCH
# goose のドライバを postgres だけに絞るタグ。**正本は deploy/prod/build-release.sh** で、
# make release は --build-arg で渡す。ここの既定値は直に docker build したときのためのもの。
ARG GOOSE_TAGS="no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb"
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/tools/go.mod server/tools/go.sum ./tools/
RUN cd tools && go mod download
COPY server/ ./
COPY VERSION /src/VERSION
# //go:embed は自パッケージの下しか見ないので、client の成果物を embed 対象へ置く（Design.md 3.4）。
COPY --from=client /src/client/dist/ ./internal/webui/dist/
# ldflags は Makefile の LDFLAGS と揃える。版は VERSION ファイルだけから取る。
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
		-ldflags "-s -w -X main.version=$(cat /src/VERSION)" -o /out/pb ./cmd/pb \
	&& cd tools \
	&& CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -tags "$GOOSE_TAGS" \
		-ldflags "-s -w" -o /out/goose github.com/pressly/goose/v3/cmd/goose

# ── 3. 実行 ───────────────────────────────────────────────
FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=unknown
LABEL org.opencontainers.image.title="Project Backyard" \
	org.opencontainers.image.version="$VERSION"
COPY --from=server /out/pb /pb
COPY --from=server /out/goose /goose
COPY server/migrations/ /migrations/
# **コンテナの中ではすべてのアドレスで待ち受け、外への公開範囲は docker の -p や compose の
# ports で決める**（DbDesign.md 3.2）。イメージで決めておくのは、PB の既定値が変わっても
# コンテナの外から届かなくならないため（pb-125）。この値は画面では「環境変数で固定」と出る。
ENV PB_BIND=0.0.0.0:8080
EXPOSE 8080
# 数値で書く。K8s の runAsNonRoot は名前では利用者を確かめられない。
USER 65532:65532
ENTRYPOINT ["/pb"]
CMD ["serve"]
