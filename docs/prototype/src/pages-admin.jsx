import { GoalsPage } from "./pages-goals";
import { formatCurrency, formatTimestamp } from "./formatting";
import React, { useState, useEffect, useRef } from "react";
import { catalog, fieldNames, resourceFields } from "./data";
import { QAResourcePage } from "./pages-qa";
import { Flow } from "./pages-flow";
import { reportCadences, reportSubjects, reportPeriod, dateInZone } from "./reporting";
import {
  useStore,
  update,
  log,
  id,
  saveResource,
  goalProgress, calibrateGoalProgress, goalTransitionProblem, goalWriteProblem,
  resourceCommand,
  assertWrite,
  exportData,
  reportEvidence,
  reportTransitionProblem, reportAuthor, reportReviewAuthority, reportEditProblem,
  generatePeriodicReport,
  saveReportSchedule,
  defectVerificationRuns, defectRecords,
  saveOrganization,
  saveAgent, agentMessages, agentRuntimes, agentModelCompatible, createWorkflowTemplate,
  saveMemberInvitation,
  revokeMemberInvitation,
} from "./store";
import {
  Icon,
  Select,
  useConfirm,
  Form,
  Button,
  IconButton,
  Badge,
  Avatar,
  PageHead,
  Section,
  Progress,
  Link,
  Modal,
  useRecoverableDraft,
  useDraftGuard,
  Field,
  ActionForm,
  Empty,
  Metric,
  Notice,
} from "./ui";
const transitions = {
  goals: [
    ["待审核", "提交审核"],
    ["进行中", "批准目标"],
    ["草稿", "退回修改"],
  ],
  reports: [
    ["已提交", "本人确认并上报"],
    ["已接受", "审核通过"],
    ["草稿", "退回修改"],
  ],
  worklogs: [
    ["已提交", "提交工时"],
    ["已接受", "审核通过"],
    ["草稿", "退回更正"],
  ],
  tests: [
    ["通过", "模拟重验通过"],
    ["失败", "记录失败"],
  ],
  testplans: [
    ["进行中", "开始测试"],
    ["待审核", "提交测试报告"],
  ],
  defects: [
    ["待验证", "提交修复"],
    ["已关闭", "验证通过"],
    ["待修复", "验证失败并重开"],
    ["待修复", "重新打开"],
  ],
  repositories: [
    ["已连接", "模拟增量同步"],
    ["已暂停", "暂停同步"],
  ],
  schedules: [
    ["启用", "启用"],
    ["暂停", "暂停"],
    ["启用", "模拟运行一次"],
  ],
  health: [
    ["处理中", "认领处置"],
    ["已恢复", "标记恢复"],
    ["已关闭", "确认关闭"],
  ],
  knowledge: [
    ["有效", "确认 / 发布"],
    ["已禁用", "禁用记忆"],
  ],
  tools: [
    ["已授权", "批准工具范围"],
    ["已撤销", "撤销授权"],
  ],
  templates: [
    ["已发布", "确认并发布"],
    ["草稿", "另存新草稿"],
  ],
  connectors: [
    ["已连接", "模拟授权连接"],
    ["已暂停", "暂停传输"],
    ["已撤销", "撤销绑定"],
  ],
  models: [
    ["生效", "模拟应用配置"],
    ["待应用", "回滚到待应用"],
  ],
  policies: [
    ["启用", "启用策略"],
    ["暂停", "暂停策略"],
  ],
  approvals: [
    ["已批准", "批准当前版本"],
    ["已拒绝", "拒绝"],
    ["待补充", "退回补充"],
  ],
  invitations: [
    ["待接受", "发送模拟邀请"],
    ["已撤销", "撤销邀请"],
  ],
  deliveries: [
    ["已接受", "记录接收"],
    ["部分接受", "逐项部分接受"],
    ["已拒收", "拒收并退回"],
  ],
};
function GenericResourcePage({ kind, recordId, toast }) {
  const s = useStore();
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState(null);
  const [generation, setGeneration] = useState(null);
  const [goalCalibration, setGoalCalibration] = useState(null);
  const [action, setAction] = useState(null);
  const [reason, setReason] = useState("");
  const [receiptItems, setReceiptItems] = useState([]);
  const [verificationRunId, setVerificationRunId] = useState("");
  const meta = catalog[kind];
  if (!meta) return <Empty title="页面不存在" />;
  const rows = s.resources[kind] || [];
  const record = rows.find((r) => r.id === recordId);
  if (s.role === "external" && !["deliveries"].includes(kind))
    return (
      <Empty
        title="当前视角无权访问"
        text="外部成员仅访问已共享成果、会话与回执。"
      />
    );
  if (recordId && !record) return <Empty title="记录不存在" />;
  if (["testplans", "testreports"].includes(kind)) return <QAResourcePage kind={kind} recordId={recordId} toast={toast} />;
  if (kind === "templates") return <WorkflowTemplates record={record} toast={toast} />;
  const filtered = rows.filter((r) =>
    `${r.name} ${r.owner} ${r.status}`
      .toLowerCase()
      .includes(query.toLowerCase()),
  );
  const defaults = rows[0] || {
    name: "",
    owner: "林予",
    status: "草稿",
    scope: "当前项目",
    content: "",
  };
  const create = () => kind === "reports" ? setGeneration({ mode: "report" }) :
    setEditing(
      {
        ...Object.fromEntries(
          resourceFields[kind].map((k) => [k, k === "name" ? "" : defaults[k] ?? ""]),
        ),
        status: "草稿",
      },
    );
  return (
    <>
      <PageHead
        eyebrow={`${record ? meta[0] + " / " + record.id : "WORKSPACE / " + kind.toUpperCase()}`}
        title={record ? record.name : meta[0]}
        subtitle={meta[1]}
      >
        {record ? (
          <>
            <Link className="btn" to={`/${kind}`}>
              返回列表
            </Link>
            {kind !== "deliveries" && <Button icon="Pencil" disabled={kind === "goals" ? Boolean(goalWriteProblem(s, record, true)) : kind === "reports" ? Boolean(reportEditProblem(s, record, { simulateAgent: true })) : false} title={kind === "goals" ? goalWriteProblem(s, record, true) || "" : kind === "reports" ? reportEditProblem(s, record, { simulateAgent: true }) || "" : undefined} onClick={() => setEditing({ ...record })}>
              编辑
            </Button>}
          </>
        ) : (
          kind !== "deliveries" && <Button variant="primary" icon="Plus" onClick={create}>
            {kind === "reports" ? "生成 / 打开周期报告" : `新建${meta[2]}`}
          </Button>
        )}
        {kind === "reports" && <Button icon="CalendarClock" onClick={() => setGeneration({ mode: "schedule" })}>定时生成设置</Button>}
        <Button
          icon="Download"
          onClick={() => exportData(kind, record || filtered)}
        >
          导出
        </Button>
      </PageHead>
      {record ? (
        <div className="detail-grid">
          <div>
            <Section title="基本信息" extra={<Badge>{record.status}</Badge>}>
              <dl className="record-fields">
                {Object.entries(record)
                  .filter(
                    ([k]) =>
                      !["id", "status", "name", "history", "revision", "items", "requirements", "runs", "currentRun"].includes(
                        k,
                       ) && !(kind === "goals" && ["metricRevision", "progressCalibration", "progress", "submission", "approval", "ownerId", "ownerKind"].includes(k)) && !(kind === "reports" && ["references", "factSnapshot", "subjectKey", "cadence", "timezone", "periodStart", "periodEnd", "cycle", "generatedSummary", "generatedFacts", "suggestedReferences", "generatedAt", "generatedBy", "generationHistory", "summary", "risk", "source", "submission", "approval", "authorId", "authorKind"].includes(k)),
                  )
                  .map(([k, v]) => (
                    <React.Fragment key={k}>
                      <dt>{kind === "reports" && k === "source" ? "旧来源说明（非事实引用）" : fieldNames[k] || k}</dt>
                      <dd>
                        {typeof v === "object" ? JSON.stringify(v) : String(v)}
                      </dd>
                    </React.Fragment>
                  ))}
              </dl>
            </Section>
            {kind === "deliveries" && <Section title="固定交付清单与逐项回执">
              <Notice>此包保存原型成果和检查证据引用，不代表真实源文件已上传或对外发布。版本与清单不随返工改写。</Notice>
              {(record.items || []).map((item) => <div className="resource-card" key={item.id}>
                <div><h3>{item.name}</h3><p>{item.kind} · {item.version}</p>
                  {item.kind === "成果引用" ? <Link to={item.reference}>查看固定成果</Link> : <code>{item.reference}</code>}
                  {item.feedback && <dl>
                    <dt>未满足条款</dt><dd>{item.feedback.requirement || "未补充"}</dd>
                    <dt>位置</dt><dd>{item.feedback.position || "未补充"}</dd>
                    <dt>期望</dt><dd>{item.feedback.expected || "未补充"}</dd>
                    <dt>实际</dt><dd>{item.feedback.actual}</dd>
                  </dl>}
                </div><Badge>{item.status}</Badge>
              </div>)}
              {!record.items?.length && <Notice tone="warn">此旧包未保存逐项清单，不能补造历史回执。</Notice>}
            </Section>}
            {kind === "goals" && (
              <Section title="贡献工作与指标证据">
                {goalProgress(record) === null ? <Notice tone="warn">进度待校准。指标变化后不沿用旧百分比，也不按自由文本自动推算。</Notice> : <><Progress value={goalProgress(record)} /><p>手工进度 {goalProgress(record)}% · {record.progressCalibration ? `对应指标修订 ${record.metricRevision || 1}` : "历史记录，未记录校准依据"}</p></>}
                {record.progressCalibration && goalProgress(record) !== null && <p>{record.progressCalibration.reason} · {record.progressCalibration.actor} · {formatTimestamp(record.progressCalibration.time)}</p>}
                <Button disabled={Boolean(goalWriteProblem(s, record))} title={goalWriteProblem(s, record) || ""} onClick={() => setGoalCalibration({ id: record.id, revision: record.revision || 1, metricRevision: record.metricRevision || 1, progress: goalProgress(record) ?? "", reason: "" })}>手工校准进度</Button>
                <p>
                  {record.metric}：基线 {record.baseline} → 当前 {record.actual}{" "}
                  → 目标 {record.target}
                </p>
                <Link className="btn" to="/work">
                  查看关联计划与工作
                </Link>
              </Section>
            )}
            {kind === "goals" && <Section title="固定修订与审核">
              <p>当前修订 {record.revision || 1} · {record.status}</p>
              {record.submission && <p>提交修订 {record.submission.revision} · {record.submission.actor} · {record.submission.reason}</p>}
              {record.approval && <><p>已批准修订 {record.approval.revision} · {record.approval.actor} · 租户负责人权限</p><p>{record.approval.reason}</p></>}
              {record.status === "待审核" && !record.submission && <Notice tone="warn">历史记录缺少固定提交内容，请退回修改后重新提交，不能补造旧审批。</Notice>}
              {record.history?.length > 0 && <details><summary>修订与决定历史 · {record.history.length} 条</summary>{[...record.history].reverse().map((entry, index) => <div key={index}><p>修订 {entry.revision || 1} · {entry.status} · {entry.actor || "内容变更"}</p><p>{entry.reason || "保存目标内容"}</p>{entry.snapshot && <p>{entry.snapshot.name} · {entry.snapshot.metric} · 目标 {entry.snapshot.target}</p>}</div>)}</details>}
            </Section>}
            {kind === "tests" && <Section title="固定运行记录">
              <Notice>新运行使用本地模拟证据，不代表真实执行。旧记录仅保留已有事实，缺失的执行人和时间不补造。</Notice>
              {!record.runs?.length && <p className="muted">当前为原始记录；首次修改或重验时保留快照。</p>}
              {[...(record.runs || [])].reverse().map((run) => <details key={run.id} className="section">
                <summary>{run.version} · {run.status} · {run.source === "mock" ? "本地模拟运行" : "已有结果快照"} · {run.id}</summary>
                <dl className="record-fields">
                  <dt>用例修订 / 运行主体</dt><dd>{run.caseRevision} / {run.owner || "未记录"}</dd>
                  <dt>来源 / 方法</dt><dd>{run.sourceKind || "未声明"} / {run.method || "未记录"}</dd>
                  <dt>输入</dt><dd>{run.inputs || "未记录"}</dd>
                  <dt>执行环境 / 工具版本</dt><dd>{run.environment || "未记录"} / {run.toolVersion || "未记录"}</dd>
                  <dt>日志 / 批次</dt><dd>{run.logs || "未记录"} / {run.batchId || "独立运行，未纳入验收批次"}</dd>
                  <dt>固定工作要求</dt><dd>{run.workRequirements?.join("；") || "未记录"}</dd>
                  <dt>期望</dt><dd>{run.expected || "未记录"}</dd>
                  <dt>实际</dt><dd>{run.actual || "未记录"}</dd>
                  <dt>证据</dt><dd>{run.evidence || "未记录"}</dd>
                  <dt>记录人 / 时间</dt><dd>{run.actor || "未记录"} / {formatTimestamp(run.time)}</dd>
                  <dt>关联缺陷 / 修复轮次</dt><dd>{run.defect || "无"} / {run.defectRepairRevision || "未绑定"}</dd>
                </dl>
              </details>)}
            </Section>}
            {kind === "reports" && <>
              <ReportNarrative report={record} state={s} />
              <ReportFacts report={record} state={s} />
            </>}
            {kind === "connectors" && (
              <Section title="权限与同步边界">
                <Notice tone="warn">
                  绑定仅覆盖「{record.scope}
                  」。不导出内部私信或全部历史；外部身份不自动取得审批权限。
                </Notice>
                <dl>
                  <dt>消息持久化</dt>
                  <dd>Go collab + 数据库，保留来源消息 ID 与方向</dd>
                  <dt>实时通道</dt>
                  <dd>Centrifugo；无需为外部连接器引入 Matrix / NATS</dd>
                  <dt>收发回执</dt>
                  <dd>区分已存、平台接收、送达与已读；按实际能力声明</dd>
                </dl>
              </Section>
            )}
            {kind === "models" && (
              <Section title="下发与能力">
                <Notice>
                  只应用于后续执行，不覆盖个人全局配置。密钥使用引用，不录入原型。
                </Notice>
                <div className="node-status-list">
                  {s.nodes.map((n) => (
                    <div key={n.id}>
                      <Icon name="Server" />
                      <span>{n.name}</span>
                      <Badge>
                        {record.nodeResults?.find((r) => r.node === n.id)
                          ?.status || "未下发"}
                      </Badge>
                    </div>
                  ))}
                </div>
              </Section>
            )}
            <Section title="相关活动">
              <div className="timeline">
                {s.events
                  .filter(
                    (e) =>
                      e.target.includes(record.id) ||
                      e.text.includes(record.name),
                  )
                  .slice(0, 8)
                  .map((e) => (
                    <div key={e.id}>
                      <span className="timeline-dot" />
                      <small>
                        {formatTimestamp(e.time)} · {e.actor}
                      </small>
                      <p>{e.text}</p>
                    </div>
                  ))}
                {!s.events.some((e) => e.text.includes(record.name)) && (
                  <p className="muted">
                    还没有后续变更，下一次操作会保留在这里。
                  </p>
                )}
              </div>
            </Section>
          </div>
          <aside>
            <Section title="下一步动作">
              <p className="muted">
                目标 {record.id} · {record.version || "当前版本"}
              </p>
              <div className="stack">
                {(transitions[kind] || []).map(([status, baseLabel]) => { const simulateAgent = kind === "reports" && status === "已提交" && reportAuthor(s, record)?.kind === "Agent"; const label = simulateAgent ? "模拟 Agent 上报" : kind === "knowledge" && status === "已禁用" && record.kind !== "记忆" ? "禁用资料" : baseLabel; return (
                  <Button
                    key={label}
                    disabled={kind === "reports" ? Boolean(reportTransitionProblem(s, record, status, { simulateAgent })) : kind === "goals" ? Boolean(goalTransitionProblem(s, record, status)) : false}
                    title={kind === "reports" ? reportTransitionProblem(s, record, status, { simulateAgent }) || "" : kind === "goals" ? goalTransitionProblem(s, record, status) || "" : undefined}
                    onClick={() => {
                      setVerificationRunId("");
                      setAction({ record, status, label, simulateAgent });
                      setReason("");
                      if (kind === "deliveries") setReceiptItems((record.items || []).map((item) => ({
                        id: item.id, status: item.status === "已接受" || status !== "已拒收" ? "已接受" : "已拒收",
                        feedback: { requirement: record.requirements?.[0] || "", expected: "", actual: "", position: "" },
                      })));
                    }}
                    icon={label.includes("撤销") ? "ShieldOff" : "ArrowRight"}
                  >
                    {label}
                  </Button>
                ); })}
              </div>
              <Notice>
                操作会保留主体、理由与历史。原型中的外部同步和运行结果均为模拟。
              </Notice>
            </Section>
          </aside>
        </div>
      ) : (
        <>
          <div className="toolbar">
            <label className="search-field">
              <Icon name="Search" />
              <input
                aria-label={`搜索${meta[0]}`} name={`search-${kind}`} autoComplete="off"
                placeholder={`搜索${meta[2]}、负责人或状态…`}
                value={query}
                onChange={(e) => setQuery(e.target.value)}
              />
            </label>
            <div className="grow" />
            <span className="muted">{filtered.length} 条记录</span>
          </div>
          {!filtered.length ? (
            <Empty
              title={query ? "没有匹配的记录" : `还没有${meta[2]}`}
              text={
                query
                  ? "试试其他关键词，或清空当前筛选。"
                  : "创建第一条记录，开始形成可追溯的工作。"
              }
            >
              {kind !== "deliveries" && <Button onClick={query ? () => setQuery("") : create}>
                {query ? "清空筛选" : kind === "reports" ? "生成 / 打开周期报告" : `创建${meta[2]}`}
              </Button>}
            </Empty>
          ) : (
            <div
              className={`resource-grid ${["knowledge", "templates", "tools", "connectors", "models"].includes(kind) ? "cards" : "rows"}`}
            >
              {filtered.map((r) => (
                <Link
                  to={`/${kind}/${r.id}`}
                  className="resource-card"
                  key={r.id}
                >
                  <span className="resource-icon">
                    <Icon name={meta[3]} size={24} />
                  </span>
                  <div className="grow">
                    <div className="split">
                      <h2>{r.name}</h2>
                      <Badge>{r.status}</Badge>
                    </div>
                    <p>
                      {r.summary ||
                        r.content ||
                        r.rule ||
                        r.impact ||
                        r.scope ||
                        r.source ||
                        r.expected ||
                        r.metric ||
                        r.reason ||
                        r.note ||
                        r.event ||
                        meta[1]}
                    </p>
                    <footer>
                      <Avatar
                        name={r.owner}
                        agent={["Atlas", "Muse", "Quill", "Verifier"].includes(
                          r.owner,
                        )}
                      />
                      <span>{r.owner}</span>
                      <small>
                        {r.version || r.cycle || r.platform || r.next || r.id}
                      </small>
                    </footer>
                    {kind === "goals" && (goalProgress(r) === null ? <Badge tone="amber">进度待校准</Badge> : <><Progress value={goalProgress(r)} /><small>手工进度 {goalProgress(r)}%</small></>)}
                  </div>
                  <Icon name="ArrowUpRight" size={16} />
                </Link>
              ))}
            </div>
          )}
        </>
      )}
      {kind === "reports" && !record && <ReportSchedules state={s} toast={toast} onEdit={(schedule) => setGeneration({ mode: "schedule", schedule })} />}
      {generation && <ReportGeneration state={s} mode={generation.mode} schedule={generation.schedule} onClose={() => setGeneration(null)} onDone={(reportId) => {
        setGeneration(null);
        if (reportId) location.hash = `/reports/${reportId}`;
        else toast("已保存本地定时配置；没有后台真实定时器");
      }} />}
      {goalCalibration && <GoalCalibration key={`${s.activeTenant}:${goalCalibration.id}`} tenant={s.activeTenant} initial={goalCalibration} onClose={() => setGoalCalibration(null)} toast={toast} />}
      {editing && (
        <ResourceEditor
          key={`${s.activeTenant}:${kind}:${editing.id || "new"}`}
          kind={kind}
          record={editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null);
            toast("记录已保存");
          }}
        />
      )}
      {action && (
        <Modal title={action.label} draftValue={{ reason, receiptItems, verificationRunId }} onClose={() => setAction(null)}>
          {({ requestClose }) =>
          <ActionForm
            label="确认操作"
            onCancel={() => setAction(null)}
            onSubmit={({ dismiss }) => {
              if (!reason.trim()) throw Error("请填写理由。");
              if (
                kind === "policies" &&
                action.record.id === "POL-01" &&
                action.status !== "启用"
              )
                throw Error("必需内部验收属于不可绕过的硬门禁。");
              resourceCommand(
                kind,
                action.record.id,
                action.status,
                reason,
                action.label,
                kind === "deliveries" ? { version: action.record.version, items: receiptItems }
                  : kind === "defects" ? { runId: verificationRunId }
                    : ["reports", "goals"].includes(kind) ? { revision: action.record.revision || 1, status: action.record.status, simulateAgent: Boolean(action.simulateAgent) } : undefined,
              );
              dismiss();
              toast("操作已记录");
            }}
          >
            <Notice tone="warn">
              {action.record.name} → {action.status}。
              {kind === "approvals" ? "确认当前固定版本及授权范围。" : ""}
            </Notice>
            {kind === "reports" && action.simulateAgent && <Notice>本次由负责人类触发本地 Agent 上报模拟，保存模拟主体和实际操作者，不代表真实 Agent 已发送报告；最终确认仍需 Human。</Notice>}
            {kind === "tests" && <Notice>本次记录为模拟执行，依据与说明作为实际结果保存，并生成独立的模拟证据引用；旧结果不覆盖。验证来源与主体沿用用例中明确配置。工作放行须进入固定测试计划逐项验证，此处单独运行不会替代验收批次。</Notice>}
            {kind === "defects" && (action.status === "已关闭" || action.label === "验证失败并重开") && <>
              <Field label="独立验证运行" value={verificationRunId} onChange={setVerificationRunId}
                options={[{ value: "", label: "请选择验证运行" }, ...defectVerificationRuns(s, defectRecords(s).find((item) => item.id === action.record.id) || action.record)
                  .filter((run) => run.status === (action.status === "已关闭" ? "通过" : "失败"))
                  .map((run) => ({ value: run.id, label: `${run.caseId} · ${run.version} · ${run.status} · ${run.owner} · ${run.id}` }))]} />
              <Notice>先提交修复，再到测试用例记录当前版本、关联此缺陷的独立验证。自测、修复前运行、旧版本和已失效结果不能用于关闭。</Notice>
              <Link to="/tests" className="text-link" onClick={event => { event.preventDefault(); requestClose(() => { setAction(null); location.hash = "/tests"; }); }}>查看测试用例与运行</Link>
            </>}
            {kind === "reports" && <ReportFacts report={s.resources.reports.find((r) => r.id === action.record.id) || action.record} state={s} />}
            {kind === "deliveries" && receiptItems.map((choice, index) => {
              const item = action.record.items.find((entry) => entry.id === choice.id);
              const change = (patch) => setReceiptItems((items) => items.map((entry, i) => i === index ? { ...entry, ...patch } : entry));
              return <Section key={choice.id} title={`${item.name} · ${item.version}`}>
                <Field label={`第 ${index + 1} 项回执`} value={choice.status}
                  options={["已接受", "已拒收"]} disabled={item.status === "已接受"}
                  onChange={(status) => change({ status })} />
                {choice.status === "已拒收" && <>
                  <Field label={`第 ${index + 1} 项未满足条款`} value={choice.feedback.requirement}
                    options={action.record.requirements || []} onChange={(requirement) => change({ feedback: { ...choice.feedback, requirement } })} />
                  {[['position', '位置'], ['expected', '期望'], ['actual', '实际']].map(([key, label]) => <Field
                    key={key} label={`第 ${index + 1} 项${label}`} value={choice.feedback[key]} required
                    onChange={(value) => change({ feedback: { ...choice.feedback, [key]: value } })} />)}
                </>}
              </Section>;
            })}
            <Field
              label="依据与说明"
              value={reason}
              onChange={setReason}
              type="textarea"
              required
            />
          </ActionForm>}
        </Modal>
      )}
    </>
  );
}
function ReportGeneration({ state, mode, schedule, onClose, onDone }) {
  const subjects = reportSubjects(state);
  const [form, setForm] = useState(() => ({ subjectKey: subjects[0]?.key || "", cadence: "周报", timezone: "Asia/Singapore",
    date: dateInZone("Asia/Singapore"), cron: "0 18 * * 5", missed: "合并补跑一次", ...schedule }));
  const change = (key, value) => setForm((current) => ({ ...current, [key]: value }));
  let period;
  try { period = reportPeriod(form.cadence, form.date); } catch { period = null; }
  return <Modal title={mode === "schedule" ? "周期报告定时设置" : "生成 / 打开周期报告"} draftValue={form} onClose={onClose}>
    <ActionForm label={mode === "schedule" ? "保存本地配置" : "生成 / 打开"} onCancel={onClose} onSubmit={({ dismiss }) => {
      if (mode === "schedule") { saveReportSchedule(form); dismiss(onDone); }
      else { const reportId = generatePeriodicReport(form); dismiss(() => onDone(reportId)); }
    }}>
      <Field label="报告归属" value={form.subjectKey} options={subjects.map((subject) => ({ value: subject.key, label: `${subject.name} · ${subject.kind}` }))} onChange={(value) => change("subjectKey", value)} required />
      <Field label="报告周期" value={form.cadence} options={reportCadences} onChange={(value) => change("cadence", value)} required />
      <Field label="报告时区" value={form.timezone} onChange={(value) => change("timezone", value)} required help="IANA 时区，例如 Asia/Singapore。周期按此时区的日历划分。" />
      {mode === "report" ? <><Field label="周期内日期" type="date" value={form.date} onChange={(value) => change("date", value)} required />
        {period && <p className="muted">对应周期：{period.periodStart} — {period.periodEnd}</p>}
        <Notice>相同归属与日历周期只保留一份报告；已生成报告保留原时区与边界。已有草稿仅刷新建议，人工正文与所选事实保留；已提交报告直接打开。</Notice></> : <>
        <Field label="Cron 表达式" value={form.cron} onChange={(value) => change("cron", value)} required />
        <Field label="错过策略" value={form.missed} options={["合并补跑一次", "跳过"]} onChange={(value) => change("missed", value)} />
        <Notice>仅保存本地模拟配置，没有后台真实定时器。模拟运行按所选时区的当前日期定位周期，只生成或更新草稿，不自动上报。</Notice>
      </>}
    </ActionForm>
  </Modal>;
}
function ReportSchedules({ state, toast, onEdit }) {
  const schedules = state.resources.schedules.filter((item) => item.action?.includes("报告") || item.action?.includes("日报"));
  return <Section title="周期报告定时配置">
    <p className="muted">本地模拟，不会在浏览器关闭后运行，也不会自动代替 Human 上报。</p>
    {schedules.map((schedule) => <div className="resource-card" key={schedule.id}><div className="grow"><h3>{schedule.name}</h3><p>{schedule.cadence || "周期未配置"} · {schedule.cron} · {schedule.timezone}</p><Badge>{schedule.status}</Badge>
      {schedule.lastReportId && <Link to={`/reports/${schedule.lastReportId}`}>打开最近报告</Link>}</div>
      <Button onClick={() => onEdit(schedule)}>编辑定时配置 {schedule.id}</Button>
      <Button disabled={state.offline || state.role !== "owner"} onClick={() => { try { resourceCommand("schedules", schedule.id, schedule.status === "启用" ? "暂停" : "启用", "本人调整本地模拟定时配置"); toast("配置状态已保存；没有后台真实定时器"); } catch (error) { toast(error.message, true); } }}>{schedule.status === "启用" ? "暂停" : "启用"}</Button>
      <Button disabled={state.offline} onClick={() => { try { resourceCommand("schedules", schedule.id, schedule.status, "本人触发周期报告本地模拟", "模拟运行一次"); toast("模拟运行完成，已生成或保留对应周期报告"); } catch (error) { toast(error.message, true); } }}>模拟运行一次 {schedule.id}</Button>
    </div>)}
  </Section>;
}
function ReportNarrative({ report, state }) {
  const subject = reportSubjects(state).find((item) => item.key === report.subjectKey);
  const author = reportAuthor(state, report), authority = reportReviewAuthority(state, report);
  return <>
    <Section title="周期与归属"><p>{subject ? `${subject.name} · ${subject.kind}` : "旧记录未声明有效归属"} · {report.cadence || "周期类型未声明"}</p>
      <p className="muted">{report.periodStart} — {report.periodEnd} · {report.timezone || "时区未声明"} · 修订 {report.revision || 1}</p></Section>
    <Section title="提交与确认责任">
      <p>作者：{author ? `${author.name} · ${author.kind}` : "身份未确认"}</p>
      <p>确认人：{authority.system ? "系统确认 · 团队所有者本人提交" : state.organization.members.find(person => person.id === authority.id)?.name || "待配置"}</p>
      <Notice tone={authority.issue ? "warn" : undefined}>{authority.issue || authority.basis}</Notice>
      {report.submission && <details open><summary>固定提交 · 修订 {report.submission.revision}</summary>
        <p>{report.submission.source} · {formatTimestamp(report.submission.time)}</p>
        <p>{report.submission.content.summary || "未填写正文"}</p><p>风险：{report.submission.content.risk || "未填写"}</p>
        <ul>{(report.submission.content.references || []).map((ref, index) => <li key={index}><span translate="no">{ref.kind}:{ref.id}</span> · 固定版本 {ref.version}</li>)}</ul>
      </details>}
      {report.approval && <p>已确认：{report.approval.actorId === "SYSTEM:team_top" ? "系统 · 团队顶层" : state.organization.members.find(person => person.id === report.approval.actorId)?.name || report.approval.actorId} · {formatTimestamp(report.approval.time)} · {report.approval.reason}</p>}
    </Section>
    <Section title="生成建议"><p>{report.generatedSummary || "此记录尚无生成建议；既有正文保留为人工内容。"}</p>
      {report.generatedAt && <p className="muted">{formatTimestamp(report.generatedAt)} · {report.generatedBy} · 建议引用 {report.suggestedReferences?.length || 0} 条</p>}
      {report.generationHistory?.length > 0 && <details><summary>建议更新历史 · {report.generationHistory.length} 次</summary>{[...report.generationHistory].reverse().map((item, index) => <p key={index}>{item.time ? formatTimestamp(item.time) : "首次生成前"} · {item.generatedSummary || "尚无建议"}</p>)}</details>}
    </Section>
    <Section title="人工校准"><p>{report.summary || "尚未填写人工正文。请在编辑中核对进展和事实。"}</p><p className="muted">风险与阻塞：{report.risk || "未填写"}</p>
      {report.source && <details><summary>原有来源说明（不作为事实引用）</summary><p>{report.source}</p></details>}
      {report.history?.length > 0 && <details><summary>报告修订与审核历史 · {report.history.length} 次</summary>{[...report.history].reverse().map((item, index) => <article key={index}><p>{item.status} · {item.time ? formatTimestamp(item.time) : `修订 ${item.revision || 1}`}</p>{item.summary && <p>{item.summary}</p>}{item.reason && <p>{item.reason}</p>}</article>)}</details>}
    </Section>
  </>;
}

