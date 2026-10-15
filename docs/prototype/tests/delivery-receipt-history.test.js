import { test, expect, beforeEach } from "bun:test";
import { completeValidation } from "./qa-helpers.js";
let api;
const storage = new Map();
globalThis.localStorage = { getItem: key => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) };
api = await import("../src/store.js");
beforeEach(() => { api.reset(); completeValidation(api, "W-103"); api.workCommand("W-103", "accept", "本地内部接受", 2); api.workCommand("W-103", "deliver", "本地固定交付", 2); api.update(state => state.role = "external"); });
const getDelivery = () => api.read().resources.deliveries.find(delivery => delivery.work === "W-103");
function choices(delivery, status) {
  return { version: delivery.version, items: delivery.items.map((item, index) => ({ id: item.id, status: status === "部分接受" ? index === 0 ? "已接受" : "已拒收" : status,
    ...(status === "已拒收" || status === "部分接受" && index !== 0 ? { feedback: { requirement: delivery.requirements[0], position: "检查记录第一条", expected: "保留可定位的完整检查记录", actual: "当前记录缺少定位信息" } } : {}) })) };
}
test("a fixed delivery receipt cannot replace rejected decisions on the same package", () => {
  for (const firstResult of ["部分接受", "已拒收"]) {
    api.reset(); completeValidation(api, "W-103"); api.workCommand("W-103", "accept", "内部接受", 2); api.workCommand("W-103", "deliver", "固定交付", 2); api.update(state => state.role = "external");
    const delivery = getDelivery();
    api.resourceCommand("deliveries", delivery.id, firstResult, "固定客户回执", "", choices(delivery, firstResult));
    const before = structuredClone(api.read());
    expect(() => api.resourceCommand("deliveries", delivery.id, "已接受", "改变原拒收决定", "", choices(delivery, "已接受"))).toThrow("固定回执");
    expect(api.read()).toEqual(before);
  }
});
test("identical receipt retries preserve the first receipt and do not repeat rework", () => {
  for (const status of ["已接受", "部分接受", "已拒收"]) {
    api.reset(); completeValidation(api, "W-103"); api.workCommand("W-103", "accept", "内部接受", 2); api.workCommand("W-103", "deliver", "固定交付", 2); api.update(state => state.role = "external");
    const delivery = getDelivery(), receipt = choices(delivery, status);
    api.resourceCommand("deliveries", delivery.id, status, "固定客户回执", "", receipt);
    const before = structuredClone(api.read());
    api.resourceCommand("deliveries", delivery.id, status, "网络重试不改原回执", "", receipt);
    expect(api.read()).toEqual(before);
  }
});
test("editing rejection evidence is a new decision and cannot overwrite the original receipt", () => {
  const delivery = getDelivery(), receipt = choices(delivery, "部分接受");
  api.resourceCommand("deliveries", delivery.id, "部分接受", "原始拒收理由", "", receipt);
  const before = structuredClone(api.read());
  const replacement = structuredClone(receipt); replacement.items[1].feedback.actual = "另一个拒收事实与原决定不同";
  expect(() => api.resourceCommand("deliveries", delivery.id, "部分接受", "改写原拒收理由", "", replacement)).toThrow("固定回执");
  expect(api.read()).toEqual(before);
});

test("rework creates a new delivery receipt without changing the original package", async () => {
  const first = getDelivery();
  api.resourceCommand("deliveries", first.id, "部分接受", "固定原回执", "", choices(first, "部分接受"));
  const original = structuredClone(api.read().resources.deliveries.find(delivery => delivery.id === first.id));
  api.update(state => state.role = "owner");
  const { workRework } = await import("../src/rework.js");
  let control = workRework(api.read().work.find(work => work.id === "W-103"));
  api.workCommand("W-103", "rework-config", "Human 明确本专业返工上限", 2, { revision: control.revision, formal: 3, internal: 3 });
  control = workRework(api.read().work.find(work => work.id === "W-103"));
  api.workCommand("W-103", "rework-continue", "Human 允许按原拒收依据修订", 2, { revision: control.revision });
  api.workCommand("W-103", "submit", "修订原拒收成果", 2);
  completeValidation(api, "W-103");
  const current = api.read().work.find(work => work.id === "W-103");
  api.workCommand("W-103", "accept", "接受新修订成果", current.version);
  api.workCommand("W-103", "deliver", "形成新的固定包", current.version);
  const next = getDelivery();
  expect(next.id).not.toBe(first.id); expect(next.version).not.toBe(first.version);
  api.update(state => state.role = "external");
  api.resourceCommand("deliveries", next.id, "已接受", "新版本客户接受", "", choices(next, "已接受"));
  expect(api.read().resources.deliveries.find(delivery => delivery.id === first.id)).toEqual(original);
  expect(api.read().resources.deliveries.find(delivery => delivery.id === next.id).status).toBe("已接受");
  expect(api.read().work.find(work => work.id === "W-103").status).toBe("已完成");
});



test("all delivery receipt outcomes require the same recorder authority and denied commands are atomic", () => {
  for (const role of ["executor", "owner", "external"]) {
    for (const status of ["已接受", "部分接受", "已拒收"]) {
      api.reset(); completeValidation(api, "W-103");
      api.workCommand("W-103", "accept", "内部接受", 2);
      api.workCommand("W-103", "deliver", "固定交付", 2);
      api.update(state => state.role = role);
      const delivery = getDelivery(), receipt = choices(delivery, status);
      const before = structuredClone(api.read()), saved = [...storage];
      if (role === "executor") {
        expect(() => api.resourceCommand("deliveries", delivery.id, status, "未经授权的登记", "", receipt)).toThrow("负责人权限");
        expect(api.read()).toEqual(before);
        expect([...storage]).toEqual(saved);
      } else {
        api.resourceCommand("deliveries", delivery.id, status, "有权身份登记", "", receipt);
        expect(getDelivery().status).toBe(status);
        expect(getDelivery().history).toHaveLength(1);
      }
    }
  }
});
