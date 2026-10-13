import { formatTimestamp } from "./formatting";
import React, { useState } from "react";
import { useStore, saveHumanEffort, reviewHumanEffort, myEffortTasks, humanConfirmedHours } from "./store";
import { PageHead, Section, Field, Button, Badge, Link, Empty, Notice, Metric, Tabs } from "./ui";

const isHuman = (record) => !record.durationKind || record.durationKind === "Human 工时";
const mine = (record) => record.ownerId ? record.ownerId === "H-lin" : record.owner === "林予";
const measured = (value, unit = "") => value !== undefined && value !== null && value !== "" && Number.isFinite(Number(value))
  ? `${value}${unit}` : "未采集";
const stateLabel = (record) => record.effortState === "confirmed" ? "本人已确认"
  : record.effortState === "calibrated" ? "已校准 · 待本人确认"
  : humanConfirmedHours(record) !== null ? "历史已提交" : "待校准";
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
function TelemetryDetail({ s, record }) {
  return <>
    <PageHead title={record.name} subtitle={`${record.owner} · ${record.date} · 运行指标只读`}><Link className="btn" to="/worklogs?tab=agent">返回运行指标</Link></PageHead>
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
function HumanDetail({ s, record, toast }) {
  const [hours, setHours] = useState(String(record.calibratedHours ?? record.estimateHours ?? record.legacyHours ?? record.hours ?? ""));
  const [note, setNote] = useState(record.note || "");
  const [timezone, setTimezone] = useState(record.timezone || "");
  const [category, setCategory] = useState(record.category || "");
  const [reviewReason, setReviewReason] = useState("");
  const [error, setError] = useState("");
  const canEdit = mine(record) && s.role !== "external";
  const run = (action) => {
    try { saveHumanEffort({ id: record.id, revision: record.revision || 1, action, calibratedHours: hours, note, timezone, category }); setError(""); toast(action === "confirm" ? "本人实际投入已确认" : "校准已保存，等待本人确认"); }
    catch (e) { setError(e.message); }
  };
  return <>
    <PageHead title={record.name} subtitle={`${record.owner} · ${record.date} · 修订 ${record.revision || 1}`}><Link className="btn" to="/worklogs">返回工作量</Link></PageHead>
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
    {s.role === "owner" && <Section title="主管审核">
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
export function EffortPage({ recordId, params, toast }) {
  const s = useStore();
  const tab = params.get("tab") === "agent" ? "agent" : "human";
  const tasks = myEffortTasks(s).filter((work) => s.project === "all" || work.project === s.project);
  const [workId, setWorkId] = useState("");
  const [date, setDate] = useState(() => new Date().toLocaleDateString("en-CA"));
  const [estimate, setEstimate] = useState("");
  const [timezone, setTimezone] = useState(Intl.DateTimeFormat().resolvedOptions().timeZone);
  const [category, setCategory] = useState("");
  const [note, setNote] = useState("");
  const [error, setError] = useState("");
  const records = s.resources.worklogs.filter((record) => s.project === "all" || s.work.find((work) => work.id === record.work)?.project === s.project);
  if (recordId) {
    const record = s.resources.worklogs.find((item) => item.id === recordId);
    return record ? isHuman(record) ? <HumanDetail key={`${record.id}:${record.revision || 1}`} s={s} record={record} toast={toast} /> : <TelemetryDetail s={s} record={record} /> : <Empty title="记录不存在于当前租户" text="请返回工作量列表。"><Link className="btn" to="/worklogs">返回工作量</Link></Empty>;
  }
  const human = records.filter(isHuman), own = human.filter(mine), telemetry = records.filter((record) => !isHuman(record));
  const actual = own.reduce((sum, record) => sum + (humanConfirmedHours(record) ?? 0), 0);
  return <>
    <PageHead title="工作量与运行指标" subtitle="Human 投入由本人评估、校准和确认；Agent 运行独立计量。" />
    <Tabs value={tab} items={[["human", "Human 工作量"], ["agent", "Agent 运行指标"]]} onChange={(value) => { location.hash = `/worklogs?tab=${value}`; }} />
    {tab === "human" ? <>
      <div className="metrics">
        <Metric label="本人确认实际投入" value={`${Math.round(actual * 10000) / 10000} 小时`} detail="当前项目范围 · 全部已记录日期" />
        <Metric label="本人待校准 / 待确认" value={own.filter((record) => humanConfirmedHours(record) === null).length} detail="预估与建议不计为实际投入" />
      </div>
      <Section title="从本人任务评估工作量">
        <Notice>先选择自己参与的任务与日期，建立待校准条目。没有计时器，也不根据聊天活动自动推算。</Notice>
        {tasks.length && s.role !== "external" ? <>
          <div className="form-grid">
            <Field label="本人任务" value={workId} onChange={setWorkId} options={[{ value: "", label: "选择本人参与的任务" }, ...tasks.map((work) => ({ value: work.id, label: `${work.id} · ${work.title}` }))]} required />
            <Field label="投入日期" type="date" value={date} onChange={setDate} required />
            <Field label="投入时区" value={timezone} onChange={setTimezone} required />
            <Field label="工作量类别" value={category} onChange={setCategory} required />
            <Field label="本人预估（小时）" type="number" min="0" max="24" step="0.25" value={estimate} onChange={setEstimate} required />
            <Field label="评估依据" value={note} onChange={setNote} required />
          </div>
          {error && <p role="alert">{error}</p>}
          <Button variant="primary" onClick={() => {
            try { const created = saveHumanEffort({ action: "estimate", workId, date, estimateHours: estimate, note, timezone, category }); location.hash = `/worklogs/${created}`; toast("评估已建立，下一步由本人校准"); }
            catch (e) { setError(e.message); }
          }}>建立待校准条目</Button>
        </> : <p className="muted">当前范围没有可评估的本人任务，或当前不是 Human 本人角色。</p>}
      </Section>
      <Section title="Human 记录">
        {human.length ? <div className="work-list">{human.map((record) => <Link className="attention-row" key={record.id} to={`/worklogs/${record.id}`}>
          <div className="grow"><strong>{record.name}</strong><p className="muted">{record.owner} · {record.date} · {humanConfirmedHours(record) === null ? "实际尚未确认" : `${humanConfirmedHours(record)} 小时实际投入`}</p><Badge>{stateLabel(record)}</Badge></div>
        </Link>)}</div> : <Empty title="尚无 Human 记录" text="从本人任务开始评估。" />}
      </Section>
    </> : <Section title="Agent 运行遥测">
      <Notice>只读展示已记录的运行事实。并行 Agent 的墙钟时间不能相加成人力工时，模型 / 工具耗时也不重复计入 Human 投入。</Notice>
      {telemetry.length ? telemetry.map((record) => <div className="attention-row" key={record.id}>
        <div className="grow"><Link to={`/worklogs/${record.id}`}><strong>{record.name}</strong></Link><p className="muted">{record.owner} · {record.date} · {record.durationKind}</p>
          <dl className="record-fields"><dt>墙钟时间</dt><dd>{measured(record.wallClockHours ?? (record.durationKind === "Agent 墙钟" ? record.hours : undefined), " 小时")}</dd><dt>模型 / 工具耗时</dt><dd>{measured(record.toolHours ?? (record.durationKind === "模型 / 工具耗时" ? record.hours : undefined), " 小时")}</dd><dt>Token</dt><dd>{measured(record.tokens)}</dd><dt>费用</dt><dd>{measured(record.cost, record.currency ? ` ${record.currency}` : "（币种未采集）")}</dd></dl>
        </div>
      </div>) : <Empty title="尚未采集 Agent 运行指标" text="不以 Human 评估填补未知遥测。" />}
    </Section>}
  </>;
}
