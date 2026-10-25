import {describe,expect,it} from "bun:test"
import {projectMetricMessages,projectMetricsCSV,readProjectMetrics,type ProjectMetrics,type ProjectMetricSnapshot} from "./project-metrics-data"

describe("project metric source and export contracts",()=>{
 it("binds project, version cohort and window to the authenticated request and abort signal",async()=>{
  const original=globalThis.fetch,seen:{url:string;signal?:AbortSignal|null}[]=[]
  globalThis.fetch=(async(input:RequestInfo|URL,init?:RequestInit)=>{seen.push({url:String(input),signal:init?.signal});return new Response(JSON.stringify({code:0,data:{projects:[]}}),{headers:{"Content-Type":"application/json"}})}) as typeof fetch
  try{const controller=new AbortController();await readProjectMetrics("1",{from:"2026-10-01",to:"2026-10-22"},"9007199254740993",controller.signal,{model:"model/v2",verifier:"judge v3",workflow:"42:hash"});const url=new URL(seen[0]!.url,"https://local.invalid");expect(url.pathname).toBe("/api/tenants/1/metrics/projects");expect(url.searchParams.get("project_id")).toBe("9007199254740993");expect(url.searchParams.get("model")).toBe("model/v2");expect(url.searchParams.get("verifier")).toBe("judge v3");expect(url.searchParams.get("workflow")).toBe("42:hash");expect(url.searchParams.get("from")).toBe("2026-10-01");expect(seen[0]!.signal).toBe(controller.signal);expect(()=>readProjectMetrics("1",{},"../other")).toThrow()}finally{globalThis.fetch=original}
 })
 it("exports exact large amounts with window, versions, missing denominators and selection",()=>{
  const snapshot:ProjectMetricSnapshot={project_id:"3",work_items:1,scope_changed:0,kpis:{KNOWN_COST_LOWER_BOUND_MICROS:{numerator:"9007199254740993",denominator:"2",value:"4503599627370496.500000",missing:1,missing_denominator:2,missing_rate:"0.500000",lower_bound:true,reason:'Known "settled" only'}},versions:{model:["v2"]},missing_sources:["LABOR_PRICING"],internal_rounds:{samples:4,sum:"18",mean:"4.5",p50:"4",p90:"8",p95:"8",unit:"rounds"},rework_cycle:{samples:0,sum:"0",mean:null,p50:null,p90:null,p95:null,unit:"ms"}}
  const data:ProjectMetrics={version:"project-metrics-v1",from:"2026-10-01",to:"2026-10-22",computed_at:"2026-10-23",cohort:"VISIBLE_WORK_WINDOW_UTC",selection:{model:"v2",verifier:"",workflow:""},projects:[snapshot],team:snapshot}
  const csv=projectMetricsCSV(data);expect(csv).toContain('"9007199254740993"');expect(csv).toContain('"4503599627370496.500000"');expect(csv).toContain('"Known ""settled"" only"');expect(csv).toContain('"missing_denominator"');expect(csv).toContain('"LABOR_PRICING"');expect(csv).toContain('"2026-10-22"');expect(csv).toContain('"model""');expect(csv).toContain('"INTERNAL_ROUNDS"');expect(csv).toContain('"TEAM"')
 })
 it("provides matching concrete labels in three locales",()=>{const base=projectMetricMessages["en-US"];for(const l of Object.values(projectMetricMessages)){expect(Object.keys(l).sort()).toEqual(Object.keys(base).sort());expect(Object.keys(l.keys).sort()).toEqual(Object.keys(base.keys).sort());for(const value of Object.values(l.keys))expect(value.trim().length).toBeGreaterThan(0)}})
})
