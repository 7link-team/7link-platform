import { useCallback, useMemo, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { useFacts } from "../runtime/use-facts"
import {MachineFilters} from "../machines/filters"
import {machineListFilters} from "../machines/filters-state"

export default function RunnersPage() {
  const {tenantID = ""} = useParams()
  const [params] = useSearchParams()
  return <Runners key={`${tenantID}:${JSON.stringify(machineListFilters(params))}`} tenantID={tenantID}/>
}
function Runners({tenantID}: {tenantID: string}) {
  const t = useTranslations("iam")
  const [cursor, setCursor] = useState<string[]>([""])
  const [params] = useSearchParams()
  const filters = useMemo(() => machineListFilters(params), [params])
  const [machine, setMachine] = useState("")
  const current = cursor[cursor.length - 1]
  const load = useCallback((signal: AbortSignal) => iamApi.machines(tenantID, current || undefined, signal, filters), [tenantID, current, filters])
  const {data, error, loading, refresh} = useFacts(`${tenantID}:${current}:${JSON.stringify(filters)}`, load)
  const rows = data?.items ?? []
  return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto">
    <PageHeader title={t("runtimeRunners")} description={t("runtimeRunnersHint")} action={<Button variant="outline" disabled={loading} onClick={refresh}>{t("refresh")}</Button>}/>
    {error && <Alert>{error}</Alert>}
    <MachineFilters/>
    <AutoTable data={rows} loading={loading} rowKey="id" pagination={false} exportable={false} emptyContent={t("fleetEmpty")} columns={[
      {field: "name", name: t("name"), format: (_, row) => <button type="button" className="min-h-11 text-left underline" onClick={() => setMachine(row.id ?? "")}>{row.name}</button>},
      {field: "state", name: t("fleetState"), format: value => t(`fleetState_${value}`)},
      {field: "os", name: t("fleetOS")}, {field: "slot_count", name: t("fleetSlots")},
    ]}/>
    <div className="flex flex-wrap justify-end gap-2"><Button variant="outline" disabled={loading || cursor.length === 1} onClick={() => {setMachine(""); setCursor(old => old.slice(0, -1))}}>{t("fleetPrevious")}</Button><Button variant="outline" disabled={loading || !data?.has_more || !data.next_cursor} onClick={() => {setMachine(""); setCursor(old => [...old, data!.next_cursor!])}}>{t("fleetNext")}</Button></div>
    {machine && <RunnerFacts key={machine} tenantID={tenantID} machineID={machine}/>}
    <section className="rounded-lg border border-dashed p-4"><h2 className="font-medium">{t("fleetUnavailableLabel")} · v1.1</h2><p className="mt-2 text-sm text-muted-foreground">{t("runtimeRunnerUnavailable")}</p></section>
  </div>
}
function RunnerFacts({tenantID, machineID}: {tenantID: string; machineID: string}) {
  const t = useTranslations("iam")
  const load = useCallback((signal: AbortSignal) => iamApi.machineCapabilities(machineID, signal), [machineID])
  const {data, error, loading, refresh} = useFacts(machineID, load)
  return <section className="min-w-0 space-y-3 rounded-lg border p-4">
    <div className="flex flex-wrap justify-between gap-2"><Link className="break-all underline" to={`/admin/${encodeURIComponent(tenantID)}/machines/${encodeURIComponent(machineID)}`}>{t("fleetDetails")} · {machineID}</Link><Button variant="outline" disabled={loading} onClick={refresh}>{t("refresh")}</Button></div>
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    {data && <Tabs defaultValue="slots"><TabsList className="h-auto flex-wrap" aria-label={t("runtimeRunners")}>{["slots", "runtimes", "workdirs"].map(tab => <TabsTrigger className="min-h-11" key={tab} value={tab}>{t(`fleetTab_${tab}`)}</TabsTrigger>)}</TabsList>
      <TabsContent value="slots"><p className="my-3 text-sm text-muted-foreground">{t("runtimeSlotsHint")}</p><AutoTable data={data.active_slots ?? []} rowKey="slot_no" pagination={false} exportable={false} emptyContent={t("fleetNoSlots")} columns={[{field: "slot_no", name: t("fleetSlots")}, {field: "execution_id", name: t("executionID")}]} /></TabsContent>
      <TabsContent value="runtimes"><AutoTable data={data.runtimes ?? []} rowKey="kind" pagination={false} exportable={false} emptyContent={t("fleetNoObservations")} columns={[{field: "kind", name: t("fleetRuntime")}, {field: "version", name: t("fleetVersion")}, {field: "inspected_at", name: t("fleetDiscoveryCheckedAt")}]} /></TabsContent>
      <TabsContent value="workdirs"><AutoTable data={data.workdirs ?? []} rowKey="id" pagination={false} exportable={false} emptyContent={t("fleetNoWorkdirs")} columns={[{field: "project_id", name: t("fleetProject")}, {field: "path", name: t("fleetPath")}]} /></TabsContent>
    </Tabs>}
  </section>
}
