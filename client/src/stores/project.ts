/**
 * プロジェクト一覧の取得状態（`GuiDesign.md` 7.1 の project ストア）。
 *
 * 7.1 は「選択中プロジェクト、プロジェクト一覧のキャッシュ」を持つと定めるが、
 * 手順10a で要るのは一覧のみである。選択中プロジェクトは `/p/:key` の実画面
 * （手順18以降）で必要になった時点で足す。現状の切替メニュー（4.4）は
 * `GET /me` の `projects[]` とルートの `:key` で足りている。
 *
 * 4状態（読み込み中・空・エラー・正常）を持つのは `GuiDesign.md` 6.2 の規約。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import * as projectsApi from '../api/projects'
import type { ProjectListItem, ProjectSort, SortOrder } from '../api/projects'
import { ApiError } from '../api/client'

/** 1ページの件数。`ApiDesign.md` 2.6 / 5.1 のサーバ既定と同じ値を明示する */
const PER_PAGE = 25

export const useProjectStore = defineStore('project', () => {
  const items = ref<ProjectListItem[]>([])

  const page = ref(1)
  const perPage = ref(PER_PAGE)
  const total = ref(0)
  const totalPages = ref(0)

  /** 既定は最終更新の降順（`GuiDesign.md` 5.2） */
  const sort = ref<ProjectSort>('updated_at')
  const order = ref<SortOrder>('desc')

  /** アーカイブ済みは既定で非表示（5.2）。表示するときは status=all を送る */
  const includeArchived = ref(false)

  const loading = ref(false)
  const error = ref<ApiError | null>(null)

  /** 一度でも取得に成功したか。読み込み中のスケルトンと再描画の出し分けに使う */
  const loaded = ref(false)

  /**
   * 応答の追い越しを防ぐ通し番号。
   *
   * 列ヘッダを続けて押すと複数のリクエストが並ぶ。遅れて届いた古い応答で
   * 新しい結果を上書きすると、画面のソート順と表示内容がずれる。
   */
  let seq = 0

  const isEmpty = computed(() => loaded.value && items.value.length === 0)

  async function fetch(): Promise<void> {
    const mine = ++seq
    loading.value = true
    error.value = null
    try {
      const res = await projectsApi.listProjects({
        status: includeArchived.value ? 'all' : 'active',
        sort: sort.value,
        order: order.value,
        page: page.value,
        per_page: perPage.value,
      })
      if (mine !== seq) return
      items.value = res.items
      page.value = res.page
      perPage.value = res.per_page
      total.value = res.total
      totalPages.value = res.total_pages
      loaded.value = true
    } catch (e: unknown) {
      if (mine !== seq) return
      error.value =
        e instanceof ApiError
          ? e
          : new ApiError({
              status: 0,
              code: 'internal_error',
              message: '予期しないエラーが発生しました',
            })
      // エラー時は前回の内容を残さない。古い一覧を新しい条件の結果として
      // 見せないため（6.2 のエラー状態は原因＋再試行を出す）。
      items.value = []
    } finally {
      if (mine === seq) loading.value = false
    }
  }

  /**
   * ソート列を選ぶ。同じ列なら向きを反転する。
   *
   * 列を切り替えたときの初期の向きは設計文書に記述がない。文字列の列は昇順、
   * 数値・日時の列は降順から始める（既定の `updated_at desc` と揃う）。
   */
  function toggleSort(next: ProjectSort): void {
    if (sort.value === next) {
      order.value = order.value === 'asc' ? 'desc' : 'asc'
    } else {
      sort.value = next
      order.value = next === 'name' || next === 'key' ? 'asc' : 'desc'
    }
    page.value = 1
    void fetch()
  }

  function setIncludeArchived(next: boolean): void {
    if (includeArchived.value === next) return
    includeArchived.value = next
    page.value = 1
    void fetch()
  }

  function goToPage(next: number): void {
    if (next < 1 || next > totalPages.value || next === page.value) return
    page.value = next
    void fetch()
  }

  return {
    items,
    page,
    perPage,
    total,
    totalPages,
    sort,
    order,
    includeArchived,
    loading,
    error,
    loaded,
    isEmpty,
    fetch,
    toggleSort,
    setIncludeArchived,
    goToPage,
  }
})
