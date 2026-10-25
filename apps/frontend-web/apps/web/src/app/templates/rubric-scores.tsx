import { useEffect, useId, useRef, useState } from "react"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { rubricApi, rubricStages, rubricRoute, type RubricBoard, type RubricStage, type RubricCommand } from "./api"

export function RubricComparison({board}: {board: RubricBoard}) {
 const t = useTranslations("iam")
 const latest = rubricStages.map(stage => board.receipts.find(r => r.stage === stage))
 return <div className="grid min-w-0 gap-3">
  <p className="break-all text-xs">{t("rubricFrozen")}: {board.template_version_id} / {board.definition_hash}</p>
  <div className="max-w-full overflow-x-auto"><table className="w-full min-w-[34rem] border-collapse text-left text-sm"><caption className="pb-2 text-left">{t("rubricComparison")}</caption>
   <thead><tr><th className="p-2" scope="col">{t("rubricDimension")}</th>{rubricStages.map(stage => <th className="p-2" scope="col" key={stage}>{t(`rubricStage_${stage}`)}</th>)}</tr></thead>
   <tbody>{board.definition?.dimensions.map(dim => <tr key={dim.dimension_key} className="border-t">
    <th className="p-2 align-top" scope="row"><span className="break-words">{dim.dimension_key}</span><small className="block">{dim.kind === "HARD_GATE" ? t("rubricHardGate") : `${t("rubricWeight")}: ${dim.weight}`}</small>{board.divergences.filter(d => d.dimension === dim.dimension_key).map(d => <p key={`${d.left_id}:${d.right_id}`} className="mt-1 text-xs font-semibold" role="status">{t("rubricDivergence", {delta: d.delta})}</p>)}</th>
    {latest.map((r, index) => { const value = r?.scores.find(s => s.dimension_key === dim.dimension_key)
     return <td key={rubricStages[index]} className="p-2 align-top">{value ? <><strong>{value.score ?? (value.pass_fail ? t("rubricGatePass") : t("rubricGateFail"))}</strong>{value.note && <p className="max-w-64 whitespace-pre-wrap break-words">{value.note}</p>}{value.proofs?.map(proof => <p key={proof.task_id} className="max-w-64 break-all text-xs">{proof.verifier_key}: {proof.task_id} / {proof.receipt_hash}</p>)}</> : <span>{t("rubricNoScore")}</span>}</td>
    })}
   </tr>)}
   <tr className="border-t"><th className="p-2" scope="row">{t("rubricTotal")}</th>{latest.map((r, index) => <td className="p-2" key={rubricStages[index]}>{r ? <>{r.calculation.total ?? "—"} / {r.calculation.weighted_min}<p>{t(r.calculation.passed ? "rubricCalculationPass" : "rubricCalculationFail")}</p></> : t("rubricNoScore")}</td>)}</tr></tbody>
  </table></div>
  <details><summary className="cursor-pointer py-2">{t("rubricHistory", {count: board.receipts.length})}</summary><ol className="grid gap-3">{board.receipts.map(r => <li key={r.id} className="border-l-2 pl-3 text-sm"><p>{t(`rubricStage_${r.stage}`)} {r.scorer_id ?? ""}</p><p className="break-all">{r.id} / {r.created_at}</p>{r.result_id && <p className="break-all">{r.result_id} / v{r.result_version} / {r.head_commit}</p>}<ul className="mt-2 grid gap-2">{r.scores.map(value => <li key={value.dimension_key} className="min-w-0"><strong>{value.dimension_key}: {value.score ?? (value.pass_fail ? t("rubricGatePass") : t("rubricGateFail"))}</strong>{value.note && <p className="whitespace-pre-wrap break-words">{value.note}</p>}{value.evidence_refs.length > 0 && <p className="break-all text-xs">{t("rubricEvidence")}: {value.evidence_refs.join(", ")}</p>}</li>)}</ul></li>)}</ol></details>
 </div>
}
export function RubricScores({project, work, packageID, itemID}: {project: string; work?: string; packageID?: string; itemID?: string}) {
 const url = rubricRoute(project, work, packageID, itemID)
 return <RubricScoresContent key={url} url={url} clientItem={Boolean(packageID)}/>
}
function RubricScoresContent({url, clientItem}: {url: string; clientItem: boolean}) {
 const t = useTranslations("iam"), id = useId()
 const [board, setBoard] = useState<RubricBoard | null>(null), [error, setError] = useState(""), [loading, setLoading] = useState(true), [saving, setSaving] = useState(false), [refresh, setRefresh] = useState(0)
 const [stage, setStage] = useState<RubricStage | "">(""), [draft, setDraft] = useState<Record<string, {value: string; note: string; evidence: string[]}>>({})
 const pending = useRef<{fingerprint: string; key: string} | null>(null), write = useRef<AbortController | null>(null)
 useEffect(() => {const c = new AbortController()
  void rubricApi.read(url, c.signal).then(next => {if (!c.signal.aborted) {setBoard(next);setError("");setStage(current => next.allowed_stages.includes(current as RubricStage) ? current : next.allowed_stages[0] ?? "")}}).catch(cause => {if (!c.signal.aborted) {setError(cause instanceof Error ? cause.message : String(cause));setBoard(null)}}).finally(() => {if (!c.signal.aborted) setLoading(false)})
  return () => c.abort()
 }, [url, refresh])
 useEffect(() => () => write.current?.abort(), [])
 const update = (key: string, patch: Partial<{value: string; note: string; evidence: string[]}>) => setDraft(previous => ({...previous, [key]: {...(previous[key] ?? {value: "", note: "", evidence: []}), ...patch}}))
 const submit = async (event: React.FormEvent<HTMLFormElement>) => {
  event.preventDefault()
  if (write.current || !valid || !board?.definition || !stage || saving || loading || !board.allowed_stages.includes(stage)) return
  const body: RubricCommand = {stage, result_id: board.result_id ?? null, work_version: board.work_version, scores: board.definition.dimensions.map(dim => {
   const value = draft[dim.dimension_key]!
   return {dimension_key: dim.dimension_key, ...(dim.kind === "HARD_GATE" ? {pass_fail: value.value === "true"} : {score: Number(value.value)}), evidence_refs: clientItem ? [] : value.evidence, note: value.note.trim()}
  })}
  const fingerprint = JSON.stringify(body);if (pending.current?.fingerprint !== fingerprint) pending.current = {fingerprint, key: crypto.randomUUID()}
  const c = new AbortController();write.current = c;setSaving(true);setError("")
  try {await rubricApi.record(url, body, pending.current.key, c.signal);if (!c.signal.aborted) {pending.current = null;setDraft({});setLoading(true);setRefresh(n => n + 1)}}
  catch (cause) {if (!c.signal.aborted) setError(cause instanceof Error ? cause.message : String(cause))}
  finally {if (!c.signal.aborted) {write.current = null;setSaving(false)}}
 }
 const valid = board?.definition?.dimensions.every(dim => {const d = draft[dim.dimension_key];return d && d.note.trim() && (clientItem || d.evidence.length > 0) && (dim.kind === "HARD_GATE" ? ["true","false"].includes(d.value) : d.value.trim() !== "" && Number.isFinite(Number(d.value)) && Number(d.value) >= 1 && Number(d.value) <= 5)})
 return <section className="grid min-w-0 gap-3 border-t pt-4" aria-labelledby={`${id}-title`}>
  <div className="flex flex-wrap items-center justify-between gap-2"><h3 className="font-medium" id={`${id}-title`}>{t("rubricTitle")}</h3><Button variant="outline" disabled={loading || saving} onClick={() => {setLoading(true);setRefresh(n => n + 1)}}>{t("refresh")}</Button></div>
  <p className="text-sm text-muted-foreground">{t("rubricNotice")}</p>
  {loading && <p role="status">{t("loading")}</p>}{error && <Alert>{error}</Alert>}
  {board && (board.definition ? <><RubricComparison board={board}/>{board.allowed_stages.length > 0 && <details><summary className="cursor-pointer py-2">{t("rubricRecord")}</summary>
   <form onSubmit={event => void submit(event)} className="grid min-w-0 gap-4"><fieldset disabled={saving || loading} className="grid min-w-0 gap-4">
    <label className="grid gap-1 text-sm">{t("rubricStage")}<select required value={stage} onChange={e => setStage(e.target.value as RubricStage)} className="min-h-11 rounded-md border bg-background p-2">{board.allowed_stages.map(s => <option key={s} value={s}>{t(`rubricStage_${s}`)}</option>)}</select></label>
    {board.definition.dimensions.map(dim => {const d = draft[dim.dimension_key] ?? {value: "", note: "", evidence: []}
     return <fieldset key={dim.dimension_key} className="grid min-w-0 gap-3 border-l-2 pl-3"><legend className="break-all font-medium">{dim.dimension_key}</legend>
      <label className="grid gap-1 text-sm">{dim.kind === "HARD_GATE" ? t("rubricHardGate") : t("rubricScore")}
       {dim.kind === "HARD_GATE" ? <select required value={d.value} onChange={e => update(dim.dimension_key, {value: e.target.value})} className="min-h-11 rounded-md border bg-background p-2"><option value="">—</option><option value="true">{t("rubricGatePass")}</option><option value="false">{t("rubricGateFail")}</option></select> : <Input required type="number" min={1} max={5} step="any" value={d.value} onChange={e => update(dim.dimension_key, {value: e.target.value})}/>}
      </label><label className="grid gap-1 text-sm">{t("rubricNote")}<Textarea required maxLength={2000} value={d.note} onChange={e => update(dim.dimension_key, {note: e.target.value})}/></label>
      {!clientItem && <label className="grid gap-1 text-sm">{t("rubricEvidence")}<select multiple required className="min-h-24 max-w-full rounded-md border bg-background p-2" value={d.evidence} onChange={e => update(dim.dimension_key, {evidence: Array.from(e.target.selectedOptions, o => o.value)})}>{board.evidence_refs.map(ref => <option value={ref} key={ref}>{ref}</option>)}</select></label>}
     </fieldset>
    })}
    <p className="text-sm text-muted-foreground">{t("rubricProofNotice")}</p><Button type="submit" disabled={!valid || !stage || saving || loading} className="min-h-11 justify-self-start">{t("rubricRecord")}</Button>
   </fieldset></form></details>}</> : <p role="status">{t("rubricUnavailable")}</p>)}
 </section>
}
