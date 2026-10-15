import { ApprovalOrchestration, HumanLifecycle } from "./governance-ui";
import { BudgetPage } from "./pages-budget";
import { recordProjectId } from "./project-lifecycle";
import { DataList } from "./data-list";
import { visibleExternalBridgeBindings } from "./external-bridge";
import { useViewState } from "./view-state";
import { KnowledgeLibrary } from "./knowledge-library";
import { AgentChannelGrants, DirectMessageButton } from "./channel-management";
import { projectRecordWriteProblem, projectIsArchived, activeProjects, requestProjectLease, releaseProjectLease } from "./store";
import { approvalSource, approvalDecisionProblem, requestModelApproval } from "./store.js";
import { repositoryRecords, repositoryWriteProblem } from "./store.js";
import { GoalsPage } from "./pages-goals";
import { memoryRevocationProblem } from "./knowledge";
import { formatCurrency, formatTimestamp } from "./formatting";
import React, { useState, useEffect, useRef, useCallback } from "react";
import { catalog, fieldNames, resourceFields, themeNames } from "./data";
import { QAResourcePage } from "./pages-qa";
import { Flow } from "./pages-flow";
import { reportCadences, reportSubjects, reportPeriod, dateInZone, reportChildProblem } from "./reporting";
import { reportNextPlan, reportNextPlanBinding, reportNextPlanDescriptor, reportCurrentPlan, reportCurrentPlanBinding, reportCurrentPlanBindingProblem, planCanRead } from "./plans";
import { ReportContentEditor, ReportContentReader } from "./report-content-ui";
import { reportNextPlanItems } from "./report-content";
import {
  useStore,
  read,
  visibleKnowledge,
  visibleActivityEvents,
  revokeKnowledgeMemory,
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
  openReportNextPlan,
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
function ApprovalBasis({ state, record }) {
  const source = approvalSource(state, record);
  const legacyModels = !record.targetKind ? state.resources.models.filter(model => model.id === record.target) : [];
  const currentHref = source?.href || (legacyModels.length === 1 ? `/models/${legacyModels[0].id}` : null);
  const text = value => typeof value === "string" && value.trim() ? value : typeof value === "number" ? String(value) : "未记录";
  const person = (id, kind) => `${text(id)} · ${text(kind)}`;
  const sourceType = kind => kind === "model" ? "模型配置" : kind === "agent" ? "Agent 配置" : "来源类型未记录或不受支持";
  const fields = record.targetKind === "model"
    ? [["id", "模型配置 ID"], ["name", "配置名称"], ["owner", "负责人"], ["protocol", "协议"], ["baseURL", "服务地址"], ["model", "模型"], ["scope", "应用范围"], ["secret", "凭据引用"], ["version", "配置版本"]]
    : record.targetKind === "agent"
      ? [["id", "Agent ID"], ["name", "名称"], ["role", "角色"], ["responsibilities", "职责"], ["systemPrompt", "System prompt"], ["taskTemplate", "任务模板"], ["runtime", "运行时"], ["modelId", "模型配置 ID"], ["modelRevision", "模型配置修订"], ["owner", "负责人"], ["skillIds", "技能引用"], ["mcpIds", "MCP 引用"], ["creatorKind", "创建者类型"], ["creatorId", "创建者 ID"]]
      : [];
  const snapshot = (value) => value && fields.length ? <dl className="record-fields">{fields.map(([key, label]) => <React.Fragment key={key}>
    <dt>{label}</dt><dd style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>{Array.isArray(value[key]) ? value[key].length ? <ul>{value[key].map((item, index) => <li key={index}>{text(item)}</li>)}</ul> : "无引用" : text(value[key])}</dd>
  </React.Fragment>)}</dl> : <p className="muted">未保存固定配置，不能用当前配置补造历史依据。</p>;
  return <Section title="固定审批依据">
    <Notice>本地 Human 审批模拟；决定仅适用于提交时的固定配置，不代表真实下发。</Notice>
    <dl className="record-fields">
      <dt>来源类型</dt><dd>{sourceType(record.targetKind)}</dd>
      <dt>来源 ID</dt><dd>{text(record.target)}</dd>
      <dt>提交修订</dt><dd>{Number.isSafeInteger(record.targetRevision) && record.targetRevision > 0 ? record.targetRevision : "未记录"}</dd>
      <dt>提出者</dt><dd>{person(record.requestedBy?.id, record.requestedBy?.kind)}</dd>
      <dt>请求来源</dt><dd>{record.requestedBy?.source === "LOCAL_SIMULATION" ? "本地模拟" : "未记录"}</dd>
      <dt>提交时间</dt><dd>{record.requestedAt ? formatTimestamp(record.requestedAt) : "未记录"}</dd>
    </dl>
    <details open><summary>提交时固定配置</summary>{snapshot(record.sourceSnapshot)}</details>
    {currentHref && <p><Link className="btn" to={currentHref}>{record.targetKind ? "查看当前源配置 / 重新提交" : "查看当前配置"}</Link></p>}
    {!record.targetKind && <p className="muted">旧记录缺少固定来源；当前配置导航不代表原审批依据。</p>}
    {record.decision ? <>
      <h3>固定决定</h3>
      <dl className="record-fields">
        <dt>决定</dt><dd>{text(record.decision.status)}</dd>
        <dt>决定者</dt><dd>{person(record.decision.actorId, record.decision.actorKind)}</dd>
        <dt>决定来源</dt><dd>{record.decision.source === "LOCAL_SIMULATION" ? "本地模拟" : "未记录"}</dd>
        <dt>决定时间</dt><dd>{record.decision.time ? formatTimestamp(record.decision.time) : "未记录"}</dd>
        <dt>决定依据</dt><dd style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>{text(record.decision.reason)}</dd>
        <dt>决定所指来源</dt><dd>{sourceType(record.decision.targetKind)} · {text(record.decision.target)} · 修订 {text(record.decision.targetRevision)}</dd>
      </dl>
      <details><summary>决定时固定配置</summary>{snapshot(record.decision.snapshot)}</details>
    </> : <p className="muted">没有固定决定记录。</p>}
  </Section>;
}
function GenericResourcePage({ kind, recordId, toast, params }) {
  const s = useStore();
  const [includeRevoked, setIncludeRevoked] = useState(false);
  const [editing, setEditing] = useState(null);
  const [generation, setGeneration] = useState(null);
  const [goalCalibration, setGoalCalibration] = useState(null);
  const [action, setAction] = useState(null);
  const [reason, setReason] = useState("");
  const [receiptItems, setReceiptItems] = useState([]);
  const [verificationRunId, setVerificationRunId] = useState("");
  const [, redrawList]=useState(0), tenantId=s.activeTenant, projectId=s.project, role=s.role;
  const rereadList=useCallback(()=>{const current=read();if(current.activeTenant!==tenantId||current.project!==projectId||current.role!==role)throw Error("列表范围已变化，请重新打开当前列表。");return current;},[tenantId,projectId,role]);
  const refreshList=useCallback(()=>{rereadList();redrawList(value=>value+1);},[rereadList]);
  const exportList=useCallback(payload=>{const current=rereadList();const authorized=(kind==="knowledge"?visibleKnowledge(current,{includeRevoked:true}):current.resources[kind]||[]).filter(record=>!["tests","deliveries"].includes(kind)||current.project==="all"||recordProjectId(current,record)===current.project);return payload.records.every(row=>authorized.some(record=>record.id===row.id && record.name===row.name && record.status===row.status && (record.owner||"")===row.owner));},[rereadList,kind]);
  const meta = catalog[kind];
  if (!meta) return <Empty title="页面不存在" />;
  const sourceRows = kind === "knowledge" ? visibleKnowledge(s, { includeRevoked: Boolean(recordId) || includeRevoked, includeArchived: Boolean(recordId) }) : s.resources[kind] || [];
  const rows = !recordId && ["tests","deliveries"].includes(kind) && s.project!=="all" ? sourceRows.filter(record=>recordProjectId(s,record)===s.project) : sourceRows;
  const record = rows.find((r) => r.id === recordId);
  const recordedText = value => typeof value === "string" && value.trim() ? value : "未记录";
  if (s.role === "external" && !["deliveries"].includes(kind))
    return (
      <Empty
        title="当前视角无权访问"
        text="外部成员仅访问已共享成果、会话与回执。"
      />
    );
  if (recordId && !record) return <Empty title={kind === "knowledge" ? "资料不存在或无权访问" : "记录不存在"} />;
  if (["testplans", "testreports"].includes(kind)) return <QAResourcePage kind={kind} recordId={recordId} toast={toast} />;
  if (kind === "templates") return <WorkflowTemplates record={record} toast={toast} params={params} />;
  const defaults = (kind === "knowledge" ? null : rows[0]) || {
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
        eyebrow={record ? `${meta[0]} / ${record.id}` : undefined}
        title={record ? record.name : meta[0]}
        subtitle={meta[1]}
      >
        {record ? (
          <>
            <Link className="btn" to={`/${kind}`}>
              返回列表
            </Link>
            {!["deliveries", "approvals"].includes(kind) && !record.revocation && <Button icon="Pencil" disabled={Boolean(projectRecordWriteProblem(s,record)) || (kind === "goals" ? Boolean(goalWriteProblem(s, record, true)) : kind === "reports" ? Boolean(reportEditProblem(s, record, { simulateAgent: true })) : false)} title={kind === "goals" ? goalWriteProblem(s, record, true) || "" : kind === "reports" ? reportEditProblem(s, record, { simulateAgent: true }) || "" : undefined} onClick={() => setEditing({ ...record })}>
              编辑
            </Button>}
          </>
        ) : null}

        {kind === "models" && record && <Button onClick={() => { try { const requestId = requestModelApproval(record.id, record.revision || 1); location.hash = `/approvals/${requestId}`; } catch (error) { toast(error.message, true); } }}>提交当前配置审批</Button>}

        {kind === "reports" && <Button icon="CalendarClock" onClick={() => setGeneration({ mode: "schedule" })}>定时生成设置</Button>}
        {record && <Button
          icon="Download"
          onClick={() => exportData(kind, record)}
        >
          导出
        </Button>}
      </PageHead>
      {kind === "models" && <Notice>本地模型与配置复用 cc-switch / cc-switch-cli 的供应商、切换、MCP、Skills 与提示词能力。本页是云配置目录和远程下发原型；配置审批、目标节点、版本与应用回执由平台扩展。尚未接入真实 cc-switch，不会修改本机配置。 <a href="https://github.com/SaladDay/cc-switch-cli" target="_blank" rel="noopener noreferrer">查看 cc-switch-cli</a></Notice>}
      {record ? (
        <div className="detail-grid">
          <div>
            {projectRecordWriteProblem(s,record) && <Notice>所属项目已归档，此记录保持原权限并只读。</Notice>}
            <Section title="基本信息" extra={<Badge>{record.status}</Badge>}>
              <dl className="record-fields">
                {Object.entries(record)
                  .filter(
                    ([k]) =>
                      !["id", "status", "name", "history", "revision", "items", "requirements", "runs", "currentRun"].includes(
                        k,
                       ) && !(kind === "approvals" && ["sourceSnapshot", "requestedBy", "requestedAt", "decision", "targetKind", "target", "targetRevision", "tenantId"].includes(k)) && !(kind === "knowledge" && ["ownerId", "ownerKind", "tenantId", "revocation"].includes(k)) && !(kind === "deliveries" && k === "sendFact") && !(kind === "models" && k === "nodeResults") && !(kind === "goals" && ["metricRevision", "progressCalibration", "progress", "submission", "approval", "ownerId", "ownerKind"].includes(k)) && !(kind === "reports" && ["references", "factSnapshot", "subjectKey", "cadence", "timezone", "periodStart", "periodEnd", "cycle", "generatedSummary", "generatedFacts", "suggestedContent", "suggestedContentFacts", "suggestedCurrentPlanBinding", "generatedContentIssues", "suggestedReferences", "generatedAt", "generatedBy", "generationHistory", "summary", "risk", "source", "submission", "approval", "authorId", "authorKind", "content", "nextPlanBinding", "currentPlanBinding", "contentRequired", "contentMigration"].includes(k)),
                  )
                  .map(([k, v]) => (
                    <React.Fragment key={k}>
                      <dt>{kind === "tests" && k === "requirementRefs" ? "关联要求" : kind === "reports" && k === "source" ? "旧来源说明（非事实引用）" : fieldNames[k] || k}</dt>
                      <dd>
                        {kind === "tests" && k === "requirementRefs"
                          ? Array.isArray(v) ? v.length ? <ul>{v.map((requirement, index) => <li key={index}>{typeof requirement === "string" ? requirement : "条款记录格式无效，请核对原始记录"}</li>)}</ul> : "未关联要求" : "条款记录格式无效，请核对原始记录"
                          : typeof v === "object" ? JSON.stringify(v) : String(v)}
                      </dd>
                    </React.Fragment>
                  ))}
              </dl>
            </Section>
            {kind === "knowledge" && record.revocation && <Section title="记忆撤销记录">
              <Notice>此派生记忆已终态撤销，不再列入日常资料或默认搜索。原消息与有权查看的历史保留；此页是历史原件，不承诺物理擦除。</Notice>
              <dl className="record-fields"><dt>撤销人</dt><dd>{record.revocation.actorId} · Human</dd><dt>撤销时间</dt><dd>{formatTimestamp(record.revocation.time)}</dd><dt>理由</dt><dd>{record.revocation.reason}</dd><dt>来源</dt><dd>本地模拟；没有真实模型上下文或索引服务</dd></dl>
            </Section>}
            {kind === "approvals" && <><ApprovalBasis state={s} record={record} /><ApprovalOrchestration key={`${s.activeTenant}:${record.id}`} record={record}/></>}
            {kind === "deliveries" && <Section title="固定发送记录">
              <Notice>{record.sendFact?.source === "LOCAL_SIMULATION"
                ? "本地模拟发送记录，不代表真实远程发布或客户接收。客户回执另行记录。"
                : "此记录未提供可核验的发送来源；不据此确认真实远程发布或客户接收。"}</Notice>
              {record.sendFact ? <dl className="record-fields">
                <dt>记录主体</dt><dd><span translate="no">{recordedText(record.sendFact.actor?.id)}</span> · {record.sendFact.actor?.kind === "Human" ? "Human" : record.sendFact.actor?.kind === "Agent" ? "Agent" : "主体类型未记录"}</dd>
                <dt>记录时间</dt><dd>{formatTimestamp(record.sendFact.time)}</dd>
                <dt>固定交付包</dt><dd><span translate="no">{recordedText(record.sendFact.deliverySnapshot?.id)}</span> · {recordedText(record.sendFact.deliverySnapshot?.name)}</dd>
                <dt>关联工作 / 固定版本</dt><dd><span translate="no">{recordedText(record.sendFact.deliverySnapshot?.workId)}</span> · {Number.isSafeInteger(record.sendFact.deliverySnapshot?.version) && record.sendFact.deliverySnapshot.version > 0 ? `v${record.sendFact.deliverySnapshot.version}` : "版本未记录"}</dd>
                <dt>原收件范围</dt><dd>{recordedText(record.sendFact.deliverySnapshot?.recipient)}</dd>
              </dl> : <p className="muted">旧包未保存固定发送记录；不会从当前负责人或工作状态补造历史。</p>}
            </Section>}
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
            {kind === "connectors" && <Section title="外部会话的使用位置">
                <p>连接后，在具体的外部共享频道「频道设置 → 外部会话」中绑定 Slack 频道、WhatsApp 会话等。绑定范围内的消息显示平台来源；回复先保存草稿，再由 Human 明确授权。</p>
                {visibleExternalBridgeBindings(s,record.id).length ? visibleExternalBridgeBindings(s,record.id).map(binding=><p key={binding.channelId}><Link target="_blank" rel="noopener noreferrer" to={`/chat?channel_id=${encodeURIComponent(binding.channelId)}&state.chat-management=${encodeURIComponent(JSON.stringify(binding.channelId))}&state.channel-management-tab=${encodeURIComponent(JSON.stringify("external"))}`}>{binding.channelName} · {binding.name}</Link> · {binding.status === "ACTIVE" ? "绑定启用（本地模拟）" : binding.status === "PAUSED" ? "已暂停" : "已撤销"}</p>) : <p className="muted">尚未绑定频道。可从共享频道设置中选择此连接器；连接成功不代表自动导入历史或开放内部会话。</p>}
                <Link className="btn" target="_blank" rel="noopener noreferrer" to='/chat?channel_id=external&state.chat-management=%22external%22&state.channel-management-tab=%22external%22'>选择外部共享频道并配置</Link>
              </Section>}
              {kind === "connectors" && <Section title="权限与同步边界">
                <Notice tone="warn">
                  绑定仅覆盖「{record.scope}
                  」。不导出内部私信或全部历史；外部身份不自动取得审批权限。
                </Notice>
                <dl>
                  <dt>消息持久化</dt>
                  <dd>保留原始平台、频道、消息 ID 和同步方向，可追溯来源。</dd>
                  <dt>使用范围</dt>
                  <dd>只同步明确绑定的外部共享频道，内部会话与私聊保持隔离。</dd>
                  <dt>收发回执</dt>
                  <dd>区分已存、平台接收、送达与已读；按实际能力声明</dd>
                </dl>
              </Section>}
            {kind === "models" && (
              <Section title="下发与能力">
                <Notice>
                  节点下发结果仅为本地模拟，不代表已连接真实节点。只应用于后续执行，不覆盖个人全局配置。密钥使用引用，不录入原型。
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
                {visibleActivityEvents(s)
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
                {!visibleActivityEvents(s).some((e) => e.text.includes(record.name)) && (
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
                {kind === "knowledge" && record.kind === "记忆" && !record.revocation && <Button disabled={Boolean(memoryRevocationProblem(s, record))} title={memoryRevocationProblem(s, record) || "撤销后不可重新启用；原消息及授权历史保留"} icon="ShieldOff" onClick={() => {setAction({record, status:"已撤销", label:"删除 / 撤销记忆", tenantId:s.activeTenant});setReason("");}}>删除 / 撤销记忆</Button>}
                {(record.revocation ? [] : transitions[kind] || []).map(([status, baseLabel]) => { const simulateAgent = kind === "reports" && status === "已提交" && reportAuthor(s, record)?.kind === "Agent"; const reportAction = kind === "reports" && status === "草稿" && record.status === "已接受" ? "REOPEN" : undefined; const label = reportAction ? "撤销确认" : simulateAgent ? "模拟 Agent 上报" : kind === "knowledge" && status === "已禁用" && record.kind !== "记忆" ? "禁用资料" : baseLabel; return (
                  <Button
                    key={label}
                    disabled={Boolean(projectRecordWriteProblem(s,record)) || (kind === "approvals" ? Boolean(approvalDecisionProblem(s, record, status)) : kind === "reports" ? Boolean(reportTransitionProblem(s, record, status, { simulateAgent, action: reportAction })) : kind === "goals" ? Boolean(goalTransitionProblem(s, record, status)) : kind === "deliveries" && (s.role === "executor" || ["已接受", "部分接受", "已拒收"].includes(record.status)))}
                    title={kind === "approvals" ? approvalDecisionProblem(s, record, status) || "" : kind === "reports" ? reportTransitionProblem(s, record, status, { simulateAgent, action: reportAction }) || "" : kind === "goals" ? goalTransitionProblem(s, record, status) || "" : kind === "deliveries" && s.role === "executor" ? "当前模拟身份没有回执登记权限" : kind === "deliveries" && ["已接受", "部分接受", "已拒收"].includes(record.status) ? "此包已有固定回执；修订成果后须形成新版本交付包" : undefined}
                    onClick={() => {
                      setVerificationRunId("");
                      setAction({ record, status, label, simulateAgent, reportAction });
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
              {kind === "deliveries" && s.role === "executor" && <Notice>当前模拟身份没有回执登记权限；接受、部分接受和拒收均需有权身份登记。</Notice>}
              {kind === "deliveries" && ["已接受", "部分接受", "已拒收"].includes(record.status) && <Notice>此包已有固定回执，结果与拒收依据保留。需要调整时，请修订成果并形成新的固定版本交付包；不会改写原回执。</Notice>}
              <Notice>
                操作会保留主体、理由与历史。原型中的外部同步和运行结果均为模拟。
              </Notice>
            </Section>
          </aside>
        </div>
      ) : (
        <>
          <DataList rows={rows} columns={[
            {key:"name",label:meta[0],value:r=>r.name,exportValue:r=>r.name,render:r=><Link className="resource-card" to={`/${kind}/${r.id}`}><h2>{r.name}</h2></Link>},
            {key:"owner",label:"负责人",value:r=>r.owner||"",exportValue:r=>r.owner||""},
            {key:"status",label:"状态",value:r=>r.status,exportValue:r=>r.status,render:r=><Badge>{r.status}</Badge>},
            {key:"version",label:"版本 / 周期",value:r=>r.version||r.cycle||"",exportValue:r=>r.version||r.cycle||""},
          ]} viewId={`resources:${kind}`} scopeKey={`${tenantId}:${projectId}:${role}`} title={meta[0]}
            searchText={r=>`${r.name} ${r.owner} ${r.status} ${r.id}`} onRefresh={refreshList} offline={s.offline} editing={Boolean(editing||generation||action)} onExport={exportList}
            onCreate={!["deliveries","approvals"].includes(kind)?create:undefined} createLabel={kind==="reports"?"生成 / 打开周期报告":`新建${meta[2]}`}
            renderActions={r=><Link className="btn" to={`/${kind}/${r.id}`}>查看详情</Link>} />
          {["deliveries","approvals"].includes(kind)&&<p className="muted">由交付或源对象审批流程生成记录，不提供手动新增或导入历史。</p>}

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
              if (kind === "knowledge" && action.status === "已撤销") revokeKnowledgeMemory({tenantId:action.tenantId, id:action.record.id, revision:action.record.revision || 1, status:action.record.status, reason});
              else resourceCommand(
                kind,
                action.record.id,
                action.status,
                reason,
                action.label,
                kind === "deliveries" ? { version: action.record.version, items: receiptItems }
                  : kind === "defects" ? { runId: verificationRunId }
                    : ["reports", "goals", "knowledge", "approvals"].includes(kind) ? { revision: action.record.revision || 1, status: action.record.status, simulateAgent: Boolean(action.simulateAgent), action: action.reportAction } : undefined,
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
            {kind === "knowledge" && action.status === "已撤销" && <Notice tone="warn">撤销仅作用于此派生记忆，属于不可重新启用的终态；原讨论与授权历史继续保留。本地撤销演示；真实检索与执行影响服务尚未连接。</Notice>}
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
function ReportPlanSnapshot({ binding, editing = false, current = false, state, report }) {
  if (!binding) return <Notice tone="warn">{current ? "尚未采用本期计划固定基线；缺失或未确认按 N2 提示，不补造原承诺。" : "尚未采用真实下周期计划；当前报告不能证明下期承诺已关联。请在编辑中打开计划并采用固定版本。"}</Notice>;
  if (current) {
    const problem = reportCurrentPlanBindingProblem(state, report, binding);
    if (problem) return <Notice tone="warn">{problem}</Notice>;
  }
  try { reportNextPlanItems(binding); }
  catch (error) { return <Notice tone="warn">{error.message}</Notice>; }
  const snapshot = binding.snapshot;
  const available = (state?.resources.plans || []).some(plan => plan.id === binding.planId && planCanRead(state, plan));
  return <div {...(current ? { "data-report-current-plan": binding.planId } : { "data-report-next-plan": binding.planId })}>
    <p>{editing || !available ? <span translate="no">{binding.planId}</span> : <Link translate="no" to={`/plans/${binding.planId}`}>{binding.planId}</Link>} · 固定修订 {binding.revision} · {binding.status}</p>
    <p className="muted">{snapshot.periodStart} — {snapshot.periodEnd} · {snapshot.timezone}</p>
    {snapshot.items.length ? <ul>{snapshot.items.map(item => <li key={item.id} style={{ contentVisibility: "auto", containIntrinsicSize: "auto 32px" }}>{item.title} · 目标 <span translate="no">{item.goalId || "未关联"}</span>{item.krId ? <span translate="no"> · KR {item.krId}</span> : null}{item.dueDate ? ` · ${item.dueDate}` : ""}</li>)}</ul>
      : <Notice tone="warn">{current ? "此固定版本没有计划项；不据此推断工作完成或目标达成。" : "此版本尚无计划项，不能作为有效下期承诺通过核对。"}</Notice>}
    {current && <Notice tone={binding.confirmed ? undefined : "warn"}>{binding.confirmed ? "采用原 Human 已确认基线；后续待确认草稿不替换原承诺。" : "采用未确认草稿；计划承诺与完成比例仍未确认。"}</Notice>}
    <p className="muted">本区展示报告采用的固定计划内容；{available ? `${editing ? "上方编辑按钮" : "链接"}打开当前计划。` : "当前计划来源不可用，固定引用保留。"}报告上报和确认均不批准计划。</p>
  </div>;
}
function ReportCurrentPlanEditor({ state, form, setForm, onNavigate }) {
  const [error, setError] = useState("");
  let plan, problem;
  try { plan = reportCurrentPlan(state, form); } catch (issue) { problem = issue.message; }
  return <Section title="本期计划固定引用">
    <p className="muted">复用本期真实成员计划，采用已确认基线；没有确认基线时明确保留未确认状态。</p>
    {problem ? <Notice tone="warn">{problem}</Notice> : plan ? <div className="actions">
      <Link className="btn" to={`/plans/${plan.id}`} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); onNavigate(() => { location.hash = `/plans/${plan.id}`; }); }}>查看 / 编辑真实本期计划</Link>
      <Button onClick={() => { try { const binding = reportCurrentPlanBinding(state, form, plan.id, plan.revision); setForm(previous => ({ ...previous, currentPlanBinding: binding })); setError(""); } catch (issue) { setError(issue.message); } }}>采用本期计划基线</Button>
    </div> : <Notice tone="warn">本期没有真实计划，不能从报告补造历史承诺。请在我的计划中核对对应周期。</Notice>}
    {error && <div role="alert"><Notice tone="danger">{error}</Notice></div>}
    <ReportPlanSnapshot binding={form.currentPlanBinding} current editing state={state} report={form} />
  </Section>;
}
function ReportNextPlanEditor({ state, form, setForm, onNavigate }) {
  const [error, setError] = useState("");
  let plan, descriptor, problem;
  try { descriptor = reportNextPlanDescriptor(state, form); plan = reportNextPlan(state, form); } catch (issue) { problem = issue.message; }
  const openPlan = () => onNavigate(() => { try {
    const planId = openReportNextPlan(form.id, form.revision || 1, form.authorKind === "Agent" || form.subjectKey.startsWith("agent:"));
    location.hash = `/plans/${planId}`;
  } catch (issue) { setError(issue.message); } });
  return <Section title="下周期计划">
    {problem ? <Notice tone="warn">{problem}。成员计划绑定已提供，汇总视图尚未实现。</Notice> : <>
      <p className="muted">{descriptor.periodStart} — {descriptor.periodEnd} · 复用真实成员计划，不另建报告内清单。</p>
      {form.subjectKey.startsWith("agent:") && <Notice>负责人类操作本地 Agent 模拟计划，不代表真实 Agent 已生成或提交。</Notice>}
      <div className="actions">
        {plan ? <Link className="btn" to={`/plans/${plan.id}`} onClick={event => { if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return; event.preventDefault(); openPlan(); }}>编辑真实下期计划</Link>
          : <Button onClick={openPlan}>创建并编辑下期计划</Button>}
        {plan && <Button onClick={() => { try {
          const binding = reportNextPlanBinding(state, form, plan.id, plan.revision);
          setForm(current => ({ ...current, nextPlanBinding: binding, ...(current.content ? { content: { ...current.content, next_plan: reportNextPlanItems(binding) } } : {}) })); setError("");
        } catch (issue) { setError(issue.message); } }}>采用下期计划当前版本</Button>}
      </div>
      {error && <div role="alert"><Notice tone="danger">{error}</Notice></div>}
      <ReportPlanSnapshot binding={form.nextPlanBinding} editing state={state} />
    </>}
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
        {report.submission.content.nextPlanBinding && <ReportPlanSnapshot binding={report.submission.content.nextPlanBinding} state={state} />}
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
    <Section title="本期计划固定引用"><ReportPlanSnapshot binding={report.submission ? report.submission.content.currentPlanBinding : report.currentPlanBinding} current state={state} report={report.submission ? { ...report, ...report.submission.content } : report} /></Section>
    <Section title="下周期计划固定引用"><ReportPlanSnapshot binding={report.submission?.content.nextPlanBinding || report.nextPlanBinding} state={state} /></Section>
    <Section title="报告四章节"><ReportContentReader content={report.submission?.content.content || report.content} report={report} state={state} /></Section>
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
    ...(kind === "repositories" ? { projectId: value.projectId } : {}),
    fields: Object.fromEntries(resourceFields[kind].filter(field => !(kind === "reports" && field === "owner")).map(key => [key, String(value[key] ?? "")])),
    ...(kind === "goals" ? { ownerId: value.ownerId, reviewerId: value.reviewerId } : {}),
    references: [...(Array.isArray(value.references) ? value.references : [])].sort((a, b) => `${a?.kind}:${a?.id}`.localeCompare(`${b?.kind}:${b?.id}`)),
    requirementRefs: [...(value.requirementRefs || [])].sort(),
    ...(kind === "reports" ? { nextPlanBinding: value.nextPlanBinding || null, currentPlanBinding: value.currentPlanBinding || null, regenerationDraft: value.regenerationDraft || null, content: value.content || null, migrateLegacyContent: value.migrateLegacyContent === true, reviewedDetailKeys: value.reviewedDetailKeys || [], reviewedRetrospective: value.reviewedRetrospective || false } : {}),
  }));
  const fields = [...resourceFields[kind].filter((field) => !(kind === "reports" && field === "owner")), ...(kind === "repositories" ? ["projectId"] : [])];
  const references = Array.isArray(form.references) ? form.references : [];
  const options = kind === "reports" ? [
    ...state.work.map((w) => ({ kind: "work", id: w.id, name: w.title, version: w.version })),
    ...state.resources.worklogs.map((w) => ({ kind: "worklog", id: w.id, name: w.name, version: w.revision || 1 })),
    ...state.resources.reports.filter((r) => r.id !== form.id && !reportChildProblem(state, form, r)).map((r) => ({ kind: "report", id: r.id, name: r.name, version: r.revision || 1 })),
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
      {({ requestClose }) => <ActionForm
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
          <ReportCurrentPlanEditor state={state} form={form} setForm={setForm} onNavigate={navigate => requestClose(navigate)} />
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
              label={kind === "repositories" && k === "projectId" ? "所属项目" : kind === "goals" && k === "target" ? "目标值" : kind === "reports" && k === "summary" ? "人工校准正文" : k === "subjectKey" ? "报告归属" : k === "cadence" ? "报告周期" : fieldNames[k] || k}
              value={String(form[k] ?? "")}
              onChange={(v) => setForm(current => {
                const next = { ...current, [k]: v };
                if (kind === "tests" && ["work", "version"].includes(k)) {
                  if (!defectRecords(state).some(item => item.id === next.defect && item.work === next.work && item.version === next.version)) next.defect = "";
                  if (k === "work") next.requirementRefs = (current.requirementRefs || []).filter(ref => state.work.find(work => work.id === v)?.requirements.includes(ref));
                }
                return next;
              })}
              disabled={kind === "repositories" && k === "projectId" && Boolean(record.projectId)}
              options={kind === "repositories" && k === "projectId" ? [{value:"",label:"请选择当前租户项目"}, ...activeProjects(state).map(project => ({value:project.id,label:project.name}))] : kind === "schedules" && k === "subjectKey" ? [{ value: "", label: "非报告任务 / 未配置" }, ...reportSubjects(state).map((subject) => ({ value: subject.key, label: `${subject.name} · ${subject.kind}` }))]
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
              required={["name", "owner"].includes(k) || (kind === "repositories" && k === "projectId")}
            />
          ))}
        </div>
        {kind === "reports" && <>
          <ReportNextPlanEditor state={state} form={form} setForm={setForm} onNavigate={navigate => requestClose(navigate)} />
          <ReportContentEditor state={state} form={form} setForm={setForm} />
          <Section title="选择事实引用">
            <p className="muted">下级报告仅显示归属与周期匹配、已有固定提交的来源。跨月按事实日期汇总，重复来源只计一次；来源变化需重新核对版本。</p>
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
      </ActionForm>}
    </Modal>
  );
}
export function Team({ toast, initialTab = "members", invitationId }) {
  const s = useStore();
  const org = s.organization, people = org.members.filter((person) => person.kind === "Human"), humans = people;
  const canEdit = s.role === "owner" && !s.offline;
  const [tab, setTab] = useViewState("team-tab", initialTab);
  const [nodeId, setNodeId] = useViewState("team-node", "");
  const [selection, setSelection] = useViewState("team-selection", invitationId ? { kind: "invitations", id: invitationId } : null);
  const [query, setQuery] = useState("");
  const [mobilePane, setMobilePane] = useViewState("team-pane", invitationId ? "detail" : "list");
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
  const newInvitation = () => edit("invitation", { name: "", email: "", ownerId: humans[0]?.id || "", projectId: activeProjects(s)[0]?.id || "",
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
    <PageHead title={tab === "members" ? "组织与成员" : "邀请记录"} subtitle={tab === "members" ? "已加入 Human 的组织归属与项目职责；Agent 和任务执行编组分别管理。" : "跟踪邀请的范围、角色、有效期与状态；待接受邀请不属于已加入成员。"}>
      {tab === "members" && <Button disabled={!canEdit} onClick={() => edit("members", { name: "" })} icon="Plus">添加成员</Button>}
      <Button variant="primary" disabled={!canEdit} onClick={newInvitation} icon="UserPlus">邀请成员</Button>
    </PageHead>
    <div className="org-overview">{tab === "members" ? <><span><strong>{people.length}</strong> 位成员 <small>Human 组织名册</small></span>
      <span><strong>{org.nodes.length}</strong> 个组织节点</span><span><strong>{s.resources.invitations.filter((item) => item.status === "待接受").length}</strong> 个待接受邀请</span></> : <>{["待接受","已接受","已撤销"].map(status=><span key={status}><strong>{s.resources.invitations.filter(i=>i.status===status).length}</strong> {status}<small>邀请生命周期</small></span>)}</>}</div>
    <nav className="org-tabs" aria-label="成员管理内容">{[["members", "成员目录"], ["invitations", "邀请记录"]].map(([value, label]) =>
      <button key={value} className={tab === value ? "active" : ""} aria-current={tab === value ? "page" : undefined} onClick={() => switchTab(value)}>{label}</button>)}</nav>
    <nav className="org-mobile-panes" aria-label="组织页面分栏">{[...(tab === "members" ? [["tree", "组织"]] : []), ["list", "列表"], ["detail", "详情"]].map(([value, label]) =>
      <button key={value} aria-pressed={mobilePane === value} onClick={() => setMobilePane(value)}>{label}</button>)}</nav>
    <div className={`org-workspace ${tab === "invitations" ? "org-invitations-workspace" : ""} org-show-${mobilePane}`}>
      {tab === "members" && <aside className="org-tree" aria-label="组织架构"><div className="org-pane-heading"><h2>组织架构</h2><IconButton name="Plus" label="新增部门 / 团队" disabled={!canEdit || !humans.length} onClick={() => editNode()} /></div>
        <button className={`org-tree-row ${!nodeId ? "active" : ""}`} onClick={() => { setNodeId(""); setSelection(null); setEditor(null); setMobilePane("list"); }}><Icon name="UsersRound" size={16} /><span>全部成员</span><small>{people.length}</small></button>
        <div className="org-tree-branches">{rows.map(({ node, depth }) => <button key={node.id} className={`org-tree-row ${nodeId === node.id ? "active" : ""}`}
          style={{ "--org-depth": Math.min(depth, 4) }} onClick={() => { setNodeId(node.id); choose("node", node); setMobilePane("list"); }}>
          <Icon name={depth ? "Folders" : "Building2"} size={16} /><span>{node.name}</span><small title="直属 Human 成员">直属 {node.memberIds.filter(id => people.some(p => p.id === id)).length}</small>
        </button>)}</div>
        {!rows.length && <p className="org-empty-note">先添加 Human 成员，再建立组织。</p>}
        <p className="org-tree-foot"><Icon name="LockKeyhole" size={14} />组织层级不开放私人会话。</p>
      </aside>}
      <section className="org-directory" aria-label={tab === "members" ? "成员目录" : "邀请记录"}>
        <div className="org-pane-heading"><div><h2>{tab === "members" ? selectedNode?.name || "全部成员" : "邀请记录"}</h2><small>{listRows.length} 条记录</small></div>
          {tab === "members" && selectedNode && <IconButton name="Settings2" label={`编辑 / 移动 ${selectedNode.name}`} disabled={!canEdit} onClick={() => editNode(selectedNode)} />}</div>
        <div className="org-search"><Icon name="Search" size={16} /><input aria-label="搜索成员或记录" placeholder={tab === "members" ? "搜索成员姓名…" : "搜索名称…"} value={query} onChange={(e) => setQuery(e.target.value)} /></div>

        <div className="org-directory-list">{listRows.map((item) => tab === "invitations" ? <button key={item.id} className={`org-person-row org-invite-row ${selected?.id===item.id ? "active" : ""}`} onClick={()=>choose("invitations",item)}>
          <Icon name="Mail"/><span className="org-row-text"><strong>{item.name || item.title}</strong><span>{item.email || "未记录邮箱"}</span><small>{s.projects.find(p=>p.id===item.projectId)?.name || item.scope} · {item.role} · 邀请人 {item.owner}<br/>有效至 {item.expires || "未记录"}</small></span><Badge>{item.status === "待接受" && item.expires < new Date().toISOString().slice(0,10) ? "已过期" : item.status}</Badge><Icon name="ChevronRight" size={14}/>
        </button> : <button key={item.id} className={`org-person-row ${selected?.id === item.id && selectedKind !== "node" ? "active" : ""}`} onClick={() => choose(tab, item)}>
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
            <DirectMessageButton key={selected.id} peerId={selected.id} />
            <HumanLifecycle key={`${s.activeTenant}:${selected.id}`} memberId={selected.id}/>
            <div className="org-detail-section"><h3>正式组织归属</h3><p>{memberships(selected).map((node) => node.name).join("、") || "尚未登记"}</p></div>
            <div className="org-detail-section"><div className="org-section-title"><h3>项目角色</h3><IconButton name="Plus" label="添加项目角色" disabled={!canEdit || !activeProjects(s).length} onClick={() => edit("projectRoles", { memberId: selected.id, projectId: activeProjects(s)[0]?.id || "", role: "项目成员" })} /></div>
              {org.projectRoles.filter((role) => role.memberId === selected.id).map((role) => <div className="org-project-role" key={role.id}><strong>{s.projects.find((project) => project.id === role.projectId)?.name || "项目不可用"}</strong><span>{role.role}</span><IconButton name="Pencil" label={`编辑项目角色 ${role.id}`} disabled={!canEdit || !!projectRecordWriteProblem(s,role)} onClick={() => edit("projectRoles", role)} />{history(role.history)}</div>)}
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
          <Field label="所属项目" value={editor.form.projectId} options={activeProjects(s).map((project) => ({ value: project.id, label: project.name }))} onChange={(projectId) => change({ projectId })} required />
          <Field label="项目职责" value={editor.form.role} options={["项目成员", "项目负责人", "评审者"]} onChange={(role) => change({ role })} required />
          <Notice>只修改项目协作角色，不移动成员的正式组织归属。</Notice>
        </>}
          {editor.kind === "invitation" && <>
            <Field label="姓名" value={editor.form.name} onChange={(name) => change({ name })} required />
            <Field label="电子邮件" type="email" value={editor.form.email} onChange={(email) => change({ email })} required />
            <Field label="邀请人" value={editor.form.ownerId} options={[{ value: "", label: "请选择" }, ...humanOptions]} onChange={(ownerId) => change({ ownerId })} required />
            <Field label="邀请项目" value={editor.form.projectId} options={[{ value: "", label: "请选择当前租户项目" }, ...activeProjects(s).map((project) => ({ value: project.id, label: project.name }))]} onChange={(projectId) => change({ projectId })} required />
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
function WorkflowTemplates({ record, toast, params }) {
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
        <Flow key={record.id} templateId={record.id} toast={toast} params={params} />
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
        {a && <DirectMessageButton key={a.id} peerId={a.id} />}
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
          <AgentChannelGrants key={`${s.activeTenant}:${a.id}`} agentId={a.id} toast={toast} />
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
  const [resourceProject,setResourceProject] = useViewState("resourceProject",activeProjects(s).some(project=>project.id===s.project) ? s.project : "");
  const leaseScope={tenantId:s.activeTenant,projectId:resourceProject};
  const [scopeRequest,setScopeRequest]=useState(null);
  const [scopeChoice,setScopeChoice]=useState("");
  const findLease = kind => [...(s.projectLeases || [])].reverse().find(lease=>lease.tenantId===s.activeTenant && lease.nodeId===nodeId && lease.projectId===leaseScope.projectId && lease.kind===kind);
  const terminalLease=findLease("terminal"), previewLease=findLease("preview");
  const control=terminalLease?.state === "ACTIVE", preview=previewLease?.state === "ACTIVE";
  const resourceAction = (kind,lease) => {
    try {
      if (lease && lease.state !== "RELEASED") { const result=releaseProjectLease({tenantId:leaseScope.tenantId,leaseId:lease.id,revision:lease.revision,reason:"用户明确释放节点本地模拟资源"});toast(result === "RELEASED" ? "本地模拟释放已记录" : "节点未确认，释放仍待处理",result !== "RELEASED"); }
      else {if(!leaseScope.projectId){setScopeChoice("");setScopeRequest(kind);return;}requestProjectLease({tenantId:leaseScope.tenantId,projectId:leaseScope.projectId,nodeId,kind});toast("已记录明确项目的本地模拟租约；不代表真实服务已启动");}
    } catch(error) {toast(error.message,true);}
  };
  const [file, setFile] = useState("README.md");
  const tab = params.get("tab") || "status";
  return (
    <>
      <PageHead
        eyebrow="团队 / 执行资源"
        title={node ? node.name : "工作在任何地方，都能继续"}
        subtitle={node ? "查看当前设备的状态、工作区、终端和授权预览。" : "接入和管理可执行任务的设备。"}
      >
        {node ? <Link className="btn" to="/machines">返回节点列表</Link> : <Button variant="primary" icon="Plus" onClick={() => setInstall(true)}>接入节点</Button>}
      </PageHead>
      {scopeRequest && <Modal title={scopeRequest === "switch" ? "更换访问上下文" : "确认工作区访问范围"} onClose={()=>setScopeRequest(null)}>
        <Notice>节点可以承载多个项目。当前从租户节点管理进入，没有唯一任务上下文；请选择本次访问的项目，用于权限、审计及归档时释放资源。这不会为节点创建子节点或绑定永久所属项目。当前终端与文件均为原型示例。</Notice>
        <Field label="本次访问项目" value={scopeChoice} options={[{value:"",label:"选择访问范围"},...activeProjects(s).map(project=>({value:project.id,label:project.name}))]} onChange={setScopeChoice} required />
        <Button variant="primary" disabled={!scopeChoice} onClick={()=>{try {if(scopeRequest!=="switch")requestProjectLease({tenantId:s.activeTenant,projectId:scopeChoice,nodeId,kind:scopeRequest});setResourceProject(scopeChoice);setScopeRequest(null);setOutput(["[模拟] 访问上下文已选择；未连接真实工作区或 Shell。"]);setCommand("");toast(scopeRequest==="switch" ? "访问上下文已切换；旧租约仍保留，可切回释放" : "已记录本地模拟访问，不代表真实服务启动");}catch(error){toast(error.message,true);}}}>{scopeRequest === "switch" ? "切换访问上下文" : "确认并申请模拟访问"}</Button>
      </Modal>}
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
                    ? "# 工作区文件示例\n\n仅用于演示文件浏览布局。\n未连接当前节点文件服务，不代表任何任务的实际成果。"
                    : file.endsWith("json")
                      ? JSON.stringify(
                          {
                            node: nodeId,
                            workspace: "本地示例",
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
                      nodeId,
                      simulated: true,
                    })
                  }
                >
                  下载引用清单
                </Button>
              </div>
            </div>
          )}
          {["terminal","preview"].includes(tab) && leaseScope.projectId && <p className="muted">访问上下文：{s.projects.find(project=>project.id===leaseScope.projectId)?.name || "原项目已不可用"} · 申请与释放按此项目留存记录。<Button onClick={()=>{setScopeChoice(leaseScope.projectId);setScopeRequest("switch");}}>更换访问上下文</Button></p>}
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
                    if (!terminalLease || leaseScope.tenantId !== s.activeTenant || node?.status !== "在线" || projectIsArchived(s,terminalLease.projectId) || s.offline) {toast("资源已停止或项目只读，不能执行新模拟命令。",true);return;}
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
                    resourceAction("terminal",terminalLease);
                  } catch (e) {
                    toast(e.message, true);
                  }
                }}
              >
                {terminalLease && terminalLease.state !== "RELEASED" ? "释放控制" : "申请 Human 控制"}
              </Button>
            </Section>
          )}
          {tab === "preview" && (
            <Section title="当前预览实例">
              <div className="preview-record">
                <Icon name="Globe2" size={32} />
                <div className="grow">
                  <h2>预览隧道示例 · Web / API</h2>
                  <p>随机域名、身份验证、租约与实际资源清理</p>
                  <code>preview-8f3a.example.invalid</code>
                </div>
                <Badge>{preview ? "本地模拟租约有效" : previewLease?.state === "RELEASED" ? "已记录模拟释放" : "旧示例 · 归属与租约未核实"}</Badge>
              </div>
              <dl>
                <dt>归属</dt>
                <dd>当前未登记任务关联，不能打开其他任务的成果</dd>
                <dt>权限</dt>
                <dd>项目成员 · 只读预览</dd>
                <dt>租约</dt>
                <dd>
                  {preview ? "明确项目的本地模拟记录，未启动真实隧道" : previewLease?.state === "RELEASED" ? "本地释放已记录，不代表远程清理完成" : "没有可核对的项目租约，不补造旧释放记录"}
                </dd>
                <dt>清理范围</dt>
                <dd>代理、凭据、域名映射、节点监听与子进程</dd>
              </dl>
              <div className="actions">
                <Button
                  onClick={() => {
                    resourceAction("preview",previewLease);
                  }}
                >
                  {previewLease && previewLease.state !== "RELEASED" ? "撤权并释放" : "申请本地模拟预览"}
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
export function Cost(props) { return <BudgetPage {...props} />; }
export function Audit() {
  const s = useStore();
  const [selectedId, setSelectedId] = useViewState("audit.record", "");
  const [actor, setActor] = useViewState("audit.actor", "");
  const [, redraw] = useState(0);
  const tenantId=s.activeTenant, projectId=s.project, role=s.role;
  const recheck = useCallback(() => {
    const current=read();
    if(current.activeTenant!==tenantId || current.project!==projectId || current.role!==role) throw Error("审计范围已变化，请返回当前列表后重试。");
    return current;
  }, [tenantId,projectId,role]);
  const refresh = useCallback(() => { recheck(); redraw(value=>value+1); }, [recheck]);
  const authorizeExport = useCallback(payload => payload.records.every(row => visibleActivityEvents(recheck()).some(event => event.id===row.id && event.text===row.text && event.actor===row.actor && event.time===row.time && event.target===row.target)), [recheck]);
  const events = visibleActivityEvents(s);
  const selected = events.find(event => event.id === selectedId);
  const columns = [
    { key: "id", label: "记录编号", value: event => event.id },
    { key: "text", label: "已记录的动作", exportValue: event => event.text, value: event => event.text },
    { key: "actor", label: "主体", exportValue: event => event.actor, value: event => event.actor },
    { key: "time", label: "记录时间", exportValue: event => event.time, value: event => event.time, render: event => formatTimestamp(event.time) },
    { key: "target", label: "关联位置", exportValue: event => event.target, value: event => event.target },
  ];
  const targetLink = event => {
    if (!event || typeof event.target !== "string" || !event.target.startsWith("/") || event.target.startsWith("//")) return null;
    const [path, query = ""] = event.target.split("?");
    const params = new URLSearchParams(query);
    params.set("tenant", s.activeTenant);
    if (!params.has("scope")) params.set("scope", /^\/p\/([^/]+)/.exec(path)?.[1] || s.project);
    return `${path}?${params}`;
  };
  return <>
    <PageHead title="决定有依据，协作有来路" subtitle="原地查看每条已记录活动；从明确的关联位置追溯讨论、执行与批准。" />
    <Section title="不可覆盖的活动历史">
      <DataList rows={events} columns={columns} viewId="audit" scopeKey={`${s.activeTenant}:${s.project}:${s.role}`} title="活动与审计记录" defaultPageSize={50}
        searchText={event => `${event.text} ${event.actor} ${event.id} ${event.target}`}
        filterRow={event => !actor || event.actor === actor}
        filters={<Field label="主体" value={actor} onChange={setActor} options={[{value:"",label:"全部主体"}, ...Array.from(new Set(events.map(event => event.actor))).map(value => ({value,label:value}))]} />}
        onClearFilters={() => setActor("")} onRefresh={refresh} offline={s.offline}
        onExport={authorizeExport}
        renderActions={event => <Button onClick={() => setSelectedId(event.id)}>查看记录</Button>} />
      <p className="muted">活动由业务操作自动记录；不允许手动新增、导入、编辑或删除历史。刷新仅重新读取原型本地状态。</p>
    </Section>
    {selectedId && <Modal title="审计记录详情" onClose={() => setSelectedId("")} headerActions={selected && targetLink(selected) ? <Link className="btn" to={targetLink(selected)} target="_blank" rel="noopener noreferrer">新标签页打开关联位置</Link> : null}>
      {selected ? <>
        <dl className="record-fields">
          <dt>记录编号</dt><dd>{selected.id}</dd><dt>主体</dt><dd>{selected.actor || "未记录"}</dd>
          <dt>记录时间</dt><dd>{selected.time ? formatTimestamp(selected.time) : "未记录"}{selected.time && /^\d{2}:\d{2}$/.test(selected.time) && " · 历史样例未记录日期与时区"}</dd>
          <dt>已记录的动作</dt><dd>{selected.text || "未记录"}</dd>
          <dt>关联位置</dt><dd style={{overflowWrap:"anywhere"}}>{selected.target || "未记录"}</dd>
          <dt>理由</dt><dd>{selected.reason || "此记录未保存独立理由"}</dd>
        </dl>
        <Notice>此原型记录仅包含上面已保存的字段，未提供完整的历史快照或证据包。关联位置展示对象当前状态，不能据此证明当时的版本、验收依据或授权。真实防篡改审计服务待实现。</Notice>
      </> : <Empty title="记录不存在或无权查看" text="未展示原始存储中的受限内容；关闭后返回当前授权列表。" />}
    </Modal>}
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
        <Section title="外观">
          <Field
            label="主题"
            options={Object.entries(themeNames).map(([value,label])=>({value,label}))}
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
          <p className="muted">主题和深色模式立即应用。</p>
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

function RepositoryPage({ recordId, toast, params = new URLSearchParams() }) {
  const s = useStore();
  const [editing, setEditing] = useState(null), [action, setAction] = useState(null), [reason, setReason] = useState("");
  const archive = ["active", "archived", "all"].includes(params.get("archive")) ? params.get("archive") : "active";
  const tenantId=s.activeTenant, projectId=s.project, role=s.role;
  const scopeKey=`${tenantId}:${projectId}:${role}:${archive}`;
  const [list,setList]=useViewState(`repositories.list.${tenantId}:${projectId}:${role}`,{query:params.get("q") || ""});
  const [,redraw]=useState(0);
  const scopedRecords=useCallback(current=>repositoryRecords(current,{archive}).filter(item=>current.project==="all"||item.projectId===current.project),[archive]);
  const rows = scopedRecords(s);
  const columns=React.useMemo(()=>[
    ...[["name","仓库"],["projectId","项目编号"],["owner","负责人"],["provider","托管平台"],["branch","分支"]].map(([key,label])=>({key,label,value:item=>item[key]||"",exportValue:item=>item[key]||""})),
    {key:"status",label:"状态",value:item=>item.archivedAt?"已归档":item.status,exportValue:item=>item.archivedAt?"已归档":item.status,render:item=><Badge>{item.archivedAt?"已归档":item.status}</Badge>},
    {key:"revision",label:"配置修订",value:item=>item.revision||1,exportValue:item=>item.revision||1},
    {key:"version",label:"原版本",value:item=>item.version||null,exportValue:item=>item.version||null},
  ],[]);
  const recheck=useCallback(()=>{const current=read();if(current.activeTenant!==tenantId||current.project!==projectId||current.role!==role||!["owner","executor"].includes(current.role))throw Error("仓库授权范围已变化，请重新打开列表。");return scopedRecords(current);},[tenantId,projectId,role,scopedRecords]);
  const refresh=useCallback(()=>{recheck();redraw(value=>value+1);},[recheck]);
  const authorizeExport=useCallback(payload=>{const current=recheck();if(!payload.records.every(item=>{const record=current.find(record=>record.id===item.id);return record&&columns.every(column=>JSON.stringify(item[column.key])===JSON.stringify(column.exportValue(record)??null));}))throw Error("仓库记录已变化，请刷新后重新导出。");return true;},[recheck,columns]);
  const record = repositoryRecords(s, { archive: "all" }).find(item => item.id === recordId);
  if (!["owner", "executor"].includes(s.role)) return <Empty title="当前视角无权访问仓库" />;
  if (recordId && !record) return <Empty title="仓库不存在或无权访问" />;
  const setFilter = (key, value) => { const next = new URLSearchParams(params); value ? next.set(key, value) : next.delete(key); location.hash = `/repositories?${next}`; };
  const canCreate = !repositoryWriteProblem(s, {}, false);
  const editProblem = record ? repositoryWriteProblem(s, record, false) : "";
  const actionProblem = record ? repositoryWriteProblem(s, record) : "";
  const create = () => setEditing({ name: "", owner: "林予", provider: "", branch: "main", scope: "", projectId: activeProjects(s).some(project => project.id === s.project) ? s.project : "", tenantId: s.activeTenant });
  const begin = (status, label) => { setAction({ id: record.id, name: record.name, status, label, tenantId: s.activeTenant, revision: record.revision || 1 }); setReason(""); };
  const text = value => typeof value === "string" && value ? value : "未记录";
  return <>
    <PageHead title={record ? record.name : "源码连接"} subtitle="接入已有代码来源，监控变更并为工作流提供输入；这里只维护连接配置。">
      {record ? <Link className="btn" to="/repositories">返回仓库列表</Link> : <Button variant="primary" disabled={!canCreate} onClick={create}>接入已有仓库</Button>}
      {record&&<Button icon="Download" onClick={() => exportData("repositories", record)}>导出</Button>}
      {record && !record.archivedAt && <Button icon="Pencil" disabled={Boolean(editProblem)} title={editProblem} onClick={() => setEditing({ ...record, tenantId: s.activeTenant, revision: record.revision || 1 })}>编辑连接配置</Button>}
    </PageHead>
    {record ? <>
      <Section title={record.archivedAt ? "已归档 · 历史只读" : "仓库配置"} extra={<Badge>{record.archivedAt ? "已归档" : record.status}</Badge>}>
        <dl className="record-fields">
          <dt>仓库 ID</dt><dd>{record.id}</dd>
          <dt>所属项目</dt><dd>{record.projectId ? <Link to={`/p/${record.projectId}`}>{s.projects.find(project => project.id === record.projectId)?.name || "项目不可用"} · {record.projectId}</Link> : "未记录；旧范围说明不能证明项目归属"}</dd>
          {[['owner','负责人'],['provider','托管平台'],['branch','分支'],['scope','范围说明'],['commit','最近提交'],['event','最近事件'],['sync','最近同步记录'],['version','原版本标识']].map(([key,label]) => <React.Fragment key={key}><dt>{label}</dt><dd style={{overflowWrap:"anywhere"}}>{text(record[key])}</dd></React.Fragment>)}
          <dt>配置修订</dt><dd>{record.revision || 1}</dd>
        </dl>
        {record.projectBinding && <p className="muted">项目绑定由 {text(record.projectBinding.actorId)} 于 {formatTimestamp(record.projectBinding.time)} 在本地明确记录；不代表旧历史已具有此归属。</p>}
        {record.archivedAt ? <>
          <Notice>归档后不可修改配置或同步，原项目、提交和执行历史保留。此操作不会删除远程仓库、提交或工作目录，也不会取消执行。</Notice>
          <dl className="record-fields"><dt>归档人</dt><dd>{text(record.archivedBy)} · Human</dd><dt>归档时间</dt><dd>{formatTimestamp(record.archivedAt)}</dd><dt>归档理由</dt><dd style={{overflowWrap:"anywhere"}}>{text(record.archiveReason)}</dd><dt>记录来源</dt><dd>{record.archiveSource === "LOCAL_SIMULATION" ? "本地模拟" : "未记录"}</dd></dl>
        </> : <>
          {actionProblem && <Notice tone="warn">{actionProblem}</Notice>}
          <div className="toolbar">{[["已连接","模拟增量同步"],["已暂停","暂停同步"],["已归档","归档仓库"]].map(([status,label]) => <Button key={label} disabled={Boolean(actionProblem)} title={actionProblem} onClick={() => begin(status,label)}>{label}</Button>)}</div>
          <Notice>同步与暂停仅更新本地模拟记录；不代表远程仓库或工作流状态已改变。</Notice>
        </>}
      </Section>
      <Section title="变更历史">
        {(record.history || []).length ? record.history.map((event,index) => <details key={index}><summary>{event.action || "历史配置"} · {event.time ? formatTimestamp(event.time) : "时间未记录"}</summary>
          <p>{event.actorId || event.actor || "主体未记录"}{event.reason ? ` · ${event.reason}` : ""}</p>
          <dl className="record-fields">{[['id','原记录'],['name','原名称'],['projectId','原项目 ID'],['owner','原负责人'],['provider','原托管平台'],['scope','原范围说明'],['branch','原分支'],['commit','原提交'],['event','原事件'],['sync','原同步记录'],['version','原版本'],['status','原同步状态']].map(([key,label]) => <React.Fragment key={key}><dt>{label}</dt><dd>{text((event.snapshot || event)[key])}</dd></React.Fragment>)}</dl>
        </details>) : <p className="muted">尚无变更历史。</p>}
      </Section>
    </> : <>
      <DataList rows={rows} columns={columns} viewId="repositories" scopeKey={scopeKey} title="源码连接" defaultSort="name"
        queryState={list} onQueryStateChange={patch=>setList(previous=>({...previous,...patch}))}
        searchText={item=>`${item.id} ${item.name} ${item.owner||""} ${item.provider||""} ${item.branch||""} ${item.projectId||""}`}
        filters={<Field label="归档状态" value={archive} options={[{value:"active",label:"未归档"},{value:"archived",label:"已归档"},{value:"all",label:"全部"}]} onChange={value=>setFilter("archive",value)}/>}
        onClearFilters={()=>setFilter("archive","active")} onRefresh={refresh} offline={s.offline} editing={Boolean(editing||action)}
        onExport={authorizeExport} exportSource={`repositories:${scopeKey}`}
        renderActions={item=><Link className="btn" target="_blank" rel="noopener noreferrer" to={`/repositories/${encodeURIComponent(item.id)}?tenant=${encodeURIComponent(tenantId)}&scope=${encodeURIComponent(projectId)}`}>新标签页查看连接</Link>}
        emptyText="当前条件下没有仓库，可清空筛选或接入已有仓库。"/>
    </>}
    {editing && <ResourceEditor kind="repositories" record={editing} onClose={() => setEditing(null)} onSaved={() => { setEditing(null); toast("仓库配置已保存"); }} />}
    {action && <Modal title={action.label} draftValue={reason} onClose={() => setAction(null)}><ActionForm label={action.status === "已归档" ? "确认归档仓库" : "确认模拟操作"} onCancel={() => setAction(null)} onSubmit={({dismiss}) => {
      resourceCommand("repositories",action.id,action.status,reason,action.label,{tenantId:action.tenantId,revision:action.revision}); dismiss(); toast(action.status === "已归档" ? "仓库已归档，历史保留" : "本地模拟记录已更新");
    }}>
      <p>{action.name} · 修订 {action.revision}</p>
      <Notice>{action.status === "已归档" ? "归档后此仓库只读，并从默认列表移出。远程仓库、提交、工作目录和现有执行保持原状；不提供恢复操作。" : "此动作只记录本地模拟结果，不连接远程服务。"}</Notice>
      <Field label={action.status === "已归档" ? "归档理由" : "操作理由"} type="textarea" required value={reason} onChange={setReason} />
    </ActionForm></Modal>}
  </>;
}

export function ResourcePage(props) {
  if (props.kind === "knowledge") return <KnowledgeLibrary {...props} ResourceEditor={ResourceEditor} legacyPage={<GenericResourcePage {...props} />} />;
  if (props.kind === "repositories") return <RepositoryPage {...props} />;
  if (props.kind === "goals") return <GoalsPage {...props} legacyPage={<GenericResourcePage {...props} />} />;
  return <GenericResourcePage {...props} />;
}
