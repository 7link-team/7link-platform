import { formatTimestamp } from "./formatting";
import React, { useState } from "react";
import { useStore, saveTestPlan, startTestBatch, recordBatchResult, publishTestReport } from "./store";
import { activeBatch, qaCounts, qaStatuses } from "./qa";
import { PageHead, Section, Button, Link, Badge, Notice, Empty, Modal, ActionForm, Field, useRecoverableDraft } from "./ui";

export function QAResourcePage({ kind, recordId, toast }) {
  const state = useStore();
  const [editing, setEditing] = useState(null);
  const [running, setRunning] = useState(null);
  const rows = state.resources[kind] || [];
  const record = rows.find(item => item.id === recordId);
  const isReport = kind === "testreports";
  if (state.role === "external") return <Empty title="当前视角无权访问" />;
  if (recordId && !record) return <Empty title="记录不存在" />;
  const perform = fn => { try { fn(); } catch (error) { toast(error.message); } };
  const batch = record && !isReport ? activeBatch(record) : null;
  return <>
    <PageHead eyebrow={isReport ? "TEST REPORT" : "TEST PLAN"} title={record?.name || (isReport ? "测试报告" : "测试计划")}
      subtitle={isReport ? "固定计划与逐项结果快照，后续改版不会改写旧报告。" : "固定用例修订、目标构建与环境，逐项记录独立验证。"}>
      <Link className="btn" to={isReport ? "/testplans" : "/tests"}>{isReport ? "测试计划" : "测试用例"}</Link>
      {!isReport && <Link className="btn" to="/testreports">测试报告</Link>}
      {record && <Link className="btn" to={`/${kind}`}>返回列表</Link>}
      {!isReport && state.role === "owner" && <Button onClick={() => setEditing(record || {})}>{record ? "修订测试计划" : "新建测试计划"}</Button>}
    </PageHead>
    {!record ? <div className="stack">{rows.length ? rows.map(item => <Link className="resource-card" to={`/${kind}/${item.id}`} key={item.id}>
      <div className="grow"><h2>{item.name}</h2><p>{isReport ? `${item.batchId} · ${formatTimestamp(item.recordedAt)}` : `修订 ${item.revision || 1} · ${item.environment} · ${item.caseRefs?.length || 0} 个固定用例`}</p></div><Badge>{item.status}</Badge>
    </Link>) : <Empty title={isReport ? "暂无测试报告" : "暂无测试计划"} />}</div> : isReport ? <>
      <Notice>不可变报告 · {formatTimestamp(record.recordedAt)} · {record.actor}。其中模拟运行均明确标记，不代表真实工具执行。</Notice>
      <Link to={`/testplans/${record.planId}`}>打开来源测试计划</Link>
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
          <Link to={`/tests/${ref.caseId}`}>{ref.snapshot.name}</Link><p>{ref.caseId} · 用例修订 {ref.caseRevision} · {ref.snapshot.work} / {ref.snapshot.version}</p>
          <p>固定期望：{ref.snapshot.expected}</p><p>条款：{ref.snapshot.requirementRefs?.join("；") || "未关联，请编辑用例明确选择"}</p>
        </div><Badge>必需</Badge></div>)}
        <Button onClick={() => perform(() => { startTestBatch(record.id); toast("已打开固定执行批次"); })}>{batch?.status === "进行中" ? "继续执行批次" : "开始新执行批次"}</Button>
      </Section>
      {batch && <Section title={`当前执行批次 · ${batch.id}`}>
        <BatchView batch={batch} onRecord={batch.status === "进行中" ? (ref) => setRunning({ ref, planId: record.id, batchId: batch.id }) : undefined} />
        {batch.status === "进行中" && <Button onClick={() => perform(() => { const reportId = publishTestReport(record.id); window.location.hash = `/testreports/${reportId}`; })}>形成固定测试报告</Button>}
        {batch.reportId && <Link className="btn" to={`/testreports/${batch.reportId}`}>查看固定测试报告</Link>}
      </Section>}
      {record.batches?.filter(item => item.id !== batch?.id).length > 0 && <Section title="历史批次与报告">
        {record.batches.filter(item => item.id !== batch?.id).map(item => <details key={item.id}><summary>{item.id} · 计划修订 {item.planRevision} · {item.status}</summary>
          <BatchView batch={item} />{item.reportId && <Link to={`/testreports/${item.reportId}`}>查看固定测试报告</Link>}
        </details>)}
      </Section>}
      {record.history?.length > 0 && <Section title="计划修订历史">{record.history.map((item, index) => <details key={index}><summary>修订 {item.revision} · {item.changeReason || "原计划"}</summary><PlanFacts plan={item} /><p>{item.caseRefs?.map(ref => `${ref.caseId} r${ref.caseRevision}`).join("；")}</p></details>)}</Section>}
    </>}
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
          <dt>关联缺陷</dt><dd>{run.defect ? <Link to={`/defects/${run.defect}`}>{run.defect}</Link> : "无"}</dd>
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
