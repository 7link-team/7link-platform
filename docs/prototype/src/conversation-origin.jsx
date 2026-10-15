import React, { useState } from "react";
import { useStore, read } from "./store";
import { resolveConversationOrigins } from "./conversation-origin.js";
import { ChatMessageContent } from "./chat-rich";
import { Modal, Section, Notice, Link, Button, Badge, FormError } from "./ui";
import { useViewState } from "./view-state";

const readable = source => ["READY", "DRAFT", "CONTEXT"].includes(source.status);
export function ConversationOriginPanel({ record, actions = [], openedTenantId }) {
  const s = useStore(), [mountedTenantId] = useState(openedTenantId || s.activeTenant), [error, setError] = useState(null), [running, setRunning] = useState(null);
  if (mountedTenantId !== s.activeTenant) return <Notice tone="warn">租户已切换，请关闭来源视图并从当前对象重新打开。</Notice>;
  const result = resolveConversationOrigins(s, record);
  const invoke = async (action, index) => {
    try {
      const current = read();
      if (current.activeTenant !== mountedTenantId) throw Error("租户已切换，请重新核对对象。");
      const source = resolveConversationOrigins(current, record).sources[index];
      if (!source || !readable(source)) throw Error("来源权限或消息状态已变化，请重新核对。");
      if (action.disabled || typeof action.onClick !== "function") throw Error("此快捷操作当前不可用。");
      setRunning(action.id); setError(null);
      await action.onClick({ record, source });
    } catch (e) { setError({ message: e.message }); }
    finally { setRunning(null); }
  };
  return <>
    <Section title="当前对象">
      <p>{record?.title || record?.name || record?.id || "协作对象"}</p>
      <p>状态：{record?.status || "未记录"} · 当前版本 / 修订：{record?.version ?? record?.revision ?? "未记录"}</p>
      <Notice>以下为固定创建来源的引用与当前可读会话。对象后续状态变化不改写原来源；消息正文显示当前记录，不还原已撤回内容。</Notice>
    </Section>
    <FormError error={error} />
    {!result.sources.length && <Notice>{result.label}</Notice>}
    {result.sources.map(source => <Section key={source.index} title={`来源 ${source.index + 1}`}>
      {!readable(source) ? <Notice tone="warn">{source.reason}</Notice> : <>
        <Badge>{source.status === "DRAFT" ? "讨论草稿阶段 · 尚无固定来源消息" : source.status === "CONTEXT" ? "来源执行上下文 · 未指定消息" : "来源消息可读"}</Badge>
        <p>{source.reason}</p>
        {source.warning && <Notice tone="warn">{source.warning}</Notice>}
        <details><summary>固定创建来源引用</summary>
          <dl>
            <dt>租户</dt><dd>{source.creation.tenantId}</dd>
            <dt>项目</dt><dd>{source.creation.projectId || "未记录"}</dd>
            <dt>频道 / 执行会话</dt><dd>{source.creation.executionId || source.creation.channelId || "未记录"}</dd>
            <dt>原消息 / 线程</dt><dd>{source.creation.messageId || "尚无消息"} / {source.creation.threadId || "未记录线程"}</dd>
            <dt>原对象版本 / 草稿修订</dt><dd>{source.creation.objectVersion ?? "未记录"} / {source.creation.draftRevision ?? "未记录"}</dd>
            <dt>生成主体 / 时间</dt><dd>{source.creation.actorKind ? `${source.creation.actorKind} · ` : ""}{source.creation.createdBy || "未记录"} / {source.creation.createdAt || "未记录"}</dd>
            <dt>确认主体 / 时间</dt><dd>{source.creation.confirmedBy || "未记录"} / {source.creation.confirmedAt || "未记录"}</dd>
          {source.creation.confirmation && <><dt>固定确认记录</dt><dd>{source.creation.confirmation.status || "未记录状态"} · {source.creation.confirmation.actorId || "未记录主体"} · {source.creation.confirmation.time || "未记录时间"} · 修订 {source.creation.confirmation.revision ?? "未记录"}</dd></>}
          </dl>
        </details>
        {source.status === "DRAFT" && <p>当前讨论草稿状态：{source.draftStatus || "未记录"}；不据此声明对象已生效。</p>}
        <div className="actions"><Link className="btn" to={source.link} target="_blank" rel="noopener noreferrer">在新标签页打开此来源会话</Link></div>
        {source.thread.map(message => <article key={message.id} className={message.id === source.message?.id ? "panel conversation-origin-highlight" : "panel"} aria-label={message.id === source.message?.id ? "定位的来源消息" : "来源线程消息"} style={message.id === source.message?.id ? { borderInlineStart: "3px solid var(--primary)", paddingInlineStart: 12 } : undefined}>
          <p>{message.id === source.message?.id && <Badge>来源消息</Badge>} {message.author || "会话记录"} · {message.time || "时间未记录"}</p>
          <ChatMessageContent message={message} />
        </article>)}
        {actions.filter(action => typeof action.onClick === "function").length > 0 && <div className="actions">{actions.filter(action => typeof action.onClick === "function").map(action => <Button key={action.id} disabled={Boolean(action.disabled || running)} onClick={() => invoke(action, source.index)}>{running === action.id ? "处理中…" : action.label}</Button>)}</div>}
      </>}
    </Section>)}
  </>;
}

export function ConversationOriginButton({ record, actions = [], label = "查看来源会话" }) {
  const s = useStore(), [openedTenantId, setOpenedTenantId] = useViewState(`conversation-origin.${record?.id || "object"}`, "");
  const result = resolveConversationOrigins(s, record);
  return <>
    <Button onClick={() => setOpenedTenantId(s.activeTenant)}>{label}{result.sources.length > 1 ? `（${result.sources.length}）` : ""}</Button>
    {openedTenantId && <Modal title="来源会话" wide onClose={() => setOpenedTenantId("")}><ConversationOriginPanel record={record} actions={actions} openedTenantId={openedTenantId} /></Modal>}
  </>;
}
