import { Activity, Suspense, useState, type ReactNode } from "react"

// Visit lazily, then preserve the page's state and DOM (including scroll offsets).
// Activity disconnects effects while hidden, so page observers and polls stop.
export function RetainedWorkspace({ active, children, fallback }: {
  active: boolean
  children: ReactNode
  fallback: ReactNode
}) {
  const [visited, setVisited] = useState(active)
  if (active && !visited) setVisited(true)
  if (!active && !visited) return null

  return <Activity mode={active ? "visible" : "hidden"}>
    <Suspense fallback={fallback}>{children}</Suspense>
  </Activity>
}
