import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {AcceptanceScheduleList,AcceptanceMetricsTable,AcceptanceStatus} from "./acceptance"

test("acceptance timeline distinguishes publication, cancellation and unknown denominators in all locales",async()=>{
 for(const locale of ["en-US","zh-CN","ms-MY"]){
  const messages=await loadMessages(locale)
  const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><AcceptanceScheduleList rows={[{id:"1",day:3,kind:"REMINDER",due_at:"2026-10-22T00:00:00Z",emitted_at:null,cancelled_at:null},{id:"2",day:14,kind:"ESCALATION",due_at:"2026-11-02T00:00:00Z",emitted_at:"2026-11-02T00:00:00Z",cancelled_at:null}]}/><AcceptanceMetricsTable value={{timezone:"America/Los_Angeles",calendar_version:7,days:[{date:"2026-11-01",receipts:2,accepted:1,acceptance_rate:.5},{date:"2026-11-02",receipts:0,accepted:0,acceptance_rate:null}]}}/></IntlAppProvider>)
  expect(html).toContain("America/Los_Angeles");expect(html).toContain("1 / 2");expect(html).toContain("0 / 0");expect(html).toContain("50");expect(html).not.toContain("acceptanceNoSamples");expect(html).not.toContain("NaN");expect(html).not.toContain("Invalid Date")
 }
})

test("acceptance container renders a loading state for the exact package",async()=>{
 const messages=await loadMessages("en-US")
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale="en-US" initialMessages={messages}><AcceptanceStatus project="3" id="21" external={true}/></IntlAppProvider>)
 expect(html).toContain('aria-busy="true"');expect(html).toContain("No receipt or acceptance is created automatically");expect(html).not.toContain("Daily client acceptance")
})
