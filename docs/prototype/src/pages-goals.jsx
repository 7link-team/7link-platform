import { GoalCollaboration, GoalQuadrants } from "./goal-collaboration";
import React, { useState } from "react";
import { useStore, id, saveStructuredGoal, goalProgress, goalWriteProblem, goalTransitionProblem, resourceCommand,
  recordGoalObservation, refreshGoalMetrics, checkGoalRisks, goalPeriodCommand, calibrateGoalProgress } from "./store";
import { goalSubjects, goalPeriods, goalMetrics, goalPeriod, structuredGoal, conversionDraft, goalResult, goalRisks,
  closedGoalStates, evidenceHref, visibleGoals, goalReadProblem, goalReviewers, goalTypes, goalActionProblem } from "./goals";
import { PageHead, Section, Field, Button, Badge, Link, Progress, Notice, Empty, Modal, ActionForm, useRecoverableDraft } from "./ui";
import { formatTimestamp } from "./formatting";

const blankKR = () => ({ id: id("KR"), title: "", unit: "", direction: "INCREASE", baseline: "", target: "", weight: 1, source: "MANUAL", metric: "", evidenceRequired: false });
const people = state => [...state.organization.members.filter(item => item.kind === "Human").map(item => ({ ...item, key: `Human:${item.id}` })), ...state.agents.map(item => ({ ...item, kind: "Agent", key: `Agent:${item.id}` }))];
const formatProgress = value => value === null ? "数据不完整" : `${Math.round(value * 10000) / 100}%`;
export function GoalsPage({ recordId, toast, legacyPage }) {
  const s = useStore(), record = s.resources.goals.find(goal => goal.id === recordId);
  const [editor, setEditor] = useState(null), [observation, setObservation] = useState(null), [action, setAction] = useState(null);
  const [view,setView] = useState("list"), [periodFilter,setPeriodFilter] = useState(""), [typeFilter,setTypeFilter] = useState("");
  const [query, setQuery] = useState(""), [subject, setSubject] = useState(""), [status, setStatus] = useState("");
  const run = fn => { try { fn(); } catch (error) { toast(error.message); } };
  if (s.role === "external") return <Empty title="当前视角无权访问目标" />;
  if (recordId && record && goalReadProblem(s,record)) return <Empty title="目标不在当前授权范围" text="个人私密目标和项目目标仅按明确范围开放。" />;
  if (recordId && !record) return <Empty title="目标不存在" />;
  const create = () => setEditor({ collaborationVersion:1, tenantId:s.activeTenant, goalType:"HUMAN", timeMode:"NATURAL", stageName:"", reviewerId:"H-lin", visibility:"PRIVATE", collaborators:[], beneficiaries:"", value:"", resources:"", name: "", owner: s.organization.members.find(person => person.id === "H-lin")?.name || "", ownerId: "H-lin", ownerKind: "Human", subjectKey: "human:H-lin", periodKind: "QUARTER", periodStart: "", periodEnd: "", timezone: "", krs: [blankKR()], alignments: [], riskPolicy: {} });
  const permission = record && goalWriteProblem(s, record), closed = record && closedGoalStates.includes(record.status);
  const result = record && goalResult(record), risks = record && structuredGoal(record) ? goalRisks(s, record) : [];
  const rows = visibleGoals(s).filter(goal => (!query || `${goal.name} ${goal.owner}`.includes(query)) && (!subject || goal.subjectKey === subject) && (!status || goal.status === status) && (!periodFilter || goal.periodKind===periodFilter) && (!typeFilter || goal.goalType===typeFilter));
  return <>
    {!record && <>
      <PageHead eyebrow="对齐方向 · 观察结果" title="目标管理" subtitle="定义业务结果，记录观察证据；交付接受与目标达成分别核对。"><Button type="button" variant="primary" onClick={create} icon="Plus">新建目标</Button></PageHead>
      <div className="form-grid"><Field label="搜索目标" value={query} onChange={setQuery} /><Field label="目标归属筛选" value={subject} onChange={setSubject} options={[{ value: "", label: "全部归属" }, ...goalSubjects(s).map(item => ({ value: item.key, label: `${item.kind} · ${item.name}` }))]} /><Field label="目标状态筛选" value={status} onChange={setStatus} options={[{ value: "", label: "全部状态" }, ...["草稿", "待审核", "进行中", "已达成", "未达成", "已取消"].map(value => ({ value, label: value }))]} /></div>
      <div className="form-grid"><Field label="周期筛选" value={periodFilter} onChange={setPeriodFilter} options={[{value:"",label:"全部周期"},...Object.entries(goalPeriods).map(([value,label])=>({value,label}))]} /><Field label="目标类型筛选" value={typeFilter} onChange={setTypeFilter} options={[{value:"",label:"全部类型"},...Object.entries(goalTypes).map(([value,label])=>({value,label}))]} /><Field label="目标视图" value={view} onChange={setView} options={[{value:"list",label:"列表"},{value:"quadrants",label:"非个人四象限"}]} /></div>
      {view==="quadrants" ? <GoalQuadrants state={s} rows={rows} /> : <div className="resource-grid cards">{rows.map(goal => {
        const value = structuredGoal(goal) ? goalResult(goal).progress : goalProgress(goal) === null ? null : goalProgress(goal) / 100;
        return <Link key={goal.id} to={`/goals/${goal.id}`} className="resource-card"><div><Badge>{goal.status}</Badge><h2>{goal.name}</h2><p>{goal.owner} · {goal.periodStart ? `${goal.periodStart} — ${goal.periodEnd}` : goal.cycle}</p>{value === null ? <Badge tone="amber">{structuredGoal(goal) ? "数据不完整" : "进度待校准"}</Badge> : <><Progress value={value * 100} /><p>{structuredGoal(goal) ? "公式进度" : "历史手工进度"} {formatProgress(value)}</p></>}<p>{structuredGoal(goal) ? `${goal.krs.length} 个 KR · ${goal.alignments.length} 项对齐` : "旧标量记录 · 待结构化"}</p></div></Link>;
      })}</div>}
      {!s.resources.goals.length && <Empty title="还没有目标" text="先明确归属与周期，再添加可观察的关键结果。" />}
    </>}
    {record && !structuredGoal(record) && <><Notice>此记录保留旧标量与历史。转换时须明确数值、单位、周期和归属；旧进度不会自动成为观察证据。</Notice><Button type="button" disabled={Boolean(permission)} title={permission || ""} onClick={() => setEditor(conversionDraft(s, record, id))}>转换为 Objective / KR</Button>{legacyPage}</>}
    {record && structuredGoal(record) && <>
      <PageHead eyebrow={`${record.id} · 定义 r${record.definitionRevision}`} title={record.name} subtitle={`${record.owner} · ${record.periodStart} — ${record.periodEnd} · ${record.timezone}`}>
        <Link className="btn" to="/goals">返回目标</Link><Button type="button" disabled={closed || Boolean(goalWriteProblem(s, record, true))} title={goalWriteProblem(s, record, true) || ""} onClick={() => setEditor(structuredClone(record))}>编辑目标定义</Button>
      </PageHead>
      {record.collaborationVersion && <GoalCollaboration state={s} goal={record} />}
      <Section title="目标进度" extra={<Badge>{record.status}</Badge>}>
        <strong>{formatProgress(result.progress)}</strong>{result.progress !== null && <Progress value={result.progress * 100} />}
        <p>可计算权重 {Math.round(result.coverage * 100)}% · 父目标只计算自身 KR，不叠加子目标进度。</p>
        {result.issues.map(issue => <p key={issue} className="muted">{issue}</p>)}
        <Button type="button" disabled={closed || Boolean(permission)} onClick={() => setAction({ type: "assessment", next: "assessment", label: "记录人工进度判断", goal: structuredClone(record) })}>记录人工进度判断</Button>
        {record.manualAssessment && <Notice>人工判断 {record.manualAssessment.progress}% · {record.manualAssessment.reason}。公式进度仍为 {formatProgress(result.progress)}，人工判断不替代达成证据。</Notice>}
        {record.draftOrigin && <p className="muted">草案来源：Agent {record.draftOrigin.agentId || "未指定"} 的本地模拟建议，由 Human 确认；未调用真实模型。</p>}
        {record.legacy && <details><summary>查看转换前的标量记录</summary><p>{record.legacy.note}</p><pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{JSON.stringify(record.legacy.scalar, null, 2)}</pre></details>}
      </Section>
      {result.results.map(({ kr, value, progress, observation: current, issue }, index) => <Section key={kr.id} title={`KR ${index + 1} · ${kr.title}`} extra={<Badge>{kr.source === "MANUAL" ? "人工观察" : "自动事实"}</Badge>}>
        {kr.kind === "QUALITATIVE" ? <><p>约定交付：{kr.delivery}</p><p>预期作用：{kr.use}</p><p>评审标准：{kr.criteria} · 截止 {kr.dueDate}</p><p>证据约定：{kr.evidencePlan}</p><p>当前判断：{value === "MET" ? "证据表明达成" : value === "NOT_MET" ? "尚未达到标准" : "未知"}</p></> : <p>{kr.direction === "INCREASE" ? "增长" : "降低"} · 基线 {kr.baseline ?? "待复核"} → 当前 {value ?? "未知"} → 目标 {kr.target ?? "待复核"} {kr.unit} · 权重 {kr.weight}</p>}
        <p>{formatProgress(progress)}{issue ? ` · ${issue}` : ""}</p>
        {kr.kind !== "QUALITATIVE" && <p className="muted">{kr.direction === "INCREASE" ? "(当前 − 基线) ÷ (目标 − 基线)" : "(基线 − 当前) ÷ (基线 − 目标)"}，结果限制在 0–100%。</p>}
        {current && <><p>{current.reason} · {current.source} · 观察 {formatTimestamp(current.observedAt)}</p><EvidenceList references={current.evidence || []} /></>}
        {kr.source === "MANUAL" && <Button type="button" disabled={closed || Boolean(permission)} onClick={() => setObservation({ goal: structuredClone(record), kr: structuredClone(kr) })}>记录观察 · KR {index + 1}</Button>}
        <details><summary>观察历史 · {kr.observations?.length || 0} 条</summary>{[...(kr.observations || [])].reverse().map(item => <div key={item.id}><p>{item.value ?? "不可计算"} {kr.unit} · 定义 r{item.definitionRevision} · {formatTimestamp(item.observedAt)} · {item.actorKind}</p><p>{item.reason || item.issue}</p><EvidenceList references={item.evidence || []} /></div>)}</details>
      </Section>)}
      <Section title="对齐与贡献"><p>多个对齐只表达贡献关系，同一工作事实不因多条路径重复计分。</p>
        {record.alignments.length ? record.alignments.map(edge => { const target = visibleGoals(s).find(goal => goal.id === edge.targetGoalId); return <p key={`${edge.targetGoalId}:${edge.targetKrId}`}><Link to={`/goals/${edge.targetGoalId}`}>{target?.name || "目标不可用"}</Link> → {target?.krs?.find(kr => kr.id === edge.targetKrId)?.title || "KR 已变更"} · {edge.relation === "breakdown" ? "拆解" : "跨目标贡献"} · {edge.reason}</p>; }) : <Notice>尚无上级对齐，组织根目标可独立存在；其他目标需要明确支撑关系。</Notice>}
        {visibleGoals(s).filter(goal => goal.alignments?.some(edge => edge.targetGoalId === record.id)).map(goal => <p key={goal.id}><Link to={`/goals/${goal.id}`}>{goal.name}</Link> · 贡献目标，进度不自动加入本目标</p>)}
      </Section>
      <Section title="固定修订与审核"><p>当前记录 r{record.revision} · 定义 r{record.definitionRevision}</p>
        {record.submission && <p>提交修订 {record.submission.revision} · {record.submission.actor} · {record.submission.reason}</p>}
        {record.approval && <p>已批准修订 {record.approval.revision} · {record.approval.actor} · {record.approval.permission}</p>}
        <div className="actions">{[["待审核", "提交审核"], ["进行中", "批准目标"], ["草稿", "退回修改"]].map(([next, label]) => <Button type="button" key={next} disabled={Boolean(goalTransitionProblem(s, record, next))} title={goalTransitionProblem(s, record, next) || ""} onClick={() => setAction({ type: "transition", goal: structuredClone(record), next, label })}>{label}</Button>)}</div>
        <details><summary>历史与固定快照 · {record.history?.length || 0} 条</summary>{[...(record.history || [])].reverse().map((entry, i) => <details key={i}><summary>r{entry.revision || 1} · {entry.status} · {entry.reason || "目标定义变更"}</summary>{entry.before && <p>{entry.actor?.kind} · {entry.actor?.id} · {formatTimestamp(entry.time)} · 定义 r{entry.before.definitionRevision || 0} → r{entry.after.definitionRevision}</p>}<pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{JSON.stringify(entry.before ? { before: entry.before, after: entry.after } : entry.snapshot || { name: entry.name, krs: entry.krs, approval: entry.approval }, null, 2)}</pre></details>)}</details>
      </Section>
      <Section title="风险与本地检查" extra={<Badge>不发送外部消息</Badge>}>
        <div className="actions"><Button type="button" disabled={closed || Boolean(permission)} onClick={() => run(() => { checkGoalRisks(record.id, { revision: record.revision }); toast("已更新本地风险提醒"); })}>检查目标风险</Button><Button type="button" disabled={closed || Boolean(permission) || !record.krs.some(kr => kr.source === "AUTO")} onClick={() => run(() => { refreshGoalMetrics(record.id, { revision: record.revision }); toast("指标已按可用事实刷新，缺失数据未填零"); })}>刷新自动指标</Button></div>
        {risks.map(risk => <p key={risk.type}>{risk.message}</p>)}
        <p className="muted">未配置的过期、临期和滞后规则不使用隐藏默认值；滞后计算需要明确的工作日历。</p>
        {(record.reminders || []).map(reminder => <p key={reminder.id}>{reminder.status} · {reminder.message} · 接收负责人 {reminder.ownerId || "待配置"}</p>)}
      </Section>
      <Section title="期末自评与结转"><p>经营结果与工作接受分开判断。缺少观察不能确认达成。</p>
        {record.review && <><p>{record.review.status} · {record.review.summary}</p><p>{record.review.reason} · 处置 {record.review.disposition} · {record.review.reviewReason}</p></>}
        <div className="actions"><Button type="button" disabled={closed || Boolean(permission) || !["进行中", "存在风险"].includes(record.status)} onClick={() => setAction({ type: "period", next: "submit", label: "提交期末自评", goal: structuredClone(record) })}>提交期末自评</Button>
          {[["confirm", "确认期末结果"], ["return", "退回期末自评"]].map(([next, label]) => <Button type="button" key={next} disabled={closed || (record.collaborationVersion ? Boolean(goalActionProblem(s,record,"approve")) : s.role !== "owner") || record.review?.status !== "待审核"} onClick={() => setAction({ type: "period", next, label, goal: structuredClone(record) })}>{label}</Button>)}
          <Button type="button" disabled={closed || (record.collaborationVersion ? Boolean(goalActionProblem(s,record,"approve")) : s.role !== "owner")} onClick={() => setAction({ type: "period", next: "cancel", label: "取消目标", goal: structuredClone(record) })}>取消目标</Button>
          <Button type="button" disabled={(record.collaborationVersion ? Boolean(goalActionProblem(s,record,"approve")) : s.role !== "owner") || !["已达成", "未达成"].includes(record.status) || !record.review?.confirmedAt} onClick={() => setAction({ type: "period", next: "carry", label: "结转为下期草稿", goal: structuredClone(record) })}>结转为下期草稿</Button></div>
        {record.carryover && <Link to={`/goals/${record.carryover.goalId}`}>打开已生成的下期草稿</Link>}
        {record.carriedFrom && <Link to={`/goals/${record.carriedFrom.goalId}`}>查看上期复核与来源</Link>}
      </Section>
    </>}
    {editor && <GoalEditor key={`${s.activeTenant}:${editor.id || "new"}`} initial={editor} state={s} onClose={() => setEditor(null)} onSaved={goalId => { setEditor(null); location.hash = `/goals/${goalId}`; toast("已保存目标定义草稿，需重新提交审批"); }} />}
    {observation && <ObservationEditor key={`${s.activeTenant}:${observation.goal.id}:${observation.kr.id}`} {...observation} state={s} onClose={() => setObservation(null)} />}
    {action && <GoalAction key={`${s.activeTenant}:${action.goal.id}:${action.next}`} action={action} state={s} onClose={() => setAction(null)} toast={toast} />}
  </>;
}
function EvidenceList({ references }) {
  const state = useStore();
  return <ul>{references.map((ref, index) => {
    const rows = ref.kind === "work" ? state.work : state.resources[{ delivery: "deliveries", report: "reports", testreport: "testreports" }[ref.kind]];
    const current = rows?.find(item => item.id === ref.id), version = current && (ref.kind === "work" || ref.kind === "delivery" ? current.version : current.revision || 1);
    return <li key={index}>{evidenceHref(ref) ? <><Link to={evidenceHref(ref)}>{ref.label || ref.id} · {ref.kind === "work" ? `固定成果版本 ${ref.version}` : "打开当前记录"}</Link><p>{!current ? "原记录已不存在；以下保留引用时快照。" : String(version) !== String(ref.version) ? `当前记录已变为版本 ${version}；以下为引用时版本 ${ref.version}。` : `引用时版本 ${ref.version}；以保存快照为准。`}</p>{ref.snapshot && <details><summary>查看保存的证据快照</summary><pre style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}>{JSON.stringify(ref.snapshot, null, 2)}</pre></details>}</> : ref.kind === "URL" ? <a href={ref.ref} target="_blank" rel="noreferrer">{ref.ref} · 外部引用未核验</a> : `${ref.ref} · 文件引用未核验`}</li>;
  })}</ul>;
}
function GoalEditor({ initial, state, onClose, onSaved }) {
  const [form, setForm, draft] = useRecoverableDraft(JSON.stringify([state.activeTenant, "structured-goal", initial.id || "new"]), () => structuredClone(initial));
  const [changeReason, setChangeReason] = useState(""), [changeImpact,setChangeImpact]=useState("");
  const set = patch => setForm(current => ({ ...current, ...patch }));
  const [dateError, setDateError] = useState("");
  const changePeriod = (kind, date, timezone) => {
    const patch = { periodKind: kind, periodStart: date, timezone };
    if(form.timeMode==="STAGE"){set(patch);return;}
    if (date && timezone) try { Object.assign(patch, goalPeriod(kind, date, timezone)); setDateError(""); } catch (error) { setDateError(error.message); }
    set(patch);
  };
  const changeKR = (index, patch) => setForm(current => ({ ...current, krs: current.krs.map((kr, i) => i === index ? { ...kr, ...patch } : kr) }));
  const draftingAgents = state.agents.filter(a=>!form.collaborationVersion || a.id===form.ownerId || form.collaborators?.some(c=>c.id===a.id&&c.permissions.includes("draft")));
  const targets = visibleGoals(state).filter(goal => goal.id !== form.id && structuredGoal(goal) && !closedGoalStates.includes(goal.status));
  return <Modal title={initial.id ? "编辑 / 转换目标定义" : "新建 Objective 与 KR"} wide onClose={onClose} {...draft.modalProps}><ActionForm label="保存目标草稿" onCancel={onClose} onSubmit={({ dismiss }) => { const goalId = saveStructuredGoal({ ...form, changeReason, changeImpact }); dismiss(() => onSaved(goalId)); }}>
    {draft.recovered && <Notice>已恢复草稿并保留原始修订；目标已变更时请重新打开，不能覆盖他人修改。</Notice>}
    <details><summary>如何添加有效的 O / KR</summary>
      <p>O 说明要改善什么、谁会受益；KR 用可观察的结果说明如何判断改善。先确认已有成果能否复用，再决定需要哪些新工作。</p>
      <ul><li>结果：写清基线、目标值、单位、周期和数据来源，避免只有“完成工作”的活动清单。</li><li>下游：说明谁会使用成果、用于什么决定或下一步，以及接收标准；文档写完不等于已被采用或有效。</li><li>证据：说明如何观察、核对结果；没有数据时标明未知，不以文件数量、发言次数替代业务效果。</li><li>过程指标与保障工作：支持、合规、巡检、知识整理同样可以有价值；说明其预防的风险、保障的服务或改善的下游体验，并提供可核对依据，不必编造收入指标。</li><li>调整：请假、临时任务或依赖变化影响承诺时，说明本次原因及范围、时间、资源或下游影响；保留原目标和原 KR。</li></ul>
      <p><strong>正例：</strong>O「让客服能准确处理常见退款问题」；KR「在本月抽样复核中，将退款指引的正确使用率从 70% 提高到 90%，以审核记录为据」。指引由谁使用、怎样核验应先明确。</p>
      <p><strong>反例：</strong>「写 20 份运营文档」只说明产出数量；尚需解释读者、用途、质量和使用证据。巡检次数也应结合覆盖范围、漏检或风险处置证据，而非一概判为无价值。</p>
      <Notice>以上是初始填写指导，不是 Agent 语义审查结论。字段完整或数值有效，不代表目标合理、可行或对下游有用；本地模拟标题也不代表真实模型审查。</Notice>
    </details>
    {initial.id && <Field label="本次调整理由" type="textarea" required value={changeReason} onChange={setChangeReason} help="说明为什么调整，以及对范围、期限、资源或下游的影响；每次打开须重新填写，旧理由不会自动沿用。" />}
    {initial.activeBaseline && <Field label="本次调整影响" required type="textarea" value={changeImpact} onChange={setChangeImpact} help="原生效版本保留；说明对范围、期限、资源和下游的影响。" />}
    <div className="form-grid"><Field label="目标名称" required value={form.name} onChange={name => set({ name })} /><Field label="目标归属" required value={form.subjectKey} onChange={subjectKey => { const type={tenant:"ORGANIZATION",team:"TEAM",project:"PROJECT_STAGE",human:"HUMAN",agent:"AGENT"}[subjectKey.split(":")[0]],person=people(state).find(p=>p.id===subjectKey.split(":")[1]);const next={...form,subjectKey,goalType:type,collaborationVersion:1,tenantId:state.activeTenant,timeMode:"NATURAL",collaborators:[],visibility:["HUMAN","AGENT"].includes(type)?"PRIVATE":"SCOPE",...(person?{ownerId:person.id,ownerKind:person.kind,owner:person.name}:{})};next.reviewerId=goalReviewers(state,next)[0]||"";set(next);}} options={[{ value: "", label: "请选择归属" }, ...goalSubjects(state).filter(item=>item.key!=="project:private").map(item => ({ value: item.key, label: `${item.kind} · ${item.name}` }))]} />
      <Field label="目标负责人" value={`${form.ownerKind}:${form.ownerId}`} onChange={key => { const person = people(state).find(person => person.key === key); if (person) set({ ownerId: person.id, ownerKind: person.kind, owner: person.name }); }} options={people(state).map(person => ({ value: person.key, label: `${person.kind} · ${person.name}` }))} />
      <Field label="目标周期" value={form.periodKind} onChange={kind => changePeriod(kind, form.periodStart, form.timezone)} options={Object.entries(goalPeriods).map(([value, label]) => ({ value, label: label.replace("报", "度") }))} />
      <Field label={form.timeMode==="STAGE"?"阶段开始日期":"周期内日期"} type="date" required value={form.periodStart} onChange={date => changePeriod(form.periodKind, date, form.timezone)} />
      <Field label="目标时区" required value={form.timezone} placeholder="例如 Asia/Singapore" onChange={timezone => changePeriod(form.periodKind, form.periodStart, timezone)} /></div>
    {form.collaborationVersion && <Section title="共同维护与责任范围"><Notice>当前操作人为本地 Human 林予（H-lin）；Agent 提出与审查均为明确本地模拟。组织上级不自动取得其他项目或个人私密资料。</Notice><p>目标类型：{goalTypes[form.goalType]}（与负责人类型分开）</p>
      {form.goalType==="PROJECT_STAGE" && <><Field label="项目阶段名称" required value={form.stageName||""} onChange={stageName=>set({stageName})}/><Field label="时间范围方式" value={form.timeMode||"NATURAL"} onChange={timeMode=>set({timeMode})} options={[{value:"NATURAL",label:"自然周期"},{value:"STAGE",label:"独立阶段起止"}]}/>{form.timeMode==="STAGE"&&<Field label="阶段结束日期" type="date" required value={form.periodEnd} onChange={periodEnd=>set({periodEnd})}/>}</>}
      <Field label="Human 审核者" value={form.reviewerId||""} onChange={reviewerId=>set({reviewerId})} options={[{value:"",label:"请选择责任范围内的 Human"},...goalReviewers(state,form).map(value=>({value,label:state.organization.members.find(p=>p.id===value)?.name||value}))]}/>
      <Field label="目标可见范围" value={form.visibility||"PRIVATE"} onChange={visibility=>set({visibility})} options={[...(["HUMAN","AGENT"].includes(form.goalType)?[{value:"PRIVATE",label:"本人及明确协作者"}]:[]),{value:"SCOPE",label:"既有组织 / 项目责任范围"}]}/>
      {[['beneficiaries','受益对象'],['value','期望改变与下游价值'],['resources','资源与实现假设']].map(([key,label])=><Field key={key} label={label} type="textarea" value={form[key]||""} onChange={v=>set({[key]:v})}/>)}
      <fieldset><legend>明确授权 Agent 共同维护（不授任务、文件或其他目标权限）</legend>{state.agents.filter(a=>!["退役","已退役","待批准"].includes(a.status)).map(a=><div key={a.id}><b>{a.name}</b>{[['suggest','建议'],['draft','草稿与价值审查'],['checkin','周期 check-in']].map(([permission,label])=><label className="check-label" key={permission}><input type="checkbox" checked={form.collaborators?.find(c=>c.id===a.id)?.permissions.includes(permission)||false} onChange={e=>{const old=form.collaborators?.find(c=>c.id===a.id),permissions=[...(old?.permissions||[]).filter(p=>p!==permission),...(e.target.checked?[permission]:[])];set({collaborators:[...(form.collaborators||[]).filter(c=>c.id!==a.id),...(permissions.length?[{id:a.id,kind:"Agent",permissions}]:[])]});}}/>{label}</label>)}</div>)}</fieldset>
      <Field label="本次草稿提出方式" value={form.simulateAgentId||""} onChange={simulateAgentId=>set({simulateAgentId})} options={[{value:"",label:"Human 提出"},...state.agents.filter(a=>a.id===form.ownerId||form.collaborators?.some(c=>c.id===a.id&&c.permissions.includes("draft"))).map(a=>({value:a.id,label:`模拟 Agent · ${a.name}`}))]}/>
    </Section>}
    {dateError && <p role="alert" className="error">{dateError}</p>}<p>固定边界：{form.periodStart || "待选择"} — {form.periodEnd || "待选择"}</p>
    <Button type="button" disabled={!draftingAgents.length || !form.name.trim()} onClick={() => set({ draftOrigin: { simulated: true, agentId: draftingAgents[0].id }, krs: form.krs.map((kr, index) => ({ ...kr, title: kr.title || `${form.name} · 可观察结果 ${index + 1}` })) })}>模拟 AI 起草 KR 标题</Button>
    <p className="muted">仅生成本地建议标题，数值、单位和证据须由你确认，不代表真实模型运行。</p>
    {form.krs.map((kr, index) => <Section key={kr.id} title={`KR ${index + 1}`} extra={<Button type="button" disabled={form.krs.length === 1} onClick={() => set({ krs: form.krs.filter(item => item.id !== kr.id) })}>删除 KR {index + 1}</Button>}>
      <Field label={`KR ${index+1} 评估类型`} value={kr.kind||"NUMERIC"} onChange={kind=>changeKR(index,{kind})} options={[{value:"NUMERIC",label:"数值指标"},{value:"QUALITATIVE",label:"约定证据评审（定性）"}]}/>
      {kr.kind==="QUALITATIVE" ? <div className="form-grid">{[['title','标题'],['delivery','约定交付'],['use','预期作用'],['criteria','评审标准'],['evidencePlan','证据约定']].map(([key,label])=><Field key={key} label={`KR ${index+1} ${label}`} required type="textarea" value={kr[key]||""} onChange={v=>changeKR(index,{[key]:v})}/>)}<Field label={`KR ${index+1} 期限`} required type="date" value={kr.dueDate||""} onChange={dueDate=>changeKR(index,{dueDate})}/><Field label={`KR ${index+1} 权重`} required value={String(kr.weight)} onChange={weight=>changeKR(index,{weight})}/></div> : <div className="form-grid"><Field label={`KR ${index + 1} 标题`} required value={kr.title} onChange={title => changeKR(index, { title })} /><Field label={`KR ${index + 1} 单位`} required value={kr.unit} onChange={unit => changeKR(index, { unit })} />
        <Field label={`KR ${index + 1} 方向`} value={kr.direction} onChange={direction => changeKR(index, { direction })} options={[{ value: "INCREASE", label: "增长" }, { value: "DECREASE", label: "降低" }]} />
        {[['baseline','基线'],['target','目标值'],['weight','权重']].map(([key,label]) => <Field key={key} label={`KR ${index + 1} ${label}`} required value={String(kr[key] ?? "")} onChange={value => changeKR(index, { [key]: value })} inputMode="decimal" help={key === 'weight' ? '大于 0，不超过 10' : '只填写数值，单位单独设置'} />)}
        <Field label={`KR ${index + 1} 来源`} value={kr.source} onChange={source => changeKR(index, { source })} options={[{ value: "MANUAL", label: "人工观察" }, { value: "AUTO", label: "自动事实" }]} />
        {kr.source === "AUTO" && <Field label={`KR ${index + 1} 自动指标`} value={kr.metric} onChange={metric => changeKR(index, { metric })} options={[{ value: "", label: "请选择指标" }, ...Object.entries(goalMetrics).map(([value, label]) => ({ value, label }))]} />}</div>}
      <label className="check-label"><input type="checkbox" checked={kr.evidenceRequired} onChange={event => changeKR(index, { evidenceRequired: event.target.checked })} />KR {index + 1} 观察必须有佐证</label>
    </Section>)}
    <Button type="button" disabled={form.krs.length >= 5} onClick={() => set({ krs: [...form.krs, blankKR()] })}>添加 KR</Button>
    <Section title="对齐到上级 KR / 跨目标贡献">
      {(form.alignments || []).map((edge, index) => <div key={index} className="stack"><div className="form-grid"><Field label={`对齐 ${index + 1} 目标`} value={edge.targetGoalId} onChange={targetGoalId => set({ alignments: form.alignments.map((item, i) => i === index ? { ...item, targetGoalId, targetKrId: "" } : item) })} options={[{ value: "", label: "请选择目标" }, ...targets.map(goal => ({ value: goal.id, label: goal.name }))]} />
        <Field label={`对齐 ${index + 1} KR`} value={edge.targetKrId} onChange={targetKrId => set({ alignments: form.alignments.map((item, i) => i === index ? { ...item, targetKrId } : item) })} options={[{ value: "", label: "请选择 KR" }, ...(targets.find(goal => goal.id === edge.targetGoalId)?.krs || []).map(kr => ({ value: kr.id, label: kr.title }))]} />
        <Field label={`对齐 ${index + 1} 关系`} value={edge.relation} onChange={relation => set({ alignments: form.alignments.map((item, i) => i === index ? { ...item, relation } : item) })} options={[{ value: "breakdown", label: "层级拆解" }, { value: "contribution", label: "跨目标贡献" }]} />
        <Field label={`对齐 ${index + 1} 依据`} required value={edge.reason} onChange={reason => set({ alignments: form.alignments.map((item, i) => i === index ? { ...item, reason } : item) })} /></div><Button type="button" onClick={() => set({ alignments: form.alignments.filter((_, i) => i !== index) })}>移除对齐 {index + 1}</Button></div>)}
      <Button type="button" onClick={() => set({ alignments: [...(form.alignments || []), { targetGoalId: "", targetKrId: "", relation: "breakdown", reason: "" }] })}>添加对齐</Button>
    </Section>
    <details><summary>风险规则（未填写则未配置）</summary><div className="form-grid">{[["lagThreshold","进度滞后阈值（0–1）"],["freshnessDays","观察有效天数"],["nearDueDays","临期提前天数"]].map(([key,label]) => <Field key={key} label={label} value={String(form.riskPolicy?.[key] ?? "")} inputMode="decimal" onChange={value => set({ riskPolicy: { ...form.riskPolicy, [key]: value } })} />)}</div><fieldset><legend>明确每周工作日（不含节假日配置）</legend>{["日","一","二","三","四","五","六"].map((name, day) => <label className="check-label" key={day}><input type="checkbox" checked={(form.riskPolicy?.workingWeekdays || []).includes(day)} onChange={event => set({ riskPolicy: { ...form.riskPolicy, workingWeekdays: [...(form.riskPolicy?.workingWeekdays || []).filter(item => item !== day), ...(event.target.checked ? [day] : [])] } })} />星期{name}</label>)}</fieldset><Notice>未选择工作日时不计算滞后；配置只表达每周日历，不推测地区节假日。</Notice></details>
  </ActionForm></Modal>;
}
function ObservationEditor({ state, goal, kr, onClose }) {
  const [form, setForm, draft] = useRecoverableDraft(JSON.stringify([state.activeTenant, "goal-observation", goal.id, kr.id]), () => ({ revision: goal.revision, value: "", judgment:"UNKNOWN",delivery:"",use:"", observedAt: "", reason: "", evidence: [], external: "", agentDraft: false }));
  const options = [...state.work.map(item => ({ kind: "work", id: item.id, version: item.version, label: item.title })), ...["delivery", "report", "testreport"].flatMap(kind => (state.resources[{ delivery: "deliveries", report: "reports", testreport: "testreports" }[kind]] || []).map(item => ({ kind, id: item.id, version: kind === "delivery" ? item.version : item.revision || 1, label: item.name })))];
  return <Modal title={`记录观察 · ${kr.title}`} onClose={onClose} wide {...draft.modalProps}><ActionForm label="保存观察" onCancel={onClose} onSubmit={({ dismiss }) => { recordGoalObservation(goal.id, kr.id, { ...form, evidence: [...form.evidence, ...(form.external.trim() ? [{ kind: "URL", ref: form.external.trim() }] : [])] }); dismiss(); }}>
    <Notice>观察是业务结果的说明，不会接受关联工作，也不会自动批准目标。</Notice>
    <div className="form-grid">{kr.kind==="QUALITATIVE"?<><Field label="证据评审判断" value={form.judgment} onChange={judgment=>setForm(c=>({...c,judgment}))} options={[{value:"UNKNOWN",label:"证据不足 / 未知"},{value:"NOT_MET",label:"未达到约定标准"},{value:"MET",label:"证据表明达到约定标准"}]}/><Field label="实际交付" required value={form.delivery} onChange={delivery=>setForm(c=>({...c,delivery}))}/><Field label="实际作用与采用" required value={form.use} onChange={use=>setForm(c=>({...c,use}))}/></>:<Field label="观察值" required inputMode="decimal" value={form.value} onChange={value => setForm(current => ({ ...current, value }))} />}<Field label="观察时间" type="datetime-local" required value={form.observedAt} onChange={observedAt => setForm(current => ({ ...current, observedAt }))} help="按当前浏览器本地时间录入；保存时固定为 UTC 时间点。" /></div>
    <Field label="观察依据" type="textarea" required value={form.reason} onChange={reason => setForm(current => ({ ...current, reason }))} />
    <Field label="外部佐证 URL（选填）" type="url" value={form.external} onChange={external => setForm(current => ({ ...current, external }))} />
    <fieldset><legend>关联本地证据（固定所选版本）</legend>{options.map(option => <label className="check-label" key={`${option.kind}:${option.id}`}><input type="checkbox" checked={form.evidence.some(ref => ref.kind === option.kind && ref.id === option.id)} onChange={event => setForm(current => ({ ...current, evidence: [...current.evidence.filter(ref => ref.kind !== option.kind || ref.id !== option.id), ...(event.target.checked ? [option] : [])] }))} />{option.kind} · {option.label} · {option.version}</label>)}</fieldset>
    {goal.ownerKind === "Agent" && <label className="check-label"><input type="checkbox" checked={form.agentDraft} onChange={event => setForm(current => ({ ...current, agentDraft: event.target.checked }))} />模拟此 Agent 的授权事实草案（必须有证据，由 Human 操作）</label>}
  </ActionForm></Modal>;
}
function GoalAction({ action, state, onClose, toast }) {
  const [form, setForm, draft] = useRecoverableDraft(JSON.stringify([state.activeTenant, "goal-action", action.goal.id, action.next]), () => ({ revision: action.goal.revision, status: action.goal.status, reason: "", summary: "", disposition: "结转", result: "未达成", periodStart: "", progress: "", definitionRevision: action.goal.definitionRevision }));
  const set = patch => setForm(current => ({ ...current, ...patch }));
  return <Modal title={action.label} onClose={onClose} {...draft.modalProps}><ActionForm label="确认操作" onCancel={onClose} onSubmit={({ dismiss }) => {
    if (action.type === "assessment") calibrateGoalProgress(action.goal.id, form);
    else if (action.type === "transition") resourceCommand("goals", action.goal.id, action.next, form.reason, action.label, { revision: form.revision, status: form.status });
    else { const target = goalPeriodCommand(action.goal.id, action.next, form); if (target) location.hash = `/goals/${target}`; }
    dismiss(); toast("目标决定已记录");
  }}><Notice>{action.goal.name} · 固定记录 r{form.revision}</Notice>
    {action.type === "assessment" && <Field label="人工判断进度（%）" required value={form.progress} inputMode="decimal" onChange={progress => set({ progress })} help="只记录解释性判断，不覆盖 KR 公式进度或达成门禁。" />}
    {action.next === "submit" && <><Field label="期末自评" type="textarea" required value={form.summary} onChange={summary => set({ summary })} /><Field label="未完成处置" value={form.disposition} onChange={disposition => set({ disposition })} options={["结转","停止","升级"]} /></>}
    {action.next === "confirm" && <Field label="期末结果" value={form.result} onChange={result => set({ result })} options={["未达成","已达成"]} />}
    {action.next === "carry" && <><Field label="下期内日期" required type="date" value={form.periodStart} onChange={periodStart => set({ periodStart })} />{action.goal.timeMode==="STAGE"&&<Field label="下阶段结束日期" required type="date" value={form.periodEnd||""} onChange={periodEnd=>set({periodEnd})}/>}<Notice>新目标只复制定义框架；基线、目标值、对齐与观察需重新确认，不继承批准或进度。</Notice></>}
    <Field label="依据与说明" type="textarea" required value={form.reason} onChange={reason => set({ reason })} />
  </ActionForm></Modal>;
}
