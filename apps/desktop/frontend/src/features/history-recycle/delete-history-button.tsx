import { useRef, useState } from "react"
import { useTranslation } from "react-i18next"
import Trash2Icon from "lucide-react/dist/esm/icons/trash-2.mjs"
import { publicDesktopErrorMessage, type DesktopClient } from "@/app/desktop-client"
import { AlertDialog, AlertDialogAction, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import type { RecycledHistoryEntry } from "./data"

export function DeleteHistoryButton({ client, entry, disabled, onBusyChange, onDeleted }: {
  client: Pick<DesktopClient, "getRecycledHistory" | "deleteRecycledHistory">
  entry?: RecycledHistoryEntry
  disabled: boolean
  onBusyChange: (busy: boolean) => void
  onDeleted: (entries: RecycledHistoryEntry[]) => Promise<void>
}) {
  const { t } = useTranslation("historyRecycle")
  const [open, setOpen] = useState(false)
  const [preparing, setPreparing] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const [confirmed, setConfirmed] = useState<RecycledHistoryEntry[]>([])
  const [error, setError] = useState("")
  const sequence = useRef(0)
  const pending = useRef(false)
  const trigger = useRef<HTMLButtonElement>(null)
  const inspect = async () => {
    if (pending.current) return
    const request = ++sequence.current
    pending.current = true
    setOpen(true); setPreparing(true); setConfirmed([]); setError(""); onBusyChange(true)
    try {
      let entries = entry ? [entry] : []
      if (!entry) {
        let cursor = ""
        const cursors = new Set<string>()
        do {
          if (cursors.has(cursor)) throw new Error("Repeated recycle cursor")
          cursors.add(cursor)
          const page = await client.getRecycledHistory(cursor)
          if (request !== sequence.current) return
          entries = [...entries, ...page.entries]
          if (entries.length > 10000) throw new Error("Recycle selection too large")
          cursor = page.next_cursor
        } while (cursor)
      }
      if (request === sequence.current) setConfirmed(entries.filter(row => !row.remote))
    } catch (reason) {
      if (request === sequence.current) setError(publicDesktopErrorMessage(reason, t("deleteLoadFailed")))
    } finally {
      if (request === sequence.current) { pending.current = false; setPreparing(false) }
    }
  }
  const remove = async () => {
    if (pending.current || !confirmed.length) return
    pending.current = true; setDeleting(true); setError("")
    try {
      const deleted = await client.deleteRecycledHistory(confirmed.map(row => ({ target: { kind: row.kind, id: row.id }, removed_at: row.removed_at })))
      setOpen(false)
      await onDeleted(deleted)
    } catch (reason) {
      setError(publicDesktopErrorMessage(reason, t("deleteFailed")))
    } finally {
      pending.current = false; setDeleting(false); onBusyChange(false)
    }
  }
  return <>
    <Button ref={trigger} variant="destructive" size="sm" disabled={disabled || entry?.remote}
      aria-label={entry ? t("deleteAria", { title: entry.title }) : t("emptyLocal")}
      onClick={() => void inspect()}><Trash2Icon />{entry ? t("delete") : t("emptyLocal")}</Button>
    <AlertDialog open={open} onOpenChange={next => {
      if (deleting) return
      setOpen(next)
      if (!next) { sequence.current++; pending.current = false; setPreparing(false); onBusyChange(false) }
    }}>
      <AlertDialogContent onCloseAutoFocus={event => { event.preventDefault(); trigger.current?.focus() }}>
        <AlertDialogHeader>
          <AlertDialogTitle>{entry ? t("deleteTitle") : t("emptyTitle")}</AlertDialogTitle>
          <AlertDialogDescription>{t("deleteDescription")}</AlertDialogDescription>
        </AlertDialogHeader>
        {preparing ? <p role="status" className="flex items-center gap-2 text-xs"><Spinner />{t("checking")}</p>
          : <div className="space-y-2 text-xs">
            {entry ? <p className="font-medium [overflow-wrap:anywhere]">{entry.title}</p> : null}
            <p>{t("deleteCount", { count: confirmed.length })}</p>
            <p className="text-muted-foreground">{t("deleteScope")}</p>
          </div>}
        {error ? <p role="alert" className="text-xs text-destructive">{error}</p> : null}
        <AlertDialogFooter>
          <AlertDialogCancel disabled={deleting}>{t("cancel")}</AlertDialogCancel>
          <AlertDialogAction disabled={preparing || deleting || !confirmed.length} onClick={event => { event.preventDefault(); void remove() }}>
            {deleting ? <Spinner /> : null}{t("deleteConfirm")}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </>
}
