import {describe,expect,it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {ReportQualityFacts,ReportKRChanges} from "./board"
import {reportLabels} from "./labels"

describe("report rollup source facts",()=>{
 it("renders real source numerators and preserves unavailable versus measured zero",()=>{
  const known=renderToStaticMarkup(<ReportQualityFacts locale="en-US" metrics={{l3_entered:20,escapes_human:1,escapes_client:1,internal_qa_completed:20,internal_first_pass:14,internal_rounds_sum:31,escape_rate:.1,internal_first_pass_rate:.7,avg_internal_rounds:1.55}}/>)
  for(const value of ["10%","70%","1.55","2 / 20","14 / 20","31 / 20"])expect(known).toContain(value)
  const empty=renderToStaticMarkup(<ReportQualityFacts locale="en-US" metrics={{l3_entered:0,escapes_human:0,escapes_client:0,internal_qa_completed:0,internal_first_pass:0,internal_rounds_sum:0}}/>)
  expect(empty).toContain("0 / 0")
  expect(empty).not.toContain("0%")
  const zero=renderToStaticMarkup(<ReportQualityFacts locale="en-US" metrics={{l3_entered:1,escapes_human:0,escapes_client:0,internal_qa_completed:1,internal_first_pass:0,internal_rounds_sum:2,escape_rate:0,internal_first_pass_rate:0,avg_internal_rounds:2}}/>)
  expect(zero).toContain("0%")
  for(const locale of ["en-US","zh-CN","ms-MY"]){
   const labels=reportLabels(locale)
   const unknown=renderToStaticMarkup(<ReportQualityFacts locale={locale}/>)
   expect(unknown).toContain(labels.qualityUnavailable)
   expect(unknown).not.toContain("0%")
   expect(unknown).not.toContain("0 / 0")
   expect(unknown).not.toContain("???")
  }
 })
 it("shows authoritative boundary values, signed delta, source drilldown and missing history",()=>{
  const changes=[{goal_id:"7",key:"done",title:"Accepted output",start:2,end:6,delta:4,missing_sources:[]},{goal_id:"8",key:"known",title:"Legacy goal",end:0,missing_sources:["KR_PERIOD_START"]}]
  for(const locale of ["en-US","zh-CN","ms-MY"]){
   const markup=renderToStaticMarkup(<MemoryRouter initialEntries={["/reports"]}><ReportKRChanges changes={changes} locale={locale}/></MemoryRouter>)
   expect(markup).toContain("2 → 6")
   expect(markup).toContain("+4")
   expect(markup).toContain("— → 0")
   expect(markup).toContain("record_id=7")
   expect(markup).toContain(reportLabels(locale).historyUnavailable)
   expect(markup).not.toContain("???")
  }
 })
})
