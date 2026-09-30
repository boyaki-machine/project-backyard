import type { RouteRecordRaw } from 'vue-router'

import AgentSetupPage from '../pages/AgentSetupPage.vue'
import AppSettingsPage from '../pages/AppSettingsPage.vue'
import AuditPage from '../pages/AuditPage.vue'
import DashboardPage from '../pages/DashboardPage.vue'
import DocsPage from '../pages/DocsPage.vue'
import ForbiddenPage from '../pages/ForbiddenPage.vue'
import LoginPage from '../pages/LoginPage.vue'
import NotFoundPage from '../pages/NotFoundPage.vue'
import PlaceholderPage from '../pages/PlaceholderPage.vue'
import MySettingsPage from '../pages/MySettingsPage.vue'
import MyAgentsPage from '../pages/MyAgentsPage.vue'
import MyTokensPage from '../pages/MyTokensPage.vue'
import ProjectSettingsPage from '../pages/ProjectSettingsPage.vue'
import ProjectsPage from '../pages/ProjectsPage.vue'
import TicketViewsPage from '../pages/TicketViewsPage.vue'
import UserDetailPage from '../pages/UserDetailPage.vue'
import UsersPage from '../pages/UsersPage.vue'

/**
 * プレースホルダページに渡す内容（GuiDesign.md 6.5）。
 *
 * 予定内容は設計文書からの転記に限る。ここで内容を創作しない。
 * 実装時はルート定義の component を差し替え、meta.placeholder を削除する。
 */
export interface PlaceholderMeta {
  /** 画面名。カード内の「<title>のページ予定」に使う */
  title: string
  /**
   * ページヘッダを「<プロジェクト名> <この文字列>」にする（`GuiDesign.md` 5.3）。
   *
   * **手順19b の時点でどのルートも持たない。** 唯一の持ち主だった
   * ダッシュボードが実画面になり、同じ見出しを `DashboardPage.vue` が
   * 自分で組み立てている。プロジェクト配下にプレースホルダを足すときに
   * 再び要る（他の画面は画面名のみを出す）ので、仕組みは残してある。
   */
  projectHeading?: string
  /** 対応する設計文書の章番号。未定義なら「設計未確定」と明記する */
  docRef: string
  /** 予定している内容（設計文書からの転記） */
  planned: string[]
  /** 状態（「未定」「構想」など） */
  status: string
}

declare module 'vue-router' {
  interface RouteMeta {
    /** 到達に必要な権限キー（GuiDesign.md 3.2「必要権限」）。手順8のルーターガードが使う */
    permission?: string
    /** 未認証でも到達してよいルート（GuiDesign.md 3.2 の「不要」） */
    public?: boolean
    /** プレースホルダの表示内容。実装済みの画面では持たない */
    placeholder?: PlaceholderMeta
  }
}

/**
 * ルート定義（GuiDesign.md 3.2 のルーティング表）。
 *
 * 未実装・設計未確定の画面には PlaceholderPage を置く（Design.md 11.3）。
 * 権限はプレースホルダの段階から実画面と同じ値を設定する（GuiDesign.md 6.5 規約）。
 */
