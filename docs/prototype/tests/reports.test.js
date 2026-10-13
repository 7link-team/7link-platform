import { reportSubmissionContent } from "../src/report-authorization.js";
import { beforeEach, expect, test } from "bun:test";
import { initial } from "../src/data.js";
if (!globalThis.localStorage) {
  const entries = new Map();
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
    getItem: (key) => entries.get(key) || null,
    setItem: (key, value) => entries.set(key, value),
    removeItem: (key) => entries.delete(key),
  } });
}
const { reportEvidence, read, reset, update, resourceCommand, saveResource, saveHumanEffort } = await import("../src/store.js");
beforeEach(() => reset());
const fresh = () => structuredClone(initial);
const ref = (kind, id, version = 1) => ({ kind, id, version });
const report = (references, extra = {}) => ({ id: "report-root", name: "本周", owner: "林予", status: "草稿",
  periodStart: "2026-10-12", periodEnd: "2026-10-18", references, ...extra });
const install = (r) => update((s) => s.resources.reports.push(r));

test("report totals separate Human hours from concurrent Agent and tool durations", () => {
  const s = fresh();
  for (let i = 0; i < 3; i++) s.resources.worklogs.push({ id: `agent-${i}`, name: `Agent ${i}`, hours: 1,
    durationKind: "Agent 墙钟", date: "2026-10-13", work: "W-104", status: "已接受" });
  s.resources.worklogs.push({ id: "tool", name: "工具", hours: 0.25, durationKind: "模型 / 工具耗时",
    date: "2026-10-13", work: "W-104", status: "已接受" });
  const result = reportEvidence(s, report([ref("worklog", "WL-01"), ...[0, 1, 2].map((n) => ref("worklog", `agent-${n}`)), ref("worklog", "tool")]));
  expect(result.errors).toEqual([]);
  expect(result.totals).toMatchObject({ human: 1.5, agent: 3, tool: 0.25 });
});

test("nested reports deduplicate each source identity across direct and child references", () => {
  const s = fresh(), work = ref("work", "W-104", 3), hours = ref("worklog", "WL-01");
  s.resources.reports.push(report([work, hours], { id: "child", name: "下级", status: "已接受" }));
  const result = reportEvidence(s, report([hours, hours, work, ref("report", "child"), ref("report", "child")]));
  expect(result.errors).toEqual([]);
  expect(result.totals.human).toBe(1.5);
  expect(result.totals.works).toBe(1);
  expect(result.sources.map((v) => `${v.kind}:${v.id}`).sort()).toEqual(["report:child", "work:W-104", "worklog:WL-01"]);
});

test("report submission preserves unaudited source state and records warnings in its snapshot", () => {
  const r = report([ref("worklog", "WL-02"), ref("report", "RP-02")]);
  install(r);
  const before = structuredClone(read().resources.worklogs);
  resourceCommand("reports", r.id, "已提交", "本人核对并保留待审核标记");
  const saved = read().resources.reports.find((v) => v.id === r.id);
  expect(saved.status).toBe("已提交");
  expect(saved.factSnapshot.warnings.some((v) => v.includes("草稿") && v.includes("尚未审核"))).toBe(true);
  expect(saved.factSnapshot.warnings.some((v) => v.includes("待审核"))).toBe(true);
  expect(read().resources.worklogs).toEqual(before);
  expect(read().resources.reports.find((v) => v.id === "RP-02").status).toBe("待审核");
});

for (const status of ["已提交", "已接受"]) {
  test(`missing report sources block ${status} with no mutation`, () => {
    const r = report([ref("work", "missing", 1)]);
    if (status === "已接受") {
      r.owner = "周宁"; r.status = "已提交"; r.revision = 1;
      r.submission = { authorId: "H-zhou", authorKind: "Human", confirmerId: "H-lin", revision: 1, content: reportSubmissionContent(r) };
    }
    install(r);
    const before = structuredClone(read());
    expect(() => resourceCommand("reports", r.id, status, "确认")).toThrow("来源 missing 不存在");
    expect(read()).toEqual(before);
  });
}

