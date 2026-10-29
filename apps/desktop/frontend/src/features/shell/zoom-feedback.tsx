import { useEffect, useState } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import type { InterfaceZoom } from "./use-interface-zoom"

export function ZoomFeedback({ zoom }: { zoom: InterfaceZoom }) {
  const { t } = useTranslation("shell")
  const [dismissed, setDismissed] = useState(0)
  useEffect(() => {
    if (!zoom.announcement) return
    const timer = window.setTimeout(() => setDismissed(zoom.announcement), 3000)
    return () => window.clearTimeout(timer)
  }, [zoom.announcement])

  if (!zoom.announcement || zoom.announcement === dismissed) return null
  return <div role="status" aria-live="polite" className="fixed bottom-4 left-1/2 z-50 flex max-w-[calc(100%-32px)] -translate-x-1/2 items-center gap-3 rounded-md bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md">
    <span className="whitespace-nowrap tabular-nums">{t("zoom.current", { percent: zoom.percent })}</span>
    {zoom.percent !== 100 ? <Button size="xs" variant="outline" onClick={() => zoom.change(0)}>{t("zoom.reset")}</Button> : null}
  </div>
}
