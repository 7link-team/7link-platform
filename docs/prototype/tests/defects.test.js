import { beforeEach, expect, test } from "bun:test";
import { migrateDefects, defectRecords } from "../src/defects.js";
const entries = new Map();
Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: key => entries.get(key) || null, setItem: (key, value) => entries.set(key, value), removeItem: key => entries.delete(key) } });
const { reset, read, update, beginWorkDraft, saveWorkDraft, confirmWorkDraft, saveResource, resourceCommand, workCommand, switchTenant, qualityResult } = await import("../src/store.js");
beforeEach(reset);
const bug = () => read().work.find(work => work.id === "BUG-01");
const record = () => defectRecords(read()).find(item => item.id === "BUG-01");
const close = () => {
  resourceCommand("defects", "BUG-01", "待验证", "修复准确版本");
  resourceCommand("tests", "TC-01", "通过", "独立浏览器验证");
  const run = read().resources.tests.find(item => item.id === "TC-01").runs.at(-1);
  resourceCommand("defects", "BUG-01", "已关闭", "Human 核对关闭", "验证通过", { runId: run.id });
};
test("canonical defect work is the only mutable fact and QA projection cannot mutate it", () => {
  expect(read().resources.defects).toBeUndefined();
  expect(bug()).toMatchObject({ workKind: "BUG", type: "软件", version: 1 });
  const projected = record(); projected.expected = "outside mutation";
  expect(bug().defect.expected).not.toBe(projected.expected);
  saveResource("defects", { ...record(), expected: "更新专业条件", status: "已关闭" });
  expect(bug().defect.expected).toBe("更新专业条件");
  expect(bug().requirements).toEqual(["更新专业条件"]);
  expect(bug().accepted).toBe(false);
  expect(read().work.filter(work => work.id === "BUG-01")).toHaveLength(1);
  for (const command of ["accept", "deliver"]) expect(() => workCommand("BUG-01", command)).toThrow("缺陷验证门");
});
test("legacy migration preserves identity, full evidence and accountable source; conflicts are isolated", () => {
  const s = structuredClone(read()); s.work = s.work.filter(work => work.workKind !== "BUG");
  const old = { id: "OLD-BUG", name: "历史缺陷", owner: "Atlas", work: "W-104", version: "v3", status: "已关闭", verification: { id: "historic-run", evidence: "retained" }, history: [{ reason: "old" }], custom: { retain: true } };
  const conflict = { ...old, id: "W-104" }, missing = { ...old, id: "orphan", work: "not-here" };
  s.resources.defects = [old, conflict, missing];
  migrateDefects(s); const migrated = s.work.find(work => work.id === old.id);
  expect(migrated).toMatchObject({ workKind: "BUG", status: "历史已关闭", accepted: false, qa: false, owner: "林予", agentId: "atlas", version: 1 });
  expect(migrated.defect.legacy).toEqual(old);
  expect(migrated.defect.verification).toEqual(old.verification);
  expect(migrated.ownerSource).toContain("W-104");
  expect(s.defectMigrationIssues.map(issue => issue.legacy)).toEqual([conflict, missing]);
  expect(s.resources.defects).toBeUndefined();
  const fixed = structuredClone(s); migrateDefects(s); expect(s).toEqual(fixed);
});
test("missing Human stays unresolved; unknown legacy lifecycle is not guessed", () => {
  const s = structuredClone(read()); s.organization.members = []; s.resources.defects = [
    { id: "NO-HUMAN", name: "缺陷", work: "W-104", version: "v3", owner: "Atlas", status: "待修复" },
    { id: "UNKNOWN", name: "缺陷", work: "W-104", version: "v3", status: "unknown" },
  ];
  migrateDefects(s);
  expect(s.work.find(work => work.id === "NO-HUMAN").owner).toBe("待补负责人");
  expect(s.work.some(work => work.id === "UNKNOWN")).toBe(false);
  expect(s.defectMigrationIssues[0].legacy.status).toBe("unknown");
});
test("closed defect cannot be reopened in place and regression reuses conversation confirmation", () => {
  close(); const previous = structuredClone(bug());
  for (const command of ["return", "takeover", "cancel", "start", "submit"]) expect(() => workCommand("BUG-01", command, "回归报告")).toThrow("终态缺陷");
  expect(() => saveResource("defects", { ...record(), actual: "覆盖旧事实" })).toThrow("终态缺陷");
  expect(() => resourceCommand("defects", "BUG-01", "待修复", "回归")).toThrow("终态缺陷");
  const count = read().work.length;
  const draftId = beginWorkDraft({ reopenedFrom: "BUG-01" });
  expect(read().work).toHaveLength(count);
  expect(read().drafts[draftId].workKind).toBe("BUG");
  const created = confirmWorkDraft(draftId, 0);
  expect(created.id).not.toBe(previous.id);
  expect(created.defect.reopenedFromWorkItemId).toBe(previous.id);
  expect(created.defect.verification).toBeUndefined();
  expect(bug()).toEqual(previous);
  expect(confirmWorkDraft(draftId, 0).id).toBe(created.id);
  expect(read().work).toHaveLength(count + 1);
});
test("new defect confirmation requires professional facts and never permits generic resource creation", () => {
  const draftId = beginWorkDraft({ workKind: "BUG", projectId: "launch" });
  expect(() => saveResource("defects", { name: "新缺陷" })).toThrow("会话草案");
  saveWorkDraft(draftId, { title: "修复按钮", criteria: "按钮可用", project: "launch", type: "图片", defect: { affectedWorkId: "W-104", affectedVersion: "v3" } }, 0);
  expect(() => confirmWorkDraft(draftId, 1)).toThrow("复现步骤");
  saveWorkDraft(draftId, { title: "修复按钮", criteria: "按钮可用", project: "launch", type: "图片", defect: { ...bug().defect } }, 1);
  const created = confirmWorkDraft(draftId, 2);
  expect(created).toMatchObject({ workKind: "BUG", type: "软件", plan: true, status: "待开始" });
  expect(created.defect.affectedWorkId).toBe("W-104");
});
test("closed defect verification stays fixed after reassignment and repair executor self-review is rejected", () => {
  update(s => { const target = s.work.find(work => work.id === "BUG-01"); target.team = { executorIds: ["agent:verifier"] }; });
  resourceCommand("defects", "BUG-01", "待验证", "修复准确版本");
  update(s => { s.work.find(work => work.id === "BUG-01").team.executorIds = ["agent:atlas"]; });
  resourceCommand("tests", "TC-01", "通过", "自审应被拒绝");
  const run = read().resources.tests.find(item => item.id === "TC-01").runs.at(-1);
  expect(() => resourceCommand("defects", "BUG-01", "已关闭", "关闭", "验证通过", { runId: run.id })).toThrow("独立验证证据");
});


