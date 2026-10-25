import { useEffect, useRef, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { ReauthenticationDialog } from "../../components/reauthentication-dialog"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { useAllowed } from "../../lib/permissions"
import { AgentPanel } from "./page"
import { toolVersions, type CatalogDetail, type CatalogPreset, type CatalogImport } from "./tool-versions-api"

const tabs = ["MCP_SERVER", "SKILL", "MACHINE"] as const
export default function ToolCatalogPage() {
  const { tenantID = "" } = useParams()
  return <Catalog key={tenantID} tenant={tenantID} />
}
function Catalog({tenant}: {tenant: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [search, setSearch] = useSearchParams()
  const tab = tabs.find(value => value === search.get("tab")) ?? "MCP_SERVER"
  const [agent, setAgent] = useState("")
  const selectedAgent = search.get("agent") ?? ""
  const validAgent = /^[1-9][0-9]{0,18}$/.test(selectedAgent) ? selectedAgent : ""
  return <div className="flex min-w-0 flex-col gap-5">
    <PageHeader title={t("toolCatalogTitle")} description={t("agentToolNotice")} />
    <nav aria-label={t("toolCatalogTitle")} className="flex flex-wrap gap-2 border-b pb-3">{tabs.map(value => <Button key={value} className="min-h-11" variant={value === tab ? "default" : "outline"} aria-current={value === tab ? "page" : undefined} onClick={() => setSearch(previous => {const next = new URLSearchParams(previous); next.set("tab", value); return next})}>{t(value === "MACHINE" ? "toolCatalogMachineTools" : `agentToolKind_${value}`)}</Button>)}</nav>
    {tab === "MACHINE" ? allowed("machine_read") ? <MachineTools key={tenant} tenant={tenant} /> : <Alert>{t("capabilityPermissionRequired")}</Alert> : <>
      <form className="flex flex-wrap items-end gap-3" onSubmit={event => {event.preventDefault(); if (!agent || /^[1-9][0-9]{0,18}$/.test(agent)) setSearch(previous => {const next = new URLSearchParams(previous); if (agent) next.set("agent", agent); else next.delete("agent"); return next})}}>
        <label className="grid max-w-full gap-2 text-sm">{t("agentPrincipal")}<Input inputMode="numeric" pattern="[1-9][0-9]{0,18}" value={agent} onChange={event => setAgent(event.target.value)} /></label><Button type="submit">{t("agentOpen")}</Button>
      </form>
      {allowed("tool_catalog_manage") || validAgent ? <AgentPanel key={`${tenant}:${validAgent}:${tab}`} tenant={tenant} agent={validAgent} tab="tools" toolKind={tab} /> : <Alert>{t("capabilityPermissionRequired")}</Alert>}
      {allowed("tool_catalog_manage") && !validAgent && <VersionedTools key={`${tenant}:${tab}`} tenant={tenant} kind={tab} />}
      <p className="text-sm text-muted-foreground">{t("toolCatalogFactsOnly")}</p>
    </>}
  </div>
}

function MachineTools({tenant}: {tenant: string}) {
  const t = useTranslations("iam")
  const [machines, setMachines] = useState<components["schemas"]["MachineView"][]>([])
  const [cursor, setCursor] = useState("")
  const [machine, setMachine] = useState("")
  const [facts, setFacts] = useState<components["schemas"]["MachineCapabilities"] | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(false)
  const [revision, setRevision] = useState(0)
  const [pagingBusy, setPagingBusy] = useState(false)
  const paging = useRef<AbortController | null>(null)
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.machines(tenant, undefined, controller.signal).then(page => {if (!controller.signal.aborted) {setMachines(page.items ?? []); setCursor(page.next_cursor ?? "")}}, cause => {if (!controller.signal.aborted) setError(String(cause.message))})
    return () => {controller.abort(); paging.current?.abort(); paging.current = null}
  }, [tenant])
  useEffect(() => {
    if (!machine) return
    const controller = new AbortController()
    queueMicrotask(() => {if (!controller.signal.aborted) {setLoading(true); setFacts(null); setError("")}})
    void iamApi.machineCapabilities(machine, controller.signal).then(data => {if (!controller.signal.aborted) setFacts(data)}, cause => {if (!controller.signal.aborted) setError(String(cause.message))}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => controller.abort()
  }, [machine, revision])
  const more = async () => {
    if (paging.current || !cursor) return
    const controller = new AbortController()
    paging.current = controller
    setPagingBusy(true); setError("")
    try {
      const page = await iamApi.machines(tenant, cursor, controller.signal)
      if (!controller.signal.aborted && paging.current === controller) {
        setMachines(previous => [...previous, ...(page.items ?? [])])
        setCursor(page.next_cursor ?? "")
      }
    } catch (cause) {
      if (!controller.signal.aborted && paging.current === controller) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))
    } finally {
      if (paging.current === controller) {paging.current = null; if (!controller.signal.aborted) setPagingBusy(false)}
    }
  }
  const discoveries = (facts?.discoveries ?? []).flatMap(inspection => (inspection.discovery ?? []).flatMap(runtime => [
    ...(runtime.skills ?? []).map(skill => ({key: `${inspection.id}:${runtime.runtime}:skill:${skill.name}`, name: skill.name, kind: "SKILL", runtime: runtime.runtime, scope: inspection.scope_key, hash: skill.sha256, state: runtime.state, checked_at: runtime.checked_at})),
    ...(runtime.mcp ?? []).map(name => ({key: `${inspection.id}:${runtime.runtime}:mcp:${name}`, name, kind: "MCP_SERVER", runtime: runtime.runtime, scope: inspection.scope_key, hash: "", state: runtime.state, checked_at: runtime.checked_at})),
  ]))
  return <section className="flex min-w-0 flex-col gap-4">
    {error && <Alert>{error}</Alert>}
    <label className="grid gap-2 text-sm">{t("fleetMachineID")}<select className="min-h-11 w-full max-w-md rounded-md border bg-background px-3" value={machine} onChange={event => {setMachine(event.target.value); setFacts(null)}}><option value="">{t("toolCatalogSelectMachine")}</option>{machines.map(row => <option key={row.id} value={row.id ?? ""}>{row.name} · {t(`fleetState_${row.state}`)}</option>)}</select></label>
    {cursor && <Button disabled={pagingBusy} className="self-start" variant="outline" onClick={() => void more()}>{t("agentMore")}</Button>}
    {machine && <div className="flex flex-wrap gap-3"><Button variant="outline" onClick={() => setRevision(value => value + 1)} disabled={loading}>{t("refresh")}</Button><Link className="inline-flex min-h-11 items-center underline" to={`/admin/${tenant}/machines/${machine}`}>{t("fleetDetails")}</Link></div>}
    <p className="text-sm text-muted-foreground">{t("toolCatalogLocalInventory")}</p>
    {machine && <><AutoTable data={facts?.tools ?? []} loading={loading} rowKey="name" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "name", name: t("name")}, {field: "version", name: t("fleetVersion")}, {field: "path", name: t("fleetPath")}, {field: "inspected_at", name: t("fleetCreatedAt")}]} />
      <AutoTable data={discoveries} loading={loading} rowKey="key" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "name", name: t("name")}, {field: "kind", name: t("agentField_kind")}, {field: "runtime", name: t("fleetRuntime")}, {field: "scope", name: t("fleetTab_workdirs")}, {field: "state", name: t("fleetState")}, {field: "hash", name: t("agentField_content_hash")}, {field: "checked_at", name: t("fleetCreatedAt")}]} /></>}
  </section>
}

