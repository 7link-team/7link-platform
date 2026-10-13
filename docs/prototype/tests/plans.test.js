import { beforeEach, expect, test } from "bun:test";
import { planPeriod, planChecks, planReportFacts, planCanDecide, planConfirmer, planSnapshot } from "../src/plans.js";
const entries = new Map();
Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: key => entries.get(key) || null, setItem: (key, value) => entries.set(key, value), removeItem: key => entries.delete(key) } });
const { read, reset, update, openPeriodPlan, savePeriodPlan, submitPeriodPlan, decidePeriodPlan, recordPlanFreeTask, saveResource, resourceCommand, switchTenant } = await import("../src/store.js");
const get = id => read().resources.plans.find(plan => plan.id === id);
const item = (patch = {}) => ({ id: "PI-1", kind: "FREE_TASK", title: "完成调研", goalId: "G-plan", krId: "KR-plan", freeCategory: "LEARNING", criteria: "保留研究记录", weight: 1, ...patch });
const create = (subjectKey = "agent:atlas") => openPeriodPlan({ subjectKey, periodKind: "WEEK", date: "2026-10-13", timezone: "Asia/Singapore", simulateAgent: true });
const save = (id, items = [item()], reason = "计划范围调整") => savePeriodPlan(id, { items, reason, simulateAgent: true }, get(id).revision);
const submit = id => submitPeriodPlan(id, get(id).revision, true);
const decide = (id, action = "confirm", reason = "核对计划范围") => { const plan = get(id); return decidePeriodPlan(id, { revision: plan.revision, submissionRevision: plan.submission?.revision, contentHash: plan.submission?.contentHash, action, reason }); };
beforeEach(() => {
  reset(); update(s => s.resources.goals.push({ id: "G-plan", name: "成员调研目标", subjectKey: "agent:atlas", ownerId: "atlas", ownerKind: "Agent", status: "进行中", revision: 1, definitionRevision: 1, krs: [{ id: "KR-plan", title: "调研完成", weight: 1 }], alignments: [] }));
});
test("member period key ignores rename and timezone changes; every period uses calendar boundaries", () => {
  const id = create();
  update(s => { s.agents.find(agent => agent.id === "atlas").name = "Renamed"; });
  expect(openPeriodPlan({ subjectKey: "agent:atlas", periodKind: "WEEK", date: "2026-10-14", timezone: "UTC", simulateAgent: true })).toBe(id);
  expect(get(id).timezone).toBe("Asia/Singapore"); expect(read().resources.plans).toHaveLength(1);
  expect(planPeriod("WEEK", "2027-01-01", "UTC")).toEqual({ periodStart: "2026-12-28", periodEnd: "2027-01-03" });
  expect(planPeriod("MONTH", "2024-02-29", "UTC").periodEnd).toBe("2024-02-29");
  expect(planPeriod("QUARTER", "2026-10-13", "UTC").periodEnd).toBe("2026-12-31");
  expect(planPeriod("YEAR", "2026-10-13", "UTC").periodStart).toBe("2026-01-01");
});
test("missing superior and self review cannot produce approval; foreign Human and external writes reject", () => {
  const id = create("human:H-lin");
  update(s => { s.resources.goals.find(goal => goal.id === "G-plan").subjectKey = "human:H-lin"; s.resources.goals.find(goal => goal.id === "G-plan").ownerId = "H-lin"; s.resources.goals.find(goal => goal.id === "G-plan").ownerKind = "Human"; });
  save(id); const before = structuredClone(read());
  expect(() => submit(id)).toThrow("独立确认"); expect(read()).toEqual(before);
  expect(planCanDecide(read(), get(id))).toContain("本人");
  expect(() => create("human:H-zhou")).toThrow("本人");
  update(s => { s.role = "external"; }); expect(() => create()).toThrow();
});
test("submitted content is immutable to stale decisions; semantic replans retain confirmed baseline", () => {
  const id = create(); save(id); submit(id);
  const stale = structuredClone(get(id)); decide(id); const baseline = structuredClone(get(id).confirmedSnapshot);
  save(id, [item({ title: "新增范围", weight: 2 })]);
  expect(get(id).status).toBe("已提交"); expect(get(id).confirmedSnapshot).toEqual(baseline);
  expect(get(id).pendingDiff[0].before.title).toBe("完成调研");
  expect(() => decidePeriodPlan(id, { revision: stale.revision, submissionRevision: stale.submission.revision, contentHash: stale.submission.contentHash, action: "confirm" })).toThrow("修订");
  decide(id, "return", "需要明确新范围"); expect(get(id).confirmedSnapshot).toEqual(baseline);
  save(id, [item({ title: "校准后范围" })]); expect(get(id).status).toBe("已提交"); decide(id);
  expect(get(id).confirmedSnapshot.items[0].title).toBe("校准后范围");
});
test("notes and estimates preserve approval but changing referenced definitions invalidates pending approval", () => {
  const id = create(); save(id); submit(id); decide(id);
  save(id, [item({ note: "补充说明", estimateHours: 2 })]); expect(get(id).status).toBe("已确认");
  save(id, [item({ weight: 3 })]); const before = structuredClone(get(id));
  update(s => { s.resources.goals.find(goal => goal.id === "G-plan").definitionRevision++; });
  expect(() => decide(id)).toThrow("依据已变化"); expect(get(id)).toEqual(before);
});
test("goal contribution, foreign KR, work assignment, duplicate work and size gates reject atomically", () => {
  const id = create();
  for (const items of [[item({ goalId: "missing" })], [item({ krId: "foreign" })], [item({ kind: "WORK_ITEM", workId: "W-103" })], [item({ kind: "WORK_ITEM", workId: "W-104" }), item({ id: "PI-2", kind: "WORK_ITEM", workId: "W-104" })], Array.from({ length: 201 }, (_, i) => item({ id: `PI-${i}` }))]) {
    save(id, items); const before = structuredClone(read()); expect(() => submit(id)).toThrow(); expect(read()).toEqual(before);
  }
  expect(() => saveResource("plans", { ...get(id), status: "已确认" })).toThrow("专用");
  expect(() => resourceCommand("plans", id, "已确认")).toThrow("固定提交");
});
test("work completion requires period-bound accepted evidence and recorded executor identity, not today's team", () => {
  const id = create(); save(id, [item({ kind: "WORK_ITEM", workId: "W-104" })]); submit(id); decide(id);
  expect(planReportFacts(read(), get(id), new Date("2026-10-14T00:00:00Z")).progress).toBe(0);
  update(s => { const work = s.work.find(work => work.id === "W-104"); work.accepted = true; work.status = "待交接"; });
  expect(planReportFacts(read(), get(id)).progress).toBeNull();
  update(s => { const work = s.work.find(work => work.id === "W-104"); work.acceptanceFacts = [{ id: "W-104:v3:accept", workId: "W-104", version: 3, time: "2026-10-13T01:00:00Z", executorIds: ["agent:atlas"] }]; work.agentId = "muse"; });
  let facts = planReportFacts(read(), get(id), new Date("2026-10-14T00:00:00Z")); expect(facts.progress).toBe(1); expect(facts.factKeys).toEqual(["W-104:v3:accept"]);
  update(s => { s.work.find(work => work.id === "W-104").acceptanceFacts[0].time = "2026-10-20T01:00:00Z"; });
  expect(planReportFacts(read(), get(id), new Date("2026-10-21T00:00:00Z")).progress).toBe(0);
});
test("FREE_TASK evidence is explicit and immutable; revised task definition cannot reuse an old completion", () => {
  const today = new Date().toISOString().slice(0, 10);
  const id = openPeriodPlan({ subjectKey: "agent:atlas", periodKind: "MONTH", date: today, timezone: "UTC", simulateAgent: true });
  save(id); submit(id); decide(id);
  expect(() => recordPlanFreeTask(id, "PI-1", { revision: get(id).revision, evidence: "", simulateAgent: true })).toThrow("至少一项");
  recordPlanFreeTask(id, "PI-1", { revision: get(id).revision, evidence: "研究记录：已逐项核对文档", simulateAgent: true });
  const facts = structuredClone(get(id).freeTaskFacts); expect(planReportFacts(read(), get(id)).progress).toBe(1);
  recordPlanFreeTask(id, "PI-1", { revision: get(id).revision, evidence: "重复请求", simulateAgent: true }); expect(get(id).freeTaskFacts).toEqual(facts);
  save(id, [item({ criteria: "新增五个比较项" })]); decide(id); expect(planReportFacts(read(), get(id)).progress).toBe(0); expect(get(id).freeTaskFacts).toEqual(facts);
});

