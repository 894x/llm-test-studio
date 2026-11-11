import { useState, type RefObject } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetDescription, SheetFooter, SheetHeader, SheetTitle } from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import { Progress } from "@/components/ui/progress"
import { Alert, AlertDescription } from "@/components/ui/alert"
import type { DesktopUpdates } from "./use-desktop-updates"

export function UpdateSheet({ updates, open, onOpenChange, returnFocus }: {
  updates: DesktopUpdates
  open: boolean
  onOpenChange: (open: boolean) => void
  returnFocus?: RefObject<HTMLElement | null>
}) {
  const { t } = useTranslation("shell")
  const { snapshot, pending, readError, run, client } = updates
  const [confirmation, setConfirmation] = useState("")
  const confirmed = !!snapshot?.latest_version && confirmation === snapshot.latest_version
  const busy = pending || snapshot?.status === "downloading" || snapshot?.status === "installing"
  const downloadable = snapshot && ["available", "skipped", "error"].includes(snapshot.status) && !!snapshot.latest_version
  const progress = snapshot && snapshot.total > 0 ? Math.min(100, Math.round(snapshot.downloaded / snapshot.total * 100)) : 0
  return <Sheet open={open} onOpenChange={next => { setConfirmation(""); onOpenChange(next) }}>
    <SheetContent className="min-w-0 data-[side=right]:w-[420px] data-[side=right]:sm:max-w-[420px]"
      onCloseAutoFocus={event => {
        if (returnFocus?.current?.isConnected) {
          event.preventDefault()
          returnFocus.current.focus()
        }
      }}>
      <SheetHeader>
        <SheetTitle>{t("updates.title")}</SheetTitle>
        <SheetDescription>{t("updates.description")}</SheetDescription>
      </SheetHeader>
      <ScrollArea className="min-h-0 flex-1" contentWidth="viewport">
        <div className="flex flex-col gap-4 px-4 pb-4 text-xs [overflow-wrap:anywhere]">
          {snapshot ? <>
            <dl className="flex flex-col gap-3">
              <div><dt className="text-muted-foreground">{t("updates.current")}</dt><dd className="mt-1 font-medium">{snapshot.current_version || t("updates.unknown")}</dd></div>
              {snapshot.latest_version ? <div><dt className="text-muted-foreground">{t("updates.latest")}</dt><dd className="mt-1 font-medium">{snapshot.latest_version}</dd></div> : null}
            </dl>
            <p role="status" className="flex items-center gap-2 text-muted-foreground">{busy ? <Spinner /> : null}{pending ? t("updates.working") : t(`updates.states.${snapshot.status}`)}</p>
            <label className="flex items-center gap-2"><Checkbox checked={snapshot.auto_check} disabled={busy || snapshot.status === "unsupported"}
              onCheckedChange={value => void run(() => client.setDesktopUpdatePreferences(value === true, false))} />{t("updates.autoCheck")}</label>
            {snapshot.status === "downloading" ? <div className="flex flex-col gap-2">
              <Progress value={progress} aria-label={t("updates.downloadProgress")} />
              <p className="tabular-nums text-muted-foreground">{t("updates.progress", { percent: progress, downloaded: (snapshot.downloaded / 1048576).toFixed(1), total: (snapshot.total / 1048576).toFixed(1) })}</p>
            </div> : null}
            {snapshot.error_code ? <Alert variant="destructive"><AlertDescription>{t(`updates.errors.${snapshot.error_code}`)}</AlertDescription></Alert> : null}
            {snapshot.notes ? <section className="flex flex-col gap-2"><h3 className="font-medium">{t("updates.releaseNotes")}</h3><p className="whitespace-pre-wrap text-muted-foreground">{snapshot.notes}</p></section> : null}
            {snapshot.status === "ready" ? <div className="flex flex-col gap-3">
              <p className="text-muted-foreground">{snapshot.install_kind === "manual" ? t("updates.macInstall") : t("updates.installHint")}</p>
              {snapshot.install_kind === "restart" ? <label className="flex items-start gap-2"><Checkbox className="mt-0.5" checked={confirmed} disabled={pending}
                onCheckedChange={value => setConfirmation(value === true ? snapshot.latest_version : "")} />{t("updates.confirm")}</label> : null}
            </div> : null}
          </> : <p role="status" className="text-muted-foreground">{t("updates.loading")}</p>}
          {readError ? <Alert variant="destructive"><AlertDescription>{t("updates.readError")}</AlertDescription></Alert> : null}
        </div>
      </ScrollArea>
      <SheetFooter className="flex-row flex-wrap justify-end gap-2">
        <Button variant="ghost" onClick={() => { void client.openDesktopUpdateRelease().catch(() => {}) }}>{t("updates.releasePage")}</Button>
        <Button variant="secondary" disabled={busy || snapshot?.status === "ready" || snapshot?.status === "unsupported"}
          onClick={() => void run(() => client.checkDesktopUpdate())}>{t("updates.check")}</Button>
        {snapshot?.status === "downloading" ? <Button variant="secondary" disabled={pending} onClick={() => void run(() => client.cancelDesktopUpdate())}>{t("updates.cancel")}</Button> : null}
        {downloadable ? <>
          <Button variant="ghost" disabled={busy} onClick={() => void run(() => client.setDesktopUpdatePreferences(snapshot.auto_check, true))}>{t("updates.skip")}</Button>
          <Button disabled={busy} onClick={() => void run(() => client.downloadDesktopUpdate())}>{t("updates.download")}</Button>
        </> : null}
        {snapshot?.status === "ready" ? <Button disabled={pending || (snapshot.install_kind === "restart" && !confirmed)}
          onClick={() => void run(() => client.installDesktopUpdate())}>{snapshot.install_kind === "manual" ? t("updates.openInstaller") : t("updates.install")}</Button> : null}
      </SheetFooter>
    </SheetContent>
  </Sheet>
}
