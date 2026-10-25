import { useEffect, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { provenanceApi, type ProvenancePage } from "./provenance-api"

export function ProvenanceFacts({page}: {page: ProvenancePage}) {
  const t = useTranslations("iam")
  return <div className="grid min-w-0 gap-4">
    <p className="text-sm text-muted-foreground">{t("provenanceLimitations")}</p>
    {(page.comparison_truncated || page.verifiers_truncated) && <Alert>{t("provenanceTruncated")}</Alert>}
    {page.items.length === 0 && <p>{t("executionNoEvidence")}</p>}
    {page.items.map((fact) => <section key={fact.id} className="grid min-w-0 gap-2 border-t py-3">
      <h3 className="break-words font-medium">{fact.title} · {fact.kind} · {t(`provenance_${fact.output_state}`)}</h3>
      <dl className="grid min-w-0 gap-2 text-sm sm:grid-cols-2">
        {[[t("executionID"), fact.execution_id], [t("provenanceProducer"), fact.producer_id === "0" ? t("provenanceUnknown") : fact.producer_id], [t("fleetMachineID"), fact.machine_id], [t("fleetCreatedAt"), fact.created_at], [t("executionPlanHash"), fact.plan_hash], [t("executionBaseCommit"), fact.base_commit], [t("executionContentHash"), fact.content_hash], ...(fact.commit ? [[t("provenanceCommit"), fact.commit]] : []), ...(fact.artifact ? [[t("provenanceFileVersion"), `${fact.artifact.file_id} / ${fact.artifact.version_id} / ${fact.artifact.file_version}`], [t("provenanceCriterion"), fact.artifact.criterion_id ?? t("provenanceUnknown")], [t("provenanceAssociation"), fact.artifact.coverage], [t("provenanceNote"), fact.artifact.note]] : [])].map(([label, value]) => <div key={label} className="min-w-0"><dt className="text-muted-foreground">{label}</dt><dd className="break-all">{value}</dd></div>)}
      </dl>
      {fact.comparison !== "NOT_REQUESTED" && <p className="text-sm">{t("provenanceComparison")}: {t(`provenance_${fact.comparison}`)} {fact.compared_evidence_id && <span className="break-all">({fact.compared_evidence_id})</span>}</p>}
    </section>)}
    <section className="grid gap-2 border-t py-3"><h3 className="font-medium">{t("provenanceVerifiers")}</h3>{page.verifiers.length === 0 && <p className="text-sm">{t("provenanceNoVerifier")}</p>}{page.verifiers.map((ref) => <dl key={ref.id} className="grid gap-1 break-all border-b py-2 text-sm"><dt>{ref.id} · {ref.state}</dt><dd>{t("provenanceCriterion")}: {ref.criterion_id} · {t("provenanceFileVersion")}: {ref.result_id}/{ref.result_version}</dd><dd>{t("executionPlanHash")}: {ref.plan_hash}</dd><dd>{t("provenanceReceipt")}: {ref.receipt_hash || t("provenanceUnknown")}</dd></dl>)}</section>
  </div>
}

export function EvidenceProvenancePanel({executionID}: {executionID: string}) {
  const t = useTranslations("iam")
  const [params, setParams] = useSearchParams()
  const before = params.getAll("evidence_before").at(-1) ?? ""
  const compare = params.get("compare_execution") ?? ""
  const [draft, setDraft] = useState(compare)
  const [page, setPage] = useState<ProvenancePage | null>(null)
  const [error, setError] = useState("")
  const [loading, setLoading] = useState(true)
  useEffect(() => {
    const controller = new AbortController()
    void provenanceApi.list(executionID, before, compare, controller.signal).then((result) => {if (!controller.signal.aborted) {setPage(result); setError("")}}).catch((cause: unknown) => {if (!controller.signal.aborted) {setPage(null); setError(cause instanceof Error ? cause.message : t("executionUnavailable"))}}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
    return () => controller.abort()
  }, [executionID, before, compare, t])
  const navigate = (next: URLSearchParams) => {setLoading(true); setPage(null); setParams(next)}
  return <div className="grid min-w-0 gap-3">
    <form className="flex flex-wrap items-end gap-3" onSubmit={(event) => {event.preventDefault(); const next = new URLSearchParams(params); next.delete("evidence_before"); if (draft.trim()) next.set("compare_execution", draft.trim()); else next.delete("compare_execution"); navigate(next)}}>
      <label className="grid min-w-0 gap-1 text-sm">{t("provenanceCompareExecution")}<input className="min-h-11 rounded-md border bg-background px-3" inputMode="numeric" pattern="[1-9][0-9]{0,18}" maxLength={19} value={draft} onChange={(event) => setDraft(event.target.value)} /></label><Button type="submit" disabled={loading}>{t("provenanceCompare")}</Button>
    </form>
    {error && <Alert>{error}</Alert>}{loading && <p role="status">{t("loading")}</p>}
    {!loading && page && <><ProvenanceFacts page={page}/><div className="flex gap-3"><Button variant="outline" disabled={!before} onClick={() => {const next = new URLSearchParams(params); const values = next.getAll("evidence_before"); next.delete("evidence_before"); values.slice(0, -1).forEach((value) => next.append("evidence_before", value)); navigate(next)}}>{t("previousPage")}</Button><Button variant="outline" disabled={!page.has_more || !page.next_before_id} onClick={() => {const next = new URLSearchParams(params); next.append("evidence_before", page.next_before_id!); navigate(next)}}>{t("nextPage")}</Button></div></>}
  </div>
}
