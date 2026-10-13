// Run in a fresh owner-role browser session. All AI and runtime operations are local simulations.
(async () => {
  const wait = () => new Promise((resolve) => setTimeout(resolve, 180));
  const check = (value, message) => {
    if (!value) throw Error(message);
  };
  const state = () =>
    JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
  const go = async (path) => {
    location.hash = path;
    await wait();
  };
  const click = async (label, root = document) => {
    const node = [...root.querySelectorAll("button")].find(
      (node) => node.textContent.trim() === label,
    );
    check(node, `Missing ${label}`);
    node.click();
    await wait();
  };
  const fill = async (label, value) => {
    const input = document.querySelector(`[name="${label}"]`);
    check(input, `Missing field ${label}`);
    Object.getOwnPropertyDescriptor(
      input.tagName === "TEXTAREA"
        ? HTMLTextAreaElement.prototype
        : HTMLInputElement.prototype,
      "value",
    ).set.call(input, value);
    input.dispatchEvent(new Event("input", { bubbles: true }));
    await wait();
  };
  const select = async (label, text) => {
    const input = document.querySelector(`[aria-label="${label}"]`);
    check(input, `Missing select ${label}`);
    input.click();
    await wait();
    const option = [...document.querySelectorAll('[role="option"]')].find(
      (node) => node.textContent.includes(text),
    );
    check(option, `Missing option ${text}`);
    option.click();
    await wait();
  };
  await go("/agents");
  await click("创建 Agent 提案");
  for (const [label, value] of [
    ["Agent 名称", "浏览器研究员"],
    ["角色", "研究"],
    ["职责与边界", "核对来源并提供证据，不负责最终批准"],
    ["System prompt（长期指令）", "始终核对来源，不编造证据"],
    ["User 任务模板（包含 {{task}}）", "研究任务：{{task}}"],
  ])
    await fill(label, value);
  await select("模型配置引用", "团队文本模型");
  const tools = JSON.stringify(state()?.resources.tools);
  for (const box of document.querySelectorAll('dialog input[type="checkbox"]'))
    box.click();
  await wait();
  document.querySelector("dialog details summary").click();
  await wait();
  check(
    document
      .querySelector("dialog")
      .textContent.includes("研究任务：请完成本次已确认任务并提供证据。"),
    "Task substitution absent",
  );
  await click("提交配置提案", document.querySelector("dialog"));
  check(!document.querySelector("dialog[open]"), "Agent submit failed");
  const agent = state().agents.find((agent) => agent.name === "浏览器研究员");
  check(agent?.status === "待批准", "Agent must await approval");
  if (tools)
    check(
      JSON.stringify(state().resources.tools) === tools,
      "Tool grants mutated",
    );
  await click("编辑配置并重新提案");
  await fill("System prompt（长期指令）", "修订长期指令");
  await click("提交配置提案", document.querySelector("dialog"));
  check(
    state().agents.find((item) => item.id === agent.id).revision === 2,
    "Config not versioned",
  );
  check(
    state().agents.find((item) => item.id === agent.id).history[0]
      .systemPrompt === "始终核对来源，不编造证据",
    "History lost",
  );
  const approval = state().resources.approvals.find(item => item.target === agent.id && item.targetRevision === 2);
  await go(`/approvals/${approval.id}`);
  await click("批准当前版本");
  await fill("依据与说明", "已审核职责、指令与工具边界");
  await click("确认操作", document.querySelector("dialog"));
  check(state().agents.find(item => item.id === agent.id).approvedRevision === 2, "Agent config approval missing");
  await go("/templates");
  await click("新建工作流模板");
  await fill("工作流模板名称", "浏览器工作流模板");
  await click("创建模板", document.querySelector("dialog"));
  await fill("AI 起草目标（本地模拟）", "产出可独立检查的研究报告");
  await click("生成 AI 模拟草案");
  let template = state().resources.templates.find(
    (item) => item.name === "浏览器工作流模板",
  );
  check(
    template.flow.flowDraft.source === "AI 模拟草案",
    "Missing simulation provenance",
  );
  await fill("节点名称", "人工确认的研究节点");
  await click("确认发布新版本");
  template = state().resources.templates.find(
    (item) => item.id === template.id,
  );
  check(template.flow.flowVersion === 1, "Template did not publish");
  check(
    template.flow.flowNodes.find((node) => node.id === "build").data.label ===
      "人工确认的研究节点",
    "Manual node change missing",
  );
  await click("编辑新草案");
  await fill("节点名称", "第二版研究节点");
  await click("确认发布新版本");
  await select("已发布工作流版本", "固定版本 v1");
  check(
    document.querySelector('[name="节点名称"]').value === "人工确认的研究节点",
    "Historical definition mutated",
  );
  check(
    document.querySelector('[name="节点名称"]').disabled,
    "Historical definition editable",
  );
  await click("查看最新定义");
  check(
    document.querySelector('[name="节点名称"]').value === "第二版研究节点",
    "Latest definition missing",
  );
  check(
    document.documentElement.scrollWidth <= innerWidth + 1,
    "Page overflow",
  );
  return {
    pass: true,
    agentRevision: 2,
    templateVersion: 2,
    simulation: true,
    historyImmutable: true,
  };
})();
