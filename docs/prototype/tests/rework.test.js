import { test, expect, beforeEach } from "bun:test";
if (!globalThis.localStorage) {
  const cache = new Map();
  globalThis.localStorage = { getItem: key => cache.get(key) || null, setItem: (key, value) => cache.set(key, value), removeItem: key => cache.delete(key) };
}
const { read, reset, update, workCommand, workRework, recordFormalRework, saveWorkTeam, saveWorkInputs, prepareInputHandoff, recordBatchResult, publishTestReport } = await import("../src/store.js?rework-tests");
const { recordInternalRound } = await import("../src/rework.js");
beforeEach(reset);
const work = (id = "W-104") => read().work.find(item => item.id === id);
const control = (id = "W-104") => workRework(work(id));
const decide = (command, patch = {}, id = "W-104") => workCommand(id, command, "Human 核对结果后明确决定", work(id).version, { revision: control(id).revision, ...patch });
function failedBatch(key) {
  update(s => {
    const w = s.work.find(item => item.id === "W-104");
    const failures = [1, 2].map(index => ({ id: `failure-${w.version}-${index}`, work: w.id, version: `v${w.version}`, status: "失败", currentRun: `run-${w.version}-${index}`, actual: "未满足原条款", evidence: "mock-failure" }));
    for (const failed of failures) if (!s.resources.tests.some(test => test.id === failed.id)) s.resources.tests.push(failed);
    recordInternalRound(w, { key: key || `batch:${w.version}`, version: w.version, failures }, "固定批次结束，必需验证失败");
  });
}
const repair = () => workCommand("W-104", "repair", "修复本轮验证发现的问题", work().version);
const reject = () => workCommand("W-104", "return", "固定版本未满足验收条件", work().version, { requirement: work().requirements[0], expected: "满足原条款", actual: "仍有缺陷", position: "首页" });

test("one failed fixed batch counts once regardless of case count; fifth failed batch stops before another repair", () => {
  expect(control().internal).toBe(0); // Historical failed checks without a closed batch do not invent rounds.
  for (let round = 1; round <= 5; round++) {
    const version = work().version;
    failedBatch(); failedBatch(); // Same closed batch is idempotent.
    expect(control()).toMatchObject({ internal: round, internalTotal: round, formalTotal: 0 });
    expect(control().events.filter(event => event.kind === "internal").at(-1).failures.length).toBe(2);
    if (round < 5) {
      repair(); expect(work().version).toBe(version + 1); expect(control().internal).toBe(round);
      expect(() => workCommand("W-104", "repair", "重复提交", version)).toThrow("版本");
    }
  }
  expect(control().hold.kind).toBe("internal"); expect(work().status).toBe("等待");
  for (const command of ["start", "resume", "submit", "repair", "check", "accept", "deliver", "return"])
    expect(() => workCommand("W-104", command, "尝试绕过", work().version)).toThrow("停线");
});

test("Human continuation opens a finite internal window without clearing counts or bypassing verification", () => {
  decide("rework-config", { internal: 1, formal: 3 }); failedBatch();
  update(s => { s.role = "executor"; }); expect(() => decide("rework-continue", { additionalRounds: 2 })).toThrow("负责人");
  update(s => { s.role = "owner"; }); expect(() => decide("rework-continue")).toThrow("轮数");
  const snapshot = structuredClone(read());
  expect(() => workCommand("W-104", "rework-continue", "旧对话框", work().version, { revision: 0, additionalRounds: 2 })).toThrow("已变化"); expect(read()).toEqual(snapshot);
  decide("rework-continue", { additionalRounds: 2 });
  expect(control()).toMatchObject({ internal: 1, internalTotal: 1, internalCeiling: 3, hold: null }); expect(work().status).toBe("待验收");
  expect(() => workCommand("W-104", "accept")).toThrow("内部检查");
  repair(); failedBatch(); expect(control().hold).toBeNull();
  repair(); failedBatch(); expect(control().hold.kind).toBe("internal"); expect(control().internalTotal).toBe(3);
  expect(control().decisions.find(item => item.action === "CONTINUE")).toMatchObject({ additionalRounds: 2, internal: 1 });
});

