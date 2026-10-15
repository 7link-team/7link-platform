import React, { useState } from "react";
import { useStore, performanceCommand, id } from "./store";
import { reportSubjects, dateInZone, reportPeriod } from "./reporting";
import { performanceCycles, performanceMetrics, performanceTemplate, performanceOwnerProblem, performanceSubjects, performancePeriods, performancePeriodKey } from "./performance";
import { PageHead, Section, Notice, Empty, Button, Badge, Field, Link, Modal, ActionForm, Metric, useRecoverableDraft } from "./ui";

const labels = { "周报": "周度", "月报": "月度", "季报": "季度", "年报": "年度" };
const scoreText = report => report?.scoring?.score == null ? "待补证 / 待评" : `${report.scoring.score} / ${report.policySnapshot.maxScore}`;
const readable = value => value === true ? "有记录" : value === false ? "已核对无延期" : "未知";
const href = patch => `/performance?${new URLSearchParams(Object.entries(patch).filter(([,value])=>value!=null && value!==""))}`;
const nextLocation = patch => { location.hash=href(patch); };
const toggle = (values, value) => values.includes(value) ? values.filter(item=>item!==value) : [...values,value];
const previousPeriodDate = (cycle, timezone) => { const start=reportPeriod(cycle,dateInZone(timezone)).periodStart;return new Date(Date.parse(`${start}T00:00:00Z`)-86400000).toISOString().slice(0,10); };
function History({ entries = [], title = "修订与决定历史" }) {
  return <Section title={title}>{entries.length ? entries.map((entry,index)=><details key={index}><summary>{entry.action === "CONFIRM" ? "确认评分" : "修订 / 校准"} · {entry.time} · Human {entry.actorId}</summary><p>{entry.reason}</p>{entry.before && <><p>保留原修订 {entry.before.revision} · {entry.before.status || entry.before.name}{entry.before.scoring ? ` · 原评分 ${scoreText(entry.before)}` : ""}</p><pre style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{entry.before.body || JSON.stringify(entry.before,null,2)}</pre></>}</details>) : <p className="muted">尚无后续修订；首次生成理由保留在报告中。</p>}</Section>;
}
function PolicyEditor({ policy, onClose, toast }) {
  const s=useStore(),subjects=performanceSubjects(s),tenantId=s.activeTenant;
  const [form,setForm,recovery]=useRecoverableDraft(`${tenantId}:performance-policy:${policy?.id || "new"}`,()=>policy ? {...structuredClone(policy),reason:""} : {
    name:"成员周期考核",subjectKeys:[],cycles:["周报","月报"],timezone:"Asia/Singapore",baseScore:60,maxScore:100,enabled:true,template:performanceTemplate,
    rules:[{id:id("RULE"),name:"已接受的独立工作",dimension:"协作进度",metric:"COMPLETED_WORK",description:"按周期内固定接受事件去重计数；缺失历史保持待补证。",points:2,limit:10},{id:id("RULE"),name:"专业质量评估",dimension:"专业评估",metric:"REVIEW",description:"由 owner 提供可核对证据并说明校准理由。",points:5,limit:4}],reason:""});
  const change=(key,value)=>setForm(old=>({...old,[key]:value}));
  const ruleChange=(ruleId,key,value)=>setForm(old=>({...old,rules:old.rules.map(rule=>rule.id===ruleId?{...rule,[key]:value}:rule)}));
  return <Modal title={policy ? "修订考核方案" : "配置考核方案"} wide className="performance-policy-dialog" onClose={onClose} {...recovery.modalProps}><ActionForm label="保存方案修订" onCancel={onClose} onSubmit={({dismiss})=>{performanceCommand({...form,action:policy?"save":"create",tenantId});dismiss();toast("方案已保存；已有报告保留原规则快照");}}>
    <div className="policy-summary" aria-label="方案配置摘要"><span><strong>{form.subjectKeys.length}</strong> 个考核对象</span><span><strong>{form.cycles.length}</strong> 个生成周期</span><span><strong>{form.rules.length}</strong> 条评分规则</span><Badge>{form.enabled ? "保存后启用" : "保存后停用"}</Badge></div>
    <Section title="基本信息"><div className="form-grid"><Field label="方案名称" value={form.name} onChange={value=>change("name",value)} required /><Field label="IANA 时区" value={form.timezone} onChange={value=>change("timezone",value)} required /><Field label="基础分" type="number" value={String(form.baseScore)} onChange={value=>change("baseScore",value)} /><Field label="总分上限" type="number" value={String(form.maxScore)} onChange={value=>change("maxScore",value)} /></div></Section>
    <Section title="考核对象与周期"><p className="muted">只给 Human 与 Agent 评分。租户与项目报告汇总工作情况，不计算总分。</p>
      <fieldset className="policy-choices"><legend>考核主体 · 可多选</legend><div>{subjects.map(subject=><label key={subject.key} className={`policy-choice ${form.subjectKeys.includes(subject.key) ? "selected" : ""}`}><input type="checkbox" checked={form.subjectKeys.includes(subject.key)} onChange={()=>change("subjectKeys",toggle(form.subjectKeys,subject.key))} /><span>{subject.name}<small>{subject.kind}</small></span></label>)}</div>{!subjects.length && <p>尚无可配置成员。</p>}</fieldset>
      <fieldset className="policy-choices"><legend>周期 · 可同时启用</legend><div>{performanceCycles.map(cycle=><label key={cycle} className={`policy-choice ${form.cycles.includes(cycle) ? "selected" : ""}`}><input type="checkbox" checked={form.cycles.includes(cycle)} onChange={()=>change("cycles",toggle(form.cycles,cycle))} /><span>{labels[cycle]}</span></label>)}</div></fieldset>
      <label className="policy-enable"><input type="checkbox" checked={form.enabled} onChange={event=>change("enabled",event.target.checked)} /><span>启用本地生成方案<small>保存后可手动模拟生成；未接入真实定时服务。</small></span></label>
    </Section>
    <Section title="评分细则" extra={<Button icon="Plus" onClick={()=>change("rules",[...form.rules,{id:id("RULE"),name:"",dimension:"",metric:"REVIEW",description:"",points:0,limit:1}])}>添加考核细则</Button>}>
      <p className="muted">单次分值可加可减，次数上限限制本项贡献。缺少证据保持待评，不自动按零分处理。</p>
      {form.rules.map((rule,index)=><fieldset className="policy-rule" key={rule.id}><legend>考核细则 {index+1}</legend><div className="policy-rule-heading"><strong>{rule.name || "未命名细则"}</strong><Button icon="Trash2" onClick={()=>change("rules",form.rules.filter(item=>item.id!==rule.id))}>移除细则 {index+1}</Button></div><div className="form-grid">
        <Field label={`细则 ${index+1} 名称`} value={rule.name} onChange={value=>ruleChange(rule.id,"name",value)} required /><Field label={`细则 ${index+1} 维度`} value={rule.dimension} onChange={value=>ruleChange(rule.id,"dimension",value)} required />
        <Field label={`细则 ${index+1} 证据来源`} value={rule.metric} options={Object.entries(performanceMetrics).map(([value,label])=>({value,label}))} onChange={value=>ruleChange(rule.id,"metric",value)} />
        <div className="policy-points"><Field label={`细则 ${index+1} 单次加减分`} type="number" step="any" value={String(rule.points)} onChange={value=>ruleChange(rule.id,"points",value)} /><Field label={`细则 ${index+1} 次数上限`} type="number" value={String(rule.limit)} onChange={value=>ruleChange(rule.id,"limit",value)} /></div>
      </div><Field label={`细则 ${index+1} 条件与依据`} type="textarea" value={rule.description} onChange={value=>ruleChange(rule.id,"description",value)} required /></fieldset>)}
      {!form.rules.length && <p className="muted">至少添加一条评分细则后保存。</p>}
    </Section>
    <Section title="报告模板与变更记录"><Field label="报告模板" type="textarea" className="policy-template-field" value={form.template} onChange={value=>change("template",value)} required help="支持 {{subject}}、{{period}}、{{score}}、{{details}}、{{cutoff}}；按文本呈现。" /><Field label="配置变更理由" type="textarea" value={form.reason} onChange={value=>change("reason",value)} required /><Notice>保存生成新的方案修订；已有报告继续保留原规则快照。各周期最后一天 00:00 截点，生成操作为本地模拟。</Notice></Section>
  </ActionForm></Modal>;
}
function GenerateDialog({ policies, onClose, onConfigure, toast, report }) {
  const s=useStore(),tenantId=s.activeTenant,initial=policies.find(policy=>policy.id===report?.policyId) || policies.find(policy=>policy.enabled);
  const [policyId,setPolicyId]=useState(initial?.id || ""),[cycle,setCycle]=useState(report?.cycle || initial?.cycles[0] || "周报"),[subjectKey,setSubjectKey]=useState(report?.subject.key || "all"),[date,setDate]=useState(report?.periodStart || previousPeriodDate(initial?.cycles[0] || "周报",initial?.timezone || "Asia/Singapore")),[reason,setReason]=useState("");
  const [revision]=useState(report?.revision),[policyRevision,setPolicyRevision]=useState(initial?.revision);
  const policy=policies.find(item=>item.id===policyId);
  if (!report && !policies.some(p=>p.enabled)) return <Modal title="模拟周期生成" onClose={onClose}><Empty title="先配置并启用考核方案" text="生成报告需要明确考核对象、周期、评分细则与报告模板。当前没有启用方案，尚未生成任何报告。" /><div className="actions"><Button onClick={onClose}>关闭</Button><Button variant="primary" onClick={onConfigure}>配置考核方案</Button></div></Modal>;
  return <Modal title={report?"重新生成并保留原报告":"模拟周期生成"} onClose={onClose} draftValue={{policyId,cycle,subjectKey,date,reason}}><ActionForm label={report?"有理由地重新计算":"生成所选成员报告"} onCancel={onClose} onSubmit={({dismiss})=>{
    if(!policy)throw Error("请先配置并启用考核方案。");
    if(subjectKey==="all" && !report) {
      const reportId=performanceCommand({action:"generate-period",tenantId,policyId,policyRevision,cycle,date,reason});
      dismiss();toast("所选周期成员报告已本地生成");nextLocation({tab:"periods",report:reportId,subject:`tenant:${tenantId}`});
    } else {
      const reportId=performanceCommand({action:"generate",tenantId,policyId,policyRevision,subjectKey,cycle,date,reason,regenerate:!!report,revision});
      dismiss();toast("已保存本地报告；没有真实定时任务运行");nextLocation({tab:"periods",report:reportId});
    }
  }}>
    <Notice>LOCAL_SIMULATION：此按钮模拟定时触发，未部署真实 scheduler。仅生成已到截点的周期；数据截至最后一天 00:00，不包含该日全天。未来截点拒绝生成。</Notice>
    <Field label="考核方案" value={policyId} disabled={!!report} options={[{value:"",label:"选择已启用方案"},...policies.filter(item=>item.enabled).map(item=>({value:item.id,label:`${item.name} · 修订 ${item.revision}`}))]} onChange={value=>{const next=policies.find(item=>item.id===value);setPolicyId(value);setPolicyRevision(next?.revision);setCycle(next?.cycles[0] || "周报");setDate(previousPeriodDate(next?.cycles[0] || "周报",next?.timezone || "Asia/Singapore"));setSubjectKey("all");}} />
    <Field label="生成周期" value={cycle} disabled={!!report} options={(policy?.cycles || []).map(value=>({value,label:labels[value]}))} onChange={value=>{setCycle(value);setDate(previousPeriodDate(value,policy.timezone));}} />
    <Field label="周期内任一日期" type="date" value={date} disabled={!!report} onChange={setDate} /><Field label="考核对象" value={subjectKey} disabled={!!report} options={[{value:"all",label:"方案中的全部 Human / Agent"},...(policy?.subjectKeys || []).map(value=>({value,label:reportSubjects(s).find(subject=>subject.key===value)?.name || value}))]} onChange={setSubjectKey} />
    <p>方案时区：{policy?.timezone || "未配置"}。重复生成同方案、主体与周期会打开原报告；重新计算需明确理由并保留历史。</p>
    <Field label="生成或重新计算理由" type="textarea" value={reason} onChange={setReason} required />
  </ActionForm></Modal>;
}
function ScoreReport({ report, ownerProblem, onAction }) {
  return <><Section title={`${report.subject.name} · ${report.subject.kind} 考核分`} extra={<Badge>{report.status}</Badge>}><div className="metrics"><Metric label="考核分" value={scoreText(report)} detail={`基础分 ${report.policySnapshot.baseScore} · 已知部分 ${report.scoring.knownScore}`} /><Metric label="方案修订" value={report.policySnapshot.revision} detail={report.policySnapshot.name} /><Metric label="报告修订" value={report.revision} detail={report.source} /></div>
    <p>生成于 {report.generatedAt} · 理由：{report.generationReason}</p><p>截点 {report.triggerLabel}。{report.uncovered}</p>
    <div className="actions"><Button disabled={!!ownerProblem} onClick={()=>onAction("regenerate")}>重新生成本主体报告</Button><Button disabled={!!ownerProblem || report.scoring.score===null || report.status==="已确认"} onClick={()=>onAction("confirm")}>确认本修订评分</Button></div>{ownerProblem && <p className="muted">{ownerProblem}</p>}
    {report.confirmation && <Notice>Human {report.confirmation.actorId} 已确认原修订 {report.confirmation.revision}：{report.confirmation.score} 分 · {report.confirmation.time} · {report.confirmation.reason}</Notice>}
  </Section><Section title="规则、证据与专业校准">{report.scoring.lines.map(line=><article className="resource-card" key={line.id}><div className="grow"><h3>{line.dimension} · {line.name}</h3><p>{line.count===null?"待评 / 未知":`${line.count} 次 × ${line.points}，最多计 ${line.limit} 次；本项 ${line.delta>0?"+":""}${line.delta} 分`}</p><p>{line.issue || line.description}</p>
    <details><summary>核对本项依据 · {line.evidence.length} 条</summary>{line.evidence.map((entry,index)=><p key={entry.id || index}>{entry.workId ? <Link target="_blank" rel="noopener noreferrer" to={`/p/${entry.project}/artifacts/${entry.workId}?version=${entry.version}`}>{entry.workSnapshot?.title || entry.workId} · v{entry.version} · {entry.time}</Link> : <a href={entry.url} target="_blank" rel="noreferrer">{entry.note}</a>}</p>)}</details>
    {line.metric==="REVIEW" && <Button disabled={!!ownerProblem} onClick={()=>onAction("calibrate",line.id)}>专业校准 · {line.name}</Button>}</div></article>)}</Section><Section title="按固定模板生成的报告"><pre style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{report.body}</pre></Section><History entries={report.history} /></>;
}
function WorkOverview({ overview, project, subjectKey }) {
  if(!overview)return <Empty title="原报告缺少周期明细快照" text="请由 owner 有理由地重新生成；不会用当前工作状态补造旧报告。" />;
  const subjectProject=subjectKey.startsWith("project:") ? subjectKey.slice(8) : null;
  const rows=overview.rows.filter(row=>(!subjectProject || row.projectId===subjectProject) && (project==="all" || (project==="unknown" ? !row.scopeKnown : row.projectId===project)));
  const groups=new Map();for(const row of rows){const key=row.projectId || "unknown";if(!groups.has(key))groups.set(key,[]);groups.get(key).push(row);}
  return <><Section title={subjectKey.startsWith("tenant:")?"租户工作汇总 · 不计分":subjectKey.startsWith("project:")?"项目工作汇总 · 不计分":"周期工作情况"}><div className="metrics">{[["完成","completed"],["进行中记录","inProgress"],["延期记录","delayed"],["变更记录","changed"]].map(([label,key])=><Metric key={key} label={label} value={rows.filter(row=>row[key]===true).length} detail="仅可核对记录，非完整数量" />)}</div><p>{overview.basis}</p><Notice tone="warn">{rows.filter(row=>row.issues.length).length} 项含未知依据。未知不等于零；裁决操作不算执行成果，当前状态不会被当作历史状态。</Notice></Section>
    {groups.size ? [...groups].map(([projectId,items])=><Section key={projectId} title={projectId==="unknown"?"项目归属未知 / 待补证":`${items[0].projectName || projectId} · 项目工作`}>
      {items.map(row=><article className="resource-card" key={row.workId}><div className="grow"><h3>{row.title} <small>{row.workId}</small></h3><p>完成：{readable(row.completed)} · 进行中：{readable(row.inProgress)} · 延期：{readable(row.delayed)} · 变更：{readable(row.changed)}</p>
        {row.projectId ? <Link target="_blank" rel="noopener noreferrer" to={`/p/${row.projectId}/work-items/${row.workId}`}>查看工作原记录</Link> : row.catalogProjectId && <Link target="_blank" rel="noopener noreferrer" to={`/p/${row.catalogProjectId}/work-items/${row.workId}`}>查看当前目录记录（非历史归属证据）</Link>}
        {row.issues.map(issue=><p className="muted" key={issue}>{issue}</p>)}<details><summary>周期依据 · {row.evidence.length} 条</summary>{row.evidence.length?row.evidence.map((entry,index)=><p key={`${entry.id}:${index}`}><Link target="_blank" rel="noopener noreferrer" to={entry.href}>{entry.kind} · {entry.role} · {entry.time}{entry.dueDate?` · 承诺 ${entry.dueDate}`:""}</Link>{entry.reason && ` · ${entry.reason}`}</p>):<p>没有可核对周期事实。</p>}</details>
      </div></article>)}
    </Section>):<Empty title="此范围暂无可核对工作" text="可切换项目范围或检查周期来源；不会自动补造历史。" />}</>;
}
export function PerformancePage({ params=new URLSearchParams(), toast=()=>{} }) {
  const s=useStore(),tenantId=s.activeTenant,policies=(s.resources.performancePolicies || []).filter(policy=>policy.tenantId===tenantId),periods=performancePeriods(s),ownerProblem=performanceOwnerProblem(s);
  const [editing,setEditing]=useState(null),[generating,setGenerating]=useState(null),[action,setAction]=useState(null),[reason,setReason]=useState(""),[value,setValue]=useState("0"),[evidence,setEvidence]=useState("");
  if(s.role==="external")return <Empty title="绩效考核仅供内部成员访问" />;
  const linkedReport=(s.resources.performanceReports || []).find(report=>report.id===params.get("report") && report.tenantId===tenantId);
  const period=periods.find(item=>item.key===(params.get("period") || (linkedReport && performancePeriodKey(linkedReport))));
  const tab=params.get("tab")==="policies"?"policies":"periods",subjectKey=params.get("subject") || linkedReport?.subject.key || `tenant:${tenantId}`,project=params.get("project") || (subjectKey.startsWith("project:")?subjectKey.slice(8):"all");
  const periodReports=period?.reports || [],subjectReports=periodReports.filter(report=>report.subject.key===subjectKey),report=subjectReports.find(item=>item.id===params.get("report")) || [...subjectReports].sort((a,b)=>b.generatedAt.localeCompare(a.generatedAt))[0];
  const latest=[...periodReports].sort((a,b)=>b.generatedAt.localeCompare(a.generatedAt))[0];
  const overview=/^(tenant|project):/.test(subjectKey)?latest?.overviewSnapshot:report?.workOverview;
  const route={tab:"periods",period:period?.key,subject:subjectKey,project,report:report?.id};
  const openAction=(kind,ruleId)=>{setReason("");setValue(String(report.assessments[ruleId]?.value || 0));setEvidence(report.assessments[ruleId]?.evidence || "");setAction({kind,ruleId,reportId:report.id,revision:report.revision,tenantId});};
  return <><PageHead title="周期绩效" subtitle="先核对工作情况，再确认个人与 Agent 的考核分。租户、项目不考核。"><Button disabled={!!ownerProblem} onClick={()=>setGenerating({})}>模拟周期生成</Button><Button disabled={!!ownerProblem} onClick={()=>setEditing({})}>配置考核方案</Button></PageHead>
    <nav className="actions" aria-label="绩效页面"><Link className="btn" aria-current={tab==="periods"?"page":undefined} to={href({tab:"periods"})}>周期列表</Link><Link className="btn" aria-current={tab==="policies"?"page":undefined} to={href({tab:"policies"})}>方案、规则与模板</Link></nav>
    <Notice>LOCAL_SIMULATION · 本地可交互原型，无真实 scheduler。各周期最后一天 00:00 截止，该日全天不在本次数据范围内。评分从规则与固定证据计算，专业评估须有证据和理由。</Notice>
    {ownerProblem && <Notice tone="warn">{ownerProblem}</Notice>}
    {tab==="policies" ? <>{ownerProblem && <Notice>{ownerProblem}</Notice>}{policies.length?policies.map(policy=><Section key={policy.id} title={policy.name} extra={<Badge>{policy.enabled?"已启用":"已停用"}</Badge>}><p>{policy.cycles.map(cycle=>labels[cycle]).join(" / ")} · {policy.timezone} · 修订 {policy.revision}</p><p>对象：{policy.subjectKeys.map(key=>performanceSubjects(s).find(subject=>subject.key===key)?.name || key).join("、")}</p><p>基础分 {policy.baseScore} · 上限 {policy.maxScore} · {policy.rules.length} 条规则</p><Button disabled={!!ownerProblem} onClick={()=>setEditing(policy)}>编辑方案、细则与模板</Button><details><summary>固定方案内容与历史</summary><pre style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{JSON.stringify(policy,null,2)}</pre></details></Section>):<Empty title="尚未配置考核方案" text="owner 可定义成员、多个周期、加减分细则和报告模板。" />}</> : period ? <>
      <Section title={`${labels[period.cycle]} · ${period.periodStart} — ${period.periodEnd}`} extra={<Link to={href({tab:"periods"})}>返回周期列表</Link>}><p>{period.timezone} · 截点 {latest?.triggerLabel}</p><div className="form-grid">
        <Field label="查看主体" value={subjectKey} options={[{value:`tenant:${tenantId}`,label:"租户汇总 · 不计分"},...s.projects.map(item=>({value:`project:${item.id}`,label:`${item.name} · 项目汇总，不计分`})),...[...new Map(periodReports.map(item=>[item.subject.key,item.subject])).values()].map(subject=>({value:subject.key,label:`${subject.name} · ${subject.kind}`}))]} onChange={subject=>nextLocation({...route,subject,project:subject.startsWith("project:")?subject.slice(8):"all",report:null})} />
        <Field label="工作项目范围" value={project} disabled={subjectKey.startsWith("project:")} options={[{value:"all",label:"全部项目"},{value:"unknown",label:"项目归属未知"},...s.projects.map(item=>({value:item.id,label:item.name}))]} onChange={project=>nextLocation({...route,project})} />
        {subjectReports.length>1 && <Field label="考核方案报告" value={report?.id || ""} options={subjectReports.map(item=>({value:item.id,label:`${item.policySnapshot.name} · 修订 ${item.revision}`}))} onChange={report=>nextLocation({...route,report})} />}
      </div>{/^(tenant|project):/.test(subjectKey) && <p className="muted">汇总依据：报告 {latest?.id} 生成时的独立租户快照 · {latest?.generatedAt}；不会把成员分数相加。</p>}</Section>
      <WorkOverview overview={overview} project={project} subjectKey={subjectKey} />
      {/^(human|agent):/.test(subjectKey) ? report?<ScoreReport report={report} ownerProblem={ownerProblem} onAction={(kind,ruleId)=>kind==="regenerate"?setGenerating({report}):openAction(kind,ruleId)} />:<Empty title="此主体尚无本期报告" />:<Section title="本期 Human / Agent 考核报告">{periodReports.map(item=><p key={item.id}><Link to={href({...route,subject:item.subject.key,report:item.id,project:"all"})}>{item.subject.name} · {item.subject.kind} · {item.policySnapshot.name}</Link> <Badge>{item.status}</Badge> · {scoreText(item)}</p>)}</Section>}
    </> : <Section title="已生成周期 · 时间倒序">{periods.length?periods.map(item=><Link className="resource-card" key={item.key} to={href({tab:"periods",period:item.key,subject:`tenant:${tenantId}`,project:"all"})}><div className="grow"><h3>{labels[item.cycle]} · {item.periodStart} — {item.periodEnd}</h3><p>{item.timezone} · {new Set(item.reports.map(report=>report.subject.key)).size} 个考核对象 · {item.reports.filter(report=>report.status==="已确认").length} 份已确认</p><small>查看租户、项目工作汇总与成员考核明细</small></div></Link>):<Empty title="尚无周期绩效报告" text="先配置方案，再使用“模拟周期生成”生成已到截点的周期。旧工作没有历史证据时将显示未知。" />}{params.get("period") && <Notice tone="warn">所选周期不存在于当前租户，已返回周期列表。</Notice>}</Section>}
    {editing && <PolicyEditor key={`${tenantId}:${editing.id || "new"}`} policy={editing.id?editing:null} onClose={()=>setEditing(null)} toast={toast} />}
    {generating && <GenerateDialog key={`${tenantId}:${generating.report?.id || "new"}`} policies={policies} report={generating.report} onConfigure={()=>{setGenerating(null);setEditing({});}} onClose={()=>setGenerating(null)} toast={toast} />}
    {action && <Modal title={action.kind==="confirm"?"确认固定评分":"有证据的专业校准"} onClose={()=>setAction(null)} draftValue={{reason,value,evidence}}><ActionForm label="保存决定" onCancel={()=>setAction(null)} onSubmit={({dismiss})=>{performanceCommand({...action,action:action.kind,reason,value,evidence});dismiss();toast("已保存决定与历史");}}>{action.kind==="calibrate" && <><Field label="专业评估次数" type="number" value={value} onChange={setValue} /><Field label="HTTP(S) 证据地址" type="url" value={evidence} onChange={setEvidence} required /></>}<Field label="专业校准或确认理由" type="textarea" value={reason} onChange={setReason} required /><Notice>自动完成与质量事实不能手改；专业校准会使本修订回到待复核，原确认保留在历史。</Notice></ActionForm></Modal>}
  </>;
}
