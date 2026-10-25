import { expect, test } from "bun:test"
import {createIamApi} from "../../lib/iam-api"
import { createPipelineApi } from "./pipeline-api"
test("pipeline reads exact authorized project/work with abort and bounded paging", async()=>{
 const calls: unknown[][]=[]
 const api=createPipelineApi({deliveryResultPage:async(...args:unknown[])=>{calls.push(args);return {} as never}})
 const signal=new AbortController().signal
 await api.list("9007199254740993","9007199254740995",50,signal)
 expect(JSON.stringify(calls[0])).toContain("/entities/9007199254740993/pipeline-runs?offset=50&limit=50&work_item_id=9007199254740995")
 expect(calls[0]![1]).toBe(signal)
 expect(()=>api.list("0","",0)).toThrow();expect(()=>api.list("1","",-1)).toThrow()
 expect(calls).toHaveLength(1)
})

test("pipeline uses shared list envelope totals instead of counting a page as the whole history", async()=>{
 const server=Bun.serve({port:0,fetch:()=>Response.json({code:0,data:[],meta:{page:{total:99,offset:50,limit:50,count:0}}})})
 try {
  const api=createPipelineApi(createIamApi(`http://127.0.0.1:${server.port}`))
  expect(await api.list("1","",50)).toEqual({items:[],total:99})
 }finally{server.stop(true)}
})
