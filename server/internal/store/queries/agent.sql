-- 自分のエージェントに関するクエリ（ApiDesign.md 4.5、DbDesign.md 8.2.1）。
--
-- **すべて owner_actor_id を条件に含める。** 他人のエージェントを 403 ではなく
-- 404 に倒すため（Design.md 6.4.5「存在を隠す」）、「見つからない」1つの結果に
-- 寄せる。me.sql の FindMyAPIToken が actor_id を条件に含めるのと同じ形である。

-- ListMyAgents は GET /me/agents（ApiDesign.md 4.5.1）。
--
-- **有効なトークンを LATERAL で1本だけ引く。** 4.5.3 が「1件につき有効な
-- トークンは1本」と定めるので通常は1行だが、LIMIT 1 を置いて形を保証する。
-- 失効済み（revoked_at IS NOT NULL）は返さない——4.4.1 と同じ扱いで、
-- 失効は本人が消したものである。**期限切れは返す**（更新が要ることに
-- 気づく必要がある）。
--
-- name: ListMyAgents :many
SELECT
  ag.actor_id,
  a.display_name,
  a.is_active,
  ag.client_kind,
  ag.model_name,
  ag.model_version,
  ag.trust_level,
  ag.token_env_suffix,
  ag.created_at,
  p.key  AS project_key,
  p.name AS project_name,
  -- **有効なトークンが無ければ空文字になる。**
  --
  -- access_token.id は NOT NULL なので、素で書くと sqlc が非 NULL（string）と
  -- 推論する。だが LEFT JOIN LATERAL の右辺は「有効なトークンが無ければ1行も
  -- 無い」ため実行時には NULL が来て、pgx が「cannot scan NULL into *string」で
  -- 落ちる。**sqlc に NULL 可と推論させる書き方が無い**——`::text` のキャストも
  -- スカラ副問い合わせも NOT NULL と推論され、ELSE の無い CASE は interface{}、
  -- NULLIF は bool に化ける（いずれも実測）。
  --
  -- そこで**型ではなく値で「無い」を表す**。id は ULID（char(26)）で空文字に
  -- なりえないため、'' は「有効なトークンが無い」を一意に意味する。
  -- **`::text` を先に噛ませるのが要点**——char(26) のまま COALESCE すると
  -- '' が26個の空白に詰められ、「空かどうか」の判定が壊れる。
  (COALESCE(t.id::text, ''))::text AS token_id,
  t.token_prefix AS token_prefix,
  -- issued_at も同じ理由で NULL が来る（pb-224 で pgtype.Timestamptz をやめ、Go の
  -- time.Time が NULL を受けられなくなった）。**token_id が '' のときは読まない**ので、
  -- 番兵に 1970-01-01 を入れる。
  COALESCE(t.issued_at, 'epoch'::timestamptz)::timestamptz AS token_issued_at,
  t.last_used_at AS token_last_used_at,
  t.expires_at   AS token_expires_at
FROM agent ag
JOIN actor a ON a.id = ag.actor_id
LEFT JOIN project p ON p.id = ag.project_id
LEFT JOIN LATERAL (
  SELECT id, token_prefix, issued_at, last_used_at, expires_at
  FROM access_token
  WHERE actor_id = ag.actor_id
    AND token_type = 'agent'
    AND revoked_at IS NULL
  ORDER BY issued_at DESC
  LIMIT 1
) t ON true
WHERE ag.owner_actor_id = @owner_actor_id
ORDER BY ag.created_at DESC;

-- FindMyAgent は1件を引く（PATCH と トークン発行の対象）。
--
-- **actor.is_active も返す。** 4.5.3 が無効化されたエージェントへの発行を
-- 409 で拒むため、呼び出し側が状態を知る必要がある。
--
-- name: FindMyAgent :one
SELECT
  ag.actor_id,
  a.display_name,
  a.is_active,
  ag.client_kind,
  ag.model_name,
  ag.model_version,
  ag.trust_level,
  ag.token_env_suffix,
  ag.created_at,
  ag.project_id,
  p.key  AS project_key,
  p.name AS project_name
FROM agent ag
JOIN actor a ON a.id = ag.actor_id
LEFT JOIN project p ON p.id = ag.project_id
WHERE ag.actor_id = @actor_id
  AND ag.owner_actor_id = @owner_actor_id;

