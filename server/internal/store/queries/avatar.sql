-- name: GetActorAvatar :one
SELECT content_type, image_data, updated_at FROM actor_avatar WHERE actor_id = @actor_id;

-- name: PutActorAvatar :exec
INSERT INTO actor_avatar (actor_id, content_type, image_data)
VALUES (@actor_id, @content_type, @image_data)
ON CONFLICT (actor_id) DO UPDATE SET
  content_type = EXCLUDED.content_type,
  image_data = EXCLUDED.image_data,
  updated_at = now();

-- name: DeleteActorAvatar :exec
DELETE FROM actor_avatar WHERE actor_id = @actor_id;

-- name: SetActorAvatarURL :exec
UPDATE actor SET avatar_url = @avatar_url WHERE id = @actor_id AND kind = 'user';
