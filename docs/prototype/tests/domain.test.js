import { completeValidation } from "./qa-helpers.js";
import { test, expect, beforeEach } from "bun:test";
import { catalog, resourceFields } from "../src/data.js";
const entries = new Map();
Object.defineProperty(globalThis, "localStorage", {
  value: {
    getItem: (k) => entries.get(k) || null,
    setItem: (k, v) => entries.set(k, v),
    removeItem: (k) => entries.delete(k),
  },
  configurable: true,
});
const { requestModelApproval, log, publishTestReport, startTestBatch, recordBatchResult, read, reset, update, workCommand, resourceCommand, saveResource } =
  await import("../src/store.js");
const verifyWork = workId => completeValidation({ read, workCommand, recordBatchResult, publishTestReport, startTestBatch }, workId);
beforeEach(() => {
  reset();
  verifyWork("W-103"); // The seed's historical green badge is not a frozen current verification plan.
  workCommand("W-103", "rework-config", "明确专业返工阈值", 2, { revision: 0, internal: 5, formal: 3 });
});
test("audit events retain an absolute occurrence timestamp without fabricating dates for legacy entries", () => {
  const legacy = { id: "legacy", time: "09:08", text: "原记录" };
  const state = { role: "owner", events: [structuredClone(legacy)] };
  const before = Date.now();
  log(state, "记录可追溯事件", "/governance/audit");
  const after = Date.now();
  const persisted = JSON.parse(JSON.stringify(state));
  const recorded = persisted.events[0];
  expect(recorded.time).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/);
  expect(Date.parse(recorded.time)).toBeGreaterThanOrEqual(before);
  expect(Date.parse(recorded.time)).toBeLessThanOrEqual(after);
  expect(recorded.target).toBe("/governance/audit");
  expect(persisted.events[1]).toEqual(legacy);
});
test("a newly created work item submits without requiring a page reload", () => {
  update((s) =>
    s.work.unshift({
      id: "new-work",
      title: "New work",
      project: "launch",
      type: "软件",
      status: "待开始",
      owner: "林予",
      agent: "Atlas",
      version: 1,
      plan: false,
      flow: false,
      qa: false,
      accepted: false,
      delivered: false,
      notes: "Original brief",
      requirements: ["Accepted output"],
    }),
  );
  for (const command of ["plan", "flow", "start", "submit"])
    workCommand("new-work", command);
  const work = read().work.find((w) => w.id === "new-work");
  expect(work.status).toBe("待验收");
  expect(work.version).toBe(2);
  expect(work.history).toEqual([
    {
      version: 1,
      notes: "Original brief",
      requirements: ["Accepted output"],
      qa: false,
      accepted: false,
    },
  ]);
});
test("all resource editors restrict forms to configuration and preserve runtime receipts", () => {
  expect(Object.keys(resourceFields).sort()).toEqual(
    Object.keys(catalog).sort(),
  );
  const metadata = [
    "id",
    "status",
    "history",
    "revision",
    "nodeResults",
    "lastRun",
    "next",
    "sync",
    "event",
    "last",
    "targetRevision",
  ];
  expect(resourceFields.testreports).toEqual([]); // Immutable reports have no editor.
  for (const [kind, fields] of Object.entries(resourceFields)) {
    if (kind === "testreports") continue;
    expect(fields).toContain("name");
    expect(fields).toContain("owner");
    for (const field of metadata) expect(fields).not.toContain(field);
  }
  resourceCommand("approvals", requestModelApproval("MD-02", 1), "已批准", "Approved");
  resourceCommand("models", "MD-02", "生效", "Apply");
  const model = read().resources.models.find((m) => m.id === "MD-02");
  const receipt = structuredClone(model.nodeResults);
  saveResource("models", { ...model, model: "revised-model" });
  expect(
    read().resources.models.find((m) => m.id === "MD-02").nodeResults,
  ).toEqual(receipt);
});
test("connector history scope and audit history remain separate for every connector", () => {
  for (const connector of [...read().resources.connectors]) {
    resourceCommand(
      "connectors",
      connector.id,
      "已连接",
      "Connect",
      "模拟授权连接",
    );
    const connected = read().resources.connectors.find(
      (c) => c.id === connector.id,
    );
    saveResource("connectors", {
      ...connected,
      name: `${connected.name} updated`,
    });
    const saved = read().resources.connectors.find(
      (c) => c.id === connector.id,
    );
    expect(saved.historyScope).toBe(connector.historyScope);
    expect(saved.history).toHaveLength(2);
    expect(saved.history[0].reason).toBe("Connect");
  }
});
test("legacy connector history text migrates without losing its sharing meaning", async () => {
  const saved = structuredClone(read());
  for (const connector of saved.resources.connectors) {
    connector.history = connector.historyScope;
    delete connector.historyScope;
  }
  saved.tenants.push({
    id: "legacy-tenant",
    name: "Legacy tenant",
    role: "owner",
  });
  saved.tenantData["legacy-tenant"] = {
    resources: structuredClone(saved.resources),
  };
  localStorage.setItem("collaboration-prototype-v1", JSON.stringify(saved));
  const legacy = await import("../src/store.js?legacy-connector-history");
  for (const connector of legacy.read().resources.connectors) {
    expect(connector.historyScope).toBe(
      saved.resources.connectors.find((c) => c.id === connector.id).history,
    );
    expect(connector.history).toEqual([]);
    legacy.resourceCommand(
      "connectors",
      connector.id,
      "已连接",
      "Migrated connection",
    );
  }
  legacy.switchTenant("legacy-tenant");
  for (const connector of legacy.read().resources.connectors) {
    expect(connector.historyScope).toBe(
      saved.resources.connectors.find((c) => c.id === connector.id).history,
    );
    expect(connector.history).toEqual([]);
    legacy.resourceCommand(
      "connectors",
      connector.id,
      "已连接",
      "Migrated tenant connection",
    );
  }
});
test("a Human cannot deliver or accept a failed internal gate", () => {
  expect(() => workCommand("W-104", "accept", "", 3)).toThrow("内部检查");
  expect(() => workCommand("W-104", "deliver", "", 3)).toThrow("内部检查");
  expect(read().resources.deliveries.length).toBe(0);
});
test("recheck, acceptance and delivery form one traceable fixed-version package", () => {
  workCommand("W-104", "contract", "", 3, { mode: "external", recipient: "验收接收人" });
  verifyWork("W-104");
  workCommand("W-104", "accept", "", 3);
  workCommand("W-104", "deliver", "", 3);
  expect(read().work.find((w) => w.id === "W-104").status).toBe("待交接");
  expect(read().work.find((w) => w.id === "W-104").l4.status).toBe("待接收");
  expect(read().resources.deliveries[0].version).toBe("v3");
  expect(read().resources.tests[0].work).toBe("W-104");
  expect(() => workCommand("W-104", "deliver", "", 3)).toThrow();
  expect(read().resources.deliveries.length).toBe(1);
});
test("rework and a new artifact invalidate the old accepted gate", () => {
  workCommand("W-103", "accept", "", 2);
  workCommand("W-103", "return", "阴影过重", 2);
  workCommand("W-103", "submit", "", 2);
  const w = read().work.find((w) => w.id === "W-103");
  expect(w.version).toBe(3);
  expect(w.qa).toBe(false);
  expect(w.accepted).toBe(false);
  expect(w.history.some((v) => v.version === 2)).toBe(true);
  expect(() => workCommand("W-103", "accept", "", 2)).toThrow("版本");
});
test("unconfirmed plan or SOP never starts execution", () => {
  expect(() => workCommand("W-105", "start")).toThrow("计划");
  workCommand("W-105", "plan");
  expect(() => workCommand("W-105", "start")).toThrow("SOP");
  workCommand("W-105", "flow");
  workCommand("W-105", "start");
  expect(read().work.find((w) => w.id === "W-105").status).toBe("进行中");
});
test("cancelled work is terminal and rejected transitions leave no mutation", () => {
  workCommand("W-104", "cancel", "需求取消", 3);
  const count = read().events.length;
  expect(() => workCommand("W-104", "check")).toThrow("取消");
  expect(read().events.length).toBe(count);
});
test("offline and external views cannot mutate internal work", () => {
  update((s) => (s.offline = true));
  expect(() => workCommand("W-103", "accept")).toThrow("离线");
  update((s) => {
    s.offline = false;
    s.role = "external";
  });
  expect(() => workCommand("W-103", "accept")).toThrow("外部客户");
});
test("executor cannot grant final Human authorization", () => {
  update((s) => (s.role = "executor"));
  expect(() => workCommand("W-103", "accept")).toThrow("权限");
  expect(() => resourceCommand("approvals", "AP-01", "已批准", "同意")).toThrow(
    "权限",
  );
});
test("editing a model invalidates its old approval target revision", () => {
  const requestId = requestModelApproval("MD-02", 1);
  const model = {
    ...read().resources.models.find((m) => m.id === "MD-02"),
    model: "changed",
  };
  saveResource("models", model);
  expect(() => resourceCommand("approvals", requestId, "已批准", "同意")).toThrow(
    "目标已变更",
  );
});
test("partial configuration rollout does not falsely report every node applied", () => {
  resourceCommand("approvals", requestModelApproval("MD-02", 1), "已批准", "限定范围");
  resourceCommand("models", "MD-02", "生效", "下发");
  const m = read().resources.models.find((m) => m.id === "MD-02");
  expect(m.status).toBe("部分生效");
  expect(m.nodeResults.some((n) => n.status === "待应用")).toBe(true);
});
test("scheduled report generation produces a draft without Human submission", () => {
  resourceCommand("schedules", "SCH-01", "启用", "手动检查", "模拟运行一次");
  expect(read().resources.reports[0].status).toBe("草稿");
  expect(read().resources.reports[0].source).toBe("SCH-01");
});
test("external rejection retains delivered package and starts a traceable rework", () => {
  workCommand("W-103", "accept");
  workCommand("W-103", "deliver");
  const d = read().resources.deliveries[0];
  update((s) => (s.role = "external"));
  resourceCommand("deliveries", d.id, "已拒收", "颜色不符合约定");
  expect(read().resources.deliveries[0].version).toBe("v2");
  expect(read().resources.deliveries[0].history.length).toBe(1);
  expect(read().work.find((w) => w.id === "W-103").status).toBe("进行中");
  expect(read().events[0].actor).toBe("陈曦");
});