-- CreateAgentActor は actor(kind='agent') を1行作る。
--
-- **user.sql の CreateUserActor と分けている。** あちらは kind='user' を
-- 固定で書いており、種別を引数にすると呼び出し側の誤りが型で止まらなくなる。
--
-- name: CreateAgentActor :exec
INSERT INTO actor (id, kind, display_name)
VALUES (@id, 'agent', @display_name);

-- name: CreateAgent :exec
INSERT INTO agent (
  actor_id, owner_actor_id, project_id, client_kind, model_name, model_version,
  token_env_suffix
) VALUES (
  @actor_id, @owner_actor_id, @project_id, @client_kind, @model_name, @model_version,
  @token_env_suffix
);

-- AgentExistsWithName は 409 already_exists の判定（ApiDesign.md 4.5.2）。
--
-- **同じ所有者の中で（プロジェクト・クライアント種別・表示名）の組を見る。**
-- 3つ組が 8.2.1 の言う同一性であり、表示名まで含めるのは、同じ端末から同じ
-- プロジェクトへ2本目をつなぐ場面（別のモデルを試す等）を塞がないためである。
--
-- **DBの一意制約にしていない。** actor.display_name は agent 表に無く、
-- 制約を張るには表をまたぐ必要がある。ここは画面の使い勝手のための検査であり、
-- 破れても壊れるものが無い（重複した行が1つ増えるだけ）。
--
-- name: AgentExistsWithName :one
SELECT EXISTS (
  SELECT 1
  FROM agent ag
  JOIN actor a ON a.id = ag.actor_id
  WHERE ag.owner_actor_id = @owner_actor_id
    AND ag.project_id     = @project_id
    AND ag.client_kind    = @client_kind
    AND a.display_name    = @display_name
);

-- AgentExistsWithNameExcept は更新時の重複検査に使う（ApiDesign.md 4.5.4）。
--
-- **client_kind と display_name はどちらも変えられる**ので、更新でも4つ組が
-- ぶつかりうる。**自分自身を除く**のが AgentExistsWithName との違いである。
--
-- name: AgentExistsWithNameExcept :one
SELECT EXISTS (
  SELECT 1
  FROM agent ag
  JOIN actor a ON a.id = ag.actor_id
  WHERE ag.owner_actor_id = @owner_actor_id
    AND ag.project_id     = @project_id
    AND ag.client_kind    = @client_kind
    AND a.display_name    = @display_name
    AND ag.actor_id      <> @exclude_actor_id
);

-- ListAgentClientKinds はクライアント種別のカタログを返す（ApiDesign.md 4.5.7）。
--
-- **画面はこれを引いて表示名を出す。** 対応表を画面へ焼き込まない——値域は
-- 今後も増える（DbDesign.md 8.2.1.1）ので、写しを置くと必ず腐る。
--
-- name: ListAgentClientKinds :many
SELECT key, display_name, has_setup_template
FROM agent_client_kind
ORDER BY sort_order, key;

-- AgentClientKindExists は入力の検証に使う（ApiDesign.md 4.5.2 / 4.5.4）。
--
-- **値域を Go の定数で持たない。** 正本は agent_client_kind の行であり、
-- 二重に持つと「検証を通った値が INSERT で落ちて 500」になる。
--
-- name: AgentClientKindExists :one
SELECT EXISTS (SELECT 1 FROM agent_client_kind WHERE key = @key);

-- AgentClientKindsWithTemplate は配置ファイルを出せる種別を返す（ApiDesign.md 5.7.1）。
--
-- **セットアップ画面の検証に使う。** has_setup_template が偽の種別を指定されたら
-- 422 にする——選んだ先に何も出ないためである（DbDesign.md 8.2.1.1）。
--
-- **ListAgentClientKinds で代用しない。** あちらはカタログ全件を返す口で、
-- 5.8.2 の登録モーダル（全種別を出す）が使う。**絞る条件をSQLに書いておくほうが、
-- 呼び出し側で bool を見落とす経路より安全である。**
--
-- name: AgentClientKindsWithTemplate :many
SELECT key, display_name
FROM agent_client_kind
WHERE has_setup_template
ORDER BY sort_order, key;

-- UpdateAgentActor は表示名と有効・無効を更新する（ApiDesign.md 4.5.4）。
--
-- **owner_actor_id を条件に含めるため agent と結合する。** actor だけを
-- 更新すると他人のエージェントを触れてしまう。
--
-- name: UpdateAgentActor :execrows
UPDATE actor a
SET display_name = COALESCE(sqlc.narg('display_name'), a.display_name),
    is_active    = COALESCE(sqlc.narg('is_active'),    a.is_active)
