import { useCallback, useEffect, useRef, useState } from "react"
import { Link, useParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Tabs, TabsList, TabsTrigger, TabsContent } from "@workspace/ui/components/tabs"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import type { components } from "../../lib/api-schema"
import { RemoteTerminal } from "./remote-terminal"
import {ModelCatalogEditor} from "./model-catalog"
import {WorkdirList} from "./workdirs"
import {ModelPrices} from "./model-prices"
import {MachineHistory} from "./machine-history"

export default function MachineDetailPage() {
  const {tenantID = "", machineID = ""} = useParams()
  return <MachineDetail key={`${tenantID}:${machineID}`} tenantID={tenantID} machineID={machineID} />
}
function MachineDetail({tenantID, machineID}: {tenantID: string; machineID: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [machine, setMachine] = useState<components["schemas"]["MachineView"] | null>(null)
  const [facts, setFacts] = useState<components["schemas"]["MachineCapabilities"] | null>(null)
  const [credentials, setCredentials] = useState<components["schemas"]["MachineCredentials"] | null>(null)
  const [inspections, setInspections] = useState<components["schemas"]["InspectionPage"] | null>(null)
  const [scope, setScope] = useState("home")
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const pending = useRef<AbortController | null>(null)
  const load = useCallback(async () => {
    pending.current?.abort(); const controller = new AbortController(); pending.current = controller
    setLoading(true); setError("")
    try {const [row, capabilities, credentialFacts, inspectionFacts] = await Promise.all([iamApi.machine(machineID, controller.signal), iamApi.machineCapabilities(machineID, controller.signal), iamApi.machineCredentials(machineID, controller.signal), iamApi.machineInspections(machineID, undefined, controller.signal)]); if (!controller.signal.aborted) {setMachine(row); setFacts(capabilities); setCredentials(credentialFacts); setInspections(inspectionFacts)}}
    catch (cause) {if (!controller.signal.aborted) {setMachine(null); setFacts(null); setCredentials(null); setInspections(null); setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}}
    finally {if (!controller.signal.aborted) setLoading(false)}
  }, [machineID, t])
  useEffect(() => {let active = true; queueMicrotask(() => {if (active) void load()}); return () => {active = false; pending.current?.abort()}}, [load])
  return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto">
    <Link className="text-sm underline" to={`/admin/${encodeURIComponent(tenantID)}/machines`}>{t("machines")}</Link>
    <PageHeader title={machine?.name ?? t("fleetDetails")} action={<Button variant="outline" disabled={loading || busy} onClick={() => void load()}>{t("refresh")}</Button>} />
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    {machine && facts && !loading && <>
      <div className="flex flex-wrap items-center justify-between gap-3"><span className="rounded-full border px-3 py-1 text-sm">{t(`fleetState_${machine.state}`)}</span>
        {allowed("machine_manage") && machine.state !== "REVOKED" && <Button variant="outline" disabled={busy} onClick={async () => {setBusy(true); setError(""); try {setMachine(await iamApi.drainMachine(machineID, machine.version, !machine.drain_requested))} catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))} finally {setBusy(false)}}}>{t(machine.drain_requested ? "fleetResume" : "fleetDrain")}</Button>}
      </div>
      <Tabs defaultValue="overview">
        <TabsList className="h-auto max-w-full flex-wrap justify-start" aria-label={t("fleetDetails")}>
          {["overview", "runtimes", "models", "prices", "capabilities", "tools", "agents", "history", "workdirs", "slots", "credentials", "inspections", "remote"].map((tab) => <TabsTrigger className="min-h-11" key={tab} value={tab}>{t(`fleetTab_${tab}`)}</TabsTrigger>)}
        </TabsList>
        <TabsContent value="overview"><AutoForm mode="detail" initialData={{id: machine.id, os: machine.os, state: t(`fleetState_${machine.state}`), slots: machine.slot_count}} fields={{id: {name: t("fleetMachineID"), type: "text"}, os: {name: t("fleetOS"), type: "text"}, state: {name: t("fleetState"), type: "text"}, slots: {name: t("fleetSlots"), type: "number"}}} /></TabsContent>
        <TabsContent value="runtimes"><AutoTable rowProps={() => ({tabIndex: 0})} data={facts.runtimes ?? []} rowKey="kind" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "kind", name: t("fleetRuntime")}, {field: "version", name: t("fleetVersion")}, {field: "path", name: t("fleetPath")}]} /></TabsContent>
        <TabsContent value="models">{(facts.discoveries ?? []).filter((row) => row.kind === "MODELS").flatMap((row) => row.discovery ?? []).map((row) => <p key={row.runtime} className="mb-2 break-words text-sm">{row.runtime} · {row.state} · {t("fleetDiscoverySource")}: {row.source} · {t("fleetDiscoveryLastSuccess")}: {row.last_success_at ?? "—"} {row.reason}</p>)}<p className="mb-3 text-sm text-muted-foreground">{t("fleetModelsHint")}</p><AutoTable rowProps={() => ({tabIndex: 0})} data={(facts.runtimes ?? []).flatMap((runtime) => (runtime.models ?? []).map((model) => ({key: `${runtime.kind}:${model}`, runtime: runtime.kind, model})))} rowKey="key" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "runtime", name: t("fleetRuntime")}, {field: "model", name: t("fleetModel")}]} />{["CODEX", "CLAUDE_CODE"].map(runtime => <ModelCatalogEditor key={`${machineID}:${runtime}`} machineID={machineID} runtime={runtime as "CODEX" | "CLAUDE_CODE"} manage={allowed("machine_manage") && machine.state !== "REVOKED"}/>)}{allowed("machine_read") && <Button variant="outline" disabled={busy || machine.state !== "ONLINE"} onClick={async () => {setBusy(true); setError(""); try {await iamApi.requestMachineInspection(machineID, "MODELS"); await load()} catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))} finally {setBusy(false)}}}>{t("fleetRefreshModels")}</Button>}</TabsContent>
        <TabsContent value="capabilities">
          <p className="mb-3 text-sm text-muted-foreground">{t("fleetDiscoveryHint")}</p>
          <label className="mb-3 flex flex-col gap-1 text-sm">{t("fleetTab_workdirs")}<select className="min-h-11 rounded-md border bg-background px-3" value={scope} onChange={(event) => setScope(event.target.value)}><option value="home">HOME</option>{(facts.workdirs ?? []).filter(workdir => !workdir.archived_at).map((workdir) => <option key={workdir.id} value={workdir.id ?? ""}>{workdir.path}</option>)}</select></label>
          {(facts.discoveries ?? []).filter((row) => row.kind === "CAPABILITIES" && row.scope_key === scope).flatMap((row) => row.discovery ?? []).map((row) => <section key={row.runtime} className="mb-4 min-w-0 rounded-lg border p-3">
            <h3 className="font-medium">{row.runtime} · {row.state}</h3>
            <p className="text-sm">{t("fleetDiscoverySource")}: {row.source} · {t("fleetDiscoveryCheckedAt")}: {row.checked_at ?? "—"}</p>
            {row.reason && <p role="status" className="break-words text-sm">{row.reason}</p>}
            <h4 className="mt-3 font-medium">{t("fleetDiscoverySkills")}</h4>
            <AutoTable rowProps={() => ({tabIndex: 0})} data={row.skills ?? []} rowKey="name" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "name", name: t("name")}, {field: "sha256", name: "SHA-256"}]} />
            <h4 className="mt-3 font-medium">{t("fleetDiscoveryMCP")}</h4>
            <ul className="list-inside list-disc break-words">{(row.mcp ?? []).map((name) => <li key={name}>{name}</li>)}</ul>
          </section>)}
          {allowed("machine_read") && <AutoForm mode="edit" key={scope} initialData={{workdir_id: scope === "home" ? "" : scope}} fields={{workdir_id: {name: t("fleetTab_workdirs"), type: "text"}}} onSubmit={async (values) => {
            setBusy(true); setError("")
            try {await iamApi.requestMachineInspection(machineID, "CAPABILITIES", String(values.workdir_id ?? "").trim() || undefined); await load()}
            catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
            finally {setBusy(false)}
          }}><Button type="submit" disabled={busy || machine.state !== "ONLINE"}>{t("refresh")}</Button></AutoForm>}
        </TabsContent>
        <TabsContent value="tools"><AutoTable rowProps={() => ({tabIndex: 0})} data={facts.tools ?? []} rowKey="name" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "name", name: t("name")}, {field: "version", name: t("fleetVersion")}, {field: "path", name: t("fleetPath")}]} /></TabsContent>
        <TabsContent value="workdirs">
          {allowed("machine_manage") && machine.state !== "REVOKED" && <AutoForm mode="edit" initialData={{project_id: "", repository_id: "", path: ""}} fields={{project_id: {name: t("fleetProject"), type: "text"}, repository_id: {name: t("fleetRepository"), type: "text"}, path: {name: t("fleetPath"), type: "text"}}} onSubmit={async (values) => {
            if (busy) return
            setBusy(true); setError("")
            try {await iamApi.registerMachineWorkdir(machineID, {project_id: String(values.project_id ?? "").trim(), repository_id: String(values.repository_id ?? "").trim(), path: String(values.path ?? "")}); await load()}
            catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
            finally {setBusy(false)}
          }}><Button type="submit" disabled={busy}>{busy ? t("loading") : t("save")}</Button></AutoForm>}
          <WorkdirList machineID={machineID} rows={(facts.workdirs ?? []).flatMap(row => row.id && row.project_id && row.repository_id && row.path && row.version ? [{id: row.id, project_id: row.project_id, repository_id: row.repository_id, path: row.path, version: row.version, archived_at: row.archived_at}] : [])} manage={allowed("machine_manage") && machine.state !== "REVOKED"} probe={allowed("machine_read")} online={machine.state === "ONLINE"} reload={load}/>
        </TabsContent>
        <TabsContent value="slots"><AutoTable rowProps={() => ({tabIndex: 0})} data={facts.active_slots ?? []} rowKey="execution_id" pagination={false} exportable={false} emptyContent={t("fleetNoSlots")} columns={[{field: "slot_no", name: t("fleetSlots")}, {field: "execution_id", name: t("executionID")}]} /></TabsContent>
        <TabsContent value="credentials">
          <p className="mb-3 max-w-3xl text-sm text-muted-foreground">{t("fleetCredentialHint")}</p>
          {credentials?.rotation && <p className="mb-3 text-sm" role="status">{t("fleetCredentialRotation")}: {t(`fleetCredential_${credentials.rotation.state}`)} · {credentials.rotation.attempts}/5 · {t("fleetCredentialRetireAt")}: {credentials.rotation.retire_old_at}{credentials.rotation.reason_code && ` · ${credentials.rotation.reason_code}`}</p>}
          {allowed("machine_manage") && credentials?.rotation_available && machine.state !== "REVOKED" && <Button className="mb-3 min-h-11" variant="outline" disabled={busy || credentials.rotation?.state === "PENDING_DELIVERY"} onClick={async () => {
            setBusy(true); setError("")
            try {await iamApi.rotateMachineCredential(machineID); await load()}
            catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
            finally {setBusy(false)}
          }}>{t("fleetRotateCredential")}</Button>}
          <AutoTable rowProps={() => ({tabIndex: 0})} data={(credentials?.items ?? []).map((item) => {
            const lifecycle = credentials?.lifecycle?.find((row) => row.id === item.id)
            return {...item, key: item.id ?? item.name, scopes: (item.scopes ?? []).join(", "), lifecycle: lifecycle ? t(`fleetCredential_${lifecycle.state}`) : "", retire_at: lifecycle?.retire_at}
          })} rowKey="key" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "name", name: t("name")}, {field: "scopes", name: t("fleetCredentialScopes")}, {field: "lifecycle", name: t("fleetState")}, {field: "retire_at", name: t("fleetCredentialRetireAt")}, {field: "created_at", name: t("fleetCreatedAt")}, {field: "last_used_at", name: t("fleetCredentialLastUsed")}, {field: "revoked_at", name: t("fleetCredentialRevoked")}]} />
        </TabsContent>
        <TabsContent value="inspections">
          <p className="mb-3 text-sm text-muted-foreground">{t("fleetInspectionHint")}</p>
          {allowed("machine_read") && <div className="mb-3 flex flex-wrap gap-2">{(["RUNTIMES", "MODELS", "CAPABILITIES"] as const).map((kind) => <Button key={kind} variant="outline" className="min-h-11" disabled={busy || machine.state !== "ONLINE"} onClick={async () => {
            setBusy(true); setError("")
            try {await iamApi.requestMachineInspection(machineID, kind); await load()}
            catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
            finally {setBusy(false)}
          }}>{t(`fleetInspect_${kind}`)}</Button>)}</div>}
          <AutoTable rowProps={() => ({tabIndex: 0})} data={(inspections?.items ?? []).map((item) => ({...item, kind: t(`fleetInspect_${item.kind}`), state: t(`fleetInspection_${item.state}`)}))} rowKey="id" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "kind", name: t("fleetRuntime")}, {field: "state", name: t("fleetState")}, {field: "scope_key", name: t("fleetTab_workdirs")}, {field: "reason_code", name: t("fleetInspectionReason")}, {field: "created_at", name: t("fleetCreatedAt")}]} />
          {inspections?.has_more && <Button variant="outline" disabled={busy} onClick={async () => {
            setBusy(true); setError("")
            try {const page = await iamApi.machineInspections(machineID, inspections.next_cursor); setInspections({...page, items: [...(inspections.items ?? []), ...(page.items ?? [])]})}
            catch (cause) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
            finally {setBusy(false)}
          }}>{t("fleetMoreInspections")}</Button>}
        </TabsContent>
        <TabsContent value="remote"><RemoteTerminal machineID={machineID} os={machine.os} online={machine.state === "ONLINE" || machine.state === "DRAINING"} executionIDs={(facts.active_slots ?? []).flatMap((slot) => slot.execution_id ? [slot.execution_id] : [])} /></TabsContent>
        <TabsContent value="agents"><MachineHistory key={`${machineID}:agents`} tenantID={tenantID} machineID={machineID} kind="agents" allowed={allowed("agent_read")}/></TabsContent>
        <TabsContent value="history"><MachineHistory key={`${machineID}:history`} tenantID={tenantID} machineID={machineID} kind="history" allowed={allowed("machine_read")}/></TabsContent>
        <TabsContent value="prices"><ModelPrices tenantID={tenantID} manage={allowed("machine_manage")}/></TabsContent>
      </Tabs>
    </>}
  </div>
}
