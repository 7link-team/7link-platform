import { completeValidation } from "./qa-helpers.js";
import { test, expect, beforeEach } from "bun:test";
if (!globalThis.localStorage) {
  const data = new Map();
  globalThis.localStorage = { getItem: (key) => data.get(key) || null, setItem: (key, value) => data.set(key, value), removeItem: (key) => data.delete(key) };
}
const { publishTestReport, startTestBatch, recordBatchResult, read, reset, update, workCommand, saveWorkInputs, prepareInputHandoff, dependencyProblems } = await import("../src/store.js?dependency-tests");
const verifyWork = workId => completeValidation({ read, workCommand, recordBatchResult, publishTestReport, startTestBatch }, workId);
beforeEach(() => {
  reset();
  update((s) => {
    for (const id of ["A", "B", "C", "D"]) {
      s.work.push({ id, title: id, project: "launch", version: 1, status: id === "B" ? "已完成" : "待验收",
        owner: "林予", agent: "Atlas", agentId: "atlas", production: { version: 1, executorIds: ["agent:atlas"] }, type: "软件", qa: true, accepted: id === "B", delivered: id === "B",
        plan: true, flow: true, flowVersion: 3, requirements: ["固定要求"], notes: "原成果",
        history: [{ version: 1, notes: "原成果", qa: true, accepted: id === "B" }],
        completionContract: { mode: "external", recipient: "接收人", reason: "" },
        ...(id === "B" ? { inputs: [{ workId: "A", version: 1 }] } : id === "C" ? { inputs: [{ workId: "B", version: 1 }] } : {}),
      });
      s.resources.tests.push({ requirementRefs: ["固定要求"], id: `test-${id}`, work: id, version: "v1", status: "通过", evidence: `mock/${id}/1` });
    }
    s.resources.deliveries.push({ id: "old-B", work: "B", version: "v1", status: "已接受", items: [{ id: "B-artifact", status: "已接受", snapshot: { notes: "原成果" } }] });
  });
  verifyWork("A");
});
const get = (id) => read().work.find((w) => w.id === id);

test("return propagates only along exact fixed input versions and preserves history and other branches", () => {
  const unaffected = structuredClone(get("D")), packages = structuredClone(read().resources.deliveries);
  workCommand("A", "return", "输入错误", 1);
  for (const id of ["B", "C"]) {
    expect(get(id).status).toBe("等待"); expect(get(id).qa).toBe(false); expect(get(id).accepted).toBe(false);
    expect(dependencyProblems(read(), get(id))).toHaveLength(1);
    for (const command of ["start", "resume", "submit", "check", "accept", "deliver"])
      expect(() => workCommand(id, command)).toThrow("输入依赖已失效");
  }
  expect(get("B").dependencyBlocks[0]).toMatchObject({ inputWorkId: "A", inputVersion: 1, sourceWorkId: "A", sourceVersion: 1 });
  expect(get("C").dependencyBlocks[0].inputWorkId).toBe("B");
  expect(get("B").dependencyHistory[0].accepted).toBe(true);
  expect(get("D")).toEqual(unaffected); expect(read().resources.deliveries).toEqual(packages);
});

test("new upstream versions require explicit adoption and each downstream produces and checks a new version", () => {
  workCommand("A", "return", "修复", 1);
  expect(() => saveWorkInputs("B", [], "删除失效引用", 1)).toThrow("不能移除");
  expect(() => saveWorkInputs("B", [{ workId: "A", version: 1 }], "旧版本", 1)).toThrow("未失效");
  workCommand("A", "submit");
  expect(() => saveWorkInputs("B", [{ workId: "A", version: 2 }], "未检查版本", 1)).toThrow("内部检查");
  verifyWork("A");
  expect(() => saveWorkInputs("B", [{ workId: "A", version: 2 }], "无固定包", 1)).toThrow("固定交接包");
  prepareInputHandoff("A", "B", 2);
  expect(get("A").accepted).toBe(false);
  saveWorkInputs("B", [{ workId: "A", version: 2 }], "采用已通过的新版本", 1);
  expect(get("A").accepted).toBe(true);
  expect(get("A").handoffs[0].receipt.level).toBe("L3");
  expect(get("B").inputs[0].handoffId).toBe(get("A").handoffs[0].id);
  expect(get("B").status).toBe("进行中"); expect(get("B").accepted).toBe(false); expect(get("B").qa).toBe(false);
  expect(dependencyProblems(read(), get("C"))).toHaveLength(1);
  expect(() => workCommand("B", "accept")).toThrow("内部检查");
  workCommand("B", "submit"); verifyWork("B");
  prepareInputHandoff("B", "C", 2);
  saveWorkInputs("C", [{ workId: "B", version: 2 }], "逐级采用新输出", 1);
  expect(dependencyProblems(read(), get("C"))).toEqual([]); expect(get("C").status).toBe("进行中");
  expect(get("C").qa).toBe(false); expect(get("C").inputHistory[0].inputs).toEqual([{ workId: "B", version: 1 }]);
});

