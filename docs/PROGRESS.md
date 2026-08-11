# 実装進捗

`Design.md` 11章の手順に対応。**ステップを完了したら必ずこの表を更新すること。**

## Phase 1

| # | 内容 | 状態 | 完了日 | 検証方法 |
|---|---|---|---|---|
| 1 | deploy/base/compose.yaml と initdb（DBロール分離） | 未着手 | | `make up` でDBが起動し、`pb_app` ロールが存在する |
| 2 | server/migrations/ 0001〜0010 の作成と適用 | 未着手 | | `make migrate` 後、テーブル23個と権限28件・ロール5件が存在する |
| 3 | `pb admin create` による初期管理者作成 | 未着手 | | 作成した管理者が `app_user` に `system_role='administrator'` で入る |
| 4 | 共通基盤（エラー形式・ページネーション・認証ミドルウェア・監査ログ） | 未着手 | | `go test ./internal/httpapi/...` が通る |
| 5 | `POST /auth/login`・`/auth/logout`・`GET /me` | 未着手 | | `curl -i -X POST .../auth/login` で Set-Cookie が返り、`GET /me` が権限一覧を返す |
| 6 | 認可ミドルウェア（`RequirePermission`） | 未着手 | | オペレータで `/admin/users` を叩くと 403 |
| 7 | `GET/POST /projects`、`check-key` | 未着手 | | プロジェクトを作成し、一覧に件数と進捗が出る |
| 8 | `GET/PATCH /projects/:key`、archive | 未着手 | | `If-Match` 不一致で 409 |
| 9 | `GET/POST /admin/users` | 未着手 | | ユーザーを作成し、初期パスワードが1回だけ返る |
| 10 | `GET/PATCH/DELETE /admin/users/:id`、password-reset、memberships | 未着手 | | 自分自身のロール変更が 409、最後の管理者の降格が 409 |
| 11 | `GET /roles`、`GET /permissions` | 未着手 | | 権限28件・ロール5件が返る |
| 12 | `/me/sessions`、`/me/tokens`、`PATCH /me`、`POST /me/password` | 未着手 | | パスワード変更後に他セッションが失効する |
| 13 | client の雛形と embed 経路の疎通 | 未着手 | | `make build` した単一バイナリで `/` にVueの初期画面が出る |
| 14 | フロント：ログイン画面、プロジェクト一覧、ユーザー管理 | 未着手 | | ブラウザでログイン→一覧表示→ユーザー追加ができる |
| 15 | フロント：ロールによるメニュー・ボタンの出し分け | 未着手 | | オペレータで「管理」セクションが表示されない |
| 16 | チケット API と画面 | 未着手 | | `ApiDesign.md` 9章の確定後に着手 |

**状態の記法**：未着手 / 進行中 / 完了 / 保留

## 設計文書との差異・積み残し

実装中に設計文書との食い違いや、文書に書かれていない判断が生じた場合はここに記録する。
**コードだけ直して済ませない。** 設計文書の修正提案とセットで残す。

| 日付 | 手順 | 内容 | 対応 |
|---|---|---|---|
| | | | |

## 環境メモ

実際に動かして分かったこと（バージョンの相性、ハマった点、回避策）を追記する。
ここに書いた内容は、後から `CLAUDE.md` や設計文書へ昇格させることを検討する。

- （記入例）goose のバージョン: v3.x
- （記入例）sqlc のバージョン: v1.x
