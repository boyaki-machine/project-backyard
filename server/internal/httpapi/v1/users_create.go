// ユーザー管理API（ApiDesign.md 6章）のうち、作成。
//
//	POST /api/v1/admin/users  6.2
//
// 一覧（6.1）は users.go にある。
package v1

import (
	"errors"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// password_mode の値（ApiDesign.md 6.2）。
const (
	passwordModeGenerate = "generate"
	passwordModeManual   = "manual"
)

// system_role の値（ApiDesign.md 6.2、DbDesign.md 6.2 の CHECK 制約）。
const (
	systemRoleOperator      = "operator"
	systemRoleAdministrator = "administrator"
)

// 表示名の長さ（ApiDesign.md 6.2、DbDesign.md 6.2 の actor.display_name の
// CHECK 制約と同じ 1〜60）。**片方だけ変えない。**
const (
	displayNameMinLen = 1
	displayNameMaxLen = 60
)

// emailMaxLen はメールアドレスの上限。RFC 5321 4.5.3.1.3 の path の上限が
// 256 オクテットで、両端の <> を除いた 254 が実用上の上限として使われる。
// app_user.email 側には長さ制限が無いため、ここが唯一の関門になる。
const emailMaxLen = 254

// localProviderKey は唯一の認証プロバイダ（DbDesign.md 7.1）。
const localProviderKey = "local"

// adminUsersPath は 201 の Location ヘッダに使う（ApiDesign.md 6.2 / 6.3）。
const adminUsersPath = "/api/v1/admin/users"

// createUserRequest は 6.2 のリクエストボディ。
//
// **ポインタで受けるのは省略と明示的な値を区別するため**である。
// password_mode と must_change_password は省略時の既定が「無指定」ではなく
// generate / true であり、ゼロ値（"" / false）と取り違えると挙動が変わる。
type createUserRequest struct {
	DisplayName        string  `json:"display_name"`
	Email              string  `json:"email"`
	SystemRole         *string `json:"system_role"`
	PasswordMode       *string `json:"password_mode"`
	Password           *string `json:"password"`
	MustChangePassword *bool   `json:"must_change_password"`
}

// createdUserView は 6.2 の 201 応答。
//
// 一覧（6.1）の要素とは別の型にしている。6.2 が返すのは作成直後の確定値だけで、
// project_count や last_login_at のような一覧固有の項目を持たないためである。
type createdUserView struct {
	ID                string  `json:"id"`
	Kind              string  `json:"kind"`
	DisplayName       string  `json:"display_name"`
	Email             string  `json:"email"`
	SystemRole        string  `json:"system_role"`
	IsActive          bool    `json:"is_active"`
	GeneratedPassword *string `json:"generated_password"`
}

// createUser は POST /api/v1/admin/users を処理する（ApiDesign.md 6.2）。
//
// **actor → app_user → user_identity → local_credential を単一トランザクション
// で作る**（6.2、DbDesign.md 6.2）。途中で失敗すると、ログインできない
// app_user や、資格情報の無い identity が残るため。
func (h *handler) createUser(w http.ResponseWriter, r *http.Request) {
	var req createUserRequest
	if err := decodeJSON(r, &req); err != nil {
		apierr.Write(w, r, err)
		return
	}

	fields, verr := validateCreateUser(req)
	if verr != nil {
		apierr.Write(w, r, verr)
		return
	}

	// **平文は生成した経路でしか持たない。** manual のときは応答に載せない
	// （6.2 の generated_password は「この応答でのみ返る」もので、呼び出し側が
	// 既に知っている値を返しても意味がない）。
	password := fields.password
	var generated *string
	if fields.passwordMode == passwordModeGenerate {
		p, err := auth.GeneratePassword()
		if err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
			return
		}
		password = p
		generated = &p
	}

	hash, err := auth.HashPassword(password)
	if err != nil {
		// 長さの検証は validateCreateUser で済んでいるので、ここに来るのは
		// ハッシュ化そのものの失敗である。利用者の入力の誤りではない。
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("初期パスワードをハッシュ化できない: %w", err)))
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)
	actorID := ulidgen.New()
	identityID := ulidgen.New()

	err = h.tx.RunInTx(ctx, func(q gen.Querier) error {
		if err := q.CreateUserActor(ctx, gen.CreateUserActorParams{
			ID:          actorID,
			DisplayName: fields.displayName,
		}); err != nil {
			return fmt.Errorf("actor を作成できない: %w", err)
		}
		if err := q.CreateAppUser(ctx, gen.CreateAppUserParams{
			ActorID:    actorID,
			Email:      fields.email,
			SystemRole: fields.systemRole,
		}); err != nil {
			// メールの一意制約違反はそのまま返す。呼び出し側で 409 に写す。
			return err
		}
		// subject には app_user へ入れたのと同じ文字列を使う（DbDesign.md 6.2）。
		// user_identity.subject は text（大小を区別する）で、app_user.email は
		// citext である。別の表記を入れると、ログイン時の突き合わせ
		// （Design.md 6.2.1 手順3）で外れる。
		if err := q.CreateUserIdentity(ctx, gen.CreateUserIdentityParams{
			ID:          identityID,
			UserID:      actorID,
			ProviderKey: localProviderKey,
			Subject:     fields.email,
		}); err != nil {
			return fmt.Errorf("user_identity を作成できない: %w", err)
		}
		if err := q.CreateLocalCredential(ctx, gen.CreateLocalCredentialParams{
			IdentityID:   identityID,
			PasswordHash: hash,
			MustChange:   fields.mustChangePassword,
		}); err != nil {
			return fmt.Errorf("local_credential を作成できない: %w", err)
		}

		// **監査は同じトランザクションで書く**（手順4b の方針、POST /projects と同じ）。
		// 記録の無いユーザーが生まれないよう、失敗したら作成ごと失敗させる。
		//
		// **detail に平文のパスワードを入れない。** audit_log は長期保存される
		// 記録であり、初期パスワードが残ると 6.2 の「この応答でのみ返る」が崩れる。
		return rec.Record(ctx, q, audit.Entry{
			Action:     audit.UserCreate,
			Result:     audit.Success,
			TargetType: "app_user",
			TargetID:   actorID,
			Detail: map[string]any{
				"email":                fields.email,
				"system_role":          fields.systemRole,
				"password_mode":        fields.passwordMode,
				"must_change_password": fields.mustChangePassword,
			},
		})
	})
	if err != nil {
		writeCreateUserError(w, r, fields.email, err)
		return
	}

	w.Header().Set("Location", adminUsersPath+"/"+actorID)
	WriteJSON(w, http.StatusCreated, createdUserView{
		ID:                actorID,
		Kind:              userKindUser,
		DisplayName:       fields.displayName,
		Email:             fields.email,
		SystemRole:        fields.systemRole,
		IsActive:          true,
		GeneratedPassword: generated,
	})
}

