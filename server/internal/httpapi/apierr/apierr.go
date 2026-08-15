// Package apierr は ApiDesign.md 2.5 のエラー応答形式を実装する。
//
//	{ "error": { "code", "message", "details": [...], "request_id" } }
//
// message は「そのまま画面に出せる日本語」とする規約のため、フロントで文言を
// 組み立てない。エンドポイント固有の文言は WithMessage で上書きする。
//
// request_id をコンテキストへ出し入れする関数を本パッケージが持つのは、
// request_id が 2.5 のエラー本体の一部であり、それを描画するのが本パッケージの
// 責務だからである。middleware パッケージが本パッケージを import する向きになり、
// 逆向きの依存（循環）を作らずに済む。
package apierr

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

// Code は ApiDesign.md 2.5.1 のエラーコード。表の15件と一対一で対応する。
type Code string

const (
	BadRequest                Code = "bad_request"
	Unauthenticated           Code = "unauthenticated"
	InvalidCredentials        Code = "invalid_credentials"
	Forbidden                 Code = "forbidden"
	CSRFFailed                Code = "csrf_failed"
	NotFound                  Code = "not_found"
	MethodNotAllowed          Code = "method_not_allowed"
	Conflict                  Code = "conflict"
	AlreadyExists             Code = "already_exists"
	LastAdministrator         Code = "last_administrator"
	SelfModificationForbidden Code = "self_modification_forbidden"
	ValidationFailed          Code = "validation_failed"
	AccountLocked             Code = "account_locked"
	RateLimited               Code = "rate_limited"
	InternalError             Code = "internal_error"
)

// statuses は ApiDesign.md 2.5.1 の Status 列。
var statuses = map[Code]int{
	BadRequest:                http.StatusBadRequest,
	Unauthenticated:           http.StatusUnauthorized,
	InvalidCredentials:        http.StatusUnauthorized,
	Forbidden:                 http.StatusForbidden,
	CSRFFailed:                http.StatusForbidden,
	NotFound:                  http.StatusNotFound,
	MethodNotAllowed:          http.StatusMethodNotAllowed,
	Conflict:                  http.StatusConflict,
	AlreadyExists:             http.StatusConflict,
	LastAdministrator:         http.StatusConflict,
	SelfModificationForbidden: http.StatusConflict,
	ValidationFailed:          http.StatusUnprocessableEntity,
	AccountLocked:             http.StatusLocked,
	RateLimited:               http.StatusTooManyRequests,
	InternalError:             http.StatusInternalServerError,
}

// messages は各コードの既定文言。
//
// 設計文書が文言を定めているのは validation_failed（ApiDesign.md 2.5）と
// invalid_credentials（Design.md 6.3）の2件のみで、残りは本実装で定めた。
// アカウントの存在を漏らさない、原因ではなく利用者の次の行動を書く、を方針とする。
var messages = map[Code]string{
	BadRequest:                "リクエストの形式が正しくありません",
	Unauthenticated:           "ログインしてください",
	InvalidCredentials:        "メールアドレスまたはパスワードが正しくありません",
	Forbidden:                 "この操作を行う権限がありません",
	CSRFFailed:                "セッションが無効です。画面を再読み込みしてからやり直してください",
	NotFound:                  "対象が見つかりません",
	MethodNotAllowed:          "この操作は許可されていません",
	Conflict:                  "他の変更と競合しました。最新の状態を読み込んでからやり直してください",
	AlreadyExists:             "その値は既に使われています。別の値を指定してください",
	LastAdministrator:         "最後のアドミニストレータのため、この操作はできません",
	SelfModificationForbidden: "自分自身に対してこの操作はできません",
	ValidationFailed:          "入力内容に誤りがあります",
	AccountLocked:             "ログインの失敗が続いたため、アカウントを一時的にロックしました。しばらくしてからやり直してください",
	RateLimited:               "リクエストが多すぎます。しばらくしてからやり直してください",
	InternalError:             "サーバ内部でエラーが発生しました",
}

