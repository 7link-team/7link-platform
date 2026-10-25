import { useCallback, useState } from "react"
import { Link, useParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@workspace/ui/components/tabs"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi } from "../../lib/iam-api"
import { metricPeriod } from "../runtime/facts"
import { useFacts } from "../runtime/use-facts"
import { historicalRate, metricsMessages } from "./messages"
import { ProjectMetricsPanel } from "./project-metrics"

export default function MetricsPage() {
  const {tenantID = ""} = useParams()
  return <Metrics key={tenantID} tenantID={tenantID}/>
}
function Metrics({tenantID}: {tenantID: string}) {
  const t = useTranslations("iam")
  const [dates, setDates] = useState({from: "", to: ""})
  const [period, setPeriod] = useState<{from?: string; to?: string}>({})
  const [invalid, setInvalid] = useState(false)
  const load = useCallback((signal: AbortSignal) => iamApi.workbenchSummary(tenantID, period, signal), [tenantID, period])
  const {data, error, loading, refresh} = useFacts(`${tenantID}:${period.from ?? ""}:${period.to ?? ""}`, load)
  return <div className="flex min-w-0 flex-1 flex-col gap-4 overflow-auto">
    <PageHeader title={t("runtimeMetrics")} description={t("runtimeMetricsHint")} action={<Button variant="outline" disabled={loading} onClick={refresh}>{t("refresh")}</Button>}/>
    <form className="flex flex-wrap items-end gap-3" onSubmit={event => {event.preventDefault(); const query = metricPeriod(dates.from, dates.to); setInvalid(!query); if (query) setPeriod(query)}}>
      {(["from", "to"] as const).map(name => <label key={name} className="flex min-w-0 flex-col gap-1 text-sm">{t(name === "from" ? "runtimeFrom" : "runtimeTo")}<input className="min-h-11 min-w-0 rounded-md border bg-background px-3" type="date" required value={dates[name]} onChange={event => setDates(old => ({...old, [name]: event.target.value}))}/></label>)}
      <Button type="submit" disabled={loading}>{t("runtimeApply")}</Button>
    </form>
    {invalid && <Alert>{t("runtimeInvalidPeriod")}</Alert>}{error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    <ProjectMetricsPanel key={tenantID} tenantID={tenantID} period={period}/>
    {data && <>
      <p className="break-words text-sm text-muted-foreground">{t("workbenchSummaryScope")} {data.from} ≤ UTC &lt; {data.to}</p>
      <MetricsValues data={data}/>
      <HistoricalMetrics tenantID={tenantID} period={period}/>
      {!!data.missing_sources?.length && <Alert>{t("workbenchSummaryMissing")} {data.missing_sources.join(", ")}</Alert>}
      <Tabs defaultValue="work"><TabsList className="h-auto max-w-full flex-wrap" aria-label={t("runtimeMetrics")}>{[["work", "workbenchSummaryLoad"], ["machines", "workbenchMachineLoad"], ["activity", "workbenchSummaryActivity"]].map(([tab, label]) => <TabsTrigger className="min-h-11" key={tab} value={tab!}>{t(label!)}</TabsTrigger>)}</TabsList>
        <TabsContent value="work"><p className="my-3 text-sm text-muted-foreground">{t("workbenchSummaryLoadHint")}</p><AutoTable data={data.load ?? []} rowKey="principal_id" pagination={false} exportable={false} emptyContent={t("workbenchEmpty")} columns={[{field: "principal_id", name: t("runtimePrincipal")}, {field: "open", name: t("workbenchSummaryOpen")}, {field: "review", name: t("workbenchSummaryReview")}, {field: "blocked", name: t("workbenchSummaryBlocked")}]} /></TabsContent>
        <TabsContent value="machines"><p className="my-3 text-sm text-muted-foreground">{t("workbenchMachineLoadHint")}</p><AutoTable data={data.machines ?? []} rowKey="id" pagination={false} exportable={false} emptyContent={t("workbenchEmpty")} columns={[{field: "name", name: t("name"), format: (_, row) => row.id ? <Link className="underline" to={`/admin/${encodeURIComponent(tenantID)}/machines/${encodeURIComponent(row.id)}`}>{row.name}</Link> : row.name}, {field: "state", name: t("fleetState"), format: value => t(`fleetState_${value}`)}, {field: "reserved", name: t("workbenchMachineReserved")}, {field: "slots", name: t("workbenchMachineSlots")}]} /></TabsContent>
        <TabsContent value="activity"><AutoTable data={data.acceptances ?? []} rowKey="work_item_id" pagination={false} exportable={false} emptyContent={t("workbenchEmpty")} columns={[{field: "title", name: t("name"), format: (_, row) => row.project_id && row.work_item_id ? <Link className="underline" to={`/admin/${encodeURIComponent(tenantID)}/my-work?${new URLSearchParams({project: row.project_id, work: row.work_item_id})}`}>{row.title}</Link> : row.title}, {field: "accepted_at", name: t("runtimeAcceptedAt")}]} /></TabsContent>
      </Tabs>
      <p className="text-sm text-muted-foreground">{t("runtimeMetricsUnavailable")}</p>
    </>}
  </div>
}
function HistoricalMetrics({tenantID, period}: {tenantID: string; period: {from?: string; to?: string}}) {
  const locale = useLocale()
  const l = metricsMessages[locale as keyof typeof metricsMessages] ?? metricsMessages["en-US"]
  const t = useTranslations("iam")
  const scope = `${tenantID}:${period.from ?? ""}:${period.to ?? ""}`
  const loadReports = useCallback((signal: AbortSignal) => iamApi.reportTimeliness(tenantID, period, signal), [tenantID, period])
  const loadQuality = useCallback((signal: AbortSignal) => iamApi.deliveryQuality(tenantID, period, signal), [tenantID, period])
  const reports = useFacts(scope, loadReports)
  const quality = useFacts(scope, loadQuality)
  const unknown = t("workbenchSummaryUnknown")
  return <section className="flex min-w-0 flex-col gap-3" aria-label={l.history}>
    <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-lg font-semibold">{l.history}</h2><Button variant="outline" disabled={reports.loading || quality.loading} onClick={() => {reports.refresh(); quality.refresh()}}>{t("refresh")}</Button></div>
    <p className="text-sm text-muted-foreground">{l.hint}</p>
    {(reports.loading || quality.loading) && <p role="status">{t("loading")}</p>}
    {reports.error && <Alert>{reports.error}</Alert>}
    {quality.error && <Alert>{quality.error}</Alert>}
    {reports.data && <><p className="text-sm">{l.missing} {reports.data.missing_deadlines}</p><AutoTable data={reports.data.days ?? []} rowKey="date" pagination={false} exportable={false} emptyContent={unknown} columns={[
      {field: "date", name: l.date}, {field: "due", name: l.due}, {field: "on_time", name: l.onTime}, {field: "late", name: l.late}, {field: "unavailable", name: l.pending}, {field: "on_time_rate", name: l.timeliness, format: (_, row) => historicalRate(row.on_time_rate, unknown)},
    ]}/></>}
    {quality.data && <AutoTable data={quality.data.days ?? []} rowKey="date" pagination={false} exportable={false} emptyContent={unknown} columns={[
      {field: "date", name: l.date}, {field: "received", name: l.received}, {field: "returned", name: l.returned}, {field: "acceptance_rate", name: l.acceptance, format: (_, row) => historicalRate(row.acceptance_rate, unknown)}, {field: "verification_passed", name: l.passed}, {field: "verification_failed", name: l.failed}, {field: "verification_pass_rate", name: l.verification, format: (_, row) => historicalRate(row.verification_pass_rate, unknown)},
    ]}/>}
  </section>
}
export function MetricsValues({data}: {data: Awaited<ReturnType<typeof iamApi.workbenchSummary>>}) {
  const t = useTranslations("iam")
  return <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">{([
    ["workbenchSummaryProjects", data.projects], ["workbenchSummaryOpen", data.open], ["workbenchSummaryReview", data.review], ["workbenchSummaryBlocked", data.blocked], ["workbenchSummaryAccepted", data.accepted], ["workbenchSummaryExecutions", data.execution_created], ["workbenchSummaryCost", data.execution_cost_micros],
  ] as const).map(([label, value]) => <div key={label} className="min-w-0 rounded-lg border p-4"><dt className="text-sm text-muted-foreground">{t(label)}</dt><dd className="mt-2 break-all text-2xl font-semibold tabular-nums">{value == null ? t("workbenchSummaryUnknown") : String(value)}</dd></div>)}</dl>
}
