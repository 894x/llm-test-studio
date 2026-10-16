import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react"

import { DesktopClientError, type DesktopClient } from "@/app/desktop-client"
import type { RemoteNode, RemoteNodeUpgradeAction, RemoteNodeUpgradeView, StartRemoteNodeUpgradeCommand } from "./data"

export type RemoteUpgradeClient = Pick<DesktopClient, "startRemoteNodeUpgrade" | "getRemoteNodeUpgrade" |
  "retryRemoteNodeUpgrade" | "recoverRemoteNodeUpgrade">
type TrackedUpgrade = {
  command: StartRemoteNodeUpgradeCommand | null
  view: RemoteNodeUpgradeView | null
  pending: "start" | "retry" | "recover" | ""
  error: string
  conflict: boolean
}
const EMPTY: TrackedUpgrade = { command: null, view: null, pending: "", error: "", conflict: false }
const POLL_MS = 1500
const PRE_INTENT_REJECTIONS = new Set(["remote_node_invalid", "remote_node_revision_conflict", "remote_node_not_found",
  "remote_node_re_pair_required", "remote_node_busy", "remote_node_identity_conflict", "remote_node_upgrade_required",
  "remote_node_credential_unavailable"])

export function isUpgradeTerminal(view: RemoteNodeUpgradeView | null): boolean {
  return view?.stage === "completed" || view?.stage === "rolled_back"
}

function changedAt(view: RemoteNodeUpgradeView): string {
  const [seconds, fraction = ""] = view.changed_at.replace(/Z$/, "").split(".")
  return `${seconds}.${fraction.padEnd(9, "0")}`
}

function latestView(previous: RemoteNodeUpgradeView | null, next: RemoteNodeUpgradeView): RemoteNodeUpgradeView {
  return previous?.operation_id === next.operation_id && changedAt(previous) > changedAt(next) ? previous : next
}

export function observedUpgradeRunner(node: RemoteNode, view: RemoteNodeUpgradeView | null) {
  if (!view?.actual_runner) return node.runner
  if (!node.runner || !node.checked_at) return view.actual_runner
  const checked = `${new Date(node.checked_at).toISOString().slice(0, 19)}.${(node.checked_at.match(/\.(\d+)/)?.[1] ?? "").padEnd(9, "0")}`
  return checked > changedAt(view) ? node.runner : view.actual_runner
}

function effective(node: RemoteNode, tracked?: TrackedUpgrade): TrackedUpgrade {
  const local = tracked ?? EMPTY
  const durable = node.upgrade
  if (local.command && !isUpgradeTerminal(local.view)) {
    if (durable && durable.operation_id !== local.command.operation_id && !isUpgradeTerminal(durable)) {
      return { ...local, conflict: true, error: "errors.remote_node_identity_conflict" }
    }
    if (!durable || durable.operation_id !== local.command.operation_id) return local
  }
  const view = local.view && (!durable || (local.view.operation_id === durable.operation_id && changedAt(local.view) >= changedAt(durable)))
    ? local.view : durable
  return { ...local, view }
}