test("every source kind rejects a stale revision even after a valid duplicate", () => {
  for (const [kind, id, version] of [["work", "W-104", 3], ["worklog", "WL-01", 1], ["report", "RP-02", 1]]) {
    const result = reportEvidence(fresh(), report([ref(kind, id, version), ref(kind, id, version + 1)]));
    expect(result.errors.some((v) => v.includes("引用版本已变化"))).toBe(true);
  }
});

test("self and indirect report cycles are blockers without stack overflow", () => {
  const s = fresh(), a = report([ref("report", "child")]);
  s.resources.reports.push(a, report([ref("report", a.id)], { id: "child" }));
  expect(reportEvidence(s, a).errors.some((v) => v.includes("循环"))).toBe(true);
  a.references = [ref("report", a.id)];
  expect(reportEvidence(s, a).errors.some((v) => v.includes("循环"))).toBe(true);
});

test("period validation rejects invalid bounds and checks each child period", () => {
  for (const [start, end] of [["", "2026-10-18"], ["2026-02-30", "2026-10-18"], ["2026-10-19", "2026-10-18"]]) {
    expect(reportEvidence(fresh(), report([ref("worklog", "WL-01")], { periodStart: start, periodEnd: end })).errors.some((v) => v.includes("起止日期"))).toBe(true);
  }
  const s = fresh();
  s.resources.reports.push(report([ref("worklog", "WL-01")], { id: "child", periodStart: "2026-10-14" }));
  // The same log is also selected directly: global dedupe must not hide a child-period conflict.
  expect(reportEvidence(s, report([ref("worklog", "WL-01"), ref("report", "child")])).errors.some((v) => v.includes("记录日期不在"))).toBe(true);
  s.resources.reports.at(-1).periodStart = "2026-10-01";
  expect(reportEvidence(s, report([ref("report", "child")])).errors.some((v) => v.includes("下级报告周期超出"))).toBe(true);
});

test("declared completion counts compare to facts without judging freeform prose", () => {
  const refs = [ref("work", "W-104", 3)];
  expect(reportEvidence(fresh(), report(refs, { claimedCompleted: 1 })).errors.some((v) => v.includes("完成 0 项不一致"))).toBe(true);
  expect(reportEvidence(fresh(), report(refs, { claimedCompleted: 0, summary: "全部正常" })).errors).toEqual([]);
  const s = fresh();
  s.resources.reports.push(report(refs, { id: "child", claimedCompleted: 1 }));
  expect(reportEvidence(s, report([ref("report", "child")])).errors.some((v) => v.includes("不一致"))).toBe(true);
});

test("malformed and legacy text-only references are not certified as checked", () => {
  for (const refs of [undefined, [], "W-104", [null], [ref("unknown", "WL-01")]]) {
    expect(reportEvidence(fresh(), report(refs, { source: "W-104" })).errors.length).toBeGreaterThan(0);
  }
});

test("invalid duration type, amount, date and missing linked work are explicit blockers", () => {
  for (const patch of [{ hours: "NaN" }, { hours: -1 }, { hours: 25 }, { durationKind: "unknown" }, { date: "2026-02-30" }, { work: "missing" }]) {
    const s = fresh(); Object.assign(s.resources.worklogs[0], patch);
    expect(reportEvidence(s, report([ref("worklog", "WL-01")])).errors.length).toBeGreaterThan(0);
  }
});

test("report source changes are rechecked at submission and do not rewrite its prior snapshot", () => {
  const r = report([ref("worklog", "WL-02")]); install(r);
  resourceCommand("reports", r.id, "已提交", "确认");
  const snapshot = structuredClone(read().resources.reports.at(-1).factSnapshot);
  saveResource("reports", { ...read().resources.reports.at(-1), summary: "重新核对来源后上报" });
  saveHumanEffort({ id: "WL-02", revision: 1, action: "calibrate", timezone: "Asia/Singapore", category: "验收", calibratedHours: 2, note: "本人补充校准" });
  expect(() => resourceCommand("reports", r.id, "已提交", "再次确认")).toThrow("引用版本已变化");
  expect(read().resources.reports.at(-1).factSnapshot).toEqual(snapshot);
});

