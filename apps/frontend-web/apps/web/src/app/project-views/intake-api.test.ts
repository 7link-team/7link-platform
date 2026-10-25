import { expect, it } from "bun:test"
import { createIamApi } from "../../lib/iam-api"
import { createIntakeChangeApi, intakeConversion, type IntakeChangeSource, type IntakeLedgerSource } from "./intake-api"

const source: IntakeChangeSource = {
  id: "9007199254740993", project_id: "3", requirement_id: "9007199254740995", original_intake_id: "9007199254740997", change_id: "9007199254740999",
  state: "TRIAGED", version: 4, card_revision: 2, proposal_hash: "a".repeat(64), proposal: "{}", impact: "{}",
}

it("reads bounded ledger pages and rejects foreign source/card bindings without rounding identifiers", async () => {
  const original: IntakeLedgerSource = { disposition_delivery: { state: "NOT_REQUESTED" }, id: source.id, project_id: "3", ref_no: 1, title: "Original <script>", kind: "CHANGE", state: "TRIAGED", version: 4, card_revision: 2, received_at: "2026-10-21T00:00:00Z", raw_content: "Original source", raw_content_format: "PLAIN", raw_content_hash: "b".repeat(64), card: source }
  let answer: unknown = { items: [original], has_more: true, next_cursor: "opaque+/=" }
  const seen: string[] = []
  const server = Bun.serve({ port: 0, fetch(request) { seen.push(request.url); return Response.json({ code: 0, data: answer }) } })
  try {
    const api = createIntakeChangeApi(createIamApi(`http://127.0.0.1:${server.port}`))
    const page = await api.list("3", "opaque+/=")
    expect(page.items?.[0]?.id).toBe("9007199254740993")
    const request = new URL(seen[0]!)
    expect(request.pathname).toBe("/entities/3/intakes")
    expect(request.searchParams.get("limit")).toBe("50")
    expect(request.searchParams.get("cursor")).toBe("opaque+/=")
    answer = original
    expect(await api.source("3", source.id!)).toEqual(original)
    for (const changed of [{ id: "4" }, { project_id: "4" }, { card: { ...source, id: "4" } }, { card: { ...source, project_id: "4" } }, { card: { ...source, card_revision: 9 } }]) {
      answer = { ...original, ...changed }
      await expect(api.source("3", source.id!)).rejects.toThrow("Invalid Intake project source")
    }
    answer = { items: [{ ...original, project_id: "4" }], has_more: false }
    await expect(api.list("3", "")).rejects.toThrow("Invalid Intake project source")
    const controller = new AbortController(); controller.abort()
    await expect(api.source("3", source.id!, controller.signal)).rejects.toThrow()
  } finally { await server.stop(true) }
})

it("freezes only source identifiers and converts the exact immutable card with replay and revision fences", async () => {
  const seen: { path: string; method: string; body: unknown; key: string | null; version: string | null }[] = []
  const server = Bun.serve({ port: 0, async fetch(request) {
    const path = new URL(request.url).pathname
    seen.push({ path, method: request.method, body: request.method === "POST" ? await request.json() : null, key: request.headers.get("Idempotency-Key"), version: request.headers.get("If-Match") })
    return Response.json({ code: 0, data: request.method === "GET" ? source : { id: source.id, version: 5, state: "CONVERTED" } })
  } })
  try {
    const api = createIntakeChangeApi(createIamApi(`http://127.0.0.1:${server.port}`))
    await api.draft("3", source.original_intake_id!, source.change_id!, "freeze-key")
    const frozen = await api.get("3", source.id!)
    await api.convert("3", frozen, "proof-for-exact-requirement", "conversion-key")
    expect(seen[0]!.body).toEqual({ original_intake_id: "9007199254740997", change_id: "9007199254740999" })
    expect(seen[0]!.key).toBe("freeze-key")
    expect(seen[1]!.path).toBe("/entities/3/intake-changes/9007199254740993")
    expect(seen[2]).toEqual({ path: "/entities/3/intake-changes/9007199254740993/conversion", method: "POST", key: "conversion-key", version: '"4"', body: { triage_card_revision: 2, proposal_hash: "a".repeat(64), reauth_proof: "proof-for-exact-requirement" } })
    for (const invalid of ["../other", "1/2", "0", "1e3"]) expect(() => api.draft("3", invalid, source.change_id!, "key")).toThrow()
  } finally { await server.stop(true) }
})

