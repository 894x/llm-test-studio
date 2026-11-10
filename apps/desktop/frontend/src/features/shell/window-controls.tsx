import CopyIcon from "lucide-react/dist/esm/icons/copy.mjs"
import MinusIcon from "lucide-react/dist/esm/icons/minus.mjs"
import SquareIcon from "lucide-react/dist/esm/icons/square.mjs"
import XIcon from "lucide-react/dist/esm/icons/x.mjs"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip"
import type { useWindowControls } from "./use-window-controls"

export function WindowControls({ controls }: { controls: ReturnType<typeof useWindowControls> }) {
  const { t } = useTranslation("shell")
  const maximiseLabel = t(controls.maximised ? "window.restore" : "window.maximise")

  return (
    <div
      role="group"
      aria-label={t("window.controls")}
      data-window-drag="false"
      className="desktop-header-actions flex shrink-0 items-center gap-1"
    >
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label={t("window.minimise")}
            disabled={!controls.minimise} onClick={controls.minimise}>
            <MinusIcon />
          </Button>
        </TooltipTrigger>
        <TooltipContent side="bottom">{t("window.minimise")}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label={maximiseLabel}
            disabled={!controls.toggleMaximise} onClick={controls.toggleMaximise}>
            {controls.maximised ? <CopyIcon /> : <SquareIcon />}
          </Button>
        </TooltipTrigger>
        <TooltipContent side="bottom">{maximiseLabel}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger asChild>
          <Button variant="ghost" size="icon-sm" aria-label={t("window.close")}
            data-window-close disabled={!controls.close} onClick={controls.close}>
            <XIcon />
          </Button>
        </TooltipTrigger>
        <TooltipContent side="bottom">{t("window.close")}</TooltipContent>
      </Tooltip>
    </div>
  )
}
