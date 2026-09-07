import { useRef, useState } from "react"

export function useCatalogSearch<T>(items: T[], values: (item: T) => string[]) {
  const [query, setQuery] = useState("")
  const inputRef = useRef<HTMLInputElement>(null)
  const terms = query.trim().toLowerCase().split(/\s+/).filter(Boolean)
  const rows = terms.length === 0 ? items : items.filter((item) => {
    const text = values(item).join(" ").toLowerCase()
    return terms.every((term) => text.includes(term))
  })

  function clear() {
    setQuery("")
    inputRef.current?.focus()
  }

  return {
    query, setQuery, clear, inputRef, rows,
    total: items.length,
    matches: rows.length,
    empty: items.length > 0 && rows.length === 0,
  }
}
