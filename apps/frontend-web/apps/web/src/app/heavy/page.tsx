import { useEffect, useRef, useState } from "react"
import { useParams, useSearchParams, Link } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { AutoTable } from "@workspace/ui/components/auto-table"
import { PageHeader } from "../../components/page-header"
import { useAllowed } from "../../lib/permissions"
import { executionIDs, heavyAPI, type Ensemble } from "./api"

export default function HeavyPage() {
 const {tenantID = "", entityID = ""} = useParams()
 return <HeavyContent key={`${tenantID}:${entityID}`} tenant={tenantID} project={entityID} />
}
function HeavyContent({tenant, project}: {tenant: string; project: string}) {
 const t = useTranslations("iam"), allowed = useAllowed(), [search, setSearch] = useSearchParams()
 const work = search.get("work_item_id") ?? "", selectedID = search.get("plan_id") ?? ""
 const [input, setInput] = useState(work), [executions, setExecutions] = useState(""), [reason, setReason] = useState(""), [synthesis, setSynthesis] = useState("")
 const [rows, setRows] = useState<Ensemble[]>([]), [error, setError] = useState(""), [loading, setLoading] = useState(false), [busy, setBusy] = useState(false), [reload, setReload] = useState(0)
 const canRead = allowed("work_item_read"), canWrite = allowed("work_item_assign")
 const selected = rows.find(row => row.id === selectedID)
 const retry = useRef<{body: string; key: string} | null>(null)
 useEffect(() => {
  if (!canRead || !/^[1-9][0-9]*$/.test(work)) return
  const controller = new AbortController()
  heavyAPI.list(tenant, project, work, controller.signal).then(data => {setRows(data); setError("")}).catch(e => {if (!controller.signal.aborted) setError(e instanceof Error ? e.message : String(e))}).finally(() => {if (!controller.signal.aborted) setLoading(false)})
  return () => controller.abort()
 }, [tenant, project, work, canRead, reload])
 async function run(action: string, branch = "") {
  setError("")
  if (reason.trim().length < 10) {setError(t("heavyInvalid")); return}
  const body = JSON.stringify({action, branch, synthesis, reason, executions, version: selected?.version, work})
  if (retry.current?.body !== body) retry.current = {body, key: crypto.randomUUID()}
  setBusy(true)
  try {
   const value = action === "CREATE" ? await heavyAPI.create(tenant, project, work, executionIDs(executions), reason, retry.current.key) : selected ? await heavyAPI.decide(tenant, project, selected, action, branch, synthesis, reason, retry.current.key) : null
   if (value) {setRows(previous => [value, ...previous.filter(row => row.id !== value.id)]); setSearch({work_item_id: work, plan_id: value.id}); retry.current = null; setReload(value => value + 1)}
  } catch (e) {setError(e instanceof Error && e.message !== "invalid" ? e.message : t("heavyInvalid"))} finally {setBusy(false)}
 }
 return <div className="min-w-0 space-y-5 p-4"><PageHeader title={t("heavyTitle")} /><p className="text-sm text-muted-foreground">{t("heavyHint")}</p>
  {!canRead ? <p>{t("projectSurfaceAccessUnavailable")}</p> : <>
   <form className="flex flex-wrap gap-2" onSubmit={e => {e.preventDefault(); if (/^[1-9][0-9]*$/.test(input)) {setLoading(true); setRows([]); setSearch({work_item_id: input}); setReload(v => v + 1)} else setError(t("heavyInvalid"))}}><label>{t("heavyWork")}<Input value={input} onChange={e => setInput(e.target.value)} /></label><Button type="submit">{t("view")}</Button></form>
   {error && <p role="alert" className="break-words text-destructive">{error}</p>}{loading && <p role="status">{t("loading")}</p>}
   <AutoTable data={rows} rowKey="id" pagination={false} exportable={false} columns={[{field: "id", name: t("heavyPlan"), format: (_, row) => <Button variant="link" onClick={() => setSearch({work_item_id: work, plan_id: row.id})}>{row.id}</Button>}, {field: "state", name: t("status"), format: value => t(`heavyState_${value}`)}, {field: "version", name: t("heavyVersion")}]} />
   {selected && <section className="space-y-3 border-t pt-4"><h2 className="font-semibold">{t("heavyPlan")} {selected.id}</h2><p className="break-words">{selected.reason}</p><ul className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">{selected.branches.map(branch => {const fact = selected.comparison.find(f => f.id === branch.execution_id); return <li key={branch.id} className="flex min-w-0 flex-col items-start gap-3 rounded-md border p-4"><Link className="underline" to={`/admin/${tenant}/entities/${project}/executions/${branch.execution_id}`}>{t("heavyExecution")} {branch.execution_id}</Link><span>{fact?.state ?? t("heavyUncompared")}</span>{branch.archived_at && <span>{t("heavyArchived")}</span>}{selected.selected_branch_id === branch.id && <span>{t("heavyChosen")}</span>}{canWrite && selected.state === "COMPARING" && fact?.state === "SUCCEEDED" && <><Button disabled={busy} onClick={() => run("SELECT", branch.id)}>{t("heavySelect")}</Button><Button disabled={busy || !/^[1-9][0-9]*$/.test(synthesis)} onClick={() => run("SYNTHESIZE", branch.id)}>{t("heavySynthesize")}</Button></>}</li>})}</ul>{selected.synthesis_execution_id && <Link className="underline" to={`/admin/${tenant}/entities/${project}/executions/${selected.synthesis_execution_id}`}>{t("heavySynthesis")} {selected.synthesis_execution_id}</Link>}</section>}
   {canWrite && work && <section className="space-y-3 border-t pt-4"><label className="block">{t("heavyExecutions")}<Input value={executions} onChange={e => setExecutions(e.target.value)} /></label><label className="block">{t("heavySynthesis")}<Input value={synthesis} onChange={e => setSynthesis(e.target.value)} /></label><label className="block">{t("govReason")}<Textarea value={reason} maxLength={2000} onChange={e => setReason(e.target.value)} /></label><div className="flex flex-wrap gap-2"><Button disabled={busy} onClick={() => run("CREATE")}>{t("heavyCreate")}</Button>{selected && ["DRAFT", "COMPARING"].includes(selected.state) && <><Button disabled={busy} onClick={() => run("COMPARE")}>{t("heavyCompare")}</Button><Button variant="destructive" disabled={busy} onClick={() => run("ABANDON")}>{t("heavyAbandon")}</Button></>}</div></section>}
  </>}
 </div>
}
