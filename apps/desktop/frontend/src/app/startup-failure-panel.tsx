import type { ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Alert, AlertDescription } from "@/components/ui/alert"
import type { StartupProgress } from "./startup-progress"

export function StartupFailurePanel({ progress, actions }: { progress: StartupProgress | null; actions: ReactNode }) {
  const { t } = useTranslation()
  const code = progress?.failure_code ?? "unknown"
  const message = `app:startup.failures.${code}`
  return (
    <Alert variant="destructive" className="w-full max-w-xl rounded-none border-0 border-l-2 bg-transparent p-0 pl-4 text-foreground [overflow-wrap:anywhere]">
      <h2 className="text-sm font-semibold">{t(`${message}.title`)}</h2>
      <AlertDescription>
        <p className="mt-1 text-muted-foreground">{t(`${message}.description`)}</p>
        <p className="mt-4 font-medium">{t("app:startup.recoveryTitle")}</p>
        <ol className="mt-1 list-decimal space-y-1 pl-4 text-text-secondary">
          <li>{t(`${message}.steps.0`)}</li>
          <li>{t(`${message}.steps.1`)}</li>
        </ol>
        <div className="mt-4 space-y-1 text-muted-foreground">
          {progress ? <p>{t("app:startup.failedStage", { stage: t(`app:startup.stages.${progress.stage}`) })}</p> : null}
          <p>{t("app:startup.errorCode", { code })}</p>
        </div>
        {actions}
        <p className="mt-2 text-muted-foreground">{t("app:startup.logHelp")}</p>
      </AlertDescription>
    </Alert>
  )
}