test("daily size, malformed estimates, blank returns and stale edits never alter approved evidence", () => {
  const id = openPeriodPlan({ subjectKey: "agent:atlas", periodKind: "DAY", date: "2026-10-13", timezone: "UTC", simulateAgent: true });
  save(id, Array.from({ length: 31 }, (_, index) => item({ id: `DAY-${index}` })));
  expect(() => submit(id)).toThrow("30");
  for (const patch of [{ weight: "" }, { weight: -1 }, { estimateHours: "2 hours" }, { estimateCostMicros: 0.5 }]) {
    const before = structuredClone(read()); expect(() => save(id, [item(patch)])).toThrow(); expect(read()).toEqual(before);
  }
  save(id); submit(id); const before = structuredClone(read());
  expect(() => decide(id, "return", null)).toThrow("四个字"); expect(read()).toEqual(before);
  expect(() => savePeriodPlan(id, { items: [item()], simulateAgent: true }, 1)).toThrow("修订");
});
test("changed confirmer, cancelled goals and missing contribution cannot approve a frozen old submission", () => {
  const id = create(); save(id); submit(id);
  update(s => { s.agents.find(agent => agent.id === "atlas").owner = "周宁"; });
  expect(() => decide(id)).toThrow("确认人已变化");
  update(s => { s.agents.find(agent => agent.id === "atlas").owner = "林予"; s.resources.goals.find(goal => goal.id === "G-plan").status = "已取消"; });
  expect(() => decide(id)).toThrow("目标");
  expect(get(id).confirmedSnapshot).toBeUndefined();
});
test("another MEMBER objective is not an eligible goal merely because this principal owns it", () => {
  const id = create();
  update(s => { s.resources.goals.find(goal => goal.id === "G-plan").subjectKey = "agent:muse"; });
  save(id); expect(planChecks(read(), get(id)).blocking.some(issue => issue.code === "B3")).toBe(true);
});
test("real period assignment history is accepted; missing history timestamps cannot fabricate eligibility", () => {
  const id = create();
  update(s => { const work = s.work.find(work => work.id === "W-103"); work.team = { executorIds: ["agent:muse"], checkerIds: [], history: [{ time: "2026-10-13T00:00:00Z", previous: { executorIds: ["agent:atlas"] }, next: { executorIds: ["agent:muse"] } }] }; });
  save(id, [item({ kind: "WORK_ITEM", workId: "W-103" })]); expect(planChecks(read(), get(id)).blocking).toHaveLength(0);
  update(s => { delete s.work.find(work => work.id === "W-103").team.history[0].time; });
  expect(planChecks(read(), get(id)).blocking.some(issue => issue.code === "WORK")).toBe(true);
});
test("cross-tenant plan reads or writes and missing facts remain unavailable", () => {
  const id = create(); save(id);
  update(s => { s.activeTenant = "other"; });
  expect(() => savePeriodPlan(id, { items: [item()], simulateAgent: true }, get(id).revision)).toThrow("不存在");
  expect(() => submit(id)).toThrow("不存在");
});


