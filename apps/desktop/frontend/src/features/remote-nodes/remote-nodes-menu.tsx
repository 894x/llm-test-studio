import { useCallback, useEffect, useId, useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import ArrowLeftIcon from "lucide-react/dist/esm/icons/arrow-left.mjs"
import DownloadIcon from "lucide-react/dist/esm/icons/download.mjs"
import MonitorIcon from "lucide-react/dist/esm/icons/monitor.mjs"
import PencilIcon from "lucide-react/dist/esm/icons/pencil.mjs"
import PlusIcon from "lucide-react/dist/esm/icons/plus.mjs"
import RefreshCwIcon from "lucide-react/dist/esm/icons/refresh-cw.mjs"
import ServerIcon from "lucide-react/dist/esm/icons/server.mjs"
import ShieldCheckIcon from "lucide-react/dist/esm/icons/shield-check.mjs"
import Trash2Icon from "lucide-react/dist/esm/icons/trash-2.mjs"

import { DesktopClientError, type DesktopClient } from "@/app/desktop-client"
import { Alert } from "@/components/ui/alert"
import { Badge } from "@/components/ui/badge"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"

import { RemoteNodeEditor } from "./remote-node-editor"
import { RemoteNodeUpgrade } from "./remote-node-upgrade"
import { RemoteNodeScheduling } from "./remote-node-scheduling"
import { observedUpgradeRunner, useRemoteNodeUpgrades } from "./use-remote-node-upgrades"
import type { RemoteNode, RemoteNodeDraft, RemoteNodesSnapshot, RemoteRunner } from "./data"

export type RemoteNodesClient = Pick<DesktopClient, "getRemoteNodes" | "saveRemoteNode" | "refreshRemoteNode" |
  "confirmRemoteNodeHostKey" | "selectDefaultExecution" | "retryRemoteInstallation" | "removeRemoteNode" |
  "startRemoteNodeUpgrade" | "getRemoteNodeUpgrade" | "retryRemoteNodeUpgrade" | "recoverRemoteNodeUpgrade" |
  "setRemoteNodeTaskConcurrency">

const POLL_INTERVAL = 1500
const MAX_POLLS = 40
const EMPTY_NODES: RemoteNode[] = []
const INSTALLATION_STAGES = new Set(["checking", "uploading", "account", "program", "configuration", "service", "verifying", "completed"])
const INSTALLATION_ERRORS = new Set(["runner_release_unavailable", "runner_release_upgrade_required", "runner_release_integrity_failed",
  "runner_upgrade_required", "runner_installation_failed", "installation_root_required", "unsupported_installation_platform",
  "installation_busy", "installation_operation_conflict", "installation_path_not_owned", "node_pairing_repair_required",
  "installation_integrity_failed", "invalid_installation_request"])

function draftFromNode(node: RemoteNode): RemoteNodeDraft {
  return { id: node.id, expected_revision: node.revision, name: node.name, region: node.region, host: node.host,
    port: node.port, user: node.user, enabled: node.enabled, authentication: node.authentication_kind,
    password: "", private_key: "", passphrase: "", remove_passphrase: false }
}

function newDraft(): RemoteNodeDraft {
  return { id: crypto.randomUUID(), expected_revision: 0, name: "", region: "", host: "", port: 22, user: "",
    enabled: true, authentication: "password", password: "", private_key: "", passphrase: "", remove_passphrase: false }
}

export function RemoteNodesMenu({ client, disabled = false }: { client: RemoteNodesClient; disabled?: boolean }) {
  const { t, i18n } = useTranslation("remoteNodes")
  const defaultID = useId()
  const [open, setOpen] = useState(false)
  const [snapshot, setSnapshot] = useState<RemoteNodesSnapshot | null>(null)
  const [editor, setEditor] = useState<RemoteNodeDraft | null>(null)
  const [busy, setBusy] = useState("")
  const [loading, setLoading] = useState(false)
  const [blocked, setBlocked] = useState(true)
  const [error, setError] = useState("")
  const [notice, setNotice] = useState("")
  const [polls, setPolls] = useState(0)
  const upgrades = useRemoteNodeUpgrades(client, snapshot?.nodes ?? EMPTY_NODES, disabled || blocked || Boolean(busy))
  const mounted = useRef(false)
  const opened = useRef(false)
  const generation = useRef(0)
  const readSequence = useRef(0)
  const activeCommand = useRef("")
  const pendingSaveID = useRef("")
  const current = useCallback((value: number) => mounted.current && generation.current === value, [])
  const endSession = useCallback(() => { mounted.current = false; ++generation.current }, [])

  const safeError = (value: unknown) => value instanceof DesktopClientError &&
    i18n.exists(`errors.${value.code}`, { ns: "remoteNodes" }) ? `errors.${value.code}` : "operationError"

  const read = useCallback(async (session: number) => {
    const sequence = ++readSequence.current
    if (current(session)) setLoading(true)
    try {
      const next = await client.getRemoteNodes()
      if (!current(session) || sequence !== readSequence.current) return null
      setSnapshot(next)
      setBlocked(false)
      if (pendingSaveID.current) {
        const saved = next.nodes.find(node => node.id === pendingSaveID.current)
        if (saved) setEditor(draftFromNode(saved))
        pendingSaveID.current = ""
      }
      return next
    } catch {
      if (current(session) && sequence === readSequence.current) {
        setBlocked(true)
        setError("loadError")
      }
      return null
    } finally {
      if (current(session) && sequence === readSequence.current) setLoading(false)
    }
  }, [client, current])

  useEffect(() => {
    mounted.current = true
    void read(generation.current)
    return endSession
  }, [read, endSession])

  const changeOpen = (next: boolean) => {
    ++generation.current
    opened.current = next
    setOpen(next)
    setEditor(null)
    setError("")
    setNotice("")
    setBusy(activeCommand.current)
    setPolls(0)
    setLoading(false)
    if (next) {
      setBlocked(true)
      void read(generation.current)
    }
  }

  const checking = snapshot?.nodes.some(node => node.checking || node.status === "installing") ?? false
  useEffect(() => {
    if (!open || !checking || polls >= MAX_POLLS || busy || blocked) return
    const session = generation.current
    const timer = setTimeout(() => {
      if (!current(session)) return
      setPolls(value => value + 1)
      void read(session)
    }, POLL_INTERVAL)
    return () => clearTimeout(timer)
  }, [open, checking, polls, busy, blocked, current, read])

  const command = async (name: string, operation: () => Promise<void>) => {
    if (activeCommand.current || blocked || disabled || !opened.current) return null
    const session = generation.current
    ++readSequence.current
    activeCommand.current = name
    setBusy(name)
    setError("")
    setNotice("")
    let succeeded = false
    try {
      await operation()
      succeeded = true
    } catch (cause) {
      if (current(session)) setError(safeError(cause))
    }
    // A rejected write may already have committed. Reconcile before another command.
    if (current(session)) setBlocked(true)
    const next = current(session) ? await read(session) : null
    activeCommand.current = ""
    if (current(session)) {
      setBusy("")
      setPolls(0)
      if (succeeded && next) setNotice("updated")
    } else if (mounted.current && opened.current) {
      setBusy("")
      void read(generation.current)
    }
    return current(session) ? { next, succeeded } : null
  }

  const save = async (draft: RemoteNodeDraft) => {
    const node = snapshot?.nodes.find(node => node.id === draft.id)
    if (node && upgrades.lockedNow(node)) return
    const result = await command("save", async () => {
      try { await client.saveRemoteNode(draft) }
      finally { pendingSaveID.current = draft.id }
    })
    if (!result) return
    const saved = result.next?.nodes.find(node => node.id === draft.id)
    if (result.succeeded) setEditor(null)
    else if (saved) setEditor(draftFromNode(saved))
  }

  const unavailable = disabled || blocked || Boolean(busy)
  const selectedNode = snapshot?.default.kind === "remote"
    ? snapshot.nodes.find(node => node.id === snapshot.default.configuration_id) : null
  const defaultLabel = selectedNode?.name ?? (snapshot ? t("local") : t("loading"))

  return <Sheet open={open} onOpenChange={changeOpen}>
    <Tooltip>
      <TooltipTrigger asChild><SheetTrigger asChild>
        <Button variant="ghost" size="icon-sm" disabled={disabled} aria-label={t("title")}>
          {!snapshot ? <Spinner /> : snapshot.default.kind === "local" ? <MonitorIcon /> : <ServerIcon />}
        </Button>
      </SheetTrigger></TooltipTrigger>
      <TooltipContent side="bottom">{t("currentDefault", { name: defaultLabel })}</TooltipContent>
    </Tooltip>
    <SheetContent className="min-h-0 min-w-0 gap-0 data-[side=right]:w-[min(600px,100vw)] data-[side=right]:sm:max-w-[600px]">
      <SheetHeader className="shrink-0 pr-12">
        <SheetTitle>{t("title")}</SheetTitle>
        <SheetDescription>{t("description")}</SheetDescription>
      </SheetHeader>
      <ScrollArea className="min-h-0 flex-1" contentWidth="viewport">
        <div className="min-w-0 space-y-4 px-4 pb-4">
          <div className="space-y-1.5">
            <label htmlFor={defaultID} className="text-xs font-medium">{t("defaultLocation")}</label>
            <SearchableSelect id={defaultID} aria-label={t("defaultLocation")}
              value={!snapshot ? "" : snapshot.default.kind === "remote" ? snapshot.default.configuration_id : "local"}
              placeholder={t("loading")}
              disabled={unavailable || !snapshot}
              options={[{ value: "local", label: t("local"), icon: <MonitorIcon className="size-4" /> }, ...(snapshot?.nodes ?? []).map(node => ({
                value: node.id, label: `${node.name}${node.region ? ` · ${node.region}` : ""} · ${t(`status.${node.status}`)}`,
                icon: <ServerIcon className="size-4" />,
              }))]}
              onValueChange={value => {
                if (!snapshot) return
                void command("default", () => client.selectDefaultExecution({ expected_revision: snapshot.default_revision,
                  location: value === "local" ? { kind: "local", configuration_id: "" } : { kind: "remote", configuration_id: value } }))
              }} />
            <p className="text-[11px] text-muted-foreground">{t("defaultHelp")}</p>
          </div>

          {error && <Alert variant="destructive">{t(error)}</Alert>}
          {blocked && !loading && <Button variant="outline" size="sm" onClick={() => { setError(""); void read(generation.current) }}>
            <RefreshCwIcon />{t("reload")}
          </Button>}
          <div role="status" aria-live="polite" hidden={!busy && !loading && !notice} className="text-xs text-muted-foreground">
            {busy ? <span className="inline-flex items-center gap-2"><Spinner />{t("working")}</span>
              : loading ? <span className="inline-flex items-center gap-2"><Spinner />{t("loading")}</span>
                : notice ? t(notice) : null}
          </div>

          {editor ? <>
            <Button variant="ghost" size="sm" disabled={Boolean(busy)} onClick={() => setEditor(null)}><ArrowLeftIcon />{t("back")}</Button>
            <RemoteNodeEditor key={`${editor.id}:${editor.expected_revision}`} draft={editor}
              node={snapshot?.nodes.find(node => node.id === editor.id)} disabled={unavailable} saving={busy === "save"}
              onSave={draft => void save(draft)} />
          </> : <>
            <div className="flex items-center justify-between gap-2">
              <h3 className="text-sm font-medium">{t("nodes")}</h3>
              <Button variant="outline" size="sm" disabled={unavailable} onClick={() => { setNotice(""); setEditor(newDraft()) }}><PlusIcon />{t("add")}</Button>
            </div>
            {snapshot?.nodes.length === 0 && <p className="py-5 text-xs text-muted-foreground">{t("empty")}</p>}
            <div className="space-y-3">
              {snapshot?.nodes.map(node => <section key={node.id} aria-label={node.name} className="min-w-0 space-y-3 rounded-lg bg-surface-subtle p-3">
                <div className="flex min-w-0 items-start justify-between gap-2">
                  <div className="min-w-0 [overflow-wrap:anywhere]">
                    <h4 className="inline text-sm font-medium">{node.name}</h4>
                    {selectedNode?.id === node.id && <span className="text-[11px] text-muted-foreground">
                      <span aria-hidden="true" className="mx-1.5">·</span>{t("isDefault")}
                    </span>}
                  </div>
                  <Badge variant="outline" className={node.status === "ready" ? "border-transparent bg-success-soft text-success-strong"
                    : node.status === "offline" ? "border-transparent bg-warning-soft text-warning-strong" : undefined}>
                    {node.checking && <Spinner />}{t(`status.${node.status}`)}
                  </Badge>
                </div>
                <div className="flex min-w-0 items-center justify-between gap-3">
                  <p className="min-w-0 text-xs text-muted-foreground [overflow-wrap:anywhere]">{node.region && `${node.region} · `}{node.user}@{node.host}:{node.port}</p>
                  <div className="flex shrink-0 items-center gap-1">
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={t("edit")} disabled={unavailable || upgrades.locked(node)}
                          onClick={() => { if (!upgrades.lockedNow(node)) setEditor(draftFromNode(node)) }}><PencilIcon /></Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("edit")}</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={t("check")} disabled={unavailable || node.checking || upgrades.locked(node)}
                          onClick={() => { if (!upgrades.lockedNow(node)) void command(`check:${node.id}`,
                            () => client.refreshRemoteNode({ id: node.id, expected_revision: node.revision })) }}><RefreshCwIcon /></Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("check")}</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button variant="ghost" size="icon-sm" aria-label={t("install")} disabled={unavailable || node.checking || upgrades.locked(node)}
                          onClick={() => { if (!upgrades.lockedNow(node)) void command(`install:${node.id}`,
                            () => client.retryRemoteInstallation({ id: node.id, expected_revision: node.revision })) }}><DownloadIcon /></Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("install")}</TooltipContent>
                    </Tooltip>
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button variant="destructive" size="icon-sm" aria-label={t("remove")} disabled={unavailable || upgrades.locked(node)}
                          onClick={() => { if (!upgrades.lockedNow(node)) void command(`remove:${node.id}`,
                            () => client.removeRemoteNode({ id: node.id, expected_revision: node.revision })) }}><Trash2Icon /></Button>
                      </TooltipTrigger>
                      <TooltipContent>{t("remove")}</TooltipContent>
                    </Tooltip>
                  </div>
                </div>
                <NodeDetails node={node} runner={observedUpgradeRunner(node, upgrades.get(node).view)} />
                {node.runner && <RemoteNodeScheduling key={`${node.id}:${node.runner.max_active_jobs}`}
                  node={node} runner={node.runner} disabled={unavailable || node.checking || !node.eligible || upgrades.locked(node)}
                  saving={busy === `scheduling:${node.id}`} onSave={value => {
                    if (!upgrades.lockedNow(node)) void command(`scheduling:${node.id}`, () => client.setRemoteNodeTaskConcurrency(value))
                  }} />}
                {node.proposed_host_key && <Alert>
                  <p className="font-medium">{t(node.status === "host_changed" ? "hostChanged" : "hostUnverified")}</p>
                  <p className="mt-1">{t("verifyFingerprint")}</p>
                  {node.host_key && <p className="mt-2 font-mono [overflow-wrap:anywhere]">{t("previousFingerprint")} {node.host_key.fingerprint}</p>}
                  <p className="mt-2 text-muted-foreground">{t("observedFingerprint")} · {node.proposed_host_key.type}</p>
                  <p className="font-mono [overflow-wrap:anywhere]">{node.proposed_host_key.fingerprint}</p>
                  <Button variant="outline" size="sm" className="mt-3" disabled={unavailable || node.checking || upgrades.locked(node)}
                    onClick={() => void command(`trust:${node.id}`, () => client.confirmRemoteNodeHostKey({
                      id: node.id, expected_revision: node.revision, key: node.proposed_host_key!,
                    }))}><ShieldCheckIcon />{t("confirmFingerprint")}</Button>
                </Alert>}
                <RemoteNodeUpgrade node={node} controller={upgrades} disabled={unavailable} />
              </section>)}
            </div>
            {checking && polls >= MAX_POLLS && <Alert>{t("pollPaused")}<Button className="mt-2" variant="outline" size="sm"
              disabled={unavailable} onClick={() => { setPolls(0); void read(generation.current) }}><RefreshCwIcon />{t("reload")}</Button></Alert>}
          </>}
        </div>
      </ScrollArea>
    </SheetContent>
  </Sheet>
}

function NodeDetails({ node, runner }: { node: RemoteNode; runner: RemoteRunner | null }) {
  const { t } = useTranslation("remoteNodes")
  return <>
    {runner && <dl className="grid min-w-0 grid-cols-2 gap-x-4 gap-y-2 text-xs">
      <div className="min-w-0"><dt className="text-muted-foreground">{t("runner")}</dt><dd className="[overflow-wrap:anywhere]"><span className="block">{runner.version}</span><span className="block text-[11px] text-muted-foreground">{runner.os}/{runner.arch}</span></dd></div>
      <div><dt className="text-muted-foreground">{t("workload")}</dt><dd className="tabular-nums">{t("workloadValue", { active: runner.active, queued: runner.queued, pending: runner.pending })}</dd></div>
    </dl>}
    {node.installation_stage && node.installation_stage !== "completed" && <p className="text-xs text-muted-foreground">{t("installationStage")} · {t(`stage.${INSTALLATION_STAGES.has(node.installation_stage) ? node.installation_stage : "unknown"}`)}</p>}
    {node.installation_error && <Alert variant="destructive">{t(`installationError.${INSTALLATION_ERRORS.has(node.installation_error) ? node.installation_error : "unknown"}`)}</Alert>}
  </>
}
