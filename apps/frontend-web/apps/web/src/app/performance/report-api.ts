import {iamApi} from "../../lib/iam-api"
import type {Revision} from "./api"

export type Cadence="WEEK"|"MONTH"|"QUARTER"|"YEAR"
export type PeriodWindow={cadence:Cadence;timezone:string;period_start:string;period_end:string;start_at:string;end_at:string;trigger_at:string;cutoff_at:string;uncovered_start_at:string;uncovered_end_at:string}
export type EvidenceFact={id:string;kind:string;version:number;sha256:string;project_id:string;work_id?:string|null;occurred_at:string;subject_id:string;subject_kind:string;role:string;reference:string}
export type WorkSummary={work_id:string;project_id:string;title:string;completed:boolean|null;in_progress:boolean|null;delayed:boolean|null;changed:boolean|null;evidence_refs:string[];gaps:string[]}
export type EvidenceSet={facts:EvidenceFact[];metrics:{metric:string;value:number;evidence_refs:string[]}[];gaps:string[];work:WorkSummary[]}
export type RuleScore={key:string;metric:string;value:number|null;matched:boolean|null;delta:number|null;evidence_refs:string[];source:"AUTOMATIC"|"HUMAN_CALIBRATION"}
export type DimensionScore={key:string;score_bps:number|null;rules:RuleScore[]}
export type ReportView={id:string;report_id:string;version:number;sha256:string;snapshot:{scheme:Revision;subject_kind:string;subject_id:string;period:PeriodWindow;evidence:EvidenceSet;assessments:Record<string,{value:number;evidence_refs:string[];reason:string;actor_id:string;assessed_at:string}>;dimensions:DimensionScore[];total_score_bps:number|null;status:"PENDING"|"CONFIRMED"|"SUMMARY";confirmation?:{revision:number;sha256:string;actor_id:string;confirmed_at:string;reason:string}|null};reason:string;created_by:string|null;created_at:string}
export type ReportPage={items:ReportView[];has_more:boolean;next_offset?:number}
export type ScheduleStatus={pending:{scheme_id:string;subject_id:string;period:PeriodWindow}[];has_more:boolean}
export type GenerateReport={scheme_id:string;subject_id:string;cadence:Cadence;timezone:string;anchor_date:string;reason:string}
export type RecalculateReport={base_version:number;scheme_version:number;reason:string;include_uncovered?:boolean}
export type CalibrateReport={base_version:number;dimension_key:string;rule_key:string;value:number;evidence_refs:string[];reason:string}
export type ConfirmReport={base_version:number;sha256:string;reason:string}
const root=(tenant:string)=>`/tenants/${encodeURIComponent(tenant)}`
const report=(tenant:string,id:string)=>`${root(tenant)}/performance-reports/${encodeURIComponent(id)}`
const get=<T,>(url:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<T>({method:"GET",url},{signal})
const post=(url:string,body:unknown,key:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<ReportView>({method:"POST",url},{signal,headers:{"Idempotency-Key":key},body:JSON.stringify(body)})
export const reportApi={
 scheduleStatus:(tenant:string,signal?:AbortSignal)=>get<ScheduleStatus>(`${root(tenant)}/performance-schedule-status`,signal),
 list:(tenant:string,offset=0,signal?:AbortSignal,subjectKind="")=>get<ReportPage>(`${root(tenant)}/performance-periods?limit=50&offset=${offset}${subjectKind?`&subject_kind=${encodeURIComponent(subjectKind)}`:""}`,signal),
 get:(tenant:string,id:string,version?:number,signal?:AbortSignal)=>get<ReportView>(`${report(tenant,id)}${version ? `?version=${version}`:""}`,signal),
 revisions:(tenant:string,id:string,offset=0,signal?:AbortSignal)=>get<ReportPage>(`${report(tenant,id)}/revisions?limit=50&offset=${offset}`,signal),
 preview:(tenant:string,incoming:Pick<GenerateReport,"cadence"|"timezone"|"anchor_date">,signal?:AbortSignal)=>get<{current:PeriodWindow;next:PeriodWindow}>(`${root(tenant)}/performance-period-preview?${new URLSearchParams(incoming)}`,signal),
 generate:(tenant:string,body:GenerateReport,key:string,signal?:AbortSignal)=>post(`${root(tenant)}/performance-reports`,body,key,signal),
 recalculate:(tenant:string,id:string,body:RecalculateReport,key:string,signal?:AbortSignal)=>post(`${report(tenant,id)}/recalculations`,body,key,signal),
 calibrate:(tenant:string,id:string,body:CalibrateReport,key:string,signal?:AbortSignal)=>post(`${report(tenant,id)}/calibrations`,body,key,signal),
 confirm:(tenant:string,id:string,body:ConfirmReport,key:string,signal?:AbortSignal)=>post(`${report(tenant,id)}/confirmations`,body,key,signal),
}
export function canConfirmReport(report:ReportView){return report.snapshot.status==="PENDING"&&report.snapshot.total_score_bps!==null&&report.snapshot.dimensions.every(dimension=>dimension.score_bps!==null&&dimension.rules.every(rule=>rule.value!==null&&rule.matched!==null&&rule.delta!==null))}
export const performanceWorkHref=(tenant:string,project:string,work:string)=>`/admin/${encodeURIComponent(tenant)}/entities/${encodeURIComponent(project)}/work-items?work=${encodeURIComponent(work)}`
