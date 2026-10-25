import {expect, test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {ReportEditor, ReportReopenControl, PlanConfirmationNotice, PlanSuggestions, ItemList} from "./page"
import {planningLabels, planningCarryLabels} from "./labels"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import type {PlanningRecord} from "./api"

test("confirmed report correction requires the independent recorded confirmer and a reason in every locale", () => {
 const record = {id: "9", state: "CONFIRMED", principal_id: "2", author_id: "2", confirmed_by: "3", can_reopen: true} as PlanningRecord
 for (const locale of ["en-US", "zh-CN", "ms-MY"]) {
  const props = {record, subject: "3", allowed: true, busy: false, reason: "Correct facts", setReason: () => {}, labels: planningLabels(locale), reopen: async () => {}}
  const markup = renderToStaticMarkup(<ReportReopenControl {...props}/>)
  expect(markup).toContain(props.labels.reopen)
  expect(markup).toContain(props.labels.reopenReason)
  expect(markup).not.toContain("disabled=")
  for (const changes of [{subject: "2"}, {record: {...record, can_reopen: false}}, {subject: undefined}, {allowed: false}, {record: {...record, state: "RETURNED"}}]) {
   expect(renderToStaticMarkup(<ReportReopenControl {...props} {...changes}/>)).toBe("")
  }
  expect(renderToStaticMarkup(<ReportReopenControl {...props} subject="4"/>)).toContain(props.labels.reopen)
  expect(renderToStaticMarkup(<ReportReopenControl {...props} reason="bad"/>)).toContain("disabled=")
  expect(renderToStaticMarkup(<ReportReopenControl {...props} busy/>)).toContain("disabled=")
 }
})

test("plan timeout advisory preserves submitted state and is localized", () => {
 const record = {id:"9",kind:"plan",state:"SUBMITTED",unconfirmed:true} as PlanningRecord
 for (const locale of ["en-US","zh-CN","ms-MY"]) {
  const labels=planningLabels(locale)
  expect(renderToStaticMarkup(<PlanConfirmationNotice record={record} labels={labels}/>)).toContain(labels.unconfirmed)
  for (const change of [{unconfirmed:false},{kind:"report"},{state:"CONFIRMED"},{state:"DRAFT"}]) expect(renderToStaticMarkup(<PlanConfirmationNotice record={{...record,...change}} labels={labels}/>)).toBe("")
 }
})

test("optional KR suggestions and carry-over sources are clear in all three locales",()=>{
	 expect(planningCarryLabels("zh-CN").suggestions).toBe("关键结果建议")
	 expect(planningCarryLabels("zh-CN").carry).toBe("带入下期")
 const item={title:"Actual assigned task",goal_id:"0",kr:"",work_item_id:"101",carried_from_report_id:"9007199254740993",carried_from_key:"continued"}
 for(const locale of ["en-US","zh-CN","ms-MY"]){
  const labels=planningCarryLabels(locale)
  const markup=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={{}}><ItemList items={[item]} labels={planningLabels(locale)}/></IntlAppProvider>)
  expect(markup).toContain(labels.source);expect(markup).toContain(item.carried_from_report_id);expect(markup).toContain(item.carried_from_key);expect(markup).toContain(labels.needsLink)
  expect(renderToStaticMarkup(<PlanSuggestions suggestions={[]} labels={labels} busy={false} add={()=>{}}/>)).toBe("")
  const suggestion={title:"Proposed KR activity",goal_id:"9",kr:"quality",category:"OTHER"}
  const available=renderToStaticMarkup(<PlanSuggestions suggestions={[suggestion]} labels={labels} busy={false} add={()=>{}}/>)
  expect(available).toContain(labels.suggestions);expect(available).toContain(labels.add);expect(available).not.toContain("disabled=")
  expect(renderToStaticMarkup(<PlanSuggestions suggestions={[suggestion]} labels={labels} busy add={()=>{}}/>)).toContain("disabled=")
 }
})

test("report editor renders structured sections, carry reasons and explicit draft acknowledgement",async()=>{
 const messages=await (await import("../../lib/i18n")).loadMessages("en-US")
 const record={project_id:"12",principal_id:"70",period_kind:"WEEK",period_start:"2026-10-19",content_hash:"fixture",id:"42",kind:"report",state:"DRAFT",version:1,content:{completed:[],uncompleted:"Waiting",retrospective:"Legacy notes",retrospective_ai_drafted:true,uncompleted_items:[{key:"next",reason:"OTHER",detail:"Generated reason",detail_ai_drafted:true,carry_over:"CARRY_TO_NEXT",item:{title:"Next",goal_id:"9",kr:"quality",category:"OTHER"}}],next_plan:[{title:"Next",goal_id:"9",kr:"quality",category:"OTHER"}],declared_confidence:"GREEN"}} as PlanningRecord
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><ReportEditor record={record} labels={planningLabels("en-US")} readonly={false} save={async()=>{}}/></IntlAppProvider>)
 for(const name of ["retro_went_well","retro_problems","retro_root_causes","unfinished_detail_0"])expect(html).toContain(`name="${name}"`)
 for(const id of ["report-completed","report-uncompleted","report-retrospective","report-next-plan"])expect(html).toContain(`id="${id}"`)
 expect(html).toContain("Legacy notes")
 expect(html.match(/role="checkbox"/g)?.length).toBe(2)
 expect(html.match(/I reviewed and accept this generated draft/g)?.length).toBe(2)
 expect(html).toContain("Reason");expect(html).toContain("Carry")
})
