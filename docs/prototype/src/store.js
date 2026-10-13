import { useSyncExternalStore } from "react";
import { initial } from "./data";
const KEY = "collaboration-prototype-v1";
let state;
try {
  const saved = JSON.parse(localStorage.getItem(KEY));
  state = saved?.version === 1 ? saved : structuredClone(initial);
} catch {
  state = structuredClone(initial);
}
function hydrate(value) {
  value.activeTenant ??= "studio";
  value.tenants ??= [{ id: "studio", name: "Northstar Studio", role: "owner" }];
  value.tenantData ??= {};

  value.budget ??= 2400;
  value.notifications ??= { critical: true, grouped: true, memory: true };
  value.taskPreviews ??= {};
  value.flowDraft ??= null;
  value.flowVersion ??= 3;
  value.work.forEach((w) => {
    if (!("plannedStart" in w)) {
      const dates = {
        "W-104": ["2026-10-12", "2026-10-16"],
        "W-103": ["2026-10-10", "2026-10-14"],
        "W-201": ["2026-10-14", "2026-10-22"],
        "W-301": ["2026-10-11", "2026-10-20"],
      };
      [w.plannedStart, w.plannedEnd] = dates[w.id] || ["", ""];
      w.scheduleRevision = 0;
      w.scheduleHistory = [];
    }

    w.history ??= [
      {
        version: w.version,
        notes: w.notes,
        requirements: [...w.requirements],
        qa: w.qa,
        accepted: w.accepted,
      },
    ];
  });
  return value;
}
state = hydrate(state);
const listeners = new Set();
export const read = () => state;
export const useStore = () =>
  useSyncExternalStore((fn) => {
    listeners.add(fn);
    return () => listeners.delete(fn);
  }, read);
