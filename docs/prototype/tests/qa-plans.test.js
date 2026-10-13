import { beforeEach, expect, test } from "bun:test";
import { caseSnapshot, qaCounts } from "../src/qa.js";
const storage = new Map();
Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: key => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) } });
const api = await import("../src/store.js?qa-plans-tests");
const { read, reset, update, workCommand, saveResource, saveTestPlan, startTestBatch, recordBatchResult, publishTestReport, qualityResult } = api;
beforeEach(reset);
const work = () => read().work.find(work => work.id === "W-104");
const plan = () => read().resources.testplans.find(plan => plan.id === work().qaPlanId);
const batch = () => plan().batches.find(batch => batch.id === plan().currentBatchId);
const begin = () => workCommand("W-104", "check");
const record = (ref, patch = {}) => recordBatchResult(plan().id, batch().id, ref.caseId, { status: "通过", actual: "按固定步骤逐条检查，符合期望", evidence: `mock/${batch().id}/${ref.caseId}`, sourceKind: "平台独立验证", ownerKind: "Agent", ownerId: "verifier", toolVersion: "browser/1", logs: "mock/log", ...patch });
const finish = () => { for (const ref of batch().caseRefs) record(ref); publishTestReport(plan().id); };
const quality = () => qualityResult(read(), work());

