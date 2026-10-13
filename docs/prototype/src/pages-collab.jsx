import { formatTimestamp } from "./formatting";
import { TaskPreviews } from "./task-previews";
import { messageProject } from "./message-sharing";
import React, { useEffect, useState, useRef, useId } from "react";
import {
  useStore,
  update,
  log,
  id,
  exportData,
  assertWrite,
  sharedVersions,
  sharedArtifactPath,
  saveWorkDraft,
  confirmWorkDraft,
  workExecutors,
  shareMessage,
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
  Link,
  Modal,
  Field,
  ActionForm,
  Empty,
  Notice,
  PanelButton,
  Tabs,
} from "./ui";
import { workPath, artifactPath, NewWork } from "./pages-work";
const channels = [
  ["team", "协作总频道", "跨项目 · 组织范围"],
  ["launch", "新品发布", "6 位成员 · 4 位 Agent"],
  ["external", "客户共创", "外部共享 · 范围受限"],
  ["private", "Quill", "私人会话 · 仅参与者"],
];
function ConversationWorkDraft({ draft, toast }) {
  const s = useStore();
  const [fields, setFields] = useState(() => ({ title: draft.title, project: draft.project, type: draft.type, criteria: draft.criteria, defect: structuredClone(draft.defect || {}) }));
  const change = (key, value) => setFields((old) => ({ ...old, [key]: value }));
  const save = () => { saveWorkDraft(draft.id, fields, draft.revision); };
  if (draft.workId) return <Section title="会话工作草稿已确认"><Link to={workPath(s.work.find((work) => work.id === draft.workId))}>打开生成的工作与计划</Link></Section>;
  return <Section title="会话工作草稿" extra={<Badge>讨论中 · 尚未生成工作</Badge>}>
    <Notice>在下方会话补充资料、讨论要求；核对草稿后，由 Human 确认生成工作与计划。</Notice>
    {draft.source && <p>来源消息：{draft.source.text}</p>}
    <Field label="想要完成什么" value={fields.title} onChange={(value) => change("title", value)} type="textarea" required />
    <div className="form-grid">
      <Field label="归属项目" value={fields.project} onChange={(value) => change("project", value)} options={[{ value: "private", label: "私人工作 · 仅自己" }, ...s.projects.filter((project) => project.id !== "private").map((project) => ({ value: project.id, label: project.name }))]} />
      <Field label="专业类型" value={fields.type} onChange={(value) => change("type", value)} options={draft.workKind === "BUG" ? ["软件"] : ["软件", "文档", "图片", "视频", "小说", "运营"]} />
    </div>
    {draft.workKind === "BUG" && <fieldset><legend>缺陷专业信息</legend>
      <Field label="受影响工作" value={fields.defect.affectedWorkId || ""} onChange={value => setFields(old => ({ ...old, defect: { ...old.defect, affectedWorkId: value, affectedVersion: `v${s.work.find(work => work.id === value)?.version || 1}` } }))} options={[{ value: "", label: "请选择受影响工作" }, ...s.work.filter(work => work.project === fields.project && work.workKind !== "BUG").map(work => ({ value: work.id, label: `${work.id} · ${work.title}` }))]} />
      {[["affectedVersion", "影响版本"], ["severity", "严重度"], ["priority", "优先级"], ["steps", "复现步骤"], ["expected", "期望结果"], ["actual", "实际结果"], ["environment", "环境"], ["commit", "关联提交"]].map(([key, label]) => <Field key={key} label={label} value={fields.defect[key] || ""} onChange={value => setFields(old => ({ ...old, defect: { ...old.defect, [key]: value } }))} type={["steps", "expected", "actual"].includes(key) ? "textarea" : "text"} />)}
    </fieldset>}
    <Field label="怎样才算完成" value={fields.criteria} onChange={(value) => change("criteria", value)} type="textarea" required />
    <div className="actions">
      <Button onClick={() => { try { save(); toast("会话草稿已保存，可继续讨论"); } catch (error) { toast(error.message, true); } }}>保存会话草稿</Button>
      <Button variant="primary" onClick={() => {
        try { save(); const work = confirmWorkDraft(draft.id, draft.revision + 1); location.hash = workPath(work); toast("已确认生成工作与计划，执行前请确认 SOP"); }
        catch (error) { toast(error.message, true); }
      }}>确认生成工作与计划</Button>
    </div>
    <small>{draft.discussion.length} 条讨论已关联到草稿；确认不会自动授予工具或发布权限。</small>
  </Section>;
}
export function Chat({ params, toast, onView }) {
  const s = useStore();
  const layoutRef = useRef(null);
  const resizeDrag = useRef(null);
  const contextId = useId();
  const [layoutWidth, setLayoutWidth] = useState(() => window.innerWidth);
  const [previewWidth, setPreviewWidth] = useState(() => {
    try {
      const value = Number(localStorage.getItem("collaboration-preview-width"));
      return Number.isFinite(value) && value >= 320 ? value : 420;
    } catch { return 420; }
  });
  const [resizing, setResizing] = useState(false);
  const maxPreviewWidth = Math.max(320, layoutWidth - 245 - 360 - 8);
  const actualPreviewWidth = Math.round(Math.min(maxPreviewWidth, Math.max(320, previewWidth)));
  const clampPreviewWidth = width => Math.round(Math.min(maxPreviewWidth, Math.max(320, width)));
  const persistPreviewWidth = width => {
    setPreviewWidth(width);
    try { localStorage.setItem("collaboration-preview-width", String(width)); } catch { /* Reading and resizing remain available when storage is full. */ }
  };
  const channel = params.get("channel_id") || "launch";
  const execution = params.get("execution_id");
  const w = s.work.find((x) => x.id === execution);
  const thread = params.get("thread_id");
  const workDraft = s.drafts?.[params.get("work_draft")];
  const conversationPath = execution
    ? `/chat?execution_id=${encodeURIComponent(execution)}`
    : `/chat?channel_id=${encodeURIComponent(channel)}`;
  const contextProject = s.projects.find((p) =>
    channel === "team" ? p.id === s.project : p.id !== "private" && p.id === channel,
  );
  const contextWork =
    channel === "private"
      ? []
      : s.work.filter((item) =>
          contextProject
            ? item.project === contextProject.id
            : channel === "team" && s.project === "all",
        );
  const title = execution
    ? w?.title || "执行不存在"
    : channels.find((c) => c[0] === channel)?.[1] || contextProject?.name || "会话不存在";
  const [message, setMessage] = useState("");
  const [newWork, setNewWork] = useState(null);
  const [sharing, setSharing] = useState(null);
  const [messageEdit, setMessageEdit] = useState(null);
  const [editText, setEditText] = useState("");
  const [shareTarget, setShareTarget] = useState("项目成员");
  const [shareProject, setShareProject] = useState("");
  const [context, setContext] = useState(false);
  const [compactContext, setCompactContext] = useState(
    () => window.matchMedia("(max-width: 1250px)").matches,
  );
  useEffect(() => {
    const media = window.matchMedia("(max-width: 1250px)");
    const change = () => setCompactContext(media.matches);
    media.addEventListener("change", change);
    return () => media.removeEventListener("change", change);
  }, []);
  useEffect(() => {
    const layout = layoutRef.current;
    if (!layout) return;
    const observer = new ResizeObserver(entries => setLayoutWidth(entries[0].contentRect.width));
    observer.observe(layout);
    return () => observer.disconnect();
  }, [execution, compactContext, s.role]);
  useEffect(() => {
    if (resizeDrag.current) {
      setPreviewWidth(resizeDrag.current.width);
      resizeDrag.current = null;
    }
    setResizing(false);
  }, [execution, compactContext]);
  const [attachment, setAttachment] = useState("");
  useEffect(() => {
    setMessage(
      localStorage.getItem(
        `draft-${s.activeTenant}-${s.role}-${execution || channel}`,
      ) || "",
    );
    setAttachment("");
  }, [channel, execution, s.role, s.activeTenant]);
  const write = (v) => {
    setMessage(v);
    localStorage.setItem(
      `draft-${s.activeTenant}-${s.role}-${execution || channel}`,
      v,
    );
  };
  const messages = s.messages.filter(
    (m) =>
      m.channel === (execution ? `exec-${execution}` : channel) &&
      (!thread || m.id === thread || m.thread === thread),
  );
  const send = (e) => {
    e.preventDefault();
    if (s.role === "external" && (execution || channel !== "external")) {
      toast("此会话不在共享范围。", true);
      return;
    }
    if (s.offline) {
      toast("离线中，草稿已保留。恢复连接后请重新发送。", true);
      return;
    }
    if (!message.trim() && !attachment) return;
    update((x) => {
      const messageId = id("m");
      x.messages.push({
        id: messageId,
        channel: execution ? `exec-${execution}` : channel,
        author: s.role === "external" ? "陈曦 · 客户" : "林予",
        text: message.trim(),
        thread: thread || null,
        file: attachment,
        time: new Date().toISOString(),
        kind: "human",
        ...(workDraft && !workDraft.workId ? { workDraft: workDraft.id } : {}),
      });
      if (workDraft && !workDraft.workId) x.drafts[workDraft.id].discussion.push({ messageId, text: message.trim(), file: attachment });
    });
    write("");
    setAttachment("");
    toast("消息已保存");
  };
  const contextContent = (
    <>
      {s.role === "external" ? (
        <>
          <h2>已共享的成果</h2>
          <p className="muted">只展示接收范围内的固定交付。</p>
          {s.work
            .filter((w) => sharedVersions(s, w.id).length > 0)
            .map((w) => (
              <Link
                key={w.id}
                className="context-work"
                to={sharedArtifactPath(s, w)}
              >
                <b>{w.title}</b>
                <small>共享版本 v{sharedVersions(s, w.id).at(-1)}</small>
              </Link>
            ))}
          <Link to="/deliveries" className="btn full">
            交付与回执
          </Link>
        </>
      ) : execution && w ? (
        <TaskPreviews
          key={`${s.activeTenant}-${w.id}`}
          work={w}
          expanded={compactContext}
          toast={toast}
        />
      ) : (
        <>
          <h2>
            {channel === "private"
              ? "私人会话"
              : contextProject?.name ||
                (channel === "team" ? "跨项目协作" : "会话上下文")}
          </h2>
          {channel === "private" ? (
            <Notice>仅参与者可见。共享前请确认具体消息与接收范围。</Notice>
          ) : contextWork.length ? (
            <>
              <div className="context-stat">
                <b>
                  {contextWork.filter((item) => item.accepted).length}
                  <small> / {contextWork.length}</small>
                </b>
                <span>已接受的工作</span>
              </div>
              <Progress
                value={
                  (contextWork.filter((item) => item.accepted).length /
                    contextWork.length) *
                  100
                }
              />
              <h3>当前需要关注</h3>
              {contextWork
                .filter((item) => item.status === "待验收")
                .map((item) => (
                  <Link
                    to={workPath(item)}
                    className="context-work"
                    key={item.id}
                  >
                    <Badge>{item.status}</Badge>
                    <b>{item.title}</b>
                    <small>{item.owner} 负责</small>
                  </Link>
                ))}
            </>
          ) : (
            <Empty title="暂无关联工作" text="当前会话范围内还没有工作记录。" />
          )}
          {contextProject && (
            <Link to={`/p/${contextProject.id}/flow`} className="btn full">
              查看工作流与交接
            </Link>
          )}
        </>
      )}
    </>
  );
  if (s.role === "external" && (channel !== "external" || execution))
    return (
      <Empty title="此会话不在共享范围" text="客户只可访问已开放的会话与成果。">
        <Link className="btn" to="/chat?channel_id=external">
          进入共享频道
        </Link>
      </Empty>
    );
  return (
    <div ref={layoutRef} className={`chat-layout${execution && !compactContext ? " resizable-preview" : ""}${resizing ? " is-resizing" : ""}`}
      style={execution && !compactContext ? { "--task-preview-width": `${actualPreviewWidth}px` } : undefined}>
      <aside className="channel-list">
        <header>
          <h2>会话</h2>
          <Icon name="MessagesSquare" />
        </header>
        <span className="label">协作频道</span>
        {[channels[0], ...s.projects.filter(project => project.id !== "private").map(project => [project.id, project.name, "项目成员频道"]), channels[2]]
          .filter((c) => s.role !== "external" || c[0] === "external")
          .map(([key, name, sub]) => (
            <PanelButton
              onView={onView}
              key={key}
              to={`/chat?channel_id=${key}`}
              className={channel === key && !execution ? "active" : ""}
            >
              <Icon name={key === "external" ? "Globe2" : "Hash"} />
              <div>
                <b>{name}</b>
                <small>{sub}</small>
              </div>
            </PanelButton>
          ))}
        {s.role !== "external" && <span className="label">私信</span>}
        <PanelButton
          onView={onView}
          to="/chat?channel_id=private"
          className={channel === "private" ? "active" : ""}
          style={s.role === "external" ? { display: "none" } : undefined}
        >
          <Avatar name="Quill" agent />
          <div>
            <b>Quill</b>
            <small>第三章 · 等待你的意见</small>
          </div>
        </PanelButton>
        <span className="label">执行会话</span>
        {s.work
          .filter((w) => s.role !== "external" && w.status !== "已完成")
          .slice(0, 3)
          .map((w) => (
            <PanelButton
              onView={onView}
              key={w.id}
              to={`/chat?execution_id=${w.id}`}
              className={execution === w.id ? "active" : ""}
            >
              <Icon name="Terminal" />
              <div>
                <b>{w.title}</b>
                <small>
                  {w.status} · 当前执行 {workExecutors(s, w).map((person) => person.name).join("、") || "未分派"}
                </small>
              </div>
            </PanelButton>
          ))}
        <footer>
          <Icon name="LockKeyhole" />
          <span>
            消息永久保存
            <br />
            资料共享遵守当前权限
          </span>
        </footer>
      </aside>
      <section className="conversation">
        <header className="chat-heading">
          <div>
            <h1>{title}</h1>
            <small>
              {execution
                ? `${w?.agent} · 执行记录 · 当前成果 v${w?.version}`
                : channel === "private"
                  ? "私人会话 · 共享前预览资料"
                  : channel === "external"
                    ? "外部共享频道 · 仅开放的历史与附件"
                    : "Human 与 Agent，一起把工作推进"}
            </small>
          </div>
          <IconButton
            name="PanelRight"
            label="切换会话上下文"
            onClick={() => setContext(!context)}
          />
        </header>
        {execution && w ? (
          <div className="execution-strip">
            <Badge>{w.status}</Badge>
            <span>
              最近有效事件：当前版本 {w.qa ? "内部检查通过" : "等待复核"}
            </span>
            <Link to={workPath(w)} className="text-link">
              查看工作
            </Link>
          </div>
        ) : (
          <div className="pinned">
            <Icon name="Pin" size={14} />
            <span>
              {s.role === "external"
                ? "仅显示明确共享的信息与确认事项。"
                : "本周目标：确认视觉方向，完成发布资料交付。"}
            </span>
          </div>
        )}
        <div className="messages">
          <div className="day-divider">今天 · 10 月 13 日</div>
          {execution && (
            <>
              <div className="run-step">
                <Icon name="CheckCircle2" />
                <div>
                  <b>输入与权限已校验</b>
                  <p>简报 v2、品牌指南 v2、当前工作目录；执行级模型配置。</p>
                </div>
                <small>09:01</small>
              </div>
              <div className="run-step">
                <Icon name="FileCode2" />
                <div>
                  <b>阶段成果已登记</b>
                  <p>产物固定到 v{w?.version}，原执行记录保留。</p>
                </div>
                <small>09:08</small>
              </div>
              <details className="log-fold">
                <summary>工具与事件记录 · 展开完整过程</summary>
                <pre>
                  09:01 context.snapshot · saved
                  <br />
                  09:02 runtime.started · workspace isolated
                  <br />
                  09:06 artifact.saved · version {w?.version}
                  <br />
                  09:10 verification · {w?.qa ? "passed" : "requires review"}
                </pre>
              </details>
              <Notice tone="warn">
                模拟执行记录。真实模型调用、工具进程和云端连接将在接口接入后提供。
              </Notice>
            </>
          )}
          {workDraft && workDraft.tenantId === s.activeTenant && s.role !== "external" && <ConversationWorkDraft key={workDraft.id} draft={workDraft} toast={toast} />}
          {messages.map((m) => (
            <article className="message" key={m.id}>
              <Avatar name={m.author} agent={m.kind === "agent"} />
              <div className="message-main">
                <header>
                  <strong>{m.author}</strong>
                  {m.kind === "agent" && (
                    <span className="agent-tag">AGENT</span>
                  )}
                  <time>{formatTimestamp(m.time)}</time>
                </header>
                <p>
                  {m.recalled ? "此消息已撤回，原记录依审计政策保留。" : m.text}
                </p>
                {m.pinned && <Badge tone="violet">已置顶</Badge>}
                {m.history?.length > 0 && (
                  <small>已编辑 · {m.history.length} 次修订</small>
                )}
                {m.file && (
                  <div className="attachment">
                    <Icon name="Paperclip" />
                    {m.file}
                    <small>本地文件引用</small>
                  </div>
                )}
                {m.work && (
                  <Link
                    className="message-work"
                    to={workPath(s.work.find((w) => w.id === m.work))}
                  >
                    <Icon name="Layers" />
                    <div>
                      <b>{s.work.find((w) => w.id === m.work)?.title}</b>
                      <small>固定版本 · 工作详情</small>
                    </div>
                    <Icon name="ArrowUpRight" />
                  </Link>
                )}
                <div className="message-actions">
                  <Button
                    icon="ThumbsUp"
                    onClick={() => {
                      if (s.offline)
                        return toast("离线中，请重连后操作。", true);
                      update((x) => {
                        const msg = x.messages.find((a) => a.id === m.id);
                        msg.reacted = !msg.reacted;
                      });
                    }}
                  >
                    {m.reacted ? "已赞同" : "赞同"}
                  </Button>
                  <Button
                    hidden={s.role === "external"}
                    icon="Ellipsis"
                    onClick={() => {
                      setMessageEdit(m);
                      setEditText(m.text);
                    }}
                  >
                    更多
                  </Button>
                  <Button
                    hidden={s.role === "external"}
                    onClick={() => setNewWork({ ...m, projectId: w?.project || contextProject?.id || (channel === "private" ? "private" : undefined) })}
                    icon="ListPlus"
                  >
                    转为工作
                  </Button>
                  <Link
                    className="btn"
                    to={`${conversationPath}&thread_id=${encodeURIComponent(m.id)}`}
                  >
                    <Icon name="MessagesSquare" />
                    回复线程
                  </Link>
                  <Button
                    hidden={s.role === "external"}
                    onClick={() => {
                      setSharing({ ...m, tenantId: s.activeTenant });
                      setShareTarget("项目成员");
                      setShareProject(messageProject(s, m)?.id || "");
                    }}
                    icon="Share2"
                  >
                    共享
                  </Button>
                </div>
              </div>
            </article>
          ))}
          {!messages.length && !execution && (
            <Empty title="开始这段协作" text="发送目标、资料或一个具体问题。" />
          )}
          {thread && (
            <div className="thread-panel">
              <div className="split">
                <h3>
                  线程 · {s.messages.find((m) => m.id === thread)?.author}
                </h3>
                <Link to={conversationPath} className="btn">
                  返回频道
                </Link>
              </div>
              <p>{s.messages.find((m) => m.id === thread)?.text}</p>
              <Notice>回复将保留当前线程引用。</Notice>
            </div>
          )}
        </div>
        <Form className="composer" onSubmit={send}>
          <textarea
            aria-label="消息内容"
            name="message-content" autoComplete="off"
            placeholder={
              execution ? "补充要求、询问进展或提交反馈…" : `发送到 ${title}…`
            }
            value={message}
            onChange={(e) => write(e.target.value)}
            onKeyDown={(e) => {
              if ((e.ctrlKey || e.metaKey) && e.key === "Enter") send(e);
            }}
          />
          <div className="composer-bottom">
            <label className="attach-control" title="添加附件">
              <Icon name="Paperclip" />
              <input
                type="file"
                aria-label="添加消息附件"
                onChange={(e) => setAttachment(e.target.files?.[0]?.name || "")}
              />
            </label>
            <span>{attachment || "明确 @ 才会唤醒 Agent"}</span>
            <Button type="submit" variant="primary" icon="ArrowUp">
              发送
            </Button>
          </div>
        </Form>
      </section>
      {execution && !compactContext && <div className="preview-resizer" role="separator" tabIndex={0}
        aria-label="调整任务预览宽度" aria-orientation="vertical" aria-controls={contextId}
        aria-valuemin={320} aria-valuemax={Math.round(maxPreviewWidth)} aria-valuenow={actualPreviewWidth}
        aria-valuetext={`预览宽度 ${actualPreviewWidth} 像素`}
        title="拖动调整预览宽度；左右方向键微调，Home / End 调到最窄 / 最宽，双击恢复默认"
        onPointerDown={event => {
          if (event.button !== 0) return;
          event.preventDefault(); event.currentTarget.focus();
          resizeDrag.current = { x: event.clientX, width: actualPreviewWidth, pointerId: event.pointerId };
          event.currentTarget.setPointerCapture(event.pointerId); setResizing(true);
        }}
        onPointerMove={event => {
          const drag = resizeDrag.current;
          if (drag && drag.pointerId === event.pointerId) setPreviewWidth(clampPreviewWidth(drag.width + drag.x - event.clientX));
        }}
        onPointerUp={event => {
          const drag = resizeDrag.current;
          if (!drag || drag.pointerId !== event.pointerId) return;
          persistPreviewWidth(clampPreviewWidth(drag.width + drag.x - event.clientX));
          resizeDrag.current = null; setResizing(false);
          event.currentTarget.releasePointerCapture(event.pointerId);
        }}
        onPointerCancel={() => {
          if (resizeDrag.current) setPreviewWidth(resizeDrag.current.width);
          resizeDrag.current = null; setResizing(false);
        }}
        onLostPointerCapture={() => { resizeDrag.current = null; setResizing(false); }}
        onDoubleClick={() => persistPreviewWidth(clampPreviewWidth(420))}
        onKeyDown={event => {
          if (event.key === "Escape" && resizeDrag.current) {
            event.preventDefault(); const drag = resizeDrag.current;
            setPreviewWidth(drag.width); resizeDrag.current = null; setResizing(false);
            event.currentTarget.releasePointerCapture(drag.pointerId); return;
          }
          const width = { ArrowLeft: actualPreviewWidth + (event.shiftKey ? 64 : 24), ArrowRight: actualPreviewWidth - (event.shiftKey ? 64 : 24), Home: 320, End: maxPreviewWidth }[event.key];
          if (width !== undefined) { event.preventDefault(); persistPreviewWidth(clampPreviewWidth(width)); }
        }}><span aria-hidden="true" /></div>}
      {compactContext ? (
        context && (
          <Modal
            title={execution ? "任务预览 · 本地示例" : "协作上下文"}
            className={execution ? "preview-dialog" : ""}
            onClose={() => setContext(false)}
          >
            {contextContent}
          </Modal>
        )
      ) : (
        <aside className={`chat-context${execution ? " execution-preview" : ""}`} id={contextId}>
          {!execution && <span className="eyebrow">协作上下文</span>}
          {contextContent}
        </aside>
      )}

      {newWork && (
        <NewWork
          source={newWork}
          toast={toast}
          onClose={() => setNewWork(null)}
        />
      )}
      {messageEdit && (
        <Modal title="消息与来源记录" key={messageEdit.id} draftValue={editText} onClose={() => setMessageEdit(null)}>
          {({ requestClose, dismiss }) => <>
          <Field
            label="消息内容"
            value={editText}
            onChange={setEditText}
            type="textarea"
          />
          <Notice>
            编辑与撤回追加历史，普通撤回不物理删除记录。只有自己的消息可以修改。
          </Notice>
          <div className="actions">
            <Button
              onClick={() => {
                try {
                  assertWrite();
                  if (messageEdit.author !== "林予")
                    throw Error("只能编辑自己发送的消息。");
                  if (!editText.trim()) throw Error("消息不能为空。");
                  update((x) => {
                    const m = x.messages.find((v) => v.id === messageEdit.id);
                    m.history ??= [];
                    m.history.push({
                      text: m.text,
                      time: new Date().toISOString(),
                    });
                    m.text = editText;
                    log(x, `编辑消息 ${m.id}`, `/chat?channel_id=${m.channel}`);
                  });
                  dismiss();
                  toast("修订已保存");
                } catch (e) {
                  toast(e.message, true);
                }
              }}
            >
              保存修订
            </Button>
            <Button
              onClick={() => requestClose(() => {
                try {
                  assertWrite();
                  update((x) => {
                    const m = x.messages.find((v) => v.id === messageEdit.id);
                    m.pinned = !m.pinned;
                  });
                  setMessageEdit(null);
                  toast("置顶状态已更新");
                } catch (e) {
                  toast(e.message, true);
                }
              })}
            >
              切换置顶
            </Button>
            <Button
              onClick={() => requestClose(() => {
                try {
                  assertWrite();
                  if (messageEdit.author !== "林予")
                    throw Error("只能撤回自己发送的消息。");
                  update((x) => {
                    x.messages.find((v) => v.id === messageEdit.id).recalled =
                      true;
                    log(x, `撤回消息 ${messageEdit.id}`);
                  });
                  setMessageEdit(null);
                  toast("已撤回，历史保留");
                } catch (e) {
                  toast(e.message, true);
                }
              })}
            >
              撤回并保留历史
            </Button>
            <Button
              onClick={() => requestClose(() => {
                try {
                  assertWrite();
                  update((x) => {
                    x.resources.knowledge.unshift({
                      id: id("K"),
                      name: messageEdit.text.slice(0, 24),
                      status: "待确认",
                      owner: "林予",
                      kind: "记忆",
                      scope: channel === "private" ? "私人" : "当前项目",
                      version: "v1",
                      source: messageEdit.id,
                      content: messageEdit.text,
                    });
                    log(
                      x,
                      `由消息 ${messageEdit.id} 提取记忆候选`,
                      "/knowledge",
                    );
                  });
                  setMessageEdit(null);
                  toast("记忆候选待确认，不自动成为正式结论");
                } catch (e) {
                  toast(e.message, true);
                }
              })}
            >
              保存为记忆候选
            </Button>
          </div>
          </>}
        </Modal>
      )}
      {sharing && (
        <Modal title="共享前，确认具体范围" key={sharing.id} draftValue={{ target: shareTarget, project: shareTarget === "项目成员" ? shareProject : undefined }} onClose={() => setSharing(null)}>
          <ActionForm
            label="确认共享选定消息"
            onCancel={() => setSharing(null)}
            onSubmit={({ dismiss }) => {
              shareMessage({ tenantId: sharing.tenantId, messageId: sharing.id, expectedText: sharing.text,
                target: shareTarget === "外部客户" ? "external" : "project", projectId: shareProject });
              dismiss();
              toast("选定消息已共享，未包含其余会话历史");
            }}
          >
            <blockquote>{sharing.text}</blockquote>
            <Field
              label="接收范围"
              options={["项目成员", "外部客户"]}
              value={shareTarget}
              onChange={setShareTarget}
            />
            {shareTarget === "项目成员" ? <Field label="接收项目" value={shareProject}
              onChange={setShareProject} required options={[{ value: "", label: "请选择接收项目" },
                ...s.projects.filter(project => project.id !== "private").map(project => ({ value: project.id, label: project.name }))]} />
              : <Notice tone="warn">接收频道：客户共创 · 外部共享频道。确认后，该频道参与者可阅读此段文本。</Notice>}
            {shareProject && shareTarget === "项目成员" && <p>接收频道：{s.projects.find(project => project.id === shareProject)?.name} · 项目成员</p>}
            <Notice>
              只分享此条消息。未选择的私人历史、引用附件和内部记录不会自动开放。
            </Notice>
          </ActionForm>
        </Modal>
      )}
    </div>
  );
}
export function Artifact({ workId, params, toast }) {
  const s = useStore();
  const w = s.work.find(
    (x) =>
      x.id === workId &&
      (s.role !== "external" || sharedVersions(s, x.id).length > 0),
  );
  const [comment, setComment] = useState("");
  const [locationName, setLocationName] = useState("当前段落");
  const [candidate, setCandidate] = useState("B");
  const [play, setPlay] = useState(false);
  const [position, setPosition] = useState(18);
  const [tab, setTab] = useState("预览");
  if (!w) return <Empty title="成果不可访问" />;
  const allowedVersions =
    s.role === "external"
      ? sharedVersions(s, w.id)
      : Array.from({ length: w.version }, (_, i) => i + 1);
  const version = Number(params.get("version")) || allowedVersions.at(-1);
  if (
    !Number.isInteger(version) ||
    version < 1 ||
    version > w.version ||
    !allowedVersions.includes(version)
  )
    return <Empty title="版本不存在" text="请选择已登记的成果版本。" />;
  const comments = s.comments.filter(
    (c) => c.work === w.id && c.version === version,
  );
  return (
    <>
      <PageHead
        eyebrow={`${w.id} / 成果查看器`}
        title={w.title}
        subtitle="预览、批注与接受，始终指向同一个固定版本。"
      >
        <Select
          aria-label="成果版本"
          value={version}
          onChange={(e) =>
            (location.hash = `${artifactPath(w)}?version=${e.target.value}`)
          }
        >
          {allowedVersions.map((v) => (
            <option key={v} value={v}>
              v{v}
              {v === w.version ? " · 当前提交" : ""}
            </option>
          ))}
        </Select>
        <Button
          icon="Download"
          onClick={() =>
            exportData(`${w.id}-v${version}`, { work: w.id, version, comments })
          }
        >
          导出版本记录
        </Button>
        <Link
          to={
            s.role === "external" ? "/deliveries" : `${workPath(w)}?tab=review`
          }
          className="btn primary"
        >
          {s.role === "external" ? "交付与回执" : "检查与接受"}
        </Link>
      </PageHead>
      <div className="artifact-layout">
        <section className="artifact-view">
          <Tabs label="成果视图" items={["预览", "版本比较", "来源与证据"].map(value => [value, value])} value={tab} onChange={setTab} />
          {tab === "版本比较" ? (
            <div className="comparison">
              <div>
                <Badge>v{Math.max(1, version - 1)}</Badge>
                <h3>上一版</h3>
                <p>保留原始构图与信息结构。</p>
                <div className="compare-removed">原版：让工作更高效</div>
              </div>
              <div>
                <Badge>v{version}</Badge>
                <h3>当前评审版</h3>
                <p>明确用户价值，细化品牌表达。</p>
                <div className="compare-added">新版：创造，更从容。</div>
              </div>
            </div>
          ) : tab === "来源与证据" ? (
            <div className="padded">
              <dl>
                <dt>基线</dt>
                <dd>简报 v2 / 品牌指南 v2</dd>
                <dt>原记录制作主体</dt>
                <dd>{w.agent}</dd>
                <dt>Human 负责人</dt>
                <dd>{w.owner}</dd>
                <dt>检查状态</dt>
                <dd>
                  {version === w.version && w.qa
                    ? "当前版本内部通过"
                    : "当前版本需核查有效证据"}
                </dd>
                <dt>说明</dt>
                <dd>原型使用本地模拟素材，非真实生成或验证结果。</dd>
              </dl>
            </div>
          ) : w.type === "图片" ? (
            <div className="candidate-gallery">
              {["A", "B", "C"].map((v, i) => (
                <button
                  key={v}
                  className={`candidate candidate-${i} ${candidate === v ? "selected" : ""}`}
                  onClick={() => {
                    setCandidate(v);
                    setLocationName(`候选 ${v} · 产品区域`);
                  }}
                >
                  <span className="candidate-label">
                    候选 {v}
                    {candidate === v ? " · 当前选择" : ""}
                  </span>
                  <div className="visual-poster">
                    <small>NORTHSTAR / OBJECTS</small>
                    <h2>
                      {["回到本质。", "创造，更从容。", "让灵感发生。"][i]}
                    </h2>
                    <div className="product-art">
                      <span />
                    </div>
                    <footer>MADE FOR YOUR EVERYDAY</footer>
                  </div>
                </button>
              ))}
            </div>
          ) : w.type === "视频" ? (
            <div className="video-studio">
              <div className="video-frame">
                <Icon name="Waves" size={70} />
                <h2>向海而生。</h2>
                <p>品牌短片 · 代理画面 · v{version}</p>
                <Button
                  onClick={() => {
                    setPlay(!play);
                    if (!play) setPosition((p) => Math.min(60, p + 5));
                  }}
                  icon={play ? "Pause" : "Play"}
                >
                  {play ? "暂停预览" : "播放模拟片段"}
                </Button>
              </div>
              <div className="timeline-track">
                {Array.from({ length: 12 }, (_, i) => (
                  <button
                    key={i}
                    onClick={() => {
                      setPosition(i * 5);
                      setLocationName(
                        `时间段 00:${String(i * 5).padStart(2, "0")}–00:${String(i * 5 + 5).padStart(2, "0")}`,
                      );
                    }}
                  >
                    00:{String(i * 5).padStart(2, "0")}
                  </button>
                ))}
              </div>
              <label className="field">
                当前时间 {position} / 60 秒
                <input
                  type="range"
                  min="0"
                  max="60"
                  value={position}
                  onChange={(e) => setPosition(Number(e.target.value))}
                />
              </label>
              <Notice>
                原型用分镜代理展示审片交互；实际视频由素材接口加载。
              </Notice>
            </div>
          ) : w.type === "软件" ? (
            <div className="browser-preview">
              <div className="browser-dots">
                <i />
                <i />
                <i />
                <span>preview / northstar / v{version} · 授权访问</span>
              </div>
              <div className="website-preview">
                <nav>
                  N O R T H S T A R <span>为专注而生</span>
                </nav>
                <div>
                  <small>DESIGNED FOR WHAT MATTERS</small>
                  <h2>
                    创造，
                    <br />
                    更从容。
                  </h2>
                  <p>
                    让工具退后一步，让灵感自然发生。
                    <br />
                    为你的日常，留出更多可能。
                  </p>
                  <Button
                    onClick={() => setLocationName("网页首屏 · 主操作按钮")}
                  >
                    标注这个区域 <Icon name="ArrowUpRight" />
                  </Button>
                </div>
                <div className="product-art">
                  <span />
                </div>
                <footer>01 / 让设计回到本质</footer>
              </div>
            </div>
          ) : (
            <article className="document">
              <small>
                {w.type === "小说"
                  ? "潮汐之城 / 第三章"
                  : "NORTHSTAR / PRODUCT BRIEF"}{" "}
                · v{version}
              </small>
              <h2>
                {w.type === "小说"
                  ? "第三章 · 风从海面来"
                  : "为重要的事，留下空间。"}
              </h2>
              <p>
                {w.type === "小说"
                  ? "林深在潮水退去之前醒来。船坞里有熟悉的松木气味，门外的世界却比昨天更远了一些。"
                  : "当工作越来越复杂，我们需要的不是更多工具，而是更清晰的目标、更自然的协作，以及可以信任的结果。"}
              </p>
              <h3>
                {w.type === "小说"
                  ? "一个尚未说出口的决定"
                  : "让每一次协作，都抵达结果"}
              </h3>
              <p>
                {w.type === "小说"
                  ? "他把那封信放在工作台的角落，没有立即打开。窗外，一只海鸟沿着港口低飞，像是在替他丈量离开的距离。"
                  : "Northstar 将目标、制作与反馈放回同一条工作线上。让每位参与者知道当前版本、自己的责任，以及下一步交给谁。"}
              </p>
              <blockquote>
                批注绑定当前 v{version}。新版不会覆盖已经接受的文字。
              </blockquote>
              <Button
                icon="MessageSquarePlus"
                onClick={() => setLocationName("第 2 段 · 当前固定修订")}
              >
                对这一段批注
              </Button>
            </article>
          )}
        </section>
        <aside className="annotation-panel">
          <div className="section-title">
            <h2>版本批注</h2>
            <Badge tone="violet">v{version}</Badge>
          </div>
          <Notice>反馈固定到当前版本 / 快照，内容变化后不会漂移。</Notice>
          <div className="annotation-list">
            {comments.map((c) => (
              <article key={c.id}>
                <div className="person">
                  <Avatar name={c.author} />
                  <strong>{c.author}</strong>
                  <small>v{c.version}</small>
                </div>
                <small>{c.location}</small>
                <p>{c.text}</p>
                <Button
                  icon={c.resolved ? "CheckCheck" : "Check"}
                  onClick={() => {
                    if (s.offline) return toast("离线中，请重连后操作。", true);
                    update(
                      (x) =>
                        (x.comments.find((a) => a.id === c.id).resolved =
                          !c.resolved),
                    );
                  }}
                >
                  {c.resolved ? "已处理 · 重新打开" : "标记已处理"}
                </Button>
              </article>
            ))}
          </div>
          <Form
            onSubmit={(e) => {
              e.preventDefault();
              if (!comment.trim()) return;
              if (s.offline)
                return toast("离线中，批注保留，请重连后提交。", true);
              update((x) => {
                x.comments.push({
                  id: id("c"),
                  work: w.id,
                  version,
                  author: s.role === "external" ? "陈曦" : "林予",
                  location: locationName,
                  text: comment,
                  resolved: false,
                });
                log(x, `对 ${w.id} v${version} 添加批注`, artifactPath(w));
              });
              setComment("");
              toast("批注已固定到当前版本");
            }}
          >
            <Field
              label="定位"
              value={locationName}
              onChange={setLocationName}
            />
            <Field
              label="具体意见与期望"
              value={comment}
              onChange={setComment}
              type="textarea"
              required
            />
            <Button variant="primary" type="submit" icon="MessageSquarePlus">
              提交批注
            </Button>
          </Form>
        </aside>
      </div>
    </>
  );
}