export const id = (prefix) => `${prefix}-${crypto.randomUUID().slice(0, 8)}`;
export function update(fn) {
  const next = structuredClone(state);
  fn(next);
  localStorage.setItem(KEY, JSON.stringify(next));
  state = next;
  listeners.forEach((fn) => fn());
}
export function reset() {
  localStorage.removeItem(KEY);
  state = hydrate(structuredClone(initial));
  listeners.forEach((fn) => fn());
}
export function log(s, text, target = "/governance/audit") {
  s.events.unshift({
    id: id("e"),
    time: new Intl.DateTimeFormat("zh-CN", {
      hour: "2-digit",
      minute: "2-digit",
    }).format(new Date()),
    actor: s.role === "external" ? "陈曦" : "林予",
    text,
    target,
  });
}
export function assertWrite() {
  if (state.role === "external")
    throw Error("当前为外部客户视角，只能对已共享成果提交反馈与回执。");
  if (state.offline)
    throw Error("当前处于离线状态。草稿已保留，恢复连接后请重新确认操作。");
}
export function workCommand(workId, command, reason = "", expectedVersion) {
  assertWrite();
  const w = state.work.find((x) => x.id === workId);
  if (!w) throw Error("工作不存在");
  if (expectedVersion !== undefined && w.version !== expectedVersion)
    throw Error("成果版本已经变化，请重新核对后再提交。");
  if (["return", "cancel", "takeover"].includes(command) && !reason.trim())
    throw Error("请填写理由，便于下一位参与者继续处理。");
  const labels = {
    plan: "确认计划",
    flow: "确认 SOP",
    start: "开始执行",
    submit: "提交成果",
    check: "完成独立复核",
    accept: "接受成果",
    deliver: "正式交付",
    return: "退回修订",
    cancel: "取消工作",
    takeover: "Human 接管",
    resume: "继续执行",
  };
  if (!labels[command]) throw Error("未知工作动作。");
  if (w.status === "已取消") throw Error("工作已取消，不能继续推进。");
  if (w.delivered && !["return"].includes(command))
    throw Error("此版本已经交付，请通过返工产生新版本。");
  if (command === "start" && w.status !== "待开始")
    throw Error("只有待开始的工作可以启动。");
  if (command === "submit" && !["进行中", "等待"].includes(w.status))
    throw Error("只有进行中或接管中的工作可以提交新成果。");
  if (
    ["plan", "flow", "accept", "deliver"].includes(command) &&
    state.role === "executor"
  )
    throw Error("当前执行者没有此项 Human 决定权限，请交给负责人。");
  if (command === "flow" && !w.plan)
    throw Error("先确认当前计划，再确认 SOP。");
  if (command === "start" && (!w.plan || !w.flow))
    throw Error("计划与 SOP 尚未全部确认。");
  if (command === "check" && w.status !== "待验收")
    throw Error("先提交固定版本再进行独立检查。");
  if (command === "accept" && !w.qa)
    throw Error("必需内部检查尚未通过，Human 批准不能绕过质量门。");
  if (command === "deliver" && (!w.qa || !w.accepted))
    throw Error("交付前须内部检查通过，并由 Human 接受当前版本。");
  if (command === "deliver" && w.delivered)
    throw Error("当前版本已经交付，请查看既有交付包。");
  update((s) => {
    const x = s.work.find((x) => x.id === workId);
    if (command === "plan") x.plan = true;
    if (command === "flow") x.flow = true;
    if (command === "start" || command === "resume") x.status = "进行中";
    if (command === "submit") {
      x.history.push({
        version: x.version,
        notes: x.notes,
        requirements: [...x.requirements],
        qa: x.qa,
        accepted: x.accepted,
      });
      x.status = "待验收";
      x.version++;
      x.qa = false;
      x.accepted = false;
      x.delivered = false;
    }
    if (command === "check") {
      x.qa = true;
      s.resources.tests.unshift({
        id: id("TC"),
        name: `${x.title} · v${x.version} 独立复核`,
        status: "通过",
        owner: "Verifier",
        version: `v${x.version}`,
        work: x.id,
        method: "模拟平台独立验证",
        expected: x.requirements.join("；"),
        actual: "演示复核通过",
        evidence: `mock-run/${x.id}/v${x.version}`,
      });
    }
    if (command === "accept") {
      x.accepted = true;
      x.status = "待交接";
    }
    if (command === "deliver") {
      x.delivered = true;
      x.status = "已完成";
      s.resources.deliveries.unshift({
        id: id("D"),
        name: `${x.title} · v${x.version}`,
        status: "待回执",
        owner: x.owner,
        work: x.id,
        version: `v${x.version}`,
        scope: "指定接收方",
        evidence: "内部检查与 Human 接受记录",
        content: "固定成果、源文件、检查证据与使用说明",
      });
    }
    if (command === "return") {
      x.status = "进行中";
      x.qa = false;
      x.accepted = false;
      x.delivered = false;
      x.notes = reason;
    }
    if (command === "cancel") x.status = "已取消";
    if (command === "takeover") {
      x.status = "等待";
      x.notes = `Human 已接管，Agent 暂停竞争写入。${reason}`;
    }
    log(
      s,
      `${labels[command]} · ${x.title} v${x.version}${reason ? " · " + reason : ""}`,
      `/p/${x.project}/work-items/${x.id}`,
    );
  });
}
export function saveResource(kind, record) {
  assertWrite();
  update((s) => {
    const index = s.resources[kind].findIndex((x) => x.id === record.id);
    if (index < 0)
      s.resources[kind].unshift({
        ...record,
        id: record.id || id(kind.toUpperCase()),
      });
    else {
      const previous = s.resources[kind][index];
      s.resources[kind][index] = {
        ...record,
        revision: (previous.revision || 1) + 1,
        history: [
          ...(previous.history || []),
          { ...previous, history: undefined },
        ],
      };
      if (["goals", "reports", "templates", "models"].includes(kind))
        s.resources[kind][index].status = "草稿";
    }
    log(s, `${index < 0 ? "创建" : "更新"}${record.name}`, `/${kind}`);
  });
}
export function resourceCommand(
  kind,
  recordId,
  status,
  reason,
  actionLabel = "",
) {
  if (state.role !== "external" || kind !== "deliveries") assertWrite();
  if (state.offline) throw Error("离线中，请恢复连接后重新确认。");
  if (
    state.role === "external" &&
    !["已接受", "部分接受", "已拒收"].includes(status)
  )
    throw Error("当前客户仅可提交交付回执。");
  if (
    state.role === "executor" &&
    ["已批准", "已接受", "已发布", "生效"].includes(status)
  )
    throw Error("此决定需要负责人权限。");
  const record = state.resources[kind].find((x) => x.id === recordId);
  if (!record) throw Error("记录不存在。");
  if (kind === "reports" && status === "已提交" && record.owner !== "林予")
    throw Error("Human 报告只能由本人确认；Agent 报告按单独策略提交。");
  if (kind === "approvals" && status === "已批准") {
    const target = state.resources.models.find((m) => m.id === record.target);
    if (target && (target.revision || 1) !== (record.targetRevision || 1))
      throw Error("审批目标已变更，请重新提交当前配置版本。");
  }
  update((s) => {
    const r = s.resources[kind].find((x) => x.id === recordId);
    r.history ??= [];
    r.history.push({
      status: r.status,
      actor: state.role === "external" ? "陈曦" : "林予",
      reason,
      time: new Date().toISOString(),
    });
    r.status = status;
    if (kind === "approvals" && status === "已批准") {
      const agent = s.agents.find(
        (a) => a.name === r.target && a.status === "待批准",
      );
      if (agent) agent.status = "空闲";
    }
    if (kind === "deliveries" && ["部分接受", "已拒收"].includes(status)) {
      const work = s.work.find((w) => w.id === r.work);
      if (work) {
        work.status = "进行中";
        work.qa = false;
        work.accepted = false;
        work.notes = reason;
        work.delivered = false;
      }
    }
    if (kind === "schedules" && status === "启用")
      r.next = "按已保存时区等待下一次触发";
    if (kind === "models" && status === "生效") {
      const approved = s.resources.approvals.some(
        (a) =>
          a.target === r.id &&
          a.status === "已批准" &&
          (a.targetRevision || 1) === (r.revision || 1),
      );
      if (!approved && record.status !== "生效")
        throw Error("当前配置需要先批准下发范围。");
      r.nodeResults = s.nodes.map((n) => ({
        node: n.id,
        status: n.status === "在线" ? "生效" : "待应用",
        version: r.version,
      }));
      if (r.nodeResults.some((n) => n.status !== "生效")) r.status = "部分生效";
    }
    if (kind === "schedules" && actionLabel === "模拟运行一次") {
      if (r.action.includes("报告") || r.action.includes("日报"))
        s.resources.reports.unshift({
          id: id("RP"),
          name: `${r.name} · 本次草稿`,
          status: "草稿",
          owner: "林予",
          cycle: new Date().toISOString().slice(0, 10),
          summary: "根据当前任务与执行记录生成的模拟报告，等待本人核对确认。",
          risk: s.work
            .filter((w) => !w.qa)
            .map((w) => w.title)
            .join("；"),
          source: r.id,
        });
      else
        s.resources.tests.unshift({
          id: id("TC"),
          name: `${r.name} · 定期检查`,
          status: "待执行",
          owner: r.owner,
          version: "当前固定输入",
          work: "W-301",
          method: "待独立执行",
          expected: "按已确认专业包检查",
          actual: "未执行，不冒充通过",
          evidence: "暂无",
        });
      r.lastRun = new Date().toISOString();
    }
    if (kind === "repositories" && actionLabel === "模拟增量同步")
      r.sync = new Intl.DateTimeFormat("zh-CN", {
        hour: "2-digit",
        minute: "2-digit",
      }).format(new Date());
    if (kind === "approvals" && status === "已批准") {
      const model = s.resources.models.find((x) => x.id === r.target);
      if (model) model.status = "待应用";
    }
    log(
      s,
      `${r.name} → ${status}${reason ? " · " + reason : ""}`,
      `/${kind}/${recordId}`,
    );
  });
}
export function exportData(name, value) {
  const blob = new Blob([JSON.stringify(value, null, 2)], {
    type: "application/json",
  });
  const url = URL.createObjectURL(blob);
  const a = document.createElement("a");
  a.href = url;
  a.download = `${name}.json`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
export function visibleWork(s) {
  return s.work.filter(
    (w) =>
      (s.project === "all" || w.project === s.project) &&
      (s.role !== "external" || w.delivered),
  );
}

// Shared packages retain their fixed version even when current work is reopened.
export function sharedVersions(s, workId) {
  return [
    ...new Set(
      s.resources.deliveries
        .filter((d) => d.work === workId)
        .map((d) => Number(String(d.version).replace(/^v/, "")))
        .filter(Number.isInteger),
    ),
  ].sort((a, b) => a - b);
}
export function sharedArtifactPath(s, w) {
  return `/p/${w.project}/artifacts/${w.id}?version=${sharedVersions(s, w.id).at(-1)}`;
}

export function projectFlow(s, project) {
  if (project === "launch") return s;
  return (
    s.projectFlows?.[project] || {
      flowNodes: initial.flowNodes,
      flowEdges: initial.flowEdges,
      flowDraft: null,
      flowVersion: 0,
    }
  );
}
export function mutableProjectFlow(s, project) {
  if (project === "launch") return s;
  s.projectFlows ??= {};
  s.projectFlows[project] ??= structuredClone(projectFlow(s, project));
  return s.projectFlows[project];
}

const tenantFields = [
  "role",
  "project",
  "projects",
  "work",
  "agents",
  "nodes",
  "messages",
  "comments",
  "resources",
  "events",
  "flowNodes",
  "flowEdges",
  "flowDraft",
  "flowVersion",
  "projectFlows",
  "drafts",
  "taskPreviews",
  "budget",
];
function tenantSnapshot(s) {
  return Object.fromEntries(
    tenantFields.map((k) => [k, structuredClone(s[k] ?? null)]),
  );
}
function emptyTenant(role = "owner") {
  const fresh = hydrate(structuredClone(initial));
  for (const k of [
    "projects",
    "work",
    "agents",
    "nodes",
    "messages",
    "comments",
    "events",
  ])
    fresh[k] = [];
  fresh.resources = Object.fromEntries(
    Object.keys(fresh.resources).map((k) => [k, []]),
  );
  fresh.role = role;
  fresh.project = "all";
  fresh.budget = 0;
  fresh.flowNodes = [];
  fresh.flowEdges = [];
  fresh.flowVersion = 0;
  fresh.projectFlows = {};
  fresh.drafts = {};
  return tenantSnapshot(fresh);
}
export function switchTenant(tenantId) {
  const target = state.tenants.find((t) => t.id === tenantId);
  if (!target) throw Error("只能切换到已加入的租户。");
  if (state.offline) throw Error("离线时不能切换租户，请先恢复连接。");
  if (tenantId === state.activeTenant) return;
  update((s) => {
    s.tenantData[s.activeTenant] = tenantSnapshot(s);
    Object.assign(
      s,
      structuredClone(s.tenantData[tenantId] || emptyTenant(target.role)),
    );
    s.activeTenant = tenantId;
  });
}
export function createTenant(name) {
  if (state.offline) throw Error("离线时不能创建租户。");
  if (!name.trim()) throw Error("请填写租户名称。");
  const tenantId = id("tenant");
  update((s) =>
    s.tenants.push({ id: tenantId, name: name.trim(), role: "owner" }),
  );
  switchTenant(tenantId);
  return tenantId;
}
export function joinTenant(code) {
  if (state.offline) throw Error("离线时不能加入租户。");
  if (code.trim() !== "DEMO-JOIN")
    throw Error("邀请码无效或已过期。原型可使用 DEMO-JOIN。");
  if (!state.tenants.some((t) => t.id === "partner"))
    update((s) =>
      s.tenants.push({ id: "partner", name: "合作空间", role: "executor" }),
    );
  switchTenant("partner");
  return "partner";
}

export function scheduleWork(workId, start, end, reason, revision) {
  assertWrite();
  const w = state.work.find((w) => w.id === workId);
  if (!w) throw Error("工作不存在。");
  if (state.role === "executor" && w.owner !== "林予")
    throw Error("只能调整本人负责的排期。");
  if ((w.scheduleRevision || 0) !== revision)
    throw Error("排期已变化，请重新打开核对。");
  if (!reason.trim()) throw Error("请填写调整说明。");
  const valid = (v) =>
    /^\d{4}-\d{2}-\d{2}$/.test(v) &&
    !Number.isNaN(Date.parse(v)) &&
    new Date(v).toISOString().slice(0, 10) === v;
  if ((start || end) && (!valid(start) || !valid(end)))
    throw Error("请填写真实有效的起止日期，或同时清空。");
  if (start > end) throw Error("结束日期不能早于开始日期。");
  update((s) => {
    const task = s.work.find((v) => v.id === workId);
    task.scheduleHistory ??= [];
    task.scheduleHistory.push({
      start: task.plannedStart || "",
      end: task.plannedEnd || "",
      reason,
      revision: task.scheduleRevision || 0,
    });
    task.plannedStart = start;
    task.plannedEnd = end;
    task.scheduleRevision = (task.scheduleRevision || 0) + 1;
    log(
      s,
      `调整 ${task.title} 排期：${start || "未排期"}${end ? " 至 " + end : ""} · ${reason}`,
      `/p/${task.project}/work-items/${task.id}`,
    );
  });
}

export function validateWorkflow(graph) {
  const issues = [],
    nodes = graph.nodes,
    edges = graph.edges,
    ids = new Set(nodes.map((n) => n.id));
  const fail = (node, message) => issues.push({ node, message });
  if (ids.size !== nodes.length) fail(nodes[0]?.id, "节点标识重复。");
  if (!ids.has("brief")) fail(nodes[0]?.id, "缺少触发入口。");
  if (!ids.has("delivery")) fail(nodes[0]?.id, "缺少最终 Human 交付环节。");
  for (const n of nodes) {
    const prefix = n.data.label || n.id;
    if (!n.data.label?.trim()) fail(n.id, `${prefix}：请填写节点名称。`);
    if (n.id !== "brief" && !edges.some((e) => e.target === n.id))
      fail(n.id, `${prefix}：缺少上游输入，请先连接。`);
    if (n.id !== "delivery" && !edges.some((e) => e.source === n.id))
      fail(n.id, `${prefix}：缺少下游接收方。`);
    if (!n.data.owner?.trim()) fail(n.id, `${prefix}：缺少 Human 负责人。`);
    if (!n.data.output?.trim() || !n.data.gate?.trim())
      fail(n.id, `${prefix}：输出契约与内部验收必须明确。`);
    if (n.data.kind === "AI 任务" && !n.data.prompt?.trim())
      fail(n.id, `${prefix}：请配置任务指令。`);
    if (n.data.kind === "工具调用" && !n.data.tool?.trim())
      fail(n.id, `${prefix}：请选择工具引用。`);
    if (n.data.kind === "HTTP 请求" && !/^https?:\/\//.test(n.data.url || ""))
      fail(n.id, `${prefix}：请配置有效的 HTTP 地址。`);
    if (n.data.kind === "条件分支") {
      if (!n.data.condition?.trim()) fail(n.id, `${prefix}：请填写分支条件。`);
      if (
        !["true", "false"].every((handle) =>
          edges.some((e) => e.source === n.id && e.sourceHandle === handle),
        )
      )
        fail(n.id, `${prefix}：是 / 否分支都需要下游。`);
    }
    if (!Number.isFinite(Number(n.data.timeout)) || Number(n.data.timeout) <= 0)
      fail(n.id, `${prefix}：超时必须大于零。`);
    if (!Number.isInteger(Number(n.data.retries)) || Number(n.data.retries) < 0)
      fail(n.id, `${prefix}：重试次数必须是非负整数。`);
  }
  for (const e of edges)
    if (!ids.has(e.source) || !ids.has(e.target))
      fail(e.source, "连接引用了不存在的节点。");
  const seen = new Set(),
    active = new Set();
  function visit(node) {
    if (active.has(node)) {
      fail(node, "检测到未定义退出条件的循环。");
      return;
    }
    if (seen.has(node)) return;
    active.add(node);
    seen.add(node);
    edges.filter((e) => e.source === node).forEach((e) => visit(e.target));
    active.delete(node);
  }
  if (ids.has("brief")) visit("brief");
  for (const n of nodes)
    if (!seen.has(n.id)) fail(n.id, `${n.data.label}：无法从触发入口到达。`);
  return issues;
}
