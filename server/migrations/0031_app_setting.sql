-- 正本: DbDesign.md 6.14（app_setting）、Design.md 10.3（設定の3層）、ApiDesign.md 11章。pb-2。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **Design.md 10.3 の第2層の置き場である。** 設定は3層に分かれ、層を決めるのは
-- 「その値がいつ必要か」と「複数のレプリカが一致していなければならないか」の2点。
-- 第1層（接続文字列・待受）はここに置けない——**接続文字列は DB の中にあり得ない**。
--
-- **第2層を DB に置いた理由は3つある。** ①レプリカ N 台が同じ行を読むので画面からの
-- 変更が全台に効く ②変更を audit_log に同一トランザクションで残せる（6.8）
-- ③**設定を1件足すのがスキーマ変更でなくなる**。
--
-- **サーバが自分の設定ファイルを書き換える案は採らなかった。** 決め手はスケールアウトで、
-- レプリカ N 台で設定ファイルが N 個に分岐し、画面からの変更が1台にしか効かない。
-- **再検討の条件は「PB を単一ノードのみで配布すると決めたとき」**（Design.md 10.3）。
--
-- ── 権限は足さない ────────────────────────────────────
--
-- **system.settings は 0010 から存在し、administrator が持っている。**
-- コードのどこからも使われておらず、**本チケットが最初の利用者である。**
-- したがってこのマイグレーションは表の追加だけで済む。

-- +goose Up

-- key を主キーにするのは、permission / role と同じ「名前で引くカタログ」だからである。
-- ULID を振らないのは、行を識別するのが key そのもので、別の識別子を持つと
-- 「同じ key の行が2つある」状態を作れてしまうため。
--
-- **value は text の1列で、型の列を置かない。** 型・既定値・検証規則は Go 側の
-- 設定レジストリだけが持つ（DbDesign.md 6.14）。型を列に持つと、'integer' と
-- 書かれた行に 'true' が入ったときにどちらが正しいかを決める根拠が無くなる。
-- 書き込みの経路は PUT /api/v1/admin/settings の1本だけで、そこがレジストリを引く。
--
-- **キーの許可リストを CHECK に書かない。** 書くと設定を1件足すたびにマイグレーションが
-- 要ることになり、DB を選んだ理由の3つめが消える。**書式だけを見る。**
-- レジストリに無いキーの行は、読む側が警告を1行出して無視する——**古いバイナリへ
-- 戻したときに落ちないため**である（新しい版が書いた行が、知らないキーとして残る）。
--
-- **行が無いことが既定値である。** ここで初期値を INSERT しない。既定値を DB と
-- コードの2か所に持つと、片方だけを直したときに食い違う。
--
-- **updated_by は audit_log と二重に見えるが、読む権限が違う。** 変更の記録は
-- audit_log に残るが、それを読むには auditlog.view が要る。設定画面を開ける人
-- （system.settings）が「いま出ている値を誰がいつ変えたか」を見られるようにする。
-- **値の履歴は持たない**——履歴が要るなら audit_log を見る。
CREATE TABLE app_setting (
  key         text PRIMARY KEY,
  value       text NOT NULL,
  updated_by  char(26) COLLATE "C" REFERENCES actor(id) ON DELETE SET NULL,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CONSTRAINT app_setting_key_format CHECK (key ~ '^[a-z][a-z0-9_]{0,62}$')
);

-- updated_at はトリガで更新する（DbDesign.md 4.3）。set_updated_at は 0001 が作った。
CREATE TRIGGER app_setting_touch BEFORE UPDATE ON app_setting
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- **秘密を本表に置かない。** value は平文であり、pg_dump がそのまま運ぶ。
-- 第3層（TLS の秘密鍵。pb-3）は暗号化した専用の表を使う。
COMMENT ON TABLE app_setting IS
  'アプリケーション設定の第2層（Design.md 10.3）。平文なので秘密を入れない。既定値は行の不在で表す';
