// 業務履歴の読み出し（ApiDesign.md 9.13.2）。手順19a。
//
// 消費者は2つとも「全件を時系列で読む」——ダッシュボードの「最近の動き」
// （GuiDesign.md 5.3）と、チケット詳細の「履歴」セクション（同 5.5）である。
// 後者は ?entity=ticket:<seq> を付けるだけで、応答の形は変わらない。
//
// **audit_log（2.10）とは読み手が違う**（9.1.1）。あちらはインスタンス管理者が
// 認証・権限・トークンを追うためのもので、ここはプロジェクトのメンバーが
// チケットの変更を読むためのものである。**必要権限は project.view**（9.13）。
package v1

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// activitySort は 9.13.2 の並び順。
//
// **Allowed が空なのは意図した状態である。** 並び順は occurred_at DESC, id DESC
// で固定されており（9.13.2）、sort に何を書いても受け付けない。ParsePage は
// Allowed に無い値を 422 にするので、この宣言だけで「sort は指定できない」が
// 表現できる。
var activitySort = SortSpec{
	DefaultPerPage: 20,
}

// activityActions は action パラメータの値域（9.13.2、DbDesign.md 6.8 の CHECK）。
var activityActions = map[string]bool{
	"create": true, "update": true, "delete": true, "transition": true,
}

// entityTicketPrefix は entity パラメータの唯一の形（9.13.2）。
//
// entity_type はチケットだけである（9.1.1）。タグ・スプリントの
// 定義変更はどちらにも記録しておらず、読む画面も無い。
const entityTicketPrefix = "ticket:"

// activityActorView は actor（9.13.2）。
//
// **null になりうる。** activity.actor_id は ON DELETE SET NULL であり
// （DbDesign.md 6.8）、ユーザーを消した後も履歴の行は残る。9.2.2 の
// assignee / reporter と同じ扱いで、画面が「削除されたユーザー」と表示する。
type activityActorView struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	DisplayName string `json:"display_name"`
}

// activityView は 9.13.2 の items[] の1件。
//
// **entity_seq / entity_title は null になりうる**（9.13.2）。DELETE は物理削除
// なので（9.5.3）、消えたチケットを指す行はチケット表と結合できない。
//
// **old_value / new_value は text のまま返す**（9.13.2）。ステータスの表示名への
// 変換は画面が行う。**assignee_id / sprint_id の値は ULID がそのまま入る**ので、
// それをどう出すかは GuiDesign.md 5.3 / 5.5 の側の判断になる。
type activityView struct {
	ID          string             `json:"id"`
	EntityType  string             `json:"entity_type"`
	EntityID    string             `json:"entity_id"`
	EntitySeq   *int32             `json:"entity_seq"`
	EntityTitle *string            `json:"entity_title"`
	Actor       *activityActorView `json:"actor"`
	Action      string             `json:"action"`
	Field       *string            `json:"field"`
	OldValue    *string            `json:"old_value"`
	NewValue    *string            `json:"new_value"`
	OccurredAt  Time               `json:"occurred_at"`
}

// activityFilters は解析済みのクエリ。
//
// entityID / action は「空文字＝絞らない」で SQL へ渡す。normalized は ETag の
// 材料で、9.2.5 と同じく**解析後の値から作る**——?action=update と ?action=UPDATE
// のような表記ゆれを ETag に持ち込まないためである。
type activityFilters struct {
	entityID   string
	action     string
	normalized string

	// entityMissing は「entity で指されたチケットが存在しない」。
	//
	// **entityID を空文字のままにしてはならない**ので、別のフラグで持つ
	// （空文字は「絞らない」を意味し、全件が返ってしまう）。9.13.2 は
	// この場合を空の一覧で返すと定めている。
	entityMissing bool
}

// ── GET /api/v1/projects/{key}/activity ─────────────────────

// listProjectActivity は業務履歴を新しい順に返す（9.13.2）。
func (h *handler) listProjectActivity(w http.ResponseWriter, r *http.Request) {
	_, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/activity")
	if !ok {
		return
	}

	page, apiErr := ParsePage(r, activitySort)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}
	// **order は並び順が固定である以上、指定そのものを受け付けない。**
	// ParsePage は order を asc/desc の形だけで見て通してしまうため、ここで
	// 弾く。9.5.2 が「受け付けない項目は、そこにキーが在るだけで 422 にする」と
	// する扱いに揃えた——黙って無視すると「指定すれば効くことがある」と読める。
	if r.URL.Query().Get("order") != "" {
		apierr.Write(w, r, apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "order", Code: "invalid",
			Message: "履歴の並び順は変更できません",
		}))
		return
	}

	ctx := r.Context()
	filters, apiErr := h.parseActivityFilters(ctx, r, projectID)
	if apiErr != nil {
		apierr.Write(w, r, apiErr)
		return
	}

	var (
		items          = make([]activityView, 0, page.PerPage)
		total          int64
		lastOccurredAt time.Time
	)

	// 指定されたチケットが無いときは DB を引かない（9.13.2）。空文字を渡すと
	// 「絞らない」の意味になり、全件が返ってしまう。
	if !filters.entityMissing {
		rows, err := h.q.ListActivity(ctx, gen.ListActivityParams{
			ProjectID:    projectID,
			EntityID:     filters.entityID,
			ActionFilter: filters.action,
			PageLimit:    int32(page.Limit()),
			PageOffset:   int32(page.Offset()),
		})
		if err != nil {
			apierr.Write(w, r, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("業務履歴を読めない: %w", err)))
			return
		}
		for _, row := range rows {
			items = append(items, buildActivityView(row))
			total = row.Total
			lastOccurredAt = row.LastOccurredAt
		}

		// **1件も返らなかったときは別クエリで数える。** ウィンドウ関数は行が
		// 無いと1行も返らないので、total と ETag の材料がここからは採れない
		// （履歴の無いチケット、および範囲外のページ）。
		if len(rows) == 0 {
			sum, err := h.q.SummarizeActivity(ctx, gen.SummarizeActivityParams{
				ProjectID:    projectID,
				EntityID:     filters.entityID,
				ActionFilter: filters.action,
			})
			if err != nil {
				apierr.Write(w, r, apierr.New(apierr.InternalError).
					WithCause(fmt.Errorf("業務履歴の件数を読めない: %w", err)))
				return
			}
			total, lastOccurredAt = sum.Total, sum.LastOccurredAt
		}
	}

	// 差分取得（2.7 / 9.13.2）。If-None-Match は解釈せず
	// ヘッダだけ出す（9.2.5 と同じ）。
	w.Header().Set("ETag", activityETag(filters.normalized, page, total, lastOccurredAt))

	WriteJSON(w, http.StatusOK, NewList(items, page, int(total)))
}

