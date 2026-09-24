package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// GET /roles と GET /permissions（ApiDesign.md 7.1 / 7.2）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - シード（DbDesign.md 7.2 / 7.3）が本当に5ロールで、**権限カタログが
//     permission 表の全行を返す**こと
//   - scope の絞り込みが role.scope に効き、**2本のクエリが同じ絞り込みを使う**こと
//     （片方だけ絞ると、返らないロールの権限が応答に混ざる）
//   - permissions[] の並びが permission.sort_order であること
//   - ?scope=project だけがオペレータでも通り、他は 403 になること
//
// PB_TEST_DATABASE_URL が無ければスキップする。
//
//	PB_TEST_DATABASE_URL='postgres://pb_app:...@127.0.0.1:5432/pb' go test ./internal/httpapi/v1/ -run Integration -v
//
// permissionCatalogSize は permission 表の行数を返す。
//
// **期待値を決め打ちしない**（憲章「期待値の作り方」）。権限カタログの正本は
// マイグレーションが入れるこの表であり（DbDesign.md 7.2 / 8.1.4 ほか）、
// 件数を書き下すと**正本を直した日に検査が嘘になる**。0027 を足したときに
// 実際そうなった。
//
// **0件なら落とす。** これが無いと、マイグレーションが当たっていない環境で
// `0 == 0` が成立して**素通りする**——「ゼロになる」を測るなら始点が意味を
// 持つことを先に確かめる、という同じ話である。
func permissionCatalogSize(t *testing.T, ctx context.Context, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM permission`).Scan(&n); err != nil {
		t.Fatalf("権限カタログの件数を読めない: %v", err)
	}
	if n == 0 {
		t.Fatal("permission 表が空である（マイグレーションが当たっていない）")
	}
	return n
}

func TestRolesCatalogIntegration(t *testing.T) {
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
	adminEmail := "role-admin-" + adminID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, adminID, adminEmail, auth.SystemRoleAdministrator)
	operatorID := ulidgen.New()
	operatorEmail := "role-op-" + operatorID + "@example.com"
	seedUserWithRole(t, ctx, pool, q, operatorID, operatorEmail, auth.SystemRoleOperator)

	// **loginAs ではなく loginAsWith を使う。** 擬似 IP を1回ごとに変え、
	// ログインの IP 単位レート制限（2.9、10回/分）を他の結合テストと奪い合わない。
	adminSession := loginAsWith(t, r, adminEmail, testPassword)
	operatorSession := loginAsWith(t, r, operatorEmail, testPassword)

	roles := func(t *testing.T, token, query string) []roleView {
		t.Helper()
		rec := getWithCookie(r, "/api/v1/roles"+query, token)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /roles%s の status = %d（body=%s）", query, rec.Code, rec.Body.String())
		}
		var body struct {
			Items []roleView `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答が JSON でない: %v", err)
		}
		return body.Items
	}

	t.Run("シードの5ロールが sort_order の順で返る", func(t *testing.T) {
		items := roles(t, adminSession, "")

		// 並びと表示名の正本は DbDesign.md 7.3 のシード。
		want := []struct{ key, scope, name string }{
			{"operator", "system", "オペレータ"},
			{"administrator", "system", "アドミニストレータ"},
			{"project_admin", "project", "プロジェクト管理者"},
			{"project_member", "project", "メンバー"},
			{"project_viewer", "project", "閲覧者"},
		}
		if len(items) != len(want) {
			t.Fatalf("ロール = %d件, want %d件（%+v）", len(items), len(want), items)
		}
		for i, w := range want {
			if items[i].Key != w.key || items[i].Scope != w.scope || items[i].DisplayName != w.name {
				t.Errorf("items[%d] = {%s %s %s}, want {%s %s %s}",
					i, items[i].Key, items[i].Scope, items[i].DisplayName, w.key, w.scope, w.name)
			}
			if !items[i].IsBuiltin {
				t.Errorf("%s の is_builtin = false。Phase 1 は組み込みのみ", w.key)
			}
		}
	})

	t.Run("権限カタログは permission 表の全行で category と description を持つ", func(t *testing.T) {
		rec := getWithCookie(r, "/api/v1/permissions", adminSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var body struct {
			Items []permissionItem `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答が JSON でない: %v", err)
		}
		// **件数を書き下さない**（憲章「期待値の作り方」）。カタログの
		// 正本はマイグレーションが入れる permission 表であり、0010 以降も
		// 0017（doc.view / doc.edit）・0019（agent 系）・0027
		// （ticket.reference.edit）と増え続ける。**数を書くと、正本を直した日に
		// 嘘になる**——実際 0027 を足したとき、この検査は直されずに落ちていた。
		//
		// **表と突き合わせるのは、APIとは別の経路だからである。** これで
		// 「30と書いてある」ではなく「**全行を返しているか**」を測れる。
		want := permissionCatalogSize(t, ctx, pool)
		if len(body.Items) != want {
			t.Fatalf("権限 = %d件, want %d件（permission 表の全行）", len(body.Items), want)
		}
		if body.Items[0].Key != "project.view" {
			t.Errorf("先頭 = %q, want project.view（sort_order 10）", body.Items[0].Key)
		}
		for _, p := range body.Items {
			if p.Category == "" || p.Description == "" {
				t.Errorf("%s に category / description が無い: %+v", p.Key, p)
			}
		}
	})

	t.Run("permissions は permission.sort_order の順で並ぶ", func(t *testing.T) {
		// **並びの正本は GET /permissions の応答**と突き合わせる。検証側で
		// 並べ直すと、実装ではなく自分の思い込みを測ることになる。
		rec := getWithCookie(r, "/api/v1/permissions", adminSession)
		var perms struct {
			Items []permissionItem `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &perms); err != nil {
			t.Fatalf("権限一覧を読めない: %v", err)
		}
		rank := make(map[string]int, len(perms.Items))
		for i, p := range perms.Items {
			rank[p.Key] = i
		}

		for _, role := range roles(t, adminSession, "") {
			prev := -1
			for _, key := range role.Permissions {
				at, ok := rank[key]
				if !ok {
					t.Errorf("%s の権限 %q がカタログに無い", role.Key, key)
					continue
				}
				if at <= prev {
					t.Errorf("%s の permissions が sort_order 順でない: %v", role.Key, role.Permissions)
					break
				}
				prev = at
			}
		}
	})

	t.Run("administrator は全権限を持ち project_viewer は閲覧のみ", func(t *testing.T) {
		byKey := map[string]roleView{}
		for _, role := range roles(t, adminSession, "") {
			byKey[role.Key] = role
		}
		// 同じ副検査の後半で project_viewer の期待値に `want` を使うので、
		// こちらは別の名前にする。
		wantAll := permissionCatalogSize(t, ctx, pool)
		if got := len(byKey["administrator"].Permissions); got != wantAll {
			t.Errorf("administrator の権限 = %d件, want %d件（DbDesign.md 7.3 は全権限）",
				got, wantAll)
		}
		// 閲覧者は5件——DbDesign.md 7.3 の3件に、8.1.4 が doc.view、
		// 8.2.6（0019）が agent.run を足した。並びは permission.sort_order で
		// 10 / 20 / 35 / 40 / 62。
		//
		// **agent.run が閲覧者にも要る。** エージェントの権限は所有者から導かれる
		// （Design.md 6.5 の委譲）ので、閲覧者が所有するエージェントも MCP を
		// 走らせられなければならない（読むだけのエージェント。Requirements.md 10.9.1）。
		want := []string{"project.view", "ticket.view", "doc.view", "knowledge.view", "agent.run"}
		got := byKey["project_viewer"].Permissions
		if len(got) != len(want) {
			t.Fatalf("project_viewer の権限 = %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("project_viewer の権限 = %v, want %v", got, want)
				break
			}
		}
	})

	t.Run("scope の絞り込みが2本のクエリに同じく効く", func(t *testing.T) {
		system := roles(t, adminSession, "?scope=system")
		if len(system) != 2 {
			t.Fatalf("scope=system = %d件, want 2件", len(system))
		}
		for _, role := range system {
			if role.Scope != "system" {
				t.Errorf("scope=system に %s（scope=%s）が混ざっている", role.Key, role.Scope)
			}
			// **権限も絞られていること。** ListRolePermissionAssignments の WHERE が
			// ListRoles と食い違うと、返らないロールの権限だけが応答から漏れる／混ざる。
			if len(role.Permissions) == 0 {
				t.Errorf("%s の permissions が空。絞り込みが権限側にも効いているか", role.Key)
			}
		}

		project := roles(t, adminSession, "?scope=project")
		if len(project) != 3 {
			t.Fatalf("scope=project = %d件, want 3件", len(project))
		}
		for _, role := range project {
			if role.Scope != "project" {
				t.Errorf("scope=project に %s（scope=%s）が混ざっている", role.Key, role.Scope)
			}
		}
	})

	t.Run("オペレータは scope=project だけ通る", func(t *testing.T) {
		// 開放しているのは ?scope=project だけである（7.1）。
		items := roles(t, operatorSession, "?scope=project")
		if len(items) != 3 {
			t.Errorf("オペレータの scope=project = %d件, want 3件", len(items))
		}
		if items[0].DisplayName != "プロジェクト管理者" {
			t.Errorf("表示名 = %q, want プロジェクト管理者", items[0].DisplayName)
		}

		for _, path := range []string{
			"/api/v1/roles",
			"/api/v1/roles?scope=system",
			"/api/v1/roles?scope=all",
		} {
			rec := getWithCookie(r, path, operatorSession)
			if rec.Code != http.StatusForbidden {
				t.Errorf("オペレータの %s = %d, want 403（body=%s）", path, rec.Code, rec.Body.String())
			}
		}
	})

	// **権限カタログは user.manage を要さない**（ApiDesign.md 7.2）。
	//
	// 消費者が2つになったための開放である——GuiDesign.md 5.6.3 の権限マトリクスと、
	// 5.8.2 のエージェント用トークンの発行結果。**後者の必要権限は「本人」**で、
	// user.manage を持たない利用者がスコープの description を引く。
	//
	// **description まで見る。** 200 が返るだけでは足りず、5.8.2 が必要とするのは
	// 「権限キーを日本語にする」ことなので、そこが空でないことまで測る。
	t.Run("権限カタログはオペレータでも読める", func(t *testing.T) {
		rec := getWithCookie(r, "/api/v1/permissions", operatorSession)
		if rec.Code != http.StatusOK {
			t.Fatalf("オペレータの /permissions = %d, want 200（body=%s）", rec.Code, rec.Body.String())
		}

		var got struct {
			Items []struct {
				Key         string `json:"key"`
				Description string `json:"description"`
			} `json:"items"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("応答を読めない: %v", err)
		}
		if len(got.Items) == 0 {
			t.Fatal("権限カタログが空である")
		}

		// エージェント用トークンの既定スコープ（Design.md 6.5）がすべて
		// カタログに在り、description を持つことを見る。**ここが欠けると
		// 5.8.2 の発行結果が権限キーのまま出る。**
		desc := map[string]string{}
		for _, it := range got.Items {
			desc[it.Key] = it.Description
		}
		for _, key := range agentDefaultScopes {
			if desc[key] == "" {
				t.Errorf("既定スコープ %q の description が空（カタログ %d件）", key, len(got.Items))
			}
		}
	})

	t.Run("解釈できない scope は 422", func(t *testing.T) {
		// **`all` も値域の外である**（7.1）。全件は「未指定」で表す。
		// 6.1 の kind が all を明示的な値として受けるのとは違うので、
		// 取り違えていないことをここで固定する。
		if rec := getWithCookie(r, "/api/v1/roles?scope=all", adminSession); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("scope=all の status = %d, want 422（値域は system / project）", rec.Code)
		}

		rec := getWithCookie(r, "/api/v1/roles?scope=SYSTEM", adminSession)
		if rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422（body=%s）", rec.Code, rec.Body.String())
		}
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Details []struct {
					Field string `json:"field"`
				} `json:"details"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("応答が JSON でない: %v", err)
		}
		if body.Error.Code != "validation_failed" {
			t.Errorf("code = %q, want validation_failed", body.Error.Code)
		}
		if len(body.Error.Details) == 0 || body.Error.Details[0].Field != "scope" {
			t.Errorf("details = %+v, want field=scope", body.Error.Details)
		}

		// **権限を持たない呼び出し元には 403 が先に返る**（7.1 の但し書き）。
		// 認可がミドルウェアで、値の検証がハンドラだからである。
		if rec := getWithCookie(r, "/api/v1/roles?scope=SYSTEM", operatorSession); rec.Code != http.StatusForbidden {
			t.Errorf("オペレータの不正な scope = %d, want 403", rec.Code)
		}
	})

	t.Run("未認証は 401", func(t *testing.T) {
		// ?scope=project は権限を要さないが、**認証は要る**（7.1）。
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/roles?scope=project", nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401（body=%s）", rec.Code, rec.Body.String())
		}
	})
}

// roleView は結合テストが読む 7.1 の items[] 要素。
type roleView struct {
	Key         string   `json:"key"`
	Scope       string   `json:"scope"`
	DisplayName string   `json:"display_name"`
	Description *string  `json:"description"`
	IsBuiltin   bool     `json:"is_builtin"`
	SortOrder   int32    `json:"sort_order"`
	Permissions []string `json:"permissions"`
}
