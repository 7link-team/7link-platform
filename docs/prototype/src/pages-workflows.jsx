import React, { useCallback, useState } from "react";
import { DataList } from "./data-list.jsx";
import { useStore, read, update, projectFlow, id, log, requestWorkFlowChangeOn, decideWorkFlowChangeOn } from "./store";
import { PageHead, Section, Button, Badge, Field, Modal, ActionForm, Notice, Empty, Link, PanelButton, Tabs } from "./ui";
import { useViewState } from "./view-state";
import { WorkRows } from "./pages-work";
import { workflowHumanLabel } from "./workflow-node-config";
import { workflowRegistryRows, workflowRegistryReadProblem, workflowRegistryProblem, workflowRegistryCommandOn, workflowFixedGraph, workflowLinkedWorks } from "./workflow-registry";
const editorPath = (projectId, workflowId) => `/p/${encodeURIComponent(projectId)}/flow?workflow_id=${encodeURIComponent(workflowId)}`;
const workflowScenario = record => (record.flow?.flowVersion ? record.flow.flowNodes : record.flow?.flowDraft?.nodes || []).find(node=>node.id==="brief")?.data?.workflowSettings?.scenario || null;
const versions = record => [...new Set([record.flow?.flowVersion, ...Object.keys(record.flow?.flowVersions || {}).map(Number)])].filter(version => workflowFixedGraph(record, version)).sort((a,b) => b-a);
export function TenantWorkflows() {
  const s = useStore(), [filter, setFilter] = useViewState("tenant-workflows.status", "active"), [, refreshView] = useState(0);
  const tenantId=s.activeTenant, role=s.role, scope=s.project, scopeKey=`${tenantId}:${scope}:${role}`;
  const collect = useCallback(current => current.projects
    .filter(project => (current.project === "all" || current.project === project.id) && !workflowRegistryReadProblem(current, project.id))
    .flatMap(project => workflowRegistryRows(current, project.id, projectFlow(current, project.id)).map(record => ({
      ...record, id:`${encodeURIComponent(project.id)}:${encodeURIComponent(record.id)}`, workflowId:record.id,
      projectId:project.id, projectName:project.name, projectArchived:Boolean(project.archivedAt)
    }))), []);
  const rows=collect(s);
  const archived = row => Boolean(row.archivedAt || row.projectArchived);
  const columns = [
    {key:"project",label:"项目",value:row=>row.projectName,exportValue:row=>({id:row.projectId,name:row.projectName})},
    {key:"definition",label:"工作流",value:row=>row.name,exportValue:row=>({id:row.workflowId,name:row.name})},
    {key:"definitionVersion",label:"定义版本",value:row=>row.flow?.flowVersion || 0,exportValue:row=>row.flow?.flowVersion || 0,
      render:row=>row.flow?.flowVersion ? `固定 v${row.flow.flowVersion}` : "尚未发布"},
    {key:"status",label:"状态",value:row=>archived(row)?"已归档":"活动",exportValue:row=>archived(row)?"已归档":"活动",
      render:row=><Badge>{archived(row)?"已归档":"活动"}</Badge>},
    {key:"version",label:"管理修订",value:row=>row.revision,exportValue:row=>row.revision}
  ];
  const recheck = useCallback(() => {
    const current=read();
    if (current.activeTenant!==tenantId || current.role!==role || current.project!==scope)
      throw Error("工作流授权范围已变化，请重新打开列表。");
    return collect(current);
  }, [tenantId,role,scope,collect]);
  const refresh = useCallback(() => {recheck();refreshView(value=>value+1);}, [recheck]);
  const authorizeExport = useCallback(payload => {
    const current=recheck();
    if (!payload.records.every(record=>{
      const row=current.find(item=>item.id===record.id);
      return row && columns.every(column=>JSON.stringify(record[column.key])===JSON.stringify(column.exportValue(row)));
    })) throw Error("工作流或授权已变化，请刷新后重新导出。");
    return true;
  }, [recheck,columns]);
  const destination = row => `${editorPath(row.projectId,row.workflowId)}&tenant=${encodeURIComponent(tenantId)}&scope=${encodeURIComponent(row.projectId)}`;
  return <>
    <PageHead title="工作流" subtitle="查看当前租户有权访问的项目工作流；定义及固定版本仍归属原项目。" />
    <Notice>新增工作流请先在左侧选择具体项目，再从该项目工作流列表新增。这里只聚合已有定义，不创建流程或执行实例。</Notice>
    <DataList rows={rows} columns={columns} title="工作流列表" viewId="tenant-workflows" scopeKey={scopeKey} defaultSort="project"
      searchText={row=>`${row.projectName} ${row.projectId} ${row.workflowId} ${row.name} ${row.description || ""}`}
      filterRow={row=>filter==="all" || (filter==="archived" ? archived(row) : !archived(row))}
      filters={<Field label="工作流状态" value={filter} onChange={setFilter} options={[{value:"active",label:"活动"},{value:"archived",label:"已归档"},{value:"all",label:"全部"}]} />}
      onClearFilters={()=>setFilter("active")} onRefresh={refresh} offline={s.offline} onExport={authorizeExport} exportSource={`workflows:${scopeKey}`}
      renderActions={row=><><Link className="btn" to={destination(row)} target="_blank" rel="noopener noreferrer">查看 / 编辑定义</Link><Link className="btn" to={`${destination(row)}&view=runs`} target="_blank" rel="noopener noreferrer">关联任务</Link></>}
      emptyText="当前授权范围没有匹配的工作流。" />
  </>;
}
export function WorkflowList({ projectId, params = new URLSearchParams(), toast, onView, listState, onListStateChange }) {
  const s = useStore(), [localList, setLocalList] = useViewState("workflow-list", {}), [action, setAction] = useViewState("workflow-list-action", null), [menu, setMenu] = useViewState("workflow-list-menu", null), [,refreshView] = useState(0);
  const list = onListStateChange ? listState || {} : localList, filter = list.filter || "active";
  const changeList = patch => onListStateChange ? onListStateChange(patch) : setLocalList(previous => ({...previous,...patch}));
  const scopeKey = `${s.activeTenant}:${projectId}:${s.role}`;
  const tenantId = s.activeTenant, role = s.role;
  const recheckRead = useCallback(() => {
    const current = read(), problem = workflowRegistryReadProblem(current, projectId);
    if (current.activeTenant !== tenantId || current.role !== role) throw Error("工作流授权范围已变化，请重新打开列表。");
    if (problem) throw Error(problem);
    return current;
  }, [tenantId, projectId, role]);
  const refresh = useCallback(() => { recheckRead(); refreshView(previous=>previous+1); }, [recheckRead]);
  const authorizeExport = useCallback(payload => {
    const current=recheckRead(), authorized=workflowRegistryRows(current,projectId,projectFlow(current,projectId));
    if(payload.records.some(record=>!authorized.some(row=>row.id===record.id&&row.revision===record.version))) throw Error("工作流已变化，请刷新后重新导出。");
  }, [recheckRead,projectId]);
  const defaultFlow = projectFlow(s, projectId), rows = workflowRegistryRows(s, projectId, defaultFlow), problem = workflowRegistryProblem(s, projectId);
  const selectedId = params.get("workflow_id") || "default", selected = rows.find(record => record.id === selectedId), runs = params.get("view") === "runs";
  const navigate = path => onView ? onView(path) : (location.hash = path);
  const open = (kind, record) => { setMenu(null); setAction({ kind, tenantId: s.activeTenant, projectId, recordId: record?.id, revision: record?.revision, sourceId: kind === "create" ? "default" : record?.id, sourceVersion: String(versions(kind === "create" ? rows[0] : record)[0] || (kind === "create" ? 0 : "")), name: kind === "rename" ? record.name : kind === "clone" ? `${record.name} · 副本` : "", description: kind === "rename" ? record.description || "" : "", reason: "" }); };
  const readProblem = workflowRegistryReadProblem(s, projectId);
  if (readProblem) return <Empty title="工作流不可用" text={readProblem}/>;
  const project = s.projects.find(item => item.id === projectId);
  const linked = workflowLinkedWorks(s, projectId, selectedId, params.has("version") ? Number(params.get("version")) : undefined);
  return <>
    <PageHead title={runs ? `${selected?.name || "工作流"} · 关联工作` : "工作流"} subtitle={`${project.name} · 多个工作流定义，共用固定版本与工作记录`}>
      <Link className="btn" to={`/deliveries?tenant=${encodeURIComponent(s.activeTenant)}&scope=${encodeURIComponent(projectId)}`} target="_blank" rel="noopener noreferrer">交付成果与回执</Link>
      {runs && <PanelButton className="btn" to={`/p/${projectId}/flow`} onView={navigate}>返回工作流列表</PanelButton>}
    </PageHead>
    {problem && <Notice tone="warn">{problem}</Notice>}
    {runs ? !selected ? <Empty title="工作流不存在" text="不能用默认工作流代替未知身份。"/> : <>
      <div className="toolbar"><PanelButton className="btn" to={editorPath(projectId, selected.id)} onView={navigate}>查看工作流定义</PanelButton><Field label="关联的定义版本" value={params.get("version") || "all"} options={[{value:"all",label:"全部版本与未绑定工作"},...versions(selected).map(version=>({value:String(version),label:`固定 v${version}`}))]} onChange={value=>navigate(`${editorPath(projectId,selected.id)}&view=runs${value === "all" ? "" : `&version=${value}`}`)}/></div>
      <Notice>这里展示已有工作及其工作流固定版本引用，不将模拟预览或配置草案当作执行记录。</Notice>
      {linked.map(work=><WorkflowChangeControls key={`change:${work.id}`} work={work} selected={selected} projectId={projectId} toast={toast}/>)}
      {linked.length ? <Section title={`关联工作 · ${linked.length}`}><WorkRows items={linked}/>{linked.map(work=><p key={work.id}>{work.id} · {workflowFixedGraph(selected,work.flowVersion) ? `固定工作流 v${work.flowVersion}` : work.flowVersion ? `版本 v${work.flowVersion} 来源暂不可核对` : "尚未确认工作流固定版本"}</p>)}</Section> : <Empty title="暂无关联工作" text="尚无符合当前身份与版本条件的工作，不生成模拟运行填充列表。"/>}
    </> : <>
      <Notice>新增或复制先生成草案，校验发布后才可确认任务采用。导出的 JSON 是当前查询摘要，不能作为工作流定义导入；请在定义编辑器中使用「导出当前定义 JSON」及「导入定义为草案」。</Notice>
      <DataList rows={rows} viewId="workflows" scopeKey={scopeKey} title="工作流列表" queryState={list} onQueryStateChange={changeList}
        defaultSort="definition" searchText={record=>`${record.id} ${record.name} ${record.description || ""}`}
        filterRow={record=>filter==="all" || (filter==="archived" ? Boolean(record.archivedAt) : !record.archivedAt)}
        filters={<Tabs label="工作流状态" items={[["active","活动"],["archived","已归档"],["all","全部"]]} value={filter} onChange={value=>changeList({filter:value,page:1})}/>}
        onClearFilters={()=>changeList({filter:"active"})} onCreate={()=>open("create")} createLabel="新建工作流" createDisabledReason={problem}
        onRefresh={refresh} offline={s.offline} editing={Boolean(action)} onExport={authorizeExport} exportSource={`workflows:${tenantId}:${projectId}`}
        columns={[
          {key:"definition",label:"定义",value:record=>record.name,exportValue:record=>({name:record.name,description:record.description||"",tenantId,projectId,publishedVersion:record.flow?.flowVersion || null}),
            render:record=><><strong>{record.name}</strong><p className="muted">{record.id}</p><p>{record.description || "尚未填写说明"}</p>{record.legacy&&<small>默认定义仅可复制固定版本；不能改名或归档。</small>}{record.history.length>0&&<details><summary>管理记录 · {record.history.length}</summary>{record.history.map((event,index)=><p key={index}>{event.time} · {event.action} · {event.actorId} · {event.reason}{event.origin ? event.origin.kind === "INITIAL_TEMPLATE" ? " · 来源：初始模板，非已发布来源" : ` · 来源 ${event.origin.workflowId} v${event.origin.version}` : ""}</p>)}</details>}</>},
          {key:"version",label:"状态与管理修订",value:record=>record.revision,exportValue:record=>record.revision,
            render:record=><><Badge>{record.archivedAt ? "已归档" : record.flow?.flowVersion ? `已发布 v${record.flow.flowVersion}` : "尚未发布"}</Badge><p>管理修订 {record.revision}</p><small>{record.flow?.flowDraft ? "有配置草案" : "无草案"}</small></>},
          {key:"scenario",label:"业务场景",value:record=>workflowScenario(record) || "未设置",exportValue:record=>workflowScenario(record)},
          {key:"owner",label:"负责人",value:record=>workflowHumanLabel(s,record.ownerId),exportValue:record=>record.ownerId},
          {key:"works",label:"关联工作",value:record=>workflowLinkedWorks(s,projectId,record.id).length,exportValue:record=>workflowLinkedWorks(s,projectId,record.id).length}
        ]}
        renderActions={record=><><PanelButton className="btn" to={editorPath(projectId,record.id)} onView={navigate}>查看 / 编辑定义</PanelButton><Button onClick={()=>setMenu({tenantId,projectId,id:record.id})}>更多操作</Button></>}
        emptyText="没有匹配的工作流，可清空条件恢复。"/>
      {menu?.tenantId===tenantId&&menu.projectId===projectId&&<Modal title="工作流操作" onClose={()=>setMenu(null)}>{(()=>{
        const record=rows.find(row=>row.id===menu.id);
        if(!record)return <Notice tone="warn">此工作流不存在或当前不可读。</Notice>;
        return <><h2>{record.name}</h2><p>{record.id} · {record.archivedAt ? "已归档" : "活动"}</p><div className="actions">
          <PanelButton className="btn" to={`${editorPath(projectId,record.id)}&view=runs`} onView={to=>{setMenu(null);navigate(to);}}>关联工作</PanelButton>
          <Button disabled={Boolean(problem)||!versions(record).length} onClick={()=>open("clone",record)}>复制固定版本</Button>
          {!problem&&!record.archivedAt&&record.flow?.flowVersion ? <Link className="btn" to={`/chat?channel_id=${encodeURIComponent(projectId)}&workflow_id=${encodeURIComponent(record.id)}`}>在会话中发起工作</Link> : <Button disabled title={problem || (record.archivedAt ? "工作流已归档" : "请先发布工作流")}>在会话中发起工作</Button>}
          {!record.legacy&&<><Button disabled={Boolean(problem)||Boolean(record.archivedAt)} onClick={()=>open("rename",record)}>名称与说明</Button><Button disabled={Boolean(problem)||Boolean(record.archivedAt)} onClick={()=>open("archive",record)}>归档工作流</Button></>}
        </div></>;
      })()}</Modal>}
    </>}
    {action && action.tenantId === s.activeTenant && action.projectId === projectId && <WorkflowRegistryAction key={`${action.kind}:${action.recordId || "new"}`} initial={action} rows={rows} onClose={()=>setAction(null)} onSaved={workflowId=>{setAction(null);toast(action.kind === "archive" ? "工作流已归档，固定版本与关联工作保留。" : "工作流已保存。新定义仍需检查与发布。");if(["create","clone"].includes(action.kind))navigate(editorPath(projectId,workflowId));}}/>}
  </>;
}
function WorkflowRegistryAction({initial,rows,onClose,onSaved}) {
  const [form,setForm]=useState(initial), change=(key,value)=>setForm(old=>({...old,[key]:value}));
  const source = rows.find(record=>record.id===form.sourceId);
  const initialTemplate = form.kind === "create" && source?.flow?.flowVersion === 0;
  return <Modal title={{create:"新建工作流",clone:"复制固定版本",rename:"名称与说明",archive:"归档工作流"}[form.kind]} onClose={onClose} draftValue={form}><ActionForm label={form.kind === "archive" ? "确认归档" : "保存"} onCancel={onClose} onSubmit={({dismiss})=>{
    let result;update(state=>{result=workflowRegistryCommandOn(state,{...form,action:form.kind,id:["rename","archive"].includes(form.kind)?form.recordId:undefined,sourceVersion:Number(form.sourceVersion)},{defaultFlow:projectFlow(state,form.projectId),makeId:id});log(state,`工作流管理 · ${form.kind}`,editorPath(form.projectId,result));});dismiss();onSaved(result);
  }}>
    {form.kind !== "archive" && <><Field label="工作流名称" value={form.name} onChange={value=>change("name",value)} required/><Field label="工作流说明" value={form.description} onChange={value=>change("description",value)} type="textarea"/></>}
    {["create","clone"].includes(form.kind)&&<><Field label="复制的固定来源版本" value={form.sourceVersion} options={[...(initialTemplate ? [{value:"0",label:"初始模板 · 非已发布来源"}] : [{value:"",label:"请选择已发布版本"}]),...versions(source).map(version=>({value:String(version),label:`${source.name} · v${version}`}))]} onChange={value=>change("sourceVersion",value)} required/><Notice>{initialTemplate ? "当前项目尚无已发布默认版本；使用现有初始模板建立首个草案，不是复制发布快照。" : "只复制所选固定定义为新草案，不复制运行记录、接受事实或未发布修改。"}新工作流尚未发布，不创建运行或接受事实。</Notice></>}
    {form.kind === "archive" && <Notice tone="warn">归档停止将此工作流用于新工作；原定义、历史版本与已关联工作继续保留，不取消正在执行的工作。</Notice>}
    <Field label="操作理由" value={form.reason} onChange={value=>change("reason",value)} type="textarea" required/>
  </ActionForm></Modal>;
}

