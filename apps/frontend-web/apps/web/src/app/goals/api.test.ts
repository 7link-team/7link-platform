import { afterEach, expect, spyOn, test } from "bun:test"
import { iamApi } from "../../lib/iam-api"
import { planItems, planPath, planningApi, isSystemAggregateDraft, type PlanningRecord } from "./api"
let restore: (() => void) | undefined
afterEach(() => restore?.())
test("canonical plan detail and close links retain their tenant/project scope and exact IDs", () => {
 expect(planPath("7", "", "9007199254740993")).toBe("/admin/7/plans/9007199254740993")
 expect(planPath("7", "")).toBe("/admin/7/plans/me")
 expect(planPath("7", "99", "9007199254740993")).toBe("/admin/7/entities/99/plans/9007199254740993")
 expect(planPath("7", "99")).toBe("/admin/7/entities/99/plans")
})
test("report reopening retries the same reason, current version/hash and idempotency key", async () => {
 const calls: {route: unknown; init?: RequestInit}[] = []
 const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never})
 restore = () => spy.mockRestore()
 const record = {id: "9007199254740993", version: 7, content_hash: "a".repeat(64)} as PlanningRecord
 for (let attempt = 0; attempt < 2; attempt++) await planningApi.decide("1", "reports", record, "REOPEN", "Correct the facts", "reopen-once")
 expect(calls[0]).toEqual(calls[1])
 expect(calls[0]?.route).toEqual({method: "POST", url: "/tenants/1/reports/9007199254740993/decisions"})
 expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key": "reopen-once"})
 expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({version: 7, expected_content_hash: record.content_hash, action: "REOPEN", reason: "Correct the facts"})
})
test("team goal and aggregate requests serialize absent project as the canonical nullable ID", async () => {
 const calls: {init?: RequestInit}[] = []
 const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (_route, init) => {calls.push({init});return {} as never})
 restore = () => spy.mockRestore()
 const period = {project_id: "0", period_kind: "QUARTER", period_start: "2026-10-01"}
 await planningApi.create("7", "goals", {...period, principal_id: "33", goal: {level: "TEAM", title: "Actual team goal", key_results: [{key: "quality", title: "Review", source: "MANUAL", baseline: 0, target: 1, current: 0}]}}, "team-goal")
 await planningApi.aggregate("7", {...period, scope: "TEAM"}, "team-report")
 expect(calls).toHaveLength(2)
 for (const call of calls) expect(JSON.parse(String(call.init?.body)).project_id).toBeNull()
 expect(JSON.parse(String(calls[0]?.init?.body)).principal_id).toBe("33")
 expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key": "team-goal"})
})
test("contributor assignment and removal bind the current goal revision and canonical KR key",async()=>{
 const calls:{route:unknown;init?:RequestInit}[]=[]
 const spy=spyOn(iamApi,"deliveryResultRequest").mockImplementation(async(route,init)=>{calls.push({route,init});return {} as never});restore=()=>spy.mockRestore()
 const record={id:"42",version:3,content_hash:"c".repeat(64)} as PlanningRecord
 await planningApi.changeGoal("7",record,{action:"ASSIGN_CONTRIBUTOR",principal_id:"33",key:"quality"},"assign-once")
 expect(JSON.parse(String(calls[0]?.init?.body))).toEqual({version:3,expected_content_hash:record.content_hash,action:"ASSIGN_CONTRIBUTOR",principal_id:"33",key:"quality"})
 expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key":"assign-once"})
 const abort=new AbortController();await planningApi.alignment("7","42",abort.signal)
 expect(calls[1]?.route).toEqual({method:"GET",url:"/tenants/7/goals/42/alignment"});expect(calls[1]?.init?.signal).toBe(abort.signal)
 await planningApi.changeGoal("7",record,{action:"REMOVE_CONTRIBUTOR",principal_id:"33",key:"quality"},"remove-once")
 expect(JSON.parse(String(calls[2]?.init?.body)).action).toBe("REMOVE_CONTRIBUTOR")
})
test("aggregate report creation and filtering preserve explicit scope without member identity", async () => {
 const calls: {route: unknown; init?: RequestInit}[] = []
 const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never})
 restore = () => spy.mockRestore()
 const body = {scope: "PROJECT" as const, project_id: "99", period_kind: "WEEK", period_start: "2026-10-19"}
 await planningApi.aggregate("7", body, "aggregate-once")
 expect(calls[0]?.route).toEqual({method: "POST", url: "/tenants/7/reports/aggregations"})
 expect(JSON.parse(calls[0]?.init?.body as string)).toEqual(body)
 expect(JSON.parse(calls[0]?.init?.body as string)).not.toHaveProperty("principal_id")
 await planningApi.list("7", "reports", {project: "99", scope: "PROJECT"})
 expect(calls[1]?.route).toEqual({method: "GET", url: "/tenants/7/reports?limit=30&sort=-id&project_id=99&scope=PROJECT"})
})
test("goal lifecycle and plan edits retain content fences and idempotency keys", async () => {
 const calls: {route: unknown; init?: RequestInit}[] = []
 const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never})
 restore = () => spy.mockRestore()
 const record = {id: "9007199254740993", version: 8, content_hash: "b".repeat(64)} as PlanningRecord
 await planningApi.changeGoal("7", record, {action: "CLOSE", result: "CANCELLED", reason: "Scope retired"}, "close-once")
 expect(calls[0]?.route).toEqual({method: "POST", url: "/tenants/7/goals/9007199254740993/changes"})
 expect(JSON.parse(calls[0]?.init?.body as string)).toEqual({version: 8, expected_content_hash: record.content_hash, action: "CLOSE", result: "CANCELLED", reason: "Scope retired"})
 expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key": "close-once"})
 const items = [{title: "Review", goal_id: "9", kr: "quality", category: "REVIEW"}]
 await planningApi.editPlan("7", record, items, "edit-once")
 expect(calls[1]?.route).toEqual({method: "PATCH", url: "/tenants/7/plans/9007199254740993"})
 expect(JSON.parse(calls[1]?.init?.body as string)).toEqual({version: 8, expected_content_hash: record.content_hash, content: {items}})
 expect(calls[1]?.init?.headers).toEqual({"Idempotency-Key": "edit-once"})
})
test("report generation sends no invented report facts and preserves exact retry key", async () => {const calls: {route: unknown; init?: RequestInit}[] = [];const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never});restore = () => spy.mockRestore();const body = {project_id: "99", principal_id: "11", period_kind: "DAY", period_start: "2026-10-20"};await planningApi.create("7", "reports", body, "report-once");await planningApi.create("7", "reports", body, "report-once");expect(calls[0]).toEqual(calls[1]);expect(calls[0]?.route).toEqual({method: "POST", url: "/tenants/7/reports"});expect(JSON.parse(calls[0]?.init?.body as string)).toEqual(body);expect(JSON.parse(calls[0]?.init?.body as string)).not.toHaveProperty("report");expect(calls[0]?.init?.headers).toEqual({"Idempotency-Key": "report-once"})})
test("decisions bind exact content hash and version; list and reconciliation carry scope and abort", async () => {const calls: {route: unknown; init?: RequestInit}[] = [];const spy = spyOn(iamApi, "deliveryResultRequest").mockImplementation(async (route, init) => {calls.push({route, init});return {} as never});restore = () => spy.mockRestore();const record = {id: "9", version: 4, content_hash: "a".repeat(64)} as PlanningRecord;await planningApi.decide("7", "plans", record, "RETURN", "Needs evidence", "decision-once");expect(JSON.parse(calls[0]?.init?.body as string)).toEqual({version: 4, expected_content_hash: "a".repeat(64), action: "RETURN", reason: "Needs evidence"});const abort = new AbortController();await planningApi.list("7", "plans", {project: "99", cursor: "opaque:cursor"}, abort.signal);expect(calls.at(-1)?.route).toEqual({method: "GET", url: "/tenants/7/plans?limit=30&sort=-id&project_id=99&cursor=opaque%3Acursor"});expect(calls.at(-1)?.init?.signal).toBe(abort.signal);await planningApi.reconcile("7", "9", abort.signal);expect(calls.at(-1)?.route).toEqual({method: "GET", url: "/tenants/7/reports/9/reconciliation"})})
test("plan items retain goal/KR links and select work or categorized activity", () => {expect(planItems({item_title_0: " Build ", item_goal_0: "1", item_kr_0: "accepted", item_work_0: "9007199254740993", item_title_1: "Review", item_goal_1: "1", item_kr_1: "quality", item_category_1: "REVIEW"}, 2)).toEqual([{title: "Build", goal_id: "1", kr: "accepted", work_item_id: "9007199254740993"}, {title: "Review", goal_id: "1", kr: "quality", category: "REVIEW"}])})

