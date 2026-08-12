// Package v1 は /api/v1 のエンドポイント実装と、その共通の約束事を持つ
// （Design.md 4.1 の httpapi/v1/、ApiDesign.md 2章）。
//
// ページネーション（2.6）と一覧エンベロープは /api/v1 の規約であるため、
// バージョンの外にある /healthcheck（2.11）とは分けて本パッケージに置く。
package v1

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
)

// ページネーションの既定値と上限（ApiDesign.md 2.6）。
const (
	DefaultPage    = 1
	DefaultPerPage = 25
	MaxPerPage     = 200
)

// 並び順（ApiDesign.md 2.6 の order）。
const (
	OrderAsc  = "asc"
	OrderDesc = "desc"
)

// SortSpec はエンドポイントごとに許可するソートの定義。
//
// ApiDesign.md 2.6 が「許可する項目はエンドポイントごとに列挙」と定めるため、
// 許可リストは共通側に持たず、呼び出し側から受け取る。
type SortSpec struct {
	Allowed      []string // 受け付ける sort の値
	DefaultSort  string   // sort 未指定時に使う値
	DefaultOrder string   // order 未指定時に使う値。空なら desc
}

// Page は解析済みのページネーション指定。
type Page struct {
	Page    int
	PerPage int
	Sort    string
	Order   string
}

// Limit は SQL の LIMIT に渡す値。
func (p Page) Limit() int { return p.PerPage }

// Offset は SQL の OFFSET に渡す値。
func (p Page) Offset() int { return (p.Page - 1) * p.PerPage }

// List は一覧応答の共通エンベロープ（ApiDesign.md 2.6）。
type List[T any] struct {
	Items      []T `json:"items"`
	Page       int `json:"page"`
	PerPage    int `json:"per_page"`
	Total      int `json:"total"`
	TotalPages int `json:"total_pages"`
}

// NewList は items と総件数からエンベロープを組み立てる。
//
// total_pages は切り上げ。総件数0のときは0ページとする。
// items が nil の場合は空スライスに置き換え、JSON が null にならないようにする。
func NewList[T any](items []T, p Page, total int) List[T] {
	if items == nil {
		items = []T{}
	}
	totalPages := 0
	if total > 0 && p.PerPage > 0 {
		totalPages = (total + p.PerPage - 1) / p.PerPage
	}
	return List[T]{
		Items:      items,
		Page:       p.Page,
		PerPage:    p.PerPage,
		Total:      total,
		TotalPages: totalPages,
	}
}

// ParsePage はクエリ文字列から page / per_page / sort / order を解析する。
//
// 不正な値は既定値へ丸めず 422 validation_failed を返す（ApiDesign.md 2.5.1）。
// 黙って別の値に読み替えると、呼び出し側の誤りが表に出ないため。
// 未指定の項目のみ既定値を使う。
func ParsePage(r *http.Request, spec SortSpec) (Page, *apierr.Error) {
	q := r.URL.Query()
	var details []apierr.Detail

	page := DefaultPage
	if v := q.Get("page"); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			details = append(details, apierr.Detail{
				Field: "page", Code: "invalid", Message: "page は整数で指定してください",
			})
		case n < 1:
			details = append(details, apierr.Detail{
				Field: "page", Code: "out_of_range", Message: "page は1以上で指定してください",
			})
		default:
			page = n
		}
	}

	perPage := DefaultPerPage
	if v := q.Get("per_page"); v != "" {
		n, err := strconv.Atoi(v)
		switch {
		case err != nil:
			details = append(details, apierr.Detail{
				Field: "per_page", Code: "invalid", Message: "per_page は整数で指定してください",
			})
		case n < 1:
			details = append(details, apierr.Detail{
				Field: "per_page", Code: "out_of_range", Message: "per_page は1以上で指定してください",
			})
		case n > MaxPerPage:
			details = append(details, apierr.Detail{
				Field: "per_page", Code: "out_of_range",
				Message: "per_page は" + strconv.Itoa(MaxPerPage) + "以下で指定してください",
			})
		default:
			perPage = n
		}
	}

	sort := spec.DefaultSort
	if v := q.Get("sort"); v != "" {
		if slices.Contains(spec.Allowed, v) {
			sort = v
		} else {
			details = append(details, apierr.Detail{
				Field: "sort", Code: "invalid", Message: "sort に指定できない項目です",
			})
		}
	}

	order := spec.DefaultOrder
	if order == "" {
		order = OrderDesc
	}
	if v := q.Get("order"); v != "" {
		if v == OrderAsc || v == OrderDesc {
			order = v
		} else {
			details = append(details, apierr.Detail{
				Field: "order", Code: "invalid", Message: "order は asc または desc で指定してください",
			})
		}
	}

	if len(details) > 0 {
		return Page{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}
	return Page{Page: page, PerPage: perPage, Sort: sort, Order: order}, nil
}

// WriteJSON は成功応答を書く（ApiDesign.md 2.2）。
func WriteJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// 書き込み失敗（クライアント切断など）はこの時点で回復手段がない。
	_ = json.NewEncoder(w).Encode(body)
}