test("rejected packages retain their shared version without exposing new revisions", async () => {
  workCommand("W-104", "contract", "", 3, { mode: "external", recipient: "验收接收人" });
  const { sharedVersions, sharedArtifactPath } =
    await import("../src/store.js");
  verifyWork("W-104");
  workCommand("W-104", "accept", "", 3);
  workCommand("W-104", "deliver", "", 3);
  workCommand("W-104", "return", "需要修订", 3);
  workCommand("W-104", "submit", "", 3);
  expect(sharedVersions(read(), "W-104")).toEqual([3]);
  expect(
    sharedArtifactPath(
      read(),
      read().work.find((w) => w.id === "W-104"),
    ),
  ).toContain("version=3");
  expect(read().work.find((w) => w.id === "W-104").version).toBe(4);
});
test("project SOP drafts and publications never change another project", async () => {
  const { projectFlow, mutableProjectFlow } = await import("../src/store.js");
  update((s) => {
    const f = mutableProjectFlow(s, "film");
    f.flowVersion = 1;
    f.flowNodes[0].data.label = "电影流程";
  });
  expect(projectFlow(read(), "film").flowVersion).toBe(1);
  expect(projectFlow(read(), "novel").flowVersion).toBe(1);
  expect(projectFlow(read(), "launch").flowVersion).toBe(3);
  expect(projectFlow(read(), "launch").flowNodes[0].data.label).not.toBe(
    "电影流程",
  );
});

