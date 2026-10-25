import { useEffect, useRef, useState } from "react"
import { useNavigate, useParams, useSearchParams } from "react-router"
import { useTranslations } from "@workspace/ui/i18n/intl-provider"
import { Button } from "@workspace/ui/components/button"
import { Input } from "@workspace/ui/components/input"
import { Textarea } from "@workspace/ui/components/textarea"
import { PageHeader } from "../../components/page-header"
import { templatesApi } from "./api"
import { useAllowed } from "../../lib/permissions"
import { WorkflowAssistant } from "./workflow-assistant"
import { WorkflowGraph } from "./graph"
import { WorkflowNodeProperties } from "./properties"
import { TemplateLibrary } from "./library"
import { TemplateDefinitionView } from "./definition-view"
import { TemplateCalibration } from "./calibration"
import { TemplateEvaluation } from "./evaluation"
import { ProgressPolicyEditor } from "./progress-policy"
import { TemplateProviders } from "./providers"
import { TemplateVersionLifecycle } from "./version-lifecycle"
import { workflowNodeKinds, newWorkflowNode, workflowIssues, softwareDefinition, connectWorkflow, removeWorkflowNode, workflowPatch, parseWorkflowProposal, type TemplateVersion, type WorkflowDef, type WorkflowInstance } from "./workflow"

