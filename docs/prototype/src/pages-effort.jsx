import { useViewState } from "./view-state";
import { formatTimestamp } from "./formatting";
import React, { useState } from "react";
import { useStore, saveHumanEffort, reviewHumanEffort, myEffortTasks, humanConfirmedHours, projectIsArchived } from "./store";
import { PageHead, Section, Field, Button, Badge, Link, Empty, Notice, Metric, Modal } from "./ui";

const isHuman = (record) => !record.durationKind || record.durationKind === "Human 工时";
const mine = (record) => record.ownerId ? record.ownerId === "H-lin" : record.owner === "林予";
const measured = (value, unit = "") => value !== undefined && value !== null && value !== "" && Number.isFinite(Number(value))
  ? `${value}${unit}` : "未采集";
const stateLabel = (record) => record.effortState === "confirmed" ? "本人已确认"
  : record.effortState === "calibrated" ? "已校准 · 待本人确认"
  : humanConfirmedHours(record) !== null ? "历史已提交" : "待校准";
const recordTaskPath = (s, record) => { const work=s.work.find(item=>item.id===record.work); return work ? `/p/${work.project}/work-items/${work.id}?tab=analysis` : "/work"; };
const workLink = (s, record) => {
  const work = s.work.find((item) => item.id === record.work);
  return work ? <Link to={`/p/${work.project}/work-items/${work.id}`}>{work.title}</Link> : "关联任务已不可用";
};
function EffortNumbers({ record }) {
  const actual = humanConfirmedHours(record);
  return <dl className="record-fields">
    <dt>投入时区</dt><dd>{record.timezone || "未记录"}</dd>
    <dt>工作量类别</dt><dd>{record.category || "未记录"}</dd>
    <dt>主管审核</dt><dd>{record.reviewState === "returned" ? "已退回，需本人更正并重新确认" : record.status === "已接受" ? "已审核" : "待审核"}</dd>
    <dt>本人预估</dt><dd>{measured(record.estimateHours, " 小时")}</dd>
    <dt>来源建议</dt><dd>{measured(record.suggestedHours, " 小时")}{record.suggestionSource && ` · ${record.suggestionSource}`}</dd>
    <dt>校准投入</dt><dd>{measured(record.calibratedHours, " 小时")}</dd>
    <dt>确认实际投入</dt><dd data-effort-actual>{actual === null ? "尚未本人确认" : `${actual} 小时`}</dd>
    {!record.effortState && record.hours !== undefined && <><dt>历史记录</dt><dd>{record.hours} 小时 · {record.status}{actual === null ? "，仅作校准参考" : "，沿用历史提交事实"}</dd></>}
    {record.legacyHours !== undefined && <><dt>原记录参考</dt><dd>{record.legacyHours} 小时</dd></>}
  </dl>;
}
function TelemetryDetail({ s, record, embedded = false }) {
  return <>
    {!embedded && <PageHead title={record.name} subtitle={`${record.owner} · ${record.date} · 运行指标只读`}><Link className="btn" to={recordTaskPath(s, record)}>返回任务分析</Link></PageHead>}
    <Section title="Agent 运行遥测" extra={<Badge>只读</Badge>}>
      <Notice>墙钟时间、模型 / 工具耗时、Token 和费用独立展示，不折算成人力投入。缺失指标显示未采集。</Notice>
      <dl className="record-fields">
        <dt>关联任务</dt><dd>{workLink(s, record)}</dd>
        <dt>墙钟时间</dt><dd>{measured(record.wallClockHours ?? (record.durationKind === "Agent 墙钟" ? record.hours : undefined), " 小时")}</dd>
        <dt>模型 / 工具耗时</dt><dd>{measured(record.toolHours ?? (record.durationKind === "模型 / 工具耗时" ? record.hours : undefined), " 小时")}</dd>
        <dt>输入 Token</dt><dd>{measured(record.inputTokens)}</dd>
        <dt>输出 Token</dt><dd>{measured(record.outputTokens)}</dd>
        <dt>总 Token</dt><dd>{measured(record.tokens)}</dd>
        <dt>费用</dt><dd>{measured(record.cost, record.currency ? ` ${record.currency}` : "（币种未采集）")}</dd>
        <dt>来源说明</dt><dd>{record.note || "未采集"}</dd>
      </dl>
    </Section>
  </>;
}
function HumanDetail({ s, record, toast, embedded = false }) {
  const [hours, setHours] = useState(String(record.calibratedHours ?? record.estimateHours ?? record.legacyHours ?? record.hours ?? ""));
  const [note, setNote] = useState(record.note || "");
  const [timezone, setTimezone] = useState(record.timezone || "");
  const [category, setCategory] = useState(record.category || "");
  const [reviewReason, setReviewReason] = useState("");
  const [error, setError] = useState("");
  const readOnly=s.offline || projectIsArchived(s,s.work.find(work=>work.id===record.work)?.project);
  const canEdit = mine(record) && s.role !== "external" && !readOnly;
  const run = (action) => {
    try { saveHumanEffort({ id: record.id, revision: record.revision || 1, action, calibratedHours: hours, note, timezone, category }); setError(""); toast(action === "confirm" ? "本人实际投入已确认" : "校准已保存，等待本人确认"); }
    catch (e) { setError(e.message); }
  };
  return <>
    {!embedded && <PageHead title={record.name} subtitle={`${record.owner} · ${record.date} · 修订 ${record.revision || 1}`}><Link className="btn" to={recordTaskPath(s, record)}>返回任务分析</Link></PageHead>}
    <div className="detail-grid">
      <Section title="Human 工作量" extra={<Badge>{stateLabel(record)}</Badge>}>
        <p>{workLink(s, record)}</p><EffortNumbers record={record} />
        {record.confirmedBy && <p className="muted">确认人：{record.owner} · {formatTimestamp(record.confirmedAt)}</p>}
        {!record.effortState && humanConfirmedHours(record) !== null && <Notice>历史提交记录保留原事实；没有补造新的确认人或确认时间。</Notice>}
      </Section>
      <Section title={canEdit ? "本人校准与确认" : "仅本人可确认"}>
        {canEdit ? <>
          <Notice>依据实际参与任务的投入校准，不从消息数量或聊天时长推断。更正已确认值后需要重新确认。</Notice>
          <Field label="投入时区" value={timezone} onChange={setTimezone} placeholder="Asia/Singapore" required />
          <Field label="工作量类别" value={category} onChange={setCategory} required />
          <Field label="校准投入（小时）" type="number" min="0" max="24" step="0.25" value={hours} onChange={setHours} required />
          <Field label="校准依据" value={note} onChange={setNote} type="textarea" required />
          {error && <p role="alert" className="muted">{error}</p>}
          <div className="actions spaced">
            <Button onClick={() => run("calibrate")}>保存校准</Button>
            <Button variant="primary" disabled={record.effortState !== "calibrated" || Number(hours) !== record.calibratedHours || note !== record.note || timezone !== record.timezone || category !== record.category} onClick={() => run("confirm")}>本人确认实际投入</Button>
          </div>
          <small>同日所有已确认实际投入合计不超过 24 小时。预估和未确认校准不计为实际。</small>
        </> : <Notice>当前记录属于 {record.owner}。可查看事实，不能替他人校准或确认。</Notice>}
      </Section>
    </div>
    {s.role === "owner" && !readOnly && <Section title="主管审核">
      <Notice>本人确认与主管审核分别记录。退回后由本人更正并重新确认；已上报报告保留原快照。</Notice>
      {record.reviewReason && <p>上次审核：{record.reviewReason}</p>}
      <Field label="审核或退回原因" value={reviewReason} onChange={setReviewReason} required />
      <div className="actions spaced">{[["accept", "审核通过"], ["return", "退回更正"]].map(([action, label]) => <Button key={action} disabled={humanConfirmedHours(record) === null || record.status === "已接受"} onClick={() => {
        try { reviewHumanEffort(record.id, action, reviewReason, record.revision || 1); setError(""); toast(label); } catch (e) { setError(e.message); }
      }}>{label}</Button>)}</div>
      {error && !canEdit && <p role="alert">{error}</p>}
    </Section>}
    <Section title="修订历史">
      {record.history?.length ? record.history.map((entry, index) => <details key={index}><summary>修订 {entry.revision || 1} · {entry.effortState ? stateLabel(entry) : entry.status} · {entry.changedAt ? formatTimestamp(entry.changedAt) : "原记录"}</summary><EffortNumbers record={entry} /><p>{entry.note}</p>{entry.reason && <p>审核依据：{entry.reason}</p>}</details>) : <p className="muted">尚无修订。每次校准和确认都保留先前数值与状态。</p>}
    </Section>
  </>;
}
export function TaskEffort({ work, toast }) {
  const s=useStore();
  const [selected,setSelected]=useViewState("effort-record",""), [calibrating,setCalibrating]=useState(false);
  const [form,setForm]=useState({date:new Date().toLocaleDateString("en-CA"),timezone:Intl.DateTimeFormat().resolvedOptions().timeZone,category:"任务投入",hours:"",note:""});
  const [error,setError]=useState("");
  const records=s.resources.worklogs.filter(record=>record.work===work.id);
  const human=records.filter(isHuman), agents=records.filter(record=>!isHuman(record));
  const confirmed=human.filter(record=>humanConfirmedHours(record)!==null);
  const selectedRecord=records.find(record=>record.id===selected);
  const editable=myEffortTasks(s).some(item=>item.id===work.id)&&s.role!=="external";
  return <Section title="投入与运行分析">
    <Notice>自动汇总本任务已有投入和运行证据。Human 未确认的投入不计入实际；任务跨度、消息量和 Agent 耗时不折算成人的工时。原型不连接真实遥测。</Notice>
    <div className="metrics"><Metric label="Human 已确认投入" value={confirmed.length ? `${confirmed.reduce((sum,r)=>sum+humanConfirmedHours(r),0)} 小时` : "未知"} detail={`${confirmed.length} 条已确认记录 · ${human.length-confirmed.length} 条待校准 / 确认`} /><Metric label="Agent 运行记录" value={agents.length} detail="各次运行独立显示，不相加并行墙钟时间" /></div>
    <h3>Human 实际参与</h3>
    {human.length ? human.map(record=><div className="attention-row" key={record.id}><div className="grow"><strong>{record.owner} · {record.date}</strong><p>{humanConfirmedHours(record)===null ? "实际投入未知" : `${humanConfirmedHours(record)} 小时`} · {stateLabel(record)}</p></div><Button onClick={()=>setSelected(record.id)}>查看依据 / 校准</Button></div>) : <p className="muted">没有本人确认的投入证据，不能推算实际工时。</p>}
    {editable && <><Button disabled={s.offline || projectIsArchived(s,work.project)} onClick={()=>{setError("");setCalibrating(true);}}>校准本人实际投入</Button><p className="muted">可选：按日期补充本人实际参与依据，保存后仍需本人确认，不要求先新建或预估工时。</p></>}
    <h3>Agent 运行</h3>
    {agents.length ? agents.map(record=><div className="attention-row" key={record.id}><div className="grow"><strong>{record.owner} · {record.date}</strong><p>墙钟：{measured(record.wallClockHours ?? (record.durationKind==="Agent 墙钟" ? record.hours : null)," 小时")} · Token：{measured(record.tokens)} · 费用：{measured(record.cost,record.currency ? ` ${record.currency}` : "（币种未采集）")}</p></div><Button onClick={()=>setSelected(record.id)}>查看运行证据</Button></div>) : <p className="muted">未采集运行遥测。</p>}
    {selectedRecord && <Modal wide title="任务投入证据" onClose={()=>setSelected("")}>{isHuman(selectedRecord) ? <HumanDetail key={`${selectedRecord.id}:${selectedRecord.revision||1}`} s={s} record={selectedRecord} toast={toast} embedded /> : <TelemetryDetail s={s} record={selectedRecord} embedded />}</Modal>}
    {calibrating && <Modal title="校准本人实际投入" draftValue={form} onClose={()=>setCalibrating(false)}><Notice>目标：{work.title}。仅保存待本人确认的校准值；不会立即进入报告实际投入。已有日期记录会打开原记录，不创建重复条目。</Notice>
      <Field label="投入日期" type="date" value={form.date} onChange={date=>setForm({...form,date})} required />
      <Field label="投入时区" value={form.timezone} onChange={timezone=>setForm({...form,timezone})} required />
      <Field label="投入类别" value={form.category} onChange={category=>setForm({...form,category})} required />
      <Field label="实际参与投入（小时）" type="number" min="0" max="24" step="0.25" value={form.hours} onChange={hours=>setForm({...form,hours})} required />
      <Field label="实际参与依据" type="textarea" value={form.note} onChange={note=>setForm({...form,note})} required />
      {error && <p role="alert">{error}</p>}
      <Button variant="primary" onClick={()=>{try {const existing=human.find(record=>mine(record)&&record.date===form.date);if(existing){setSelected(existing.id);setCalibrating(false);toast("已打开当日原记录，请核对后校准");return;}const id=saveHumanEffort({action:"calibrate",workId:work.id,...form,calibratedHours:form.hours});setCalibrating(false);setSelected(id);toast("校准已保存，请本人确认后计入实际投入");}catch(e){setError(e.message);}}}>保存校准并核对</Button>
    </Modal>}
  </Section>;
}
// Historical evidence URLs remain readable, but there is no independent creation workflow.
export function EffortPage({ recordId, toast }) {
  const s=useStore();
  const record=s.resources.worklogs.find(item=>item.id===recordId);
  if(record)return isHuman(record) ? <HumanDetail key={`${record.id}:${record.revision||1}`} s={s} record={record} toast={toast} /> : <TelemetryDetail s={s} record={record} />;
  return <><PageHead title="历史投入证据" subtitle="投入分析已融入任务和周期报告；此地址仅保留历史记录访问。" /><Link className="btn" to="/work">查看我的任务</Link><Section title="已有证据">{s.resources.worklogs.filter(record=>s.project==="all"||s.work.some(work=>work.id===record.work&&work.project===s.project)).map(record=><p key={record.id}><Link to={`/worklogs/${record.id}`}>{record.name} · {record.owner} · {record.date}</Link> · <Link to={recordTaskPath(s,record)}>任务投入分析</Link></p>)}</Section></>;
}
