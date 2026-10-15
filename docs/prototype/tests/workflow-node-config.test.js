import { test, expect } from "bun:test";
import { initial } from "../src/data.js";
import { workflowNodeLibrary, normalizeWorkflowNode, fieldsForWorkflowNode, workflowResourceOptions, validateWorkflowNodeConfig, workflowGraphForValidation, simulateWorkflowNode } from "../src/workflow-node-config.js";
const state = () => structuredClone(initial);
const node = (kind, data = {}) => normalizeWorkflowNode({ id: kind === "触发" ? "brief" : kind === "交付" ? "delivery" : "step", data: { kind, label: kind, owner: "林予", ...data } });
const graph = n => ({ nodes: [n], edges: [] });
const issues = (n, s = state()) => validateWorkflowNodeConfig(graph(n), s);
const keys = n => fieldsForWorkflowNode(n).map(field => field.key);
test("node panels expose kind-specific configuration and progressive trigger/wait choices", () => {
  for (const [kind] of workflowNodeLibrary) expect(fieldsForWorkflowNode(node(kind)).length).toBeGreaterThan(0);
  expect(keys(node("AI 任务"))).toContain("modelId");
  expect(keys(node("构建"))).toContain("command");
  expect(keys(node("构建"))).not.toContain("prompt");
  expect(keys(node("触发"))).not.toContain("cron");
  expect(keys(node("触发", { triggerMode: "定时" }))).toContain("cron");
  expect(keys(node("触发", { triggerMode: "Git 事件" }))).toContain("branchFilter");
  expect(keys(node("等待", { waitMode: "时长" }))).not.toContain("waitFor");
});
test("CI configurations require fixed provenance and meaningful execution/report/artifact fields", () => {
  const samples = [
    ["源码检出", { repositoryId: initial.resources.repositories[0].id, gitRef: "main", commit: "{{trigger.commit}}" }],
    ["构建", { environment: "node:22", command: "bun run build", artifactPath: "dist/" }],
    ["测试", { environment: "node:22", command: "bun test", reportPath: "test.xml", passCriteria: "all required tests pass" }],
    ["制品归档", { artifactPath: "dist/", artifactName: "web", artifactVersion: "{{checkout.commit}}", checksum: "{{build.sha256}}" }],
    ["部署 / 回滚", { environment: "staging", artifactRef: "web@v3", approver: "林予", command: "deploy web@v3", healthCheck: "GET /health is 200", rollbackRef: "web@v2" }],
  ];
  for (const [kind, data] of samples) {
    const configured = node(kind, data);
    expect(issues(configured)).toEqual([]);
    for (const descriptor of fieldsForWorkflowNode(configured).filter(field => field.required)) {
      expect(issues(node(kind, { ...data, [descriptor.key]: "" })).some(issue => issue.field === descriptor.key)).toBe(true);
    }
  }
  expect(issues(node("部署 / 回滚", { ...samples[4][1], deployMode: "回滚", rollbackRef: "" }))).toEqual([]);
});
test("Human, team, model, tools and repository references are checked against current available identities", () => {
  const s = state(), ai = node("AI 任务", { prompt: "review", modelId: "MD-01", skillId: "T-02", mcpId: "T-01", executorIds: ["H-lin", "agent:atlas"] });
  expect(issues(ai, s)).toEqual([]);
  for (const data of [{ owner: "unknown" }, { executorIds: ["agent:unknown"] }, { modelId: "MD-02" }, { mcpId: "T-03" }, { skillId: "T-01" }]) expect(issues(node("AI 任务", { ...ai.data, ...data }), s).length).toBeGreaterThan(0);
  s.agents.find(agent => agent.id === "atlas").status = "退役";
  expect(issues(ai, s).some(issue => issue.field === "executorIds")).toBe(true);
  const repo = s.resources.repositories[0]; repo.archivedAt = "2026-10-14";
  expect(workflowResourceOptions(s, "repositories").some(option => option.value === repo.id)).toBe(false);
  expect(issues(node("源码检出", { repositoryId: repo.id, gitRef: "main", commit: "abc" }), s).some(issue => issue.field === "repositoryId")).toBe(true);
});
test("credentials remain references and HTTP metadata rejects embedded credentials and malformed JSON", () => {
  const valid = { url: "https://example.invalid/api", credential: "vault/ci/api", headers: '{"Accept":"application/json"}' };
  expect(issues(node("HTTP 请求", valid))).toEqual([]);
  for (const change of [{ credential: "sk-live-secret" }, { url: "https://user:password@example.invalid" }, { url: "https://example.invalid?token=secret" }, { headers: '{"Authorization":"Bearer secret"}' }, { headers: "[]" }, { method: "POST", body: "not json" }, { expectedStatus: "999" }]) expect(issues(node("HTTP 请求", { ...valid, ...change })).length).toBeGreaterThan(0);
});
test("trigger schedule, fixed input mapping, wait and error policy fail closed on invalid configuration", () => {
  expect(issues(node("触发", { triggerMode: "定时", cron: "*/15 9-17 * * 1-5", timezone: "Asia/Singapore" }))).toEqual([]);
  for (const cron of ["* * *", "61 * * * *", "*/0 * * * *", "0 0 32 * *", "0 0 * 13 *"]) expect(issues(node("触发", { triggerMode: "定时", cron })).some(issue => issue.field === "cron")).toBe(true);
  expect(issues(node("触发", { triggerMode: "定时", cron: "0 0 * * *", timezone: "wrong" })).some(issue => issue.field === "timezone")).toBe(true);
  expect(issues(node("触发", { triggerMode: "Git 事件", branchFilter: "" })).some(issue => issue.field === "branchFilter")).toBe(true);
  expect(issues(node("等待", { waitMode: "时长", waitSeconds: "0" })).some(issue => issue.field === "waitSeconds")).toBe(true);
  expect(issues(node("触发", { inputs: "K-1", inputVersion: "" })).some(issue => issue.field === "inputVersion")).toBe(true);
  expect(issues(node("触发", { onError: "有限重试后暂停", retries: "0" })).some(issue => issue.field === "retries")).toBe(true);
});
test("new CI producers reuse the existing graph validator quality gate without mutating persisted kinds", async () => {
  globalThis.localStorage ??= { getItem: () => null, setItem: () => {}, removeItem: () => {} };
  const { validateWorkflow } = await import("../src/store.js");
  for (const kind of ["源码检出", "构建", "测试", "制品归档", "部署 / 回滚"]) {
    const original = { nodes: [node("触发"), { ...node("独立检查"), id: "check" }, { ...node("人工审批"), id: "approve" }, node(kind), node("交付")], edges: [{ source: "brief", target: "check" }, { source: "check", target: "approve" }, { source: "approve", target: "step" }, { source: "step", target: "delivery" }] };
    const snapshot = structuredClone(original);
    expect(validateWorkflow(original)).toEqual([]);
    expect(validateWorkflow(workflowGraphForValidation(original)).some(issue => issue.node === "delivery")).toBe(true);
    expect(original).toEqual(snapshot);
    original.edges = [{ source: "brief", target: "step" }, { source: "step", target: "check" }, { source: "check", target: "approve" }, { source: "approve", target: "delivery" }];
    expect(validateWorkflow(workflowGraphForValidation(original))).toEqual([]);
  }
});
test("simulation records immutable configuration snapshots and never fabricates artifacts or execution success", () => {
  const current = node("构建", { environment: "node:22", command: "bun run build", artifactPath: "dist/" }), g = graph(current);
  const before = structuredClone(g), record = simulateWorkflowNode(current, g, state(), "2026-10-14T00:00:00Z");
  expect(record.mode).toBe("LOCAL_SIMULATION"); expect(record.status).toContain("未执行"); expect(record.output.artifact).toBeNull(); expect(g).toEqual(before);
  current.data.command = "changed"; expect(record.configuration.command).toBe("bun run build");
  const bad = simulateWorkflowNode(node("构建"), graph(node("构建")), state()); expect(bad.status).toBe("配置未通过");
  const structural = simulateWorkflowNode(current, g, state(), "fixed", [{ node: current.id, message: "Missing upstream" }]); expect(structural.status).toBe("配置未通过");
});

