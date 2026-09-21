/// <reference types="vite/client" />

import type { uiText } from './src/locales/ui'

declare module 'vue' {
  interface ComponentCustomProperties {
    $ui: typeof uiText
  }
}
