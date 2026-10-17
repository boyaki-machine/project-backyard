package v1

import (
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONWholeBody(t *testing.T) {
	for _, tc := range []struct {
		name, body      string
		optional, valid bool
	}{
		{"object", `{"value":"ok"}`, false, true},
		{"trailing whitespace", "{\"value\":\"ok\"} \r\n\t", false, true},
		{"second object", `{"value":"ok"}{"value":"other"}`, false, false},
		{"trailing garbage", `{"value":"ok"}garbage`, false, false},
		{"trailing null", `{"value":"ok"} null`, false, false},
		{"missing required body", "", false, false},
		{"missing optional body", "", true, true},
		{"optional whitespace", " \r\n\t", true, true},
		{"optional trailing garbage", `{}garbage`, true, false},
		{"optional second object", `{} {}`, true, false},
		{"exact size limit", `{}` + strings.Repeat(" ", maxRequestBodyBytes-2), false, true},
		{"size limit after object", `{}` + strings.Repeat(" ", maxRequestBodyBytes-1), false, false},
		{"optional size limit", strings.Repeat(" ", maxRequestBodyBytes+1), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/", strings.NewReader(tc.body))
			var dst struct {
				Value string `json:"value"`
			}
			decode := decodeJSON
			if tc.optional {
				decode = decodeOptionalJSON
			}
			err := decode(r, &dst)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, want %v; err=%v", err == nil, tc.valid, err)
			}
		})
	}
}

type brokenJSONBody struct{ io.Reader }

func (brokenJSONBody) Close() error { return nil }
func (b brokenJSONBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	if err == io.EOF {
		return n, errors.New("body interrupted")
	}
	return n, err
}

func TestDecodeJSONReadsPastFirstValue(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Body = brokenJSONBody{strings.NewReader(`{}`)}
	if err := decodeJSON(r, &struct{}{}); err == nil {
		t.Fatal("read error after JSON was ignored")
	}
}
