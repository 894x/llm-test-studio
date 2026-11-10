import { Component, type ReactNode } from "react"
import RefreshCwIcon from "lucide-react/dist/esm/icons/refresh-cw.mjs"
import { useTranslation } from "react-i18next"

import { reportWorkspaceRenderFailure } from "@/app/desktop-client"
import { Button } from "@/components/ui/button"

class WorkspaceRenderBoundary extends Component<{
  children: ReactNode
  fallback: ReactNode
}, { failed: boolean }> {
  state = { failed: false }

  static getDerivedStateFromError() {
    return { failed: true }
  }

  componentDidCatch(error: Error) {
    reportWorkspaceRenderFailure(error)
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children
  }
}

export function WorkspaceErrorBoundary({ children }: { children: ReactNode }) {
  const { t } = useTranslation("shell")
  return (
    <WorkspaceRenderBoundary fallback={
      <div role="alert" className="flex min-h-0 flex-1 items-center justify-center px-4">
        <div className="max-w-md space-y-3">
          <h2 className="text-sm font-semibold">{t("pageError.title")}</h2>
          <p className="text-xs text-muted-foreground">{t("pageError.description")}</p>
          <p className="text-xs text-muted-foreground">{t("pageError.reloadHint")}</p>
          <Button variant="outline" size="sm" onClick={() => window.location.reload()}>
            <RefreshCwIcon />{t("pageError.reload")}
          </Button>
        </div>
      </div>
    }>
      {children}
    </WorkspaceRenderBoundary>
  )
}
