// プロジェクトAPI（ApiDesign.md 5章）のうち、一覧とキーの重複確認。
//
//	GET /api/v1/projects            5.1
//	GET /api/v1/projects/check-key  5.2
//
// 作成（5.3）は projects_create.go にある。
package v1

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// status の値（ApiDesign.md 5.1。project.status の CHECK 制約は
// DbDesign.md 6.4 の active / archived で、all は絞り込みをしないことを表す）。
const (
	projectStatusActive   = "active"
	projectStatusArchived = "archived"
	projectStatusAll      = "all"
)

// projectSortSpec は 5.1 が許可するソート項目と既定値。
//
// 許可リストをここに置くのは、ApiDesign.md 2.6 が「許可する項目は
// エンドポイントごとに列挙」と定めているためである。ListProjects の
// ORDER BY はこの5つと一対一で対応する。
var projectSortSpec = SortSpec{
	Allowed:      []string{"name", "key", "updated_at", "ticket_count", "progress"},
	DefaultSort:  "updated_at",
	DefaultOrder: OrderDesc,
}

// projectKeyPattern はプロジェクトキーの形式（ApiDesign.md 5.3）。
// DbDesign.md 6.4 の CHECK 制約と同じ正規表現である。**片方だけ変えない。**
var projectKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,19}$`)

// reservedProjectKeys は予約語（ApiDesign.md 5.2）。
// ルーティングと衝突するため、プロジェクトキーとして使わせない。
//
// **check-key を含めるのは /api/v1/projects/ 直下の兄弟パスだから**である
// （手順11で追加）。chi は静的ルートを {key} より優先するため、check-key という
// キーのプロジェクトを作れてしまうと GET /projects/check-key（5.4）が
// 本エンドポイントに吸われ、そのプロジェクトへ到達できなくなる。
var reservedProjectKeys = map[string]bool{
	"admin": true, "api": true, "mcp": true, "login": true, "logout": true,
	"me": true, "p": true, "new": true, "projects": true, "static": true,
	"assets": true, "check-key": true,
}

// projectListItem は 5.1 の items[] 要素。
type projectListItem struct {
	ID          string  `json:"id"`
	Key         string  `json:"key"`
	Name        string  `json:"name"`
	Description *string `json:"description"`
	Status      string  `json:"status"`
	TicketCount int64   `json:"ticket_count"`
	ClosedCount int64   `json:"closed_count"`
	Progress    float64 `json:"progress"`
	// MyRole は当該プロジェクトでのプロジェクトロール。
	//
	// **メンバーでないアドミニストレータでは null になる。** 5.1 の
	// 「管理者は全件」で返る行がこれにあたり、役割としては何も持たない。
	// ApiDesign.md 6.1 の「意味を持たないフィールドは null を返し、
	// フィールド自体を省略しない」に従う。
	MyRole    *string `json:"my_role"`
	UpdatedAt Time    `json:"updated_at"`
}

// checkKeyView は 5.2 の応答。
//
// reason は available が false のときだけ現れる（5.2 の例のとおり）。
type checkKeyView struct {
	Key       string `json:"key"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

// check-key が返す理由（ApiDesign.md 5.2）。
const (
	keyReasonReserved      = "reserved"
	keyReasonAlreadyExists = "already_exists"
	keyReasonInvalidFormat = "invalid_format"
)

// listProjects は GET /api/v1/projects を処理する（ApiDesign.md 5.1）。
//
// 可視範囲の判定は「メンバーであるか、アドミニストレータであるか」であり、
// project.view の有無では切らない。オペレータもシステムロールとして
// project.view を持つため、それで切ると全件が見えてしまう（手順6a の判断。
// 5.1 と 5.4 の読み合わせ）。project.view の要求はルート定義側で行う。
func (h *handler) listProjects(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFromContext(r.Context())
	if p == nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("GET /projects が認証ミドルウェアを通っていない")))
		return
	}

	page, pageErr := ParsePage(r, projectSortSpec)
	status, statusErr := parseProjectStatusFilter(r)
	// 2.6 の details は「項目ごとの誤り」を並べるものなので、
	// 先に見つかったほうだけを返さず、両方を1つの 422 にまとめる。
	if e := mergeValidationErrors(pageErr, statusErr); e != nil {
		apierr.Write(w, r, e)
		return
	}

	admin := p.IsAdministrator()

	summary, err := h.q.SummarizeProjects(r.Context(), gen.SummarizeProjectsParams{
		ActorID:         p.ActorID,
		IsAdministrator: admin,
		StatusFilter:    status,
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("プロジェクトの総件数を読めない: %w", err)))
		return
	}

	rows, err := h.q.ListProjects(r.Context(), gen.ListProjectsParams{
		ActorID:         p.ActorID,
		IsAdministrator: admin,
		StatusFilter:    status,
		Sort:            page.Sort,
		SortOrder:       page.Order,
		PageLimit:       int32(page.Limit()),
		PageOffset:      int32(page.Offset()),
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("プロジェクト一覧を読めない: %w", err)))
		return
	}

	items := make([]projectListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, projectListItem{
			ID:          row.ID,
			Key:         row.Key,
			Name:        row.Name,
			Description: textPtr(row.Description),
			Status:      row.Status,
			TicketCount: row.TicketCount,
			ClosedCount: row.ClosedCount,
			Progress:    row.Progress,
			MyRole:      textPtr(row.MyRole),
			UpdatedAt:   Time(row.UpdatedAt.Time),
		})
	}

	// 差分取得（ApiDesign.md 2.7）。Phase 1 ではポーリングを実装しないため
	// If-None-Match は解釈せず、ヘッダだけ先に出す。後から全一覧エンドポイントを
	// 改修せずに済むようにするための先行実装である。
	w.Header().Set("ETag", projectsETag(summary.Total, summary.LastUpdatedAt))

	WriteJSON(w, http.StatusOK, NewList(items, page, int(summary.Total)))
}

