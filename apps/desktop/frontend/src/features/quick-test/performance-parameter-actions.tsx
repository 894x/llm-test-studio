import { useEffect, useLayoutEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { useTranslation } from "react-i18next"
import CopyIcon from "lucide-react/dist/esm/icons/copy.mjs"
import ClipboardPasteIcon from "lucide-react/dist/esm/icons/clipboard-paste.mjs"
import { Button } from "@/components/ui/button"
import { Spinner } from "@/components/ui/spinner"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import { parsePerformanceParameters, readPerformanceParameters, writePerformanceParameters } from "./performance-parameters-clipboard"
import type { usePerformanceProfileEditor } from "./use-performance-profile-editor"

export function PerformanceParameterActions({ editor, disabled, container }: {
  editor: ReturnType<typeof usePerformanceProfileEditor>
  disabled: boolean
  container?: HTMLElement | null
}) {
  const { t } = useTranslation("quickTest")
  const [pending, setPending] = useState<"copy" | "import" | null>(null)
  const [feedback, setFeedback] = useState<{ key: string; error: boolean } | null>(null)
  const operation = useRef({ active: true, busy: false })
  const current = useRef({ form: editor.form, disabled })
  useLayoutEffect(() => {
    current.current = { form: editor.form, disabled }
  }, [editor.form, disabled])
  useEffect(() => {
    const state = operation.current
    state.active = true
    return () => { state.active = false }
  }, [])

  const perform = async (action: "copy" | "import") => {
    const state = operation.current
    if (disabled || state.busy) return
    setFeedback(null)
    const profile = action === "copy" ? editor.validate() : undefined
    if (profile === null) {
      setFeedback({ key: "share.fixParameters", error: true })
      return
    }
    state.busy = true
    setPending(action)
    const sourceForm = editor.form
    let failureKey = action === "copy" ? "share.copyFailed" : "share.readFailed"
    try {
      if (action === "copy") {
        await writePerformanceParameters(JSON.stringify(profile, null, 2))
      } else {
        const text = await readPerformanceParameters()
        if (!state.active) return
        if (current.current.disabled || current.current.form !== sourceForm) {
          setFeedback({ key: "share.parametersChanged", error: true })
          return
        }
        failureKey = "share.invalidParameters"
        const form = parsePerformanceParameters(text)
        editor.setForm(form)
        editor.setFieldErrors({})
      }
      if (state.active) setFeedback({ key: action === "copy" ? "share.copied" : "share.imported", error: false })
    } catch {
      if (state.active) setFeedback({ key: failureKey, error: true })
    } finally {
      state.busy = false
      if (state.active) setPending(null)
    }
  }

  const buttons = <TooltipProvider delayDuration={250}>
    <div role="group" aria-label={t("share.title")} className="flex shrink-0 items-center justify-end gap-1">
      <Tooltip><TooltipTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t("share.copy")} disabled={disabled || !!pending} onClick={() => void perform("copy")}>
          {pending === "copy" ? <Spinner /> : <CopyIcon />}
        </Button>
      </TooltipTrigger><TooltipContent>{t("share.copy")}</TooltipContent></Tooltip>
      <Tooltip><TooltipTrigger asChild>
        <Button type="button" variant="ghost" size="icon-sm" aria-label={t("share.import")} disabled={disabled || !!pending} onClick={() => void perform("import")}>
          {pending === "import" ? <Spinner /> : <ClipboardPasteIcon />}
        </Button>
      </TooltipTrigger><TooltipContent>{t("share.import")}</TooltipContent></Tooltip>
    </div>
  </TooltipProvider>
  return <>
    {container ? createPortal(buttons, container) : <div className="flex justify-end">{buttons}</div>}
    {feedback ? <p role={feedback.error ? "alert" : "status"} className={feedback.error ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>{t(feedback.key)}</p> : null}
  </>
}
