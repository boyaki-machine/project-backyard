-- アプリケーション設定に関するクエリ（DbDesign.md 6.14、ApiDesign.md 11章）。pb-2。
--
-- **Design.md 10.3 の第2層の置き場である。** 第1層（接続文字列・待受）は
-- 環境変数と設定ファイルにしか置けないので、ここには現れない。
--
-- **行が無いことが既定値である。** 既定値の正本は Go 側の設定レジストリ
-- （internal/config/registry.go）で、ここには「画面から変えられたもの」だけが入る。
--
-- **プロジェクトで閉じていない唯一のクエリ群である。** 設定はサーバ全体のもので、
-- 必要権限は system.settings（アドミニストレータ）。

-- 全件を返す。**キーで絞らない**——設定は6件程度で、レジストリ側が
-- 知らないキーを読み飛ばすため（config.OverlayDatabase）。
-- 起動時に1回、設定の保存ごとに1回しか呼ばれない。
-- name: ListAppSettings :many
SELECT
  s.key,
  s.value,
  s.updated_at,
  s.updated_by,
  a.kind AS updated_by_kind,
  a.display_name AS updated_by_display_name
FROM app_setting s
LEFT JOIN actor a ON a.id = s.updated_by
ORDER BY s.key;

-- 1件を書く。**PUT は追加と変更を兼ねる（冪等）**ので upsert にする
-- （ApiDesign.md 11.2）。同じ本文を2回送っても結果は変わらない。
--
-- updated_at はトリガが動かすので書かない（DbDesign.md 4.3）。
-- **ただし INSERT では DEFAULT now() が入り、UPDATE ではトリガが入れる**ので、
-- どちらの経路でも埋まる。
-- name: UpsertAppSetting :exec
INSERT INTO app_setting (key, value, updated_by)
VALUES (@key, @value, @updated_by)
ON CONFLICT (key) DO UPDATE
  SET value      = EXCLUDED.value,
      updated_by = EXCLUDED.updated_by;

-- 1件を消す。**「既定に戻す」がこれである**（ApiDesign.md 11.2 の value: null）。
-- 設定を消すことと既定へ戻すことは同じ状態なので、別の口を作らない。
--
-- **行が無くても誤りにしない。** 冪等であり、何度呼んでも「既定」に収束する。
-- name: DeleteAppSetting :exec
DELETE FROM app_setting WHERE key = @key;
