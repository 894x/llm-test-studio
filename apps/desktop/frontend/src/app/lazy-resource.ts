import { useSyncExternalStore } from "react"

export type ResourceState<T> = {
  data: T | null
  error: unknown
  pending: boolean
  stale: boolean
  revision: number
}

// Reads begin only when a consumer asks for the resource. Invalidation preserves
// the displayed data while preventing an older request from replacing a write.
export function createLazyResource<T>(read: () => Promise<T>) {
  let state: ResourceState<T> = { data: null, error: null, pending: false, stale: true, revision: 0 }
  let request: { revision: number; promise: Promise<T> } | undefined
  const listeners = new Set<() => void>()
  const publish = (next: ResourceState<T>) => {
    state = next
    listeners.forEach(listener => listener())
  }
  const refresh = (): Promise<T> => {
    if (request?.revision === state.revision) return request.promise
    const previous = request?.promise
    const revision = state.revision
    publish({ ...state, error: null, pending: true })
    const current = {
      revision,
      promise: (previous ? previous.catch(() => undefined).then(read) : Promise.resolve().then(read)).then(
        data => {
          if (revision === state.revision) publish({ ...state, data, error: null, pending: false, stale: false })
          return data
        },
        error => {
          if (revision === state.revision) publish({ ...state, error, pending: false })
          throw error
        },
      ).finally(() => { if (request === current) request = undefined }),
    }
    request = current
    return current.promise
  }
  return {
    subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener) } },
    getSnapshot: () => state,
    load: () => state.data !== null && !state.stale ? Promise.resolve(state.data) : refresh(),
    refresh,
    invalidate() { publish({ ...state, stale: true, revision: state.revision + 1, pending: false }) },
    set(data: T) { publish({ data, error: null, pending: false, stale: false, revision: state.revision + 1 }) },
  }
}

export type LazyResource<T> = ReturnType<typeof createLazyResource<T>>

export function useResource<T>(resource: LazyResource<T>): ResourceState<T> {
  return useSyncExternalStore(resource.subscribe, resource.getSnapshot)
}
