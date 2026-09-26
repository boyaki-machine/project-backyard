package v1

import (
	"context"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/boyaki-machine/project-backyard/server/internal/config"
	"github.com/boyaki-machine/project-backyard/server/internal/httpapi/middleware"
	"github.com/boyaki-machine/project-backyard/server/internal/maintenance"
	"github.com/boyaki-machine/project-backyard/server/internal/store/gen"
	"github.com/boyaki-machine/project-backyard/server/internal/tlscert"
)

// レート制限の上限と窓（ApiDesign.md 2.9）。
//
// 表のもう1行「アカウントあたり 5回/15分（超過で account_locked）」は
// Design.md 6.3 のロックそのもので、local_credential の failed_attempts /
// locked_until として login.go に実装済みである。
const (
	loginRateLimit  = 10
	loginRateWindow = time.Minute
	actorRateLimit  = 600
	actorRateWindow = time.Minute
)

// TxRunner は複数の書き込みを1トランザクションで実行する。
//
// 実装は store.NewTxRunner（プール）。**インターフェースとして受け取るのは、
// ハンドラのテストが実DBを立てずに差し替えられるようにするため**であり、
// Queries を gen.Querier で受けているのと同じ理由による。
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(gen.Querier) error) error
}

// Deps は /api/v1 のハンドラが必要とする外部資源と設定。
type Deps struct {
	// Queries は sqlc の問い合わせ口。
	Queries gen.Querier

	// Tx はトランザクションの実行口。POST /projects（ApiDesign.md 5.3）のように
	// 複数の書き込みが不可分である操作が使う。
	Tx TxRunner

	// Settings は実行中の設定（Design.md 10.3）。**nil なら既定値だけの
	// Live を組む**——設定を渡さないテストが、レジストリの既定で動くように。
	Settings *config.Live

	// OnSettingsChanged は設定が変わったときに呼ばれる（任意）。
	OnSettingsChanged func(*config.Set)

	// Certs は出す TLS 証明書の入れ物（Design.md 6.6.1）。
	Certs *tlscert.Holder
	// TLSListening は実際に TLS で待ち受けているか。
	TLSListening bool
	// ListenURL は実際に待ち受けているスキームとアドレス（ApiDesign.md 11.4）。
	ListenURL string

	// DBStats は DB の接続状態と統計を読む口（ApiDesign.md 11.10）。
	// **nil なら GET /admin/database は 500 を返す。**
	DBStats DatabaseStats

	// Backups は PB 全体の書き出しと取り込みの口（ApiDesign.md 11.11〜11.12）。
	// **nil なら 500 を返す。**
	Backups Backups

	// Maintenance は保守モードの旗（Design.md 10.4）。**取り込みが
	// 自分で立てて自分で降ろす。** nil なら保守モードに入らない。
	Maintenance *maintenance.Flag
}

