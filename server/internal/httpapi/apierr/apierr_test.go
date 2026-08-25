package apierr

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// エラーログがテスト出力を埋めないよう、既定ロガーを捨てる。
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// ApiDesign.md 2.5.1 の表をそのまま写したもの。実装との対応を1件ずつ照合する。
var table = []struct {
	code   Code
	status int
}{
	{BadRequest, 400},
	{Unauthenticated, 401},
	{InvalidCredentials, 401},
	{Forbidden, 403},
	{CSRFFailed, 403},
	{NotFound, 404},
	{MethodNotAllowed, 405},
	{Conflict, 409},
	{AlreadyExists, 409},
	{LastAdministrator, 409},
	{SelfModificationForbidden, 409},
	{ValidationFailed, 422},
	{AccountLocked, 423},
	{RateLimited, 429},
	{InternalError, 500},
}

// 領域ごとの追加コード。**2.5.1 の表に「加わるもの」**として、章ごとに定義される。
//
// いまは 9.14（チケット）の1件だけである。ここへ足すときは、必ず設計文書側の
// 表にも同じ行があること——**実装だけに在るコードは、消費者が知りようがない。**
var domainTable = []struct {
	code   Code
	status int
	docRef string
}{
	{InvalidTransition, 409, "ApiDesign.md 9.14"},
}

func TestStatusMatchesApiDesign(t *testing.T) {
	if want := len(table) + len(domainTable); len(statuses) != want {
		t.Fatalf("コード数が設計文書の表と一致しない: 実装 %d 件 / 表 %d 件（2.5.1 が %d 件・領域ごとの追加が %d 件）",
			len(statuses), want, len(table), len(domainTable))
	}
	for _, tt := range table {
		if got := New(tt.code).Status(); got != tt.status {
			t.Errorf("%s: status = %d, want %d", tt.code, got, tt.status)
		}
	}
	for _, tt := range domainTable {
		if got := New(tt.code).Status(); got != tt.status {
			t.Errorf("%s（%s）: status = %d, want %d", tt.code, tt.docRef, got, tt.status)
		}
	}
}

func TestEveryCodeHasJapaneseMessage(t *testing.T) {
	for _, tt := range table {
		msg := New(tt.code).Message
		if msg == "" {
			t.Errorf("%s: 既定メッセージが空", tt.code)
		}
	}
	for _, tt := range domainTable {
		if msg := New(tt.code).Message; msg == "" {
			t.Errorf("%s（%s）: 既定メッセージが空", tt.code, tt.docRef)
		}
	}
}

func TestUnknownCodeFallsBackToInternalError(t *testing.T) {
	e := New(Code("no_such_code"))
	if e.Code != InternalError {
		t.Errorf("code = %q, want %q", e.Code, InternalError)
	}
	if e.Status() != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", e.Status())
	}
}

// Write が ApiDesign.md 2.5 の形（error で包み、details と request_id を持つ）で
// 書き出すことを確認する。
func TestWriteShape(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/projects", nil)
	req = req.WithContext(NewRequestIDContext(req.Context(), "01K2F8QW3H7YRJ4M5N6P7Q8R9S"))

	Write(rec, req, New(ValidationFailed).WithDetails(Detail{
		Field:   "key",
		Code:    "already_exists",
		Message: "このプロジェクトキーは使用されています",
	}))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", ct)
	}

	var got struct {
		Error struct {
			Code      string `json:"code"`
			Message   string `json:"message"`
			RequestID string `json:"request_id"`
			Details   []struct {
				Field   string `json:"field"`
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("応答が JSON として読めない: %v (%s)", err, rec.Body.String())
	}
	if got.Error.Code != "validation_failed" {
		t.Errorf("code = %q", got.Error.Code)
	}
	if got.Error.Message != "入力内容に誤りがあります" {
		t.Errorf("message = %q", got.Error.Message)
	}
	if got.Error.RequestID != "01K2F8QW3H7YRJ4M5N6P7Q8R9S" {
		t.Errorf("request_id = %q（コンテキストから補われていない）", got.Error.RequestID)
	}
	if len(got.Error.Details) != 1 || got.Error.Details[0].Field != "key" {
		t.Errorf("details = %+v", got.Error.Details)
	}
}

// details が無いときは JSON に details キー自体を出さない（omitempty）。
func TestWriteOmitsEmptyDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	Write(rec, req, New(NotFound))

	var raw map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("応答が JSON として読めない: %v", err)
	}
	if _, ok := raw["error"]["details"]; ok {
		t.Errorf("details が空でも出力されている: %s", rec.Body.String())
	}
}