// parseActivityFilters は entity / action を解析する（9.13.2）。
//
// **entity は資源の指定ではなくフィルタである。** 書式が違えば 422 だが、
// 形が正しくてチケットが無いときは 404 にせず空の一覧を返す（9.2.1 の
// assignee や tag に存在しない ULID を渡したときと同じ扱い）。
func (h *handler) parseActivityFilters(
	ctx context.Context, r *http.Request, projectID string,
) (activityFilters, *apierr.Error) {
	q := r.URL.Query()
	var (
		f       activityFilters
		details []apierr.Detail
		parts   []string
	)

	if v := q.Get("action"); v != "" {
		// **カンマ区切りの OR を受け付けない**（9.13.2）。9.2.1 のフィルタ群と
		// 違う扱いだが、複数選択を要する画面が無い。
		if activityActions[v] {
			f.action = v
			parts = append(parts, "action="+v)
		} else {
			details = append(details, apierr.Detail{
				Field: "action", Code: "invalid",
				Message: "action は create / update / delete / transition のいずれかで指定してください",
			})
		}
	}

	var seq int32
	hasEntity := false
	if v := q.Get("entity"); v != "" {
		n, err := parseEntityTicketSeq(v)
		if err != nil {
			details = append(details, apierr.Detail{
				Field: "entity", Code: "invalid",
				Message: "entity は ticket:31 の形で指定してください",
			})
		} else {
			seq, hasEntity = n, true
			parts = append(parts, "entity=ticket:"+strconv.Itoa(int(n)))
		}
	}

	// **書式の誤りを先に全部返す。** チケットを引きに行くのは形が正しいと
	// 分かってからで、422 になるリクエストで DB を触らない。
	if len(details) > 0 {
		return activityFilters{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}

	if hasEntity {
		id, err := h.q.FindTicketIDBySeq(ctx, gen.FindTicketIDBySeqParams{
			ProjectID: projectID, Seq: seq,
		})
		switch {
		case err == nil:
			f.entityID = id
		case errors.Is(err, pgx.ErrNoRows):
			// 9.13.2：存在しない seq は空の一覧（404 にしない）。
			f.entityMissing = true
		default:
			return activityFilters{}, apierr.New(apierr.InternalError).
				WithCause(fmt.Errorf("チケット %d を読めない: %w", seq, err))
		}
	}

	f.normalized = strings.Join(parts, "&")
	return f, nil
}

// parseEntityTicketSeq は `ticket:31` から 31 を取り出す（9.13.2）。
//
// **受け付ける entity_type はチケットだけである**（9.1.1）。
// 接頭辞が違うもの、seq が整数でないもの、区切りを欠くものはすべて誤りとする。
func parseEntityTicketSeq(v string) (int32, error) {
	rest, ok := strings.CutPrefix(v, entityTicketPrefix)
	if !ok {
		return 0, fmt.Errorf("entity %q は ticket: で始まらない", v)
	}
	n, err := strconv.Atoi(rest)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("entity %q の seq が正の整数でない", v)
	}
	return int32(n), nil
}

// buildActivityView は1行を 9.13.2 の形へ写す。
func buildActivityView(row gen.ListActivityRow) activityView {
	v := activityView{
		ID:         row.ID,
		EntityType: row.EntityType,
		EntityID:   row.EntityID,
		Action:     row.Action,
		Field:      textPtr(row.Field),
		OldValue:   textPtr(row.OldValue),
		NewValue:   textPtr(row.NewValue),
		OccurredAt: Time(row.OccurredAt),
	}
	if row.EntitySeq.Valid {
		seq := row.EntitySeq.Int32
		v.EntitySeq = &seq
	}
	v.EntityTitle = textPtr(row.EntityTitle)
	if row.ActorID.Valid {
		v.Actor = &activityActorView{
			ID:          row.ActorID.String,
			Kind:        row.ActorKind.String,
			DisplayName: row.ActorDisplayName.String,
		}
	}
	return v
}

// activityETag は一覧の ETag（2.7 / 9.13.2）。
//
// 材料は 9.2.5 と同じ組み立てで、①フィルタ条件を正規化した文字列のハッシュ
// ②件数 ③結果の MAX(occurred_at) である。**page / per_page もハッシュに含める**
// ——ETag は応答本文を指す検証子であり（RFC 9110 8.8.1）、2ページ目と1ページ目が
// 同じ値になってはならない。**sort / order は含めない**（並び順が固定であり、
// 指定そのものを 422 で弾いている）。
func activityETag(normalized string, page Page, total int64, last time.Time) string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s|page=%d|per_page=%d", normalized, page.Page, page.PerPage)

	stamp := etagStamp(last)
	return fmt.Sprintf(`W/"act-%08x-%d-%d"`, h.Sum32(), total, stamp)
}
