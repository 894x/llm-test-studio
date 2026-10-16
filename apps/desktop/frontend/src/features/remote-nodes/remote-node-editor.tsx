import { useId, useState } from "react"
import { useTranslation } from "react-i18next"

import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Field, FieldContent, FieldDescription, FieldGroup, FieldLabel } from "@/components/ui/field"
import { Input } from "@/components/ui/input"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Spinner } from "@/components/ui/spinner"
import { Textarea } from "@/components/ui/textarea"

import type { RemoteNode, RemoteNodeDraft } from "./data"

export function RemoteNodeEditor({ draft, node, disabled, saving, onSave }: {
  draft: RemoteNodeDraft; node?: RemoteNode; disabled: boolean; saving: boolean
  onSave: (draft: RemoteNodeDraft) => void
}) {
  const { t } = useTranslation("remoteNodes")
  const id = useId()
  const [value, setValue] = useState(draft)
  const set = <K extends keyof RemoteNodeDraft>(key: K, next: RemoteNodeDraft[K]) => setValue(current => ({ ...current, [key]: next }))
  const existingAuth = node?.has_authentication && node.authentication_kind === value.authentication
  return <form className="min-w-0 space-y-4" onSubmit={event => {
    event.preventDefault()
    if (disabled) return
    onSave({ ...value, name: value.name.trim(), region: value.region.trim(), host: value.host.trim(), user: value.user.trim() })
    // The command owns its request copy; credentials stay out of retained form state.
    setValue(current => ({ ...current, password: "", private_key: "", passphrase: "" }))
  }}>
    <h3 className="text-sm font-medium">{t(node ? "editNode" : "addNode")}</h3>
    <FieldGroup>
      <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-3">
        <Field><FieldContent><FieldLabel htmlFor={`${id}-name`}>{t("field.name")}</FieldLabel><Input id={`${id}-name`} required maxLength={256} disabled={disabled} value={value.name} onChange={event => set("name", event.target.value)} /></FieldContent></Field>
        <Field><FieldContent><FieldLabel htmlFor={`${id}-region`}>{t("field.region")}</FieldLabel><Input id={`${id}-region`} maxLength={128} disabled={disabled} value={value.region} onChange={event => set("region", event.target.value)} /></FieldContent></Field>
      </div>
      <div className="grid min-w-0 grid-cols-[minmax(0,1fr)_96px] gap-3">
        <Field><FieldContent><FieldLabel htmlFor={`${id}-host`}>{t("field.host")}</FieldLabel><Input id={`${id}-host`} required maxLength={253} disabled={disabled} value={value.host} onChange={event => set("host", event.target.value)} /></FieldContent></Field>
        <Field><FieldContent><FieldLabel htmlFor={`${id}-port`}>{t("field.port")}</FieldLabel><Input id={`${id}-port`} type="number" required min={1} max={65535} step={1} disabled={disabled} value={value.port || ""} onChange={event => set("port", Number(event.target.value))} /></FieldContent></Field>
      </div>
      <Field><FieldContent><FieldLabel htmlFor={`${id}-user`}>{t("field.user")}</FieldLabel><Input id={`${id}-user`} required maxLength={64} autoComplete="off" disabled={disabled} value={value.user} onChange={event => set("user", event.target.value)} /></FieldContent></Field>
      <Field><FieldContent><FieldLabel htmlFor={`${id}-auth`}>{t("field.authentication")}</FieldLabel>
        <SearchableSelect id={`${id}-auth`} aria-label={t("field.authentication")} disabled={disabled} value={value.authentication}
          options={[{ value: "password", label: t("field.passwordAuth") }, { value: "private_key", label: t("field.privateKeyAuth") }]}
          onValueChange={next => setValue(current => ({ ...current, authentication: next as "password" | "private_key", password: "", private_key: "", passphrase: "", remove_passphrase: false }))} />
      </FieldContent></Field>
      {value.authentication === "password" ? <Field><FieldContent>
        <FieldLabel htmlFor={`${id}-password`}>{t("field.password")}</FieldLabel>
        <Input id={`${id}-password`} type="password" autoComplete="new-password" required={!existingAuth} disabled={disabled} value={value.password} onChange={event => set("password", event.target.value)} />
        {existingAuth && <FieldDescription>{t("keepCredential")}</FieldDescription>}
      </FieldContent></Field> : <>
        <Field><FieldContent><FieldLabel htmlFor={`${id}-key`}>{t("field.privateKey")}</FieldLabel>
          <Textarea id={`${id}-key`} rows={4} className="max-h-40 resize-y font-mono text-xs" autoComplete="off" spellCheck={false} required={!existingAuth} disabled={disabled} value={value.private_key} onChange={event => set("private_key", event.target.value)} />
          {existingAuth && <FieldDescription>{t("keepCredential")}</FieldDescription>}
        </FieldContent></Field>
        <Field><FieldContent><FieldLabel htmlFor={`${id}-passphrase`}>{t("field.passphrase")}</FieldLabel>
          <Input id={`${id}-passphrase`} type="password" autoComplete="new-password" disabled={disabled || value.remove_passphrase} value={value.passphrase} onChange={event => set("passphrase", event.target.value)} />
          {node?.has_passphrase && <FieldDescription>{t("keepPassphrase")}</FieldDescription>}
        </FieldContent></Field>
        {node?.has_passphrase && <Field className="items-center"><Checkbox id={`${id}-remove-passphrase`} disabled={disabled} checked={value.remove_passphrase} onCheckedChange={next => setValue(current => ({ ...current, remove_passphrase: next === true, passphrase: "" }))} /><FieldLabel htmlFor={`${id}-remove-passphrase`}>{t("field.removePassphrase")}</FieldLabel></Field>}
      </>}
      <Field className="items-center"><Checkbox id={`${id}-enabled`} disabled={disabled} checked={value.enabled} onCheckedChange={next => set("enabled", next === true)} /><FieldLabel htmlFor={`${id}-enabled`}>{t("field.enabled")}</FieldLabel></Field>
    </FieldGroup>
    <p className="text-[11px] text-muted-foreground">{t("credentialHelp")}</p>
    <Button type="submit" disabled={disabled}>{saving && <Spinner />}{t("save")}</Button>
  </form>
}
