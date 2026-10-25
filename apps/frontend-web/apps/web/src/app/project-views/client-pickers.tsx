import { useContext, useEffect, useState } from "react"
import { type FieldRendererProps } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useLocale, useTranslations } from "@workspace/ui/i18n/intl-provider"
import { iamApi, type WorkItem } from "../../lib/iam-api"
import type { components } from "../../lib/api-schema"
import { clientworkApi } from "../../lib/clientwork-api"
import { Alert } from "../../components/alert"
import { ClientPickerScope } from "./client-picker-scope"
import { PackageEvidenceMapping } from "./package-evidence"
import { packageEvidenceLabels } from "./package-evidence-labels"
type Choice = { id: string; title: string }
export function ReferencePicker({value, onChange, disabled, labelId}: FieldRendererProps) {
  const {project, collection} = useContext(ClientPickerScope)
  const t = useTranslations("iam")
  const [page, setPage] = useState(0)
  const [history, setHistory] = useState<string[]>([""])
  const [next, setNext] = useState("")
  const [rows, setRows] = useState<Choice[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [retry, setRetry] = useState(0)
  const selected = (Array.isArray(value) ? value : []) as string[]
  const requestKey=JSON.stringify([project,collection,page,history,retry])
  const [previousRequest,setPreviousRequest]=useState("")
  if(previousRequest!==requestKey){setPreviousRequest(requestKey);setBusy(true);setError("");setRows([])}
  useEffect(() => {
    const controller = new AbortController()
    const load = async () => {
      if (collection === "sheets") {
        const result = await iamApi.requirements(project, {offset: page * 20, limit: 20}, controller.signal)
        return {items: result.items.map(row => ({id: row.id!, title: row.title})), next: (page * 20 + result.items.length < result.total) ? "more" : ""}
      }
      if (collection === "cases") {
        const result = await clientworkApi.list(project, "cases", history[page], controller.signal)
        return {items: result.items.map(row => ({id: row.id!, title: row.title})), next: result.next_cursor ?? ""}
      }
      const result = await clientworkApi.list(project, collection === "issues" ? "previews" : "sheets", history[page], controller.signal)
      return {items: result.items.map(row => ({id: row.id!, title: row.title})), next: result.next_cursor ?? ""}
    }
    void load().then(result => { if (!controller.signal.aborted) {setRows(result.items); setNext(result.next)} }).catch(e => {if (!controller.signal.aborted) setError(e instanceof Error ? e.message : t("clientLoadFailed"))}).finally(() => {if (!controller.signal.aborted) setBusy(false)})
    return () => controller.abort()
  }, [project, collection, page, history, retry, t])
  return <div role="group" aria-labelledby={labelId} aria-busy={busy} className="grid gap-2">
    {error && <Alert>{error}</Alert>}
    {rows.map(row => <label key={row.id} className="flex min-h-10 items-center gap-2"><input type="checkbox" disabled={disabled || busy} checked={selected.includes(row.id)} onChange={e => onChange(e.target.checked ? [...selected, row.id] : selected.filter(id => id !== row.id))} />{row.title}</label>)}
    {!busy && !rows.length && !error && <p role="status">{t("clientEmpty")}</p>}
    <p className="text-sm text-muted-foreground">{t("clientSelected", {count: selected.length})}</p>
    <div className="flex gap-2"><Button type="button" variant="outline" disabled={disabled || busy || page === 0} onClick={() => setPage(v => v-1)}>{t("fleetPrevious")}</Button><Button type="button" variant="outline" disabled={disabled || busy || !next} onClick={() => {setHistory(old => [...old.slice(0,page+1),next]); setPage(v => v+1)}}>{t("fleetNext")}</Button>{error && <Button type="button" variant="outline" onClick={() => setRetry(v => v+1)}>{t("clientRetry")}</Button>}</div>
  </div>
}
export function ProgressArtifactPicker(props: FieldRendererProps) { return <ArtifactPicker {...props} progress /> }
export function ArtifactPicker({value, onChange, disabled, labelId, progress = false}: FieldRendererProps & {progress?: boolean}) {
  const {project} = useContext(ClientPickerScope)
  const t = useTranslations("iam")
  const locale = useLocale()
  const labels = packageEvidenceLabels[locale as keyof typeof packageEvidenceLabels] ?? packageEvidenceLabels["en-US"]
  const selected = (Array.isArray(value) ? value : []) as components["schemas"]["ClientArtifactRef"][]
  const [page, setPage] = useState(0)
  const [works, setWorks] = useState<WorkItem[]>([])
  const [total, setTotal] = useState(0)
  const [work, setWork] = useState<WorkItem | null>(null)
  const [result, setResult] = useState<components["schemas"]["ResultView"] | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const requestKey=JSON.stringify([project,page,progress])
  const [previousRequest,setPreviousRequest]=useState("")
  if(previousRequest!==requestKey){setPreviousRequest(requestKey);setBusy(true);setWorks([]);setError("");setWork(null);setResult(null)}
  useEffect(() => {
    const controller = new AbortController()
    void iamApi.workItems(project,{state:progress ? "in:NEEDS_HUMAN_REVIEW,INTERNAL_QA,ACCEPTED" : "eq:ACCEPTED",offset:page*20,limit:20},controller.signal).then(v => {if (!controller.signal.aborted) {setWorks(v.items.filter(w => !progress || Boolean(w.current_claim_id)));setTotal(v.total)}}).catch(e => {if(!controller.signal.aborted) setError(String(e))}).finally(() => {if(!controller.signal.aborted)setBusy(false)})
    return () => controller.abort()
  },[project,page,progress])
  useEffect(() => {
    if (!work) return
    const controller = new AbortController()
    void iamApi.deliveryResultRequest<components["schemas"]["ResultView"]>({method:"GET",url:`/entities/${project}/work-items/${work.id}/results/${work.current_claim_id}`},{signal:controller.signal}).then(current => {if(!controller.signal.aborted){if(!current || current.id!==work.current_claim_id) throw new Error(t("clientNoAcceptedEvidence"));setResult(current)}}).catch(e => {if(!controller.signal.aborted)setError(String(e))}).finally(() => {if(!controller.signal.aborted)setBusy(false)})
    return () => controller.abort()
  },[project,work,t])
  return <div role="group" aria-labelledby={labelId} aria-busy={busy} className="grid gap-2">
    {error && <Alert>{error}</Alert>}
    <select className="min-h-10 rounded border bg-background p-2" aria-label={t(progress ? "clientProgressSource" : "clientAcceptedWork")} disabled={disabled || busy} value={work?.id ?? ""} onChange={e => {setWork(works.find(w => w.id === e.target.value) ?? null);setResult(null);setError("");setBusy(Boolean(e.target.value))}}><option value="">{t("clientChooseWork")}</option>{works.map(w => <option key={w.id} value={w.id!}>{w.title}</option>)}</select>
    {result && <PackageEvidenceMapping result={result}/>}
    {result?.evidence?.map(evidence => <label key={evidence.id} className="flex min-h-10 items-center gap-2"><input type="checkbox" disabled={disabled || evidence.coverage !== "COVERED"} checked={selected.some(v => v.evidence_id === evidence.id)} onChange={e => onChange(e.target.checked ? [...selected,{work_item_id:work!.id!,result_id:result.id!,evidence_id:evidence.id!}] : selected.filter(v => v.evidence_id !== evidence.id))}/>{evidence.name} / {evidence.coverage}</label>)}
    <section className="grid min-w-0 gap-2" aria-label={labels.selected}>{selected.map(ref => <div key={`${ref.work_item_id}:${ref.result_id}:${ref.evidence_id}`} className="flex min-w-0 flex-wrap items-center gap-2 text-sm"><span className="break-all">{labels.work}: {ref.work_item_id} ? {labels.result}: {ref.result_id} ? {labels.evidence}: {ref.evidence_id}</span><Button type="button" variant="outline" disabled={disabled || busy} aria-label={`${labels.remove} ${ref.evidence_id}`} onClick={() => onChange(selected.filter(item => item.work_item_id !== ref.work_item_id || item.result_id !== ref.result_id || item.evidence_id !== ref.evidence_id))}>{labels.remove}</Button></div>)}</section>
    <p className="text-sm">{t("clientSelected",{count:selected.length})}</p>
    <div className="flex gap-2"><Button type="button" variant="outline" disabled={busy || page === 0} onClick={() => setPage(v=>v-1)}>{t("fleetPrevious")}</Button><Button type="button" variant="outline" disabled={busy || (page+1)*20 >= total} onClick={() => setPage(v=>v+1)}>{t("fleetNext")}</Button></div>
  </div>
}
