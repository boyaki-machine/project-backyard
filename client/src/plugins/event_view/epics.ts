/**
 * エピックの候補（載せるエピック・行の絞り込み）と袖章（`GuiDesign.md` 5.4.4・5.15）。
 *
 * `GET /tickets?type=epic` を2本取る——全件（棚に戻ったものも。袖章の順位と題名）と、
 * 棚に戻っていないもの（棚に戻ったものを畳むため）。並びは `sort_key`（サーバの既定）。
 */
import { computed, ref } from 'vue'

import * as ticketsApi from '../../api/tickets'
import type { Ticket } from '../../api/tickets'
import { sleeveMap } from '../../lib/epicSleeve'
import type { EpicChoice } from './EpicChecklist.vue'

export function useEpicChoices() {
  const all = ref<Ticket[]>([])
  const activeSeqs = ref<Set<number>>(new Set())
  const loaded = ref(false)

  async function load(key: string): Promise<void> {
    try {
      const [everything, active] = await Promise.all([
        ticketsApi.listTickets(key, { type: 'epic', retired: 'true' }),
        ticketsApi.listTickets(key, { type: 'epic' }),
      ])
      all.value = everything.items
      activeSeqs.value = new Set(active.items.map((t) => t.seq))
    } catch {
      // 候補が出ないだけ。画面は止めない（袖章は灰になる）
      all.value = []
      activeSeqs.value = new Set()
    } finally {
      loaded.value = true
    }
  }

  const choices = computed<EpicChoice[]>(() =>
    all.value.map((t) => ({ seq: t.seq, title: t.title, retired: !activeSeqs.value.has(t.seq) })),
  )
  const sleeve = computed(() => sleeveMap(all.value))

  return { all, choices, sleeve, loaded, load }
}