function VersionedTools({tenant, kind}: {tenant: string; kind: "MCP_SERVER" | "SKILL"}) {
  const t = useTranslations("iam")
  const [entries, setEntries] = useState<components["schemas"]["ToolView"][]>([])
  const [cursor, setCursor] = useState("")
  const [selected, setSelected] = useState("")
  const [detail, setDetail] = useState<CatalogDetail | null>(null)
  const [credential, setCredential] = useState("")
  const [rotate, setRotate] = useState(false)
  const [rotated, setRotated] = useState("")
  const [presets, setPresets] = useState<CatalogPreset[]>([])
  const [preset, setPreset] = useState("")
  const [machine, setMachine] = useState("")
  const [sources, setSources] = useState<NonNullable<CatalogImport["from_local"]>[]>([])
  const [local, setLocal] = useState("")
  const [entryKey, setEntryKey] = useState("")
  const [title, setTitle] = useState("")
  const [transport, setTransport] = useState<CatalogImport["transport"]>("STREAMABLE_HTTP")
  const [endpoint, setEndpoint] = useState("")
  const [hash, setHash] = useState("")
  const [note, setNote] = useState("")
  const [reason, setReason] = useState("")
  const [upstreamHash, setUpstreamHash] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const [revision, setRevision] = useState(0)
  const active = useRef(true)
  const command = useRef<{signature: string; key: string} | null>(null)
  const paging = useRef<AbortController | null>(null)
  useEffect(() => {active.current = true; return () => {active.current = false; paging.current?.abort()}}, [])
  useEffect(() => {
    const controller = new AbortController()
    void Promise.all([iamApi.agentTools(tenant, undefined, undefined, controller.signal), toolVersions.presets(tenant, controller.signal)]).then(([page, templates]) => {
      if (!controller.signal.aborted) {setEntries(page.items ?? []); setCursor(page.next_cursor ?? ""); setPresets(templates)}
    }, cause => {if (!controller.signal.aborted) setError(cause.message)})
    return () => controller.abort()
  }, [tenant, revision])
  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    void toolVersions.detail(tenant, selected, controller.signal).then(value => {if (!controller.signal.aborted) {setDetail(value); setTitle(value.entry.title ?? ""); setEndpoint(value.entry.endpoint ?? ""); setTransport(value.entry.transport === "STDIO_PACKAGE" ? "STDIO_PACKAGE" : "STREAMABLE_HTTP"); setHash(value.entry.content_hash ?? "")}}, cause => {if (!controller.signal.aborted) {setDetail(null); setError(cause.message)}})
    return () => controller.abort()
  }, [tenant, selected, revision])
  const run = async (signature: string, fn: (key: string) => Promise<unknown>) => {
    if (busy) return
    setBusy(true); setError("")
    if (command.current?.signature !== signature) command.current = {signature, key: crypto.randomUUID()}
    try {await fn(command.current.key); if (active.current) setRevision(value => value + 1)}
    catch (cause) {if (active.current) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {if (active.current) setBusy(false)}
  }
  const more = async () => {
    if (!cursor || paging.current) return
    const controller = new AbortController(); paging.current = controller
    try {const page = await iamApi.agentTools(tenant, undefined, cursor, controller.signal); if (!controller.signal.aborted) {setEntries(previous => [...previous, ...(page.items ?? [])]); setCursor(page.next_cursor ?? "")}}
    catch (cause) {if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {if (paging.current === controller) paging.current = null}
  }
  const state = (value: "ACTIVE" | "DISABLED" | "ARCHIVED", versionNo?: number) => {
    if (!detail) return
    const body = {version: detail.entry.version ?? 0, reason, ...(versionNo ? {version_no: versionNo, version_state: "REVOKED" as const} : {state: value})}
    void run(JSON.stringify({selected, body}), key => toolVersions.state(tenant, selected, body, key))
  }
  const loadSources = async () => {
    setBusy(true); setError(""); setSources([]); setLocal("")
    try {const facts = await toolVersions.sources(tenant, machine); if (active.current) setSources((facts.discoveries ?? []).flatMap(inspection => (inspection.discovery ?? []).filter(runtime => runtime.state === "READY").flatMap(runtime => (runtime.mcp ?? []).map(name => ({machine_id: machine, runtime: runtime.runtime as "CODEX" | "CLAUDE_CODE", name, scope: inspection.scope_key ?? ""})))))}
    catch (cause) {if (active.current) setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    finally {if (active.current) setBusy(false)}
  }
  const inputClass = "min-h-11 rounded-md border bg-background px-3"
  const configFields = <>
    <label className="grid gap-2 text-sm">{t("agentField_transport")}<select className={inputClass} value={transport} onChange={event => setTransport(event.target.value as CatalogImport["transport"])}><option value="STREAMABLE_HTTP">STREAMABLE_HTTP</option><option value="STDIO_PACKAGE">STDIO_PACKAGE</option></select></label>
    <label className="grid gap-2 text-sm">{t("agentField_endpoint")}<Input required maxLength={512} value={endpoint} onChange={event => setEndpoint(event.target.value)} /></label>
    {transport === "STDIO_PACKAGE" && <label className="grid gap-2 text-sm">{t("agentField_content_hash")}<Input required pattern="[a-fA-F0-9]{64}" maxLength={64} value={hash} onChange={event => setHash(event.target.value)} /></label>}
  </>
  return <section className="grid min-w-0 gap-5 border-t pt-5">
    <h2 className="text-lg font-semibold">{t("toolVersionsTitle")}</h2>
    {error && <Alert>{error}</Alert>}
    <label className="grid max-w-xl gap-2 text-sm">{t("toolSelectEntry")}<select className={inputClass} value={selected} disabled={busy} onChange={event => {setSelected(event.target.value); setCredential(""); setRotated(""); setRotate(false); setDetail(null); setReason(""); setError("")}}><option value="">{t("toolSelectEntry")}</option>{entries.filter(row => row.kind === kind).map(row => <option key={row.id} value={row.id ?? ""}>{row.title} · {row.entry_key} · {row.state}</option>)}</select></label>
    {cursor && <Button variant="outline" className="justify-self-start" onClick={() => void more()}>{t("agentMore")}</Button>}
    {detail && <div className="grid gap-4">
      <p>{detail.entry.entry_key} · {detail.entry.state} · {t("toolCurrentVersion")} {detail.current_version_no}</p>
      {detail.entry.kind === "MCP_SERVER" && detail.entry.transport === "STREAMABLE_HTTP" && detail.entry.state !== "ARCHIVED" && <form className="grid max-w-2xl gap-3" onSubmit={event => {event.preventDefault(); setRotate(true)}}>
        <label className="grid gap-2 text-sm">{t("toolRotateCredential")}<Input type="password" autoComplete="new-password" value={credential} maxLength={8000} required onChange={event => setCredential(event.target.value)} /></label>
        <Button className="justify-self-start" disabled={busy || !credential}>{t("toolRotateCredential")}</Button>
        <p className="text-sm text-muted-foreground">{t("toolCredentialNextCall")}</p>{rotated && <p role="status">••••{rotated}</p>}
      </form>}
      {rotate && <ReauthenticationDialog purpose="agent_tool_credential_write" target={`tenant:${tenant}:tool:${detail.entry.entry_key}`} onClose={() => {setRotate(false); setCredential("")}} onVerified={async proof => {
        const result = await toolVersions.credential(tenant, selected, {version: detail.entry.version ?? 0, secret: credential, reauth_proof: proof}, crypto.randomUUID())
        setCredential(""); setRotate(false); setRotated(result.last4); setRevision(value => value + 1)
      }} />}
      <form className="flex flex-wrap items-end gap-3" onSubmit={event => {event.preventDefault(); const body = {version: detail.entry.version ?? 0, title}; void run(JSON.stringify({selected, body}), key => toolVersions.metadata(tenant, selected, body, key))}}><label className="grid gap-2 text-sm">{t("agentField_title")}<Input required maxLength={200} value={title} onChange={event => setTitle(event.target.value)} /></label><Button disabled={busy || detail.entry.state === "ARCHIVED"}>{t("toolMetadataSave")}</Button></form>
      <AutoTable data={detail.versions} rowKey="id" pagination={false} exportable={false} columns={[{field: "version_no", name: t("fleetVersion")}, {field: "state", name: t("fleetState")}, {field: "content_sha256", name: t("agentField_content_hash")}, {field: "source", name: t("toolSource"), format: (_, row) => t(`toolSource_${row.source}`)}, {field: "source_ref", name: t("toolLocalName")}, {field: "endpoint", name: t("agentField_endpoint")}, {field: "change_note", name: t("toolChangeNote")}, {field: "published_at", name: t("fleetCreatedAt")}]} />
      {detail.entry.kind === "MCP_SERVER" && detail.entry.state !== "ARCHIVED" && <form className="grid max-w-2xl gap-3" onSubmit={event => {event.preventDefault(); const body = {version: detail.entry.version ?? 0, transport, endpoint, content_hash: hash, change_note: note}; void run(JSON.stringify({selected, body}), key => toolVersions.publish(tenant, selected, body, key))}}><h3 className="font-medium">{t("toolPublishVersion")}</h3>{configFields}<label className="grid gap-2 text-sm">{t("toolChangeNote")}<Input maxLength={2000} value={note} onChange={event => setNote(event.target.value)} /></label><Button disabled={busy} className="justify-self-start">{t("toolPublishVersion")}</Button></form>}
      {detail.entry.state !== "ARCHIVED" && <div className="grid gap-3"><label className="grid max-w-2xl gap-2 text-sm">{t("toolReason")}<Input minLength={10} maxLength={2000} value={reason} onChange={event => setReason(event.target.value)} /></label><div className="flex flex-wrap gap-2">{detail.entry.state !== "ACTIVE" && <Button disabled={busy} onClick={() => state("ACTIVE")}>{t("toolEnable")}</Button>}{detail.entry.state === "ACTIVE" ? <Button variant="outline" disabled={busy || reason.trim().length < 10} onClick={() => state("DISABLED")}>{t("toolDisable")}</Button> : <Button variant="outline" disabled={busy || reason.trim().length < 10} onClick={() => state("ARCHIVED")}>{t("toolArchive")}</Button>}{detail.entry.kind === "MCP_SERVER" && detail.versions.filter(row => row.state === "ACTIVE").map(row => <Button key={row.id} variant="outline" disabled={busy || reason.trim().length < 10} onClick={() => state("DISABLED", row.version_no)}>{t("toolRevoke")} v{row.version_no}</Button>)}</div></div>}
      <p role="status">{t(detail.upstream_changed ? "toolUpstreamChanged" : "toolUpstreamUnchanged")}</p>
      {detail.entry.state !== "ARCHIVED" && <form className="grid max-w-2xl gap-3" onSubmit={event => {event.preventDefault(); const body = {version: detail.entry.version ?? 0, content_hash: upstreamHash, note: reason}; void run(JSON.stringify({selected, body}), key => toolVersions.upstream(tenant, selected, body, key))}}><label className="grid gap-2 text-sm">{t("toolReportHash")}<Input required pattern="[a-fA-F0-9]{64}" maxLength={64} value={upstreamHash} onChange={event => setUpstreamHash(event.target.value)} /></label><Button className="justify-self-start" disabled={busy || reason.trim().length < 10}>{t("toolUpstreamReport")}</Button></form>}
      <AutoTable data={detail.upstream} rowKey="id" pagination={false} exportable={false} columns={[{field: "content_hash", name: t("toolReportHash")}, {field: "note", name: t("toolReason")}, {field: "reported_by", name: t("agentPrincipal")}, {field: "reported_at", name: t("fleetCreatedAt")}]} />
    </div>}
    {kind === "MCP_SERVER" && !selected && <form className="grid max-w-2xl gap-3" onSubmit={event => {event.preventDefault(); const source = local ? sources[Number(local)] : undefined; const body: CatalogImport = {entry_key: entryKey, kind: "MCP_SERVER", title, transport, endpoint, content_hash: hash, ...(source ? {from_local: source} : {preset_key: preset})}; void run(JSON.stringify(body), async key => {const created = await toolVersions.import(tenant, body, key); if (active.current) {setSelected(created.id ?? ""); setDetail(null)}})}}>
      <h3 className="font-medium">{t("toolSourceImport")}</h3><p className="text-sm text-muted-foreground">{t("toolImportDisabled")}</p>
      <label className="grid gap-2 text-sm">{t("toolPreset")}<select className={inputClass} value={preset} onChange={event => {const row = presets.find(item => item.key === event.target.value); setPreset(event.target.value); setLocal(""); if (row) {setTransport(row.transport === "STDIO_PACKAGE" ? "STDIO_PACKAGE" : "STREAMABLE_HTTP"); setEndpoint(row.endpoint); setHash(row.content_hash)}}}><option value="">{t("toolPreset")}</option>{presets.map(row => <option key={row.key} value={row.key}>{row.title}</option>)}</select></label>
      <div className="flex flex-wrap items-end gap-3"><label className="grid gap-2 text-sm">{t("fleetMachineID")}<Input inputMode="numeric" pattern="[1-9][0-9]{0,18}" value={machine} onChange={event => {setMachine(event.target.value); setSources([]); setLocal("")}} /></label><Button type="button" variant="outline" disabled={busy || !/^[1-9][0-9]{0,18}$/.test(machine)} onClick={() => void loadSources()}>{t("toolLoadSources")}</Button></div>
      <label className="grid gap-2 text-sm">{t("toolLocalName")}<select className={inputClass} value={local} onChange={event => {setLocal(event.target.value); setPreset("")}}><option value="">{t("toolNoMachineMcp")}</option>{sources.map((row, index) => <option key={`${row.runtime}:${row.scope}:${row.name}`} value={String(index)}>{row.name} · {row.runtime} · {row.scope}</option>)}</select></label>
      <label className="grid gap-2 text-sm">{t("agentField_entry_key")}<Input required pattern="[a-z0-9][a-z0-9-]{1,63}" maxLength={64} value={entryKey} onChange={event => setEntryKey(event.target.value)} /></label><label className="grid gap-2 text-sm">{t("agentField_title")}<Input required maxLength={200} value={title} onChange={event => setTitle(event.target.value)} /></label>{configFields}<Button className="justify-self-start" disabled={busy || (!preset && local === "")}>{t("toolImport")}</Button>
    </form>}
  </section>
}