// createUserFields は検証を通ったあとの確定値。
//
// 省略と既定の解決をここで済ませ、以降はポインタを扱わない。
type createUserFields struct {
	displayName        string
	email              string
	systemRole         string
	passwordMode       string
	password           string
	mustChangePassword bool
}

// validateCreateUser は 6.2 の検証表を実装する。
//
// **すべての項目を見てから返す。** 1つ目で打ち切ると、フォームが誤りを
// 1件ずつしか出せない（ApiDesign.md 2.5 の details は項目ごとに紐づける）。
func validateCreateUser(req createUserRequest) (createUserFields, *apierr.Error) {
	var details []apierr.Detail
	f := createUserFields{}

	f.displayName = strings.TrimSpace(req.DisplayName)
	switch n := utf8.RuneCountInString(f.displayName); {
	case n < displayNameMinLen:
		details = append(details, apierr.Detail{
			Field: "display_name", Code: "required", Message: "表示名を入力してください",
		})
	case n > displayNameMaxLen:
		details = append(details, apierr.Detail{
			Field: "display_name", Code: "too_long",
			Message: fmt.Sprintf("表示名は%d文字以内で入力してください", displayNameMaxLen),
		})
	}

	f.email = strings.TrimSpace(req.Email)
	if d := validateEmail(f.email); d != nil {
		details = append(details, *d)
	}

	f.systemRole = systemRoleOperator
	if req.SystemRole != nil {
		switch *req.SystemRole {
		case systemRoleOperator, systemRoleAdministrator:
			f.systemRole = *req.SystemRole
		default:
			details = append(details, apierr.Detail{
				Field: "system_role", Code: "invalid",
				Message: "system_role は operator または administrator で指定してください",
			})
		}
	}

	// 省略時は generate（GuiDesign.md 5.6.1 の初期選択が「自動生成して表示する」）。
	f.passwordMode = passwordModeGenerate
	if req.PasswordMode != nil {
		switch *req.PasswordMode {
		case passwordModeGenerate, passwordModeManual:
			f.passwordMode = *req.PasswordMode
		default:
			details = append(details, apierr.Detail{
				Field: "password_mode", Code: "invalid",
				Message: "password_mode は generate または manual で指定してください",
			})
		}
	}

	// password は manual のときだけ必須（6.2）。generate のときに送られても
	// 使わない。**「使わない値が送られた」を 422 にはしない**。モードを
	// 切り替えるフォームが前の入力を残したまま送るのは自然な作りであり、
	// それを誤りとして弾くと画面側が余計な制御を持つことになる。
	if f.passwordMode == passwordModeManual {
		switch {
		case req.Password == nil || *req.Password == "":
			details = append(details, apierr.Detail{
				Field: "password", Code: "required", Message: "パスワードを入力してください",
			})
		default:
			f.password = *req.Password
			if err := auth.ValidatePassword(f.password); err != nil {
				details = append(details, apierr.Detail{
					Field: "password", Code: "too_short", Message: err.Error(),
				})
			}
		}
	}

	// 省略時は true（GuiDesign.md 5.6.1 のチェックボックスが既定でオン。
	// 6.2 のリクエスト例も true）。管理者が決めたパスワードを本人が
	// 使い続ける状態を既定にしない。
	f.mustChangePassword = true
	if req.MustChangePassword != nil {
		f.mustChangePassword = *req.MustChangePassword
	}

	if len(details) > 0 {
		return createUserFields{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return f, nil
}

// validateEmail は 6.2 の「形式検証」を実装する。誤りが無ければ nil。
//
// **net/mail.ParseAddress を使い、その上で表示名付きの形式を弾く。**
// ParseAddress は RFC 5322 の解析器で `山田 <a@example.com>` も通すが、
// app_user.email に入れてよいのはアドレス部だけである。自前の正規表現を
// 書かないのは、RFC 5322 のアドレスが正規表現で正しく書ける形をしておらず、
// 手書きの式は必ず正しいアドレスを弾くか誤ったものを通すかのどちらかになるため。
func validateEmail(email string) *apierr.Detail {
	if email == "" {
		return &apierr.Detail{
			Field: "email", Code: "required", Message: "メールアドレスを入力してください",
		}
	}
	if len(email) > emailMaxLen {
		return &apierr.Detail{
			Field: "email", Code: "too_long",
			Message: fmt.Sprintf("メールアドレスは%d文字以内で入力してください", emailMaxLen),
		}
	}
	addr, err := mail.ParseAddress(email)
	if err != nil || addr.Name != "" || addr.Address != email {
		return &apierr.Detail{
			Field: "email", Code: "invalid", Message: "メールアドレスの形式が正しくありません",
		}
	}
	return nil
}

// writeCreateUserError はトランザクションの失敗を応答へ写す。
//
// **メールの重複だけを 409 に分ける**（6.2）。他は 500 に倒す。
func writeCreateUserError(w http.ResponseWriter, r *http.Request, email string, err error) {
	if isEmailConflict(err) {
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage(fmt.Sprintf("メールアドレス %s は既に使われています", email)).
			WithDetails(apierr.Detail{
				Field: "email", Code: "already_exists",
				Message: "別のメールアドレスを指定してください",
			}))
		return
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).
		WithCause(fmt.Errorf("ユーザーを作成できない: %w", err)))
}

// isEmailConflict はメールの一意制約違反かを判定する。
//
// 23505 は unique_violation（PostgreSQL のエラーコード）。同じトランザクション内の
// 他の INSERT は ULID を主キーにしており衝突しないため、app_user 表の 23505 は
// email 以外にありえない。user_identity の (provider_key, subject) にも
// UNIQUE があるが、subject は同じメールから作るので app_user 側が先に落ちる。
func isEmailConflict(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return false
	}
	return pgErr.TableName == "app_user" || pgErr.ConstraintName == "app_user_email_key"
}