function WorkflowChangeControls({work,selected,projectId,toast}) {
  const s=useStore(),[form,setForm]=useState(null);
  const command=fn=>{try{update(fn);setForm(null);toast("变更记录已保存；原固定版本和执行记录保留。");}catch(error){toast(error.message,true);}};
  return <details><summary>{work.id} · 运行工作流变更</summary><Notice>申请对比固定定义并记录生效边界。批准后仍待运行执行方核实并应用；原型不改写运行版本。</Notice><Button disabled={!work.flowVersion || ["已完成","已取消"].includes(work.status)} onClick={()=>setForm({tenantId:s.activeTenant,projectId,workId:work.id,baseVersion:work.flowVersion,targetVersion:"",reason:"",boundary:""})}>申请版本变更</Button>
    {(work.flowChangeRequests || []).map(request=><section key={request.id}><p>v{request.fromVersion} → v{request.toVersion} · {request.status}</p><p>{request.reason}</p><p>生效边界：{request.boundary}</p><details><summary>定义补丁 · {request.patch.length} 项</summary><pre>{JSON.stringify(request.patch,null,2)}</pre></details>{request.decision&&<p>{request.decision.actor} · {request.decision.reason}</p>}{request.status==="待审批"&&s.role==="owner"&&<Button onClick={()=>setForm({tenantId:s.activeTenant,projectId,workId:work.id,requestId:request.id,reason:"",decision:"reject"})}>审批变更</Button>}</section>)}
    {form&&<Modal title={form.requestId?"审批运行变更":"申请运行变更"} onClose={()=>setForm(null)} draftValue={form}><ActionForm label={form.requestId?"记录审批结论":"提交变更申请"} onCancel={()=>setForm(null)} onSubmit={()=>command(state=>form.requestId?decideWorkFlowChangeOn(state,form):requestWorkFlowChangeOn(state,{...form,targetVersion:Number(form.targetVersion)}))}>
      {form.requestId?<Field label="审批结论" value={form.decision} onChange={decision=>setForm({...form,decision})} options={[{value:"reject",label:"拒绝"},{value:"approve",label:"批准迁移方案（待应用）"}]}/>:<><Field label="目标固定版本" value={form.targetVersion} onChange={targetVersion=>setForm({...form,targetVersion})} options={[{value:"",label:"请选择"},...versions(selected).filter(v=>v!==work.flowVersion).map(v=>({value:String(v),label:`v${v}`}))]}/><Field label="生效边界及已执行节点保护" type="textarea" value={form.boundary} onChange={boundary=>setForm({...form,boundary})} required/></>}
      <Field label="理由" type="textarea" value={form.reason} onChange={reason=>setForm({...form,reason})} required/>
    </ActionForm></Modal>}
  </details>;
}
