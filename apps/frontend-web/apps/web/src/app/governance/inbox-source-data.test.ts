import { afterEach, expect, it, spyOn } from "bun:test"
import { iamApi } from "../../lib/iam-api"
import { decideInboxSource, loadInboxSource } from "./inbox-source-data"
import type { InboxItem } from "./api"

afterEach(()=>{request.mockRestore();page.mockRestore()})
let request=spyOn(iamApi,"deliveryResultRequest"),page=spyOn(iamApi,"deliveryResultPage")
const item=(kind:InboxItem["kind"]):InboxItem=>({id:"101",object_id:"102",project_id:"9",kind,priority:"NORMAL",title:"Decision",created_at:"2026-10-23T00:00:00Z",object_type:"project"})
it("uses canonical source methods, exact current versions and frozen plan hash with a caller-owned replay key",async()=>{
 request=spyOn(iamApi,"deliveryResultRequest").mockResolvedValue({})
 for(const [kind,method,suffix,field,yes,no] of [
  ["MEMORY_RULE_APPROVAL","POST","/reviews","action","APPROVED","REJECTED"],
  ["DISPATCH_PROPOSAL","POST","/decisions","decision","CONFIRM","DISMISS"],
  ["ENSEMBLE_APPROVAL","POST","/decisions","action","APPROVE","ABANDON"],
  ["AGENT_SPAWN_APPROVAL","PUT","/state","action","CREATE","REJECT"],
  ["PLAN_APPROVAL","POST","/decisions","action","APPROVE","REJECT"],
  ["QUALITY_ESCAPE","PUT","/learning","state","RECORDED","DISMISSED"],
  ["PREVIEW_SHARE_APPROVAL","POST","/decisions","decision","APPROVE","REJECT"],
 ] as const){
  for(const action of ["A","R"] as const){await decideInboxSource("7",item(kind),{source:{id:"102",version:4,content_hash:"frozen-hash"},path:"/source/102"},action,"Human review reason","same-request-key")
   const [route,init]=request.mock.calls.at(-1)!;expect(route.method).toBe(method);expect(route.url.endsWith(suffix)).toBe(true);expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("same-request-key")
   const body=JSON.parse(String(init?.body));expect(body[field]).toBe(action==="A"?yes:no)
   if(kind==="PLAN_APPROVAL"){expect(body.version).toBe(4);expect(body.content_hash).toBe("frozen-hash");expect(body.feedback).toBe("Human review reason")}
   else if(kind!=="MEMORY_RULE_APPROVAL")expect(new Headers(init?.headers).get("If-Match")).toBe('"4"')
  }
 }
})
it("loads exact later-page review plans and never substitutes the first visible plan",async()=>{
 page=spyOn(iamApi,"deliveryResultPage").mockResolvedValueOnce({items:Array.from({length:100},(_,i)=>({id:String(i+500),version:1})),total:101}).mockResolvedValueOnce({items:[{id:"102",version:8,content_hash:"current"}],total:101})
 const loaded=await loadInboxSource("7",item("PLAN_APPROVAL"),new AbortController().signal)
 expect(loaded.source.id).toBe("102");expect(loaded.source.version).toBe(8);expect(page).toHaveBeenCalledTimes(2)
 expect(page.mock.calls[1]![0].url).toContain("offset=100")
})
it("finds a later-page worklog through the canonical cursor list and rejects that exact version",async()=>{
 request=spyOn(iamApi,"deliveryResultRequest").mockResolvedValueOnce({items:[{id:"500",version:1,project_id:"9"}],has_more:true,next_cursor:"500"}).mockResolvedValueOnce({items:[{id:"102",version:8,project_id:"9",state:"SUBMITTED"}],has_more:false}).mockResolvedValueOnce({})
 const signal=new AbortController().signal,loaded=await loadInboxSource("7",item("WORKLOG_APPROVAL"),signal)
 expect(loaded.source.id).toBe("102");expect(loaded.source.version).toBe(8);expect(loaded.path).toBe("/tenants/7/projects/9/worklogs/102")
 expect(request.mock.calls[0]![0]).toEqual({method:"GET",url:"/tenants/7/projects/9/worklogs?limit=100"})
 expect(request.mock.calls[1]![0]).toEqual({method:"GET",url:"/tenants/7/projects/9/worklogs?limit=100&cursor=500"})
 expect(request.mock.calls[1]![1]?.signal).toBe(signal)
 await decideInboxSource("7",item("WORKLOG_APPROVAL"),loaded,"R","Return this exact record","worklog-retry")
 const [route,init]=request.mock.calls[2]!
 expect(route).toEqual({method:"PUT",url:"/tenants/7/projects/9/worklogs/102/state"})
 expect(new Headers(init?.headers).get("If-Match")).toBe('"8"');expect(new Headers(init?.headers).get("Idempotency-Key")).toBe("worklog-retry")
 expect(JSON.parse(String(init?.body))).toEqual({state:"REJECTED",reason:"Return this exact record"})
})
it("fails closed when worklog pagination cycles, finishes without the target, or returns a foreign project",async()=>{
 request=spyOn(iamApi,"deliveryResultRequest").mockResolvedValue({items:[{id:"500",version:1}],has_more:true,next_cursor:"500"})
 await expect(loadInboxSource("7",item("WORKLOG_APPROVAL"),new AbortController().signal)).rejects.toThrow("no longer available")
 expect(request).toHaveBeenCalledTimes(2)
 request.mockReset().mockResolvedValue({items:[],has_more:false})
 await expect(loadInboxSource("7",item("WORKLOG_APPROVAL"),new AbortController().signal)).rejects.toThrow("no longer available")
 request.mockReset().mockResolvedValue({items:[{id:"102",version:8,project_id:"88"}],has_more:false})
 await expect(loadInboxSource("7",item("WORKLOG_APPROVAL"),new AbortController().signal)).rejects.toThrow("another project")
})
it("rejects a cross-project source and preserves failed transport errors without a synthetic decision",async()=>{
 request=spyOn(iamApi,"deliveryResultRequest").mockResolvedValue({id:"102",version:2,project_id:"88"})
 await expect(loadInboxSource("7",item("DISPATCH_PROPOSAL"),new AbortController().signal)).rejects.toThrow("another project")
 request.mockRejectedValue(new Error("authority revoked"))
 await expect(decideInboxSource("7",item("QUALITY_ESCAPE"),{source:{id:"102",version:2},path:"/source"},"R","reason","retry-key")).rejects.toThrow("authority revoked")
})
it("keeps OPEN actions separate from worklog return and requirement-conflict resolution",async()=>{
 request=spyOn(iamApi,"deliveryResultRequest").mockResolvedValue({})
 for(const kind of ["WORKLOG_APPROVAL","REQUIREMENT_CONFLICT"] as const){const loaded={source:{id:"102",version:7},path:"/source"};await expect(decideInboxSource("7",item(kind),loaded,"A","reason","key")).rejects.toThrow();await decideInboxSource("7",item(kind),loaded,"R","exact reason","key")}
 expect(JSON.parse(String(request.mock.calls.at(-1)![1]?.body))).toEqual({version:7,action:"RESOLVE",reason:"exact reason"})
})
it("never implicitly acknowledges memory conflicts and forwards only the explicit acknowledgement",async()=>{
 request=spyOn(iamApi,"deliveryResultRequest").mockResolvedValue({})
 const loaded={source:{id:"102",version:7,conflict_memory_ids:["303"]},path:"/source"}
 await decideInboxSource("7",item("MEMORY_RULE_APPROVAL"),loaded,"A","Reviewed conflicts","unacknowledged")
 await decideInboxSource("7",item("MEMORY_RULE_APPROVAL"),loaded,"A","Reviewed conflicts","acknowledged",true)
 for(const [index,acknowledged] of [false,true].entries()){
  const [route,init]=request.mock.calls[index]!
  expect(route.method).toBe("POST");expect(route.url).toBe("/tenants/7/knowledge/102/reviews")
  expect(JSON.parse(String(init?.body))).toEqual({version:7,action:"APPROVED",reason:"Reviewed conflicts",conflicts_acknowledged:acknowledged})
  expect(new Headers(init?.headers).get("Idempotency-Key")).toBe(acknowledged?"acknowledged":"unacknowledged")
 }
})
