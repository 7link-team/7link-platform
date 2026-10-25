import {expect,test} from "bun:test"
import {createAcceptanceApi} from "./acceptance-api"

test("acceptance reads preserve exact resource IDs and server-owned tenant calendar",async()=>{
 const calls:unknown[][]=[]
 const api=createAcceptanceApi({deliveryResultRequest:async(...args:unknown[])=>{calls.push(args);return {}as never}})
 const signal=new AbortController().signal
 await api.schedule("9007199254740993","9007199254740995","opaque-cursor",signal)
 await api.metrics("9007199254740993",signal)
 expect(JSON.stringify(calls[0])).toContain("9007199254740995/acceptance-reminders?limit=50&cursor=opaque-cursor")
 expect((calls[0]![1]as {signal:AbortSignal}).signal).toBe(signal)
 expect(JSON.stringify(calls[1])).toContain("acceptance-metrics?days=14")
 expect(JSON.stringify(calls[1])).not.toContain("date=")
 expect(()=>api.schedule("0","1","",signal)).toThrow()
 expect(calls).toHaveLength(2)
})
