import { useEffect, useState } from "react"
import { useSearchParams } from "react-router"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { templatesApi, type TemplateFamily, type TemplateFilters } from "./api"
import type { TemplateVersion } from "./workflow"

const fields = {
 domain: ["SOFTWARE", "VIDEO", "NOVEL", "TEAM_TASKS"],
 origin: ["FIRST_PARTY", "TEAM"],
 state: ["DRAFT", "PUBLISHED", "DEPRECATED", "RETIRED"],
 team_mode: ["HUMAN_ONLY", "HYBRID", "AUTONOMOUS"],
} as const
export function TemplateLibrary({tenantID, onSelect}: {tenantID: string; onSelect: (id: string)=>void}) {
 const t = useTranslations("iam")
 const [search,setSearch] = useSearchParams()
 const filterKey = JSON.stringify(Object.fromEntries(Object.keys(fields).map(key=>[key,search.get(`library_${key}`) ?? ""])))
 const key = search.get("library_key") ?? "", cursor = search.get("library_cursor") ?? ""
 const [families,setFamilies]=useState<TemplateFamily[]>([])
 const [versions,setVersions]=useState<TemplateVersion[]>([])
 const [next,setNext]=useState("")
 const [loading,setLoading]=useState(true)
 const [error,setError]=useState("")
 const [retry,setRetry]=useState(0)
 useEffect(()=>{
  const abort=new AbortController()
  queueMicrotask(()=>{if(!abort.signal.aborted){setLoading(true);setError("");setFamilies([]);setVersions([]);setNext("")}})
  const filters=JSON.parse(filterKey) as TemplateFilters
  const request=key ? templatesApi.list(tenantID,cursor,abort.signal,{...filters,key}).then(page=>{if(!abort.signal.aborted){setVersions(page.items);setNext(page.next_cursor ?? "")}}) : templatesApi.families(tenantID,cursor,filters,abort.signal).then(page=>{if(!abort.signal.aborted){setFamilies(page.items);setNext(page.next_cursor ?? "")}})
  void request.catch(cause=>{if(!abort.signal.aborted)setError(cause instanceof Error ? cause.message : String(cause))}).finally(()=>{if(!abort.signal.aborted)setLoading(false)})
  return()=>abort.abort()
 },[tenantID,key,cursor,filterKey,retry])
 const update=(change:(params:URLSearchParams)=>void)=>setSearch(previous=>{const params=new URLSearchParams(previous);change(params);return params})
 const label=(field:string,value:string)=>t(`templateLibrary_${field}_${value}`)
 return <section className="space-y-3" aria-label={t("templateLibrary")}>
  <form key={filterKey} className="flex flex-wrap items-end gap-3" onSubmit={event=>{event.preventDefault();const data=new FormData(event.currentTarget);update(params=>{for(const field of Object.keys(fields)){const value=String(data.get(field) ?? "");if(value)params.set(`library_${field}`,value);else params.delete(`library_${field}`)}params.delete("library_cursor");params.delete("library_key")})}}>
   {Object.entries(fields).map(([field,values])=><label className="flex min-w-36 flex-col gap-1 text-sm" key={field}>{t(`templateLibrary_${field}`)}<select aria-label={t(`templateLibrary_${field}`)} name={field} defaultValue={search.get(`library_${field}`) ?? ""} className="min-h-10 rounded-md border bg-background px-2"><option value="">{t("all")}</option>{values.map(value=><option key={value} value={value}>{label(field,value)}</option>)}</select></label>)}
   <Button type="submit" disabled={loading}>{t("search")}</Button>
  </form>
  {key && <div className="flex flex-wrap items-center gap-3"><Button variant="outline" onClick={()=>update(params=>{params.delete("library_key");params.delete("library_cursor")})}>{t("templateLibrary")}</Button><h2 className="break-all font-medium">{key} ? {t("templateVersionHistory")}</h2></div>}
  {loading && <p role="status">{t("loading")}</p>}
  {error && <div role="alert"><p>{error}</p><Button variant="outline" onClick={()=>setRetry(value=>value+1)}>{t("templateRefresh")}</Button></div>}
  {!loading && !error && <><ul className="divide-y rounded-md border">{key ? versions.map(version=><li key={version.id}><button type="button" className="flex min-h-14 w-full flex-wrap items-center justify-between gap-3 px-4 py-3 text-left hover:bg-muted focus-visible:outline-ring" onClick={()=>onSelect(version.id)}><span>{version.key} / {version.definition.semver}</span><span className="text-sm">{label("state",version.state)}</span><code className="w-full break-all text-xs text-muted-foreground">{version.hash}</code></button></li>) : families.map(family=><li key={family.key} className="space-y-2 p-4"><div className="flex flex-wrap items-center justify-between gap-3"><h2 className="break-all font-medium">{family.name || family.key}</h2><span className="text-sm text-muted-foreground">{t("templateMatchingVersions",{count:family.version_count})}</span></div><p className="flex flex-wrap gap-3 text-sm"><span>{label("domain",family.latest.definition.domain)}</span><span>{label("origin",family.latest.origin ?? "TEAM")}</span><span>{t("templateLatestVersion")}: {family.latest.definition.semver} ? {label("state",family.latest.state)}</span></p><div className="flex flex-wrap gap-2"><Button variant="outline" onClick={()=>onSelect(family.latest.id)}>{t("templateOpenLatest")}</Button>{family.latest_published && <Button variant="outline" onClick={()=>onSelect(family.latest_published!.id)}>{t("templatePublishedVersion")}: {family.latest_published.definition.semver}</Button>}<Button variant="outline" onClick={()=>update(params=>{params.set("library_key",family.key);params.delete("library_cursor")})}>{t("templateVersionHistory")}</Button></div></li>)}</ul>
   {(key ? versions : families).length===0 && <p>{t("templateEmpty")}</p>}
   <div className="flex gap-2"><Button variant="outline" disabled={!cursor} onClick={()=>update(params=>params.delete("library_cursor"))}>{t("templateFirstPage")}</Button><Button variant="outline" disabled={!next} onClick={()=>update(params=>params.set("library_cursor",next))}>{t("templateNext")}</Button></div>
  </>}
 </section>
}
