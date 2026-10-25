import {expect,test} from "bun:test"
import {renderToStaticMarkup} from "react-dom/server"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {loadMessages} from "../../lib/i18n"
import {ReportSnapshot,PeriodSummary,DuePeriods} from "./report-board"
import type {ReportView,PeriodWindow} from "./report-api"
import {blankScheme} from "./api"
const period:PeriodWindow={cadence:"MONTH",timezone:"Asia/Kuala_Lumpur",period_start:"2026-10-01",period_end:"2026-10-31",start_at:"2026-09-30T16:00:00Z",end_at:"2026-10-31T16:00:00Z",trigger_at:"2026-10-30T16:00:00Z",cutoff_at:"2026-10-30T16:00:00Z",uncovered_start_at:"2026-10-30T16:00:00Z",uncovered_end_at:"2026-10-31T16:00:00Z"}
for(const locale of ["en-US","zh-CN","ms-MY"])test(`period snapshots show unknown evidence, cutoff, provenance and confirmation in ${locale}`,async()=>{
 const messages=await loadMessages(locale),t=messages.iam as Record<string,string>,definition=blankScheme()
 definition.name="Frozen source scheme";definition.dimensions=[{key:"quality",title:"Source dimension",weight_bps:10000,maximum_points:100,rules:[{key:"review",description:"Source rule version",condition:{metric:"REVIEW_SCORE",operator:"GTE",threshold:8000},effect:"ADD",points:100,evidence_kinds:["HUMAN_REVIEW"]}]}]
 const report:ReportView={id:"99",report_id:"9007199254740993",version:2,sha256:"a".repeat(64),reason:"Retain exact source evidence",created_by:"123",created_at:"2026-10-31T00:00:00Z",snapshot:{scheme:{id:"1",scheme_id:"2",version:4,definition,sha256:"b".repeat(64),reason:"Frozen rules",created_by:"123",created_at:"2026-10-01T00:00:00Z"},subject_id:"9007199254740995",subject_kind:"HUMAN",period,assessments:{},status:"PENDING",total_score_bps:null,dimensions:[{key:"quality",score_bps:null,rules:[{key:"review",metric:"REVIEW_SCORE",value:null,matched:null,delta:null,evidence_refs:[],source:"AUTOMATIC"}]}],evidence:{facts:[],metrics:[],gaps:["No independent review yet"],work:[{work_id:"1234",project_id:"99",title:"Frozen work title",completed:null,in_progress:true,delayed:false,changed:null,evidence_refs:[],gaps:[]}]}}}
 const render=()=>renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><ReportSnapshot report={report}/><PeriodSummary period={period} next={{...period,trigger_at:"2026-11-29T16:00:00Z"}}/></IntlAppProvider>)
 const pending=render()
 for(const value of [t.performanceUnknown,t.performanceUnknownNotice,t.performanceCutoff,t.performanceUncovered,t.performanceProjectFilter,t.performanceProjectFilterNotice,`${t.performanceProject} 99`,t.performanceValue_HUMAN,"2026-11-29T16:00:00Z","No independent review yet","Frozen source scheme","Source rule version","9007199254740995"])expect(pending).toContain(value!)
 expect(pending).not.toContain(t.performanceConfirmedBy!)
 report.created_by=null;report.snapshot.assessments[JSON.stringify(["quality","review"])]= {value:8500,evidence_refs:[],reason:"Frozen calibration rationale",actor_id:"reviewer-99",assessed_at:"2026-11-01T08:00:00Z"}
 report.snapshot.status="CONFIRMED";report.snapshot.total_score_bps=850000;report.snapshot.confirmation={revision:2,sha256:report.sha256,actor_id:"reviewer-99",confirmed_at:"2026-11-01T08:00:00Z",reason:"Explicit signed judgment"}
 const confirmed=render();expect(confirmed).toContain(t.performanceStatus_CONFIRMED!);expect(confirmed).toContain("reviewer-99");expect(confirmed).toContain("Explicit signed judgment");expect(confirmed).toContain("85");expect(confirmed).toContain(t.performanceSystem!);expect(confirmed).toContain("Frozen calibration rationale")
 const due=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><DuePeriods status={{pending:[{scheme_id:"9007199254740993",subject_id:"9007199254740995",period}],has_more:true}} onGenerate={()=>{}}/></IntlAppProvider>)
 for(const value of [t.performanceDuePeriods,t.performanceDueNotice,t.performanceMoreDuePeriods,t.performanceGenerate,"9007199254740993","2026-10-01","Asia/Kuala_Lumpur"])expect(due).toContain(value!)
 const empty=renderToStaticMarkup(<IntlAppProvider initialLocale={locale} initialMessages={messages}><DuePeriods status={{pending:[],has_more:false}} onGenerate={()=>{}}/></IntlAppProvider>)
 expect(empty).toContain(t.performanceNoDuePeriods!)
})