it("rejects non-triaged or malformed frozen conversion metadata before sending a request", () => {
  for (const altered of [{ state: "CONVERTED" }, { version: 0 }, { card_revision: 0 }, { proposal_hash: "unfrozen" }, { requirement_id: "../other" }]) {
    expect(() => intakeConversion({ ...source, ...altered }, "proof")).toThrow()
  }
  expect(() => intakeConversion(source, "")).toThrow()
})

it("converts only the exact NEW card with opaque IDs and stable command headers", async () => {
  const draft = { summary: "Export", requirement_draft: "Export selected rows", criteria: ["CSV contains every selected row"], risks: [], questions: [], size: "S" as const }
  const triage = { id: "9007199254740995", intake_id: "9007199254740993", project_id: "3", revision: 2, version: 4, hash: "b".repeat(64), actor_id: "11", created_at: "2026-10-21T00:00:00Z", draft }
  const original: IntakeLedgerSource = { disposition_delivery: { state: "NOT_REQUESTED" }, id: triage.intake_id, project_id: "3", ref_no: 7, title: "Export", kind: "NEW", state: "TRIAGED", version: 4, card_revision: 2, received_at: triage.created_at, raw_content_format: "PLAIN", raw_content_hash: "a".repeat(64), triage }
  const seen: unknown[] = []
  const server = Bun.serve({ port: 0, async fetch(request) {
    seen.push({ path: new URL(request.url).pathname, method: request.method, key: request.headers.get("Idempotency-Key"), version: request.headers.get("If-Match"), body: await request.json() })
    return Response.json({ code: 0, data: { id: "9007199254740997", version: 1, state: "DRAFT" } })
  } })
  try {
    const api = createIntakeChangeApi(createIamApi(`http://127.0.0.1:${server.port}`))
    expect((await api.newRequirement("3", original, "new-exact-card")).id).toBe("9007199254740997")
    expect(seen).toEqual([{ path: "/entities/3/intakes/9007199254740993/requirement", method: "POST", key: "new-exact-card", version: '"4"', body: { triage_card_revision: 2, proposal_hash: triage.hash } }])
    for (const changed of [{ project_id: "4" }, { kind: "REWORK" }, { state: "CONVERTED" }, { version: 0 }, { card_revision: 0 }, { triage: { ...triage, intake_id: "5" } }, { triage: { ...triage, project_id: "4" } }, { triage: { ...triage, revision: 1 } }, { triage: { ...triage, version: 3 } }, { triage: { ...triage, hash: "invalid" } }]) {
      expect(() => api.newRequirement("3", { ...original, ...changed }, "invalid")).toThrow("Invalid Intake conversion")
    }
    expect(seen).toHaveLength(1)
    await api.newProject("3",original,"Export project","EXP","fresh-intake-proof","new-project-key")
    expect(seen[1]).toEqual({path:"/entities/3/intakes/9007199254740993/project",method:"POST",key:"new-project-key",version:'"4"',body:{triage_card_revision:2,proposal_hash:triage.hash,project:{name:"Export project",code:"EXP"},reauth_proof:"fresh-intake-proof"}})
    for(const [name,code,proof] of [["","EXP","proof"],["Export","bad code","proof"],["Export","EXP",""]])expect(()=>api.newProject("3",original,name!,code!,proof!,"invalid")).toThrow()
    expect(seen).toHaveLength(2)
  } finally { await server.stop(true) }
})