test("starting verification never changes a failing case or fabricates success", () => {
  const before = structuredClone(read().resources.tests.find(test => test.id === "TC-01"));
  expect(begin()).toBe(`/testplans/${plan().id}`);
  expect(read().resources.tests.find(test => test.id === "TC-01")).toEqual(before);
  expect(batch().items.every(item => item.status === "未执行" && item.runId === null)).toBe(true);
  expect(quality()).toBe("失败");
  expect(() => workCommand("W-104", "accept")).toThrow("内部检查");
  expect(plan().caseRefs.find(ref => ref.caseId === "TC-01").snapshot.requirementRefs).toEqual(before.requirementRefs);
  expect(plan().caseRefs.some(ref => ref.caseId !== "TC-01" && ref.snapshot.requirementRefs.includes("产品信息与简报 v2 一致"))).toBe(true);
});
test("every pending, skipped, blocked, failed or self-tested required item blocks quality regardless of cached flags", () => {
  for (const patch of [{ status: "失败" }, { status: "跳过" }, { status: "阻塞" }, { sourceKind: "Agent 自测", ownerId: "atlas" }, { ownerId: "atlas" }]) {
    reset(); begin(); const refs = batch().caseRefs;
    record(refs[0], patch); for (const ref of refs.slice(1)) record(ref); publishTestReport(plan().id);
    update(s => { s.work.find(work => work.id === "W-104").qa = true; }); expect(quality()).not.toBe("通过");
  }
  reset(); begin(); finish(); expect(quality()).toBe("通过");
  update(s => { s.work.find(work => work.id === "W-104").qa = false; }); expect(quality()).toBe("通过");
});
test("stable reviewer identity prevents renamed executor and Human/Agent same-name confusion", () => {
  begin(); const ref = batch().caseRefs[0];
  update(s => { s.agents.find(agent => agent.id === "atlas").name = "改名执行者"; s.agents.find(agent => agent.id === "verifier").name = "周宁"; });
  record(ref, { ownerId: "atlas" }); for (const other of batch().caseRefs.slice(1)) record(other); expect(quality()).not.toBe("通过");
  publishTestReport(plan().id); startTestBatch(plan().id);
  expect(() => record(ref, { ownerKind: "Agent", ownerId: "H-zhou", sourceKind: "Human 手测" })).toThrow("主体身份");
  record(ref, { ownerKind: "Human", ownerId: "H-zhou", sourceKind: "Human 手测" });
  for (const other of batch().caseRefs.slice(1)) record(other); publishTestReport(plan().id); expect(quality()).toBe("通过");
  const run = read().resources.tests.find(test => test.id === ref.caseId).runs.at(-1);
  expect(run).toMatchObject({ ownerKind: "Human", ownerId: "H-zhou", owner: "周宁" });
  update(s => { s.work.find(work => work.id === "W-104").team = { executorIds: ["H-zhou"] }; }); expect(quality()).toBe("通过");
});
test("legacy missing requirement mappings never silently claim full coverage", () => {
  const legacy = { ...read().resources.tests[0] }; delete legacy.requirementRefs;
  expect(caseSnapshot(read(), legacy).requirementRefs).toEqual([]);
  expect(() => saveResource("tests", legacy)).toThrow("验收条款");
  update(s => { delete s.resources.tests.find(test => test.id === "TC-01").requirementRefs; });
  expect(begin).toThrow("关联当前版本");
  expect(work().qaPlanId).toBeUndefined();
});
test("partial report freezes failures and not-executed separately and cannot be edited", () => {
  begin(); record(batch().caseRefs[0], { status: "失败", actual: "导航仍遮挡按钮" });
  const reportId = publishTestReport(plan().id); const report = structuredClone(read().resources.testreports[0]);
  expect(report.counts["失败"]).toBe(1); expect(report.counts["未执行"]).toBe(batch().items.length - 1);
  expect(publishTestReport(plan().id)).toBe(reportId);
  expect(() => record(batch().caseRefs[0])).toThrow("已固定");
  expect(() => saveResource("testreports", { ...report, counts: {} })).toThrow("固定快照");
  startTestBatch(plan().id); finish();
  expect(read().resources.testreports.find(item => item.id === reportId)).toEqual(report);
  expect(batch().id).not.toBe(report.batchId);
});
test("case edits preserve executed snapshots and force owner-confirmed plan revision with explicit retest", () => {
  begin(); finish(); const oldBatch = structuredClone(batch()); const reportId = publishTestReport(plan().id);
  const ref = plan().caseRefs[0]; const testcase = read().resources.tests.find(test => test.id === ref.caseId);
  saveResource("tests", { ...testcase, steps: "新增键盘与触屏核对步骤" });
  expect(quality()).not.toBe("通过"); expect(() => startTestBatch(plan().id)).toThrow("改版");
  const revisionInput = { ...plan(), caseIds: plan().caseRefs.map(ref => ref.caseId), changeReason: "步骤改版，所选用例全部重验" };
  update(s => { s.role = "executor"; }); expect(() => saveTestPlan(revisionInput)).toThrow("负责人");
  update(s => { s.role = "owner"; }); saveTestPlan(revisionInput);
  expect(plan().retestCaseIds).toEqual(revisionInput.caseIds); expect(plan().revision).toBe(2);
  startTestBatch(plan().id); expect(batch().items.every(item => item.status === "未执行")).toBe(true);
  expect(read().resources.testreports.find(item => item.id === reportId).batchSnapshot.items).toEqual(oldBatch.items);
  expect(plan().batches.find(item => item.id === oldBatch.id).items).toEqual(oldBatch.items);
});
test("removed coverage, missing evidence, case revision and work version each invalidate the gate", () => {
  for (const mutate of [
    s => { s.resources.tests.find(test => test.id === batch().caseRefs[0].caseId).evidence = ""; },
    s => { const test = s.resources.tests.find(test => test.id === batch().caseRefs[0].caseId); test.revision = (test.revision || 1) + 1; },
    s => { s.work.find(work => work.id === "W-104").requirements.push("新增未覆盖条款"); },
    s => { s.work.find(work => work.id === "W-104").version++; },
    s => { s.resources.tests = s.resources.tests.filter(test => test.id !== batch().caseRefs[0].caseId); },
  ]) { reset(); begin(); finish(); expect(quality()).toBe("通过"); update(mutate); expect(quality()).not.toBe("通过"); }
});
test("incomplete, unknown identity and stale batch submissions reject atomically", () => {
  begin(); const ref = batch().caseRefs[0];
  for (const patch of [{ actual: "" }, { evidence: "" }, { ownerId: "missing" }, { toolVersion: "" }, { status: "未执行" }]) {
    const before = structuredClone(read()); expect(() => record(ref, patch)).toThrow(); expect(read()).toEqual(before);
  }
  const oldBatchId = batch().id; publishTestReport(plan().id); startTestBatch(plan().id);
  expect(() => recordBatchResult(plan().id, oldBatchId, ref.caseId, { status: "通过", actual: "old", evidence: "old", ownerKind: "Agent", ownerId: "verifier", sourceKind: "平台独立验证", toolVersion: "1" })).toThrow("已固定");
});
test("failure records count once when batch closes; stopped work cannot start a new batch", () => {
  begin(); record(batch().caseRefs[0], { status: "失败" });
  expect(work().rework?.internal || 0).toBe(0);
  publishTestReport(plan().id);
  expect(work().rework.internal).toBe(1);
  publishTestReport(plan().id);
  expect(work().rework.internal).toBe(1);
  update(s => { s.work.find(work => work.id === "W-104").rework = { hold: { reason: "停线" } }; });
  expect(() => startTestBatch(plan().id)).toThrow("停线");
});
test("report counts preserve all five distinct outcomes", () => {
  expect(qaCounts(["未执行", "通过", "失败", "跳过", "阻塞"].map(status => ({ status })))).toEqual({ 未执行: 1, 通过: 1, 失败: 1, 跳过: 1, 阻塞: 1 });
});

