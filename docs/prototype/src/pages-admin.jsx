import React, { useState } from "react";
import { catalog, fieldNames } from "./data";
import {
  useStore,
  update,
  log,
  id,
  saveResource,
  resourceCommand,
  assertWrite,
  exportData,
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
export function ResourcePage({ kind, recordId, toast }) {
  const s = useStore();
  const [query, setQuery] = useState("");
  const [editing, setEditing] = useState(null);
  const [action, setAction] = useState(null);
  const [reason, setReason] = useState("");
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
  const create = () =>
    setEditing(
      Object.fromEntries(
        Object.entries(defaults)
          .filter(([k]) => k !== "id")
          .map(([k, v]) => [
            k,
            k === "name" ? "" : k === "status" ? "草稿" : v,
          ]),
      ),
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
            <Button icon="Pencil" onClick={() => setEditing({ ...record })}>
              编辑
            </Button>
          </>
        ) : (
          <Button variant="primary" icon="Plus" onClick={create}>
            新建{meta[2]}
          </Button>
        )}
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
                      !["id", "status", "name", "history", "revision"].includes(
                        k,
                      ),
                  )
                  .map(([k, v]) => (
                    <React.Fragment key={k}>
                      <dt>{fieldNames[k] || k}</dt>
                      <dd>
                        {typeof v === "object" ? JSON.stringify(v) : String(v)}
                      </dd>
                    </React.Fragment>
                  ))}
              </dl>
            </Section>
            {kind === "goals" && (
              <Section title="贡献工作与指标证据">
                <Progress value={Number(record.progress) || 0} />
                <p>
                  {record.metric}：基线 {record.baseline} → 当前 {record.actual}{" "}
                  → 目标 {record.target}
                </p>
                <Link className="btn" to="/work">
                  查看关联计划与工作
                </Link>
              </Section>
            )}
            {kind === "reports" && (
              <Section title="上报前核对">
                <ul className="check-list">
                  <li>
                    <Icon name="CheckCircle2" />
                    已引用真实工作与接受记录
                  </li>
                  <li>
                    <Icon name="AlertCircle" />
                    风险与未完成项随汇总保留
                  </li>
                  <li>
                    <Icon name="UserCheck" />
                    Human 报告必须本人确认
                  </li>
                </ul>
              </Section>
            )}
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
                        {e.time} · {e.actor}
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
                {(transitions[kind] || []).map(([status, label]) => (
                  <Button
                    key={label}
                    onClick={() => {
                      setAction({ record, status, label });
                      setReason("");
                    }}
                    icon={label.includes("撤销") ? "ShieldOff" : "ArrowRight"}
                  >
                    {label}
                  </Button>
                ))}
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
                aria-label={`搜索${meta[0]}`}
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
              <Button onClick={query ? () => setQuery("") : create}>
                {query ? "清空筛选" : `创建${meta[2]}`}
              </Button>
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
                    {kind === "goals" && <Progress value={r.progress || 0} />}
                  </div>
                  <Icon name="ArrowUpRight" size={16} />
                </Link>
              ))}
            </div>
          )}
        </>
      )}
      {editing && (
        <ResourceEditor
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
        <Modal title={action.label} onClose={() => setAction(null)}>
          <ActionForm
            label="确认操作"
            onCancel={() => setAction(null)}
            onSubmit={() => {
              if (!reason.trim()) throw Error("请填写理由。");
              if (
                kind === "policies" &&
                action.record.id === "POL-01" &&
                action.status !== "启用"
              )
                throw Error("必需内部验收属于不可绕过的硬门禁。");
              if (
                kind === "defects" &&
                action.status === "已关闭" &&
                !s.work.find((w) => w.id === action.record.work)?.qa
              )
                throw Error("关联工作尚未通过内部复核，不能关闭缺陷。");
              resourceCommand(
                kind,
                action.record.id,
                action.status,
                reason,
                action.label,
              );
              setAction(null);
              toast("操作已记录");
            }}
          >
            <Notice tone="warn">
              {action.record.name} → {action.status}。
              {kind === "approvals" ? "确认当前固定版本及授权范围。" : ""}
            </Notice>
            <Field
              label="依据与说明"
              value={reason}
              onChange={setReason}
              type="textarea"
              required
            />
          </ActionForm>
        </Modal>
      )}
    </>
  );
}
function ResourceEditor({ kind, record, onClose, onSaved }) {
  const [form, setForm] = useState(record);
  const fields = Object.keys(form).filter(
    (k) => !["id", "status", "progress", "history", "revision"].includes(k),
  );
  return (
    <Modal
      title={`${record.id ? "编辑" : "新建"}${catalog[kind][2]}`}
      onClose={onClose}
      wide
    >
      <ActionForm
        onCancel={onClose}
        onSubmit={() => {
          if (!form.name?.trim()) throw Error("名称不能为空。");
          if (
            kind === "worklogs" &&
            (!Number.isFinite(Number(form.hours)) ||
              Number(form.hours) <= 0 ||
              Number(form.hours) > 24)
          )
            throw Error("工时须大于 0 且不超过 24 小时。");
          saveResource(kind, form);
          onSaved();
        }}
      >
        <div className="form-grid">
          {fields.map((k) => (
            <Field
              key={k}
              label={fieldNames[k] || k}
              value={String(form[k] ?? "")}
              onChange={(v) => setForm({ ...form, [k]: v })}
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
                    : k === "hours"
                      ? "number"
                      : "text"
              }
              required={["name", "owner"].includes(k)}
            />
          ))}
        </div>
        <Notice>状态通过专门动作流转，编辑内容不会自动完成审批或验收。</Notice>
      </ActionForm>
    </Modal>
  );
}
export function Team({ toast }) {
  const s = useStore();
  const [invite, setInvite] = useState(false);
  const [name, setName] = useState("");
  const [email, setEmail] = useState("");
  const [role, setRole] = useState("项目成员");
  return (
    <>
      <PageHead
        title="好的协作，从清楚的责任开始"
        subtitle="组织关系、项目小团队与执行角色，各有边界。"
      >
        <Button
          variant="primary"
          icon="UserPlus"
          onClick={() => setInvite(true)}
        >
          邀请成员
        </Button>
      </PageHead>
      <div className="two-col">
        <Section title="组织架构">
          <div className="org-root">
            <Icon name="Building2" />
            <div>
              <b>Northstar Studio</b>
              <small>组织 · 林予负责</small>
            </div>
            <Badge tone="violet">3 个团队</Badge>
          </div>
          <div className="org-branches">
            {[
              ["产品与工程", "林予", "Atlas / Verifier"],
              ["品牌与创意", "周宁", "Muse"],
              ["内容工作室", "许言", "Quill"],
            ].map(([name, owner, agents]) => (
              <div className="org-team" key={name}>
                <Icon name="UsersRound" />
                <div>
                  <h3>{name}</h3>
                  <p>
                    {owner} 负责 · {agents}
                  </p>
                </div>
                <Link to="/agents" className="text-link">
                  查看 Agent
                </Link>
              </div>
            ))}
          </div>
          <Notice>
            组织上下级不自动获得私人会话访问权。项目角色可跨部门。
          </Notice>
        </Section>
        <Section title="项目小团队">
          <div className="team-contract">
            <h2>新品发布 · 视觉环节</h2>
            <div className="person">
              <Avatar name="周宁" />
              <div>
                <strong>周宁</strong>
                <small>Human · 唯一结果负责人</small>
              </div>
            </div>
            <div className="person">
              <Avatar name="Muse" agent />
              <div>
                <strong>Muse</strong>
                <small>制作候选与局部修订</small>
              </div>
            </div>
            <div className="person">
              <Avatar name="Verifier" agent />
              <div>
                <strong>Verifier</strong>
                <small>独立规格检查</small>
              </div>
            </div>
            <Link to="/p/launch/work-items/W-103" className="btn full">
              查看分工与交付契约
            </Link>
          </div>
        </Section>
      </div>
      <Section title="共享与成员管理">
        <div className="link-tiles">
          <Link to="/invitations">
            <Icon name="Mail" />
            <h3>邀请与外部成员</h3>
            <p>角色、期限、范围与撤销</p>
          </Link>
          <Link to="/agents">
            <Icon name="Bot" />
            <h3>Agent 编制</h3>
            <p>来源、主管、能力与预算</p>
          </Link>
          <Link to="/policies">
            <Icon name="ShieldCheck" />
            <h3>授权与职责分离</h3>
            <p>关键决定保留 Human 关口</p>
          </Link>
        </div>
      </Section>
      {invite && (
        <Modal title="邀请一起完成工作的人" onClose={() => setInvite(false)}>
          <ActionForm
            label="创建模拟邀请"
            onCancel={() => setInvite(false)}
            onSubmit={() => {
              assertWrite();
              if (!email.trim()) throw Error("请输入电子邮件。");
              saveResource("invitations", {
                id: id("INV"),
                name: name || email,
                status: "待接受",
                owner: "林予",
                email,
                role,
                scope:
                  role === "外部客户" ? "新品发布 · 选定成果" : "新品发布项目",
                expires: "2026-10-20",
              });
              setInvite(false);
              toast("模拟邀请已保存，未发送真实邮件");
            }}
          >
            <Field label="姓名" value={name} onChange={setName} />
            <Field
              label="电子邮件"
              value={email}
              onChange={setEmail}
              type="email"
              required
            />
            <Field
              label="项目角色"
              value={role}
              onChange={setRole}
              options={["项目成员", "评审者", "外部客户"]}
            />
            <Notice>
              邀请不会自动共享内部全部历史。外部客户仅访问明确开放的成果。
            </Notice>
          </ActionForm>
        </Modal>
      )}
    </>
  );
}
export function Agents({ agentId, toast }) {
  const confirm = useConfirm();
  const s = useStore();
  const a = s.agents.find((x) => x.id === agentId);
  const [spawn, setSpawn] = useState(false);
  const [name, setName] = useState("");
  const [runtime, setRuntime] = useState("Codex CLI");
  return (
    <>
      <PageHead
        title={a ? a.name : "不是更多 Agent，而是更合适的伙伴"}
        subtitle={
          a
            ? `${a.role} · ${a.owner} 负责 · ${a.origin}`
            : "身份与模型解耦，能力、预算和责任始终清晰。"
        }
      >
        <Button icon="Plus" variant="primary" onClick={() => setSpawn(true)}>
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
              <dt>Human 负责人</dt>
              <dd>{a.owner}</dd>
              <dt>Runtime</dt>
              <dd>{a.runtime}</dd>
              <dt>模型配置</dt>
              <dd>{a.model}</dd>
              <dt>来源</dt>
              <dd>{a.origin}</dd>
              <dt>授权</dt>
              <dd>只访问已分派工作与明确共享资料</dd>
            </dl>
            <Link className="btn" to="/models">
              模型配置与下发
            </Link>
            <Link className="btn" to="/tools">
              工具与 Skills
            </Link>
          </Section>
          <Section title="生命周期">
            <div className="stack">
              {["暂停", "空闲", "退役"].map((status) => (
                <Button
                  key={status}
                  onClick={async () => {
                    try {
                      assertWrite();
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
                    } catch (e) {
                      toast(e.message, true);
                    }
                  }}
                >
                  {status === "空闲" ? "恢复" : status}
                </Button>
              ))}
            </div>
            <Notice>
              退役与暂停保留工作历史；真实产品需完成交接与委托撤销。
            </Notice>
          </Section>
        </div>
      ) : (
        <>
          <div className="agent-grid">
            {s.agents.map((a) => (
              <Link to={`/agents/${a.id}`} className="agent-card" key={a.id}>
                <div className="split">
                  <Avatar name={a.name} agent size="large" />
                  <Badge>{a.status}</Badge>
                </div>
                <h2>{a.name}</h2>
                <p>{a.role}</p>
                <div className="agent-details">
                  <span>{a.runtime}</span>
                  <span>{a.model}</span>
                </div>
                <footer>
                  <Avatar name={a.owner} />
                  <span>{a.owner} 负责</span>
                  <small>{a.origin}</small>
                </footer>
              </Link>
            ))}
          </div>
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
              ].map((name, i) => (
                <div key={name}>
                  <Icon name="Terminal" />
                  <strong>{name}</strong>
                  <Badge tone={i === 3 || i === 5 ? "amber" : "violet"}>
                    {i === 3 || i === 5 ? "待确认来源" : "模拟适配目标"}
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
      {spawn && (
        <Modal title="创建有边界的 Agent" onClose={() => setSpawn(false)}>
          <ActionForm
            label="提交编制提案"
            onCancel={() => setSpawn(false)}
            onSubmit={() => {
              assertWrite();
              if (!name.trim()) throw Error("请填写名称。");
              update((x) => {
                x.agents.push({
                  id: id("agent"),
                  name,
                  role: "待确认专业职责",
                  runtime,
                  model: "团队文本模型",
                  status: "待批准",
                  owner: "林予",
                  origin: "Human 提案",
                });
                x.resources.approvals.unshift({
                  id: id("AP"),
                  name: `新增 Agent ${name}`,
                  status: "待审核",
                  owner: "林予",
                  target: name,
                  scope: "项目编制 / 预算内",
                  reason: "待明确职责、期限与工具范围",
                  version: "v1",
                });
                log(x, `创建 Agent 提案 ${name}`);
              });
              setSpawn(false);
              toast("提案已进入审批，不会立即启动运行");
            }}
          >
            <Field
              label="Agent 名称"
              value={name}
              onChange={setName}
              required
            />
            <Field
              label="Runtime"
              value={runtime}
              onChange={setRuntime}
              options={["Codex CLI", "Claude Code", "OpenCode", "直接 API"]}
            />
            <Notice>
              身份、运行方式和模型分别配置。新增 Agent 不自动获得额外权限。
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
          value={`$${total.toFixed(2)}`}
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
                  <td>${w.cost.toFixed(2)}</td>
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
  return (
    <>
      <PageHead
        title="决定有依据，协作有来路"
        subtitle="从一个交付，追溯到它的要求、讨论、执行与批准。"
      >
        <Button icon="Download" onClick={() => exportData("audit", s.events)}>
          导出审计记录
        </Button>
      </PageHead>
      <label className="search-field">
        <Icon name="Search" />
        <input
          aria-label="查找审计事件"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="查找主体、动作或对象…"
        />
      </label>
      <Section title="不可覆盖的活动历史">
        <div className="audit-list">
          {s.events
            .filter((e) => (e.text + e.actor).includes(query))
            .map((e) => (
              <Link key={e.id} to={e.target}>
                <span className="audit-icon">
                  <Icon name="Fingerprint" />
                </span>
                <div className="grow">
                  <strong>{e.text}</strong>
                  <small>
                    {e.actor} · {e.time} · {e.id}
                  </small>
                </div>
                <Icon name="ArrowUpRight" />
              </Link>
            ))}
        </div>
      </Section>
    </>
  );
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
