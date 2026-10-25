import {useLocale,useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Link} from "react-router"
import {useAllowed} from "../../lib/permissions"
import type {ProjectDetail} from "../../lib/iam-api"

export type BudgetTrend = ProjectDetail["budget_trend"]

export function ProjectWorkAreas({tenantID,projectID}:{tenantID:string;projectID:string}) {
 const t=useTranslations("iam"),allowed=useAllowed()
 const links=[{path:"work-items",label:"workItems",permission:"work_item_read"},{path:"requirements",label:"requirements",permission:"requirement_read"},{path:"conversations",label:"conversations",permission:"channel_read"},{path:"deliveries",label:"projectSurfaceDeliveries",permission:"channel_read"}].filter(link=>allowed(link.permission))
 if(!links.length)return null
 const base=`/admin/${encodeURIComponent(tenantID)}/entities/${encodeURIComponent(projectID)}`
 return <nav className="mb-4 space-y-3 rounded-lg border p-4" aria-label={t("projectWorkAreas")}><h2 className="font-semibold">{t("projectWorkAreas")}</h2><p className="text-sm text-muted-foreground">{t("projectWorkAreasHint")}</p><div className="flex flex-wrap gap-2">{links.map(link=><Link key={link.path} className="inline-flex min-h-11 items-center rounded-md border px-4 text-sm font-medium hover:bg-accent focus-visible:outline-2" to={`${base}/${link.path}`}>{t(link.label)}</Link>)}</div></nav>
}

export function ProjectBudgetTrend({trend}:{trend?:BudgetTrend}) {
 const t=useTranslations("iam"),locale=useLocale()
 const points=trend?.points??[]
 const valid=points.filter(p=>/^\d+$/.test(p.spent_micros)&&Number.isFinite(Date.parse(p.at)))
 const ceiling=valid.reduce((max,p)=>BigInt(p.spent_micros)>max?BigInt(p.spent_micros):max,1n)
 const path=valid.map((p,i)=>`${valid.length<2?0:i/(valid.length-1)*600},${100-Number(BigInt(p.spent_micros)*10000n/ceiling)/100}`).join(" ")
 return <section className="grid gap-3 border-b py-4" aria-label={t("overviewBudgetTrend")}>
  <h2 className="font-medium">{t("overviewBudgetTrend")}</h2>
  <p className="text-sm text-muted-foreground">{t("overviewTrendMethod")}</p>
  {!trend?.available?<p role="status">{t("overviewTrendUnavailable")}</p>:valid.length===0?<p>{t("overviewTrendEmpty")}</p>:<>
   {valid.length>1&&<svg viewBox="0 0 600 104" className="h-28 w-full text-primary" aria-hidden="true"><polyline points={path} fill="none" stroke="currentColor" strokeWidth="2" vectorEffect="non-scaling-stroke"/></svg>}
   <div className="max-h-64 overflow-auto"><table className="w-full text-left text-sm"><thead><tr><th scope="col">{t("overviewTrendAt")}</th><th scope="col">{t("budgetSpent")}</th></tr></thead><tbody>{valid.map(p=><tr key={p.id??p.at}><td className="py-2">{new Date(p.at).toLocaleString(locale)}</td><td className="tabular-nums">{p.spent_micros}</td></tr>)}</tbody></table></div>
  </>}
  {trend?.truncated&&<p className="text-sm text-muted-foreground">{t("overviewTrendTruncated")}</p>}
 </section>
}

// Render persisted facts without treating missing health refreshes as a clean bill of health.
export function ProjectOverviewFacts({project}:{project:ProjectDetail}){
 const t=useTranslations("iam"),locale=useLocale()
 const facts=(value:unknown):unknown[]=>Array.isArray(value)?value:[]
 const readable=(value:unknown)=>typeof value==="string"?value:JSON.stringify(value)
 const holds=facts(project.holds),issues=facts(project.provisioning_issues),reasons=facts(project.health_reasons)
 const field=(name:string,value:unknown)=><div className="grid gap-1 border-b py-3"><dt className="text-sm text-muted-foreground">{t(name)}</dt><dd className="break-words">{value==null||value===""?t("overviewUnknown"):String(value)}</dd></div>
 return <><section className="grid gap-3 border-b py-4" aria-label={t("overviewFacts")}>
  <h2 className="font-medium">{t("overviewFacts")}</h2>
  <dl className="grid gap-x-6 sm:grid-cols-2 xl:grid-cols-3">
   {field("projectHealth",project.health)}{field("overviewHealthAt",project.health_refreshed_at?new Date(project.health_refreshed_at).toLocaleString(locale):null)}
   {field("projectLeadAgent",project.lead_agent_id)}{field("projectTeamMode",project.team_mode)}{field("overviewDispatch",project.dispatch_policy)}
   {field("projectBudgetCap",project.budget_cap_micros??t("budgetUnlimited"))}{field("budgetSpent",project.budget_spent_micros)}{field("overviewBudgetState",project.budget_state)}
  </dl>
  <p className="text-sm text-muted-foreground">{t("overviewSnapshotNotice")}</p>
  {[["overviewHealthReasons",reasons],["overviewHolds",holds],["overviewProvisioning",issues]].map(([key,values])=><div key={key as string}><h3 className="font-medium">{t(key as string)}</h3>{(values as unknown[]).length?<ul className="list-disc pl-5 text-sm">{(values as unknown[]).map((value,index)=><li className="whitespace-pre-wrap break-words" key={index}>{readable(value)}</li>)}</ul>:<p className="text-sm text-muted-foreground">{t("overviewNoRecordedFacts")}</p>}</div>)}
 </section><ProjectBudgetTrend trend={project.budget_trend}/></>
}
