// GET /api/v1/projects/{key}/tickets（ApiDesign.md 9.2）。
//
// **バックログ画面（GuiDesign.md 5.4）の唯一のデータ源**であり、Phase 2 の
// カンバン・ガントも同じエンドポイントを読む。同一データの別の描き方であって、
// 別のクエリではない（4.1.1）。
//
// **既定が他の一覧と2か所ちがう**（9.2.1）。
//
//   - per_page の既定が 200（他は 25）。この画面はページャを持たず、
//     フィルタ後の全件を1回で取り切る（9.2.3）
//   - sort の既定が sort_key（他は updated_at）。人が手で並べた順序（9.4）を
//     既定の見え方にする
//
// **ページャは規約どおり返す**（9.2.3）。画面が出さないだけである。total が
// per_page を超えたら、画面が件数とともに「フィルタで絞り込んでください」を出す。
// サーバは 200 件で打ち切るだけでエラーにしない。
package v1

import (
	"fmt"
	"hash/fnv"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// ticketTypeEpic は種別「エピック」（DbDesign.md 6.6）。
//
// **3値のうちこれだけが名前で参照される。** エピックはバックログに行として出さず
// フィルタになるため（GuiDesign.md 5.4）、画面もサーバも「エピックかどうか」を
// 見る箇所がある。story と task は値域の一員としてしか出てこない。
const ticketTypeEpic = "epic"

// チケットの権限キー（DbDesign.md 7.2 の権限カタログ）。
//
// **ルート定義で宣言するものはここに置かない**（routes.go に文字列で並べる方が、
// 眺めたときに必要権限が読める。Design.md 6.4.4）。ここに在るのは
// **本文の内容によって追加で要る権限**で、宣言では表せないものだけである。
const (
	// permTicketAssign は assignee_id を変えるときに追加で要る（ApiDesign.md 9.5.2）。
	//
	// **遷移（9.6）の required_permission はここに置かない。** あちらは
	// workflow_transition の列から読む値であり、プロジェクトごとに変わる。
	permTicketAssign = "ticket.assign"
)

// チケットの値域（ApiDesign.md 9.2.1 / 9.3、DbDesign.md 6.6 の CHECK と同じ）。
var (
	ticketTypes        = []string{"epic", "story", "task"}
	ticketPriorities   = []string{"lowest", "low", "medium", "high", "highest"}
	ticketStatusCatego = []string{"todo", "in_progress", "review", "done"}
)

// ticketSortSpec は 9.2.1 の sort / order / per_page の既定と許可リスト。
var ticketSortSpec = SortSpec{
	Allowed: []string{
		"sort_key", "seq", "title", "status", "priority",
		"due_date", "created_at", "updated_at",
	},
	DefaultSort:    "sort_key",
	DefaultOrder:   OrderAsc,
	DefaultPerPage: 200,
}

// filterAll / filterOpen / filterClosed は open クエリの内部表現（9.2.1）。
const (
	filterAll    = "all"
	filterOpen   = "open"
	filterClosed = "closed"
)

// assigneeMe / filterNone はクエリに書ける特別な値（9.2.1）。
//
// **配列の値としてSQLへ渡さない。** ULID の値域と重ならない保証が無く、
// 混ぜると「none という ID のアクター」と区別できなくなるためである。
const (
	assigneeMe = "me"
	filterNone = "none"
)

// dueWithinPattern は due_within の書式（9.2.1 の「7d 形式」）。
var dueWithinPattern = regexp.MustCompile(`^([0-9]+)d$`)

// ticketFilters は 9.2.1 のクエリパラメータを解析した結果。
type ticketFilters struct {
	statusKeys       []string
	statusCategories []string
	types            []string
	priorities       []string
	assigneeIDs      []string
	assigneeNone     bool
	tagIDs           []string
	tagNone          bool
	sprintIDs        []string
	sprintNone       bool
	openFilter       string
	dueWithinDays    int32
	overdueOnly      bool
	staleDays        int32
	parentSeqs       []int32

	// normalized は ETag の材料（9.2.5）。解析後の値から作るので、
	// 同じ意味の違う書き方（?type=bug,task と ?type=task,bug）が同じ値になる。
	normalized string
}

// listTickets は GET /api/v1/projects/{key}/tickets を処理する。
func (h *handler) listTickets(w http.ResponseWriter, r *http.Request) {
	p, _, projectID, ok := projectScopeContext(w, r, h.q, "GET /projects/{key}/tickets")
	if !ok {
		return
	}

	page, pageErr := ParsePage(r, ticketSortSpec)
	filters, filterErr := parseTicketFilters(r, p)
	// 2.6 の details は「項目ごとの誤り」を並べるものなので、
	// 先に見つかったほうだけを返さず、両方を1つの 422 にまとめる。
	if e := mergeValidationErrors(pageErr, filterErr); e != nil {
		apierr.Write(w, r, e)
		return
	}

	rows, err := h.q.ListTickets(r.Context(), gen.ListTicketsParams{
		ProjectID:        projectID,
		StatusKeys:       filters.statusKeys,
		StatusCategories: filters.statusCategories,
		Types:            filters.types,
		Priorities:       filters.priorities,
		AssigneeIds:      filters.assigneeIDs,
		AssigneeNone:     filters.assigneeNone,
		TagIds:           filters.tagIDs,
		TagNone:          filters.tagNone,
		SprintIds:        filters.sprintIDs,
		SprintNone:       filters.sprintNone,
		OpenFilter:       filters.openFilter,
		DueWithinDays:    filters.dueWithinDays,
		OverdueOnly:      filters.overdueOnly,
		StaleDays:        filters.staleDays,
		ParentSeqs:       filters.parentSeqs,
		Sort:             page.Sort,
		SortOrder:        page.Order,
		PageLimit:        int32(page.Limit()),
		PageOffset:       int32(page.Offset()),
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("チケット一覧を読めない: %w", err)))
		return
	}

	// tags[] は行ごとに引かず1回でまとめる（9.2.2 の要点）。
	ticketIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		ticketIDs = append(ticketIDs, row.ID)
	}
	tagsByTicket, err := ticketTagsFor(r.Context(), h.q, ticketIDs)
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).WithCause(err))
		return
	}

	items := make([]ticketListItem, 0, len(rows))
	var total int64
	var lastUpdated pgtype.Timestamptz
	for _, row := range rows {
		items = append(items, buildTicketListItem(row, tagsByTicket[row.ID]))
		// 窓関数の値はどの行も同じ。0件のときは 0 のままでよい。
		total, lastUpdated = row.Total, row.LastUpdatedAt
	}

	// 差分取得（2.7 / 9.2.5）。Phase 1 では If-None-Match を解釈せず
	// ヘッダだけ出す（5.1 / 6.1 と同じ）。
	w.Header().Set("ETag", ticketsETag(filters.normalized, page, total, lastUpdated))

	WriteJSON(w, http.StatusOK, NewList(items, page, int(total)))
}

