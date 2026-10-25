import {useEffect,useRef,useState} from "react"
import {useLocale} from "@workspace/ui/i18n/intl-provider"
import {Button} from "@workspace/ui/components/button"
import {iamApi,type WorkItem} from "../../lib/iam-api"
import {clientworkApi,type ClientRecords} from "../../lib/clientwork-api"
import {Alert} from "../../components/alert"
import {testCaseImportLabels} from "./test-case-import-labels"
import {importTestCaseCSV,testCaseFailureCSV,testCaseCSVColumns,type TestCaseImportReport} from "./test-case-import-api"

type CaseMetadata={requirement_id?:string;criterion_id?:string;criterion_source?:string;preconditions?:string;priority?:string;unclassified?:boolean}
function download(name:string,text:string){const url=URL.createObjectURL(new Blob([text],{type:"text/csv;charset=utf-8"}));const a=document.createElement("a");a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}

export function TestCaseImportReportView({report,locale}:{report:TestCaseImportReport;locale:string}){
 const l=testCaseImportLabels(locale)
 const names:Record<string,string>={title:l.titleColumn,criterion:l.criterion,preconditions:l.preconditions,steps:l.steps,expected:l.expected,priority:l.priority}
 return <div className="grid min-w-0 gap-3">
  <p role="status">{report.errors.length?l.issues:report.preview?l.noWrites:`${l.done}: ${report.imported}`}</p>
  {report.unclassified>0&&<p className="border-l-2 border-amber-500 pl-3 text-sm">{l.unclassified} ({report.unclassified})</p>}
  {report.errors.length>0&&<ul className="list-disc pl-5">{report.errors.map((issue,i)=><li key={i}>{l.row} {issue.row} / {names[issue.column]??issue.column}: {l.codes[issue.code as keyof typeof l.codes]??issue.code}</li>)}</ul>}
  <div className="max-h-96 min-w-0 overflow-auto"><table className="w-full text-left text-sm"><thead><tr><th className="p-2">{l.row}</th>{report.columns.map((column,i)=><th key={`${column}:${i}`} className="p-2">{names[column]??column}</th>)}<th className="p-2">{l.outcome}</th></tr></thead><tbody>{report.rows.map(row=><tr key={row.row} className="border-t"><td className="p-2 align-top">{row.row}</td>{row.source.map((cell,i)=><td key={i} className="max-w-64 whitespace-pre-wrap break-words p-2 align-top">{cell}</td>)}<td className="p-2 align-top">{row.case_id?`${l.caseID} ${row.case_id} / ${l.version} ${row.version}`:report.errors.some(issue=>issue.row===row.row)?l.issues:row.unclassified?l.unclassified:l.mapped}</td></tr>)}</tbody></table></div>
 </div>
}

export function TestCaseMetadata({project,row,canEdit=false,onUpdated}:{project:string;row:ClientRecords["cases"]&CaseMetadata;canEdit?:boolean;onUpdated?:()=>void}){
 const locale=useLocale(),l=testCaseImportLabels(locale)
 const [choices,setChoices]=useState<{id:string;text:string}[]>([]),[criterion,setCriterion]=useState(row.criterion_id??""),[busy,setBusy]=useState(false),[error,setError]=useState("")
 const pending=useRef<{criterion:string;key:string}|null>(null)
 useEffect(()=>{if(!row.requirement_id||!canEdit)return;const c=new AbortController();void iamApi.requirement(project,row.requirement_id,c.signal).then(result=>{if(!c.signal.aborted)setChoices((result.criteria??[]).map(v=>({id:v.id!,text:v.text})))}).catch(e=>{if(!c.signal.aborted)setError(String(e))});return()=>c.abort()},[project,row.requirement_id,canEdit])
 if(!row.requirement_id)return null
 const save=async()=>{if(busy||!criterion)return;setBusy(true);setError("");if(pending.current?.criterion!==criterion)pending.current={criterion,key:crypto.randomUUID()};try{await clientworkApi.command(project,"cases",{...row,criterion_id:criterion},pending.current.key,row.id!,undefined,row.version,true);pending.current=null;onUpdated?.()}catch(e){setError(String(e))}finally{setBusy(false)}}
 return <section className="grid gap-2 border-l-2 pl-3"><p>{l.requirement}: {row.requirement_id} / {l.priority}: {row.priority}</p><p className="whitespace-pre-wrap">{l.preconditions}: {row.preconditions}</p><p>{l.criterion}: {row.criterion_source}{row.criterion_id?` / ${row.criterion_id}`:""}</p>{row.unclassified&&<p role="status">{l.unclassified}</p>}{error&&<Alert>{error}</Alert>}{canEdit&&<><label className="grid gap-1">{l.criterion}<select className="min-h-11 rounded border bg-background p-2" value={criterion} disabled={busy} onChange={e=>setCriterion(e.target.value)}><option value="">{l.criterion}</option>{choices.map(c=><option key={c.id} value={c.id}>{c.text}</option>)}</select></label><Button type="button" disabled={busy||!criterion||criterion===row.criterion_id} onClick={()=>void save()}>{busy?l.busy:l.classify}</Button></>}</section>
}

