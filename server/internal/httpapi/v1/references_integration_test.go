package v1

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// 外部参照API（ApiDesign.md 9.10.2）を**実際のDBに対して**通す。手順17c。
//
// 単体テストはフェイクを差し替えるので queries/reference.sql は一度も実行され
// ない。**ここでしか確かめられないものが6つある。**
//
//   - **DbDesign.md 6.12 の CHECK が実在すること**（ck_ticket_reference_code /
//     _doc）。アプリ側の検証を外しても DB が止めることを、直接 INSERT で見る
//   - UpdateTicketReference の CASE WHEN ..._set が「据え置き」と「NULL にする」を
//     実際に撃ち分けること（フェイクは Go 側で写しているだけで SQL を通らない）
//   - **並び順が kind → sort_order → created_at であること**（ORDER BY は DB のもの）
//   - NextTicketReferenceSortOrder が「最大値 + 10」を返すこと
//   - **チケットを消すと参照が CASCADE で消えること**（FK の挙動は DB のもの）
//   - activity が業務トランザクションと同時に確定すること
//
// PB_TEST_DATABASE_URL が無ければスキップする。実行は `make test-db`
// （Development.md 6.1。接続文字列を手で書かない）。
func TestTicketReferenceIntegration(t *testing.T) {
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
	adminEmail := "tref-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	session := loginAs(t, r, adminEmail)

	key := "tref-" + strings.ToLower(adminID[len(adminID)-8:])
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(),
			`DELETE FROM project WHERE key = $1`, key); err != nil {
			t.Errorf("プロジェクトの後始末に失敗した: %v", err)
		}
	})
	rec := postWithCookie(r, "/api/v1/projects", session,
		fmt.Sprintf(`{"key":%q,"name":"外部参照結合テスト","workflow_template":"simple"}`, key))
	if rec.Code != http.StatusCreated {
		t.Fatalf("プロジェクト作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	base := "/api/v1/projects/" + key

	rec = postWithCookie(r, base+"/tickets", session,
		`{"type":"task","title":"認証APIの実装"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("チケット作成の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	ticket := viewOf(t, rec)
	seq := int(ticket["seq"].(float64))
	ticketID := ticket["id"].(string)
	refs := fmt.Sprintf("%s/tickets/%d/references", base, seq)

	// **まず「効く」ことを確かめてから、断られる側を測る**（LEARNINGS #35）。
	var codeID, docID string

	t.Run("POST は code を作る", func(t *testing.T) {
		rec := postWithCookie(r, refs, session,
			`{"kind":"code","repository":"my-app","branch":"pb/31","commit_sha":"a1b2c3d",
			  "url":"https://example.com/c/a1b2c3d","label":"認証ハンドラを追加"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		codeID = v["id"].(string)
		if v["kind"] != "code" || v["repository"] != "my-app" {
			t.Errorf("応答 = %+v, want code / my-app", v)
		}
		// sort_order 省略時は末尾（1件目なので 10）。
		if got := v["sort_order"].(float64); got != 10 {
			t.Errorf("sort_order = %v, want 10", got)
		}
		// **created_by は返る**（9.10.2）。画面は使わないがデータには残す。
		by, _ := v["created_by"].(map[string]any)
		if by == nil || by["kind"] != "user" {
			t.Errorf("created_by = %+v, want kind=user", v["created_by"])
		}
	})

	t.Run("POST は doc を作り sort_order が最大値+10 になる", func(t *testing.T) {
		rec := postWithCookie(r, refs, session,
			`{"kind":"doc","url":"https://example.com/auth-design.md","label":"認証設計メモ"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		docID = v["id"].(string)
		if got := v["sort_order"].(float64); got != 20 {
			t.Errorf("sort_order = %v, want 20（NextTicketReferenceSortOrder）", got)
		}
		// 1つの表なので、doc は code 側の列が NULL になる。
		if v["repository"] != nil || v["branch"] != nil {
			t.Errorf("doc の repository/branch = %v/%v, want null/null",
				v["repository"], v["branch"])
		}
	})

	t.Run("GET は kind 昇順で返す", func(t *testing.T) {
		rec := getWithCookie(r, refs, session)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		items := viewOf(t, rec)["items"].([]any)
		if len(items) != 2 {
			t.Fatalf("items = %d件, want 2", len(items))
		}
		first := items[0].(map[string]any)
		second := items[1].(map[string]any)
		if first["kind"] != "code" || second["kind"] != "doc" {
			t.Errorf("並び = %v, %v, want code, doc", first["kind"], second["kind"])
		}
	})

	// **並び順の第2キーが効くこと**（9.10.2）。同じ kind の中は sort_order の昇順。
	t.Run("同じ kind の中は sort_order の昇順", func(t *testing.T) {
		rec := postWithCookie(r, refs, session,
			`{"kind":"code","repository":"my-app","commit_sha":"0000000","sort_order":5}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		extraID := viewOf(t, rec)["id"].(string)
		t.Cleanup(func() {
			deleteWithCookie(r, refs+"/"+extraID, session)
		})

		items := viewOf(t, getWithCookie(r, refs, session))["items"].([]any)
		if len(items) != 3 {
			t.Fatalf("items = %d件, want 3", len(items))
		}
		// sort_order=5 の code が先頭、次に sort_order=10 の code、最後に doc。
		if got := items[0].(map[string]any)["id"]; got != extraID {
			t.Errorf("先頭 = %v, want sort_order=5 の行", got)
		}
		if got := items[2].(map[string]any)["kind"]; got != "doc" {
			t.Errorf("末尾の kind = %v, want doc", got)
		}
	})

	t.Run("PATCH は送った項目だけを変える", func(t *testing.T) {
		rec := patchProjectWithCookie(r, refs+"/"+docID, session, "",
			`{"label":"認証設計メモ（改訂）"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		v := viewOf(t, rec)
		if v["label"] != "認証設計メモ（改訂）" {
			t.Errorf("label = %v, want 改訂後", v["label"])
		}
		// **url は送っていないので据え置き**（CASE WHEN url_set の据え置き側）。
		if v["url"] != "https://example.com/auth-design.md" {
			t.Errorf("url = %v, want 変わっていないこと", v["url"])
		}
	})

	t.Run("PATCH の null は項目を空にする", func(t *testing.T) {
		rec := patchProjectWithCookie(r, refs+"/"+codeID, session, "", `{"branch":null}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}
		if v := viewOf(t, rec); v["branch"] != nil {
			t.Errorf("branch = %v, want null", v["branch"])
		}
		// **同じ行の commit_sha は据え置き**——NULL にする側と据え置く側を1回で
		// 撃ち分ける。1件取得のエンドポイントは持たないので一覧から拾う。
		items := viewOf(t, getWithCookie(r, refs, session))["items"].([]any)
		for _, it := range items {
			row := it.(map[string]any)
			if row["id"] == codeID && row["commit_sha"] != "a1b2c3d" {
				t.Errorf("commit_sha = %v, want a1b2c3d（据え置き）", row["commit_sha"])
			}
		}
	})

	t.Run("PATCH は kind を拒む", func(t *testing.T) {
		rec := patchProjectWithCookie(r, refs+"/"+docID, session, "",
			`{"kind":"code","repository":"my-app"}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "immutable_field") {
			t.Errorf("details = %s, want immutable_field", rec.Body.String())
		}
	})

	t.Run("PATCH は必須項目を空にできない", func(t *testing.T) {
		rec := patchProjectWithCookie(r, refs+"/"+docID, session, "", `{"url":null}`)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), `"field":"url"`) {
			t.Errorf("details = %s, want url", rec.Body.String())
		}
	})

	// **CHECK が DB に実在することを、アプリを迂回して確かめる**（DbDesign.md 6.12）。
	// アプリ側の検証を将来ゆるめても、ここが最後の砦であることが読める。
	t.Run("CHECK が必須項目を守る", func(t *testing.T) {
		cases := []struct {
			name   string
			kind   string
			column string
		}{
			{"code に repository が無い", "code", "repository"},
			{"doc に url が無い", "doc", "url"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				_, err := pool.Exec(context.Background(),
					`INSERT INTO ticket_reference (id, ticket_id, kind) VALUES ($1, $2, $3)`,
					ulidgen.New(), ticketID, c.kind)
				if err == nil {
					t.Fatalf("%s の INSERT が通ってしまった", c.name)
				}
				if !strings.Contains(err.Error(), "ck_ticket_reference_") {
					t.Errorf("err = %v, want ck_ticket_reference_* 違反", err)
				}
			})
		}
	})

	// **kind の CHECK も見る**（'code' / 'doc' 以外は入らない）。
	t.Run("CHECK が未知の kind を弾く", func(t *testing.T) {
		_, err := pool.Exec(context.Background(),
			`INSERT INTO ticket_reference (id, ticket_id, kind, url) VALUES ($1, $2, 'spec', 'x')`,
			ulidgen.New(), ticketID)
		if err == nil {
			t.Fatal("kind='spec' の INSERT が通ってしまった")
		}
	})

	t.Run("activity は update として1行ずつ積まれる", func(t *testing.T) {
		rows := activityRows(t, pool, ticketID, "update")
		var code, doc int
		for _, a := range rows {
			switch a.field {
			case "reference.code":
				code++
			case "reference.doc":
				doc++
			}
		}
		// code: 作成2件（1件は sort_order=5 の後片付けで削除も入る）＋ branch を消した更新
		if code < 2 {
			t.Errorf("reference.code の行 = %d, want 2以上（%+v）", code, rows)
		}
		if doc < 2 {
			t.Errorf("reference.doc の行 = %d, want 2以上（作成とラベル変更。%+v）", doc, rows)
		}
		// **action='delete' を使っていないこと**——それは「チケットが消された」
		// を意味する（9.5.3）。ここまでで参照の削除は起きている。
		if got := activityRows(t, pool, ticketID, "delete"); len(got) != 0 {
			t.Errorf("action=delete の行 = %+v, want 0件", got)
		}
	})

	// **参照の増減は親チケットの version と updated_at を動かさない**（9.10.2）。
	t.Run("親チケットの version と updated_at が動かない", func(t *testing.T) {
		before := getTicketIT(t, r, session, base, seq)
		beforeVersion := before["version"].(float64)
		beforeUpdated := before["updated_at"].(float64)

		rec := postWithCookie(r, refs, session,
			`{"kind":"doc","url":"https://example.com/tmp.md"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		tmpID := viewOf(t, rec)["id"].(string)

		after := getTicketIT(t, r, session, base, seq)
		if got := after["version"].(float64); got != beforeVersion {
			t.Errorf("version = %v, want %v（参照は ticket の列を変えない）", got, beforeVersion)
		}
		if got := after["updated_at"].(float64); got != beforeUpdated {
			t.Errorf("updated_at = %v, want %v", got, beforeUpdated)
		}

		if rec := deleteWithCookie(r, refs+"/"+tmpID, session); rec.Code != http.StatusNoContent {
			t.Fatalf("後始末の削除 status = %d", rec.Code)
		}
	})

	// **詳細応答に同じものが入る**（9.5.1）。画面は別の GET を呼ばない。
	t.Run("詳細応答の references が一覧と一致する", func(t *testing.T) {
		detail := getTicketIT(t, r, session, base, seq)
		inDetail := detail["references"].([]any)
		inList := viewOf(t, getWithCookie(r, refs, session))["items"].([]any)
		if len(inDetail) != len(inList) {
			t.Fatalf("詳細 = %d件 / 一覧 = %d件, want 同数", len(inDetail), len(inList))
		}
		for i := range inDetail {
			a := inDetail[i].(map[string]any)
			b := inList[i].(map[string]any)
			if a["id"] != b["id"] {
				t.Errorf("%d番目 = %v / %v, want 同じ並び", i, a["id"], b["id"])
			}
		}
	})

	t.Run("DELETE は 204 を返し、消えたものは 404 になる", func(t *testing.T) {
		if rec := deleteWithCookie(r, refs+"/"+docID, session); rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204（body=%s）", rec.Code, rec.Body.String())
		}
		rec := deleteWithCookie(r, refs+"/"+docID, session)
		if rec.Code != http.StatusNotFound {
			t.Errorf("2回目の status = %d, want 404", rec.Code)
		}
	})

	// **他チケットの参照は 404 に寄せる**（ticket_id で絞る。Design.md 6.4.5）。
	t.Run("他チケットの参照は 404", func(t *testing.T) {
		rec := postWithCookie(r, base+"/tickets", session,
			`{"type":"task","title":"別のチケット"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("チケット作成の status = %d", rec.Code)
		}
		otherSeq := int(viewOf(t, rec)["seq"].(float64))

		got := deleteWithCookie(r,
			fmt.Sprintf("%s/tickets/%d/references/%s", base, otherSeq, codeID), session)
		if got.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404（body=%s）", got.Code, got.Body.String())
		}
	})

	// **チケットを消すと参照が CASCADE で消える**（DbDesign.md 6.12 の FK）。
	t.Run("チケットの削除で参照が CASCADE で消える", func(t *testing.T) {
		rec := postWithCookie(r, base+"/tickets", session,
			`{"type":"task","title":"消されるチケット"}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("チケット作成の status = %d", rec.Code)
		}
		v := viewOf(t, rec)
		doomedSeq := int(v["seq"].(float64))
		doomedID := v["id"].(string)

		got := postWithCookie(r,
			fmt.Sprintf("%s/tickets/%d/references", base, doomedSeq), session,
			`{"kind":"code","repository":"my-app"}`)
		if got.Code != http.StatusCreated {
			t.Fatalf("参照作成の status = %d（body=%s）", got.Code, got.Body.String())
		}
		if n := referenceCountOf(t, pool, doomedID); n != 1 {
			t.Fatalf("削除前の参照 = %d件, want 1", n)
		}

		if rec := deleteWithCookie(r,
			fmt.Sprintf("%s/tickets/%d", base, doomedSeq), session); rec.Code != http.StatusNoContent {
			t.Fatalf("チケット削除の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		if n := referenceCountOf(t, pool, doomedID); n != 0 {
			t.Errorf("削除後の参照 = %d件, want 0（ON DELETE CASCADE）", n)
		}
	})
}

// referenceCountOf は1チケットの外部参照の件数を直接数える。
//
// **API を通さない。** CASCADE は FK の挙動であり、チケットを消したあとに
// 参照を読むエンドポイントはもう 404 を返す（親が居ない）。表を直接見るしかない。
func referenceCountOf(t *testing.T, pool *pgxpool.Pool, ticketID string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM ticket_reference WHERE ticket_id = $1`, ticketID).Scan(&n); err != nil {
		t.Fatalf("参照の件数を読めない: %v", err)
	}
	return n
}
