/**
 * プロジェクトの状態（`GuiDesign.md` 7.1 の project ストア）。
 *
 * 7.1 が定める「選択中プロジェクト、プロジェクト一覧のキャッシュ」の両方を持つ。
 * 一覧は手順10a、選択中プロジェクトは手順11b（プロジェクト設定画面）で足した。
 * **2つは独立している**——一覧を取り直しても選択中は変わらないし、その逆も同じ。
 *
 * 4状態（読み込み中・空・エラー・正常）を持つのは `GuiDesign.md` 6.2 の規約。
 */
import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import * as projectsApi from '../api/projects'
import type { ProjectDetail, ProjectListItem, ProjectSort, SortOrder } from '../api/projects'
import { ApiError } from '../api/client'
import { uiText } from '../locales/ui'

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
              message: uiText('予期しないエラーが発生しました'),
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

  // ── 選択中プロジェクト（`GuiDesign.md` 7.1）────────────────────────
  //
  // `GET /projects/:key`（5.4）の応答をそのまま持つ。ワークフローもメンバーも
  // 自分の実効権限も同じ応答に入るため、設定画面はこの1件で描ける。

  const current = ref<ProjectDetail | null>(null)
  const currentLoading = ref(false)
  const currentError = ref<ApiError | null>(null)

  /** 取得済みの `current` がどのキーのものか。ルートの `:key` と突き合わせる */
  const currentKey = computed(() => current.value?.key ?? null)
  /**
   * 予定日時の終日を区切る基準タイムゾーン（`DbDesign.md` 6.23。pb-217）。
   * 読み込み前の一瞬だけは端末のタイムゾーンで代用する——空のまま日付を出せないため。
   */
  const planTimezone = computed(
    () => current.value?.timezone ?? Intl.DateTimeFormat().resolvedOptions().timeZone,
  )

  /** 一覧と同じ追い越し対策。プロジェクトを続けて切り替えたときに古い応答で上書きしない */
  let currentSeq = 0

  /**
   * 選択中プロジェクトを取得する。
   *
   * 常に問い合わせる（キャッシュを返さない）。設定画面は `version` を持ち帰って
   * 楽観ロックに使うため、**古い値を掴んだまま保存すると必ず 409 になる**。
   */
  async function fetchCurrent(key: string): Promise<void> {
    const mine = ++currentSeq
    currentLoading.value = true
    currentError.value = null
    try {
      const res = await projectsApi.getProject(key)
      if (mine !== currentSeq) return
      current.value = res
    } catch (e: unknown) {
      if (mine !== currentSeq) return
      currentError.value =
        e instanceof ApiError
          ? e
          : new ApiError({
              status: 0,
              code: 'internal_error',
              message: uiText('予期しないエラーが発生しました'),
            })
      current.value = null
    } finally {
      if (mine === currentSeq) currentLoading.value = false
    }
  }

  /**
   * 更新系の応答（5.4 と同形式）で選択中を差し替える。
   *
   * **進行中の取得より新しい**ので、通し番号を進めて追い越しを止める。
   * これをしないと、保存の直後に遅れて届いた取得結果が古い `version` を戻す。
   */
  function setCurrent(next: ProjectDetail): void {
    currentSeq++
    current.value = next
    currentError.value = null
  }

  /** 画面を離れるときに捨てる。次に開いたとき前のプロジェクトが一瞬見えないように */
  function clearCurrent(): void {
    currentSeq++
    current.value = null
    currentError.value = null
    currentLoading.value = false
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
    current,
    currentKey,
    planTimezone,
    currentLoading,
    currentError,
    fetchCurrent,
    setCurrent,
    clearCurrent,
  }
})
