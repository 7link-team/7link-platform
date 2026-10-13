import { completeValidation } from "./qa-helpers.js";
import { beforeEach, expect, test } from "bun:test";
const entries = new Map();
Object.defineProperty(globalThis, "localStorage", {
  configurable: true,
  value: {
    getItem: (key) => entries.get(key) || null,
    setItem: (key, value) => entries.set(key, value),
    removeItem: (key) => entries.delete(key),
  },
});
const {
  publishTestReport, startTestBatch, recordBatchResult, read,
  reset,
  update,
  resourceCommand,
  saveResource,
  workCommand,
  defectVerificationRuns, defectRecords,
} = await import("../src/store.js");
const verifyWork = workId => completeValidation({ read, workCommand, recordBatchResult, publishTestReport, startTestBatch }, workId);
beforeEach(() => reset());
const testcase = () =>
  read().resources.tests.find((item) => item.id === "TC-01");
const defect = () =>
  defectRecords(read()).find((item) => item.id === "BUG-01");
const run = (status = "通过") => {
  resourceCommand("tests", "TC-01", status, `当前修复${status}依据`);
  return testcase().runs.at(-1);
};
const repair = () =>
  resourceCommand(
    "defects",
    "BUG-01",
    "待验证",
    "已提交对应版本修复",
    "提交修复",
  );
const close = (runId) =>
  resourceCommand("defects", "BUG-01", "已关闭", "验证符合原期望", "验证通过", {
    runId,
  });

test("attempts retain old full facts and failed evidence never reuses success", () => {
  const original = structuredClone(testcase());
  run();
  const captured = structuredClone(testcase().runs);
  expect(captured[0]).toMatchObject({
    caseId: "TC-01",
    status: original.status,
    version: original.version,
    expected: original.expected,
    actual: original.actual,
    evidence: original.evidence,
    inputs: original.inputs,
    method: original.method,
    owner: original.owner,
    source: "legacy",
    actor: null,
    time: null,
  });
  expect(captured[0].workRequirements).toEqual(
    read().work.find((item) => item.id === "W-104").requirements,
  );
  const failure = run("失败");
  expect(failure.evidence).not.toBe(captured[1].evidence);
  expect(failure).toMatchObject({
    status: "失败",
    source: "mock",
    actor: "林予",
    sourceKind: "平台独立验证",
  });
  expect(failure.time).toBeTruthy();
  expect(testcase().runs.slice(0, 2)).toEqual(captured);
  const runs = structuredClone(testcase().runs);
  saveResource("tests", {
    ...testcase(),
    expected: "新要求",
    inputs: "新输入",
    runs: [],
  });
  expect(testcase().runs).toEqual(runs);
  expect(testcase().currentRun).toBeUndefined();
  expect(testcase().status).toBe("待执行");
  expect(testcase().evidence).toBe("");
});
test("work-level recheck uses full immutable capture", () => {
  const old = structuredClone(testcase());
  verifyWork("W-104");
  expect(testcase().runs[0]).toMatchObject({
    actual: old.actual,
    evidence: old.evidence,
    expected: old.expected,
    inputs: old.inputs,
  });
  expect(testcase().runs.at(-1).source).toBe("mock");
});
test("green work alone cannot close and mismatched version cannot submit repair", () => {
  update((s) => {
    s.work.find((item) => item.id === "W-104").qa = true;
  });
  expect(() => close()).toThrow("先提交修复");
  saveResource("defects", { ...defect(), version: "v2", status: "已关闭" });
  expect(defect().status).toBe("待修复");
  expect(() => repair()).toThrow("准确工作版本");
});
test("closure requires post-repair independent run tied to current defect version", () => {
  const early = run();
  repair();
  expect(() => close(early.id)).toThrow("独立验证证据");
  const accepted = run();
  close(accepted.id);
  expect(defect()).toMatchObject({
    status: "已关闭",
    repairVersion: "v3",
    verification: accepted,
  });
  const verification = structuredClone(defect().verification);
  run("失败");
  expect(defect().verification).toEqual(verification);
});
test("self tests and repair-owner reviews are not independent", () => {
  repair();
  for (const patch of [
    { sourceKind: "Agent 自测" },
    { sourceKind: "平台独立验证", owner: "Atlas" },
    { sourceKind: "平台独立验证", owner: "Verifier", defect: "" },
  ]) {
    saveResource("tests", { ...testcase(), ...patch });
    const result = run();
    expect(() => close(result.id)).toThrow("独立验证证据");
    expect(defect().status).toBe("待验证");
  }
});
test("failed verification reopens and retains selected immutable failure", () => {
  repair();
  const failed = run("失败");
  expect(() => close(failed.id)).toThrow("结论与缺陷动作");
  resourceCommand(
    "defects",
    "BUG-01",
    "待修复",
    "原复现仍失败",
    "验证失败并重开",
    { runId: failed.id },
  );
  expect(defect()).toMatchObject({ status: "待修复", verification: failed });
  repair();
  expect(
    defect().history.some((item) => item.verification?.id === failed.id),
  ).toBe(true);
  expect(() => close(failed.id)).toThrow("独立验证证据");
  close(run().id);
  expect(defect().status).toBe("已关闭");
});
test("new failure, case revision, or work version invalidate an older pass", () => {
  repair();
  const passed = run();
  run("失败");
  expect(() => close(passed.id)).toThrow("独立验证证据");
  const latest = run();
  saveResource("tests", { ...testcase(), inputs: "已变更输入" });
  expect(() => close(latest.id)).toThrow("独立验证证据");
  const revised = run();
  update((s) => {
    s.work.find((item) => item.id === "W-104").version++;
  });
  expect(defectVerificationRuns(read(), defect())).toEqual([]);
  expect(() => close(revised.id)).toThrow("独立验证证据");
});

