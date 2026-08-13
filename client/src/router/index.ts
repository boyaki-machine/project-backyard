import { createRouter, createWebHistory } from 'vue-router'

import { routes } from './routes'

/**
 * ルーター（GuiDesign.md 3.2）。
 *
 * 認証・権限によるガード（GuiDesign.md 7.2）は auth ストアを前提とするため、
 * 手順8で beforeEach として足す。手順7の時点ではルート定義のみを持つ。
 */
export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior() {
    return { top: 0 }
  },
})
