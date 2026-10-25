import {templateRequest} from "./api"
export type EvalFileRef={file_id:string;version_id:string;sha256:string;binding_id?:string|null}
export type EvalExpected=Record<string,"PASS"|"WARN"|"FAIL">
export type EvalCase={id:string;template_version_id:string;template_key:string;project_id:string;title:string;kind:"JUDGEMENT"|"TOOL_PROBE";origin:"SEED"|"MANUAL"|"ESCAPE";input_ref:EvalFileRef[];expected:EvalExpected;source_escape_id:string|null;source_snapshot:unknown;active:boolean;version:number;created_by:string|null;created_at:string}
export type EvalDraft=Pick<EvalCase,"template_version_id"|"template_key"|"project_id"|"title"|"kind"|"input_ref"|"expected">
export type EvalPatch=Pick<EvalCase,"title"|"expected"|"active">
export type EvalTarget={verifier_key:string;provider_ref:string;machine_id:string|0|null;agent_id?:string;role?:"ACCEPTOR";model?:string;configuration_sha256?:string}
export type EvalRun={id:string;template_version_id:string;template_key:string;state:"RUNNING"|"COMPLETED"|"UNAVAILABLE"|"FAILED";snapshot:{template_version_id:string;definition_sha256:string;target:EvalTarget;verifier:Record<string,unknown>;cases:EvalCase[]};sha256:string;results:{sample_id:string;verdict:string;evidence_refs:string[]}[];cases_total:number;false_positives?:number;false_negatives?:number;passed:number;failed:number;inconclusive:number;agreement_ppm:number|null;limitations:string;created_by:string|null;started_at:string;finished_at:string|null}
export type EvalPage<T>={items:T[];next_cursor?:string;has_more:boolean}
const root=(tenant:string)=>`/tenants/${encodeURIComponent(tenant)}`
const query=(key:string,cursor:string,filters:Record<string,string>={})=>new URLSearchParams({limit:"50",sort:"id",template_key:key,...(cursor?{cursor}:{}),...Object.fromEntries(Object.entries(filters).filter(([,value])=>value))})
export const evalApi={
 cases:(tenant:string,key:string,cursor:string,origin:string,active:string,signal?:AbortSignal)=>templateRequest<EvalPage<EvalCase>>("GET",`${root(tenant)}/eval-cases?${query(key,cursor,{origin,active})}`,undefined,signal),
 create:(tenant:string,body:EvalDraft,retry:string,signal?:AbortSignal)=>templateRequest<EvalCase>("POST",`${root(tenant)}/eval-cases`,body,signal,retry),
 patch:(tenant:string,row:EvalCase,body:EvalPatch,retry:string,signal?:AbortSignal)=>templateRequest<EvalCase>("PATCH",`${root(tenant)}/eval-cases/${encodeURIComponent(row.id)}`,body,signal,retry,row.version),
 runs:(tenant:string,key:string,cursor:string,signal?:AbortSignal)=>templateRequest<EvalPage<EvalRun>>("GET",`${root(tenant)}/eval-runs?${query(key,cursor)}`,undefined,signal),
 run:(tenant:string,id:string,signal?:AbortSignal)=>templateRequest<EvalRun>("GET",`${root(tenant)}/eval-runs/${encodeURIComponent(id)}`,undefined,signal),
 start:(tenant:string,template_key:string,template_version_id:string,target:EvalTarget,retry:string,signal?:AbortSignal)=>templateRequest<EvalRun>("POST",`${root(tenant)}/eval-runs`,{template_key,template_version_id,target},signal,retry),
}
export function parseEvalExpected(text:string):EvalExpected{const value:unknown=JSON.parse(text);if(!value||typeof value!=="object"||Array.isArray(value)||Object.keys(value).length===0||Object.values(value).some(verdict=>!["PASS","WARN","FAIL"].includes(verdict)))throw new Error("Invalid expected verdicts");return value as EvalExpected}
export function parseEvalRefs(text:string):EvalFileRef[]{const value:unknown=JSON.parse(text);if(!Array.isArray(value)||!value.length||value.length>20||value.some(ref=>!ref||typeof ref!=="object"||typeof ref.file_id!=="string"||!/^[1-9][0-9]*$/.test(ref.file_id)||typeof ref.version_id!=="string"||!/^[1-9][0-9]*$/.test(ref.version_id)||typeof ref.sha256!=="string"||!/^[a-f0-9]{64}$/.test(ref.sha256)))throw new Error("Invalid pinned file references");return value.map(ref=>({file_id:ref.file_id,version_id:ref.version_id,sha256:ref.sha256}))}

export function buildEvalTarget(verifier: string, provider: string, machine: string, agent: string, model: string): EvalTarget {
 const target: EvalTarget = {verifier_key: verifier, provider_ref: provider, machine_id: machine || 0}
 if (agent || model) {
  if (!/^[1-9][0-9]*$/.test(agent) || !model.trim() || model.length > 200) throw new Error("Invalid Agent target")
  target.agent_id = agent; target.role = "ACCEPTOR"; target.model = model
 }
 return target
}
