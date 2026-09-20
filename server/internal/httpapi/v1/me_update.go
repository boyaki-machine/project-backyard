// 自分自身に関するAPI（ApiDesign.md 4章）のうち、プロフィールの更新。
//
//	PATCH /api/v1/me  4.2
//
// **6.4（管理者による更新）と更新できる列が違う。** 本人は
// locale / timezone / theme / hue を変えられるが system_role と is_active は
// 変えられず、管理者はその逆である。両者を1つのハンドラにまとめると、
// どちらの経路からでも全列が書ける形になり、境界がコードから読めなくなる。
package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/audit"
	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// 見た目の設定の値域（GuiDesign.md 8.11、DbDesign.md 6.2 の CHECK 制約）。
//
// **DB の CHECK と同じ値をここにも置く。** 制約違反を 500 で返さず、
// 422 の details に写して入力欄へ紐づけるためである（ApiDesign.md 2.5）。
const (
	themeLight  = "light"
	themeDark   = "dark"
	themeSystem = "system"

	hueBlue  = "blue"
	hueGreen = "green"
)

// localeJa / localeEn は画面が対応する表示言語（GuiDesign.md 5.8）。
//
// **DB に CHECK 制約が無い列である**（DbDesign.md 6.2）。値域を決めるのは
// アプリ側であり、増やすときはここと画面の選択肢を同時に足す。
const (
	localeJa = "ja"
	localeEn = "en"
)

// timezoneMaxLen は timezone の上限。IANA のタイムゾーン名で最も長いものが
// 32文字程度（America/Argentina/ComodRivadavia）なので、余裕を見て倍に取る。
// 長さで弾くのは、後段の time.LoadLocation に極端な値を渡さないためである。
const timezoneMaxLen = 64

// updateMeRequest は 4.2 のリクエスト本体。
//
// **「送られなかった」と「送られた」を区別できる型で受ける**（6.4 と同じ）。
// 6項目とも NULL への更新が無い列なので *string で足りる。
//
// SystemRole を受け口として持つのは、**送られたことを検出して 422 を返す**
// ためである（4.2「送られた場合は無視せず 422 を返す」）。黙って捨てると、
// 呼び出し側は権限が上がったと誤解したまま動く。
type updateMeRequest struct {
	DisplayName *string `json:"display_name"`
	Email       *string `json:"email"`
	Locale      *string `json:"locale"`
	Timezone    *string `json:"timezone"`
	Theme       *string `json:"theme"`
	Hue         *string `json:"hue"`
	SystemRole  *string `json:"system_role"`
}

// updateMeFields は検証を通ったあとの確定値。nil は「変更しない」。
type updateMeFields struct {
	displayName *string
	email       *string
	locale      *string
	timezone    *string
	theme       *string
	hue         *string
}

// changed は変更が1つでもあるかを返す。
func (f updateMeFields) changed() bool {
	return f.displayName != nil || f.email != nil || f.locale != nil ||
		f.timezone != nil || f.theme != nil || f.hue != nil
}

// patchMe は PATCH /api/v1/me を処理する（ApiDesign.md 4.2）。
//
// **楽観ロック（2.8）は課さない。** 自分の設定を同時に2箇所から編集する状況が
// 実質無く、課すと GET /me に ETag が要る——全画面の起動時に呼ばれるため
// 影響が広い。ただし version は加算し、6.4 の If-Match を壊さない。
func (h *handler) patchMe(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("PATCH /me が認証ミドルウェアを通っていない")))
		return
	}

	var req updateMeRequest
	if e := decodeJSON(r, &req); e != nil {
		apierr.Write(w, r, e)
		return
	}
	fields, e := validateUpdateMe(req)
	if e != nil {
		apierr.Write(w, r, e)
		return
	}

	ctx := r.Context()
	rec := audit.FromRequest(r)

	var view sessionView
	err := h.tx.RunInTx(ctx, func(q gen.Querier) error {
		// **現在値をトランザクションの中で読む。** 監査ログの「変更前」が
		// 更新と別の時点の状態になることを防ぐ。
		cur, err := q.GetActorProfile(ctx, p.ActorID)
		if err != nil {
			return err
		}

		rows, err := q.UpdateMyProfile(ctx, gen.UpdateMyProfileParams{
			Email:    nargText(fields.email),
			Locale:   nargText(fields.locale),
			Timezone: nargText(fields.timezone),
			Theme:    nargText(fields.theme),
			Hue:      nargText(fields.hue),
			ActorID:  p.ActorID,
		})
		if err != nil {
			return err // メールの一意制約違反はそのまま返す。呼び出し側で 409 に写す
		}
		if rows == 0 {
			// **version 不一致という選択肢が無い**（条件に使っていない）ので、
			// 0行は「自分の行が消えた」ことだけを意味する。認証を通った直後に
			// 管理者が削除した場合に起きる。
			return pgx.ErrNoRows
		}

		if err := q.UpdateMyDisplayName(ctx, gen.UpdateMyDisplayNameParams{
			DisplayName: nargText(fields.displayName),
			ActorID:     p.ActorID,
		}); err != nil {
			return fmt.Errorf("actor を更新できない: %w", err)
		}

		// **メールを変えたら user_identity.subject も追随させる**（4.2）。
		// ログインは subject = app_user.email で突き合わせており
		// （Design.md 6.2.1 手順2〜3）、片方だけ変えると当人がログインできなくなる。
		// 6.4（管理者による変更）と同じ理由・同じ操作である。
		if fields.email != nil {
			if err := q.UpdateLocalIdentitySubject(ctx, gen.UpdateLocalIdentitySubjectParams{
				Subject: *fields.email,
				UserID:  p.ActorID,
			}); err != nil {
				return fmt.Errorf("user_identity.subject を更新できない: %w", err)
			}
		}

		if err := recordMeUpdate(ctx, q, rec, p.ActorID, cur, fields); err != nil {
			return err
		}

		view, err = h.buildMeView(ctx, q, p)
		return err
	})
	if err != nil {
		writeMeUpdateError(w, r, p.ActorID, fields.email, err)
		return
	}

	WriteJSON(w, http.StatusOK, view)
}

