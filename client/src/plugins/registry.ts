/**
 * ビューのプラグインの登録表（`Design.md` 4.1、`GuiDesign.md` 5.15「プラグインの境界」）。
 *
 * **本体との接点はここだけである。** ルート（`router/routes.ts`）・入れ物
 * （`TicketViewsPage.vue`・`lib/ticketViews.ts`・`AppShell.vue`）・メニュー（`SideMenu.vue`）・
 * プロジェクト設定の「表示するビュー」（5.9.1）は、この表を読んでプラグインを足す。
 * **プラグインの名前を本体のコードに書かない。**
 *
 * - ページは遅延読み込み（`import()`）。**ビルドに含め、実行時に外部の JS を読まない**
 * - メニューに出すかはプロジェクトの `settings.views`（`DbDesign.md` 6.4）。権限の境界ではない
 * - 文言はプラグインの中に持つ（英訳は `<プラグイン>/locales/en.ts`。`locales/ui.ts` が引く）
 *
 * 将来、別に配布できるよう準備するための切れ目で、外部配布の仕組みはまだ作らない。
 */
import { defineAsyncComponent, h, type Component } from 'vue'

import { messages as eventViewMessages } from './event_view/locales/en'

export interface ViewPlugin {
  /** プラグインの識別子 */
  id: string
  /** プラグインの版 */
  version: string
  /** ルート（`/p/:key/<segment>`） */
  route: {
    segment: string
    /** 到達に必要な権限（`meta.permission`） */
    permission: string
    /** ページ（遅延読み込み） */
    page: () => Promise<Component | { default: Component }>
    /** **`TicketViewsPage` に載せる**——詳細を開いてもページを再マウントさせない */
    inTicketViews: true
    /** 詳細を浮かせて開くときの `from` の値 */
    from: string
  }
  /** メインメニュー（4.1・4.3） */
  menu: {
    /** 文言（日本語の原文。`uiText` で訳す） */
    label: string
    icon: string | Component
    /** 本体のどの項目の次に置くか */
    after: 'gantt'
    /** 表示を決める設定キー（`settings.views.<key>`）と、キーが無いときの既定 */
    settingKey: string
    defaultVisible: boolean
  }
  /** プロジェクト設定の「表示するビュー」（5.9.1） */
  settings: {
    /** チェックの横に添える説明（日本語の原文） */
    description: string
    /**
     * チェックを入れているあいだだけ下に出す、プラグイン固有の設定欄。
     * `settings` を受け、差し替えた `settings` を `update` で返す
     */
    panel?: () => Promise<Component | { default: Component }>
  }
  /** 使う API（`openapi.yaml` の公開された口だけ）。文書のための一覧 */
  api: readonly string[]
}

/**
 * メニューのアイコンも遅延読み込みにする。**この表は `locales/ui.ts` が引く**ため、
 * `.vue` を静的に import すると、文言を使うだけの e2e（`ticket-groups.spec.ts` など）まで
 * `.vue` を読み込もうとして落ちる（Playwright の変換器は `.vue` を解釈できない）。
 */
const MainMenuIcon = defineAsyncComponent(() => import('../components/MainMenuIcon.vue'))

/**
 * 登録したプラグイン。**いまは1件**（タイムライン。pb-296）。
 *
 * 名前を `…Sources` で終えるのは、日本語の原文を `uiText` で引く前提の表である
 * ことを i18n の検査（`scripts/check-i18n.mjs`）に伝えるため。
 */
const viewPluginSources: readonly ViewPlugin[] = [
  {
    id: 'event_view',
    version: '1.0.0',
    route: {
      segment: 'timeline',
      permission: 'ticket.view',
      page: () => import('./event_view/TimelinePage.vue'),
      inTicketViews: true,
      from: 'timeline',
    },
    menu: {
      label: 'タイムライン',
      icon: () => h(MainMenuIcon, { name: 'clock' }),
      after: 'gantt',
      settingKey: 'timeline',
      defaultVisible: false,
    },
    settings: {
      description: 'イベント当日の進行を、エピックを行にして見る',
      panel: () => import('./event_view/ViewSettings.vue'),
    },
    api: [
      'GET /projects/:key/tickets?view=timeline',
      'POST /projects/:key/tickets/:seq/reschedule',
      'GET /projects/:key/schedule-baselines',
      'GET /projects/:key/schedule-baselines/:id',
      'POST /projects/:key/schedule-baselines',
      'DELETE /projects/:key/schedule-baselines/:id',
      'PATCH /projects/:key/tickets/:seq',
      'POST /projects/:key/tickets/:seq/transition',
      'GET /projects/:key',
      'PATCH /projects/:key',
    ],
  },
]

export const viewPlugins: readonly ViewPlugin[] = viewPluginSources

/** プラグインの英訳（日本語の原文 → 英語）。`locales/ui.ts` が本体の英訳の後に引く */
export const pluginEnglishMessages: Record<string, string> = { ...eventViewMessages }

/** パス（`/p/:key/<segment>`）か `from` からプラグインを引く。どれでもなければ `undefined` */
export function viewPluginOf(path: string, from: unknown): ViewPlugin | undefined {
  return viewPlugins.find((p) => path.endsWith(`/${p.route.segment}`) || from === p.route.from)
}
