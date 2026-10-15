import { ExecutionProcess } from "./execution-ui";
import { executionRecord, executionReadProblem, executionPath, executionKinds, executionStates } from "./executions";
import { useViewState } from "./view-state";
import { Flow } from "./pages-flow";
import { WorkflowList } from "./pages-workflows";
import { ChannelManagement, DirectMessageStart } from "./channel-management";
import { ChatMessageContent, ChatComposerTools, EmojiPicker } from "./chat-rich";
import { channelReadProblem, channelWriteProblem, visibleChannels, visibleChannelMessages, assertChannelWrite, channelMessageReadProblem, directMessagePeer, directMessagePolicy } from "./channels";
import { formatTimestamp } from "./formatting";
import { TaskPreviews } from "./task-previews";
import { messageProject } from "./message-sharing";
import React, { useEffect, useLayoutEffect, useState, useRef, useId } from "react";
import {
  useStore,
  read,
  postChannelMessage,
  submitExecutionInput,
  changeChannelMessage,
  activeProjects,
  assertProjectRecordWritable,
  projectIsArchived,
  saveResource,
  update,
  log,
  id,
  exportData,
  assertWrite,
  sharedVersions,
  sharedArtifactPath,
  saveWorkDraft,
  beginWorkDraft,
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
import { workPath, artifactPath, WorkDetail, ConversationWorkDraft } from "./pages-work";
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
  const channelRecord = s.channels.find(c => c.id === channel);
  const isDirect = channelRecord?.kind === "DIRECT";
  const [directStart,setDirectStart] = useViewState("chat-new-direct",null);
  const directChannels = visibleChannels(s,{archived:"all"}).filter(c => c.kind === "DIRECT");
  const [management, setManagement] = useViewState("chat-management", "");
  const [managementTab,setManagementTab]=useViewState("channel-management-tab","details");
  const [workPreview, setWorkPreview] = useState(null);
  const [flowPreview, setFlowPreview] = useViewState("chat-flow-preview",null);
  const previewVersion = flowPreview?.flowState && Object.hasOwn(flowPreview.flowState,"version") ? flowPreview.flowState.version : flowPreview?.version;
  const [reactionPicker,setReactionPicker] = useState(null);
  const [attachmentsReading,setAttachmentsReading] = useState(false);
  const composerInput = useRef(null);
  const messagesRef = useRef(null);
  const [channelQuery, setChannelQuery] = useState("");
  const [archiveFilter, setArchiveFilter] = useState("active");
  const [channelDirectoryOpen, setChannelDirectoryOpen] = useState(false);
  const channelDirectoryButton = useRef(null);
  const [channelSearchOpen, setChannelSearchOpen] = useState(false);
  const [channelFilterOpen, setChannelFilterOpen] = useState(false);
  const channelSearchRef = useRef(null);
  const channelSearchButton = useRef(null);
  const channelSearchId = useId();
  const channelFilterId = useId();
  const filteredChannels = visibleChannels(s, { query: channelQuery, archived: archiveFilter }).filter(c => c.kind !== "DIRECT");
  useEffect(() => {
    if (channelSearchOpen) channelSearchRef.current?.focus({ preventScroll: true });
  }, [channelSearchOpen, channelDirectoryOpen]);
  const execution = params.get("execution_id");
  const executionEntry = executionRecord(s, execution);
  const w = executionEntry?.workSnapshot;
  const thread = params.get("thread_id");
  const scrollKey = JSON.stringify([s.activeTenant,s.role,execution || channel,thread || ""]);
  useLayoutEffect(() => {
    const container=messagesRef.current;
    if (!container) return;
    try { const top=Number(sessionStorage.getItem(`chat-scroll:${scrollKey}`)); if(Number.isFinite(top)) container.scrollTop=top; } catch { /* Reading still works without session storage. */ }
    return () => { try { sessionStorage.setItem(`chat-scroll:${scrollKey}`,String(container.scrollTop)); } catch { /* Best effort view state only. */ } };
  }, [scrollKey]);
  const workDraft = s.drafts?.[params.get("work_draft")];
  const conversationPath = execution
    ? `/chat?execution_id=${encodeURIComponent(execution)}`
    : `/chat?channel_id=${encodeURIComponent(channel)}`;
  const contextProject = s.projects.find((p) =>
    channel === "team" ? p.id === s.project : p.id !== "private" && p.id === (channelRecord?.projectId || channel),
  );
  const contextWork =
    isDirect
      ? []
      : s.work.filter((item) =>
          contextProject
            ? item.project === contextProject.id
            : channel === "team" && s.project === "all",
        );
  const title = execution
    ? executionEntry?.title || "执行不存在"
    : isDirect ? directMessagePeer(s,channelRecord).name : channelRecord?.name || contextProject?.name || "会话不存在";
  const [message, setMessage] = useState("");

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
  const [attachment, setAttachment] = useState([]);
  const attachmentDraftKey=`chat-files:${JSON.stringify([s.activeTenant,s.role,execution || channel])}`;
  const writeAttachments=files=>{setAttachment(files);try{if(files.length)sessionStorage.setItem(attachmentDraftKey,JSON.stringify(files));else sessionStorage.removeItem(attachmentDraftKey);}catch{toast("附件草稿暂未保存到本地，当前页面的原件仍保留。",true);}};
  useEffect(() => {
    setMessage(
      localStorage.getItem(
        `draft-${s.activeTenant}-${s.role}-${execution || channel}`,
      ) || "",
    );
    try { setAttachment(JSON.parse(sessionStorage.getItem(attachmentDraftKey) || "[]")); } catch {setAttachment([]);}
    setWorkPreview(null);
    setReactionPicker(null);
  }, [channel, execution, s.role, s.activeTenant]);
  const write = (v) => {
    setMessage(v);
    try {localStorage.setItem(
      `draft-${s.activeTenant}-${s.role}-${execution || channel}`,
      v,
    );} catch {toast("草稿暂未保存到本地，当前输入仍保留。",true);}
  };
  const readableMessages = visibleChannelMessages(s, execution ? `exec-${execution}` : channel);
  const threadRoot = thread ? readableMessages.find(m => m.id === thread && !m.recalled) : null;
  const messages = execution ? [] : readableMessages.filter(m => !thread || threadRoot && (m.id === thread || m.thread === thread));
  const messageId = params.get("message_id");
  const targetMessage = messageId && readableMessages.find(m => m.id === messageId && !m.recalled && (!thread || threadRoot && (m.id === thread || m.thread === thread)));
  useLayoutEffect(() => {
    if (!targetMessage || !messagesRef.current) return;
    const segment = execution && targetMessage.turnId ? `turn:${targetMessage.turnId}` : `message:${targetMessage.id}`;
    const element = [...messagesRef.current.querySelectorAll('[data-segment]')].find(node=>node.dataset.segment===segment);
    if (!element) return;
    const top = element.getBoundingClientRect().top - messagesRef.current.getBoundingClientRect().top;
    element.focus({preventScroll:true});
    messagesRef.current.scrollTop += top - 16;
  }, [scrollKey, messageId, Boolean(targetMessage)]);
  const writeProblem = channelWriteProblem(s, execution ? `exec-${execution}` : channel);
  const announcement = channelRecord?.announcement && !channelRecord.announcement.cancelledAt && visibleChannelMessages(s, channel).find(m => m.id === channelRecord.announcement.messageId && !m.recalled);
  const send = (e) => {
    e.preventDefault();
    if(attachmentsReading) {toast("请等原件读取或录音完成后发送。",true);return;}
    try {
      if(execution) submitExecutionInput({tenantId:s.activeTenant,executionId:execution,revision:executionEntry?.revision,text:message,attachments:attachment,threadId:thread});
      else postChannelMessage({ tenantId: s.activeTenant, channelId: execution ? `exec-${execution}` : channel,
        text: message, attachments: attachment, threadId: thread, workDraftId: workDraft && !workDraft.workId ? workDraft.id : undefined });
    } catch (error) { toast(error.message, true); return; }
    write("");
    writeAttachments([]);
    toast(execution ? "执行输入已保存 · 待本地模拟处理" : "消息已保存");
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
                to={sharedArtifactPath(s, w)} target="_blank" rel="noopener noreferrer"
              >
                <b>{w.title}</b>
                <small>共享版本 v{sharedVersions(s, w.id).at(-1)}</small>
              </Link>
            ))}
          <Link to="/deliveries" className="btn full" target="_blank" rel="noopener noreferrer">
            交付与回执
          </Link>
        </>
      ) : execution && w ? (
        <TaskPreviews
          key={`${s.activeTenant}-${execution}`}
          scopeId={execution}
          work={w}
          expanded={compactContext}
          toast={toast}
        />
      ) : execution ? <Empty title="未关联工作项" text="临时探索尚无成果资源清单；关联工作后也不会复制其他执行的预览。" /> : (
        <>
          <h2>
            {isDirect
              ? "私人会话"
              : contextProject?.name ||
                (channel === "team" ? "跨项目协作" : "会话上下文")}
          </h2>
          {isDirect ? (
            <Notice>{directMessagePolicy(s,channelRecord)}</Notice>
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
                    to={workPath(item)} target="_blank" rel="noopener noreferrer"
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
            <Button icon="Workflow" className="full" onClick={() => {if(compactContext)setContext(false);setFlowPreview({tenantId:s.activeTenant,projectId:contextProject.id,channelId:channel});}}>查看工作流</Button>
          )}
        </>
      )}
    </>
  );
  const channelDirectory = (
      <aside className="channel-list" aria-label="会话目录">
        <header>
          <h2>会话</h2>
          <div className="channel-directory-actions">
            <IconButton ref={channelSearchButton} name="Search" label="搜索频道" aria-expanded={channelSearchOpen} aria-controls={channelSearchId} className={`icon-button${channelQuery ? " is-filtered" : ""}`} onClick={() => setChannelSearchOpen(open => !open)} />
            <IconButton name="ListFilter" label="筛选频道" aria-expanded={channelFilterOpen} aria-controls={channelFilterId} className={`icon-button${archiveFilter !== "active" ? " is-filtered" : ""}`} onClick={() => setChannelFilterOpen(open => !open)} />
            {s.role !== "external" && <IconButton name="Plus" label="创建频道" onClick={() => { setChannelDirectoryOpen(false); setManagement("new"); }} />}
          </div>
        </header>
        {channelSearchOpen && <div id={channelSearchId} className="channel-directory-search">
          <Icon name="Search" />
          <input ref={channelSearchRef} type="search" name="channel-search" autoComplete="off" aria-label="查找频道" placeholder="查找频道…" value={channelQuery} onChange={event => setChannelQuery(event.target.value)} onKeyDown={event => {
            if (event.key === "Escape") { event.preventDefault(); setChannelSearchOpen(false); channelSearchButton.current?.focus({ preventScroll: true }); }
          }} />
          <IconButton name="X" label={channelQuery ? "清除频道搜索" : "收起频道搜索"} onClick={() => {
            if (channelQuery) { setChannelQuery(""); channelSearchRef.current?.focus(); }
            else { setChannelSearchOpen(false); channelSearchButton.current?.focus({ preventScroll: true }); }
          }} />
        </div>}
        {channelFilterOpen && <div id={channelFilterId} className="channel-directory-filter">
          <Select aria-label="频道状态" value={archiveFilter} onChange={event => setArchiveFilter(event.target.value)} options={[{value:"active",label:"活动频道"},{value:"archived",label:"已归档"},{value:"all",label:"全部频道"}]} />
        </div>}
        <div className="channel-directory-label"><span className="label">协作频道</span>
          {(channelQuery || archiveFilter !== "active") && <button type="button" className="channel-filter-reset" onClick={() => { setChannelQuery(""); setArchiveFilter("active"); }}>清除筛选</button>}
        </div>
        {!filteredChannels.length && <p className="channel-directory-empty" role="status">没有匹配的频道</p>}
        {filteredChannels.map(c => [c.id,c.name,c.archivedAt ? "已归档 · 只读" : c.shared ? "外部共享 · 明确范围" : c.kind === "PROJECT_DEFAULT" ? "项目默认频道" : c.kind === "CUSTOM" ? "自建频道" : "组织范围"])
          .map(([key, name, sub]) => (
            <PanelButton
              onView={to => { setChannelDirectoryOpen(false); onView(to); }}
              key={key}
              to={`/chat?channel_id=${encodeURIComponent(key)}`}
              className={channel === key && !execution ? "active" : ""}
            >
              <Icon name={key === "external" ? "Globe2" : "Hash"} />
              <div>
                <b>{name}</b>
                <small>{sub}</small>
              </div>
            </PanelButton>
          ))}
        {s.role !== "external" && <div className="channel-directory-label"><span className="label">私信</span><IconButton name="MessageSquarePlus" label="发起私信" disabled={s.offline} onClick={() => {setChannelDirectoryOpen(false);setDirectStart({tenantId:s.activeTenant,peerId:""});}} /></div>}
        {directChannels.map(c => {const peer=directMessagePeer(s,c);return <PanelButton key={c.id} onView={to => {setChannelDirectoryOpen(false);onView(to);}} to={`/chat?channel_id=${encodeURIComponent(c.id)}&tenant=${encodeURIComponent(s.activeTenant)}&scope=all`} className={channel === c.id && !execution ? "active" : ""}>
          <Avatar name={peer.name} agent={peer.kind === "Agent"} /><div><b>{peer.name}</b><small>{peer.kind} · {channelWriteProblem(s,c.id) ? "历史只读" : "私信"}</small></div>
        </PanelButton>;})}
        <span className="label">执行会话</span>
        {(s.executions || []).filter(e=>!executionReadProblem(s,e.id)).map(e=>(
          <PanelButton onView={to=>{setChannelDirectoryOpen(false);onView(to);}} key={e.id} to={executionPath(e)} className={execution===e.id ? "active" : ""}>
            <Icon name="Terminal" /><div><b>{e.title}</b><small>{e.id} · {executionStates[e.state]} · {executionKinds[e.kind]}</small></div>
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
  );
  if (channelReadProblem(s, execution ? `exec-${execution}` : channel))
    return (
      <><PageHead title="会话不可访问" /><Empty title="此会话不在可访问范围" text={channelReadProblem(s, execution ? `exec-${execution}` : channel)}>
        <Link className="btn" to="/chat?channel_id=external">
          进入共享频道
        </Link>
      </Empty></>
    );
  if (thread && !threadRoot) return <><PageHead title="线程不可访问" /><Empty title="线程不可访问" text="线程不存在、已撤回，或不属于当前授权频道。"><Link className="btn" to={conversationPath}>返回当前会话</Link></Empty></>;
  return (
    <div ref={layoutRef} className={`chat-layout${execution && !compactContext ? " resizable-preview" : ""}${resizing ? " is-resizing" : ""}`}
      style={execution && !compactContext ? { "--task-preview-width": `${actualPreviewWidth}px` } : undefined}>
      {channelDirectoryOpen ? <Modal title="会话列表" className="channel-directory-dialog" returnFocusRef={channelDirectoryButton} onClose={() => setChannelDirectoryOpen(false)}>{channelDirectory}</Modal> : channelDirectory}
      <section className="conversation">
        <header className="chat-heading">
          <IconButton ref={channelDirectoryButton} className="icon-button mobile-channel-directory-trigger" name="MessagesSquare" label="打开会话列表" onClick={() => setChannelDirectoryOpen(true)} />
          <div>
            <h1>{title}</h1>
            <small>
              {execution
                ? `${executionEntry?.id} · ${executionKinds[executionEntry?.kind]} · ${executionStates[executionEntry?.state]}`
                : isDirect
                  ? "私人会话 · 共享前预览资料"
                  : channelRecord?.shared
                    ? "外部共享频道 · 仅开放的历史与附件"
                    : "Human 与 Agent，一起把工作推进"}
            </small>
          </div>
          {!execution && channelRecord?.externalBridge && <Badge>{channelRecord.externalBridge.platform} · {channelRecord.externalBridge.status === "ACTIVE" ? "已绑定 · 模拟" : channelRecord.externalBridge.status === "PAUSED" ? "已暂停" : "已撤销"}</Badge>}
          {!execution && <IconButton name="Settings2" label={isDirect ? "私信详情" : "频道设置"} onClick={() => {setManagementTab("details");setManagement(channel);}} />}
          <IconButton
            name="PanelRight"
            label="切换会话上下文"
            onClick={() => setContext(!context)}
          />
        </header>
        {writeProblem && <Notice tone="warn">{writeProblem}</Notice>}
        {isDirect && !execution && <Notice>{directMessagePolicy(s,channelRecord)}</Notice>}
        {messageId && (!targetMessage || execution && !targetMessage.turnId) && <Notice tone="warn">定位消息不存在、已撤回或当前无法展示；没有跳到其他消息。请核对来源链接或返回会话历史。</Notice>}
        {channelRecord?.shared && <Notice tone="warn">外部成员在此频道 · 仅显示当前授权消息和明确共享的资料。</Notice>}
        {!execution && !isDirect && <div className="pinned"><Icon name="Pin" size={14} /><span className="channel-announcement-text">{announcement ? `公告 v${channelRecord.announcement.revision}：${announcement.text}` : channelRecord?.topic || "当前没有公告；可从频道设置发布。"}</span></div>}
        <div className="messages" ref={messagesRef} onScroll={event=>{try {sessionStorage.setItem(`chat-scroll:${scrollKey}`,String(event.currentTarget.scrollTop));} catch { /* View-state storage may be unavailable. */ }}}>
          {!execution && <div className="day-divider">今天 · 10 月 13 日</div>}
          {execution && <ExecutionProcess key={`${s.activeTenant}-${execution}`} executionId={execution} focusedTurnId={targetMessage?.turnId} toast={toast} onWork={()=>setWorkPreview({tenantId:s.activeTenant,workId:executionEntry.workId,channelId:`exec-${execution}`})} />}
          {params.get("workflow_id") && !workDraft && s.role !== "external" && <Section title="从工作流发起任务">
            <Notice>当前选择：{params.get("workflow_id") === "default" ? "项目交付流程" : s.projectWorkflows?.[channelRecord?.projectId || channel]?.find(item=>item.id===params.get("workflow_id"))?.name || "工作流不可用"}。先在会话讨论目标和输入，由 Human 确认后生成任务；不会直接开始执行。</Notice>
            <Button onClick={() => {try {const draftId=beginWorkDraft({channel,projectId:channelRecord?.projectId || channel,workflowId:params.get("workflow_id")});onView(`/chat?channel_id=${encodeURIComponent(channel)}&work_draft=${encodeURIComponent(draftId)}`);}catch(error){toast(error.message,true);}}}>在会话中起草任务</Button>
          </Section>}
          {workDraft && workDraft.tenantId === s.activeTenant && s.role !== "external" && <ConversationWorkDraft key={`${workDraft.id}:${workDraft.revision}`} draft={workDraft} toast={toast} />}
          {messages.map((m) => (
            <article className={`message${targetMessage?.id===m.id ? " message-location-highlight" : ""}`} tabIndex={targetMessage?.id===m.id ? -1 : undefined} key={m.id} data-segment={`message:${m.id}`}>
              <Avatar name={m.author} agent={m.kind === "agent"} />
              <div className="message-main">
                <header>
                  <strong>{m.author}</strong>
                  {m.kind === "agent" && (
                    <span className="agent-tag">AGENT</span>
                  )}
                  {m.source?.platform && <Badge>{m.source.platform} · 外部来源{m.source.mode === "LOCAL_SIMULATION" ? " · 模拟" : ""}</Badge>}
                  <time>{formatTimestamp(m.time)}</time>
                </header>
                <ChatMessageContent message={m} />
                {m.pinned && <Badge tone="violet">已置顶</Badge>}
                {m.history?.length > 0 && (
                  <small>已编辑 · {m.history.length} 次修订</small>
                )}
                {m.work && (
                  <button type="button"
                    className="message-work"
                    onClick={()=>setWorkPreview({tenantId:s.activeTenant,workId:m.work,messageId:m.id})}
                  >
                    <Icon name="Layers" />
                    <div>
                      <b>{s.work.find((w) => w.id === m.work)?.title}</b>
                      <small>关联工作 · 当前页查看详情</small>
                    </div>
                    <Icon name="ArrowUpRight" />
                  </button>
                )}
                <div className="message-actions">
                  {(m.emojiReactions || []).map(reaction=><button type="button" key={reaction.emoji} className={`chat-reaction${reaction.principalIds.includes(s.role === "external" ? "external-demo" : "H-lin") ? " selected" : ""}`} disabled={Boolean(writeProblem) || m.recalled} aria-label={`回应 ${reaction.emoji}`} aria-pressed={reaction.principalIds.includes(s.role === "external" ? "external-demo" : "H-lin")} onClick={()=>{try{changeChannelMessage({tenantId:s.activeTenant,messageId:m.id,expectedText:m.text,action:"emoji",emoji:reaction.emoji});}catch(error){toast(error.message,true);}}}>{reaction.emoji} {reaction.principalIds.length}</button>)}
                  <IconButton name="Smile" label="添加消息回应" disabled={Boolean(writeProblem) || m.recalled} onClick={()=>setReactionPicker({tenantId:s.activeTenant,messageId:m.id,expectedText:m.text})} />
                  <Button
                    icon="ThumbsUp"
                    onClick={() => {
                      try { changeChannelMessage({tenantId:s.activeTenant,messageId:m.id,expectedText:m.text,action:"react"}); }
                      catch(error) { toast(error.message,true); }
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
                    onClick={() => {try {const draftId=beginWorkDraft({sourceId:m.id,projectId:w?.project || contextProject?.id || (isDirect ? "private" : undefined),channel,threadId:params.get("thread_id")||undefined});const next=new URLSearchParams(params);next.set("quick_work",draftId);onView(`/chat?${next}`);}catch(error){toast(error.message,true);}}}
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
                  线程 · {threadRoot?.author}
                </h3>
                <Link to={conversationPath} className="btn">
                  返回频道
                </Link>
              </div>
              <p>{threadRoot?.text}</p>
              <Notice>回复将保留当前线程引用。</Notice>
            </div>
          )}
        </div>
        <Form className="composer" onSubmit={send}>
          <fieldset disabled={!!writeProblem} className="channel-composer-fields">
          <textarea
            ref={composerInput}
            aria-label={execution ? "执行指令" : "消息内容"}
            name="message-content" autoComplete="off"
            placeholder={
              execution ? "向此执行发送指令 · 提交后等待处理回执…" : `发送到 ${title}…`
            }
            value={message}
            onChange={(e) => write(e.target.value)}
            onKeyDown={(e) => {
              if ((e.ctrlKey || e.metaKey) && e.key === "Enter") send(e);
            }}
          />
          {!execution && <ChatComposerTools value={message} onChange={write} inputRef={composerInput} files={attachment} onFiles={writeAttachments} contextKey={JSON.stringify([s.activeTenant,s.role,execution || channel])} toast={toast} disabled={Boolean(writeProblem)} onBusy={setAttachmentsReading} />}
          <div className="composer-bottom">
            <span>{attachment.length ? `${attachment.length} 个本地附件 · 尚未发送` : "提及须为当前成员；发送不代表 Agent 已执行"}</span>
            <Button type="submit" variant="primary" icon="ArrowUp" disabled={attachmentsReading}>
              {execution ? "提交执行指令" : "发送"}
            </Button>
          </div>
          </fieldset>
        </Form>
      </section>
      {reactionPicker && <EmojiPicker title="回应这条消息" onClose={()=>setReactionPicker(null)} onSelect={emoji=>{try{changeChannelMessage({...reactionPicker,action:"emoji",emoji});}catch(error){toast(error.message,true);}}} />}
      {workPreview && <Modal title="关联工作详情" wide className="conversation-work-dialog" onClose={()=>setWorkPreview(null)}
        headerActions={!(workPreview.tenantId !== s.activeTenant || (workPreview.messageId ? channelMessageReadProblem(s,s.messages.find(message=>message.id===workPreview.messageId)) : channelReadProblem(s,workPreview.channelId))) && <Link className="btn modal-open-external" aria-label="新标签页打开工作" title="新标签页打开工作" to={workPath(s.work.find(work=>work.id===workPreview.workId))} target="_blank" rel="noopener noreferrer"><Icon name="ArrowUpRight" /><span>新标签页打开工作</span></Link>}>
        {workPreview.tenantId !== s.activeTenant || (workPreview.messageId ? channelMessageReadProblem(s,s.messages.find(message=>message.id===workPreview.messageId)) : channelReadProblem(s,workPreview.channelId)) ? <Empty title="此工作引用已不可访问" /> : <>
          <div onClickCapture={event=>{const anchor=event.target.closest('a[href^="#/"]');if(anchor){anchor.target="_blank";anchor.rel="noopener noreferrer";}}}><WorkDetail workId={workPreview.workId} params={new URLSearchParams()} toast={toast} embedded /></div>
        </>}
      </Modal>}
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
        <aside aria-label={execution ? "执行成果预览" : "协作上下文"} className={`chat-context${execution ? " execution-preview" : ""}`} id={contextId}>
          {!execution && <span className="eyebrow">协作上下文</span>}
          {contextContent}
        </aside>
      )}

      {flowPreview && <Modal title="工作流" wide className="conversation-flow-dialog" onClose={() => setFlowPreview(null)}>
        {flowPreview.tenantId !== s.activeTenant || channelReadProblem(s,flowPreview.channelId) || !s.projects.some(project=>project.id===flowPreview.projectId) ? <Empty title="工作流不在当前会话访问范围"/> : <>
          <div className="actions"><Link className="btn" target="_blank" rel="noopener noreferrer" to={`/p/${flowPreview.projectId}/flow?scope=${encodeURIComponent(flowPreview.projectId)}${flowPreview.workflowId ? `&workflow_id=${encodeURIComponent(flowPreview.workflowId)}` : ""}${previewVersion ? `&version=${encodeURIComponent(previewVersion)}` : ""}${flowPreview.view === "runs" ? "&view=runs" : ""}`}>在新标签页打开工作流</Link>{flowPreview.workflowId && <Button onClick={() => setFlowPreview({...flowPreview,workflowId:null,view:null,version:null,flowState:{}})}>返回工作流列表</Button>}</div>
          <div onClick={event => {
            const anchor=event.target.closest('a[href^="#/"]');if(!anchor || event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey || anchor.target === "_blank")return;
            const [path,query=""]=anchor.getAttribute("href").slice(1).split("?");const p=new URLSearchParams(query);
            if(path===`/p/${flowPreview.projectId}/flow` ) {event.preventDefault();event.stopPropagation();setFlowPreview({...flowPreview,workflowId:p.get("workflow_id"),view:p.get("view"),version:p.get("version"),flowState:{}});}
            else {anchor.target="_blank";anchor.rel="noopener noreferrer";}
          }}>
            {flowPreview.workflowId && flowPreview.view !== "runs" ? <Flow key={flowPreview.workflowId} projectId={flowPreview.projectId} params={new URLSearchParams({workflow_id:flowPreview.workflowId,...(flowPreview.version ? {version:flowPreview.version} : {})})} toast={toast} embedded embeddedState={flowPreview.flowState || {}} onEmbeddedStateChange={patch=>setFlowPreview(previous=>({...previous,flowState:{...(previous.flowState || {}),...patch}}))}/> : <WorkflowList projectId={flowPreview.projectId} listState={flowPreview.listState} onListStateChange={patch=>setFlowPreview(previous=>({...previous,listState:{...(previous.listState || {}),...patch}}))} params={new URLSearchParams({...(flowPreview.workflowId ? {workflow_id:flowPreview.workflowId} : {}),...(flowPreview.view ? {view:flowPreview.view} : {}),...(flowPreview.version ? {version:flowPreview.version} : {})})} toast={toast} onView={to => {const p=new URLSearchParams(to.split("?")[1]);setFlowPreview({...flowPreview,workflowId:p.get("workflow_id"),view:p.get("view"),version:p.get("version"),flowState:{}});}}/>}
          </div>
        </>}
      </Modal>}
      {directStart && <DirectMessageStart key={`${directStart.tenantId}:${directStart.peerId}`} initial={directStart} onClose={() => setDirectStart(null)} onCreated={id => {setDirectStart(null);onView(`/chat?channel_id=${encodeURIComponent(id)}&tenant=${encodeURIComponent(s.activeTenant)}&scope=all`);}} />}
      {management && <ChannelManagement key={`${s.activeTenant}:${management}`} channelId={management === "new" ? undefined : management} onClose={() => setManagement("")} initialTab={managementTab} onTabChange={setManagementTab} toast={toast} onCreated={id => {setManagement("");setArchiveFilter("active");setChannelQuery("");onView?.(`/chat?channel_id=${encodeURIComponent(id)}`);}} />}
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
                  changeChannelMessage({tenantId:s.activeTenant,messageId:messageEdit.id,expectedText:messageEdit.text,action:"edit",text:editText});
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
                  changeChannelMessage({tenantId:s.activeTenant,messageId:messageEdit.id,expectedText:messageEdit.text,action:"pin"});
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
                  changeChannelMessage({tenantId:s.activeTenant,messageId:messageEdit.id,expectedText:messageEdit.text,action:"recall"});
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
                  const currentMessage = read().messages.find(m => m.id === messageEdit.id);
                  const problem = channelMessageReadProblem(read(),currentMessage); if (problem) throw Error(problem);
                  assertChannelWrite(read(),currentMessage.channel);
                  if (currentMessage.recalled || currentMessage.text !== messageEdit.text) throw Error("消息已变更，请重新打开。");
                  saveResource("knowledge", {
                    name: messageEdit.text.slice(0, 24),
                    status: "待确认",
                    owner: "林予",
                    kind: "记忆",
                    scope: isDirect ? "私人" : "当前项目",
                    version: "v1",
                    source: messageEdit.id,
                    content: messageEdit.text,
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
        <Modal title="共享前，确认具体范围" key={sharing.id} draftValue={{ target: shareTarget, project: ["项目成员", "指定频道"].includes(shareTarget) ? shareProject : undefined }} onClose={() => setSharing(null)}>
          <ActionForm
            label="确认共享选定消息"
            onCancel={() => setSharing(null)}
            onSubmit={({ dismiss }) => {
              shareMessage({ tenantId: sharing.tenantId, messageId: sharing.id, expectedText: sharing.text,
                target: shareTarget === "外部客户" ? "external" : shareTarget === "指定频道" ? "channel" : "project", projectId: shareProject, channelId: shareProject });
              dismiss();
              toast("选定消息已共享，未包含其余会话历史");
            }}
          >
            <blockquote>{sharing.text}</blockquote>
            <Field
              label="接收范围"
              options={["项目成员", "指定频道", "外部客户"]}
              value={shareTarget}
              onChange={value => {setShareTarget(value);setShareProject("");}}
            />
            {shareTarget === "指定频道" && <Field label="接收频道" value={shareProject} onChange={setShareProject} required options={[{value:"",label:"请选择明确接收频道"},...visibleChannels(s).filter(c => c.kind !== "DIRECT").map(c => ({value:c.id,label:`${c.name}${c.shared ? " · 外部共享" : ""}`}))]} />}
            {shareTarget === "项目成员" ? <Field label="接收项目" value={shareProject}
              onChange={setShareProject} required options={[{ value: "", label: "请选择接收项目" },
                ...s.projects.filter(project => project.id !== "private").map(project => ({ value: project.id, label: project.name }))]} />
              : shareTarget === "外部客户" && <Notice tone="warn">接收频道：客户共创 · 外部共享频道。确认后，该频道参与者可阅读此段文本。</Notice>}
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
                  disabled={projectIsArchived(s,w.project)}
                  onClick={() => {
                    if (s.offline) return toast("离线中，请重连后操作。", true);
                    try { assertProjectRecordWritable(read().work.find(work=>work.id===read().comments.find(item=>item.id===c.id)?.work)); } catch(error) {toast(error.message,true);return;}
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
              try { assertProjectRecordWritable(read().work.find(work=>work.id===w.id)); } catch(error) {toast(error.message,true);return;}
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