test("formal rejection resets only internal declaration chain; business total survives and every post-limit rejection stops", () => {
  failedBatch(); repair(); expect(control().internal).toBe(1);
  for (let count = 1; count <= 4; count++) {
    reject();
    expect(control()).toMatchObject({ internal: 0, internalTotal: 1, formalTotal: count });
    if (count >= 3) {
      expect(control().hold.kind).toBe("formal"); expect(() => workCommand("W-104", "resume")).toThrow("停线");
      decide("rework-continue"); expect(control().formalTotal).toBe(count);
    }
    workCommand("W-104", "submit");
  }
  expect(control().decisions.filter(item => item.action === "CONTINUE").map(item => item.formalWindow)).toEqual([1, 1]);
});

test("fixed package multiple rejected items and repeat decisions increment once without inventing legacy actor", () => {
  update(s => {
    const w = s.work.find(item => item.id === "W-104");
    w.returnFeedback = [{ delivery: "package-a", version: "v3", reason: "第一项拒收" }, { delivery: "package-a", version: "v3", reason: "第二项拒收" }];
    recordFormalRework(s, w, "逐项拒收", { key: "delivery:package-a", source: "delivery", version: "v3" });
  });
  expect(control().formalTotal).toBe(1);
  expect(control().events.find(event => event.kind === "formal")).toMatchObject({ actor: "H-lin", reason: "逐项拒收" });
  expect(control().events.find(event => event.kind === "formal").legacy).toBeUndefined();
  update(s => recordFormalRework(s, s.work.find(item => item.id === "W-104"), "重复回执", { key: "delivery:package-a", source: "delivery", version: "v3" }));
  expect(control().formalTotal).toBe(1);
});

test("non-software limits stay unknown until explicit configuration; editing limits never silently clears a hold", () => {
  expect(control("W-103").limits).toEqual({ internal: null, formal: null });
  workCommand("W-103", "return", "视觉质量未满足", work("W-103").version);
  expect(control("W-103").hold.kind).toBe("configuration");
  expect(() => decide("rework-continue", {}, "W-103")).toThrow("配置");
  for (const internal of [0, -1, 1.5, "", Infinity, true]) expect(() => decide("rework-config", { internal, formal: 3 }, "W-103")).toThrow("正整数");
  decide("rework-config", { internal: 2, formal: 3 }, "W-103"); expect(control("W-103").hold.kind).toBe("configuration");
  decide("rework-continue", {}, "W-103"); expect(control("W-103").formalTotal).toBe(1);
  workCommand("W-103", "submit"); expect(work("W-103").status).toBe("待验收");
});

test("reassignment, takeover and cancellation preserve accounting and the stop decision", () => {
  decide("rework-config", { internal: 1, formal: 3 }); failedBatch();
  const before = structuredClone(control());
  saveWorkTeam("W-104", { revision: work().team?.revision || 0, previousOwner: work().owner, ownerId: "H-lin", coordinatorId: "agent:atlas", executorIds: ["agent:muse"], checkerIds: ["agent:verifier"], receiverId: "H-lin" }, "更换制作人");
  expect(control()).toEqual(before);
  workCommand("W-104", "takeover", "由 Human 接管核查"); expect(control().hold.kind).toBe("internal");
  expect(() => workCommand("W-104", "resume")).toThrow("停线");
  workCommand("W-104", "cancel", "停止本工作并保留部分成果");
  expect(work().status).toBe("已取消"); expect(control().internalTotal).toBe(1); expect(control().decisions.at(-1).action).toBe("cancel");
  expect(() => decide("rework-continue", { additionalRounds: 1 })).toThrow("已取消");
});

test("duplicate package feedback never clears an unrelated stop or re-stops an authorized window", () => {
  decide("rework-config", { internal: 1, formal: 1 });
  const receipt = () => update(s => {
    const w = s.work.find(item => item.id === "W-104"); w.status = "进行中";
    recordFormalRework(s, w, "同包下一条拒收", { key: "delivery:one", source: "delivery", version: "v3" });
  });
  receipt(); expect(control().hold.kind).toBe("formal");
  expect(() => workCommand("W-104", "return", "另一个理由", work().version)).toThrow("停线");
  receipt(); expect(work().status).toBe("等待"); expect(control().formalTotal).toBe(1);
  decide("rework-continue"); receipt(); expect(work().status).toBe("进行中"); expect(control().hold).toBeNull();
  workCommand("W-104", "submit"); failedBatch(); expect(control().hold.kind).toBe("internal");
  receipt(); expect(work().status).toBe("等待"); expect(control().hold.kind).toBe("internal"); expect(control().formalTotal).toBe(1);
});