// Mount は /api/v1 のルートを r に並べる。
//
// **必要権限はルート定義に宣言する**（Design.md 6.4.4）。本関数を眺めるだけで
// 全エンドポイントの認証・認可要件が読み取れる状態を保つこと。
//
//	middleware.RequirePermission(deps.Queries, "user.manage")        システムロール層
//	middleware.RequireProjectPermission(deps.Queries, "ticket.close") プロジェクト層（{key} が要る）
//	middleware.RequirePermissionUnlessQuery(deps.Queries, "user.manage", "scope", "project")
//	                                        クエリの値で素通しする例外（7.1 の GET /roles だけ）
//
// 認証・自分自身の3本（ApiDesign.md 3.1 / 3.2 / 4.1）に .With(...) が
// 付いていないのは、いずれも「必要権限：不要」または「認証済み・本人」で
// あり、権限キーを要求しないためである。
func Mount(r chi.Router, deps Deps) {
	settings := deps.Settings
	if settings == nil {
		settings = config.LiveDefaults()
	}
	h := &handler{
		q: deps.Queries, tx: deps.Tx, settings: settings,
		onSettingsChanged: deps.OnSettingsChanged,
		certs:             deps.Certs,
		tlsListening:      deps.TLSListening,
		listenURL:         deps.ListenURL,
		dbStats:           deps.DBStats,
		backups:           deps.Backups,
		maintenance:       deps.Maintenance,
	}

	// ── 認証不要 ────────────────────────────────
	// ログインは認証を通れない状態で叩くもののため、認証必須グループの外に置く。
	// CSRF（2.4）の対象にもならない。Cookie 認証ではないためである。
	// IP 単位のレート制限だけを掛ける（2.9）。
	r.With(middleware.RateLimit(loginRateLimit, loginRateWindow, middleware.ClientIPKey)).
		Post("/auth/login", h.login)

	// ログインの第2要素（ApiDesign.md 3.4）。**認証不要のまま置く**
	// ——挑戦トークンが本人であることの証明を兼ねるので、Cookie も Bearer も
	// 持たない状態で叩かれる。CSRF（2.4）の対象にもならない。
	r.With(middleware.RateLimit(loginRateLimit, loginRateWindow, middleware.ClientIPKey)).
		Post("/auth/login/mfa", h.loginMFA)

	// パスキーでのログイン（ApiDesign.md 3.5 / 3.6）。**認証不要のまま置く**
	// ——パスワードの代わりに本人を特定する手段であり、Cookie も Bearer も持たない
	// 状態で叩かれる。**アカウント単位の制限もロックも持たない**（2.9）。
	r.With(middleware.RateLimit(loginRateLimit, loginRateWindow, middleware.ClientIPKey)).
		Post("/auth/passkey/options", h.startPasskeyLogin)
	r.With(middleware.RateLimit(loginRateLimit, loginRateWindow, middleware.ClientIPKey)).
		Post("/auth/login/passkey", h.loginPasskey)

	// ── 認証必須 ────────────────────────────────
	// Cookie か Bearer での認証を必須とする（Design.md 6.2.2）。
	//
	// 連鎖の順は 認証 → レート制限 → CSRF とする。レート制限を CSRF より
	// 前に置くのは、CSRF に失敗し続けるリクエストも「送られた回数」として
	// 数えるためである。どちらもアクターが確定していないと判定できないので、
	// 認証より前には置けない。
	r.Group(func(r chi.Router) {
		r.Use(middleware.Authenticate(deps.Queries))
		r.Use(middleware.RateLimit(actorRateLimit, actorRateWindow, middleware.ActorKey))
		r.Use(middleware.RequireCSRF)

		r.Post("/auth/logout", h.logout)

		// ── 自分自身（ApiDesign.md 4章）────────────────────────
		//
		// **4章は権限キーを要求しない。** GET /me 以外は人間のブラウザ
		// セッションに限定し、制限付きトークンから資格情報を作れないようにする。
		// 対象が常に自分自身なので、誰のアカウントを触るかで必要権限が
		// 変わることがない。オペレータでもアドミニストレータでも同じ経路を
		// 通り、触れる範囲はハンドラが p.ActorID で閉じている。
		//
		// **6章（管理者によるユーザー管理）と混ぜない。** あちらは他人を
		// 触るため user.manage を要し、system_role と is_active を変えられる。
		// こちらは locale / timezone / theme / hue を変えられる代わりに、
		// system_role が送られたら 422 で弾く（4.2）。
		r.Get("/me", h.me)
		self := r.With(middleware.RequireHumanSession)
		self.Patch("/me", h.patchMe)
		self.Post("/me/password", h.changeMyPassword)
		// アクセストークン（4.4）。**扱うのは token_type='api' だけ**であり、
		// 対象の絞り込みはクエリ側（me.sql）にある。他人のトークンとセッションは
		// 「見つからない」に寄せるため、認可ミドルウェアでは表現できない。
		// 第2要素（ApiDesign.md 4.6）。**必要権限は「本人」**であり、
		// 4章の他の節と同じく権限キーを要求しない。触れる範囲はハンドラが
		// p.ActorID で閉じている。
		self.Get("/me/mfa", h.getMyMfa)
		self.Post("/me/mfa/totp", h.startMyTotp)
		self.Post("/me/mfa/totp/{id}/confirm", h.confirmMyTotp)
		self.Delete("/me/mfa/totp/{id}", h.deleteMyTotp)
		self.Post("/me/mfa/recovery-codes", h.regenerateMyRecoveryCodes)
		// パスキー（ApiDesign.md 4.7）。4.6 と同じく「本人」であり、
		// 触れる範囲はハンドラが p.ActorID で閉じている。**/options は /{id} より
		// 先に一致する**（chi は静的なセグメントを優先する）が、メソッドも違う。
		self.Get("/me/passkeys", h.listMyPasskeys)
		self.Post("/me/passkeys/options", h.startMyPasskeyRegistration)
		self.Post("/me/passkeys", h.registerMyPasskey)
		self.Delete("/me/passkeys/{id}", h.deleteMyPasskey)

		self.Get("/me/tokens", h.listMyTokens)
		self.Post("/me/tokens", h.createMyToken)
		self.Delete("/me/tokens/{id}", h.deleteMyToken)
		// 自分のエージェント（4.5）。**RequirePermission を付けない。**
		//
		// agent.register / agent.token.issue は project_admin と administrator が
		// 持つ権限だが（DbDesign.md 7.3）、**それは「他人のエージェントを管理する」
		// 側の権限**である。登録と発行は本人の操作で、Requirements.md 10.9.1 の
		// 系統B が「参加する本人」と定めている。/me/tokens と同じ扱いにする。
		//
		// 他人のエージェントを「見つからない」に寄せるのはクエリ側（agent.sql が
		// すべて owner_actor_id を条件に含める）であり、ミドルウェアでは
		// 表現できない。
		self.Get("/me/agents", h.listMyAgents)
		self.Post("/me/agents", h.createMyAgent)
		self.Patch("/me/agents/{id}", h.updateMyAgent)
		self.Delete("/me/agents/{id}", h.deleteMyAgent)
		self.Post("/me/agents/{id}/tokens", h.createMyAgentToken)
		// **系統B——各人が自分の端末へ置く接続設定**（ApiDesign.md 4.5.8、手順28b）。
		// **必要権限は「本人」**（4.5 と同じ）。系統A（agent-setup）は
		// agent.register を要るが、こちらは自分のエージェントの話である。
		self.Get("/me/agents/{id}/setup", h.getMyAgentSetup)
		self.Get("/me/agents/{id}/setup.zip", h.getMyAgentSetupZip)
		self.Delete("/me/agents/{id}/tokens/{token_id}", h.deleteMyAgentToken)

		// クライアント種別のカタログ（ApiDesign.md 4.5.7）。**必要権限は無い**
		// （認証済みであればよい）——消費者は GuiDesign.md 5.8.2 の画面で、
		// そこの必要権限は「本人」である。/permissions と同じ扱い。
		//
		// **/me の配下に置かない。** 本人のデータではなくカタログである。
		r.Get("/agent-client-kinds", h.listAgentClientKinds)
		// エージェント用トークンの既定スコープ（ApiDesign.md 4.5.9）。
		// 4.5.7 と同じく**必要権限は無い**。画面に写しを持たせないための口である。
		r.Get("/agent-scopes", h.getAgentScopes)

		// ── プロジェクト（ApiDesign.md 5章）──────────────────
		//
		// 一覧が要求するのは project.view であり、**どのプロジェクトが見えるかは
		// 決めない**（オペレータもシステムロールとして project.view を持つ）。
		// 可視範囲の絞り込みはハンドラ側のクエリが行う（5.1、手順6a の判断）。
		r.With(middleware.RequirePermission(deps.Queries, "project.view")).
			Get("/projects", h.listProjects)
		// check-key は作成前の確認であり、作成と同じ権限を要求する（5.2）。
		// 誰でも叩けると、キーの当たりを付けてプロジェクトの存在を探れる。
		r.With(middleware.RequirePermission(deps.Queries, "project.create")).
			Get("/projects/check-key", h.checkProjectKey)
		// project.create を持つのはアドミニストレータのみ（5.3、
		// DbDesign.md 7.3）。ここで役割を名指ししないのは、権限の割り当てが
		// role_permission のデータ側で決まるためである（Design.md 6.4.2）。
		r.With(middleware.RequirePermission(deps.Queries, "project.create")).
			Post("/projects", h.createProject)

		// ── プロジェクト個別（5.4〜5.6）────────────────────────
		//
		// **ここから先は RequireProjectPermission を使う。** システムロールだけを
		// 見る RequirePermission では、非メンバーのオペレータ（system_role として
		// project.view を持つ）が他人のプロジェクトを読めてしまう。到達できない
		// プロジェクトは 404 に倒す（Design.md 6.4.5）。
		//
		// **{key} は check-key より後に並べても効く。** chi は静的なセグメントを
		// パラメータより優先するため、/projects/check-key は必ず上の行に届く。
		// 逆に check-key というキーのプロジェクトは到達不能になるので、
		// 5.2 の予約語に入れてある（reservedProjectKeys）。
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.view")).
			Get("/projects/{key}", h.getProject)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Patch("/projects/{key}", h.patchProject)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.archive")).
			Post("/projects/{key}/archive", h.archiveProject)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.archive")).
			Post("/projects/{key}/unarchive", h.unarchiveProject)

		// ── ダッシュボード（ApiDesign.md 9.13）── 手順19a ─────────
		//
		// **どちらも project.view である**（9.13）。チケットを数える／チケットの
		// 変更履歴を返すのに ticket.view ではないのは、**9.13 がプロジェクト
		// ダッシュボードのデータ源として定義している**ためで、読み手は
		// 「そのプロジェクトを開ける人」である。project.view を
		// 持たずに ticket.view を持つロールは存在しない（migration 0010）。
		//
		// **子資源なので RequireProjectPermission を通す。** 非メンバーには
		// 404 が返る（Design.md 6.4.5）。どちらのクエリも project_id で
		// 閉じてあり（stats.sql / activity.sql）、他プロジェクトの行に
		// 触れる経路は無い。
		//
		// **stats と activity は静的なセグメントなので、{key} 配下の他の
		// ルートと衝突しない。**
		// ── エージェント連携セットアップ（ApiDesign.md 5.7）── 手順28a ──
		//
		// **必要権限は agent.register**（GuiDesign.md 3.2）。project.edit では
		// ない——持つのは project_admin だけで、0010 の割り当てをそのまま使う。
		//
		// **agent-setup と agent-setup.zip は静的なセグメントなので、{key} 配下の
		// 他のルートと衝突しない**（stats / activity と同じ）。**zip を別パスに
		// してあるのは、ブラウザの <a download href> で落とすためである。**
		r.With(middleware.RequireProjectPermission(deps.Queries, "agent.register")).
			Get("/projects/{key}/agent-setup", h.getAgentSetup)
		r.With(middleware.RequireProjectPermission(deps.Queries, "agent.register")).
			Get("/projects/{key}/agent-setup.zip", h.getAgentSetupZip)

		r.With(middleware.RequireProjectPermission(deps.Queries, "project.view")).
			Get("/projects/{key}/stats", h.getProjectStats)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.view")).
			Get("/projects/{key}/activity", h.listProjectActivity)

		// ── タグ・スプリント（ApiDesign.md 9.11 / 9.12）──────────
		//
		// **読みと書きで必要権限が違う。** 一覧は ticket.view（バックログと
		// チケット詳細が選択肢として読む）、定義の変更は project.edit
		// （「プロジェクトの分類軸・区切りを決める」行為であり、ワークフローと
		// 同じ性格を持つ）。権限カタログは増やしていない——DbDesign.md 7.2 の
		// 28件は Design.md 付録Aで確定済みであり、既存権限の内側に収まる。
		//
		// **どちらもプロジェクトの子資源なので RequireProjectPermission を通す。**
		// 非メンバーには 404 が返る（Design.md 6.4.5）。{id} で指す行も
		// project_id で絞ってあり（tag.sql / sprint.sql）、他プロジェクトの
		// タグを指しても「見つからない」に寄る。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tags", h.listTags)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Post("/projects/{key}/tags", h.createTag)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Patch("/projects/{key}/tags/{id}", h.patchTag)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Delete("/projects/{key}/tags/{id}", h.deleteTag)

		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/sprints", h.listSprints)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Post("/projects/{key}/sprints", h.createSprint)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Patch("/projects/{key}/sprints/{id}", h.patchSprint)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Delete("/projects/{key}/sprints/{id}", h.deleteSprint)

		// スプリントの運用（ApiDesign.md 9.12.1 / 9.12.2）。
		//
		// **start は {id} を取らない。** 開始は常に新規作成であり、既にある
		// planned のスプリントを開始する形にはしていない（9.12.1）。
		//
		// **{id}/finish より先に /start を宣言する。** chi は静的な区間を
		// パラメータより優先して照合するので順序に依存しないが、**読む人が
		// 「start という id があるのか」と迷わない**ように並べておく。
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Post("/projects/{key}/sprints/start", h.startSprint)
		r.With(middleware.RequireProjectPermission(deps.Queries, "project.edit")).
			Post("/projects/{key}/sprints/{id}/finish", h.finishSprint)

		// ── チケット（ApiDesign.md 9.2 / 9.3 / 9.4）──────────────
		//
		// **3本とも必要権限が違う。** 読みは ticket.view、作成は ticket.create、
		// 並べ替えは ticket.edit（9.4 が「並べ替えは編集である」と定める）。
		//
		// operator の書き込み権限は 0040 で剥奪した。project_viewer は
		// ticket.create / ticket.edit を持たないため、この宣言で拒否される。
		//
		// **子資源なので RequireProjectPermission を通す。** 非メンバーには
		// 404 が返る（Design.md 6.4.5）。{seq} で指す行も project_id で
		// 絞ってあり（ticket.sql）、他プロジェクトの番号を指しても
		// 「見つからない」に寄る。
		//
		// **{seq}/move と {seq}/transition(s) は静的なセグメントを含むので、
		// /tickets/{seq} そのものと並べても衝突しない**（chi は静的な
		// セグメントをパラメータより先に照合する）。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tickets", h.listTickets)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.create")).
			Post("/projects/{key}/tickets", h.createTicket)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.edit")).
			Post("/projects/{key}/tickets/{seq}/move", h.moveTicket)

		// ── チケット1件（ApiDesign.md 9.5 / 9.6 / 9.7。手順17a）─────
		//
		// **4つとも必要権限が違う。** 読みは ticket.view、編集は ticket.edit、
		// 削除は ticket.delete、遷移は ticket.transition。
		//
		// 0040 以降、operator はチケットの書き込み権限を持たない。
		// 編集・削除・遷移はプロジェクトロールで許可される場合だけ通る。
		//
		// **PATCH の assignee_id だけは、これに加えて ticket.assign を要する**
		// （9.5.2）。必要権限がリクエスト本文の内容で変わるため、ミドルウェアの
		// 宣言では表せず、ハンドラ内で見ている（tickets_update.go）。
		// **例外はここ1か所に留める**——ルート定義を眺めて必要権限が読めなく
		// なるのを避けるため（Design.md 6.4.4）。
		//
		// **遷移は ticket.transition に加えて workflow_transition の
		// required_permission も要る**（9.6 の検証5）。こちらはプロジェクトごとに
		// 変わる値なので、宣言ではなくDBから読む（ticket_workflow.go）。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tickets/{seq}", h.getTicket)
		// **ticket.edit と ticket.self_edit の OR である**（9.5.2。0029）。
		// 狭いほうしか持たない呼び出し元には、**送れる項目をハンドラが絞る**
		// ——どの項目を送ったかで可否が決まるので、6.4.4 の宣言では表せない
		// （9.8 のコメント削除に続く2例目の OR）。
		r.With(middleware.RequireAnyProjectPermission(deps.Queries,
			"ticket.edit", "ticket.self_edit")).
			Patch("/projects/{key}/tickets/{seq}", h.updateTicket)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.delete")).
			Delete("/projects/{key}/tickets/{seq}", h.deleteTicket)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.transition")).
			Post("/projects/{key}/tickets/{seq}/transition", h.transitionTicket)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tickets/{seq}/transitions", h.listTicketTransitions)

		// ── チケットの外部参照（ApiDesign.md 9.10.2。手順17c）───────
		//
		// **読みは ticket.view、更新系は ticket.reference.edit**（9.10.2。
		// 0027 で ticket.edit から切り出した）。**切り出したのは、
		// エージェントに開けたい範囲がここで初めて ticket.edit より狭く
		// なったからである**——6.12 が「kind='code' の書き手はエージェント」
		// と定めるのに、4.5.3 の許可リストは ticket.edit を含まなかった。
		// ticket.edit を許可リストへ足すと、本文・担当・期日の書き換えや
		// 並べ替えまで同時に開く（DbDesign.md 6.12.1）。**ticket.edit を持つ
		// ロールにはすべて配るので、人から見た可否は変わらない。** 参照を足す
		// ことはチケットを編集することであり、新しい権限は増やしていない
		// （DbDesign.md 7.2 の28件は Design.md 付録Aで確定済み）。
		//
		// **書き手は MCP の pb_add_reference を使うエージェントと、/me/tokens の
		// API トークンを持つクライアントである**（9.10.2）。画面は kind='code'
		// の追加を持たず、表示と削除だけを行う（GuiDesign.md 5.5）——つまり
		// POST の主な呼び手はブラウザではない。
		//
		// **{seq}/references は静的なセグメントを含むので、/tickets/{seq} や
		// {seq}/transitions と並べても衝突しない**（chi は静的なセグメントを
		// パラメータより先に照合する）。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tickets/{seq}/references", h.listTicketReferences)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.reference.edit")).
			Post("/projects/{key}/tickets/{seq}/references", h.createTicketReference)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.reference.edit")).
			Patch("/projects/{key}/tickets/{seq}/references/{id}", h.patchTicketReference)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.reference.edit")).
			Delete("/projects/{key}/tickets/{seq}/references/{id}", h.deleteTicketReference)

		// ── コメント（ApiDesign.md 9.8）── 手順18a ────────────────
		//
		// **メソッドごとに必要権限が違う唯一の資源である**（9.8）。読むのは
		// ticket.view、投稿は comment.create、編集は comment.edit_own、
		// 削除は comment.delete_any **または** comment.edit_own である。
		//
		// **DELETE だけ OR なので RequireAnyProjectPermission を使う。**
		// 「自分のものか」は行を読まないと決まらないためハンドラ側で見るが、
		// **どの権限で通りうるかはここに残す**（Design.md 6.4.4）。
		//
		// **PATCH の「自分のもののみ」も同じ形である。** 宣言は
		// comment.edit_own で、所有者の照合は comments.go が行う。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tickets/{seq}/comments", h.listTicketComments)
		r.With(middleware.RequireProjectPermission(deps.Queries, "comment.create")).
			Post("/projects/{key}/tickets/{seq}/comments", h.createTicketComment)
		r.With(middleware.RequireProjectPermission(deps.Queries, "comment.edit_own")).
			Patch("/projects/{key}/tickets/{seq}/comments/{id}", h.patchTicketComment)
		r.With(middleware.RequireAnyProjectPermission(deps.Queries,
			"comment.delete_any", "comment.edit_own")).
			Delete("/projects/{key}/tickets/{seq}/comments/{id}", h.deleteTicketComment)

		// ── 完了条件（DoD）（ApiDesign.md 9.9）── 手順18a ─────────
		//
		// **更新系はすべて ticket.edit である**（9.9）。完了条件はチケットの
		// 内容そのものであり、コメントのように「自分が書いたもの」という
		// 概念を持たない——誰が足した条件でも、担当が変われば直す。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tickets/{seq}/dod", h.listTicketDoD)
		// **ticket.self_edit でも通る。ただし is_satisfied は書けない**
		// （9.9。0029）——完了の判定は人が行う。
		r.With(middleware.RequireAnyProjectPermission(deps.Queries,
			"ticket.edit", "ticket.self_edit")).
			Post("/projects/{key}/tickets/{seq}/dod", h.createDoDItem)
		r.With(middleware.RequireAnyProjectPermission(deps.Queries,
			"ticket.edit", "ticket.self_edit")).
			Patch("/projects/{key}/tickets/{seq}/dod/{id}", h.patchDoDItem)
		r.With(middleware.RequireAnyProjectPermission(deps.Queries,
			"ticket.edit", "ticket.self_edit")).
			Delete("/projects/{key}/tickets/{seq}/dod/{id}", h.deleteDoDItem)

		// ── 完了レポート（ApiDesign.md 9.15）── 手順26c ───────────
		//
		// **必要権限は ticket.transition**（9.15）。レポートは「作業の完了を
		// 申告する」操作で、盤面を動かす権限と同じ重さにある。専用の権限キーを
		// 足さない——DbDesign.md 7.2 は権限キーを削除しないと定めており、
		// **使い分ける必要が現れる前にカタログを増やさない。**
		//
		// **GET を置かない**（9.15）。人が読むのは完了レポートの
		// コメントであり、agent_report の行そのものを読む面が無い。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.transition")).
			Post("/projects/{key}/tickets/{seq}/reports", h.submitTicketReport)

		// ── チケット間リンク（ApiDesign.md 9.10.1）── 手順18a ─────
		//
		// **PATCH を持たない**（9.10.1）。一意制約が (source, target, link_type)
		// である以上、link_type の変更は別の行になるのと同じである。
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.view")).
			Get("/projects/{key}/tickets/{seq}/links", h.listTicketLinks)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.edit")).
			Post("/projects/{key}/tickets/{seq}/links", h.createTicketLink)
		r.With(middleware.RequireProjectPermission(deps.Queries, "ticket.edit")).
			Delete("/projects/{key}/tickets/{seq}/links/{id}", h.deleteTicketLink)

		// ── プロジェクト文書（ApiDesign.md 10章）── 手順22a ───────
		//
		// **読みは doc.view、更新系は doc.edit**（10章の表）。**doc.edit は
		// operator と project_member が持たない**（DbDesign.md 8.1.4）——
		// 憲章は全参加者を縛るため、更新できる人を絞る。
		//
		// **「その操作ができない人」が実在しないという問題
		// （Design.md 付録A）に対する、最初の実例である。** これまでの宣言は
		// すべて operator がシステムロール側から通ってしまい、負の側を
		// 検証できなかった。ここで初めて 403 を実際に出せる。
		//
		// **ワイルドカードで受ける。** 文書はパスで指し（10.1）、階層の深さに
		// 上限が無いためである。**{key}/docs 配下は他に静的なセグメントを
		// 持たない**ので、/docs と /docs/* の2本で衝突しない。
		//
		// **_revisions のルートを別に置いていない。** chi のワイルドカードが
		// すべてを飲むため、履歴かどうかは末尾のセグメントで分岐する
		// （10.1、doc_scope.go の parseDocPath）。slug の CHECK が _ を弾くので
		// （DbDesign.md 8.1.1）、「_revisions という名の文書」と取り違えない。
		// **GET 以外で _revisions を叩くと 405**（10.1）。
		//
		// **子資源なので RequireProjectPermission を通す。** 非メンバーには
		// 404 が返る（Design.md 6.4.5）。木を読むクエリは project_id で
		// 閉じており（document.sql）、文書の内部 ID はその木からしか出てこない。
		r.With(middleware.RequireProjectPermission(deps.Queries, "doc.view")).
			Get("/projects/{key}/docs", h.listDocs)
		r.With(middleware.RequireProjectPermission(deps.Queries, "doc.edit")).
			Post("/projects/{key}/docs", h.createDoc)
		r.With(middleware.RequireProjectPermission(deps.Queries, "doc.view")).
			Get("/projects/{key}/docs/*", h.getDoc)
		r.With(middleware.RequireProjectPermission(deps.Queries, "doc.edit")).
			Patch("/projects/{key}/docs/*", h.patchDoc)
		r.With(middleware.RequireProjectPermission(deps.Queries, "doc.edit")).
			Delete("/projects/{key}/docs/*", h.deleteDoc)

		// ── ロール・権限カタログ（ApiDesign.md 7章）──────────────
		//
		// **GET /roles だけ必要権限が scope で変わる**（7.1）。?scope=project は
		// 権限を要さず、それ以外（system・未指定）は user.manage を要する。
		// プロジェクト設定のメンバータブ（GuiDesign.md 5.9.2）はプロジェクト
		// 管理者が開く画面でありながら、ロールの表示名を必要とするためである。
		//
		// **この振り分けをハンドラへ移さない。** ルート定義を眺めて必要権限が
		// 読めなくなる（Design.md 6.4.4）。
		r.With(middleware.RequirePermissionUnlessQuery(
			deps.Queries, "user.manage", "scope", roleScopeProject)).
			Get("/roles", h.listRoles)
		// 権限カタログは**認証済みなら誰でも読める**（ApiDesign.md 7.2。
		// user.manage を求めない）。消費者が2つあるためである
		// ——GuiDesign.md 5.6.3 の権限マトリクス（/admin/users の中）と、
		// 5.8.2 のエージェント用トークンの発行結果（/me/agents。必要権限は「本人」）。
		// **後者は user.manage を持たない。**
		//
		// 開放しても渡る情報は増えない。カタログは Design.md 6.4.2 に全文があり、
		// 本人の実効権限は GET /me が既に返している。
		r.Get("/permissions", h.listPermissions)

		// ── ユーザー管理（ApiDesign.md 6章）────────────────────
		//
		// **6章はすべてアドミニストレータ専用**（同章の前書き）。プロジェクト層は
		// 関係しないので RequirePermission で足りる。user.manage を持つのは
		// アドミニストレータのみ（DbDesign.md 7.3）だが、ここで
		// 役割を名指ししないのは割り当てが role_permission のデータ側で
		// 決まるためである（Design.md 6.4.2）。
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Get("/admin/users", h.listUsers)
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Post("/admin/users", h.createUser)

		// ── ユーザー個別（6.3〜6.8）────────────────────────────
		//
		// **プロジェクト個別（5.4〜5.6）と違い RequirePermission のままでよい。**
		// 誰のアカウントを触るかによって必要権限が変わらないためである
		// （6章はすべて user.manage）。自分自身や最後のアドミニストレータを
		// 守るのは認可ではなく業務のガードであり、ハンドラ側にある（6.4 / 6.5）。
		//
		// **{id} は静的なセグメントより後に並べても効く。** chi は静的な
		// セグメントをパラメータより優先するため、/admin/users への
		// GET / POST は上の2行に届く。
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Get("/admin/users/{id}", h.getUser)
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Patch("/admin/users/{id}", h.patchUser)
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Delete("/admin/users/{id}", h.deleteUser)
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Post("/admin/users/{id}/password-reset", h.resetUserPassword)
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Post("/admin/users/{id}/sessions/revoke", h.revokeUserSessions)
		// 第2要素の解除（ApiDesign.md 6.9）。**6.6 のリセットと同じ
		// 権限だが別の操作である**——あちらはパスワード、こちらは認証器。
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Post("/admin/users/{id}/mfa/reset", h.resetUserMfa)
		// パスキーの全削除（ApiDesign.md 6.10）。**6.9 と同じ権限だが別の操作
		// である**——あちらは第2要素、こちらはパスワードの代わりになる鍵。
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Post("/admin/users/{id}/passkeys/reset", h.resetUserPasskeys)
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Put("/admin/users/{id}/memberships/{key}", h.putUserMembership)
		r.With(middleware.RequirePermission(deps.Queries, "user.manage")).
			Delete("/admin/users/{id}/memberships/{key}", h.deleteUserMembership)

		// ── アプリケーション設定（ApiDesign.md 11章）──────────────
		//
		// **system.settings は 0010 から存在していたが、ここが最初の利用者
		// である**。user.manage とは別の権限なので、ユーザー管理を
		// 持たない役割に設定だけを配ることができる。
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Get("/admin/settings", h.listSettings)
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Put("/admin/settings", h.updateSettings)
		// **締め出されうる設定の確認**（11.8）。**新しい設定を通って
		// 届いたか**を見るので、平文で来た確認は受け取らない。
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Post("/admin/settings/confirm", h.confirmSettings)
		// **未確認だけを返す軽い口**（11.9）。画面がこれを定期的に
		// 引いて、**どの画面にいても確認ボタンを出す。**
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Get("/admin/settings/pending", h.getPendingSettings)

		// ── TLS 証明書（ApiDesign.md 11.4〜11.6）──────────────
		//
		// **設定と同じ system.settings である。** 第3層（Design.md 6.6.1）で、
		// app_setting とは別の表・別のエンドポイントだが、触れる人は同じである。
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Get("/admin/tls/certificates", h.listTLSCertificates)
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Post("/admin/tls/certificates", h.uploadTLSCertificate)
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Delete("/admin/tls/certificates/{id}", h.deleteTLSCertificate)
		// **取り出す口（11.7）。** 証明書をクライアントへ渡すまで
		// エージェントは PB へ繋げないので、**繋げない相手から取ってこなければ
		// ならない**という循環がある。この口がそれを断つ。
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Get("/admin/tls/certificates/{id}/certificate.zip", h.downloadTLSCertificate)

		// ── DB の接続状態と統計（ApiDesign.md 11.10）──────────
		//
		// **設定ではなく状態だが、触れる人は設定と同じである。** 接続先や
		// 表ごとの件数は、設定を変える人が障害の切り分けに使う。
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Get("/admin/database", h.getDatabaseStatus)

		// ── バックアップと復元（ApiDesign.md 11.11〜11.12）────
		//
		// **書き出しは読むだけ、取り込みは PB 全体を入れ替える。** 権限は
		// 同じ system.settings だが、取り込みは**画面で pb_owner の資格情報も
		// 受け取る**（DbDesign.md 3.4）——PB はそれを持たない。
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Get("/admin/backup.tar.gz", h.downloadBackup)
		r.With(middleware.RequirePermission(deps.Queries, "system.settings")).
			Post("/admin/restore", h.restoreBackup)
	})
}