const { reportPeriod, dateInZone, periodicReportKey, reportSubjects } = await import("../src/reporting.js");
const { generatePeriodicReport, saveReportSchedule, createTenant } = await import("../src/store.js");
const periodOptions = (extra = {}) => ({ subjectKey: "human:H-lin", cadence: "周报", date: "2026-10-13", timezone: "Asia/Singapore", ...extra });
test("calendar report periods cover ISO week, leap month, quarter and year boundaries", () => {
  for (const [cadence, date, start, end] of [["日报", "2026-10-13", "2026-10-13", "2026-10-13"], ["周报", "2027-01-01", "2026-12-28", "2027-01-03"],
    ["月报", "2028-02-15", "2028-02-01", "2028-02-29"], ["季报", "2026-12-31", "2026-10-01", "2026-12-31"], ["年报", "2028-12-31", "2028-01-01", "2028-12-31"]])
    expect(reportPeriod(cadence, date)).toEqual({ periodStart: start, periodEnd: end });
  expect(() => reportPeriod("日报", "2026-02-30")).toThrow("有效");
  expect(dateInZone("Asia/Singapore", new Date("2026-10-12T17:00:00Z"))).toBe("2026-10-13");
  expect(dateInZone("America/Los_Angeles", new Date("2026-10-12T17:00:00Z"))).toBe("2026-10-12");
});
test("manual generation opens same seeded calendar period and keeps Human text and selected facts", () => {
  const before = structuredClone(read().resources.reports.find((r) => r.id === "RP-01"));
  expect(generatePeriodicReport(periodOptions())).toBe("RP-01");
  expect(generatePeriodicReport(periodOptions({ date: "2026-10-18" }))).toBe("RP-01");
  const result = read().resources.reports.find((r) => r.id === "RP-01");
  expect(result.summary).toBe(before.summary); expect(result.references).toEqual(before.references);
  expect(result.generatedSummary).toContain("当前关联");
  expect(read().resources.reports.filter((r) => periodicReportKey(read(), r) === periodicReportKey(read(), result))).toHaveLength(1);
});
test("changed facts refresh suggestions without overwriting manual corrections or source choices", () => {
  const reportId = generatePeriodicReport(periodOptions({ cadence: "月报" }));
  let record = read().resources.reports.find((r) => r.id === reportId);
  saveResource("reports", { ...record, summary: "人工保留的风险判断", references: [ref("work", "W-104", 3)] });
  const before = structuredClone(read().resources.reports.find((r) => r.id === reportId));
  update((s) => { const work=s.work.find(w=>w.id==="W-104");work.version++;work.acceptanceFacts=[{id:"W-104:v4:accept",workId:work.id,version:4,project:work.project,time:"2026-10-12T02:00:00Z",subjectKeys:["human:H-lin"],executorIds:["H-lin"]}]; });
  expect(generatePeriodicReport(periodOptions({ cadence: "月报" }))).toBe(reportId);
  record = read().resources.reports.find((r) => r.id === reportId);
  expect(record.summary).toBe(before.summary); expect(record.references).toEqual(before.references);
  expect(record.suggestedReferences.find((r) => r.id === "W-104").version).toBe(4);
  expect(record.revision).toBe(before.revision + 1); expect(record.generationHistory).toHaveLength(1);
  expect(() => saveResource("reports", before)).toThrow("已变化");
});
test("schedule and manual generation share one record and never overwrite a submitted report", () => {
  const options = periodOptions({ cadence: "日报", date: dateInZone("Asia/Singapore") });
  const reportId = generatePeriodicReport(options);
  const scheduleId = saveReportSchedule({ ...options, cron: "0 18 * * *" });
  resourceCommand("schedules", scheduleId, "启用", "本地模拟", "模拟运行一次");
  expect(read().resources.schedules.find((r) => r.id === scheduleId).lastReportId).toBe(reportId);
  let record = read().resources.reports.find((r) => r.id === reportId);
  saveResource("reports", { ...record, summary: "本人校准", references: [ref("work", "W-104", 3)] });
  resourceCommand("reports", reportId, "已提交", "确认事实");
  const submitted = structuredClone(read().resources.reports.find((r) => r.id === reportId));
  update((s) => { s.work.find((w) => w.id === "W-104").version++; });
  resourceCommand("schedules", scheduleId, "启用", "重复本地模拟", "模拟运行一次");
  expect(generatePeriodicReport(options)).toBe(reportId);
  expect(read().resources.reports.find((r) => r.id === reportId)).toEqual(submitted);
  expect(read().resources.reports.filter((r) => periodicReportKey(read(), r) === periodicReportKey(read(), submitted))).toHaveLength(1);
});
test("editing submitted report retains report ID and frozen historical submission", () => {
  const reportId = generatePeriodicReport(periodOptions());
  let record = read().resources.reports.find((r) => r.id === reportId);
  saveResource("reports", { ...record, references: [ref("work", "W-104", 3)], summary: "提交前正文" });
  resourceCommand("reports", reportId, "已提交", "确认");
  record = structuredClone(read().resources.reports.find((r) => r.id === reportId));
  saveResource("reports", { ...record, summary: "修订正文" });
  const saved = read().resources.reports.find((r) => r.id === reportId);
  expect(saved.id).toBe(reportId); expect(saved.status).toBe("草稿");
  expect(saved.history.at(-1)).toMatchObject({ status: "已提交", summary: "提交前正文", factSnapshot: record.factSnapshot });
  expect(saved.revision).toBe(record.revision + 1);
});
test("saving cannot create duplicate periods, alter identity or certify unknown legacy owner text", () => {
  const original = read().resources.reports.find((r) => r.id === "RP-01");
  const before = structuredClone(read());
  expect(() => saveResource("reports", { ...original, id: "duplicate" })).toThrow("已有报告");
  expect(() => saveResource("reports", { ...original, cadence: "月报", periodStart: "2026-10-01", periodEnd: "2026-10-31" })).toThrow("不可改写");
  expect(() => saveResource("reports", { ...original, periodEnd: "2026-10-19" })).toThrow("日历周期");
  expect(read()).toEqual(before);
  expect(periodicReportKey(read(), { owner: "林予", cadence: "周报", periodStart: original.periodStart, periodEnd: original.periodEnd, timezone: "Asia/Singapore" })).toBeNull();
});
test("all explicit ownership scopes generate independently and foreign tenant subjects are rejected", () => {
  for (const subject of reportSubjects(read())) {
    const reportId = generatePeriodicReport(periodOptions({ subjectKey: subject.key, cadence: "年报" }));
    expect(read().resources.reports.find((r) => r.id === reportId).subjectKey).toBe(subject.key);
  }
  const utc = generatePeriodicReport(periodOptions({ timezone: "UTC" })); expect(utc).toBe("RP-01"); expect(read().resources.reports.find(r=>r.id===utc).timezone).toBe("Asia/Singapore");
  createTenant("报告隔离");
  expect(() => generatePeriodicReport(periodOptions({ subjectKey: "agent:atlas" }))).toThrow("当前租户");
  expect(() => generatePeriodicReport(periodOptions({ timezone: "Invalid/Zone" }))).toThrow("时区");
});


