import { serializeWorkflowDefinition, parseWorkflowDefinition, replaceWorkflowDraft, evaluateLoopBoundary } from "./workflow-definition";
import { workflowRecordForProject } from "./workflow-registry";
import { workflowNodeLibrary as library, normalizeWorkflowNode as normalize, groupsForWorkflowNode, workflowInputFields, workflowErrorFields, workflowHumanLabel, workflowHumanValue, workflowResourceOptions, validateWorkflowNodeConfig, workflowGraphForValidation, simulateWorkflowNode, workflowSettingsDefaults, validateWorkflowSettings, validateWorkflowCollaboration } from "./workflow-node-config";
import { useViewState } from "./view-state";
import { projectIsArchived } from "./project-lifecycle";
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
  flowDefinition,
  publishProjectFlow,
  templateFlow, mutableTemplateFlow, templateDefinition, publishWorkflowTemplate, draftWorkflowTemplate,
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
  Tabs,
} from "./ui";
function TaskNode({ id: nodeId, data, selected }) {
  const state = useStore();
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
        {workflowHumanLabel(state, data.owner)}
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
export function Flow({ projectId, templateId, params = new URLSearchParams(), toast, embedded = false, embeddedState = {}, onEmbeddedStateChange }) {
  const useFlowState = (name, fallback) => {
    const local = useViewState(name, fallback);
    if (!embedded || !onEmbeddedStateChange) return local;
    const candidate = embeddedState[name];
    const valid = name === "version" ? candidate === null || Number.isSafeInteger(candidate) : typeof candidate === typeof fallback;
    const value = candidate === undefined || !valid ? fallback : candidate;
    return [value, next => onEmbeddedStateChange({[name]: typeof next === "function" ? next(value) : next})];
  };
  const [popupVersion, setPopupVersion] = useFlowState("version", params.has("version") ? Number(params.get("version")) : null);
  const [popupMode, setPopupMode] = useFlowState("view", "canvas");
  const workflowId = params.get("workflow_id") || "default";
  const [brief, setBrief] = useState("");
  const [transfer, setTransfer] = useState(null);
  const [loopProbe,setLoopProbe]=useState({text:"[]",result:null});
  const path = templateId ? `/templates/${templateId}` : `/p/${projectId}/flow`;
  const getMutable = (state) => templateId ? mutableTemplateFlow(state, templateId) : mutableProjectFlow(state, projectId, workflowId);
  const s = useStore(),
    confirm = useConfirm(),
    [selected, setSelected] = useFlowState("flow-node", params.get("node") || "build"),
    [editingDraft, setEditing] = useState(() => !embedded && Boolean((templateId ? templateFlow(s, templateId) : workflowId === "default" ? projectFlow(s, projectId) : workflowRecordForProject(s, projectId, workflowId)?.flow)?.flowDraft)),
    [tab, setTab] = useFlowState("flow-tab", "配置"),
    [picker, setPicker] = useState(false),
    [search, setSearch] = useState(""),
    [issues, setIssues] = useState([]),
    [checkedDefinition, setCheckedDefinition] = useState(null),
    [debug, setDebug] = useState({}),
    [inspectorMode, setInspectorMode] = useFlowState("flow-inspector", "node"),
    [settingsTab, setSettingsTab] = useFlowState("flow-settings-tab", "基本信息"),
    [expandedPanel, setExpandedPanel] = useFlowState("flow-wide-panel", false),
    [sourceNode, setSourceNode] = useState("");
  const workflowRecord = !templateId && workflowId !== "default" ? workflowRecordForProject(s, projectId, workflowId) : null;
  const unavailable = !templateId && workflowId !== "default" && !workflowRecord;
  const readOnly = projectIsArchived(s,projectId) || Boolean(workflowRecord?.archivedAt);
  const def = unavailable ? {flowNodes:[],flowEdges:[],flowVersion:0} : templateId ? templateFlow(s, templateId) : projectFlow(s, projectId, workflowId),
    requestedVersion = embedded ? popupVersion : params.has("version")
      ? Number(params.get("version"))
      : null,
    historical = requestedVersion !== null,
    editing = !embedded && editingDraft && !historical && !readOnly,
    definition = historical && !unavailable
      ? (templateId ? templateDefinition(s, templateId, requestedVersion) : flowDefinition(s, projectId, requestedVersion, workflowId))
      : null,
    raw = historical
      ? definition || { nodes: [], edges: [] }
      : def.flowDraft && (editing || !def.flowVersion)
        ? def.flowDraft
        : { nodes: def.flowNodes, edges: def.flowEdges };
  const graph = { baseVersion: raw.baseVersion ?? def.flowVersion ?? 0, nodes: raw.nodes.map(normalize), edges: raw.edges },
    node = graph.nodes.find((n) => n.id === selected),
    mode = embedded ? popupMode : params.get("view") || "canvas";
  const workflowURL = patch => {
    const query = new URLSearchParams(location.hash.split("?")[1] || "");
    if (!templateId) query.set("workflow_id",workflowId);
    for (const [key, value] of Object.entries(patch)) value === null ? query.delete(key) : query.set(key, String(value));
    return `${path}?${query}`;
  };
  const locateIssue = issue => {
    setSelected(issue.node);
    if (issue.field?.startsWith("workflowSettings.")) {
      setInspectorMode("settings");
      const key = issue.field.split(".")[1];
      setSettingsTab(["variables", "credentialRefs", "notificationTarget", "notificationEvents"].includes(key) ? "变量与通知" : ["name", "scenario", "description", "stages"].includes(key) ? "基本信息" : "运行控制");
    } else {
      setInspectorMode("node");
      setTab(["inputs", "inputVersion", "inputContract", ...workflowInputFields.map(field => field.key)].includes(issue.field) ? "输入" : ["receiver", "output", "gate", "outputSchema"].includes(issue.field) ? "输出" : ["retryDelay", "retries", "onError", "timeout", ...workflowErrorFields.map(field => field.key)].includes(issue.field) ? "错误策略" : "配置");
    }
  };
  const act = (fn) => {
    try {
      if (readOnly) throw Error("项目或工作流已归档，版本只读。");
      assertWrite();
      fn();
    } catch (e) {
      toast(e.message, true);
    }
  };
  const mutate = (fn) =>
    act(() => update((x) => {
      const next = structuredClone(graph); fn(next, x);
      replaceWorkflowDraft(getMutable(x), next, JSON.stringify(def.flowDraft));
    }));
  const begin = () =>
    act(() => {
      update((x) => {
        const flow = getMutable(x);
        if (!flow.flowDraft) replaceWorkflowDraft(flow, graph, JSON.stringify(def.flowDraft));
        else if (flow.flowDraft.baseVersion === undefined) flow.flowDraft.baseVersion = flow.flowVersion || 0;
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
            owner: s.organization.members.find(person => person.id === "H-lin" && person.kind === "Human")?.id || "",
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
    setInspectorMode("node");
    setPicker(false);
    setTab("配置");
    setIssues([]);
  };
  const inspect = () => {
    const found = [...validateWorkflow(workflowGraphForValidation(graph)), ...validateWorkflowNodeConfig(graph, s), ...validateWorkflowSettings(graph), ...validateWorkflowCollaboration(graph)];
    setIssues(found);
    setCheckedDefinition(found.length ? null : JSON.stringify(graph));
    return found;
  };
  const publish = () =>
    act(() => {
      if (s.role !== "owner") throw Error("工作流发布需要负责人确认。");
      const found = inspect();
      if (found.length) {
        locateIssue(found[0]);
        throw Error(found[0].message);
      }
      update((x) => {
        const version = templateId ? publishWorkflowTemplate(x, templateId, graph) : publishProjectFlow(x, projectId, graph, workflowId);
        log(
          x,
          `发布工作流定义 v${version}；已确认工作保留原版本引用`,
          path,
        );
      });
      setEditing(false);
      toast("工作流定义已发布，已确认工作的版本引用保持不变。");
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
  const field = (label, key, options, type = "text", extra = {}) => (
    <Field
      key={key}
      label={label}
      {...Object.fromEntries(Object.entries(extra).filter(([key]) => key !== "humanRef"))}
      value={extra.humanRef ? workflowHumanValue(s, node?.data[key]) : node?.data[key] ?? ""}
      options={options}
      type={type}
      disabled={!editing}
      onChange={(value) => change(key, value)}
    />
  );
  const configField = descriptor => {
    let options = descriptor.options ? descriptor.required ? descriptor.options : [{value:"", label:"未设置 · 不覆盖默认策略"}, ...descriptor.options] : undefined;
    if (descriptor.resource) {
      options = [{ value: "", label: descriptor.empty || "请选择" }, ...workflowResourceOptions(s, descriptor.resource)];
      const current = node?.data[descriptor.key];
      if (current && !options.some(option => option.value === current)) options.push({ value: current, label: `${current} · 引用不可用` });
    }
    return field(descriptor.label, descriptor.key, options, descriptor.type || "text", { required: Boolean(descriptor.required), help: descriptor.help, min: descriptor.min, max: descriptor.max, step: descriptor.step, humanRef: descriptor.resource === "humans" });
  };
  const humans = workflowResourceOptions(s, "humans");
  const teamMembers = [...s.organization.members.filter(person => person.kind === "Human"), ...s.agents.filter(agent => !["退役", "已退役", "待批准"].includes(agent.status)).map(agent => ({ id: `agent:${agent.id}`, name: agent.name, kind: "Agent" }))];
  const upstream = graph.edges.filter(edge => edge.target === selected);
  const downstream = graph.edges.filter(edge => edge.source === selected);
  const nodeIssues = issues.filter(issue => issue.node === selected);
  const debugKey = `${s.activeTenant}:${path}:${historical ? requestedVersion : editing ? "draft" : "published"}:${selected}`;
  const workflowSettings = { ...workflowSettingsDefaults, ...(graph.nodes.find(n => n.id === "brief")?.data.workflowSettings || {}) };
  if (workflowSettings.scenario === "Git CI/CD") workflowSettings.scenario = "构建与发布";
  if (workflowSettings.scenario === "混合流程") workflowSettings.scenario = "综合协作";
  const setting = (label, key, options, type = "text", help) => <Field key={key} label={label} value={workflowSettings[key]} options={options} type={type} help={help} disabled={!editing} onChange={value => mutate(draft => {
    const entry = draft.nodes.find(n => n.id === "brief");
    if (entry) entry.data.workflowSettings = { ...workflowSettings, [key]: value };
  })}/>;
  const selectedSource = graph.nodes.find(n => n.id === sourceNode && upstream.some(edge => edge.source === n.id));
  const sourceVersion = historical ? `v${requestedVersion}` : editing ? "draft" : def.flowVersion ? `v${def.flowVersion}` : "unpublished";
  const mapSource = () => {
    if (!selectedSource) return;
    mutate(draft => {
      const target = draft.nodes.find(n => n.id === selected);
      if (!target) return;
      target.data.inputs = `${target.data.inputs ? target.data.inputs + "\n" : ""}{{${selectedSource.id}.output}}`;
      target.data.inputVersion = `${selectedSource.id}@${editing ? "{{definition.version}}" : sourceVersion}`;
      if (target.data.kind === "部署 / 回滚" && selectedSource.data.kind === "制品归档") {
        target.data.artifactRef = `${selectedSource.id}:${selectedSource.data.artifactName || "artifact"}@${selectedSource.data.artifactVersion || sourceVersion}`;
      }
    });
    toast("已插入来源引用；发布后随流程定义版本固定，未读取实际制品。");
  };
  const effectiveTab = ({ "参数": "配置", "输入输出": "输入", "调试": "运行记录" })[tab] || tab;
  const connections = (edges, incoming) => edges.map(edge => <div className="node-connection" key={edge.id}>
    <Button onClick={() => {setSelected(incoming ? edge.source : edge.target);setSourceNode("");}}>{graph.nodes.find(item => item.id === (incoming ? edge.source : edge.target))?.data.label || "节点已不存在"}</Button>
    {!incoming && <small>{edge.sourceHandle === "true" ? "满足条件" : edge.sourceHandle === "false" ? "不满足条件" : "固定输出"}</small>}
    {editing && <IconButton name="X" label="移除此连接" onClick={() => mutate(draft => { draft.edges = draft.edges.filter(item => item.id !== edge.id); })}/>}
  </div>);
  if (unavailable) return <Empty heading="h1" title="工作流不存在" text="当前项目没有此工作流，不能用默认定义替代。"/>;
  if (historical && !definition)
    return (
      <Empty heading="h1"
        title="工作流版本不存在"
        text="此版本没有保存的定义，不能用最新版本代替。"
      />
    );
  return (
    <>
      <PageHead
        eyebrow="协作 / WORKFLOW"
        title={templateId ? "工作流模板" : workflowId === "default" ? "项目交付流程" : workflowRecordForProject(s, projectId, workflowId)?.name || "工作流"}
        subtitle={`${templateId ? s.resources.templates.find(t => t.id === templateId)?.name : s.projects.find((p) => p.id === projectId)?.name || "项目"} · ${historical ? `固定版本 v${requestedVersion}` : def.flowVersion ? `已发布 v${def.flowVersion}` : "初始模板 · 尚未发布"}`}
      >
        {!templateId && !embedded && <Link className="btn" to={`/p/${projectId}/flow?scope=${encodeURIComponent(projectId)}`}>返回工作流列表</Link>}
        <Badge>
          {historical ? "固定版本只读" : editing ? checkedDefinition === JSON.stringify(graph) ? "校验通过 · 待发布" : "草案 · 待校验" : "查看定义"}
        </Badge>
        {historical ? (
          <Button onClick={() => {embedded ? setPopupVersion(null) : location.hash = workflowURL({version:null});}}>查看最新定义</Button>
        ) : editing ? (
          <>
            <Button
              icon="Save"
              onClick={() =>
                act(() => {
                  update((x) =>
                    log(x, "保存工作流草案", path),
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
        ) : !embedded ? (
          <Button disabled={readOnly} variant="primary" icon="Pencil" onClick={begin}>
            编辑新草案
          </Button>
        ) : <Link className="btn" to={`${path}?workflow_id=${encodeURIComponent(workflowId)}&scope=${encodeURIComponent(projectId)}`} target="_blank" rel="noopener noreferrer">在新标签页编辑工作流</Link>}
      </PageHead>
      {!embedded && <div className="toolbar">
        <Button onClick={() => { const text=serializeWorkflowDefinition(graph,{version:historical?requestedVersion:def.flowVersion}); const url=URL.createObjectURL(new Blob([text],{type:"application/json"})); const a=document.createElement("a"); a.href=url;a.download="workflow-definition.json";a.click();URL.revokeObjectURL(url); }}>导出当前定义 JSON</Button>
        <Button disabled={readOnly || historical} onClick={()=>setTransfer({tenantId:s.activeTenant,text:"",baseVersion:def.flowVersion || 0,expectedDraft:JSON.stringify(def.flowDraft)})}>导入定义为草案</Button>
        {historical && <Button disabled={readOnly || s.role!=="owner"} onClick={()=>act(()=>{ if(def.flowDraft) throw Error("请先处理现有草案，避免回滚覆盖修改。"); update(x=>replaceWorkflowDraft(getMutable(x),{...structuredClone(graph),baseVersion:def.flowVersion || 0,rollbackFrom:requestedVersion},JSON.stringify(def.flowDraft))); location.hash=workflowURL({version:null});setEditing(true);toast("历史定义已建立新草案；检查后发布为新版本，不撤销外部副作用。"); })}>以此版本创建回滚草案</Button>}
      </div>}
      {transfer && <Modal title="导入工作流定义" onClose={()=>setTransfer(null)}><Notice>导入只替换草案，校验并授权发布后才形成新版本；不会复制运行状态。已有草案需明确确认替换。</Notice><Field label="工作流定义 JSON" type="textarea" value={transfer.text} onChange={text=>setTransfer({...transfer,text})}/><label>选择工作流定义文件<input aria-label="选择工作流定义文件" type="file" accept=".json,application/json" onChange={async event=>{const file=event.target.files?.[0];if(file){try{const text=await file.text();setTransfer(previous=>previous?{...previous,text}:null);}catch(error){toast(error.message,true);}}}}/></label><Button onClick={async()=>{if(def.flowDraft && !(await confirm("替换现有草案？已发布版本保持不变。")))return;act(()=>{const imported=parseWorkflowDefinition(transfer.text);const errors=validateWorkflow(imported);if(errors.length)throw Error(errors[0].message);update(x=>{if(x.activeTenant!==transfer.tenantId)throw Error("租户已变化，请重新打开导入。");replaceWorkflowDraft(getMutable(x),{...imported,baseVersion:transfer.baseVersion},transfer.expectedDraft);});setTransfer(null);setEditing(true);setCheckedDefinition(null);toast("定义已导入草案，请检查配置并发布。");});}}>确认导入草案</Button></Modal>}
      {templateId && !historical && <div className="panel">
        <Field label="AI 起草目标（本地模拟）" value={brief} onChange={setBrief} type="textarea" />
        <Button disabled={Boolean(def.flowDraft)} onClick={() => act(() => { draftWorkflowTemplate(templateId, brief); setEditing(true); toast("已生成 AI 模拟草案，请手动审阅后发布"); })}>生成 AI 模拟草案</Button>
        <Notice>{def.flowDraft?.source || "手动编辑与 AI 模拟起草共用同一画布。"} · 不调用模型，不自动发布；已有草案保留，避免覆盖手动修改。</Notice>
      </div>}
      <div className="toolbar">
        <div className="segmented">
          {[ ["canvas", "Workflow", "画布"], ["list", "List", "节点列表"] ].map(([value, icon, label]) => embedded ? <Button key={value} onClick={() => setPopupMode(value)} variant={mode === value ? "primary" : "secondary"} icon={icon}>{label}</Button> :
            <Link key={value} to={workflowURL({view:value})} className={mode === value ? "selected" : ""}><Icon name={icon}/>{label}</Link>)}
        </div>
        <span className="muted">
          {graph.nodes.length} 个节点 · {graph.edges.length} 条连接
        </span>
        <div className="grow" />
        <Select
          aria-label="已发布工作流版本"
          value={historical ? String(requestedVersion) : "latest"}
          onChange={(event) => {
            const version = event.target.value;
            setEditing(false);
            if (embedded) setPopupVersion(version === "latest" ? null : Number(version));
            else location.hash = workflowURL({version:version === "latest" ? null : version});
          }}
          options={[
            { value: "latest", label: "最新定义" },
            ...[
              ...new Set([
                ...Object.keys(def.flowVersions || {}).map(Number),
                def.flowVersion,
              ]),
            ]
              .filter((v) => v > 0)
              .sort((a, b) => b - a)
              .map((v) => ({ value: String(v), label: `固定版本 v${v}` })),
          ]}
        />
        <Button icon="Settings2" onClick={() => setInspectorMode(inspectorMode === "settings" ? "node" : "settings")}>流程设置</Button>
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
      <Notice>
        这里只保存流程定义和工作版本引用；节点测试为模拟，不代表真实执行引擎已运行。
      </Notice>
      {issues.length > 0 && (
        <div className="workflow-issues" role="alert">
          <b>发布前需处理 {issues.length} 项</b>
          {issues.map((issue, i) => (
            <button
              key={i}
              onClick={() => {
                locateIssue(issue);
              }}
            >
              <Icon name="AlertCircle" size={14} />
              {issue.message}
            </button>
          ))}
        </div>
      )}
      <div className={`flow-layout enhanced-flow ${expandedPanel ? "flow-panel-expanded" : ""}`}>
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
                setInspectorMode("node");
                setTab("配置");
              }}
              onNodeContextMenu={(e, n) => {
                e.preventDefault();
                setSelected(n.id);
                setInspectorMode("node");
                setTab("配置");
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
                  onClick={() => {setSelected(n.id); setInspectorMode("node");}}
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
          <div className="flow-inspector-controls">
            <Button variant={inspectorMode === "node" ? "primary" : "secondary"} onClick={() => setInspectorMode("node")}>节点</Button>
            <Button variant={inspectorMode === "settings" ? "primary" : "secondary"} onClick={() => setInspectorMode("settings")}>流程</Button>
            <IconButton name={expandedPanel ? "Minimize2" : "Maximize2"} label={expandedPanel ? "收窄配置面板" : "展开配置面板"} onClick={() => setExpandedPanel(!expandedPanel)}/>
          </div>
          {inspectorMode === "settings" ? <>
            <div className="node-panel-heading"><span className="eyebrow">WORKFLOW SETTINGS</span><h2>流程整体设置</h2><small>随定义版本保存 · {historical ? `v${requestedVersion}` : editing ? "草案" : `v${def.flowVersion || 0}`}</small></div>
            <Tabs label="流程设置" items={["基本信息", "运行控制", "变量与通知"].map(t => [t,t])} value={settingsTab} onChange={setSettingsTab}/>
            <Notice>{editing ? "编辑自动保存至草案；确认发布后形成固定版本。" : "当前定义只读；编辑新草案后修改。"} 配置不会启动 Runner、定时任务或通知。</Notice>
            {issues.filter(i => i.field?.startsWith("workflowSettings.")).map((i,index) => <Notice key={index} tone="warn">{i.message}</Notice>)}
            {settingsTab === "基本信息" && <>
              {setting("流程名称", "name")}
              {setting("应用场景", "scenario", ["协作交付", "构建与发布", "综合协作", "业务自动化"])}
              {setting("流程说明", "description", undefined, "textarea")}
              {setting("阶段划分说明", "stages", undefined, "textarea", "例如准备 → 执行 → 独立检查 → Human 接收；节点阶段在各节点配置中声明。")}
              <h3>触发入口</h3><p>触发方式、事件过滤和输入契约在入口节点统一配置。</p>
              <Button onClick={() => {setSelected("brief");setInspectorMode("node");setTab("配置");}}>配置触发节点</Button>
            </>}
            {settingsTab === "运行控制" && <>
              {setting("最大并行运行数", "concurrency", undefined, "number")}
              {setting("同一工作 / 分支重复触发", "duplicatePolicy", ["排队等待", "拒绝重复触发", "请求 Human 决定"])}
              {setting("并发隔离键", "concurrencyKey", undefined, "text", "例如 {{project.id}}/{{trigger.branch}}；仅定义引用，不求值。")}
              {setting("流程超时（秒）", "timeout", undefined, "number")}
              <h3>有界循环</h3><Field label="启用循环边界" value={workflowSettings.loopBoundary?.enabled ? "yes":"no"} options={[{value:"no",label:"未启用"},{value:"yes",label:"启用"}]} disabled={!editing} onChange={value=>mutate(d=>{const entry=d.nodes.find(n=>n.id==="brief");entry.data.workflowSettings={...workflowSettings,loopBoundary:{...workflowSettings.loopBoundary,enabled:value==="yes"}};})}/>
              {workflowSettings.loopBoundary?.enabled && <>{[["maxAttempts","累计尝试次数"],["maxSeconds","累计时间（秒）"],["maxCost","累计成本上限"],["exitCondition","退出条件"]].map(([key,label])=><Field key={key} label={label} value={workflowSettings.loopBoundary[key] || ""} disabled={!editing} type={key==="exitCondition"?"textarea":"number"} onChange={value=>mutate(d=>{d.nodes.find(n=>n.id==="brief").data.workflowSettings={...workflowSettings,loopBoundary:{...workflowSettings.loopBoundary,[key]:value}};})}/>)}</>}
              <details><summary>循环边界模拟验证</summary><Notice>输入尝试方案、耗时秒数、成本和退出结果，仅验证定义；不冒充真实运行停线。</Notice><Field label="尝试记录 JSON" type="textarea" value={loopProbe.text} onChange={text=>setLoopProbe({text,result:null})}/><Button onClick={()=>{try{setLoopProbe({...loopProbe,result:evaluateLoopBoundary(workflowSettings.loopBoundary,JSON.parse(loopProbe.text))});}catch(error){toast(error.message,true);}}}>验证循环边界</Button>{loopProbe.result&&<pre>{JSON.stringify(loopProbe.result,null,2)}</pre>}</details>
              {setting("新版本对进行中运行", "versionPolicy", ["保留原固定版本", "暂停后申请迁移"])}
              {setting("未知外部动作结果", "unknownEffect", ["暂停并核实，禁止自动重放", "暂停并申请补偿"])}
              {setting("配置预览记录保留天数", "retentionDays", undefined, "number")}
            </>}
            {settingsTab === "变量与通知" && <>
              {setting("流程变量（JSON）", "variables", undefined, "textarea", "非机密常量；节点用 {{vars.key}} 引用。")}
              {setting("凭据引用（JSON）", "credentialRefs", undefined, "textarea", "只保存名称到 vault/、credential/ 或 secret/ 引用的映射。")}
              {setting("状态通知", "notificationEvents", ["失败 / 阻塞 / 待审批", "开始 / 完成 / 失败 / 阻塞 / 待审批", "仅失败 / 阻塞"])}
              {setting("通知频道 ID / 目标引用", "notificationTarget", undefined, "text", "需在实际运行时核对频道权限；保存配置不等于通知已发送。")}
            </>}
          </> : <>
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
                    const record = simulateWorkflowNode(node, graph, s, new Date().toISOString(), [...validateWorkflow(workflowGraphForValidation(graph)), ...validateWorkflowSettings(graph), ...validateWorkflowCollaboration(graph)]);
                    setDebug(value => ({ ...value, [debugKey]: [record, ...(value[debugKey] || [])] }));
                    setTab("运行记录");
                  }}
                >
                  模拟预览
                </Button>
              </div>
              <Tabs className="node-panel-tabs" label="节点详情" items={["配置", "输入", "输出", "运行记录", "错误策略", "说明"].map(value => [value, value])} value={effectiveTab} onChange={setTab} />
              {nodeIssues.length > 0 && <Notice tone="warn">{nodeIssues.map((issue, index) => <p key={index}>{issue.message}</p>)}</Notice>}
              {effectiveTab === "配置" && <>
                <Notice>{historical ? `固定 v${requestedVersion}，配置只读。` : editing ? "草案自动保存。先检查配置，再由负责人发布；不会更新已有工作的固定版本。" : "当前定义只读；编辑新草案后配置节点。"}</Notice>
                {field("节点名称", "label", undefined, "text", { required: true })}
                {field("Human 负责人", "owner", [{ value: "", label: "请选择 Human" }, ...humans], "text", { required: true, humanRef: true })}
                <details className="node-advanced">
                  <summary>执行小团队 · {(node.data.executorIds || []).length} 位协作者</summary>
                  <p className="muted">负责人承担节点结果责任；可明确选择参与执行的 Human 与 Agent。</p>
                  <fieldset disabled={!editing}><legend>参与执行身份</legend>{teamMembers.map(member => <label className="check-label" key={member.id}><input type="checkbox" checked={(node.data.executorIds || []).includes(member.id)} onChange={event => change("executorIds", event.target.checked ? [...(node.data.executorIds || []), member.id] : node.data.executorIds.filter(value => value !== member.id))}/>{member.name} · {member.kind}</label>)}</fieldset>
                  {node.data.executors && <p>原定义协作说明：{node.data.executors}</p>}
                </details>
                {groupsForWorkflowNode(node).map(group => <section className="node-config-group" key={group.label}><h3>{group.label}</h3>{group.fields.map(configField)}</section>)}
                {["构建", "测试", "部署 / 回滚", "工具调用", "HTTP 请求", "源码检出"].includes(node.data.kind) && <Notice>命令和请求仅作为定义保存，不会执行。机密只填写凭据引用；不要在命令、地址或参数中放入密钥。</Notice>}
              </>}
              {effectiveTab === "输入" && <>
                <Notice>声明输入如何来自固定上游版本；编辑映射不会读取文件或解析表达式。</Notice>
                <h3>上游节点 · {upstream.length}</h3>
                {upstream.length ? connections(upstream, true) : <p className="muted">无上游；入口输入由触发配置声明。</p>}
                {upstream.length > 0 && <div className="node-source-browser">
                  <Field label="选择上游输出来源" value={selectedSource?.id || ""} options={[{value:"",label:"选择已连接的上游节点"}, ...upstream.map(edge => ({value:edge.source,label:`${graph.nodes.find(n => n.id === edge.source)?.data.label || edge.source} · ${edge.source}`}))]} onChange={setSourceNode}/>
                  {selectedSource && <><Badge>声明契约 · 无实际运行数据</Badge><p>{selectedSource.data.output || "未声明输出"}</p><pre>{selectedSource.data.outputSchema || `{{${selectedSource.id}.output}}`}</pre><small>定义来源：{selectedSource.id}@{sourceVersion}</small><Button disabled={!editing} icon="ArrowDownToLine" onClick={mapSource}>插入上游引用{node.data.kind === "部署 / 回滚" && selectedSource.data.kind === "制品归档" ? "与制品" : ""}</Button></>}
                </div>}
                {field("输入引用 / 字段映射", "inputs", undefined, "textarea", { help: "注明来源节点、字段或资料 ID，不复制私人原件。" })}
                {field("固定输入版本 / 版本映射", "inputVersion", undefined, "text", { help: "例如 source@v3 或 {{checkout.commit}}；声明输入后必填。" })}
                {field("输入缺失 / 格式要求", "inputContract", undefined, "textarea")}
                {workflowInputFields.filter(f => !f.when || f.when(node.data)).map(configField)}
              </>}
              {effectiveTab === "输出" && <>
                {field("固定输出契约", "output", undefined, "textarea", { required: true })}
                {field("输出字段结构（JSON）", "outputSchema", undefined, "textarea", {help:"可声明字段结构供下游引用；不是运行结果或自动生成的制品。"})}
                {field("必需内部验收", "gate", undefined, "textarea", { required: true })}
                {field("下游 Human 接收人", "receiver", [{ value: "", label: "由下游节点负责人接收" }, ...humans], "text", {humanRef: true})}
                <Notice>发布固定定义；运行时的实际制品版本与验收证据不由本编辑器生成。最终交付仍须独立检查及 Human 审批。</Notice>
                <h3>下游节点 · {downstream.length}</h3>
                {downstream.length ? connections(downstream, false) : <p className="muted">最终交付节点；按配置中的 Human 接收方式收尾。</p>}
              </>}
              {effectiveTab === "错误策略" && <>
                <Notice>声明未来执行的错误处理；模拟预览不会触发重试、通知或回滚。</Notice>
                {field("超时秒数", "timeout", undefined, "number", { min: 1, required: true })}
                {field("失败后", "onError", ["暂停并通知负责人", "有限重试后暂停", "终止本次流程"])}
                <details className="node-advanced" open={node.data.onError === "有限重试后暂停"}><summary>有限重试参数{node.data.onError !== "有限重试后暂停" ? " · 当前策略不启用" : ""}</summary>
                {field("最大重试次数", "retries", undefined, "number", { min: 0, step: 1, required: true })}
                {field("重试间隔（秒）", "retryDelay", undefined, "number", { min: 1, step: 1 })}
                </details>
                {workflowErrorFields.filter(f => !f.when || f.when(node.data)).map(configField)}
                {field("人工恢复 / 排查说明", "recoveryNotes", undefined, "textarea")}
              </>}
              {effectiveTab === "说明" && <>
                {field("节点所属阶段", "stageName", undefined, "text", {help:"仅分组说明，不改变节点连线与依赖顺序。"})}
                {field("节点说明 / 操作手册", "notes", undefined, "textarea")}
                {field("权限与副作用说明", "sideEffectNotes", undefined, "textarea", {help:"声明外部发信、发布、写入或部署影响，以及需要的授权。"})}
                <h3>定位与版本</h3><p>节点 {node.id} · {sourceVersion}</p><p className="muted">节点、页签及滚动位置均可用顶部「复制位置链接」分享。</p>
              </>}
              {effectiveTab === "运行记录" && <>
                <Notice>仅保存当前页面中的配置预览记录；不调用模型、Git、网络或 CI，不产生真实运行记录或正式验收证据。</Notice>
                {(debug[debugKey] || []).length ? debug[debugKey].map((record, index) => <details className="node-advanced" key={`${record.time}:${index}`} open={index === 0}>
                  <summary>{record.status} · {new Date(record.time).toLocaleTimeString()}</summary>
                  <p>{record.mode} · {record.kind}</p>
                  {record.issues.length > 0 && <Notice tone="warn">{record.issues.join("；")}</Notice>}
                  <h3>输入快照</h3><pre>{JSON.stringify(record.input, null, 2)}</pre>
                  <h3>预期输出契约</h3><pre>{JSON.stringify(record.output, null, 2)}</pre>
                  <details><summary>当时配置快照</summary><pre>{JSON.stringify(record.configuration, null, 2)}</pre></details>
                </details>) : <Empty title="尚无节点预览记录" text="点击模拟预览，检查当前配置与输入输出契约；不会执行真实任务。"/>}
              </>}
            </>
          ) : (
            <Empty title="从画布选择一个节点" />
          )}
          </>}
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
