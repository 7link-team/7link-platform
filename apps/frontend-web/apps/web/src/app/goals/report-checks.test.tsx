import {expect,it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {MemoryRouter} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {ReportChecks} from "./report-checks"
import {planningLabels} from "./labels"
import type {PlanningCheck} from "./api"
const check:PlanningCheck={result:"BLOCKED",content_hash:"exact-hash",report_progress:{expected_progress:0.8,computed_progress:0.4,expected_source:"CONFIRMED_PLAN",missing_sources:[]},issues:[{code:"B1_UNBACKED_COMPLETION",blocking:true,work_item_id:"9007199254740993"}],facts:{work:[{id:"9007199254740993",title:"Actual claim",state:"ACTIVE"}],result_count:0,handoff_count:0,missing_sources:[]}}
for(const locale of ["en-US","zh-CN","ms-MY"] as const)it(`links exact reconciliation facts and sections in ${locale}`,()=>{
 const html=renderToStaticMarkup(<MemoryRouter><IntlAppProvider initialLocale={locale} initialMessages={{}}><ReportChecks check={check} labels={planningLabels(locale)} tenant="1" project="3"/></IntlAppProvider></MemoryRouter>)
 for(const expected of ["80%","40%","CONFIRMED_PLAN","exact-hash",'href="#report-completed"',"/admin/1/entities/3/work-items?work=9007199254740993","Actual claim"])expect(html).toContain(expected)
})
