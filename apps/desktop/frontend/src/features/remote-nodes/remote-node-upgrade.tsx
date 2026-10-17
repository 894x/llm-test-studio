import { useState } from "react"
import { useTranslation } from "react-i18next"
import { Collapsible } from "radix-ui"
import ArrowUpCircleIcon from "lucide-react/dist/esm/icons/arrow-up-circle.mjs"
import ChevronRightIcon from "lucide-react/dist/esm/icons/chevron-right.mjs"
import CopyIcon from "lucide-react/dist/esm/icons/copy.mjs"
import RefreshCwIcon from "lucide-react/dist/esm/icons/refresh-cw.mjs"
import RotateCcwIcon from "lucide-react/dist/esm/icons/rotate-ccw.mjs"

import { Alert } from "@/components/ui/alert"
import { AlertDialog, AlertDialogCancel, AlertDialogContent, AlertDialogDescription, AlertDialogFooter, AlertDialogHeader, AlertDialogTitle, AlertDialogTrigger } from "@/components/ui/alert-dialog"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import { copyReportTextToClipboard as copyTextToClipboard } from "@/features/reports/report-clipboard"
import type { RemoteNode } from "./data"
import { isUpgradeTerminal, observedUpgradeRunner, type useRemoteNodeUpgrades } from "./use-remote-node-upgrades"

export function RemoteNodeUpgrade({ node, controller, disabled }: {
  node: RemoteNode; controller: ReturnType<typeof useRemoteNodeUpgrades>; disabled: boolean
}) {
  const { t, i18n } = useTranslation("remoteNodes")
  const [confirmOpen, setConfirmOpen] = useState(false)
  const [copyStatus, setCopyStatus] = useState("")
  const operation = controller.get(node)
  const view = operation.view
  const runner = observedUpgradeRunner(node, view)
  const terminal = isUpgradeTerminal(view)
  const locked = controller.locked(node)
  const tasksBusy = Boolean(runner && (runner.active > 0 || runner.queued > 0 || runner.pending > 0))
  const canStart = !locked && node.enabled && node.has_authentication && Boolean(node.host_key) && Boolean(runner) && !node.checking && !tasksBusy
  const pending = Boolean(operation.pending)
  const active = Boolean(view || operation.command) && !terminal
  const error = operation.error && i18n.exists(operation.error, { ns: "remoteNodes" }) ? t(operation.error) : operation.error ? t("upgrade.commandError") : ""
  return <section aria-label={t("upgrade.title")} className="min-w-0 space-y-2">
    <Collapsible.Root className="min-w-0 space-y-2">
      <div className="flex min-w-0 items-center justify-between gap-2">
        {runner ? <Collapsible.Trigger asChild>
          <Button variant="ghost" size="sm" className="group -ml-2.5 text-muted-foreground">
            <ChevronRightIcon className="transition-transform group-data-[state=open]:rotate-90" />{t("upgrade.details")}
          </Button>
        </Collapsible.Trigger> : <p className="text-[11px] text-muted-foreground">{t("upgrade.noRunner")}</p>}
        {!active && <AlertDialog open={confirmOpen} onOpenChange={setConfirmOpen}>
          <AlertDialogTrigger asChild>
            <Button variant="outline" size="sm" disabled={disabled || !canStart}>
              <ArrowUpCircleIcon />{t("upgrade.start")}
            </Button>
          </AlertDialogTrigger>
          <AlertDialogContent>
            <AlertDialogHeader>
              <AlertDialogTitle>{t("upgrade.confirmTitle", { name: node.name })}</AlertDialogTitle>
              <AlertDialogDescription>{t("upgrade.help")}</AlertDialogDescription>
            </AlertDialogHeader>
            <AlertDialogFooter>
              <AlertDialogCancel>{t("upgrade.cancel")}</AlertDialogCancel>
              <Button disabled={disabled || !canStart} onClick={() => { if (disabled || !canStart) return; setConfirmOpen(false); controller.start(node) }}>
                {t("upgrade.confirm")}
              </Button>
            </AlertDialogFooter>
          </AlertDialogContent>
        </AlertDialog>}
      </div>
      {runner && <Collapsible.Content className="space-y-1">
        <dl className="min-w-0 text-[11px]">
          <dt className="flex items-center justify-between gap-2 text-muted-foreground">
            {t("upgrade.actualBuild")}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={t("upgrade.copyBuild")} onClick={async () => {
                  try { await copyTextToClipboard(runner.build_id); setCopyStatus("upgrade.copied") }
                  catch { setCopyStatus("upgrade.copyFailed") }
                }}><CopyIcon /></Button>
              </TooltipTrigger>
              <TooltipContent>{t("upgrade.copyBuild")}</TooltipContent>
            </Tooltip>
          </dt>
          <dd className="select-text font-mono [overflow-wrap:anywhere]">{runner.build_id}</dd>
        </dl>
        {copyStatus && <p role="status" className="text-[11px] text-muted-foreground">{t(copyStatus)}</p>}
      </Collapsible.Content>}
    </Collapsible.Root>
    {tasksBusy && !active && <p className="text-[11px] text-warning-strong">{t("upgrade.busy", { active: runner!.active, queued: runner!.queued, pending: runner!.pending })}</p>}
    {view && <>
      <div role="status" aria-live="polite" className="flex min-w-0 items-center gap-2 text-xs">
        {active && <Spinner />}<span>{t(`upgrade.stage.${view.stage}`)}</span>
      </div>
      <dl className="min-w-0 space-y-2 text-[11px]">
        <div className="min-w-0"><dt className="text-muted-foreground">{t("upgrade.targetBuild")}</dt><dd className="mt-0.5 font-mono [overflow-wrap:anywhere]">{view.target_build_id}</dd></div>
        <div className="min-w-0"><dt className="text-muted-foreground">{t("upgrade.operation")}</dt><dd className="mt-0.5 font-mono [overflow-wrap:anywhere]">{view.operation_id}</dd></div>
      </dl>
      {!view.confirmed && <Alert>{t("upgrade.unconfirmed")}</Alert>}
      {view.error_code && <Alert variant="destructive">{t(`upgrade.error.${view.error_code === "installation_busy" && view.stage === "pausing" ? "installation_busy_maintenance" : view.error_code}`, { defaultValue: t("upgrade.error.unknown") })}</Alert>}
      {view.resume_attempted && <p className="text-[11px] text-warning-strong">{t("upgrade.resumeAttempted")}</p>}
    </>}
    {!view && operation.command && <Alert>{t("upgrade.unconfirmed")}<p className="mt-1 font-mono [overflow-wrap:anywhere]">{operation.command.operation_id}</p></Alert>}
    {error && <Alert variant="destructive">{error}</Alert>}
    {active && <>
      <p className="text-[11px] text-muted-foreground">{t("upgrade.background")}</p>
      <div className="flex flex-wrap gap-2">
        <Button variant="ghost" size="sm" disabled={disabled || pending} onClick={() => controller.refresh(node)}><RefreshCwIcon />{t("upgrade.refresh")}</Button>
        <Button variant="outline" size="sm"
          disabled={disabled || pending || operation.conflict || (!view && tasksBusy)} onClick={() => controller.retry(node)}>
          {pending ? <Spinner /> : <RefreshCwIcon />}{t("upgrade.retry")}
        </Button>
        {view?.restorable && !view.resume_attempted && !terminal && <Button variant="outline" size="sm"
          disabled={disabled || pending || operation.conflict} onClick={() => controller.recover(node)}>
          <RotateCcwIcon />{t("upgrade.recover")}
        </Button>}
      </div>
    </>}
  </section>
}