function ReportFacts({ report, state }) {
  const check = reportEvidence(state, report);
  return <Section title="事实核对与来源" className="report-facts">
    <p className="muted">同一工作或耗时记录只计一次；作者判断保留在正文，不由系统猜测。Human 报告仍由本人确认。</p>
    <dl className="record-fields">
      <dt>引用工作 / 已证实周期完成</dt><dd>{check.totals.works} / {check.totals.completed}</dd>
      <dt>Human 工时</dt><dd data-report-total="human">{check.totals.human} 小时</dd>
      <dt>Agent 墙钟</dt><dd data-report-total="agent">{check.totals.agent} 小时</dd>
      <dt>模型 / 工具耗时</dt><dd data-report-total="tool">{check.totals.tool} 小时</dd>
    </dl>
    {check.errors.length > 0 ? <div role="alert"><Notice tone="warn">
      <strong>存在阻断项，修正后才能上报</strong>
      <ul>{check.errors.map((text) => <li key={text}>{text}</li>)}</ul>
    </Notice></div> : <Notice>当前可计算的来源、版本、日期和完成数核对通过。</Notice>}
    {check.warnings.length > 0 && <Notice tone="warn">
      <strong>以下待确认事实随上报保留</strong>
      <ul>{check.warnings.map((text) => <li key={text}>{text}</li>)}</ul>
    </Notice>}
    <div className="stack">
      {check.sources.map((source) => <div key={`${source.kind}:${source.id}`}>
        <Link to={source.href}>{source.name}</Link> <Badge>{source.status}</Badge>
        <p className="muted">{source.id} · 引用 v{source.selectedVersion} / 当前 v{source.currentVersion ?? source.version}
          {source.kind === "work" ? ` · 计划${source.plan ? "已确认" : "未确认"} · 内部检查${source.quality} · ${source.accepted ? "已接受" : "未接受"}` : ""}
          {source.kind === "worklog" ? ` · ${source.durationKind} ${source.hours} 小时` : ""}</p>
        {source.acceptanceFacts?.length > 0 && <details><summary>固定接受裁决 · {source.acceptanceFacts.length} 次</summary>
          {source.acceptanceFacts.map(fact => <div key={fact.id}><p><span translate="no">{fact.id}</span> · v{fact.version} · {formatTimestamp(fact.time)}</p>
            <p>{fact.workSnapshot?.title || "历史成果内容不可用，保留裁决事实"}</p><p>执行主体：{fact.executorIds?.join("、") || "未记录"}</p>
          </div>)}
        </details>}
      </div>)}
    </div>
    {report.factSnapshot && <details>
      <summary>上次提交 / 审核时的核对快照</summary>
      <p>{formatTimestamp(report.factSnapshot.recordedAt)} · Human {report.factSnapshot.totals.human} 小时 · Agent {report.factSnapshot.totals.agent} 小时 · 工具 {report.factSnapshot.totals.tool} 小时</p>
      <ul>{report.factSnapshot.sources.map((source) => <li key={`${source.kind}:${source.id}`}>{source.name} · v{source.version} · {source.status}</li>)}</ul>
      {report.factSnapshot.warnings.map((text) => <p key={text}>{text}</p>)}
    </details>}
  </Section>;
}