test("actual tenant switch isolates plan settings, plans and approvals", () => {
  const id = create(); save(id); submit(id);
  update(s => { s.planningSettings = { tenantOwnerId: "H-zhou" }; s.tenants.push({ id: "other", name: "Other", role: "owner" }); });
  switchTenant("other");
  expect(read().resources.plans).toHaveLength(0);
  expect(read().planningSettings).toEqual({});
  expect(() => decidePeriodPlan(id, { revision: 3, action: "confirm" })).toThrow("不存在");
  switchTenant("studio");
  expect(get(id).status).toBe("已提交"); expect(read().planningSettings.tenantOwnerId).toBe("H-zhou");
});

test("tenant-owner role cannot self-confirm a Human plan even when a distinct superior is configured", () => {
  update(s => { s.planningSettings = { tenantOwnerId: "H-zhou" }; const goal = s.resources.goals.find(goal => goal.id === "G-plan"); goal.subjectKey = "human:H-lin"; goal.ownerId = "H-lin"; goal.ownerKind = "Human"; });
  const id = create("human:H-lin"); save(id); submit(id);
  expect(get(id).submission.confirmerId).toBe("H-zhou");
  const before = structuredClone(read());
  expect(() => decide(id)).toThrow("本人"); expect(read()).toEqual(before);
});

