import React, { useState } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  Handle,
  Position,
  applyNodeChanges,
  applyEdgeChanges,
  addEdge,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import {
  useStore,
  update,
  log,
  id,
  assertWrite,
  projectFlow,
  mutableProjectFlow,
  validateWorkflow,
} from "./store";
import {
  Icon,
  Button,
  IconButton,
  Badge,
  PageHead,
  Field,
  Select,
  Modal,
  Notice,
  Empty,
  Link,
  useConfirm,
} from "./ui";
const library = [
  ["AI 任务", "Bot", "模型与 Agent", "使用模型完成分析、生成与制作"],
  ["工具调用", "Wrench", "模型与 Agent", "MCP / Skill / 专业工具"],
  ["人工审批", "Stamp", "协作与质量", "等待有权 Human 作出决定"],
  ["独立检查", "ShieldCheck", "协作与质量", "验证固定成果和验收条款"],
  ["条件分支", "GitBranch", "流程控制", "根据输入走满足条件的分支"],
  ["并行汇合", "GitCompareArrows", "流程控制", "等待指定上游输出齐备"],
  ["等待", "Clock3", "流程控制", "等待事件或指定时长"],
  ["HTTP 请求", "Globe2", "连接与数据", "通过受控连接器交换数据"],
  ["数据转换", "FileJson", "连接与数据", "映射与整理节点输入输出"],
];
const kindOf = (n) =>
  n.data.kind ||
  (n.id === "brief" ? "触发" : n.id === "delivery" ? "交付" : "AI 任务");
