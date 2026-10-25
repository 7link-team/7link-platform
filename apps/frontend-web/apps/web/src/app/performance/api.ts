import {iamApi} from "../../lib/iam-api"
export type Rule={key:string;description:string;condition:{metric:string;operator:string;threshold:number};effect:string;points:number;evidence_kinds:string[]}
export type Dimension={key:string;title:string;weight_bps:number;maximum_points:number;rules:Rule[]}
export type SchemeDefinition={name:string;subject_kind:string;scope:string;subject_ids:string[];enabled:boolean;schedule?:{timezone:string;cadences:("WEEK"|"MONTH"|"QUARTER"|"YEAR")[]}|null;dimensions:Dimension[];template:{title:string;summary:string;dimension_order:string[]}}
export type Revision={id:string;scheme_id:string;version:number;definition:SchemeDefinition;sha256:string;reason:string;created_by:string;created_at:string}
export type RevisionPage={items:Revision[];next_cursor?:string;has_more:boolean}
const root=(tenant:string)=>`/tenants/${encodeURIComponent(tenant)}/performance-schemes`
const get=<T,>(url:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<T>({method:"GET",url},{signal})
export const performanceApi={
 list:(tenant:string,cursor:string,signal?:AbortSignal)=>get<RevisionPage>(`${root(tenant)}?limit=30${cursor ? `&cursor=${encodeURIComponent(cursor)}`:""}`,signal),
 get:(tenant:string,scheme:string,version?:number,signal?:AbortSignal)=>get<Revision>(`${root(tenant)}/${encodeURIComponent(scheme)}${version ? `?version=${version}`:""}`,signal),
 history:(tenant:string,scheme:string,cursor:string,signal?:AbortSignal)=>get<RevisionPage>(`${root(tenant)}/${encodeURIComponent(scheme)}/revisions?limit=30${cursor ? `&cursor=${encodeURIComponent(cursor)}`:""}`,signal),
 save:(tenant:string,base:Revision|null,definition:SchemeDefinition,reason:string,key:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<Revision>({method:"POST",url:base ? `${root(tenant)}/${encodeURIComponent(base.scheme_id)}/revisions`:root(tenant)},{signal,headers:{"Idempotency-Key":key},body:JSON.stringify({base_version:base?.version??0,definition,reason})}),
}
export const blankScheme=():SchemeDefinition=>({name:"",subject_kind:"HUMAN",scope:"",subject_ids:[],enabled:false,dimensions:[],template:{title:"",summary:"",dimension_order:[]}})
