import {afterEach,expect,spyOn,test} from "bun:test"
import {iamApi} from "../../lib/iam-api"
import {calibrationApi,canAcceptCalibration,parseCalibrationLabels,type Calibration} from "./calibration-api"
let restore:(()=>void)|undefined
afterEach(()=>restore?.())
test("calibration routes retain frozen IDs, explicit labels, version precondition and retry keys",async()=>{
 const calls:{route:{method:string;url:string};init?:RequestInit}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never});restore=()=>spy.mockRestore()
 const signal=new AbortController().signal,body={template_version_id:"9007199254740993",verifier_key:"visual",provider_ref:"actual-model/version",method:"AGREEMENT" as const,machine_id:0 as const}
 await calibrationApi.start("7",body,"same-key",signal);await calibrationApi.start("7",body,"same-key",signal)
 expect(calls[0]).toEqual(calls[1]);expect(calls[0]?.route).toEqual({method:"POST",url:"/tenants/7/verifier-calibrations"});expect(JSON.parse(calls[0]?.init?.body as string)).toEqual({...body,machine_id:null})
 await calibrationApi.confirm("7",{id:"9007199254740995",version:7} as Calibration,true,"Reviewed actual evidence","confirm-key",signal)
 expect(calls[2]?.route).toEqual({method:"PUT",url:"/tenants/7/verifier-calibrations/9007199254740995/confirmation"});expect(calls[2]?.init?.headers).toEqual({"Idempotency-Key":"confirm-key","If-Match":"\"7\""});expect(JSON.parse(calls[2]?.init?.body as string)).toEqual({accept:true,note:"Reviewed actual evidence"})
 await calibrationApi.addSamples("7",body.template_version_id,"visual",[{input:{reference:"frozen-source"},expected:"PASS"}],"label-key",signal)
 expect(calls[3]?.route.url).toBe("/tenants/7/template-versions/9007199254740993/calibration-samples");expect(JSON.parse(calls[3]?.init?.body as string)).toEqual({verifier_key:"visual",samples:[{input:{reference:"frozen-source"},expected:"PASS"}]})
 await calibrationApi.list("7","software","visual","99",signal);await calibrationApi.samples("7",body.template_version_id,"visual","88",signal);await calibrationApi.get("7","9007199254740995",signal)
 expect(calls[4]?.route.url).toBe("/tenants/7/verifier-calibrations?limit=50&sort=id&cursor=99&template_key=software&verifier_key=visual")
 expect(calls[5]?.route.url).toBe("/tenants/7/template-versions/9007199254740993/calibration-samples?limit=50&sort=id&cursor=88&verifier_key=visual")
 expect(calls[6]?.route.url).toBe("/tenants/7/verifier-calibrations/9007199254740995");expect(calls.every(call=>call.init?.signal===signal)).toBe(true)
})
test("only actual completed agreement above frozen target can be accepted",()=>{
 const row={method:"AGREEMENT",state:"CALIBRATING",run_state:"PENDING",target_ppm:850000} as Calibration
 expect(canAcceptCalibration(row)).toBe(false);row.run_state="UNAVAILABLE";expect(canAcceptCalibration(row)).toBe(false)
 row.run_state="COMPLETED";expect(canAcceptCalibration(row)).toBe(false);row.agreement_ppm=0;expect(canAcceptCalibration(row)).toBe(false)
 row.agreement_ppm=849999;expect(canAcceptCalibration(row)).toBe(false);row.agreement_ppm=850000;expect(canAcceptCalibration(row)).toBe(true)
 row.state="CALIBRATED";expect(canAcceptCalibration(row)).toBe(false);row.state="DRIFTED";row.method="PROBE";expect(canAcceptCalibration(row)).toBe(false)
})
test("sample labels require explicit expected verdict and preserve arbitrary JSON inputs",()=>{
 expect(parseCalibrationLabels('[{"input":null,"expected":"FAIL"},{"input":{"reference":"9007199254740993"},"expected":"PASS"}]')).toEqual([{input:null,expected:"FAIL"},{input:{reference:"9007199254740993"},expected:"PASS"}])
 for(const text of ["null","[]","{}",'[{"input":{},"expected":"INCONCLUSIVE"}]','[{"expected":"PASS"}]',JSON.stringify(Array.from({length:101},()=>({input:{},expected:"PASS"})))])expect(()=>parseCalibrationLabels(text)).toThrow()
})
