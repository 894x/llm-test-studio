import type { ReactNode } from "react"

export function PageFrame({
  title,
  description,
  count,
  children,
  inspector,
  inspectorLabel,
}: {
  title: string
  description: string
  count?: string
  children: ReactNode
  inspector?: ReactNode
  inspectorLabel?: string
}) {
  return (
    <main className="flex min-h-0 flex-1">
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
          {count ? (
            <span className="shrink-0 text-[11px] tabular-nums text-muted-foreground">
              {count}
            </span>
          ) : null}
        </div>
        {children}
      </section>
      {inspector !== undefined ? (
        <aside
          aria-label={inspectorLabel ?? `${title}详情`}
          className="hidden w-[300px] shrink-0 border-l bg-background min-[960px]:block"
        >
          {inspector}
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
    <div className="flex items-start justify-between gap-3 px-4 pb-3 pt-4">
      <div className="min-w-0">
        <div className="truncate text-sm font-semibold">{title}</div>
        <div className="mt-1 truncate font-mono text-[10px] text-muted-foreground">
          {subtitle}
        </div>
      </div>
      {trailing}
    </div>
  )
}

export function InspectorRow({ label, value }: { label: string; value: string }) {
  return (
    <div data-slot="inspector-definition-row" className="py-2">
      <dt className="text-[11px] text-muted-foreground">{label}</dt>
      <dd className="mt-1 min-w-0 truncate text-xs font-medium" title={value}>
        {value}
      </dd>
    </div>
  )
}

export function EmptyInspector({ label }: { label: string }) {
  return <div className="p-4 text-xs text-muted-foreground">{label}</div>
}
