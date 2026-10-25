// Isolated UI contract fixture. Responses are explicit fixtures, not live facts.
import {createRoot} from "react-dom/client"
import {createMemoryRouter, Outlet, RouterProvider, useLocation} from "react-router"
import {IntlAppProvider} from "@workspace/ui/i18n/intl-provider"
import {AuthContext} from "../../src/components/auth"
import {loadMessages} from "../../src/lib/i18n"
import Dashboard from "../../src/app/dashboard/page"
import Agents, {AgentOperationsPage} from "../../src/app/agents/page"
import Goals, {PlanningPage} from "../../src/app/goals/page"
import Templates from "../../src/app/templates/page"
import {softwareDefinition} from "../../src/app/templates/workflow"
import {MemoryReviewPage} from "../../src/app/knowledge/page"
import RetrievalDebugPage from "../../src/app/knowledge/retrieval-debug"
import QualityEscapesPage from "../../src/app/governance/quality-escapes"
import {WorkflowConfigPage,WorklogsPage} from "../../src/app/work-management/page"
import SpawnGrantsPage from "../../src/app/governance/spawn-grants"
import "../../src/index.css"

const params = new URLSearchParams(location.search)
const page = params.get("page") ?? "home"
const locale = params.get("locale") ?? "en-US"
const denied = params.has("denied")
const requests: {method: string; url: string; body?: unknown; key?: string | null}[] = []
Object.assign(window, {fixtureRequests: requests})
const nativeFetch = window.fetch.bind(window)
const definition = softwareDefinition()
const version = {id: "91", key: "fixture-template", state: "PUBLISHED", hash: "a".repeat(64), version: 1, definition}
let goalVersion = 1
let hasContributor = false
let learningState = "PENDING"
let workflowVersion = 1
let fixtureWorkflow = {version:1,source:"PROJECT",team_version:3,states:[{key:"draft",display_name:"Draft",group:"TODO",maps_to:"DRAFT"},{key:"ready",display_name:"Ready",group:"TODO",maps_to:"READY"}],transitions:[{from:"draft",to:"ready",enabled:true,manual_allowed:true,conditions:[]}]}
const escape = () => ({id:"61",project_id:"2",work_item_id:"77",stage:"HUMAN_REVIEW",category:"FUNCTIONAL_DEFECT",summary:"Exact-result Human escape fixture",learning_state:learningState,version:learningState==="PENDING"?1:2,passed_by:[{stage:"AGENT_ACCEPTANCE",principal_id:"31",model_family:"fixture-family",verifier_version:"a".repeat(64)}],memory_ids:learningState==="PENDING"?[]:["71"]})
let spawnVersion = 1
let spawnState = params.has("exhausted") ? "EXHAUSTED" : "ACTIVE"
const spawnGrant = () => ({id: "90", grantee_agent_id: "9007199254740993", grantee_principal_id: "33", granted_by: "11", state: spawnState, effective_state: spawnState, effective_approval_mode: "REQUIRE_APPROVAL", project_ids: ["2"], max_active_agents: 3, max_spawns_per_day: 5, allowed_roles: ["EXECUTOR"], allowed_machine_ids: ["5"], allowed_models: [{runtime: "CODEX", model: "fixture-model"}], max_effort: "medium", budget_cap_micros: 50000000, per_agent_cap_micros: 10000000, agent_ttl_hours: 168, idle_retire_hours: 48, approval_mode: "REQUIRE_APPROVAL", active_count: 1, spawned_today: 2, spent_micros: "9007199254740993", reason: "Explicit capacity fixture", starts_at: "2026-10-20T00:00:00Z", ends_at: "2040-10-22T00:00:00Z", version: spawnVersion})
const contributorGoal = () => ({id:"42",project_id:"2",principal_id:"11",kind:"goal",period_kind:"QUARTER",period_start:"2026-10-01",state:"ACTIVE",content_hash:"b".repeat(64),version:goalVersion,content:{level:"PROJECT",title:"Actual aligned goal fixture",key_results:[{key:"quality",title:"Quality output",source:"MANUAL",baseline:0,target:10,current:3}]}})
window.fetch = async (input, init) => {
  const url = String(input)
  if (!url.includes("/api/")) return nativeFetch(input, init)
  const method = init?.method ?? "GET"
  requests.push({method, url, ...(init?.body ? {body: JSON.parse(String(init.body)), key: new Headers(init.headers).get("Idempotency-Key")} : {})})
  const path = new URL(url, location.origin).pathname
  let data: unknown = {items: [], has_more: false}
  if (path.includes("/workflow-configs/")) {
    if(method==="PUT") {const body=JSON.parse(String(init?.body));fixtureWorkflow={...fixtureWorkflow,...body,version:++workflowVersion}}
    data={...fixtureWorkflow,source:new URL(url,location.origin).searchParams.has("team_default")?"TEAM":"PROJECT"}
  }
  if(path.endsWith("/worklogs")) data={items:[{id:"51",principal_id:"12",work_item_id:"77",actor_kind:"HUMAN",category:"DEVELOPMENT",work_date:"2026-10-20",minutes:60,cost_micros:null,note:"Actual work fixture",state:"SUBMITTED",version:1}],has_more:false}
  if(path.endsWith("/worklog-summary")) data={approved_minutes:60,known_cost_micros:0,unknown_cost_count:1,by_work_item:{"77":60},by_date:{"2026-10-20":60},by_category:{DEVELOPMENT:60},by_principal:{"12":60}}
  if(path.includes("/quality-escapes")) {
    if(path.endsWith("/learning")) {learningState="RECORDED";data=escape()}
    else if(path.endsWith("/61")) data=escape()
    else return Response.json({code:0,data:[escape()],meta:{page:{total:1,limit:50,offset:0}}})
  }
  if (path.includes("/spawn-grants")) {
    if (params.has("spawn-error")) return Response.json({code: 160503, message: "Fixture spawn source unavailable"}, {status: 503})
    if (path.endsWith("/revocations")) {spawnState = "REVOKED"; spawnVersion++; data = spawnGrant()}
    else if (path.endsWith("/members")) {
      const offset = Number(new URL(url, location.origin).searchParams.get("offset") ?? "0")
      const all = Array.from({length: params.has("member-pages") ? 120 : 1}, (_, index) => ({agent_id: String(params.has("member-pages") ? 1000 + index : 44), state: "ACTIVE", spent_micros: "1000000", per_agent_cap_micros: "10000000", reserved_at: "2026-10-20T00:00:00Z"}))
      return Response.json({code: 0, data: all.slice(offset, offset + 50), meta: {page: {total: all.length, limit: 50, offset}}})
    }
    else if (path.endsWith("/90")) data = spawnGrant()
    else return Response.json({code: 0, data: [spawnGrant()], meta: {page: {total: 1, limit: 50, offset: 0}}})
  }
  if (path.endsWith("/goals/42/alignment")) data = {goal_id:"42",version:goalVersion,coverage:.5,uncovered:["quality"],children:[],visibility_limited:false,contributors:[{kr:"quality",principal_id:"11",source:"OWNER"},...(hasContributor?[{kr:"quality",principal_id:"33",source:"ASSIGNED"}]:[])]}
  if (path.endsWith("/goals/42/changes")) {const change = JSON.parse(String(init?.body));hasContributor=change.action==="ASSIGN_CONTRIBUTOR";goalVersion++;data=contributorGoal()}
  if (path.endsWith("/knowledge/retrieval-debug")) {
    if (params.has("fail-debug") && requests.filter(r => r.url.endsWith("/knowledge/retrieval-debug")).length === 1) return new Response(JSON.stringify({code:160503,message:"Fixture retrieval unavailable"}), {status:503,headers:{"Content-Type":"application/json"}})
    const body = JSON.parse(String(init?.body))
    const entry = {id:"82",scope:body.scope,kind:"DOCUMENT",state:"ACTIVE",title:"Actual fixture match",body:"Export amount from stored entry",provenance:"Explicit fixture source",version:1,content_hash:"a".repeat(64),created_by:"11",created_at:"2026-10-20T00:00:00Z"}
    data = {query:body,actor_id:"11",engine:"LEXICAL_ENTRIES",tokens:["export","amount"],scanned:2,truncated:false,evaluated_at:"2026-10-20T12:00:00Z",candidates:[{entry,text_raw:2,t:1,r:1,w:.6,s:.7,final:.895,text_rank:1,rank:1},{entry:{...entry,id:"83",title:"Excluded pending rule",kind:"RULE",state:"PENDING_REVIEW"},text_raw:0,t:0,r:0,w:0,s:0,final:0,text_rank:0,rank:0,exclusion:"INACTIVE"}]}
  }
  if (path.endsWith("/me")) data = {subject: "11", organization_id: "1"}
  if (path.endsWith("/sessions")) data = []
  if (path.endsWith("/workbench-summary")) {
    if (params.has("summary-error") || (params.has("summary-retry") && requests.filter(value=>value.url.endsWith("/workbench-summary")).length===1)) return new Response(JSON.stringify({code:503,message:"Fixture summary unavailable"}),{status:503,headers:{"Content-Type":"application/json"}})
    data = {from:"2026-10-13T00:00:00Z",to:"2026-10-21T00:00:00Z",projects:2,work_items:8,open:5,blocked:1,review:2,accepted:3,execution_created:4,load:[{principal_id:"11",open:5,blocked:1,review:2}],acceptances:[{project_id:"2",work_item_id:"77",title:"Actual accepted fixture work",accepted_at:"2026-10-20T09:00:00Z"}],missing_sources:["EXECUTION_COST"]}
  }
  if (path.endsWith("/inbox-items")) data = {items: Array.from({length: 6}, (_, index) => ({id: String(index + 1), object_id: String(index + 1), project_id: "2", kind: "PLAN_CONFIRMATION", priority: "HIGH", title: `Decision ${index + 1}`, object_type: "plan", created_at: "2026-10-20T12:00:00Z", source_principal_id: "11", source_scope: "MEMBER"})), has_more: false, capabilities: [{kind: "QUALITY_ESCAPE", status: "UNAVAILABLE"}]}
  if (path.endsWith("/projects")) data = [{project_id: "2", name: "Authorized project", stage: "ACTIVE", health: "GREEN", counts: {}, budget: {}}]
  if (path.endsWith("/my-work")) data = {items: [{id: "7", project_id: "2", title: "My actual fixture work", state: "ACTIVE", responsibility: "accountable"}], has_more: false}
  if (path.endsWith("/agent-operations")) data = {operations: [
    {name: "work.claim", method: "POST", path: "/work/claims", summary: "Fixture registered claim operation", required_permission: "work_claim", delegable: false, human_only: false, side_effects: ["WRITES_STATE"], parameters: [], input_schema: {type: "object"}, output_schema: {type: "object"}},
    {name: "work.read", method: "GET", path: "/work", summary: "Fixture read operation", required_permission: "work_read", delegable: false, human_only: false, side_effects: ["READ_ONLY"], parameters: [], input_schema: {}, output_schema: {}},
    {name: "work.approve", method: "POST", path: "/work/approval", summary: "Fixture human operation", required_permission: "work_approve", delegable: false, human_only: true, side_effects: ["WRITES_STATE"], parameters: [], input_schema: {}, output_schema: {}},
  ]}
  if (/\/(goals|plans)\/42$/.test(path)) {
    const kind = path.includes("/goals/") ? "goals" : "plans"
    data = {id: "42", project_id: "2", principal_id: "11", kind: kind === "goals" ? "GOAL" : "PLAN", period_kind: "QUARTER", period_start: "2026-10-01", state: "SUBMITTED", content_hash: "b".repeat(64), version: 1, content: kind === "goals" ? {level: "MEMBER", title: "Exact linked goal", key_results: [{key: "quality", title: "Exact key result", source: "MANUAL", baseline: 0, target: 10, current: 3}]} : {items: [{title: "Exact linked plan item", goal_id: "42", kr: "quality"}]}}
    if (params.has("contributions") && kind === "goals") data=contributorGoal()
  }
  if (path.endsWith("/templates")) data = {items: [version], has_more: false}
  if (path.endsWith("/templates/91")) data = version
  if (path.endsWith("/knowledge")) data = {items: [{id: "81", title: "Pending review rule", scope: "TEAM", kind: "RULE", state: "PENDING_REVIEW", version: 1, content: "Actual fixture rule content", source: "HUMAN"}], has_more: false}
  if (params.has("unavailable") && path.endsWith("/inbox-items")) return new Response(JSON.stringify({code: 503, message: "Fixture inbox unavailable"}), {status: 503, headers: {"Content-Type": "application/json"}})
  return new Response(JSON.stringify({code: 0, message: "ok", data}), {headers: {"Content-Type": "application/json"}})
}