export default function TemplatesPage() {
 const {tenantID = "", templateID = ""} = useParams()
 const [search] = useSearchParams()
 return <TemplatesContent key={`${tenantID}:${templateID}:${search.get("version") ?? ""}`}/>
}
function TemplatesContent() {
 const {tenantID = "", templateID: routeTemplateID} = useParams()
 const navigate = useNavigate()
 const [search, setSearch] = useSearchParams()
 const templateID = routeTemplateID || search.get("version") || ""
 const t = useTranslations("iam")
 const allowed = useAllowed()
 const [version, setVersion] = useState<TemplateVersion | null>(null)
 const [workflow, setWorkflow] = useState<WorkflowDef | null>(null)
 const [history, setHistory] = useState<WorkflowDef[]>([])
 const [future, setFuture] = useState<WorkflowDef[]>([])
 const [selected, setSelected] = useState("")
 const [json, setJson] = useState("")
 const [proposal, setProposal] = useState<{base: string; workflow: WorkflowDef} | null>(null)
 const [reason, setReason] = useState("")
 const [key, setKey] = useState("")
 const [project, setProject] = useState("")
 const [accountable, setAccountable] = useState("")
 const [busy, setBusy] = useState(false)
 const [message, setMessage] = useState("")
 const [draftDefinition] = useState(softwareDefinition)
 const pending = useRef<{fingerprint: string; key: string} | null>(null)
 const sequence = useRef(0)
 const accept = (v: TemplateVersion) => {setVersion(v);setWorkflow(v.definition.workflow);setJson(JSON.stringify(v.definition.workflow, null, 2));setHistory([]);setFuture([])}
 useEffect(() => {if(!templateID)return;const abort = new AbortController(); const current = ++sequence.current;queueMicrotask(() => {if (!abort.signal.aborted) {setBusy(true);setMessage("")}});templatesApi.get(tenantID, templateID, abort.signal).then(v=>{if(!abort.signal.aborted && sequence.current===current)accept(v)}).catch(error=>{if(!abort.signal.aborted)setMessage(error instanceof Error ? error.message : String(error))}).finally(()=>{if(!abort.signal.aborted && sequence.current===current)setBusy(false)});return()=>abort.abort()},[tenantID,templateID])
 const change = (nextWorkflow: WorkflowDef) => {if (workflow) setHistory([...history, workflow]);setFuture([]);setWorkflow(nextWorkflow);setJson(JSON.stringify(nextWorkflow, null, 2))}
 const run = async (fingerprint: string, action: (idempotencyKey: string) => Promise<void>) => {if (busy) return;if (pending.current?.fingerprint !== fingerprint) pending.current = {fingerprint, key: crypto.randomUUID()};setBusy(true);setMessage("");try {await action(pending.current.key);pending.current = null;setMessage(t("templateSaved"))}catch (error) {setMessage(error instanceof Error ? error.message : String(error))}finally {setBusy(false)}}
 const editLabels = {objective: t("templateEditObjective"), criterion: t("templateEditCriterion"), verification: t("templateEditVerification"), add: t("templateEditAdd"), remove: t("templateEditRemove"), connect: t("templateEditConnect"), disconnect: t("templateEditDisconnect"), work: t("templateEditWork"), team: t("templateEditTeam"), client: t("templateEditClient"), move: t("templateEditMove")}
 const node = workflow?.nodes.find(n => n.id === selected)
 const issues = workflow ? workflowIssues(workflow) : []
 const canEdit = version?.state === "DRAFT" && allowed("template_manage") && !busy
 return <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-4 overflow-auto"><PageHeader title={t("templates")}/><p className="text-sm text-muted-foreground">{t("templateRuntimeNotice")}</p>{message && <p role="status" className="rounded-md border p-3 text-sm">{message}</p>}
 {!version ? <><div className="flex flex-wrap items-end gap-3"><label className="space-y-1 text-sm">{t("templateKey")}<Input value={key} onChange={e => setKey(e.target.value)} maxLength={64}/></label><Button disabled={busy || !key.trim()} onClick={() => {const definition = draftDefinition;void run(`draft:${key}`, async idempotencyKey => accept(await templatesApi.draft(tenantID, key, definition, idempotencyKey)))}}>{t("templateCreate")}</Button></div>{busy && <p role="status">{t("loading")}</p>}<TemplateLibrary tenantID={tenantID} onSelect={id=>setSearch(previous=>{const next=new URLSearchParams(previous);next.set("version",id);return next})}/></> : workflow && <>
 <div className="flex flex-wrap items-center gap-2"><Button variant="outline" onClick={() => {if (routeTemplateID) {navigate(`/admin/${encodeURIComponent(tenantID)}/templates`);return}setVersion(null);setWorkflow(null);setSearch(previous => {const next = new URLSearchParams(previous);next.delete("version");return next})}}>{t("templateBack")}</Button><strong>{version.key} / {version.definition.semver} / {version.state}</strong><code className="max-w-full break-all text-xs">{version.hash}</code></div>
 <TemplateVersionLifecycle key={version.id} tenantID={tenantID} version={version} canManage={allowed("template_manage")} onChanged={next=>{accept(next);if(next.id!==version.id){if(routeTemplateID)navigate(`/admin/${encodeURIComponent(tenantID)}/templates/${next.id}/workflow`);else setSearch(previous=>{const query=new URLSearchParams(previous);query.set("version",next.id);return query})}}}/>
 <TemplateDefinitionView key={`definition:${version.id}`} version={version} calibration={<TemplateCalibration key={version.id} tenantID={tenantID} version={version} canRead={allowed("template_read")} canManage={allowed("template_manage")}/>} evaluation={<TemplateEvaluation key={version.id} tenantID={tenantID} version={version} canRead={allowed("template_read")} canManage={allowed("template_manage")}/>} providers={<TemplateProviders tenantID={tenantID} canRead={allowed("template_read")} canManage={allowed("integration_manage")}/>}/>
 <WorkflowAssistant key={`assistant:${version.id}:${version.hash}`} tenantID={tenantID} version={version} workflow={workflow} canEdit={canEdit} onProposal={(result,base)=>{setProposal({base,workflow:result.workflow});setJson(JSON.stringify(result.patch,null,2));setMessage(`${result.model} / ${result.provider_id} / v${result.provider_version} / ${result.response_sha256}`)}}/>
 <ProgressPolicyEditor value={workflow.parameters.execution_progress} disabled={!canEdit} onChange={value => {const parameters = {...workflow.parameters}; if (value === undefined) delete parameters.execution_progress; else parameters.execution_progress = value; change({...workflow, parameters})}}/>
 <WorkflowGraph workflow={workflow} selected={selected} onSelect={setSelected} onMove={canEdit ? (id,x,y) => change({...workflow,nodes:workflow.nodes.map(n => n.id===id ? {...n,x,y}:n)}) : undefined}/>
 {canEdit && <div className="flex flex-wrap items-center gap-2"><span className="text-xs text-muted-foreground">{editLabels.move}</span>{workflowNodeKinds.map(kind => <Button key={kind} variant="outline" onClick={() => {const nextNode=newWorkflowNode(kind,t(`templateNode_${kind}`),workflow.nodes.length);change({...workflow,nodes:[...workflow.nodes,nextNode]});setSelected(nextNode.id)}}>{editLabels.add}: {t(`templateNode_${kind}`)}</Button>)}</div>}
 {issues.length > 0 && <section aria-label={t("templateValidation")} className="rounded-md border p-3"><h2 className="font-medium">{t("templateValidation")}</h2><ul className="list-inside list-disc text-sm">{issues.map((issue,index)=><li key={index}><code>{issue.path}</code>: {t(issue.code)}</li>)}</ul><p className="text-xs text-muted-foreground">{t("templateValidationNotice")}</p></section>}

 <div className="grid gap-4 lg:grid-cols-[1fr_2fr]"><section className="space-y-3"><h2 className="font-medium">{t("templateProperties")}</h2>{node && <><label className="block text-sm">{t("templateNodeName")}<Input disabled={!canEdit} value={node.name} onChange={e => change({...workflow, nodes: workflow.nodes.map(n => n.id === node.id ? {...n, name: e.target.value} : n)})}/></label><p className="text-xs">{node.kind} / {node.id}</p>{canEdit && <div className="space-y-2"><WorkflowNodeProperties key={`${node.id}:${history.length}:${future.length}`} node={node} onChange={next => change({...workflow,nodes:workflow.nodes.map(n=>n.id===node.id ? next : n)})}/>{node.kind==="WORK_ITEM" && <><label className="block text-sm">{editLabels.objective}<Textarea value={node.objective ?? ""} onChange={e=>change({...workflow,nodes:workflow.nodes.map(n=>n.id===node.id ? {...n,objective:e.target.value}:n)})}/></label>{node.criteria?.map((criterion,index)=><div key={index} className="space-y-2"><Button variant="outline" onClick={()=>change({...workflow,nodes:workflow.nodes.map(n=>n.id===node.id ? {...n,criteria:n.criteria?.filter((_,i)=>i!==index)}:n)})}>{editLabels.remove}: {editLabels.criterion} {index+1}</Button><label className="block text-sm">{editLabels.criterion} {index+1}<Textarea value={criterion.text} onChange={e=>change({...workflow,nodes:workflow.nodes.map(n=>n.id===node.id ? {...n,criteria:n.criteria?.map((c,i)=>i===index ? {...c,text:e.target.value}:c)}:n)})}/></label><label className="block text-sm">{editLabels.verification}<Input value={criterion.verification_method} onChange={e=>change({...workflow,nodes:workflow.nodes.map(n=>n.id===node.id ? {...n,criteria:n.criteria?.map((c,i)=>i===index ? {...c,verification_method:e.target.value}:c)}:n)})}/></label></div>)}<Button variant="outline" disabled={(node.criteria?.length ?? 0)>=50} onClick={()=>change({...workflow,nodes:workflow.nodes.map(n=>n.id===node.id ? {...n,criteria:[...(n.criteria ?? []),{text:"",verification_method:""}]}:n)})}>{editLabels.add}: {editLabels.criterion}</Button></>}<Button variant="outline" disabled={node.id===workflow.entry_node_id} onClick={() => {change(removeWorkflowNode(workflow,node.id));setSelected("")}}>{editLabels.remove}</Button><label className="block text-sm">{editLabels.connect}<select className="ml-2 rounded-md border bg-background p-2" value="" onChange={e=>{const nextWorkflow=connectWorkflow(workflow,node.id,e.target.value);if(nextWorkflow!==workflow)change(nextWorkflow)}}><option value=""></option>{workflow.nodes.filter(n=>n.id!==node.id).map(n=><option key={n.id} value={n.id}>{n.name}</option>)}</select></label>{workflow.edges.filter(e=>e.source===node.id).map(edge=><div key={edge.id} className="space-y-2"><label className="block text-sm">{t("templateEdgeBranch")}<Input value={edge.branch ?? ""} onChange={event=>change({...workflow,edges:workflow.edges.map(e=>e.id===edge.id ? {...e,branch:event.target.value}:e)})}/></label><Button variant="outline" onClick={()=>change({...workflow,edges:workflow.edges.filter(e=>e.id!==edge.id)})}>{editLabels.disconnect}: {workflow.nodes.find(n=>n.id===edge.target)?.name}</Button></div>)}</div>}</>}<label className="block text-sm">{t("templateReason")}<Input value={reason} onChange={e => setReason(e.target.value)} maxLength={2000}/></label><div className="flex flex-wrap gap-2"><Button variant="outline" disabled={!canEdit || !history.length} onClick={() => {const previous = history.at(-1)!;setFuture([workflow, ...future]);setHistory(history.slice(0, -1));setWorkflow(previous);setJson(JSON.stringify(previous, null, 2))}}>{t("templateUndo")}</Button><Button variant="outline" disabled={!canEdit || !future.length} onClick={() => {const nextWorkflow = future[0]!;setHistory([...history, workflow]);setFuture(future.slice(1));setWorkflow(nextWorkflow);setJson(JSON.stringify(nextWorkflow, null, 2))}}>{t("templateRedo")}</Button></div>
 <Button disabled={busy || !canEdit || !reason.trim() || issues.length > 0} onClick={() => {const patch = workflowPatch(version, workflow, reason);void run(JSON.stringify(patch), async idempotencyKey => accept(await templatesApi.patch(tenantID, version.id, patch, idempotencyKey)))}}>{t("templateSave")}</Button><Button variant="outline" disabled={busy || !canEdit || issues.length > 0 || JSON.stringify(workflow) !== JSON.stringify(version.definition.workflow)} onClick={() => void run(`publish:${version.id}:${version.hash}`, async idempotencyKey => accept(await templatesApi.publish(tenantID, version.id, version.hash, idempotencyKey)))}>{t("templatePublish")}</Button>
 {version.state === "PUBLISHED" && <div className="space-y-3 border-t pt-3"><label className="block text-sm">{t("templateProject")}<Input inputMode="numeric" value={project} onChange={e => setProject(e.target.value)}/></label><label className="block text-sm">{t("templateAccountable")}<Input inputMode="numeric" value={accountable} onChange={e => setAccountable(e.target.value)}/></label><Button disabled={busy || !/^\d+$/.test(project) || !/^\d+$/.test(accountable)} onClick={() => void run(`instantiate:${version.id}:${project}:${accountable}`, async idempotencyKey => {await templatesApi.instantiate(tenantID, version.id, project, accountable, idempotencyKey)})}>{t("templateInstantiate")}</Button></div>}</section>
 <section className="space-y-2"><label className="block text-sm" htmlFor="workflow-json">{t("templateJSON")}</label><Textarea id="workflow-json" className="min-h-80 font-mono text-xs" disabled={!canEdit} value={json} onChange={e => setJson(e.target.value)}/><div className="flex flex-wrap gap-2"><Button variant="outline" disabled={!canEdit} onClick={() => {try {setProposal({base:JSON.stringify(workflow),workflow:parseWorkflowProposal(json,version,workflow)});setMessage("")}catch {setMessage(t("templateInvalidJSON"))}}}>{t("templateReviewProposal")}</Button><Button variant="outline" onClick={() => {const blob = new Blob([JSON.stringify({...version, definition: {...version.definition, workflow}}, null, 2)], {type: "application/json"});const url = URL.createObjectURL(blob);const a = document.createElement("a");a.href = url;a.download = "workflow.json";a.click();URL.revokeObjectURL(url)}}>{t("templateExport")}</Button></div>{proposal && <section className="space-y-3 rounded-md border p-3"><h2 className="font-medium">{t("templateProposalDiff")}</h2><p className="text-sm">{t("templateProposalNotice")}</p><div className="grid gap-3 xl:grid-cols-2"><div><h3>{t("templateBefore")}</h3><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{JSON.stringify(JSON.parse(proposal.base),null,2)}</pre></div><div><h3>{t("templateAfter")}</h3><pre className="max-h-80 overflow-auto whitespace-pre-wrap break-words text-xs">{JSON.stringify(proposal.workflow,null,2)}</pre></div></div>{workflowIssues(proposal.workflow).map((issue,index)=><p key={index} className="text-sm"><code>{issue.path}</code>: {t(issue.code)}</p>)}<div className="flex gap-2"><Button disabled={!canEdit || proposal.base!==JSON.stringify(workflow) || workflowIssues(proposal.workflow).length>0} onClick={()=>{change(proposal.workflow);setProposal(null);setMessage(t("templateImported"))}}>{t("templateApplyProposal")}</Button><Button variant="outline" onClick={()=>setProposal(null)}>{t("templateDiscardProposal")}</Button></div></section>}</section></div></>}
 </div>
}

