package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// touchTimeout は last_used_at の更新に許す時間。
// 認証の成否には関係しない付随処理なので、待たせない。
const touchTimeout = 3 * time.Second

// Authenticate は Cookie または Bearer トークンを検証し、
// auth.Principal をコンテキストへ載せる（Design.md 6.2.2）。
//
//	Cookie(pb_session) または Authorization: Bearer <token>
//	  → SHA-256 でハッシュ化
//	  → access_token を token_hash（UNIQUE索引）で検索
//	  → revoked_at IS NULL かつ有効期限内であることを確認
//	  → last_used_at を更新（1分粒度で間引く）
//	  → actor をロードしてリクエストコンテキストに載せる
//
// **失敗はすべて 401 unauthenticated に統一する。** 「トークンが無い」
// 「失効している」「アカウントが無効」を応答で区別すると、有効なトークンを
// 総当たりする側に手がかりを与えるため。区別が必要な運用者向けの情報は
// WithCause でサーバログにのみ出す（ApiDesign.md 2.5、Design.md 10.1）。
//
// 認可（権限の検証）は行わない。RequirePermission は手順6で追加する。
func Authenticate(q gen.Querier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			plaintext, source := credential(r)
			if plaintext == "" {
				apierr.Write(w, r, apierr.New(apierr.Unauthenticated).
					WithCause(errors.New("Cookie も Authorization ヘッダも無い")))
				return
			}

			row, err := q.FindAccessTokenByHash(r.Context(), auth.HashToken(plaintext))
			if err != nil {
				if errors.Is(err, pgx.ErrNoRows) {
					apierr.Write(w, r, apierr.New(apierr.Unauthenticated).
						WithCause(errors.New("該当するトークンが無い")))
					return
				}
				// DB 障害を 401 にすると、利用者が再ログインを試み続けることになる。
				apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
				return
			}

			if reason := invalidReason(row); reason != "" {
				apierr.Write(w, r, apierr.New(apierr.Unauthenticated).
					WithCause(errors.New(reason)))
				return
			}

			scopes, err := auth.DecodeScopes(row.Scopes)
			if err != nil {
				// 空に倒すとスコープの絞り込みが消えるため、通さない。
				apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
				return
			}

			touchLastUsed(r.Context(), q, row.TokenID)

			// expires_at は NULL 許容。NULL のまま nil を載せ、
			// GET /me の expires_at も null になる（ApiDesign.md 4.5）。
			var expiresAt *time.Time
			if row.ExpiresAt.Valid {
				t := row.ExpiresAt.Time
				expiresAt = &t
			}

			cached, cachedAt := permissionCache(r.Context(), row)

			p := &auth.Principal{
				ActorID:             row.ActorID,
				ActorKind:           row.ActorKind,
				DisplayName:         row.DisplayName,
				Email:               row.Email.String,
				SystemRole:          row.SystemRole.String,
				TokenID:             row.TokenID,
				TokenType:           row.TokenType,
				Scopes:              scopes,
				ProjectID:           row.ProjectID.String,
				ExpiresAt:           expiresAt,
				Source:              source,
				CachedPermissions:   cached,
				PermissionsCachedAt: cachedAt,
			}
			next.ServeHTTP(w, r.WithContext(auth.NewPrincipalContext(r.Context(), p)))
		})
	}
}

// credential は資格情報を取り出し、その送出経路を返す。
//
// **Cookie を優先する。** ApiDesign.md 2.4 の CSRF は「Cookie 認証のときだけ
// 要求する」という規約であり、Cookie が付いているのに Bearer 側を採って
// CSRF の対象外にしてしまうと、Cookie を自動送信するブラウザからの
// リクエストが検証を素通りしうる。安全側に倒す。
func credential(r *http.Request) (string, auth.CredentialSource) {
	if c, err := r.Cookie(auth.SessionCookieName); err == nil && c.Value != "" {
		return c.Value, auth.SourceCookie
	}
	if t := auth.BearerToken(r.Header.Get("Authorization")); t != "" {
		return t, auth.SourceBearer
	}
	return "", ""
}

// invalidReason はトークンが使えない理由を返す。使えるなら空文字。
//
// 応答には出さない。サーバログへ出す文言である。
func invalidReason(row gen.FindAccessTokenByHashRow) string {
	if row.RevokedAt.Valid {
		return "トークンが失効している（revoked_at）"
	}
	// expires_at は NULL 許容。NULL は無期限として扱う。
	// セッションには手順5で必ず期限を設定する（ApiDesign.md 3.1 の Max-Age=1209600）。
	if row.ExpiresAt.Valid && !row.ExpiresAt.Time.After(time.Now()) {
		return "トークンの有効期限が切れている（expires_at）"
	}
	if !row.IsActive {
		// ApiDesign.md 3.1 が「アカウント無効も認証失敗と区別しない」と
		// 定めているため、ここも 401 に倒す（403 にしない）。
		return "アクターが無効化されている（actor.is_active=false）"
	}
	return ""
}

// permissionCache は実効権限のセッションキャッシュを取り出す（Design.md 6.4.5）。
//
// 両方が揃っているときだけキャッシュとして扱う。片方だけが入っている行は
// 想定していないが（書き込みも無効化も2列を同時に扱う）、そうなっていたら
// キャッシュ不在として計算し直す。
//
// **解釈できなくても認証は通す。** キャッシュを読めなかった場合はロールから
// 計算し直すだけで、得られる権限は正本と同じものになる。壊れた scopes を
// 500 にする（安全側に倒れないため）のとは事情が違う。
func permissionCache(ctx context.Context, row gen.FindAccessTokenByHashRow) ([]string, *time.Time) {
	if !row.PermissionsCachedAt.Valid || len(row.CachedPermissions) == 0 {
		return nil, nil
	}
	permissions, err := auth.DecodeCachedPermissions(row.CachedPermissions)
	if err != nil {
		slog.WarnContext(ctx, "実効権限のキャッシュを解釈できなかった。計算し直す",
			slog.String("request_id", apierr.RequestIDFromContext(ctx)),
			slog.String("token_id", row.TokenID),
			slog.String("cause", err.Error()),
		)
		return nil, nil
	}
	cachedAt := row.PermissionsCachedAt.Time
	return permissions, &cachedAt
}

// touchLastUsed は last_used_at を更新する。失敗しても認証は通す。
//
// 「最終利用日時が1回書けなかった」ためにリクエストを落とす価値はない。
// リクエストのキャンセルを引き継がないのは、応答を返した後の後片付けとして
// 走らせるためである。
func touchLastUsed(ctx context.Context, q gen.Querier, tokenID string) {
	touchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), touchTimeout)
	defer cancel()

	if err := q.TouchAccessTokenLastUsed(touchCtx, tokenID); err != nil {
		slog.WarnContext(ctx, "last_used_at を更新できなかった",
			slog.String("request_id", apierr.RequestIDFromContext(ctx)),
			slog.String("token_id", tokenID),
			slog.String("cause", err.Error()),
		)
	}
}
