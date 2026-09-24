package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/boyaki-machine/project-backyard/server/internal/auth"
	"github.com/boyaki-machine/project-backyard/server/internal/store"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/ulidgen"
)

// アクセストークン管理（ApiDesign.md 4.4）を**実際のDBに対して**通す。
//
// フェイクでは確かめられないものがここにある。
//
//   - **発行した平文が本当に Bearer 認証を通ること**（手順15b の完了条件そのもの）。
//     ハンドラ単体では「ハッシュを保存した」ところまでしか見えず、認証側が
//     同じハッシュで引けるかは実DBを通さないと分からない
//   - 失効したトークンが次のリクエストから 401 になること
//   - token_type='api' の絞り込みが効き、**セッションが一覧にも DELETE にも
//     現れない**こと
//   - 他人のトークンが 404 になること（WHERE の actor_id が効いているか）
//   - 上限5本を DB の実際の行数で数えていること
//   - パスワード変更（4.3）が API トークンも失効させること
//
// PB_TEST_DATABASE_URL が無ければスキップする（make test-db）。
func TestMeTokensIntegration(t *testing.T) {
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

	uniq := strings.ToLower(ulidgen.New())

	// newSelf はトークンを持つ利用者を1人作る。**テストごとに作る**——
	// 上限5本の判定が他のテストの発行本数に引きずられないようにする。
	newSelf := func(t *testing.T, label string) (string, string) {
		t.Helper()
		id := ulidgen.New()
		email := "tok-" + label + "-" + uniq + "@example.com"
		seedUserWithRole(t, ctx, pool, q, id, email, auth.SystemRoleOperator)
		return id, email
	}

	// issue は POST /me/tokens を叩き、応答を返す。
	issue := func(r http.Handler, session, body string) *httptest.ResponseRecorder {
		return bodyWithCookie(r, http.MethodPost, "/api/v1/me/tokens", session, body, "")
	}

	// ── 端から端まで（手順15b の完了条件）─────────────────────

	t.Run("発行した平文でBearer認証が通り、失効すると401になる", func(t *testing.T) {
		_, email := newSelf(t, "e2e")
		session := loginAs(t, r, email)

		rec := issue(r, session, `{"name":"CLI (結合テスト)","expires_in_days":90}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d, want 201（body=%s）", rec.Code, rec.Body.String())
		}
		var issued tokenJSON
		decodeJSONBody(t, rec, &issued)
		if issued.Token == "" {
			t.Fatal("token が空")
		}

		// **これが手順の完了条件である。** 発行された平文で GET /me が通る。
		me := bearerGet(r, "/api/v1/me", issued.Token)
		if me.Code != http.StatusOK {
			t.Fatalf("Bearer での GET /me status = %d, want 200（body=%s）",
				me.Code, me.Body.String())
		}

		// Bearer 認証は CSRF を要求しない（ApiDesign.md 2.4）。
		list := bearerGet(r, "/api/v1/me/tokens", issued.Token)
		if list.Code != http.StatusOK {
			t.Fatalf("Bearer での一覧 status = %d, want 200（body=%s）",
				list.Code, list.Body.String())
		}

		// 失効させる。
		del := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/tokens/"+issued.ID, session, "", "")
		if del.Code != http.StatusNoContent {
			t.Fatalf("失効の status = %d, want 204（body=%s）", del.Code, del.Body.String())
		}

		// **次のリクエストから 401。**
		after := bearerGet(r, "/api/v1/me", issued.Token)
		if after.Code != http.StatusUnauthorized {
			t.Fatalf("失効後の Bearer status = %d, want 401（body=%s）",
				after.Code, after.Body.String())
		}
	})

	// ── GET /me/tokens（4.4.1）─────────────────────────────────

	t.Run("一覧はapiトークンだけを返し、セッションを含まない", func(t *testing.T) {
		_, email := newSelf(t, "list")
		session := loginAs(t, r, email)

		if rec := issue(r, session, `{"name":"一覧の1本","expires_in_days":30}`); rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d（body=%s）", rec.Code, rec.Body.String())
		}

		items := listTokens(t, r, session)
		if len(items) != 1 {
			t.Fatalf("items = %d件, want 1（セッションが混ざっていないか）", len(items))
		}
		got := items[0]
		if got.Name != "一覧の1本" {
			t.Errorf("name = %q", got.Name)
		}
		if !strings.HasPrefix(got.TokenPrefix, auth.APITokenPrefix) {
			t.Errorf("token_prefix = %q, want %q で始まる", got.TokenPrefix, auth.APITokenPrefix)
		}
		if got.LastUsedAt != nil {
			t.Errorf("last_used_at = %v, want null（一度も使っていない）", got.LastUsedAt)
		}
		if got.Status != tokenStatusActive {
			t.Errorf("status = %q, want active", got.Status)
		}
	})

	t.Run("失効済みは一覧に出ず、期限切れは出る", func(t *testing.T) {
		actorID, email := newSelf(t, "states")
		session := loginAs(t, r, email)

		revoked := issue(r, session, `{"name":"失効させる","expires_in_days":30}`)
		if revoked.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d（body=%s）", revoked.Code, revoked.Body.String())
		}
		var revokedTok tokenJSON
		decodeJSONBody(t, revoked, &revokedTok)
		if del := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/tokens/"+revokedTok.ID, session, "", ""); del.Code != http.StatusNoContent {
			t.Fatalf("失効の status = %d（body=%s）", del.Code, del.Body.String())
		}

		// 期限切れは API から作れない（1〜365日）。**DBへ直接入れる**——
		// 「期限切れをどう見せるか」の確認であり、値域の検証とは別の話である。
		expiredID := ulidgen.New()
		if _, err := pool.Exec(ctx, `
			INSERT INTO access_token (id, actor_id, token_type, token_hash, token_prefix,
			                          name, scopes, expires_at)
			VALUES ($1, $2, 'api', $3, 'pb_api_x', '期限切れ', '[]'::jsonb, now() - interval '1 day')`,
			expiredID, actorID, "expired-"+expiredID); err != nil {
			t.Fatalf("期限切れのトークンを入れられない: %v", err)
		}

		items := listTokens(t, r, session)
		if len(items) != 1 {
			t.Fatalf("items = %d件, want 1（失効済みが混ざっていないか）%+v", len(items), items)
		}
		if items[0].ID != expiredID {
			t.Fatalf("残った行 = %q, want %q（期限切れ）", items[0].ID, expiredID)
		}
		if items[0].Status != tokenStatusExpired {
			t.Errorf("status = %q, want expired", items[0].Status)
		}
	})

	// ── POST /me/tokens（4.4.2）────────────────────────────────

	t.Run("上限5本を超えると409", func(t *testing.T) {
		_, email := newSelf(t, "limit")
		session := loginAs(t, r, email)

		for i := 1; i <= maxAPITokensPerActor; i++ {
			rec := issue(r, session, `{"name":"上限の検証","expires_in_days":30}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("%d本目の status = %d, want 201（body=%s）", i, rec.Code, rec.Body.String())
			}
		}

		over := issue(r, session, `{"name":"6本目","expires_in_days":30}`)
		if over.Code != http.StatusConflict {
			t.Fatalf("%d本目の status = %d, want 409（body=%s）",
				maxAPITokensPerActor+1, over.Code, over.Body.String())
		}

		// **1本失効させれば枠が空く。** 上限が「失効していないもの」を
		// 数えていることの確認である。
		items := listTokens(t, r, session)
		if len(items) != maxAPITokensPerActor {
			t.Fatalf("items = %d件, want %d", len(items), maxAPITokensPerActor)
		}
		if del := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/tokens/"+items[0].ID, session, "", ""); del.Code != http.StatusNoContent {
			t.Fatalf("失効の status = %d（body=%s）", del.Code, del.Body.String())
		}
		again := issue(r, session, `{"name":"枠が空いた","expires_in_days":30}`)
		if again.Code != http.StatusCreated {
			t.Fatalf("1本失効させた後の status = %d, want 201（body=%s）",
				again.Code, again.Body.String())
		}
	})

	t.Run("スコープは権限キーだけを受け付ける", func(t *testing.T) {
		_, email := newSelf(t, "scopes")
		session := loginAs(t, r, email)

		ok := issue(r, session,
			`{"name":"読み取り専用","expires_in_days":30,"scopes":["ticket.view","project.view"]}`)
		if ok.Code != http.StatusCreated {
			t.Fatalf("権限キーの status = %d, want 201（body=%s）", ok.Code, ok.Body.String())
		}

		// 権限キーではない語彙。権限カタログに無いので 422。
		ng := issue(r, session,
			`{"name":"旧語彙","expires_in_days":30,"scopes":["ticket:read"]}`)
		if ng.Code != http.StatusUnprocessableEntity {
			t.Fatalf("旧語彙の status = %d, want 422（body=%s）", ng.Code, ng.Body.String())
		}

		items := listTokens(t, r, session)
		if len(items) != 1 {
			t.Fatalf("items = %d件, want 1（422 の分が入っていないか）", len(items))
		}
		if len(items[0].Scopes) != 2 {
			t.Errorf("scopes = %v, want 2件", items[0].Scopes)
		}
	})

	// ── DELETE /me/tokens/{id}（4.4.3）─────────────────────────

	t.Run("他人のトークンは404で、失効もされない", func(t *testing.T) {
		_, ownerEmail := newSelf(t, "owner")
		_, otherEmail := newSelf(t, "other")
		ownerSession := loginAs(t, r, ownerEmail)
		otherSession := loginAs(t, r, otherEmail)

		rec := issue(r, ownerSession, `{"name":"持ち主のトークン","expires_in_days":30}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var owned tokenJSON
		decodeJSONBody(t, rec, &owned)

		del := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/tokens/"+owned.ID, otherSession, "", "")
		if del.Code != http.StatusNotFound {
			t.Fatalf("他人による失効の status = %d, want 404（body=%s）",
				del.Code, del.Body.String())
		}

		// **本当に失効していないこと。** 404 を返しつつ UPDATE が走っていたら
		// 意味が無い。
		if items := listTokens(t, r, ownerSession); len(items) != 1 {
			t.Fatalf("持ち主の items = %d件, want 1（他人に失効させられた）", len(items))
		}
	})

	t.Run("失効は冪等で、2回目も204", func(t *testing.T) {
		_, email := newSelf(t, "idem")
		session := loginAs(t, r, email)

		rec := issue(r, session, `{"name":"2回切る","expires_in_days":30}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var tok tokenJSON
		decodeJSONBody(t, rec, &tok)

		readRevokedAt := func() time.Time {
			t.Helper()
			var at time.Time
			if err := pool.QueryRow(ctx,
				`SELECT revoked_at FROM access_token WHERE id = $1`, tok.ID).Scan(&at); err != nil {
				t.Fatalf("revoked_at を読めない: %v", err)
			}
			return at
		}

		// **1回目の値を控えてから2回目を叩く。** 控えないと「上書きされて
		// いない」を測れない——現在時刻と比べるだけでは、上書きされていても
		// 通ってしまう。
		del1 := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/tokens/"+tok.ID, session, "", "")
		if del1.Code != http.StatusNoContent {
			t.Fatalf("1回目の失効 status = %d, want 204（body=%s）", del1.Code, del1.Body.String())
		}
		first := readRevokedAt()
		if first.IsZero() {
			t.Fatal("1回目の失効で revoked_at が入っていない")
		}

		del2 := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/tokens/"+tok.ID, session, "", "")
		if del2.Code != http.StatusNoContent {
			t.Fatalf("2回目の失効 status = %d, want 204（body=%s）", del2.Code, del2.Body.String())
		}
		if second := readRevokedAt(); !second.Equal(first) {
			t.Errorf("revoked_at が上書きされた: %v -> %v", first, second)
		}

		// **監査ログは1件だけ。** 冪等な2回目で二重に記録しない。
		var n int
		if err := pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE action = 'token.revoke' AND target_id = $1`,
			tok.ID).Scan(&n); err != nil {
			t.Fatalf("監査ログを数えられない: %v", err)
		}
		if n != 1 {
			t.Errorf("token.revoke = %d件, want 1", n)
		}
	})

	t.Run("セッションのIDを渡しても404", func(t *testing.T) {
		actorID, email := newSelf(t, "session")
		session := loginAs(t, r, email)

		var sessionID string
		if err := pool.QueryRow(ctx,
			`SELECT id FROM access_token WHERE actor_id = $1 AND token_type = 'session'`,
			actorID).Scan(&sessionID); err != nil {
			t.Fatalf("セッションの ID を読めない: %v", err)
		}

		del := bodyWithCookie(r, http.MethodDelete,
			"/api/v1/me/tokens/"+sessionID, session, "", "")
		if del.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404（body=%s）", del.Code, del.Body.String())
		}

		// 現在のセッションが生きていること（この経路では切れない）。
		if me := getWithCookie(r, "/api/v1/me", session); me.Code != http.StatusOK {
			t.Fatalf("セッションが切れた: status = %d", me.Code)
		}
	})

	// ── 4.3 との関係 ───────────────────────────────────────────

	t.Run("パスワードを変えるとAPIトークンも失効する", func(t *testing.T) {
		_, email := newSelf(t, "pwchange")
		session := loginAs(t, r, email)

		rec := issue(r, session, `{"name":"パスワード変更で切れる","expires_in_days":30}`)
		if rec.Code != http.StatusCreated {
			t.Fatalf("発行の status = %d（body=%s）", rec.Code, rec.Body.String())
		}
		var tok tokenJSON
		decodeJSONBody(t, rec, &tok)

		const newPassword = "another-quiet-harbor-8241"
		pw := bodyWithCookie(r, http.MethodPost, "/api/v1/me/password", session,
			`{"current_password":"`+testPassword+`","new_password":"`+newPassword+`"}`, "")
		if pw.Code != http.StatusNoContent {
			t.Fatalf("パスワード変更の status = %d, want 204（body=%s）", pw.Code, pw.Body.String())
		}

		// **RevokeMyOtherSessions は token_type で絞らない**（me.sql）。
		// ブラウザだけ切って Bearer を残す理由が無い、という判断の確認である。
		after := bearerGet(r, "/api/v1/me", tok.Token)
		if after.Code != http.StatusUnauthorized {
			t.Fatalf("パスワード変更後の Bearer status = %d, want 401（body=%s）",
				after.Code, after.Body.String())
		}
		// 現在のセッションは残る（4.3）。
		if me := getWithCookie(r, "/api/v1/me", session); me.Code != http.StatusOK {
			t.Fatalf("現在のセッションまで切れた: status = %d", me.Code)
		}
	})
}

// ── 結合テストの小道具（4.4）──────────────────────────────────

// bearerGet は Authorization: Bearer で GET する（ApiDesign.md 2.3）。
//
// **CSRF を付けない。** Bearer 認証では要求されないこと（2.4）が、
// この呼び出しが通ること自体で確かめられる。
func bearerGet(r http.Handler, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// listTokens は GET /me/tokens を引いて items を返す。
func listTokens(t *testing.T, r http.Handler, session string) []tokenJSON {
	t.Helper()
	rec := getWithCookie(r, "/api/v1/me/tokens", session)
	if rec.Code != http.StatusOK {
		t.Fatalf("一覧の status = %d（body=%s）", rec.Code, rec.Body.String())
	}
	var got tokenListJSON
	decodeJSONBody(t, rec, &got)
	return got.Items
}

// decodeJSONBody は応答本文を dst へ読む。
//
// **応答の型（accessTokenView 等）をそのまま使わない。** v1.Time は
// MarshalJSON だけを持つため読み戻せない（me_tokens_test.go の tokenJSON）。
func decodeJSONBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("応答を読めない: %v（body=%s）", err, rec.Body.String())
	}
}