// buildMeView は更新後の応答を組み立てる。
//
// **GET /me（4.1）と同一構造にする**（4.2）。画面はこの応答で表示を差し替える
// （GuiDesign.md 6.4「サーバ応答後に反映する」）ため、形が違うと auth ストアへ
// そのまま流し込めない。5.5 の PATCH が 5.4 と同形式であるのと同じ扱いである。
//
// **権限は読み直さない。** 4.2 で変えられる列に権限へ効くものが無く
// （system_role は 422 で弾く）、ミドルウェアが同じリクエストで解決済みの
// 値をそのまま使えばよい。
func (h *handler) buildMeView(
	ctx context.Context, q gen.Querier, p *auth.Principal,
) (sessionView, error) {
	row, err := q.GetActorProfile(ctx, p.ActorID)
	if err != nil {
		return sessionView{}, fmt.Errorf("更新後のプロフィールを読めない: %w", err)
	}

	systemPerms, ok := auth.SystemPermissionsFromContext(ctx)
	if !ok {
		// 認可ミドルウェアを通らないルート（4.2 は権限キーを要求しない）では
		// 載っていない。GET /me と同じ経路で解決する（Design.md 6.4.5）。
		systemPerms, _, err = middleware.SystemPermissions(ctx, q, p)
		if err != nil {
			return sessionView{}, err
		}
	}

	return h.buildSessionView(ctx, q, profile{
		ActorID:            row.ActorID,
		Kind:               row.Kind,
		DisplayName:        row.DisplayName,
		Email:              row.Email.String,
		SystemRole:         row.SystemRole.String,
		Locale:             row.Locale.String,
		Timezone:           row.Timezone.String,
		Theme:              row.Theme.String,
		Hue:                row.Hue.String,
		MustChangePassword: row.MustChange.Bool,
	}, systemPerms, p.Scopes, p.ExpiresAt)
}

// ── 監査（ApiDesign.md 2.10）─────────────────────────────────

// recordMeUpdate は 4.2 の監査を書く。
//
// **action は user.update である**（2.10 の語彙にこれ以外の当てはまりが無い）。
// 6.4 と同じ action になるが、`actor_id` と `target_id` が一致することで
// 「本人が自分で変えた」と読み分けられる。
//
// **role.change は書かない。** 4.2 では system_role を変えられないためである。
//
// detail には**変更した項目だけ**を前後の形で入れる（6.4 と同じ）。
// theme / hue も残す——見た目の設定であっても、変わった経緯を追えることに
// 変わりはなく、選り分ける基準を作るほうが読み手を迷わせる。
func recordMeUpdate(
	ctx context.Context, q gen.Querier, rec *audit.Recorder,
	actorID string, cur gen.GetActorProfileRow, f updateMeFields,
) error {
	if !f.changed() {
		// 何も送られていない PATCH。version は進むが、記録することが無い。
		return nil
	}

	detail := map[string]any{}
	if f.displayName != nil {
		detail["display_name"] = changeDetail(cur.DisplayName, *f.displayName)
	}
	if f.email != nil {
		detail["email"] = changeDetail(cur.Email.String, *f.email)
	}
	if f.locale != nil {
		detail["locale"] = changeDetail(cur.Locale.String, *f.locale)
	}
	if f.timezone != nil {
		detail["timezone"] = changeDetail(cur.Timezone.String, *f.timezone)
	}
	if f.theme != nil {
		detail["theme"] = changeDetail(cur.Theme.String, *f.theme)
	}
	if f.hue != nil {
		detail["hue"] = changeDetail(cur.Hue.String, *f.hue)
	}

	return rec.Record(ctx, q, audit.Entry{
		Action:     audit.UserUpdate,
		Result:     audit.Success,
		TargetType: "app_user",
		TargetID:   actorID,
		Detail:     detail,
	})
}

// ── 入力の検証（ApiDesign.md 4.2）─────────────────────────────