test("input-contract edits preserve a rework stop and no handoff may bypass it", () => {
  decide("rework-config", { internal: 1, formal: 3 }); failedBatch();
  const stop = structuredClone(control().hold);
  saveWorkInputs("W-104", [], "明确当前修复输入", work().version, work().inputsRevision || 0);
  expect(work().status).toBe("等待"); expect(control().hold).toEqual(stop);
  expect(() => prepareInputHandoff("W-104", "W-103", work().version, "H-lin")).toThrow("停线");
  expect(() => workCommand("W-104", "submit")).toThrow("停线");
  update(s => { s.work.find(item => item.id === "W-104").dependencyBlocks = [{ inputWorkId: "W-103", inputVersion: 2, reason: "输入仍失效" }]; });
  decide("rework-continue", { additionalRounds: 1 }); expect(control().hold).toBeNull(); expect(work().status).toBe("等待");
  expect(() => workCommand("W-104", "check")).toThrow("输入依赖");
});

test("every submission path preserves unresolved failures through takeover, resume and input changes", () => {
  for (const role of ["owner", "executor"]) for (const path of ["takeover", "resume", "inputs"]) {
    reset(); update(s => { s.role = role; });
    if (path === "inputs" && role === "executor") continue;
    if (path === "inputs") saveWorkInputs("W-104", [], "补充输入", work().version);
    else { workCommand("W-104", "takeover", "本人接管修复"); if (path === "resume") workCommand("W-104", "resume"); }
    const before = structuredClone(read());
    expect(() => workCommand("W-104", "submit")).toThrow("提交内部修复"); expect(read()).toEqual(before);
    repair(); expect(work().version).toBe(4); expect(control().internalTotal).toBe(0); // No historical batch invented.
    expect(control().events.at(-1)).toMatchObject({ kind: "repair", version: 3, submittedVersion: 4 });
  }
});

test("legacy formal totals enforce a derived stop until an explicit Human continuation", () => {
  update(s => { const w = s.work.find(item => item.id === "W-104"); w.returnFeedback = [1, 2, 3].map(version => ({ version, reason: "历史退回" })); });
  expect(control()).toMatchObject({ formalTotal: 3, hold: { kind: "formal", legacy: true } });
  expect(control().events.every(event => event.actor === undefined)).toBe(true);
  workCommand("W-104", "takeover", "Human检查历史停线"); expect(work().status).toBe("等待");
  expect(() => workCommand("W-104", "submit")).toThrow("停线");
  decide("rework-continue"); workCommand("W-104", "submit"); expect(control().formalTotal).toBe(3);
  expect(control().decisions.some(event => event.action === "CONTINUE")).toBe(true);
});

test("Human engineer may request changes under existing permissions and fresh decision has actor/time", () => {
  update(s => { s.role = "executor"; }); reject();
  expect(control().formalTotal).toBe(1);
  const event = control().events.find(event => event.kind === "formal");
  expect(event).toMatchObject({ actor: "H-lin", source: "work", key: "work:v3" }); expect(event.time).toBeDefined(); expect(event.legacy).toBeUndefined();
  workCommand("W-104", "submit"); expect(control().internalTotal).toBe(0);
});


test("unfinished failed batch blocks every new version path until its immutable report settles the round", () => {
  const href = workCommand("W-104", "check");
  const plan = read().resources.testplans.find(item => `/testplans/${item.id}` === href);
  const batch = plan.batches.find(item => item.id === plan.currentBatchId);
  recordBatchResult(plan.id, batch.id, batch.caseRefs[0].caseId, { status: "失败", actual: "原条款未满足", evidence: "mock-failure/current-round", sourceKind: "平台独立验证", ownerKind: "Agent", ownerId: "verifier", toolVersion: "mock/1" });
  expect(control().internalTotal).toBe(0);
  const before = structuredClone(read());
  for (const command of ["submit", "repair", "return"]) expect(() => workCommand("W-104", command, "尝试跳过结算", work().version)).toThrow("结束失败验证批次");
  expect(read()).toEqual(before);
  workCommand("W-104", "takeover", "Human接管仍需先结算"); expect(() => repair()).toThrow("结束失败验证批次");
  const reportId = publishTestReport(plan.id);
  expect(control().internalTotal).toBe(1); expect(control().events.find(event => event.kind === "internal").key).toBe(`batch:${batch.id}`);
  const report = structuredClone(read().resources.testreports.find(item => item.id === reportId));
  repair(); expect(work().version).toBe(4); expect(control().internalTotal).toBe(1);
  expect(read().resources.testreports.find(item => item.id === reportId)).toEqual(report);
});