it("saves NEW and REWORK assessments with exact source versions and validates returned card identity", async () => {
  const original: IntakeLedgerSource = { disposition_delivery: { state: "NOT_REQUESTED" }, id: "9007199254740993", project_id:"3", ref_no:7, title:"Original", kind:"NEW", state:"RECEIVED", version:1, card_revision:0, received_at:"2026-10-21T00:00:00Z", raw_content:"Unchanged", raw_content_hash:"a".repeat(64), raw_content_format:"PLAIN" }
  const body = { summary:"Requested export", requirement_draft:"Export rows", criteria:["Contains current rows"], risks:[], questions:["CSV or JSON?"], size:"S" as const }
  const writes: {path:string;body:unknown;version:string|null;key:string|null}[]=[]
  let answer: unknown = { id:original.id, state:"TRIAGED", version:2, revision:1 }
  const server=Bun.serve({port:0,async fetch(request){
    if(request.method==="POST")writes.push({path:new URL(request.url).pathname,body:await request.json(),version:request.headers.get("If-Match"),key:request.headers.get("Idempotency-Key")})
    return Response.json({code:0,data:answer})
  }})
  try {
    const api=createIntakeChangeApi(createIamApi(`http://127.0.0.1:${server.port}`))
    await api.triage("3",original,body,"triage-key")
    expect(writes).toEqual([{path:"/entities/3/intakes/9007199254740993/triage-cards",body,version:'"1"',key:"triage-key"}])
    for(const changed of [{project_id:"4"},{version:0},{kind:"CHANGE"},{state:"CONVERTED"}])expect(()=>api.triage("3",{...original,...changed},body,"invalid")).toThrow()
    expect(writes).toHaveLength(1)
    const triage={id:"9007199254740997",intake_id:original.id,project_id:"3",revision:1,version:2,hash:"b".repeat(64),actor_id:"11",created_at:"2026-10-21T00:00:00Z",draft:body}
    answer={...original,state:"TRIAGED",version:2,card_revision:1,triage}
    expect((await api.source("3",original.id!)).triage?.draft).toEqual(body)
    for(const changed of [{intake_id:"4"},{project_id:"4"},{revision:2},{version:1}]){
      answer={...original,state:"TRIAGED",version:2,card_revision:1,triage:{...triage,...changed}}
      await expect(api.source("3",original.id!)).rejects.toThrow("Invalid Intake project source")
    }
  } finally {await server.stop(true)}
})


it("disposes only actual rejected items pinned to source, receipt and work versions", async () => {
 const rejected={item_id:"rejected",work_item_id:"9007199254740995",result_id:"20",evidence_id:"30",requirement_id:"10",source_intake_id:"50",comment:"Broken export"}
 const original:IntakeLedgerSource={disposition_delivery:{state:"NOT_REQUESTED"},id:"9007199254740993",project_id:"3",ref_no:7,title:"Rejected",kind:"REWORK",state:"RECEIVED",version:4,card_revision:0,received_at:"2026-10-21T00:00:00Z",raw_content_format:"PLAIN",raw_content_hash:"a".repeat(64),rework:{id:"9007199254740993",project_id:"3",package_id:"70",receipt_id:"71",state:"RECEIVED",version:4,content_hash:"a".repeat(64),rejection:{package_id:"70",receipt_id:"71",recipient_id:"4",content_hash:"b".repeat(64),items:[rejected]}}}
 const seen:unknown[]=[]
 const server=Bun.serve({port:0,async fetch(request){seen.push({path:new URL(request.url).pathname,version:request.headers.get("If-Match"),key:request.headers.get("Idempotency-Key"),body:await request.json()});return Response.json({code:0,data:{id:original.id,version:5,state:"CONVERTED"}})}})
 try{
 const api=createIntakeChangeApi(createIamApi(`http://127.0.0.1:${server.port}`))
 const body={receipt_id:"71",source_hash:original.raw_content_hash,items:[{item_id:"rejected",mode:"REOPEN_WORK_ITEMS" as const,work_version:7,reason:"Repair the defect",client_message:"We will repair it"}]}
 await api.dispose("3",original,body,"disposition-key")
 expect(seen).toEqual([{path:"/entities/3/intakes/9007199254740993/dispositions",version:'"4"',key:"disposition-key",body}])
 for(const changed of [{project_id:"4"},{kind:"NEW"},{version:3},{raw_content_hash:"c".repeat(64)},{rework:undefined}])expect(()=>api.dispose("3",{...original,...changed},body,"invalid")).toThrow()
 for(const item of [{...body.items[0]!,item_id:"accepted"},{...body.items[0]!,work_version:0},{...body.items[0]!,reason:" "}])expect(()=>api.dispose("3",original,{...body,items:[item]},"invalid")).toThrow()
 expect(seen).toHaveLength(1)
 }finally{await server.stop(true)}
})

