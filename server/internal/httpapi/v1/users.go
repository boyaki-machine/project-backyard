// ユーザー管理API（ApiDesign.md 6章）のうち、一覧。
//
//	GET /api/v1/admin/users  6.1
//
// 作成（6.2）は users_create.go にある。詳細・更新・削除（6.3〜6.7）は手順13。
//
// **すべてアドミニストレータ専用**（user.manage）。権限の宣言は routes.go にある。
package v1

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/apierr"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
)

// kind の値（ApiDesign.md 6.1）。actor.kind の CHECK 制約（DbDesign.md 6.2）の
// うち user / agent を受け、all は絞り込みをしないことを表す。
// system は 6.1 の列挙に無いため受け付けない（クエリ側でも常に除外している）。
const (
	userKindUser  = "user"
	userKindAgent = "agent"
	userKindAll   = "all"
)

// is_active の値（ApiDesign.md 6.1）。true / false / all の3値で、
// 真偽値ではなく文字列として扱う。「未指定」と「false」を区別するためである。
const (
	userActiveTrue  = "true"
	userActiveFalse = "false"
	userActiveAll   = "all"
)

// userSortSpec は 6.1 が許可するソート項目と既定値。
//
// **既定を display_name の昇順にする。** 6.1 は sort の既定を display_name と
// 定めるが order には触れていない。名簿は昇順で読むものであり、GuiDesign.md
// 5.6 の作例も名前順に並んでいる（手順12a の判断。6.1 へ order 行の追加を提案済み）。
//
// system_role と is_active は手順12c で足した（GuiDesign.md 5.6 の全列で
// 並べ替えたいという要望）。**並びの意味づけは SQL 側のコメントにある**
// （system_role は role.sort_order 順・エージェントは末尾、is_active は無効が先）。
var userSortSpec = SortSpec{
	Allowed: []string{
		"display_name", "email", "system_role", "is_active", "last_login_at", "created_at",
	},
	DefaultSort:  "display_name",
	DefaultOrder: OrderAsc,
}

// userListItem は 6.1 の items[] 要素。
//
// **kind によって意味を持たないフィールドは null を返し、フィールド自体を
// 省略しない**（6.1）。フロントの分岐を単純にするためである。したがって
// email / system_role / last_login_at / agent はいずれもポインタで持つ。
type userListItem struct {
	ID           string     `json:"id"`
	Kind         string     `json:"kind"`
	DisplayName  string     `json:"display_name"`
	Email        *string    `json:"email"`
	SystemRole   *string    `json:"system_role"`
	Agent        *agentView `json:"agent"`
	IsActive     bool       `json:"is_active"`
	LastLoginAt  *Time      `json:"last_login_at"`
	ProjectCount int64      `json:"project_count"`
	CreatedAt    Time       `json:"created_at"`
}

// agentView は 6.1 の agent オブジェクト。
//
// **kind='agent' の行にだけ入る**（人間の行では null）。中身は
// DbDesign.md 8.2.1 の agent テーブルの列である。0019 まで常に null だった
// ——テーブルが無く、埋められる値が1つも無かったためである。
//
// **owner は「このエージェントが誰に付いているか」**（agent.owner_actor_id）。
// エージェントの実効権限はこの人から導かれる（Design.md 6.5 の委譲）ため、
// 管理者が一覧で最初に見るべき値である。**登録した人ではなく、権限の根拠。**
//
// **me_agents.go の myAgentView とは別物である。** あちらは本人の一覧で、
// エージェント1件を単独で表す（名前・状態・トークンを持つ）。こちらは
// userListItem の一部として、人間の行と同じ表に並ぶ前提の形である。
type agentView struct {
	ClientKind string    `json:"client_kind"`
	ModelName  string    `json:"model_name"`
	ProjectKey string    `json:"project_key"`
	TrustLevel int32     `json:"trust_level"`
	Owner      *ownerRef `json:"owner"`
}

// ownerRef はエージェントの所有者（6.1 の agent.owner）。
//
// **ticket_view.go の actorRef を使わない。** あちらは kind を持つが、
// 所有者は agent.owner_actor_id が app_user を参照する以上**必ず人間**であり、
// kind は常に "user" になって何も伝えない（DbDesign.md 8.2.1）。
type ownerRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
}

// listUsers は GET /api/v1/admin/users を処理する（ApiDesign.md 6.1）。
func (h *handler) listUsers(w http.ResponseWriter, r *http.Request) {
	page, pageErr := ParsePage(r, userSortSpec)
	kind, kindErr := parseUserKindFilter(r)
	active, activeErr := parseUserActiveFilter(r)
	if err := mergeValidationErrors(pageErr, kindErr, activeErr); err != nil {
		apierr.Write(w, r, err)
		return
	}
	pattern := likePattern(r.URL.Query().Get("q"))

	ctx := r.Context()
	rows, err := h.q.ListAdminUsers(ctx, gen.ListAdminUsersParams{
		KindFilter:   kind,
		ActiveFilter: active,
		QPattern:     pattern,
		Sort:         page.Sort,
		SortOrder:    page.Order,
		PageLimit:    int32(page.Limit()),
		PageOffset:   int32(page.Offset()),
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("ユーザー一覧を取得できない: %w", err)))
		return
	}

	// **総件数は一覧と同じ絞り込みで別に数える**（ApiDesign.md 2.6）。
	// ページ内の件数から推測できないため。
	summary, err := h.q.SummarizeAdminUsers(ctx, gen.SummarizeAdminUsersParams{
		KindFilter:   kind,
		ActiveFilter: active,
		QPattern:     pattern,
	})
	if err != nil {
		apierr.Write(w, r, apierr.New(apierr.InternalError).
			WithCause(fmt.Errorf("ユーザーの総件数を取得できない: %w", err)))
		return
	}

	items := make([]userListItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, userListItem{
			ID:           row.ID,
			Kind:         row.Kind,
			DisplayName:  row.DisplayName,
			Email:        textPtr(row.Email),
			SystemRole:   textPtr(row.SystemRole),
			Agent:        adminAgentView(row),
			IsActive:     row.IsActive,
			LastLoginAt:  apiTimestamptz(row.LastLoginAt),
			ProjectCount: row.ProjectCount,
			CreatedAt:    Time(row.CreatedAt.Time),
		})
	}

	// 差分取得（ApiDesign.md 2.7）。projects と同じく、
	// If-None-Match は解釈せずヘッダだけ先に出す。
	w.Header().Set("ETag", usersETag(summary.Total, summary.LastUpdatedAt))

	WriteJSON(w, http.StatusOK, NewList(items, page, int(summary.Total)))
}