test("Human actual totals exclude drafts and calibration but preserve declared historical actuals", () => {
  const s = fresh();
  const legacy = s.resources.worklogs.find((item) => item.id === "WL-01");
  const own = s.resources.worklogs.find((item) => item.id === "WL-02");
  const refs = [ref("worklog", legacy.id), ref("worklog", own.id)];
  for (const effortState of [undefined, "estimate", "calibrated"]) {
    own.effortState = effortState; own.suggestedHours = 10; own.calibratedHours = 3; own.hours = 10;
    const result = reportEvidence(s, report(refs));
    expect(result.totals.human).toBe(1.5);
    expect(result.warnings.some((warning) => warning.includes("不计入 Human 实际投入"))).toBe(true);
  }
  own.effortState = "confirmed"; own.confirmedHours = 2; own.hours = 23;
  expect(reportEvidence(s, report(refs)).totals.human).toBe(3.5);
  own.confirmedHours = 0;
  expect(reportEvidence(s, report(refs)).errors).toEqual([]);
  expect(reportEvidence(s, report(refs)).totals.human).toBe(1.5);
});


test("group reports bind the authoritative Human owner rather than their subject display name", () => {
  for (const [subjectKey, name] of [["project:launch", "林予"], ["team:ORG-engineering", "林予"], ["tenant:studio", "林予"], ["project:film", "周宁"], ["team:ORG-creative", "周宁"]]) {
    const reportId = generatePeriodicReport(periodOptions({ subjectKey }));
    let record = read().resources.reports.find((item) => item.id === reportId);
    expect(record.owner).toBe(name);
    expect(record.subjectName).toBe(reportSubjects(read()).find((subject) => subject.key === subjectKey).name);
    if (name === "林予") {
      saveResource("reports", { ...record, owner: "伪造负责人", references: [ref("work", "W-104", 3)] });
      record = read().resources.reports.find((item) => item.id === reportId);
      expect(record.owner).toBe(name);
      resourceCommand("reports", reportId, "已提交", "本人确认归属报告");
      const saved = read().resources.reports.find((item) => item.id === reportId);
      expect(saved.status).toBe(subjectKey.startsWith("project:") ? "已提交" : "已接受");
      if (!subjectKey.startsWith("project:")) expect(saved.approval.actorId).toBe("SYSTEM:team_top");
    } else {
      const before = structuredClone(read());
      expect(() => saveResource("reports", { ...record, owner: "林予" })).toThrow("本人");
      expect(() => resourceCommand("reports", reportId, "已提交", "不能替他人确认")).toThrow("本人确认");
      expect(read()).toEqual(before);
    }
  }
});

