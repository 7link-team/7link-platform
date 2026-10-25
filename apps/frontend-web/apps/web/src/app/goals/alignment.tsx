import {useEffect, useState} from "react"
import {Link, useParams} from "react-router"
import {Button} from "@workspace/ui/components/button"
import {Input} from "@workspace/ui/components/input"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Alert} from "../../components/alert"
import {useAllowed} from "../../lib/permissions"
import {planningApi, type GoalAlignment, type GoalInput, type PlanningRecord} from "./api"

export type ContributionChange = {action:"ASSIGN_CONTRIBUTOR"|"REMOVE_CONTRIBUTOR";principal_id:string;key:string}

export function GoalAlignmentPanel({record,busy,save}:{record:PlanningRecord;busy:boolean;save:(change:ContributionChange)=>Promise<void>}) {
  const {tenantID=""}=useParams()
  const t=useTranslations("iam")
  const allowed=useAllowed()
  const [data,setData]=useState<GoalAlignment|null>(null)
  const [error,setError]=useState("")
  const [principal,setPrincipal]=useState("")
  const goal=record.content as GoalInput
  const [kr,setKR]=useState(goal.key_results[0]?.key??"")
  const [revision,setRevision]=useState(0)
  useEffect(()=>{
    const controller=new AbortController()
    void planningApi.alignment(tenantID,record.id,controller.signal).then(value=>{if(!controller.signal.aborted){setData(value);setError("")}},(e:Error)=>{if(!controller.signal.aborted)setError(e.message)})
    return ()=>controller.abort()
  },[tenantID,record.id,record.version,revision])
  const editable=allowed("goal_manage")&&goal.level!=="MEMBER"&&["ACTIVE","AT_RISK"].includes(record.state)
  const change=async (value:ContributionChange)=>{await save(value)}
  return <section className="min-w-0 space-y-3 rounded-md border p-4">
    <h3 className="font-medium">{t("goalAlignmentTitle")}</h3>
    {error&&<><Alert>{error}</Alert><Button variant="outline" onClick={()=>setRevision(revision+1)}>{t("retryNow")}</Button></>}
    {!data&&!error&&<p role="status">{t("loading")}</p>}
    {data&&<>
      {data.visibility_limited&&<Alert>{t("goalAlignmentLimited")}</Alert>}
      {data.coverage!==undefined&&<p>{t("goalAlignmentCoverage")}: {Math.round(data.coverage*100)}%</p>}
      {(data.uncovered??[]).map(key=><p key={key} className="text-sm">{t("goalAlignmentUncovered")}: {key}</p>)}
      <ul className="space-y-2">{(data.children??[]).map(child=><li key={child.id} className="break-words text-sm"><Link className="underline" to={`/admin/${encodeURIComponent(tenantID)}${child.project_id&&child.project_id!=="0"?`/entities/${encodeURIComponent(child.project_id)}`:""}/goals?record_id=${encodeURIComponent(child.id)}&principal_id=${encodeURIComponent(child.principal_id)}`}>{(child.content as GoalInput).title}</Link> · {(child.content as GoalInput).parent_kr} · {child.state}</li>)}</ul>
      <h4 className="font-medium">{t("goalContributors")}</h4>
      <ul className="space-y-2">{(data.contributors??[]).map((c,index)=><li key={`${c.kr}:${c.principal_id}:${c.source}:${index}`} className="flex flex-wrap items-center gap-2 text-sm"><span>{c.kr} · {c.principal_id} · {t(`goalContributor_${c.source}`)}</span>{editable&&c.source==="ASSIGNED"&&<Button variant="outline" disabled={busy} onClick={()=>void change({action:"REMOVE_CONTRIBUTOR",principal_id:c.principal_id,key:c.kr})}>{t("goalContributorRemove")}</Button>}</li>)}</ul>
      {editable&&<form className="flex flex-wrap items-end gap-3" onSubmit={e=>{e.preventDefault();void change({action:"ASSIGN_CONTRIBUTOR",principal_id:principal,key:kr})}}>
        <label className="space-y-1 text-sm"><span>{t("goalContributorPrincipal")}</span><Input required inputMode="numeric" pattern="[1-9][0-9]*" value={principal} onChange={e=>setPrincipal(e.target.value)}/></label>
        <label className="space-y-1 text-sm"><span>{t("goalContributorKR")}</span><select className="block min-h-11 rounded border bg-background px-3" value={kr} onChange={e=>setKR(e.target.value)}>{goal.key_results.map(k=><option key={k.key} value={k.key}>{k.title}</option>)}</select></label>
        <Button type="submit" disabled={busy}>{t("goalContributorAssign")}</Button>
      </form>}
    </>}
  </section>
}
