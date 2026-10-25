import { useCallback, useEffect, useRef, useState } from "react"
import { useNavigate, useParams } from "react-router"
import { Eye } from "lucide-react"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"

export default function ExecutionsPage() {
  const {tenantID = "", entityID = ""} = useParams()
  return <Executions key={`${tenantID}:${entityID}`} tenantID={tenantID} projectID={entityID} />
}
function Executions({tenantID, projectID}: {tenantID: string; projectID: string}) {
  const t = useTranslations("iam")
  const navigate = useNavigate()
  const [page, setPage] = useState<components["schemas"]["ExecutionPage"]>({items: [], has_more: false})
  const [cursors, setCursors] = useState<string[]>([""])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const pending = useRef<AbortController | null>(null)
  const cursor = cursors[cursors.length - 1] ?? ""
  const load = useCallback(async () => {
    pending.current?.abort(); const controller = new AbortController(); pending.current = controller
    setLoading(true); setError("")
    try {const result = await iamApi.executions(tenantID, projectID, cursor || undefined, controller.signal); if (!controller.signal.aborted) setPage(result)}
    catch (cause) {if (!controller.signal.aborted) {setPage({items: [], has_more: false}); setError(cause instanceof Error ? cause.message : t("executionUnavailable"))}}
    finally {if (!controller.signal.aborted) setLoading(false)}
  }, [tenantID, projectID, cursor, t])
  useEffect(() => {let active = true; queueMicrotask(() => {if (active) void load()}); return () => {active = false; pending.current?.abort()}}, [load])
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
    <PageHeader title={t("executions")} description={t("executionDescription")} />
    {error && <Alert>{error}</Alert>}
    <AutoTable fill data={loading ? [] : page.items ?? []} loading={loading} rowKey="id" pagination={false} onRefresh={load} exportable={false} emptyContent={error ? t("fleetRetry") : t("executionEmpty")} columns={[
      {field: "id", name: t("executionID"), width: "200px"},
      {field: "work_item_id", name: t("executionWorkItem"), width: "200px"},
      {field: "state", name: t("fleetState"), width: "160px", format: (value) => t(`executionState_${value}`)},
      {field: "result_state", name: t("executionResult"), width: "160px", format: (value) => t(`executionResult_${value}`)},
      {field: "machine_id", name: t("fleetMachineID"), width: "200px"},
      {field: "plan", name: t("fleetRuntime"), width: "200px", format: (_, row) => `${row.plan.runtime} · ${row.plan.model}`},
    ]} actions={[{name: t("fleetDetails"), icon: Eye, onclick: (row) => {if (row.id) navigate(`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(projectID)}/executions/${encodeURIComponent(row.id)}`)}}]} footer={<div className="flex w-full justify-end gap-2">
      <Button variant="outline" disabled={loading || cursors.length === 1} onClick={() => setCursors((old) => old.slice(0, -1))}>{t("fleetPrevious")}</Button>
      <Button variant="outline" disabled={loading || !page.has_more || !page.next_cursor} onClick={() => setCursors((old) => [...old, page.next_cursor!])}>{t("fleetNext")}</Button>
    </div>} />
  </div>
}
