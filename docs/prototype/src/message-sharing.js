// Sharing copies explicitly selected text, never the source conversation's permissions.
export function messageProject(state, message) {
  const projectId = message.channel?.startsWith("exec-")
    ? state.work.find(work => work.id === message.channel.slice(5))?.project
    : message.channel;
  return state.projects.find(project => project.id === projectId && project.id !== "private") || null;
}

export function prepareMessageShare(state, input) {
  if (input.tenantId !== state.activeTenant) throw Error("租户已切换，请重新打开共享预览。");
  const source = state.messages.find(message => message.id === input.messageId);
  if (!source || source.recalled) throw Error("原消息已撤回或不存在，请重新选择。");
  if (source.text !== input.expectedText) throw Error("原消息已修订，请重新预览后共享。");
  if (!source.text?.trim()) throw Error("此消息没有可共享的文本；附件需单独授权。");
  if (input.target === "external") {
    const execution = source.channel?.startsWith("exec-") && state.work.find(work => work.id === source.channel.slice(5));
    if (source.channel === "private" || execution?.project === "private")
      throw Error("私人材料需要先获得可对外分享授权，不能直接转发。");
    return { source, channel: "external", targetName: "客户共创 · 外部共享频道" };
  }
  if (input.target !== "project") throw Error("请选择共享接收范围。");
  const project = state.projects.find(project => project.id === input.projectId && project.id !== "private");
  if (!project) throw Error("请明确选择当前租户中的接收项目。");
  return { source, channel: project.id, targetName: `${project.name} · 项目成员` };
}
