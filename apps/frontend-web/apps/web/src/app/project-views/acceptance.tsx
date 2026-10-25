import {useEffect,useState} from "react"
import {Button} from "@workspace/ui/components/button"
import {useLocale,useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {acceptanceApi,type AcceptanceReminder,type AcceptanceMetrics} from "./acceptance-api"

export function AcceptanceScheduleList({rows}:{rows:AcceptanceReminder[]}){
 const t=useTranslations("iam"),locale=useLocale()
 return <ol className="grid gap-3">{rows.map(row=><li key={row.id} className="border-l-2 pl-3 text-sm"><p>{t(row.kind==="ESCALATION"?"acceptanceEscalation":"acceptanceReminder",{day:row.day})}</p><time dateTime={row.due_at}>{new Date(row.due_at).toLocaleString(locale)}</time><p>{row.cancelled_at?t("acceptanceCancelled"):row.emitted_at?t("acceptanceEmitted"):t("acceptanceScheduled")}</p></li>)}</ol>
}
export function AcceptanceMetricsTable({value}:{value:AcceptanceMetrics}){
 const t=useTranslations("iam"),locale=useLocale()
 return <div className="grid gap-3"><h3 className="font-medium">{t("acceptanceDaily")}</h3><p className="text-sm text-muted-foreground">{t("acceptanceFormula")} · {value.timezone} · v{value.calendar_version}</p><div className="overflow-auto"><table className="w-full text-left text-sm"><thead><tr><th scope="col">{t("acceptanceDay")}</th><th scope="col">{t("acceptanceSamples")}</th><th scope="col">{t("acceptanceRate")}</th></tr></thead><tbody>{value.days.map(day=><tr key={day.date}><th scope="row" className="py-2 font-normal">{day.date}</th><td>{day.accepted} / {day.receipts}</td><td>{day.acceptance_rate==null?t("acceptanceNoSamples"):new Intl.NumberFormat(locale,{style:"percent",maximumFractionDigits:1}).format(day.acceptance_rate)}</td></tr>)}</tbody></table></div></div>
}
export function AcceptanceStatus({project,id,external}:{project:string;id:string;external:boolean}){
 const t=useTranslations("iam"),[page,setPage]=useState<{items:AcceptanceReminder[];has_more:boolean;next_cursor?:string}>({items:[],has_more:false}),[cursor,setCursor]=useState(""),[metrics,setMetrics]=useState<AcceptanceMetrics|null>(null),[error,setError]=useState(""),[loadedKey,setLoadedKey]=useState(""),[revision,setRevision]=useState(0)
 const readKey=JSON.stringify([project,id,external,cursor,revision]),busy=loadedKey!==readKey
 useEffect(()=>{const c=new AbortController()
  const reads=[acceptanceApi.schedule(project,id,cursor,c.signal).then(v=>{if(!c.signal.aborted)setPage(v)}).catch(e=>{if(!c.signal.aborted)setPage({items:[],has_more:false});throw e})]
  if(!external)reads.push(acceptanceApi.metrics(project,c.signal).then(v=>{if(!c.signal.aborted)setMetrics(v)}).catch(e=>{if(!c.signal.aborted)setMetrics(null);throw e}))
  void Promise.allSettled(reads).then(results=>{if(c.signal.aborted)return;const failure=results.find(r=>r.status==="rejected");setError(failure?.status==="rejected"?String(failure.reason):"");setLoadedKey(readKey)})
  return()=>c.abort()
 },[project,id,external,cursor,revision,readKey])
 return <section className="grid gap-4 border-t py-4" aria-busy={busy} aria-label={t("acceptanceSchedule")}><h2 className="font-medium">{t("acceptanceSchedule")}</h2><p className="text-sm text-muted-foreground">{t("acceptanceNotice")}</p>{busy&&<p role="status">{t("loading")}</p>}{!busy&&error&&<Alert>{error}</Alert>}{!busy&&<AcceptanceScheduleList rows={page.items}/>}{!busy&&!page.items.length&&<p>{t("clientEmpty")}</p>}<div className="flex flex-wrap gap-2"><Button variant="outline" disabled={busy} onClick={()=>setRevision(v=>v+1)}>{t("refresh")}</Button><Button variant="outline" disabled={busy||!cursor} onClick={()=>setCursor("")}>{t("clientFirstPage")}</Button><Button variant="outline" disabled={busy||!page.has_more} onClick={()=>setCursor(page.next_cursor??"")}>{t("fleetNext")}</Button></div>{!busy&&!external&&metrics&&<AcceptanceMetricsTable value={metrics}/>}</section>
}