// 内部原因は応答に出さない。ログにだけ残す。
func TestWriteDoesNotLeakCause(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	cause := errors.New("pq: relation \"secret_table\" does not exist")
	Write(rec, req, New(InternalError).WithCause(cause))

	if body := rec.Body.String(); contains(body, "secret_table") {
		t.Errorf("内部原因が応答に漏れている: %s", body)
	}
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// 内部原因を持たないエラーはログ行を出さない。
// ステータスとパスはアクセスログが1行で記録しているため（Design.md 10.1）。
func TestWriteLogsOnlyWhenCausePresent(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)

	Write(httptest.NewRecorder(), req, New(NotFound))
	if buf.Len() != 0 {
		t.Errorf("cause 無しでログが出ている: %s", buf.String())
	}

	Write(httptest.NewRecorder(), req, New(InternalError).WithCause(errors.New("pool exhausted")))
	if buf.Len() == 0 {
		t.Error("cause 有りでログが出ていない")
	}
	if !contains(buf.String(), "pool exhausted") {
		t.Errorf("cause がログに出ていない: %s", buf.String())
	}
}

func TestErrorUnwrapsCause(t *testing.T) {
	cause := errors.New("原因")
	e := New(Conflict).WithCause(cause)
	if !errors.Is(e, cause) {
		t.Error("errors.Is が cause を辿れない")
	}
}

func TestWithMessageOverrides(t *testing.T) {
	e := New(Conflict).WithMessage("このプロジェクトキーは使用されています")
	if e.Message != "このプロジェクトキーは使用されています" {
		t.Errorf("message = %q", e.Message)
	}
	if e.Status() != http.StatusConflict {
		t.Errorf("status = %d, want 409", e.Status())
	}
}

func TestRequestIDFromEmptyContext(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if id := RequestIDFromContext(req.Context()); id != "" {
		t.Errorf("request_id = %q, want 空文字", id)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// retry_after_sec は応答本体と Retry-After ヘッダの両方に出る
// （ApiDesign.md 3.1 / 2.9）。
func TestWithRetryAfter(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	Write(rec, req, New(AccountLocked).WithRetryAfter(842))

	if rec.Code != http.StatusLocked {
		t.Fatalf("status = %d, want 423", rec.Code)
	}
	if got := rec.Header().Get("Retry-After"); got != "842" {
		t.Errorf("Retry-After = %q, want 842", got)
	}

	var body struct {
		Error struct {
			Code          string `json:"code"`
			RetryAfterSec int    `json:"retry_after_sec"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("応答が JSON でない: %v", err)
	}
	if body.Error.RetryAfterSec != 842 {
		t.Errorf("retry_after_sec = %d, want 842", body.Error.RetryAfterSec)
	}
}

// 0以下は無視し、フィールドもヘッダも出さない。
func TestWithRetryAfterIgnoresNonPositive(t *testing.T) {
	for _, sec := range []int{0, -1} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/me", nil)
		Write(rec, req, New(RateLimited).WithRetryAfter(sec))

		if got := rec.Header().Get("Retry-After"); got != "" {
			t.Errorf("sec=%d: Retry-After = %q, want 空", sec, got)
		}
		if strings.Contains(rec.Body.String(), "retry_after_sec") {
			t.Errorf("sec=%d: retry_after_sec が出ている: %s", sec, rec.Body.String())
		}
	}
}
