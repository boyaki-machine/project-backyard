package v1

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// チケット検索の条件（ApiDesign.md 9.2.1「検索の条件」）を**実際のDBに対して**通す。
//
// 単体テストはフェイクを差し替えるので、queries/search.sql も ListTickets の検索の
// 条件も一度も実行されない。**ここでしか確かめられないものが6つある。**
//
//   - キーワードがタイトル・本文・コメントに当たり、削除したコメントには当たらないこと
//   - **本文が NULL のチケットに、語を含まないのに当たらないこと**（NOT EXISTS と NULL）
//   - `%` と `_` が文字として扱われること（ILIKE のエスケープ）
//   - 完了日時の範囲で未完了が外れ、closed_at の並べ替えで未完了が末尾に来ること
//   - 着手日時を activity から導き、**着手し直しても最初の着手を採る**こと
//   - 着手していないものが着手日時の範囲で外れること
//
// **境目の時刻は DB の clock_timestamp() から取る。** activity.occurred_at と closed_at は
// DB の時計で入るので、テストの端末の時計と比べると、ずれの分だけ結果が揺れる。
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`（Development.md 6.1）。
func TestTicketSearchIntegration(t *testing.T) {
	dsn := os.Getenv("PB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})

	adminID := ulidgen.New()
	adminEmail := "tks-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "ts-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"チケット検索結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	seqOf := func(v map[string]any) int { return int(v["seq"].(float64)) }

	// searchSeqs は一覧を番号の昇順で引き、seq の並びを返す。
	// **retired=true はチケット検索が常に送る**（GuiDesign.md 5.13）。
	searchSeqs := func(t *testing.T, query string) []int {
		t.Helper()
		rec := getWithCookie(r, base+"/tickets?retired=true&sort=seq&"+query, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("一覧の status = %d（query=%s body=%s）", rec.Code, query, rec.Body.String())
		}
		var body struct {
			Items []struct {
				Seq int `json:"seq"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("一覧を読めない: %v", err)
		}
		seqs := []int{}
		for _, it := range body.Items {
			seqs = append(seqs, it.Seq)
		}
		return seqs
	}
	transition := func(t *testing.T, seq int, to string) {
		t.Helper()
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/transition", base, seq),
			session, fmt.Sprintf(`{"to":%q}`, to))
		if rec.Code != http.StatusOK {
			t.Fatalf("%d を %s へ遷移する status = %d（body=%s）", seq, to, rec.Code, rec.Body.String())
		}
	}
	postComment := func(t *testing.T, seq int, body string) string {
		t.Helper()
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/comments", base, seq),
			session, fmt.Sprintf(`{"body_md":%q,"kind":"discussion"}`, body))
		if rec.Code != http.StatusCreated {
			t.Fatalf("コメント作成の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		return viewOf(t, rec)["id"].(string)
	}
	// dbNow は DB の時計のいまを、クエリに載せられる形で返す。
	dbNow := func(t *testing.T) string {
		t.Helper()
		var ts time.Time
		if err := pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&ts); err != nil {
			t.Fatalf("DB の時計を読めない: %v", err)
		}
		return url.QueryEscape(ts.UTC().Format(time.RFC3339Nano))
	}
	kw := func(s string) string { return "q=" + url.QueryEscape(s) }

	// タイトル・本文・コメントのそれぞれにだけ「認証」を持つチケットと、持たないもの。
	byTitle := createTicketIT(t, r, session, base, `{"type":"task","title":"認証APIの実装"}`)
	byBody := createTicketIT(t, r, session, base,
		`{"type":"task","title":"本文で当たる","body_md":"ログインの認証を直す"}`)
	byComment := createTicketIT(t, r, session, base, `{"type":"story","title":"コメントで当たる"}`)
	noBody := createTicketIT(t, r, session, base, `{"type":"task","title":"本文なし"}`)
	deletedOnly := createTicketIT(t, r, session, base, `{"type":"task","title":"削除したコメントだけが持つ"}`)
	percent := createTicketIT(t, r, session, base, `{"type":"task","title":"達成率100%の確認"}`)
	hundred := createTicketIT(t, r, session, base, `{"type":"task","title":"達成率100件の確認"}`)
	epic := createTicketIT(t, r, session, base, `{"type":"epic","title":"認証基盤"}`)

	postComment(t, seqOf(byComment), "方式は認証サーバに寄せる")

	// **始点を確かめる。** 本文を送らなかったチケットが NULL でなければ、下の
	// 「NULL の本文で当たらない」は何も測っていない（Development.md 8.5）。
	var bodyIsNull bool
	if err := pool.QueryRow(ctx, `SELECT body_md IS NULL FROM ticket WHERE id = $1`,
		noBody["id"]).Scan(&bodyIsNull); err != nil || !bodyIsNull {
		t.Fatalf("本文を送らなかったチケットの body_md IS NULL = %v（err=%v）。始点が意味を持たない", bodyIsNull, err)
	}

	t.Run("キーワードはタイトル・本文・コメントに当たり、エピックも出る", func(t *testing.T) {
		got := searchSeqs(t, kw("認証"))
		want := []int{seqOf(byTitle), seqOf(byBody), seqOf(byComment), seqOf(epic)}
		if !slices.Equal(got, want) {
			t.Errorf("q=認証 = %v, want %v（%d は本文が NULL で語を含まない。含まれていたら NULL の扱いの誤り）",
				got, want, seqOf(noBody))
		}
	})

	t.Run("削除したコメントには当たらない", func(t *testing.T) {
		id := postComment(t, seqOf(deletedOnly), "消す前は当たる語ケルベロス")
		// 始点：削除する前は当たる
		if got := searchSeqs(t, kw("ケルベロス")); !slices.Equal(got, []int{seqOf(deletedOnly)}) {
			t.Fatalf("削除する前の q=ケルベロス = %v, want [%d]（始点が意味を持たない）", got, seqOf(deletedOnly))
		}
		rec := deleteWithCookie(r, fmt.Sprintf("%s/tickets/%d/comments/%s", base, seqOf(deletedOnly), id), session)
		if rec.Code >= http.StatusMultipleChoices {
			t.Fatalf("コメント削除の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if got := searchSeqs(t, kw("ケルベロス")); len(got) != 0 {
			t.Errorf("削除した後の q=ケルベロス = %v, want なし", got)
		}
	})

	t.Run("語は AND で、語ごとに別の場所に当たってよい", func(t *testing.T) {
		// 「コメントで」はタイトル、「サーバ」はコメントにある。全角の空白で区切る。
		if got := searchSeqs(t, kw("コメントで　サーバ")); !slices.Equal(got, []int{seqOf(byComment)}) {
			t.Errorf("q=コメントで　サーバ = %v, want [%d]", got, seqOf(byComment))
		}
		if got := searchSeqs(t, kw("認証 どこにも無い語")); len(got) != 0 {
			t.Errorf("片方の語が当たらないのに一致した: %v", got)
		}
	})

	t.Run("% と _ は文字として扱う", func(t *testing.T) {
		// % がワイルドカードとして効くと「100件」にも当たる
		if got := searchSeqs(t, kw("100%")); !slices.Equal(got, []int{seqOf(percent)}) {
			t.Errorf("q=100%% = %v, want [%d]（%d に当たるならエスケープされていない）",
				got, seqOf(percent), seqOf(hundred))
		}
		// _ がワイルドカードとして効くと「率100」の両方に当たる
		if got := searchSeqs(t, kw("率1_0")); len(got) != 0 {
			t.Errorf("q=率1_0 = %v, want なし", got)
		}
	})

	t.Run("番号の範囲は両端を含み、片方だけでもよい", func(t *testing.T) {
		got := searchSeqs(t, fmt.Sprintf("seq_from=%d&seq_to=%d", seqOf(byBody), seqOf(noBody)))
		if want := []int{seqOf(byBody), seqOf(byComment), seqOf(noBody)}; !slices.Equal(got, want) {
			t.Errorf("番号の範囲 = %v, want %v", got, want)
		}
		if got := searchSeqs(t, fmt.Sprintf("seq_from=%d", seqOf(epic))); !slices.Equal(got, []int{seqOf(epic)}) {
			t.Errorf("seq_from だけ = %v, want [%d]", got, seqOf(epic))
		}
	})

	t.Run("完了日時の範囲で未完了は外れ、closed_at で並べると未完了は末尾", func(t *testing.T) {
		since := dbNow(t)
		transition(t, seqOf(byTitle), "in_progress")
		transition(t, seqOf(byTitle), "done")
		before := dbNow(t)

		if got := searchSeqs(t, "closed_since="+since+"&closed_before="+before); !slices.Equal(got, []int{seqOf(byTitle)}) {
			t.Errorf("完了日時の範囲 = %v, want [%d]", got, seqOf(byTitle))
		}
		if got := searchSeqs(t, "closed_since="+before); len(got) != 0 {
			t.Errorf("完了より後だけを指定したのに当たった: %v", got)
		}

		for _, order := range []string{"asc", "desc"} {
			rec := getWithCookie(r, base+"/tickets?retired=true&sort=closed_at&order="+order, session)
			if rec.Code != http.StatusOK {
				t.Fatalf("closed_at で並べた一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
			}
			var body struct {
				Items []struct {
					Seq      int     `json:"seq"`
					ClosedAt *string `json:"closed_at"`
				} `json:"items"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("一覧を読めない: %v", err)
			}
			if len(body.Items) == 0 || body.Items[0].Seq != seqOf(byTitle) {
				t.Errorf("order=%s の先頭 = %+v, want 完了した %d（未完了は末尾）", order, body.Items, seqOf(byTitle))
			}
		}
	})

	t.Run("着手日時は最初の着手を採り、着手していないものは外れる", func(t *testing.T) {
		start := dbNow(t)
		transition(t, seqOf(byBody), "in_progress") // 最初の着手
		mid := dbNow(t)
		transition(t, seqOf(byBody), "todo")
		transition(t, seqOf(byBody), "in_progress") // 着手し直し
		end := dbNow(t)

		// 始点：todo から出た遷移が本当に2回ある（1回なら「最初を採る」を測れない）
		var exits int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM activity
			 WHERE entity_type = 'ticket' AND entity_id = $1 AND action = 'transition'
			   AND field = 'status_key' AND old_value = 'todo'`, byBody["id"]).Scan(&exits); err != nil || exits != 2 {
			t.Fatalf("todo から出た遷移 = %d（err=%v）, want 2", exits, err)
		}

		if got := searchSeqs(t, "started_since="+start+"&started_before="+mid); !slices.Equal(got, []int{seqOf(byBody)}) {
			t.Errorf("最初の着手の範囲 = %v, want [%d]", got, seqOf(byBody))
		}
		if got := searchSeqs(t, "started_since="+mid); slices.Contains(got, seqOf(byBody)) {
			t.Errorf("着手し直した時刻で当たった: %v（最初の着手を採っていない）", got)
		}
		got := searchSeqs(t, "started_before="+end)
		if want := []int{seqOf(byTitle), seqOf(byBody)}; !slices.Equal(got, want) {
			t.Errorf("started_before だけ = %v, want %v（着手していないものは外れる）", got, want)
		}
	})
}
