import { test, expect, beforeEach } from "bun:test";
if (!globalThis.localStorage) {
  const data = new Map();
  globalThis.localStorage = { getItem: key => data.get(key) || null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) };
}
const { read, reset, update, saveHumanEffort, reviewHumanEffort, humanConfirmedHours, myEffortTasks, saveResource, resourceCommand, switchTenant } = await import("../src/store.js?effort-tests");
beforeEach(reset);
const get = id => read().resources.worklogs.find(record => record.id === id);
const estimate = (patch = {}) => saveHumanEffort({ action: "estimate", timezone: "Asia/Singapore", category: "验收", workId: "W-104", date: "2026-10-14", estimateHours: 2, note: "根据本人待完成的验收工作评估", ...patch });
const calibrate = (id, value, revision = get(id).revision || 1) => saveHumanEffort({ id, revision, action: "calibrate", timezone: "Asia/Singapore", category: "验收", calibratedHours: value, note: "核对实际参与的验收投入" });
const confirm = (id, revision = get(id).revision || 1) => saveHumanEffort({ id, revision, action: "confirm" });

test("Human estimate, calibration and confirmed actual are separate with immutable revision history", () => {
  const id = estimate();
  expect(get(id)).toMatchObject({ ownerId: "H-lin", effortState: "estimate", estimateHours: 2, suggestedHours: null, revision: 1 });
  expect(humanConfirmedHours(get(id))).toBeNull(); expect(get(id).hours).toBeUndefined();
  expect(() => confirm(id)).toThrow("先保存校准");
  calibrate(id, 1.25);
  expect(get(id)).toMatchObject({ effortState: "calibrated", estimateHours: 2, calibratedHours: 1.25, revision: 2 });
  expect(humanConfirmedHours(get(id))).toBeNull();
  confirm(id);
  expect(get(id)).toMatchObject({ effortState: "confirmed", confirmedHours: 1.25, hours: 1.25, confirmedBy: "H-lin", revision: 3 });
  expect(humanConfirmedHours(get(id))).toBe(1.25);
  const confirmedSnapshot = structuredClone(get(id));
  calibrate(id, 1.5);
  expect(humanConfirmedHours(get(id))).toBeNull(); expect(get(id).confirmedBy).toBeUndefined();
  expect(get(id).history.at(-1)).toMatchObject({ effortState: "confirmed", confirmedHours: 1.25, confirmedAt: confirmedSnapshot.confirmedAt });
  expect(get(id).history[0].estimateHours).toBe(2);
  confirm(id); expect(humanConfirmedHours(get(id))).toBe(1.5);
});

test("legacy submitted facts remain actual while legacy drafts and new suggestions are not actual", () => {
  expect(humanConfirmedHours(get("WL-01"))).toBe(1.5);
  expect(get("WL-01").confirmedAt).toBeUndefined();
  expect(humanConfirmedHours(get("WL-02"))).toBeNull();
  expect(humanConfirmedHours(get("WL-03"))).toBeNull();
  const before = structuredClone(get("WL-02"));
  calibrate("WL-02", 0.75);
  expect(get("WL-02").legacyHours).toBe("0.5");
  expect(get("WL-02").history[0]).toMatchObject(before);
  expect(humanConfirmedHours({ effortState: "estimate", suggestedHours: 9, estimateHours: 3, hours: 10, status: "已提交" })).toBeNull();
  for (const hours of [NaN, Infinity, -1, 25, "", null]) expect(humanConfirmedHours({ effortState: "confirmed", confirmedHours: hours })).toBeNull();
});

test("stale revisions, other people and readonly Agent metrics cannot be changed or confirmed", () => {
  const telemetry = structuredClone(get("WL-03"));
  expect(() => calibrate("WL-01", 2)).toThrow("本人");
  expect(() => calibrate("WL-03", 2)).toThrow("只读");
  const id = estimate(); calibrate(id, 1);
  const state = structuredClone(read());
  expect(() => confirm(id, 1)).toThrow("已变化");
  expect(() => saveHumanEffort({ id, revision: 2, action: "calibrate", calibratedHours: 1, note: "修改目标", workId: "W-102" })).toThrow("不可改写");
  for (const record of [get("WL-01"), get("WL-02"), telemetry]) {
    expect(() => saveResource("worklogs", { ...record, hours: 20 })).toThrow("工作量");
    for (const status of ["已提交", "已接受", "草稿"]) expect(() => resourceCommand("worklogs", record.id, status, "绕过")).toThrow("工作量");
  }
  expect(read()).toEqual(state); expect(get("WL-03")).toEqual(telemetry);
});

test("24-hour day limit sums only this person's confirmed actual and includes legacy submissions", () => {
  update(s => { s.resources.worklogs.push({ id: "confirmed", owner: "林予", status: "已提交", hours: "23", date: "2026-10-14", work: "W-102" }); });
  const id = estimate({ estimateHours: 24 });
  calibrate(id, 2); // Draft calibration is allowed and does not count as actual.
  const before = structuredClone(get(id));
  expect(() => confirm(id)).toThrow("合计不能超过 24"); expect(get(id)).toEqual(before);
  calibrate(id, 1); confirm(id); expect(humanConfirmedHours(get(id))).toBe(1);
  const tomorrow = estimate({ date: "2026-10-15", estimateHours: 24 }); calibrate(tomorrow, 24); confirm(tomorrow);
  expect(humanConfirmedHours(get(tomorrow))).toBe(24);
});

