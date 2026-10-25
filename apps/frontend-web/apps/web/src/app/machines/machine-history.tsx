import {useEffect, useState} from "react"
import {Link} from "react-router"
import {AutoTable} from "@workspace/ui/components/auto-table"
import {Button} from "@workspace/ui/components/button"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {machineHistoryApi, type HostedAgent, type MachineExecution, type HistoryPage} from "./machine-history-api"
export function MachineHistory({tenantID, machineID, kind, allowed}: {tenantID: string; machineID: string; kind: "agents" | "history"; allowed: boolean}) {
 const t = useTranslations("iam")
 const [page, setPage] = useState<HistoryPage<HostedAgent | MachineExecution> | null>(null)
 const [cursor, setCursor] = useState<string>()
 const [previous, setPrevious] = useState<(string | undefined)[]>([])
 const [error, setError] = useState("")
 const [loading, setLoading] = useState(true)
 const [refresh, setRefresh] = useState(0)
 useEffect(() => {
  if (!allowed) return
  const controller = new AbortController()
  const request = kind === "agents" ? machineHistoryApi.agents(tenantID, machineID, cursor, controller.signal) : machineHistoryApi.executions(machineID, cursor, controller.signal)
  request.then(value => {if (!controller.signal.aborted) {setPage(value);setLoading(false)}}).catch(cause => {if (!controller.signal.aborted) {setError(cause instanceof Error ? cause.message : t("fleetUnavailable"));setLoading(false)}})
  return () => controller.abort()
 }, [tenantID, machineID, kind, allowed, cursor, refresh, t])
 if (!allowed) return <p role="status">{t("fleetHistoryNoAccess")}</p>
 const columns = kind === "agents" ? [{field: "name", name: t("name")}, {field: "state", name: t("fleetState")}, {field: "process_status", name: t("fleetProcessState")}, {field: "runtime", name: t("fleetRuntime")}, {field: "model", name: t("fleetModel")}] : [{field: "id", name: t("executionID"), render: (_: unknown, row: HostedAgent | MachineExecution) => "project_id" in row ? <Link className="underline" to={`/admin/${encodeURIComponent(tenantID)}/entities/${row.project_id}/executions/${row.id}`}>{row.id}</Link> : row.id}, {field: "state", name: t("fleetState")}, {field: "result_state", name: t("fleetResultState")}, {field: "runtime", name: t("fleetRuntime")}, {field: "model", name: t("fleetModel")}, {field: "created_at", name: t("fleetCreatedAt")}, {field: "updated_at", name: t("fleetHistoryUpdatedAt")}]
 return <section className="min-w-0 space-y-3" aria-label={t(`fleetTab_${kind}`)}>
  <p className="text-sm text-muted-foreground">{t(kind === "agents" ? "fleetHostedAgentsHint" : "fleetExecutionHistoryHint")}</p>
  {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
  <div className="overflow-x-auto"><AutoTable data={page?.items ?? []} rowKey="id" pagination={false} exportable={false} emptyContent={t(kind === "agents" ? "fleetNoHostedAgents" : "fleetNoExecutionHistory")} columns={columns}/></div>
  <div className="flex flex-wrap gap-2"><Button variant="outline" disabled={loading || previous.length === 0} onClick={() => {setLoading(true);setError("");setCursor(previous.at(-1));setPrevious(old => old.slice(0, -1))}}>{t("previous")}</Button><Button variant="outline" disabled={loading || !page?.has_more} onClick={() => {setLoading(true);setError("");setPrevious(old => [...old, cursor]);setCursor(page?.next_cursor)}}>{t("next")}</Button><Button variant="outline" disabled={loading} onClick={() => {setLoading(true);setError("");setRefresh(value => value + 1)}}>{t("refresh")}</Button></div>
 </section>
}