// Detail はフィールド単位のエラー。フォームの各入力欄に紐づける（ApiDesign.md 2.5）。
type Detail struct {
	Field   string `json:"field"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Error は 2.5 のエラー本体。error インターフェースを満たすので、
// ハンドラから素の error として返して呼び出し側で Write できる。
type Error struct {
	Code    Code     `json:"code"`
	Message string   `json:"message"`
	Details []Detail `json:"details,omitempty"`
	// RetryAfterSec は再試行までの秒数。account_locked（ApiDesign.md 3.1）と
	// rate_limited（2.9）で使う。0 なら出さない。
	//
	// details ではなく本体の任意フィールドに置くのは、details が
	// {field, code, message} の配列であり（2.5）、数値を載せる場所が
	// 無いためである。フロントが「あと N 分」を組み立てられるよう、
	// 文言ではなく数値のまま返す。
	RetryAfterSec int    `json:"retry_after_sec,omitempty"`
	RequestID     string `json:"request_id,omitempty"`

	// cause は応答には出さず、サーバログにのみ残す内部原因。
	cause error
}

// envelope は error キーで包む外側（ApiDesign.md 2.5）。
type envelope struct {
	Error *Error `json:"error"`
}

// New は既定の文言を持つエラーを作る。未知のコードは internal_error として扱う。
func New(code Code) *Error {
	if _, ok := statuses[code]; !ok {
		code = InternalError
	}
	return &Error{Code: code, Message: messages[code]}
}

// WithMessage は画面に出す文言を差し替える。
func (e *Error) WithMessage(msg string) *Error {
	e.Message = msg
	return e
}

// WithDetails はフィールド単位のエラーを足す。
func (e *Error) WithDetails(details ...Detail) *Error {
	e.Details = append(e.Details, details...)
	return e
}

// WithRetryAfter は再試行までの秒数を添える。
//
// 応答本体の retry_after_sec と、ApiDesign.md 2.9 が定める Retry-After ヘッダの
// 両方になる。0以下なら何もしない。
func (e *Error) WithRetryAfter(sec int) *Error {
	if sec > 0 {
		e.RetryAfterSec = sec
	}
	return e
}

// WithCause は応答に出さない内部原因を添える。ログにのみ現れる。
func (e *Error) WithCause(err error) *Error {
	e.cause = err
	return e
}

// Status は ApiDesign.md 2.5.1 の HTTP ステータスを返す。
func (e *Error) Status() int {
	if s, ok := statuses[e.Code]; ok {
		return s
	}
	return http.StatusInternalServerError
}

// Error は error インターフェースの実装。ログ用であり、画面には出さない。
func (e *Error) Error() string {
	if e.cause != nil {
		return string(e.Code) + ": " + e.cause.Error()
	}
	return string(e.Code) + ": " + e.Message
}

// Unwrap は WithCause で添えた内部原因を返す。
func (e *Error) Unwrap() error { return e.cause }

// Write は 2.5 の形式で応答を書く。
//
// request_id はコンテキストから補う。5xx と内部原因はサーバログへ出し、
// 応答には内部原因を含めない（利用者に内部構造を見せないため）。
func Write(w http.ResponseWriter, r *http.Request, e *Error) {
	if e == nil {
		e = New(InternalError)
	}
	if e.RequestID == "" {
		e.RequestID = RequestIDFromContext(r.Context())
	}

	status := e.Status()

	// 内部原因を持つものだけを別行で出す（Design.md 10.1 エラーログ）。
	// ステータスとパスはアクセスログ側が1リクエスト1行で記録しているため、
	// ここで出すのは応答本文に含められない情報がある場合に限る。
	if e.cause != nil {
		attrs := []any{
			slog.String("request_id", e.RequestID),
			slog.String("code", string(e.Code)),
			slog.Int("status", status),
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.String("cause", e.cause.Error()),
		}
		if status >= http.StatusInternalServerError {
			slog.Error("リクエスト処理に失敗した", attrs...)
		} else {
			slog.Warn("リクエストを拒否した", attrs...)
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// ApiDesign.md 2.9 の Retry-After。標準ヘッダなので本体と併せて出す。
	if e.RetryAfterSec > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(e.RetryAfterSec))
	}
	w.WriteHeader(status)
	// 応答本文の書き込み失敗（クライアント切断など）は回復手段がないため握りつぶす。
	_ = json.NewEncoder(w).Encode(envelope{Error: e})
}

// WriteCode は New(code) をそのまま Write する短縮形。
func WriteCode(w http.ResponseWriter, r *http.Request, code Code) {
	Write(w, r, New(code))
}

// ── request_id のコンテキスト受け渡し ──────────────────────────

type contextKey struct{}

// NewRequestIDContext は request_id を載せたコンテキストを返す。
func NewRequestIDContext(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, contextKey{}, id)
}

// RequestIDFromContext は request_id を取り出す。無ければ空文字を返す。
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(contextKey{}).(string)
	return id
}
