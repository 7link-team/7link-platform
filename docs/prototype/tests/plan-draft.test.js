import { expect, test } from "bun:test";
import { initial } from "../src/data.js";
import { periodPlanDraftProposal, planSnapshot, ensureReportNextPlanDraft } from "../src/plans.js";
import { reportDetailFingerprint, reportNextPlanItems } from "../src/report-content.js";
import { upsertPeriodicReport } from "../src/reporting.js";
const descriptor = { subjectKey: "agent:atlas", periodKind: "WEEK", periodStart: "2026-10-19", periodEnd: "2026-10-25", timezone: "UTC" };
const now = new Date("2026-10-14T00:00:00Z");
function fixture() {
  const s = structuredClone(initial); s.activeTenant = "studio"; s.tenants = [{ id: "studio" }]; s.role = "owner"; s.resources.plans = []; s.work = [];
  s.agents.find(a => a.id === "atlas").ownerPrincipalId = "H-lin";
  s.resources.goals = [{ id: "G", subjectKey: "agent:atlas", ownerKind: "Agent", ownerId: "atlas", status: "进行中", revision: 2, definitionRevision: 2,
    krs: [{ id: "KR", title: "客户激活", baseline: 0, target: 10, direction: "INCREASE", weight: 1, definitionRevision: 1 }], alignments: [] }];
  return s;
}
function draft(s, report, d = descriptor) { let n = 0; return periodPlanDraftProposal(s, d, report, () => `PI-${++n}`, now); }
function previous(s, entries) {
  const p = { id: "PREVIOUS", tenantId: "studio", subjectKey: descriptor.subjectKey, periodKind: "WEEK", periodStart: "2026-10-12", periodEnd: "2026-10-18", timezone: "UTC", revision: 3, status: "已确认",
    items: [{ id: "ORIGINAL", kind: "WORK_ITEM", title: "原承诺", goalId: "G", krId: "KR", workId: "W", weight: 2 }] };
  p.confirmedSnapshot = { ...planSnapshot(s, p), revision: 3 };
  const r = { id: "REPORT", subjectKey: descriptor.subjectKey, cadence: "周报", periodStart: p.periodStart, periodEnd: p.periodEnd, timezone: "UTC", revision: 5, status: "已提交" };
  const binding = { planId: p.id, tenantId: "studio", sourceRevision: 3, revision: 3, status: "已确认", confirmed: true, snapshot: p.confirmedSnapshot };
  r.submission = { revision: 4, content: { ...r, references: [], currentPlanBinding: binding, content: { uncompleted: entries } } };
  r.content = { uncompleted: [] }; // Today's edited content cannot rewrite the fixed carry decision.
  return r;
}
test("system suggestions retain unknown KR status and do not mutate state or create work/approval", () => {
  const s = fixture(), before = structuredClone(s), result = draft(s);
  expect(s).toEqual(before); expect(result.items).toHaveLength(1);
  expect(result.items[0].title).toBe("建议：推进 客户激活"); expect(result.items[0].goalId).toBe("G");
  expect(result.sources[0]).toMatchObject({ kind: "KR_SUGGESTION", definitionRevision: 2, krId: "KR" });
  expect(result.checks.blocking).toEqual([]); expect(result.checks.warnings.some(w => w.code === "N2")).toBe(true);
});
test("all currently eligible work statuses draft; accepted, waiting, foreign and historical-only assignments do not", () => {
  const s = fixture();
  const statuses = ["READY", "CLAIMED", "IN_PROGRESS", "REWORK", "待开始", "待认领", "进行中", "返工", "待验收", "已完成", "等待", "已取消"];
  s.work = statuses.map((status, n) => ({ id: `W-${n}`, title: `工作${n}`, status, version: 1, team: { executorIds: ["agent:atlas"] } }));
  s.work.push({ id: "HISTORY", title: "仅历史分派", status: "READY", team: { executorIds: [], history: [{ previous: { executorIds: ["agent:atlas"] } }] } },
    { id: "FOREIGN", title: "他人工作", status: "READY", team: { executorIds: ["agent:muse"] } });
  const result = draft(s), work = result.items.filter(i => i.kind === "WORK_ITEM");
  expect(work.map(i => i.workId)).toEqual(statuses.slice(0, 8).map((_, n) => `W-${n}`));
  expect(work.every(i => i.goalId === "")).toBe(true);
  expect(result.checks.blocking.filter(i => i.code === "B3")).toHaveLength(8);
});
test("fixed carry decisions preserve original identity, weight and references, deduplicating assigned work", () => {
  const s = fixture(); s.work = [{ id: "W", title: "当前标题", status: "IN_PROGRESS", version: 4, team: { executorIds: ["agent:atlas"] } }];
  const report = previous(s, [{ key: "U", title: "原承诺", plan_item_id: "ORIGINAL", carry_over: "CARRY_TO_NEXT" }]);
  const before = structuredClone(report), result = draft(s, report), carried = result.items.filter(i => i.workId === "W");
  expect(report).toEqual(before); expect(carried).toHaveLength(1);
  expect(carried[0]).toMatchObject({ id: "PI-1", title: "原承诺", carriedFromItemId: "ORIGINAL", weight: 2, goalId: "G", krId: "KR" });
  expect(result.sources[0]).toMatchObject({ kind: "REPORT_CARRY", reportId: "REPORT", reportRevision: 4, planRevision: 3, originalItemId: "ORIGINAL" });
});
test("drop and escalation do not carry; draft report and missing confirmed source remain explicit issues", () => {
  const s = fixture(), report = previous(s, ["DROP", "ESCALATE"].map(carry_over => ({ plan_item_id: "ORIGINAL", carry_over })));
  expect(draft(s, report).sources.some(i => i.kind === "REPORT_CARRY")).toBe(false);
  report.status = "草稿";
  expect(draft(s, report).issues.join()).toContain("未固定提交");
  report.status = "已提交"; report.submission.content.content.uncompleted = [{ title: "未知来源", plan_item_id: "MISSING", carry_over: "CARRY_TO_NEXT" }];
  expect(draft(s, report).issues.join()).toContain("缺少已确认");
});
test("only eligible open goal KR suggestions are generated; proven achieved KR is omitted", () => {
  const s = fixture(), g = s.resources.goals[0];
  g.krs[0].currentObservationId = "O"; g.krs[0].observations = [{ id: "O", definitionRevision: 1, availability: "available", value: 10, observedAt: now.toISOString() }];
  expect(draft(s).items).toHaveLength(0);
  g.krs[0].observations[0].definitionRevision = 0;
  expect(draft(s).items).toHaveLength(1);
  for (const status of ["草稿", "已达成", "未达成", "已取消"]) { g.status = status; expect(draft(s).items).toHaveLength(0); }
  g.status = "进行中"; g.subjectKey = "agent:muse"; g.ownerId = "muse";
  expect(draft(s).items).toHaveLength(0);
});
test("explicit work goal mapping keeps real references and rejects foreign KR contribution", () => {
  const s = fixture(); s.work = [{ id: "W", title: "真实工作", status: "READY", version: 1, goalId: "G", krId: "FOREIGN", team: { checkerIds: ["agent:atlas"] } }];
  let item = draft(s).items[0]; expect(item.goalId).toBe("G"); expect(item.krId).toBe("");
  s.work[0].krId = "KR"; expect(draft(s).items[0].krId).toBe("KR");
});
test("real quality rework hold is drafted despite waiting display; generic waiting and cancellation are excluded", () => {
  const s = fixture();
  s.work = ["formal", "internal", "configuration", "dependency"].map(kind => ({ id: kind, title: kind, status: "等待", team: { executorIds: ["agent:atlas"] }, rework: { hold: { kind } } }));
  s.work.push({ id: "CANCELLED", title: "已取消", status: "已取消", team: { executorIds: ["agent:atlas"] }, rework: { hold: { kind: "formal" } } });
  expect(draft(s).items.filter(i => i.kind === "WORK_ITEM").map(i => i.workId)).toEqual(["formal", "internal"]);
});
test("scope, corrupted period, unrelated previous report and overflow refuse without state mutation", () => {
  const s = fixture(), before = structuredClone(s);
  expect(() => draft(s, null, { ...descriptor, subjectKey: "project:launch" })).toThrow("真实成员");
  expect(() => draft(s, null, { ...descriptor, periodEnd: "2026-10-26" })).toThrow("日历周期");
  const report = previous(s, []); report.subjectKey = "human:H-lin";
  expect(() => draft(s, report)).toThrow("紧邻上周期"); expect(s).toEqual(before);
  s.work = Array.from({ length: 201 }, (_, n) => ({ id: `W-${n}`, title: `工作${n}`, status: "READY", team: { executorIds: ["agent:atlas"] } }));
  const full = structuredClone(s); expect(() => draft(s)).toThrow("未截断"); expect(s).toEqual(full);
});
test("fixed report body cannot import another member or calendar period into carryover", () => {
  for (const field of ["subjectKey", "periodStart", "periodEnd", "cadence", "timezone"]) {
    const s = fixture(), report = previous(s, [{ key: "U", plan_item_id: "ORIGINAL", carry_over: "CARRY_TO_NEXT" }]);
    report.submission.content[field] = field === "subjectKey" ? "agent:muse" : "CORRUPT";
    if (field === "subjectKey") report.submission.content.currentPlanBinding.snapshot.subjectKey = "agent:muse";
    const before = structuredClone(s); expect(() => draft(s, report)).toThrow("原记录冲突"); expect(s).toEqual(before);
  }
});
test("English terminal states cannot be resurrected by a quality hold", () => {
  const s = fixture();
  s.work = ["CANCELLED", "ACCEPTED", "COMPLETED"].map(status => ({ id: status, title: status, status, team: { executorIds: ["agent:atlas"] }, rework: { hold: { kind: "formal" } } }));
  expect(draft(s).items.filter(i => i.kind === "WORK_ITEM")).toEqual([]);
});
test("future or invalid observation time is not proof a KR was achieved at drafting time", () => {
  for (const observedAt of ["2099-01-01T00:00:00Z", "invalid", undefined]) {
    const s = fixture(), kr = s.resources.goals[0].krs[0];
    kr.currentObservationId = "O"; kr.observations = [{ id: "O", definitionRevision: 1, availability: "available", value: 10, observedAt }];
    expect(draft(s).sources.filter(i => i.kind === "KR_SUGGESTION")).toHaveLength(1);
  }
});
test("draft report can carry an explicitly author-confirmed disposition without submitting the whole report", () => {
  const s = fixture(), report = previous(s, []);
  const entry = { key: "U", title: "原承诺", plan_item_id: "ORIGINAL", reason: "WAITING_CLIENT", detail: "等待资料", carry_over: "CARRY_TO_NEXT" };
  report.status = "草稿"; report.currentPlanBinding = report.submission.content.currentPlanBinding;
  report.content = { uncompleted: [entry] };
  expect(draft(s, report).sources.some(i => i.kind === "REPORT_CARRY")).toBe(false);
  entry.detail_confirmation = { fingerprint: reportDetailFingerprint(entry), actorId: "atlas", actorKind: "Agent" };
  expect(draft(s, report).sources.find(i => i.kind === "REPORT_CARRY")).toMatchObject({ fixedReport: false, reportRevision: 5, originalItemId: "ORIGINAL" });
  entry.detail = "变更原因后未重新确认";
  expect(draft(s, report).sources.some(i => i.kind === "REPORT_CARRY")).toBe(false);
});
test("unreadable fixed report never falls back to today's author-confirmed draft", () => {
  for (const status of ["已提交", "待审核", "已接受"]) {
    const s = fixture(), report = previous(s, []), entry = { key: "U", title: "当前稿", plan_item_id: "ORIGINAL", reason: "OTHER", detail: "已核对说明", carry_over: "CARRY_TO_NEXT" };
    entry.detail_confirmation = { fingerprint: reportDetailFingerprint(entry), actorId: "atlas", actorKind: "Agent" };
    report.status = status; report.currentPlanBinding = report.submission.content.currentPlanBinding; report.content = { uncompleted: [entry] }; delete report.submission.content.references;
    expect(() => draft(s, report)).toThrow("不能降级");
  }
});
const options = { subjectKey: "agent:atlas", cadence: "周报", date: "2026-10-14", timezone: "UTC" };
function generate(s, source = "本地报告测试") { let n = 0; return upsertPeriodicReport(s, options, source, prefix => `${prefix}-${++n}`); }
test("actual report generation creates one real next draft and binds exactly its revision without approving anything", () => {
  const s = fixture(), beforeWork = structuredClone(s.work), beforeGoals = structuredClone(s.resources.goals), rid = generate(s);
  const report = s.resources.reports.find(r => r.id === rid), p = s.resources.plans[0];
  expect(s.resources.plans).toHaveLength(1); expect(p).toMatchObject({ ...descriptor, status: "草稿", revision: 1, draftedBy: { kind: "System" } });
  expect(p.items).toHaveLength(1); expect(p.confirmedSnapshot).toBeUndefined(); expect(p.submission).toBeUndefined();
  expect(report.nextPlanBinding).toMatchObject({ planId: p.id, revision: p.revision, tenantId: s.activeTenant, status: "草稿" });
  expect(report.content.next_plan).toEqual(reportNextPlanItems(report.nextPlanBinding));
  expect(report.suggestedContent.next_plan).toEqual([]); expect(report.suggestedContent).not.toBe(report.content);
  expect(report.content.completed).toEqual([]); expect(s.work).toEqual(beforeWork); expect(s.resources.goals).toEqual(beforeGoals);
  expect(generate(s, "本地定时重试")).toBe(rid); expect(s.resources.plans).toHaveLength(1);
});
test("generation preserves existing authored Plan timezone, confirmation, revisions and contents", () => {
  const s = fixture(), p = { ...descriptor, timezone: "Asia/Singapore", tenantId: "studio", id: "EXISTING", revision: 7, status: "已确认", items: [{ id: "REAL", kind: "FREE_TASK", title: "本人已确认的计划", goalId: "G", krId: "KR", freeCategory: "OTHER", criteria: "保留实际依据", weight: 2 }], history: [] };
  p.confirmedSnapshot = { ...planSnapshot(s, p), revision: 7 }; s.resources.plans.push(p);
  const before = structuredClone(p), rid = generate(s), r = s.resources.reports.find(r => r.id === rid);
  expect(s.resources.plans).toEqual([before]); expect(r.nextPlanBinding).toMatchObject({ planId: p.id, revision: 7, confirmedSnapshot: before.confirmedSnapshot });
  expect(r.nextPlanBinding.snapshot.timezone).toBe("Asia/Singapore"); expect(r.content.next_plan[0].title).toBe("本人已确认的计划");
});
test("duplicate, unreadable and overflow Plan sources never create an arbitrary or truncated next Plan", () => {
  const s = fixture(), r = { ...options, periodStart: "2026-10-12", periodEnd: "2026-10-18" };
  const p = { ...descriptor, tenantId: "studio", id: "D1", revision: 1, status: "草稿", items: [] };
  s.resources.plans = [p, { ...p, id: "D2" }]; let before = structuredClone(s);
  expect(() => ensureReportNextPlanDraft(s, r, () => "NEW", now)).toThrow("重复计划"); expect(s).toEqual(before);
  s.resources.plans = []; s.role = "executor"; const other = { ...r, subjectKey: "human:H-zhou" }; before = structuredClone(s);
  expect(() => ensureReportNextPlanDraft(s, other, () => "NEW", now)).toThrow("无权读取"); expect(s).toEqual(before);
  s.role = "owner"; s.work = Array.from({ length: 201 }, (_, n) => ({ id: `W-${n}`, title: `工作${n}`, status: "READY", team: { executorIds: ["agent:atlas"] } })); before = structuredClone(s);
  expect(() => ensureReportNextPlanDraft(s, r, () => "NEW", now)).toThrow("未截断"); expect(s).toEqual(before);
});
test("aggregate report generation cannot create fictitious member Plans", () => {
  const s = fixture();
  for (const subjectKey of ["project:launch", "team:ORG-engineering", "tenant:studio"]) {
    let n = 0; const rid = upsertPeriodicReport(s, { ...options, subjectKey }, "本地汇总测试", prefix => `${prefix}-${subjectKey}-${++n}`), r = s.resources.reports.find(r => r.id === rid);
    expect(s.resources.plans).toEqual([]); expect(r.nextPlanBinding).toBeUndefined(); expect(r.content.next_plan).toEqual([]);
  }
});
