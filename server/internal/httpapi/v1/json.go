package v1

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// decodeJSON は JSON 本文を読む。形式の誤りと上限超過は 400 bad_request にする。
func decodeJSON(r *http.Request, dst any) *apierr.Error {
	return decodeBoundedJSON(r, dst, maxRequestBodyBytes, false)
}

// decodeOptionalJSON は本文を省略できる口に使う。chunked 転送でも同じ判定にする。
func decodeOptionalJSON(r *http.Request, dst any) *apierr.Error {
	return decodeBoundedJSON(r, dst, maxRequestBodyBytes, true)
}

func decodeBoundedJSON(r *http.Request, dst any, limit int64, optional bool) *apierr.Error {
	// 上限+1まで読んで超過を検知する。先頭のJSONだけで成功させると、
	// 後続の別のJSONやごみ、上限を超えた空白を黙って受け入れてしまう。
	body, err := io.ReadAll(io.LimitReader(r.Body, limit+1))
	if err != nil {
		return apierr.New(apierr.BadRequest).WithCause(fmt.Errorf("リクエスト本文を読めない: %w", err))
	}
	if int64(len(body)) > limit {
		return apierr.New(apierr.BadRequest).WithCause(fmt.Errorf("リクエスト本文が%dバイトを超えている", limit))
	}
	if optional && len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	// Unmarshalは末尾の空白以外を拒み、本文全体が1つのJSONであることを確かめる。
	if err := json.Unmarshal(body, dst); err != nil {
		return apierr.New(apierr.BadRequest).WithCause(fmt.Errorf("リクエスト本文を JSON として読めない: %w", err))
	}
	return nil
}