it("previews a real delegated card and sends only its exact immutable conversion metadata", async()=>{
 const card={id:"9007199254740995",intake_id:"9007199254740993",project_id:"3",actor_id:"9007199254740997",revision:2,version:4,hash:"c".repeat(64),created_at:"2026-10-22T00:00:00Z",draft:{summary:"Export",requirement_draft:"Export exact rows",criteria:["Every row"],risks:[],questions:[],size:"S" as const}}
 const writes:unknown[]=[];let answer:unknown=card
 const server=Bun.serve({port:0,async fetch(request){if(request.method==="POST"){writes.push({path:new URL(request.url).pathname,body:await request.json(),key:request.headers.get("Idempotency-Key"),version:request.headers.get("If-Match")});return Response.json({code:0,data:{id:"9007199254740999",state:"DRAFT",version:1}})}return Response.json({code:0,data:answer})}})
 try{
 const api=createIntakeChangeApi(createIamApi(`http://127.0.0.1:${server.port}`))
 expect(await api.delegatedCard("3",card.intake_id)).toEqual(card)
 await api.delegatedRequirement("3",card,"delegated-stable")
 expect(writes).toEqual([{path:"/entities/3/intakes/9007199254740993/delegated-requirement",body:{triage_card_revision:2,proposal_hash:card.hash},key:"delegated-stable",version:'"4"'}])
 for(const changed of [{project_id:"4"},{intake_id:"5"},{actor_id:"../invalid"},{revision:0},{version:0},{hash:"unfrozen"}]){answer={...card,...changed};await expect(api.delegatedCard("3",card.intake_id)).rejects.toThrow()}
 for(const changed of [{project_id:"4"},{revision:0},{version:0},{hash:"unfrozen"}])expect(()=>api.delegatedRequirement("3",{...card,...changed},"bad")).toThrow()
 expect(writes).toHaveLength(1)
 const controller=new AbortController();controller.abort();await expect(api.delegatedCard("3",card.intake_id,controller.signal)).rejects.toThrow()
 }finally{await server.stop(true)}
})

it("requests configured retriage with opaque IDs and stable retry identity",async()=>{
 const requests: {url:string;key:string|null;body:unknown;match:string|null}[]=[]
 const server=Bun.serve({port:0,async fetch(request){requests.push({url:request.url,key:request.headers.get("Idempotency-Key"),match:request.headers.get("If-Match"),body:await request.json()});return Response.json({code:0,data:{id:"9007199254740993",version:2,state:"REQUESTED",requested_at:"2026-10-22T12:00:00Z",coalesced:true}},{status:202})}})
 try{
 const api=createIntakeChangeApi(createIamApi(`http://127.0.0.1:${server.port}`))
 const result=await api.requestTriage("3","9007199254740993","new information","same-key")
 expect(result.coalesced).toBe(true)
 expect(new URL(requests[0]!.url).pathname).toBe("/entities/3/intakes/9007199254740993/triage-requests")
 expect(requests[0]!.key).toBe("same-key");expect(requests[0]!.match).toBeNull();expect(requests[0]!.body).toEqual({note:"new information"})
 expect(()=>api.requestTriage("3","50","x".repeat(2001),"key")).toThrow()
 }finally{await server.stop(true)}
})