function GoalCalibration({ tenant, initial, onClose, toast }) {
  const [form, setForm, draft] = useRecoverableDraft(JSON.stringify([tenant, "goal-calibration", initial.id]), () => initial,
    value => ({ progress: String(value.progress), reason: value.reason }));
  return <Modal title="手工校准目标进度" {...draft.modalProps} onClose={onClose}>
    <ActionForm label="保存校准" onCancel={onClose} onSubmit={({ dismiss }) => { calibrateGoalProgress(form.id, form); dismiss(); toast("手工进度已保存；待审核目标需重新提交，工作验收不受影响"); }}>
      {draft.recovered && <Notice>已恢复未保存的修改，并保留原始修订。若记录已变化，请放弃修改后重新打开。</Notice>}
      <Notice>对应指标修订 {form.metricRevision}。填写本次判断依据；指标变化后需重新校准。</Notice>
      <Field label="手工进度（%）" type="number" min="0" max="100" step="any" required value={form.progress} onChange={progress => setForm(current => ({ ...current, progress }))} />
      <Field label="校准依据" type="textarea" required value={form.reason} onChange={reason => setForm(current => ({ ...current, reason }))} />
    </ActionForm>
  </Modal>;
}
function ResourceEditor({ kind, record, onClose, onSaved }) {
  const state = useStore();
  const [form, setForm, draft] = useRecoverableDraft(["goals", "reports"].includes(kind) ? JSON.stringify([state.activeTenant, "resource", kind, record.id || "new"]) : null, () => ({ ...record,
    ...(kind === "worklogs" ? { durationKind: record.durationKind || "Human 工时" } : {}),
  }), value => ({
    fields: Object.fromEntries(resourceFields[kind].filter(field => !(kind === "reports" && field === "owner")).map(key => [key, String(value[key] ?? "")])),
    ...(kind === "goals" ? { ownerId: value.ownerId, reviewerId: value.reviewerId } : {}),
    references: [...(Array.isArray(value.references) ? value.references : [])].sort((a, b) => `${a.kind}:${a.id}`.localeCompare(`${b.kind}:${b.id}`)),
    requirementRefs: [...(value.requirementRefs || [])].sort(),
  }));
  const fields = resourceFields[kind].filter((field) => !(kind === "reports" && field === "owner"));
  const references = Array.isArray(form.references) ? form.references : [];
  const options = kind === "reports" ? [
    ...state.work.map((w) => ({ kind: "work", id: w.id, name: w.title, version: w.version })),
    ...state.resources.worklogs.map((w) => ({ kind: "worklog", id: w.id, name: w.name, version: w.revision || 1 })),
    ...state.resources.reports.filter((r) => r.id !== form.id).map((r) => ({ kind: "report", id: r.id, name: r.name, version: r.revision || 1 })),
  ] : [];
  const chooseSource = (option, checked) => setForm((current) => ({ ...current,
    references: [...(Array.isArray(current.references) ? current.references : []).filter((r) => !(r?.kind === option.kind && r?.id === option.id)),
      ...(checked ? [{ kind: option.kind, id: option.id, version: option.version }] : [])],
  }));
  return (
    <Modal
      title={`${record.id ? "编辑" : "新建"}${catalog[kind][2]}`}
      onClose={onClose}
      {...draft.modalProps}
      wide
    >
      <ActionForm
        onCancel={onClose}
        onSubmit={({ dismiss }) => {
          if (!form.name?.trim()) throw Error("名称不能为空。");
          if (
            kind === "worklogs" &&
            (!Number.isFinite(Number(form.hours)) ||
              Number(form.hours) <= 0 ||
              Number(form.hours) > 24)
          )
            throw Error("工时须大于 0 且不超过 24 小时。");
          saveResource(kind, form);
          dismiss(onSaved);
        }}
      >
        {draft.recovered && <Notice>已恢复未保存的修改，并保留原始修订。若记录已变化，请放弃修改后重新打开。</Notice>}
        {kind === "reports" && <>
          <p className="muted">{form.owner} · {form.cadence || "旧版周期未声明"} · {form.periodStart} — {form.periodEnd} · {form.timezone || "时区未声明"}</p>
          {form.generatedSummary && <Section title="生成建议（不会覆盖人工正文）"><p>{form.generatedSummary}</p>
            <Button onClick={() => setForm((current) => ({ ...current, references: structuredClone(current.suggestedReferences || []) }))}>采用本次建议事实引用</Button></Section>}
          {record.status !== "草稿" && <Notice>保存会在原报告编号下建立草稿修订，原提交与事实快照保留在历史中。</Notice>}
        </>}
        {kind === "tests" && <fieldset><legend>关联验收条款</legend>{(state.work.find(work => work.id === form.work)?.requirements || []).map(requirement => <label className="check-label" key={requirement}>
          <input type="checkbox" checked={(form.requirementRefs || []).includes(requirement)} onChange={event => { const refs = form.requirementRefs || []; setForm({ ...form, requirementRefs: event.target.checked ? [...refs, requirement] : refs.filter(ref => ref !== requirement) }); }} />{requirement}
        </label>)}</fieldset>}
        <div className="form-grid">
          {fields.map((k) => (
            <Field
              key={k}
              label={kind === "goals" && k === "target" ? "目标值" : kind === "reports" && k === "summary" ? "人工校准正文" : k === "subjectKey" ? "报告归属" : k === "cadence" ? "报告周期" : fieldNames[k] || k}
              value={String(form[k] ?? "")}
              onChange={(v) => setForm(current => {
                const next = { ...current, [k]: v };
                if (kind === "tests" && ["work", "version"].includes(k)) {
                  if (!defectRecords(state).some(item => item.id === next.defect && item.work === next.work && item.version === next.version)) next.defect = "";
                  if (k === "work") next.requirementRefs = (current.requirementRefs || []).filter(ref => state.work.find(work => work.id === v)?.requirements.includes(ref));
                }
                return next;
              })}
              options={kind === "schedules" && k === "subjectKey" ? [{ value: "", label: "非报告任务 / 未配置" }, ...reportSubjects(state).map((subject) => ({ value: subject.key, label: `${subject.name} · ${subject.kind}` }))]
                : kind === "schedules" && k === "cadence" ? [{ value: "", label: "非报告任务" }, ...reportCadences]
                : k === "durationKind" ? ["Human 工时", "Agent 墙钟", "模型 / 工具耗时"]
                : kind === "tests" && k === "sourceKind" ? ["Human 手测", "Agent 自测", "平台独立验证"]
                : kind === "tests" && k === "defect" ? [{ value: "", label: "无关联缺陷" }, ...defectRecords(state).filter((item) => item.work === form.work && item.version === form.version).map((item) => ({ value: item.id, label: `${item.id} · ${item.name}` }))]
                : undefined}
              type={
                [
                  "content",
                  "summary",
                  "note",
                  "reason",
                  "rule",
                  "expected",
                  "actual",
                ].includes(k)
                  ? "textarea"
                  : k === "email"
                    ? "email"
                    : ["hours", "claimedCompleted"].includes(k)
                      ? "number"
                      : ["periodStart", "periodEnd", "date"].includes(k)
                        ? "date"
                        : "text"
              }
              required={["name", "owner"].includes(k)}
            />
          ))}
        </div>
        {kind === "reports" && <>
          <Section title="选择事实引用">
            <p className="muted">选择当前租户中的工作、耗时与下级报告。来源变化后需重新选择版本；重复来源在汇总中只计一次。</p>
            {[['work', '工作'], ['worklog', '耗时记录'], ['report', '下级报告']].map(([type, title]) => <fieldset key={type}>
              <legend>{title}</legend>
              <div className="stack">{options.filter((o) => o.kind === type).map((option) => {
                const selected = references.find((r) => r?.kind === option.kind && r?.id === option.id);
                return <label className="switch-label" key={option.id}>
                  <input type="checkbox" checked={Boolean(selected)} aria-label={`引用 ${option.id}`}
                    onChange={(e) => chooseSource(option, e.target.checked)} />
                  <span>{option.name} · {option.id} · v{option.version}
                    {selected && selected.version !== option.version ? " · 已选旧版本，请取消后重选" : ""}</span>
                </label>;
              })}</div>
            </fieldset>)}
            {references.filter((ref) => !options.some((option) => option.kind === ref?.kind && option.id === ref?.id)).map((ref, i) =>
              <div key={`${ref?.kind}:${ref?.id}:${i}`}><span>不可用来源：{ref?.id || "未知"}</span> <Button onClick={() => chooseSource(ref || {}, false)}>移除无效引用 {ref?.id || "未知"}</Button></div>)}
          </Section>
          <ReportFacts report={form} state={state} />
        </>}
        <Notice>状态通过专门动作流转，编辑内容不会自动完成审批或验收。</Notice>
      </ActionForm>
    </Modal>
  );
}
export function Team({ toast, initialTab = "members", invitationId }) {
  const s = useStore();
  const org = s.organization, people = org.members.filter((person) => person.kind === "Human"), humans = people;
  const canEdit = s.role === "owner" && !s.offline;
  const [tab, setTab] = useState(initialTab);
  const [nodeId, setNodeId] = useState("");
  const [selection, setSelection] = useState(invitationId ? { kind: "invitations", id: invitationId } : null);
  const [query, setQuery] = useState("");
  const [mobilePane, setMobilePane] = useState(invitationId ? "detail" : "list");
  const [editor, setEditor] = useState(null);
  const personName = (id) => people.find((person) => person.id === id)?.name || "成员不可用";
  const parentName = (id) => org.nodes.find((node) => node.id === id)?.name || "顶层组织";
  const selectedNode = org.nodes.find((node) => node.id === nodeId);
  const rows = [], seen = new Set();
  const walk = (parentId, depth) => org.nodes.filter((node) => node.parentId === parentId).forEach((node) => {
    if (seen.has(node.id)) return; seen.add(node.id); rows.push({ node, depth }); walk(node.id, depth + 1);
  });
  walk("", 0);
  const memberships = (person) => org.nodes.filter((node) => node.memberIds.includes(person.id));
  const memberRows = people.filter((person) => (!nodeId || selectedNode?.memberIds.includes(person.id))
    && person.name.toLowerCase().includes(query.toLowerCase()));
  const listRows = tab === "members" ? memberRows : s.resources.invitations
    .filter((item) => `${item.title || item.name} ${item.email || ""}`.toLowerCase().includes(query.toLowerCase()));
  const selected = selection?.kind === "node" ? org.nodes.find((node) => node.id === selection.id)
    : listRows.find((item) => item.id === selection?.id) || listRows[0];
  const selectedKind = selection?.kind === "node" ? "node" : tab;
  const choose = (kind, item) => { setSelection({ kind, id: item.id }); setEditor(null); setMobilePane("detail"); };
  const switchTab = (next) => { setTab(next); setSelection(null); setEditor(null); setQuery(""); setMobilePane("list"); };
  const edit = (kind, form) => { setEditor({ kind, form: structuredClone(form) }); setMobilePane("detail"); };
  const editNode = (node) => edit("nodes", node || { name: "", parentId: nodeId, ownerId: humans[0]?.id || "", memberIds: [], start: new Date().toISOString().slice(0, 10), end: "" });
  const newInvitation = () => edit("invitation", { name: "", email: "", ownerId: humans[0]?.id || "", projectId: s.projects[0]?.id || "",
    role: "项目成员", scopeKind: "project", expires: new Date(Date.now() + 7 * 86400000).toISOString().slice(0, 10) });
  const snapshot = (record) => !record ? "新建记录" : [record.name, record.owner && `结果负责人 ${record.owner}`,
    record.ownerId && `负责人 ${personName(record.ownerId)}`, "parentId" in record && `归属 ${parentName(record.parentId)}`,
    record.memberIds && `成员 ${record.memberIds.map(personName).join("、") || "暂无"}`,
    record.executorIds && `执行 ${record.executorIds.map(personName).join("、")}`,
    record.checkerIds && `检查 ${record.checkerIds.map(personName).join("、")}`,
    record.coordinatorId && `协调 ${personName(record.coordinatorId)}`, record.receiverId && `接收 ${personName(record.receiverId)}`,
    record.role, record.start && `${record.start} 至 ${record.end || "长期"}`].filter(Boolean).join(" · ");
  const history = (items) => items?.length > 0 && <details className="org-history"><summary>变更历史 · {items.length} 次</summary>
    {[...items].reverse().map((item, i) => <article key={i}><strong>{item.reason || item.action || "记录更新"}</strong>
      <small>{item.actor || "当前负责人"} · {formatTimestamp(item.time)}</small>
      {item.previous !== undefined && <p>之前：{snapshot(item.previous)}</p>}{item.next && <p>之后：{snapshot(item.next)}</p>}
    </article>)}</details>;
  const detailHead = (name, subtitle, agent = false) => <div className="org-detail-identity"><Avatar name={name} agent={agent} /><h2>{name}</h2><p>{subtitle}</p></div>;
  if (invitationId && !s.resources.invitations.some(item => item.id === invitationId)) return <Empty title="邀请不存在于当前租户" text="请返回邀请列表。" />;
  if (s.role === "external") return <Empty title="当前视角无权访问" text="外部成员不通过组织树查看内部成员与工作分工。" />;
  return <div className="org-page">
    <PageHead title="组织与成员" subtitle="管理 Human 的组织归属与项目角色。Agent 在独立名册管理，执行编组在任务中配置。">
      <Button disabled={!canEdit} onClick={() => edit("members", { name: "" })} icon="Plus">添加成员</Button>
      <Button variant="primary" disabled={!canEdit} onClick={newInvitation} icon="UserPlus">邀请成员</Button>
    </PageHead>
    <div className="org-overview"><span><strong>{people.length}</strong> 位成员 <small>Human 组织名册</small></span>
      <span><strong>{org.nodes.length}</strong> 个组织节点</span><span><strong>{s.resources.invitations.filter((item) => item.status === "待接受").length}</strong> 个待接受邀请</span></div>
    <nav className="org-tabs" aria-label="成员管理内容">{[["members", "成员目录"], ["invitations", "邀请记录"]].map(([value, label]) =>
      <button key={value} className={tab === value ? "active" : ""} aria-current={tab === value ? "page" : undefined} onClick={() => switchTab(value)}>{label}</button>)}</nav>
    <nav className="org-mobile-panes" aria-label="组织页面分栏">{[["tree", "组织"], ["list", "列表"], ["detail", "详情"]].map(([value, label]) =>
      <button key={value} aria-pressed={mobilePane === value} onClick={() => setMobilePane(value)}>{label}</button>)}</nav>
    <div className={`org-workspace org-show-${mobilePane}`}>
      <aside className="org-tree" aria-label="组织架构"><div className="org-pane-heading"><h2>组织架构</h2><IconButton name="Plus" label="新增部门 / 团队" disabled={!canEdit || !humans.length} onClick={() => editNode()} /></div>
        <button className={`org-tree-row ${!nodeId ? "active" : ""}`} onClick={() => { setNodeId(""); setSelection(null); setEditor(null); setMobilePane("list"); }}><Icon name="UsersRound" size={16} /><span>全部成员</span><small>{people.length}</small></button>
        <div className="org-tree-branches">{rows.map(({ node, depth }) => <button key={node.id} className={`org-tree-row ${nodeId === node.id ? "active" : ""}`}
          style={{ "--org-depth": Math.min(depth, 4) }} onClick={() => { setNodeId(node.id); choose("node", node); setMobilePane("list"); }}>
          <Icon name={depth ? "Folders" : "Building2"} size={16} /><span>{node.name}</span><small title="直属 Human 成员">直属 {node.memberIds.filter(id => people.some(p => p.id === id)).length}</small>
        </button>)}</div>
        {!rows.length && <p className="org-empty-note">先添加 Human 成员，再建立组织。</p>}
        <p className="org-tree-foot"><Icon name="LockKeyhole" size={14} />组织层级不开放私人会话。</p>
      </aside>
      <section className="org-directory" aria-label={tab === "members" ? "成员目录" : "邀请记录"}>
        <div className="org-pane-heading"><div><h2>{tab === "members" ? selectedNode?.name || "全部成员" : "邀请记录"}</h2><small>{listRows.length} 条记录</small></div>
          {tab === "members" && selectedNode && <IconButton name="Settings2" label={`编辑 / 移动 ${selectedNode.name}`} disabled={!canEdit} onClick={() => editNode(selectedNode)} />}</div>
        <div className="org-search"><Icon name="Search" size={16} /><input aria-label="搜索成员或记录" placeholder={tab === "members" ? "搜索成员姓名…" : "搜索名称…"} value={query} onChange={(e) => setQuery(e.target.value)} /></div>

        <div className="org-directory-list">{listRows.map((item) => <button key={item.id} className={`org-person-row ${selected?.id === item.id && selectedKind !== "node" ? "active" : ""}`} onClick={() => choose(tab, item)}>
          <Avatar name={item.name || item.title} agent={item.kind === "Agent"} /><span className="org-row-text"><strong>{item.name || item.title}</strong>
            <small>{tab === "members" ? memberships(item).map((node) => node.name).join("、") || "未登记正式归属" : item.email}</small></span>
          <span className="org-row-tag">{tab === "members" ? item.kind : item.status}</span><Icon name="ChevronRight" size={14} />
        </button>)}</div>
        {!listRows.length && <Empty title={query ? "没有匹配记录" : tab === "members" ? "还没有成员" : "暂无邀请"} text={query ? "换个姓名或名称试试。" : "当前租户的记录会显示在这里。"} />}
      </section>
      <aside className="org-detail" aria-label="成员与组织详情">
        {!editor && <div className="org-pane-heading"><h2>详情</h2></div>}
        {editor ? <OrganizationEditor key={JSON.stringify([s.activeTenant, editor.kind, editor.form.id || editor.form.workId || editor.form.memberId || editor.form.parentId || "new"])}
          state={s} initial={editor} onClose={() => setEditor(null)} onSaved={kind => {
            if (kind === "invitation") { setTab("invitations"); setSelection(null); setQuery(""); }
            setEditor(null); toast(kind === "invitation" ? "本地邀请已创建，未发送真实邮件" : "记录已保存");
          }} /> : selected ? <div className="org-detail-body">
          {selectedKind === "node" && <>{detailHead(selected.name, "组织节点")}
            <dl className="org-facts"><dt>上级组织</dt><dd>{parentName(selected.parentId)}</dd><dt>Human 负责人</dt><dd>{personName(selected.ownerId)}</dd><dt>有效期</dt><dd>{selected.start} — {selected.end || "长期"}</dd><dt>正式成员</dt><dd>{selected.memberIds.map(personName).join("、") || "暂无"}</dd></dl>
            <Button disabled={!canEdit} onClick={() => editNode(selected)}>编辑 / 移动 {selected.name}</Button>{history(selected.history)}</>}
          {selectedKind === "members" && <>{detailHead(selected.name, selected.kind === "Human" ? "Human · 组织成员" : "Agent · 协作成员", selected.kind === "Agent")}
            <div className="org-detail-section"><h3>正式组织归属</h3><p>{memberships(selected).map((node) => node.name).join("、") || "尚未登记"}</p></div>
            <div className="org-detail-section"><div className="org-section-title"><h3>项目角色</h3><IconButton name="Plus" label="添加项目角色" disabled={!canEdit || !s.projects.length} onClick={() => edit("projectRoles", { memberId: selected.id, projectId: s.projects[0]?.id || "", role: "项目成员" })} /></div>
              {org.projectRoles.filter((role) => role.memberId === selected.id).map((role) => <div className="org-project-role" key={role.id}><strong>{s.projects.find((project) => project.id === role.projectId)?.name || "项目不可用"}</strong><span>{role.role}</span><IconButton name="Pencil" label={`编辑项目角色 ${role.id}`} disabled={!canEdit} onClick={() => edit("projectRoles", role)} />{history(role.history)}</div>)}
              {!org.projectRoles.some((role) => role.memberId === selected.id) && <p className="muted">尚未分配项目角色</p>}</div>
            <p className="org-helper">项目协作不会改变正式归属；组织上下级不会获得私人会话访问权。</p></>}
          {selectedKind === "invitations" && <>{detailHead(selected.name, selected.email)}<Badge>{selected.status}</Badge><dl className="org-facts"><dt>项目角色</dt><dd>{selected.role}</dd><dt>可见范围</dt><dd>{selected.scope}</dd><dt>邀请人</dt><dd>{selected.owner}</dd><dt>登记人</dt><dd>{selected.createdBy || "历史未记录"}</dd><dt>有效截止</dt><dd>{selected.expires}{selected.expires < new Date().toISOString().slice(0, 10) ? " · 已过期" : ""}</dd></dl>
            {selected.status === "待接受" && <Button disabled={!canEdit} onClick={() => edit("revoke", selected)}>撤销邀请</Button>}{history(selected.history)}<p className="org-helper">本地模拟记录，不代表邮件已发送或真实账号已加入。</p></>}
        </div> : <div className="org-detail-empty"><Icon name="UsersRound" size={32} /><h3>选择一条记录</h3><p>在这里查看归属、分工与变更历史。</p></div>}
      </aside>
    </div>
  </div>;
}
function OrganizationEditor({ state: s, initial, onClose, onSaved }) {
  const editorRef = useRef(null);
  const [editor, setEditor, draft] = useRecoverableDraft(JSON.stringify([s.activeTenant, "organization", initial.kind, initial.form.id || initial.form.workId || initial.form.memberId || initial.form.parentId || "new"]), () => ({ ...initial, reason: "" }), value => ({ ...value, form: { ...value.form, memberIds: [...(value.form.memberIds || [])].sort() } }));
  const guard = useDraftGuard(draft.modalProps, onClose);
  const org = s.organization, people = org.members.filter(person => person.kind === "Human");
  const options = people.map(person => ({ value: person.id, label: `${person.name} · ${person.kind}` }));
  const humanOptions = people.map(person => ({ value: person.id, label: person.name }));
  const change = patch => setEditor(value => ({ ...value, form: { ...value.form, ...patch } }));
  useEffect(() => { editorRef.current?.querySelector("input:not([type=hidden]),textarea,[role=combobox]")?.focus(); }, []);
  const selectPeople = (field, label) => <fieldset className="org-person-picker"><legend>{label}</legend><div>
    {people.map((person) => <label className="switch-label" key={person.id}>
      <input type="checkbox" aria-label={`${label} ${person.name}`} checked={(editor.form[field] || []).includes(person.id)}
        onChange={(e) => change({ [field]: e.target.checked ? [...(editor.form[field] || []), person.id] : (editor.form[field] || []).filter((id) => id !== person.id) })} />
      <span>{person.name}<small>{person.kind}</small></span></label>)}
  </div></fieldset>;
  return <>
    <div className="org-pane-heading"><h2>{({ nodes: "编辑组织层级", members: "添加演示成员", projectRoles: "配置项目角色", invitation: "创建本地邀请", revoke: "撤销邀请" })[editor.kind]}</h2>
      <IconButton name="X" label="关闭编辑" onClick={() => guard.requestClose()} /></div>
    <div className="org-editor" ref={editorRef}>
      {draft.recovered && <Notice>已恢复此记录的未保存修改，保留原修订。若记录已变化，请放弃后重新打开。</Notice>}
      <ActionForm label={editor.kind === "invitation" ? "创建本地邀请" : editor.kind === "revoke" ? "确认撤销" : "保存"} onCancel={() => guard.requestClose()} onSubmit={() => {
        if (editor.kind === "invitation") saveMemberInvitation(editor.form, editor.reason);
        else if (editor.kind === "revoke") revokeMemberInvitation(editor.form.id, editor.reason, editor.form.revision || 1);
        else saveOrganization(editor.kind, editor.form, editor.reason);
        guard.dismiss(() => onSaved(editor.kind));
      }}>
        {editor.kind === "members" && <><Field label="演示成员名称" value={editor.form.name} onChange={(name) => change({ name })} required /><Notice>只创建本地 Human 成员记录，不代表真实账号已接受邀请。</Notice></>}
        {editor.kind === "nodes" && <>
          <Field label="组织节点名称" value={editor.form.name} onChange={(name) => change({ name })} required />
          <Field label="上级组织" value={editor.form.parentId} options={[{ value: "", label: "顶层组织" }, ...org.nodes.map((node) => ({ value: node.id, label: node.name }))]} onChange={(parentId) => change({ parentId })} />
          <Field label="Human 组织负责人" value={editor.form.ownerId} options={[{ value: "", label: "请选择" }, ...humanOptions]} onChange={(ownerId) => change({ ownerId })} required />
          <div className="form-grid"><Field label="有效开始日期" type="date" value={editor.form.start} onChange={(start) => change({ start })} required />
            <Field label="有效结束日期" type="date" value={editor.form.end} onChange={(end) => change({ end })} /></div>
          {selectPeople("memberIds", "正式成员")}
        </>}
        {editor.kind === "projectRoles" && <>
          <Field label="项目成员" value={editor.form.memberId} options={options} onChange={(memberId) => change({ memberId })} required />
          <Field label="所属项目" value={editor.form.projectId} options={s.projects.map((project) => ({ value: project.id, label: project.name }))} onChange={(projectId) => change({ projectId })} required />
          <Field label="项目职责" value={editor.form.role} options={["项目成员", "项目负责人", "评审者"]} onChange={(role) => change({ role })} required />
          <Notice>只修改项目协作角色，不移动成员的正式组织归属。</Notice>
        </>}
          {editor.kind === "invitation" && <>
            <Field label="姓名" value={editor.form.name} onChange={(name) => change({ name })} required />
            <Field label="电子邮件" type="email" value={editor.form.email} onChange={(email) => change({ email })} required />
            <Field label="邀请人" value={editor.form.ownerId} options={[{ value: "", label: "请选择" }, ...humanOptions]} onChange={(ownerId) => change({ ownerId })} required />
            <Field label="邀请项目" value={editor.form.projectId} options={[{ value: "", label: "请选择当前租户项目" }, ...s.projects.map((project) => ({ value: project.id, label: project.name }))]} onChange={(projectId) => change({ projectId })} required />
            <Field label="项目角色" value={editor.form.role} options={["项目成员", "评审者", "外部客户"]} onChange={(role) => change({ role, scopeKind: role === "外部客户" ? "shared" : editor.form.scopeKind })} />
            <Field label="可见范围" value={editor.form.scopeKind} options={[...(editor.form.role !== "外部客户" ? [{ value: "project", label: "所选项目协作内容" }] : []), { value: "shared", label: "所选项目已共享成果" }]} onChange={(scopeKind) => change({ scopeKind })} />
            <Field label="有效截止日期" type="date" value={editor.form.expires} onChange={(expires) => change({ expires })} required />
            <p className="org-helper">只创建待接受的本地记录，不发送邮件、不添加成员，也不开放私人会话。</p>
          </>}
          {editor.kind === "revoke" && <p>撤销给 {editor.form.name} 的待接受邀请。已有成员资格不受此操作影响。</p>}
          <Field label={editor.kind === "invitation" ? "邀请说明" : editor.kind === "revoke" ? "撤销原因" : "变更说明"} value={editor.reason} onChange={reason => setEditor(current => ({ ...current, reason }))} type="textarea" required />
      </ActionForm>
    </div>
  </>;
}
function WorkflowTemplates({ record, toast }) {
  const s = useStore();
  const [creating, setCreating] = useState(false),
    [name, setName] = useState("");
  const closeCreating = () => { setCreating(false); setName(""); };
  if (record)
    return (
      <>
        <Link className="btn" to="/templates">
          返回工作流模板
        </Link>
        <Flow key={record.id} templateId={record.id} toast={toast} />
      </>
    );
  return (
    <>
      <PageHead
        title="工作流模板"
        subtitle="流程模板与工作包实例分开。复用节点、输入输出与验收结构，编辑后发布新版本。"
      >
        <Button variant="primary" onClick={() => setCreating(true)}>
          新建工作流模板
        </Button>
      </PageHead>
      <div className="agent-grid">
        {s.resources.templates.map((item) => (
          <Link
            key={item.id}
            to={`/templates/${item.id}`}
            className="agent-card"
          >
            <Badge>{item.status}</Badge>
            <h2>{item.name}</h2>
            <p>{item.domain || "自定义流程"}</p>
            <footer>
              {item.owner} ·{" "}
              {item.flow?.flowVersion
                ? `已发布 v${item.flow.flowVersion}`
                : "尚未发布"}
            </footer>
          </Link>
        ))}
      </div>
      {creating && (
        <Modal title="新建工作流模板" draftValue={name} onClose={closeCreating}>
          <ActionForm
            label="创建模板"
            onCancel={closeCreating}
            onSubmit={({ dismiss }) => {
              const templateId = createWorkflowTemplate(name);
              dismiss();
              location.hash = `/templates/${templateId}`;
              toast("模板已创建，可手动编辑或 AI 模拟起草");
            }}
          >
            <Field
              label="工作流模板名称"
              value={name}
              onChange={setName}
              required
            />
          </ActionForm>
        </Modal>
      )}
    </>
  );
}
export function Agents({ agentId, toast }) {
  const confirm = useConfirm(),
    s = useStore(),
    a = s.agents.find((item) => item.id === agentId);
  const [sourceView, setSourceView] = useState("Human");
  const [agentQuery, setAgentQuery] = useState("");
  const creatorKind = (agent) => agent.creatorKind || "未知";
  const creatorName = (agent) => (creatorKind(agent) === "Agent" ? s.agents : s.organization.members).find(person => person.id === agent.creatorId)?.name || "历史未记录创建者";
  const roster = s.agents.filter(agent => creatorKind(agent) === sourceView && `${agent.name} ${agent.role} ${creatorName(agent)}`.toLowerCase().includes(agentQuery.toLowerCase()));
  const [editing, setEditing] = useState(null),
    [config, setConfig] = useState({}),
    [task, setTask] = useState("请完成本次已确认任务并提供证据。");
  const open = (agent, creator) => {
    const model = s.resources.models.find(
      (model) => model.id === agent?.modelId || model.name === agent?.model,
    );
    setConfig({
      creatorKind: agent?.creatorKind || (creator ? "Agent" : "Human"),
      creatorId: agent?.creatorId || creator?.id || "H-lin",
      expectedRevision: agent ? agent.revision || 1 : undefined,
      name: agent?.name || "",
      role: agent?.role || "",
      responsibilities: agent?.responsibilities || "",
      systemPrompt: agent?.systemPrompt || "",
      taskTemplate: agent?.taskTemplate || "本次任务：{{task}}",
      runtime: agent?.runtime || "Codex CLI",
      modelId: model?.id || "",
      owner: agent?.owner || s.organization?.members?.find(member => member.kind === "Human")?.name || "",
      skillIds: agent?.skillIds || [],
      mcpIds: agent?.mcpIds || [],
    });
    setEditing(agent?.id || "new");
  };
  const field = (label, key, type = "text", options) => (
    <Field
      label={label}
      value={config[key] || ""}
      type={type}
      options={options}
      required
      onChange={(value) =>
        setConfig((previous) => ({ ...previous, [key]: value }))
      }
    />
  );
  const toolNames = (ids) =>
    (ids || [])
      .map((id) => {
        const tool = s.resources.tools.find((item) => item.id === id);
        return tool ? `${tool.name} · ${tool.status}` : `${id} · 引用失效`;
      })
      .join("；") || "未选择";
  return (
    <>
      <PageHead
        title={a ? a.name : "Agent 名册"}
        subtitle="Human 创建与 Agent 创建分开管理。任务编组引用这里的身份，任务结束不会删除 Agent。"
      >
        {a && <Button icon="GitBranch" disabled={["退役", "待批准"].includes(a.status)} onClick={() => open(null, a)}>模拟子 Agent 创建提案</Button>}
        <Button icon="Plus" variant="primary" onClick={() => open(null)}>
          创建 Agent 提案
        </Button>
      </PageHead>
      {a ? (
        <div className="detail-grid">
          <Section title="Agent 档案">
            <div className="profile-hero">
              <Avatar name={a.name} agent size="large" />
              <div>
                <h2>{a.name}</h2>
                <p>{a.role}</p>
              </div>
              <Badge>{a.status}</Badge>
            </div>
            <dl>
              <dt>配置版本</dt>
              <dd>
                v{a.revision || 1} ·{" "}
                {a.approvedRevision
                  ? `已批准 v${a.approvedRevision}`
                  : a.status === "待批准"
                    ? "等待批准"
                    : "演示档案"}
              </dd>
              <dt>Human 负责人</dt>
              <dd>{a.owner}</dd>
              <dt>职责边界</dt>
              <dd>{a.responsibilities || "尚未配置"}</dd>
              <dt>Runtime / 模型</dt>
              <dd>
                {a.runtime} / {a.model}
              </dd>
              <dt>System prompt（长期指令）</dt>
              <dd style={{ whiteSpace: "pre-wrap" }}>
                {a.systemPrompt || "尚未配置"}
              </dd>
              <dt>User 任务模板</dt>
              <dd style={{ whiteSpace: "pre-wrap" }}>
                {a.taskTemplate || "尚未配置"}
              </dd>
              <dt>专属 Skills</dt>
              <dd>{toolNames(a.skillIds)}</dd>
              <dt>专属 MCP</dt>
              <dd>{toolNames(a.mcpIds)}</dd>
              <dt>来源</dt>
              <dd>{creatorKind(a)} 创建 · {a.creationMode || "演示档案"}</dd>
              <dt>创建者</dt><dd>{creatorKind(a) === "Agent" && a.creatorId && s.agents.some(parent => parent.id === a.creatorId) ? <Link to={`/agents/${a.creatorId}`}>{creatorName(a)}</Link> : creatorName(a)}</dd>
              <dt>参与任务</dt><dd>{s.work.filter(work => work.team ? [work.team.coordinatorId, work.team.receiverId, ...work.team.executorIds, ...work.team.checkerIds].includes(`agent:${a.id}`) : work.agentId === a.id).map(work => <p key={work.id}><Link to={`/p/${work.project}/work-items/${work.id}`}>{work.title}</Link></p>)}</dd>
              <dt>创建的 Agent</dt><dd>{s.agents.filter(child => child.creatorKind === "Agent" && child.creatorId === a.id).map(child => <p key={child.id}><Link to={`/agents/${child.id}`}>{child.name}</Link> · {child.status}</p>)}</dd>
            </dl>
            <Button onClick={() => open(a)} disabled={a.status === "退役"}>
              编辑配置并重新提案
            </Button>
            <Link className="btn" to="/models">
              模型配置
            </Link>
            <Link className="btn" to="/tools">
              工具授权
            </Link>
            <Notice>
              工具选择只是此 Agent
              的引用清单，不扩大工具授权范围；待授权或已撤销的工具不能真实调用。此原型不启动
              Runtime。
            </Notice>
            {(a.history || []).length > 0 && (
              <details>
                <summary>历史配置（{a.history.length}）</summary>
                {a.history.map((old, index) => (
                  <div key={index}>
                    <b>
                      v{old.revision || 1} · {old.role}
                    </b>
                    <p>{old.responsibilities}</p>
                    <pre>
                      {JSON.stringify(
                        agentMessages(old, "固定示例任务"),
                        null,
                        2,
                      )}
                    </pre>
                  </div>
                ))}
              </details>
            )}
          </Section>
          <Section title="生命周期">
            <div className="stack">
              {["暂停", "空闲", "退役"].map((status) => (
                <Button
                  key={status}
                  disabled={
                    a.status === "退役" ||
                    (status === "空闲" &&
                      (a.status === "待批准" ||
                        (a.revision && a.approvedRevision !== a.revision)))
                  }
                  onClick={async () => {
                    try {
                      assertWrite();
                      if (s.role !== "owner")
                        throw Error("生命周期变更需要负责人确认。");
                      if (
                        status === "退役" &&
                        !(await confirm(
                          "确认退役此 Agent？历史保留，后续停止分派。",
                        ))
                      )
                        return;
                      update((x) => {
                        x.agents.find((v) => v.id === a.id).status = status;
                        log(x, `${a.name} → ${status}`, `/agents/${a.id}`);
                      });
                      toast("Agent 状态已更新");
                    } catch (error) {
                      toast(error.message, true);
                    }
                  }}
                >
                  {status === "空闲" ? "恢复" : status}
                </Button>
              ))}
            </div>
            <Notice>
              待批准配置不能通过恢复绕过审批。暂停与退役保留历史。
            </Notice>
          </Section>
        </div>
      ) : (
        <>
        <nav className="org-tabs" aria-label="Agent 创建来源">{[["Human", "Human 创建"], ["Agent", "Agent 创建"], ...(s.agents.some(agent => creatorKind(agent) === "未知") ? [["未知", "历史来源待核对"]] : [])].map(([value, label]) => <button key={value} className={sourceView === value ? "active" : ""} aria-pressed={sourceView === value} onClick={() => setSourceView(value)}>{label} · {s.agents.filter(agent => creatorKind(agent) === value).length}</button>)}</nav>
        <Field label="搜索 Agent、岗位或创建者" value={agentQuery} onChange={setAgentQuery} />
        {sourceView === "Agent" && <Notice>由 Agent 创建的身份独立保留。创建者退役或任务结束不会级联删除；暂停、接管与退役是有记录的独立操作。</Notice>}
        <div className="agent-grid">
          {roster.map((agent) => (
            <Link
              to={`/agents/${agent.id}`}
              className="agent-card"
              key={agent.id}
            >
              <div className="split">
                <Avatar name={agent.name} agent size="large" />
                <Badge>{agent.status}</Badge>
              </div>
              <h2>{agent.name}</h2>
              <p>{agent.role}</p>
              <div className="agent-details">
                <span>{agent.runtime}</span>
                <span>{agent.model}</span>
              </div>
              <footer>
                {agent.owner} 负责 · 创建者 {creatorName(agent)}
              </footer>
            </Link>
          ))}
        </div>
        {!roster.length && <Empty title="此列表暂无 Agent" text="创建来源会固定保留，其他来源可在对应列表查看。" />}
        </>
      )}
      {!a && (
        <>
          <Section title="Runtime 能力目标">
            <div className="runtime-grid">
              {[
                "Codex CLI",
                "Claude Code",
                "OpenCode",
                "dsh",
                "OpenClaw",
                "Hermes",
                "OpenAI compatible",
                "Anthropic Messages",
              ].map((name) => (
                <div key={name}>
                  <Icon name="Terminal" />
                  <strong>{name}</strong>
                  <Badge>
                    {["dsh", "Hermes"].includes(name)
                      ? "待确认来源"
                      : "模拟适配目标"}
                  </Badge>
                  <small>事件 / 授权 / 停止 / 恢复 / 用量分别验证</small>
                </div>
              ))}
            </div>
          </Section>
          <Section title="多模型候选与独立检查">
            <Notice>
              Heavy
              仅在需要不同能力或独立判断时启用。候选合成后仍需正式验收，模型投票不替代事实。
            </Notice>
            <Link className="btn" to="/p/launch/artifacts/W-103">
              进入候选比较与选择
            </Link>
          </Section>
        </>
      )}
      {editing && (
        <Modal
          title={editing === "new" ? "创建有边界的 Agent" : "编辑 Agent 配置"}
          onClose={() => setEditing(null)}
          draftValue={{ ...config, skillIds: [...(config.skillIds || [])].sort(), mcpIds: [...(config.mcpIds || [])].sort() }}
        >
          <ActionForm
            label="提交配置提案"
            onCancel={() => setEditing(null)}
            onSubmit={({ dismiss }) => {
              const target = saveAgent(
                config,
                editing === "new" ? undefined : editing,
                config.expectedRevision,
              );
              dismiss();
              toast("配置已进入审批，不会立即启动运行");
              location.hash = `/agents/${target}`;
            }}
          >
            <Notice>{config.creatorKind === "Agent" ? `创建来源：Agent · ${s.agents.find(parent => parent.id === config.creatorId)?.name || "历史创建者"}。此操作模拟其创建请求，仍须 Human 审批，不会启动真实进程。` : "创建来源：Human。创建后来源不可在配置编辑中改写。"}</Notice>
            {field("Agent 名称", "name")}
            {field("角色", "role")}
            {field("职责与边界", "responsibilities", "textarea")}
            {field("Human 负责人", "owner", "text", [{ value: "", label: "请选择当前租户 Human" }, ...(s.organization?.members || []).filter(member => member.kind === "Human").map(member => ({ value: member.name, label: member.name }))])}
            {field("System prompt（长期指令）", "systemPrompt", "textarea")}
            {field(
              "User 任务模板（包含 {{task}}）",
              "taskTemplate",
              "textarea",
            )}
            {field("Runtime", "runtime", "text", agentRuntimes)}
            <Field
              label="模型配置引用"
              value={config.modelId || ""}
              onChange={(value) =>
                setConfig((previous) => ({ ...previous, modelId: value }))
              }
              options={[
                { value: "", label: "请选择模型" },
                ...s.resources.models.map((model) => ({
                  value: model.id,
                  label: `${model.name} · ${model.protocol} · ${model.status}`,
                })),
              ]}
            />
            <Notice>
              {agentModelCompatible(
                config.runtime,
                s.resources.models.find((model) => model.id === config.modelId),
              )
                ? "协议符合原型适配声明；真实 Runtime 能力仍待验证。"
                : "请选择协议匹配的模型：Codex CLI 对应 OpenAI，Claude Code 对应 Anthropic；OpenCode / API 支持两类声明。"}
            </Notice>
            {[
              ["Skill", "skillIds", "专属 Skills"],
              ["MCP", "mcpIds", "专属 MCP"],
            ].map(([kind, key, label]) => (
              <fieldset key={key}>
                <legend>{label}</legend>
                {s.resources.tools
                  .filter((tool) => tool.kind === kind)
                  .map((tool) => (
                    <label className="checkline" key={tool.id}>
                      <input
                        type="checkbox"
                        checked={(config[key] || []).includes(tool.id)}
                        onChange={(event) =>
                          setConfig((previous) => ({
                            ...previous,
                            [key]: event.target.checked
                              ? [...previous[key], tool.id]
                              : previous[key].filter((id) => id !== tool.id),
                          }))
                        }
                      />
                      <span>
                        {tool.name} · {tool.status}
                        <small> · {tool.scope}</small>
                      </span>
                    </label>
                  ))}
              </fieldset>
            ))}
            <Field
              label="本次任务示例（不保存为长期指令）"
              value={task}
              onChange={setTask}
              type="textarea"
            />
            <details>
              <summary>查看消息预览（不调用模型）</summary>
              {agentMessages(config, task).map((message) => (
                <div key={message.role}>
                  <b>{message.role}</b>
                  <pre
                    style={{ whiteSpace: "pre-wrap", overflowWrap: "anywhere" }}
                  >
                    {message.content}
                  </pre>
                </div>
              ))}
            </details>
            <Notice>
              System 与 User
              消息分别保存；任务占位符仅在预览时替换。选择工具不代表授予权限。
            </Notice>
          </ActionForm>
        </Modal>
      )}
    </>
  );
}
export function Machines({ nodeId, params, toast }) {
  const confirm = useConfirm();
  const s = useStore();
  const node = s.nodes.find((n) => n.id === nodeId);
  const [install, setInstall] = useState(false);
  const [command, setCommand] = useState("");
  const [output, setOutput] = useState([
    "$ runner status",
    "daemon: online · cloud: connected",
    "workspace: /workspaces/northstar",
    "交互终端默认只读，控制权需单独申请。",
  ]);
  const [control, setControl] = useState(false);
  const [preview, setPreview] = useState(true);
  const [file, setFile] = useState("README.md");
  const tab = params.get("tab") || "status";
  return (
    <>
      <PageHead
        eyebrow="团队 / 执行资源"
        title={node ? node.name : "工作在任何地方，都能继续"}
        subtitle="纯 Go Runner · 出站连接 · 系统保活 · 有界执行"
      >
        <Button variant="primary" icon="Plus" onClick={() => setInstall(true)}>
          接入节点
        </Button>
      </PageHead>
      {node ? (
        <>
          <div className="tabs">
            {[
              ["status", "状态与维护"],
              ["files", "工作区文件"],
              ["terminal", "实时终端"],
              ["preview", "预览与隧道"],
            ].map(([k, v]) => (
              <Link
                to={`/machines/${node.id}?tab=${k}`}
                key={k}
                className={tab === k ? "selected" : ""}
              >
                {v}
              </Link>
            ))}
          </div>
          {tab === "status" && (
            <>
              <div className="metrics">
                <Metric
                  label="节点状态"
                  value={node.status}
                  detail="心跳与业务就绪分别判断"
                  icon="Radio"
                />
                <Metric
                  label="CPU"
                  value={node.status === "失联" ? "未知" : `${node.cpu}%`}
                  detail="最近一次采样"
                />
                <Metric
                  label="内存"
                  value={node.status === "失联" ? "未知" : `${node.mem}%`}
                  detail="主机内存使用"
                />
                <Metric
                  label="执行槽"
                  value={node.slots}
                  detail={`Runner ${node.version}`}
                  icon="Boxes"
                />
              </div>
              <Section title="服务生命周期">
                <div className="actions">
                  {[
                    ["在线", "启动"],
                    ["排空中", "平滑停止"],
                    ["维护中", "进入维护"],
                    ["排空升级中", "排空升级"],
                    ["已吊销", "吊销节点"],
                  ].map(([status, label]) => (
                    <Button
                      key={label}
                      onClick={async () => {
                        try {
                          assertWrite();
                          if (
                            status === "已吊销" &&
                            !(await confirm(
                              "吊销节点会停止后续任务与访问。确认继续？",
                            ))
                          )
                            return;
                          update((x) => {
                            x.nodes.find((n) => n.id === node.id).status =
                              status;
                            log(
                              x,
                              `${node.name} → ${status}`,
                              `/machines/${node.id}`,
                            );
                          });
                          toast("节点命令已记录（模拟）");
                        } catch (e) {
                          toast(e.message, true);
                        }
                      }}
                    >
                      {label}
                    </Button>
                  ))}
                </div>
                <Notice>
                  排空停止接新任务，等待活跃任务结束或安全接续后切换。系统服务自动重启不等于任务无损恢复。
                </Notice>
              </Section>
            </>
          )}
          {tab === "files" && (
            <div className="file-browser">
              <aside>
                {[
                  "README.md",
                  "src/App.tsx",
                  "src/styles.css",
                  "evidence/mobile-375.json",
                  "artifacts/manifest.json",
                ].map((f) => (
                  <button
                    className={file === f ? "active" : ""}
                    key={f}
                    onClick={() => setFile(f)}
                  >
                    <Icon
                      name={f.endsWith("json") ? "FileJson" : "FileCode2"}
                    />
                    {f}
                  </button>
                ))}
              </aside>
              <div>
                <div className="split">
                  <h2>{file}</h2>
                  <Badge tone="violet">只读 · 工作区内</Badge>
                </div>
                <pre>
                  {file === "README.md"
                    ? "# Northstar\n\n固定成果版本 v3\n负责人：林予\n状态：等待移动端内部复核\n\n本视图仅演示授权工作区文件，不读取真实主机。"
                    : file.endsWith("json")
                      ? JSON.stringify(
                          {
                            work: "W-104",
                            version: 3,
                            status: "requires-review",
                            source: "prototype-fixture",
                          },
                          null,
                          2,
                        )
                      : '// 原型文件预览\n// 实际代码通过授权文件接口载入\nexport const artifact = { version: 3, status: "submitted" };'}
                </pre>
                <Button
                  icon="Download"
                  onClick={() =>
                    exportData("file-preview", {
                      file,
                      work: "W-104",
                      simulated: true,
                    })
                  }
                >
                  下载引用清单
                </Button>
              </div>
            </div>
          )}
          {tab === "terminal" && (
            <Section
              title="工作区终端"
              extra={
                <Badge tone={control ? "amber" : "violet"}>
                  {control ? "Human 控制中" : "只读观察"}
                </Badge>
              }
            >
              <Notice tone="warn">
                模拟终端，不执行系统命令。生产节点需独立 Shell
                权限、超时、审计与单写控制。
              </Notice>
              <div className="terminal">
                <pre>{output.join("\n")}</pre>
                <Form
                  onSubmit={(e) => {
                    e.preventDefault();
                    if (!control) {
                      toast("请先申请终端控制权。", true);
                      return;
                    }
                    if (!command.trim()) return;
                    setOutput([
                      ...output,
                      `$ ${command}`,
                      "[模拟] 命令已记录；未调用真实 Shell。",
                    ]);
                    update((x) =>
                      log(
                        x,
                        `模拟终端输入 · ${command}`,
                        `/machines/${node.id}?tab=terminal`,
                      ),
                    );
                    setCommand("");
                  }}
                >
                  <span>$</span>
                  <input
                    aria-label="终端命令"
                    value={command}
                    onChange={(e) => setCommand(e.target.value)}
                    placeholder="输入模拟命令…"
                    disabled={!control}
                  />
                  <Button type="submit" disabled={!control}>
                    发送
                  </Button>
                </Form>
              </div>
              <Button
                onClick={() => {
                  try {
                    assertWrite();
                    setControl(!control);
                    toast(control ? "控制权已释放" : "已取得本地模拟控制权");
                  } catch (e) {
                    toast(e.message, true);
                  }
                }}
              >
                {control ? "释放控制" : "申请 Human 控制"}
              </Button>
            </Section>
          )}
          {tab === "preview" && (
            <Section title="当前预览实例">
              <div className="preview-record">
                <Icon name="Globe2" size={32} />
                <div className="grow">
                  <h2>northstar-v3 · Web / API</h2>
                  <p>随机域名、身份验证、租约与实际资源清理</p>
                  <code>preview-8f3a.example.invalid</code>
                </div>
                <Badge>{preview ? "在线" : "已释放"}</Badge>
              </div>
              <dl>
                <dt>归属</dt>
                <dd>W-104 / v3 / 当前节点</dd>
                <dt>权限</dt>
                <dd>项目成员 · 只读预览</dd>
                <dt>租约</dt>
                <dd>
                  {preview ? "剩余 28 分钟 · 模拟续租" : "已撤权并记录模拟释放"}
                </dd>
                <dt>清理范围</dt>
                <dd>代理、凭据、域名映射、节点监听与子进程</dd>
              </dl>
              <div className="actions">
                <Link to="/p/launch/artifacts/W-104" className="btn">
                  打开授权预览
                </Link>
                <Button
                  onClick={() => {
                    setPreview(!preview);
                    update((x) =>
                      log(
                        x,
                        preview ? "释放模拟预览资源" : "重新申请模拟预览",
                        `/machines/${node.id}?tab=preview`,
                      ),
                    );
                    toast(
                      preview ? "访问已关闭，清理记录已保存" : "模拟预览已申请",
                    );
                  }}
                >
                  {preview ? "撤权并释放" : "重新申请"}
                </Button>
              </div>
            </Section>
          )}
        </>
      ) : (
        <>
          <div className="node-grid">
            {s.nodes.map((n) => (
              <Link className="node-card" to={`/machines/${n.id}`} key={n.id}>
                <div className="split">
                  <span className="node-icon">
                    <Icon
                      name={n.os === "Linux" ? "Server" : "Monitor"}
                      size={28}
                    />
                  </span>
                  <Badge>{n.status}</Badge>
                </div>
                <h2>{n.name}</h2>
                <p>
                  {n.os} · {n.runtime}
                </p>
                <div className="split">
                  <small>CPU</small>
                  <b>{n.status === "失联" ? "未知" : `${n.cpu}%`}</b>
                </div>
                <Progress value={n.cpu} />
                <div className="split">
                  <small>内存</small>
                  <b>{n.status === "失联" ? "未知" : `${n.mem}%`}</b>
                </div>
                <Progress value={n.mem} />
                <footer>
                  <span>执行槽 {n.slots}</span>
                  <small>v{n.version}</small>
                </footer>
              </Link>
            ))}
          </div>
          <Section title="云端与节点，保持同一个事实">
            <div className="link-tiles">
              <Link to="/health">
                <Icon name="Activity" />
                <h3>健康与告警</h3>
                <p>区分主机在线、进程存活与业务可用</p>
              </Link>
              <Link to="/models">
                <Icon name="SlidersHorizontal" />
                <h3>模型配置</h3>
                <p>期望与实际版本、灰度和回滚</p>
              </Link>
              <Link to="/schedules">
                <Icon name="CalendarClock" />
                <h3>持久调度</h3>
                <p>时区、补跑、幂等与授权</p>
              </Link>
            </div>
          </Section>
        </>
      )}
      {install && (
        <Modal title="把这台设备加入执行网络" onClose={() => setInstall(false)}>
          <Notice>
            原生 Runner 不需要
            Node.js。以下是发行命令形态说明，原型不安装服务或提交真实令牌。
          </Notice>
          <ol className="install-steps">
            <li>
              <h3>下载对应系统的签名安装器</h3>
              <p>macOS / Linux / Windows · 校验版本与完整性</p>
            </li>
            <li>
              <h3>注册系统服务</h3>
              <code>runner install</code>
            </li>
            <li>
              <h3>安全绑定账号</h3>
              <code>runner auth login --token-stdin</code>
            </li>
            <li>
              <h3>启动并核对状态</h3>
              <code>
                runner start
                <br />
                runner status
                <br />
                runner doctor
              </code>
            </li>
          </ol>
          <Button
            variant="primary"
            onClick={() => {
              try {
                assertWrite();
                update((x) => {
                  x.nodes.push({
                    id: id("node"),
                    name: "新节点 · 待认证",
                    os: "Linux",
                    status: "待认证",
                    runtime: "未发现",
                    slots: "0 / 0",
                    version: "未确认",
                    cpu: 0,
                    mem: 0,
                  });
                  log(x, "创建待认证节点记录", "/machines");
                });
                setInstall(false);
                toast("已创建待认证演示节点");
              } catch (e) {
                toast(e.message, true);
              }
            }}
          >
            模拟创建待认证节点
          </Button>
        </Modal>
      )}
    </>
  );
}
export function Cost({ toast }) {
  const s = useStore();
  const [limit, setLimit] = useState(String(s.budget));
  const [policy, setPolicy] = useState(
    s.resources.policies.find((p) => p.id === "POL-02")?.status === "启用",
  );
  const total = s.work.reduce((n, w) => n + w.cost, 0);
  return (
    <>
      <PageHead
        title="清楚每一份投入，支持每一个结果"
        subtitle="实际、估算与未知分开记录；失败和候选也计入成本。"
      >
        <Button
          icon="Download"
          onClick={() =>
            exportData(
              "usage",
              s.work.map(({ id, cost, agent, project }) => ({
                id,
                cost,
                agent,
                project,
              })),
            )
          }
        >
          导出明细
        </Button>
      </PageHead>
      <div className="metrics">
        <Metric
          label="已记录费用"
          value={formatCurrency(total)}
          detail="USD · 当前演示工作"
        />
        <Metric
          label="预算使用"
          value={`${Math.round((total / Number(limit)) * 100)}%`}
          detail={`项目预算 $${limit}`}
        />
        <Metric label="待对账调用" value="3" detail="未报告用量不记为零" />
        <Metric
          label="自动调节"
          value={policy ? "已开启" : "已暂停"}
          detail="不改变质量要求与数据边界"
        />
      </div>
      <div className="two-col">
        <Section
          title="近 7 日执行成本"
          extra={<Badge tone="violet">含失败与候选</Badge>}
        >
          <div className="bar-chart">
            {[24, 42, 35, 68, 51, 84, 63].map((v, i) => (
              <div key={i}>
                <span>${v}</span>
                <i style={{ height: `${v * 2}px` }} />
                <small>10/{7 + i}</small>
              </div>
            ))}
          </div>
        </Section>
        <Section title="预算与自动调节">
          <Field
            label="项目预算（USD）"
            value={limit}
            onChange={setLimit}
            type="number"
            min="1"
          />
          <label className="switch-label">
            <input
              type="checkbox"
              checked={policy}
              onChange={(e) => setPolicy(e.target.checked)}
            />
            <span>允许在授权模型集合内调节</span>
          </label>
          <Notice>
            80% 提醒；100%
            停止新派发。缓存、上下文裁剪与候选数量调整不得删除硬要求。
          </Notice>
          <Button
            variant="primary"
            onClick={() => {
              try {
                assertWrite();
                if (Number(limit) <= 0) throw Error("预算必须大于零。");
                update((x) => {
                  x.budget = Number(limit);
                  let policyRecord = x.resources.policies.find(
                    (p) => p.id === "POL-02",
                  );
                  if (!policyRecord) {
                    policyRecord = {
                      id: "POL-02",
                      name: "预算自动调节",
                      owner: "林予",
                    };
                    x.resources.policies.push(policyRecord);
                  }
                  policyRecord.status = policy ? "启用" : "暂停";
                  log(
                    x,
                    `调整预算 $${limit}，自动调节${policy ? "启用" : "暂停"}`,
                    "/cost",
                  );
                });
                toast("预算策略已保存");
              } catch (e) {
                toast(e.message, true);
              }
            }}
          >
            保存策略
          </Button>
        </Section>
      </div>
      <Section title="按工作追溯">
        <div className="table-scroll">
          <table>
            <thead>
              <tr>
                <th>工作</th>
                <th>执行者</th>
                <th>费用</th>
                <th>计量状态</th>
                <th>来源</th>
              </tr>
            </thead>
            <tbody>
              {s.work.map((w) => (
                <tr key={w.id}>
                  <td>
                    <Link to={`/p/${w.project}/work-items/${w.id}`}>
                      {w.title}
                    </Link>
                  </td>
                  <td>{w.agent}</td>
                  <td>{formatCurrency(w.cost)}</td>
                  <td>
                    <Badge tone="violet">模拟账本</Badge>
                  </td>
                  <td>{w.id}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Section>
    </>
  );
}
export function Audit() {
  const s = useStore();
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(1);
  const needle = query.trim().toLocaleLowerCase();
  const filtered = s.events.filter(event => `${event.text} ${event.actor} ${event.id} ${event.target}`.toLocaleLowerCase().includes(needle));
  const pages = Math.max(1, Math.ceil(filtered.length / 50));
  const currentPage = Math.min(page, pages);
  const visible = filtered.slice((currentPage - 1) * 50, currentPage * 50);
  return <>
    <PageHead title="决定有依据，协作有来路" subtitle="从一个交付，追溯到它的要求、讨论、执行与批准。">
      <Button icon="Download" onClick={() => exportData("audit", filtered)}>导出当前筛选记录</Button>
    </PageHead>
    <label className="search-field"><Icon name="Search" />
      <input aria-label="查找审计事件" name="audit-search" autoComplete="off" value={query}
        onChange={event => { setQuery(event.target.value); setPage(1); }} placeholder="查找主体、动作、对象或记录编号…" />
    </label>
    <Section title="不可覆盖的活动历史">
      <p className="muted" role="status">共 {new Intl.NumberFormat("zh-CN").format(filtered.length)} 条记录 · 第 {currentPage} / {pages} 页</p>
      {!filtered.length ? <Empty title={query ? "没有匹配的活动" : "还没有活动记录"} text={query ? "尝试其他主体、动作或对象编号。" : "提交、修改和审核等操作会保留在这里。"} /> : <div className="audit-list">
        {visible.map(event => <Link key={event.id} to={event.target}>
          <span className="audit-icon"><Icon name="Fingerprint" /></span>
          <div className="grow"><strong>{event.text}</strong><small>{event.actor} · {formatTimestamp(event.time)} · <span translate="no">{event.id}</span></small></div>
          <Icon name="ArrowUpRight" />
        </Link>)}
      </div>}
      {pages > 1 && <nav className="actions" aria-label="审计记录分页">
        <Button disabled={currentPage === 1} onClick={() => setPage(currentPage - 1)}>上一页</Button>
        <Button disabled={currentPage === pages} onClick={() => setPage(currentPage + 1)}>下一页</Button>
      </nav>}
    </Section>
  </>;
}
export function Settings({ toast }) {
  const s = useStore();
  return (
    <>
      <PageHead
        title="你的空间，你的节奏"
        subtitle="外观、通知、隐私与设备，围绕你的工作方式配置。"
      />
      <div className="two-col">
        <Section title="外观与客户端">
          <Field
            label="主题"
            options={[
              "aurora",
              "modern",
              "business",
              "eyeCare",
              "pinkKawaii",
              "neoBrutalism",
            ]}
            value={s.theme}
            onChange={(v) => update((x) => (x.theme = v))}
          />
          <label className="switch-label">
            <input
              type="checkbox"
              checked={s.dark}
              onChange={(e) => update((x) => (x.dark = e.target.checked))}
            />
            深色模式
          </label>
          <div className="client-platforms">
            {["Web", "macOS", "Windows", "Linux", "iOS", "Android"].map((p) => (
              <span key={p}>
                <Icon
                  name={
                    p === "iOS" || p === "Android" ? "Smartphone" : "Monitor"
                  }
                />
                {p}
              </span>
            ))}
          </div>
          <p className="muted">
            同一 Web UI，响应式布局与 Tauri
            壳能力分离。本原型可缩窄浏览器体验手机布局。
          </p>
        </Section>
        <Section title="通知与隐私">
          <label className="switch-label">
            <input
              type="checkbox"
              checked={s.notifications.critical}
              onChange={(e) =>
                update((x) => (x.notifications.critical = e.target.checked))
              }
            />
            关键审批与停线提醒
          </label>
          <label className="switch-label">
            <input
              type="checkbox"
              checked={s.notifications.grouped}
              onChange={(e) =>
                update((x) => (x.notifications.grouped = e.target.checked))
              }
            />
            普通频道消息聚合
          </label>
          <label className="switch-label">
            <input
              type="checkbox"
              checked={s.notifications.memory}
              onChange={(e) =>
                update((x) => (x.notifications.memory = e.target.checked))
              }
            />
            自动提取记忆候选
          </label>
          <Notice>
            消息默认永久保存。私人资料不会因加入团队自动公开；记忆可更正与禁用。
          </Notice>
          <Link to="/knowledge" className="btn">
            管理资料与记忆
          </Link>
        </Section>
        <Section title="会话与设备">
          <div className="device-row">
            <Icon name="Monitor" />
            <div>
              <b>当前浏览器</b>
              <small>本地原型 · 已保存演示状态</small>
            </div>
            <Badge tone="green">当前设备</Badge>
          </div>
          <Button
            onClick={() =>
              toast("演示设备已标记撤销；真实会话由身份接口管理。")
            }
          >
            模拟撤销其他设备
          </Button>
        </Section>
        <Section title="演示与异常状态">
          <Field
            label="角色视角"
            value={s.role}
            options={["owner", "executor", "external"]}
            onChange={(v) => update((x) => (x.role = v))}
          />
          <label className="switch-label">
            <input
              type="checkbox"
              checked={s.offline}
              onChange={(e) => update((x) => (x.offline = e.target.checked))}
            />
            模拟离线（阻止正式写操作）
          </label>
          <p className="muted">
            owner：负责人 · executor：执行者 ·
            external：外部客户。角色切换仅用于原型验证，不是安全鉴权。
          </p>
        </Section>
      </div>
    </>
  );
}

export function ResourcePage(props) {
  if (props.kind === "goals") return <GoalsPage {...props} legacyPage={<GenericResourcePage {...props} />} />;
  return <GenericResourcePage {...props} />;
}