test("tenant creation and switching isolate projects, messages and domain records", async () => {
  const { createTenant, switchTenant, joinTenant } =
    await import("../src/store.js");
  const original = read().work.length;
  const created = createTenant("空白工作室");
  expect(read().activeTenant).toBe(created);
  expect(read().projects).toEqual([]);
  expect(read().work).toEqual([]);
  expect(read().resources.deliveries).toEqual([]);
  update((s) => s.messages.push({ id: "private-tenant", text: "属于新租户" }));
  switchTenant("studio");
  expect(read().work.length).toBe(original);
  expect(read().messages.some((m) => m.id === "private-tenant")).toBe(false);
  switchTenant(created);
  expect(read().messages.some((m) => m.id === "private-tenant")).toBe(true);
  expect(() => joinTenant("expired")).toThrow("无效");
  joinTenant("DEMO-JOIN");
  expect(read().role).toBe("executor");
  expect(read().work).toEqual([]);
  expect(() => switchTenant("unauthorized")).toThrow("已加入");
});

test("calendar schedule validates real dates, versions and preserves history", async () => {
  const { scheduleWork } = await import("../src/store.js");
  expect(() =>
    scheduleWork("W-104", "2026-02-30", "2026-03-01", "测试", 0),
  ).toThrow("有效");
  expect(() =>
    scheduleWork("W-104", "2026-10-18", "2026-10-12", "测试", 0),
  ).toThrow("早于");
  scheduleWork("W-104", "2026-10-14", "2026-10-18", "等待上游", 0);
  const w = read().work.find((w) => w.id === "W-104");
  expect(w.plannedEnd).toBe("2026-10-18");
  expect(w.scheduleHistory[0].end).toBe("2026-10-16");
  expect(() => scheduleWork("W-104", "", "", "撤销排期", 0)).toThrow("变化");
  scheduleWork("W-104", "", "", "撤销排期", 1);
  expect(read().work.find((w) => w.id === "W-104").plannedStart).toBe("");
});

