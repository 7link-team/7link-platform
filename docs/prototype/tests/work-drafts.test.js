import { test, expect, beforeEach } from "bun:test";
if (!globalThis.localStorage) {
  const data = new Map();
  globalThis.localStorage = { getItem: key => data.get(key) || null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) };
}
const { read, reset, update, beginWorkDraft, saveWorkDraft, confirmWorkDraft, switchTenant } = await import("../src/store.js?work-draft-tests");
beforeEach(reset);

test("conversation draft remains outside formal work until Human confirms goal and criteria", () => {
  const originalCount = read().work.length;
  const draftId = beginWorkDraft({ projectId: "launch", channel: "launch" });
  expect(read().work).toHaveLength(originalCount);
  expect(read().drafts[draftId]).toMatchObject({ project: "launch", channel: "launch", status: "讨论中", tenantId: read().activeTenant });
  expect(() => confirmWorkDraft(draftId, 0)).toThrow("补充");
  saveWorkDraft(draftId, { title: "目标", criteria: "可验证条款", type: "文档", project: "launch" }, 0);
  expect(read().work).toHaveLength(originalCount);
  update(s => { s.drafts[draftId].discussion.push({ messageId: "discussion", text: "确认范围" }); });
  const work = confirmWorkDraft(draftId, 1);
  expect(work).toMatchObject({ title: "目标", plan: true, flow: false, status: "待开始", project: "launch", draftId, requirements: ["可验证条款"], agent: "未分派", agentId: null });
  expect(work.planSnapshot.discussion[0].text).toBe("确认范围");
  expect(confirmWorkDraft(draftId, 1).id).toBe(work.id);
  expect(read().work).toHaveLength(originalCount + 1);
  expect(() => saveWorkDraft(draftId, { title: "覆盖" }, 1)).toThrow("已生成");
});

test("message conversion preserves original source, thread and explicit project independent of global scope", () => {
  update(s => { s.project = "novel"; s.messages.push({ id: "source", channel: "launch", thread: "root", text: "原目标" }); });
  const draftId = beginWorkDraft({ sourceId: "source", projectId: "launch", channel: "launch", threadId: "root" });
  expect(beginWorkDraft({ sourceId: "source", projectId: "launch" })).toBe(draftId);
  const draft = read().drafts[draftId];
  expect(draft).toMatchObject({ project: "launch", channel: "launch", threadId: "root", title: "原目标" });
  update(s => { s.messages.find(m => m.id === "source").text = "后来编辑"; });
  saveWorkDraft(draftId, { title: "确认目标", criteria: "验收条款", type: "软件", project: "launch" }, 0);
  const work = confirmWorkDraft(draftId, 1);
  expect(work.source).toBe("source");
  expect(work.sourceContext).toMatchObject({ text: "原目标", messageId: "source", channel: "launch", threadId: "root", project: "launch", tenantId: "studio" });
});

test("work drafts reject stale edits, unavailable context, offline and unauthorized confirmation", () => {
  expect(() => beginWorkDraft({ sourceId: "absent" })).toThrow("来源消息");
  expect(() => beginWorkDraft({ executionId: "absent" })).toThrow("来源工作");
  expect(() => beginWorkDraft({ projectId: "absent" })).toThrow("归属项目");
  const draftId = beginWorkDraft({ executionId: "W-104" });
  const fields = { title: "目标", criteria: "条款", type: "软件", project: "launch" };
  saveWorkDraft(draftId, fields, 0);
  expect(() => saveWorkDraft(draftId, fields, 0)).toThrow("已变化");
  expect(() => confirmWorkDraft(draftId, 0)).toThrow("已变化");
  update(s => { s.role = "executor"; });
  expect(() => confirmWorkDraft(draftId, 1)).toThrow("Human");
  update(s => { s.role = "owner"; s.offline = true; });
  expect(() => beginWorkDraft()).toThrow();
});

test("tenant switching isolates drafts and rejects foreign draft confirmation", () => {
  const draftId = beginWorkDraft({ projectId: "launch" });
  update(s => { s.tenants.push({ id: "empty", name: "Empty", role: "owner" }); });
  switchTenant("empty");
  expect(read().drafts[draftId]).toBeUndefined();
  expect(() => confirmWorkDraft(draftId, 0)).toThrow("当前租户");
  expect(() => beginWorkDraft({ sourceId: "m1" })).toThrow("来源消息");
  switchTenant("studio");
  expect(read().drafts[draftId].project).toBe("launch");
});


test("source channel and execution determine context when no explicit project is supplied", () => {
  update(s => { s.project = "novel"; s.messages.push({ id: "execution-source", channel: "exec-W-104", text: "执行讨论" }); });
  const channelDraft = beginWorkDraft({ sourceId: "m1" });
  const privateDraft = beginWorkDraft({ sourceId: "m6" });
  expect(read().drafts[channelDraft].project).toBe("launch");
  expect(read().drafts[privateDraft].project).toBe("private");
  const draftId = beginWorkDraft({ sourceId: "execution-source" });
  expect(read().drafts[draftId]).toMatchObject({ executionId: "W-104", channel: "exec-W-104", project: "launch" });
});
