import {useEffect,useRef,useState} from "react"
import {useSearchParams} from "react-router"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {AutoForm} from "@workspace/ui/components/auto-form"
import {Button} from "@workspace/ui/components/button"
import {Alert} from "../../components/alert"
import {testReportsApi,testReportSelection,type SavedTestReport} from "./test-reports-api"

export function TestReports({project,plan,title,canManage}:{project:string;plan:string;title:string;canManage:boolean}){
 const t=useTranslations("iam"),[params,setParams]=useSearchParams()
 const reportID=params.get("report")??""
 const [revision,setRevision]=useState(0),[report,setReport]=useState<SavedTestReport|null>(null),[error,setError]=useState(""),[busy,setBusy]=useState(false)
 const [reportScope,setReportScope]=useState("")
 const attempt=useRef<{signature:string;key:string}|null>(null)
 useEffect(()=>{
  if(!reportID||!canManage)return
  const controller=new AbortController()
  void testReportsApi.get(project,reportID,controller.signal).then(row=>{if(!controller.signal.aborted){setReport(row);setReportScope(project);setError("")}}).catch(e=>{if(!controller.signal.aborted){setReport(null);setError(String(e))}})
  return()=>controller.abort()
 },[project,reportID,revision,canManage])
 const download=async()=>{setBusy(true);setError("");try{const blob=await testReportsApi.download(project,reportID);const url=URL.createObjectURL(blob);const link=document.createElement("a");link.href=url;link.download="test-report.pdf";link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}catch(e){setError(String(e))}finally{setBusy(false)}}
 if(!canManage)return null
 const current=report?.id===reportID&&reportScope===project?report:null
 return <section className="grid min-w-0 gap-3 border-t pt-4"><h3 className="font-medium">{t("testReportTitle")}</h3><p className="text-sm text-muted-foreground">{t("testReportFrozen")}</p>{error&&<Alert>{error}</Alert>}
 <AutoForm mode="edit" fields={{title:{name:t("clientTitle"),type:"text",required:true},plan_ids:{name:t("testReportPlans"),type:"textarea",required:true}}} initialData={{title,plan_ids:plan}} onSubmit={async values=>{
  if(busy)return
  setBusy(true);setError("")
  try{const ids=testReportSelection(String(values.plan_ids??""));const body={title:String(values.title??""),plan_ids:ids};const signature=JSON.stringify(body);if(attempt.current?.signature!==signature)attempt.current={signature,key:crypto.randomUUID()};const created=await testReportsApi.create(project,body.title,ids,attempt.current.key);attempt.current=null;setReport(null);setParams(old=>{const next=new URLSearchParams(old);next.set("report",created.id);return next});setRevision(v=>v+1)}catch(e){setError(String(e))}finally{setBusy(false)}
 }}><Button type="submit" disabled={busy}>{t(busy?"clientSaving":"testReportCreate")}</Button></AutoForm>
 {reportID&&<><p className="break-all text-sm">{t("testReportID")}: {reportID}</p><Button variant="outline" disabled={busy} onClick={()=>setRevision(v=>v+1)}>{t("clientRetry")}</Button>{!current&&!error&&<p role="status">{t("loading")}</p>}</>}
 {current&&<TestReportReceipt report={current}/>} {current?.state==="BUILT"&&<Button disabled={busy} onClick={()=>void download()}>{t("testReportDownload")}</Button>}
 </section>
}

export function TestReportReceipt({report}:{report:SavedTestReport}){
 const t=useTranslations("iam")
 return <section className="grid min-w-0 gap-2"><p role="status">{t(report.state==="BUILT"?"testReportBuilt":report.state==="FAILED"?"testReportFailed":"testReportPending")}</p><dl className="grid grid-cols-2 gap-2 text-sm">{(["total","passed","failed","blocked","unexecuted","human_recorded"] as const).map(key=><div key={key}><dt>{t(`testReport_${key}`)}</dt><dd>{report.summary[key]}</dd></div>)}</dl>{report.content_hash&&<p className="break-all text-xs">SHA-256: {report.content_hash}</p>}{report.file_id&&<p className="break-all text-xs">{t("testReportFile")}: {report.file_id} / {report.version_id}</p>}</section>
}