test("one outcome per case and batch rejects stale dialogs and retains failures", () => {
  begin(); const ref = batch().caseRefs[0]; record(ref, { status: "失败" }); const before = structuredClone(read());
  expect(() => record(ref)).toThrow("结果已固定"); expect(read()).toEqual(before);
  api.resourceCommand("tests", ref.caseId, "通过", "批次外单独重验");
  expect(batch().items.find(item => item.caseId === ref.caseId).status).toBe("失败"); expect(quality()).toBe("失败");
  publishTestReport(plan().id); expect(work().rework.internal).toBe(1);
});
test("late reports archive old version or declaration chain without counting or stopping the new chain", () => {
  for (const change of ["version", "chain"]) {
    reset(); begin(); record(batch().caseRefs[0], { status: "失败" });
    const oldBatch = structuredClone(batch());
    expect(() => workCommand("W-104", "repair", "未结束失败批次", 3)).toThrow("结束失败验证批次");
    // Simulate an imported old unfinished batch alongside a newer work version/chain.
    update(s => { const w = s.work.find(work => work.id === "W-104"); w.rework = structuredClone(api.workRework(w)); if (change === "version") w.version++; else w.rework.chain++; });
    const before = structuredClone(work()); const reportId = publishTestReport(plan().id);
    expect(work()).toEqual(before);
    expect(read().resources.testreports.find(report => report.id === reportId).batchSnapshot.workContexts).toEqual(oldBatch.workContexts);
    expect(read().resources.testreports.find(report => report.id === reportId).counts["失败"]).toBe(1);
    expect(work().rework.internal).toBe(0);
  }
});

test("changing or replacing a plan cannot orphan an unfinished failed batch", () => {
  begin(); record(batch().caseRefs[0], { status: "失败" });
  const input = { ...plan(), caseIds: plan().caseRefs.map(ref => ref.caseId), changeReason: "修订重验" };
  const before = structuredClone(read());
  expect(() => saveTestPlan(input)).toThrow("结束当前批次");
  expect(() => saveTestPlan({ ...input, id: undefined })).toThrow("未结束的验证批次");
  expect(read()).toEqual(before);
  const reportId = publishTestReport(plan().id); saveTestPlan(input);
  expect(plan().revision).toBe(2);
  expect(read().resources.testreports.find(report => report.id === reportId).counts["失败"]).toBe(1);
  expect(work().rework.internal).toBe(1);
});

test("all passing items still require a fixed report before acceptance", () => {
  begin(); for (const ref of batch().caseRefs) record(ref);
  expect(quality()).toBe("待验证"); expect(work().qa).toBe(false);
  expect(() => workCommand("W-104", "accept")).toThrow("内部检查");
  publishTestReport(plan().id); expect(quality()).toBe("通过"); expect(work().qa).toBe(true);
});

const changeExecutors = executorIds => api.saveWorkTeam("W-104", {
  revision: work().team?.revision || 0, previousOwner: work().owner, ownerId: "H-lin",
  coordinatorId: "agent:atlas", executorIds, checkerIds: ["agent:verifier"], receiverId: "H-lin",
}, "调整当前任务执行团队");

test("producer self-review cannot become independent by reassignment before or after a batch", () => {
  for (const phase of ["before", "during", "after"]) {
    reset(); const production = structuredClone(work().production);
    if (phase === "before") changeExecutors(["agent:muse"]);
    begin(); if (phase === "during") changeExecutors(["agent:muse"]);
    for (const ref of batch().caseRefs) record(ref, { ownerId: "atlas" });
    publishTestReport(plan().id); const runs = structuredClone(batch().items);
    if (phase === "after") changeExecutors(["agent:muse"]);
    expect(work().production).toEqual(production);
    expect(batch().workContexts.find(context => context.workId === "W-104").production).toEqual(production);
    expect(batch().items).toEqual(runs); expect(quality()).toBe("待验证");
  }
});

