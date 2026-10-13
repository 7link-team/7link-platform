import { test, expect, beforeEach } from "bun:test";
const entries = new Map();
Object.defineProperty(globalThis, "localStorage", {
  value: {
    getItem: (k) => entries.get(k) || null,
    setItem: (k, v) => entries.set(k, v),
    removeItem: (k) => entries.delete(k),
  },
  configurable: true,
});
const { read, reset, update, workCommand, resourceCommand, saveResource } =
  await import("../src/store.js");
beforeEach(() => reset());
test("a Human cannot deliver or accept a failed internal gate", () => {
  expect(() => workCommand("W-104", "accept", "", 3)).toThrow("内部检查");
  expect(() => workCommand("W-104", "deliver", "", 3)).toThrow("内部检查");
  expect(read().resources.deliveries.length).toBe(0);
});
test("recheck, acceptance and delivery form one traceable fixed-version package", () => {
  workCommand("W-104", "check", "", 3);
  workCommand("W-104", "accept", "", 3);
  workCommand("W-104", "deliver", "", 3);
  expect(read().work.find((w) => w.id === "W-104").status).toBe("已完成");
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
  const model = {
    ...read().resources.models.find((m) => m.id === "MD-02"),
    model: "changed",
  };
  saveResource("models", model);
  expect(() => resourceCommand("approvals", "AP-01", "已批准", "同意")).toThrow(
    "目标已变更",
  );
});
test("partial configuration rollout does not falsely report every node applied", () => {
  resourceCommand("approvals", "AP-01", "已批准", "限定范围");
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
  const { sharedVersions, sharedArtifactPath } =
    await import("../src/store.js");
  workCommand("W-104", "check", "", 3);
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
  expect(projectFlow(read(), "novel").flowVersion).toBe(0);
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
    nodes: [node("brief", "触发"), node("work"), node("delivery", "交付")],
    edges: [
      { source: "brief", target: "work" },
      { source: "work", target: "delivery" },
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
