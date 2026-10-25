import { useEffect, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { useAuth } from "../../components/auth"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { iamApi, type WorkItemDetail } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { WorkAssignmentControls } from "../work-items/assignment"
import { WorkResults } from "../work-items/results"

export default function MyWorkPage() {
  const { tenantID = "", entityID = "" } = useParams()
  const { session } = useAuth()
  return <PersonalWorkPage key={`${tenantID}:${entityID}:${session?.subject}`} tenant={tenantID} project={entityID} actor={session?.subject ?? ""} />
}

function PersonalWorkPage({ tenant, project, actor }: { tenant: string; project: string; actor: string }) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const [read, setRead] = useState<components["schemas"]["PersonalWorkPage"]>({ items: [], has_more: false })
  const [detail, setDetail] = useState<WorkItemDetail | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const selectedProject = project || params.get("project") || ""
  const selected = params.get("work") || ""
  const cursor = params.get("cursor") || ""
  const previous = params.getAll("previous")
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.personalWork(tenant, project, cursor, controller.signal)
      .then((next) => { if (!controller.signal.aborted) { setRead(next); setError("") } })
      .catch((cause: unknown) => { if (!controller.signal.aborted) { setRead({ items: [], has_more: false }); setError(cause instanceof Error ? cause.message : "personalWorkUnavailable") } })
      .finally(() => { if (!controller.signal.aborted) setLoading(false) })
    return () => controller.abort()
  }, [tenant, actor, project, cursor, revision])
  useEffect(() => {
    const controller = new AbortController()
    if (selected && selectedProject) {
      void iamApi.workItem(selectedProject, selected, controller.signal)
        .then((next) => { if (!controller.signal.aborted) setDetail(next) })
        .catch((cause: unknown) => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : "personalWorkUnavailable") })
    }
    return () => controller.abort()
  }, [selectedProject, selected, revision])
  const currentDetail = detail?.id === selected && detail.project_id === selectedProject ? detail : null
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
    <PageHeader title={t("personalWorkTitle")} description={t("personalWorkDescription")} />
    {error && <Alert>{t.has(error) ? t(error) : error}</Alert>}
    <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto">
      <AutoTable data={loading ? [] : read.items ?? []} loading={loading} rowKey="id" pagination={false} exportable={false}
        emptyContent={error ? t("personalWorkUnavailable") : t("workEmpty")}
        onRefresh={() => { setLoading(true); setRevision((v) => v + 1) }}
        columns={[
          { field: "project_id", name: t("projects") },
          { field: "number", name: t("workNumber") },
          { field: "title", name: t("workTitle"), render: (_, row) => <button type="button" className="text-left underline underline-offset-4" onClick={() => { setDetail(null); setParams((old) => { const next = new URLSearchParams(old); next.set("project", row.project_id!); next.set("work", row.id!); return next }) }}>{row.title}</button> },
          { field: "state", name: t("status"), format: (v) => t.has(`workState_${v}`) ? t(`workState_${v}`) : String(v) },
          { field: "responsibility", name: t("personalWorkResponsibility"), format: (v) => t(`personalWork_${v}`) },
        ]} footer={<div className="flex w-full justify-end gap-2">
          <Button variant="outline" disabled={loading || !cursor} onClick={() => { const next = new URLSearchParams(); const prior = previous.slice(0, -1); for (const value of prior) next.append("previous", value); const target = previous.at(-1); if (target) next.set("cursor", target); setLoading(true); setParams(next) }}>{t("fleetPrevious")}</Button>
          <Button variant="outline" disabled={loading || !read.has_more || !read.next_cursor} onClick={() => { const next = new URLSearchParams(); for (const value of previous) next.append("previous", value); next.append("previous", cursor); next.set("cursor", read.next_cursor!); setLoading(true); setParams(next) }}>{t("fleetNext")}</Button>
        </div>} />
      {selected && <section className="space-y-3 rounded-md border p-4" aria-busy={!currentDetail && !error}>
        <div className="flex flex-wrap items-center justify-between gap-2">
          <h2 className="font-medium">{currentDetail?.title ?? t("loading")}</h2>
          <Button variant="outline" onClick={() => setParams((old) => { const next = new URLSearchParams(old); next.delete("project"); next.delete("work"); return next })}>{t("personalWorkClose")}</Button>
        </div>
        {currentDetail && <>
          <AutoForm key={`${currentDetail.id}:${currentDetail.version}`} mode="detail" initialData={{ ...currentDetail, state: t.has(`workState_${currentDetail.state}`) ? t(`workState_${currentDetail.state}`) : currentDetail.state }} fields={{
            title: { name: t("workTitle"), type: "text" },
            objective: { name: t("workObjective"), type: "textarea" },
            description: { name: t("description"), type: "textarea" },
            state: { name: t("status"), type: "text" },
            accountable_principal_id: { name: t("personalWork_accountable"), type: "text" },
          }} />
          <WorkAssignmentControls key={`assignment:${currentDetail.id}`} entityID={selectedProject} detail={currentDetail} onUpdated={(next) => { setDetail(next); setRevision((v) => v + 1) }} />
          <WorkResults key={`results:${currentDetail.id}`} entityID={selectedProject} detail={currentDetail} onUpdated={(next) => { setDetail(next); setRevision((v) => v + 1) }} />
          <Link className="inline-flex underline underline-offset-4" to={`/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(selectedProject)}/work-items`}>{t("workItems")}</Link>
        </>}
      </section>}
    </div>
  </div>
}