test("new output pins its own producers and preserves the prior version provenance", () => {
  const previous = structuredClone(work().production);
  changeExecutors(["agent:muse", "H-zhou"]);
  workCommand("W-104", "return", "重新产出版本", work().version);
  const before = structuredClone(read()); expect(begin).toThrow("提交固定版本"); expect(read()).toEqual(before);
  workCommand("W-104", "submit");
  expect(work().history.at(-1).production).toEqual(previous);
  expect(work().production).toEqual({ version: 4, executorIds: ["agent:muse", "H-zhou"] });
  changeExecutors(["agent:atlas"]);
  begin(); for (const ref of batch().caseRefs) record(ref, { ownerId: "muse" }); publishTestReport(plan().id);
  expect(quality()).toBe("待验证"); startTestBatch(plan().id);
  for (const ref of batch().caseRefs) record(ref, { ownerId: "H-zhou", ownerKind: "Human", sourceKind: "Human 手测" });
  publishTestReport(plan().id); expect(quality()).toBe("待验证"); startTestBatch(plan().id);
  for (const ref of batch().caseRefs) record(ref, { ownerId: "atlas" });
  publishTestReport(plan().id); expect(quality()).toBe("通过");
});

test("legacy absent, empty or stale producer provenance never infers independence from today's team", async () => {
  for (const production of [undefined, { version: 3, executorIds: [] }, { version: 2, executorIds: ["agent:muse"] }]) {
    reset(); begin(); finish(); const legacy = structuredClone(read());
    if (production) legacy.work.find(item => item.id === "W-104").production = production;
    else delete legacy.work.find(item => item.id === "W-104").production;
    storage.set("collaboration-prototype-v1", JSON.stringify(legacy));
    const restored = await import(`../src/store.js?qa-legacy-${crypto.randomUUID()}`);
    const restoredWork = restored.read().work.find(item => item.id === "W-104");
    expect(restoredWork.production).toEqual(production);
    expect(restored.qualityResult(restored.read(), restoredWork)).toBe("待验证");
  }
});

test("accepted internal and external versions reject every QA start entry atomically for both roles", () => {
  for (const mode of ["internal", "external"]) for (const role of ["owner", "executor"]) {
    reset(); begin(); finish();
    workCommand("W-104", "contract", "", work().version, { mode, reason: "仅内部使用", recipient: "接收人" });
    workCommand("W-104", "accept"); update(s => { s.role = role; });
    for (const start of [() => startTestBatch(plan().id), () => api.resourceCommand("testplans", plan().id, "进行中", "重新验证"), begin]) {
      const before = structuredClone(read()); expect(start).toThrow(); expect(read()).toEqual(before);
      expect(work().accepted).toBe(true); expect(quality()).toBe("通过");
    }
  }
});

test("accepted version can reenter verification only after formal return and new submission", () => {
  begin(); finish(); const oldPlan = plan().id, oldBatch = structuredClone(batch());
  workCommand("W-104", "contract", "", work().version, { mode: "internal", reason: "内部使用" });
  workCommand("W-104", "accept"); workCommand("W-104", "return", "已验收成果需正式修订", work().version);
  expect(() => startTestBatch(oldPlan)).toThrow("提交固定版本"); workCommand("W-104", "submit"); begin();
  expect(work().version).toBe(4); expect(work().accepted).toBe(false); expect(work().qa).toBe(false);
  expect(batch().workContexts[0].version).toBe(4);
  expect(read().resources.testplans.find(item => item.id === oldPlan).batches.find(item => item.id === oldBatch.id)).toEqual(oldBatch);
  finish(); expect(quality()).toBe("通过");
});

function sharedPlan() {
  begin(); finish();
  const route = workCommand("W-103", "check"), second = read().resources.testplans.find(item => route === `/testplans/${item.id}`);
  const secondBatch = second.batches.find(item => item.id === second.currentBatchId);
  for (const ref of secondBatch.caseRefs) recordBatchResult(second.id, secondBatch.id, ref.caseId, {
    status: "通过", actual: "核对固定要求", evidence: "mock/second-work", sourceKind: "平台独立验证", ownerKind: "Agent", ownerId: "verifier", toolVersion: "mock/1",
  });
  publishTestReport(second.id);
  const merged = saveTestPlan({ name: "跨工作固定计划", scope: "W-104 / W-103", owner: "林予", targetBuild: "fixture/current", environment: "mock", assignment: "verifier", criterion: "全部条款", windowStart: "2026-10-13", windowEnd: "2026-10-14",
    caseIds: read().resources.tests.filter(test => ["W-104", "W-103"].includes(test.work)).map(test => test.id) });
  startTestBatch(merged); finish(); return merged;
}

