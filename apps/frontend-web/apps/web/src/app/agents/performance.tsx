import { useEffect, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { performanceApi, performanceTypes, type AgentPerformance, type PerformanceMeasures } from "./performance-api"

export function PerformanceFacts({ value }: { value: AgentPerformance }) {
  const t = useTranslations("iam")
  const rate = (value: number | null) => value === null ? "—" : `${(value * 100).toFixed(1)}%`
  const fpa = (v: PerformanceMeasures) => v.fpa === null ? t("agentStatsSmallSample", { n: v.fpa_n }) : rate(v.fpa)
  const cost = (v: number | null) => v === null ? "—" : String(v)
  return <div className="space-y-4">
    <p className="text-sm text-muted-foreground">{t("agentStatsScope")}</p>
    <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
      {[[t("agentStatsFPA"), fpa(value)], [t("agentStatsCPA"), cost(value.cpa_micros)], [t("agentStatsRework"), rate(value.rework_rate)], [t("agentStatsLoad"), `${value.load.active_items}/${value.load.max_parallel_items}`]].map(([label, fact]) => <div key={label} className="border p-4"><dt className="text-sm text-muted-foreground">{label}</dt><dd className="mt-2 text-xl tabular-nums">{fact}</dd></div>)}
    </dl>
    <p>{t("agentStatsRunning", { n: value.load.running_executions })}</p>
    <p className="text-sm">{t("agentStatsDenominators", { claims: value.claims, rework: value.rework_decisions, accepted: value.accepted_items })}</p>
    <p className="text-sm text-muted-foreground">{t("agentStatsCostHint")}</p>
    <div className="overflow-x-auto"><table className="w-full text-left text-sm tabular-nums">
      <caption className="pb-3 text-left font-medium">{t("agentStatsByType")}</caption>
      <thead><tr>{["agentStatsType", "agentStatsFPA", "agentStatsSamples", "agentStatsCPA", "agentStatsRework", "agentStatsClaims", "agentStatsAccepted"].map(key => <th key={key} scope="col" className="p-3 font-medium">{t(key)}</th>)}</tr></thead>
      <tbody>{value.by_type.map(row => <tr key={row.work_item_type} className="border-t"><th scope="row" className="p-3 font-medium">{row.work_item_type}</th><td className="p-3">{fpa(row)}</td><td className="p-3">{row.fpa_n}</td><td className="p-3">{cost(row.cpa_micros)}</td><td className="p-3">{rate(row.rework_rate)}</td><td className="p-3">{row.claims}</td><td className="p-3">{row.accepted_items}</td></tr>)}</tbody>
    </table></div>
    <p className="text-sm text-muted-foreground">{t("agentStatsComputed")} <time dateTime={value.computed_at}>{value.computed_at}</time></p>
  </div>
}

export function AgentPerformancePanel({ tenant, agent }: { tenant: string; agent: string }) {
  const t = useTranslations("iam")
  const [search, setSearch] = useSearchParams()
  const days = search.get("stats_days") === "90" ? 90 : 30
  const rawKind = search.get("stats_type") ?? ""
  const kind = performanceTypes.find(value => value === rawKind) ?? ""
  const [revision, setRevision] = useState(0)
  const requestKey = JSON.stringify([tenant, agent, days, kind, revision])
  const [result, setResult] = useState<{ key: string; value: AgentPerformance | null; error: string } | null>(null)
  const value = result?.key === requestKey ? result.value : null
  const error = result?.key === requestKey ? result.error : ""
  useEffect(() => {
    const controller = new AbortController()
    let pending = false
    const load = async () => {
      if (pending) return
      pending = true
      try {
        const value = await performanceApi.read(tenant, agent, days, kind, controller.signal)
        if (!controller.signal.aborted) setResult({ key: requestKey, value, error: "" })
      } catch (cause) {
        if (!controller.signal.aborted) setResult({ key: requestKey, value: null, error: cause instanceof Error ? cause.message : t("agentBrowseFailed") })
      } finally { pending = false }
    }
    void load()
    const interval = setInterval(() => { void load() }, 60_000)
    return () => { controller.abort(); clearInterval(interval) }
  }, [tenant, agent, days, kind, revision, requestKey, t])
  const filter = (key: string, value: string) => setSearch(previous => { const next = new URLSearchParams(previous); if (value) next.set(key, value); else next.delete(key); return next })
  return <section className="space-y-4" aria-label={t("agentTab_performance")}>
    <h2 className="text-lg font-semibold">{t("agentTab_performance")}</h2>
    <div className="flex flex-wrap items-end gap-3">
      <label className="grid gap-2 text-sm">{t("agentStatsWindow")}<select className="min-h-11 rounded-md border bg-background px-3" value={days} onChange={event => filter("stats_days", event.target.value)}>{[30, 90].map(value => <option key={value} value={value}>{t("agentStatsDays", { n: value })}</option>)}</select></label>
      <label className="grid gap-2 text-sm">{t("agentStatsType")}<select className="min-h-11 rounded-md border bg-background px-3" value={kind} onChange={event => filter("stats_type", event.target.value)}><option value="">{t("agentStatsAllTypes")}</option>{performanceTypes.map(value => <option key={value} value={value}>{value}</option>)}</select></label>
      <Button variant="outline" onClick={() => setRevision(value => value + 1)}>{t("refresh")}</Button>
    </div>
    {error && <Alert>{error}</Alert>}
    {value ? <PerformanceFacts value={value}/> : !error && <p role="status">{t("loading")}</p>}
  </section>
}
