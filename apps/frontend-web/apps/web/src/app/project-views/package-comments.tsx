import { useEffect, useState } from "react"
import type { FieldRendererProps } from "@workspace/ui/components/auto-form"
import { Button } from "@workspace/ui/components/button"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Alert } from "../../components/alert"
import { clientworkApi, type ClientPackageCommentInput, type ClientPackageThread, type ClientRefusalAnchor } from "../../lib/clientwork-api"
import { MutationForm } from "./client"
import { PackageSourceViewer, type PackageSourceSelection } from "./package-source-viewer"

export type PackageCommentArtifact={name:string;version_id?:string|null;sha256:string}

export function RefusalAnchors({ anchors }: { anchors: ClientRefusalAnchor[] }) {
  const t = useTranslations("iam")
  return <ol className="grid gap-2" aria-label={t("packageReferencedComments")}>{anchors.map(anchor => <li key={anchor.thread_id} className="grid gap-1 border-l-2 pl-3 text-sm">
    <p>{t("packageCommentThread")} {anchor.thread_id} · {anchor.anchor_kind}</p>
    <p className="break-all text-xs">{anchor.version_ref}</p>
    {anchor.anchor && <p className="text-xs">{t("packageCommentLocation")}: {anchor.anchor_kind==="CODE_LINES"?`${String(anchor.anchor.start_line??"")}–${String(anchor.anchor.end_line??"")}`:anchor.anchor_kind==="TABLE_ROW"?`${t("packageCommentRow")}: ${String(anchor.anchor.row_index??"")}`:anchor.anchor_kind==="TEXT_PARAGRAPH"?`${t("packageCommentParagraph")}: ${String(anchor.anchor.paragraph_index??"")}`:anchor.anchor_kind==="HTML_REGION"&&anchor.anchor.rect&&typeof anchor.anchor.rect==="object"?Object.entries(anchor.anchor.rect).filter(([key])=>["x","y","w","h"].includes(key)).map(([key,value])=>`${key}: ${String(value)}`).join(", "):""}</p>}
    <pre className="max-h-64 overflow-auto whitespace-pre-wrap break-words">{anchor.anchor_excerpt}</pre>
  </li>)}</ol>
}

export function PackageComments({ project, delivery, item, artifact, external, published, value, onChange, disabled, labelId }: {
  project: string; delivery: string; item: string; external: boolean; published: boolean;
  artifact?:PackageCommentArtifact;
} & Partial<FieldRendererProps>) {
  const t = useTranslations("iam")
  const [rows, setRows] = useState<ClientPackageThread[]>([])
  const [cursor, setCursor] = useState("")
  const [next, setNext] = useState("")
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [revision, setRevision] = useState(0)
  const selected = Array.isArray(value) ? value as string[] : []
  const scope = `${project}:${delivery}:${item}:${cursor}:${revision}`
  const [previous, setPrevious] = useState("")
  if (scope !== previous) { setPrevious(scope); setRows([]); setError(""); setBusy(true) }
  useEffect(() => {
    const abort = new AbortController()
    void clientworkApi.packageComments(project, delivery, item, cursor, abort.signal).then(page => {
      if (!abort.signal.aborted) { setRows(page.items); setNext(page.next_cursor ?? "") }
    }).catch(cause => { if (!abort.signal.aborted) setError(cause instanceof Error ? cause.message : t("clientLoadFailed")) }).finally(() => { if (!abort.signal.aborted) setBusy(false) })
    return () => abort.abort()
  }, [project, delivery, item, cursor, revision, t])
  return <section role="group" aria-labelledby={labelId} aria-busy={busy} className="grid min-w-0 gap-2">
    {error && <Alert>{error}</Alert>}
    {!external && <p className="text-sm text-muted-foreground">{t("packageClientCommentsReadOnly")}</p>}
    {rows.map(row => <article key={row.thread_id} className="grid gap-1 border-l-2 pl-3 text-sm">
      {onChange && <label className="flex min-h-11 items-center gap-2"><input type="checkbox" checked={selected.includes(row.thread_id)} disabled={disabled || busy || Boolean(row.converted_target_id) || (!selected.includes(row.thread_id) && selected.length >= 20)} onChange={event => onChange(event.target.checked ? [...selected, row.thread_id] : selected.filter(id => id !== row.thread_id))}/>{t("packageReferenceComment")} {row.thread_id}</label>}
      <p className="whitespace-pre-wrap break-words">{row.body}</p>
      <RefusalAnchors anchors={[row]}/>
      {row.converted_target_id && <p role="status">{t("packageCommentConverted")}</p>}
    </article>)}
    {!busy && !error && !rows.length && <p role="status">{t("clientEmpty")}</p>}
    <div className="flex flex-wrap gap-2"><Button type="button" variant="outline" disabled={busy || !cursor} onClick={() => setCursor("")}>{t("clientFirstPage")}</Button><Button type="button" variant="outline" disabled={busy || !next} onClick={() => setCursor(next)}>{t("fleetNext")}</Button><Button type="button" variant="outline" disabled={busy} onClick={() => setRevision(n => n + 1)}>{t("clientRetry")}</Button></div>
    {external && published && !onChange && <PackageCommentComposer key={`${project}:${delivery}:${item}`} project={project} delivery={delivery} item={item} artifact={artifact} saved={()=>{setCursor("");setRevision(n=>n+1)}}/>}
  </section>
}

