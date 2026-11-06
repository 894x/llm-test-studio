import { useState } from "react"
import { useTranslation } from "react-i18next"
import SaveIcon from "lucide-react/dist/esm/icons/save.mjs"
import { Autocomplete, AutocompleteContent, AutocompleteEmpty, AutocompleteInput, AutocompleteItem, AutocompleteList } from "@/components/ui/autocomplete"
import { Button } from "@/components/ui/button"
import { InputGroup, InputGroupAddon, InputGroupInput } from "@/components/ui/input-group"
import { SearchableSelect } from "@/components/ui/searchable-select"
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip"
import type { CatalogSnapshot, SaveQuickTestTargetCommand, SavedQuickTestTarget } from "@/features/catalog/data"
import { PROTOCOLS, protocolOptions } from "@/features/catalog/protocols"
import type { PlanRunConnection } from "@/features/runs/data"
import { ConnectionURLInput } from "./connection-url-input"
import { resolveConnectionURL } from "./connection-url"
import { ModelIcon, ProtocolIcon } from "./model-icon"
import { SaveChannelSheet } from "./save-channel-sheet"
import { QuickTestField } from "./quick-test-field"

export function PlanConnectionFields({ connection, onChange, catalog, disabled, ready, errors, status, onSaveChannel, idPrefix = "plan-test", supportedProtocols }: {
  connection: PlanRunConnection
  onChange: (patch: Partial<PlanRunConnection>) => void
  catalog: CatalogSnapshot
  disabled: boolean
  ready: boolean
  errors: Record<string, string>
  status?: React.ReactNode
  idPrefix?: string
  supportedProtocols?: readonly string[]
  onSaveChannel?: (command: SaveQuickTestTargetCommand) => Promise<SavedQuickTestTarget>
}) {
  const { t } = useTranslation("quickTest")
  const [saveOpen, setSaveOpen] = useState(false)
  const channels = catalog.channels.filter(item => item.enabled && item.credential_configured)
  const channel = channels.find(item => item.id === connection.channel_id)
  const missing = ready && !!connection.channel_id && !channel
  const models = [...new Set(connection.channel_id
    ? catalog.channel_models.filter(item => item.channel_id === connection.channel_id && item.protocols.includes(connection.protocol)).map(item => item.upstream_model_name)
    : catalog.models.filter(item => item.protocols.includes(connection.protocol)).map(item => item.name))]
  const descriptor = PROTOCOLS.find(item => item.id === connection.protocol)!
  const address = channel?.base_url ?? connection.base_url ?? ""
  const suffix = resolveConnectionURL(address, descriptor.request_paths[0].path, descriptor.request_paths)?.suffix ?? ""
  const id = (key: string) => `${idPrefix}-${key}`
  return <>
    <div className="grid min-w-0 grid-cols-4 items-start gap-x-4 gap-y-2">
      <QuickTestField id={id("channel_id")} label={t("fillChannel")} error={errors.channel_id} className="row-span-2 grid grid-rows-subgrid">
        <SearchableSelect id={id("channel_id")} aria-label={t("fillChannel")} value={missing ? "missing" : connection.channel_id || "manual"}
          disabled={disabled || !ready} trailing={status} className="w-full" aria-invalid={!!errors.channel_id}
          aria-describedby={errors.channel_id ? `${id("channel_id")}-error` : undefined}
          options={[{ value: "manual", label: t("manual") }, ...(missing ? [{ value: "missing", label: t("task.missingChannel"), disabled: true }] : []), ...channels.map(item => ({ value: item.id, label: item.name }))]}
          onValueChange={value => { const selected = channels.find(item => item.id === value); onChange({ channel_id: selected?.id ?? "", base_url: selected?.base_url ?? address, api_key: "", credential_run_id: undefined }) }} />
      </QuickTestField>
      <QuickTestField id={id("base_url")} label={t("address")} error={errors.base_url} className="col-span-3 row-span-2 grid grid-rows-subgrid"
        labelHint={<p className="text-[11px] font-normal text-muted-foreground">{t("task.endpointHint")}</p>}>
        <ConnectionURLInput id={id("base_url")} value={address} suffix={suffix} disabled={disabled || !!connection.channel_id}
          placeholder="https://api.example.com" aria-invalid={!!errors.base_url} aria-describedby={errors.base_url ? `${id("base_url")}-error` : undefined}
          onChange={event => onChange({ base_url: event.target.value, api_key: "", credential_run_id: undefined })} />
      </QuickTestField>
    </div>
    <div className="grid min-w-0 grid-cols-4 items-start gap-4">
      <QuickTestField id={id("model")} label={t("model.label")} error={errors.model}>
        <Autocomplete modal={false} openOnInputClick items={models} value={connection.model} onValueChange={value => onChange({ model: value ?? "" })}>
          <AutocompleteInput id={id("model")} leading={<ModelIcon model={connection.model} />} aria-label={t("model.label")} value={connection.model}
            placeholder={t("model.placeholder")} disabled={disabled} aria-invalid={!!errors.model}
            aria-describedby={errors.model ? `${id("model")}-error` : undefined} onChange={event => onChange({ model: event.target.value })} />
          <AutocompleteContent><AutocompleteEmpty>{t(ready ? "model.empty" : "catalog.connectionsLoading")}</AutocompleteEmpty>
            <AutocompleteList>{value => <AutocompleteItem key={value} value={value}><ModelIcon model={value} /><span className="min-w-0 [overflow-wrap:anywhere]">{value}</span></AutocompleteItem>}</AutocompleteList>
          </AutocompleteContent>
        </Autocomplete>
      </QuickTestField>
      <QuickTestField id={id("protocol")} label={t("protocol")}>
        <SearchableSelect id={id("protocol")} value={connection.protocol} disabled={disabled} className="w-full"
          options={protocolOptions.filter(([value]) => !supportedProtocols || supportedProtocols.includes(value)).map(([value, label]) => ({ value, label, icon: <ProtocolIcon protocol={value} /> }))}
          onValueChange={value => onChange({ protocol: value as PlanRunConnection["protocol"], credential_run_id: undefined })} />
      </QuickTestField>
      <QuickTestField id={id("api_key")} label="API Key" error={errors.api_key} className="col-span-2">
        <InputGroup><InputGroupInput id={id("api_key")} type="password" autoComplete="off" value={connection.api_key ?? ""}
          disabled={disabled || !!connection.channel_id || !!connection.credential_run_id}
          aria-invalid={!!errors.api_key} aria-describedby={errors.api_key ? `${id("api_key")}-error` : undefined}
          placeholder={channel ? t("savedCredential", { name: channel.name }) : connection.credential_run_id ? t("task.rememberedKey") : ""}
          onChange={event => onChange({ api_key: event.target.value })} />
          {!connection.channel_id && onSaveChannel ? <InputGroupAddon><TooltipProvider><Tooltip><TooltipTrigger asChild>
            <Button type="button" variant="ghost" size="icon-sm" disabled={disabled} aria-label={t("saveChannel.title")} onClick={() => setSaveOpen(true)}><SaveIcon /></Button>
          </TooltipTrigger><TooltipContent>{t("saveChannel.title")}</TooltipContent></Tooltip></TooltipProvider></InputGroupAddon> : null}
        </InputGroup>
        {connection.credential_run_id ? <Button type="button" variant="ghost" size="sm" onClick={() => onChange({ credential_run_id: undefined, api_key: "" })}>{t("task.useOtherKey")}</Button> : null}
      </QuickTestField>
    </div>
    {saveOpen && onSaveChannel ? <SaveChannelSheet open={saveOpen} onOpenChange={setSaveOpen}
      connection={{ protocol: connection.protocol, base_url: connection.base_url ?? "", model_name: connection.model.trim(), api_key: connection.api_key?.trim() ?? "", credential_run_id: connection.credential_run_id }}
      onSave={onSaveChannel} onSaved={saved => onChange({ channel_id: saved.channel_id, api_key: "", credential_run_id: undefined })} /> : null}
  </>
}