test("accepted and pending-review reports stay fixed during scheduled or manual regeneration", () => {
  for (const status of ["待审核", "已接受"]) {
    reset();
    const options = periodOptions({ subjectKey: "agent:atlas", cadence: "日报", date: "2026-10-13" });
    update((s) => { s.resources.reports.find((item) => item.id === "RP-02").status = status; });
    const before = structuredClone(read().resources.reports.find((item) => item.id === "RP-02"));
    update((s) => { s.work.find((item) => item.id === "W-104").version++; });
    expect(generatePeriodicReport(options)).toBe("RP-02");
    expect(read().resources.reports.find((item) => item.id === "RP-02")).toEqual(before);
  }
});


test("Agent reporting uses immutable identity across renames and never adopts same-name legacy records", () => {
  update((s) => {
    s.work.find((item) => item.id === "W-104").agentId = "atlas";
    s.work.find((item) => item.id === "W-104").acceptanceFacts = [{ id: "W-104:v3:accept", workId: "W-104", version: 3,
      time: "2026-10-13T08:00:00Z", executorIds: ["agent:atlas"], subjectKeys: ["agent:atlas", "project:launch", "tenant:studio"] }];
    s.resources.worklogs.find((item) => item.id === "WL-03").ownerAgentId = "atlas";
    s.agents.find((item) => item.id === "atlas").name = "Atlas renamed";
    s.agents.push({ id: "distinct-agent", name: "Atlas", kind: "Agent", status: "待批准" });
    s.work.push({ id: "ambiguous-legacy", title: "旧版无身份工作", agent: "Atlas", owner: "周宁", project: "launch", version: 1 });
    s.resources.worklogs.push({ id: "ambiguous-log", name: "旧版无身份日志", owner: "Atlas", durationKind: "Agent 墙钟", date: "2026-10-13", work: "W-104", hours: 1 });
  });
  const ownId = generatePeriodicReport(periodOptions({ subjectKey: "agent:atlas", cadence: "月报" }));
  const own = read().resources.reports.find((item) => item.id === ownId);
  expect(own.references.some((item) => item.id === "W-104")).toBe(true);
  expect(own.references.some((item) => item.id === "WL-03")).toBe(true);
  expect(own.references.some((item) => item.id === "ambiguous-legacy" || item.id === "ambiguous-log")).toBe(false);
  const otherId = generatePeriodicReport(periodOptions({ subjectKey: "agent:distinct-agent", cadence: "月报" }));
  expect(read().resources.reports.find((item) => item.id === otherId).references).toEqual([]);
});
test("Human reports cannot inherit same-name Agent telemetry or legacy Agent assignments", () => {
  update((s) => {
    s.organization.members.push({ id: "H-atlas", name: "Atlas", kind: "Human" });
    s.resources.worklogs.push({ id: "human-atlas", ownerId: "H-atlas", owner: "旧显示名", durationKind: "Human 工时", date: "2026-10-13", work: "W-104", hours: 2, status: "已提交" });
  });
  const id = generatePeriodicReport(periodOptions({ subjectKey: "human:H-atlas", cadence: "月报" }));
  expect(read().resources.reports.find((item) => item.id === id).references).toEqual([{ kind: "worklog", id: "human-atlas", version: 1 }]);
});

