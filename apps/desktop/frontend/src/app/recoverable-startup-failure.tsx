import { useRef, useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import FolderOpenIcon from "lucide-react/dist/esm/icons/folder-open.mjs"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Alert, AlertDescription } from "@/components/ui/alert"
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle } from "@/components/ui/alert-dialog"
import { StartupFailurePanel } from "./startup-failure-panel"
import type { StartupProgress } from "./startup-progress"
import type { DesktopClient } from "./desktop-client"
import type { DatabaseRecoveryResult } from "./database-recovery-data"

export function RecoverableStartupFailure({ progress, client, actions, onContinue }: {
  progress: StartupProgress | null
  client: Pick<DesktopClient, "backupAndCreateDatabase" | "openDatabaseRecoveryDirectory">
  actions: ReactNode
  onContinue: () => void
}) {
  const { t } = useTranslation()
  const [confirming, setConfirming] = useState(false)
  const [pending, setPending] = useState(false)
  const busy = useRef(false)
  const [result, setResult] = useState<DatabaseRecoveryResult | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [opening, setOpening] = useState(false)
  const [openError, setOpenError] = useState(false)
  const [reopenRequired, setReopenRequired] = useState(false)

  const recover = async () => {
    if (busy.current) return
    busy.current = true
    setConfirming(false)
    setPending(true)
    setError(null)
    try {
      const next = await client.backupAndCreateDatabase()
      if (next.status === "completed") setResult(next)
      else {
        setReopenRequired(next.reopen_required === true)
        setError(next.reopen_required ? t("app:startup.databaseRecovery.reopenRequired") : t("app:startup.databaseRecovery.failed", { reason: t(`app:startup.failures.${next.failure_code ?? "unknown"}.description`) }))
      }
    } catch {
      setReopenRequired(true)
      setError(t("app:startup.databaseRecovery.transportFailed"))
    } finally {
      busy.current = false
      setPending(false)
    }
  }
  const openBackup = async () => {
    setOpening(true)
    setOpenError(false)
    try { await client.openDatabaseRecoveryDirectory() }
    catch { setOpenError(true) }
    finally { setOpening(false) }
  }

  if (result?.status === "completed") return (
    <Alert className="w-full max-w-xl border-0 bg-transparent p-0 [overflow-wrap:anywhere]">
      <h2 className="text-sm font-semibold">{t("app:startup.databaseRecovery.completedTitle")}</h2>
      <AlertDescription>
        <p className="mt-2 text-text-secondary">{t("app:startup.databaseRecovery.completed")}</p>
        <p className="mt-2 text-muted-foreground">{t("app:startup.databaseRecovery.sendToAdmin")}</p>
        {!result.workspace_ready ? <p className="mt-2 text-destructive">{t("app:startup.databaseRecovery.workspaceNotReady")}</p> : null}
        <div className="mt-4 flex flex-wrap gap-2">
          <Button size="sm" onClick={onContinue}>{t(result.workspace_ready ? "app:startup.databaseRecovery.continue" : "app:startup.databaseRecovery.viewFailure")}</Button>
          <Button size="sm" variant="outline" disabled={opening} aria-busy={opening} onClick={() => void openBackup()}>
            {opening ? <Spinner /> : <FolderOpenIcon />}{t("app:startup.databaseRecovery.openBackup")}
          </Button>
        </div>
        {openError ? <p role="status" className="mt-2 text-destructive">{t("app:startup.databaseRecovery.openFailed")}</p> : null}
      </AlertDescription>
    </Alert>
  )

  return <>
    <StartupFailurePanel progress={progress} actions={<>
      {progress?.recovery_available && !reopenRequired ? <div className="mt-4 space-y-2">
        <p className="text-text-secondary">{t("app:startup.databaseRecovery.offer")}</p>
        <Button size="sm" disabled={pending} aria-busy={pending} onClick={() => setConfirming(true)}>
          {pending ? <Spinner /> : null}{t(pending ? "app:startup.databaseRecovery.pending" : "app:startup.databaseRecovery.action")}
        </Button>
      </div> : null}
      {error ? <p role="status" className="mt-2 text-destructive">{error}</p> : null}
      {actions}
    </>} />
    <AlertDialog open={confirming} onOpenChange={setConfirming}>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t("app:startup.databaseRecovery.confirmTitle")}</AlertDialogTitle>
          <AlertDialogDescription asChild><div className="space-y-2">
            <p>{t("app:startup.databaseRecovery.confirmDescription")}</p>
            <p>{t("app:startup.databaseRecovery.sendToAdmin")}</p>
          </div></AlertDialogDescription>
        </AlertDialogHeader>
        <AlertDialogFooter>
          <AlertDialogCancel>{t("app:startup.databaseRecovery.cancel")}</AlertDialogCancel>
          <Button onClick={() => void recover()}>{t("app:startup.databaseRecovery.confirm")}</Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  </>
}