// validateUpdateMe は 4.2 の変更可能項目を検証する。
//
// **display_name と email の規則は 6.2 / 6.4 と共有する。** 経路によって
// 通る値が変わると、同じ表に別の規則で入った行が混ざる。
//
// **すべての項目を見てから返す**（2.5 の details は項目ごとに紐づける）。
func validateUpdateMe(req updateMeRequest) (updateMeFields, *apierr.Error) {
	var details []apierr.Detail
	var f updateMeFields

	// **system_role は「送られたこと」自体が誤りである**（4.2）。
	// 値の中身は見ない——operator を送られても administrator を送られても、
	// 本人が自分のロールを触れないことに変わりはない。
	if req.SystemRole != nil {
		details = append(details, apierr.Detail{
			Field: "system_role", Code: "not_allowed",
			Message: "システムロールはご自身では変更できません。アドミニストレータにご依頼ください",
		})
	}

	if req.DisplayName != nil {
		name := strings.TrimSpace(*req.DisplayName)
		switch n := utf8.RuneCountInString(name); {
		case n < displayNameMinLen:
			details = append(details, apierr.Detail{
				Field: "display_name", Code: "required", Message: "表示名を入力してください",
			})
		case n > displayNameMaxLen:
			details = append(details, apierr.Detail{
				Field: "display_name", Code: "too_long",
				Message: fmt.Sprintf("表示名は%d文字以内で入力してください", displayNameMaxLen),
			})
		default:
			f.displayName = &name
		}
	}

	if req.Email != nil {
		email := strings.TrimSpace(*req.Email)
		if d := validateEmail(email); d != nil {
			details = append(details, *d)
		} else {
			f.email = &email
		}
	}

	if req.Locale != nil {
		switch *req.Locale {
		case localeJa, localeEn:
			f.locale = req.Locale
		default:
			details = append(details, apierr.Detail{
				Field: "locale", Code: "invalid",
				Message: "言語は日本語（ja）または英語（en）を選択してください",
			})
		}
	}

	if req.Timezone != nil {
		if d := validateTimezone(*req.Timezone); d != nil {
			details = append(details, *d)
		} else {
			f.timezone = req.Timezone
		}
	}

	if req.Theme != nil {
		switch *req.Theme {
		case themeLight, themeDark, themeSystem:
			f.theme = req.Theme
		default:
			details = append(details, apierr.Detail{
				Field: "theme", Code: "invalid",
				Message: "テーマは light / dark / system のいずれかで指定してください",
			})
		}
	}

	if req.Hue != nil {
		switch *req.Hue {
		case hueBlue, hueGreen:
			f.hue = req.Hue
		default:
			details = append(details, apierr.Detail{
				Field: "hue", Code: "invalid",
				Message: "色相は blue または green で指定してください",
			})
		}
	}

	if len(details) > 0 {
		return updateMeFields{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return f, nil
}

// validateTimezone は IANA のタイムゾーン名として解決できるかを見る。
//
// **値の一覧を持たない。** IANA のデータベースは更新されるものであり、
// アプリ側に写しを置くと古くなる。Go の time.LoadLocation は実行環境の
// tzdata（または埋め込みの time/tzdata）を引くので、そこを正本にする。
//
// **"Local" を弾く。** time.LoadLocation はこの名前をサーバのローカル時刻と
// して解決してしまうが、利用者ごとの設定として保存する値ではない（誰の
// ローカルかがサーバの設定に依存し、端末をまたぐと意味が変わる）。
// **"UTC" は通す**——こちらは名前だけで一意に定まる。
func validateTimezone(tz string) *apierr.Detail {
	invalid := &apierr.Detail{
		Field: "timezone", Code: "invalid",
		Message: "タイムゾーンは Asia/Tokyo のような IANA の名前で指定してください",
	}
	if tz == "" || len(tz) > timezoneMaxLen || tz == "Local" {
		return invalid
	}
	if _, err := time.LoadLocation(tz); err != nil {
		return invalid
	}
	return nil
}

// ── 応答への写し ────────────────────────────────────────────

// writeMeUpdateError は 4.2 の失敗を応答へ写す。
func writeMeUpdateError(
	w http.ResponseWriter, r *http.Request, actorID string, email *string, err error,
) {
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) {
		apierr.Write(w, r, apiErr)
		return
	}
	// **メールの重複は 6.4 と同じ already_exists に写す。** 呼び出し側が
	// 「メールが使われている」を1つのコードで扱えるようにする。
	if email != nil && isEmailConflict(err) {
		apierr.Write(w, r, apierr.New(apierr.AlreadyExists).
			WithMessage(fmt.Sprintf("メールアドレス %s は既に使われています", *email)).
			WithDetails(apierr.Detail{
				Field: "email", Code: "already_exists",
				Message: "別のメールアドレスを指定してください",
			}))
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		// 認証を通った直後に自分のアカウントが消えた場合。**401 に倒す。**
		// 404 だと「/me というパスが無い」と読めてしまう。
		apierr.Write(w, r, apierr.New(apierr.Unauthenticated).
			WithCause(fmt.Errorf("アクター %q が見つからない", actorID)))
		return
	}
	apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
}
