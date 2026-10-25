import { useCallback, useEffect, useMemo, useRef, useState } from "react"
import { useNavigate, useParams, useSearchParams } from "react-router"
import { Eye } from "lucide-react"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import type { components } from "../../lib/api-schema"

import {Enrollment} from "./enrollment"
import { Gateway } from "./gateway"
import {MachineFilters} from "./filters"
import {machineListFilters} from "./filters-state"

export default function MachinesPage() {
  const { tenantID = "" } = useParams()
  const [params] = useSearchParams()
  return <Machines key={`${tenantID}:${JSON.stringify(machineListFilters(params))}`} tenantID={tenantID} />
}
function Machines({tenantID}: {tenantID: string}) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const filters = useMemo(() => machineListFilters(params), [params])
  const [page, setPage] = useState<components["schemas"]["MachinePage"]>({items: [], has_more: false})
  const [cursors, setCursors] = useState<string[]>([""])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const pending = useRef<AbortController | null>(null)
  const cursor = cursors[cursors.length - 1] ?? ""
  const load = useCallback(async () => {
    pending.current?.abort()
    const controller = new AbortController()
    pending.current = controller
    setLoading(true); setError("")
    try {
      const result = await iamApi.machines(tenantID, cursor || undefined, controller.signal, filters)
      if (!controller.signal.aborted) setPage(result)
    } catch (cause) {
      if (!controller.signal.aborted) {setPage({items: [], has_more: false}); setError(cause instanceof Error ? cause.message : t("fleetUnavailable"))}
    } finally {if (!controller.signal.aborted) setLoading(false)}
  }, [tenantID, cursor, filters, t])
  useEffect(() => {let active = true; queueMicrotask(() => {if (active) void load()}); return () => {active = false; pending.current?.abort()}}, [load])
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
    <PageHeader title={t("machines")} description={t("fleetDescription")} />
    {error && <Alert>{error}</Alert>}
    <MachineFilters/>
    {allowed("machine_manage") && <Enrollment key={params.get("user_code") ?? ""} tenantID={tenantID} initialCode={params.get("user_code") ?? ""} onChanged={load}/> }
    {allowed("gateway_manage") && <details className="shrink-0 max-h-[70vh] overflow-auto rounded-lg border p-3"><summary className="min-h-11 cursor-pointer font-medium">{t("fleetGatewayTitle")}</summary><Gateway tenantID={tenantID} /></details>}
    <AutoTable fill data={loading ? [] : page.items ?? []} loading={loading} rowKey="id" pagination={false} onRefresh={load} exportable={false} emptyContent={error ? t("fleetRetry") : t("fleetEmpty")} columns={[
      {field: "name", name: t("name"), width: "220px"},
      {field: "state", name: t("fleetState"), width: "160px", format: (value) => t(`fleetState_${value}`)},
      {field: "os", name: t("fleetOS"), width: "130px"},
      {field: "slot_count", name: t("fleetSlots"), width: "130px"},
      {field: "remote_access", name: t("fleetRemote"), width: "160px", format: (value) => value === "AVAILABLE" ? t("fleetRemoteAvailable") : t("fleetUnavailableLabel")},
    ]} actions={[{name: t("fleetDetails"), icon: Eye, onclick: (row) => {if (row.id) navigate(`/admin/${encodeURIComponent(tenantID)}/machines/${encodeURIComponent(row.id)}`)}}]} footer={<div className="flex w-full items-center justify-end gap-2">
      <Button variant="outline" disabled={loading || cursors.length === 1} onClick={() => setCursors((old) => old.slice(0, -1))}>{t("fleetPrevious")}</Button>
      <Button variant="outline" disabled={loading || !page.has_more || !page.next_cursor} onClick={() => setCursors((old) => [...old, page.next_cursor!])}>{t("fleetNext")}</Button>
    </div>} />
  </div>
}
