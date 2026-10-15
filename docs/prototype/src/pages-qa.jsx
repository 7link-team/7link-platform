import { formatTimestamp } from "./formatting";
import { useViewState } from "./view-state";
import React, { useCallback, useState } from "react";
import { useStore, read, saveTestPlan, startTestBatch, recordBatchResult, publishTestReport } from "./store";
import { DataList } from "./data-list";
import { activeBatch, qaCounts, qaStatuses } from "./qa";
import { PageHead, Section, Button, Link, Badge, Notice, Empty, Modal, ActionForm, Field, useRecoverableDraft } from "./ui";

export function qaPlanProjectIds(state, plan) {
  return [...new Set((plan?.caseRefs || []).map(ref=>state.work.find(work=>work.id===ref.snapshot?.work)?.project).filter(Boolean))];
}
export function qaRowsInProject(state, rows, kind) {
  return state.project==="all" ? rows : rows.filter(item=>qaPlanProjectIds(state,kind==="testreports" ? item.planSnapshot : item).includes(state.project));
}
export function QAResourcePage({ kind, recordId, toast }) {
  const state = useStore();
  const [reportPreview,setReportPreview]=useViewState("qa.report","");
  const [editing, setEditing] = useState(null);
  const [running, setRunning] = useState(null);
  const [,refreshView] = useState(0);
  const tenantId=state.activeTenant, role=state.role, projectId=state.project;
  const recheckRead=useCallback(()=>{
    const current=read();
    if(current.activeTenant!==tenantId || current.role!==role || current.project!==projectId || current.role==="external") throw Error("测试计划访问范围已变化，请重新打开列表。");
    return current;
  },[tenantId,role,projectId]);
  const refresh=useCallback(()=>{recheckRead();refreshView(value=>value+1);},[recheckRead]);
  const authorizeExport=useCallback(payload=>{
    const current=recheckRead();
    const authorized=qaRowsInProject(current,current.resources.testplans,"testplans");
    if(payload.records.some(item=>!authorized.some(plan=>plan.id===item.id && (plan.revision || 1)===item.version))) throw Error("测试计划或项目范围已变化，请刷新后重新导出。");
  },[recheckRead]);
  const records = state.resources[kind] || [];
  const rows = qaRowsInProject(state,records,kind);
  const record = records.find(item => item.id === recordId);
  const isReport = kind === "testreports";
  const recordProjects=record ? qaPlanProjectIds(state,isReport ? record.planSnapshot : record) : [];
  if (state.role === "external") return <Empty title="当前视角无权访问" />;
  if (recordId && !record) return <Empty title="记录不存在" />;
  const perform = fn => { try { fn(); } catch (error) { toast(error.message); } };
  const batch = record && !isReport ? activeBatch(record) : null;
  return <>
    <PageHead eyebrow={isReport ? "TEST REPORT" : "TEST PLAN"} title={record?.name || (isReport ? "测试报告" : "测试计划")}
      subtitle={isReport ? "固定计划与逐项结果快照，后续改版不会改写旧报告。" : "固定用例修订、目标构建与环境，逐项记录独立验证。"}>
      <Link className="btn" to={isReport ? "/testplans" : "/tests"} target="_blank" rel="noopener noreferrer">{isReport ? "测试计划" : "测试用例"}</Link>
      {record && <Link className="btn" to={isReport?`/testplans/${record.planId}`:`/${kind}`} {...(isReport ? {target:"_blank",rel:"noopener noreferrer"} : {})}>{isReport?"打开所属测试计划":"返回列表"}</Link>}
      {!isReport && record && state.role === "owner" && <Button onClick={() => setEditing(record)}>修订测试计划</Button>}
    </PageHead>
    {record && <Notice>固定计划关联项目：{recordProjects.map(id=>state.projects.find(project=>project.id===id)?.name || id).join("、") || "暂不可核对"}。此处展示完整固定范围，不随当前项目筛选改写；跨项目计划保留全部用例。</Notice>}
    {!record && !isReport ? <DataList rows={rows} viewId="qa-plans" scopeKey={`${tenantId}:${role}:${projectId}`} title="测试计划列表" defaultSort="name"
      searchText={plan=>`${plan.id} ${plan.name} ${plan.scope || ""} ${plan.environment || ""} ${plan.targetBuild || ""} ${plan.owner || ""}`}
      onCreate={role==="owner" ? ()=>setEditing({}) : undefined} createLabel="新建测试计划" createDisabledReason={state.offline ? "当前离线，不能创建测试计划" : ""}
      onRefresh={refresh} offline={state.offline} editing={Boolean(editing)} onExport={authorizeExport} exportSource={`testplans:${tenantId}:${projectId}`}
      columns={[
        {key:"name",label:"测试计划",value:plan=>plan.name,exportValue:plan=>plan.name,render:plan=><><strong>{plan.name}</strong><p className="muted">{plan.id}</p><p>{plan.scope}</p></>},
        {key:"version",label:"计划修订",value:plan=>plan.revision || 1,exportValue:plan=>plan.revision || 1},
        {key:"status",label:"状态",value:plan=>plan.status,exportValue:plan=>plan.status || null,render:plan=><Badge>{plan.status || "未记录"}</Badge>},
        {key:"environment",label:"环境与构建",value:plan=>plan.environment,exportValue:plan=>({environment:plan.environment || null,targetBuild:plan.targetBuild || null}),render:plan=><>{plan.environment || "未记录"}<p>{plan.targetBuild || "尚未指定构建"}</p></>},
        {key:"owner",label:"负责人",value:plan=>plan.owner,exportValue:plan=>plan.owner || null},
        {key:"cases",label:"固定用例数",value:plan=>plan.caseRefs?.length || 0,exportValue:plan=>plan.caseRefs?.length || 0}
      ]}
      renderActions={plan=><Link className="btn" to={`/testplans/${encodeURIComponent(plan.id)}`} target="_blank" rel="noopener noreferrer">新标签页查看计划</Link>}
      emptyText="没有匹配的测试计划，可清空搜索或新建计划。"/> : !record ? <div className="stack">{rows.length ? rows.map(item => <Link className="resource-card" to={`/${kind}/${item.id}`} key={item.id}>
      <div className="grow"><h2>{item.name}</h2><p>{isReport ? `${item.batchId} · ${formatTimestamp(item.recordedAt)}` : `修订 ${item.revision || 1} · ${item.environment} · ${item.caseRefs?.length || 0} 个固定用例`}</p></div><Badge>{item.status}</Badge>
    </Link>) : <Empty title={isReport ? "暂无测试报告" : "暂无测试计划"} />}</div> : isReport ? <>
      <Notice>不可变报告 · {formatTimestamp(record.recordedAt)} · {record.actor}。其中模拟运行均明确标记，不代表真实工具执行。</Notice>
      <Link to={`/testplans/${record.planId}`} target="_blank" rel="noopener noreferrer">打开来源测试计划</Link>
      <PlanFacts plan={record.planSnapshot} />
      <BatchView batch={record.batchSnapshot} />
    </> : <>
      <PlanFacts plan={record} />
      {batch?.status === "进行中" && <Notice>修订或替换计划前，请先形成固定测试报告结束当前批次；尚未执行的项会单独保留，之后再明确重验。</Notice>}
      {record.migrationNote && <Notice tone="warn">{record.migrationNote}</Notice>}
      <Section title={`固定用例 · 计划修订 ${record.revision || 1}`}>
        <Notice>修订计划将全部所选用例标为重验；旧批次与报告完整保留。用例修改不会静默替换此处固定版本。</Notice>
        {record.changeReason && <p>变更影响与重验范围：{record.changeReason}</p>}
        {record.caseRefs?.map(ref => <div className="resource-card" key={ref.caseId}><div className="grow">
          <Link to={`/tests/${ref.caseId}`} target="_blank" rel="noopener noreferrer">{ref.snapshot.name}</Link><p>{ref.caseId} · 用例修订 {ref.caseRevision} · {ref.snapshot.work} / {ref.snapshot.version}</p>
          <p>固定期望：{ref.snapshot.expected}</p><p>条款：{ref.snapshot.requirementRefs?.join("；") || "未关联，请编辑用例明确选择"}</p>
        </div><Badge>必需</Badge></div>)}
        <Button onClick={() => perform(() => { startTestBatch(record.id); toast("已打开固定执行批次"); })}>{batch?.status === "进行中" ? "继续执行批次" : "开始新执行批次"}</Button>
      </Section>
      {batch && <Section title={`当前执行批次 · ${batch.id}`}>
        <BatchView batch={batch} onRecord={batch.status === "进行中" ? (ref) => setRunning({ ref, planId: record.id, batchId: batch.id }) : undefined} />
        {batch.status === "进行中" && <Button onClick={() => perform(() => { const reportId = publishTestReport(record.id); setReportPreview(reportId); })}>形成固定测试报告</Button>}
        {batch.reportId && <Button onClick={()=>setReportPreview(batch.reportId)}>查看固定测试报告</Button>}
      </Section>}
      {record.batches?.filter(item => item.id !== batch?.id).length > 0 && <Section title="历史批次与报告">
        {record.batches.filter(item => item.id !== batch?.id).map(item => <details key={item.id}><summary>{item.id} · 计划修订 {item.planRevision} · {item.status}</summary>
          <BatchView batch={item} />{item.reportId && <Button onClick={()=>setReportPreview(item.reportId)}>查看固定测试报告</Button>}
        </details>)}
      </Section>}
      {record.history?.length > 0 && <Section title="计划修订历史">{record.history.map((item, index) => <details key={index}><summary>修订 {item.revision} · {item.changeReason || "原计划"}</summary><PlanFacts plan={item} /><p>{item.caseRefs?.map(ref => `${ref.caseId} r${ref.caseRevision}`).join("；")}</p></details>)}</Section>}
    </>}
    {!isReport&&reportPreview&&<Modal title="测试执行报告" wide onClose={()=>setReportPreview("")} headerActions={<Link className="btn" to={`/testreports/${reportPreview}`} target="_blank" rel="noopener noreferrer">新标签页打开报告</Link>}><QAResourcePage kind="testreports" recordId={reportPreview} toast={toast}/></Modal>}
    {editing && <PlanEditor key={`${state.activeTenant}:${editing.id || "new"}`} record={editing} state={state} onClose={() => setEditing(null)} onSaved={planId => { setEditing(null); window.location.hash = `/testplans/${planId}`; toast("已固定计划修订，所选用例需要重新执行"); }} />}
    {running && <RunEditor key={`${state.activeTenant}:${running.planId}:${running.batchId}:${running.ref.caseId}`} running={running} onClose={() => setRunning(null)} onSaved={() => { setRunning(null); toast("已记录逐项模拟结果"); }} />}
  </>;
}
function PlanFacts({ plan }) {
  return <Section title="固定计划范围"><dl className="record-fields">
    <dt>范围</dt><dd>{plan.scope}</dd><dt>目标提交 / 构建</dt><dd>{plan.targetBuild || "未记录，开始前请修订补充"}</dd>
    <dt>环境</dt><dd>{plan.environment}</dd><dt>负责人 / 分派</dt><dd>{plan.owner} / {plan.assignment || "未记录"}</dd>
    <dt>执行窗口</dt><dd>{plan.windowStart || "未记录"} — {plan.windowEnd || "未记录"}</dd>
    <dt>完成条件</dt><dd>{plan.criterion}</dd>
  </dl></Section>;
}
function BatchView({ batch, onRecord }) {
  const counts = qaCounts(batch.items);
  return <div className="stack" data-qa-batch={batch.id}>
    {onRecord && <Notice>每批每项只记录一次结果。需要重验时，请先形成固定测试报告结束本批次，再开始新执行批次；原失败不会被覆盖。</Notice>}
    <p data-qa-counts>{qaStatuses.map(status => `${status} ${counts[status]}`).join(" · ")}</p>
    <p className="muted">{batch.environment} · {batch.targetBuild} · {batch.status}</p>
    {batch.caseRefs.map(ref => {
      const item = batch.items.find(item => item.caseId === ref.caseId);
      const run = item?.run;
      return <article className="section" key={ref.caseId} data-qa-case={ref.caseId}>
        <div className="split"><h3>{ref.snapshot.name} · r{ref.caseRevision}</h3><Badge>{item?.status || "未执行"}</Badge></div>
        <p>固定期望：{ref.snapshot.expected}</p><p>前置：{ref.snapshot.preconditions || "旧用例未记录"}</p><p>步骤：{ref.snapshot.steps || ref.snapshot.method}</p>
        {run && <dl className="record-fields"><dt>来源 / 主体</dt><dd>{run.sourceKind} / {run.owner} · 本地模拟</dd>
          <dt>实际结果</dt><dd>{run.actual}</dd><dt>证据</dt><dd>{run.evidence}</dd><dt>工具版本</dt><dd>{run.toolVersion}</dd>
          <dt>日志</dt><dd>{run.logs || "未附加"}</dd><dt>记录时间</dt><dd>{formatTimestamp(run.time)}</dd>
          <dt>关联缺陷</dt><dd>{run.defect ? <Link to={`/defects/${run.defect}`} target="_blank" rel="noopener noreferrer">{run.defect}</Link> : "无"}</dd>
        </dl>}
        {onRecord && !item?.runId && <Button onClick={() => onRecord(ref)}>记录结果 · {ref.caseId}</Button>}
        {onRecord && item?.runId && <p className="muted">本项已固定；结束本批次后可开始新批次重验。</p>}
      </article>;
    })}
  </div>;
}
function PlanEditor({ record, state, onClose, onSaved }) {
  const [form, setForm, draft] = useRecoverableDraft(JSON.stringify([state.activeTenant, "qa-plan", record.id || "new"]), () => ({ ...record, owner: record.owner || "林予", caseIds: record.caseRefs?.map(ref => ref.caseId) || [], changeReason: "" }), value => ({ ...value, caseIds: [...value.caseIds].sort() }));
  const fields = [["name", "计划名称"], ["scope", "测试范围"], ["targetBuild", "目标提交 / 构建"], ["environment", "执行环境"], ["assignment", "分派主体"], ["windowStart", "执行开始"], ["windowEnd", "执行结束"], ["criterion", "完成条件"]];
  return <Modal title={record.id ? "修订测试计划" : "新建测试计划"} {...draft.modalProps} onClose={onClose} wide><ActionForm onCancel={onClose} label="固定计划修订" onSubmit={({ dismiss }) => { const planId = saveTestPlan(form); dismiss(() => onSaved(planId)); }}>
    {draft.recovered && <Notice>已恢复未保存的计划修改，并保留原始修订。若计划已变化，请放弃后重新打开。</Notice>}
    <div className="form-grid">{fields.map(([key, label]) => <Field key={key} label={label} value={form[key] || ""} onChange={value => setForm({ ...form, [key]: value })} type={key.startsWith("window") ? "date" : "text"} required />)}</div>
    <fieldset><legend>固定用例修订（全部为必需重验项）</legend>{state.resources.tests.map(test => <label className="check-label" key={test.id}>
      <input type="checkbox" checked={form.caseIds.includes(test.id)} onChange={event => setForm({ ...form, caseIds: event.target.checked ? [...form.caseIds, test.id] : form.caseIds.filter(id => id !== test.id) })} />
      {test.id} · {test.name} · r{test.revision || 1} · {test.version}
    </label>)}</fieldset>
    {record.id && <Field label="变更影响与重验范围" value={form.changeReason} onChange={value => setForm({ ...form, changeReason: value })} required />}
    <Notice>固定当前选择的用例内容和条款。修改计划不删除旧批次，也不迁移旧通过结果；所选用例全部重验。</Notice>
  </ActionForm></Modal>;
}
function RunEditor({ running, onClose, onSaved }) {
  const state = useStore();
  const definition = running.ref.snapshot;
  const [form, setForm, draft] = useRecoverableDraft(JSON.stringify([state.activeTenant, "qa-result", running.planId, running.batchId, running.ref.caseId]), () => ({ status: "通过", actual: "", evidence: "", sourceKind: definition.sourceKind || "平台独立验证", ownerKind: definition.sourceKind === "Human 手测" ? "Human" : "Agent", ownerId: "", toolVersion: "", logs: "" }));
  return <Modal title={`记录逐项结果 · ${running.ref.caseId}`} {...draft.modalProps} onClose={onClose}><ActionForm label="保存逐项结果" onCancel={onClose} onSubmit={({ dismiss }) => { recordBatchResult(running.planId, running.batchId, running.ref.caseId, form); dismiss(onSaved); }}>
    {draft.recovered && <Notice>已恢复此固定批次、此用例的未保存结果，不会迁移到其他批次。</Notice>}
    <Notice>本次是本地模拟。通过、失败、跳过和阻塞分别记录；Agent 自测不能满足独立验证门禁。</Notice>
    <p>固定期望：{definition.expected}</p>
    <Field label="执行结论" options={qaStatuses.filter(status => status !== "未执行")} value={form.status} onChange={value => setForm({ ...form, status: value })} />
    <Field label="验证来源" options={["Human 手测", "Agent 自测", "平台独立验证"]} value={form.sourceKind} onChange={value => setForm({ ...form, sourceKind: value, ownerKind: value === "Human 手测" ? "Human" : "Agent", ownerId: "" })} />
    <Field label="运行主体" options={[{ value: "", label: "请选择明确身份" }, ...(form.ownerKind === "Human" ? state.organization.members.filter(person => person.kind === "Human") : state.agents.filter(agent => !["退役", "待批准"].includes(agent.status))).map(person => ({ value: person.id, label: `${person.name} · ${form.ownerKind} · ${person.id}` }))]} value={form.ownerId} onChange={value => setForm({ ...form, ownerId: value })} />
    {[["actual", "实际结果 / 跳过或阻塞原因"], ["evidence", "证据引用"], ["toolVersion", "工具版本（手测填写不适用）"], ["logs", "日志引用（选填）"]].map(([key, label]) => <Field key={key} label={label} value={form[key]} onChange={value => setForm({ ...form, [key]: value })} required={key !== "logs"} />)}
  </ActionForm></Modal>;
}
