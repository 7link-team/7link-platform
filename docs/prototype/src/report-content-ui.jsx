import React from "react";
import { emptyReportContent, normalizeReportContent, reportNextPlanItems, reportContentFacts, prepareReportRegeneration, adoptReportGeneratedContent, reportDetailFingerprint, reportRetroFingerprint, reportReasons, reportDispositions, reportFactKind, reportFactReference, reportFactTitle } from "./report-content";
import { reportCurrentPlanBindingProblem } from "./plans";
import { reportAuthor } from "./report-authorization";
import { Section, Notice, Button, Field } from "./ui";
import { id } from "./store";
const retroLabels = { went_well: "做得好的方面", problems: "问题与影响", root_causes: "根因分析", improvements: "改进建议" };
function contentView(content, binding, actor) {
  try { return { content: normalizeReportContent(content, binding, content, {}, actor) }; }
  catch (error) { return { error: error.message }; }
}
function currentPlanView(state, report) {
  if (!report.currentPlanBinding) return { items: [], problem: "尚未采用固定本期计划；可保留计划外事项，关联计划前请先采用真实基线。" };
  const problem = state ? reportCurrentPlanBindingProblem(state, report, report.currentPlanBinding) : "固定计划来源暂时无法核对。";
  return { items: problem ? [] : report.currentPlanBinding.snapshot.items, problem };
}
function PlanItemSource({ item, plan }) {
  if (!item.plan_item_id) return <p className="muted" data-report-plan-source>计划外事项 · 未声明计划项关联</p>;
  const original = plan.items.find(candidate => candidate.id === item.plan_item_id);
  return <p className="muted" data-report-plan-source>固定计划项 · <span translate="no">{item.plan_item_id}</span> · {original?.title || "原引用无法核对，记录保留"}{original?.workId && <span translate="no"> · {original.workId}</span>}</p>;
}
function ContentError({ message }) {
  return <Notice tone="warn">报告章节无法读取：{message} 原始内容仍保留；请核对来源或恢复草稿，不会自动清空或补造章节。</Notice>;
}
export function ReportContentReader({ content: rawContent, report = {}, state }) {
  const content = rawContent;
  if (!content && report.contentRequired === true) return <ContentError message="此报告必须保留结构化四章节，请恢复原章节草稿。" />;
  if (!content) return <Notice tone="warn">旧报告只有原正文，未补造四章节或作者确认；请显式建立四章节草稿。</Notice>;
  const actor = report.authorId && report.authorKind ? { id: report.authorId, kind: report.authorKind } : state ? reportAuthor(state, report) : { id: report.subjectKey?.split(":")[1], kind: report.subjectKey?.startsWith("agent:") ? "Agent" : "Human" };
  const view = contentView(content, report.nextPlanBinding, actor);
  if (view.error) return <ContentError message={view.error} />;
  let facts = [];
  try { if (state) facts = reportContentFacts(state, report); }
  catch (error) { return <><ContentError message={error.message} /><ReportContentBody content={view.content} plan={currentPlanView(state, report)} facts={facts} /></>; }
  return <ReportContentBody content={view.content} plan={currentPlanView(state, report)} facts={facts} />;
}
function FreeTaskEvidence({ fact }) {
  if (!fact) return <p className="muted">非交付任务证据暂时无法核对，原引用保留。</p>;
  return <div data-report-free-evidence><p>{fact.evidence}</p><p className="muted">{fact.time} · {fact.actor.kind} {fact.actor.id}{fact.actor.kind === "Agent" ? ` · 本地 Agent 模拟，由 Human ${fact.actor.recordedBy} 记录` : " · 本人记录"}</p></div>;
}
function OperationEvidence({ fact }) {
  if (!fact) return <p className="muted">裁决/交付证据暂时无法核对，原引用保留。</p>;
  return <div data-report-operation-evidence><p>{reportFactTitle(fact)}</p><p className="muted">明确本地模拟 · {fact.actor.kind} {fact.actor.id} · {fact.time} · 固定版本 {fact.version}</p>{fact.reason && <p>{fact.reason}</p>}{fact.kind === "DELIVERY" && <p>本地模拟 SENT 回执 · 接收方 {fact.deliverySnapshot.recipient} · 不代表真实远程发布或接收</p>}</div>;
}
function ReportContentBody({ content, plan, facts = [] }) {
  return <div className="stack" data-report-content>
    <Section title="完成工作"><p>{content.summary.narrative || "统计说明未填写"}</p>
      <p className="muted">声明条目 {content.summary.completed_count ?? "未声明"} · 接受工作 {content.summary.accepted_work_items ?? "未声明"} · 执行成本 {content.summary.cost_micros ?? "未知"}</p>
      {content.completed.length ? content.completed.map(item => <article key={item.key}><h3>{item.title}</h3><PlanItemSource item={item} plan={plan} /><p><span translate="no">{item.fact_ref.kind}:{item.fact_ref.id} · {item.fact_ref.event_id}</span> · {item.fact_ref.kind === "FREE_TASK_EVIDENCE" ? "固定计划完成证据 · 不属于交付接受" : `固定版本 ${item.fact_ref.version ?? "未声明"}`}</p>{item.fact_ref.kind === "FREE_TASK_EVIDENCE" && <FreeTaskEvidence fact={facts.find(fact => fact.kind === "FREE_TASK_EVIDENCE" && fact.itemId === item.fact_ref.id && fact.id === item.fact_ref.event_id)} />}{["DECISION", "DELIVERY"].includes(item.fact_ref.kind) && <OperationEvidence fact={facts.find(fact => JSON.stringify(reportFactReference(fact)) === JSON.stringify(item.fact_ref))} />}{item.note && <p>{item.note}</p>}</article>) : <p className="muted">没有声明完成条目。</p>}
    </Section>
    <Section title="未完成与处置">{content.uncompleted.length ? content.uncompleted.map(item => <article key={item.key}><h3>{item.title}</h3><PlanItemSource item={item} plan={plan} /><p>{reportReasons[item.reason] || "原因未填写"} · {reportDispositions[item.carry_over] || "处置未填写"}</p><p>{item.detail || "尚无说明"}</p><p className="muted">{item.detail_confirmation ? `作者确认：${item.detail_confirmation.actorKind} ${item.detail_confirmation.actorId}${item.detail_confirmation.actorKind === "Agent" ? ` · 本地模拟，由 Human ${item.detail_confirmation.recordedBy || "记录人未知"} 记录` : ""}` : "作者尚未确认当前原因与处置"}</p></article>) : <p className="muted">尚无未完成条目；无事项的声明规则待确认。</p>}</Section>
    <Section title="复盘">{Object.entries(retroLabels).map(([key,label]) => <div key={key}><h3>{label}</h3><ul>{(content.retrospective[key] || []).map((item,index) => <li key={index}>{typeof item === "string" ? item : item.text}{item.propose_memory && <span className="muted"> · 提议知识回流，尚未入库</span>}</li>)}</ul></div>)}
      {content.retrospective.ai_drafted && <Notice tone="warn">AI/模板复盘尚未经作者确认。</Notice>}
    </Section>
    <Section title="下期承诺章节"><p className="muted">仅来自已采用的真实计划版本，完整内容见下方计划固定引用。</p>{content.next_plan.map(item => <p key={item.key}>{item.title} · <span translate="no">{item.plan_item_id}</span></p>)}</Section>
  </div>;
}
export function ReportContentEditor({ state, form, setForm }) {
  const [proposalError, setProposalError] = React.useState("");
  const [regenerationSections, setRegenerationSections] = React.useState([]);
  const [regenerationError, setRegenerationError] = React.useState("");
  const [regenerationStatus, setRegenerationStatus] = React.useState("");
  const actor = reportAuthor(state, form);
  try { reportNextPlanItems(form.nextPlanBinding); }
  catch (error) { return <ContentError message={error.message} />; }
  const view = form.content ? contentView(form.content, form.nextPlanBinding, actor) : {};
  const content = view.content;
  const suggestionView = form.suggestedContent != null ? contentView(form.suggestedContent, null, actor) : {};
  const suggested = suggestionView.content;
  const setContent = mutate => setForm(previous => ({ ...previous, content: mutate(previous.content) }));
  if (view.error) return <ContentError message={view.error} />;
  if ((form.reviewedDetailKeys != null && (!Array.isArray(form.reviewedDetailKeys) || form.reviewedDetailKeys.some(key => typeof key !== "string"))) ||
    (form.reviewedRetrospective != null && typeof form.reviewedRetrospective !== "boolean")) return <ContentError message="作者核对选择格式无效，请核对恢复的草稿。" />;
  if (!content && form.contentRequired === true) return <ContentError message="此报告必须保留结构化四章节，请恢复原章节草稿。" />;
  if (!content) return <Section title="报告四章节"><Notice tone="warn">原正文保留。显式迁移只建立空草稿，保存时在原编号下保留旧修订与固定历史；不会猜测成果、复盘或作者确认。</Notice><Button onClick={() => setForm(previous => ({ ...previous, content: emptyReportContent(previous.nextPlanBinding), migrateLegacyContent: true }))}>建立四章节草稿</Button></Section>;
  let facts;
  try { facts = reportContentFacts(state, { ...form, submission: null }); }
  catch (error) { return <ContentError message={error.message} />; }
  const appendFact = fact => setForm(previous => {
    const free = fact.kind === "FREE_TASK_EVIDENCE", planned = free && previous.currentPlanBinding?.snapshot.items.find(item => item.id === fact.itemId);
    const item = { key: id("RC"), title: reportFactTitle(fact, planned), goal_id: free ? planned?.goalId || "" : "", kr_id: free ? planned?.krId || "" : "", plan_item_id: free ? fact.itemId : "", fact_ref: reportFactReference(fact), note: "" };
    const refs = (Array.isArray(previous.references) ? previous.references : []).map(ref => reportFactKind(fact) === "WORK_ITEM" && ref?.kind === "work" && ref.id === fact.workId && ref.version === fact.version ? { ...ref, acceptanceFactIds: [...new Set([...(ref.acceptanceFactIds || []),fact.id])] } : ref);
    if (reportFactKind(fact) === "WORK_ITEM" && !refs.some(ref => reportFactKind(fact) === "WORK_ITEM" && ref?.kind === "work" && ref.id === fact.workId && ref.version === fact.version)) refs.push({ kind: "work", id: fact.workId, version: fact.version, acceptanceFactIds: [fact.id] });
    return { ...previous, references: refs, content: { ...previous.content, completed: [...previous.content.completed,item] } };
  });
  const plan = currentPlanView(state, form);
  const referenceOptions = (item, completed) => {
    const used = [...content.completed, ...content.uncompleted].filter(other => other.key !== item.key).map(other => other.plan_item_id);
    const eligible = plan.items.filter(candidate => (!completed || (candidate.kind === "WORK_ITEM" && item.fact_ref.kind === "WORK_ITEM" && candidate.workId === item.fact_ref.id) || (candidate.kind === "FREE_TASK" && item.fact_ref.kind === "FREE_TASK_EVIDENCE" && candidate.id === item.fact_ref.id)) && (!used.includes(candidate.id) || candidate.id === item.plan_item_id));
    const options = [{ value: "", label: "计划外事项 · 不关联计划项" }, ...eligible.map(candidate => ({ value: candidate.id, label: `${candidate.title} · ${candidate.id}` }))];
    if (item.plan_item_id && !eligible.some(candidate => candidate.id === item.plan_item_id)) options.push({ value: item.plan_item_id, label: `原引用无法核对 · ${item.plan_item_id}`, disabled: true });
    return options;
  };
  const linkPlanItem = (key, section, planItemId) => setForm(previous => {
    const original = plan.items.find(item => item.id === planItemId);
    return { ...previous, ...(section === "uncompleted" ? { reviewedDetailKeys: (previous.reviewedDetailKeys || []).filter(item => item !== key) } : {}),
      content: { ...previous.content, [section]: previous.content[section].map(item => item.key !== key ? item : {
        ...item, plan_item_id: planItemId,
        ...(section === "uncompleted" ? { work_item_id: original?.workId || "", detail_confirmation: null } : original ? { goal_id: original.goalId || "", kr_id: original.krId || "" } : {}),
      }) } };
  });
  const editUncompleted = (key, field, value) => setForm(previous => ({ ...previous, reviewedDetailKeys: (previous.reviewedDetailKeys || []).filter(item => item !== key), content: { ...previous.content, uncompleted: previous.content.uncompleted.map(item => item.key === key ? { ...item, [field]: value } : item) } }));
  return <div className="stack" data-report-content-editor>
    {actor?.kind === "Agent" && <Notice>负责人类操作本地 Agent 作者核对模拟，记录实际操作者；勾选不代表真实 Agent 已核对，也不替代 Human 最终审核。</Notice>}
    {plan.problem && <Notice tone="warn">{plan.problem}</Notice>}
    {suggestionView.error && <ContentError message={`生成建议无效：${suggestionView.error}`} />}
    <details data-report-regeneration>
      <summary>按最新事实重填章节</summary>
      <p className="muted">替换所选章节中的条目、说明及原因确认；保留未选章节、周期解释与人工复盘。只修改当前表单，保存后保留原报告修订。</p>
      <fieldset><legend>选择要重填的章节</legend>
        {[["completed", "完成工作"], ["uncompleted", "未完成与处置"], ["next_plan", "下期计划"]].map(([key, label]) => <label className="switch-label" key={key}>
          <input type="checkbox" name={`regenerate-${key}`} autoComplete="off" checked={regenerationSections.includes(key)} onChange={event => { setRegenerationSections(old => event.target.checked ? [...old, key] : old.filter(section => section !== key)); setRegenerationError(""); setRegenerationStatus(""); }} />{label}
        </label>)}
      </fieldset>
      <Button onClick={() => {
        try {
          const stored = state.resources.reports.find(report => report.id === form.id);
          if (!stored || stored.revision !== form.revision) throw Error("报告已变化，请重新打开当前修订。");
          const next = prepareReportRegeneration(state, form, regenerationSections);
          setForm(next); setRegenerationError(""); setRegenerationStatus("已重填所选章节，尚未保存；原因与事实仍需核对。");
        } catch (error) { setRegenerationError(error.message); setRegenerationStatus(""); }
      }}>重填所选章节</Button>
      {regenerationError && <div role="alert"><Notice tone="danger">{regenerationError}</Notice></div>}
      {regenerationStatus && <p role="status" aria-live="polite">{regenerationStatus}</p>}
    </details>
    {suggested && <Section title="本期条目生成建议">
      <p className="muted">可核对完成条目 {suggested.completed.length} · 计划未完成 / 待核对条目 {suggested.uncompleted.length}。采用会补入新条目，保留人工说明与复盘；冲突须先处理，原因仍需作者确认。</p>
      <details><summary>查看建议条目</summary>{suggested.completed.map(item => <p key={item.key}>完成 · {item.title}</p>)}{suggested.uncompleted.map(item => <p key={item.key}>未完成 / 待核对 · {item.title} · {item.detail}</p>)}</details>
      <Button onClick={() => { try { setForm(adoptReportGeneratedContent(state, form)); setProposalError(""); } catch (error) { setProposalError(error.message); } }}>采用本期条目建议</Button>
      {proposalError && <div role="alert"><Notice tone="danger">{proposalError}</Notice></div>}
    </Section>}
    {Array.isArray(form.generatedContentIssues) && form.generatedContentIssues.length > 0 && <Notice tone="warn">{form.generatedContentIssues.join("；")}</Notice>}
    <Section title="完成工作">
      <Field label="周期事实说明" value={content.summary.narrative} onChange={value => setContent(old => ({ ...old, summary: { ...old.summary, narrative: value } }))} type="textarea" />
      <Notice>提供固定接受工作、非交付任务及本地模拟裁决/发送事实。裁决和发送不代表执行者完成工作，不计入接受工作数；外部接受及下级条目读取器仍未接入。</Notice>
      {facts.filter(fact => !content.completed.some(item => item.fact_ref.event_id === fact.id && item.fact_ref.kind === reportFactKind(fact))).map(fact => <div key={`${fact.kind || "WORK_ITEM"}:${fact.id}`}><p><Button onClick={() => appendFact(fact)}>加入完成事实 · {fact.kind === "FREE_TASK_EVIDENCE" ? `非交付任务 ${fact.itemId}` : ["DECISION", "DELIVERY"].includes(fact.kind) ? reportFactTitle(fact) : `${fact.workId} · v${fact.version}`}</Button> <span translate="no">{fact.id}</span></p>{fact.kind === "FREE_TASK_EVIDENCE" && <FreeTaskEvidence fact={fact} />}{["DECISION", "DELIVERY"].includes(fact.kind) && <OperationEvidence fact={fact} />}</div>)}
      {!facts.length && <p className="muted">当前主体与周期没有可用的固定接受事实；不会将当前状态换算为历史完成。</p>}
      {content.completed.map((item,index) => <fieldset key={item.key}><legend>完成条目 {index + 1}</legend>
        <Field label={`完成标题 ${index+1}`} value={item.title} onChange={title => setContent(old => ({ ...old, completed: old.completed.map(current => current.key===item.key ? {...current,title} : current) }))} />
        <Field label={`完成计划关联 ${index+1}`} value={item.plan_item_id} onChange={value => linkPlanItem(item.key,"completed",value)} options={referenceOptions(item,true)} help="仅列出与该完成事实对应的固定计划项；非交付任务须保留其原计划项关联。" />
        <PlanItemSource item={item} plan={plan} />
        <p translate="no">{item.fact_ref.id} · {item.fact_ref.event_id}{item.fact_ref.kind === "FREE_TASK_EVIDENCE" ? " · 固定计划完成证据" : ` · v${item.fact_ref.version}`}</p>
        {item.fact_ref.kind === "FREE_TASK_EVIDENCE" && <FreeTaskEvidence fact={facts.find(fact => fact.kind === "FREE_TASK_EVIDENCE" && fact.itemId === item.fact_ref.id && fact.id === item.fact_ref.event_id)} />}
        {["DECISION", "DELIVERY"].includes(item.fact_ref.kind) && <OperationEvidence fact={facts.find(fact => JSON.stringify(reportFactReference(fact)) === JSON.stringify(item.fact_ref))} />}
        <Field label={`完成说明 ${index+1}`} value={item.note} type="textarea" onChange={note => setContent(old => ({ ...old, completed: old.completed.map(current => current.key===item.key ? {...current,note} : current) }))} />
        <Button onClick={() => setContent(old => ({ ...old, completed: old.completed.filter(current => current.key!==item.key) }))}>移除完成条目 {index+1}</Button>
      </fieldset>)}
    </Section>
    <Section title="未完成与处置"><Notice>没有原计划项的说明属于作者声明，不能当作已核验的计划结果；原因与处置需要作者逐项确认。结转选择暂不自动创建任务。</Notice>
      {content.uncompleted.map((item,index) => <fieldset key={item.key}><legend>未完成条目 {index+1}</legend>
        <Field label={`未完成标题 ${index+1}`} value={item.title} onChange={v => editUncompleted(item.key,"title",v)} />
        <Field label={`未完成计划关联 ${index+1}`} value={item.plan_item_id} onChange={value => linkPlanItem(item.key,"uncompleted",value)} options={referenceOptions(item,false)} help="引用已采用的固定计划项；变更关联后须重新确认原因与处置。" />
        <PlanItemSource item={item} plan={plan} />
        <Field label={`未完成原因 ${index+1}`} value={item.reason} onChange={v => editUncompleted(item.key,"reason",v)} options={[{value:"",label:"请选择原因"},...Object.entries(reportReasons).map(([value,label]) => ({value,label}))]} />
        <Field label={`原因说明 ${index+1}`} value={item.detail} type="textarea" onChange={v => editUncompleted(item.key,"detail",v)} />
        <Field label={`未完成处置 ${index+1}`} value={item.carry_over} onChange={v => editUncompleted(item.key,"carry_over",v)} options={[{value:"",label:"请选择处置"},...Object.entries(reportDispositions).map(([value,label]) => ({value,label}))]} />
        <label className="check-label"><input type="checkbox" aria-label={`确认原因与处置 ${index+1}`} checked={(form.reviewedDetailKeys || []).includes(item.key) || item.detail_confirmation?.fingerprint === reportDetailFingerprint(item)} onChange={event => setForm(old => ({...old, reviewedDetailKeys:event.target.checked ? [...new Set([...(old.reviewedDetailKeys || []),item.key])] : (old.reviewedDetailKeys || []).filter(key=>key!==item.key),content:{...old.content,uncompleted:old.content.uncompleted.map(current=>current.key===item.key ? {...current,detail_confirmation:null} : current)}}))} />作者已核对当前原因与处置</label>
        <Button onClick={() => setContent(old => ({...old,uncompleted:old.uncompleted.filter(current=>current.key!==item.key)}))}>移除未完成条目 {index+1}</Button>
      </fieldset>)}
      <Button onClick={() => setContent(old => ({ ...old, uncompleted: [...old.uncompleted,{key:id("RU"),title:"",plan_item_id:"",work_item_id:"",reason:"",detail:"",carry_over:"",detail_confirmation:null}] }))}>添加未完成说明</Button>
    </Section>
    <Section title="复盘"><Notice>每行一项；建议不会自动改写工作流或进入记忆库。仅 AI/模板内容需要明确作者确认。</Notice>
      {Object.entries(retroLabels).map(([key,label]) => <Field key={key} label={label} type="textarea" value={content.retrospective[key].map(item => typeof item === "string" ? item : item.text).join("\n")} onChange={value => setForm(previous => ({ ...previous, reviewedRetrospective:false, content:{...previous.content,retrospective:{...previous.content.retrospective,[key]: value.split("\n").map((text,index) => key==="improvements" ? {text,propose_memory:previous.content.retrospective.improvements[index]?.propose_memory || false} : text)}} }))} />)}
      {(content.retrospective.ai_drafted || content.retrospective.origin==="AI") && <label className="check-label"><input type="checkbox" aria-label="确认复盘内容" checked={form.reviewedRetrospective===true || content.retrospective.confirmation?.fingerprint===reportRetroFingerprint(content.retrospective)} onChange={event => setForm(old => ({...old,reviewedRetrospective:event.target.checked,content:{...old.content,retrospective:{...old.content.retrospective,confirmation:null}}}))} />作者已核对当前 AI/模板复盘</label>}
    </Section>
    <Section title="下期承诺章节"><p className="muted">采用真实下期计划后自动展示其固定条目；在计划编辑器修订，不重复输入。</p>{reportNextPlanItems(form.nextPlanBinding).map(item=><p key={item.key}>{item.title}</p>)}</Section>
  </div>;
}
