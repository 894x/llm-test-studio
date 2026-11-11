import { useEffect, useRef, type ReactNode } from "react"
import ActivityIcon from "lucide-react/dist/esm/icons/activity.mjs"
import ArchiveRestoreIcon from "lucide-react/dist/esm/icons/archive-restore.mjs"
import ContrastIcon from "lucide-react/dist/esm/icons/contrast.mjs"
import MoonIcon from "lucide-react/dist/esm/icons/moon.mjs"
import SunIcon from "lucide-react/dist/esm/icons/sun.mjs"
import Settings2Icon from "lucide-react/dist/esm/icons/settings-2.mjs"
import { useTranslation } from "react-i18next"

import { useTheme, type ColorTheme, type ThemePreference } from "@/app/theme-context"
import { Button } from "@/components/ui/button"
import { Separator } from "@/components/ui/separator"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useLanguage } from "@/i18n/language-state"
import type { LanguagePreference } from "@/i18n/locale"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { DESKTOP_PAGES, type DesktopPage } from "./navigation"
import { useInterfaceZoom, type InterfaceZoom } from "./use-interface-zoom"
import { ZoomFeedback } from "./zoom-feedback"
import { useWindowControls } from "./use-window-controls"
import { WindowControls } from "./window-controls"

function InterfaceSettingsMenu({ zoom, onOpenUpdates, updateAvailable }: { zoom: InterfaceZoom; onOpenUpdates?: (trigger: HTMLElement | null) => void; updateAvailable?: boolean }) {
  const { theme, setTheme, colorTheme, setColorTheme } = useTheme()
  const { preference, setPreference } = useLanguage()
  const { t } = useTranslation("shell")
  const label = t(`theme.${theme}`)
  const settingsTrigger = useRef<HTMLButtonElement>(null)

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              ref={settingsTrigger}
              variant="ghost"
              size="icon-sm"
              aria-label={t("settings.trigger", { theme: label })}
            >
              <Settings2Icon />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent side="bottom">{t("settings.tooltip")}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-44">
        {onOpenUpdates ? <>
          <DropdownMenuItem onSelect={() => onOpenUpdates(settingsTrigger.current)}>{t("updates.title")}{updateAvailable ? <span className="ml-auto size-1.5 rounded-full bg-primary" aria-label={t("updates.available")} /> : null}</DropdownMenuItem>
          <DropdownMenuSeparator />
        </> : null}
        <DropdownMenuLabel>{t("settings.language")}</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={preference}
          onValueChange={(value) =>
            setPreference(value as LanguagePreference)
          }
        >
          <DropdownMenuRadioItem value="system">
            {t("language.system")}
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="zh-CN">
            {t("language.zh-CN")}
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="en-US">
            {t("language.en-US")}
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>{t("settings.appearance")}</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={theme}
          onValueChange={(value) => setTheme(value as ThemePreference)}
        >
          <DropdownMenuRadioItem value="system">
            <ContrastIcon /> {t("theme.system")}
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="light">
            <SunIcon /> {t("theme.light")}
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">
            <MoonIcon /> {t("theme.dark")}
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>{t("settings.colorTheme")}</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          aria-label={t("settings.colorTheme")}
          value={colorTheme}
          onValueChange={(value) => setColorTheme(value as ColorTheme)}
        >
          <DropdownMenuRadioItem value="classic">
            {t("colorTheme.classic")}
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="high-contrast">
            {t("colorTheme.high-contrast")}
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
        <DropdownMenuSeparator />
        <DropdownMenuLabel>{t("zoom.current", { percent: zoom.percent })}</DropdownMenuLabel>
        <DropdownMenuItem disabled={!zoom.canIncrease} onSelect={() => zoom.change(1)}>{t("zoom.increase")}</DropdownMenuItem>
        <DropdownMenuItem disabled={!zoom.canDecrease} onSelect={() => zoom.change(-1)}>{t("zoom.decrease")}</DropdownMenuItem>
        <DropdownMenuItem disabled={zoom.percent === 100} onSelect={() => zoom.change(0)}>{t("zoom.reset")}</DropdownMenuItem>
        <p className="px-2 py-1 text-[11px] text-muted-foreground [overflow-wrap:anywhere]">{t("zoom.hint")}</p>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function DesktopShell({
  activePage,
  onNavigate,
  onOpenRecycleBin,
  onOpenUpdates,
  updateAvailable,
  actions,
  children,
}: {
  activePage: DesktopPage
  onNavigate: (page: DesktopPage) => void
  onOpenRecycleBin?: () => void
  onOpenUpdates?: (trigger: HTMLElement | null) => void
  updateAvailable?: boolean
  actions?: ReactNode
  children: ReactNode
}) {
  const { t } = useTranslation("shell")
  const { t: tr } = useTranslation("historyRecycle")
  const zoom = useInterfaceZoom()
  const windowControls = useWindowControls()
  const navigation = useRef<HTMLElement>(null)

  useEffect(() => {
    const nav = navigation.current
    if (!nav) return
    const revealCurrentPage = () => {
      nav.querySelector<HTMLElement>('[aria-current="page"]')?.scrollIntoView({ block: "nearest", inline: "nearest" })
    }
    revealCurrentPage()
    const observer = new ResizeObserver(revealCurrentPage)
    observer.observe(nav)
    return () => observer.disconnect()
  }, [activePage])

  return (
    <div className="flex h-[calc(100svh/var(--interface-zoom,1))] min-h-0 flex-col overflow-hidden bg-background text-foreground">
      <header data-window-drag="true"
        className="flex h-12 shrink-0 items-center bg-background px-4"
        onDoubleClick={event => {
          const region = (event.target as Element).closest("[data-window-drag]")
          if (region?.getAttribute("data-window-drag") === "true") windowControls.toggleMaximise?.()
        }}>
        <div className="mr-3 flex shrink-0 items-center gap-2" aria-label="LLM Test Studio">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <ActivityIcon className="size-4" />
          </div>
          <div className="hidden min-[1100px]:block">
            <div className="whitespace-nowrap text-[13px] font-semibold leading-none">
              LLM Test Studio
            </div>
          </div>
        </div>

        <nav
          ref={navigation}
          data-window-drag="false"
          aria-label={t("navigationAria")}
          className="flex h-10 min-w-0 items-center overflow-x-auto overflow-y-hidden px-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
        >
          {DESKTOP_PAGES.map((page) => {
            const active = page.id === activePage
            return (
              <Button
                key={page.id}
                variant="ghost"
                size="sm"
                aria-current={active ? "page" : undefined}
                onClick={() => onNavigate(page.id)}
                className={cn(
                  "desktop-nav-button shrink-0 px-2 text-xs font-normal active:not-aria-[haspopup]:translate-y-0",
                  active && "bg-accent font-medium text-accent-foreground",
                )}
              >
                {t(page.labelKey)}
              </Button>
            )
          })}
        </nav>

        <div className="min-w-8 flex-1 self-stretch" aria-hidden="true" />

        <div data-window-drag="false" className="desktop-header-actions ml-2 flex shrink-0 items-center gap-1">
          {onOpenRecycleBin ? <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={tr("title")}
                aria-current={activePage === "recycle-bin" ? "page" : undefined}
                className={cn(activePage === "recycle-bin" && "bg-accent text-accent-foreground")}
                onClick={onOpenRecycleBin}><ArchiveRestoreIcon /></Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">{tr("title")}</TooltipContent>
          </Tooltip> : null}
          {zoom.percent !== 100 ? <Tooltip>
            <TooltipTrigger asChild><Button size="xs" variant="ghost" className="tabular-nums" aria-label={t("zoom.resetAria", { percent: zoom.percent })} onClick={() => zoom.change(0)}>{zoom.percent}%</Button></TooltipTrigger>
            <TooltipContent>{t("zoom.reset")}</TooltipContent>
          </Tooltip> : null}
          {updateAvailable && onOpenUpdates ? <Button variant="ghost" size="xs" onClick={event => onOpenUpdates(event.currentTarget)}>{t("updates.available")}</Button> : null}
          <InterfaceSettingsMenu zoom={zoom} onOpenUpdates={onOpenUpdates} updateAvailable={updateAvailable} />
          {actions}
        </div>
        <Separator orientation="vertical" className="mx-3 h-4 data-vertical:self-center" />
        <WindowControls controls={windowControls} />
      </header>
      {children}
      <ZoomFeedback zoom={zoom} />
    </div>
  )
}