export function useRemoteNodeUpgrades(client: RemoteUpgradeClient, nodes: RemoteNode[], disabled: boolean) {
  const [tracked, setTracked] = useState<Record<string, TrackedUpgrade>>({})
  const trackedRef = useRef(tracked)
  const nodesRef = useRef(nodes)
  useLayoutEffect(() => { nodesRef.current = nodes }, [nodes])
  const mounted = useRef(false)
  const generation = useRef(0)
  const readSequence = useRef(new Map<string, number>())
  const update = useCallback((id: string, transform: (previous: TrackedUpgrade) => TrackedUpgrade) => {
    if (!mounted.current) return
    const next = { ...trackedRef.current, [id]: transform(trackedRef.current[id] ?? EMPTY) }
    trackedRef.current = next
    setTracked(next)
  }, [])
  const get = (node: RemoteNode) => effective(node, tracked[node.id])
  const getLatest = (id: string) => {
    const node = nodesRef.current.find(node => node.id === id)
    return node ? effective(node, trackedRef.current[id]) : trackedRef.current[id] ?? EMPTY
  }
  const endSession = useCallback(() => { mounted.current = false; ++generation.current }, [])
  useEffect(() => { mounted.current = true; return endSession }, [client, endSession])

  const read = useCallback(async (id: string, operationID: string, valid: () => boolean = () => true) => {
    const session = generation.current
    const sequence = (readSequence.current.get(id) ?? 0) + 1
    readSequence.current.set(id, sequence)
    const current = () => mounted.current && generation.current === session && valid() && readSequence.current.get(id) === sequence
    try {
      const view = await client.getRemoteNodeUpgrade(id)
      if (!current()) return
      if (view && (view.configuration_id !== id || view.operation_id !== operationID)) {
        update(id, previous => ({ ...previous, conflict: true, error: "errors.remote_node_identity_conflict" }))
        return
      }
      update(id, previous => {
        if (!view) return { ...previous, error: previous.view ? "upgrade.missingOperation" : previous.error }
        return { ...previous, view: latestView(previous.view, view), error: "" }
      })
      return view
    } catch {
      if (current()) update(id, previous => ({ ...previous, error: previous.error || "upgrade.readError" }))
    }
  }, [client, update])

  const active = nodes.flatMap(node => {
    const operation = get(node)
    const operationID = operation.view?.operation_id ?? operation.command?.operation_id
    return operationID && !isUpgradeTerminal(operation.view) && !operation.conflict ? [{ id: node.id, operationID }] : []
  })
  const activeKey = active.map(item => `${item.id}:${item.operationID}`).join("|")
  useEffect(() => {
    if (!activeKey) return
    const operations = activeKey.split("|").map(item => { const [id, operationID] = item.split(":"); return { id, operationID } })
    let live = true
    let timer: ReturnType<typeof setTimeout> | undefined
    const poll = async () => {
      await Promise.all(operations.map(operation => read(operation.id, operation.operationID, () => live)))
      if (live) timer = setTimeout(() => void poll(), POLL_MS)
    }
    timer = setTimeout(() => void poll(), POLL_MS)
    return () => { live = false; clearTimeout(timer) }
  }, [activeKey, read])

  const perform = async (node: RemoteNode, kind: TrackedUpgrade["pending"], start?: StartRemoteNodeUpgradeCommand) => {
    const previous = getLatest(node.id)
    if (disabled || previous.pending || previous.conflict) return
    const command = start ?? previous.command
    const operationID = start?.operation_id ?? previous.view?.operation_id ?? command?.operation_id
    if (!operationID) return
    if (kind === "recover" && (!previous.view?.restorable || previous.view.resume_attempted || isUpgradeTerminal(previous.view))) return
    const session = generation.current
    readSequence.current.set(node.id, (readSequence.current.get(node.id) ?? 0) + 1)
    update(node.id, current => ({ ...current, command: start ?? current.command, view: start ? null : current.view,
      pending: kind, error: "" }))
    const action: RemoteNodeUpgradeAction = { id: node.id, expected_revision: node.revision, operation_id: operationID }
    let rejection = ""
    try {
      const view = kind === "recover" ? await client.recoverRemoteNodeUpgrade(action)
        : kind === "start" && command ? await client.startRemoteNodeUpgrade(command)
          : await client.retryRemoteNodeUpgrade(action)
      if (mounted.current && generation.current === session) {
        if (view.configuration_id !== node.id || view.operation_id !== operationID) {
          update(node.id, current => ({ ...current, conflict: true, error: "errors.remote_node_identity_conflict" }))
        } else update(node.id, current => ({ ...current, view: latestView(current.view, view), error: "" }))
      }
    } catch (error) {
      if (error instanceof DesktopClientError) rejection = error.code
      if (mounted.current && generation.current === session) update(node.id, current => ({ ...current,
        error: error instanceof DesktopClientError ? `errors.${error.code}` : "upgrade.commandError" }))
    } finally {
      if (mounted.current && generation.current === session) {
        const observed = await read(node.id, operationID)
        update(node.id, current => ({ ...current, pending: "",
          ...(kind === "start" && observed === null && PRE_INTENT_REJECTIONS.has(rejection) ? { command: null, view: null } : {}) }))
      }
    }
  }

  return {
    get,
    locked: (node: RemoteNode) => {
      const operation = get(node)
      return Boolean(operation.pending || operation.conflict || (!isUpgradeTerminal(operation.view) && (operation.command || operation.view)))
    },
    lockedNow: (node: RemoteNode) => {
      const operation = getLatest(node.id)
      return Boolean(operation.pending || operation.conflict || (!isUpgradeTerminal(operation.view) && (operation.command || operation.view)))
    },
    start: (node: RemoteNode) => {
      const operation = getLatest(node.id)
      const runner = observedUpgradeRunner(node, operation.view)
      if (operation.pending || operation.conflict || (!isUpgradeTerminal(operation.view) && (operation.command || operation.view)) ||
        !runner || runner.active > 0 || runner.queued > 0 || runner.pending > 0) return
      const command: StartRemoteNodeUpgradeCommand = { id: node.id, expected_revision: node.revision,
        operation_id: crypto.randomUUID(), expected_build_id: runner.build_id }
      void perform(node, "start", command)
    },
    retry: (node: RemoteNode) => {
      const operation = getLatest(node.id)
      if (isUpgradeTerminal(operation.view)) return
      void perform(node, operation.view ? "retry" : "start", operation.view ? undefined : operation.command ?? undefined)
    },
    recover: (node: RemoteNode) => { void perform(node, "recover") },
    refresh: (node: RemoteNode) => {
      const operation = getLatest(node.id)
      const operationID = operation.view?.operation_id ?? operation.command?.operation_id
      if (operationID) void read(node.id, operationID)
    },
  }
}
