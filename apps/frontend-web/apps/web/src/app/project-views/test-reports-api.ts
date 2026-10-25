import {iamApi} from "../../lib/iam-api"
import {collaborationID, splitReferenceIDs} from "../../lib/clientwork-api"

export type SavedTestReport = {
 id:string;title:string;state:"PENDING"|"BUILT"|"FAILED";job_id:string
 file_id:string|null;version_id:string|null;content_hash:string|null
 summary:{total:number;passed:number;failed:number;blocked:number;unexecuted:number;human_recorded:number}
 created_at:string;built_at:string|null
}
const reportPath=(project:string,id?:string)=>`/entities/${collaborationID(project)}/test-reports${id?`/${collaborationID(id)}`:""}`
export function testReportSelection(value:string):string[]{
 const ids=splitReferenceIDs(value).map(collaborationID)
 if(ids.length<1||ids.length>15||new Set(ids).size!==ids.length)throw new Error("Select 1–15 distinct plans")
 return ids
}
export const testReportsApi={
 create:(project:string,title:string,plans:string[],key:string)=>iamApi.deliveryResultRequest<{id:string}>({method:"POST",url:reportPath(project)},{headers:{"Idempotency-Key":key},body:JSON.stringify({title,plan_ids:plans})}),
 get:(project:string,id:string,signal?:AbortSignal)=>iamApi.deliveryResultRequest<SavedTestReport>({method:"GET",url:reportPath(project,id)},{signal}),
 download:(project:string,id:string)=>iamApi.deliveryResultDownload({method:"GET",url:`${reportPath(project,id)}/content`}),
}