export function ProjectWorkflowGraph({tenantID, projectID}: {tenantID: string; projectID: string}) {
 return <ProjectWorkflowGraphContent key={`${tenantID}:${projectID}`} tenantID={tenantID} projectID={projectID}/>
}
function ProjectWorkflowGraphContent({tenantID, projectID}: {tenantID: string; projectID: string}) {
 const t = useTranslations("iam")
 const allowed = useAllowed()
 const [instance, setInstance] = useState<WorkflowInstance | null>(null)
 const [error, setError] = useState("")
 const [refresh, setRefresh] = useState(0)
 const [advancing, setAdvancing] = useState(false)
 const pendingAdvance = useRef<string | null>(null)
 const advanceAbort = useRef<AbortController | null>(null)
 const readAbort = useRef<AbortController | null>(null)
 useEffect(() => () => advanceAbort.current?.abort(), [])
 const advance = async () => {
  if (advanceAbort.current || !allowed("project_manage")) return
  const abort = new AbortController()
  readAbort.current?.abort()
  advanceAbort.current = abort
  pendingAdvance.current ??= crypto.randomUUID()
  setAdvancing(true);setError("")
  try {
   const next = await templatesApi.advance(tenantID, projectID, pendingAdvance.current, abort.signal)
   if (!abort.signal.aborted) {setInstance(next);pendingAdvance.current = null}
  } catch (failure) {
   if (!abort.signal.aborted) setError(failure instanceof Error ? failure.message : String(failure))
  } finally {
   if (!abort.signal.aborted) {setAdvancing(false);advanceAbort.current = null}
  }
 }
 useEffect(() => {const abort = new AbortController();readAbort.current=abort;templatesApi.instance(tenantID, projectID, abort.signal).then(next=>{if(!abort.signal.aborted)setInstance(next)}).catch(error => {if (!abort.signal.aborted) setError(error instanceof Error ? error.message : String(error))});return () => abort.abort()}, [tenantID, projectID, refresh])
 return <section className="space-y-3"><div className="flex items-center justify-between"><h2 className="font-medium">{t("templateInstance")}</h2><div className="flex flex-wrap gap-2">{allowed("project_manage") && <Button disabled={!instance || advancing} aria-busy={advancing} onClick={() => void advance()}>{advancing ? t("loading") : t("templateAdvance")}</Button>}<Button variant="outline" disabled={advancing} onClick={() => {setError("");setRefresh(refresh + 1)}}>{t("templateRefresh")}</Button></div></div>{error && <p role="alert">{error}</p>}{instance ? <><code className="break-all text-xs">{instance.hash}</code><WorkflowGraph workflow={instance.workflow} runs={instance.runs}/><ul className="space-y-1 text-sm">{instance.runs.map(run => <li key={`${run.node_id}:${run.expansion_key}`}>{run.node_id}: {run.state}{run.reason ? ` / ${run.reason}` : ""}</li>)}</ul></> : !error && <p role="status">{t("loading")}</p>}</section>
}
