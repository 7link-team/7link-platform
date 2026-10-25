import {expect,it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import en from "../../messages/en-US.json"
import zh from "../../messages/zh-CN.json"
import ms from "../../messages/ms-MY.json"
import {TestReports,TestReportReceipt} from "./test-reports"
import type {SavedTestReport} from "./test-reports-api"

const report:SavedTestReport={id:"9007199254740993",title:"Frozen",state:"BUILT",job_id:"2",file_id:"9007199254740995",version_id:"9007199254740997",content_hash:"a".repeat(64),summary:{total:3,passed:1,failed:1,blocked:0,unexecuted:1,human_recorded:2},created_at:"2026-10-24T00:00:00Z",built_at:"2026-10-24T00:00:01Z"}
it("shows saved hash/version and honest pending state in every locale; unauthorized report controls are absent",()=>{
 for(const [locale,messages] of [["en-US",en],["zh-CN",zh],["ms-MY",ms]] as const){
  const wrap=(children:React.ReactNode)=><IntlAppProvider initialLocale={locale} initialMessages={{iam:messages}}><MemoryRouter>{children}</MemoryRouter></IntlAppProvider>
  const html=renderToStaticMarkup(wrap(<TestReportReceipt report={report}/>))
  for(const value of [messages.testReportBuilt,messages.testReport_failed,report.content_hash!,report.file_id!,report.version_id!])expect(html).toContain(value)
  const pending=renderToStaticMarkup(wrap(<TestReportReceipt report={{...report,state:"PENDING",content_hash:null,file_id:null,version_id:null}}/>))
  expect(pending).toContain(messages.testReportPending);expect(pending).not.toContain("SHA-256")
  const allowed=renderToStaticMarkup(wrap(<TestReports project="3" plan="9007199254740993" title="Current frozen plan" canManage={true}/>))
  expect(allowed).toContain("Current frozen plan");expect(allowed).toContain("9007199254740993")
  const denied=renderToStaticMarkup(wrap(<TestReports project="3" plan="4" title="Plan" canManage={false}/>))
  expect(denied).not.toContain("<form");expect(denied).not.toContain(messages.testReportCreate)
  for(const key of Object.keys(en).filter(key=>key.startsWith("testReport")||key.startsWith("testFailure")))expect((messages as Record<string,string>)[key]).not.toContain("?")
 }
})
