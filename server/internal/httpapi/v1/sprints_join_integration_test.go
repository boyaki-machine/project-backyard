package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// スプリント中にオンステージへ入ったものを、その場で所属させる（ApiDesign.md 9.12.3。pb-129）。
//
// **入口4つを1本の盤面で通す。** 所属を書くのが開始だけだと、途中で加わった配下は
// 9.2.1 の条件2 を満たせず、根が棚に戻ったあともバックログに残る——**stg で起きた形
// （完了した根の配下が残る）を最後に一覧で確かめる。**
func TestSprintJoinDuringActiveIntegration(t *testing.T) {
	url := os.Getenv("PB_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PB_TEST_DATABASE_URL が未設定のためスキップする")
	}

	ctx := context.Background()
	pool, err := store.NewPool(ctx, url)
	if err != nil {
		t.Fatalf("DBに接続できない: %v", err)
	}
	t.Cleanup(pool.Close)

	q := gen.New(pool)
	r := routerWithDeps(Deps{Queries: q, Tx: store.NewTxRunner(pool)})

	adminID := ulidgen.New()
	adminEmail := "spj-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "spj-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"スプリント途中参加の結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key
	create := func(body string) int {
		return seqOf(createTicketIT(t, r, session, base, body))
	}
	withParent := func(title string, parent int) int {
		return create(fmt.Sprintf(`{"type":"task","title":%q,"parent_seq":%d}`, title, parent))
	}

	// ── 開始の時点でオンステージに居るのは根 R だけ ───────────────
	root := create(`{"type":"story","title":"根"}`)
	if rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/move", base, root), session,
		`{"staged":true,"position":"first"}`); rec.Code != http.StatusOK {
		t.Fatalf("根をオンステージへ上げる status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	rec = postWithCookie(r, base+"/sprints/start", session, `{"name":"Sprint 1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("開始の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	sprintID := viewOf(t, rec)["id"].(string)

	// ── スプリント中に入る4つの入口 ────────────────────────────
	// ① 作成：根の配下に子を作る
	created := withParent("途中で作った子", root)
	// ② 親の付け替え：バックログの X（子 X1 つき）を根の配下へ
	moved := create(`{"type":"task","title":"付け替える X"}`)
	movedChild := withParent("X の子", moved)
	view := viewOf(t, getWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, moved), session))
	patchTicketIT(t, r, session, base, moved, fmt.Sprintf(`"%d"`, int(view["version"].(float64))),
		fmt.Sprintf(`{"parent_seq":%d}`, root))
	// ③ 段の移動：バックログの根 Y（子 Y1 つき）をオンステージへ
	staged := create(`{"type":"story","title":"途中で上げる Y"}`)
	stagedChild := withParent("Y の子", staged)
	if rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/move", base, staged), session,
		`{"staged":true,"position":"last"}`); rec.Code != http.StatusOK {
		t.Fatalf("Y をオンステージへ上げる status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	// ④ 着手による段上げ：バックログの根 Z の子 Z1 に着手すると Z が上がる（9.6）
	started := create(`{"type":"story","title":"着手で上がる Z"}`)
	startedChild := withParent("Z の子", started)
	if rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/transition", base, startedChild), session,
		`{"to":"in_progress"}`); rec.Code != http.StatusOK {
		t.Fatalf("Z の子の着手 status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	// 対照：バックログに残る W（子 W1 つき）は入らない
	stay := create(`{"type":"story","title":"バックログの W"}`)
	stayChild := withParent("W の子", stay)

	enrolled := []int{root, created, moved, movedChild, staged, stagedChild, started, startedChild}
	slices.Sort(enrolled)
	got := ticketSeqs(t, r, session, base, "?sprint="+sprintID)
	slices.Sort(got)
	if !equalInts(got, enrolled) {
		t.Fatalf("スプリントの対象 = %v, want %v（途中で入った部分木がすべて所属し、W は入らない）", got, enrolled)
	}

	// ── 全部完了してから終えると、途中で入ったものも棚に戻る ───────────
	for _, seq := range []int{created, movedChild, moved, root, stagedChild, staged, startedChild, started} {
		closeTicket(t, r, session, base, seq)
	}
	if rec := postWithCookie(r, fmt.Sprintf("%s/sprints/%s/finish", base, sprintID), session, ``); rec.Code != http.StatusOK {
		t.Fatalf("終了の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	visible := ticketSeqs(t, r, session, base, "")
	slices.Sort(visible)
	want := []int{stay, stayChild}
	if !equalInts(visible, want) {
		t.Errorf("終了後の一覧 = %v, want %v（完了した部分木は配下まで棚に戻る）", visible, want)
	}
}