test("current plan detects a later return instead of counting its historical accepted fact as current completion", () => {
  const id = create(); save(id, [item({ kind: "WORK_ITEM", workId: "W-104" })]); submit(id); decide(id);
  update(s => { const work = s.work.find(work => work.id === "W-104"); work.accepted = false; work.acceptanceFacts = [{ id: "W-104:v3:accept", workId: "W-104", version: 3, time: "2026-10-13T01:00:00Z", executorIds: ["agent:atlas"] }]; });
  expect(planReportFacts(read(), get(id), new Date("2026-10-14T00:00:00Z")).progress).toBe(0);
});


test("owner role cannot replace designated Human confirmation authority", () => {
  for (const role of ["owner", "executor"]) {
    for (const subjectKey of ["human:H-xu", "agent:muse"]) {
      const state = structuredClone(read());
      state.role = role; state.planningSettings = { tenantOwnerId: "H-zhou" };
      state.agents.find(agent => agent.id === "muse").ownerPrincipalId = "H-zhou";
      const plan = { subjectKey, submission: { confirmerId: "H-zhou" } };
      expect(planConfirmer(state, subjectKey).id).toBe("H-zhou");
      expect(planCanDecide(state, plan)).toContain("指定 Human");
    }
    const state = structuredClone(read()); state.role = role;
    expect(planCanDecide(state, { subjectKey: "agent:atlas", submission: { confirmerId: "H-lin" } })).toBeNull();
    state.planningSettings = { tenantOwnerId: "H-zhou" };
    expect(planCanDecide(state, { subjectKey: "human:H-lin", submission: { confirmerId: "H-zhou" } })).toContain("本人");
  }
});
test("unauthorized owner confirm and return reject valid submissions without any mutation", () => {
  const id = create(); save(id); submit(id);
  for (const subjectKey of ["human:H-xu", "agent:atlas"]) {
    update(s => {
      s.role = "owner"; s.planningSettings = { tenantOwnerId: "H-zhou" };
      s.agents.find(agent => agent.id === "atlas").ownerPrincipalId = "H-zhou";
      const plan = s.resources.plans.find(plan => plan.id === id);
      plan.subjectKey = subjectKey;
      const goal = s.resources.goals.find(goal => goal.id === "G-plan");
      goal.subjectKey = subjectKey; goal.ownerId = subjectKey.split(":")[1]; goal.ownerKind = subjectKey.startsWith("human:") ? "Human" : "Agent";
      plan.submission.snapshot = planSnapshot(s, plan);
      plan.submission.contentHash = JSON.stringify(plan.submission.snapshot);
      plan.submission.confirmerId = "H-zhou";
      plan.submission.actor = { kind: goal.ownerKind, id: goal.ownerId };
    });
    expect(planChecks(read(), get(id)).blocking).toEqual([]);
    expect(planConfirmer(read(), subjectKey).id).toBe("H-zhou");
    for (const action of ["confirm", "return"]) {
      const before = structuredClone(read());
      const persisted = [...entries.entries()];
      expect(() => decide(id, action)).toThrow("指定 Human");
      expect(read()).toEqual(before);
      expect([...entries.entries()]).toEqual(persisted);
    }
  }
});
