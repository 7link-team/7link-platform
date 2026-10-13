import { completeValidation } from "./qa-helpers.js";
import { test, expect, beforeEach } from "bun:test";
if (!globalThis.localStorage) {
  const storage = new Map();
  globalThis.localStorage = { getItem: (key) => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: (key) => storage.delete(key) };
}
const { publishTestReport, startTestBatch, recordBatchResult, read, reset, update, workCommand, resourceCommand } = await import("../src/store.js?completion-tests");
const verifyWork = workId => completeValidation({ read, workCommand, recordBatchResult, publishTestReport, startTestBatch }, workId);
beforeEach(() => reset());
const work = () => read().work.find((w) => w.id === "W-104");

test("missing or incomplete receiving contracts cannot authorize completion", () => {
  verifyWork("W-104");
  expect(() => workCommand("W-104", "accept")).toThrow("接收契约");
  for (const contract of [{ mode: "internal", reason: "" }, { mode: "external", recipient: "" }, { mode: "unknown" }])
    expect(() => workCommand("W-104", "contract", "", 3, contract)).toThrow();
  expect(work().accepted).toBe(false);
  expect(work().completionContract).toBeUndefined();
  expect(read().resources.deliveries).toHaveLength(0);
});

test("internal use completes only after L2 and L3 while L4 is explicitly inapplicable", () => {
  workCommand("W-104", "contract", "", 3, { mode: "internal", reason: "仅供本人使用，无外部接收人" });
  expect(() => workCommand("W-104", "accept")).toThrow("内部检查");
  verifyWork("W-104"); workCommand("W-104", "accept");
  expect(work().status).toBe("已完成");
  expect(work().l4).toEqual({ status: "不适用", reason: "仅供本人使用，无外部接收人", version: 3 });
  expect(work().accepted).toBe(true);
  expect(work().delivered).toBe(false);
  expect(read().resources.deliveries).toHaveLength(0);
  expect(() => workCommand("W-104", "deliver")).toThrow();
  expect(() => workCommand("W-104", "contract", "", 3, { mode: "external", recipient: "客户" })).toThrow();
  expect(work().publication).toBeUndefined();
});

test("external delivery waits for all fixed items and the current package to be accepted", () => {
  workCommand("W-104", "contract", "", 3, { mode: "external", recipient: "指定接收人" });
  verifyWork("W-104"); workCommand("W-104", "accept"); workCommand("W-104", "deliver");
  const delivery = read().resources.deliveries[0];
  expect(work().status).toBe("待交接"); expect(work().l4.status).toBe("待接收");
  expect(delivery.recipient).toBe("指定接收人"); expect(delivery.status).toBe("待回执");
  resourceCommand("deliveries", delivery.id, "已接受", "全部符合约定");
  expect(work().status).toBe("已完成"); expect(work().l4.status).toBe("已接受");
  expect(delivery.items.every((i) => i.status === "待回执")).toBe(true);
  expect(read().resources.deliveries[0].items.every((i) => i.status === "已接受")).toBe(true);
});

test("old or invalidated delivery receipts never complete a reworked current work", () => {
  workCommand("W-104", "contract", "", 3, { mode: "external", recipient: "接收人" });
  verifyWork("W-104"); workCommand("W-104", "accept"); workCommand("W-104", "deliver");
  const delivery = read().resources.deliveries[0];
  workCommand("W-104", "return", "修订"); workCommand("W-104", "submit");
  resourceCommand("deliveries", delivery.id, "已接受", "旧版接收");
  expect(work().version).toBe(4); expect(work().status).toBe("待验收"); expect(work().l4).toBeUndefined();
  verifyWork("W-104"); workCommand("W-104", "accept"); workCommand("W-104", "deliver");
  const current = read().resources.deliveries[0];
  update((s) => s.resources.tests.find((t) => t.work === "W-104" && t.version === "v4").status = "失败");
  resourceCommand("deliveries", current.id, "已接受", "客户回执不能替代检查");
  expect(work().status).not.toBe("已完成");
});
