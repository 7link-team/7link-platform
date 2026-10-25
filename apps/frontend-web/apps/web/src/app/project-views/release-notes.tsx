import {useEffect,useState} from "react"
import {useTranslations} from "@workspace/ui/i18n/intl-provider"
import {Button} from "@workspace/ui/components/button"
import {Alert} from "../../components/alert"
import {MutationForm} from "./client"
import {releaseNotesApi,type ReleaseNoteDraft} from "./release-notes-api"

export function ReleaseNoteHistory({rows,onAdopt}:{rows:ReleaseNoteDraft[];onAdopt?:(row:ReleaseNoteDraft,text:string,key:string)=>Promise<void>}) {
  const t=useTranslations("iam")
  return <div className="grid gap-4">{rows.map(row=><section className="grid gap-2 border-b pb-4" key={row.id}>
    <p>{t("releaseNoteAgent")}: {row.drafter_principal_id} · {row.state} · {row.created_at}</p>
    <p>{row.note}</p><code className="break-all text-xs">{row.source_fingerprint}</code>
    {row.content&&<p className="whitespace-pre-wrap">{row.content}</p>}
    {row.state==="DRAFT"&&onAdopt&&<MutationForm initialData={{content:row.content??""}} fields={{content:{name:t("releaseNoteFinal"),type:"textarea",required:true}}} label={t("releaseNoteAdopt")} submit={(values,key)=>onAdopt(row,String(values.content??""),key)}/>}
  </section>)}</div>
}
export function ReleaseNotes({project,id,version,onChanged}:{project:string;id:string;version:number;onChanged:()=>void}) {
  const t=useTranslations("iam"),[rows,setRows]=useState<ReleaseNoteDraft[]>([]),[error,setError]=useState(""),[revision,setRevision]=useState(0)
  useEffect(()=>{const controller=new AbortController();void releaseNotesApi.list(project,id,controller.signal).then(setRows).catch(e=>{if(!controller.signal.aborted)setError(e instanceof Error?e.message:t("clientLoadFailed"))});return()=>controller.abort()},[project,id,version,revision,t])
  return <section className="grid gap-3"><h3>{t("releaseNoteTitle")}</h3><p>{t("releaseNoteHelp")}</p>{error&&<Alert>{error}</Alert>}
    <MutationForm fields={{note:{name:t("releaseNoteRequestNote"),type:"textarea",required:true}}} label={t("releaseNoteRequest")} submit={async(values,key)=>{await releaseNotesApi.request(project,id,version,String(values.note??""),key);setRevision(n=>n+1)}}/>
    <Button variant="outline" onClick={()=>setRevision(n=>n+1)}>{t("releaseNoteRefresh")}</Button>
    <ReleaseNoteHistory rows={rows} onAdopt={async(row,text,key)=>{await releaseNotesApi.adopt(project,id,version,row,text,key);await onChanged();setRevision(n=>n+1)}}/>
  </section>
}