test("groups and tab-specific metadata disclose only relevant fields without changing legacy defaults", async () => {
  const { groupsForWorkflowNode, workflowInputFields, workflowErrorFields } = await import("../src/workflow-node-config.js");
  for (const [kind] of workflowNodeLibrary) {
    const configured = node(kind), groups = groupsForWorkflowNode(configured);
    expect(groups.flatMap(group => group.fields)).toEqual(fieldsForWorkflowNode(configured));
    expect(groups.every(group => group.label && group.fields.every(field => field.group === group.label))).toBe(true);
  }
  expect(keys(node("触发", { triggerMode: "Webhook" }))).toContain("webhookCredential");
  expect(keys(node("触发", { triggerMode: "会话事件" }))).toContain("conversationEvent");
  expect(keys(node("触发"))).not.toContain("webhookCredential");
  expect(keys(node("部署 / 回滚"))).not.toContain("canaryPercent");
  expect(keys(node("部署 / 回滚", { deploymentStrategy: "金丝雀" }))).toContain("canaryPercent");
  expect(workflowInputFields.map(field => field.key)).toEqual(["inputSchema", "pinnedInput", "pinnedInputVersion"]);
  expect(workflowErrorFields.some(field => field.key === "unknownEffectPolicy")).toBe(true);
  expect(issues(node("AI 任务", { prompt: "legacy instruction" }))).toEqual([]);
});
test("new node types require their own contracts and resolve actual frozen child versions", () => {
  const published = initial.resources.templates.find(item => item.flow?.flowVersion > 0);
  const examples = [
    ["容器镜像", { imageName: "registry.invalid/project/image", imageTag: "commit-abc" }],
    ["安全扫描", { scanTarget: "image@sha256:abc", scannerRef: "scanner@v1", reportPath: "scan.json" }],
    ["通知", { notificationRecipients: "project-owner", notificationTemplate: "工作 {{work.id}} 等待确认" }],
    ["子工作流", { childTemplateId: published.id, childVersion: String(published.flow.flowVersion), childInputs: '{"source":"{{input.source}}"}', childOutputs: '{"artifact":"{{child.artifact}}"}' }],
  ];
  for (const [kind, data] of examples) {
    expect(issues(node(kind, data))).toEqual([]);
    for (const field of fieldsForWorkflowNode(node(kind, data)).filter(field => field.required)) expect(issues(node(kind, { ...data, [field.key]: "" })).some(issue => issue.field === field.key)).toBe(true);
  }
  expect(issues(node("子工作流", { ...examples[3][1], childVersion: "999" })).some(issue => issue.field === "childVersion")).toBe(true);
});
test("optional numeric enums JSON and credential maps reject malformed values, not legacy absence", () => {
  const ai = { prompt: "do work" };
  expect(issues(node("AI 任务", { ...ai, temperature: "0.5", maxOutputTokens: "2048", responseFormat: "JSON 对象" }))).toEqual([]);
  for (const temperature of ["NaN", "Infinity", "-1", "2.1", "1e2", " 1 ", 1, true]) expect(issues(node("AI 任务", { ...ai, temperature })).some(issue => issue.field === "temperature")).toBe(true);
  for (const maxOutputTokens of ["0", "1.5", "9007199254740992"]) expect(issues(node("AI 任务", { ...ai, maxOutputTokens })).some(issue => issue.field === "maxOutputTokens")).toBe(true);
  expect(issues(node("AI 任务", { ...ai, responseFormat: "unknown" })).some(issue => issue.field === "responseFormat")).toBe(true);
  const build = { environment: "node:22", command: "build", artifactPath: "dist/" };
  expect(issues(node("构建", { ...build, environmentVariables: '{"MODE":"production"}', environmentCredentials: '{"API_TOKEN":"vault/project/token"}' }))).toEqual([]);
  for (const environmentCredentials of ['{"API_TOKEN":"plain-secret"}', '{"bad key":"vault/key"}', '{"API_TOKEN":null}', '{"API_TOKEN":42}']) expect(issues(node("构建", { ...build, environmentCredentials })).some(issue => issue.field === "environmentCredentials")).toBe(true);
  for (const environmentVariables of ['{"API_TOKEN":"secret"}', '{"COUNT":3}', '[]', '{broken', {}]) expect(issues(node("构建", { ...build, environmentVariables })).some(issue => issue.field === "environmentVariables")).toBe(true);
});
test("pinned examples are versioned and checked against the explicit supported Schema subset", () => {
  const schema = JSON.stringify({ type: "object", properties: { commit: { type: "string" }, runs: { type: "array", items: { type: "integer" } } }, required: ["commit"], additionalProperties: false });
  const config = { inputSchema: schema, pinnedInput: '{"commit":"abc","runs":[1,2]}', pinnedInputVersion: "trigger@v1" };
  expect(issues(node("触发", config))).toEqual([]);
  expect(issues(node("触发", { ...config, pinnedInputVersion: "" })).some(issue => issue.field === "pinnedInputVersion")).toBe(true);
  for (const pinnedInput of ['{}', '{"commit":1}', '{"commit":"abc","runs":[1.5]}', '{"commit":"abc","secret":true}']) expect(issues(node("触发", { ...config, pinnedInput })).some(issue => issue.field === "pinnedInput")).toBe(true);
  for (const inputSchema of ['{"type":"object","$ref":"https://example.invalid/schema"}', '{"type":"object","required":["absent"]}', '{"type":"string","properties":{}}', '{"type":"object","additionalProperties":"false"}']) expect(issues(node("触发", { ...config, inputSchema })).some(issue => issue.field === "inputSchema")).toBe(true);
});
test("error and approval strategy definitions remain explicit and cannot imply execution or bypass unknown effects", () => {
  expect(issues(node("触发", { retrySafety: "按已核实的幂等结果处理" })).some(issue => issue.field === "idempotencyKey")).toBe(true);
  expect(issues(node("触发", { retrySafety: "禁止自动重试", onError: "有限重试后暂停" })).some(issue => issue.field === "retrySafety")).toBe(true);
  expect(issues(node("触发", { unknownEffectPolicy: "自动重放" })).some(issue => issue.field === "unknownEffectPolicy")).toBe(true);
  expect(issues(node("触发", { unknownEffectPolicy: "暂停并请求补偿确认" })).some(issue => issue.field === "compensationRef")).toBe(true);
  const approval = { approver: "H-lin", approvalBasis: "fixed package", approvalMode: "并行", approvalStrategy: "QUORUM", approvalQuorum: "2", approvalParticipants: '{"humanIds":["H-lin","H-zhou"]}' };
  expect(issues(node("人工审批", approval))).toEqual([]);
  expect(issues(node("人工审批", { ...approval, approvalQuorum: "3" })).some(issue => issue.field === "approvalQuorum")).toBe(true);
  for (const approvalParticipants of ['{"humanIds":["H-missing"]}', '{"humanIds":["H-lin","H-lin"]}', '{"humanIds":"H-lin"}', '{"votes":["approved"]}']) expect(issues(node("人工审批", { ...approval, approvalParticipants })).some(issue => issue.field === "approvalParticipants")).toBe(true);
  const current = node("人工审批", approval), simulated = simulateWorkflowNode(current, graph(current), state());
  expect(simulated.status).toContain("未执行"); expect(simulated).not.toHaveProperty("decision"); expect(simulated.output.artifact).toBeNull();
});
test("Human references use stable IDs and legacy names resolve only when unambiguous without mutating definitions", async () => {
  const { workflowHumanLabel, workflowHumanValue } = await import("../src/workflow-node-config.js");
  const s = state();
  expect(workflowResourceOptions(s, "humans").find(option => option.value === "H-lin")).toBeDefined();
  expect(workflowHumanValue(s, "林予")).toBe("H-lin"); expect(workflowHumanLabel(s, "H-lin")).toBe("林予");
  const legacy = node("人工审批", { approver: "林予", approvalBasis: "v1", receiver: "周宁" }), snapshot = structuredClone(legacy);
  expect(issues(legacy, s)).toEqual([]); expect(legacy).toEqual(snapshot);
  s.organization.members.push({ id: "H-duplicate", name: "林予", kind: "Human" });
  expect(workflowHumanValue(s, "林予")).toBe("林予"); expect(workflowHumanLabel(s, "林予")).toContain("身份待确认");
  expect(issues(legacy, s).map(issue => issue.field)).toEqual(expect.arrayContaining(["owner", "approver"]));
  expect(issues(node("人工审批", { ...legacy.data, owner: "H-lin", approver: "H-duplicate" }), s)).toEqual([]);
  expect(workflowHumanValue(s, "")).toBe(""); expect(workflowHumanLabel(s, "missing")).toContain("身份待确认");
  expect(issues(node("触发", { owner: "H-lin", receiver: "林予" }), s).some(issue => issue.field === "receiver")).toBe(true);
});

