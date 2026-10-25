import {useEffect, useState, type ReactNode} from "react"
import {Link} from "react-router"
import {useLocale, useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {iamApi} from "../../lib/iam-api"
import type {components} from "../../lib/api-schema"
import {AutoTable} from "@workspace/ui/components/auto-table"
import {homeMessages, homeMetricValue} from "./messages"

type Summary = components["schemas"]["WorkbenchSummary"]

export function WorkbenchSummary({tenantID, decisions}: {tenantID: string; decisions?: ReactNode}) {
  const t = useTranslations("iam")
  const locale = useLocale()
  const labels = homeMessages[locale as keyof typeof homeMessages] ?? homeMessages["en-US"]
  const label = (key: string) => labels[key as keyof typeof labels] ?? labels.unavailable
  const [data, setData] = useState<Summary | null>(null)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.workbenchSummary(tenantID, {}, controller.signal).then(value => {
      if (!controller.signal.aborted) {setData(value); setError("")}
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause))
    })
    return () => controller.abort()
  }, [tenantID, revision])
  const root = `/admin/${encodeURIComponent(tenantID)}`
  const loads = data?.load ?? []
  const activity = data?.activity ?? []
  const workHref = (project: string, work: string) => `${root}/my-work?${new URLSearchParams({project, work})}`
  const activityHref = (value: NonNullable<Summary["activity"]>[number]) => {
    if (!value.object_id) return root
    if (value.kind === "CLIENT_RECEIPT") return `${root}/entities/${value.project_id}/deliveries?id=${value.object_id}`
    if (value.kind !== "REPORT_CONFIRMED") return value.project_id ? workHref(value.project_id, value.object_id) : root
    return `${root}${value.project_id && value.project_id !== "0" ? `/entities/${encodeURIComponent(value.project_id)}` : ""}/reports?${new URLSearchParams({record_id: value.object_id, ...(value.principal_id && value.principal_id !== "0" ? {principal_id: value.principal_id} : {scope: value.project_id && value.project_id !== "0" ? "PROJECT" : "TEAM"})})}`
  }
  return <section className="min-w-0 space-y-4" aria-label={t("workbenchSummaryTitle")}>
    <div className="flex flex-wrap items-center justify-between gap-3"><h2 className="font-semibold">{t("workbenchSummaryTitle")}</h2><button type="button" className="min-h-11 rounded-md border px-3 text-sm" onClick={() => {setData(null); setError(""); setRevision(value => value + 1)}}>{t("refresh")}</button></div>
    {error && <Alert>{error}</Alert>}
    {!data && !error && <p role="status">{t("loading")}</p>}
    {data && <>
      <p className="text-sm text-muted-foreground">{t("workbenchSummaryScope")} {data.from.slice(0, 10)} ≤ UTC &lt; {data.to.slice(0, 10)}</p>
      <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-5">
        {(data.kpis ?? []).map(metric => <div key={metric.key} className="min-w-0 rounded-md border p-4"><dt className="text-sm text-muted-foreground"><Link className="underline-offset-4 hover:underline" to={`${root}/metrics`}>{label(metric.key)}</Link></dt><dd className="mt-2 break-words text-2xl font-semibold tabular-nums">{homeMetricValue(metric.key, metric.key === "REWORK_P50_HOURS" ? metric.mean_hours : metric.value, labels.unavailable)}</dd>{metric.key === "REWORK_P50_HOURS" && metric.mean_hours && <p className="mt-2 text-xs text-muted-foreground">{labels.mean} · P50 {metric.value === ">336" ? ">336" : `≤${metric.value}`} h · P90 {metric.p90_hours === ">336" ? ">336" : `≤${metric.p90_hours}`} h · {metric.denominator} {labels.cycles}</p>}{metric.unavailable_reason ? <p className="mt-2 text-xs text-muted-foreground">{label(metric.unavailable_reason)}</p> : metric.key !== "REWORK_P50_HOURS" && <p className="mt-2 break-words text-xs text-muted-foreground">{labels.facts}: {metric.numerator} / {metric.denominator}</p>}{metric.key === "EVIDENCE_COVERAGE" && <p className="mt-2 text-xs text-muted-foreground">{labels.target}</p>}</div>)}
      </dl>
      <div className="grid gap-4 lg:grid-cols-12">
        {decisions && <div className="min-w-0 lg:col-span-8">{decisions}</div>}
        <section className="min-w-0 rounded-md border p-4 lg:col-span-4" aria-label={t("workbenchSummaryLoad")}><h3 className="font-medium">{t("workbenchSummaryLoad")}</h3><p className="my-2 text-sm text-muted-foreground">{t("workbenchSummaryLoadHint")}</p>
          <ul className="divide-y">{loads.map(value => <li key={value.principal_id} className="space-y-2 py-3"><div className="flex flex-wrap justify-between gap-2"><span className="break-all">{value.display_name || value.principal_id}</span><span className="text-sm tabular-nums">{labels.capacity}: {value.active ?? labels.unavailable} / {value.capacity ?? labels.unavailable}</span></div>{value.active != null && value.capacity != null && value.capacity > 0 ? <progress className="h-2 w-full accent-primary" aria-label={`${value.display_name || value.principal_id}: ${labels.capacity}`} max={value.capacity} value={Math.min(value.active, value.capacity)}/> : <p className="text-xs text-muted-foreground">{label(value.capacity_unavailable ?? "CAPACITY_NOT_CONFIGURED_OR_NOT_VISIBLE")}</p>}<p className="text-xs text-muted-foreground">{t("workbenchSummaryOpen")}: {value.open} · {t("workbenchSummaryBlocked")}: {value.blocked} · {t("workbenchSummaryReview")}: {value.review}</p></li>)}</ul>{!loads.length && <p className="text-sm text-muted-foreground">{t("workbenchEmpty")}</p>}
        </section>
      <section className="min-w-0 space-y-3 rounded-md border p-4 lg:col-span-8" aria-label={labels.health}><h3 className="font-medium">{labels.health}</h3><AutoTable data={data.project_health ?? []} rowKey="project_id" pagination={false} exportable={false} columns={[
        {field: "code", name: t("projects"), format: (_, project) => <Link className="underline" to={`${root}/entities/${project.project_id}/project`}>{project.code || project.project_id}</Link>},
        {field: "stage", name: t("projectStage"), format: value => t.has(`project_stage_${value}`) ? t(`project_stage_${value}`) : String(value)},
        {field: "mode", name: labels.mode},
        {field: "accepted", name: labels.accepted, format: (_, project) => `${project.accepted} / ${project.total}`},
        {field: "budget_spent_micros", name: labels.budget, format: (_, project) => {
          const spent = project.budget_spent_micros
          const cap = project.budget_cap_micros
          const percent = spent && cap && /^[0-9]+$/.test(spent) && /^[1-9][0-9]*$/.test(cap) ? Number(BigInt(spent) * 100n / BigInt(cap) > 100n ? 100n : BigInt(spent) * 100n / BigInt(cap)) : null
          return <div className="min-w-36 space-y-1">{percent !== null && <progress className="h-2 w-full accent-primary" aria-label={labels.budget} max={100} value={percent}/>}<span className="text-xs tabular-nums">{homeMetricValue("COST_PER_ACCEPTED", spent, labels.unavailable)} / {homeMetricValue("COST_PER_ACCEPTED", cap, labels.unavailable)}</span></div>
        }},
        {field: "health", name: labels.risk, format: value => t.has(`project_health_${value}`) ? t(`project_health_${value}`) : String(value)},
      ]}/></section>
      <section className="min-w-0 space-y-3 rounded-md border p-4 lg:col-span-4" aria-label={t("workbenchMachineLoad")}>
        <h3 className="font-medium">{t("workbenchMachineLoad")}</h3>
        <p className="text-sm text-muted-foreground">{t("workbenchMachineLoadHint")}</p>
        <AutoTable data={data.machines ?? []} rowKey="id" pagination={false} exportable={false} columns={[
          {field: "name", name: t("workbenchMachineName"), format: (_, machine) => machine.id ? <Link className="break-words underline underline-offset-4" to={`${root}/machines/${encodeURIComponent(machine.id)}`}>{machine.name}</Link> : machine.name},
          {field: "state", name: t("status"), format: value => t(`workbenchMachineState_${value}`)},
          {field: "reserved", name: t("workbenchMachineReserved")},
          {field: "slots", name: t("workbenchMachineSlots")},
        ]}/>
      </section>
        <section className="min-w-0 rounded-md border p-4 lg:col-span-12" aria-label={t("workbenchSummaryActivity")}><h3 className="font-medium">{t("workbenchSummaryActivity")}</h3><ul className="divide-y">{activity.map(value => <li key={`${value.kind}:${value.object_id}:${value.occurred_at}`} className="py-3"><p className="text-sm text-muted-foreground">{label(value.kind)}</p><Link className="break-words underline underline-offset-4" to={activityHref(value)}>{value.title}</Link><time className="mt-1 block text-sm text-muted-foreground" dateTime={value.occurred_at}>{value.occurred_at.replace("T", " ")}</time></li>)}</ul>{!activity.length && <p className="mt-2 text-sm text-muted-foreground">{t("workbenchEmpty")}</p>}</section>
      </div>

    </>}
  </section>
}
