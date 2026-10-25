import {afterEach,expect,spyOn,test} from "bun:test"
import {iamApi} from "../../lib/iam-api"
import {canConfirmReport,reportApi,type ReportView} from "./report-api"
let restore:(()=>void)|undefined
afterEach(()=>restore?.())
test("period APIs preserve exact logical IDs, revision hashes and retry keys",async()=>{
 const calls:{route:unknown;init:RequestInit|undefined}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never});restore=()=>spy.mockRestore()
 const body={scheme_id:"9007199254740993",subject_id:"9007199254740995",cadence:"MONTH" as const,timezone:"Asia/Kuala_Lumpur",anchor_date:"2026-10-22",reason:"Reviewed exact period evidence"},signal=new AbortController().signal
 await reportApi.generate("7",body,"same-retry",signal);await reportApi.generate("7",body,"same-retry",signal)
 expect(calls[0]).toEqual(calls[1]);expect(calls[0]?.route).toEqual({method:"POST",url:"/tenants/7/performance-reports"});expect(JSON.parse(calls[0]?.init?.body as string)).toEqual(body)
 await reportApi.recalculate("7","9007199254740997",{base_version:4,scheme_version:2,reason:"New source receipts"},"recalc",signal)
 await reportApi.calibrate("7","9007199254740997",{base_version:5,dimension_key:"quality",rule_key:"review",value:8500,evidence_refs:["fact-1"],reason:"Human reviewed frozen evidence"},"calibration",signal)
 await reportApi.confirm("7","9007199254740997",{base_version:6,sha256:"a".repeat(64),reason:"Explicit final Human confirmation"},"confirmation",signal)
 expect(calls.slice(2).map(call=>(call.route as {url:string}).url)).toEqual(["/tenants/7/performance-reports/9007199254740997/recalculations","/tenants/7/performance-reports/9007199254740997/calibrations","/tenants/7/performance-reports/9007199254740997/confirmations"])
 expect(calls.slice(0,5).map(call=>(call.init?.headers as Record<string,string>)["Idempotency-Key"])).toEqual(["same-retry","same-retry","recalc","calibration","confirmation"])
 await reportApi.list("7",50,signal);await reportApi.get("7","9007199254740997",2,signal);await reportApi.revisions("7","9007199254740997",50,signal);await reportApi.preview("7",body,signal)
 expect(calls[5]?.route).toEqual({method:"GET",url:"/tenants/7/performance-periods?limit=50&offset=50"})
 expect(calls[6]?.route).toEqual({method:"GET",url:"/tenants/7/performance-reports/9007199254740997?version=2"})
 expect(calls[7]?.route).toEqual({method:"GET",url:"/tenants/7/performance-reports/9007199254740997/revisions?limit=50&offset=50"})
 expect((calls[8]?.route as {url:string}).url).toContain("timezone=Asia%2FKuala_Lumpur")
 expect(calls.every(call=>call.init?.signal===signal)).toBe(true)
 expect(calls.slice(5).every(call=>call.init?.body===undefined)).toBe(true)
})
test("explicit confirmation is disabled for missing evidence, summaries and already confirmed revisions",()=>{
 const report={snapshot:{status:"PENDING",total_score_bps:null,dimensions:[{score_bps:null,rules:[{value:null,matched:null,delta:null}]}]}} as ReportView
 expect(canConfirmReport(report)).toBe(false)
 report.snapshot.total_score_bps=0;report.snapshot.dimensions[0]!.score_bps=0
 expect(canConfirmReport(report)).toBe(false)
 const rule=report.snapshot.dimensions[0]!.rules[0]!;rule.value=0;rule.matched=false;rule.delta=0
 expect(canConfirmReport(report)).toBe(true)
 report.snapshot.status="CONFIRMED";expect(canConfirmReport(report)).toBe(false)
 report.snapshot.status="SUMMARY";expect(canConfirmReport(report)).toBe(false)
})
test("schedule status reads due windows with cancellation",async()=>{
 const signal=new AbortController().signal
 const spy=spyOn(iamApi,"deliveryResultRequest").mockResolvedValue({pending:[],has_more:false} as never);restore=()=>spy.mockRestore()
 await expect(reportApi.scheduleStatus("9007199254740993",signal)).resolves.toEqual({pending:[],has_more:false})
 expect(spy).toHaveBeenCalledWith({method:"GET",url:"/tenants/9007199254740993/performance-schedule-status"},{signal})
})
test("subject type filter is sent to the server before pagination",async()=>{
 const signal=new AbortController().signal
 const spy=spyOn(iamApi,"deliveryResultRequest").mockResolvedValue({items:[],has_more:false} as never);restore=()=>spy.mockRestore()
 await reportApi.list("7",50,signal,"HUMAN")
 expect(spy).toHaveBeenCalledWith({method:"GET",url:"/tenants/7/performance-periods?limit=50&offset=50&subject_kind=HUMAN"},{signal})
})