export function TestCaseImport({project,onImported}:{project:string;onImported:()=>void}){
 const locale=useLocale(),l=testCaseImportLabels(locale)
 const [works,setWorks]=useState<WorkItem[]>([]),[page,setPage]=useState(0),[total,setTotal]=useState(0),[work,setWork]=useState("")
 const [file,setFile]=useState<File|null>(null),[report,setReport]=useState<TestCaseImportReport|null>(null),[busy,setBusy]=useState(false),[loading,setLoading]=useState(true),[error,setError]=useState(""),[loadRevision,setLoadRevision]=useState(0)
 const pending=useRef<string|null>(null),active=useRef(true),errorRef=useRef<HTMLDivElement>(null)
 useEffect(()=>{active.current=true;return()=>{active.current=false}},[])
 useEffect(()=>{const c=new AbortController();void iamApi.workItems(project,{offset:page*20,limit:20},c.signal).then(result=>{if(!c.signal.aborted){setWorks(result.items);setTotal(result.total)}}).catch(e=>{if(!c.signal.aborted)setError(String(e))}).finally(()=>{if(!c.signal.aborted)setLoading(false)});return()=>c.abort()},[project,page,loadRevision])
 const selected=works.find(row=>row.id===work)
 const reset=()=>{setReport(null);setError("");pending.current=null}
 const submit=async(preview:boolean)=>{
  if(busy||!file||!selected?.requirement_id)return
  setBusy(true);setError("");pending.current??=crypto.randomUUID()
  try{const result=await importTestCaseCSV(project,work,selected.requirement_id,file,preview,pending.current);if(!active.current)return;setReport(result);if(result.imported>0){pending.current=null;onImported()}}
  catch(e){if(active.current){setError(e instanceof Error?e.message:l.failed);setTimeout(()=>errorRef.current?.focus(),0)}}finally{if(active.current)setBusy(false)}
 }
 return <details className="grid min-w-0 gap-3 rounded border p-3"><summary className="min-h-11 cursor-pointer py-2 font-medium">{l.title}</summary><div className="grid min-w-0 gap-3" aria-busy={busy||loading}>
  <p className="text-sm text-muted-foreground">{l.help}</p>
  <label className="grid gap-1">{l.chooseWork}<select className="min-h-11 rounded border bg-background p-2" disabled={busy||loading} value={work} onChange={e=>{setWork(e.target.value);reset()}}><option value="">{l.chooseWork}</option>{works.map(row=><option key={row.id} value={row.id??""} disabled={!row.requirement_id||!row.id}>{row.title}</option>)}</select></label>
  {!loading&&!works.length&&<p>{l.empty}</p>}{selected?.requirement_id&&<p className="text-sm">{l.requirement}: {selected.requirement_id}</p>}
  <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={busy||loading||page===0} onClick={()=>{setLoading(true);setWork("");reset();setPage(p=>p-1)}}>{l.previous}</Button><Button type="button" variant="outline" disabled={busy||loading||(page+1)*20>=total} onClick={()=>{setLoading(true);setWork("");reset();setPage(p=>p+1)}}>{l.next}</Button></div>
  <label className="grid gap-1">{l.file}<input type="file" accept=".csv,text/csv" disabled={busy} className="min-h-11 max-w-full rounded border p-2" onChange={e=>{reset();const next=e.target.files?.[0]??null;if(next&&next.size>2*1024*1024){setFile(null);setError(l.tooLarge)}else setFile(next)}}/></label>
  {error&&<div ref={errorRef} tabIndex={-1}><Alert>{error}</Alert><Button type="button" variant="outline" disabled={busy||loading} onClick={()=>{setLoading(true);setError("");setLoadRevision(v=>v+1)}}>{l.retry}</Button></div>}
  <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={busy} onClick={()=>download("test-case-template.csv","\ufeff"+testCaseCSVColumns.join(",")+"\r\n")}>{l.template}</Button><Button type="button" disabled={busy||!file||!selected?.requirement_id} onClick={()=>void submit(true)}>{busy?l.busy:l.preview}</Button><Button type="button" disabled={busy||!report?.preview||report.errors.length>0||!file||!selected?.requirement_id} onClick={()=>void submit(false)}>{l.import}</Button>{report&&report.errors.length>0&&<Button type="button" variant="outline" onClick={()=>download("test-case-import-errors.csv",testCaseFailureCSV(report))}>{l.failures}</Button>}</div>
  {report&&<TestCaseImportReportView report={report} locale={locale}/>}
 </div></details>
}
