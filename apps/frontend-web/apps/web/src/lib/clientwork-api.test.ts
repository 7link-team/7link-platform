import { describe, expect, it, spyOn } from "bun:test"
import assert from "node:assert/strict"
import { approvedChangeDelta, clientworkApi, packageItemDecision, toleranceBudget, toleranceForecast } from "./clientwork-api"
import { iamApi } from "./iam-api"

describe("commitment and forecast input precision", () => {
  const deadline = "2027-01-01T00:00:00.000Z"
  it("preserves zero versus unset amounts and exact safe integer limits", () => {
    const value = toleranceBudget({ appetite_end_at: deadline, appetite_cost_micros: "0", appetite_scope_criteria: "" })
    expect(value.appetite_cost_micros).toBe(0)
    expect(value.appetite_scope_criteria).toBeNull()
    expect(value.tolerance_cost_pct).toBe(15)
    expect(toleranceBudget({ appetite_end_at: deadline, appetite_cost_micros: String(Number.MAX_SAFE_INTEGER) }).appetite_cost_micros).toBe(Number.MAX_SAFE_INTEGER)
  })
  it("refuses rounded, fractional, negative and invalid values with localized feedback", () => {
    for (const amount of ["9007199254740993", "1.5", "-1", "NaN", "Infinity"]) {
      expect(() => toleranceBudget({ appetite_end_at: deadline, appetite_cost_micros: amount }, "localized")).toThrow("localized")
      expect(() => toleranceForecast({ end_at: deadline, cost_micros: amount }, "localized")).toThrow("localized")
    }
    for (const date of ["invalid", "1960-01-01", "10000-01-01"]) {
      expect(() => toleranceBudget({ appetite_end_at: date })).toThrow()
      expect(() => toleranceForecast({ end_at: date }, "localized")).toThrow("localized")
    }
    expect(() => toleranceBudget({ appetite_end_at: deadline, tolerance_time_pct: 101 })).toThrow()
    expect(() => toleranceBudget({ appetite_end_at: deadline, tolerance_cost_pct: 0.5 })).toThrow()
  })
  it("records exact confirmed forecasts and signed approved changes", () => {
    expect(toleranceForecast({ end_at: deadline, cost_micros: "0", scope_criteria: "10" }, "localized")).toEqual({ end_at: deadline, cost_micros: 0, scope_criteria: 10 })
    expect(approvedChangeDelta({ schedule_delta_days: -2, cost_delta_micros: "-100", scope_delta_criteria: 1 })).toEqual({ schedule_delta_days: -2, cost_delta_micros: -100, scope_delta_criteria: 1 })
    expect(() => approvedChangeDelta({ schedule_delta_days: 36501 })).toThrow()
    expect(() => approvedChangeDelta({ cost_delta_micros: "9007199254740993" })).toThrow()
    expect(() => approvedChangeDelta({ scope_delta_criteria: 0.25 })).toThrow()
  })
})

it("scopes CLIENT comments to exact frozen items with abort and replay keys",async()=>{
 const calls:Parameters<typeof iamApi.deliveryResultRequest>[]=[]
 const request=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(...args)=>{calls.push(args);return {} as never})
 const item="a".repeat(64),signal=new AbortController().signal
 try{
  await clientworkApi.packageComments("3","8",item,"cursor",signal)
  const body={body:"A precise source comment",anchor_kind:"TABLE_ROW" as const,row_index:0}
  await clientworkApi.createPackageComment("3","8",item,body,"source-replay")
  expect(calls[0]).toEqual([{method:"GET",url:`/entities/3/delivery-packages/8/items/${item}/comments?limit=20&cursor=cursor`},{signal}])
  expect(calls[1]).toEqual([{method:"POST",url:`/entities/3/delivery-packages/8/items/${item}/comments`},{headers:{"Idempotency-Key":"source-replay"},body:JSON.stringify(body)}])
  for(const invalid of ["../other","0","1/2"])expect(()=>clientworkApi.packageComments(invalid,"8",item,"")).toThrow()
  for(const invalid of ["b".repeat(63),"g".repeat(64),"../private"])expect(()=>clientworkApi.packageComments("3","8",invalid,"")).toThrow()
  expect(calls).toHaveLength(2)
 }finally{request.mockRestore()}
})

it("references deduplicated CLIENT source IDs only for rejected receipt items",()=>{
 const item="a".repeat(64),row={items:[{item_id:item}],content_hash:"b".repeat(64)} as Parameters<typeof packageItemDecision>[0]
 const base={signer_name:"Signer",confirmed:true,reason:"Reviewed",[`result_${item}`]:"REJECTED",[`category_${item}`]:"OTHER",[`comment_${item}`]:"Detailed rejected artifact",[`threads_${item}`]:["90","90","91"]}
 const rejected=packageItemDecision(row,base,"invalid")
 const rejectedItem=rejected.items?.[0]
 assert.ok(rejectedItem,"rejected receipt must contain the reviewed item")
 expect(rejectedItem.comment_thread_ids).toEqual(["90","91"])
 const accepted=packageItemDecision(row,{...base,[`result_${item}`]:"ACCEPTED"},"invalid")
 const acceptedItem=accepted.items?.[0]
 assert.ok(acceptedItem,"accepted receipt must contain the reviewed item")
 expect(acceptedItem.comment_thread_ids).toBeUndefined()
 expect(()=>packageItemDecision(row,{...base,[`threads_${item}`]:Array.from({length:21},(_,n)=>String(n+1))},"invalid")).toThrow()
 expect(()=>packageItemDecision(row,{...base,[`threads_${item}`]:["../private"]},"invalid")).toThrow()
})