test("test defect references require the same tenant work and version and reject atomically", () => {
  for (const change of [{ defect: "missing-defect" }, { work: "W-103", requirementRefs: ["遵循品牌色与素材授权"] }, { version: "v2" }]) {
    const before = structuredClone(read());
    expect(() => saveResource("tests", { ...testcase(), ...change })).toThrow("同一工作与同一版本");
    expect(read()).toEqual(before);
  }
  saveResource("tests", { ...testcase(), defect: "BUG-01" });
  expect(testcase().defect).toBe("BUG-01");
  saveResource("tests", { ...testcase(), defect: "" });
  expect(testcase().defect).toBe("");
  expect(() => run()).not.toThrow();
});

test("new results reject moved defects while historical evidence stays readable", () => {
  run();
  const history = structuredClone(testcase().runs);
  update(s => { s.work.find(item => item.id === "BUG-01").defect.affectedVersion = "v999"; });
  const before = structuredClone(read());
  expect(() => run()).toThrow("同一工作与同一版本");
  expect(read()).toEqual(before);
  saveResource("tests", { ...testcase(), defect: "" });
  expect(testcase().runs).toEqual(history);
  expect(testcase().runs.every(result => result.defect === "BUG-01")).toBe(true);
});

test("fixed batch cannot produce a result after its associated defect moves", () => {
  const href = workCommand("W-104", "check");
  const plan = read().resources.testplans.find(item => `/testplans/${item.id}` === href);
  const batch = plan.batches.find(item => item.id === plan.currentBatchId);
  update(s => { s.work.find(item => item.id === "BUG-01").defect.affectedWorkId = "W-103"; });
  const before = structuredClone(read());
  expect(() => recordBatchResult(plan.id, batch.id, "TC-01", {
    status: "通过", actual: "固定范围验证", evidence: "mock-evidence/defect-match", sourceKind: "平台独立验证",
    ownerKind: "Agent", ownerId: "verifier", toolVersion: "mock-browser/1",
  })).toThrow("同一工作与同一版本");
  expect(read()).toEqual(before);
});
