// 自分自身に関するAPI（ApiDesign.md 4章）のうち、アクセストークンの管理。
//
//	GET    /api/v1/me/tokens       4.4.1
//	POST   /api/v1/me/tokens       4.4.2
//	DELETE /api/v1/me/tokens/{id}  4.4.3
//
// **扱うのは token_type='api' の行だけである。** ブラウザのセッション
// （'session'）は現れない——本人が自分のセッションを見る・切る画面を持たないと
// 決めており（GuiDesign.md 5.8）、混ぜると「一覧に出ているのに失効させられない
// 行」が生まれる。エージェント用（'agent'、Phase 2）はプロジェクト設定側から
// 発行する（Design.md 6.5）。
//
// **6.7（管理者による全失効）と役割が違う。** あちらは他人の端末を丸ごと切る
// もので、こちらは本人が自分の1本を選んで切る。共有するのは access_token という
// 置き場だけである。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// トークンの制約（ApiDesign.md 4.4.2）。
const (
	// tokenNameMaxLen は name の上限。project.name の CHECK（1〜100）に揃える。
	// access_token.name には DB の CHECK が無いため、アプリ側が持つ。
	tokenNameMaxLen = 100

	// tokenMinExpiresInDays / tokenMaxExpiresInDays は expires_in_days の値域。
	//
	// **無期限を許さない。** Design.md 6.5 はエージェントトークンについて
	// 「有効期限必須」と定めており、CLI トークンだけ例外にする理由が無い。
	// expires_at が NULL のトークンは失効操作でしか消えず、置き忘れを検出できない。
	tokenMinExpiresInDays = 1
	tokenMaxExpiresInDays = 365

	// maxAPITokensPerActor は1人あたりの発行本数の上限（利用者の判断、2026-08-22）。
	//
	// 大量に発行するユースケースが無く、増えるほど「どこからアクセスしているのか」を
	// 本人が把握できなくなる。**数えるのは失効していないもので、期限切れを含む**
	// ——GET /me/tokens が返す行と一致させないと、一覧に出ている行数と上限が
	// 食い違い、どれを失効させれば発行できるのかが画面から読めなくなる。
	maxAPITokensPerActor = 5
)

// トークンの状態（ApiDesign.md 4.4.1 の status）。
const (
	tokenStatusActive  = "active"
	tokenStatusExpired = "expired"
)

// accessTokenView は 4.4.1 が返す1行。**平文を持たない。**
type accessTokenView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	IssuedAt    Time     `json:"issued_at"`
	LastUsedAt  *Time    `json:"last_used_at"`
	ExpiresAt   *Time    `json:"expires_at"`
	Status      string   `json:"status"`
}

// accessTokenListView は 4.4.1 の応答。
//
// **ページネーションも ETag も持たない**（7.1 / 7.2 と同じ）。1人5本が上限で、
// 絞り込みも差分取得も意味を持たないためである。
type accessTokenListView struct {
	Items []accessTokenView `json:"items"`
}

// issuedAccessTokenView は 4.4.2 の応答。**token を持つ唯一の形**である。
type issuedAccessTokenView struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Token       string   `json:"token"`
	TokenPrefix string   `json:"token_prefix"`
	Scopes      []string `json:"scopes"`
	IssuedAt    Time     `json:"issued_at"`
	ExpiresAt   *Time    `json:"expires_at"`
	Status      string   `json:"status"`
}

// ── GET /api/v1/me/tokens（4.4.1）───────────────────────────

// listMyTokens は自分のアクセストークンを一覧する。
//
// **失効済みは返さない。期限切れは返す。** 失効は本人が消したものであり、
// 残すと増え続けて読めなくなる（記録は監査ログの token.revoke にある）。
// 期限切れは「更新しないと使えない」と本人が気づく必要があり、かつ上限5本を
// 占めている。
func (h *handler) listMyTokens(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("GET /me/tokens が認証ミドルウェアを通っていない")))
		return
	}

	rows, err := h.q.ListMyAPITokens(r.Context(), p.ActorID)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("アクセストークンを読めない: %w", err)))
		return
	}

	// **now を1回だけ取る。** 行ごとに time.Now() を呼ぶと、境界にある
	// トークンが同じ応答の中で有効・期限切れに割れうる。
	now := time.Now()
	items := make([]accessTokenView, 0, len(rows))
	for _, row := range rows {
		scopes, err := auth.DecodeScopes(row.Scopes)
		if err != nil {
			// **落とさずに空で出す。** ここは表示のための一覧であり、
			// 解釈できないスコープを理由に画面ごと止めると、その行を
			// 失効させることもできなくなる。認可の判定には使わない値である。
			scopes = []string{}
		}
		items = append(items, accessTokenView{
			ID:          row.ID,
			Name:        row.Name.String,
			TokenPrefix: row.TokenPrefix.String,
			Scopes:      scopes,
			IssuedAt:    Time(row.IssuedAt.Time),
			LastUsedAt:  apiTimestamptz(row.LastUsedAt),
			ExpiresAt:   apiTimestamptz(row.ExpiresAt),
			Status:      tokenStatus(row.ExpiresAt, now),
		})
	}

	WriteJSON(w, http.StatusOK, accessTokenListView{Items: items})
}

