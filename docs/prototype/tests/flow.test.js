import { test, expect } from "bun:test";
import { initial } from "../src/data.js";
// These checks use isolated state values; they do not replace shared test storage.
const { validateWorkflow, projectFlow, flowDefinition, publishProjectFlow, lockWorkFlow } =
  await import("../src/store.js");
const fresh = () => ({ ...structuredClone(initial), flowVersion: 3 });
const graph = () => ({ nodes: structuredClone(initial.flowNodes), edges: structuredClone(initial.flowEdges) });

test("every delivery path requires independent checking followed by Human approval", () => {
  expect(validateWorkflow(graph())).toEqual([]);
  for (const [source, target] of [["brief", "delivery"], ["build", "approval"], ["check", "delivery"]]) {
    const bypass = graph();
    bypass.edges.push({ id: `bypass-${source}`, source, target });
    expect(validateWorkflow(bypass).some((issue) => issue.message.includes("每条交付路径"))).toBe(true);
  }
  const reversed = graph();
  reversed.nodes.find((n) => n.id === "check").data.kind = "人工审批";
  reversed.nodes.find((n) => n.id === "approval").data.kind = "独立检查";
  expect(validateWorkflow(reversed).some((issue) => issue.message.includes("每条交付路径"))).toBe(true);
});

test("publication retains fixed definitions and existing work version references", () => {
  const state = fresh();
  const work = state.work.find((w) => w.id === "W-105");
  expect(lockWorkFlow(state, work)).toBe(3);
  const original = structuredClone(flowDefinition(state, "launch", 3));
  const next = graph();
  next.nodes.find((n) => n.id === "build").data.label = "Revised build";
  expect(publishProjectFlow(state, "launch", {...next,baseVersion:3})).toBe(4);
  expect(work.flowVersion).toBe(3);
  expect(flowDefinition(state, "launch", work.flowVersion)).toEqual(original);
  expect(flowDefinition(state, "launch", 4).nodes.find((n) => n.id === "build").data.label).toBe("Revised build");
  next.nodes[0].data.label = "Caller mutation";
  expect(flowDefinition(state, "launch", 4).nodes[0].data.label).not.toBe("Caller mutation");
  expect(publishProjectFlow(state, "launch", {...graph(),baseVersion:4})).toBe(5);
  expect(flowDefinition(state, "launch", 4).nodes.find((n) => n.id === "build").data.label).toBe("Revised build");
  expect(flowDefinition(state, "launch", 3)).toEqual(original);
  expect(flowDefinition(state, "launch", 99)).toBeNull();
  expect(flowDefinition(state, "launch", 0)).toBeNull();
});

test("project publications and work locks reject unpublished templates and invalid graphs", () => {
  const state = fresh();
  expect(() => lockWorkFlow(state, { project: "unpublished" })).toThrow("先发布");
  const originalLaunch = structuredClone(projectFlow(state, "launch"));
  const originalNovel = structuredClone(projectFlow(state, "novel"));
  const invalid = graph();
  invalid.edges.push({ source: "brief", target: "delivery" });
  expect(() => publishProjectFlow(state, "film", invalid)).toThrow("每条交付路径");
  expect(projectFlow(state, "film").flowVersion).toBe(1);
  publishProjectFlow(state, "film", {...graph(),baseVersion:1});
  expect(projectFlow(state, "film").flowVersion).toBe(2);
  expect(projectFlow(state, "novel")).toEqual(originalNovel);
  expect(state.flowNodes).toEqual(originalLaunch.flowNodes);
  expect(state.flowVersion).toBe(3);
});

// REQ-QA-002: producing new output after a gate invalidates that gate.
test("new output after checking or approval requires a fresh gate", () => {
  for (const kind of ["AI 任务", "工具调用", "HTTP 请求", "数据转换"]) {
    for (const upstream of ["check", "approval"]) {
      const g=graph();
      const next = upstream === "check" ? "approval" : "delivery";
      g.nodes.push({id:"late-output",data:{...g.nodes.find(n=>n.id==='build').data,kind,tool:"approved-tool",url:"https://example.invalid"}});
      g.edges=g.edges.filter(e=>!(e.source===upstream && e.target===next));
      g.edges.push({source:upstream,target:"late-output"},{source:"late-output",target:next});
      expect(validateWorkflow(g).some(issue=>issue.message.includes("每条交付路径"))).toBe(true);
    }
  }
});
