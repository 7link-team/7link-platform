import { WorkCalendar } from "./pages-calendar";
import React, { useState, useEffect } from "react";
import {
  useStore,
  update,
  log,
  id,
  workCommand,
  visibleWork,
  assertWrite,
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
          value={`$${work.reduce((a, w) => a + w.cost, 0).toFixed(2)}`}
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
                      {w.owner} 负责 · {w.agent} 正在协作
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
                    {e.time} · {e.actor}
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
export function WorkList({ params, openNew, projectId, toast, viewPath }) {
  const s = useStore();
  const view = params.get("view") || "list";
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState("全部");
  const work = visibleWork(s).filter(
    (w) =>
      (!projectId || w.project === projectId) &&
      (!query || w.title.includes(query)) &&
      (filter === "全部" || w.status === filter),
  );
  const prefix = projectId
    ? `/p/${projectId}/work-items`
    : viewPath === "/collaboration"
      ? "/collaboration"
      : "/work";
  return (
    <>
      <PageHead
        title={projectId ? "项目工作" : "与我有关的工作"}
        subtitle="计划、执行、验收与交接，始终是同一件工作。"
      >
        <Button variant="primary" icon="Plus" onClick={openNew}>
          发起工作
        </Button>
      </PageHead>
      <div className="toolbar">
        <label className="search-field">
          <Icon name="Search" />
          <input
            aria-label="查找工作"
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
export function WorkDetail({ workId, params, toast }) {
  const s = useStore();
  const w = s.work.find(
    (x) => x.id === workId && (s.role !== "external" || x.delivered),
  );
  const [confirm, setConfirm] = useState(null);
  const [reason, setReason] = useState("");
  if (!w)
    return (
      <Empty title="工作不可访问" text="对象不存在，或当前视角没有访问权限。" />
    );
  const tab = params.get("tab") || "brief";
  const run = (cmd) => {
    try {
      workCommand(w.id, cmd, "", w.version);
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
        subtitle={`Human ${w.owner} 负责 · ${w.agent} 执行 · 当前成果 v${w.version}`}
      >
        <Badge>{w.status}</Badge>
        <Button
          icon="MessagesSquare"
          onClick={() => (location.hash = `/chat?execution_id=${w.id}`)}
        >
          进入执行会话
        </Button>
      </PageHead>
      <div className="stepper">
        {[
          ["目标", "已明确"],
          ["计划", w.plan ? "已确认" : "待确认"],
          ["SOP", w.flow ? "已发布" : "待确认"],
          ["内部验收", w.qa ? "通过" : "未通过"],
          ["Human 接受", w.accepted ? "已接受" : "待处理"],
          ["交付", w.delivered ? "已交付" : "待交接"],
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
                  {w.requirements.map((r, i) => (
                    <li key={r}>
                      <Icon name={w.qa ? "CircleCheck" : "Circle"} size={18} />
                      <span>
                        <small>REQ-{i + 1}</small>
                        {r}
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
                  {w.plan && !w.flow && (
                    <Button variant="primary" onClick={() => run("flow")}>
                      确认 SOP v1
                    </Button>
                  )}
                  {w.plan && w.flow && w.status === "待开始" && (
                    <Button variant="primary" onClick={() => run("start")}>
                      开始执行
                    </Button>
                  )}
                  {["进行中", "等待"].includes(w.status) && (
                    <Button variant="primary" onClick={() => run("submit")}>
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
              <Section title="环节交付契约">
                <dl>
                  <dt>上游输入</dt>
                  <dd>已确认简报 v2 / 品牌指南 v2</dd>
                  <dt>输出</dt>
                  <dd>
                    {w.type}成果 v{w.version}、源文件、检查证据
                  </dd>
                  <dt>小团队</dt>
                  <dd>
                    {w.owner} 负责 · {w.agent} 制作 · Verifier 独立检查
                  </dd>
                  <dt>下游接收方</dt>
                  <dd>项目负责人 / 交付环节</dd>
                  <dt>资源限制</dt>
                  <dd>$200 · 软件内部修复最多 5 轮 · 正式返工 3 次升级</dd>
                  <dt>退回规则</dt>
                  <dd>绑定版本和未满足条款，暂停受影响下游</dd>
                </dl>
                <Link className="btn" to={`/p/${w.project}/flow`}>
                  打开 工作流
                </Link>
              </Section>
              <Section title="确认基线">
                <div className="actions">
                  <Button disabled={w.plan} onClick={() => run("plan")}>
                    {w.plan ? "计划已确认" : "确认计划"}
                  </Button>
                  <Button
                    disabled={!w.plan || w.flow}
                    onClick={() => run("flow")}
                  >
                    {w.flow ? "SOP 已确认" : "确认 SOP"}
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
              <Section
                title={`v${w.version} · 内部质量门`}
                extra={<Badge>{w.qa ? "通过" : "失败"}</Badge>}
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
                    disabled={w.qa}
                  >
                    模拟独立复核通过
                  </Button>
                  <Button
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
              <Section title="Human 接受与正式交付">
                <p>
                  接受当前 v{w.version}
                  ，确认完成条件与交付材料。内部未通过时，批准动作会被阻止。
                </p>
                <div className="actions">
                  <Button onClick={() => run("accept")} disabled={w.accepted}>
                    接受当前版本
                  </Button>
                  <Button
                    variant="primary"
                    onClick={() => setConfirm("deliver")}
                    disabled={w.delivered}
                  >
                    {w.delivered ? "已正式交付" : "批准正式交付"}
                  </Button>
                </div>
              </Section>
            </>
          )}
          {tab === "activity" && (
            <Section title="完整活动记录">
              <div className="timeline">
                {s.events
                  .filter((e) => e.target.includes(w.id))
                  .map((e) => (
                    <div key={e.id}>
                      <span className="timeline-dot" />
                      <small>
                        {e.time} · {e.actor}
                      </small>
                      <p>{e.text}</p>
                    </div>
                  ))}
              </div>
            </Section>
          )}
        </div>
        <aside>
          <Section title="责任与上下文">
            <div className="person">
              <Avatar name={w.owner} />
              <div>
                <strong>{w.owner}</strong>
                <small>Human 结果负责人</small>
              </div>
            </div>
            <div className="person">
              <Avatar name={w.agent} agent />
              <div>
                <strong>{w.agent}</strong>
                <small>执行 Agent · 有界授权</small>
              </div>
            </div>
            <dl>
              <dt>成果版本</dt>
              <dd>v{w.version}</dd>
              <dt>执行费用</dt>
              <dd>${w.cost.toFixed(2)}</dd>
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
          title={
            {
              return: "退回修订",
              deliver: "确认正式交付",
              cancel: "取消工作",
              takeover: "Human 接管",
            }[confirm]
          }
          onClose={() => setConfirm(null)}
        >
          <ActionForm
            label="确认"
            onCancel={() => setConfirm(null)}
            onSubmit={() => {
              workCommand(w.id, confirm, reason, w.version);
              setConfirm(null);
              toast("决定已记录，相关视图同步更新");
            }}
          >
            <Notice tone="warn">
              目标：{w.title} · v{w.version}。
              {confirm === "deliver"
                ? "仅当内部验收和 Human 接受均有效时才能交付。"
                : "保留原版本和历史，受影响执行将按新状态处理。"}
            </Notice>
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
  const s = useStore();
  const [name, setName] = useState(source?.text || "");
  const [project, setProject] = useState(
    s.project === "all" ? "private" : s.project,
  );
  const [type, setType] = useState("软件");
  const [criteria, setCriteria] = useState(
    "结果可使用，内部验收通过后由 Human 接受。",
  );
  return (
    <Modal title="发起一件工作" onClose={onClose}>
      <ActionForm
        label="创建计划草稿"
        onCancel={onClose}
        onSubmit={() => {
          assertWrite();
          if (!name.trim()) throw Error("请描述需要完成的工作。");
          let created;
          update((x) => {
            if (
              project === "private" &&
              !x.projects.some((p) => p.id === "private")
            )
              x.projects.push({
                id: "private",
                name: "我的私人工作",
                type: "个人",
                owner: "林予",
                progress: 0,
                budget: 200,
                spent: 0,
                deadline: "2026-10-31",
                color: "violet",
                description: "仅自己可见的正式工作；共享前选择具体资料。",
              });
            const existing =
              source && x.work.find((w) => w.source === source.id);
            if (existing) {
              created = existing;
              return;
            }
            created = {
              id: id("W"),
              title: name.trim(),
              project,
              type,
              status: "待开始",
              owner: "林予",
              agent: "Atlas",
              due: "待确认",
              version: 1,
              plan: false,
              flow: false,
              qa: false,
              accepted: false,
              delivered: false,
              cost: 0,
              notes: "由会话与已知资料生成，等待计划和 SOP 确认。",
              requirements: [criteria],
              source: source?.id,
            };
            x.work.unshift(created);
            log(x, `创建工作草稿 · ${name}`, workPath(created));
          });
          onClose();
          location.hash = workPath(created);
          toast("草稿已建立，下一步确认计划与 SOP");
        }}
      >
        <Field
          label="想要完成什么"
          value={name}
          onChange={setName}
          type="textarea"
          required
        />
        <div className="form-grid">
          <Field
            label="归属项目"
            value={project}
            onChange={setProject}
            options={[
              { value: "private", label: "私人工作 · 仅自己" },
              ...s.projects
                .filter((x) => x.id !== "private")
                .map((x) => ({ value: x.id, label: x.name })),
            ]}
          />
          <Field
            label="专业类型"
            value={type}
            onChange={setType}
            options={["软件", "文档", "图片", "视频", "小说", "运营"]}
          />
        </div>
        <Field
          label="怎样才算完成"
          value={criteria}
          onChange={setCriteria}
          type="textarea"
          required
        />
        <Notice>
          先形成可核对的计划草稿。确认后才进入正式执行，创建不会自动授予工具权限。
        </Notice>
      </ActionForm>
    </Modal>
  );
}

export function ProjectList({ toast }) {
  const s = useStore();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [owner, setOwner] = useState("林予");
  return (
    <>
      <PageHead
        title="多个项目，同一个工作台"
        subtitle="从目标、责任与交付开始，再组织合适的工作。"
      >
        <Button icon="Plus" variant="primary" onClick={() => setOpen(true)}>
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
        <Modal title="创建一个有明确结果的项目" onClose={() => setOpen(false)}>
          <ActionForm
            label="创建项目"
            onCancel={() => setOpen(false)}
            onSubmit={() => {
              assertWrite();
              if (!name.trim()) throw Error("请填写项目名称。");
              const key = id("project");
              update((x) => {
                x.projects.push({
                  id: key,
                  name,
                  description,
                  owner,
                  type: "通用协作",
                  progress: 0,
                  budget: 0,
                  spent: 0,
                  deadline: "待确认",
                  color: "violet",
                });
                log(x, `创建项目 ${name}`, `/p/${key}`);
              });
              setOpen(false);
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
              options={["林予", "周宁", "许言"]}
            />
          </ActionForm>
        </Modal>
      )}
    </>
  );
}
