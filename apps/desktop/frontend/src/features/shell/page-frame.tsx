import { useState, type ReactNode } from "react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import { ScrollArea } from "@/components/ui/scroll-area"
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet"

export function PageFrame({
  title,
  description,
  count,
  children,
  inspector,
  inspectorLabel,
  actions,
}: {
  title: string
  description: string
  count?: string
  children: ReactNode
  inspector?: ReactNode
  inspectorLabel?: string
  actions?: ReactNode
}) {
  const { t } = useTranslation("common")
  const [detailsOpen, setDetailsOpen] = useState(false)

  return (
    <main className="flex min-h-0 min-w-0 flex-1">
      <section className="flex min-w-0 flex-1 flex-col" aria-labelledby="page-heading">
        <div className="flex shrink-0 items-end justify-between gap-3 px-4 py-3">
          <div className="min-w-0">
            <h1 id="page-heading" className="text-lg font-semibold tracking-tight">
              {title}
            </h1>
            <p className="mt-1 truncate text-[11px] text-muted-foreground">
              {description}
            </p>
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {inspector !== undefined ? (
              <Sheet open={detailsOpen} onOpenChange={setDetailsOpen}>
                <SheetTrigger asChild>
                  <Button variant="ghost" size="sm" className="min-[1180px]:hidden">
                    {inspectorLabel ?? t("page.details", { title })}
                  </Button>
                </SheetTrigger>
                <SheetContent className="data-[side=right]:w-[340px] max-w-full gap-0 p-0" aria-describedby={undefined}>
                  <SheetHeader className="sr-only">
                    <SheetTitle>{inspectorLabel ?? t("page.details", { title })}</SheetTitle>
                  </SheetHeader>
                  <ScrollArea contentWidth="viewport" className="flex-1 pt-8">{detailsOpen ? inspector : null}</ScrollArea>
                </SheetContent>
              </Sheet>
            ) : null}
            {count ? <span className="text-[11px] tabular-nums text-muted-foreground">{count}</span> : null}
            {actions}
          </div>
        </div>
        {children}
      </section>
      {inspector !== undefined ? (
        <aside
          aria-label={inspectorLabel ?? t("page.details", { title })}
          className="hidden min-h-0 min-w-0 w-[320px] shrink-0 border-l border-divider bg-background min-[1180px]:flex"
        >
          <ScrollArea contentWidth="viewport" className="flex-1">{detailsOpen ? null : inspector}</ScrollArea>
        </aside>
      ) : null}
    </main>
  )
}

export function InspectorHeader({
  title,
  subtitle,
  trailing,
}: {
  title: string
  subtitle: string
  trailing?: ReactNode
}) {
  return (
    <div className="flex min-w-0 items-start justify-between gap-3 px-4 pb-3 pt-4">
      <div className="min-w-0">
        <div className="text-sm font-semibold [overflow-wrap:anywhere]">{title}</div>
        <div className="mt-1 font-mono text-[10px] text-muted-foreground [overflow-wrap:anywhere]">
          {subtitle}
        </div>
      </div>
      {trailing}
    </div>
  )
}

export function InspectorRow({ label, value }: { label: string; value: string }) {
  return (
    <div data-slot="inspector-definition-row" className="min-w-0 py-2 [overflow-wrap:anywhere]">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0 whitespace-normal text-xs font-medium" title={value}>
        {value}
      </dd>
    </div>
  )
}

export function EmptyInspector({ label }: { label: string }) {
  return <div className="p-4 text-xs text-muted-foreground">{label}</div>
}
