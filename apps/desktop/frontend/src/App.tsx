import { useMemo } from "react"

import { ThemeProvider } from "@/app/theme"
import {
  createDesktopClient,
  type DesktopClient,
} from "@/app/desktop-client"
import { TooltipProvider } from "@/components/ui/tooltip"
import { RunWorkspace } from "@/features/runs/run-workspace"

function App({ client }: { client?: DesktopClient }) {
  const desktopClient = useMemo(() => client ?? createDesktopClient(), [client])

  return (
    <ThemeProvider>
      <TooltipProvider delayDuration={250}>
        <RunWorkspace client={desktopClient} />
      </TooltipProvider>
    </ThemeProvider>
  )
}

export default App
