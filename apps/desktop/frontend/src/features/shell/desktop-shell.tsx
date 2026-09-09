import type { ReactNode } from "react"
import ActivityIcon from "lucide-react/dist/esm/icons/activity.mjs"
import ContrastIcon from "lucide-react/dist/esm/icons/contrast.mjs"
import MoonIcon from "lucide-react/dist/esm/icons/moon.mjs"
import SunIcon from "lucide-react/dist/esm/icons/sun.mjs"
import Settings2Icon from "lucide-react/dist/esm/icons/settings-2.mjs"
import { useTranslation } from "react-i18next"

import { useTheme, type ThemePreference } from "@/app/theme-context"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
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

function InterfaceSettingsMenu() {
  const { theme, setTheme } = useTheme()
  const { preference, setPreference } = useLanguage()
  const { t } = useTranslation("shell")
  const label = t(`theme.${theme}`)

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={t("settings.trigger", { theme: label })}
              className="rounded-full"
            >
              <Settings2Icon />
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent side="bottom">{t("settings.tooltip")}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-44">
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
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

export function DesktopShell({
  activePage,
  onNavigate,
  actions,
  children,
}: {
  activePage: DesktopPage
  onNavigate: (page: DesktopPage) => void
  actions?: ReactNode
  children: ReactNode
}) {
  const { t } = useTranslation("shell")
  return (
    <div className="flex h-svh min-h-[640px] flex-col overflow-hidden bg-background text-foreground">
      <header className="flex h-12 shrink-0 items-center bg-background px-4">
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <ActivityIcon className="size-4" />
          </div>
          <div className="mr-3 hidden min-w-0 min-[1100px]:block">
            <div className="truncate text-sm font-semibold leading-none">
              LLM Test Studio
            </div>
            <div className="mt-1 text-[10px] leading-none text-muted-foreground">
              {t("product.subtitle")}
            </div>
          </div>
        </div>

        <nav
          aria-label={t("navigationAria")}
          className="flex h-10 min-w-0 flex-1 items-center overflow-x-auto overflow-y-hidden px-1 [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
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

        <div className="desktop-header-actions ml-2 flex shrink-0 items-center gap-1">
          <InterfaceSettingsMenu />
          {actions}
        </div>
      </header>
      {children}
    </div>
  )
}
