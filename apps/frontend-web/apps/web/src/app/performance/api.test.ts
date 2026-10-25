import {afterEach,expect,spyOn,test} from "bun:test"
import {iamApi} from "../../lib/iam-api"
import {blankScheme,performanceApi,type Revision} from "./api"
let restore:(()=>void)|undefined
afterEach(()=>restore?.())
test("scheme commands preserve immutable base revision, explicit reason and retry identity",async()=>{
 const calls:{route:unknown;init:RequestInit|undefined}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never});restore=()=>spy.mockRestore()
 const definition=blankScheme(),signal=new AbortController().signal
 definition.schedule={timezone:"Asia/Kuala_Lumpur",cadences:["WEEK","MONTH"]}
 await performanceApi.save("7",null,definition,"Initial rules","stable",signal)
 await performanceApi.save("7",null,definition,"Initial rules","stable",signal)
 expect(calls[0]).toEqual(calls[1]);expect(calls[0]?.route).toEqual({method:"POST",url:"/tenants/7/performance-schemes"})
 expect(JSON.parse(calls[0]?.init?.body as string)).toEqual({base_version:0,definition,reason:"Initial rules"})
 const base={scheme_id:"9007199254740993",version:4} as Revision
 await performanceApi.save("7",base,definition,"Threshold evidence changed","revise-key",signal)
 expect(calls[2]?.route).toEqual({method:"POST",url:"/tenants/7/performance-schemes/9007199254740993/revisions"})
 expect(JSON.parse(calls[2]?.init?.body as string).base_version).toBe(4)
 expect(calls[2]?.init?.headers).toEqual({"Idempotency-Key":"revise-key"});expect(calls[2]?.init?.signal).toBe(signal)
 await performanceApi.get("7",base.scheme_id,2,signal)
 expect(calls[3]?.route).toEqual({method:"GET",url:"/tenants/7/performance-schemes/9007199254740993?version=2"})
 await performanceApi.history("7",base.scheme_id,"3",signal)
 expect(calls[4]?.route).toEqual({method:"GET",url:"/tenants/7/performance-schemes/9007199254740993/revisions?limit=30&cursor=3"})
 await performanceApi.list("7",base.scheme_id,signal)
 expect(calls[5]?.route).toEqual({method:"GET",url:"/tenants/7/performance-schemes?limit=30&cursor=9007199254740993"})
 expect(calls.slice(3).every(call=>call.init?.body===undefined && call.init?.signal===signal)).toBe(true)
})
