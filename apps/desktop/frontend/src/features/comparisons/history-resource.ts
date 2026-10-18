import { createLazyResource } from "@/app/lazy-resource"
import type { DesktopClient } from "@/app/desktop-client"
import { EMPTY_COMPARISONS, type ComparisonSnapshot } from "./data"

export function createComparisonHistoryResource(read: DesktopClient["getComparisons"]) {
  const queued = new Map<string, number>()
  let revision = 0
  const resource = createLazyResource<ComparisonSnapshot>(async (): Promise<ComparisonSnapshot> => {
    const pending = new Map(queued)
    const head = await read("")
    const previous = resource.getSnapshot().data ?? EMPTY_COMPARISONS
    const headIDs = new Set(head.comparisons.map(item => item.id))
    const active = [...new Set([
      ...previous.comparisons.filter(item => item.status === "running").map(item => item.id),
      ...pending.keys(),
    ])].filter(id => !headIDs.has(id))
    const refreshed: ComparisonSnapshot["comparisons"] = []
    for (let offset = 0; offset < active.length; offset += 100) {
      const update = await read("", active.slice(offset, offset + 100))
      refreshed.push(...update.comparisons)
    }
    const current = resource.getSnapshot().data ?? EMPTY_COMPARISONS
    const merged = new Map(current.comparisons.map(item => [item.id, item]))
    const overlaps = head.comparisons.some(item => merged.has(item.id))
    for (const item of [...head.comparisons, ...refreshed]) {
      merged.set(item.id, item)
      if (pending.has(item.id) && pending.get(item.id) === queued.get(item.id)) queued.delete(item.id)
    }
    return { ...head, comparisons: [...merged.values()], next_cursor: overlaps ? current.next_cursor : head.next_cursor }
  })
  const queueRun = (runID: string) => {
    const ids = (resource.getSnapshot().data?.comparisons ?? [])
      .filter(item => item.channels.some(channel => channel.run_id === runID)).map(item => item.id)
    ids.forEach(id => queued.set(id, ++revision))
    if (ids.length) resource.invalidate()
    return ids
  }
  return { resource, queueRun }
}