test("workflow validation rejects missing contracts, cycles and incomplete branches", async () => {
  const { validateWorkflow } = await import("../src/store.js");
  const node = (id, kind = "AI 任务") => ({
    id,
    data: {
      label: id,
      kind,
      owner: "林予",
      output: "固定产物",
      gate: "内部检查通过",
      prompt: "完成任务",
      timeout: "300",
      retries: "2",
    },
  });
  const graph = {
    nodes: [
      node("brief", "触发"),
      node("work"),
      node("check", "独立检查"),
      node("approval", "人工审批"),
      node("delivery", "交付"),
    ],
    edges: [
      { source: "brief", target: "work" },
      { source: "work", target: "check" },
      { source: "check", target: "approval" },
      { source: "approval", target: "delivery" },
    ],
  };
  expect(validateWorkflow(graph)).toEqual([]);
  graph.nodes[1].data.owner = "";
  expect(
    validateWorkflow(graph).some((i) => i.message.includes("负责人")),
  ).toBe(true);
  graph.nodes[1].data.owner = "林予";
  graph.edges.push({ source: "work", target: "brief" });
  expect(validateWorkflow(graph).some((i) => i.message.includes("循环"))).toBe(
    true,
  );
  graph.edges.pop();
  graph.nodes[1].data.kind = "条件分支";
  expect(validateWorkflow(graph).some((i) => i.message.includes("分支"))).toBe(
    true,
  );
});

