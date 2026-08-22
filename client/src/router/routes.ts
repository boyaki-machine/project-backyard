import type { RouteRecordRaw } from 'vue-router'

import ForbiddenPage from '../pages/ForbiddenPage.vue'
import LoginPage from '../pages/LoginPage.vue'
import NotFoundPage from '../pages/NotFoundPage.vue'
import PlaceholderPage from '../pages/PlaceholderPage.vue'
import MySettingsPage from '../pages/MySettingsPage.vue'
import MyTokensPage from '../pages/MyTokensPage.vue'
import ProjectSettingsPage from '../pages/ProjectSettingsPage.vue'
import ProjectsPage from '../pages/ProjectsPage.vue'
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
   * ダッシュボードだけが持つ。他のプロジェクト配下の画面は画面名のみを出す。
   * 実画面へ差し替えるときは、同じ見出しをその画面が組み立てる。
   */
  projectHeading?: string
  /** 対応する設計文書の章番号。未定義なら「設計未確定」と明記する */
  docRef: string
  /** 予定している内容（設計文書からの転記） */
  planned: string[]
  /** Phase と実装予定（docs/PROGRESS.md の手順番号） */
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
export const routes: RouteRecordRaw[] = [
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

  {
    path: '/p/:key',
    component: PlaceholderPage,
    meta: {
      permission: 'project.view',
      placeholder: {
        title: 'プロジェクトダッシュボード',
        projectHeading: 'ダッシュボード',
        docRef: 'GuiDesign.md 5.3',
        status: 'Phase 1・未着手（docs/PROGRESS.md 手順16以降）',
        planned: [
          'ステータス別チケット件数',
          '自分の担当（未完了 上位5件）',
          '期限が近いチケット',
          '最近の動き',
          '要対応（放置検出・期限超過）',
        ],
      },
    },
  },

  {
    path: '/p/:key/tickets',
    component: PlaceholderPage,
    meta: {
      permission: 'ticket.view',
      placeholder: {
        title: 'チケット一覧',
        docRef: 'GuiDesign.md 5.4',
        status: 'Phase 1・未着手（docs/PROGRESS.md 手順16）',
        planned: [
          '状態・種別・担当・優先度によるフィルタ（条件はURLクエリに反映）',
          'ID・タイトル・状態・優先度・担当・期限の一覧',
          '親子関係のインデント表示',
          '行クリックでチケット詳細へ遷移',
          '列ヘッダのクリックによるソートとページング',
        ],
      },
    },
  },

  {
    path: '/p/:key/tickets/:seq',
    component: PlaceholderPage,
    meta: {
      permission: 'ticket.view',
      placeholder: {
        title: 'チケット詳細',
        docRef: 'GuiDesign.md 5.5',
        status: 'Phase 1・未着手（docs/PROGRESS.md 手順16）',
        planned: [
          '説明（Markdownソース＋ライブプレビュー）',
          '完了条件（DoD、Phase 1 は manual 型のみ）',
          '関連チケット（手動リンクのみ）',
          'コメント（kind を投稿時に選択）',
          '右サイドバーのメタ情報（状態・担当・優先度・種別・親・見積・期限・スプリント）',
          '履歴（activity の時系列表示、既定は畳む）',
        ],
      },
    },
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

  {
    path: '/admin/audit',
    component: PlaceholderPage,
    meta: {
      permission: 'auditlog.view',
      placeholder: {
        title: '監査ログ',
        docRef: 'GuiDesign.md 5.7',
        status: 'Phase 1 内で未定（docs/PROGRESS.md）',
        planned: [
          '期間・操作・実行者・結果によるフィルタ',
          '日時・実行者・操作・対象・結果の一覧',
          '行クリックで詳細（IP、User-Agent、detail）を展開',
          'CSVエクスポート',
        ],
      },
    },
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

  // ── Phase 2（GuiDesign.md 10章）─────────────────────────────
  {
    path: '/p/:key/board',
    component: PlaceholderPage,
    meta: {
      permission: 'ticket.view',
      placeholder: {
        title: 'カンバンボード',
        docRef: 'GuiDesign.md 10章',
        status: 'Phase 2',
        planned: [
          '列＝ワークフローのステータス',
          'ドラッグ&ドロップによる遷移',
          'is_agent_reachable の列にはエージェントのカードのみ移動可',
        ],
      },
    },
  },

  {
    path: '/p/:key/gantt',
    component: PlaceholderPage,
    meta: {
      permission: 'ticket.view',
      placeholder: {
        title: 'ガントチャート',
        docRef: 'GuiDesign.md 10章',
        status: 'Phase 2',
        planned: ['集中モード（2.3.2）の主な用途', '仮想スクロールによる大量行への対応'],
      },
    },
  },

  {
    path: '/p/:key/wbs',
    component: PlaceholderPage,
    meta: {
      permission: 'ticket.view',
      placeholder: {
        title: 'WBS',
        docRef: 'GuiDesign.md 10章',
        status: 'Phase 2',
        planned: ['ツリー＋インライン編集', 'ガントと同一データの別ビュー'],
      },
    },
  },

  {
    path: '/p/:key/sprints',
    component: PlaceholderPage,
    meta: {
      permission: 'ticket.view',
      placeholder: {
        title: 'スプリント管理',
        docRef: '設計未確定（GuiDesign.md 3.2 にルートのみ）',
        status: 'Phase 2',
        planned: [],
      },
    },
  },

  {
    path: '/p/:key/approvals',
    component: PlaceholderPage,
    meta: {
      permission: 'proposal.review',
      placeholder: {
        title: '承認キュー',
        docRef: 'GuiDesign.md 10章',
        status: 'Phase 2',
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
        status: 'Phase 2',
        planned: ['知識の一覧・編集・履歴', 'kind（規約／決定／注意／失敗）によるフィルタ'],
      },
    },
  },

  {
    path: '/p/:key/settings/agents',
    component: PlaceholderPage,
    meta: {
      permission: 'agent.register',
      placeholder: {
        title: 'エージェント連携セットアップ',
        docRef: 'GuiDesign.md 10章',
        status: 'Phase 2',
        planned: [
          'クライアントの選択',
          'トークンの発行',
          '設定ファイルのコピー／ダウンロード',
          '接続確認',
        ],
      },
    },
  },

  // ── Phase 3（GuiDesign.md 10章）─────────────────────────────
  {
    path: '/p/:key/history',
    component: PlaceholderPage,
    meta: {
      permission: 'project.view',
      placeholder: {
        title: 'プロジェクトヒストリー・要約',
        docRef: 'GuiDesign.md 10章',
        status: 'Phase 3',
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
        title: 'システム設定・認証プロバイダ',
        docRef: 'GuiDesign.md 10章',
        status: 'Phase 3',
        planned: ['OIDC/SAML の設定', 'auth_provider テーブルの編集UI'],
      },
    },
  },

  // ── エラー画面（GuiDesign.md 3.2）───────────────────────────
  { path: '/403', component: ForbiddenPage, meta: { public: true } },
  { path: '/404', component: NotFoundPage, meta: { public: true } },
  { path: '/:pathMatch(.*)*', component: NotFoundPage, meta: { public: true } },
]