// parseTicketFilters は 9.2.1 のクエリを解析する。
//
// **解釈できない値は既定へ丸めず 422**（2.6）。ただし status / assignee / tag /
// sprint の値そのもの（ステータスキー・ULID）は照合しない——存在しない値は
// 何にも当たらないだけであり、他プロジェクトの ID を投げて存在を探れる経路を
// 作らないためでもある（Design.md 6.4.5）。
func parseTicketFilters(r *http.Request, p *auth.Principal) (ticketFilters, *apierr.Error) {
	q := r.URL.Query()
	var details []apierr.Detail
	f := ticketFilters{
		statusKeys:       []string{},
		statusCategories: []string{},
		types:            []string{},
		priorities:       []string{},
		assigneeIDs:      []string{},
		tagIDs:           []string{},
		sprintIDs:        []string{},
		// **nil にしない。** pgx は nil スライスを SQL の NULL として送るため、
		// cardinality(NULL) = NULL となり「指定なし」の判定が偽になって
		// 1件も返らなくなる。他の配列フィルタが splitFilter の空スライスで
		// 埋まっているのと同じ理由である。
		parentSeqs:    []int32{},
		openFilter:    filterAll,
		dueWithinDays: -1,
		// staleDays も「指定なし」を負で表す（dueWithinDays と同じ）。
		// 0 は「0日以上更新なし」＝全件になってしまうため使えない。
		staleDays: -1,
	}
	var parts []string
	add := func(name string, values []string) {
		if len(values) > 0 {
			sorted := slices.Clone(values)
			slices.Sort(sorted)
			parts = append(parts, name+"="+strings.Join(sorted, ","))
		}
	}

	f.statusKeys = splitFilter(q.Get("status"))
	add("status", f.statusKeys)

	f.statusCategories = splitFilter(q.Get("status_category"))
	details = appendEnumErrors(details, "status_category", f.statusCategories, ticketStatusCatego)
	add("status_category", f.statusCategories)

	f.types = splitFilter(q.Get("type"))
	details = appendEnumErrors(details, "type", f.types, ticketTypes)
	add("type", f.types)

	f.priorities = splitFilter(q.Get("priority"))
	details = appendEnumErrors(details, "priority", f.priorities, ticketPriorities)
	add("priority", f.priorities)

	// assignee は me（自分）と none（未割当）を含みうる（9.2.1）。
	for _, v := range splitFilter(q.Get("assignee")) {
		switch v {
		case assigneeMe:
			f.assigneeIDs = append(f.assigneeIDs, p.ActorID)
		case filterNone:
			f.assigneeNone = true
		default:
			f.assigneeIDs = append(f.assigneeIDs, v)
		}
	}
	add("assignee", f.assigneeIDs)
	if f.assigneeNone {
		parts = append(parts, "assignee_none=1")
	}

	for _, v := range splitFilter(q.Get("tag")) {
		if v == filterNone {
			f.tagNone = true
			continue
		}
		f.tagIDs = append(f.tagIDs, v)
	}
	add("tag", f.tagIDs)
	if f.tagNone {
		parts = append(parts, "tag_none=1")
	}

	for _, v := range splitFilter(q.Get("sprint")) {
		if v == filterNone {
			f.sprintNone = true
			continue
		}
		f.sprintIDs = append(f.sprintIDs, v)
	}
	add("sprint", f.sprintIDs)
	if f.sprintNone {
		parts = append(parts, "sprint_none=1")
	}

	switch v := q.Get("open"); v {
	case "":
	case "true":
		f.openFilter = filterOpen
		parts = append(parts, "open=true")
	case "false":
		f.openFilter = filterClosed
		parts = append(parts, "open=false")
	default:
		details = append(details, apierr.Detail{
			Field: "open", Code: "invalid",
			Message: "open は true または false で指定してください",
		})
	}

	if v := q.Get("due_within"); v != "" {
		m := dueWithinPattern.FindStringSubmatch(v)
		if m == nil {
			details = append(details, apierr.Detail{
				Field: "due_within", Code: "invalid",
				Message: "due_within は 7d のように日数で指定してください",
			})
		} else {
			days, err := strconv.Atoi(m[1])
			if err != nil || days > 3650 {
				details = append(details, apierr.Detail{
					Field: "due_within", Code: "out_of_range",
					Message: "due_within は 3650d 以下で指定してください",
				})
			} else {
				f.dueWithinDays = int32(days)
				parts = append(parts, "due_within="+strconv.Itoa(days))
			}
		}
	}

	// overdue（9.2.1。手順19b）。**true 以外は受け付けない**——false は
	// 「期限を過ぎていないもの」ではなく「絞らない」であり、それはキーを
	// 送らないことで表せる。値を2つ持つと同じ意味の書き方が2通りになる。
	switch v := q.Get("overdue"); v {
	case "":
	case "true":
		f.overdueOnly = true
		parts = append(parts, "overdue=true")
	default:
		details = append(details, apierr.Detail{
			Field: "overdue", Code: "invalid",
			Message: "overdue は true で指定してください",
		})
	}

	// stale（9.2.1。手順19b）。書式は due_within と同じ <N>d で、上限も同じ。
	if v := q.Get("stale"); v != "" {
		m := dueWithinPattern.FindStringSubmatch(v)
		if m == nil {
			details = append(details, apierr.Detail{
				Field: "stale", Code: "invalid",
				Message: "stale は 14d のように日数で指定してください",
			})
		} else {
			days, err := strconv.Atoi(m[1])
			if err != nil || days > 3650 {
				details = append(details, apierr.Detail{
					Field: "stale", Code: "out_of_range",
					Message: "stale は 3650d 以下で指定してください",
				})
			} else {
				f.staleDays = int32(days)
				parts = append(parts, "stale="+strconv.Itoa(days))
			}
		}
	}

	// parent は**カンマ区切りで複数指定できる**（9.2.1）。それぞれの部分木の
	// OR になる。バックログのエピックフィルタがこれを使う（GuiDesign.md 5.4）。
	//
	// **正規化では数値として並べ替える。** 他のフィルタは ULID や語なので
	// 文字列の並べ替えでよいが、seq は数なので "10" < "9" になってしまい、
	// ?parent=9,10 と ?parent=10,9 が別の ETag になる（9.2.5）。
	if raw := q.Get("parent"); raw != "" {
		seqs := make([]int32, 0, 4)
		for _, v := range splitFilter(raw) {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				details = append(details, apierr.Detail{
					Field: "parent", Code: "invalid",
					Message: "parent はチケット番号（1以上の整数）で指定してください",
				})
				continue
			}
			if !slices.Contains(seqs, int32(n)) {
				seqs = append(seqs, int32(n))
			}
		}
		if len(seqs) > 0 {
			slices.Sort(seqs)
			f.parentSeqs = seqs
			labels := make([]string, len(seqs))
			for i, n := range seqs {
				labels[i] = strconv.Itoa(int(n))
			}
			parts = append(parts, "parent="+strings.Join(labels, ","))
		}
	}

	if len(details) > 0 {
		return ticketFilters{}, apierr.New(apierr.ValidationFailed).WithDetails(details...)
	}

	slices.Sort(parts)
	f.normalized = strings.Join(parts, "&")
	return f, nil
}

