import { createApp } from 'vue'
import { createPinia } from 'pinia'

import { setUnauthorizedHandler } from './api/client'
import App from './App.vue'
import { i18n } from './i18n'
import { router } from './router'
import { REDIRECT_QUERY } from './router/guards'
import { useAuthStore } from './stores/auth'
import { useUiStore } from './stores/ui'
import './styles/tokens.css'
import './styles/base.css'

const app = createApp(App)

app.use(createPinia())
app.use(i18n)
app.use(router)

// テーマと色相を <html> へ反映してから描画する（GuiDesign.md 8.11）。
// 描画後に当てると、既定のライトから一瞬切り替わって見える。
useUiStore().init()

// セッションが失効した状態で API を叩いたときの共通処理。
// api/client.ts からストアやルータを import すると循環するため、ここで結ぶ。
setUnauthorizedHandler(() => {
  const auth = useAuthStore()
  if (!auth.isAuthenticated) return
  auth.clear()
  const from = router.currentRoute.value.fullPath
  void router.replace({ path: '/login', query: { [REDIRECT_QUERY]: from } })
})

app.mount('#app')
