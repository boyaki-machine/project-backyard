-- +goose Up
CREATE TABLE actor_avatar (
  actor_id     char(26) COLLATE "C" PRIMARY KEY REFERENCES actor(id) ON DELETE CASCADE,
  content_type text NOT NULL CHECK (content_type = 'image/png'),
  image_data   bytea NOT NULL,
  updated_at   timestamptz NOT NULL DEFAULT now()
);