test("report review cannot bypass author submission and every decision retains its prior snapshot", () => {
  const reportId = generatePeriodicReport(periodOptions({ subjectKey: "human:H-xu" }));
  const find = () => read().resources.reports.find(item => item.id === reportId);
  const before = structuredClone(read());
  expect(() => resourceCommand("reports", reportId, "已接受", "审核草稿")).toThrow("不能跳过本人确认");
  expect(() => resourceCommand("reports", reportId, "草稿", "退回草稿")).toThrow("不能跳过本人确认");
  expect(() => resourceCommand("reports", reportId, "已提交", "代确认")).toThrow("本人确认");
  expect(read()).toEqual(before);
  const own = generatePeriodicReport(periodOptions({ cadence: "日报" }));
  resourceCommand("reports", own, "已提交", "本人已确认");
  const submitted = structuredClone(read().resources.reports.find(item => item.id === own));
  expect(() => resourceCommand("reports", own, "已接受", "本人不可自审")).toThrow("本人");
  expect(read().resources.reports.find(item => item.id === own)).toEqual(submitted);
  const agentId = generatePeriodicReport(periodOptions({ subjectKey: "agent:atlas", cadence: "日报", date: "2026-10-14" }));
  resourceCommand("reports", agentId, "已提交", "明确 Agent 本地模拟", "", { revision: 1, status: "草稿", simulateAgent: true });
  const agentSubmitted = structuredClone(read().resources.reports.find(item => item.id === agentId));
  resourceCommand("reports", agentId, "已接受", "Human 审核有据");
  const accepted = read().resources.reports.find(item => item.id === agentId);
  expect(accepted.history.at(-1).snapshot).toMatchObject({ status: "已提交", summary: agentSubmitted.summary, references: agentSubmitted.references, factSnapshot: agentSubmitted.factSnapshot });
  expect(accepted.approval.snapshot).toEqual(agentSubmitted.submission);
  expect(() => resourceCommand("reports", agentId, "已接受", "重复审核")).toThrow("已提交或待审核");
});

test("report edit cannot inject accepted status and stale review dialogs cannot approve new revisions", () => {
  const reportId = generatePeriodicReport(periodOptions({ subjectKey: "agent:atlas", cadence: "日报", date: "2026-10-14" }));
  const find = () => read().resources.reports.find(item => item.id === reportId);
  saveResource("reports", { ...find(), status: "已接受" });
  expect(find().status).toBe("草稿");
  resourceCommand("reports", reportId, "已提交", "Agent 本地模拟", "", { revision: find().revision, status: find().status, simulateAgent: true });
  const old = { revision: find().revision, status: find().status };
  resourceCommand("reports", reportId, "草稿", "请补充校准依据");
  expect(find().history.at(-1).snapshot.status).toBe("已提交");
  saveResource("reports", { ...find(), summary: "重新校准", status: "已接受" });
  expect(find().status).toBe("草稿");
  resourceCommand("reports", reportId, "已提交", "再次 Agent 模拟", "", { revision: find().revision, status: find().status, simulateAgent: true });
  expect(() => resourceCommand("reports", reportId, "已接受", "旧对话框", "审核通过", old)).toThrow("已变化");
  update(s => { s.role = "executor"; });
  expect(() => resourceCommand("reports", reportId, "草稿", "非主管退回")).toThrow("负责人");
});

test("new report with valid descriptor cannot inject a supervisor acceptance", () => {
  const id = generatePeriodicReport(periodOptions({ cadence: "日报" }));
  const generated = structuredClone(read().resources.reports.find(item => item.id === id));
  update(s => { s.resources.reports = s.resources.reports.filter(item => item.id !== id); });
  saveResource("reports", { ...generated, status: "已接受" });
  expect(read().resources.reports.find(item => item.id === id).status).toBe("草稿");
});
