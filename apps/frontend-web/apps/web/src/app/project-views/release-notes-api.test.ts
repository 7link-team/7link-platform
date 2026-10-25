import {expect,test} from "bun:test"
import {createReleaseNotesApi,type ReleaseNoteDraft} from "./release-notes-api"
import {iamApi} from "../../lib/iam-api"

test("release note requests and edited adoption preserve exact version, source and retry identity",async()=>{
 const calls:{url:string;init?:RequestInit}[]=[]
 const api=createReleaseNotesApi({deliveryResultRequest:async(route,init)=>{calls.push({url:route.url,init});return [] as never}} as Pick<typeof iamApi,"deliveryResultRequest">)
 const row={id:"7",version:2,state:"DRAFT",content:"Agent text",source_fingerprint:"a".repeat(64)} as ReleaseNoteDraft
 await api.list("3","9");await api.request("3","9",4,"Summarize changes","retry-request")
 expect(calls[0].url).toBe("/entities/3/delivery-packages/9/release-note-drafts")
 expect(calls[1].init?.headers).toEqual({"If-Match":"\"4\"","Idempotency-Key":"retry-request"})
 expect(()=>api.adopt("3","9",4,row," Agent text ","retry")).toThrow()
 await api.adopt("3","9",4,row,"Human edited final","retry-adopt")
 expect(calls[2].url).toEndWith("/7/adoption")
 expect(calls[2].init?.headers).toEqual({"If-Match":"\"2\"","Idempotency-Key":"retry-adopt"})
 expect(JSON.parse(String(calls[2].init?.body))).toEqual({source_fingerprint:row.source_fingerprint,package_version:4,content:"Human edited final"})
 for(const invalid of ["bad","0","3/4"])expect(()=>api.list(invalid,"9")).toThrow()
 expect(()=>api.request("3","9",0,"note","retry")).toThrow()
 expect(()=>api.adopt("3","9",4,{...row,state:"ADOPTED"},"Human final","retry")).toThrow()
})
