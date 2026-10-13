import React, { useState, useEffect, useCallback, useRef } from "react";
import { createRoot } from "react-dom/client";
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
  MarkerType,
} from "@xyflow/react";
import "./editor.css";
import '../workflow-definition.js';
const {kinds,defaults,clone}=globalThis.WorkflowDefinition;
const query = new URLSearchParams(location.search),
  domain = query.get("domain") || "SOFTWARE",
  readonly = query.get("readonly") === "1",
  project = query.get("project") || "p1";
const key = "prototype-workflow-" + domain;
const seed=()=>globalThis.WorkflowDefinition.seed(domain);
function load() {
  try {
    const stored = JSON.parse(localStorage.getItem(key));
    if (
      stored?.definition?.schema === "WorkflowDef" &&
      stored.definition.schema_version === 1
    )
      return stored;
  } catch {}
  const definition = seed();
  return { definition, history: [clone(definition)], patches: [] };
}
const validate = def => globalThis.WorkflowDefinition.validate(def, domain);
function patchOperations(before, after, action) {
  const operations=[];
  for (const field of ['name','parameters','entry_node_id','output_bindings']) {
    if(JSON.stringify(before[field])!==JSON.stringify(after[field])) operations.push({op:'update_workflow',field,before:clone(before[field]),value:clone(after[field])});
  }
  for (const [field,kind] of [['nodes','node'],['edges','edge']]) {
    const previous=new Map(before[field].map(item=>[item.id,item]));
    const current=new Map(after[field].map(item=>[item.id,item]));
    for (const [id,item] of previous) if (!current.has(id)) operations.push({op:'remove_'+kind,id,before:clone(item)});
    for (const [id,item] of current) {
      if (!previous.has(id)) operations.push({op:'add_'+kind,id,value:clone(item)});
      else if (JSON.stringify(previous.get(id))!==JSON.stringify(item)) operations.push({op:'update_'+kind,id,before:clone(previous.get(id)),value:clone(item)});
    }
  }
  // React Flow has already moved the in-memory node before drag-end fires.
  if (action.op==='set_position' && !operations.some(op=>op.id===action.id)) {
    const moved=after.nodes.find(n=>n.id===action.id);
    if(moved) operations.push({op:'update_node',id:moved.id,value:clone(moved)});
  }
  return operations;
}
function FlowNode({ data, selected }) {
  return (
    <div className={"node " + (selected ? "selected" : "")}>
      <Handle type="target" position={Position.Left} />
      <small>{kinds[data.kind]}</small>
      <strong>{data.label}</strong>
      {data.status && (
        <div className={"status " + (data.status === "失败" ? "failed" : "")}>
          {data.status}
        </div>
      )}
      {data.workId && <small>{data.workId}</small>}
      <Handle type="source" position={Position.Right} />
    </div>
  );
}
const nodeTypes = { workflow: FlowNode };
function Editor() {
  const dragBefore = useRef(null), importInput = useRef(null);
  const [undoStack,setUndoStack]=useState([]), [redoStack,setRedoStack]=useState([]);
  const [stored, setStored] = useState(load),
    [definition, setDefinition] = useState(() => load().definition),
    [selected, setSelected] = useState(null),
    [edgeSelected, setEdgeSelected] = useState(null),
    [message, setMessage] = useState(
      "拖动节点；从右侧圆点拖至目标左侧圆点连线。",
    ),
    [issues, setIssues] = useState([]),
    [configText, setConfigText] = useState(""),
    [proposal, setProposal] = useState(null),
    [approved, setApproved] = useState(false),
    [scope, setScope] = useState("template"),
    [prompt, setPrompt] = useState("在交付前增加团队复核关口"),
    [instance, setInstance] = useState(null),
    [jsonText, setJsonText] = useState(""),
    [workFilter, setWorkFilter] = useState(""),
    [dirty, setDirty] = useState(false),
    [patches, setPatches] = useState(() => load().patches || []);
  const node = definition.nodes.find((n) => n.id === selected),
    edge = definition.edges.find((e) => e.id === edgeSelected);
  const update = useCallback(
    (next, patch) => {
      next = {
        ...next,
        edges: next.edges.map((e) => ({
          source_port: e.branch || e.source_port || "out",
          target_port: "in",
          ...e,
        })),
      };
      const before=patch.op==='set_position' && dragBefore.current ? dragBefore.current : definition;
      const operations=patchOperations(before,next,patch);
      if(!operations.length && !['undo','redo'].includes(patch.op)) return;
      if (!['undo','redo'].includes(patch.op)) {
        setUndoStack(stack=>[...stack,clone(before)].slice(-100));
        setRedoStack([]);
      }
      dragBefore.current=null;
      setDefinition(next);
      setDirty(true);
      setApproved(false);
      setPatches((p) => [
        ...p,
        {
          schema: "WorkflowPatch",
          base_version: definition.version,
          base_hash: "prototype-simulated-hash",
          at: new Date().toISOString(),
          author: "demo-human · 本地模拟操作者",
          reason: "本地交互编辑（模拟）：" + patch.op,
          operations,
          action: patch,
        },
      ]);
    },
    [definition],
  );
  useEffect(() => {
    setConfigText(node ? JSON.stringify(node.config, null, 2) : "");
  }, [selected]);
  useEffect(() => {
    if (!readonly) return;
    function receive(event) {
      if (
        event.source !== parent ||
        event.origin !== location.origin ||
        event.data?.type !== "workflow-instance"
      )
        return;
      setInstance(event.data);
    }
    window.addEventListener("message", receive);
    parent.postMessage(
      { type: "workflow-instance-request", project },
      location.origin,
    );
    const timer = setInterval(
      () =>
        parent.postMessage(
          { type: "workflow-instance-request", project },
          location.origin,
        ),
      2000,
    );
    return () => {
      clearInterval(timer);
      window.removeEventListener("message", receive);
    };
  }, []);
  const visible =
    readonly && instance?.definition ? instance.definition : definition;
  const selectedWork = workFilter || instance?.items?.[0]?.id || '';
  const selectedRun = instance?.node_runs?.find(r => r.work_item_id === selectedWork && r.node_id === selected);
  const visibleNodes = visible.nodes;
  const nodes = visibleNodes.map((n) => ({
    id: n.id, type: 'workflow', position: n.display.position,
    data: {label:n.label,kind:n.kind,status:readonly ? instance?.node_runs?.find(r=>r.work_item_id===selectedWork&&r.node_id===n.id)?.status || 'NOT_RUN' : undefined,workId:readonly ? selectedWork : undefined},
    selected:n.id===selected,
  }));
  const edges = visible.edges
    .filter(
      (e) =>
        visibleNodes.some((n) => n.id === e.source) &&
        visibleNodes.some((n) => n.id === e.target),
    )
    .map((e) => ({
      ...e,
      markerEnd: { type: MarkerType.ArrowClosed },
      selected: e.id === edgeSelected,
    }));
  function connectionPorts(source) {
    const isCondition=definition.nodes.find(n=>n.id===source)?.kind==='CONDITION';
    const branch=isCondition ? (definition.edges.some(e=>e.source===source&&(e.branch||e.source_port)==='true') ? 'false' : 'true') : undefined;
    return {source_port:branch || 'out',target_port:'in',...(branch ? {branch,label:branch} : {})};
  }
  function restore(direction) {
    const stack=direction==='undo' ? undoStack : redoStack;
    if(!stack.length)return;
    const snapshot=stack.at(-1);
    if(direction==='undo') {setUndoStack(stack.slice(0,-1));setRedoStack(items=>[...items,clone(definition)]);}
    else {setRedoStack(stack.slice(0,-1));setUndoStack(items=>[...items,clone(definition)]);}
    update({...clone(snapshot),version:definition.version,status:'draft'},{op:direction});
    setSelected(null);setEdgeSelected(null);setMessage('已'+(direction==='undo'?'撤销':'重做')+'（本地快照）；仍须重新人工审核。');
  }
  async function importDefinition(file) {
    if(!file)return;
    if(file.size>2*1024*1024){setMessage('导入被阻止：原型JSON文件上限2 MiB。');if(importInput.current)importInput.current.value='';return;}
    try {
      const imported=JSON.parse(await file.text()),errors=validate(imported);
      setIssues(errors);
      if(errors.length){setMessage('导入被阻止：定义校验失败。');return;}
      if(imported.workflow_id!==definition.workflow_id){setMessage('导入被阻止：须为当前领域同一workflow_id。');return;}
      update({...clone(imported),version:definition.version,status:'draft'},{op:'import_definition'});
      setMessage('JSON已导入为草稿；发布历史保持独立，仍须人工审核。');
    } catch {setMessage('导入失败：无法解析WorkflowDef JSON。');}
    finally {if(importInput.current)importInput.current.value='';}
  }
  function exportDefinition() {
    const url=URL.createObjectURL(new Blob([JSON.stringify(visible,null,2)],{type:'application/json'}));
    const anchor=document.createElement('a');anchor.href=url;anchor.download='workflow-'+visible.version+'.json';anchor.click();URL.revokeObjectURL(url);
  }
  function save() {
    const errors = validate(definition);
    setIssues(errors);
    if (errors.length) {
      setMessage("保存被阻止：请修复校验问题。");
      return;
    }
    const next = {
      ...stored,
      definition: { ...definition, status: "draft" },
      patches,
    };
    try {
      localStorage.setItem(key, JSON.stringify(next));
      setStored(next);
      setDefinition(next.definition);
      setDirty(false);
      setMessage("草稿已保存到此浏览器；已发布版本与进行中项目保持锁定。");
    } catch {
      setMessage("本地存储不可用，未保存；可复制 JSON。");
    }
  }
  function publish() {
    const errors = validate(definition);
    setIssues(errors);
    if (errors.length || !approved) {
      setMessage("须先通过校验，并由人类完成草稿审核。");
      return;
    }
    if (scope === "project") {
      try {
        localStorage.setItem(
          "prototype-workflow-project-" + project,
          JSON.stringify({
            ...definition,
            status: "project_override",
            project,
          }),
        );
        setMessage(
          "仅当前项目流程已记录（模拟）；既有实例不自动改图，重新拆解需裁决。",
        );
        setApproved(false);
        setProposal(null);
      } catch {
        setMessage("存储不可用，未发布。");
      }
      return;
    }
    const versions = stored.history.map(
        (h) => Number(h.version.split(".")[2]) || 0,
      ),
      version = "1.0." + (Math.max(...versions) + 1),
      published = { ...clone(definition), version, status: "published", published_at: new Date().toISOString() },
      next = {
        definition: published,
        history: [...stored.history, published],
        patches,
      };
    try {
      localStorage.setItem(key, JSON.stringify(next));
      setStored(next);
      setDefinition(published);
      setApproved(false);
      setProposal(null);
      setDirty(false);
      setMessage("已模拟发布 " + version + "；进行中项目仍锁定原版本。");
    } catch {
      setMessage("存储不可用，未发布。");
    }
  }
  function add(type, position) {
    if (definition.nodes.length >= 200) {
      setMessage("单图已达200节点，请使用子流程分层。");
      return;
    }
    const id = "n-" + Date.now();
    update(
      {
        ...definition,
        status: "draft",
        nodes: [
          ...definition.nodes,
          {
            id,
            kind: type,
            input_bindings: {},
            output_bindings: {},
            label: kinds[type],
            display: {
              position: position || {
                x: 120,
                y: 120 + definition.nodes.length * 8,
              },
            },
            config: clone(defaults[type]),
          },
        ],
      },
      { op: "add_node", id, type },
    );
    setSelected(id);
    setEdgeSelected(null);
  }
  function draft() {
    const id = "agent-" + Date.now(),
      target =
        definition.nodes.find(
          (n) =>
            n.kind === "HUMAN_GATE" &&
            n.config.audience === "client" &&
            n.id !== "n2",
        ) || definition.nodes.at(-1),
      incoming = definition.edges.find((e) => e.target === target.id);
    const newNode = {
      id,
      kind: "HUMAN_GATE",
      input_bindings: {},
      output_bindings: {},
      label: "Agent 建议 · " + (prompt.trim().slice(0, 32) || "团队复核"),
      display: {
        position: {
          x: target.display.position.x - 110,
          y: target.display.position.y + 160,
        },
      },
      config: clone(defaults.HUMAN_GATE),
    };
    setProposal({
      node: newNode,
      incoming,
      target: target.id,
      prompt,
      source: "协调员 Agent · 共享频道需求确认后（模拟）",
    });
    setApproved(false);
    setMessage("模拟生成结构化图补丁；尚未修改流程。");
  }
  function apply() {
    const p = proposal,
      newEdges = p.incoming
        ? definition.edges
            .filter((e) => e.id !== p.incoming.id)
            .concat(
              { ...p.incoming, target: p.node.id },
              { id: "e-" + p.node.id, source: p.node.id, target: p.target },
            )
        : definition.edges.concat({
            id: "e-" + p.node.id,
            source: p.node.id,
            target: p.target,
          });
    update(
      {
        ...definition,
        status: "draft",
        nodes: [...definition.nodes, p.node],
        edges: newEdges,
      },
      {
        op: "agent_graph_patch",
        node: p.node,
        replaceEdge: p.incoming?.id,
        source: p.source,
      },
    );
    setProposal(null);
    setMessage("已由你采纳图补丁；保存、审核后才可发布。");
  }
  return (
    <>
      <div className="toolbar">
        <strong>
          {readonly ? "项目流程实例 · 只读" : "WorkflowDef · " + domain}{" "}
          <small className="muted">开发 v1.1 / 原型模拟</small>
        </strong>
        {!readonly && (
          <>
            <button
              onClick={() => {
                update(
                  { ...clone(stored.history.at(-1)), status: "draft" },
                  { op: "create_draft" },
                );
                setMessage("已从最新发布版本创建可编辑草稿。");
              }}
            >
              创建草稿
            </button>
            <button
              onClick={() => {
                const e = validate(definition);
                setIssues(e);
                setMessage(
                  e.length
                    ? "校验未通过"
                    : "校验通过：静态 DAG、节点配置及人工关口有效。",
                );
              }}
            >
              校验
            </button>
            <button onClick={save}>保存草稿</button>
            <button onClick={()=>restore('undo')} disabled={!undoStack.length}>撤销</button>
            <button onClick={()=>restore('redo')} disabled={!redoStack.length}>重做</button>
            <button onClick={()=>importInput.current.click()} title="原型文件上限2 MiB，单图最多200节点">导入 JSON · ≤2 MiB</button>
            <input ref={importInput} type="file" accept="application/json,.json" aria-label="导入 WorkflowDef JSON" hidden onChange={e=>importDefinition(e.target.files[0])}/>
            <button className="primary" onClick={publish} disabled={!approved}>
              发布新版本
            </button>
          </>
        )}
        {readonly && (
          <select
            aria-label="工作项实例筛选"
            value={selectedWork}
            onChange={(e) => setWorkFilter(e.target.value)}
          >
            {!instance?.items?.length && <option value="">暂无工作项实例</option>}
            {(instance?.items || []).map(item=><option key={item.id} value={item.id}>{item.id}</option>)}
          </select>
        )}
        <span className="muted">
          {readonly
            ? (instance?.projectName || project) + ' · 锁定 ' + visible.version
            : definition.version +
              " · " +
              definition.status +
              (dirty ? " · 未保存" : "")}
        </span>
        <button onClick={exportDefinition}>导出 JSON</button>
      </div>
      <div className="notice" role="status">
        {readonly
          ? instance?.error || "投影项目锁定的已发布 WorkflowDef；逐工作项查看节点运行事实（最多40项）。未绑定执行事实的节点为 NOT_RUN；状态每2秒同步。"
          : message}
      </div>
      <div className="layout">
        <aside className="palette">
          <b>{readonly ? "图例" : "添加节点"}</b>
          {Object.entries(kinds).map(([type, label]) =>
            readonly ? (
              <p key={type} className="muted">
                {label}
              </p>
            ) : (
              <button
                key={type}
                draggable
                onDragStart={(e) =>
                  e.dataTransfer.setData("workflow-node", type)
                }
                onClick={() => add(type)}
              >
                {label}
              </button>
            ),
          )}
          <p className="muted">
            外层静态 DAG；循环在节点内部有界执行。Trigger 定时 / webhook /
            云效事件：v2。
          </p>
        </aside>
        <div
          className="canvas"
          onDragOver={(e) => e.preventDefault()}
          onDrop={(e) => {
            e.preventDefault();
            if (readonly) return;
            const type = e.dataTransfer.getData("workflow-node");
            if (kinds[type]) add(type, { x: 120, y: 120 });
          }}
        >
          <ReactFlow
            nodes={nodes}
            edges={edges}
            nodeTypes={nodeTypes}
            defaultViewport={{ x: 30, y: 110, zoom: 0.8 }}
            onlyRenderVisibleElements
            minZoom={0.15}
            nodesDraggable={!readonly}
            nodesConnectable={!readonly}
            elementsSelectable
            onNodeClick={(_, n) => {
              setSelected(n.id);
              setEdgeSelected(null);
            }}
            onEdgeClick={(_, e) => {
              setEdgeSelected(e.id);
              setSelected(null);
            }}
            onNodesChange={(changes) => {
              if (readonly) return;
              const next = applyNodeChanges(changes, nodes);
              const removed = definition.nodes.filter(
                (n) => !next.some((x) => x.id === n.id),
              );
              if (
                removed.some(
                  (n) => n.kind === "HUMAN_GATE" && n.config.required,
                )
              ) {
                setMessage("必经人工关口不可删除。");
                return;
              }
              const updated = definition.nodes
                .filter((n) => next.some((x) => x.id === n.id))
                .map((n) => ({
                  ...n,
                  display: {
                    position: next.find((x) => x.id === n.id).position,
                  },
                }));
              if (changes.some((c) => c.type === "position")) {
                setDefinition((d) => ({ ...d, nodes: updated }));
                setDirty(true);
              }
              if (changes.some((c) => c.type === "remove"))
                update(
                  {
                    ...definition,
                    nodes: updated,
                    edges: definition.edges.filter(
                      (e) =>
                        updated.some((n) => n.id === e.source) &&
                        updated.some((n) => n.id === e.target),
                    ),
                  },
                  { op: "remove_nodes", ids: removed.map((n) => n.id) },
                );
            }}
            onNodeDragStop={(_, n) => {
              if (!readonly)
                update(
                  {
                    ...definition,
                    nodes: definition.nodes.map((x) =>
                      x.id === n.id
                        ? { ...x, display: { position: n.position } }
                        : x,
                    ),
                  },
                  { op: "set_position", id: n.id, position: n.position },
                );
            }}
            onNodeDragStart={()=>{if(!readonly)dragBefore.current=clone(definition);}}
            onEdgesChange={(changes) => {
              if (!readonly && changes.some((c) => c.type === "remove"))
                update(
                  {
                    ...definition,
                    edges: applyEdgeChanges(changes, edges).map(
                      ({ markerEnd, selected, ...e }) => e,
                    ),
                  },
                  { op: "edge_changes", changes },
                );
            }}
            onConnect={(connection) => {
              if (!readonly)
                update(
                  {
                    ...definition,
                    edges: addEdge(
                      { ...connection, ...connectionPorts(connection.source), id: 'e-' + Date.now() },
                      definition.edges,
                    ),
                  },
                  { op: "connect", ...connection },
                );
            }}
            deleteKeyCode={readonly ? null : ["Backspace", "Delete"]}
          >
            <Background />
            <Controls showInteractive={!readonly} />
            <MiniMap />
          </ReactFlow>
        </div>
        <aside className="props">
          <b>{readonly ? "实例节点详情" : "节点 / 连线属性"}</b>
          <label>
            按名称选择节点
            <select
              aria-label="按名称选择节点"
              value={selected || ""}
              onChange={(e) => {
                setSelected(e.target.value);
                setEdgeSelected(null);
              }}
            >
              <option value="">选择节点</option>
              {visible.nodes.map((n) => (
                <option key={n.id} value={n.id}>
                  {n.label}
                  {n.workId ? " · " + n.workId : ""}
                </option>
              ))}
            </select>
          </label>
          {readonly ? (
            <>
              {instance?.definition?.nodes.find((n) => n.id === selected) ? (
                <>
                  <p>{visible.nodes.find(n=>n.id===selected)?.label}</p>
                  <p>{selectedRun?.status || 'NOT_RUN'}</p>
                  <p className="muted">{selectedRun?.source || '无节点运行绑定'} · {selectedRun?.execution || '无执行记录'} · 轮次 {selectedRun?.round || 0}</p>
                  {selectedWork && <button onClick={()=>parent.postMessage({type:'workflow-open-work',id:selectedWork},location.origin)}>查看工作项与证据</button>}
                </>
              ) : (
                <p className="muted">选择节点查看当前项目状态。</p>
              )}
            </>
          ) : node ? (
            <>
              <p className="muted">
                {node.id} · {kinds[node.kind]}
              </p>
              <div className="row">
                {["左", "右", "上", "下"].map((label, i) => (
                  <button
                    key={label}
                    aria-label={"移动节点" + label}
                    onClick={() =>
                      update(
                        {
                          ...definition,
                          nodes: definition.nodes.map((n) =>
                            n.id === node.id
                              ? {
                                  ...n,
                                  display: {
                                    position: {
                                      x:
                                        n.display.position.x +
                                        [-20, 20, 0, 0][i],
                                      y:
                                        n.display.position.y +
                                        [0, 0, -20, 20][i],
                                    },
                                  },
                                }
                              : n,
                          ),
                        },
                        { op: "move_node", id: node.id, direction: label },
                      )
                    }
                  >
                    {label}
                  </button>
                ))}
              </div>
              <label>
                名称
                <input
                  value={node.label}
                  onChange={(e) =>
                    update(
                      {
                        ...definition,
                        nodes: definition.nodes.map((n) =>
                          n.id === node.id
                            ? { ...n, label: e.target.value }
                            : n,
                        ),
                      },
                      { op: "set_label", id: node.id, value: e.target.value },
                    )
                  }
                />
              </label>
              <label>
                类型配置 JSON
                <textarea
                  aria-label="类型配置 JSON"
                  value={configText}
                  onChange={(e) => setConfigText(e.target.value)}
                />
              </label>
              <button
                onClick={() => {
                  try {
                    const config = JSON.parse(configText);
                    if (
                      !config ||
                      Array.isArray(config) ||
                      typeof config !== "object"
                    )
                      throw Error();
                    update(
                      {
                        ...definition,
                        nodes: definition.nodes.map((n) =>
                          n.id === node.id ? { ...n, config } : n,
                        ),
                      },
                      { op: "set_config", id: node.id, config },
                    );
                    setMessage("节点配置已应用，请校验并保存。");
                  } catch {
                    setMessage("配置必须为有效 JSON 对象。");
                  }
                }}
              >
                应用属性
              </button>
              <p className="muted">{JSON.stringify(defaults[node.kind])}</p>
              {node.kind === "SUBFLOW" && (
                <details>
                  <summary>按需查看子流程引用</summary>
                  <p>{node.config.workflowRef}</p>
                  <p className="muted">
                    以独立版本定义加载，不内联扩张父图。原型引用示例：平台检查 →
                    测试 → 产品验收 → 人工接受；未连接真实子流程注册表。
                  </p>
                </details>
              )}
              <label>
                连至节点
                <select
                  aria-label="连至节点"
                  defaultValue=""
                  onChange={(e) => {
                    if (!e.target.value) return;
                    update(
                      {
                        ...definition,
                        edges: addEdge(
                          {
                            id: "e-" + Date.now(),
                            source: node.id,
                            ...connectionPorts(node.id),
                            target: e.target.value,
                          },
                          definition.edges,
                        ),
                      },
                      {
                        op: "connect",
                        source: node.id,
                        target: e.target.value,
                      },
                    );
                    e.target.value = "";
                  }}
                >
                  <option value="">选择连线目标</option>
                  {definition.nodes
                    .filter((n) => n.id !== node.id)
                    .map((n) => (
                      <option value={n.id} key={n.id}>
                        {n.label}
                      </option>
                    ))}
                </select>
              </label>
              <button
                disabled={node.kind === "HUMAN_GATE" && node.config.required}
                onClick={() => {
                  update(
                    {
                      ...definition,
                      nodes: definition.nodes.filter((n) => n.id !== node.id),
                      edges: definition.edges.filter(
                        (e) => e.source !== node.id && e.target !== node.id,
                      ),
                    },
                    { op: "remove_node", id: node.id },
                  );
                  setSelected(null);
                }}
              >
                删除节点
              </button>
              {node.kind === "HUMAN_GATE" && (
                <p className="muted">
                  必经人工关口不可删除；audience：team 团队 / client 甲方。
                </p>
              )}
            </>
          ) : edge ? (
            <>
              <p>
                {edge.source} → {edge.target}
              </p>
              <label>
                分支标签
                <input
                  value={edge.label || ""}
                  onChange={(e) =>
                    update(
                      {
                        ...definition,
                        edges: definition.edges.map((x) =>
                          x.id === edge.id
                            ? {
                                ...x,
                                label: e.target.value,
                              }
                            : x,
                        ),
                      },
                      { op: "set_edge", id: edge.id, label: e.target.value },
                    )
                  }
                />
              </label>
              <button
                onClick={() => {
                  update(
                    {
                      ...definition,
                      edges: definition.edges.filter((e) => e.id !== edge.id),
                    },
                    { op: "remove_edge", id: edge.id },
                  );
                  setEdgeSelected(null);
                }}
              >
                删除连线
              </button>
            </>
          ) : (
            <p className="muted">
              选择节点编辑名称、JSON
              Logic、数据来源、轮次、映射、子流程引用；选择连线编辑分支标签。
            </p>
          )}
        </aside>
      </div>
      {issues.length > 0 && (
        <div className="foot issues" role="alert">
          {issues.map((e, i) => (
            <div key={i}>{e}</div>
          ))}
        </div>
      )}
      {!readonly && (
        <div className="review">
          <b>Agent 草稿 → 人审核 → 发布（全程本地模拟）</b>
          <label>
            流程需求
            <input
              style={{ width: "100%", padding: 8, marginTop: 5 }}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
            />
          </label>
          <div className="row">
            <button onClick={draft}>模拟 Agent 起草图补丁</button>
            <select
              aria-label="发布范围"
              value={scope}
              onChange={(e) => {
                setScope(e.target.value);
                setApproved(false);
              }}
            >
              <option value="template">模板新版本</option>
              <option value="project">仅当前项目 {project}</option>
            </select>
            <select
              aria-label="版本历史"
              className="versions"
              defaultValue=""
              onChange={(e) => {
                if (!e.target.value) return;
                const old = stored.history.find(
                  (h) => h.version === e.target.value,
                );
                setMessage(
                  "版本差异：当前 " +
                    definition.nodes.length +
                    " 节点 / " +
                    definition.edges.length +
                    " 连线；" +
                    old.version +
                    " 为 " +
                    old.nodes.length +
                    " / " +
                    old.edges.length +
                    "。查看下方版本 JSON。",
                );
                setJsonText(JSON.stringify(old, null, 2));
              }}
            >
              <option value="">版本差异</option>
              {stored.history.map((h) => (
                <option key={h.version}>{h.version}</option>
              ))}
            </select>
          </div>
          {proposal && (
            <>
              <p>{proposal.source}</p>
              <pre className="patch">
                {JSON.stringify(
                  {
                    op: "insert_node",
                    node: proposal.node,
                    replaceEdge: proposal.incoming?.id,
                    target: proposal.target,
                  },
                  null,
                  2,
                )}
              </pre>
              <div className="row">
                <button onClick={apply}>人工采纳图补丁</button>
                <button
                  onClick={() => {
                    setProposal(null);
                    setMessage("已拒绝 Agent 草稿，流程未修改。");
                  }}
                >
                  拒绝草稿
                </button>
              </div>
            </>
          )}
          <label>
            <input
              type="checkbox"
              checked={approved}
              disabled={!!proposal}
              onChange={(e) => setApproved(e.target.checked)}
            />{" "}
            我已人工审核节点、连线与关口；同意所选发布范围
          </label>
          <p className="muted">
            运行期重新拆解提交图补丁与裁决，不能自动修改已锁定实例；Agent
            不是发布人。
          </p>
        </div>
      )}
      <div className="foot">
        <details>
          <summary>
            唯一真相 WorkflowDef JSON / 结构化补丁 ({patches.length})
          </summary>
          <p className="muted">
            画布是 JSON
            投影；节点布局、属性和连线变更记录为补丁。仅本浏览器持久化，不连接生产服务。
          </p>
          <textarea
            className="json"
            aria-label="WorkflowDef JSON"
            value={jsonText || JSON.stringify(visible, null, 2)}
            readOnly
          />
          <button onClick={() => setJsonText("")}>显示当前定义</button>
          <pre className="patch">
            {JSON.stringify(patches.slice(-10), null, 2)}
          </pre>
        </details>
      </div>
    </>
  );
}
createRoot(document.getElementById("root")).render(<Editor />);
