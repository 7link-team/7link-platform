import { useEffect, useState } from "react"
import { useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { iamApi, type WorkItem, type WorkItemDetail } from "../../lib/iam-api"
import { WorkResults } from "../work-items/results"

// These are projections over the existing work aggregate. Result actions reuse
// the canonical controls and commands; no second review or delivery state exists.
export function ProjectWorkView({ project, view }: { project: string; view: "review" | "defects" | "deliveries" }) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const [page, setPage] = useState<{ items: WorkItem[]; total: number }>({ items: [], total: 0 })
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const [detail, setDetail] = useState<WorkItemDetail | null>(null)
  const rawOffset = Number(params.get("offset") ?? 0)
  const offset = Number.isSafeInteger(rawOffset) && rawOffset >= 0 ? rawOffset : 0
  const type = params.get("type") === "INCIDENT" ? "INCIDENT" : "BUG"
  const selected = params.get("work") ?? ""
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.workItems(project, {
      offset, limit: 20, sort: "-number",
      ...(view === "defects" ? { type: `eq:${type}` } : { state: view === "review" ? "eq:NEEDS_HUMAN_REVIEW" : "eq:ACCEPTED" }),
    }, controller.signal)
      .then((next) => { if (!controller.signal.aborted) { setPage(next); setError("") } })
      .catch((cause: unknown) => { if (!controller.signal.aborted) { setPage({ items: [], total: 0 }); setError(cause instanceof Error ? cause.message : "workUnavailable") } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [project, view, type, offset, revision])
  useEffect(() => {
    const controller = new AbortController()
    if (selected) void iamApi.workItem(project, selected, controller.signal)
      .then((next) => { if (!controller.signal.aborted) setDetail(next) })
      .catch((cause: unknown) => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : "workUnavailable") })
    return () => controller.abort()
  }, [project, selected, revision])
  const current = detail?.id === selected && detail.project_id === project ? detail : null
  const changeParams = (patch: Record<string, string>) => {
    setLoading(true)
    setParams((old) => { const next = new URLSearchParams(old); for (const [key, value] of Object.entries(patch)) { if (value) next.set(key, value); else next.delete(key) }; return next })
  }
  return <div className="min-h-0 flex-1 space-y-3 overflow-auto">
    {error && <Alert>{t.has(error) ? t(error) : error}</Alert>}
    {view === "defects" && <div className="flex flex-wrap gap-2" role="group" aria-label={t("workType")}>
      {(["BUG", "INCIDENT"] as const).map((value) => <Button key={value} disabled={type === value} variant={type === value ? "default" : "outline"} onClick={() => changeParams({ type: value, offset: "", work: "" })}>{t(`workType_${value}`)}</Button>)}
    </div>}
    <AutoTable data={loading ? [] : page.items} rowKey="id" loading={loading} pagination={false} exportable={false}
      emptyContent={error ? t("workUnavailable") : t("workEmpty")}
      onRefresh={() => { setLoading(true); setRevision((v) => v + 1) }}
      columns={[
        { field: "number", name: t("workNumber") },
        { field: "title", name: t("workTitle"), render: (_, row) => <button type="button" className="text-left underline underline-offset-4" onClick={() => { setDetail(null); setParams((old) => { const next = new URLSearchParams(old); next.set("work", row.id!); return next }) }}>{row.title}</button> },
        { field: "state", name: t("status"), format: (v) => t.has(`workState_${v}`) ? t(`workState_${v}`) : String(v) },
        ...(view === "defects" ? [{ field: "severity", name: t("workSeverity") }, { field: "defect_stage", name: t("projectSurfaceDefects") }] : []),
      ]}
      footer={<div className="flex w-full flex-wrap items-center justify-between gap-2">
        <span>{t("projectPageSummary", { start: page.items.length && !loading ? offset + 1 : 0, end: loading ? 0 : offset + page.items.length, total: page.total })}</span>
        <div className="flex gap-2">
          <Button variant="outline" disabled={loading || offset === 0} onClick={() => changeParams({ offset: String(Math.max(0, offset - 20)) })}>{t("fleetPrevious")}</Button>
          <Button variant="outline" disabled={loading || offset + page.items.length >= page.total} onClick={() => changeParams({ offset: String(offset + 20) })}>{t("fleetNext")}</Button>
        </div>
      </div>} />
    {selected && <section className="space-y-3 rounded-md border p-4" aria-busy={!current && !error}>
      <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="font-medium">{current?.title ?? t("loading")}</h2><Button variant="outline" onClick={() => setParams((old) => { const next = new URLSearchParams(old); next.delete("work"); return next })}>{t("personalWorkClose")}</Button></div>
      {current && <><p className="whitespace-pre-wrap">{current.description}</p><WorkResults key={current.id} entityID={project} detail={current} onUpdated={(next) => { setDetail(next); setRevision((v) => v + 1) }} /></>}
    </section>}
  </div>
}
