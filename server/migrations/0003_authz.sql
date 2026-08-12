-- 正本: DbDesign.md 6.3（認可）
-- 前進のみ。down は書かない（DbDesign.md 5.3）。
--
-- project_member.project_id の FK は project 作成後（0004 末尾）に付与する。
-- PostgreSQL は前方参照を許さないため（DbDesign.md 5.2）。

-- +goose Up

CREATE TABLE permission (
  key         text PRIMARY KEY,
  category    text NOT NULL,
  description text NOT NULL,
  sort_order  integer NOT NULL DEFAULT 0
);

CREATE TABLE role (
  key          text PRIMARY KEY,
  scope        text NOT NULL CHECK (scope IN ('system','project')),
  display_name text NOT NULL,
  description  text,
  is_builtin   boolean NOT NULL DEFAULT true,
  sort_order   integer NOT NULL DEFAULT 0
);

CREATE TABLE role_permission (
  role_key       text NOT NULL REFERENCES role(key)       ON DELETE CASCADE,
  permission_key text NOT NULL REFERENCES permission(key) ON DELETE CASCADE,
  PRIMARY KEY (role_key, permission_key)
);

CREATE TABLE project_member (
  project_id char(26) COLLATE "C" NOT NULL,
  actor_id   char(26) COLLATE "C" NOT NULL REFERENCES actor(id) ON DELETE CASCADE,
  role_key   text        NOT NULL REFERENCES role(key) ON DELETE RESTRICT,
  joined_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (project_id, actor_id)
);
CREATE INDEX idx_project_member_actor ON project_member (actor_id);
