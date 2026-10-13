import { formatCurrency, formatTimestamp } from "./formatting";
import { WorkCalendar } from "./pages-calendar";
import React, { useState, useEffect } from "react";
import {
  useStore, defectRecords, defectStatus, defectCommand, defectVerificationRuns, saveDefect,
  read,
  workCommand,
  workRework,
  pendingFailedBatches,
  qualityResult,
  dependencyProblems,
  saveWorkInputs,
  prepareInputHandoff,
  beginWorkDraft,
  organizationPeople,
  createProject,
  saveWorkTeam,
  workExecutors,
  workTeamMemberAvailable,
  visibleWork,
  exportData,
} from "./store";
import {
  Icon,
  Select,
  Form,
  Button,
  IconButton,
  Badge,
  Avatar,
  PageHead,
  Section,
  Progress,
  Tabs,
  Link,
  Modal,
  useRecoverableDraft,
  Field,
  ActionForm,
  Empty,
  Metric,
  Notice,
} from "./ui";
export const workPath = (w) => `/p/${w.project}/work-items/${w.id}`;
export const artifactPath = (w) => `/p/${w.project}/artifacts/${w.id}`;
export function WorkRows({ items }) {
  return (
    <div className="work-list">
      {items.map((w) => (
        <Link className="work-row" to={workPath(w)} key={w.id}>
          <span className={`work-symbol ${w.type === "图片" ? "teal" : ""}`}>
            <Icon
              name={
                {
                  软件: "Code2",
                  图片: "Image",
                  文档: "FileText",
                  视频: "Clapperboard",
                  小说: "BookOpen",
                  运营: "Megaphone",
                }[w.type]
              }
            />
          </span>
          <div className="grow">
            <strong>{w.title}</strong>
            <small>
              {w.id} · {w.type} · v{w.version}
              {!!w.dependencyBlocks?.length && " · 输入失效，暂停待重验"}
            </small>
          </div>
          <Badge>{w.status}</Badge>
          <span className="row-owner">
            <Avatar name={w.owner} />
            {w.owner}
          </span>
          <small className="due">{w.due}</small>
          <Icon name="ChevronRight" size={16} />
        </Link>
      ))}
    </div>
  );
}
export function Dashboard({ openNew }) {
  const s = useStore();
  const work = visibleWork(s);
  const waiting = work.filter((w) => ["待验收", "等待"].includes(w.status));
  return (
    <>
      <PageHead
        eyebrow="TUESDAY, OCTOBER 13 · 您的协作空间"
        title="早上好，林予。"
        subtitle={`工作在持续推进。${waiting.length} 项工作，正等待你的判断。`}
      >
        <Button icon="Plus" variant="primary" onClick={openNew}>
          发起工作
        </Button>
      </PageHead>
      <div className="metrics">
        <Metric
          label="推进中的项目"
          value={s.projects
            .filter((p) => s.project === "all" || p.id === s.project)
            .length.toString()
            .padStart(2, "0")}
          detail="每一份进展，都有事实依据"
          icon="Layers"
        />
        <Metric
          label="需要你的决定"
          value={waiting.length.toString().padStart(2, "0")}
          detail="质量、创意方向与下一步"
          icon="CircleDot"
        />
        <Metric
          label="已接受的工作"
          value={work
            .filter((w) => w.accepted)
            .length.toString()
            .padStart(2, "0")}
          detail="固定版本 · 可追溯交接"
          icon="CheckCheck"
        />
        <Metric
          label="本月执行费用"
          value={formatCurrency(work.reduce((a, w) => a + w.cost, 0))}
          detail="含候选、检查与重做"
          icon="Wallet"
        />
      </div>
      <div className="dashboard-grid">
        <div>
          <Section
            title="把注意力留给这些事"
            extra={
              <Link to="/overview?view=attention" className="text-link">
                全部待处理 <Icon name="ArrowUpRight" size={14} />
              </Link>
            }
          >
            <div className="attention-list">
              {waiting.slice(0, 3).map((w, i) => (
                <Link key={w.id} to={workPath(w)} className="attention-row">
                  <span className={`attention-icon ${w.qa ? "teal" : "amber"}`}>
                    <Icon name={w.qa ? "Sparkles" : "ShieldAlert"} size={22} />
                  </span>
                  <div className="grow">
                    <div className="row-title">
                      {w.title}
                      <Badge tone={w.qa ? "teal" : "amber"}>
                        {w.qa ? "等待方向确认" : "需要关注"}
                      </Badge>
                    </div>
                    <p>{w.notes}</p>
                    <small>
                      {w.owner} 负责 · {workExecutors(s, w).map((person) => person.name).join("、") || "尚未分派执行者"}
                    </small>
                  </div>
                  <Icon name="ArrowRight" />
                </Link>
              ))}
            </div>
          </Section>
          <Section
            title="项目全景"
            extra={
              <Link to="/projects" className="text-link">
                查看项目 <Icon name="ArrowUpRight" size={14} />
              </Link>
            }
          >
            <div className="project-grid">
              {s.projects
                .filter((p) => s.project === "all" || p.id === s.project)
                .map((p) => (
                  <Link
                    className={`project-card ${p.color}`}
                    to={`/p/${p.id}`}
                    key={p.id}
                  >
                    <div className="project-monogram">
                      <Icon
                        name={
                          p.id === "launch"
                            ? "Orbit"
                            : p.id === "film"
                              ? "Clapperboard"
                              : "Waves"
                        }
                        size={28}
                      />
                      <span>{p.type}</span>
                    </div>
                    <h3>{p.name}</h3>
                    <p>{p.description}</p>
                    <div className="split">
                      <span>已验收进度</span>
                      <b>
                        {Math.round(
                          (s.work.filter(
                            (w) => w.project === p.id && w.accepted,
                          ).length /
                            Math.max(
                              1,
                              s.work.filter((w) => w.project === p.id).length,
                            )) *
                            100,
                        )}
                        %
                      </b>
                    </div>
                    <Progress
                      value={
                        (s.work.filter((w) => w.project === p.id && w.accepted)
                          .length /
                          Math.max(
                            1,
                            s.work.filter((w) => w.project === p.id).length,
                          )) *
                        100
                      }
                    />
                    <footer>
                      <Avatar name={p.owner} />
                      <span>{p.owner}</span>
                      <small>截止 {p.deadline.slice(5)}</small>
                    </footer>
                  </Link>
                ))}
            </div>
          </Section>
          <Section title="继续你的工作">
            <WorkRows
              items={work.filter((w) => w.status !== "已完成").slice(0, 4)}
            />
          </Section>
        </div>
        <aside className="dashboard-aside">
          <Section
            title="协作中的伙伴"
            extra={
              <Link to="/agents" className="text-link">
                全部
              </Link>
            }
          >
            <div className="partner-list">
              {s.agents.map((a) => (
                <Link to={`/agents/${a.id}`} className="partner" key={a.id}>
                  <Avatar name={a.name} agent />
                  <div className="grow">
                    <strong>{a.name}</strong>
                    <small>{a.role}</small>
                  </div>
                  <Badge tone={a.status === "运行中" ? "green" : "violet"}>
                    {a.status}
                  </Badge>
                </Link>
              ))}
            </div>
          </Section>
          <Section
            title="最近发生"
            extra={
              <span className="live">
                <span /> 实时同步
              </span>
            }
          >
            <div className="timeline">
              {s.events.slice(0, 5).map((e) => (
                <Link to={e.target} key={e.id}>
                  <span className="timeline-dot" />
                  <small>
                    {formatTimestamp(e.time)} · {e.actor}
                  </small>
                  <p>{e.text}</p>
                </Link>
              ))}
            </div>
          </Section>
          <div className="quiet-note">
            <Icon name="ShieldCheck" size={24} />
            <h3>安心让工作继续</h3>
            <p>
              内部检查没有通过的成果，不会被自动交付。需要你时，我们会带着依据来。
            </p>
            <Link to="/policies" className="text-link">
              查看工作边界 <Icon name="ArrowRight" size={14} />
            </Link>
          </div>
        </aside>
      </div>
    </>
  );
}
export function WorkList({ params, openNew, projectId, toast, viewPath, workKind }) {
  const s = useStore();
  const view = params.get("view") || "list";
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("全部");
  const work = visibleWork(s).filter(
    (w) =>
      (!workKind || w.workKind === workKind) &&
      (!projectId || w.project === projectId) &&
      (!query || w.title.includes(query)) &&
      (filter === "全部" || w.status === filter),
  );
  const prefix = workKind === "BUG" ? "/defects" : projectId
    ? `/p/${projectId}/work-items`
    : viewPath === "/collaboration"
      ? "/collaboration"
      : "/work";
  return (
    <>
      <PageHead
        title={workKind === "BUG" ? "缺陷工作" : projectId ? "项目工作" : "与我有关的工作"}
        subtitle="计划、执行、验收与交接，始终是同一件工作。"
      >
        <Button variant="primary" icon="Plus" onClick={openNew}>
          {workKind === "BUG" ? "发起缺陷工作" : "发起工作"}
        </Button>
      </PageHead>
      <div className="toolbar">
        <label className="search-field">
          <Icon name="Search" />
          <input
            aria-label="查找工作"
            name="work-search" autoComplete="off"
            placeholder="查找工作…"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
          />
        </label>
        <Select
          aria-label="工作状态"
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
        >
          {[
            "全部",
            "待开始",
            "进行中",
            "等待",
            "待验收",
            "待交接",
            "已完成",
            "已取消",
          ].map((v) => (
            <option key={v}>{v}</option>
          ))}
        </Select>
        <div className="grow" />
        <div className="segmented">
          <Link
            to={`${prefix}?view=list`}
            className={view === "list" ? "selected" : ""}
          >
            <Icon name="List" />
            列表
          </Link>
          <Link
            to={`${prefix}?view=board`}
            className={view === "board" ? "selected" : ""}
          >
            <Icon name="Columns3" />
            看板
          </Link>
          <Link
            to={`${prefix}?view=calendar`}
            className={view === "calendar" ? "selected" : ""}
          >
            <Icon name="CalendarDays" />
            日历
          </Link>
        </div>
      </div>
      {view === "calendar" ? (
        <WorkCalendar work={work} toast={toast} projectId={projectId} />
      ) : !work.length ? (
        <Empty />
      ) : view === "board" ? (
        <div className="kanban">
          {["待开始", "进行中", "等待", "待验收", "待交接", "已完成"].map(
            (status) => (
              <section key={status}>
                <div className="split">
                  <h2>{status}</h2>
                  <small>
                    {work.filter((w) => w.status === status).length}
                  </small>
                </div>
                {work
                  .filter((w) => w.status === status)
                  .map((w) => (
                    <Link className="kanban-card" to={workPath(w)} key={w.id}>
                      <small>
                        {w.id} · {w.type}
                      </small>
                      <h3>{w.title}</h3>
                      <p>{w.notes}</p>
                      <footer>
                        <Avatar name={w.owner} />
                        <span>{w.owner}</span>
                        <small>v{w.version}</small>
                      </footer>
                    </Link>
                  ))}
              </section>
            ),
          )}
        </div>
      ) : (
        <WorkRows items={work} />
      )}
    </>
  );
}
export function Project({ projectId, openNew }) {
  const s = useStore();
  const p = s.projects.find((x) => x.id === projectId);
  if (!p) return <Empty title="项目不存在" />;
  return (
    <>
      <PageHead
        eyebrow={`协作 / ${p.type}`}
        title={p.name}
        subtitle={p.description}
      >
        <Button icon="Plus" variant="primary" onClick={openNew}>
          添加工作
        </Button>
      </PageHead>
      <div className="project-banner">
        <span className="project-orbit">
          <Icon name="Orbit" size={64} />
        </span>
        <div className="grow">
          <Badge tone="green">项目进行中</Badge>
          <h2>从共同目标，到共同成果。</h2>
          <p>
            {p.owner} 负责 · 截止 {p.deadline} · 共享范围清楚，协作自然发生
          </p>
        </div>
        <Link className="btn" to="/chat?channel_id=launch">
          <Icon name="MessagesSquare" />
          进入项目频道
        </Link>
      </div>
      <div className="tabs">
        <Link to={`/p/${p.id}/work-items`}>工作</Link>
        <Link to={`/p/${p.id}/flow`}>工作流</Link>
        <Link to="/deliveries">交付</Link>
        <Link to="/repositories">仓库</Link>
        <Link to="/testplans">测试计划</Link>
      </div>
      <div className="two-col">
        <Section title="当前工作">
          <WorkRows items={visibleWork(s).filter((w) => w.project === p.id)} />
        </Section>
        <Section title="项目契约">
          <dl>
            <dt>结果负责人</dt>
            <dd>{p.owner}</dd>
            <dt>执行预算</dt>
            <dd>
              ${p.spent} / ${p.budget}
            </dd>
            <dt>交付原则</dt>
            <dd>固定版本、内部验收通过、Human 批准</dd>
            <dt>资料边界</dt>
            <dd>项目成员可见；外部仅已共享成果</dd>
          </dl>
          <Link className="btn" to="/knowledge">
            查看项目资料
          </Link>
        </Section>
      </div>
    </>
  );
}
function CompletionContract({ work, toast }) {
  const [mode, setMode] = useState(work.completionContract?.mode || "");
  const [recipient, setRecipient] = useState(work.completionContract?.recipient || "");
  const [reason, setReason] = useState(work.completionContract?.reason || "");
  return <Section title="接收契约" extra={<Badge>{work.completionContract ? "已确认" : "未确认"}</Badge>}>
    <Field label="完成与接收方式" value={mode} disabled={work.accepted}
      options={[{ value: "", label: "请选择，不推测接收方式" }, { value: "internal", label: "内部使用，无外部接收方" }, { value: "external", label: "指定接收人交付" }]} onChange={setMode} />
    {mode === "internal" && <Field label="L4 不适用理由" value={reason} onChange={setReason} required disabled={work.accepted} />}
    {mode === "external" && <Field label="指定接收人" value={recipient} onChange={setRecipient} required disabled={work.accepted} />}
    <Button disabled={work.accepted} onClick={() => {
      try { workCommand(work.id, "contract", "", work.version, { mode, recipient, reason }); toast("接收契约已确认"); }
      catch (error) { toast(error.message, true); }
    }}>确认接收契约</Button>
    <Notice>无外部接收方只免除 L4；内部检查与 Human 接受仍然必需。内部完成不授予公开发布权限。</Notice>
  </Section>;
}
function InputContract({ work, toast }) {
  const s = useStore();
  const [inputs, setInputs] = useState(() => structuredClone(work.inputs || []));
  const [reason, setReason] = useState("");
  const blocked = dependencyProblems(s, work);
  const change = (index, patch) => setInputs((entries) => entries.map((entry, i) => i === index ? { ...entry, ...patch } : entry));
  return <Section title="固定输入依赖" extra={<Badge>{work.inputs === undefined ? "未配置" : work.inputs.length ? `${work.inputs.length} 项已确认` : "已确认无输入"}</Badge>}>
    <Notice>先准备指定下游 Human 接收的固定包，再接收并采用；这一次接收同时记录 L3，不需上游重复批准。采用新输入后需要重新提交和验收。</Notice>
    {inputs.map((input, index) => <div className="form-grid" key={index}>
      <Field label={`输入 ${index + 1} 工作`} value={input.workId}
        options={[{ value: "", label: "选择输入工作" }, ...s.work.filter((item) => item.id !== work.id).map((item) => ({ value: item.id, label: `${item.id} · ${item.title}` }))]}
        onChange={(workId) => change(index, { workId, handoffId: undefined, version: s.work.find((item) => item.id === workId)?.version || 1 })} />
      <Field label={`输入 ${index + 1} 固定版本`} type="number" value={String(input.version)} onChange={(value) => change(index, { version: Number(value), handoffId: undefined })} />
      {s.work.some((item) => item.id === input.workId) && <Link className="btn" to={`/p/${s.work.find((item) => item.id === input.workId).project}/artifacts/${input.workId}?version=${input.version}`}>查看输入 {index + 1} 固定成果</Link>}
      <Field label={`输入 ${index + 1} 指定 Human 接收人`} value={input.recipientId || s.work.find((item) => item.id === input.workId)?.handoffs?.find((pack) => pack.id === input.handoffId)?.recipientId || s.work.find((item) => item.id === input.workId)?.team?.receiverId || organizationPeople(s).find((person) => person.name === work.owner && person.kind === "Human")?.id || ""}
        options={[{ value: "", label: "请选择 Human" }, ...organizationPeople(s).filter((person) => person.kind === "Human").map((person) => ({ value: person.id, label: person.name }))]}
        onChange={(recipientId) => change(index, { recipientId, handoffId: undefined })} />
      <Button onClick={() => {
        try { const handoffId = prepareInputHandoff(input.workId, work.id, input.version, input.recipientId); change(index, { handoffId }); toast("固定交接包已准备，等待指定 Human 接收"); }
        catch (error) { toast(error.message, true); }
      }}>准备输入 {index + 1} 固定交接包</Button>
      {(() => { const pack = s.work.find((item) => item.id === input.workId)?.handoffs?.find((item) => item.id === input.handoffId);
        return pack ? <details><summary>固定包 {pack.id} · {pack.recipient} · {pack.status}</summary>
          {pack.items.map((item) => <p key={item.id}>{item.name || item.id} · {item.version} · {item.status}<br />{item.reference}</p>)}
          <small>{pack.receipt ? `L3 接收人 ${pack.receipt.actor} · ${pack.receipt.reason}` : "接收后同时记录 L3；外部发布需要另行授权。"}</small>
        </details> : <small>尚未准备固定包</small>; })()}

      <Button disabled={blocked.some((block) => block.inputWorkId === input.workId)} onClick={() => setInputs(inputs.filter((_, i) => i !== index))}>移除输入 {index + 1}</Button>
    </div>)}
    <Button onClick={() => setInputs([...inputs, { workId: "", version: 1 }])}>添加输入引用</Button>
    <Field label="输入确认或采用说明" value={reason} onChange={setReason} required />
    <Button onClick={() => {
      try { saveWorkInputs(work.id, inputs, reason, work.version, work.inputsRevision || 0); toast("输入已确认，旧验收不会自动沿用"); }
      catch (error) { toast(error.message, true); }
    }}>{blocked.length ? "接收新固定包并重新验收" : inputs.length ? "接收固定包并采用输入" : "确认无输入依赖"}</Button>
  </Section>;
}
function WorkTeamEditor({ work, toast }) {
  const s = useStore();
  const [form, setForm] = useState(null);
  const people = organizationPeople(s), humans = people.filter((person) => person.kind === "Human");
  const name = (id) => people.find((person) => person.id === id)?.name || "成员已不可用";
  const open = () => {
    const ownerId = humans.find((person) => person.name === work.owner)?.id || "";
    const legacyAgent = work.agentId && people.some((person) => person.id === `agent:${work.agentId}` && workTeamMemberAvailable(s, person.id)) ? `agent:${work.agentId}` : undefined;
    setForm({ revision: work.team?.revision || 0, previousOwner: work.owner, ownerId,
      coordinatorId: work.team?.coordinatorId || legacyAgent || ownerId,
      executorIds: [...(work.team?.executorIds || (legacyAgent ? [legacyAgent] : []))],
      checkerIds: [...(work.team?.checkerIds || [])], receiverId: work.team?.receiverId || ownerId });
  };
  const describe = (snapshot) => `${snapshot.owner || "未配置"} 负责 · 协调 ${snapshot.coordinatorId ? name(snapshot.coordinatorId) : "未配置"} · 执行 ${(snapshot.executorIds || []).map(name).join("、") || "未配置"} · 检查 ${(snapshot.checkerIds || []).map(name).join("、") || "未配置"} · Human 接收 ${snapshot.receiverId ? name(snapshot.receiverId) : "未配置"}`;
  return <>
    <Button className="full" disabled={s.role !== "owner"} onClick={open}>配置工作小团队</Button>
    <p className="muted">当前执行编组引用 Human 与 Agent 名册；不改变 Human 正式组织、Agent 生命周期或既有成果作者。</p>
    {work.team?.history?.length > 0 && <details><summary>编组修订历史 · {work.team.revision}</summary>{work.team.history.map((entry, index) => <div key={index}><p>修订 {index + 1} · {entry.reason}</p><small>之前：{describe(entry.previous)}</small><br /><small>之后：{describe(entry.next)}</small></div>)}</details>}
    {form && <WorkTeamForm key={`${s.activeTenant}:${work.id}`} state={s} work={work} initial={form} people={people} onClose={() => setForm(null)} toast={toast} />}
  </>;
}
function WorkTeamForm({ state: s, work, initial, people, onClose, toast }) {
  const [form, setForm, draft] = useRecoverableDraft(JSON.stringify([s.activeTenant, "work-team", work.id]), () => ({ ...initial, reason: "" }), value => ({ ...value, executorIds: [...value.executorIds].sort(), checkerIds: [...value.checkerIds].sort() }));
  const humans = people.filter(person => person.kind === "Human");
  const options = [...people.filter((person) => person.kind === "Agent"), ...humans].map((person) => ({ value: person.id, label: `${person.name} · ${person.kind}${workTeamMemberAvailable(s, person.id) ? "" : " · 不可新分派"}` }));
  const humanOptions = humans.map((person) => ({ value: person.id, label: person.name }));
  const name = id => people.find(person => person.id === id)?.name || "成员已不可用";
  const change = (patch) => setForm((old) => ({ ...old, ...patch }));
  const picker = (field, label) => <fieldset className="org-person-picker"><legend>{label}</legend><div>
    {options.map((person) => <label className="switch-label" key={person.value}>
      <input type="checkbox" aria-label={`${label} ${name(person.value)}`} checked={form[field].includes(person.value)} disabled={!workTeamMemberAvailable(s, person.value) && !work.team?.[field]?.includes(person.value)}
        onChange={(event) => change({ [field]: event.target.checked ? [...form[field], person.value] : form[field].filter((id) => id !== person.value) })} />
      <span>{person.label}</span>
    </label>)}
  </div></fieldset>;
  return <Modal title="配置工作小团队" {...draft.modalProps} onClose={onClose}>
      <ActionForm label="保存工作小团队" onCancel={onClose} onSubmit={({ dismiss }) => { saveWorkTeam(work.id, form, form.reason); dismiss(); toast("任务执行编组已更新，正式组织与历史成果保持原记录"); }}>
        {draft.recovered && <Notice>已恢复未保存的编组，保留原负责人和编组修订。若编组已变化，请放弃后重新打开。</Notice>}
        <Notice>从当前租户的 Agent 池与 Human 名册编组。退役或待批准 Agent 不可新分派，旧引用保留。下游接收人须为 Human，负责固定包的接收决定；公开发布仍需单独授权。</Notice>
        <Field label="Human 结果负责人" value={form.ownerId} options={[{ value: "", label: "请选择" }, ...humanOptions]} onChange={(ownerId) => change({ ownerId })} required />
        <Field label="协调者" value={form.coordinatorId} options={[{ value: "", label: "请选择" }, ...options.map((option) => ({ ...option, disabled: !workTeamMemberAvailable(s, option.value) && work.team?.coordinatorId !== option.value }))]} onChange={(coordinatorId) => change({ coordinatorId })} required />
        {picker("executorIds", "执行者")}{picker("checkerIds", "检查者")}
        <Field label="下游 Human 接收人" value={form.receiverId} options={[{ value: "", label: "请选择" }, ...humanOptions]} onChange={(receiverId) => change({ receiverId })} required />
        <Field label="编组变更说明" value={form.reason} onChange={reason => change({ reason })} required />
      </ActionForm>
    </Modal>;
}
function ReworkControl({ work, role, toast }) {
  const control = workRework(work);
  const [internal, setInternal] = useState(String(control.limits.internal ?? ""));
  const [formal, setFormal] = useState(String(control.limits.formal ?? ""));
  const [extra, setExtra] = useState("");
  const [reason, setReason] = useState("");
  const [error, setError] = useState("");
  const decide = (command) => {
    try {
      workCommand(work.id, command, reason, work.version, { revision: control.revision, internal, formal, additionalRounds: extra });
      setError(""); toast("Human 决定已保留");
    } catch (e) { setError(e.message); }
  };
  return <Section title="修复与正式返工" extra={<Badge>{control.hold ? "已停线" : "有界执行"}</Badge>}>
    <dl className="record-fields">
      <dt>当前声明链失败验证轮</dt><dd>{control.internal} / {control.internalCeiling ?? control.limits.internal ?? "未配置"} 轮</dd>
      <dt>失败验证轮历史累计</dt><dd>{control.internalTotal} 轮</dd>
      <dt>正式质量返工累计</dt><dd>{control.formalTotal} 次 · {control.limits.formal ?? "未配置"} 次升级</dd>
    </dl>
    <small>每个已结束且失败的固定验证批次计一轮，修复提交不重复计数。更换执行者或节点不清零。正式退回才重开内部声明链；达到正式上限后，每次返工都须 Human 决定。</small>
    {control.hold && <Notice tone="warn">{control.hold.reason}。负责人可明确继续、接管或取消；必需验证仍须完成。</Notice>}
    {(!control.limits.internal || !control.limits.formal) && <Notice>该专业包上限未配置。请负责人明确两类上限后再修复。</Notice>}
    {role === "owner" && work.status !== "已取消" && <>
      <details><summary>配置本工作返工上限</summary>
        <Field label="内部修复上限" type="number" min="1" step="1" value={internal} onChange={setInternal} required />
        <Field label="正式返工升级阈值" type="number" min="1" step="1" value={formal} onChange={setFormal} required />
        <p className="muted">配置不清零累计，也不会自动解除已有停线。</p>
        <Button onClick={() => decide("rework-config")}>保存返工上限</Button>
      </details>
      <Field label="返工处理依据" value={reason} onChange={setReason} type="textarea" required />
      {control.hold?.kind === "internal" && <Field label="本次额外内部修复轮数" type="number" min="1" step="1" value={extra} onChange={setExtra} required />}
      {control.hold && <Button variant="primary" onClick={() => decide("rework-continue")}>明确继续当前返工</Button>}
      {error && <p role="alert">{error}</p>}
    </>}
    <details><summary>返工事件与 Human 决定</summary>
      {control.events.map((event, i) => <p key={`event-${i}`}>{event.kind === "internal" ? `失败验证轮 ${event.round} · v${event.version} · ${event.key || "历史记录"}` : event.kind === "repair" ? `修复提交 · v${event.version} → v${event.submittedVersion}` : event.kind === "formal" ? `正式退回 · ${event.key}` : `声明链 ${event.previousChain} 结束 · 内部 ${event.previousInternal} 轮`} · {event.reason}</p>)}
      {control.decisions.map((decision, i) => <p key={`decision-${i}`}>{decision.action} · {decision.reason}{decision.additionalRounds && ` · 本次额外 ${decision.additionalRounds} 轮`} · {formatTimestamp(decision.time)}</p>)}
      {!control.events.length && !control.decisions.length && <p className="muted">尚无返工事件。</p>}
    </details>
    <p className="muted">环境重试和范围变更不计质量返工；其专属处理入口尚未实现，请勿以质量退回代替。</p>
  </Section>;
}
function openDefectDraft(toast, options = {}) {
  try { const draftId = beginWorkDraft({ workKind: "BUG", ...options }); location.hash = `/chat?channel_id=team&work_draft=${encodeURIComponent(draftId)}`; }
  catch (error) { toast(error.message, true); }
}
export function LegacyDefectRoute({ work, ...props }) {
  useEffect(() => { location.replace(`#${workPath(work)}${location.hash.includes("?") ? `?${location.hash.split("?")[1]}` : ""}`); }, [work.id]);
  return <WorkDetail {...props} workId={work.id} />;
}
export function DefectList({ params, toast }) {
  const s = useStore();
  if (s.role === "external") return <Empty title="当前视角无权访问" />;
  return <>
    <WorkList params={params} toast={toast} workKind="BUG" viewPath="/defects" openNew={() => openDefectDraft(toast)} />
    {s.defectMigrationIssues?.length > 0 && <Section title="待修复迁移"><Notice tone="warn">以下原记录无法安全关联，已完整保留，未覆盖任何现有工作。</Notice>{s.defectMigrationIssues.map((issue, index) => <details key={index}><summary>{issue.legacy.id || "缺少编号"} · {issue.reason}</summary><pre>{JSON.stringify(issue.legacy, null, 2)}</pre></details>)}</Section>}
  </>;
}
function DefectPanel({ work, toast }) {
  const s = useStore();
  const current = defectRecords(s).find(record => record.id === work.id);
  const [editing, setEditing] = useState(null), [action, setAction] = useState(null), [reason, setReason] = useState(""), [runId, setRunId] = useState("");
  if (!work.defect) return <Notice tone="warn">缺陷专业数据缺失，请先修复迁移记录。</Notice>;
  const terminal = ["已关闭", "已取消"].includes(current.status);
  const fields = [["severity", "严重度"], ["priority", "优先级"], ["steps", "复现步骤"], ["expected", "期望结果"], ["actual", "实际结果"], ["environment", "环境"], ["commit", "关联提交"], ["version", "影响版本"]];
  const act = (status, label) => { setReason(""); setRunId(""); setAction({ status, label, revision: current.revision }); };
  return <Section title="缺陷专业信息" extra={<Badge>{current.status}</Badge>}>
    <p><Link to={workPath(s.work.find(item => item.id === current.work) || { id: current.work, project: work.project })}>{current.work}</Link> · 影响版本 {current.version} · 缺陷工作 v{work.version}</p>
    <p className="muted">{work.ownerSource || "Human 负责人由工作团队维护"} · 专业阶段 {work.defect.stage || "未验证"}</p>
    {work.status === "历史已关闭" && <Notice>保留原系统关闭状态与证据；迁移没有补造本系统的验证、Human 接受或交付记录。</Notice>}
    {work.defect.reopenedFromWorkItemId && <p>回归来源：<Link to={`/defects/${work.defect.reopenedFromWorkItemId}`}>{work.defect.reopenedFromWorkItemId}</Link>（原终态保留）</p>}
    <dl className="record-fields">{fields.map(([key, label]) => <React.Fragment key={key}><dt>{label}</dt><dd>{current[key] || "未记录"}</dd></React.Fragment>)}</dl>
    {current.verification && <p>固定验证：{current.verification.id} · {current.verification.status} · {current.verification.owner} · {current.verification.evidence}</p>}
    <div className="actions">
      {!terminal && <Button onClick={() => setEditing(structuredClone(current))}>编辑缺陷信息</Button>}
      {!terminal && current.status !== "待验证" && <Button onClick={() => act("待验证", "提交修复")}>提交修复</Button>}
      {!terminal && <><Button onClick={() => act("已关闭", "验证通过")}>验证通过</Button><Button onClick={() => act("待修复", "验证失败并重开")}>验证失败并重开</Button></>}
      {terminal && <Button onClick={() => openDefectDraft(toast, { reopenedFrom: work.id })}>报告回归并新建缺陷</Button>}
    </div>
    {current.history?.length > 0 && <details><summary>缺陷修复与验证历史 · {current.history.length} 条</summary>{current.history.map((entry, index) => <p key={index}>{entry.time ? formatTimestamp(entry.time) : "历史记录"} · {entry.status || entry.stage || "修订"} · {entry.reason || entry.repairReason} {entry.verification?.id}</p>)}</details>}
    {work.defect.legacy && <details><summary>完整旧记录</summary><pre>{JSON.stringify(work.defect.legacy, null, 2)}</pre></details>}
    {editing && <Modal title="编辑缺陷信息" draftValue={editing} onClose={() => setEditing(null)}><ActionForm onCancel={() => setEditing(null)} onSubmit={({ dismiss }) => { saveDefect(editing); dismiss(); toast("缺陷工作已更新"); }}>
      <Field label="名称" value={editing.name} onChange={name => setEditing(old => ({ ...old, name }))} />
      <Field label="受影响工作" value={editing.work} onChange={value => setEditing(old => ({ ...old, work: value }))} options={s.work.filter(item => item.project === work.project && item.workKind !== "BUG").map(item => ({ value: item.id, label: `${item.id} · ${item.title}` }))} />
      {fields.map(([key, label]) => <Field key={key} label={label} value={editing[key] || ""} onChange={value => setEditing(old => ({ ...old, [key]: value }))} />)}
    </ActionForm></Modal>}
    {action && <Modal title={action.label} draftValue={{ reason, runId }} onClose={() => setAction(null)}><ActionForm label="确认操作" onCancel={() => setAction(null)} onSubmit={({ dismiss }) => { defectCommand(work.id, action.status, reason, action.label, { runId, revision: action.revision }); dismiss(); toast("缺陷工作状态已更新"); }}>
      {action.status !== "待验证" && <Field label="独立验证运行" value={runId} onChange={setRunId} options={[{ value: "", label: "请选择验证运行" }, ...defectVerificationRuns(s, current).filter(run => run.status === (action.status === "已关闭" ? "通过" : "失败")).map(run => ({ value: run.id, label: `${run.id} · ${run.status} · ${run.owner}` }))]} />}
      <Field label="依据与说明" value={reason} onChange={setReason} type="textarea" required />
    </ActionForm></Modal>}
  </Section>;
}

