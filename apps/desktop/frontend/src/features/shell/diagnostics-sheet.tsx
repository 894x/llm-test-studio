import { useRef, useState, type ReactNode } from "react"
import FileJsonIcon from "lucide-react/dist/esm/icons/file-json.mjs"
import FolderOpenIcon from "lucide-react/dist/esm/icons/folder-open.mjs"
import { useTranslation } from "react-i18next"

import {
  publicDesktopErrorMessage,
  type DesktopClient,
  type DesktopDiagnosticsSnapshot,
} from "@/app/desktop-client"
import { Button } from "@/components/ui/button"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet"
import { Spinner } from "@/components/ui/spinner"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

export function DiagnosticsSheet({ client }: { client: DesktopClient }) {
  const { t } = useTranslation("shell")
  const [snapshot, setSnapshot] = useState<DesktopDiagnosticsSnapshot | null>(null)
  const [loadError, setLoadError] = useState("")
  const [openingDirectory, setOpeningDirectory] = useState(false)
  const [commandError, setCommandError] = useState("")
  const loadGeneration = useRef(0)

  const load = async (generation: number) => {
    setSnapshot(null)
    setLoadError("")
    try {
      const next = await client.getDiagnostics()
      if (loadGeneration.current === generation) setSnapshot(next)
    } catch (error) {
      if (loadGeneration.current === generation) {
        setLoadError(publicDesktopErrorMessage(error, t("diagnostics.loadError")))
      }
    }
  }

  const openDirectory = async () => {
    setOpeningDirectory(true)
    setCommandError("")
    try {
      await client.openDiagnosticsDirectory()
    } catch (error) {
      setCommandError(publicDesktopErrorMessage(error, t("diagnostics.openError")))
    } finally {
      setOpeningDirectory(false)
    }
  }

  const maxFileMB = snapshot
    ? Math.round(snapshot.max_file_bytes / (1024 * 1024))
    : 0
  const correlations = snapshot
    ? [
        snapshot.run_correlation ? "Run" : "",
        snapshot.request_correlation ? "Request" : "",
      ].filter(Boolean)
    : []

  return (
    <Sheet
      onOpenChange={(open) => {
        const generation = ++loadGeneration.current
        if (open) {
          setCommandError("")
          void load(generation)
        }
      }}
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <SheetTrigger asChild>
            <Button variant="ghost" size="icon-sm" aria-label={t("diagnostics.title")}>
              <FileJsonIcon />
            </Button>
          </SheetTrigger>
        </TooltipTrigger>
        <TooltipContent side="bottom">{t("diagnostics.title")}</TooltipContent>
      </Tooltip>
      <SheetContent>
        <SheetHeader className="border-b">
          <SheetTitle>{t("diagnostics.title")}</SheetTitle>
          <SheetDescription>
            {t("diagnostics.description")}
          </SheetDescription>
        </SheetHeader>

        <div className="flex-1 px-4">
          {!snapshot && !loadError ? (
            <div className="flex items-center gap-2 py-3 text-xs text-muted-foreground">
              <Spinner /> {t("diagnostics.loading")}
            </div>
          ) : loadError ? (
            <div role="alert" className="border-l-2 border-destructive pl-3 text-xs text-destructive">
              {loadError}
            </div>
          ) : snapshot ? (
            <dl className="divide-y text-xs">
              <DiagnosticRow label={t("diagnostics.status")}>
                {snapshot.available ? t("diagnostics.available") : t("diagnostics.unavailable")}
              </DiagnosticRow>
              <DiagnosticRow label={t("diagnostics.format")}>JSON Lines</DiagnosticRow>
              <DiagnosticRow label={t("diagnostics.maxFileSize")}>{maxFileMB} MB</DiagnosticRow>
              <DiagnosticRow label={t("diagnostics.retention")}>
                {t("diagnostics.retentionValue", { count: snapshot.backup_files })}
              </DiagnosticRow>
              <DiagnosticRow label={t("diagnostics.correlation")}>
                {correlations.length === 2
                  ? t("diagnostics.bothCorrelations")
                  : correlations.join(", ") || t("diagnostics.notEnabled")}
              </DiagnosticRow>
              <DiagnosticRow label={t("diagnostics.sensitiveData")}>{t("diagnostics.redaction")}</DiagnosticRow>
            </dl>
          ) : null}
          {commandError ? (
            <p role="alert" className="mt-3 text-xs text-destructive">
              {commandError}
            </p>
          ) : null}
        </div>

        <SheetFooter className="border-t">
          <Button
            onClick={() => void openDirectory()}
            disabled={!snapshot?.available || openingDirectory}
          >
            {openingDirectory ? <Spinner className="text-primary-foreground" /> : <FolderOpenIcon />}
            {t("diagnostics.openFolder")}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}

function DiagnosticRow({
  label,
  children,
}: {
  label: string
  children: ReactNode
}) {
  return (
    <div className="flex items-center justify-between gap-4 py-3">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="text-right font-medium text-foreground">{children}</dd>
    </div>
  )
}