// tokenStatus は expires_at から 4.4.1 の status を決める。
//
// expires_at が NULL（無期限）は active とする。4.4.2 は無期限の発行を許さないが、
// 列としては NULL を許すため（DbDesign.md 6.2）、読む側は倒れないようにしておく。
func tokenStatus(expiresAt pgtype.Timestamptz, now time.Time) string {
	if expiresAt.Valid && !expiresAt.Time.After(now) {
		return tokenStatusExpired
	}
	return tokenStatusActive
}

// ── POST /api/v1/me/tokens（4.4.2）──────────────────────────

// createTokenRequest は 4.4.2 のリクエスト本体。
//
// **ExpiresInDays をポインタで受ける。** 必須項目だが、値型にすると 0 が
// 「送られなかった」と「0日と書かれた」のどちらか分からない。前者は required、
// 後者は値域違反で、画面に出す文言が違う。
type createTokenRequest struct {
	Name          string   `json:"name"`
	ExpiresInDays *int     `json:"expires_in_days"`
	Scopes        []string `json:"scopes"`
}

// createTokenFields は検証を通ったあとの確定値。
type createTokenFields struct {
	name          string
	expiresInDays int
	scopes        []string
}

// createMyToken は自分のアクセストークンを発行する（ApiDesign.md 4.4.2）。
//
// **平文はこの応答でのみ返る。** DB に載るのは SHA-256 のハッシュと
// 先頭8文字だけで（DbDesign.md 6.2）、再表示する経路を作らない。
func (h *handler) createMyToken(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("POST /me/tokens が認証ミドルウェアを通っていない")))
		return
	}

	var req createTokenRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	fields, e := validateCreateToken(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()

	// **スコープの語彙は権限カタログのキーである**（Design.md 6.4.1）。
	// カタログ照合はトランザクションの外で済ませる——読み取りだけで、
	// 発行の可否に他の書き込みが絡まないためである。
	if len(fields.scopes) > 0 {
		if e := h.validateTokenScopes(ctx, fields.scopes); e != nil {
			apierr.Write(w, r, e)
			return
		}
	}

	plaintext, err := auth.NewToken(auth.APITokenPrefix)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}
	encodedScopes, err := auth.EncodeScopes(fields.scopes)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	tokenID := ulidgen.New()
	issuedAt := time.Now()
	expiresAt := issuedAt.AddDate(0, 0, fields.expiresInDays)
	rec := audit.FromRequest(r)

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **数えるのと入れるのを同じトランザクションで行う**（4.4.2）。
		// 分けると、同時に2本 POST されたときに上限を超える。
		n, err := q.CountMyAPITokens(ctx, p.ActorID)
		if err != nil {
			return fmt.Errorf("アクセストークンの本数を数えられない: %w", err)
		}
		if n >= maxAPITokensPerActor {
			return apierr.New(apierr.Conflict).
				WithMessage(fmt.Sprintf(
					"アクセストークンは%d本までです。新しく発行するには、いずれかを失効させてください",
					maxAPITokensPerActor))
		}

		if err := q.CreateAccessToken(ctx, gen.CreateAccessTokenParams{
			ID:          tokenID,
			ActorID:     p.ActorID,
			TokenType:   auth.TokenTypeAPI,
			TokenHash:   auth.HashToken(plaintext),
			TokenPrefix: text(auth.TokenPrefix(plaintext)),
			Name:        text(fields.name),
			// project_id は NULL（全プロジェクト）。プロジェクト単位のトークンは
			// Phase 2 のエージェント用である（Design.md 6.5）。
			Scopes:    encodedScopes,
			ExpiresAt: pgtype.Timestamptz{Time: expiresAt, Valid: true},
			// client_info はセッション（User-Agent）用の列。CLI トークンには
			// 相当するものが無く、本人が付ける name が識別子になる。
		}); err != nil {
			return fmt.Errorf("アクセストークンを発行できない: %w", err)
		}

		// **平文は detail に入れない。** audit_log は長期保存される記録である。
		// **token_id は差し替えない**——操作に使ったのはブラウザのセッションであり、
		// 発行された側は target_id に入る（login.success が WithToken するのとは
		// 意味が違う。あちらは「そのトークンでログインした」記録である）。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.TokenIssue,
			Result:     audit.Success,
			TargetType: "access_token",
			TargetID:   tokenID,
			Detail: map[string]any{
				"name":       fields.name,
				"scopes":     fields.scopes,
				"expires_at": expiresAt.UTC().Format(time.RFC3339),
			},
		})
	})
	if err != nil {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			apierr.Write(w, r, apiErr)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	WriteJSON(w, http.StatusCreated, issuedAccessTokenView{
		ID:          tokenID,
		Name:        fields.name,
		Token:       plaintext,
		TokenPrefix: auth.TokenPrefix(plaintext),
		Scopes:      fields.scopes,
		IssuedAt:    Time(issuedAt),
		ExpiresAt:   apiTime(&expiresAt),
		Status:      tokenStatusActive,
	})
}