test("both batch-start entries synchronize every work member without touching unrelated work", () => {
  for (const entry of ["work", "plan"]) {
    reset(); const merged = sharedPlan();
    const unrelated = structuredClone(read().work.filter(item => !["W-104", "W-103"].includes(item.id)));
    for (const id of ["W-104", "W-103"]) expect(read().work.find(item => item.id === id).qa).toBe(true);
    if (entry === "work") begin(); else startTestBatch(merged);
    for (const id of ["W-104", "W-103"]) {
      const item = read().work.find(item => item.id === id);
      expect(item.qa).toBe(false); expect(item.accepted).toBe(false); expect(qualityResult(read(), item)).toBe("待验证");
    }
    expect(read().work.filter(item => !["W-104", "W-103"].includes(item.id))).toEqual(unrelated);
  }
});

test("any invalid member rejects shared batch atomically through both start entries", () => {
  for (const entry of ["work", "plan"]) for (const invalid of ["accepted", "completed", "hold", "unsubmitted", "revision", "adoption"]) {
    reset(); const merged = sharedPlan();
    if (invalid === "accepted") workCommand("W-103", "accept");
    else update(s => {
      const other = s.work.find(item => item.id === "W-103");
      if (invalid === "completed") other.status = "已完成";
      if (invalid === "hold") other.rework = { hold: { reason: "停线" } };
      if (invalid === "unsubmitted") other.status = "进行中";
      if (invalid === "revision") s.resources.tests.find(test => test.work === other.id).revision++;
      if (invalid === "adoption") { other.qaPlanId = "unadopted"; s.role = "executor"; }
    });
    const before = structuredClone(read());
    expect(() => entry === "work" ? begin() : startTestBatch(merged)).toThrow(); expect(read()).toEqual(before);
  }
});

test("accepted evidence cannot be invalidated by plan replacement, case edits or standalone reruns", () => {
  for (const mode of ["internal", "external"]) {
    reset(); begin(); finish();
    workCommand("W-104", "contract", "", work().version, { mode, reason: "内部使用", recipient: "指定接收人" });
    workCommand("W-104", "accept");
    const input = { ...plan(), caseIds: plan().caseRefs.map(ref => ref.caseId), changeReason: "修订计划重验" };
    const testcase = read().resources.tests.find(test => test.id === input.caseIds[0]);
    const other = read().resources.tests.find(test => test.work === "W-103");
    for (const change of [
      () => saveTestPlan(input), () => saveTestPlan({ ...input, id: undefined }),
      () => saveTestPlan({ ...input, caseIds: [other.id] }),
      () => saveResource("tests", { ...testcase, steps: "新增步骤" }),
      () => saveResource("tests", { ...testcase, id: undefined }),
      () => saveResource("tests", { ...other, id: testcase.id }),
      () => api.resourceCommand("tests", testcase.id, "失败", "新验证失败"),
    ]) {
      const before = structuredClone(read()); expect(change).toThrow("当前版本已接受");
      expect(read()).toEqual(before); expect(quality()).toBe("通过");
    }
  }
});


test("plan revision synchronizes retained and removed work members while preserving fixed reports", () => {
  for (const removedId of ["W-104", "W-103"]) {
    reset(); sharedPlan();
    const previous = structuredClone(plan()), reports = structuredClone(read().resources.testreports);
    saveTestPlan({ ...previous, caseIds: previous.caseRefs.filter(ref => ref.snapshot.work !== removedId).map(ref => ref.caseId), changeReason: "显式移除工作，保留原固定报告" });
    for (const workId of ["W-104", "W-103"]) {
      const item = read().work.find(work => work.id === workId);
      expect(item.qa).toBe(false); expect(item.accepted).toBe(false);
      expect(qualityResult(read(), item)).toBe(workId === removedId ? "未执行" : "待验证");
    }
    expect(read().resources.testreports).toEqual(reports);
    expect(read().resources.testplans.find(item => item.id === previous.id).batches).toEqual(previous.batches);
  }
});
