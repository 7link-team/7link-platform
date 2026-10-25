import { MySubmissions } from "./submissions"
import { IntakeTriageRequest } from "./intake-triage-request"
import { IntakeConversation, IntakeSuggestions } from "./intake-conversation"
import { SourceDisposition } from "./source-disposition"
import { useContext, useEffect, useState } from "react"
import { Link, useParams, useSearchParams } from "react-router"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { PageHeader } from "../../components/page-header"
import { AuthContext } from "../../components/auth"
import { useAllowed } from "../../lib/permissions"
import { intakeChangeApi, type IntakeLedgerSource, type IntakeLedgerPage } from "./intake-api"
import { ClarificationRequestForm, Clarifications } from "./clarifications"
import { IntakeDelegatedNew, IntakeNewProject, IntakeNewRequirement, IntakeReworkDisposition } from "./intake-conversion"
import { IntakeTriage } from "./intake-triage"

export default function IntakeWorkbench() {
  const { tenantID = "", entityID = "" } = useParams()
  const allowed = useAllowed()
  const auth = useContext(AuthContext)
  const t = useTranslations("iam")
  if (!allowed("scope_change") && (allowed("intake_convert") || allowed("intake_triage") || allowed("intake_submit"))) return <div className="space-y-4"><PageHeader title={t("intakeWorkbenchTitle")} />{allowed("intake_submit") && <MySubmissions key={`submissions:${tenantID}:${entityID}:${auth?.session?.subject??""}`} tenant={tenantID} project={entityID} />}{allowed("intake_triage") && <IntakeTriageRequest key={`triage:${tenantID}:${entityID}:${auth?.session?.subject??""}`} project={entityID} />}{allowed("intake_convert") && <IntakeDelegatedNew key={`${tenantID}:${entityID}:${auth?.session?.subject??""}`} project={entityID} base={`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(entityID)}`} />}</div>
  if (!allowed("scope_change")) return <div className="space-y-3"><PageHeader title={t("intakeWorkbenchTitle")} /><p role="status">{t("projectSurfaceAccessUnavailable")}</p></div>
  return <IntakeLedger key={`${tenantID}:${entityID}:${auth?.session?.subject ?? ""}`} tenant={tenantID} project={entityID} />
}