test("every node validates its declared output JSON and every new producer preserves the original quality gate", async () => {
  const { validateWorkflow } = await import("../src/store.js");
  for (const kind of ["触发", ...workflowNodeLibrary.map(row => row[0]), "交付"]) {
    expect(issues(node(kind, { outputSchema: "[]" })).some(issue => issue.field === "outputSchema")).toBe(true);
    expect(issues(node(kind, { outputSchema: '{"artifact":"string"}' })).some(issue => issue.field === "outputSchema")).toBe(false);
  }
  for (const kind of ["容器镜像", "安全扫描", "通知", "子工作流"]) {
    const graph = { nodes: [node("触发"), { ...node("独立检查"), id: "check" }, { ...node("人工审批"), id: "approval" }, node(kind), node("交付")], edges: [{ source: "brief", target: "check" }, { source: "check", target: "approval" }, { source: "approval", target: "step" }, { source: "step", target: "delivery" }] };
    expect(validateWorkflow(workflowGraphForValidation(graph)).some(issue => issue.node === "delivery")).toBe(true);
    expect(graph.nodes.find(item => item.id === "step").data.kind).toBe(kind);
  }
});

test("Schema rejects explicitly invalid null keywords and JSON numeric overflow", () => {
  for (const key of ["properties", "required", "additionalProperties", "enum"]) expect(issues(node("触发", { inputSchema: JSON.stringify({ type: "object", [key]: null }) })).some(issue => issue.field === "inputSchema")).toBe(true);
  expect(issues(node("触发", { inputSchema: '{"type":"array","items":null}' })).some(issue => issue.field === "inputSchema")).toBe(true);
  expect(issues(node("触发", { pinnedInput: '{"value":1e999}', pinnedInputVersion: "v1" })).some(issue => issue.field === "pinnedInput")).toBe(true);
});

