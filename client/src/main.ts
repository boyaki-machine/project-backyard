import { createApp } from 'vue'
import { createPinia } from 'pinia'

import App from './App.vue'
import { router } from './router'
import { useUiStore } from './stores/ui'
import './styles/tokens.css'
import './styles/base.css'

const app = createApp(App)

app.use(createPinia())
app.use(router)

// テーマと色相を <html> へ反映してから描画する（GuiDesign.md 8.11）。
// 描画後に当てると、既定のライトから一瞬切り替わって見える。
useUiStore().init()

app.mount('#app')
