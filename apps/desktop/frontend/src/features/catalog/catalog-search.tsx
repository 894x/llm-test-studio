import SearchIcon from "lucide-react/dist/esm/icons/search.mjs"
import XIcon from "lucide-react/dist/esm/icons/x.mjs"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Empty, EmptyDescription, EmptyTitle } from "@/components/ui/empty"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { ScrollArea } from "@/components/ui/scroll-area"
import type { useCatalogSearch } from "./use-catalog-search"

export function CatalogSearch({ search: { query, setQuery, clear, inputRef, total, matches }, label, placeholder }: {
  search: Pick<ReturnType<typeof useCatalogSearch>, "query" | "setQuery" | "clear" | "inputRef" | "total" | "matches">
  label: string
  placeholder: string
}) {
  const { t } = useTranslation("catalog")
  return (
    <div className="ml-auto flex min-w-0 max-w-full items-center gap-2">
      <span role="status" className="w-24 shrink-0 text-right text-[11px] tabular-nums text-muted-foreground">
        {query.trim() ? t("search.matches", { matches, total }) : ""}
      </span>
      <InputGroup className="w-64 max-w-full">
        <InputGroupAddon className="pl-1"><SearchIcon className="size-4" aria-hidden="true" /></InputGroupAddon>
        <InputGroupInput
          ref={inputRef}
          type="text"
          role="searchbox"
          aria-label={label}
          placeholder={placeholder}
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape" && !event.nativeEvent.isComposing && query) {
              event.preventDefault()
              event.stopPropagation()
              clear()
            }
          }}
          className="text-xs md:text-xs"
        />
        <InputGroupAddon>
          <Button type="button" variant="ghost" size="icon-xs" disabled={!query} aria-label={t("search.clear")} title={t("search.clear")} onClick={clear}>
            <XIcon aria-hidden="true" />
          </Button>
        </InputGroupAddon>
      </InputGroup>
    </div>
  )
}

export function CatalogSearchEmpty({ onClear }: { onClear: () => void }) {
  const { t } = useTranslation("catalog")
  return (
    <ScrollArea className="min-h-0 flex-1">
      <Empty>
        <EmptyTitle>{t("search.empty")}</EmptyTitle>
        <EmptyDescription>{t("search.emptyDescription")}</EmptyDescription>
        <Button type="button" variant="outline" size="sm" onClick={onClear}>{t("search.reset")}</Button>
      </Empty>
    </ScrollArea>
  )
}
