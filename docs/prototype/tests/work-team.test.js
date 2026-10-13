import { completeValidation } from "./qa-helpers.js";
import { test, expect, beforeEach } from "bun:test";
if (!globalThis.localStorage) { const data = new Map(); globalThis.localStorage = { getItem: key => data.get(key) || null, setItem: (key, value) => data.set(key, value), removeItem: key => data.delete(key) }; }
const { publishTestReport, startTestBatch, workCommand, recordBatchResult, read, reset, update, saveWorkTeam, workExecutors, prepareInputHandoff, saveWorkInputs } = await import("../src/store.js?work-team-tests");
const verifyWork = workId => completeValidation({ read, workCommand, recordBatchResult, publishTestReport, startTestBatch }, workId);
beforeEach(() => { reset(); verifyWork("W-103"); });
const work = () => read().work.find(item => item.id === "W-103");
const input = (patch = {}) => ({ revision: work().team?.revision || 0, previousOwner: work().owner, ownerId: "H-xu", coordinatorId: "agent:atlas", executorIds: ["agent:atlas", "H-lin"], checkerIds: ["agent:verifier"], receiverId: "H-lin", ...patch });

test("task composition links Human and Agent identities without changing organization, lifecycle or historical authors", () => {
  const organization = structuredClone(read().organization), agents = structuredClone(read().agents), oldWork = structuredClone(work());
  saveWorkTeam("W-103", input(), "当前任务协作编组");
  expect(workExecutors(read(), work()).map(person => person.name)).toEqual(["Atlas", "林予"]);
  expect(work().owner).toBe("许言");
  expect(work().agent).toBe(oldWork.agent); expect(work().history).toEqual(oldWork.history);
  expect(read().organization).toEqual(organization); expect(read().agents).toEqual(agents);
  expect(work().completionContract).toEqual(oldWork.completionContract);
  update(s => { s.agents.find(agent => agent.id === "atlas").name = "Atlas 新名称"; });
  expect(workExecutors(read(), work())[0].name).toBe("Atlas 新名称");
  expect(work().team.executorIds[0]).toBe("agent:atlas");
});

test("L3 receiver must be Human and next fixed package follows explicit team receiver without editing old packages", () => {
  const original = structuredClone(read());
  expect(() => saveWorkTeam("W-103", input({ receiverId: "agent:atlas" }), "Agent不能作Human接收决定")).toThrow("Human");
  expect(read()).toEqual(original);
  saveWorkTeam("W-103", input(), "明确下游 Human 接收");
  const packageId = prepareInputHandoff("W-103", "W-104", 2);
  expect(work().handoffs[0]).toMatchObject({ id: packageId, recipientId: "H-lin", recipient: "林予" });
  saveWorkInputs("W-104", [{ workId: "W-103", version: 2, handoffId: packageId }], "按包接收", 3);
  const fixed = structuredClone(work().handoffs[0]);
  saveWorkTeam("W-103", input({ receiverId: "H-xu" }), "后续包接收人调整");
  expect(work().handoffs[0]).toEqual(fixed);
  const nextPackage = prepareInputHandoff("W-103", "W-104", 2);
  expect(nextPackage).not.toBe(packageId);
  expect(work().handoffs[1].recipientId).toBe("H-xu");
});

test("team revisions reject stale state and current roster membership is authoritative", () => {
  const first = input(); saveWorkTeam("W-103", first, "首次编组");
  expect(() => saveWorkTeam("W-103", first, "过期编组")).toThrow("已变化");
  update(s => { s.agents = s.agents.filter(agent => agent.id !== "atlas"); });
  expect(workExecutors(read(), work())[0].name).toBe("成员已不可用");
  expect(() => saveWorkTeam("W-103", input(), "已不存在的Agent")).toThrow("协调者");
  expect(work().team.history).toHaveLength(1);
});


test("legacy executor lookup uses stable Agent identity while unassigned work invents no member", () => {
  const current = { agent: "Atlas", agentId: "atlas" };
  update(s => { s.agents.find(agent => agent.id === "atlas").name = "Renamed"; });
  expect(workExecutors(read(), current)).toEqual([{ id: "agent:atlas", name: "Renamed", kind: "Agent" }]);
  expect(workExecutors(read(), { agent: "未分派", agentId: null })).toEqual([]);
  expect(workExecutors(read(), { agent: "Old unbound" })).toEqual([{ id: "legacy", name: "Old unbound", kind: "未绑定身份" }]);
});


for (const status of ["退役", "待批准"]) test(`${status} Agent cannot be newly assigned to any task role but existing references survive`, () => {
  update(s => { s.agents.find(agent => agent.id === "atlas").status = status; });
  for (const patch of [{ coordinatorId: "agent:atlas", executorIds: ["H-lin"] }, { coordinatorId: "H-lin", executorIds: ["agent:atlas"] }, { coordinatorId: "H-lin", executorIds: ["H-lin"], checkerIds: ["agent:atlas"] }])
    expect(() => saveWorkTeam("W-103", input(patch), "不可用Agent")).toThrow("不能新分派");
  update(s => { s.agents.find(agent => agent.id === "atlas").status = "空闲"; });
  saveWorkTeam("W-103", input(), "先有效分派");
  update(s => { s.agents.find(agent => agent.id === "atlas").status = status; });
  saveWorkTeam("W-103", input(), "保留既有编组引用");
  expect(work().team.executorIds).toContain("agent:atlas");
  expect(() => saveWorkTeam("W-103", input({ checkerIds: ["agent:atlas"] }), "新增检查角色")).toThrow("不能新分派");
});
