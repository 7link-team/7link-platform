import {expect,test} from "bun:test"
import {createProjectBudgetApi} from "./budget-api"

test("budget commands preserve exact request identity, concurrency version and nullable cap",async()=>{
 const calls:unknown[][]=[]
 const api=createProjectBudgetApi({deliveryResultRequest:async(...args:unknown[])=>{calls.push(args);return {} as never},deliveryResultPage:async(...args:unknown[])=>{calls.push(args);return {items:[],total:0} as never}})
 const body={cap_micros:100,notify_pct:80,reason:"Reviewed cost ceiling"}
 await api.change("9007199254740993",3,body,"same-key");await api.change("9007199254740993",3,body,"same-key")
 expect(calls[0]).toEqual(calls[1]);expect(JSON.stringify(calls[0])).toContain("9007199254740993");expect(JSON.stringify(calls[0])).toContain("same-key")
 await api.change("3",4,{...body,cap_micros:null},"remove");expect(JSON.stringify(calls[2])).toContain('cap_micros\\":null')
 await api.history("3",10);expect(JSON.stringify(calls[3])).toContain("offset=10&limit=10")
 for(const bad of [{...body,cap_micros:0},{...body,cap_micros:Number.MAX_SAFE_INTEGER+1},{...body,notify_pct:100},{...body,reason:"short"}])expect(()=>api.change("3",1,bad,"bad")).toThrow()
 expect(calls).toHaveLength(4)
})
