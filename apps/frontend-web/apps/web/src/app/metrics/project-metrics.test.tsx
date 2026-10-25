import {expect,it} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {ProjectMetricsValues} from "./project-metrics"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {projectMetricMessages,projectMetricNotes,type ProjectMetrics,type ProjectMetricSnapshot} from "./project-metrics-data"

for(const locale of ["en-US","zh-CN","ms-MY"] as const)it(`renders exact facts, unavailable samples and explicit lower bounds in ${locale}`,async()=>{
 const missing={samples:0,sum:"0",mean:null,p50:null,p90:null,p95:null,unit:"ms"}
 const snapshot:ProjectMetricSnapshot={project_id:"3",work_items:2,scope_changed:1,kpis:{L2_FIRST_PASS:{numerator:"0",denominator:"0",value:null,missing:0,missing_denominator:0,missing_rate:null,lower_bound:false},KNOWN_COST_LOWER_BOUND_MICROS:{numerator:"9007199254740993",denominator:"1",value:"9007199254740993.000000",missing:1,missing_denominator:2,missing_rate:"0.500000",lower_bound:true}},internal_rounds:missing,rework_cycle:missing,versions:{model:["observed-v2"],verifier:[],workflow:[]},missing_sources:["LABOR_PRICING"]}
 const data:ProjectMetrics={from:"2026-10-01T00:00:00Z",to:"2026-10-22T00:00:00Z",computed_at:"2026-10-23T00:00:00Z",version:"project-metrics-v1",cohort:"VISIBLE_WORK_WINDOW_UTC",selection:{model:"",verifier:"",workflow:""},team:snapshot,projects:[snapshot]}
 const html=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={await loadMessages(locale)}><ProjectMetricsValues data={data} locale={locale}/></IntlAppProvider>)
 expect(html).toContain("9007199254740993.000000");expect(html).toContain(projectMetricMessages[locale].empty);expect(html).toContain(projectMetricMessages[locale].lower);expect(html).toContain("0 / 0");expect(html).toContain("1 / 2 · 0.500000");expect(html).toContain(projectMetricNotes[locale].LABOR_PRICING);expect(html).toContain("observed-v2");expect(html).toContain("project-metrics-v1");expect(html).not.toContain("100%")
})