// handler は /api/v1 のハンドラが共有する依存。
type handler struct {
	q  gen.Querier
	tx TxRunner

	// settings は実行中の設定（Design.md 10.3 の第2層）。**cookieSecure を
	// bool で持っていたのを置き換えた**——画面から変えられるように
	// なったので、組み立て時の値を握り続けると変更が効かない。
	settings *config.Live

	// onSettingsChanged は設定が変わったときに呼ぶ。ロガーの入れ替え
	// （log_format / log_level）を serve.go 側で行うための口である。
	onSettingsChanged func(*config.Set)

	// certs は出す TLS 証明書の入れ物（Design.md 6.6.1）。**nil なら
	// TLS で待ち受けていない**（証明書の登録はできる）。
	certs *tlscert.Holder
	// tlsListening は実際に TLS で待ち受けているか。**設定の実効値ではない**
	// ——設定を変えても再起動までは待受が変わらないためである。
	tlsListening bool

	// listenURL は実際に待ち受けているスキームとアドレス（ApiDesign.md 11.4）。
	// **設定の bind から画面が組み立てない**ので、ここで組み立てて渡す。
	listenURL string

	// dbStats は DB の接続状態と統計を読む口（ApiDesign.md 11.10）。
	dbStats DatabaseStats

	// backups は PB 全体の書き出しと取り込みの口（ApiDesign.md 11.11〜11.12）。
	backups Backups
	// maintenance は保守モードの旗（Design.md 10.4）。
	maintenance *maintenance.Flag
}
