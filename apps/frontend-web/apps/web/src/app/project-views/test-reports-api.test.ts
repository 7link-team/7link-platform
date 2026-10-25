import {expect,it,spyOn} from "bun:test"
import {iamApi} from "../../lib/iam-api"
import {testReportsApi,testReportSelection} from "./test-reports-api"

it("retains decimal IDs, bounded distinct selection and exact report replay/download scope",async()=>{
 expect(testReportSelection("9007199254740993, 9007199254740995")).toEqual(["9007199254740993","9007199254740995"])
 for(const input of ["","1,1","../other",Array.from({length:16},(_,i)=>String(i+1)).join(",")])expect(()=>testReportSelection(input)).toThrow()
 const calls:Parameters<typeof iamApi.deliveryResultRequest>[]=[]
 const request=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(...args)=>{calls.push(args);return {id:"9007199254740997"} as never})
 const download=spyOn(iamApi,"deliveryResultDownload").mockResolvedValue(new Blob(["%PDF-1.4"],{type:"application/pdf"}))
 try{
  await testReportsApi.create("9007199254740993","Frozen",["9007199254740995"],"same-key")
  await testReportsApi.get("9007199254740993","9007199254740997")
  expect(calls[0]![0].url).toBe("/entities/9007199254740993/test-reports")
  expect(calls[0]![1]!.headers).toEqual({"Idempotency-Key":"same-key"})
  expect(JSON.parse(calls[0]![1]!.body as string)).toEqual({title:"Frozen",plan_ids:["9007199254740995"]})
  await testReportsApi.download("9007199254740993","9007199254740997")
  expect(download.mock.calls[0]![0].url).toBe("/entities/9007199254740993/test-reports/9007199254740997/content")
  expect(()=>testReportsApi.get("../other","1")).toThrow()
  request.mockRejectedValueOnce(new Error("Forbidden"));await expect(testReportsApi.get("1","2")).rejects.toThrow("Forbidden")
 }finally{request.mockRestore();download.mockRestore()}
})