FROM agent ag
WHERE ag.actor_id = a.id
  AND a.id = @actor_id
  AND ag.owner_actor_id = @owner_actor_id;

-- UpdateAgentModel はモデルとクライアント種別を更新する（ApiDesign.md 4.5.4）。
--
-- **client_kind は 0020 から変更できる。** 値域が今後も増えるため、`other` で
-- 登録した人が、PB がその種別に対応した日に移れる必要がある。**project_id は
-- 変えられない**——そのエージェントが行った仕事はプロジェクトに属する。
--
-- **token_env_suffix も変えられる**（手順28a）。端末を替えたときに直せる必要があるのは
-- display_name と同じ理由である。**変えたら接続設定を取り直す**——.mcp.json に古い変数名が
-- 残っていると ~/.zshrc を直しても繋がらない。
--
-- name: UpdateAgentModel :execrows
UPDATE agent
SET model_name       = COALESCE(sqlc.narg('model_name'),       model_name),
    model_version    = COALESCE(sqlc.narg('model_version'),    model_version),
    client_kind      = COALESCE(sqlc.narg('client_kind'),      client_kind),
    token_env_suffix = COALESCE(sqlc.narg('token_env_suffix'), token_env_suffix)
WHERE actor_id = @actor_id
  AND owner_actor_id = @owner_actor_id;

-- AgentEnvSuffixExists は 409 already_exists の判定（ApiDesign.md 4.5.2 / 4.5.4）。
--
-- **一意は（所有者・接尾）である**（DbDesign.md 8.2.1）。環境変数は端末ごとの名前空間
-- なので他人と重なってよいが、**同じ人の中で重なると ~/.zshrc の1行が2つのエージェントに
-- 解釈される。**
--
-- **DBに部分一意インデックスがある**ので、この検査が破れても壊れない。画面に
-- 読める message を返すために先に見ている（AgentExistsWithName と同じ考え方）。
--
-- **@exclude_actor_id には、新規作成のとき空文字を渡す**（ULID は空文字になりえない）。
-- 更新のときだけ自分自身が除かれる。
--
-- name: AgentEnvSuffixExists :one
SELECT EXISTS (
  SELECT 1
  FROM agent
  WHERE owner_actor_id   = @owner_actor_id
    AND token_env_suffix = @token_env_suffix
    AND actor_id        <> @exclude_actor_id
);

-- ListMyProjectKeysForAgent は project_key の検証に使う（ApiDesign.md 4.5.2）。
--
-- **自分がメンバーであるプロジェクトに限る。** エージェントの権限は所有者から
-- 導かれる（Design.md 6.5 の委譲）ので、自分が入っていないプロジェクトの
-- エージェントを作っても権限0件になる。**作れてしまうほうが分かりにくい。**
--
-- アーカイブ済みも返す。アーカイブは status で表す状態であって不可視に
-- するものではない（ApiDesign.md 5.6）。
--
-- name: FindMyProjectByKey :one
SELECT p.id, p.key, p.name
FROM project p
JOIN project_member pm ON pm.project_id = p.id AND pm.actor_id = @actor_id
WHERE p.key = @project_key;

-- ── エージェント用トークン（ApiDesign.md 4.5.3 / 4.5.5） ──────────────

-- FindActiveAgentToken は再発行のときに失効させる相手を引く。
--
-- name: ListActiveAgentTokens :many
SELECT t.id, t.token_prefix
FROM access_token t
JOIN agent ag ON ag.actor_id = t.actor_id
WHERE t.actor_id = @actor_id
  AND ag.owner_actor_id = @owner_actor_id
  AND t.token_type = 'agent'
  AND t.revoked_at IS NULL;

-- RevokeAgentToken は失効させる（4.5.5）。
--
-- **行は消さない**（4.4.3 と同じ）。既に失効済みなら WHERE が外れ、
-- revoked_at を上書きしない（冪等）。
--
-- **owner_actor_id を条件に含める。** 他人のエージェントのトークンを
-- 404 に倒すため。
--
-- name: RevokeAgentToken :execrows
UPDATE access_token t
SET revoked_at = now()
FROM agent ag
WHERE ag.actor_id = t.actor_id
  AND t.id = @id
  AND t.actor_id = @actor_id
  AND ag.owner_actor_id = @owner_actor_id
  AND t.token_type = 'agent'
  AND t.revoked_at IS NULL;

