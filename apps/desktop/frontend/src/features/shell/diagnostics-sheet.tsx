import { useRef, useState, type ReactNode } from "react"
import FileJsonIcon from "lucide-react/dist/esm/icons/file-json.mjs"
import FolderOpenIcon from "lucide-react/dist/esm/icons/folder-open.mjs"

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
        setLoadError(publicDesktopErrorMessage(error, "无法读取诊断状态"))
      }
    }
  }

  const openDirectory = async () => {
    setOpeningDirectory(true)
    setCommandError("")
    try {
      await client.openDiagnosticsDirectory()
    } catch (error) {
      setCommandError(publicDesktopErrorMessage(error, "无法打开日志目录"))
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
            <Button variant="ghost" size="icon-sm" aria-label="诊断信息">
              <FileJsonIcon />
            </Button>
          </SheetTrigger>
        </TooltipTrigger>
        <TooltipContent side="bottom">诊断信息</TooltipContent>
      </Tooltip>
      <SheetContent>
        <SheetHeader className="border-b">
          <SheetTitle>诊断信息</SheetTitle>
          <SheetDescription>
            查看本地日志能力，并从系统文件管理器打开日志目录。
          </SheetDescription>
        </SheetHeader>

        <div className="flex-1 px-4">
          {!snapshot && !loadError ? (
            <div className="flex items-center gap-2 py-3 text-xs text-muted-foreground">
              <Spinner /> 正在读取诊断状态…
            </div>
          ) : loadError ? (
            <div role="alert" className="border-l-2 border-destructive pl-3 text-xs text-destructive">
              {loadError}
            </div>
          ) : snapshot ? (
            <dl className="divide-y text-xs">
              <DiagnosticRow label="状态">
                {snapshot.available ? "结构化日志已启用" : "诊断日志暂不可用"}
              </DiagnosticRow>
              <DiagnosticRow label="格式">JSON Lines</DiagnosticRow>
              <DiagnosticRow label="单文件上限">{maxFileMB} MB</DiagnosticRow>
              <DiagnosticRow label="轮转保留">
                {snapshot.backup_files} 个历史文件
              </DiagnosticRow>
              <DiagnosticRow label="问题关联">
                {correlations.length === 2
                  ? "Run 与 Request"
                  : correlations.join("、") || "未启用"}
              </DiagnosticRow>
              <DiagnosticRow label="敏感信息">写入前自动脱敏</DiagnosticRow>
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
            打开日志目录
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