it("reads viewer bytes only through the exact package version with abort and byte limit",async()=>{
 const request=spyOn(iamApi,"deliveryResultDownload").mockResolvedValue(new Blob(["pinned"]))
 const signal=new AbortController().signal
 try{
  await clientworkApi.packageCommentSource("3","8","9007199254740993",signal)
  expect(request).toHaveBeenCalledWith({method:"GET",url:"/entities/3/delivery-packages/8/artifacts/9007199254740993/content"},{maxBytes:4*1024*1024,signal})
  expect(()=>clientworkApi.packageCommentSource("3","8","../internal")).toThrow()
  expect(request).toHaveBeenCalledTimes(1)
 }finally{request.mockRestore()}
})

it("binds item receipts and rework to exact package IDs, versions and replay keys", async () => {
  const calls: Parameters<typeof iamApi.deliveryResultRequest>[] = []
  const request = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (...args) => { calls.push(args); return {} as never })
  const project = "9007199254740993", id = "9007199254740995"
  const signal = new AbortController().signal
  try {
    await clientworkApi.packageRework(project, id, signal)
    await clientworkApi.command(project, "packages", {}, "rework-replay-key", id, "rework", 3)
    const body = { content_hash: "a".repeat(64), reason: "Reviewed both artifacts", items: [
      { item_id: "b".repeat(64), result: "ACCEPTED" },
      { item_id: "c".repeat(64), result: "REJECTED", feedback_category: "FUNCTIONAL_DEFECT", comment: "The delivered artifact fails the agreed criterion" },
    ] }
    await clientworkApi.command(project, "packages", body, "receipt-replay-key", id, "receipts", 2)
    expect(calls[0]).toEqual([{ method: "GET", url: `/entities/${project}/delivery-packages/${id}/rework` }, { signal }])
    expect(calls[1]).toEqual([{ method: "POST", url: `/entities/${project}/delivery-packages/${id}/rework` }, {
      headers: { "Idempotency-Key": "rework-replay-key", "If-Match": '"3"' }, body: "{}",
    }])
    expect(calls[2]).toEqual([{ method: "POST", url: `/entities/${project}/delivery-packages/${id}/receipts` }, {
      headers: { "Idempotency-Key": "receipt-replay-key", "If-Match": '"2"' }, body: JSON.stringify(body),
    }])
    const sent = JSON.parse(String(calls[2]![1]!.body)) as Record<string, unknown>
    expect(sent).not.toHaveProperty("decision")
    expect(sent).not.toHaveProperty("work_item_id")
  } finally { request.mockRestore() }
})

it("rejects malformed rework scopes before requests and preserves authority failures", async () => {
  const request = spyOn(iamApi, "deliveryResultRequest").mockResolvedValue({} as never)
  try {
    for (const invalid of ["0", "../other", "1/2", "1e3"]) {
      expect(() => clientworkApi.packageRework(invalid, "4")).toThrow()
      expect(() => clientworkApi.packageRework("3", invalid)).toThrow()
    }
    expect(request).not.toHaveBeenCalled()
    request.mockRejectedValueOnce(new Error("Forbidden"))
    await expect(clientworkApi.packageRework("3", "4")).rejects.toThrow("Forbidden")
  } finally { request.mockRestore() }
})

it("requests canonical redacted bundle state and ZIP with exact project/package scope",async()=>{
 const calls:Parameters<typeof iamApi.deliveryResultRequest>[]=[]
 const request=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(...args)=>{calls.push(args);return {} as never})
 const downloads:Parameters<typeof iamApi.deliveryResultDownload>[]=[]
 const download=spyOn(iamApi,"deliveryResultDownload").mockImplementation(async(...args)=>{downloads.push(args);return new Blob([])})
 try{const abort=new AbortController();await clientworkApi.packageBundle("3","70",abort.signal);await clientworkApi.packageBundleContent("3","70");expect(calls[0]?.[0]).toEqual({method:"GET",url:"/entities/3/delivery-packages/70/bundle"});expect(calls[0]?.[1]?.signal).toBe(abort.signal);expect(downloads[0]?.[0]).toEqual({method:"GET",url:"/entities/3/delivery-packages/70/bundle/content"});expect(downloads[0]?.[1]?.maxBytes).toBe(512*1024*1024);expect(()=>clientworkApi.packageBundle("bad","70")).toThrow()}finally{request.mockRestore();download.mockRestore()}
})