// checkProjectKey は GET /api/v1/projects/check-key を処理する（ApiDesign.md 5.2）。
//
// 新規作成モーダルの即時検証用であり、**この結果は作成時の重複検出には使わない。**
// 5.3 が「競合検出はDBの UNIQUE 制約に委ね、check-key の結果を信頼しない」と
// 定めている（確認から作成までの間に他者が同じキーを取りうるため）。
func (h *handler) checkProjectKey(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "key", Code: "required", Message: "プロジェクトキーを指定してください",
		}))
		return
	}

	// 判定の順は 形式 → 予約語 → 既存。形式が不正なキーはそもそも
	// 保存できないため、DBを引く前に落とす。
	switch {
	case !projectKeyPattern.MatchString(key):
		WriteJSON(w, http.StatusOK, checkKeyView{Key: key, Reason: keyReasonInvalidFormat})
		return
	case reservedProjectKeys[key]:
		WriteJSON(w, http.StatusOK, checkKeyView{Key: key, Reason: keyReasonReserved})
		return
	}

	exists, err := h.q.ProjectKeyExists(r.Context(), key)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("プロジェクトキー %q の重複を確認できない: %w", key, err)))
		return
	}
	if exists {
		WriteJSON(w, http.StatusOK, checkKeyView{Key: key, Reason: keyReasonAlreadyExists})
		return
	}
	WriteJSON(w, http.StatusOK, checkKeyView{Key: key, Available: true})
}

// parseProjectStatusFilter は status クエリを解析する（ApiDesign.md 5.1）。
//
// 未指定は active。解釈できない値は 2.6 の方針どおり既定へ丸めず 422 にする。
func parseProjectStatusFilter(r *http.Request) (string, *apierr.Error) {
	v := r.URL.Query().Get("status")
	switch v {
	case "":
		return projectStatusActive, nil
	case projectStatusActive, projectStatusArchived, projectStatusAll:
		return v, nil
	default:
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "status", Code: "invalid",
			Message: "status は active / archived / all のいずれかで指定してください",
		})
	}
}

// mergeValidationErrors は複数の 422 を1つに畳む。すべて nil なら nil。
//
// フォームの入力欄へ紐づけられるよう details を失わないこと（ApiDesign.md 2.5）。
func mergeValidationErrors(errs ...*apierr.Error) *apierr.Error {
	var merged *apierr.Error
	for _, e := range errs {
		if e == nil {
			continue
		}
		if merged == nil {
			merged = e
			continue
		}
		merged = merged.WithDetails(e.Details...)
	}
	return merged
}

// projectsETag は一覧の ETag を作る（ApiDesign.md 2.7）。
//
// 「プロジェクト集合の MAX(updated_at) と件数から生成する」に従う。件数は
// 呼び出した利用者に見える集合のものなので、同じURLでも利用者ごとに変わる。
//
// **弱い検証子として W/"..." の形で書く**（RFC 9110 8.8.3）。2.7 の例は
// 当初 `"W/proj-…"` と引用符の内側に W/ を置いていたが、それでは弱い ETag に
// ならない（値そのものが `W/proj-…` という文字列になる）ため、手順11で
// 文書側の例を実装に合わせて直した。
//
// 秒ではなくナノ秒まで含める。同一秒内の更新で値が変わらないと、
// 変わっていない一覧を「変わっていない」と誤って扱えてしまうため。
func projectsETag(total int64, lastUpdated pgtype.Timestamptz) string {
	var stamp int64
	if lastUpdated.Valid {
		stamp = lastUpdated.Time.UTC().UnixNano()
	}
	return fmt.Sprintf(`W/"proj-%d-%d"`, total, stamp)
}

// textPtr は NULL 可能なテキスト列を JSON の string / null に写す。
//
// nullable（空文字を null にする）とは別物である。列が空文字を保持できる場合に
// それを null へ潰すと、DBの状態と応答が食い違う。
func textPtr(t pgtype.Text) *string {
	if !t.Valid {
		return nil
	}
	s := strings.Clone(t.String)
	return &s
}