test("workflow settings validate all policy choices and credential mappings without changing old definitions", async () => {
  const { validateWorkflowSettings, workflowSettingsDefaults } = await import("../src/workflow-node-config.js");
  const entry = node("触发");
  expect(validateWorkflowSettings(graph(entry))).toEqual([]);
  entry.data.workflowSettings = { ...workflowSettingsDefaults, variables:'{"project":"release"}', credentialRefs:'{"deploy":"vault/ci/deploy"}' };
  expect(validateWorkflowSettings(graph(entry))).toEqual([]);
  for (const [key, value] of Object.entries({concurrency:"0", timeout:"1.5", retentionDays:"-1", duplicatePolicy:"cancel unreviewed", variables:'{"nested":{"password":"secret"}}', credentialRefs:'{"deploy":"plain-secret"}', scenario:"unknown"})) {
    const bad = structuredClone(entry); bad.data.workflowSettings[key] = value;
    expect(validateWorkflowSettings(graph(bad)).some(issue => issue.field === `workflowSettings.${key}`)).toBe(true);
  }
});

test("published workflow settings freeze with each definition and never update existing work version references", async () => {
  const { publishProjectFlow, flowDefinition } = await import("../src/store.js");
  const s=state(); s.flowVersion=3; const oldVersion=s.flowVersion, oldWorkVersion=s.work[0].flowVersion;
  const draft={nodes:structuredClone(s.flowNodes),edges:structuredClone(s.flowEdges)};
  draft.nodes.find(n=>n.id==='brief').data.workflowSettings={name:"CI release",scenario:"Git CI/CD",variables:'{"tag":"v1"}'};
  publishProjectFlow(s,"launch",{...draft,baseVersion:s.flowVersion});
  const version=s.flowVersion;
  expect(flowDefinition(s,"launch",version).nodes[0].data.workflowSettings.name).toBe("CI release");
  draft.nodes[0].data.workflowSettings.name="changed";
  expect(flowDefinition(s,"launch",version).nodes[0].data.workflowSettings.name).toBe("CI release");
  publishProjectFlow(s,"launch",{...draft,baseVersion:s.flowVersion});
  expect(flowDefinition(s,"launch",version).nodes[0].data.workflowSettings.name).toBe("CI release");
  expect(flowDefinition(s,"launch",oldVersion).nodes[0].data.workflowSettings).toBeUndefined();
  expect(s.work[0].flowVersion).toBe(oldWorkVersion);
});

