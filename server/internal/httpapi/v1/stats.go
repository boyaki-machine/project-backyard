// プロジェクトの集計（ApiDesign.md 9.13.1）。手順19a。
//
// プロジェクトダッシュボード（GuiDesign.md 5.3）の4枚のカードと「要対応」
// ブロックのデータ源である。**必要権限は project.view**（9.13）。
package v1

import (
	"fmt"
	"net/http"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// staleThresholdDays は「放置」と見なす日数（ApiDesign.md 9.13.1）。
//
// **Phase 1 では 14 で固定し、プロジェクトごとの設定にしない。** 5.3 の
// ワイヤーフレームの文言（「14日以上更新されていません」）と一致させたもので、
// 基準を変えたくなるのは運用に載せてからである。**値は応答にも載せて画面へ渡す**
// ——画面が「14日以上」という文言を自分で組み立てると、ここを変えたときに
// 2か所を直すことになる。
const staleThresholdDays = 14

// statsByCategoryView は by_category（9.13.1）。
//
// **map ではなく構造体にしてある。** 9.13.1 は「そのカテゴリのステータスが
// ワークフローに1つも無くても 0 を返す」と定めており（simple テンプレートは
// review を持たない。DbDesign.md 7.4）、**構造体ならその不変条件が型で保たれる。**
// map にすると、集計結果に現れなかったキーを詰め忘れても気づけない。
type statsByCategoryView struct {
	Todo       int64 `json:"todo"`
	InProgress int64 `json:"in_progress"`
	Review     int64 `json:"review"`
	Done       int64 `json:"done"`
}

// statsStaleView は stale（9.13.1）。件数と閾値を組にして返す。
type statsStaleView struct {
	Count         int64 `json:"count"`
	ThresholdDays int32 `json:"threshold_days"`
}

// projectStatsView は 9.13.1 の応答。
//
// **by_category の合計は total と一致しないことがある。** ステータスキーが
// ワークフローに解決できないチケットはどのカテゴリにも数えられないためで、
// クエリ側（stats.sql）に理由がある。
type projectStatsView struct {
	ByCategory statsByCategoryView `json:"by_category"`
	Total      int64               `json:"total"`
	Open       int64               `json:"open"`
	Overdue    int64               `json:"overdue"`
	Stale      statsStaleView      `json:"stale"`
	Unassigned int64               `json:"unassigned"`
}

// ── GET /api/v1/projects/{key}/stats ────────────────────────

// getProjectStats はダッシュボードの集計を返す（9.13.1）。
//
// **ETag（2.7）を付けない。** 2.7 が対象とするのは一覧系 GET で、本エンドポイントは
// ページャを持たない。加えて ETag の材料（件数と MAX(updated_at)）を採る走査が
// 本体の集計とほぼ同じなので、付けても DB の仕事は減らない。
func (h *handler) getProjectStats(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/stats")
	if !ok {
		return
	}

	row, err := h.q.GetProjectTicketStats(r.Context(), gen.GetProjectTicketStatsParams{
		ProjectID: projectID,
		StaleDays: staleThresholdDays,
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("プロジェクトの集計を読めない: %w", err)))
		return
	}

	WriteJSON(w, http.StatusOK, projectStatsView{
		ByCategory: statsByCategoryView{
			Todo:       row.Todo,
			InProgress: row.InProgress,
			Review:     row.Review,
			Done:       row.Done,
		},
		Total:      row.Total,
		Open:       row.OpenCount,
		Overdue:    row.Overdue,
		Stale:      statsStaleView{Count: row.Stale, ThresholdDays: staleThresholdDays},
		Unassigned: row.Unassigned,
	})
}
