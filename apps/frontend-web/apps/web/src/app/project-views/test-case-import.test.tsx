import {describe,expect,it,spyOn} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {iamApi} from "../../lib/iam-api"
import {TestCaseImportReportView} from "./test-case-import"
import {importTestCaseCSV,testCaseFailureCSV,type TestCaseImportReport} from "./test-case-import-api"
import {testCaseImportLabels} from "./test-case-import-labels"

const report:TestCaseImportReport={imported:0,unclassified:1,preview:true,columns:["title","criterion","preconditions","steps","expected","priority"],rows:[{row:2,source:["Login, quoted","Unknown","Session\nready",'Click "go"',"Dashboard","P9"],unclassified:true}],errors:[{row:2,column:"priority",code:"PRIORITY"}]}
describe("Testhub CSV workflow",()=>{
 it("uploads original bytes with exact scope, preview state and stable idempotency key",async()=>{
  const calls:Parameters<typeof iamApi.deliveryResultRequest>[]=[]
  const request=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(...args)=>{calls.push(args);return report as never})
  try{
   const file=new File(['\ufefftitle,criterion,preconditions,steps,expected,priority\n"Title, quoted",C1,,Run,Done,P1'],"cases.csv",{type:"text/csv"})
   await importTestCaseCSV("9007199254740993","9007199254740995","9007199254740997",file,true,"same-key")
   await importTestCaseCSV("9007199254740993","9007199254740995","9007199254740997",file,false,"same-key")
   expect(calls[0]![0].url).toBe("/entities/9007199254740993/test-case-imports?work_item_id=9007199254740995&requirement_id=9007199254740997&preview=true")
   expect(calls[1]![0].url).toEndWith("preview=false")
   for(const call of calls){expect(call[1]!.headers).toEqual({"Idempotency-Key":"same-key"});const sent=(call[1]!.body as FormData).get("file") as File;expect(await sent.text()).toBe(await file.text())}
   expect(()=>importTestCaseCSV("../other","2","3",file,false,"key")).toThrow()
   expect(calls).toHaveLength(2)
   request.mockRejectedValueOnce(new Error("Forbidden"));await expect(importTestCaseCSV("1","2","3",file,false,"key")).rejects.toThrow("Forbidden")
  }finally{request.mockRestore()}
 })
 it("renders source mapping, localized failures and honest import outcomes in three locales",()=>{
  for(const locale of ["en-US","zh-CN","ms-MY"]){
   const l=testCaseImportLabels(locale)
   const markup=renderToStaticMarkup(<TestCaseImportReportView report={report} locale={locale}/>)
   for(const text of [l.issues,l.codes.PRIORITY,l.unclassified,l.preconditions,"Login, quoted"])expect(markup).toContain(text)
   expect(markup).not.toContain(`${l.done}: 1`)
   const imported={...report,preview:false,imported:1,errors:[],rows:[{...report.rows[0]!,case_id:"9007199254740999",version:1}]}
   const result=renderToStaticMarkup(<TestCaseImportReportView report={imported} locale={locale}/>)
   expect(result).toContain(`${l.done}: 1`);expect(result).toContain("9007199254740999");expect(result).not.toContain(l.noWrites)
  }
 })
 it("exports actual failed source rows with CSV escaping and spreadsheet formula protection",()=>{
  const text=testCaseFailureCSV(report)
  expect(text).toStartWith("\ufeff");expect(text).toContain('"Login, quoted"');expect(text).toContain('"Session\nready"');expect(text).toContain('"Click ""go"""');expect(text).toContain('"2","priority","PRIORITY"')
  const unsafe={...report,rows:[{...report.rows[0]!,source:["=1+1","@import"," +2","-3","Safe","P9"]}]}
  const protectedCSV=testCaseFailureCSV(unsafe)
  for(const source of ["=1+1","@import"," +2","-3"])expect(protectedCSV).toContain(`"'${source}"`)
 })
})