-- RevokeAllAgentTokens は無効化のときに全部切る（4.5.4）。
--
-- **無効化したのに動き続けるのは利用者の期待に反する。** actor.is_active を
-- false にするだけでは認証が止まる（middleware の invalidReason）が、
-- 行としては有効なトークンが残り、4.5.1 の一覧に「有効」と出てしまう。
--
-- name: RevokeAllAgentTokens :execrows
UPDATE access_token
SET revoked_at = now()
WHERE actor_id = @actor_id
  AND token_type = 'agent'
  AND revoked_at IS NULL;

-- ── エージェントの削除（ApiDesign.md 4.5.4。手順26a）─────────────

-- FindDeletedAgentActor は「削除されたエージェント」の付け替え先を引く。
--
-- **kind='agent' である。** 人の付け替え先（kind='system' の
-- 「削除されたユーザー」）と分けているのは、GuiDesign.md 8.4.2 が
-- アバターの**形**で人（円）とエージェント（角丸四角）を区別しているためで、
-- system へ寄せると**過去のコメントが全部円になり、人が書いたように見える**。
--
-- **agent の行を持たない actor である。** トークンを1本も持たないので
-- 認証の経路（Design.md 6.4.5）には現れず、/me/agents にも
-- GET /admin/users?kind=agent にも出ない（どちらも agent と内部結合する）。
--
-- display_name で引くのは FindDeletedUserActor と同じ事情である
-- （actor に key に相当する列が無い。DbDesign.md 6.2）。
--
-- name: FindDeletedAgentActor :one
SELECT id FROM actor
WHERE kind = 'agent' AND display_name = @display_name
  AND NOT EXISTS (SELECT 1 FROM agent ag WHERE ag.actor_id = actor.id)
ORDER BY created_at, id
LIMIT 1;

-- CreateDeletedAgentActor は付け替え先を1件作る。
--
-- **シードで先に置かず、最初に必要になった削除で作る**（CreateSystemActor と
-- 同じ作法。手順13a の判断）。置いても、削除が起きるまで一度も参照されない。
--
-- name: CreateDeletedAgentActor :exec
INSERT INTO actor (id, kind, display_name) VALUES (@id, 'agent', @display_name);

-- CountAgentComments はそのエージェントが書いたコメント数を数える。
--
-- **0 件なら付け替え先を作らない**（reassignCommentsToSystemActor と同じ）。
--
-- name: CountAgentComments :one
SELECT count(*) FROM comment WHERE author_id = @author_id;

-- DeleteMyAgentActor はエージェントの actor 行を物理削除する。
--
-- agent / access_token は ON DELETE CASCADE で追従するので、**そのエージェントの
-- 資格情報は1本残らず消える**（ApiDesign.md 4.5.4）。
--
-- **owner_actor_id を条件に含める。** 他人のエージェントを 403 ではなく 404 に
-- 倒すためで、本ファイルの他のクエリと同じ方針である（Design.md 6.4.5）。
--
-- **kind='agent' に限る。** 万一人のアクター ID を渡されても消さない
-- （DeleteActorByID が kind='user' に限っているのと対である）。
--
-- name: DeleteMyAgentActor :execrows
DELETE FROM actor a
WHERE a.id = @actor_id
  AND a.kind = 'agent'
  AND EXISTS (
    SELECT 1 FROM agent ag
    WHERE ag.actor_id = a.id AND ag.owner_actor_id = @owner_actor_id
  );

-- ListOwnedAgentActorIDs は、その人が所有するエージェントを列挙する。
--
-- **人の削除（ApiDesign.md 6.5）で使う。** agent.owner_actor_id の
-- ON DELETE CASCADE が消すのは agent の行だけで、**エージェントの actor 行・
-- その access_token・そのコメントは残る**（FK の向きは agent.actor_id → actor）。
-- 放置すると、agent 行を失った actor が FindAccessTokenByHash の
-- LEFT JOIN agent から外れ、**認証は通るが実効権限が0件のトークンが残る。**
--
-- name: ListOwnedAgentActorIDs :many
SELECT actor_id FROM agent WHERE owner_actor_id = @owner_actor_id ORDER BY actor_id;
