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

// スプリントの運用（ApiDesign.md 9.12.1 / 9.12.2）を**実際のDBに対して**通す。pb-6。
//
// 単体テストはフェイクを差し替えるため queries/sprint.sql が一度も実行されない。
// **ここでしか確かめられないものが4つある。**
//
//   - ListOnstageTicketIDs の再帰CTEが**配下まで届くこと**。段を決めるのは親で
//     あり、子は staged_at が NULL のままオンステージ段に出る（GuiDesign.md 5.4）
//     ——staged_at だけで書いた実装はここで落ちる
//   - **エピックが対象から外れること**（どちらの段にも行として出ない）
//   - 9.2.1 の retired が3条件の AND で効くこと。とくに**親が未完了なら
//     完了した子も残る**——条件3 を落とした実装はここで落ちる
//   - 終了で**完了した根だけ**が段から降り、未完了の根が残ること
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestSprintLifecycleIntegration(t *testing.T) {
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
	adminEmail := "spr-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "sp-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"スプリント結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	// ── 盤面を作る ───────────────────────────────────────
	//
	//	1 epic   グルーピング専用。**どちらの段にも出ない**
	//	2 story  親を持たない。オンステージへ上げる
	//	3 task     └ 2 の子。**staged_at は NULL のまま親と一緒に運ばれる**
	//	4 task       └ 3 の子（孫まで届くかを見る）
	//	5 story  バックログに残す
	epic := createTicketIT(t, r, session, base, `{"type":"epic","title":"まとめ"}`)
	parent := createTicketIT(t, r, session, base, `{"type":"story","title":"親の仕事"}`)
	child := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"task","title":"子の仕事","parent_seq":%d}`, seqOf(parent)))
	grandchild := createTicketIT(t, r, session, base,
		fmt.Sprintf(`{"type":"task","title":"孫の仕事","parent_seq":%d}`, seqOf(child)))
	backlogOnly := createTicketIT(t, r, session, base, `{"type":"story","title":"まだやらない"}`)

	// ── ⓪ 着手すると、表示上のトップレベルの祖先が段へ上がる（9.6。pb-5）──
	//
	// **孫に着手する。** 上がるのは孫でも子でもなく、**部分木の根である親**
	// ——段に置けるのは表示上のトップレベルだけで、配下は親と一緒に運ばれる。
	if got := stagedAtOf(t, r, session, base, seqOf(parent)); got != nil {
		t.Fatalf("着手する前から親がオンステージに居る: %v（始点が意味を持たない）", got)
	}
	rec = postWithCookie(r, fmt.Sprintf("%s/tickets/%d/transition", base, seqOf(grandchild)),
		session, `{"to":"in_progress"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("孫の着手の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if stagedAtOf(t, r, session, base, seqOf(parent)) == nil {
		t.Error("孫に着手したのに、部分木の根（親）がオンステージへ上がっていない")
	}
	// **上がるのは根だけである。** 孫と子は staged_at が NULL のまま、
	// 親と一緒にオンステージ段へ出る（GuiDesign.md 5.4）。
	for _, tk := range []map[string]any{child, grandchild} {
		if got := stagedAtOf(t, r, session, base, seqOf(tk)); got != nil {
			t.Errorf("seq=%d の staged_at = %v, want null（子は親と一緒に運ばれる）",
				seqOf(tk), got)
		}
	}

	// **未着手へ戻しても降りない**（9.6）。「未着手だがオンステージ」は
	// 段が表せなければならない状態である（DbDesign.md 6.6）。
	rec = postWithCookie(r, fmt.Sprintf("%s/tickets/%d/transition", base, seqOf(grandchild)),
		session, `{"to":"todo"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("孫を未着手へ戻す status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if stagedAtOf(t, r, session, base, seqOf(parent)) == nil {
		t.Error("未着手へ戻したらオンステージから降りた（降ろすのは手か、スプリントの終了だけ）")
	}

	// ── ① 開始：オンステージの部分木が対象になる ──────────────
	rec = postWithCookie(r, base+"/sprints/start", session,
		`{"name":"Sprint 1","start_date":"2026-09-08","end_date":"2026-09-21"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("開始の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	sprint := viewOf(t, rec)
	sprintID := sprint["id"].(string)
	if sprint["status"] != "active" {
		t.Errorf("開始したスプリントの status = %v, want active", sprint["status"])
	}
	// **親・子・孫の3件が対象になる。** エピックとバックログの行は入らない。
	if n := sprint["ticket_count"].(float64); n != 3 {
		t.Errorf("ticket_count = %v, want 3（親と配下2件。エピックとバックログは入らない）", n)
	}

	// 実データで確かめる——sprint フィルタが3件を返すこと。
	wantEnrolled := []int{seqOf(parent), seqOf(child), seqOf(grandchild)}
	if got := ticketSeqs(t, r, session, base, "?sprint="+sprintID); !equalInts(got, wantEnrolled) {
		t.Errorf("スプリントの対象 = %v, want %v（**配下まで届くこと**）", got, wantEnrolled)
	}
	wantOut := []int{seqOf(epic), seqOf(backlogOnly)}
	if got := ticketSeqs(t, r, session, base, "?sprint=none"); !equalInts(got, wantOut) {
		t.Errorf("対象外 = %v, want %v（エピックとバックログの行）", got, wantOut)
	}

	// ── ② 進行中は同時に1本だけ（9.12.1）───────────────────
	rec = postWithCookie(r, base+"/sprints/start", session, `{"name":"Sprint 2"}`)
	if rec.Code != http.StatusConflict {
		t.Fatalf("2本目の開始の status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}

	// ── ③ 完了させる：親の前に子を完了させる（9.6 の検証7）──────
	//
	// **孫 → 子 → 親の順でしか完了できない。** 未完了の子を抱えた親は
	// done へ進めない（pb-72）。
	closeTicket(t, r, session, base, seqOf(grandchild))
	closeTicket(t, r, session, base, seqOf(child))

	// **スプリントが動いている間は、完了しても消えない**（9.2.1 の条件2）。
	// 期間の中で何が終わったかを見る面が要る。
	all := []int{seqOf(epic), seqOf(parent), seqOf(child), seqOf(grandchild), seqOf(backlogOnly)}
	if got := ticketSeqs(t, r, session, base, ""); !equalInts(got, all) {
		t.Errorf("スプリント進行中の一覧 = %v, want %v（完了しても消えない）", got, all)
	}

	// ── ④ 終了：完了した根だけが降り、棚に戻る ─────────────────
	rec = postWithCookie(r, base+"/sprints/"+sprintID+"/finish", session, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("終了の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	finished := viewOf(t, rec)
	if finished["status"] != "completed" {
		t.Errorf("終了後の status = %v, want completed", finished["status"])
	}

	// **親が未完了なので、完了した子も残る**（9.2.1 の条件3）。
	// これが「子だけ消えて配下が歯抜けになる」ことを防いでいる。
	if got := ticketSeqs(t, r, session, base, ""); !equalInts(got, all) {
		t.Errorf("終了直後の一覧 = %v, want %v（**親が未完了なら完了した子も残る**）", got, all)
	}
	// 親はまだ未完了なので、段にも残っている。
	if staged := stagedAtOf(t, r, session, base, seqOf(parent)); staged == nil {
		t.Error("未完了の根がオンステージから降りている（9.12.2 は完了したものだけを降ろす）")
	}

	// ── ⑤ 親も完了させると、部分木が丸ごと棚に戻る ────────────
	closeTicket(t, r, session, base, seqOf(parent))

	// **親・子・孫の3件が一覧から消える。** 残るのはエピックとバックログの行。
	wantVisible := []int{seqOf(epic), seqOf(backlogOnly)}
	if got := ticketSeqs(t, r, session, base, ""); !equalInts(got, wantVisible) {
		t.Errorf("棚に戻ったあとの一覧 = %v, want %v", got, wantVisible)
	}
	// **retired=true が唯一の逃げ道である**（9.2.1。検索画面ができるまで）。
	if got := ticketSeqs(t, r, session, base, "?retired=true"); !equalInts(got, all) {
		t.Errorf("retired=true の一覧 = %v, want %v", got, all)
	}

	// **完了した根は段から降りている。** finish の時点では未完了だったので、
	// 降りたのではなく「棚に戻ったことで一覧から外れた」形である——
	// **段と棚は別の軸**であり、ここを取り違えると片方だけ直して通らなくなる。
	if got := ticketSeqs(t, r, session, base, "?retired=true&sprint="+sprintID); !equalInts(got, wantEnrolled) {
		t.Errorf("スプリントの所属 = %v, want %v（**終了しても sprint_id は消えない**）", got, wantEnrolled)
	}

	// ── ⑥ 終わったスプリントは二度終えられない（9.12.2）────────
	rec = postWithCookie(r, base+"/sprints/"+sprintID+"/finish", session, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("2回目の終了の status = %d, want 409（body=%s）", rec.Code, rec.Body.String())
	}

	// ── ⑦ 終わったので、次のスプリントを始められる ───────────
	//
	// **未完了だったものが次のスプリントへ持ち越される**——ここでは全部
	// 完了してしまったので、対象は0件になる。**それでも開始できる**（9.12.1）。
	rec = postWithCookie(r, base+"/sprints/start", session, `{"name":"Sprint 2"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("次の開始の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	if n := viewOf(t, rec)["ticket_count"].(float64); n != 0 {
		t.Errorf("次のスプリントの ticket_count = %v, want 0（棚に戻った行は入らない）", n)
	}
}

// ── 補助 ────────────────────────────────────────────────────

func seqOf(ticket map[string]any) int {
	return int(ticket["seq"].(float64))
}

// closeTicket は 9.6 の遷移で完了にする。**closed_at は遷移の副作用でしか動かない**
// （DbDesign.md 6.6）ので、この経路以外で完了状態は作れない。
//
// **未着手から完了へは直接進めない。** simple テンプレートの順路は
// todo → in_progress → done である（DbDesign.md 7.4）——遷移そのものが
// ワークフローの定義に縛られることを、この2段が示している。
func closeTicket(t *testing.T, r http.Handler, session, base string, seq int) {
	t.Helper()
	// **中間の in_progress は既に済んでいることがある。** 子が未着手を出た
	// 時点で pb-72 の連動が祖先を進行中にするためで、409 は「もうそこに居る」
	// を意味する。**完了への遷移だけは必ず通ること**を要求する。
	rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/transition", base, seq), session,
		`{"to":"in_progress"}`)
	if rec.Code != http.StatusOK && rec.Code != http.StatusConflict {
		t.Fatalf("seq=%d を in_progress にできない: status = %d（body=%s）",
			seq, rec.Code, rec.Body.String())
	}
	rec = postWithCookie(r, fmt.Sprintf("%s/tickets/%d/transition", base, seq), session,
		`{"to":"done"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("seq=%d を完了にできない: status = %d（body=%s）",
			seq, rec.Code, rec.Body.String())
	}
}

// stagedAtOf は 9.5.1 の staged_at を読む（null ならバックログ段）。
func stagedAtOf(t *testing.T, r http.Handler, session, base string, seq int) any {
	t.Helper()
	rec := getWithCookie(r, fmt.Sprintf("%s/tickets/%d?retired=true", base, seq), session)
	if rec.Code != http.StatusOK {
		t.Fatalf("seq=%d を読めない: status = %d（body=%s）", seq, rec.Code, rec.Body.String())
	}
	return viewOf(t, rec)["staged_at"]
}