test("dependency contracts reject missing, self, duplicate, cyclic and stale references atomically", () => {
  const before = structuredClone(read());
  for (const refs of [[{ workId: "missing", version: 1 }], [{ workId: "A", version: 1 }], [{ workId: "D", version: 9 }], [{ workId: "D", version: 1 }, { workId: "D", version: 1 }], [{ workId: "B", version: 1 }]])
    expect(() => saveWorkInputs("A", refs, "配置", 1)).toThrow();
  expect(() => saveWorkInputs("A", [], "过期", 2)).toThrow("已变化");
  expect(read()).toEqual(before);
  update((s) => { const d = s.work.find((w) => w.id === "D"); d.qa = false; d.status = "待开始"; });
  expect(get("D").inputs).toBeUndefined();
  saveWorkInputs("D", [], "确认不依赖其他工作", 1);
  expect(get("D").inputs).toEqual([]); expect(get("D").status).toBe("待开始");
  expect(() => saveWorkInputs("D", [], "旧契约", 1, 0)).toThrow("已变化");
});

test("returning a newer upstream output leaves consumers of an unrelated older version untouched", () => {
  update((s) => { const a = s.work.find((w) => w.id === "A"); a.version = 2; a.status = "进行中"; });
  const beforeB = structuredClone(get("B")), beforeC = structuredClone(get("C"));
  workCommand("A", "return", "仅第二版需修订", 2);
  expect(get("B")).toEqual(beforeB); expect(get("C")).toEqual(beforeC);
});


test("fixed handoffs require the specified Human and preserve snapshots across new output", () => {
  const packageId = prepareInputHandoff("A", "D", 1);
  const prepared = structuredClone(get("A").handoffs[0]);
  expect(prepareInputHandoff("A", "D", 1)).toBe(packageId);
  expect(() => saveWorkInputs("C", [{ workId: "A", version: 1 }], "错误接收目标", 1)).toThrow("固定交接包");
  update((s) => { s.work.find((w) => w.id === "D").owner = "周宁"; });
  prepareInputHandoff("A", "D", 1);
  expect(() => saveWorkInputs("D", [{ workId: "A", version: 1 }], "非指定接收人", 1)).toThrow("指定");
  expect(get("A").accepted).toBe(false);
  update((s) => { s.work.find((w) => w.id === "D").owner = "林予"; });
  saveWorkInputs("D", [{ workId: "A", version: 1 }], "接收完整固定包", 1);
  const received = structuredClone(get("A").handoffs[0]);
  expect(received.items.every((item) => item.status === "已接受")).toBe(true);
  expect(received.items.map((item) => item.snapshot)).toEqual(prepared.items.map((item) => item.snapshot));
  expect(received.history[0].items).toEqual(prepared.items);
  expect(get("A").delivered).toBe(false);
  workCommand("A", "return", "修订", 1); workCommand("A", "submit"); verifyWork("A");
  expect(get("A").handoffs[0]).toEqual(received);
  expect(get("D").dependencyBlocks).toHaveLength(1);
  expect(() => saveWorkInputs("D", [{ workId: "A", version: 2 }], "新版本无包", 1, 1)).toThrow("固定交接包");
});


test("internal receiving cannot revoke an existing completed external delivery", () => {
  update(s => { const source = s.work.find(w => w.id === "A"); source.accepted = true; source.delivered = true; source.status = "已完成"; source.l4 = { status: "已接受", version: 1, recipient: "接收人" }; });
  const previous = structuredClone(get("A").l4);
  prepareInputHandoff("A", "D", 1);
  saveWorkInputs("D", [{ workId: "A", version: 1 }], "采用已交付成果", 1);
  expect(get("A").status).toBe("已完成");
  expect(get("A").l4).toEqual(previous);
  expect(get("A").delivered).toBe(true);
});
