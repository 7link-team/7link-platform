import {expect,test} from "bun:test"
import {AttentionClock,createAttentionApi} from "./attention-api"
import {iamApi} from "../../lib/iam-api"
test("attention clock excludes hidden, blurred, initially idle and stale activity intervals",()=>{
 const clock=new AttentionClock(0,true,true)
 expect(clock.take(15000)).toBe(0)
 clock.activity(15000);expect(clock.take(30000)).toBe(15000)
 clock.presence(35000,false,true);expect(clock.take(335000)).toBe(5000)
 clock.presence(335000,true,true);expect(clock.take(425000)).toBe(0)
 clock.activity(425000);expect(clock.take(545000)).toBe(60000)
 clock.activity(545000);clock.presence(550000,true,false);expect(clock.take(565000)).toBe(5000)
 clock.presence(565000,true,true);expect(clock.take(580000)).toBe(15000)
})
test("attention API sends exact sequence delta and close flag with stable create retry identity",async()=>{
 const calls:{url:string;init?:RequestInit}[]=[]
 const api=createAttentionApi({deliveryResultRequest:async(route,init)=>{calls.push({url:route.url,init});return {} as never}} as Pick<typeof iamApi,"deliveryResultRequest">)
 await api.start("99","7","8","retry");await api.beat("99","7","9",2,15000,true)
 expect(calls[0].url).toBe("/entities/99/work-items/7/attention-sessions");expect(calls[0].init?.headers).toEqual({"Idempotency-Key":"retry"})
 expect(JSON.parse(String(calls[0].init?.body))).toEqual({claim_id:"8",surface:"REVIEW_DESK"})
 expect(calls[1].url).toEndWith("/9");expect(calls[1].init?.keepalive).toBe(true);expect(JSON.parse(String(calls[1].init?.body))).toEqual({sequence:2,active_ms_delta:15000,ended:true})
 for(const [seq,delta] of [[0,1],[1,-1],[1,1.5]])expect(()=>api.beat("99","7","9",seq,delta,false)).toThrow()
 expect(()=>api.start("99","7","invalid","retry")).toThrow()
})