// parseUserKindFilter は kind クエリを解析する（ApiDesign.md 6.1）。
//
// 未指定は all。解釈できない値は 2.6 の方針どおり既定へ丸めず 422 にする。
func parseUserKindFilter(r *http.Request) (string, *apierr.Error) {
	switch v := r.URL.Query().Get("kind"); v {
	case "":
		return userKindAll, nil
	case userKindUser, userKindAgent, userKindAll:
		return v, nil
	default:
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "kind", Code: "invalid",
			Message: "kind は user / agent / all のいずれかで指定してください",
		})
	}
}

// parseUserActiveFilter は is_active クエリを解析する（ApiDesign.md 6.1）。
//
// **真偽値としてではなく3値の文字列として受ける。** 未指定（all）と false を
// 区別する必要があり、Go の bool へ素直に写せないためである。
func parseUserActiveFilter(r *http.Request) (string, *apierr.Error) {
	switch v := r.URL.Query().Get("is_active"); v {
	case "":
		return userActiveAll, nil
	case userActiveTrue, userActiveFalse, userActiveAll:
		return v, nil
	default:
		return "", apierr.New(apierr.ValidationFailed).WithDetails(apierr.Detail{
			Field: "is_active", Code: "invalid",
			Message: "is_active は true / false / all のいずれかで指定してください",
		})
	}
}

// likePattern は部分一致検索の q を ILIKE のパターンへ変える（ApiDesign.md 6.1）。
//
// 当てる先は表示名・メール・**ロールの表示名**（role.display_name）である。
// ロールを含めたのは手順12c で、画面に出ている文字列で探せることが目的
// （「アドミニストレータ」で絞れる）。**画面に出ないキー（administrator）は
// 対象にしない。** どの列に当てるかは SQL 側（ListAdminUsers）が持つ。
//
// **メタ文字をここでエスケープする。** `%` や `_` をそのまま通すと、利用者の
// 入力がワイルドカードとして働き、意図しない行が当たる（`_` は1文字の
// ワイルドカードなので、`a_b` の検索が `axb` にも当たってしまう）。
// **strings.Replace を3回つなげず NewReplacer を使う。** つなげると、
// `\` を `\\` にした後の回で `%` を `\%` に変え、さらに次の回がその `\` を
// 拾って二重にエスケープしうる。NewReplacer は1度の走査で置換するため、
// 置換後の文字列を読み直さない。
//
// 空文字は「絞り込まない」を表す。クエリ側が `@q_pattern = ”` で分岐する。
func likePattern(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(q) + "%"
}

// usersETag は一覧の ETag を作る（ApiDesign.md 2.7）。
//
// projectsETag と同じ作りで、接頭辞だけが違う。**弱い検証子として W/"..." の
// 形で書く**（RFC 9110 8.8.3）。材料は総件数と MAX(updated_at) で、後者は
// actor と app_user の新しいほうを採っている（SummarizeAdminUsers のコメント）。
func usersETag(total int64, lastUpdated pgtype.Timestamptz) string {
	var stamp int64
	if lastUpdated.Valid {
		stamp = lastUpdated.Time.UTC().UnixNano()
	}
	return fmt.Sprintf(`W/"user-%d-%d"`, total, stamp)
}

// adminAgentView は一覧の1行から 6.1 の agent オブジェクトを組み立てる。
//
// **client_kind の有無で判定する。** agent 表は kind='agent' の actor にしか
// 行を持たないので、この列が NULL なら人間である。kind の文字列で分岐せず
// 結合の結果で見るのは、**両者が食い違ったときに黙って壊れないため**——
// agent 表に行があるのに kind が user なら、それはデータの誤りであって
// 「エージェントとして描かない」で隠してよいものではない。
func adminAgentView(row gen.ListAdminUsersRow) *agentView {
	if !row.ClientKind.Valid {
		return nil
	}
	v := &agentView{
		ClientKind: row.ClientKind.String,
		ModelName:  row.AgentModelName.String,
		ProjectKey: row.AgentProjectKey.String,
		TrustLevel: row.AgentTrustLevel.Int32,
	}
	if row.OwnerActorID.Valid {
		v.Owner = &ownerRef{
			ID:          row.OwnerActorID.String,
			DisplayName: row.OwnerDisplayName.String,
		}
	}
	return v
}
