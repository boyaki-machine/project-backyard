import { createRouter, createWebHistory } from 'vue-router'

import { authGuard } from './guards'
import { routes } from './routes'

/**
 * ルーター（GuiDesign.md 3.2）。
 *
 * 認証・権限によるガード（GuiDesign.md 7.2）は guards.ts が持つ。
 */
export const router = createRouter({
  history: createWebHistory(),
  routes,
  scrollBehavior() {
    return { top: 0 }
  },
})

router.beforeEach(authGuard)
