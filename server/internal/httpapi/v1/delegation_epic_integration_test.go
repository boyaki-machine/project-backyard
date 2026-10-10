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

// エピックの直下のチケットを自律実行の根にする（Delegation.md 4.1）を**実際のDBに
// 対して**通す。pb-282。
//
// 0057 で DB の制約（ck_ticket_delegated_no_parent）を外し、判定をサーバへ移した。
// **制約が消えた分を、サーバが漏れなく塞いでいるか**をここで確かめる。
//
//   - エピックの直下は根にできる。エピック以外の親の下は根にできない
//   - 自律実行の中のエピックの下は、木が入れ子になるので根にできない
//   - 根の親を PATCH で変えるときも同じ条件（エピック以外の親へは移せない）
//   - 直下に根を持つエピックは、種別を変えられない
//   - main は根の子孫にしか起票できない（根の上のエピックの直下は木の外）
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`。
func TestDelegationEpicRootIntegration(t *testing.T) {
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
	adminEmail := "deleg-epic-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "deleg-epic-" + strings.ToLower(adminID[len(adminID)-8:])
	var createdAgents []string
	// **プロジェクトを先に消してから、エージェントを消す**（委任が RESTRICT で参照する。pb-253）。
	t.Cleanup(func() {
		bg := context.Background()
		pool.Exec(bg, `DELETE FROM project WHERE key = $1`, key)
		for _, id := range createdAgents {
			pool.Exec(bg, `DELETE FROM audit_log WHERE actor_id = $1`, id)
			pool.Exec(bg, `DELETE FROM actor WHERE id = $1`, id)
		}
	})
	if rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"エピックの根の結合テスト","workflow_template":"with_review"}`, key)); rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	register := func(name string) agentJSON {
		t.Helper()
		rec := postWithCookie(r, "/api/v1/me/agents", session,
			fmt.Sprintf(`{"display_name":%q,"project_key":%q,"client_kind":"claude_code"}`, name, key))
		if rec.Code != http.StatusCreated {
			t.Fatalf("エージェント登録の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var got agentJSON
		decodeJSONBody(t, rec, &got)
		createdAgents = append(createdAgents, got.ID)
		return got
	}
	main := register("main")
	evaluator := register("evaluator")
	var mainToken issuedAgentTokenJSON
	decodeJSONBody(t, postWithCookie(r, "/api/v1/me/agents/"+main.ID+"/tokens", session,
		`{"expires_in_days":30}`), &mainToken)

	ticket := func(t *testing.T, typ, title string, parent int) int {
		t.Helper()
		body := fmt.Sprintf(`{"type":%q,"title":%q}`, typ, title)
		if parent > 0 {
			body = fmt.Sprintf(`{"type":%q,"title":%q,"parent_seq":%d}`, typ, title, parent)
		}
		rec := postWithCookie(r, base+"/tickets", session, body)
		if rec.Code != http.StatusCreated {
			t.Fatalf("%s の作成 status = %d（body=%s）", title, rec.Code, rec.Body.String())
		}
		return int(viewOf(t, rec)["seq"].(float64))
	}
	delegateWith := func(seq int, mainID string) (int, string) {
		rec := postWithCookie(r, fmt.Sprintf("%s/tickets/%d/delegation", base, seq), session,
			fmt.Sprintf(`{"main_actor_id":%q,"evaluator_actor_id":%q,"scope":{}}`, mainID, evaluator.ID))
		return rec.Code, rec.Body.String()
	}
	delegate := func(seq int) (int, string) { return delegateWith(seq, main.ID) }
	versionOf := func(t *testing.T, seq int) string {
		t.Helper()
		return fmt.Sprintf(`"%v"`, getTicketIT(t, r, session, base, seq)["version"])
	}
	patch := func(t *testing.T, seq int, body string) (int, string) {
		t.Helper()
		rec := patchProjectWithCookie(r, fmt.Sprintf("%s/tickets/%d", base, seq), session, versionOf(t, seq), body)
		return rec.Code, rec.Body.String()
	}

	epic := ticket(t, "epic", "受験の準備", 0)
	epic2 := ticket(t, "epic", "部活との両立", 0)
	story := ticket(t, "story", "数学の得点を上げる", epic)
	parentStory := ticket(t, "story", "英語の計画", 0)
	child := ticket(t, "task", "単語帳を決める", parentStory)

	t.Run("エピックの直下は根にできる", func(t *testing.T) {
		if code, body := delegate(story); code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", code, body)
		}
		if mode := getTicketIT(t, r, session, base, story)["execution_mode"]; mode != "delegated" {
			t.Errorf("execution_mode = %v, want delegated", mode)
		}
	})

	t.Run("エピック以外の親の下は根にできない", func(t *testing.T) {
		code, body := delegate(child)
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "エピックの直下") {
			t.Fatalf("status = %d, want 422（body=%s）", code, body)
		}
	})

	t.Run("根はエピック以外の親へ移せず、別のエピックへは移せる", func(t *testing.T) {
		if code, body := patch(t, story, fmt.Sprintf(`{"parent_seq":%d}`, parentStory)); code != http.StatusUnprocessableEntity {
			t.Fatalf("エピック以外へ status = %d, want 422（body=%s）", code, body)
		}
		if code, body := patch(t, story, fmt.Sprintf(`{"parent_seq":%d}`, epic2)); code != http.StatusOK {
			t.Fatalf("別のエピックへ status = %d, want 200（body=%s）", code, body)
		}
		if code, body := patch(t, story, `{"parent_seq":null}`); code != http.StatusOK {
			t.Fatalf("親なしへ status = %d, want 200（body=%s）", code, body)
		}
		if code, body := patch(t, story, fmt.Sprintf(`{"parent_seq":%d}`, epic)); code != http.StatusOK {
			t.Fatalf("元のエピックへ status = %d, want 200（body=%s）", code, body)
		}
	})

	t.Run("直下に根を持つエピックは種別を変えられない", func(t *testing.T) {
		if code, body := patch(t, epic, `{"type":"story"}`); code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", code, body)
		}
		// 根を持たないエピックは今までどおり変えられる。
		if code, body := patch(t, epic2, `{"type":"story"}`); code != http.StatusOK {
			t.Fatalf("根の無いエピック status = %d, want 200（body=%s）", code, body)
		}
	})

	t.Run("自律実行の中のエピックの下は根にできない（入れ子）", func(t *testing.T) {
		// **別の main を立てる。** 1つの main の principal は1つの委任に使う（12.2）。
		outerMain := register("outer-main")
		outer := ticket(t, "epic", "まとめて任せる", 0)
		if code, body := delegateWith(outer, outerMain.ID); code != http.StatusCreated {
			t.Fatalf("エピックの自律実行 status = %d（body=%s）", code, body)
		}
		inner := ticket(t, "story", "入れ子にしたい", outer)
		code, body := delegate(inner)
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, "自律実行の中") {
			t.Fatalf("status = %d, want 422（body=%s）", code, body)
		}
	})

	t.Run("main は根の上のエピックの直下に起票できない", func(t *testing.T) {
		// 委任を承認済にする（承認の手順はこのテストの対象外。経路は delegation_approval_integration_test.go）。
		if _, err := pool.Exec(ctx,
			`UPDATE delegation SET status = 'approved' WHERE root_ticket_id =
			   (SELECT id FROM ticket WHERE project_id = (SELECT id FROM project WHERE key = $1) AND seq = $2)`,
			key, story); err != nil {
			t.Fatalf("承認済にできない: %v", err)
		}
		rec := bearerPost(r, base+"/tickets", mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"木の外","parent_seq":%d}`, epic))
		if rec.Code != http.StatusForbidden {
			t.Fatalf("エピックの直下 status = %d, want 403（body=%s）", rec.Code, rec.Body.String())
		}
		rec = bearerPost(r, base+"/tickets", mainToken.Token,
			fmt.Sprintf(`{"type":"task","title":"木の中","parent_seq":%d}`, story))
		if rec.Code != http.StatusCreated {
			t.Fatalf("根の直下 status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
	})
}