// splitFilter はカンマ区切りの複数指定を分解する（9.2.1）。
// 空要素と前後の空白は落とし、重複も畳む。
func splitFilter(raw string) []string {
	if raw == "" {
		return []string{}
	}
	out := make([]string, 0, 4)
	for _, v := range strings.Split(raw, ",") {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// appendEnumErrors は値域の決まっているフィルタを照合する。
func appendEnumErrors(
	details []apierr.Detail, field string, values, allowed []string,
) []apierr.Detail {
	for _, v := range values {
		if !slices.Contains(allowed, v) {
			details = append(details, apierr.Detail{
				Field: field, Code: "invalid",
				Message: fmt.Sprintf("%s は %s のいずれかで指定してください",
					field, strings.Join(allowed, " / ")),
			})
			return details // 同じ項目で何件も並べない
		}
	}
	return details
}

// ticketsETag は一覧の ETag を作る（ApiDesign.md 9.2.5）。
//
// 「①フィルタ条件を正規化した文字列のハッシュ ②結果の件数 ③結果の
// MAX(updated_at)」から生成する。
//
// **フィルタ条件をハッシュに混ぜるのは本エンドポイント固有である**（9.2.5）。
// 5.1 の /projects と違い条件の組み合わせが多く、「件数と最終更新が同じで
// 内容が違う結果」が現実に起こりうる（?type=bug と ?type=task が偶然どちらも
// 12件で最終更新が同じ、など）。
//
// **sort / order / page / per_page もハッシュに含める。** ETag は応答本文を
// 指す検証子であり（RFC 9110 8.8.1）、並び順やページが違えば本文も違う。
// 9.2.5 の「フィルタ条件」を条件節だけに読むと、2ページ目と1ページ目が
// 同じ ETag になる。
func ticketsETag(normalized string, page Page, total int64, lastUpdated pgtype.Timestamptz) string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s|sort=%s|order=%s|page=%d|per_page=%d",
		normalized, page.Sort, page.Order, page.Page, page.PerPage)

	var stamp int64
	if lastUpdated.Valid {
		stamp = lastUpdated.Time.UTC().UnixNano()
	}
	return fmt.Sprintf(`W/"tkt-%08x-%d-%d"`, h.Sum32(), total, stamp)
}
