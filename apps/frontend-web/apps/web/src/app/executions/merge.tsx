import { useCallback, useRef, useState } from "react"
import { useLocale } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { AutoForm } from "@workspace/ui/components/auto-form"
import { Alert } from "../../components/alert"
import { ApiError } from "../../lib/iam-api"
import { useAllowed } from "../../lib/permissions"
import { useFacts } from "../runtime/use-facts"
import { mergeApi, mergeLabels, type MergeFact, type MergeRequest } from "./merge-data"

export function MergeFacts({rows}: {rows: MergeFact[]}) {
  const locale = useLocale(), l = mergeLabels[locale as keyof typeof mergeLabels] ?? mergeLabels["en-US"]
  return <ol className="grid min-w-0 gap-3">{rows.map(row => <li className="grid min-w-0 gap-2 border-l-2 pl-3 text-sm" key={row.id}>
    <p className="break-all">#{row.attempt} · {row.id} · {l.states[row.state as keyof typeof l.states] ?? row.state}</p>
    <p className="break-all">{row.source_commit} → {row.target_branch}</p><p className="break-words">{row.reason}</p>
    <p className="break-all">{row.authority_source}</p>
    {row.observation && <><p className="break-all">{row.observation.merge_commit}</p><p>{row.observation.pushed ? l.pushed : l.unpushed} · {row.observation.reason}</p>{!!row.observation.conflict_files?.length && <details><summary className="min-h-11 cursor-pointer">{l.conflict}</summary><ul>{row.observation.conflict_files.map(path => <li className="break-all" key={path}>{path}</li>)}</ul></details>}</>}
  </li>)}{!rows.length && <li>{l.empty}</li>}</ol>
}
export function ExecutionMerge({executionID, state}: {executionID: string; state: string}) {
  const locale = useLocale(), l = mergeLabels[locale as keyof typeof mergeLabels] ?? mergeLabels["en-US"], allowed = useAllowed()
  const [revision, setRevision] = useState(0), [busy, setBusy] = useState(false), [error, setError] = useState(""), [unknown, setUnknown] = useState(false), [created, setCreated] = useState<MergeFact | null>(null)
  const pending = useRef<{body: MergeRequest; key: string} | null>(null)
  const load = useCallback((signal: AbortSignal) => mergeApi.list(executionID, signal), [executionID])
  const facts = useFacts(`${executionID}:${revision}`, load)
  const request = async (body?: MergeRequest) => {
    if (busy) return
    if (!pending.current && body) pending.current = {body, key: crypto.randomUUID()}
    if (!pending.current) return
    setBusy(true); setError("")
    try {const result = await mergeApi.request(executionID, pending.current.body, pending.current.key); setCreated(result); pending.current = null; setUnknown(false); setRevision(v => v + 1)}
    catch (cause) {setError(cause instanceof Error ? cause.message : l.failure); const uncertain = !(cause instanceof ApiError) || cause.status >= 500; setUnknown(uncertain); if (!uncertain) pending.current = null}
    finally {setBusy(false)}
  }
  const rows = facts.data ?? (created ? [created] : [])
  return <section className="grid min-w-0 gap-4" aria-label={l.title}><h2 className="text-lg font-semibold">{l.title}</h2><p className="text-sm text-muted-foreground">{l.hint}</p>
    {(error || facts.error) && <Alert>{error || facts.error}</Alert>}
    <Button variant="outline" className="min-h-11 w-fit" disabled={busy || facts.loading} onClick={() => setRevision(v => v + 1)}>{l.refresh}</Button>
    <MergeFacts rows={rows}/>
    {created && <p role="status">{l.accepted}: {created.id}</p>}
    {allowed("code_merge") && state === "SUCCEEDED" && !rows.some(row => ["PENDING", "LEASED", "MERGED"].includes(row.state)) && (unknown ? <><p role="status">{l.unknown}</p><Button disabled={busy} onClick={() => void request()}>{l.retry}</Button></> : <AutoForm key={created?.id ?? "new"} mode="edit" fields={{target_branch: {name: l.branch, type: "text"}, reason: {name: l.reason, type: "textarea", required: true}, push: {name: l.push, type: "checkbox"}}} onSubmit={async data => request({target_branch: String(data.target_branch ?? "").trim() || undefined, reason: String(data.reason ?? "").trim(), push: data.push === true})} ><Button type="submit" disabled={busy}>{l.request}</Button></AutoForm>)}
  </section>
}
