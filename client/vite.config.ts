import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'

// 開発時は Vite（:5173）で配信し、/api と /mcp だけを Go サーバ（:8080）へ中継する。
// 配布時は client/dist を server/internal/webui/dist/ へコピーして embed する（Design.md 3.4）。
export default defineConfig({
  plugins: [vue()],
  server: {
    port: 5173,
    fs: {
      // リポジトリ直下の VERSION を ?raw で読む（client/src/version.ts）。
      // 既定でも許可されることが多いが、判定に依存させない。
      allow: ['..'],
    },
    // 別のポートへ黙ってずれると Cookie の送り先が変わるため、空いていなければ失敗させる。
    strictPort: true,
    proxy: {
      // /healthcheck は監視用であり画面からは呼ばないため中継しない（ApiDesign.md 2.11）。
      '/api': { target: 'http://127.0.0.1:8080' },
      '/mcp': { target: 'http://127.0.0.1:8080' },
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
