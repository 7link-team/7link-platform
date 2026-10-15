// Build actual local receipts, Plan/report bindings and model application results.
// Imported historical variants below are explicit UI tests, never verified deliveries.
import { spawnSync } from "node:child_process";
const original = spawnSync(process.execPath, ["tests/delivery-receipt-browser-fixture.js"], {
  encoding: "utf8", windowsHide: true,
});
if (original.status !== 0) throw Error(original.stderr || "Actual receipt fixture failed");
globalThis.localStorage = { getItem: () => null, setItem: () => {}, removeItem: () => {} };
const api = await import("../src/store.js");
api.update(state => Object.assign(state, JSON.parse(original.stdout)));
api.update(state => state.role = "owner");
const { prepareStructuredReport } = await import("./report-lifecycle-fixtures.js");
const reportId = api.generatePeriodicReport({ subjectKey: "agent:atlas", cadence: "周报", date: "2026-10-14", timezone: "UTC" });
prepareStructuredReport(api, reportId);
const modelApproval = api.requestModelApproval("MD-01", api.read().resources.models.find(model => model.id === "MD-01").revision || 1);
api.resourceCommand("approvals", modelApproval, "已批准", "Human 核对当前固定模型配置与下发范围");
api.resourceCommand("models", "MD-01", "生效", "本地展示测试：保留逐节点模拟结果");
const state = api.read();
state.testResourcePresentation = { deliveries: state.testDeliveryReceiptCases, reportId, modelId: "MD-01", testId: "TC-01" };
console.log(JSON.stringify(state));