export function WorkDetail({ workId, params, toast }) {
  const s = useStore();
  const w = s.work.find(
    (x) => x.id === workId && (s.role !== "external" || x.delivered),
  );
  const [confirm, setConfirm] = useState(null);
  const [reason, setReason] = useState("");
  const [returnDetails, setReturnDetails] = useState({ requirement: "", expected: "", actual: "", position: "" });
  const closeAction = () => { setConfirm(null); setReason(""); setReturnDetails({ requirement: "", expected: "", actual: "", position: "" }); };
  if (!w)
    return (
      <Empty title="工作不可访问" text="对象不存在，或当前视角没有访问权限。" />
    );
  const rework = workRework(w);
  const unfinishedBatches = pendingFailedBatches(s, w);
  const executors = workExecutors(s, w);
  const people = organizationPeople(s);
  const memberName = (id) => people.find((person) => person.id === id)?.name || "成员已不可用";
  const tab = params.get("tab") || "brief";
  const run = (cmd) => {
    try {
      const href = workCommand(w.id, cmd, "", w.version);
      if (href) location.hash = href;
      toast("工作状态已更新");
    } catch (e) {
      toast(e.message, true);
    }
  };
  return (
    <>
      <PageHead
        eyebrow={`${s.projects.find((p) => p.id === w.project)?.name} / ${w.id}`}
        title={w.title}
        subtitle={`Human ${w.owner} 负责 · ${executors.map((person) => person.name).join("、") || "未分派"} 执行 · 当前成果 v${w.version}`}
      >
        <Badge>{w.status}</Badge>
        <Button
          icon="MessagesSquare"
          onClick={() => (location.hash = `/chat?execution_id=${w.id}`)}
        >
          进入执行会话
        </Button>
      </PageHead>
      {dependencyProblems(s, w).length > 0 && <Notice tone="warn">
        输入失效，当前工作暂停并等待采用新输入、重新验收。
        {dependencyProblems(s, w).map((block, index) => <p key={index}>{block.inputWorkId} · v{block.inputVersion}：{block.reason}</p>)}
        <Link to={`${workPath(w)}?tab=plan`}>处理固定输入依赖</Link>
      </Notice>}
      {unfinishedBatches.length > 0 && <Notice tone="warn">提交新成果前，请先结束失败验证批次并形成固定报告。{unfinishedBatches.map(batch => <p key={batch.batchId}><Link to={`/testplans/${batch.planId}`}>{batch.needsNewBatch ? "历史批次未记录轮次，请启动并完成新的验证批次" : "打开测试计划，结束失败批次"} · {batch.batchId}</Link></p>)}</Notice>}
      {rework.hold && <Notice tone="warn">返工已停线：{rework.hold.reason}。请在“修复与正式返工”中记录 Human 决定。</Notice>}
      {w.workKind === "BUG" && <DefectPanel work={w} toast={toast} />}
      <div className="stepper">
        {[
          ["目标", "已明确"],
          ["计划", w.plan ? "已确认" : "待确认"],
          ["SOP", w.flow ? "已发布" : "待确认"],
          ["内部验收", w.qa ? "通过" : "未通过"],
          ["Human 接受", w.accepted ? "已接受" : "待处理"],
          ["交付", w.completionContract?.mode === "internal" ? (w.accepted ? "内部完成" : "无需对外交付") : w.delivered ? "已交付" : "待交接"],
          ["L4 接收", w.l4?.status || "未确认"],
        ].map(([label, status], i) => (
          <div
            className={
              [
                "已明确",
                "已确认",
                "已发布",
                "通过",
                "已接受",
                "已交付",
              ].includes(status)
                ? "done"
                : ""
            }
            key={label}
          >
            <span>{i + 1}</span>
            <div>
              <b>{label}</b>
              <small>{status}</small>
            </div>
          </div>
        ))}
      </div>
      <div className="tabs">
        {[
          ["brief", "工作简报"],
          ["plan", "计划与契约"],
          ["results", "成果与版本"],
          ["review", "检查与接受"],
          ["activity", "活动记录"],
        ].map(([v, l]) => (
          <Link
            key={v}
            to={`${workPath(w)}?tab=${v}`}
            className={tab === v ? "selected" : ""}
          >
            {l}
          </Link>
        ))}
      </div>
      <div className="detail-grid">
        <div>
          {tab === "brief" && (
            <>
              <Section title="要完成什么">
                <p className="body-copy">{w.notes}</p>
                <h3>必须满足的条件</h3>
                <ul className="check-list">
                  {w.requirements.map((r) => (
                    <li key={r}>
                      <Icon
                        name={
                          qualityResult(s, w) === "通过"
                            ? "CircleCheck"
                            : "CircleHelp"
                        }
                      />
                      <span>
                        <b>{r}</b>
                        <small>验收条款 · 逐条结论请查看关联检查记录</small>
                      </span>
                    </li>
                  ))}
                </ul>
              </Section>
              <Section title="当前进展">
                <Notice tone={w.qa ? "info" : "warn"}>
                  {w.qa
                    ? "内部检查已通过，等待有权负责人接受当前版本。"
                    : "当前版本尚未满足全部内部验收条件。请修复后重验，不能直接批准交付。"}
                </Notice>
                <div className="actions spaced">
                  {!w.plan && (
                    <Button variant="primary" onClick={() => run("plan")}>
                      确认计划 v1
                    </Button>
                  )}
                  {w.plan && (!w.flow || !w.flowVersion) && (
                    <Button variant="primary" onClick={() => run("flow")}>
                      确认工作流版本
                    </Button>
                  )}
                  {w.plan && w.flow && w.status === "待开始" && (
                    <Button variant="primary" onClick={() => run("start")}>
                      开始执行
                    </Button>
                  )}
                  {["进行中", "等待"].includes(w.status) && (
                    <Button variant="primary" disabled={Boolean(rework.hold) || unfinishedBatches.length > 0} onClick={() => run("submit")}>
                      提交新成果版本
                    </Button>
                  )}
                  <Link className="btn" to={`${workPath(w)}?tab=review`}>
                    查看检查与接受
                  </Link>
                </div>
              </Section>
            </>
          )}
          {tab === "plan" && (
            <>
              <CompletionContract key={w.id} work={w} toast={toast} />
              <InputContract key={`${w.id}:${w.inputsRevision || 0}`} work={w} toast={toast} />
              {w.draftId && <Section title="会话确认的计划">
                <p>{w.planSnapshot?.goal}</p><p>完成条件：{w.planSnapshot?.criteria}</p>
                {w.sourceContext && <p>来源消息：{w.sourceContext.text}</p>}
                <Link className="btn" to={`/chat?${s.drafts?.[w.draftId]?.executionId ? `execution_id=${encodeURIComponent(s.drafts[w.draftId].executionId)}` : `channel_id=${encodeURIComponent(s.drafts?.[w.draftId]?.channel || "team")}`}&work_draft=${encodeURIComponent(w.draftId)}`}>查看来源会话与草稿</Link>
              </Section>}
              <Section title="环节交付契约">
                <dl>
                  <dt>上游输入</dt>
                  <dd>{w.inputs === undefined ? "未配置输入引用" : w.inputs.length ? w.inputs.map((input) => `${input.workId} v${input.version}`).join("；") : "已明确确认无输入依赖"}</dd>
                  <dt>输出</dt>
                  <dd>
                    {w.type}成果 v{w.version}、源文件、检查证据
                  </dd>
                  <dt>小团队</dt>
                  <dd>
                    {w.owner} 负责 · {executors.map((person) => person.name).join("、")} 执行
                  </dd>
                  {w.team ? <>
                    <dt>协调者</dt><dd>{memberName(w.team.coordinatorId)}</dd>
                    <dt>检查者</dt><dd>{w.team.checkerIds.map(memberName).join("、")}</dd>
                    <dt>下游 Human 接收人</dt><dd>{memberName(w.team.receiverId)}</dd>
                  </> : <><dt>团队角色</dt><dd>尚未配置完整小团队，请在本工作责任区确认。</dd></>}
                  <dt>下游接收方</dt>
                  <dd>{w.completionContract?.mode === "internal" ? `无外部接收方：${w.completionContract.reason}` : w.completionContract?.recipient || "接收契约未确认"}</dd>
                  <dt>资源限制</dt>
                  <dd>内部修复上限 {rework.limits.internal ?? "未配置"} 轮 · 正式返工 {rework.limits.formal ?? "未配置"} 次升级</dd>
                  <dt>退回规则</dt>
                  <dd>绑定版本和未满足条款；按已登记输入引用暂停受影响下游，未配置关系不作推断</dd>
                </dl>
                <Link
                  className="btn"
                  to={`/p/${w.project}/flow${w.flowVersion ? `?version=${w.flowVersion}` : ""}`}
                >
                  打开 工作流
                </Link>
              </Section>
              <Section title="确认基线">
                <div className="actions">
                  <Button disabled={w.plan} onClick={() => run("plan")}>
                    {w.plan ? "计划已确认" : "确认计划"}
                  </Button>
                  <Button
                    disabled={!w.plan || (w.flow && !!w.flowVersion)}
                    onClick={() => run("flow")}
                  >
                    {w.flow && w.flowVersion
                      ? `已锁定工作流 v${w.flowVersion}`
                      : "确认工作流版本"}
                  </Button>
                </div>
              </Section>
            </>
          )}
          {tab === "results" && (
            <Section title="固定成果与来源">
              <div className="artifact-cover">
                <Icon name="Layers" size={36} />
                <h2>{w.title}</h2>
                <p>当前提交版本 v{w.version}</p>
                <Link className="btn primary" to={artifactPath(w)}>
                  打开成果查看器
                </Link>
              </div>
              <p className="muted">
                历史版本保留；批注、验证和交付始终指向指定版本。
              </p>
            </Section>
          )}
          {tab === "review" && (
            <>
              <CompletionContract key={w.id} work={w} toast={toast} />
              <Section
                title={`v${w.version} · 内部质量门`}
                extra={<Badge>{qualityResult(s, w)}</Badge>}
              >
                <ul className="check-list">
                  {w.requirements.map((r, i) => (
                    <li key={r}>
                      <Icon
                        name={
                          w.qa
                            ? "CircleCheck"
                            : i === 0
                              ? "CircleX"
                              : "CircleCheck"
                        }
                      />
                      <span>
                        <b>{r}</b>
                        <small>
                          {w.qa
                            ? "通过"
                            : i === 0
                              ? "待修复 / 独立重验"
                              : "已检查，版本变更后需核对适用性"}
                        </small>
                      </span>
                    </li>
                  ))}
                </ul>
                <Notice>
                  本原型的检查结果为模拟。正式产品必须使用平台独立运行与真实证据。
                </Notice>
                <div className="actions spaced">
                  <Button
                    icon="RotateCcw"
                    onClick={() => run("check")}
                    disabled={Boolean(rework.hold)}
                  >
                    开始独立验证
                  </Button>
                  <Button disabled={Boolean(rework.hold) || unfinishedBatches.length > 0 || !["待验收", "等待", "进行中"].includes(w.status) || !s.resources.tests.some(test => test.work === w.id && test.version === `v${w.version}` && test.status === "失败")} onClick={() => { setConfirm("repair"); setReason(""); }}>提交内部修复</Button>
                  <Button
                    disabled={Boolean(rework.hold) || unfinishedBatches.length > 0}
                    icon="CornerUpLeft"
                    onClick={() => {
                      setConfirm("return");
                      setReason("");
                    }}
                  >
                    退回修订
                  </Button>
                </div>
              </Section>
              <Section title={w.completionContract?.mode === "internal" ? "Human 接受与内部完成" : "Human 接受与正式交付"}>
                <p>
                  接受当前 v{w.version}
                  ，确认完成条件与交付材料。内部未通过时，批准动作会被阻止。
                </p>
                <div className="actions">
                  <Button onClick={() => run("accept")} disabled={w.accepted || Boolean(rework.hold)}>
                    接受当前版本
                  </Button>
                  {w.completionContract?.mode !== "internal" && <Button
                    variant="primary"
                    onClick={() => setConfirm("deliver")}
                    disabled={w.delivered || Boolean(rework.hold)}
                  >
                    {w.delivered ? "已交付，查看接收结果" : "批准正式交付"}
                  </Button>}
                </div>
              </Section>
            </>
          )}
          {tab === "activity" && (
            <Section title="完整活动记录">
              {(w.returnFeedback || []).map((feedback, index) => <div key={index} className="resource-card">
                <div><h3>退回 · {String(feedback.version).startsWith("v") ? feedback.version : `v${feedback.version}`}</h3>
                  <p>条款：{feedback.requirement || "未补充"} · 位置：{feedback.position || "未补充"}</p>
                  <p>期望：{feedback.expected || "未补充"}</p><p>实际：{feedback.actual}</p>
                </div>
              </div>)}
              <div className="timeline">
                {s.events
                  .filter((e) => e.target.includes(w.id))
                  .map((e) => (
                    <div key={e.id}>
                      <span className="timeline-dot" />
                      <small>
                        {formatTimestamp(e.time)} · {e.actor}
                      </small>
                      <p>{e.text}</p>
                    </div>
                  ))}
              </div>
            </Section>
          )}
        </div>
        <aside>
          <ReworkControl key={`${w.id}:${rework.revision}`} work={w} role={s.role} toast={toast} />
          <Section title="当前执行编组与上下文">
            <div className="person">
              <Avatar name={w.owner} />
              <div>
                <strong>{w.owner}</strong>
                <small>Human 结果负责人</small>
              </div>
            </div>
            {executors.map((person) => <div className="person" key={person.id}>
              <Avatar name={person.name} agent={person.kind === "Agent"} />
              <div><strong>{person.name}</strong><small>{person.kind} 当前执行者 · 有界授权</small></div>
            </div>)}
            {w.team && <dl>
              <dt>协调者</dt><dd>{memberName(w.team.coordinatorId)}</dd>
              <dt>检查者</dt><dd>{w.team.checkerIds.map(memberName).join("、")}</dd>
              <dt>下游 Human 接收人</dt><dd>{memberName(w.team.receiverId)}</dd>
            </dl>}
            <WorkTeamEditor work={w} toast={toast} />
            <Link className="btn full" to="/members">查看 Human 组织名册</Link>
            <dl>
              <dt>成果版本</dt>
              <dd>v{w.version}</dd>
              <dt>执行费用</dt>
              <dd>{formatCurrency(w.cost)}</dd>
              <dt>截止</dt>
              <dd>{w.plannedEnd || w.due}</dd>
              <dt>计划周期</dt>
              <dd>
                {w.plannedStart
                  ? `${w.plannedStart} — ${w.plannedEnd}`
                  : "未排期"}
              </dd>
              <dt>项目排期</dt>
              <dd>
                <Link
                  className="text-link"
                  to={`/p/${w.project}/work-items?view=calendar`}
                >
                  查看日历
                </Link>
              </dd>
              <dt>共享范围</dt>
              <dd>项目成员</dd>
            </dl>
            <div className="stack">
              <Button
                onClick={() => {
                  setConfirm("takeover");
                  setReason("");
                }}
                icon="Hand"
              >
                Human 接管
              </Button>
              <Button
                onClick={() => {
                  setConfirm("cancel");
                  setReason("");
                }}
                icon="CircleStop"
              >
                取消工作
              </Button>
              <Button
                icon="Download"
                onClick={() =>
                  exportData(w.id, {
                    work: w,
                    events: s.events.filter((e) => e.target.includes(w.id)),
                  })
                }
              >
                导出工作证据
              </Button>
            </div>
          </Section>
        </aside>
      </div>
      {confirm && (
        <Modal
          key={confirm}
          draftValue={{ reason, ...(confirm === "return" ? { ...returnDetails, requirement: returnDetails.requirement || w.requirements[0] } : {}) }}
          title={
            {
              return: "退回修订",
              repair: "提交内部修复",
              deliver: "确认正式交付",
              cancel: "取消工作",
              takeover: "Human 接管",
            }[confirm]
          }
          onClose={closeAction}
        >
          <ActionForm
            label="确认"
            onCancel={closeAction}
            onSubmit={({ dismiss }) => {
              workCommand(w.id, confirm, reason, w.version, confirm === "return" ? { ...returnDetails, requirement: returnDetails.requirement || w.requirements[0] } : undefined);
              dismiss();
              toast("决定已记录，相关视图同步更新");
            }}
          >
            <Notice tone="warn">
              目标：{w.title} · v{w.version}。
              {confirm === "deliver"
                ? "仅当内部验收和 Human 接受均有效时才能交付。"
                : "保留原版本和历史，受影响执行将按新状态处理。"}
            </Notice>
            {confirm === "repair" && <Notice>针对当前版本全部失败检查，提交一次实际修复。保存新成果版本，不重复计数；轮次由已结束且失败的固定验证批次累计，新版本仍须独立验证。</Notice>}
            {confirm === "return" && <>
              <Field label="未满足条款" value={returnDetails.requirement || w.requirements[0]} options={w.requirements}
                onChange={(requirement) => setReturnDetails({ ...returnDetails, requirement })} />
              {[['position', '成果位置'], ['expected', '期望结果'], ['actual', '实际结果']].map(([key, label]) => <Field
                key={key} label={label} value={returnDetails[key]} required
                onChange={(value) => setReturnDetails({ ...returnDetails, [key]: value })} />)}
              <Notice>反馈归属本工作 {w.id} 的 v{w.version}。尚未建立的上游依赖不会被自动退回。</Notice>
            </>}
            <Field
              label="理由与交接说明"
              value={reason}
              onChange={setReason}
              type="textarea"
              required={confirm !== "deliver"}
            />
          </ActionForm>
        </Modal>
      )}
    </>
  );
}
export function NewWork({ onClose, source, toast }) {
  useEffect(() => {
    try {
      const params = new URLSearchParams(location.hash.split("?")[1] || "");
      const projectId = location.hash.match(/^#\/p\/([^/]+)/)?.[1];
      const draftId = beginWorkDraft({ sourceId: source?.id, projectId: source?.projectId || projectId, channel: params.get("channel_id") || undefined,
        executionId: params.get("execution_id") || undefined, threadId: params.get("thread_id") || undefined });
      const draft = read().drafts[draftId];
      const conversation = draft.executionId ? `execution_id=${encodeURIComponent(draft.executionId)}` : `channel_id=${encodeURIComponent(draft.channel)}`;
      location.hash = `/chat?${conversation}&work_draft=${encodeURIComponent(draftId)}${draft.threadId ? `&thread_id=${encodeURIComponent(draft.threadId)}` : ""}`;
      toast("已进入会话工作草稿，补充并确认后生成正式工作");
    } catch (error) { toast(error.message, true); }
    onClose();
  }, []);
  return null;
}

export function ProjectList({ toast }) {
  const s = useStore();
  const humans = organizationPeople(s).filter(person => person.kind === "Human");
  const defaultOwnerId = humans.some(person => person.id === "H-lin") ? "H-lin" : "";
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [owner, setOwner] = useState(defaultOwnerId);
  const [draftTenant, setDraftTenant] = useState(s.activeTenant);
  const closeProject = () => { setOpen(false); setName(""); setDescription(""); setOwner(defaultOwnerId); };
  return (
    <>
      <PageHead
        title="多个项目，同一个工作台"
        subtitle="从目标、责任与交付开始，再组织合适的工作。"
      >
        <Button icon="Plus" variant="primary" onClick={() => { setDraftTenant(s.activeTenant); setOwner(defaultOwnerId); setOpen(true); }}>
          创建项目
        </Button>
      </PageHead>
      <div className="project-grid">
        {s.projects
          .filter((p) => s.project === "all" || s.project === p.id)
          .map((p) => (
            <Link
              key={p.id}
              to={`/p/${p.id}`}
              className={`project-card ${p.color}`}
            >
              <div className="project-monogram">
                <Icon name="Orbit" size={30} />
                <span>{p.type}</span>
              </div>
              <h2>{p.name}</h2>
              <p>{p.description}</p>
              <footer>
                <Avatar name={p.owner} />
                {p.owner}
                <small>{p.deadline}</small>
              </footer>
            </Link>
          ))}
      </div>
      {open && (
        <Modal title="创建一个有明确结果的项目" draftValue={{ name, description, owner }} onClose={closeProject}>
          <ActionForm
            label="创建项目"
            onCancel={closeProject}
            onSubmit={({ dismiss }) => {
              const key = createProject({ name, description, ownerId: owner, tenantId: draftTenant });
              dismiss();
              location.hash = `/p/${key}`;
              toast("项目已创建，下一步确定计划与交付要求");
            }}
          >
            <Field label="项目名称" value={name} onChange={setName} required />
            <Field
              label="目标与预期结果"
              value={description}
              onChange={setDescription}
              type="textarea"
              required
            />
            <Field
              label="Human 负责人"
              value={owner}
              onChange={setOwner}
              options={[{ value: "", label: "请选择当前租户 Human" }, ...humans.map(person => ({ value: person.id, label: person.name }))]}
              required
            />
          </ActionForm>
        </Modal>
      )}
    </>
  );
}