function IntakeLedger({ tenant, project }: { tenant: string; project: string }) {
  const t = useTranslations("iam")
  const allowed = useAllowed()
  const [params, setParams] = useSearchParams()
  const cursor = params.get("cursor") ?? ""
  const selected = params.get("intake") ?? ""
  const previous = params.getAll("previous")
  const [revision, setRevision] = useState(0)
  const [saved, setSaved] = useState(false)
  const [triageRequested, setTriageRequested] = useState(false)
  const [page, setPage] = useState<IntakeLedgerPage | null>(null)
  const [source, setSource] = useState<IntakeLedgerSource | null>(null)
  const [listError, setListError] = useState("")
  const [sourceError, setSourceError] = useState("")
  const [listLoaded, setListLoaded] = useState("")
  const [sourceLoaded, setSourceLoaded] = useState("")
  const listKey = `${project}:${cursor}:${revision}`
  const sourceKey = `${project}:${selected}:${revision}`
  useEffect(() => {
    const controller = new AbortController()
    void intakeChangeApi.list(project, cursor, controller.signal).then(next => {
      if (!controller.signal.aborted) { setPage(next); setListError(""); setListLoaded(listKey) }
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) { setPage(null); setListError(cause instanceof Error ? cause.message : t("clientSaveFailed")); setListLoaded(listKey) }
    })
    return () => controller.abort()
  }, [project, cursor, revision, listKey, t])
  useEffect(() => {
    if (!selected) return
    const controller = new AbortController()
    void intakeChangeApi.source(project, selected, controller.signal).then(next => {
      if (!controller.signal.aborted) { setSource(next); setSourceError(""); setSourceLoaded(sourceKey) }
    }).catch((cause: unknown) => {
      if (!controller.signal.aborted) { setSource(null); setSourceError(cause instanceof Error ? cause.message : t("clientSaveFailed")); setSourceLoaded(sourceKey) }
    })
    return () => controller.abort()
  }, [project, selected, revision, sourceKey, t])
  const loading = listLoaded !== listKey
  const currentPage = !loading ? page : null
  const current = sourceLoaded === sourceKey ? source : null
  const base = `/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}`
  const move = (target: string, history: string[]) => setParams(old => {
    const next = new URLSearchParams(old)
    next.delete("previous"); next.delete("cursor"); next.delete("intake")
    for (const value of history) next.append("previous", value)
    if (target) next.set("cursor", target)
    return next
  })
  return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 overflow-hidden">
    <PageHeader title={t("intakeWorkbenchTitle")} description={t("intakeWorkbenchDescription")} />
    {allowed("intake_submit") && <MySubmissions tenant={tenant} project={project} />}
    <p role="status" className="text-sm text-muted-foreground">{t("intakeWorkbenchUnavailable")}</p>
    {saved && <p role="status">{t("intakeTriageSaved")}</p>}
    {triageRequested && <p role="status">{t("intakeRequestQueued")}</p>}
    {!loading && listError && <Alert>{listError}</Alert>}
    <div className="min-h-0 min-w-0 flex-1 space-y-4 overflow-auto">
      <AutoTable data={currentPage?.items ?? []} loading={loading} rowKey="id" pagination={false} exportable={false}
        emptyContent={listError ? t("intakeWorkbenchReadFailed") : t("intakeWorkbenchEmpty")}
        onRefresh={() => setRevision(value => value + 1)}
        columns={[
          { field: "ref_no", name: t("workNumber") },
          { field: "title", name: t("workTitle"), render: (_, row) => <button type="button" className="text-left underline underline-offset-4" onClick={() => { if (row.id) setParams(old => { const next = new URLSearchParams(old); next.set("intake", row.id!); return next }) }}>{row.title || row.id}</button> },
          { field: "kind", name: t("intakeWorkbenchKind") },
          { field: "state", name: t("status") },
          { field: "card_revision", name: t("intakeCardRevision") },
          { field: "received_at", name: t("intakeWorkbenchReceived") },
        ]}
        footer={<div className="flex w-full justify-end gap-2">
          <Button variant="outline" disabled={loading || !cursor} onClick={() => move(previous.at(-1) ?? "", previous.slice(0, -1))}>{t("fleetPrevious")}</Button>
          <Button variant="outline" disabled={loading || !currentPage?.has_more || !currentPage.next_cursor} onClick={() => move(currentPage!.next_cursor!, [...previous, cursor])}>{t("fleetNext")}</Button>
        </div>} />
      {selected && <section className="space-y-3 border-t pt-4" aria-label={t("intakeWorkbenchSource")} aria-busy={sourceLoaded !== sourceKey}>
        <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="font-medium">{t("intakeWorkbenchSource")}</h2><Button variant="outline" onClick={() => setParams(old => { const next = new URLSearchParams(old); next.delete("intake"); return next })}>{t("personalWorkClose")}</Button></div>
        {sourceLoaded !== sourceKey && <p role="status">{t("loading")}</p>}
        {sourceLoaded === sourceKey && sourceError && <><Alert>{sourceError}</Alert><Button variant="outline" onClick={() => setRevision(value => value + 1)}>{t("intakeRead")}</Button></>}
        {current && <>
          <dl className="grid gap-3 text-sm sm:grid-cols-2">{[["intakeWorkbenchID", current.id], ["status", current.state], ["clientVersion", current.version], ["intakeWorkbenchSourceHash", current.raw_content_hash]].map(([label, value]) => <div key={label}><dt className="text-muted-foreground">{t(String(label))}</dt><dd className="break-all">{value}</dd></div>)}</dl>
          <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words text-sm">{current.raw_content ?? t("intakeWorkbenchContentUnavailable")}</pre>
          {current.triage && <section className="space-y-2" aria-label={t("intakeTriageTitle")}>
            <h3 className="font-medium">{t("intakeTriageTitle")} · {current.triage.revision}</h3>
            <p className="break-all text-sm">{current.triage.hash}</p>
            <p className="whitespace-pre-wrap break-words">{current.triage.draft.summary}</p>
            <p className="text-sm text-muted-foreground">{t("intakeTriageDraftOnly")}</p>
          </section>}
          <IntakeConversation key={`conversation:${current.id}:${current.version}`} project={project} source={current} />
          <IntakeSuggestions key={`suggestions:${current.id}:${current.version}`} project={project} source={current} />
          {allowed("intake_triage") && current.id && ["RECEIVED","TRIAGED","CLARIFYING"].includes(current.state) && <IntakeTriageRequest key={`request:${current.id}:${current.version}`} project={project} intakeID={current.id} onSaved={()=>{setTriageRequested(true);setRevision(value=>value+1)}} />}
          <SourceDisposition key={`source-disposition:${current.id}:${current.version}`} project={project} source={current} onSaved={()=>{setSaved(true);setRevision(value=>value+1)}} />
          <IntakeReworkDisposition key={`disposition:${current.id}:${current.version}`} project={project} source={current} onSaved={()=>{setSaved(true);setRevision(value=>value+1)}}/>
          <IntakeNewProject key={`project:${current.id}:${current.version}`} project={project} source={current} onSaved={()=>{setSaved(true);setRevision(value=>value+1)}}/>
          <IntakeNewRequirement key={`convert:${current.id}:${current.version}`} project={project} base={base} source={current} onSaved={() => { setSaved(true); setRevision(value => value + 1) }} />
 <ClarificationRequestForm key={`clarify:${current.id}:${current.version}`} project={project} source={current} onSaved={() => { setSaved(true); setRevision(value => value + 1) }} />
 {current.card ? <>
            <h3 className="font-medium">{t("intakeFrozenProposal")} · {t("intakeCardRevision")} {current.card.card_revision}</h3>
            <p className="break-all text-sm">{t("intakeProposalHash")}: {current.card.proposal_hash}</p>
            <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words text-xs">{current.card.proposal}</pre>
            <pre className="max-h-96 overflow-auto whitespace-pre-wrap break-words text-xs">{current.card.impact}</pre>
            {current.card.change_id && current.id && <Link className="inline-block underline underline-offset-4" to={`${base}/changes?${new URLSearchParams({ id: current.card.change_id, intake: current.id })}`}>{t("intakeWorkbenchOpenChange")}</Link>}
          </> : !current.triage && <p role="status" className="text-sm text-muted-foreground">{t("intakeWorkbenchNoCard")}</p>}
          {["NEW", "REWORK"].includes(current.kind) && ["RECEIVED", "TRIAGED"].includes(current.state) && <IntakeTriage key={`${current.id}:${current.version}`} source={current} project={project} onSaved={() => { setSaved(true); setRevision(value => value + 1) }} />}
        </>}
      </section>}
    </div>
 <Clarifications project={project} external={false} onChanged={() => setRevision(value => value + 1)}/>
  </div>
}