// validateTokenScopes はスコープが権限カタログのキーであることを確かめる
// （ApiDesign.md 4.4.2）。
//
// **カタログに無い値を通さない。** スコープは「権限の上限」であり縮小しか
// できない（Design.md 6.4.1）。解釈できない語彙を保存すると、絞ったつもりの
// トークンが権限0件になり、その理由が誰にも分からなくなる。
func (h *handler) validateTokenScopes(ctx context.Context, scopes []string) *apierr.Error {
	catalog, err := h.q.ListPermissions(ctx)
	if err != nil {
		return apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("権限カタログを読めない: %w", err))
	}
	known := make(map[string]struct{}, len(catalog))
	for _, p := range catalog {
		known[p.Key] = struct{}{}
	}

	var unknown []string
	for _, s := range scopes {
		if _, ok := known[s]; !ok {
			unknown = append(unknown, s)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	return apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
		Field: "scopes", Code: "invalid",
		Message: fmt.Sprintf("権限キーとして認識できない値があります：%s",
			strings.Join(unknown, ", ")),
	})
}

// validateCreateToken は 4.4.2 の入力を検証する。
//
// **すべての項目を見てから返す**（2.5 の details は項目ごとに紐づける）。
func validateCreateToken(req createTokenRequest) (createTokenFields, *apierr.Error) {
	var details []apierr.Detail
	var f createTokenFields

	name := strings.TrimSpace(req.Name)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		details = append(details, apierr.Detail{
			Field: "name", Code: "required",
			Message: "トークンの名前を入力してください",
		})
	case n > tokenNameMaxLen:
		details = append(details, apierr.Detail{
			Field: "name", Code: "too_long",
			Message: fmt.Sprintf("名前は%d文字以内で入力してください", tokenNameMaxLen),
		})
	default:
		f.name = name
	}

	switch {
	case req.ExpiresInDays == nil:
		details = append(details, apierr.Detail{
			Field: "expires_in_days", Code: "required",
			Message: "有効期限を選択してください",
		})
	case *req.ExpiresInDays < tokenMinExpiresInDays || *req.ExpiresInDays > tokenMaxExpiresInDays:
		details = append(details, apierr.Detail{
			Field: "expires_in_days", Code: "invalid",
			Message: fmt.Sprintf("有効期限は%d〜%d日で指定してください",
				tokenMinExpiresInDays, tokenMaxExpiresInDays),
		})
	default:
		f.expiresInDays = *req.ExpiresInDays
	}

	// **nil と空配列を同じに扱う。** どちらも「絞り込みなし」である
	// （Design.md 6.4.1）。値域の照合はカタログを引くので別に行う。
	f.scopes = req.Scopes
	if f.scopes == nil {
		f.scopes = []string{}
	}
	for i, s := range f.scopes {
		if strings.TrimSpace(s) == "" {
			details = append(details, apierr.Detail{
				Field: "scopes", Code: "invalid",
				Message: fmt.Sprintf("%d番目のスコープが空です", i+1),
			})
			break
		}
	}

	if len(details) > 0 {
		return createTokenFields{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return f, nil
}

// ── DELETE /api/v1/me/tokens/{id}（4.4.3）───────────────────

// deleteMyToken は自分のアクセストークンを失効させる。
//
// **行は消さず revoked_at を立てる。** audit_log.token_id から辿れる先を
// 残すためである。
//
// **冪等。** 既に失効済みでも 204 を返す。ただし監査ログは二重に書かない
// ——同じ操作が2行になると、読み手が「2回切った」と読む。
func (h *handler) deleteMyToken(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("DELETE /me/tokens/{id} が認証ミドルウェアを通っていない")))
		return
	}
	id := chi.URLParam(r, "id")

	ctx := r.Context()
	rec := audit.FromRequest(r)

	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **他人のトークン・セッション・存在しない ID はすべて 404 に寄せる**
		// （Design.md 6.4.5「存在を隠す」）。FindMyAPIToken の WHERE が
		// actor_id と token_type を含んでいるので、区別せず1つの結果になる。
		row, err := q.FindMyAPIToken(ctx, gen.FindMyAPITokenParams{
			ID: id, ActorID: p.ActorID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return apierr.New(apierr.NotFound).
				WithMessage("アクセストークンが見つかりません")
		} else if err != nil {
			return fmt.Errorf("アクセストークンを引けない: %w", err)
		}
		if row.RevokedAt.Valid {
			// 既に失効済み。冪等に成功として返し、記録は足さない。
			return nil
		}

		if _, err := q.RevokeMyAPIToken(ctx, gen.RevokeMyAPITokenParams{
			ID: id, ActorID: p.ActorID,
		}); err != nil {
			return fmt.Errorf("アクセストークンを失効できない: %w", err)
		}

		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.TokenRevoke,
			Result:     audit.Success,
			TargetType: "access_token",
			TargetID:   id,
			Detail: map[string]any{
				"name":         row.Name.String,
				"token_prefix": row.TokenPrefix.String,
			},
		})
	})
	if err != nil {
		var apiErr *apierr.Error
		if errors.As(err, &apiErr) {
			apierr.Write(w, r, apiErr)
			return
		}
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