const permissions = new Set(denied ? [] : ["project_read", "work_item_read", "goal_read", "template_read", "memory_approve", "knowledge_manage", "knowledge_read", "policy_read","policy_manage","audit_read","knowledge_write","workflow_manage","worklog_approve", ...(params.has("spawn-manage")?["spawn_grant_manage"]:[]), ...(params.has("contributions")?["goal_manage"]:[])])
export function Shell() {
  const current = useLocation()
  return <AuthContext.Provider value={{status: "authenticated", session: {subject: "11", organization_id: "1"}} as never}><main className="min-h-screen p-4"><output data-testid="route">{current.pathname}{current.search}</output><Outlet context={{permissions, permissionsResolved: true, tenantID: "1", menus: []}}/></main></AuthContext.Provider>
}
export function Plans() {return <PlanningPage kind="plans"/>}
const entries = [
  ["", Dashboard], ["agents", Agents], ["governance/agent-operations", AgentOperationsPage],
  ["entities/:entityID/goals", Goals], ["entities/:entityID/plans", Plans],
  ["templates", Templates], ["governance/memory-review", MemoryReviewPage],
  ["entities/:entityID/governance/memory-review", MemoryReviewPage],
  ["knowledge/debug", RetrievalDebugPage], ["entities/:entityID/knowledge/debug", RetrievalDebugPage],
  ["governance/spawn-grants", SpawnGrantsPage],
  ["entities/:entityID/workflow-config",WorkflowConfigPage],["entities/:entityID/worklogs",WorklogsPage],["entities/:entityID/escapes",QualityEscapesPage],
] as const
const initial = page === "quality" ? "/admin/1/entities/2/escapes?escape_id=61" : page === "workflow" ? "/admin/1/entities/2/workflow-config" : page === "worklogs" ? "/admin/1/entities/2/worklogs" : page === "home" ? "/admin/1" : page === "goal" ? "/admin/1/entities/2/goals?record_id=42" : page === "plan" ? "/admin/1/entities/2/plans?record_id=42" : page === "template" ? "/admin/1/templates?version=91" : `/admin/1/${page}`
const router = createMemoryRouter([{element: <Shell/>, children: entries.map(([path, Component]) => ({path: `/admin/:tenantID${path ? `/${path}` : ""}`, element: <Component/>}))}], {initialEntries: [page === "governance/spawn-grants" && !denied ? initial + "?spawn_grant_id=90" : initial]})
void loadMessages(locale).then(messages => createRoot(document.getElementById("root")!).render(<IntlAppProvider initialLocale={locale} initialMessages={messages}><RouterProvider router={router}/></IntlAppProvider>))
