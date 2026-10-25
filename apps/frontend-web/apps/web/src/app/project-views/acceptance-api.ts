import { iamApi } from "../../lib/iam-api"
import { collaborationID } from "../../lib/clientwork-api"

export type AcceptanceReminder = {id:string;day:number;kind:string;due_at:string;emitted_at:string|null;cancelled_at:string|null}
export type AcceptanceMetrics = {timezone:string;calendar_version:number;days:{date:string;receipts:number;accepted:number;acceptance_rate:number|null}[]}
export const createAcceptanceApi = (api:Pick<typeof iamApi,"deliveryResultRequest">)=>({
 schedule:(project:string,id:string,cursor:string,signal?:AbortSignal)=>api.deliveryResultRequest<{items:AcceptanceReminder[];has_more:boolean;next_cursor?:string}>({method:"GET",url:`/entities/${collaborationID(project)}/delivery-packages/${collaborationID(id)}/acceptance-reminders?${new URLSearchParams({limit:"50",...(cursor?{cursor}:{})})}`},{signal}),
 metrics:(project:string,signal?:AbortSignal)=>api.deliveryResultRequest<AcceptanceMetrics>({method:"GET",url:`/entities/${collaborationID(project)}/acceptance-metrics?days=14`},{signal}),
})
export const acceptanceApi=createAcceptanceApi(iamApi)