function normalize(n) {
  return {
    ...n,
    type: "task",
    data: {
      owner: "林予",
      output: "固定成果与检查证据",
      gate: "必需内部检查通过",
      prompt: "依据已确认的输入契约完成工作。",
      runtime: "Codex CLI",
      timeout: "300",
      retries: "2",
      ...n.data,
      kind: kindOf(n),
    },
  };
}
function TaskNode({ id: nodeId, data, selected }) {
  const icon =
    library.find((n) => n[0] === data.kind)?.[1] ||
    (data.kind === "触发" ? "Play" : "PackageCheck");
  return (
    <div className={`workflow-node ${selected ? "selected" : ""}`}>
      <div className="workflow-node-head">
        <span
          className={`workflow-node-icon ${data.kind === "人工审批" ? "amber" : ""}`}
        >
          <Icon name={icon} size={23} />
        </span>
        <small>{data.kind}</small>
      </div>
      <b>{data.label}</b>
      <footer>
        <Icon name="UserCheck" size={12} />
        {data.owner}
        <span>{data.gate ? "有验收契约" : "待配置"}</span>
      </footer>
      {nodeId !== "brief" && (
        <Handle type="target" position={Position.Left} id="in" />
      )}
      {nodeId !== "delivery" &&
        (data.kind === "条件分支" ? (
          <>
            <Handle
              type="source"
              position={Position.Right}
              id="true"
              style={{ top: "35%" }}
            />
            <Handle
              type="source"
              position={Position.Right}
              id="false"
              style={{ top: "75%" }}
            />
            <span className="port-label yes">是</span>
            <span className="port-label no">否</span>
          </>
        ) : (
          <Handle type="source" position={Position.Right} id="out" />
        ))}
    </div>
  );
}
const nodeTypes = { task: TaskNode };
export function Flow({ projectId, params, toast }) {
  const s = useStore(),
    confirm = useConfirm(),
    [selected, setSelected] = useState("build"),
    [editing, setEditing] = useState(false),
    [tab, setTab] = useState("参数"),
    [picker, setPicker] = useState(false),
    [search, setSearch] = useState(""),
    [issues, setIssues] = useState([]),
    [debug, setDebug] = useState({});
  const def = projectFlow(s, projectId),
    raw =
      editing && def.flowDraft
        ? def.flowDraft
        : { nodes: def.flowNodes, edges: def.flowEdges };
  const graph = { nodes: raw.nodes.map(normalize), edges: raw.edges },
    node = graph.nodes.find((n) => n.id === selected),
    mode = params.get("view") || "canvas";
  const act = (fn) => {
    try {
      assertWrite();
      fn();
    } catch (e) {
      toast(e.message, true);
    }
  };
  const mutate = (fn) =>
    act(() => update((x) => fn(mutableProjectFlow(x, projectId).flowDraft, x)));
  const begin = () =>
    act(() => {
      update((x) => {
        const flow = mutableProjectFlow(x, projectId);
        flow.flowDraft ??= structuredClone(graph);
      });
      setEditing(true);
      setIssues([]);
    });
  const change = (field, value) =>
    mutate((d) => {
      const n = d.nodes.find((n) => n.id === selected);
      if (n) n.data[field] = value;
    });
  const add = (kind) => {
    const nid = id("step");
    mutate((d) =>
      d.nodes.push(
        normalize({
          id: nid,
          position: {
            x: 330 + (d.nodes.length % 3) * 260,
            y: 430 + Math.floor(d.nodes.length / 3) * 130,
          },
          data: {
            label: kind,
            kind,
            prompt: "",
            owner: "林予",
            output: "固定成果与检查证据",
            gate: "必需内部检查通过",
            condition: "",
            tool: "",
            url: "",
          },
        }),
      ),
    );
    setSelected(nid);
    setPicker(false);
    setTab("参数");
    setIssues([]);
  };
  const inspect = () => {
    const found = validateWorkflow(graph);
    setIssues(found);
    return found;
  };
  const publish = () =>
    act(() => {
      if (s.role !== "owner") throw Error("工作流发布需要负责人确认。");
      const found = inspect();
      if (found.length) {
        setSelected(found[0].node);
        throw Error(found[0].message);
      }
      update((x) => {
        const f = mutableProjectFlow(x, projectId);
        f.flowNodes = structuredClone(graph.nodes);
        f.flowEdges = structuredClone(graph.edges);
        f.flowVersion++;
        f.flowDraft = null;
        log(
          x,
          `发布工作流 v${f.flowVersion}；既有运行锁定原版本`,
          `/p/${projectId}/flow`,
        );
      });
      setEditing(false);
      toast("工作流已发布，既有运行不会被改写。");
    });
  const remove = async () => {
    if (!node || ["brief", "delivery"].includes(node.id)) return;
    if (
      !(await confirm(`删除「${node.data.label}」及其连线？此操作只影响草案。`))
    )
      return;
    mutate((d) => {
      d.nodes = d.nodes.filter((n) => n.id !== selected);
      d.edges = d.edges.filter(
        (e) => e.source !== selected && e.target !== selected,
      );
    });
    setSelected("brief");
  };
  const duplicate = () => {
    if (!node) return;
    const nid = id("step");
    mutate((d) =>
      d.nodes.push({
        ...structuredClone(node),
        id: nid,
        position: { x: node.position.x + 40, y: node.position.y + 150 },
        data: { ...node.data, label: `${node.data.label} · 副本` },
      }),
    );
    setSelected(nid);
  };
  const field = (label, key, options, type = "text") => (
    <Field
      label={label}
      value={node?.data[key] ?? ""}
      options={options}
      type={type}
      disabled={!editing}
      onChange={(value) => change(key, value)}
    />
  );
  return (
    <>
      <PageHead
        eyebrow="协作 / WORKFLOW"
        title="工作流"
        subtitle={`${s.projects.find((p) => p.id === projectId)?.name || "项目实例"} · ${def.flowVersion ? `已发布 v${def.flowVersion}` : "初始模板 · 尚未发布"}`}
      >
        <Badge>{editing ? "编辑草案" : "查看定义"}</Badge>
        {editing ? (
          <>
            <Button
              icon="Save"
              onClick={() =>
                act(() => {
                  update((x) =>
                    log(x, "保存工作流草案", `/p/${projectId}/flow`),
                  );
                  toast("草案已保存");
                })
              }
            >
              保存草案
            </Button>
            <Button variant="primary" onClick={publish}>
              确认发布新版本
            </Button>
          </>
        ) : (
          <Button variant="primary" icon="Pencil" onClick={begin}>
            编辑新草案
          </Button>
        )}
      </PageHead>
      <div className="toolbar">
        <div className="segmented">
          <Link
            to={`/p/${projectId}/flow?view=canvas`}
            className={mode === "canvas" ? "selected" : ""}
          >
            <Icon name="Workflow" />
            画布
          </Link>
          <Link
            to={`/p/${projectId}/flow?view=list`}
            className={mode === "list" ? "selected" : ""}
          >
            <Icon name="List" />
            节点列表
          </Link>
        </div>
        <span className="muted">
          {graph.nodes.length} 个节点 · {graph.edges.length} 条连接
        </span>
        <div className="grow" />
        <Button
          icon="ClipboardCheck"
          onClick={() => {
            if (!inspect().length) toast("结构与必需配置检查通过");
          }}
        >
          检查配置
        </Button>
        <Button icon="Plus" disabled={!editing} onClick={() => setPicker(true)}>
          添加节点
        </Button>
      </div>
      {issues.length > 0 && (
        <div className="workflow-issues" role="alert">
          <b>发布前需处理 {issues.length} 项</b>
          {issues.map((issue, i) => (
            <button
              key={i}
              onClick={() => {
                setSelected(issue.node);
                setTab("参数");
              }}
            >
              <Icon name="AlertCircle" size={14} />
              {issue.message}
            </button>
          ))}
        </div>
      )}
      <div className="flow-layout enhanced-flow">
        <div className="flow-area">
          {mode === "canvas" ? (
            <ReactFlow
              nodeTypes={nodeTypes}
              nodes={graph.nodes.map((n) => ({
                ...n,
                selected: n.id === selected,
                deletable: false,
              }))}
              edges={graph.edges}
              fitView
              minZoom={0.15}
              maxZoom={2}
              nodesDraggable={editing}
              nodesConnectable={editing}
              deleteKeyCode={null}
              defaultEdgeOptions={{
                type: "smoothstep",
                style: { strokeWidth: 2 },
                markerEnd: { type: "arrowclosed" },
              }}
              onNodesChange={(changes) => {
                if (editing)
                  mutate(
                    (d) =>
                      (d.nodes = applyNodeChanges(
                        changes.filter((c) => c.type !== "remove"),
                        d.nodes,
                      )),
                  );
              }}
              onEdgesChange={(changes) => {
                if (editing)
                  mutate((d) => (d.edges = applyEdgeChanges(changes, d.edges)));
              }}
              onConnect={(c) => {
                if (editing)
                  mutate(
                    (d) =>
                      (d.edges = addEdge({ ...c, id: id("edge") }, d.edges)),
                  );
              }}
              onNodeClick={(_, n) => {
                setSelected(n.id);
                setTab("参数");
              }}
              onNodeContextMenu={(e, n) => {
                e.preventDefault();
                setSelected(n.id);
                setTab("参数");
              }}
            >
              <Background gap={24} />
              <Controls />
              <MiniMap
                pannable
                zoomable
                nodeColor="var(--primary)"
                bgColor="var(--card)"
                maskColor="color-mix(in srgb,var(--bg) 70%,transparent)"
              />
            </ReactFlow>
          ) : (
            <div className="flow-list">
              {graph.nodes.map((n) => (
                <Button
                  key={n.id}
                  onClick={() => setSelected(n.id)}
                  icon={
                    library.find((k) => k[0] === n.data.kind)?.[1] || "Workflow"
                  }
                >
                  {n.data.label}
                </Button>
              ))}
            </div>
          )}
          <span className="flow-canvas-hint">
            拖动节点 · 连接端口 · 选择节点编辑
          </span>
        </div>
        <aside className="flow-inspector node-panel">
          <div className="node-panel-heading">
            <span className="eyebrow">{node?.data.kind || "节点"}</span>
            <h2>{node?.data.label || "选择一个节点"}</h2>
            <small>{node?.id}</small>
          </div>
          {node ? (
            <>
              <div className="node-actions">
                <Button
                  disabled={
                    !editing || ["brief", "delivery"].includes(selected)
                  }
                  icon="Copy"
                  onClick={duplicate}
                >
                  复制
                </Button>
                <Button
                  disabled={
                    !editing || ["brief", "delivery"].includes(selected)
                  }
                  icon="Trash2"
                  onClick={remove}
                >
                  删除
                </Button>
                <Button
                  icon="Play"
                  onClick={() => {
                    setDebug((v) => ({
                      ...v,
                      [selected]: {
                        time: new Date().toLocaleTimeString(),
                        input: { node: selected, source: "固定上游示例" },
                        output: {
                          status: "模拟通过",
                          artifact: `mock/${selected}/v1`,
                        },
                      },
                    }));
                    setTab("调试");
                  }}
                >
                  模拟测试
                </Button>
              </div>
              <div className="tabs node-panel-tabs">
                {["参数", "输入输出", "调试"].map((t) => (
                  <button
                    key={t}
                    className={tab === t ? "selected" : ""}
                    onClick={() => setTab(t)}
                  >
                    {t}
                  </button>
                ))}
              </div>
              {tab === "参数" ? (
                <>
                  <Notice>
                    {editing
                      ? "草案更改即时保存，发布后才供后续运行使用。"
                      : "当前定义只读；编辑新草案后修改参数。"}
                  </Notice>
                  {field("节点名称", "label")}
                  {field("Human 负责人", "owner")}
                  {field("共同执行者", "executors")}
                  {node.data.kind === "AI 任务" && (
                    <>
                      {field("Runtime", "runtime", [
                        "Codex CLI",
                        "Claude Code",
                        "OpenCode",
                        "模型 API",
                      ])}
                      {field("指令 / 提示词", "prompt", undefined, "textarea")}
                    </>
                  )}
                  {node.data.kind === "工具调用" &&
                    field("工具 / Skill 引用", "tool")}
                  {node.data.kind === "HTTP 请求" && (
                    <>
                      {field("方法", "method", [
                        "GET",
                        "POST",
                        "PUT",
                        "DELETE",
                      ])}
                      {field("请求地址", "url")}
                      {field("凭据引用（不填密钥）", "credential")}
                    </>
                  )}
                  {node.data.kind === "条件分支" &&
                    field("条件表达式", "condition", undefined, "textarea")}
                  {node.data.kind === "并行汇合" &&
                    field("汇合策略", "join", [
                      "全部必需输入到齐",
                      "任一可选输入到齐",
                    ])}
                  {node.data.kind === "等待" &&
                    field("等待事件 / 时长", "waitFor")}
                  {["人工审批", "交付"].includes(node.data.kind) &&
                    field("批准人 / 接收人", "approver")}
                  {field("必需内部验收", "gate")}
                  <details className="node-advanced">
                    <summary>错误与重试</summary>
                    {field("超时秒数", "timeout", undefined, "number")}
                    {field("重试次数", "retries", undefined, "number")}
                    {field("失败后", "onError", [
                      "暂停并通知负责人",
                      "有限重试后暂停",
                    ])}
                  </details>
                </>
              ) : tab === "输入输出" ? (
                <>
                  {field(
                    "输入映射 / 固定版本引用",
                    "inputs",
                    undefined,
                    "textarea",
                  )}
                  {field("输出契约", "output", undefined, "textarea")}
                  <h3>上游连接</h3>
                  {graph.edges
                    .filter((e) => e.target === selected)
                    .map((e) => (
                      <div className="node-connection" key={e.id}>
                        <Button onClick={() => setSelected(e.source)}>
                          {
                            graph.nodes.find((n) => n.id === e.source)?.data
                              .label
                          }
                        </Button>
                        {editing && (
                          <IconButton
                            name="X"
                            label="移除此连接"
                            onClick={() =>
                              mutate(
                                (d) =>
                                  (d.edges = d.edges.filter(
                                    (v) => v.id !== e.id,
                                  )),
                              )
                            }
                          />
                        )}
                      </div>
                    ))}
                  <h3>下游接收</h3>
                  {graph.edges
                    .filter((e) => e.source === selected)
                    .map((e) => (
                      <div className="node-connection" key={e.id}>
                        <Button onClick={() => setSelected(e.target)}>
                          {
                            graph.nodes.find((n) => n.id === e.target)?.data
                              .label
                          }
                        </Button>
                        <small>
                          {e.sourceHandle === "true"
                            ? "满足条件"
                            : e.sourceHandle === "false"
                              ? "不满足条件"
                              : ""}
                        </small>
                        {editing && (
                          <IconButton
                            name="X"
                            label="移除此连接"
                            onClick={() =>
                              mutate(
                                (d) =>
                                  (d.edges = d.edges.filter(
                                    (v) => v.id !== e.id,
                                  )),
                              )
                            }
                          />
                        )}
                      </div>
                    ))}
                </>
              ) : (
                <>
                  <Notice>
                    只模拟节点输入输出，不会调用模型或发出网络请求，也不产生正式质量验收证据。
                  </Notice>
                  {debug[selected] ? (
                    <>
                      <small>最近模拟 · {debug[selected].time}</small>
                      <h3>输入</h3>
                      <pre>
                        {JSON.stringify(debug[selected].input, null, 2)}
                      </pre>
                      <h3>输出</h3>
                      <pre>
                        {JSON.stringify(debug[selected].output, null, 2)}
                      </pre>
                    </>
                  ) : (
                    <Empty
                      title="尚未测试此节点"
                      text="点击模拟测试，查看示例输入输出。"
                    />
                  )}
                </>
              )}
            </>
          ) : (
            <Empty title="从画布选择一个节点" />
          )}
        </aside>
      </div>
      {picker && (
        <Modal title="添加工作流节点" onClose={() => setPicker(false)} wide>
          <Field
            label="搜索节点"
            value={search}
            onChange={setSearch}
            placeholder="AI、审批、条件、工具…"
          />
          <div className="node-library">
            {library
              .filter((row) =>
                row.join(" ").toLowerCase().includes(search.toLowerCase()),
              )
              .map(([kind, icon, category, desc]) => (
                <button key={kind} onClick={() => add(kind)}>
                  <Icon name={icon} size={24} />
                  <span>
                    <small>{category}</small>
                    <b>{kind}</b>
                    <p>{desc}</p>
                  </span>
                  <Icon name="Plus" size={16} />
                </button>
              ))}
          </div>
        </Modal>
      )}
    </>
  );
}
