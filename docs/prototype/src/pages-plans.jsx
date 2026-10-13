import React, { useState } from "react";
import { useStore, id, openPeriodPlan, savePeriodPlan, submitPeriodPlan, decidePeriodPlan, recordPlanFreeTask } from "./store";
import { planPeriods, planCategories, planSubject, planCanRead, planActor, planCanDecide, planChecks, planEligibleGoals, planWorkEligible, planReportFacts } from "./plans";
import { reportSubjects, dateInZone } from "./reporting";
import { PageHead, Section, Notice, Empty, Button, Badge, Field, Link, Modal, ActionForm, useRecoverableDraft } from "./ui";
const labels = { DAY: "日计划", WEEK: "周计划", MONTH: "月计划", QUARTER: "季计划", YEAR: "年计划" };
function PlanEditor({ plan, onClose, toast }) {
  const s = useStore();
  const [form, setForm, recovery] = useRecoverableDraft(`${s.activeTenant}:plan:${plan.id}`, () => ({ revision: plan.revision, items: structuredClone(plan.items), reason: "" }));
  const goals = planEligibleGoals(s, plan.subjectKey), works = s.work.filter(work => planWorkEligible(s, plan, work));
  const change = (id, key, value) => setForm(old => ({ ...old, items: old.items.map(item => item.id === id ? { ...item, [key]: value, ...(key === "goalId" ? { krId: "" } : {}) } : item) }));
  return <Modal title="编辑周期计划" wide onClose={onClose} {...recovery.modalProps}><ActionForm onCancel={onClose} onSubmit={({ dismiss }) => { savePeriodPlan(plan.id, { ...form, simulateAgent: plan.subjectKey.startsWith("agent:") }, form.revision); dismiss(); toast("计划修订已保存"); }}>
    <Notice>每项关联目标；已确认承诺变更须重新提交，原基线保留。权重默认等权 1，保存时明确确认。</Notice>
    {!goals.length && <Notice tone="warn">没有可贡献的结构化目标，请先在<Link to="/goals">目标与 KR</Link>中配置。</Notice>}
    {form.items.map((item, index) => <fieldset key={item.id}><legend>计划项 {index + 1}</legend><div className="form-grid">
      <Field label={`任务类型 ${index + 1}`} value={item.kind} onChange={v => change(item.id, "kind", v)} options={[{ value: "WORK_ITEM", label: "关联工作" }, { value: "FREE_TASK", label: "非交付任务" }]} />
      <Field label={`标题 ${index + 1}`} value={item.title} onChange={v => change(item.id, "title", v)} required />
      <Field label={`目标 ${index + 1}`} value={item.goalId} onChange={v => change(item.id, "goalId", v)} options={[{ value: "", label: "请选择可贡献目标" }, ...goals.map(goal => ({ value: goal.id, label: goal.name }))]} />
      <Field label={`KR ${index + 1}`} value={item.krId} onChange={v => change(item.id, "krId", v)} options={[{ value: "", label: "未指定 KR" }, ...(goals.find(goal => goal.id === item.goalId)?.krs || []).map(kr => ({ value: kr.id, label: kr.title }))]} />
      {item.kind === "WORK_ITEM" ? <Field label={`关联工作 ${index + 1}`} value={item.workId || ""} onChange={v => change(item.id, "workId", v)} options={[{ value: "", label: "请选择已分派工作" }, ...works.map(work => ({ value: work.id, label: `${work.id} · ${work.title}` }))]} /> : <><Field label={`任务类别 ${index + 1}`} value={item.freeCategory || "OTHER"} onChange={v => change(item.id, "freeCategory", v)} options={planCategories} /><Field label={`完成标准 ${index + 1}`} value={item.criteria || ""} onChange={v => change(item.id, "criteria", v)} required /></>}
      {[["weight", "权重", "number"], ["dueDate", "承诺日期", "date"], ["estimateHours", "预计小时", "number"], ["estimateCostMicros", "预计成本 micros", "number"], ["note", "备注", "text"]].map(([key, label, type]) => <Field key={key} label={`${label} ${index + 1}`} value={String(item[key] ?? "")} onChange={v => change(item.id, key, v)} type={type} />)}
    </div><Button type="button" onClick={() => setForm(old => ({ ...old, items: old.items.filter(candidate => candidate.id !== item.id) }))}>移除计划项 {index + 1}</Button></fieldset>)}
    <Button type="button" onClick={() => setForm(old => ({ ...old, items: [...old.items, { id: id("PI"), kind: "FREE_TASK", title: "", goalId: "", krId: "", freeCategory: "OTHER", criteria: "", weight: 1 }] }))}>添加计划项</Button>
    <Field label="变更原因" value={form.reason} onChange={reason => setForm(old => ({ ...old, reason }))} type="textarea" />
  </ActionForm></Modal>;
}
export function PlansPage({ planId, toast }) {
  const s = useStore();
  const [subjectKey, setSubject] = useState("human:H-lin"), [periodKind, setKind] = useState("WEEK"), [date, setDate] = useState(dateInZone("Asia/Singapore")), [timezone, setZone] = useState("Asia/Singapore");
  const [editing, setEditing] = useState(false), [action, setAction] = useState(null), [reason, setReason] = useState("");
  if (s.role === "external") return <Empty title="内部周期计划不可访问" />;
  const plan = planId && s.resources.plans.find(plan => plan.id === planId);
  if (planId && (!plan || !planCanRead(s, plan))) return <Empty title="计划不存在或无权访问" />;
  const attempt = (fn, message) => { try { fn(); if (message) toast(message); } catch (error) { toast(error.message, true); } };
  if (!plan) return <><PageHead title="我的周期计划" subtitle="成员按周期承诺工作，由 Human 上级确认。" /><Section title="生成或打开本期计划"><div className="form-grid">
    <Field label="计划主体" value={subjectKey} onChange={setSubject} options={reportSubjects(s).filter(subject => ["Human", "Agent"].includes(subject.kind)).map(subject => ({ value: subject.key, label: `${subject.name} · ${subject.kind}` }))} />
    <Field label="计划周期" value={periodKind} onChange={setKind} options={Object.keys(planPeriods).map(value => ({ value, label: labels[value] }))} />
    <Field label="周期日期" type="date" value={date} onChange={setDate} /><Field label="计划时区" value={timezone} onChange={setZone} />
  </div>{subjectKey.startsWith("agent:") && <Notice>由负责人类操作本地 Agent 模拟，保留实际操作人，不代表真实 Agent 已提交。</Notice>}<Button onClick={() => attempt(() => { location.hash = `/plans/${openPeriodPlan({ subjectKey, periodKind, date, timezone, simulateAgent: subjectKey.startsWith("agent:") })}`; })}>{subjectKey.startsWith("agent:") ? "生成 / 打开 Agent 模拟计划" : "生成 / 打开本人计划"}</Button></Section>
    <Section title="成员计划">{s.resources.plans.filter(plan => planCanRead(s, plan)).map(plan => <p key={plan.id}><Link to={`/plans/${plan.id}`}>{planSubject(s, plan.subjectKey)?.name || plan.subjectKey} · {labels[plan.periodKind]} · {plan.periodStart}</Link> <Badge>{plan.status}</Badge> · 修订 {plan.revision}</p>)}{!s.resources.plans.length && <Empty title="尚无周期计划" text="既有工作计划不会被自动转换为已确认周期计划。" />}</Section></>;
  const checks = planChecks(s, plan), facts = planReportFacts(s, plan), simulation = plan.subjectKey.startsWith("agent:");
  let authorProblem; try { planActor(s, plan, simulation); } catch (error) { authorProblem = error.message; }
  const decisionProblem = planCanDecide(s, plan);
  const openAction = (kind, itemId) => { setReason(""); setAction({ kind, itemId, revision: plan.revision, submissionRevision: plan.submission?.revision, contentHash: plan.submission?.contentHash }); };
  return <><PageHead title={`${planSubject(s, plan.subjectKey)?.name || plan.subjectKey} · ${labels[plan.periodKind]}`} subtitle={`${plan.periodStart} — ${plan.periodEnd} · ${plan.timezone} · 修订 ${plan.revision}`}><Badge>{plan.status}</Badge><Link className="btn" to="/plans/me">计划列表</Link></PageHead>
    {simulation && <Notice>Agent 起草与提交为本地模拟；最终确认仍由 Human 决定。</Notice>}
    <Section title="核对与上级确认"><p>确认人：{s.organization.members.find(person => person.id === checks.confirmer.id)?.name || "待配置"} · {checks.confirmer.basis || checks.confirmer.issue}</p>{[...checks.blocking, ...checks.warnings].map((issue, i) => <p key={i}>{issue.code || "提示"} · {issue.message}</p>)}
      <div className="actions"><Button disabled={Boolean(authorProblem)} onClick={() => setEditing(true)}>编辑计划项</Button>{["草稿", "已退回"].includes(plan.status) && <Button disabled={Boolean(authorProblem)} onClick={() => attempt(() => submitPeriodPlan(plan.id, plan.revision, simulation), "计划已提交")}>{simulation ? "模拟 Agent 提交计划" : "本人提交计划"}</Button>}{plan.status === "已提交" && <><Button disabled={Boolean(decisionProblem)} onClick={() => openAction("confirm")}>确认固定计划</Button><Button disabled={Boolean(decisionProblem)} onClick={() => openAction("return")}>退回计划</Button></>}</div>{authorProblem && <p>{authorProblem}</p>}{plan.status === "已提交" && decisionProblem && <p>{decisionProblem}</p>}
    </Section>
    <Section title="当前修订计划项">{plan.items.map(item => <article key={item.id} className="section"><h3>{item.title}</h3><p><Link to={`/goals/${item.goalId}`}>{s.resources.goals.find(goal => goal.id === item.goalId)?.name || "未关联目标"}</Link> · {item.krId || "未指定 KR"}</p><p>权重 {item.weight} · 日期 {item.dueDate || "未声明"} · {item.estimateHours ?? "未知"} 小时 · 成本 {item.estimateCostMicros ?? "未知"} micros</p>{item.workId ? <Link to={`/p/${s.work.find(work => work.id === item.workId)?.project}/work-items/${item.workId}`}>查看工作与责任团队 · {item.workId}</Link> : <p>完成标准：{item.criteria}</p>}{item.note && <p>{item.note}</p>}</article>)}</Section>
    <Section title="已确认基线与实际进展"><p>基线修订 {facts.basisRevision || "尚未确认"} · 完成比例 {facts.progress === null ? "未知" : `${Math.round(facts.progress * 100)}%`} · 应完成 {facts.expected === null ? "未知（缺少承诺日期或工作日历）" : `${Math.round(facts.expected * 100)}%`}</p><p>阻塞 {facts.blocked} · 未知 {facts.unknown} · 唯一完成事实 {facts.factKeys.length}</p>
      {facts.outcomes.map(item => <p key={item.itemId}>{item.title} · <Badge>{item.status}</Badge>{plan.confirmedSnapshot.items.find(original => original.id === item.itemId)?.kind === "FREE_TASK" && item.status !== "完成" && <Button disabled={Boolean(authorProblem)} onClick={() => openAction("evidence", item.itemId)}>记录完成证据 · {item.title}</Button>}</p>)}
      {plan.pendingDiff?.length > 0 && <details open><summary>与已确认基线的差异 · {plan.pendingDiff.length} 项</summary>{plan.pendingDiff.map(change => <div key={change.itemId}><strong>{change.kind} · {change.label}</strong><pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{JSON.stringify({ 原值: change.before, 新值: change.after }, null, 2)}</pre></div>)}</details>}
    </Section>
    {plan.history.length > 0 && <Section title="修订与决定历史">{plan.history.map((entry, i) => <details key={i}><summary>修订 {entry.revision} · {entry.status} · {entry.decision || "编辑"}</summary><p>{entry.reason} · {entry.actor?.kind} {entry.actor?.id} · {entry.time}</p><p>{entry.submission?.snapshot.items.map(item => item.title).join("、") || entry.items?.map(item => item.title).join("、")}</p></details>)}</Section>}
    {editing && <PlanEditor key={`${s.activeTenant}:${plan.id}`} plan={plan} toast={toast} onClose={() => setEditing(false)} />}
    {action && <Modal title={action.kind === "evidence" ? "记录完成证据" : action.kind === "confirm" ? "确认固定计划" : "退回计划"} draftValue={reason} onClose={() => setAction(null)}><ActionForm label="确认操作" onCancel={() => setAction(null)} onSubmit={({ dismiss }) => { if (action.kind === "evidence") recordPlanFreeTask(plan.id, action.itemId, { revision: action.revision, evidence: reason, simulateAgent: simulation }); else decidePeriodPlan(plan.id, { ...action, action: action.kind, reason }); dismiss(); toast("周期计划已更新"); }}><Field label={action.kind === "evidence" ? "完成证据或说明" : "确认或退回说明"} value={reason} onChange={setReason} type="textarea" required={action.kind !== "confirm"} /></ActionForm></Modal>}
  </>;
}