const routeSources: RouteRecordRaw[] = [
  // ログイン後の初期画面は /projects（GuiDesign.md 3.1 / 5.2）。
  { path: '/', redirect: '/projects' },

  // メインメニューを出さない唯一の画面（GuiDesign.md 5.1 のレイアウト例外）。
  { path: '/login', component: LoginPage, meta: { public: true } },

  // 新規作成モーダルは URL を持つ唯一のモーダル（GuiDesign.md 3.2 の `?new=1`）。
  // モーダル本体は手順10b。
  {
    path: '/projects',
    component: ProjectsPage,
    meta: { permission: 'project.view' },
  },

  // 実画面（GuiDesign.md 5.3）。手順19b でプレースホルダから差し替えた。
  //
  // **必要権限は project.view**（3.2）。チケットの2ブロック（自分の担当・
  // 期限が近い）と集計カードの導線だけが ticket.view を要し、**画面側で
  // 出し分ける**（7.3）——集計そのものは project.view で読める（9.13）。
  //
  // **`projectKey` を props で渡す。** ルートの `:key` を画面が自分で
  // `route.params` から読むと、プロジェクトを切り替えたときに watch を
  // 書く場所が増える。ここで1回変換する。
  {
    path: '/p/:key',
    component: DashboardPage,
    props: (to) => ({ projectKey: String(to.params.key) }),
    meta: { permission: 'project.view' },
  },

  // 実画面（GuiDesign.md 5.4）。手順16c で新設した。
  //
  // **チケットを見る「視点」はパスで分ける**（3.2）。`?view=` にしないのは、
  // 視点ごとにメニュー項目・権限・件数バッジを持てるようにするためである。
  //
  // **下の `/p/:key/tickets/:seq`・`/p/:key/search` と同じコンポーネントである**
  // （入れ物の `TicketViewsPage`）。詳細は一覧を消さず、
  // その右にペインとして開く（2.2.1）。入れ物がパスと `from` を見て、バックログか
  // 検索かを出し分ける。
  {
    path: '/p/:key/backlog',
    component: TicketViewsPage,
    meta: { permission: 'ticket.view' },
  },

  // 画面を持たず /p/:key/backlog へリダイレクトする（GuiDesign.md 3.2）。
  //
  // 以前はここがチケット一覧だった。**リダイレクトを残すのは、既存の
  // ブックマークを壊さないため**である。
  // **クエリは引き継ぐ**——5.4 のフィルタ条件は URL に載っており、
  // 落とすと共有されたリンクが素の状態で開く。
  {
    path: '/p/:key/tickets',
    redirect: (to) => ({ path: `/p/${String(to.params.key)}/backlog`, query: to.query }),
  },

  // 実画面（GuiDesign.md 5.5）。手順17b でプレースホルダから差し替えた。
  //
  // **component が一覧の入れ物（`TicketViewsPage`）なのは誤りではない。** チケット詳細は
  // 画面を置き換えず、**一覧の右に3枚目のペインとして開く**（2.2.1）。一覧と同じ
  // コンポーネントを指すことで、行をクリックしても再マウントされず、
  // **一覧の取得結果・折りたたみ・スクロール位置がそのまま残る**
  // （5.4「戻したときに保つもの」）。
  //
  // **URL は `/p/:key/tickets/:seq` のまま変えない**（3.2）——チケット単体の
  // 共有URLとして既に確定している。**一覧の条件はクエリで持ち回る**ので、クエリを
  // 解釈できない相手（共有された素のURL）でも壊れない（バックログがフィルタ無しで
  // 並ぶだけである）。**チケット検索から開いたときは `from=search` が付き**、後ろに
  // 検索結果が残る。
  {
    path: '/p/:key/tickets/:seq',
    component: TicketViewsPage,
    meta: { permission: 'ticket.view' },
  },

  // 実画面（GuiDesign.md 5.10）。手順22b で新設した。
  //
  // **2本のルートが同じコンポーネントを指す。** 文書を選んでいない状態
  // （`/p/:key/docs`）と本文（`/p/:key/docs/rules/naming`）は同じ画面で、
  // **URL は本文側が持つ**（5.10）——共有された URL を開くと、その文書が
  // 開いた状態で木も展開される。同じコンポーネントなので、文書を切り替えても
  // 再マウントされず、**木の取得結果と折りたたみがそのまま残る。**
  //
  // **`:path(.*)` にしてある。** 3.2 の表記は `:path*` だが、あれは「パス」の
  // 説明であって vue-router の文法ではない。`:path*` は repeatable でパラメータが
  // 配列になり、`rules/naming` を毎回つなぎ直すことになる。
  {
    path: '/p/:key/docs',
    component: DocsPage,
    props: (to) => ({ projectKey: String(to.params.key) }),
    meta: { permission: 'doc.view' },
  },
  {
    path: '/p/:key/docs/:path(.*)',
    component: DocsPage,
    props: (to) => ({ projectKey: String(to.params.key) }),
    meta: { permission: 'doc.view' },
  },

  // 実画面（GuiDesign.md 5.9）。手順11b でプレースホルダから差し替えた
  {
    path: '/p/:key/settings',
    component: ProjectSettingsPage,
    meta: { permission: 'project.edit' },
  },

  // 実画面（GuiDesign.md 5.6）。手順12b でプレースホルダから差し替えた。
  // 行の操作メニュー（`[⋯]`）は手順13b で足した。「ロールと権限」タブの
  // 中身は手順14（`GET /roles` / `GET /permissions`）
  {
    path: '/admin/users',
    component: UsersPage,
    meta: { permission: 'user.manage' },
  },

  // 実画面（GuiDesign.md 5.6.2）。手順13b でプレースホルダから差し替えた
  {
    path: '/admin/users/:id',
    component: UserDetailPage,
    meta: { permission: 'user.manage' },
  },

  // 実画面（GuiDesign.md 5.12）。
  //
  // **/admin/system（認証プロバイダ、構想）とは別画面である。** 必要権限が
  // system.settings と authprovider.manage で分かれており、ユーザー管理を
  // 持たない役割に設定だけを配ることができる。
  {
    path: '/admin/settings',
    component: AppSettingsPage,
    meta: { permission: 'system.settings' },
  },

  {
    path: '/admin/audit',
    component: AuditPage,
    meta: { permission: 'auditlog.view' },
  },

  // 実画面（GuiDesign.md 5.8）。手順15 でプレースホルダから差し替えた。
  //
  // **meta.permission を持たない。** 必要権限は「本人」であり、権限キーで
  // 判定するものではない（3.2）。サーバ側も 4章に認可ミドルウェアを
  // 付けていない（routes.go）。
  {
    path: '/me',
    component: MySettingsPage,
  },

  // 実画面（GuiDesign.md 5.8.1）。手順15b でプレースホルダから差し替えた。
  //
  // **`/me` と同じく meta.permission を持たない。** 必要権限は「本人」であり、
  // 権限キーで判定するものではない（3.2）。
  {
    path: '/me/tokens',
    component: MyTokensPage,
  },

  // 実画面（GuiDesign.md 5.8.2）。手順24b で足した。**プレースホルダを
  // 経由していない**——このルートは 24b で初めて存在する（3.2 の行も同時に足した）。
  //
  // **`/me` と同じく meta.permission を持たない。** 必要権限は「本人」であり、
  // 権限キーで判定するものではない（3.2）。サーバ側も ApiDesign.md 4.5 に
  // 認可ミドルウェアを付けていない（routes.go）。
  {
    path: '/me/agents',
    component: MyAgentsPage,
  },

  // ── チケットで駆動する視点（GuiDesign.md 3.2 / 10章）──────────
  //
  // 状態は「未定」と出す。
  //
  // **WBS とスプリント管理のルートは持たない。** WBS が指すのは
  // チケットの親子階層で、**バックログが既にその面である**。バーンダウン・
  // ベロシティは進捗分析（/p/:key/insights）が持つ。
  {
    path: '/p/:key/board',
    component: PlaceholderPage,
    meta: {
      permission: 'ticket.view',
      placeholder: {
        title: 'カンバンボード',
        docRef: 'GuiDesign.md 10章',
        status: '未定',
        planned: [
          '列＝ワークフローのステータス',
          'ドラッグ&ドロップによる遷移',
          'is_agent_reachable の列にはエージェントのカードのみ移動可',
        ],
      },
    },
  },

  // 実画面（GuiDesign.md 5.14。pb-220 でプレースホルダから差し替えた）。
  //
  // **バックログ・詳細と同じ入れ物を指す**——詳細を `from=gantt` で開いても
  // ガントが再マウントされず、スクロール位置と拡大の具合が残る。
  {
    path: '/p/:key/gantt',
    component: TicketViewsPage,
    meta: { permission: 'ticket.view' },
  },

  // 実画面（GuiDesign.md 5.13）。
  //
  // **バックログ・詳細と同じ入れ物を指す**——詳細を開いても検索画面が再マウントされず、
  // 取得結果とスクロール位置が残る（上の `/p/:key/tickets/:seq` の注記）。
  {
    path: '/p/:key/search',
    component: TicketViewsPage,
    meta: { permission: 'ticket.view' },
  },



  {
    // 実画面（GuiDesign.md 5.11）。手順28a でプレースホルダから差し替えた。
    //
    // **プロジェクト設定のタブではなく独立したルートである**（5.9）。
    // 必要権限も違う——`project.edit` ではなく **`agent.register`** で、
    // 持つのは project_admin だけである（0010）。
    //
    // **系統A（リポジトリの初回接続）だけを担う**（Requirements.md 10.9.1）。
    // トークンの発行・接続確認・各人の接続設定は `/me/agents` 側（系統B、手順28b）。
    path: '/p/:key/settings/agents',
    component: AgentSetupPage,
    meta: { permission: 'agent.register' },
  },

  // ── 構想（GuiDesign.md 10章）───────────────────────────────
  //
  // **必要権限は画面ごとに違う**（3.2 が正本）。承認キューは `proposal.review`、
  // プロジェクトメモリは `knowledge.view`、進捗分析とヒストリーは `project.view`
  // ——後者はチケット個々を見ずに集計だけを読む画面のため、ticket.view ではない（4.3）。
  {
    path: '/p/:key/approvals',
    component: PlaceholderPage,
    meta: {
      permission: 'proposal.review',
      placeholder: {
        title: '承認キュー',
        docRef: 'GuiDesign.md 10章',
        status: '構想',
        planned: [
          'AIの提案（知識更新・サブタスク・ドキュメント差分）を差分ビューで一括レビュー',
          'proposal テーブル1つを源とする単一画面',
          'メインメニューへの未処理件数バッジ',
        ],
      },
    },
  },

  {
    path: '/p/:key/knowledge',
    component: PlaceholderPage,
    meta: {
      permission: 'knowledge.view',
      placeholder: {
        title: 'プロジェクトメモリ',
        docRef: 'GuiDesign.md 10章',
        status: '構想',
        planned: ['知識の一覧・編集・履歴', 'kind（規約／決定／注意／失敗）によるフィルタ'],
      },
    },
  },

  {
    path: '/p/:key/insights',
    component: PlaceholderPage,
    meta: {
      permission: 'project.view',
      placeholder: {
        title: '進捗分析',
        docRef: 'GuiDesign.md 10章',
        status: '構想',
        planned: [
          'チケットの消化状況・残存チケットの傾向',
          'バックログ・カンバン・ガントが「いま何があるか」を見せるのに対し、この画面だけが「どう進んでいるか」を集計で答える',
          'Requirements.md 3章のベロシティ・見積り精度トラッキング',
        ],
      },
    },
  },

  {
    path: '/p/:key/history',
    component: PlaceholderPage,
    meta: {
      permission: 'project.view',
      placeholder: {
        title: 'プロジェクトヒストリー・要約',
        docRef: 'GuiDesign.md 10章',
        status: '構想',
        planned: [
          '年表形式の履歴',
          'AIによるプロジェクト要約の集約（日次バッチで事前生成）',
          '期間を絞った PDF／Markdown エクスポート',
        ],
      },
    },
  },

  {
    path: '/admin/system',
    component: PlaceholderPage,
    meta: {
      permission: 'authprovider.manage',
      placeholder: {
        title: '認証プロバイダ（OIDC/SAML）',
        docRef: 'GuiDesign.md 10章',
        status: '構想',
        planned: ['OIDC/SAML の設定', 'auth_provider テーブルの編集UI'],
      },
    },
  },

  // ── エラー画面（GuiDesign.md 3.2）───────────────────────────
  { path: '/403', component: ForbiddenPage, meta: { public: true } },
  { path: '/404', component: NotFoundPage, meta: { public: true } },
  { path: '/:pathMatch(.*)*', component: NotFoundPage, meta: { public: true } },
]

export const routes = routeSources
