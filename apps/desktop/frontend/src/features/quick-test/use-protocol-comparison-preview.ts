import { useEffect, useState } from "react"
import type { ProtocolComparisonPreview } from "./protocol-comparison-data"

export type PreviewProtocolComparison = (planID: string, suiteID: string) => Promise<ProtocolComparisonPreview>

export function useProtocolComparisonPreview(
  getPreview: PreviewProtocolComparison | undefined,
  planID: string,
  suiteID: string,
) {
  const [attempt, setAttempt] = useState(0)
  const [state, setState] = useState<{ key: string; data?: ProtocolComparisonPreview; error?: boolean }>({ key: "" })
  const key = getPreview && (planID || suiteID) ? `${planID}:${suiteID}:${attempt}` : ""
  useEffect(() => {
    if (!key || !getPreview) return
    let current = true
    void getPreview(planID, suiteID).then(data => {
      if (current) setState({ key, data })
    }, () => {
      if (current) setState({ key, error: true })
    })
    return () => { current = false }
  }, [key, getPreview, planID, suiteID])
  return {
    data: state.key === key ? state.data : undefined,
    error: Boolean(key && state.key === key && state.error),
    loading: Boolean(key && state.key !== key),
    retry: () => setAttempt(value => value + 1),
  }
}
