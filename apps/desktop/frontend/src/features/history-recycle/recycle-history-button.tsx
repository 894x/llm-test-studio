import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import Trash2Icon from "lucide-react/dist/esm/icons/trash-2.mjs"
import TriangleAlertIcon from "lucide-react/dist/esm/icons/triangle-alert.mjs"
import { publicDesktopErrorMessage, type DesktopClient } from "@/app/desktop-client"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { HistoryChanged, HistoryRecyclePreview, HistoryRecycleTarget } from "./data"

export type HistoryRecycleClient = Pick<DesktopClient, "previewHistoryRecycle" | "recycleHistory">

export function RecycleHistoryButton({ client, target, onHistoryChanged, onRecycled, compact = false, disabled = false, name }: {
  client: HistoryRecycleClient
  target: HistoryRecycleTarget
  onHistoryChanged?: HistoryChanged
  onRecycled?: HistoryChanged
  compact?: boolean
  disabled?: boolean
  name?: string
}) {
  const { t } = useTranslation("historyRecycle")
  const [open, setOpen] = useState(false)
  const [preview, setPreview] = useState<HistoryRecyclePreview | null>(null)
  const [pending, setPending] = useState(false)
  const [error, setError] = useState("")
  const [refreshError, setRefreshError] = useState(false)
  const inFlight = useRef(false)
  const request = useRef(0)
  const inspect = async () => {
    if (inFlight.current) return
    const sequence = ++request.current
    inFlight.current = true
    setOpen(true); setPending(true); setError(""); setRefreshError(false); setPreview(null)
    try {
      const value = await client.previewHistoryRecycle(target)
      if (sequence === request.current) setPreview(value)
    } catch (reason) {
      if (sequence === request.current) setError(publicDesktopErrorMessage(reason, t("moveFailed")))
    } finally {
      if (sequence === request.current) { inFlight.current = false; setPending(false) }
    }
  }
  const move = async () => {
    if (!preview?.can_recycle || inFlight.current) return
    inFlight.current = true; setPending(true); setError("")
    try {
      const entry = await client.recycleHistory({ kind: preview.entry.kind, id: preview.entry.id })
      setOpen(false)
      const refreshes = await Promise.allSettled([
        Promise.resolve().then(() => onRecycled?.(entry, "recycle")),
        Promise.resolve().then(() => onHistoryChanged?.(entry, "recycle")),
      ])
      setRefreshError(refreshes.some(result => result.status === "rejected"))
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("moveFailed")))
    } finally { inFlight.current = false; setPending(false) }
  }
  return <>
    <Button type="button" variant="ghost" size={compact ? "icon-sm" : "sm"} disabled={disabled || pending}
      aria-label={name ? t("moveAria", { title: name }) : t("move")} title={compact ? t("move") : undefined}
      onClick={event => { event.stopPropagation(); void inspect() }}><Trash2Icon />{compact ? null : t("move")}</Button>
    {refreshError ? compact ? <TooltipProvider><Tooltip><TooltipTrigger asChild>
      <span role="status" tabIndex={0} className="inline-flex size-7 shrink-0 items-center justify-center rounded-md text-warning-strong outline-none focus-visible:ring-2 focus-visible:ring-ring">
        <TriangleAlertIcon className="size-4" /><span className="sr-only">{t("movedRefreshFailed")}</span>
      </span>
    </TooltipTrigger><TooltipContent>{t("movedRefreshFailed")}</TooltipContent></Tooltip></TooltipProvider>
      : <p role="status" className="max-w-[240px] whitespace-normal text-xs text-warning-strong [overflow-wrap:anywhere]">{t("movedRefreshFailed")}</p> : null}
    <AlertDialog open={open} onOpenChange={next => {
      if (!next && pending && preview) return
      setOpen(next)
      if (!next) { request.current++; inFlight.current = false; setPending(false) }
    }}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("confirmTitle")}</AlertDialogTitle>
          <AlertDialogDescription>{t("confirmDescription")}</AlertDialogDescription>
        </AlertDialogHeader>
        {pending && !preview ? <p role="status" className="flex items-center gap-2 text-xs text-muted-foreground"><Spinner />{t("checking")}</p> : null}
        {preview ? <div className="space-y-2 text-xs">
          <p className="font-medium [overflow-wrap:anywhere]">{preview.entry.title}</p>
          <p className="text-muted-foreground">{t("affected", { runs: preview.entry.run_ids.length, reports: preview.entry.report_ids.length })}</p>
          <p className="text-muted-foreground">{t("localHint")}</p>
          {preview.entry.remote ? <p className="text-muted-foreground">{t("remoteHint")}</p> : null}
          {!preview.can_recycle ? <p role="status" className="text-warning-strong">{t(`blocked.${preview.blocked_reason}`)}</p> : null}
        </div> : null}
        {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={pending && !!preview}>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction disabled={pending || !preview?.can_recycle} onClick={event => { event.preventDefault(); void move() }}>
            {pending && preview ? <Spinner /> : null}{t("move")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </>
}
