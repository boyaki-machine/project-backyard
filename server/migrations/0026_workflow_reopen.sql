-- 正本: DbDesign.md 7.4（ワークフローテンプレート）。pb-69。
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- **`done → in_progress`（再オープン）を3テンプレートすべてに足す。**
--
-- 0010 の時点で、`done` から出る遷移はどのテンプレートにも無かった。完了は終端で
-- あり、そこから戻る用途を想定していなかったためである。**運用で逆だと分かった**
-- ——完了と判定したチケットを確認したら直っていなかった、誤操作で完了にした、と
-- いう場面が実際に起き、**戻す手段が画面にもAPIにも無い**（利用者の報告、2026-09-07）。
-- ワークフローの遷移は `workflow_transition` の行として定義されるので、行が無い限り
-- 9.6 は検証2 で 409 に倒れ、9.7 は「完了から進行中へは直接進められません」を返す。
--
-- **`required_permission` は `ticket.transition` ではなく `ticket.close` である。**
-- 再オープンは完了判定の取り消しであり、**閉じられる人だけが開け直せる**のが筋である。
-- `ticket.close` は 0010 で project_admin にだけ与えてあり、project_member は持たない。
-- 差し戻し遷移（`review → in_progress`、`approval → in_progress`）と揃えて
-- `ticket.transition` にする案は棄却した——あちらは完了していないものを前段へ戻す
-- 操作で、完了判定そのものは動いていない。
--
-- **ただし、いまの構成では `ticket.close` で誰も締め出されない**（Design.md 付録A の
-- 論点②）。実効権限はシステムロールとプロジェクトロールの和で（Design.md 6.4.1）、
-- operator も administrator も ticket.close を持つ。**クローズ（in_progress → done）
-- にも等しく当てはまる既存の論点であり、ここでは動かさない。**
--
-- **`allowed_actor_kinds` は `["user"]`。** エージェントは自分でチケットを
-- クローズできない（`Requirements.md` 10.8.6、DbDesign.md 7.4）。**その裏返しとして、
-- 開け直すこともできない**——どちらも完了判定を動かす操作である。
--
-- **`closed_at` は API 側が既に戻す。** 遷移先の category が 'done' でなければ NULL へ
-- 戻る（ApiDesign.md 9.6 の表、`SetTicketStatus`）。**足りていなかったのは行だけ**なので、
-- このマイグレーションにコードの変更は伴わない。
--
-- **`done → todo` は足さない。** チケットが求めたのは「完了から進行中に戻せる」まで
-- である。完了から未着手まで一息に戻す場面は挙がっておらず、必要になってから足す。
--
-- **ULID は固定値。** 末尾の TG / TH / TJ は 0010 の T1〜TF に続く採番である
-- （Crockford Base32 は I L O U を除くので F の次は G H J）。再実行しても主キーで
-- 弾かれるので冪等になる。

-- +goose Up

-- ── ① テンプレート（新しく作るプロジェクトに効く）────────────────

INSERT INTO workflow_transition
  (id, workflow_id, from_status_key, to_status_key,
   required_permission, allowed_actor_kinds) VALUES
  -- simple
  ('01JZZZZZZZZZZZZZZZZZZZZZTG','01JZZZZZZZZZZZZZZZZZZZZZW1',
   'done','in_progress','ticket.close','["user"]'::jsonb),
  -- with_review
  ('01JZZZZZZZZZZZZZZZZZZZZZTH','01JZZZZZZZZZZZZZZZZZZZZZW2',
   'done','in_progress','ticket.close','["user"]'::jsonb),
  -- with_approval
  ('01JZZZZZZZZZZZZZZZZZZZZZTJ','01JZZZZZZZZZZZZZZZZZZZZZW3',
   'done','in_progress','ticket.close','["user"]'::jsonb)
ON CONFLICT DO NOTHING;

-- ── ② 既存プロジェクト（0024 と違い、ここは波及させる）──────────
--
-- **プロジェクトのワークフローはテンプレートの複製である**（DbDesign.md 6.5、
-- `project/create.go`）。複製が走るのはプロジェクト作成時だけなので、①だけでは
-- 既に作られたプロジェクト（stg の pb など）に入らない。
--
-- **0024（文書テンプレートの5件目）は「既存プロジェクトには波及しない」と決めたが、
-- ここでは同じ判断をしない。** あちらは足りない1枚をプロジェクト管理者が画面から
-- 書ける。**ワークフローの遷移を画面から足す経路は無い**ので、ここで入れなければ
-- 既存プロジェクトは永久に完了から戻せない。
--
-- **ID はワークフローの ULID の先頭24文字に 'R1' を継いで作る**（利用者の判断、
-- 2026-09-07）。規約「ID はアプリ側で生成し、DB の自動採番を使わない」（DbDesign.md 4.2）
-- からの局所的な逸脱であり、理由は3つある——(a) 決定的なので再実行しても同じ値になり
-- 冪等である、(b) ULID の時刻部分を残すので C ロケールの並びが崩れない、(c) どの
-- workflow の行かが目で追える。残る72ビットの乱数部を共有するため、既存の遷移IDと
-- ぶつかることは実際上ありえない。**次に同じ形で足すときは 'R2' を使う。**
--
-- **`todo` / `in_progress` / `done` が揃うワークフローだけを対象にする。**
-- カテゴリ（category='done'）ではなくキーで引くのは、カテゴリが同じステータスを
-- 複数持つワークフローで行が増えてしまうためである。現存するワークフローは3つの
-- テンプレートの複製しかなく、キーはいずれも 'done' / 'in_progress' である。

INSERT INTO workflow_transition
  (id, workflow_id, from_status_key, to_status_key,
   required_permission, allowed_actor_kinds)
SELECT substr(w.id, 1, 24) || 'R1',
       w.id, 'done', 'in_progress', 'ticket.close', '["user"]'::jsonb
  FROM workflow w
 WHERE NOT w.is_template
   AND EXISTS (SELECT 1 FROM workflow_status s
                WHERE s.workflow_id = w.id AND s.key = 'done')
   AND EXISTS (SELECT 1 FROM workflow_status s
                WHERE s.workflow_id = w.id AND s.key = 'in_progress')
   AND NOT EXISTS (SELECT 1 FROM workflow_transition t
                    WHERE t.workflow_id = w.id
                      AND t.from_status_key = 'done'
                      AND t.to_status_key = 'in_progress')
ON CONFLICT DO NOTHING;