// REQ-QA-002: all current required checks, not a stale work.qa cache, guard delivery.
test("accepted evidence requires formal rework before new failed checks can block delivery", () => {
  workCommand("W-103", "accept");
  const accepted = structuredClone(read());
  expect(() => resourceCommand("tests", "TC-02", "失败", "独立检查失败")).toThrow("当前版本已接受");
  expect(read()).toEqual(accepted);
  workCommand("W-103", "return", "新问题需正式修订", 2);
  workCommand("W-103", "submit");
  workCommand("W-103", "check");
  const current = read().resources.tests.find(test => test.work === "W-103" && test.version === "v3");
  resourceCommand("tests", current.id, "失败", "新版本独立检查失败");
  const w = read().work.find((w) => w.id === "W-103");
  expect(w.qa).toBe(false); expect(w.accepted).toBe(false); expect(w.status).toBe("待验收");
  expect(() => workCommand(w.id, "deliver")).toThrow("内部检查");
  verifyWork("W-103");
  expect(read().work.find((w) => w.id === "W-104").qa).toBe(false);
  expect(read().work.find((w) => w.id === "W-103").qa).toBe(true);
  workCommand("W-103", "accept"); workCommand("W-103", "deliver");
  expect(read().resources.deliveries[0].version).toBe("v3");
});
test("every nonpassing or evidence-free current check blocks a stale accepted flag", () => {
  for (const [status, evidence] of [
    ["失败", "run"],
    ["待执行", "run"],
    ["INCONCLUSIVE", "run"],
    ["通过", ""],
  ]) {
    reset();
    update((s) => {
      s.work.find((w) => w.id === "W-103").accepted = true;
      Object.assign(
        s.resources.tests.find((t) => t.id === "TC-02"),
        { status, evidence },
      );
    });
    const before = structuredClone(read());
    expect(() => workCommand("W-103", "accept")).toThrow("内部检查");
    expect(() => workCommand("W-103", "deliver")).toThrow("内部检查");
    expect(read()).toEqual(before);
  }
});
test("rechecks retain failed evidence and new versions are unexecuted", async () => {
  const { qualityResult } = await import("../src/store.js");
  verifyWork("W-104");
  const old = read().resources.tests.find((t) => t.id === "TC-01");
  expect(old.status).toBe("通过");
  expect(
    old.runs.some(
      (h) => h.status === "失败" && h.evidence === "run-104 / mobile-375",
    ),
  ).toBe(true);
  workCommand("W-104", "return", "重新制作");
  workCommand("W-104", "submit");
  expect(
    qualityResult(
      read(),
      read().work.find((w) => w.id === "W-104"),
    ),
  ).toBe("未执行");
  resourceCommand("tests", "TC-01", "通过", "旧版本重验");
  expect(read().work.find((w) => w.id === "W-104").qa).toBe(false);
});
test("executor cannot approve goals or tool authorization regardless of status label", () => {
  update((s) => (s.role = "executor"));
  for (const [kind, target, status] of [
    ["goals", "G-02", "进行中"],
    ["tools", "any", "已授权"],
  ]) {
    const before = structuredClone(read());
    expect(() => resourceCommand(kind, target, status, "批准")).toThrow("权限");
    expect(read()).toEqual(before);
  }
});

test("missing current checks cannot be replaced by a cached quality flag", () => {
  update(s => { s.resources.tests = []; s.work.find(w=>w.id==='W-103').qa=true; });
  expect(()=>workCommand('W-103','accept')).toThrow('内部检查');
});
test("legacy work without a recorded workflow version must reconfirm before start", () => {
  update(s => { const w=s.work.find(w=>w.id==='W-105'); w.plan=true; w.flow=true; delete w.flowVersion; });
  expect(()=>workCommand('W-105','start')).toThrow('锁定');
  workCommand('W-105','flow');
  workCommand('W-105','start');
  expect(read().work.find(w=>w.id==='W-105').flowVersion).toBe(3);
});

test("quality display distinguishes failed, pending, unexecuted and passed evidence", async () => {
  const {qualityResult}=await import('../src/store.js');
  for (const [status,evidence,expected] of [['失败','run','失败'],['待执行','run','待验证'],['通过','','待验证'],['通过','run','通过']]) {
    reset();
    verifyWork("W-103");
    update(s=>Object.assign(s.resources.tests.find(t=>t.id==='TC-02'),{status,evidence}));
    expect(qualityResult(read(),read().work.find(w=>w.id==='W-103'))).toBe(expected);
  }
  update(s=>s.resources.tests=[]);
  expect(qualityResult(read(),read().work.find(w=>w.id==='W-103'))).toBe('待验证'); // A frozen required case was removed.
});