test("migration isolates every nonexistent or malformed affected version without erasing legacy facts", () => {
  const s = structuredClone(read());
  const invalid = ["v999", "latest", "", "v0", "v-1"].map((version, index) => ({ id: `OLD-${index}`, name: "旧缺陷", work: "W-104", version, status: "已关闭" }));
  s.resources.defects = invalid; migrateDefects(s);
  expect(s.defectMigrationIssues.map(issue => issue.legacy)).toEqual(invalid);
  for (const record of invalid) expect(s.work.some(work => work.id === record.id)).toBe(false);
  expect(s.work.filter(work => work.status === "历史已关闭")).toHaveLength(0);
});

test("editing defect metadata cannot release a rework hold and missing typed details stay readable", () => {
  update(s => { const work = s.work.find(work => work.id === "BUG-01"); work.status = "等待"; work.rework = { revision: 1, internal: 5, formal: 0, chain: 1, events: [], hold: { reason: "返工停线" } }; });
  saveResource("defects", { ...record(), environment: "updated" });
  expect(bug().status).toBe("等待"); expect(bug().rework.hold.reason).toBe("返工停线");
  const s = structuredClone(read()); delete s.work.find(work => work.id === "BUG-01").defect;
  expect(() => defectRecords(s)).not.toThrow();
});


test("new defect work output invalidates the prior repair submission without changing affected version", () => {
  resourceCommand("defects", "BUG-01", "待验证", "第一次修复");
  resourceCommand("tests", "TC-01", "通过", "第一轮独立验证");
  const run = read().resources.tests.find(item => item.id === "TC-01").runs.at(-1);
  workCommand("BUG-01", "takeover", "继续修改缺陷工作");
  workCommand("BUG-01", "submit");
  expect(record().status).toBe("待修复");
  expect(() => resourceCommand("defects", "BUG-01", "已关闭", "关闭", "验证通过", { runId: run.id })).toThrow("先提交修复");
  resourceCommand("defects", "BUG-01", "待验证", "第二次修复");
  expect(record().repairWorkVersion).toBe(bug().version);
  expect(() => resourceCommand("defects", "BUG-01", "已关闭", "关闭", "验证通过", { runId: run.id })).toThrow("独立验证证据");
});


test("common quality reads only the closed defect's fixed independent verification, never historic closure or a green flag", () => {
  expect(qualityResult(read(), bug())).toBe("未执行");
  close(); const accepted = structuredClone(bug());
  expect(qualityResult(read(), accepted)).toBe("通过");
  for (const patch of [
    work => { delete work.defect.closure; },
    work => { work.defect.closure.workVersion++; },
    work => { work.defect.verification.status = "失败"; },
    work => { work.defect.verification.sourceKind = "Agent 自测"; },
    work => { work.defect.verification.evidence = ""; },
    work => { work.defect.verification.defect = "OTHER"; },
    work => { work.defect.verification.version = "v999"; },
    work => { work.defect.verification.defectRepairRevision++; },
  ]) { const changed = structuredClone(accepted); patch(changed); expect(qualityResult(read(), changed)).not.toBe("通过"); }
  const s = structuredClone(read()); s.resources.defects = [{ id: "OLD-CLOSED", name: "旧关闭", owner: "Atlas", work: "W-104", version: "v3", status: "已关闭", verification: structuredClone(accepted.defect.verification) }];
  migrateDefects(s);
  expect(qualityResult(s, s.work.find(work => work.id === "OLD-CLOSED"))).toBe("未执行");
  // Later case changes do not rewrite the closure-time verdict.
  saveResource("tests", { ...read().resources.tests.find(item => item.id === "TC-01"), expected: "下一版条件" });
  expect(qualityResult(read(), bug())).toBe("通过");
  expect(bug().defect.verification).toEqual(accepted.defect.verification);
});
