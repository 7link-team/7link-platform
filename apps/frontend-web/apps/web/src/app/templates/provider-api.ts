import {templateRequest} from "./api"
import {iamApi} from "../../lib/iam-api"
export const providerKinds=["VIDEO_GEN","IMAGE_GEN","TTS","MUSIC_GEN","ASR_ALIGN","VLM_JUDGE","LLM_JUDGE"] as const
export type ProviderAdapter={key:string;kind:string;license_notice:string;region_notice:string;public_config_fields:string[];secret_required:boolean}
export type Provider={id:string;kind:string;adapter_key:string;display_name:string;config:Record<string,unknown>;has_secret:boolean;machine_id:string|null;state:"ENABLED"|"DISABLED"|"UNAVAILABLE";health_checked_at?:string;limitations:string;license_notice:string;region_notice:string;license_accepted_by:string;license_accepted_at:string;version:number}
export type ProviderDraft={kind:string;adapter_key:string;display_name:string;config:Record<string,unknown>;secret?:string;machine_id:string|null;accept_license_notice:boolean}
export type ProviderPatch={display_name?:string;config?:Record<string,unknown>;secret?:string;state?:"ENABLED"|"DISABLED";accept_license_notice:boolean}
export type ProviderPage<T>={items:T[];next_cursor?:string;has_more:boolean}
export type ProviderBinding={binding_id:string;project_id:string;version:number;providers:{kind:string;provider_id:string;provider_version:number}[]}
const root=(tenant:string)=>`/tenants/${encodeURIComponent(tenant)}`
const item=(tenant:string,id:string)=>`${root(tenant)}/template-providers/${encodeURIComponent(id)}`
const binding=(tenant:string,project:string)=>`${root(tenant)}/entities/${encodeURIComponent(project)}/template-binding/providers`
export const providerApi={
 adapters:(tenant:string,signal?:AbortSignal)=>templateRequest<ProviderPage<ProviderAdapter>>("GET",`${root(tenant)}/template-provider-adapters`,undefined,signal),
 list:(tenant:string,cursor:string,kind="",state="",signal?:AbortSignal)=>templateRequest<ProviderPage<Provider>>("GET",`${root(tenant)}/template-providers?${new URLSearchParams({limit:"50",sort:"id",...(cursor?{cursor}:{}),...(kind?{kind}:{}),...(state?{state}:{})})}`,undefined,signal),
 create:(tenant:string,body:ProviderDraft,key:string,signal?:AbortSignal)=>templateRequest<Provider>("POST",`${root(tenant)}/template-providers`,{...body,machine_id:body.machine_id||null},signal,key),
 patch:(tenant:string,row:Provider,body:ProviderPatch,key:string,signal?:AbortSignal)=>templateRequest<Provider>("PATCH",item(tenant,row.id),body,signal,key,row.version),
 health:(tenant:string,id:string,key:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<Provider>({method:"POST",url:`${item(tenant,id)}/health-checks`},{signal,headers:{"Idempotency-Key":key}}),
 binding:(tenant:string,project:string,signal?:AbortSignal)=>templateRequest<ProviderBinding>("GET",binding(tenant,project),undefined,signal),
 bind:(tenant:string,project:string,version:number,providers:Record<string,string>,key:string,signal?:AbortSignal)=>templateRequest<ProviderBinding>("PUT",binding(tenant,project),{providers},signal,key,version),
}
export function parseProviderConfig(text:string,adapter:ProviderAdapter){const value:unknown=JSON.parse(text);if(!value||typeof value!=="object"||Array.isArray(value)||Object.keys(value).some(key=>!adapter.public_config_fields.includes(key)))throw new Error("Invalid public provider config");return value as Record<string,unknown>}
