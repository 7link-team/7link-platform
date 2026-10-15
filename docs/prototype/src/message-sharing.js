import { executionRecord } from "./executions.js";
import { channelMessageReadProblem, assertChannelWrite } from "./channels.js";
// Sharing copies explicitly selected text, never the source conversation's permissions.
export function messageProject(state, message) {
  const projectId = message.channel?.startsWith("exec-")
    ? executionRecord(state,message.channel.slice(5))?.projectId
    : state.channels?.find(c => c.id === message.channel)?.projectId || message.channel;
  return state.projects.find(project => project.id === projectId && project.id !== "private") || null;
}

export function prepareMessageShare(state, input) {
  if (input.tenantId !== state.activeTenant) throw Error("租户已切换，请重新打开共享预览。");
  const source = state.messages.find(message => message.id === input.messageId);
  if (!source || source.recalled) throw Error("原消息已撤回或不存在，请重新选择。");
  const problem = channelMessageReadProblem(state, source); if (problem) throw Error(problem);
  if (!source.channel.startsWith("exec-")) assertChannelWrite(state, source.channel);
  if (source.text !== input.expectedText) throw Error("原消息已修订，请重新预览后共享。");
  if (!source.text?.trim()) throw Error("此消息没有可共享的文本；附件需单独授权。");
  const privateSource = state.channels.find(c => c.id === source.channel)?.kind === "DIRECT";
  if (input.target === "external") {
    const execution = source.channel?.startsWith("exec-") && executionRecord(state,source.channel.slice(5));
    if (privateSource || execution?.projectId === "private")
      throw Error("私人材料需要先获得可对外分享授权，不能直接转发。");
    assertChannelWrite(state, "external");
    return { source, channel: "external", targetName: "客户共创 · 外部共享频道" };
  }
  if (input.target === "channel") {
    const channel = state.channels.find(c => c.id === input.channelId && c.kind !== "DIRECT");
    if (!channel) throw Error("请选择明确的接收频道。");
    assertChannelWrite(state, channel.id);
    if (channel.shared && (privateSource || source.channel.startsWith("exec-") && executionRecord(state,source.channel.slice(5))?.projectId === "private")) throw Error("私人材料不能直接对外共享。");
    return { source, channel: channel.id, targetName: `${channel.name} · ${channel.shared ? "外部共享" : "频道成员"}` };
  }
  if (input.target !== "project") throw Error("请选择共享接收范围。");
  const project = state.projects.find(project => project.id === input.projectId && project.id !== "private");
  if (!project) throw Error("请明确选择当前租户中的接收项目。");
  assertChannelWrite(state, project.id);
  return { source, channel: project.id, targetName: `${project.name} · 项目成员` };
}
