/**
 * 画面に出すバージョン（`GuiDesign.md` 5.1 のフッタ、4.2 のユーザーメニュー）。
 *
 * リポジトリ直下の `VERSION`（`Design.md` 11.1 の正本）をビルド時に埋め込む。
 * `/healthcheck` から取得しないのは、既定でバージョンを返さない設定
 * （`PB_HEALTH_SHOW_VERSION=false`、`ApiDesign.md` 2.11）と衝突するため。
 *
 * `?raw` は Vite の機能で、型は `vite/client` に含まれる（依存を増やしていない）。
 */
import versionRaw from '../../VERSION?raw'

export const APP_VERSION = versionRaw.trim()