test("partial receipts preserve fixed items, accepted decisions and structured rework", () => {
  workCommand("W-103", "accept"); workCommand("W-103", "deliver");
  const delivery = read().resources.deliveries[0];
  const fixed = structuredClone(delivery.items.map(({ status, ...item }) => item));
  const items = delivery.items.map((item, index) => ({ id: item.id, status: index === 0 ? "已接受" : "已拒收", feedback: index === 0 ? undefined : {
    requirement: delivery.requirements[0], expected: "完整检查记录", actual: "记录缺少定位", position: "检查报告第一条",
  } }));
  update((s) => s.role = "external");
  const receipt = { version: delivery.version, items };
  resourceCommand("deliveries", delivery.id, "部分接受", "保留成果", "", receipt);
  const result = read().resources.deliveries[0];
  expect(result.status).toBe("部分接受");
  expect(result.items[0].status).toBe("已接受");
  expect(result.items[1].feedback.expected).toBe("完整检查记录");
  expect(result.items.map(({ status, feedback, ...item }) => item)).toEqual(fixed);
  expect(read().work.find((w) => w.id === delivery.work).returnFeedback[0]).toMatchObject({ delivery: delivery.id, item: items[1].id, version: "v2", requirement: delivery.requirements[0] });
  const eventCount = read().events.length;
  resourceCommand("deliveries", delivery.id, "部分接受", "保留成果", "", receipt);
  expect(read().events.length).toBe(eventCount);
  expect(read().work.find((w) => w.id === delivery.work).returnFeedback).toHaveLength(receipt.items.filter(item => item.status === "已拒收").length);
  expect(() => resourceCommand("deliveries", delivery.id, "已拒收", "推翻已接受项")).toThrow("已接受项");
});
test("receipt validation rejects stale, mismatched, incomplete or unstructured item decisions", () => {
  workCommand("W-103", "accept"); workCommand("W-103", "deliver");
  const delivery = read().resources.deliveries[0], before = structuredClone(read());
  const accepts = delivery.items.map((item) => ({ id: item.id, status: "已接受" }));
  for (const [status, receipt] of [
    ["部分接受", undefined], ["已接受", { version: "v1", items: accepts }],
    ["已接受", { version: delivery.version, items: [] }],
    ["部分接受", { version: delivery.version, items: accepts }],
    ["已拒收", { version: delivery.version, items: accepts.map((item) => ({ ...item, status: "已拒收" })) }],
  ]) expect(() => resourceCommand("deliveries", delivery.id, status, "理由", "", receipt)).toThrow();
  expect(read()).toEqual(before);
  expect(() => saveResource("deliveries", { ...delivery, version: "v999" })).toThrow("固定交付包");
});
test("late rejection of an old delivery never resets a newer work version", () => {
  workCommand("W-103", "accept"); workCommand("W-103", "deliver");
  const delivery = read().resources.deliveries[0];
  workCommand("W-103", "return", "新版修订", 2); workCommand("W-103", "submit", "", 2);
  verifyWork("W-103"); workCommand("W-103", "accept", "", 3);
  const newer = structuredClone(read().work.find((w) => w.id === "W-103"));
  resourceCommand("deliveries", delivery.id, "已拒收", "旧版不接受");
  expect(read().work.find((w) => w.id === "W-103")).toEqual(newer);
  expect(read().resources.deliveries[0].items.every((item) => item.status === "已拒收")).toBe(true);
  expect(read().resources.deliveries[0].items[0].feedback.structured).toBe(false);
});
test("structured work return pins requirement and version and repeated decisions are idempotent", () => {
  const work = read().work.find((w) => w.id === "W-103");
  const feedback = { requirement: work.requirements[0], expected: "浅色", actual: "深色", position: "主视觉背景" };
  expect(() => workCommand(work.id, "return", "修改", 2, { ...feedback, expected: "" })).toThrow("期望");
  expect(() => workCommand(work.id, "return", "修改", 2, { ...feedback, requirement: "不存在" })).toThrow("条款");
  workCommand(work.id, "return", "修改", 2, feedback);
  expect(read().work.find((w) => w.id === work.id).returnFeedback[0]).toMatchObject({ ...feedback, version: 2, structured: true });
  const count = read().events.length; workCommand(work.id, "return", "修改", 2, feedback);
  expect(read().events.length).toBe(count);
});
