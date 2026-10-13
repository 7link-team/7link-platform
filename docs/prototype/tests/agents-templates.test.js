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
  read,
  reset,
  update,
  saveAgent,
  agentMessages,
  resourceCommand,
  saveResource,
  createWorkflowTemplate,
  draftWorkflowTemplate,
  templateFlow,
  templateDefinition,
  publishWorkflowTemplate,
} = await import("../src/store.js");
beforeEach(() => reset());
const config = () => ({
  name: "测试研究员",
  role: "研究",
  responsibilities: "核对来源，不作最终批准",
  systemPrompt: "长期职责",
  taskTemplate: "研究 {{task}}；输出 {{task}}",
  runtime: "Codex CLI",
  modelId: "MD-01",
  owner: "林予",
  skillIds: ["T-02"],
  mcpIds: ["T-03"],
});
const agent = (id) => read().agents.find((item) => item.id === id);
const approve = (id) =>
  resourceCommand("approvals", id, "已批准", "已审核配置边界");

test("Agent stores system and task template separately, references tools without granting access", () => {
  const tools = structuredClone(read().resources.tools),
    id = saveAgent(config());
  expect(agent(id)).toMatchObject({
    status: "待批准",
    revision: 1,
    modelId: "MD-01",
    skillIds: ["T-02"],
    mcpIds: ["T-03"],
  });
  expect(agentMessages(agent(id), "本次输入")).toEqual([
    { role: "system", content: "长期职责" },
    { role: "user", content: "研究 本次输入；输出 本次输入" },
  ]);
  expect(agent(id).taskTemplate).toBe(config().taskTemplate);
  expect(read().resources.tools).toEqual(tools);
  approve(read().resources.approvals[0].id);
  expect(agent(id).approvedRevision).toBe(1);
  expect(agent(id).status).toBe("空闲");
});
test("old approval cannot approve new Agent config, history stays fixed", () => {
  const id = saveAgent(config()),
    old = read().resources.approvals[0].id;
  saveAgent({ ...config(), systemPrompt: "新长期职责" }, id, 1);
  expect(() => approve(old)).toThrow("审批目标已变更");
  expect(agent(id).history[0].systemPrompt).toBe("长期职责");
  approve(read().resources.approvals[0].id);
  expect(agent(id).approvedRevision).toBe(2);
  expect(agent(id).history[0].systemPrompt).toBe("长期职责");
});
test("Agent validation rejects missing task injection, wrong protocols and invalid tools", () => {
  for (const invalid of [
    { taskTemplate: "固定文本" },
    { runtime: "Claude Code" },
    { mcpIds: ["T-02"] },
    { skillIds: ["missing"] },
    { responsibilities: "" },
  ])
    expect(() => saveAgent({ ...config(), ...invalid })).toThrow();
  const id = saveAgent(config());
  update((s) => (s.resources.models[0].revision = 2));
  expect(() => approve(read().resources.approvals[0].id)).toThrow(
    "模型配置已变更",
  );
  expect(agent(id).status).toBe("待批准");
});
test("template AI drafts are explicit simulations; manual edits and published history isolated", () => {
  const id = createWorkflowTemplate("研究流程"),
    project = structuredClone(read().flowNodes);
  draftWorkflowTemplate(id, "可复核研究报告");
  expect(templateFlow(read(), id).flowDraft.source).toBe("AI 模拟草案");
  expect(() => draftWorkflowTemplate(id, "覆盖手改")).toThrow("已有草案");
  update((s) => {
    const flow = templateFlow(s, id);
    flow.flowDraft.nodes.find((n) => n.id === "build").data.label =
      "人工修订节点";
    publishWorkflowTemplate(s, id, flow.flowDraft);
  });
  const fixed = structuredClone(templateDefinition(read(), id, 1));
  expect(fixed.nodes.find((n) => n.id === "build").data.label).toBe(
    "人工修订节点",
  );
  draftWorkflowTemplate(id, "新的模拟目标");
  update((s) => publishWorkflowTemplate(s, id, templateFlow(s, id).flowDraft));
  expect(templateDefinition(read(), id, 1)).toEqual(fixed);
  expect(
    templateDefinition(read(), id, 2).nodes.find((n) => n.id === "build").data
      .prompt,
  ).toContain("新的模拟目标");
  expect(read().flowNodes).toEqual(project);
  expect(templateFlow(read(), "SOP-01").flowVersion).toBe(3);
  expect(templateDefinition(read(), id, 99)).toBeNull();
});
test("template publication validates graph, rejects generic bypass and enforces owner role", () => {
  const id = createWorkflowTemplate("模板");
  draftWorkflowTemplate(id, "目标");
  const invalid = structuredClone(templateFlow(read(), id).flowDraft);
  invalid.edges.push({ source: "brief", target: "delivery" });
  expect(() => update((s) => publishWorkflowTemplate(s, id, invalid))).toThrow(
    "每条交付路径",
  );
  expect(templateFlow(read(), id).flowVersion).toBe(0);
  expect(() => resourceCommand("templates", id, "已发布", "略过检查")).toThrow(
    "画布",
  );
  expect(() => saveResource("templates", { id, name: "改写" })).toThrow("画布");
  update((s) => (s.role = "executor"));
  expect(() =>
    update((s) =>
      publishWorkflowTemplate(s, id, templateFlow(s, id).flowDraft),
    ),
  ).toThrow("负责人");
  update((s) => (s.offline = true));
  expect(() => draftWorkflowTemplate(id, "离线")).toThrow("离线");
});

test("Agent owner must be a current-tenant Human and stale edits reject atomically", () => {
  const original = structuredClone(read());
  for (const owner of ["外部陌生人", "Atlas"]) {
    expect(() => saveAgent({ ...config(), owner })).toThrow("当前租户");
    expect(read()).toEqual(original);
  }
  const id = saveAgent(config());
  saveAgent({ ...config(), responsibilities: "已确认新职责" }, id, 1);
  const latest = structuredClone(read());
  expect(() =>
    saveAgent({ ...config(), systemPrompt: "旧弹窗覆盖" }, id, 1),
  ).toThrow("重新打开");
  expect(read()).toEqual(latest);
  expect(() => saveAgent(config(), id)).toThrow("重新打开");
  expect(read()).toEqual(latest);
  update((s) => {
    s.organization.members = s.organization.members.filter(
      (member) => member.name !== "林予",
    );
  });
  const noOwner = structuredClone(read());
  expect(() => saveAgent(config(), id, 2)).toThrow("当前租户");
  expect(read()).toEqual(noOwner);
});