test("task/date identity, real dates and numeric bounds are enforced without guessing effort", () => {
  expect(myEffortTasks(read()).map(work => work.id)).not.toContain("W-103");
  for (const patch of [{ workId: "W-103" }, { workId: "missing" }, { date: "2026-02-30" }, { estimateHours: "" }, { estimateHours: -1 }, { estimateHours: 25 }, { estimateHours: Infinity }, { note: "" }])
    expect(() => estimate(patch)).toThrow();
  const id = estimate({ estimateHours: 0 });
  expect(() => estimate()).toThrow("已有本人记录");
  expect(get(id).suggestedHours).toBeNull();
  calibrate(id, 0); confirm(id); expect(humanConfirmedHours(get(id))).toBe(0);
});

test("offline, external role and tenant switching cannot change another person's or tenant's effort", () => {
  update(s => { s.role = "external"; }); expect(() => estimate()).toThrow();
  update(s => { s.role = "owner"; s.offline = true; }); expect(() => estimate()).toThrow();
  update(s => { s.offline = false; });
  const id = estimate();
  update(s => { s.tenants.push({ id: "other", name: "Other", role: "owner" }); });
  switchTenant("other"); expect(() => confirm(id, 1)).toThrow("当前租户"); expect(myEffortTasks(read())).toEqual([]);
  switchTenant("studio"); expect(get(id).effortState).toBe("estimate");
});


test("Human executor can assess and confirm personal effort but cannot act for another person", () => {
  update(s => { s.role = "executor"; });
  const id = estimate(); calibrate(id, 1); confirm(id);
  expect(humanConfirmedHours(get(id))).toBe(1);
  expect(() => calibrate("WL-01", 1)).toThrow("本人");
  expect(() => confirm("WL-01")).toThrow("本人");
  expect(() => estimate({ workId: "W-103" })).toThrow("本人参与");
});

test("timezone and category are explicit; historical missing metadata is not fabricated", () => {
  expect(get("WL-02").timezone).toBeUndefined(); expect(get("WL-02").category).toBe("协作");
  for (const patch of [{ timezone: "" }, { timezone: "Invalid/Zone" }, { category: " " }]) expect(() => estimate(patch)).toThrow();
  const id = estimate(); expect(get(id)).toMatchObject({ timezone: "Asia/Singapore", category: "验收" });
  calibrate(id, 2); confirm(id);
  saveHumanEffort({ id, revision: get(id).revision, action: "calibrate", calibratedHours: 2, note: "更正类别", category: "开发", timezone: "UTC" });
  expect(humanConfirmedHours(get(id))).toBeNull();
  expect(get(id).history.at(-1)).toMatchObject({ timezone: "Asia/Singapore", category: "验收", confirmedHours: 2 });
});

test("supervisor review requires self-confirmed actual; return preserves snapshots and requires recalibration", () => {
  const id = estimate();
  const review = (action, reason = "核对任务记录", revision = get(id).revision) => reviewHumanEffort(id, action, reason, revision);
  expect(() => review("accept")).toThrow("本人已确认");
  calibrate(id, 2); expect(() => review("return")).toThrow("本人已确认"); confirm(id);
  update(s => { s.resources.reports.push({ id: "effort-snapshot", status: "已接受", factSnapshot: structuredClone(get(id)) }); });
  const reportSnapshot = structuredClone(read().resources.reports.at(-1));
  expect(() => review("return", "")).toThrow("原因");
  update(s => { s.role = "executor"; }); expect(() => review("accept")).toThrow("负责人");
  update(s => { s.role = "owner"; });
  expect(() => review("accept", "核对", 1)).toThrow("修订已变化");
  review("return"); expect(get(id)).toMatchObject({ effortState: "returned", reviewState: "returned" });
  expect(humanConfirmedHours(get(id))).toBeNull(); expect(get(id).history.at(-1).confirmedHours).toBe(2);
  expect(() => confirm(id)).toThrow("先保存校准");
  calibrate(id, 1); confirm(id); review("accept");
  expect(get(id)).toMatchObject({ effortState: "confirmed", confirmedHours: 1, reviewState: "accepted", reviewedBy: "H-lin", status: "已接受" });
  expect(() => review("accept")).toThrow("待审核");
  expect(read().resources.reports.at(-1)).toEqual(reportSnapshot);
  calibrate(id, 1.5); expect(get(id).history.at(-1)).toMatchObject({ status: "已接受", confirmedHours: 1 });
  expect(get(id).reviewedBy).toBeUndefined();
});

test("historical submitted Human can be reviewed without inventing self-confirmation; Agent cannot", () => {
  reviewHumanEffort("WL-01", "accept", "核对历史提交事实", 1);
  expect(get("WL-01")).toMatchObject({ status: "已接受", hours: "1.5", reviewState: "accepted" });
  expect(get("WL-01").confirmedBy).toBeUndefined(); expect(get("WL-01").timezone).toBeUndefined();
  expect(get("WL-01").history.at(-1)).toMatchObject({ status: "已提交", hours: "1.5" });
  expect(() => reviewHumanEffort("WL-03", "accept", "错误审核", 1)).toThrow();
});
