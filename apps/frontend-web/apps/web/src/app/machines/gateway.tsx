import { useCallback, useEffect, useState } from "react"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import type { components } from "../../lib/api-schema"

type Config = components["schemas"]["GatewayView"]
type Request = components["schemas"]["GatewayRequestView"]

export function Gateway({tenantID, section}: {tenantID: string; section?: "providers" | "models" | "budgets" | "requests"}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [config, setConfig] = useState<Config | null>(null)
  const [requests, setRequests] = useState<Request[]>([])
  const [error, setError] = useState("")
  const [message, setMessage] = useState("")
  const [busy, setBusy] = useState(false)
  const [importVersion, setImportVersion] = useState(0)
  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const [row, ledger] = await Promise.all([iamApi.gatewayConfig(tenantID, signal), iamApi.gatewayRequests(tenantID, signal)])
      if (!signal?.aborted) {setConfig(row); setRequests(ledger); setError("")}
    } catch (cause) {if (!signal?.aborted) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
  }, [tenantID, t])
  useEffect(() => {const controller = new AbortController(); queueMicrotask(() => {if (!controller.signal.aborted) void load(controller.signal)}); return () => controller.abort()}, [load])
  const run = async (action: () => Promise<void>) => {
    if (busy) return
    setBusy(true); setError(""); setMessage("")
    try {await action()} catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))} finally {setBusy(false)}
  }
  return <section className="flex min-w-0 flex-col gap-4" aria-busy={busy}>
    <p className="max-w-3xl text-sm text-muted-foreground">{t("fleetGatewayHint")}</p>
    {error && <Alert>{error}</Alert>}
    {message && <p role="status" className="break-words">{message}</p>}
    <Button className="self-start" variant="outline" disabled={busy} onClick={() => void run(() => load())}>{t("refresh")}</Button>
    {!config && !error && <p role="status">{t("loading")}</p>}
    {config && <>
      <p className="text-sm font-medium">{t(config.available ? "fleetGatewayConfigured" : "fleetUnavailableLabel")} · {t("fleetVersion")} {config.version}</p>
      <div hidden={Boolean(section && section !== "providers")} className="space-y-4">
      <AutoForm key={`config:${config.version}`} mode={allowed("gateway_manage") ? "edit" : "detail"} initialData={{enabled: config.enabled, machine_id: config.machine_id ?? "", default_model: config.default_model, key_ref: config.key_ref ?? ""}} fields={{key_ref: {name: t("fleetGatewayKeyRef"), type: "text"}, enabled: {name: t("fleetGatewayEnabled"), type: "checkbox"}, machine_id: {name: t("fleetMachineID"), type: "text"}, default_model: {name: t("fleetGatewayDefaultModel"), type: "text"}}} onSubmit={async (values) => run(async () => {
        const body = {key_ref: String(values.key_ref ?? "").trim() || undefined, enabled: values.enabled === true, machine_id: String(values.machine_id ?? "").trim() || undefined, default_model: String(values.default_model ?? "").trim(), providers: config.providers ?? [], models: config.models ?? [], budgets: config.budgets ?? []}
        setConfig(await iamApi.saveGatewayConfig(tenantID, config.version, body)); setMessage(t("fleetGatewaySaved"))
      })}>{allowed("gateway_manage") && <Button type="submit" disabled={busy}>{t("save")}</Button>}</AutoForm>
      <h3 className="font-medium">{t("fleetGatewayProviders")}</h3>
      <AutoTable rowProps={() => ({tabIndex: 0})} data={config.providers ?? []} rowKey="id" pagination={false} exportable={false} columns={[{field: "id", name: t("name")}, {field: "base_url", name: t("fleetGatewayURL")}, {field: "key_ref", name: t("fleetGatewayKeyRef")}]} />
      {allowed("gateway_manage") && <>
        <AutoForm key={`provider-key:${importVersion}`} mode="edit" initialData={{secret: ""}} fields={{secret: {name: t("fleetGatewayProviderSecret"), type: "password", required: true, config: {autoComplete: "new-password", minLength: 4, maxLength: 4096}}}} onSubmit={async (values) => run(async () => {
          const key = await iamApi.importGatewayProviderKey(tenantID, String(values.secret ?? ""), crypto.randomUUID())
          setImportVersion((version) => version + 1)
          setMessage(`${t("fleetGatewayKeyRef")}: ${key.id}`)
        })}><Button type="submit" disabled={busy}>{t("fleetGatewayImportKey")}</Button></AutoForm>
        <AutoForm mode="edit" initialData={{id: "", base_url: "", key_ref: ""}} fields={{id: {name: t("name"), type: "text"}, base_url: {name: t("fleetGatewayURL"), type: "text"}, key_ref: {name: t("fleetGatewayKeyRef"), type: "text"}}} onSubmit={async (values) => run(async () => {
          const provider = {id: String(values.id ?? "").trim(), base_url: String(values.base_url ?? "").trim(), key_ref: String(values.key_ref ?? "").trim() || undefined}
          setConfig(await iamApi.saveGatewayConfig(tenantID, config.version, {...config, providers: [...(config.providers ?? []).filter((row) => row.id !== provider.id), provider]})); setMessage(t("fleetGatewaySaved"))
        })}><Button type="submit" disabled={busy}>{t("fleetGatewaySaveProvider")}</Button></AutoForm>
        <Button className="self-start" variant="outline" disabled={busy} onClick={() => void run(async () => {const key = await iamApi.createGatewayKey(tenantID, crypto.randomUUID()); setMessage(`${t("fleetGatewayKeyRef")}: ${key.id} · ••••${key.last4}`)})}>{t("fleetGatewayCreateKey")}</Button>
      </>}
      </div>
      <div hidden={Boolean(section && section !== "models")} className="space-y-4">
      <h3 className="font-medium">{t("fleetGatewayModels")}</h3>
      <AutoTable rowProps={() => ({tabIndex: 0})} data={config.models ?? []} rowKey="id" pagination={false} exportable={false} columns={[{field: "id", name: t("fleetModel")}, {field: "provider", name: t("fleetGatewayProvider")}, {field: "runtime", name: t("fleetRuntime")}, {field: "input_micros", name: t("fleetGatewayInputPrice")}, {field: "output_micros", name: t("fleetGatewayOutputPrice")}, {field: "cache_read_micros", name: t("fleetGatewayCacheRead")}, {field: "cache_write_micros", name: t("fleetGatewayCacheWrite")}]} />
      {allowed("gateway_manage") && <AutoForm mode="edit" initialData={{id: "", provider: "", runtime: "CODEX", input_micros: 0, output_micros: 0, cache_read_micros: 0, cache_write_micros: 0}} fields={{id: {name: t("fleetModel"), type: "text"}, provider: {name: t("fleetGatewayProvider"), type: "text"}, runtime: {name: t("fleetRuntime"), type: "text"}, input_micros: {name: t("fleetGatewayInputPrice"), type: "number"}, output_micros: {name: t("fleetGatewayOutputPrice"), type: "number"}, cache_read_micros: {name: t("fleetGatewayCacheRead"), type: "number"}, cache_write_micros: {name: t("fleetGatewayCacheWrite"), type: "number"}}} onSubmit={async (values) => run(async () => {
        const model = {id: String(values.id ?? "").trim(), provider: String(values.provider ?? "").trim(), runtime: String(values.runtime ?? "") as "CODEX" | "CLAUDE_CODE", input_micros: Number(values.input_micros), output_micros: Number(values.output_micros), cache_read_micros: Number(values.cache_read_micros), cache_write_micros: Number(values.cache_write_micros)}
        setConfig(await iamApi.saveGatewayConfig(tenantID, config.version, {...config, models: [...(config.models ?? []).filter((row) => row.id !== model.id), model]})); setMessage(t("fleetGatewaySaved"))
      })}><Button type="submit" disabled={busy}>{t("fleetGatewaySaveModel")}</Button></AutoForm>}
      </div>
      <div hidden={Boolean(section && section !== "budgets")} className="space-y-4">
      <h3 className="font-medium">{t("fleetGatewayBudgets")}</h3>
      <AutoTable rowProps={() => ({tabIndex: 0})} data={config.budgets ?? []} rowKey="project_id" pagination={false} exportable={false} columns={[{field: "project_id", name: t("fleetProject")}, {field: "limit_micros", name: t("fleetGatewayLimit")}]} />
      {allowed("gateway_manage") && <AutoForm mode="edit" initialData={{project_id: "", limit_micros: 0}} fields={{project_id: {name: t("fleetProject"), type: "text"}, limit_micros: {name: t("fleetGatewayLimit"), type: "number"}}} onSubmit={async (values) => run(async () => {
        const budget = {project_id: String(values.project_id ?? "").trim(), limit_micros: Number(values.limit_micros)}
        setConfig(await iamApi.saveGatewayConfig(tenantID, config.version, {...config, budgets: [...(config.budgets ?? []).filter((row) => row.project_id !== budget.project_id), budget]})); setMessage(t("fleetGatewaySaved"))
      })}><Button type="submit" disabled={busy}>{t("fleetGatewaySaveBudget")}</Button></AutoForm>}
      </div>
      <div hidden={Boolean(section && section !== "requests")} className="space-y-4">
      <h3 className="font-medium">{t("fleetGatewayRequests")}</h3>
      <AutoTable rowProps={() => ({tabIndex: 0})} data={requests} rowKey="id" pagination={false} exportable={false} columns={[{field: "request_id", name: t("fleetGatewayRequestID")}, {field: "model", name: t("fleetModel")}, {field: "state", name: t("fleetState")}, {field: "reason", name: t("fleetInspectionReason")}, {field: "created_at", name: t("fleetCreatedAt")}]} />
      {allowed("gateway_manage") && <AutoForm mode="edit" initialData={{project_id: "", max_input_tokens: 1000, max_output_tokens: 1000}} fields={{project_id: {name: t("fleetProject"), type: "text", required: true}, max_input_tokens: {name: t("fleetGatewayMaxInputTokens"), type: "number", required: true}, max_output_tokens: {name: t("fleetGatewayMaxOutputTokens"), type: "number", required: true}}} onSubmit={async (values) => run(async () => {
        await iamApi.createGatewayRequest(tenantID, config.default_model, crypto.randomUUID(), String(values.project_id ?? "").trim(), Number(values.max_input_tokens), Number(values.max_output_tokens)); await load(); setMessage(t("fleetGatewayUnavailableRequest"))
      })}><Button type="submit" variant="outline" disabled={busy || !config.default_model}>{t("fleetGatewayCheck")}</Button></AutoForm>}
      </div>
    </>}
  </section>
}