test("plan schedule and evidenced free-task completion serialize local dates as instants", () => {
 const values = {item_title_0: "Review", item_goal_0: "1", item_kr_0: "quality", item_weight_0: "2.5", item_due_0: "2026-10-20T09:30", item_completed_0: "2026-10-20T08:15", item_evidence_0: " receipt:42 ", item_category_0: "REVIEW"}
 const item = planItems(values, 1)[0]!
 expect(item).toEqual({title: "Review", goal_id: "1", kr: "quality", weight: 2.5, due_at: new Date(values.item_due_0).toISOString(), completed_at: new Date(values.item_completed_0).toISOString(), completion_evidence: "receipt:42", category: "REVIEW"})
 const linked = planItems({...values, item_work_0: "123"}, 1)[0]!
 expect(linked.work_item_id).toBe("123")
 expect(linked).not.toHaveProperty("completed_at")
 expect(linked).not.toHaveProperty("completion_evidence")
 expect(linked).not.toHaveProperty("category")
})
test("only unowned aggregate drafts offer first-save adoption", () => {
 const record = {state: "DRAFT", principal_id: "0", content: {scope: "PROJECT"}} as PlanningRecord
 expect(isSystemAggregateDraft(record)).toBe(true)
 expect(isSystemAggregateDraft({...record, author_id: "0"})).toBe(true)
 expect(isSystemAggregateDraft({...record, content: {scope: "TEAM"} as PlanningRecord["content"]})).toBe(true)
 expect(isSystemAggregateDraft({...record, author_id: "12"})).toBe(false)
 expect(isSystemAggregateDraft({...record, state: "SUBMITTED"})).toBe(false)
 expect(isSystemAggregateDraft({...record, principal_id: "12"})).toBe(false)
 expect(isSystemAggregateDraft({...record, content: {scope: "MEMBER"} as PlanningRecord["content"]})).toBe(false)
})

