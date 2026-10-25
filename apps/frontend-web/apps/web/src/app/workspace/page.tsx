import { useCallback } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { executionWorkdirs, workspaceTab } from "../runtime/facts"
import { useFacts } from "../runtime/use-facts"
import { EvidenceProvenancePanel } from "../executions/provenance"
import { RemoteTerminal } from "../machines/remote-terminal"

export default function WorkspacePage() {
  const {tenantID = "", entityID = "", executionID = ""} = useParams()
  return <Workspace key={`${tenantID}:${entityID}:${executionID}`} tenantID={tenantID} projectID={entityID} executionID={executionID}/>
}
function Workspace({tenantID, projectID, executionID}: {tenantID: string; projectID: string; executionID: string}) {
  const t = useTranslations("iam")
  const [search, setSearch] = useSearchParams()
  const tab = workspaceTab(search.get("tab"))
  const load = useCallback(async (signal: AbortSignal) => {
    const detail = await iamApi.execution(executionID, signal)
    if (detail.execution.project_id !== projectID) throw new Error(t("executionWrongProject"))
    return detail
  }, [executionID, projectID, t])
  const {data, error, loading, refresh} = useFacts(`${tenantID}:${projectID}:${executionID}`, load)
  const execution = data?.execution
  const root = `/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(projectID)}/executions`
  return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto">
    <PageHeader title={t("runtimeWorkspace")} description={executionID} action={<Button variant="outline" disabled={loading} onClick={refresh}>{t("refresh")}</Button>}/>
    <Link className="text-sm underline" to={`${root}/${encodeURIComponent(executionID)}`}>{t("fleetDetails")}</Link>
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    {execution && data && <>
      <dl className="grid gap-3 rounded-lg border p-4 sm:grid-cols-2">{[["executionWorkItem", execution.work_item_id], ["fleetMachineID", execution.machine_id], ["fleetPath", execution.plan.workdir], ["executionBaseCommit", execution.plan.base_commit], ["fleetState", t(`executionState_${execution.state}`)], ["executionResult", t(`executionResult_${execution.result_state}`)]].map(([label, value]) => <div className="min-w-0" key={label}><dt className="text-sm text-muted-foreground">{t(label!)}</dt><dd className="break-all text-sm">{value || "—"}</dd></div>)}</dl>
      <Tabs value={tab} onValueChange={value => setSearch(previous => {const next = new URLSearchParams(previous); next.set("tab", workspaceTab(value)); return next})}><TabsList className="h-auto max-w-full flex-wrap" aria-label={t("runtimeWorkspace")}>{["logs", "evidence", "workdirs", "remote"].map(tab => <TabsTrigger className="min-h-11" key={tab} value={tab}>{t(tab === "logs" || tab === "evidence" ? `executionTab_${tab}` : `fleetTab_${tab}`)}</TabsTrigger>)}</TabsList>
        <TabsContent value="logs">{data.has_more_logs && <Alert>{t("executionLogsTruncated")}</Alert>}<pre className="max-h-[60dvh] overflow-auto whitespace-pre-wrap break-words rounded-lg border bg-muted/30 p-4 text-xs">{(data.logs ?? []).map(row => `[${row.dispatch_epoch}:${row.seq}] ${row.content}`).join("\n") || t("executionNoLogs")}</pre></TabsContent>
        <TabsContent value="evidence"><EvidenceProvenancePanel executionID={executionID}/></TabsContent>
        <TabsContent value="workdirs">{execution.machine_id && <WorkspaceDirectory key={execution.machine_id} execution={execution}/>}<p className="mt-3 text-sm text-muted-foreground">{t("runtimeFilesUnavailable")}</p></TabsContent>
        <TabsContent value="remote">{execution.machine_id ? <WorkspaceRemote key={`${execution.id}:${execution.machine_id}`} execution={execution}/> : <Alert>{t("fleetUnavailable")}</Alert>}</TabsContent>
      </Tabs>
    </>}
  </div>
}
function WorkspaceDirectory({execution}: {execution: Awaited<ReturnType<typeof iamApi.execution>>["execution"]}) {
  const t = useTranslations("iam")
  const load = useCallback((signal: AbortSignal) => iamApi.machineCapabilities(execution.machine_id!, signal), [execution.machine_id])
  const {data, error, loading, refresh} = useFacts(execution.machine_id!, load)
  return <section className="space-y-3">{error && <Alert>{error}</Alert>}<Button variant="outline" disabled={loading} onClick={refresh}>{t("refresh")}</Button><AutoTable loading={loading} data={data ? executionWorkdirs(execution, data) : []} rowKey="id" pagination={false} exportable={false} emptyContent={t("fleetNoWorkdirs")} columns={[{field: "id", name: t("fleetTab_workdirs")}, {field: "repository_id", name: t("fleetRepository")}, {field: "path", name: t("fleetPath")}]} /></section>
}
function WorkspaceRemote({execution}: {execution: Awaited<ReturnType<typeof iamApi.execution>>["execution"]}) {
  const t = useTranslations("iam")
  const load = useCallback(async (signal: AbortSignal) => {
    const machine = await iamApi.machine(execution.machine_id!, signal)
    if (machine.id !== execution.machine_id) throw new Error(t("fleetUnavailable"))
    return machine
  }, [execution.machine_id, t])
  const {data, error, loading, refresh} = useFacts(execution.machine_id!, load)
  return <section className="space-y-3">{error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}{error && <Button variant="outline" onClick={refresh}>{t("refresh")}</Button>}{data?.id && execution.id && <RemoteTerminal machineID={data.id} os={data.os} online={data.state === "ONLINE" || data.state === "DRAINING"} executionIDs={[execution.id]}/>}</section>
}
