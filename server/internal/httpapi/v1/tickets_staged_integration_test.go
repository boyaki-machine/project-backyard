package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// オンステージで絞る条件（ApiDesign.md 9.2.1「オンステージで絞る」）を**実際のDBに対して**通す。
//
// 単体テストはフェイクを差し替えるので、ListTickets の staged_tree は一度も実行されない。
// **ここでしか確かめられないものが3つある。**
//
//   - **子と孫が staged_at を持たないまま、親と一緒に返る**こと（段を決めるのは親。9.4.1）
//   - エピックの配下に置いた根は返り、エピック自身は返らないこと
//   - **棚に戻ったオンステージの根は既定で外れ、retired=true で戻る**こと（条件は種類ごとに独立）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`（Development.md 6.1）。
func TestTicketStagedFilterIntegration(t *testing.T) {
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
	adminEmail := "stg-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "st-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"オンステージ絞り込み結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	seqOf := func(v map[string]any) int { return int(v["seq"].(float64)) }
	stage := func(t *testing.T, seq int) {
		t.Helper()
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/move", base, seq), session,
			`{"staged":true,"position":"first"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("%d をオンステージへ上げる status = %d（body=%s）", seq, rec.Code, rec.Body.String())
		}
	}
	transition := func(t *testing.T, seq int, to string) {
		t.Helper()
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/transition", base, seq),
			session, fmt.Sprintf(`{"to":%q}`, to))
		if rec.Code != http.StatusOK {
			t.Fatalf("%d を %s へ遷移する status = %d（body=%s）", seq, to, rec.Code, rec.Body.String())
		}
	}

	// ── 盤面を作る ───────────────────────────────────────
	//
	//	epic   グルーピング専用。**どちらの段にも出ない**
	//	root   オンステージへ上げる
	//	child    └ root の子（staged_at は NULL のまま）
	//	grand      └ child の子
	//	inEpic epic の子。表示上のトップレベルなのでオンステージへ上げられる
	//	back   バックログに残す
	//	backC    └ back の子
	//	shelf  オンステージへ上げ、スプリントを終えたあとに完了させる（棚に戻る）
	epic := createTicketIT(t, r, session, base, `{"type":"epic","title":"まとめ"}`)
	root := createTicketIT(t, r, session, base, `{"type":"story","title":"親の仕事"}`)
	child := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"task","title":"子の仕事","parent_seq":%d}`, seqOf(root)))
	grand := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"task","title":"孫の仕事","parent_seq":%d}`, seqOf(child)))
	inEpic := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"task","title":"エピック配下の仕事","parent_seq":%d}`, seqOf(epic)))
	back := createTicketIT(t, r, session, base, `{"type":"story","title":"まだやらない"}`)
	backC := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"task","title":"まだやらない子","parent_seq":%d}`, seqOf(back)))
	shelf := createTicketIT(t, r, session, base, `{"type":"task","title":"終わって棚に戻る"}`)

	for _, tk := range []map[string]any{root, inEpic, shelf} {
		stage(t, seqOf(tk))
	}

	// **始点を確かめる。** 子と孫が staged_at を持っていたら、下の「配下も返る」は
	// staged_at だけで絞っても通ってしまい、何も測っていない（Development.md 8.5）。
	for _, tk := range []map[string]any{child, grand} {
		if got := stagedAtOf(t, r, session, base, seqOf(tk)); got != nil {
			t.Fatalf("seq=%d の staged_at = %v, want null。始点が意味を持たない", seqOf(tk), got)
		}
	}

	// shelf を棚に戻す：スプリントを始めて、未完了のまま終え（段に残る。9.12.2）、そのあと完了させる。
	rec = postWithCookie(r, base+"/sprints/start", session, `{"name":"Sprint 1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("開始の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	sprintID := viewOf(t, rec)["id"].(string)
	rec = postWithCookie(r, base+"/sprints/"+sprintID+"/finish", session, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("終了の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	transition(t, seqOf(shelf), "in_progress")
	transition(t, seqOf(shelf), "done")
	if stagedAtOf(t, r, session, base, seqOf(shelf)) == nil {
		t.Fatal("終了後に完了させた根がオンステージに居ない。始点が意味を持たない")
	}

	onstage := []int{seqOf(root), seqOf(child), seqOf(grand), seqOf(inEpic)}

	t.Run("オンステージの根と、その子と孫が返る。エピックとバックログは返らない", func(t *testing.T) {
		if got := ticketSeqs(t, r, session, base, "?staged=true&sort=seq"); !equalInts(got, onstage) {
			t.Errorf("staged=true = %v, want %v（エピック %d、バックログ %d・%d、棚に戻った %d は外れる）",
				got, onstage, seqOf(epic), seqOf(back), seqOf(backC), seqOf(shelf))
		}
	})

	t.Run("棚に戻った根は retired=true で戻る", func(t *testing.T) {
		want := append(append([]int(nil), onstage...), seqOf(shelf))
		if got := ticketSeqs(t, r, session, base, "?staged=true&retired=true&sort=seq"); !equalInts(got, want) {
			t.Errorf("staged=true&retired=true = %v, want %v", got, want)
		}
	})

	t.Run("他の条件と AND で効く", func(t *testing.T) {
		want := []int{seqOf(child), seqOf(grand), seqOf(inEpic)}
		if got := ticketSeqs(t, r, session, base, "?staged=true&type=task&sort=seq"); !equalInts(got, want) {
			t.Errorf("staged=true&type=task = %v, want %v", got, want)
		}
	})

	t.Run("指定しなければ絞らない", func(t *testing.T) {
		want := []int{seqOf(epic), seqOf(root), seqOf(child), seqOf(grand), seqOf(inEpic), seqOf(back), seqOf(backC)}
		if got := ticketSeqs(t, r, session, base, "?sort=seq"); !equalInts(got, want) {
			t.Errorf("条件なし = %v, want %v", got, want)
		}
	})

	t.Run("true 以外は 422", func(t *testing.T) {
		for _, v := range []string{"false", "1", "yes"} {
			rec := getWithCookie(r, base+"/tickets?staged="+v, session)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("staged=%s の status = %d, want 422（body=%s）", v, rec.Code, rec.Body.String())
			}
		}
	})
}