test("plan edits preserve exact typed carry-over provenance without reading prose", () => {
 const source={title:"Continue",goal_id:"9",kr:"quality",work_item_id:"101",carried_from_report_id:"9007199254740993",carried_from_key:"actual-task",carried_from_hash:"b".repeat(64)}
 const items=planItems({item_title_0:"Continue reviewed task",item_goal_0:"9",item_kr_0:"quality",item_work_0:"101"},1,[source])
 expect(items[0]).toEqual({...source,title:"Continue reviewed task"})
 expect(planItems({item_title_0:"work_item_id=999",item_goal_0:"9",item_kr_0:"quality",item_category_0:"OTHER"},1)[0]).not.toHaveProperty("work_item_id")
})

test("plan annotations preserve item and source identity and allow explicit clearing", () => {
 const source = {id:"9007199254740993", carried_from_item_id:"9007199254740992", title:"Task", goal_id:"9", kr:"quality", category:"OTHER", notes:"Old", estimate_hours:2, estimate_cost_micros:"9223372036854775807"}
 const values = {item_title_0:"Task",item_goal_0:"9",item_kr_0:"quality",item_notes_0:"New",item_hours_0:"1.5",item_cost_0:"0"}
	 expect(planItems({item_title_0:"Task",item_goal_0:"9",item_kr_0:"quality"},1,[source])[0]).toEqual(source)
 expect(planItems(values,1,[source])[0]).toEqual({...source,notes:"New",estimate_hours:1.5,estimate_cost_micros:"0"})
 const cleared=planItems({...values,item_notes_0:"",item_hours_0:"",item_cost_0:""},1,[source])[0]!
 expect(cleared.id).toBe(source.id)
 expect(cleared.carried_from_item_id).toBe(source.carried_from_item_id)
 expect(cleared).not.toHaveProperty("notes")
 expect(cleared).not.toHaveProperty("estimate_hours")
 expect(cleared).not.toHaveProperty("estimate_cost_micros")
})
