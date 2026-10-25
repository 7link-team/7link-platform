import {iamApi} from "../../lib/iam-api"
export type CalibrationSample={id:string;template_version_id:string;verifier_key:string;input:unknown;expected:"PASS"|"WARN"|"FAIL";origin:string;sha256:string;created_by:string|null}
export type Calibration={id:string;template_version_id:string;template_key:string;verifier_key:string;provider_ref:string;method:"AGREEMENT"|"PROBE";machine_id:string|null;state:"UNCALIBRATED"|"CALIBRATING"|"CALIBRATED"|"DRIFTED";run_id:string;run_state:"PENDING"|"COMPLETED"|"UNAVAILABLE"|"FAILED";agreement_ppm?:number|null;target_ppm:number;sample_size:number;calibrated_at?:string;calibrated_by?:string|null;drift_checked_at?:string;next_review_at:string;version:number;limitations:string;results:{sample_id:string;verdict:string;evidence_refs:string[]}[]}
export type CalibrationPage<T>={items:T[];next_cursor?:string;has_more:boolean}
export type SampleLabel={input:unknown;expected:"PASS"|"WARN"|"FAIL"}
export type CalibrationStart={template_version_id:string;verifier_key:string;provider_ref:string;method:"AGREEMENT"|"PROBE";machine_id:string|0}
const root=(tenant:string)=>`/tenants/${encodeURIComponent(tenant)}`
const request=<T,>(method:"GET"|"POST"|"PUT",url:string,signal?:AbortSignal,body?:unknown,key?:string,version?:number)=>iamApi.deliveryResultRequest<T>({method,url},{signal,...(body===undefined?{}:{body:JSON.stringify(body),headers:{"Idempotency-Key":key!,...(version===undefined?{}:{"If-Match":`"${version}"`})}})})
const query=(cursor:string,filter:Record<string,string>)=>new URLSearchParams({limit:"50",sort:"id",...(cursor?{cursor}:{}),...Object.fromEntries(Object.entries(filter).filter(([,value])=>value))})
export const calibrationApi={
 list:(tenant:string,templateKey:string,verifierKey:string,cursor:string,signal?:AbortSignal)=>request<CalibrationPage<Calibration>>("GET",`${root(tenant)}/verifier-calibrations?${query(cursor,{template_key:templateKey,verifier_key:verifierKey})}`,signal),
 get:(tenant:string,id:string,signal?:AbortSignal)=>request<Calibration>("GET",`${root(tenant)}/verifier-calibrations/${encodeURIComponent(id)}`,signal),
 samples:(tenant:string,version:string,verifierKey:string,cursor:string,signal?:AbortSignal)=>request<CalibrationPage<CalibrationSample>>("GET",`${root(tenant)}/template-versions/${encodeURIComponent(version)}/calibration-samples?${query(cursor,{verifier_key:verifierKey})}`,signal),
 addSamples:(tenant:string,version:string,verifier_key:string,samples:SampleLabel[],key:string,signal?:AbortSignal)=>request<CalibrationPage<CalibrationSample>>("POST",`${root(tenant)}/template-versions/${encodeURIComponent(version)}/calibration-samples`,signal,{verifier_key,samples},key),
 start:(tenant:string,body:CalibrationStart,key:string,signal?:AbortSignal)=>request<Calibration>("POST",`${root(tenant)}/verifier-calibrations`,signal,{...body,machine_id:body.machine_id||null},key),
 confirm:(tenant:string,row:Calibration,accept:boolean,note:string,key:string,signal?:AbortSignal)=>request<Calibration>("PUT",`${root(tenant)}/verifier-calibrations/${encodeURIComponent(row.id)}/confirmation`,signal,{accept,note},key,row.version),
}
export function canAcceptCalibration(row:Calibration){return row.method==="AGREEMENT"&&row.state!=="CALIBRATED"&&row.run_state==="COMPLETED"&&row.agreement_ppm!=null&&row.agreement_ppm>=row.target_ppm}
export function parseCalibrationLabels(text:string):SampleLabel[]{
 const parsed:unknown=JSON.parse(text)
 if(!Array.isArray(parsed)||parsed.length<1||parsed.length>100||parsed.some(value=>!value||typeof value!=="object"||!("input"in value)||!["PASS","WARN","FAIL"].includes(value.expected)))throw new Error("Invalid labelled samples")
 return parsed.map(value=>({input:value.input,expected:value.expected}))
}
