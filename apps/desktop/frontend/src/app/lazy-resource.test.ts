import { describe, expect, it, vi } from "vitest"
import { createLazyResource } from "./lazy-resource"

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

describe("on-demand resources", () => {
  it("does no work until requested, shares reads, and reuses completed data", async () => {
    const pending = deferred<string>()
    const read = vi.fn(() => pending.promise)
    const resource = createLazyResource(read)
    expect(read).not.toHaveBeenCalled()
    const first = resource.load()
    expect(resource.load()).toBe(first)
    pending.resolve("data")
    await first
    await expect(resource.load()).resolves.toBe("data")
    expect(read).toHaveBeenCalledTimes(1)
  })

  it("queues a fresh read after invalidation and never publishes the stale response", async () => {
    const pending = deferred<string>()
    const read = vi.fn().mockReturnValueOnce(pending.promise).mockResolvedValue("new")
    const resource = createLazyResource<string>(read)
    const old = resource.load()
    await Promise.resolve()
    resource.invalidate()
    const current = resource.load()
    expect(resource.load()).toBe(current)
    expect(read).toHaveBeenCalledTimes(1)
    pending.resolve("old")
    await old
    expect(resource.getSnapshot().data).not.toBe("old")
    await current
    expect(resource.getSnapshot().data).toBe("new")
    expect(read).toHaveBeenCalledTimes(2)
  })

  it("keeps an authoritative write when an earlier read finishes", async () => {
    const pending = deferred<string>()
    const resource = createLazyResource(() => pending.promise)
    const read = resource.load()
    resource.set("saved")
    pending.resolve("before save")
    await read
    await expect(resource.load()).resolves.toBe("saved")
  })

  it("keeps loaded data after a refresh error and permits explicit retry", async () => {
    const read = vi.fn().mockResolvedValueOnce("loaded").mockRejectedValueOnce(new Error("offline")).mockResolvedValue("retried")
    const resource = createLazyResource<string>(read)
    await resource.load()
    await expect(resource.refresh()).rejects.toThrow("offline")
    expect(resource.getSnapshot().data).toBe("loaded")
    await resource.refresh()
    expect(resource.getSnapshot().error).toBeNull()
    expect(resource.getSnapshot().data).toBe("retried")
  })
})
