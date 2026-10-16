import { DataList } from "./data-list";
import { ConversationOriginButton } from "./conversation-origin.jsx";
import { ExecutionRegistry, WorkExecutionStart } from "./execution-ui";
import { TaskEffort } from "./pages-effort";
import { useViewState } from "./view-state";
import { activeProjects, projectIsArchived, projectLifecycleProblem, projectArchiveImpact, changeProjectArchive, pauseProjectWork, releaseProjectLease, resourceCommand } from "./store";
import { formatCurrency, formatTimestamp } from "./formatting";
import { WorkCalendar } from "./pages-calendar";
import React, { useState, useEffect, useRef, useCallback } from "react";
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
  cancelInputHandoff,
  beginWorkDraft,
  saveWorkDraft,
  confirmWorkDraft,
  workDraftReadProblem,
  organizationPeople,
  createProject,
  projectHumanOwner,
  projectMetadataWriteProblem,
  saveProjectMetadata,
  saveWorkTeam,
  workExecutors,
  workTeamMemberAvailable,
  visibleWork,
  visibleActivityEvents,
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
export const workPath = (w) => w.workKind === "BUG" ? `/defects/${w.id}` : `/p/${w.project}/work-items/${w.id}`;
export const artifactPath = (w) => `/p/${w.project}/artifacts/${w.id}`;
export function WorkRows({ items, showProject = false }) {
  const s = useStore();
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
              {showProject && `${s.projects.find(p => p.id === w.project)?.name || "项目归属待核实"} · `}{w.id} · {w.type} · v{w.version}
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
          value={activeProjects(s)
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
              {activeProjects(s)
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
              {visibleActivityEvents(s).slice(0, 5).map((e) => (
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
  const view = ["board", "calendar"].includes(params.get("view")) ? params.get("view") : "list";
  const [query, setQuery] = useViewState("work-search", "");
  const [filter, setFilter] = useViewState("work-status", "全部");
  const scopeKey = `${s.activeTenant}:${projectId || s.project}:${s.role}:${viewPath || ""}:${workKind || "TASK"}`;
  const [list, setList] = useViewState(`task-list.${scopeKey}`, {});
  const [detailId, setDetailId] = useViewState(`task-detail.${scopeKey}`, "");
  const [detailTab, setDetailTab] = useViewState(`task-detail-tab.${scopeKey}.${detailId}`, "brief");
  const [, refreshView] = useState(0);
  const tenantId = s.activeTenant, role = s.role, scope = s.project;
  const authorizedRows = useCallback(current => visibleWork({...current, project:projectId || current.project}, {includeArchived:Boolean(projectId)}).filter(w =>
    (viewPath !== "/work" || w.owner === current.organization.members.find(p=>p.id==="H-lin")?.name || [w.team?.ownerId,w.team?.coordinatorId,w.team?.receiverId,...(w.team?.executorIds||[]),...(w.team?.checkerIds||[])].includes("H-lin")) &&
    (!workKind || w.workKind === workKind) && (!projectId || w.project === projectId)), [projectId, viewPath, workKind]);
  const rows = authorizedRows(s);
  const searchText = w => `${w.id} ${w.title} ${w.owner || ""} ${w.type || ""}`;
  const work = rows.filter(w => searchText(w).toLocaleLowerCase().includes(query.toLocaleLowerCase()) && (filter === "全部" || w.status === filter));
  const selected = rows.find(w => w.id === detailId);
  const changeList = patch => {
    if (Object.hasOwn(patch, "query")) setQuery(patch.query);
    setList(previous => ({...previous, ...patch}));
  };
  const columns = [
    {key:"title", label:"任务", value:w=>w.title, exportValue:w=>w.title,
      render:w=><><Button onClick={()=>setDetailId(w.id)}>{w.title}</Button><small>{w.id}</small></>},
    {key:"project", label:"项目", value:w=>s.projects.find(p=>p.id===w.project)?.name || w.project, exportValue:w=>w.project},
    {key:"type", label:"类型", value:w=>w.type, exportValue:w=>w.type},
    {key:"status", label:"状态", value:w=>w.status, exportValue:w=>w.status, render:w=><Badge>{w.status}</Badge>},
    {key:"owner", label:"负责人", value:w=>w.owner, exportValue:w=>w.owner},
    {key:"due", label:"截止日期", value:w=>w.due, exportValue:w=>w.due},
    {key:"version", label:"版本", value:w=>w.version, exportValue:w=>w.version}
  ];
  const recheckRows = useCallback(() => {
    const current = read();
    if (current.activeTenant !== tenantId || current.role !== role || current.project !== scope)
      throw Error("任务列表授权范围已变化，请重新核对。");
    return authorizedRows(current);
  }, [tenantId, role, scope, authorizedRows]);
  const refresh = useCallback(() => { recheckRows(); refreshView(value=>value+1); }, [recheckRows]);
  const authorizeExport = useCallback(payload => {
    const current = recheckRows();
    if (!payload.records.every(record => {
      const row = current.find(item=>item.id===record.id);
      return row && columns.every(column=>JSON.stringify(record[column.key])===JSON.stringify(column.exportValue(row) ?? null));
    })) throw Error("任务或授权已变化，请刷新后重新导出。");
    return true;
  }, [recheckRows, columns]);
  const taskStatuses = ["待开始","进行中","等待","待验收","待交接","已完成","已取消"];
  const statusFilter = <Field label="任务状态" value={filter} onChange={value=>{setFilter(value);changeList({page:1});}}
    options={["全部", ...taskStatuses].map(value=>({value,label:value}))} />;
  const prefix = workKind === "BUG" ? "/defects" : projectId
    ? `/p/${projectId}/work-items`
    : viewPath === "/collaboration"
      ? "/collaboration"
      : "/work";
  const viewLink = next => { const queryParams = new URLSearchParams(params); queryParams.set("view", next); return `${prefix}?${queryParams}`; };
  return (
    <>
      <PageHead
        title={workKind === "BUG" ? "缺陷" : "任务"}
        subtitle={viewPath === "/collaboration" ? "按当前项目范围查看任务；每项任务保留原项目归属。" : "计划、执行、验收与交接，始终是同一件工作。"}
      >
        <div className="work-view-actions">
          <div className="segmented">
            <Link
              to={viewLink("list")}
              className={view === "list" ? "selected" : ""}
            >
              <Icon name="List" />
              列表
            </Link>
            <Link
              to={viewLink("board")}
              className={view === "board" ? "selected" : ""}
            >
              <Icon name="Columns3" />
              看板
            </Link>
            <Link
              to={viewLink("calendar")}
              className={view === "calendar" ? "selected" : ""}
            >
              <Icon name="CalendarDays" />
              日历
            </Link>
          </div>
          <Button variant="primary" icon="Plus" onClick={openNew}>
            {workKind === "BUG" ? "发起缺陷" : "发起任务"}
          </Button>
        </div>
      </PageHead>
      {view !== "list" && <div className="toolbar">
        <Field label="搜索任务" value={query} onChange={setQuery} />
        {statusFilter}
      </div>}
      {view === "calendar" ? (
        <WorkCalendar work={work} toast={toast} projectId={projectId} />
      ) : view === "board" ? (
        <div className="kanban">
          {taskStatuses.map(
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
                    <button type="button" className="kanban-card" onClick={()=>setDetailId(w.id)} key={w.id} style={{width:"100%",textAlign:"left",font:"inherit",color:"inherit"}}>
                      <small>
                        {viewPath === "/collaboration" && `${s.projects.find(p => p.id === w.project)?.name || "项目归属待核实"} · `}{w.id} · {w.type}
                      </small>
                      <h3>{w.title}</h3>
                      <p>{w.notes}</p>
                      <footer>
                        <Avatar name={w.owner} />
                        <span>{w.owner}</span>
                        <small>v{w.version}</small>
                      </footer>
                    </button>
                  ))}
              </section>
            ),
          )}
        </div>
      ) : (
        <DataList rows={rows} columns={columns} title="任务列表" viewId="tasks" scopeKey={scopeKey}
          queryState={{...list, query}} onQueryStateChange={changeList} defaultSort="title"
          searchText={searchText} filterRow={w=>filter === "全部" || w.status === filter} filters={statusFilter}
          onClearFilters={()=>setFilter("全部")} onRefresh={refresh} offline={s.offline} editing={Boolean(selected)}
          onExport={authorizeExport} exportSource={`tasks:${scopeKey}`}
          renderActions={w=><Button onClick={()=>setDetailId(w.id)}>查看任务</Button>}
          emptyText="当前条件下没有任务，可清空条件恢复。" />
      )}
      {detailId && <Modal title="任务详情" wide onClose={()=>setDetailId("")}
        headerActions={selected && <Link className="btn" to={`${workPath(selected)}?tenant=${encodeURIComponent(s.activeTenant)}&scope=${encodeURIComponent(projectId || s.project)}&tab=${encodeURIComponent(detailTab)}`} target="_blank" rel="noopener noreferrer">在新标签页打开任务</Link>}>
        {selected ? <div onClickCapture={event=>{
          const anchor=event.target.closest('a[href^="#/"]');
          if (!anchor) return;
          const [path, queryString=""] = anchor.getAttribute("href").slice(1).split("?");
          const nextParams = new URLSearchParams(queryString);
          if (path === workPath(selected) && nextParams.has("tab") && !event.metaKey && !event.ctrlKey && !event.shiftKey && !event.altKey && event.button === 0) {
            event.preventDefault(); event.stopPropagation(); setDetailTab(nextParams.get("tab"));
          } else { anchor.target="_blank"; anchor.rel="noopener noreferrer"; }
        }}>
          <WorkDetail key={`${scopeKey}:${selected.id}`} workId={selected.id} params={new URLSearchParams({tab:detailTab})} toast={toast} embedded />
        </div> : <Empty title="任务不存在或已不在当前授权范围" />}
      </Modal>}
    </>
  );
}
export function Project({ projectId, openNew, toast = () => {} }) {
  const s = useStore();
  const p = s.projects.find((x) => x.id === projectId);
  const [settings, setSettings] = useState(null);
  const [lifecycle,setLifecycle] = useState(null);
  if (!p) return <Empty title="项目不存在" />;
  return (
    <>
      <PageHead
        eyebrow={`协作 / ${p.type}`}
        title={p.name}
        subtitle={p.description}
      >
        {p.id !== "private" && s.role !== "external" && <Button icon="Settings2" onClick={() => setSettings({tenantId:s.activeTenant,project:structuredClone(p)})}>项目设置</Button>}
        {p.id !== "private" && s.role !== "external" && <Button disabled={Boolean(projectLifecycleProblem(s,p))} title={projectLifecycleProblem(s,p)} onClick={() => setLifecycle({projectId:p.id,tenantId:s.activeTenant,revision:p.revision ?? 1,action:p.archivedAt ? "restore" : "archive"})}>{p.archivedAt ? "恢复项目" : "归档项目"}</Button>}
        <Button disabled={Boolean(p.archivedAt)} icon="Plus" variant="primary" onClick={openNew}>
          添加工作
        </Button>
      </PageHead>
      <div className="project-banner">
        <span className="project-orbit">
          <Icon name="Orbit" size={64} />
        </span>
        <div className="grow">
          <Badge tone={p.archivedAt ? "amber" : "green"}>{p.archivedAt ? "已归档 · 历史只读" : "项目进行中"}</Badge>
          <h2>从共同目标，到共同成果。</h2>
          <p>
            {p.owner} 负责 · 截止 {p.deadline} · 共享范围清楚，协作自然发生
          </p>
        </div>
        <Link className="btn" to={`/chat?channel_id=${encodeURIComponent(p.id)}`}>
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
          <WorkRows items={visibleWork({...s,project:"all"},{includeArchived:true}).filter((w) => w.project === p.id)} />
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
      {p.archivedAt && <Notice>项目于 {formatTimestamp(p.archivedAt)} 归档。原工作、成果、审批和资料保持原权限与身份；恢复不会自动重启。</Notice>}
      {(p.history || []).filter(event=>["ARCHIVE","RESTORE"].includes(event.kind)).map((event,index)=><p className="muted" key={index}>{event.kind === "ARCHIVE" ? "归档" : "恢复"} · {event.actorId} · {formatTimestamp(event.time)} · {event.reason} · 本地模拟</p>)}
      {lifecycle && <ProjectArchiveDialog snapshot={lifecycle} action={lifecycle.action} onClose={()=>setLifecycle(null)} toast={toast} />}
      {settings && <ProjectMetadataEditor key={`${settings.tenantId}:${settings.project.id}:${settings.project.revision ?? 1}`} snapshot={settings} onClose={() => setSettings(null)} onReload={() => setSettings({tenantId:s.activeTenant,project:structuredClone(p)})} toast={toast} />}
    </>
  );
}
export function ProjectArchiveDialog({ snapshot, action, onClose, toast }) {
  const s = useStore(), [reason,setReason] = useState("");
  const project = s.projects.find(item => item.id === snapshot.projectId);
  const impact = projectArchiveImpact(s,snapshot.projectId);
  const problem = snapshot.tenantId !== s.activeTenant ? "租户已切换，请重新打开。" : projectLifecycleProblem(s,project);
  const stale = project && (project.revision ?? 1) !== snapshot.revision;
  const perform = fn => { try { fn(); } catch(error) { toast(error.message,true); } };
  return <Modal title={action === "archive" ? "归档项目 · 核对影响" : "恢复项目"} draftValue={reason} onClose={onClose} wide>
    <ActionForm label={action === "archive" ? "确认归档项目" : "确认恢复项目"} onCancel={onClose} onSubmit={({dismiss}) => {
      changeProjectArchive({...snapshot,action,reason,impactFingerprint:JSON.stringify(impact.items)});dismiss();toast(action === "archive" ? "项目已归档，历史保留" : "项目已恢复，执行与资源保持停止");
    }}>
      <Notice>{action === "archive" ? "归档独立于完成或取消，不会替你取消工作或处理审批。持续资源须有明确的本地处置记录，未处理项会阻止归档。" : "恢复保留原项目身份、权限与历史；不会重启工作、调度、租约或恢复已撤销授权。后续启用仍需分别确认。"}</Notice>
      {(problem || stale) && <Notice tone="warn">{problem || "项目修订已变化，请关闭并重新打开核对。"}</Notice>}
      <Field label={action === "archive" ? "归档与处置理由" : "恢复理由"} type="textarea" required value={reason} onChange={setReason} />
      {action === "archive" && <Section title={`影响清单 · ${impact.blockers.length} 项待处理`}>
        {impact.items.length ? impact.items.map(item => <div className="resource-card" key={`${item.kind}:${item.id}`}><div className="grow"><h3>{item.title}</h3><p>{item.status} · {item.blocked ? "尚未满足归档条件" : "已核对 / 保留历史"}</p><Link to={item.href}>查看原记录</Link>
          {item.blocked && item.action && <Button disabled={Boolean(problem || stale)} onClick={() => perform(() => {
            if (!reason.trim()) throw Error("请先填写处置理由。");
            if (item.action === "pause-work") { const work=s.work.find(work=>work.id===item.id);pauseProjectWork({tenantId:snapshot.tenantId,workId:item.id,version:work.version,reason}); }
            if (item.action === "pause-schedule") resourceCommand("schedules",item.id,"暂停",reason);
            if (item.action === "release-lease") { const lease=s.projectLeases.find(lease=>lease.id===item.id); const result=releaseProjectLease({tenantId:snapshot.tenantId,leaseId:item.id,revision:lease.revision,reason}); if(result!=="RELEASED") throw Error("节点未确认，本地模拟释放失败；仍不能归档。"); }
          })}>{item.action === "pause-work" ? "明确暂停推进（本地模拟）" : item.action === "pause-schedule" ? "暂停周期调度" : "释放本地模拟租约"}</Button>}
        </div></div>) : <p>当前没有已绑定该项目的持续工作或资源。</p>}
        <Notice>{impact.disclosure} 未明确项目 ID 的资料 {impact.unknownKnowledge} 项，不据此声明项目资料已全部覆盖。</Notice>
      </Section>}
    </ActionForm>
  </Modal>;
}

export function ProjectMetadataEditor({ snapshot, onClose, onReload, toast }) {
  const s = useStore(), original = snapshot.project;
  const [form,setForm] = useState(() => ({ name: original.name || "", description: original.description || "", ownerId: projectHumanOwner(s,original)?.id || "", deadline: original.deadline === "待确认" ? "" : original.deadline || "" }));
  const current = s.projects.find(project => project.id === original.id);
  const switched = snapshot.tenantId !== s.activeTenant;
  const problem = switched ? "租户已切换，请关闭后在当前租户重新打开项目设置。" : projectMetadataWriteProblem(s,current);
  const recordedHistory = Array.isArray(current?.history) ? current.history : [];
  const histories = recordedHistory.filter(item=>item?.kind === "METADATA" && item.previous && item.next && typeof item.previous === "object" && typeof item.next === "object" && !Array.isArray(item.previous) && !Array.isArray(item.next));
  const historyCorrupt = current?.history != null && (!Array.isArray(current.history) || recordedHistory.some(item=>!item || typeof item !== "object" || Array.isArray(item) || item.kind === "METADATA" && !histories.includes(item)));
  const stale = current && (current.revision ?? 1) !== (original.revision ?? 1);
  const humans = organizationPeople(s).filter(person => person.kind === "Human");
  const change = (key,value) => setForm(previous => ({...previous,[key]:value}));
  const fields = <>
    <Field label="项目名称" value={form.name} onChange={value=>change("name",value)} maxLength={200} required disabled={!!problem} />
    <Field label="项目简介" value={form.description} onChange={value=>change("description",value)} type="textarea" maxLength={2000} disabled={!!problem} help="最多 2000 字，可以留空。" />
    <Field label="Human 负责人" value={form.ownerId} onChange={value=>change("ownerId",value)} options={[{value:"",label:"请选择当前租户 Human"},...humans.map(person=>({value:person.id,label:`${person.name} · ${person.id}`}))]} required disabled={!!problem} />
    <Field label="截止日期" type="date" value={form.deadline} onChange={value=>change("deadline",value)} disabled={!!problem} help="可保留已过期的真实日期；清空表示待确认。" />
    <Notice>修改项目负责人只更新项目资料；已有工作的负责人、编组、计划和执行记录保持原归属。</Notice>
  </>;
  return <Modal title="项目设置 · 常规资料" onClose={onClose} draftValue={form}>
    {switched || !current ? <Notice tone="warn">{problem}</Notice> : <>
      <p>项目 <span translate="no">{original.id}</span> · 资料 v{original.revision ?? 1}</p>
      {stale && <Notice tone="warn">项目资料已更新；旧表单不会覆盖当前版本。<Button onClick={onReload}>重新打开最新资料</Button></Notice>}
      {problem ? <><Notice tone="warn">{problem}</Notice>{fields}</> : <ActionForm label="保存项目资料" onCancel={onClose} onSubmit={({dismiss})=>{
        saveProjectMetadata({...form,projectId:original.id,tenantId:snapshot.tenantId,revision:original.revision ?? 1});
        dismiss();toast("项目资料已保存，关联工作保持原归属");
      }}>{fields}</ActionForm>}
      {historyCorrupt && <Notice tone="warn">项目历史格式异常；保存将被阻止，不会覆盖原始历史。</Notice>}
      <details><summary>项目资料变更历史（{histories.length}）</summary>
        {histories.slice().reverse().map((entry,index)=><div key={index}>
          <b>v{entry.revision} → v{entry.revision + 1} · {formatTimestamp(entry.time)} · {entry.actorId}</b>
          <dl>{[["name","名称"],["description","简介"],["owner","负责人"],["deadline","截止日期"]].map(([key,label])=><React.Fragment key={key}><dt>{label}</dt><dd style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{entry.previous[key] || "未记录"}{key === "owner" ? `（${entry.previous.ownerId || "原身份未记录"}）` : ""} → {entry.next[key] || "未填写"}{key === "owner" ? `（${entry.next.ownerId || "身份未记录"}）` : ""}</dd></React.Fragment>)}</dl>
        </div>)}
      </details>
    </>}
  </Modal>;
}
function CompletionContract({ work, toast, compact = false }) {
  const [open,setOpen]=useState(false);
  const [mode, setMode] = useState(work.completionContract?.mode || "");
  const [recipient, setRecipient] = useState(work.completionContract?.recipient || "");
  const [reason, setReason] = useState(work.completionContract?.reason || "");
  const s=useStore(), [snapshot,setSnapshot]=useState({tenantId:s.activeTenant,version:work.version}), writable=!work.accepted&&!projectIsArchived(s,work.project)&&s.role==="owner"&&!s.offline;
  const summary=work.completionContract?.mode==="internal"?"内部使用，不需要外部回执":work.completionContract?.mode==="external"?`交付给 ${work.completionContract.recipient}`:"尚未明确交给谁、是否需要外部回执";
  const content=<><p>{summary}</p><Button disabled={!writable} onClick={()=>{setSnapshot({tenantId:s.activeTenant,version:work.version});setMode(work.completionContract?.mode||"");setRecipient(work.completionContract?.recipient||"");setReason(work.completionContract?.reason||"");setOpen(true);}}>{work.completionContract?"修改交付方式":"设置交付方式"}</Button>{work.accepted&&<small>当前成果已接受，交付方式已固定。</small>}</>;
  return <>
    {compact?<div className="delivery-summary">{content}</div>:<Section title="交付方式" extra={<Badge>{work.completionContract?"已确认":"待负责人确认"}</Badge>}>{content}<p className="muted">由有权负责人在接受成果前确认。内部质量检查和 Human 接受仍然必需。</p></Section>}
    {open&&<Modal title="确认交付方式" onClose={()=>setOpen(false)} draftValue={{mode,recipient,reason}}>
      <ActionForm label="保存交付方式" onCancel={()=>setOpen(false)} onSubmit={({dismiss})=>{if(read().activeTenant!==snapshot.tenantId)throw Error("租户已切换，请重新打开当前工作的交付方式。");workCommand(work.id,"contract","",snapshot.version,{mode,recipient,reason});dismiss();toast("交付方式已保存；没有接受成果或向外发送。");}}>
        <p>工作由 {work.owner} 负责；有权负责人确认固定 v{snapshot.version} 的交付去向。</p>
        <Field label="完成与接收方式" value={mode} disabled={!writable} required options={[{value:"",label:"请选择交付去向"},{value:"internal",label:"内部使用，无外部接收方"},{value:"external",label:"指定接收人交付"}]} onChange={setMode}/>
        {mode==="internal"&&<Field label="内部使用说明" value={reason} onChange={setReason} required disabled={!writable} help="说明谁使用成果、为什么不需要外部交付回执。"/>}
        {mode==="external"&&<Field label="指定接收人" value={recipient} onChange={setRecipient} required disabled={!writable} help="正式交付后由此接收方核对交付包并记录回执。"/>}
        <Notice>保存只确定交付方式。通过内部检查后仍须有权 Human 接受成果；对外交付另行批准，内部使用不等于允许公开发布。</Notice>
      </ActionForm>
    </Modal>}
  </>;
}
function WorkHandoffs({ work, toast }) {
  const s=useStore(), [selected,setSelected]=useState(null), [reason,setReason]=useState("");
  const packs=s.work.flatMap(source=>(source.handoffs || []).filter(pack=>source.id===work.id || pack.targetWorkId===work.id).map(pack=>({source,pack})));
  if (!packs.length) return null;
  return <Section title="内部交接包">
    {packs.map(({source,pack})=><div key={pack.id}><p>{source.title} → {s.work.find(item=>item.id===pack.targetWorkId)?.title || "接收工作不可核对"} · v{pack.version} · {pack.status}</p>
      <details><summary>查看固定材料与处置历史</summary>{pack.items.map(item=><p key={item.id}>{item.name || item.id} · {item.reference}</p>)}{(pack.history || []).map((event,index)=><p key={index}>{event.action || event.status} · {event.actorId || event.actor} · {event.reason}</p>)}</details>
      {pack.status === "待接收" && <Button disabled={projectIsArchived(s,source.project) || s.role === "external"} onClick={()=>{setSelected({tenantId:s.activeTenant,sourceId:source.id,handoffId:pack.id,version:pack.version,fingerprint:JSON.stringify(pack.items)});setReason("");}}>终止此待交接包</Button>}
    </div>)}
    {selected && <Modal title="终止待交接包" draftValue={reason} onClose={()=>setSelected(null)}><ActionForm label="确认终止交接" onCancel={()=>setSelected(null)} onSubmit={({dismiss})=>{cancelInputHandoff({...selected,reason});dismiss();toast("待交接已终止，固定材料与历史保留");}}><Notice>只终止此待接收包，不取消上下游工作，不改写已接受材料；需要重新交接时另建固定包。</Notice><Field label="终止交接理由" type="textarea" value={reason} onChange={setReason} required /></ActionForm></Modal>}
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
      {s.work.some((item) => item.id === input.workId) && <Link className="btn" target="_blank" rel="noopener noreferrer" to={`/p/${s.work.find((item) => item.id === input.workId).project}/artifacts/${input.workId}?version=${input.version}`}>查看输入 {index + 1} 固定成果</Link>}
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
      <Button onClick={()=>{try{const draftId=beginWorkDraft({scopeChangeFrom:work.id,scopeChangeReason:reason});const params=new URLSearchParams(location.hash.split("?")[1]||"");params.set("quick_work",draftId);location.hash=`${location.hash.slice(1).split("?")[0]}?${params}`;setError("");}catch(e){setError(e.message);}}}>按范围变化起草关联任务</Button>
      {error && <p role="alert">{error}</p>}
    </>}
    <details><summary>返工事件与 Human 决定</summary>
      {control.events.map((event, i) => <p key={`event-${i}`}>{event.kind === "internal" ? `失败验证轮 ${event.round} · v${event.version} · ${event.key || "历史记录"}` : event.kind === "repair" ? `修复提交 · v${event.version} → v${event.submittedVersion}` : event.kind === "formal" ? `正式退回 · ${event.key}` : `声明链 ${event.previousChain} 结束 · 内部 ${event.previousInternal} 轮`} · {event.reason}</p>)}
      {control.decisions.map((decision, i) => <p key={`decision-${i}`}>{decision.action} · {decision.reason}{decision.additionalRounds && ` · 本次额外 ${decision.additionalRounds} 轮`} · {formatTimestamp(decision.time)}</p>)}
      {!control.events.length && !control.decisions.length && <p className="muted">尚无返工事件。</p>}
    </details>
    <p className="muted">环境故障在执行记录中核对安全重试；范围变化创建关联原工作的独立草稿，两者均不增加原工作的质量返工计数。</p>
  </Section>;
}
function openDefectDraft(toast, options = {}) {
  try { const draftId = beginWorkDraft({ workKind: "BUG", ...options }); const params=new URLSearchParams(location.hash.split("?")[1]||"");params.set("quick_work",draftId);location.hash=`${location.hash.slice(1).split("?")[0]}?${params}`; }
  catch (error) { toast(error.message, true); }
}
export function LegacyDefectRoute({ work, ...props }) {
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

export function WorkDetail({ workId, params, toast, embedded = false }) {
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
  const archived = projectIsArchived(s,w.project);
  const rework = workRework(w);
  const unfinishedBatches = pendingFailedBatches(s, w);
  const executors = workExecutors(s, w);
  const people = organizationPeople(s);
  const memberName = (id) => people.find((person) => person.id === id)?.name || "成员已不可用";
  const tab = params.get("tab") || "brief";
  const run = (cmd) => {
    try {
      const href = workCommand(w.id, cmd, "", w.version);
      if (href) { if (embedded) window.open(`#${href}`, "_blank", "noopener,noreferrer"); else location.hash = href; }
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
        {w.workKind === "BUG" && !embedded && <Link className="btn" to="/defects">返回缺陷列表</Link>}
        {s.role !== "external" && <Link className="btn" to={`${workPath(w)}?tab=executions`} {...(embedded ? {target:"_blank",rel:"noopener noreferrer"}: {})}>查看执行记录</Link>}
      </PageHead>
      {archived && <Notice>所属项目已归档，此工作仅供查看历史。<Link to={`/p/${w.project}`}>查看项目及恢复入口</Link></Notice>}
      {w.scopeChange&&<Notice>范围变化来源：<Link to={`/p/${w.scopeChange.projectId}/work-items/${w.scopeChange.workId}?version=${w.scopeChange.version}`}>{w.scopeChange.workId} · v{w.scopeChange.version}</Link> · {w.scopeChange.reason}。原工作与返工累计保留。</Notice>}
      <WorkHandoffs work={w} toast={toast} />
      {w.production?.version === w.version && !w.production.executorIds?.length && <Notice tone="warn">此成果版本未记录执行者，无法核对独立检查是否与执行者分离。请在责任区配置执行小团队，再提交新成果并重新验证；修改编组不会补造旧版本的执行身份。</Notice>}
      {dependencyProblems(s, w).length > 0 && <Notice tone="warn">
        输入失效，当前工作暂停并等待采用新输入、重新验收。
        {dependencyProblems(s, w).map((block, index) => <p key={index}>{block.inputWorkId} · v{block.inputVersion}：{block.reason}</p>)}
        <Link to={`${workPath(w)}?tab=plan`}>处理固定输入依赖</Link>
      </Notice>}
      {unfinishedBatches.length > 0 && <Notice tone="warn">提交新成果前，请先结束失败验证批次并形成固定报告。{unfinishedBatches.map(batch => <p key={batch.batchId}><Link to={`/testplans/${batch.planId}`}>{batch.needsNewBatch ? "历史批次未记录轮次，请启动并完成新的验证批次" : "打开测试计划，结束失败批次"} · {batch.batchId}</Link></p>)}</Notice>}
      {rework.hold && <Notice tone="warn">返工已停线：{rework.hold.reason}。请在“修复与正式返工”中记录 Human 决定。</Notice>}
      {w.workKind === "BUG" && <fieldset disabled={archived} style={{border:0,padding:0,margin:0}}><DefectPanel work={w} toast={toast} /></fieldset>}
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
          ["executions", "执行记录"],
          ["review", "检查与接受"],
          ["analysis", "投入与运行分析"],
          ["activity", "活动记录"],
        ].filter(([v])=>v!=="executions" || s.role!=="external").map(([v, l]) => (
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
          {tab === "executions" && s.role !== "external" && <ExecutionRegistry key={`${s.activeTenant}-${w.id}`} work={w} toast={toast} />}
          {tab === "brief" && (
            <>
              <CompletionContract key={w.id} work={w} toast={toast} />
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
                    <Button disabled={archived} variant="primary" onClick={() => run("plan")}>
                      确认计划 v1
                    </Button>
                  )}
                  {w.plan && (!w.flow || !w.flowVersion) && (
                    <Button disabled={archived} variant="primary" onClick={() => run("flow")}>
                      确认工作流版本
                    </Button>
                  )}
                  {w.plan && w.flow && w.status === "待开始" && (
                    <WorkExecutionStart work={w} archived={archived} toast={toast} onHumanStart={()=>run("start")} />
                  )}
                  {["进行中", "等待"].includes(w.status) && (
                    <Button variant="primary" disabled={archived || Boolean(rework.hold) || unfinishedBatches.length > 0} onClick={() => run("submit")}>
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
              <fieldset disabled={archived} style={{border:0,padding:0,margin:0}}><CompletionContract key={w.id} work={w} toast={toast} /></fieldset>
              <fieldset disabled={archived} style={{border:0,padding:0,margin:0}}><InputContract key={`${w.id}:${w.inputsRevision || 0}`} work={w} toast={toast} /></fieldset>
              {w.draftId && <Section title="会话确认的计划">
                <p>{w.planSnapshot?.goal}</p><p>完成条件：{w.planSnapshot?.criteria}</p>
                <ConversationOriginButton record={w} label="查看来源会话与草稿" />
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
                  <Button disabled={archived || w.plan} onClick={() => run("plan")}>
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
              <fieldset disabled={archived} style={{border:0,padding:0,margin:0}}><CompletionContract key={w.id} work={w} toast={toast} /></fieldset>
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
                    disabled={archived || Boolean(rework.hold)}
                  >
                    开始独立验证
                  </Button>
                  <Button disabled={archived || Boolean(rework.hold) || unfinishedBatches.length > 0 || !["待验收", "等待", "进行中"].includes(w.status) || !s.resources.tests.some(test => test.work === w.id && test.version === `v${w.version}` && test.status === "失败")} onClick={() => { setConfirm("repair"); setReason(""); }}>提交内部修复</Button>
                  <Button
                    disabled={archived || Boolean(rework.hold) || unfinishedBatches.length > 0}
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
                  <Button onClick={() => run("accept")} disabled={archived || w.accepted || Boolean(rework.hold)}>
                    接受当前版本
                  </Button>
                  {w.completionContract?.mode !== "internal" && <Button
                    variant="primary"
                    onClick={() => setConfirm("deliver")}
                    disabled={archived || w.delivered || Boolean(rework.hold)}
                  >
                    {w.delivered ? "已交付，查看接收结果" : "批准正式交付"}
                  </Button>}
                </div>
              </Section>
            </>
          )}
          {tab === "analysis" && s.role !== "external" && <TaskEffort key={w.id} work={w} toast={toast} />}
          {tab === "activity" && (
            <Section title="完整活动记录">
              {(w.lifecycleHistory || []).map((event,index)=><p key={`lifecycle-${index}`}>{event.action} · {event.actorId} · {formatTimestamp(event.time)} · {event.reason} · 本地模拟</p>)}
              {(w.returnFeedback || []).map((feedback, index) => <div key={index} className="resource-card">
                <div><h3>退回 · {String(feedback.version).startsWith("v") ? feedback.version : `v${feedback.version}`}</h3>
                  <p>条款：{feedback.requirement || "未补充"} · 位置：{feedback.position || "未补充"}</p>
                  <p>期望：{feedback.expected || "未补充"}</p><p>实际：{feedback.actual}</p>
                </div>
              </div>)}
              <div className="timeline">
                {visibleActivityEvents(s)
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
          {(s.projectLeases || []).some(lease => lease.workId === w.id) && <Section title="持续资源 · 本地模拟">
            <p className="muted">此处管理租约；不代表实际进程或隧道已启动、释放。</p>
            {s.projectLeases.filter(lease => lease.workId === w.id).map(lease => <div key={lease.id}><p>{lease.kind === "terminal" ? "终端" : "预览"} · {lease.state} · {lease.resourceId}</p>
              {lease.state !== "RELEASED" && <Button onClick={() => { try { const result=releaseProjectLease({tenantId:s.activeTenant,leaseId:lease.id,revision:lease.revision,reason:"Human 明确释放此工作本地模拟资源"});toast(result === "RELEASED" ? "本地模拟释放已记录" : "释放未确认，可在节点恢复在线后重试",result !== "RELEASED"); } catch(error) {toast(error.message,true);} }}>释放本地模拟资源</Button>}
            </div>)}
          </Section>}
          <fieldset disabled={archived} style={{border:0,padding:0,margin:0}}><ReworkControl key={`${w.id}:${rework.revision}`} work={w} role={s.role} toast={toast} /></fieldset>
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
            <fieldset disabled={archived} style={{border:0,padding:0,margin:0}}><WorkTeamEditor work={w} toast={toast} /></fieldset>
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
                disabled={archived}
                onClick={() => {
                  setConfirm("takeover");
                  setReason("");
                }}
                icon="Hand"
              >
                Human 接管
              </Button>
              <Button
                disabled={archived}
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
                    events: visibleActivityEvents(s).filter((e) => e.target.includes(w.id)),
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
export function ConversationWorkDraft({ draft, toast, inline = false, onConfirmed, editorFields, onEditorChange }) {
  const s = useStore();
  const [localFields, setLocalFields] = useState(() => workDraftFields(draft));
  const fields = editorFields || localFields, setFields = onEditorChange || setLocalFields;
  const [editingRevision] = useState(draft.revision);
  const change = (key, value) => setFields((old) => ({ ...old, [key]: value, ...(key === "project" ? {workflowId:"default"} : {}) }));
  const save = () => { saveWorkDraft(draft.id, fields, editingRevision); };
  const readProblem=workDraftReadProblem(s,draft);
  if(readProblem)return <Notice tone="warn">{readProblem}</Notice>;
  if (draft.workId) return <Section title="会话工作草稿已确认"><Link target="_blank" rel="noopener noreferrer" to={workPath(s.work.find((work) => work.id === draft.workId))}>打开生成的工作与计划</Link></Section>;
  return <Section title={inline?"任务要求":"会话工作草稿"} extra={<Badge>讨论中 · 尚未生成工作</Badge>}>
    <Notice>{inline?"确认后会在当前任务列表新增任务，并保存计划草稿；执行前仍需确认工作流版本。":"在下方会话补充资料、讨论要求；核对草稿后，由 Human 确认生成工作与计划。"}</Notice>
    {draft.scopeChange&&<Notice>新范围独立草稿 · 原工作 {draft.scopeChange.workId} / v{draft.scopeChange.version} · {draft.scopeChange.reason}。确认不会修改或终止原任务。</Notice>}
    {draft.source && <p>来源消息：{draft.source.text}</p>}
    <Field label="想要完成什么" value={fields.title} onChange={(value) => change("title", value)} type="textarea" required />
    <div className="form-grid">
      <Field disabled={inline&&s.project!=="all"} label="归属项目" value={fields.project} onChange={(value) => change("project", value)} options={[{ value: "private", label: "私人工作 · 仅自己" }, ...activeProjects(s).filter((project) => project.id !== "private").map((project) => ({ value: project.id, label: project.name }))]} />
      <Field label="专业类型" value={fields.type} onChange={(value) => change("type", value)} options={draft.workKind === "BUG" ? ["软件"] : ["软件", "文档", "图片", "视频", "小说", "运营"]} />
    </div>
    <Field label="任务使用的工作流" value={fields.workflowId} onChange={value => change("workflowId",value)} options={[{value:"default",label:"项目交付流程"},...(s.projectWorkflows?.[fields.project] || []).filter(item=>!item.archivedAt).map(item=>({value:item.id,label:`${item.name} · ${item.flow.flowVersion ? `v${item.flow.flowVersion}` : "待发布"}`}))]} help="Human 确认任务后，执行前须锁定所选工作流的已发布版本；讨论和保存不会启动执行。"/>
    {draft.workKind === "BUG" && <fieldset><legend>缺陷专业信息</legend>
      <Field label="受影响工作" value={fields.defect.affectedWorkId || ""} onChange={value => setFields(old => ({ ...old, defect: { ...old.defect, affectedWorkId: value, affectedVersion: `v${s.work.find(work => work.id === value)?.version || 1}` } }))} options={[{ value: "", label: "请选择受影响工作" }, ...s.work.filter(work => work.project === fields.project && work.workKind !== "BUG").map(work => ({ value: work.id, label: `${work.id} · ${work.title}` }))]} />
      {[["affectedVersion", "影响版本"], ["severity", "严重度"], ["priority", "优先级"], ["steps", "复现步骤"], ["expected", "期望结果"], ["actual", "实际结果"], ["environment", "环境"], ["commit", "关联提交"]].map(([key, label]) => <Field key={key} label={label} value={fields.defect[key] || ""} onChange={value => setFields(old => ({ ...old, defect: { ...old.defect, [key]: value } }))} type={["steps", "expected", "actual"].includes(key) ? "textarea" : "text"} />)}
    </fieldset>}
    <Field label="怎样才算完成" value={fields.criteria} onChange={(value) => change("criteria", value)} type="textarea" required />
    <div className="actions">
      <Button onClick={() => { try { save(); toast(inline?"任务草稿已保存，可继续编辑":"会话草稿已保存，可继续讨论"); } catch (error) { toast(error.message, true); } }}>{inline?"保存任务草稿":"保存会话草稿"}</Button>
      <Button variant="primary" onClick={() => {
        try { save(); const work = confirmWorkDraft(draft.id, editingRevision + 1); toast(inline?"任务与计划草稿已创建，已保留当前列表。":"已确认生成工作与计划，保留当前会话；执行前请确认工作流版本"); onConfirmed?.(work); }
        catch (error) { toast(error.message, true); }
      }}>确认生成工作与计划</Button>
    </div>
    <small>{draft.discussion.length} 条讨论已关联到草稿；确认不会自动授予工具或发布权限。</small>
  </Section>;
}
function workDraftFields(draft) {
  return { title:draft.title, project:draft.project, type:draft.type, criteria:draft.criteria, workflowId:draft.workflowId || "default", defect:structuredClone(draft.defect || {}) };
}
function WorkDraftModal({draft,toast,onClose}) {
  const [fields,setFields,guard]=useRecoverableDraft(JSON.stringify([draft.tenantId,"task-draft",draft.id,draft.revision]),()=>workDraftFields(draft));
  return <Modal title="发起任务" wide onClose={onClose} {...guard.modalProps}><ConversationWorkDraft inline draft={draft} toast={toast} editorFields={fields} onEditorChange={setFields} onConfirmed={onClose}/></Modal>;
}
export function NewWork({ onClose, source, toast, draftId:requestedDraftId }) {
  const s=useStore();
  const [draftId,setDraftId]=useState(requestedDraftId||new URLSearchParams(location.hash.split("?")[1]||"").get("quick_work"));
  const [problem,setProblem]=useState("");
  const [tenantId]=useState(s.activeTenant);
  const initialized=useRef(false);
  useEffect(()=>{
    if(draftId||initialized.current)return;
    initialized.current=true;
    try {
      const params=new URLSearchParams(location.hash.split("?")[1]||"");
      const projectId=source?.projectId||(s.project!=="all"?s.project:undefined);
      const created=beginWorkDraft({sourceId:source?.id,projectId,channel:params.get("channel_id")||undefined,executionId:params.get("execution_id")||undefined,threadId:params.get("thread_id")||undefined,workflowId:params.get("workflow_id")||"default"});
      setDraftId(created);params.set("quick_work",created);location.hash=`${location.hash.slice(1).split("?")[0]}?${params}`;
    } catch(error){setProblem(error.message);}
  },[draftId]);
  const close=()=>{const params=new URLSearchParams(location.hash.split("?")[1]||"");params.delete("quick_work");const path=location.hash.slice(1).split("?")[0];onClose();location.hash=`${path}${params.size?`?${params}`:""}`;};
  const draft=s.activeTenant===tenantId?s.drafts?.[draftId]:null;
  const readProblem=workDraftReadProblem(s,draft);
  if (!problem && !readProblem) return <WorkDraftModal key={`${draft.id}:${draft.revision}`} draft={draft} toast={toast} onClose={close}/>;
  return <Modal title="发起任务" wide onClose={close}>{problem||readProblem?<Notice tone="warn">{problem||readProblem}</Notice>:<Empty title="任务草稿不可访问" text="草稿不存在或租户已切换，请关闭后重新发起。"/>}</Modal>;
}

export function ProjectList({ toast, params = new URLSearchParams() }) {
  const archive = ["active","archived","all"].includes(params.get("archive")) ? params.get("archive") : "active";
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
      <Field label="项目范围" value={archive} options={[{value:"active",label:"进行中"},{value:"archived",label:"已归档"},{value:"all",label:"全部"}]} onChange={value=>{location.hash=`/projects?archive=${value}`;}} />
      <div className="project-grid">
        {s.projects
          .filter((p) => (s.project === "all" || s.project === p.id) && (archive === "all" || (archive === "archived" ? Boolean(p.archivedAt) : !p.archivedAt)))
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
              <h2>{p.name}</h2>{p.archivedAt && <Badge>已归档 · 历史只读</Badge>}
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
