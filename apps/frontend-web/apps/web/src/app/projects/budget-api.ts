import {iamApi, type ProjectDetail} from "../../lib/iam-api"
import {collaborationID} from "../../lib/clientwork-api"

export type BudgetEvent = {id:string;kind:string;cap_micros:number|null;spent_micros:number;notify_pct:number;cap_version:number;state:string;reason:string;actor_id:string|null;occurred_at:string}
export type BudgetChange = {cap_micros:number|null;notify_pct:number;reason:string;confirm_below_spent?:boolean}
export const createProjectBudgetApi = (api:Pick<typeof iamApi,"deliveryResultRequest"|"deliveryResultPage">) => ({
 history:(project:string,offset:number,signal?:AbortSignal)=>api.deliveryResultPage<BudgetEvent>({method:"GET",url:`/entities/${collaborationID(project)}/project/budget/events?${new URLSearchParams({offset:String(offset),limit:"10"})}`},signal),
 change:(project:string,version:number,body:BudgetChange,key:string)=>{
  if(!Number.isSafeInteger(version)||version<1||(body.cap_micros!==null&&(!Number.isSafeInteger(body.cap_micros)||body.cap_micros<=0||body.cap_micros>1_000_000_000_000_000))||!Number.isInteger(body.notify_pct)||body.notify_pct<50||body.notify_pct>95||Array.from(body.reason.trim()).length<10||Array.from(body.reason).length>2000)throw new Error("Invalid budget change")
  return api.deliveryResultRequest<ProjectDetail>({method:"PUT",url:`/entities/${collaborationID(project)}/project/budget`},{headers:{"If-Match":`"${version}"`,"Idempotency-Key":key},body:JSON.stringify(body)})
 },
})
export const projectBudgetApi=createProjectBudgetApi(iamApi)
