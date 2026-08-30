import type { ReactNode } from "react"
import ActivityIcon from "lucide-react/dist/esm/icons/activity.mjs"
import ContrastIcon from "lucide-react/dist/esm/icons/contrast.mjs"
import MoonIcon from "lucide-react/dist/esm/icons/moon.mjs"
import SunIcon from "lucide-react/dist/esm/icons/sun.mjs"

import { useTheme, type ThemePreference } from "@/app/theme-context"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"
import { DESKTOP_PAGES, type DesktopPage } from "./navigation"

function ThemeMenu() {
  const { theme, setTheme } = useTheme()
  const icon =
    theme === "dark" ? (
      <MoonIcon />
    ) : theme === "light" ? (
      <SunIcon />
    ) : (
      <ContrastIcon />
    )
  const label =
    theme === "dark" ? "深色" : theme === "light" ? "浅色" : "跟随系统"

  return (
    <DropdownMenu>
      <Tooltip>
        <TooltipTrigger asChild>
          <DropdownMenuTrigger asChild>
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label={`主题：${label}`}
              className="rounded-full"
            >
              {icon}
            </Button>
          </DropdownMenuTrigger>
        </TooltipTrigger>
        <TooltipContent side="bottom">主题：{label}</TooltipContent>
      </Tooltip>
      <DropdownMenuContent align="end" className="w-36">
        <DropdownMenuLabel>外观</DropdownMenuLabel>
        <DropdownMenuRadioGroup
          value={theme}
          onValueChange={(value) => setTheme(value as ThemePreference)}
        >
          <DropdownMenuRadioItem value="system">
            <ContrastIcon /> 跟随系统
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="light">
            <SunIcon /> 浅色
          </DropdownMenuRadioItem>
          <DropdownMenuRadioItem value="dark">
            <MoonIcon /> 深色
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
  return (
    <div className="flex h-svh min-h-[640px] flex-col overflow-hidden bg-background text-foreground">
      <header className="flex h-12 shrink-0 items-center border-b bg-background px-3">
        <div className="flex min-w-0 items-center gap-2">
          <div className="flex size-7 shrink-0 items-center justify-center rounded-md bg-primary text-primary-foreground">
            <ActivityIcon className="size-4" />
          </div>
          <div className="mr-3 hidden min-w-0 sm:block">
            <div className="truncate text-sm font-semibold leading-none">
              llm-studio
            </div>
            <div className="mt-1 text-[10px] leading-none text-muted-foreground">
              本地测试工作台
            </div>
          </div>
        </div>

        <nav
          aria-label="主导航"
          className="flex min-w-0 flex-1 items-center overflow-x-auto"
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
                  "shrink-0 px-2 text-xs font-normal",
                  active && "bg-accent font-medium text-accent-foreground",
                )}
              >
                {page.label}
              </Button>
            )
          })}
        </nav>

        <div className="ml-2 flex shrink-0 items-center gap-1">
          <ThemeMenu />
          {actions}
        </div>
      </header>
      {children}
    </div>
  )
}
