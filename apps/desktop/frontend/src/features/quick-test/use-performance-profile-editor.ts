import { useState } from "react"
import type { QuickPerformanceProfile } from "./data"
import { performanceFormFromProfile, performanceProfileFromForm, validatePerformanceForm,
  firstPerformanceErrorField, type PerformanceFieldErrors } from "./performance-profile"

export function usePerformanceProfileEditor(profile?: QuickPerformanceProfile) {
  const [form, setForm] = useState(() => performanceFormFromProfile(profile))
  const [fieldErrors, setFieldErrors] = useState<PerformanceFieldErrors>({})
  return { form, setForm, fieldErrors, setFieldErrors, validate() {
    const errors = validatePerformanceForm(form)
    setFieldErrors(errors)
    const field = firstPerformanceErrorField(errors)
    if (field) {
      document.getElementById(`quick-performance-${field}`)?.focus()
      return null
    }
    return performanceProfileFromForm(form)
  } }
}