function PackageCommentComposer({project,delivery,item,artifact,saved}:{project:string;delivery:string;item:string;artifact?:PackageCommentArtifact;saved:()=>void}){
  const t=useTranslations("iam")
  const [kind,setKind]=useState<NonNullable<ClientPackageCommentInput["anchor_kind"]>>("CODE_LINES")
  const [opened,setOpened]=useState(false)
  const [source,setSource]=useState<string|null>(null),[loadError,setError]=useState(""),[selected,setSelected]=useState<PackageSourceSelection>({})
  const scope=JSON.stringify([project,delivery,item,artifact?.version_id,artifact?.sha256,artifact?.name,opened])
  const [previous,setPrevious]=useState(scope)
  if(scope!==previous){setPrevious(scope);setSource(null);setError("");setSelected({})}
  const error=opened&&!artifact?.version_id?t("packageCommentViewerUnavailable"):loadError
  useEffect(()=>{const abort=new AbortController();if(!opened||!artifact?.version_id)return()=>abort.abort()
    void clientworkApi.packageCommentSource(project,delivery,artifact.version_id,abort.signal).then(async blob=>{const bytes=await blob.arrayBuffer();if(abort.signal.aborted)return;const digest=Array.from(new Uint8Array(await crypto.subtle.digest("SHA-256",bytes)),b=>b.toString(16).padStart(2,"0")).join("");if(digest!==artifact.sha256)throw new Error(t("packageCommentViewerUnavailable"));const text=new TextDecoder("utf-8",{fatal:true}).decode(bytes);if(text.includes("\0"))throw new Error(t("packageCommentViewerUnavailable"));if(!abort.signal.aborted)setSource(text)}).catch(cause=>{if(!abort.signal.aborted)setError(cause instanceof Error?cause.message:t("clientLoadFailed"))});return()=>abort.abort()
  },[project,delivery,item,artifact?.version_id,artifact?.sha256,artifact?.name,t,opened])
  return <details onToggle={event=>setOpened(event.currentTarget.open)}><summary className="cursor-pointer py-3">{t("packageAddClientComment")}</summary><p className="text-sm text-muted-foreground">{t("packageCommentPinnedNotice")}</p>{artifact&&<p className="break-all text-xs">{artifact.name} · {t("clientVersion")}: {artifact.version_id} · SHA-256: {artifact.sha256}</p>}
    <label className="grid gap-1 py-2">{t("packageCommentLocation")}<select className="min-h-11 rounded-md border bg-background px-3" value={kind} onChange={event=>{setKind(event.target.value as typeof kind);setSelected({})}}>{(["CODE_LINES","TABLE_ROW","TEXT_PARAGRAPH","HTML_REGION"] as const).map(value=><option key={value} value={value}>{t(`packageCommentKind${value}`)}</option>)}</select></label>
    {error&&<Alert>{error}</Alert>}{source===null&&!error&&<p role="status">{t("loadingShort")}</p>}
    {source!==null&&<PackageSourceViewer key={`viewer:${kind}`} text={source} name={artifact?.name??""} kind={kind} onPick={setSelected}/>}
    {selected.anchor_kind&&<p role="status">{t("packageCommentSelectionReady")}</p>}
    <MutationForm key={`comment:${kind}`} fields={{body:{name:t("packageCommentBody"),type:"textarea",required:true}}} label={t("packageAddClientComment")} submit={async(values,key)=>{
      const body=String(values.body??"").trim()
      if(!body||Array.from(body).length>2000)throw new Error(t("packageItemInvalid"))
      if(selected.anchor_kind!==kind||source===null)throw new Error(t("packageCommentPickFirst"))
      const input:ClientPackageCommentInput={...selected,body}
      await clientworkApi.createPackageComment(project,delivery,item,input,key);saved()
    }}/>
  </details>
}
