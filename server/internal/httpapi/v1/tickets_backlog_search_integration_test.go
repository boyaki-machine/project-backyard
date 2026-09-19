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

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// pb-84: 実DBで、検索→祖先補完→件数計算→200件制限の順序を確かめる。
func TestBacklogSearchIntegration(t *testing.T) {
	dsn := os.Getenv("PB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}
	ctx := context.Background()
	pool, err := store.NewPool(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})
	adminID := ulidgen.New()
	email := "backlog-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, email, auth.SystemRoleAdministrator)
	session := loginAs(t, r, email)
	key := "bl-6-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Error(err)
		}
		var count int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM project WHERE key=$1`, key).Scan(&count); err != nil || count != 0 {
			t.Errorf("後始末 count=%d err=%v", count, err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session, fmt.Sprintf(`{"key":%q,"name":"バックログ検索試験","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("project: %d %s", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key
	seqOf := func(v map[string]any) int { return int(v["seq"].(float64)) }
	create := func(body string) int { return seqOf(createTicketIT(t, r, session, base, body)) }
	epic := create(`{"type":"epic","title":"基盤エピック"}`)
	parent := create(fmt.Sprintf(`{"type":"story","title":"親の設計","parent_seq":%d}`, epic))
	child := create(fmt.Sprintf(`{"type":"task","title":"中間の実装","parent_seq":%d}`, parent))
	leaf := create(fmt.Sprintf(`{"type":"task","title":"日本語の探索対象100%%_確認","parent_seq":%d}`, child))
	bodyOnly := create(`{"type":"task","title":"キーワードは本文だけ","body_md":"日本語の探索対象"}`)
	rec = postWithCookie(r, base+"/tags", session, `{"name":"検索タグ"}`)
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body.String())
	}
	tagID := viewOf(t, rec)["id"].(string)
	tagged := create(fmt.Sprintf(`{"type":"task","title":"タグ付きの作業","tag_ids":[%q]}`, tagID))
	type response struct {
		Items []struct {
			Seq int `json:"seq"`
		} `json:"items"`
		Total int `json:"total"`
	}
	fetch := func(t *testing.T, query string) response {
		t.Helper()
		rec := getWithCookie(r, base+"/tickets?type=story,task&sort=seq&"+query, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("get %s: %d %s", query, rec.Code, rec.Body.String())
		}
		var out response
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	seqs := func(out response) []int {
		ids := []int{}
		for _, it := range out.Items {
			ids = append(ids, it.Seq)
		}
		return ids
	}
	check := func(t *testing.T, term string, want []int) {
		t.Helper()
		got := fetch(t, "search_mode=backlog&q="+url.QueryEscape(term))
		if !slices.Equal(seqs(got), want) || got.Total != len(want) {
			t.Errorf("q=%q got=%v total=%d want=%v", term, seqs(got), got.Total, want)
		}
	}
	t.Run("日本語の子が一致すると祖先を重複なく補完", func(t *testing.T) { check(t, "日本語", []int{parent, child, leaf}) })
	t.Run("エピック名で子孫を検索", func(t *testing.T) { check(t, "基盤エピック", []int{parent, child, leaf}) })
	t.Run("タグ名", func(t *testing.T) { check(t, "検索タグ", []int{tagged}) })
	t.Run("番号とキー付き番号と大文字", func(t *testing.T) {
		check(t, fmt.Sprint(tagged), []int{tagged})
		check(t, strings.ToUpper(fmt.Sprintf("%s-%d", key, tagged)), []int{tagged})
	})
	t.Run("記号は文字でANDは全角空白も区切る", func(t *testing.T) {
		check(t, "100%_　確認", []int{parent, child, leaf})
		check(t, "100%X", []int{})
		check(t, "日本語 検索タグ", []int{})
	})
	t.Run("空白は通常一覧で本文は通常検索だけ", func(t *testing.T) {
		got := fetch(t, "search_mode=backlog&q="+url.QueryEscape("　 "))
		if got.Total != 5 {
			t.Errorf("total=%d want=5", got.Total)
		}
		got = fetch(t, "q="+url.QueryEscape("日本語の探索対象"))
		if !slices.Equal(seqs(got), []int{leaf, bodyOnly}) {
			t.Errorf("通常検索を変えた: %v", seqs(got))
		}
	})
	t.Run("既存フィルタから外れた子の祖先を出さない", func(t *testing.T) {
		got := fetch(t, "search_mode=backlog&q="+url.QueryEscape("日本語")+"&tag="+tagID)
		if got.Total != 0 || len(got.Items) != 0 {
			t.Errorf("一致しない子の祖先が残った: %+v", got)
		}
	})
	t.Run("全件を検索してから200件に制限", func(t *testing.T) {
		var filler []int
		for i := 0; i < 201; i++ {
			filler = append(filler, create(fmt.Sprintf(`{"type":"task","title":"上限試験 %d"}`, i)))
		}
		outside := create(`{"type":"task","title":"取得範囲外の固有キーワード"}`)
		before := fetch(t, "")
		if len(before.Items) != 200 || slices.Contains(seqs(before), outside) {
			t.Fatal("始点が200件の外になっていない")
		}
		check(t, "取得範囲外の固有キーワード", []int{outside})
		got := fetch(t, "search_mode=backlog&q="+url.QueryEscape("上限試験"))
		if len(got.Items) != 200 || got.Total != len(filler) || !slices.Equal(seqs(got), filler[:200]) {
			t.Errorf("limit=%d total=%d", len(got.Items), got.Total)
		}
	})
	t.Run("別プロジェクトの語は一致しない", func(t *testing.T) {
		ids, err := q.SearchBacklogTicketIDs(ctx, gen.SearchBacklogTicketIDsParams{ProjectID: ulidgen.New(), Patterns: []string{"%日本語%"}})
		if err != nil || len(ids) != 0 {
			t.Errorf("project isolation ids=%v err=%v", ids, err)
		}
	})
}
