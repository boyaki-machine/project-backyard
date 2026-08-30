package v1

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 文書API（ApiDesign.md 10章）を**実際のDBに対して**通す。
//
// 単体テストはフェイクを差し替えるため、queries/document.sql の SQL は一度も
// 実行されない。ここでしか確かめられないものが6つある。
//
//   - ListDocumentTree の ORDER BY（parent_id NULLS FIRST, sort_order, slug）が
//     実際にその順で返り、木とパスが組み上がること
//   - uq_document_slug（NULLS NOT DISTINCT。DbDesign.md 8.1.1）が
//     **トップレベルでも**効き、409 already_exists に写ること
//   - UPDATE ... WHERE version = ? が楽観ロックとして働き、version が +1 されること
//   - parent_id の CASCADE で**部分木ごと**消え、document_revision も落ちること
//   - NextDocumentSortOrder の IS NOT DISTINCT FROM が、トップレベルと
//     子とで別々に「末尾 + 10」を返すこと
//   - リビジョンが POST で1件、内容が変わった PATCH でだけ積まれること
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`
// （Development.md 6.1。接続文字列を手で書かない）。
func TestDocsIntegration(t *testing.T) {
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
	adminEmail := "doc-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "dc-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"文書結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}

	base := "/api/v1/projects/" + key + "/docs"

	// ── ⓪ テンプレートの複製を取り除く ─────────────────
	//
	// **手順23 から、作りたてのプロジェクトには型の4文書が並ぶ**
	// （DbDesign.md 8.1.2）。本テストが測るのは 10章のAPIの意味論であって
	// 複製ではない——複製そのものは projects_integration_test.go の
	// assertDocTemplatesCopied が測る。**以降の検証は「空から作る」前提で
	// 書かれている**ので、ここで0件へ戻してから始める。
	//
	// APIの DELETE ではなく直接 SQL で消すのは、まだ測っていない
	// エンドポイントに前提を預けないためである。
	if _, err := pool.Exec(ctx, `DELETE FROM document WHERE project_id = (
		SELECT id FROM project WHERE key = $1)`, key); err != nil {
		t.Fatalf("複製された文書を取り除けない: %v", err)
	}

	// ── ① 文書が1件も無い状態 ───────────────────────────
	//
	// is_template = true の4件が漏れて出ないことを、実データに対して確かめる。
	empty := getWithCookie(r, base, session)
	if empty.Code != http.StatusOK {
		t.Fatalf("空の目次の status = %d（body=%s）", empty.Code, empty.Body.String())
	}
	if items := docItems(t, empty); len(items) != 0 {
		t.Fatalf("作りたてのプロジェクトに文書が %d件ある。テンプレート行が漏れている可能性がある: %s",
			len(items), empty.Body.String())
	}

	// ── ② 作成と sort_order の既定 ─────────────────────
	//
	// **sort_order を省略して3件作る。** 同じ親の中の末尾（最大値 + 10）に
	// 積まれるので、10 / 20 / 30 になるはずである（10.4）。
	visionID := createDoc(t, r, base, session, `{"slug":"vision","title":"価値観・世界観","body_md":"価値観の本文。"}`, 10)
	rulesID := createDoc(t, r, base, session,
		`{"slug":"rules","title":"規約","body_md":"本書は規約である。\n\n## 命名\n\n- 単数形\n\n## ブランチ\n\n- main へ直接コミットしない\n"}`, 20)
	createDoc(t, r, base, session, `{"slug":"decisions","title":"判断の記録"}`, 30)

	// 子はトップレベルとは別の集合で数える（IS NOT DISTINCT FROM。document.sql）。
	namingID := createDoc(t, r, base, session,
		`{"slug":"naming","title":"命名","parent_path":"rules","body_md":"命名の本文。"}`, 10)
	_ = visionID
	_ = namingID

	// ── ③ 目次の木と並び ──────────────────────────────
	tree := getWithCookie(r, base, session)
	if tree.Code != http.StatusOK {
		t.Fatalf("目次の status = %d（body=%s）", tree.Code, tree.Body.String())
	}
	items := docItems(t, tree)
	// 期待値を数え上げる——条件に合う行は「トップレベルの3件」、規則は
	// sort_order 昇順、同値は無い（10 / 20 / 30）。
	wantTop := []string{"vision", "rules", "decisions"}
	if len(items) != len(wantTop) {
		t.Fatalf("トップレベル = %d件, want %d（body=%s）", len(items), len(wantTop), tree.Body.String())
	}
	for i, want := range wantTop {
		if got, _ := items[i]["path"].(string); got != want {
			t.Errorf("items[%d].path = %q, want %q（sort_order 昇順）", i, got, want)
		}
	}
	// path は slug を根から連ねたもの（10.1）。
	children, _ := items[1]["children"].([]any)
	if len(children) != 1 {
		t.Fatalf("rules の子 = %d件, want 1", len(children))
	}
	child, _ := children[0].(map[string]any)
	if got, _ := child["path"].(string); got != "rules/naming" {
		t.Errorf("子の path = %q, want rules/naming", got)
	}
	// version は木のドラッグ&ドロップが If-Match に使う（10.2）。
	if v, ok := items[1]["version"].(float64); !ok || v != 1 {
		t.Errorf("rules の version = %v, want 1", items[1]["version"])
	}

	// ── ④ ?outline=1 と ?section= ────────────────────
	outline := getWithCookie(r, base+"?outline=1", session)
	if outline.Code != http.StatusOK {
		t.Fatalf("?outline=1 の status = %d（body=%s）", outline.Code, outline.Body.String())
	}
	rulesItem := docItems(t, outline)[1]
	sections, _ := rulesItem["outline"].([]any)
	if len(sections) != 2 {
		t.Fatalf("rules の outline = %v, want 2件", rulesItem["outline"])
	}

	sec := getWithCookie(r, base+"/rules?section=%E5%91%BD%E5%90%8D", session)
	if sec.Code != http.StatusOK {
		t.Fatalf("?section= の status = %d（body=%s）", sec.Code, sec.Body.String())
	}
	if got, _ := viewOf(t, sec)["body_md"].(string); got != "## 命名\n\n- 単数形" {
		t.Errorf("章の body_md = %q", got)
	}

	miss := getWithCookie(r, base+"/rules?section=nope", session)
	if miss.Code != http.StatusNotFound {
		t.Fatalf("無い章の status = %d, want 404（body=%s）", miss.Code, miss.Body.String())
	}
	if !strings.Contains(miss.Body.String(), "available_sections") {
		t.Errorf("404 に available_sections が無い: %s", miss.Body.String())
	}

	// ── ⑤ 一意制約（NULLS NOT DISTINCT がトップレベルでも効く）──
	//
	// **既定の UNIQUE では NULL どうしが別物**になるため、これが無いと
	// トップレベルで slug が重複できてしまう（DbDesign.md 8.1.1）。
	dup := postWithCookie(r, base, session, `{"slug":"rules","title":"別の規約"}`)
	if dup.Code != http.StatusConflict {
		t.Fatalf("重複 slug の status = %d, want 409（body=%s）", dup.Code, dup.Body.String())
	}
	if code := errorOf(t, dup).Code; code != "already_exists" {
		t.Errorf("code = %q, want already_exists", code)
	}
	// 親が違えば同じ slug を置ける。
	sameSlug := postWithCookie(r, base, session, `{"slug":"rules","title":"入れ子の規約","parent_path":"rules"}`)
	if sameSlug.Code != http.StatusCreated {
		t.Fatalf("親が違う同名 slug の status = %d, want 201（body=%s）", sameSlug.Code, sameSlug.Body.String())
	}

	// ── ⑥ 楽観ロックとリビジョン ───────────────────────
	//
	// **リビジョンは POST で1件できている**（10.4）。ここから内容を変える
	// PATCH を1回、変えない PATCH を1回入れて、履歴が1件だけ伸びることを見る。
	revs := getWithCookie(r, base+"/rules/_revisions", session)
	if revs.Code != http.StatusOK {
		t.Fatalf("履歴の status = %d（body=%s）", revs.Code, revs.Body.String())
	}
	if total := docTotal(t, revs); total != 1 {
		t.Fatalf("作成直後の履歴 = %d件, want 1（POST が revision_no=1 を作る）", total)
	}

	edited := patchProjectWithCookie(r, base+"/rules", session, `"1"`,
		`{"body_md":"直した本文","change_reason":"表現を直す"}`)
	if edited.Code != http.StatusOK {
		t.Fatalf("PATCH の status = %d（body=%s）", edited.Code, edited.Body.String())
	}
	if v, _ := viewOf(t, edited)["version"].(float64); v != 2 {
		t.Errorf("version = %v, want 2（どの更新でも +1）", viewOf(t, edited)["version"])
	}

	// 古い If-Match は 409 conflict（2.8 / 10.4）。
	stale := patchProjectWithCookie(r, base+"/rules", session, `"1"`, `{"title":"規約（改）"}`)
	if stale.Code != http.StatusConflict {
		t.Fatalf("古い If-Match の status = %d, want 409（body=%s）", stale.Code, stale.Body.String())
	}
	if code := errorOf(t, stale).Code; code != "conflict" {
		t.Errorf("code = %q, want conflict", code)
	}

	// sort_order だけの更新はリビジョンを作らないが、version は +1 する（10.4）。
	reordered := patchProjectWithCookie(r, base+"/rules", session, `"2"`, `{"sort_order":15}`)
	if reordered.Code != http.StatusOK {
		t.Fatalf("並べ替えの status = %d（body=%s）", reordered.Code, reordered.Body.String())
	}
	if v, _ := viewOf(t, reordered)["version"].(float64); v != 3 {
		t.Errorf("version = %v, want 3", viewOf(t, reordered)["version"])
	}

	revs2 := getWithCookie(r, base+"/rules/_revisions", session)
	if total := docTotal(t, revs2); total != 2 {
		t.Fatalf("履歴 = %d件, want 2（作成1 + 内容が変わった PATCH 1。並べ替えは積まない）", total)
	}
	firstRev := docItems(t, revs2)[0]
	if no, _ := firstRev["revision_no"].(float64); no != 2 {
		t.Errorf("先頭 = %v, want 2（revision_no の降順）", firstRev["revision_no"])
	}
	if reason, _ := firstRev["change_reason"].(string); reason != "表現を直す" {
		t.Errorf("change_reason = %q", reason)
	}

	// 本文つき1件（10.5）。**作成時の本文が revision 1 に残っている**ので戻せる。
	rev1 := getWithCookie(r, base+"/rules/_revisions/1", session)
	if rev1.Code != http.StatusOK {
		t.Fatalf("リビジョン1の status = %d（body=%s）", rev1.Code, rev1.Body.String())
	}
	if body, _ := viewOf(t, rev1)["body_md"].(string); !strings.Contains(body, "## 命名") {
		t.Errorf("リビジョン1の本文 = %q, want 作成時の本文", body)
	}
	if missing := getWithCookie(r, base+"/rules/_revisions/99", session); missing.Code != http.StatusNotFound {
		t.Errorf("無いリビジョンの status = %d, want 404", missing.Code)
	}

	// ── ⑦ _revisions は読み取り専用（10.1）──────────────
	notAllowed := patchProjectWithCookie(r, base+"/rules/_revisions", session, `"3"`, `{"title":"x"}`)
	if notAllowed.Code != http.StatusMethodNotAllowed {
		t.Fatalf("_revisions への PATCH = %d, want 405（body=%s）", notAllowed.Code, notAllowed.Body.String())
	}

	// ── ⑧ 移動と循環 ─────────────────────────────────
	//
	// **自分の子孫へは移せない**（10.4 の cycle）。DB は自己参照すら止めない
	// ので、ここが効かないと木が輪になる。
	cycle := patchProjectWithCookie(r, base+"/rules", session, `"3"`, `{"parent_path":"rules/naming"}`)
	if cycle.Code != http.StatusUnprocessableEntity {
		t.Fatalf("循環の status = %d, want 422（body=%s）", cycle.Code, cycle.Body.String())
	}
	if !hasDetail(errorOf(t, cycle), "parent_path", "cycle") {
		t.Errorf("details = %v, want parent_path/cycle", errorOf(t, cycle).Details)
	}

	// vision の下へ rules を部分木ごと移す。**子の path も付け替わる**（10.4）。
	moved := patchProjectWithCookie(r, base+"/rules", session, `"3"`, `{"parent_path":"vision"}`)
	if moved.Code != http.StatusOK {
		t.Fatalf("移動の status = %d（body=%s）", moved.Code, moved.Body.String())
	}
	if p, _ := viewOf(t, moved)["path"].(string); p != "vision/rules" {
		t.Errorf("移動後の path = %q, want vision/rules", p)
	}
	afterMove := getWithCookie(r, base+"/vision/rules/naming", session)
	if afterMove.Code != http.StatusOK {
		t.Fatalf("移動後の子の status = %d, want 200（body=%s）", afterMove.Code, afterMove.Body.String())
	}
	if p, _ := viewOf(t, afterMove)["parent_path"].(string); p != "vision/rules" {
		t.Errorf("移動後の子の parent_path = %q, want vision/rules", p)
	}

	// ── ⑨ 削除は部分木ごと（CASCADE。10.4 / DbDesign.md 8.1.1）──
	var beforeDocs, beforeRevs int
	countDocs(t, ctx, pool, rulesID, &beforeDocs, &beforeRevs)
	if beforeDocs == 0 || beforeRevs == 0 {
		t.Fatalf("削除前の件数が 0（docs=%d revs=%d）。検証にならない", beforeDocs, beforeRevs)
	}

	del := deleteWithCookie(r, base+"/vision/rules", session)
	if del.Code != http.StatusNoContent {
		t.Fatalf("削除の status = %d, want 204（body=%s）", del.Code, del.Body.String())
	}
	// 子（naming と入れ子の rules）も、リビジョンも消える。
	var afterDocs, afterRevs int
	countDocs(t, ctx, pool, rulesID, &afterDocs, &afterRevs)
	if afterDocs != 0 || afterRevs != 0 {
		t.Errorf("削除後に残っている: 文書=%d件 リビジョン=%d件", afterDocs, afterRevs)
	}
	if gone := getWithCookie(r, base+"/vision/rules/naming", session); gone.Code != http.StatusNotFound {
		t.Errorf("子の status = %d, want 404（部分木ごと消える）", gone.Code)
	}
}

// ── 補助 ────────────────────────────────────────────────────

// createDoc は文書を1件作り、その id を返す。sort_order の既定も確かめる。
func createDoc(
	t *testing.T, r http.Handler, base, session, body string, wantSortOrder float64,
) string {
	t.Helper()
	rec := postWithCookie(r, base, session, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("作成の status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
	}
	view := viewOf(t, rec)
	if got, _ := view["sort_order"].(float64); got != wantSortOrder {
		t.Errorf("sort_order = %v, want %v（同じ親の中の末尾 + 10）", view["sort_order"], wantSortOrder)
	}
	if view["created_by"] == nil {
		t.Error("created_by が null。作成者が入っていない")
	}
	id, _ := view["id"].(string)
	if id == "" {
		t.Fatalf("id が空（body=%s）", rec.Body.String())
	}
	return id
}

func docItems(t *testing.T, rec *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	raw, _ := viewOf(t, rec)["items"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, it := range raw {
		m, _ := it.(map[string]any)
		items = append(items, m)
	}
	return items
}

func docTotal(t *testing.T, rec *httptest.ResponseRecorder) int {
	t.Helper()
	total, _ := viewOf(t, rec)["total"].(float64)
	return int(total)
}

// countDocs は id を根とする部分木の文書数と、その全リビジョン数を数える。
func countDocs(
	t *testing.T, ctx context.Context, pool *pgxpool.Pool, rootID string, docs, revs *int,
) {
	t.Helper()
	const query = `
		WITH RECURSIVE subtree AS (
		  SELECT id FROM document WHERE id = $1
		  UNION
		  SELECT c.id FROM document c JOIN subtree s ON c.parent_id = s.id
		)
		SELECT (SELECT count(*) FROM subtree)::int,
		       (SELECT count(*) FROM document_revision r
		         WHERE r.document_id IN (SELECT id FROM subtree))::int`
	if err := pool.QueryRow(ctx, query, rootID).Scan(docs, revs); err != nil {
		t.Fatalf("件数を数えられない: %v", err)
	}
}