test("collaboration nodes share existing Human/team contracts and require fixed business references", async () => {
  const { validateWorkflowCollaboration } = await import("../src/workflow-node-config.js");
  const examples = [
    ["任务分派", { taskContractRef: "W-105", taskContractVersion: "v1", assignmentMode: "负责人执行", assignmentPlan: "负责人完成制作，独立检查节点负责核验" }],
    ["材料交接", { handoffPackageRef: "PKG-1", handoffPackageVersion: "v2", handoffEvidence: "REPORT-1@v2 / artifact@v2", receiver: "H-zhou", handoffAcceptance: "沿用固定包的一次 Human 接收（L3）" }],
    ["退回重做", { reworkTargetNode: "producer", reworkSourceRef: "W-105", reworkSourceVersion: "v2", reworkReason: "验收条款未满足", revalidationRequirements: "修复后针对新成果重跑必需检查", reworkAccounting: "沿用原工作轮次与上限，达到阈值由 Human 决定" }],
    ["需求变更", { changeRef: "CHANGE-1", changeVersion: "v1", changeImpact: "W-105 / plan@v2 → 待批新基线", changeConfirmer: "H-lin", changeSyncPlan: "确认后暂停受影响步骤、同步计划并重新验收" }],
  ];
  for (const [kind, data] of examples) {
    const current = node(kind, { ...data, owner: "H-lin" });
    const g = { nodes: [{ ...node("触发"), id: "producer" }, current], edges: [{ source: "producer", target: current.id }] };
    expect(validateWorkflowNodeConfig(g, state())).toEqual([]);
    expect(validateWorkflowCollaboration(g)).toEqual([]);
    for (const field of fieldsForWorkflowNode(current).filter(field => field.required)) expect(issues(node(kind, { ...data, [field.key]: "" })).some(issue => issue.field === field.key)).toBe(true);
    const before = structuredClone(g), beforeState = state();
    const record = simulateWorkflowNode(current, g, beforeState);
    expect(g).toEqual(before); expect(beforeState).toEqual(state());
    expect(record.mode).toBe("LOCAL_SIMULATION"); expect(record.output.artifact).toBeNull();
    expect(record).not.toHaveProperty("acceptance"); expect(record).not.toHaveProperty("decision");
  }
});
test("collaboration validation rejects missing teams and unrelated return targets without resetting counts", async () => {
  const { validateWorkflowCollaboration } = await import("../src/workflow-node-config.js");
  const assignment = node("任务分派", { assignmentMode: "按已选小团队分工", executorIds: [] });
  expect(validateWorkflowCollaboration(graph(assignment)).some(issue => issue.field === "executorIds")).toBe(true);
  assignment.data.executorIds = ["H-lin", "agent:atlas"];
  expect(validateWorkflowCollaboration(graph(assignment))).toEqual([]);
  const returning = node("退回重做", { reworkTargetNode: "producer", rework: { internal: 5, formalTotal: 3 } });
  const g = { nodes: [{ ...node("触发"), id: "producer" }, { ...node("AI 任务"), id: "middle" }, returning, { ...node("交付"), id: "downstream" }, { ...node("触发"), id: "unrelated" }], edges: [{ source: "producer", target: "middle" }, { source: "middle", target: returning.id }, { source: returning.id, target: "downstream" }] };
  expect(validateWorkflowCollaboration(g)).toEqual([]);
  const snapshot = structuredClone(g);
  for (const target of [returning.id, "downstream", "unrelated", "missing"]) {
    returning.data.reworkTargetNode = target;
    expect(validateWorkflowCollaboration(g).some(issue => issue.field === "reworkTargetNode")).toBe(true);
    expect(returning.data.rework).toEqual(snapshot.nodes.find(item => item.id === returning.id).data.rework);
  }
  expect(issues(node("材料交接", { receiver: "missing" })).some(issue => issue.field === "receiver")).toBe(true);
  expect(issues(node("需求变更", { changeConfirmer: "missing" })).some(issue => issue.field === "changeConfirmer")).toBe(true);
  expect(issues(node("退回重做", { reworkAccounting: "清零重试" })).some(issue => issue.field === "reworkAccounting")).toBe(true);
  expect(issues(node("材料交接", { handoffAcceptance: "自动接受" })).some(issue => issue.field === "handoffAcceptance")).toBe(true);
});
