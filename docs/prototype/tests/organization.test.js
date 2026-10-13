import { beforeEach, expect, test } from "bun:test";
if (!globalThis.localStorage) {
  const entries = new Map();
  Object.defineProperty(globalThis, "localStorage", { configurable: true, value: {
    getItem: (key) => entries.get(key) || null, setItem: (key, value) => entries.set(key, value), removeItem: (key) => entries.delete(key),
  } });
}
const { read, reset, update, saveOrganization, saveWorkTeam, organizationPeople, createTenant, switchTenant } = await import("../src/store.js");
beforeEach(() => reset());
const node = () => ({ name: "测试部门", parentId: "ORG-root", ownerId: "H-lin", memberIds: ["H-lin"], start: "2026-10-13", end: "" });
const team = () => ({ ownerId: "H-xu", previousOwner: read().work.find((w) => w.id === "W-103").owner, revision: 0,
  coordinatorId: "H-lin", executorIds: ["H-lin", "agent:atlas", "agent:atlas"], checkerIds: ["agent:verifier"], receiverId: "H-xu" });
const unchanged = (fn, message) => { const before = structuredClone(read()); expect(fn).toThrow(message); expect(read()).toEqual(before); };

test("organization nodes create, move and retain before/after effective-dated history", () => {
  const id = saveOrganization("nodes", node(), "建立跨职能部门");
  const first = read().organization.nodes.find((n) => n.id === id);
  expect(first).toMatchObject({ ...node(), revision: 1 });
  saveOrganization("nodes", { ...first, parentId: "ORG-creative", end: "2026-12-31", memberIds: ["H-lin", "H-lin"] }, "调整归属");
  const saved = read().organization.nodes.find((n) => n.id === id);
  expect(saved).toMatchObject({ parentId: "ORG-creative", memberIds: ["H-lin"], revision: 2, end: "2026-12-31" });
  expect(saved.history).toHaveLength(2);
  expect(saved.history[1]).toMatchObject({ previous: { parentId: "ORG-root", end: "" }, next: { parentId: "ORG-creative", end: "2026-12-31" }, reason: "调整归属", actor: "当前负责人" });
  expect(Number.isNaN(Date.parse(saved.history[1].time))).toBe(false);
  unchanged(() => saveOrganization("nodes", first, "旧表单"), "记录已变化");
});

test("organization forbids self and descendant cycles without side effects", () => {
  const root = read().organization.nodes.find((n) => n.id === "ORG-root");
  for (const parentId of ["ORG-root", "ORG-engineering"])
    unchanged(() => saveOrganization("nodes", { ...root, parentId }, "移动"), "循环");
});

test("organization validates current tenant references, Human owner and real date interval", () => {
  for (const [patch, message] of [[{ ownerId: "agent:atlas" }, "Human"], [{ ownerId: "foreign" }, "Human"], [{ parentId: "foreign" }, "上级"],
    [{ memberIds: ["foreign"] }, "当前租户"], [{ memberIds: ["agent:atlas"] }, "当前租户"], [{ start: "2026-02-30" }, "日期"], [{ end: "2026-10-12" }, "日期"], [{ end: "2026-13-01" }, "日期"]])
    unchanged(() => saveOrganization("nodes", { ...node(), ...patch }, "变更"), message);
});

test("cross-project roles leave formal memberships unchanged and count each identity once", () => {
  const before = structuredClone(read().organization.nodes);
  const projects = read().projects.map((p) => p.id);
  for (const projectId of projects) saveOrganization("projectRoles", { memberId: "H-lin", projectId, role: "评审者" }, "跨项目协作");
  expect(read().organization.nodes).toEqual(before);
  expect(read().organization.projectRoles.filter((r) => r.memberId === "H-lin" && r.role === "评审者")).toHaveLength(projects.length);
  const people = organizationPeople(read()); expect(new Set(people.map((p) => p.id)).size).toBe(people.length);
  unchanged(() => saveOrganization("projectRoles", { memberId: "H-lin", projectId: projects[0], role: "评审者" }, "重复"), "已具有");
  unchanged(() => saveOrganization("projectRoles", { memberId: "H-lin", projectId: "foreign", role: "评审者" }, "无效"), "当前租户");
});

test("empty tenants isolate organization, Agent roster and project roles across switches", () => {
  const original = structuredClone(read().organization);
  const tenant = createTenant("独立组织");
  expect(read().organization.nodes).toEqual([]);
  expect(read().organization.projectRoles).toEqual([]);
  expect(organizationPeople(read()).map((person) => person.id)).toEqual(["H-lin"]);
  expect(read().agents).toEqual([]);
  unchanged(() => saveOrganization("nodes", { ...node(), ownerId: "H-zhou" }, "泄漏身份"), "当前租户");
  const member = saveOrganization("members", { name: "本地 Human" }, "显式添加");
  saveOrganization("nodes", { ...node(), parentId: "", ownerId: member, memberIds: [member] }, "建立本地组织");
  const local = structuredClone(read().organization);
  switchTenant("studio"); expect(read().organization).toEqual(original);
  switchTenant(tenant); expect(read().organization).toEqual(local);
  expect(organizationPeople(read()).map((p) => p.name)).toEqual(["林予", "本地 Human"]);
});

test("small team synchronizes sole Human result owner and preserves delivery and work state", () => {
  const previous = structuredClone(read().work.find((w) => w.id === "W-103"));
  saveWorkTeam("W-103", team(), "多执行者协作");
  const saved = read().work.find((w) => w.id === "W-103");
  expect(saved.owner).toBe("许言");
  expect(saved.team).toMatchObject({ revision: 1, coordinatorId: "H-lin", executorIds: ["H-lin", "agent:atlas"], checkerIds: ["agent:verifier"], receiverId: "H-xu" });
  expect(saved.team).not.toHaveProperty("ownerId");
  expect(saved.team.history[0]).toMatchObject({ previous: { owner: previous.owner }, next: { owner: "许言" }, reason: "多执行者协作" });
  const { owner, team: ignored, ...rest } = saved;
  const { owner: oldOwner, ...oldRest } = previous;
  expect(rest).toEqual(oldRest);
});

test("small team rejects stale team revision or changed owner", () => {
  const input = team(); saveWorkTeam("W-103", input, "首次配置");
  unchanged(() => saveWorkTeam("W-103", input, "旧版本"), "已变化");
  reset(); const stale = team(); update((s) => { s.work.find((w) => w.id === "W-103").owner = "许知"; });
  unchanged(() => saveWorkTeam("W-103", stale, "旧负责人"), "已变化");
});

test("every team role uses current tenant identities and result owner must be Human", () => {
  for (const patch of [{ ownerId: "agent:atlas" }, { coordinatorId: "foreign" }, { receiverId: "foreign" }, { executorIds: [] }, { checkerIds: [] }, { executorIds: ["foreign"] }, { checkerIds: ["foreign"] }])
    unchanged(() => saveWorkTeam("W-103", { ...team(), ...patch }, "错误配置"), /Human|指定|不能为空/);
});

for (const scenario of ["offline", "executor", "external"]) test(`${scenario} cannot configure organization or team`, () => {
  update((s) => { if (scenario === "offline") s.offline = true; else s.role = scenario; });
  unchanged(() => saveOrganization("nodes", node(), "越权"), /离线|只读|负责人|外部/);
  unchanged(() => saveWorkTeam("W-103", team(), "越权"), /离线|只读|负责人|外部/);
});

test("legacy tenant snapshots migrate their own organization without active-tenant leakage", async () => {
  const tenant = createTenant("旧版租户");
  const saved = structuredClone(read());
  delete saved.organization;
  delete saved.tenantData.studio.organization;
  localStorage.setItem("collaboration-prototype-v1", JSON.stringify(saved));
  const migrated = await import(`../src/store.js?organization-migration=${Date.now()}`);
  expect(migrated.read().organization).toEqual({ nodes: [], members: [], projectRoles: [] });
  migrated.switchTenant("studio");
  expect(migrated.organizationPeople(migrated.read()).some((p) => p.id === "H-lin")).toBe(true);
  migrated.switchTenant(tenant);
  expect(migrated.organizationPeople(migrated.read())).toEqual([]);
});

const { saveMemberInvitation, revokeMemberInvitation } = await import("../src/store.js");
const invite = () => ({ name: "新同事", email: "new@example.test", ownerId: "H-xu", projectId: read().projects[1].id,
  role: "项目成员", scopeKind: "project", expires: new Date(Date.now() + 86400000 * 5).toISOString().slice(0, 10) });
test("local invitations record selected tenant project, inviter, scope and expiry without adding members", () => {
  const org = structuredClone(read().organization), input = invite();
  const id = saveMemberInvitation(input, "协作邀请");
  const record = read().resources.invitations.find((item) => item.id === id);
  expect(record.createdBy).toBe("林予");
  expect(record.history[0].actor).toBe("林予");
  expect(record).toMatchObject({ ...input, owner: "许言", status: "待接受", revision: 1 });
  expect(record.scope).toBe(`${read().projects[1].name} · 所选项目协作内容`);
  expect(read().organization).toEqual(org);
  revokeMemberInvitation(id, "范围变化", 1);
  expect(read().resources.invitations.find((item) => item.id === id)).toMatchObject({ status: "已撤销", revision: 2 });
  expect(read().resources.invitations.find((item) => item.id === id).history.map((item) => item.reason)).toEqual(["协作邀请", "范围变化"]);
  unchanged(() => revokeMemberInvitation(id, "重复撤销", 2), "只能撤销");
});
test("invitation rejects foreign projects/identities, unsafe external scope and invalid expiration", () => {
  for (const [patch, message] of [[{ projectId: "foreign" }, "当前租户"], [{ ownerId: "agent:atlas" }, "Human"],
    [{ role: "外部客户", scopeKind: "project" }, "已共享成果"], [{ expires: "2000-01-01" }, "截止日期"],
    [{ expires: "2099-02-30" }, "截止日期"], [{ email: "invalid" }, "电子邮件"]])
    unchanged(() => saveMemberInvitation({ ...invite(), ...patch }, "错误邀请"), message);
  const id = saveMemberInvitation({ ...invite(), role: "外部客户", scopeKind: "shared" }, "受限成果邀请");
  expect(read().resources.invitations.find((item) => item.id === id).scope).toContain("已共享成果");
  unchanged(() => revokeMemberInvitation(id, "旧表单", 0), "已变化");
});
test("invitation access remains owner-only and tenant-isolated", () => {
  const input = invite(); const invitationId = saveMemberInvitation(input, "本地");
  createTenant("独立邀请空间");
  unchanged(() => saveMemberInvitation(input, "跨租户"), "当前租户");
  unchanged(() => revokeMemberInvitation(invitationId, "跨租户撤销", 1), "当前租户");
  for (const role of ["executor", "external"]) {
    reset(); update((s) => { s.role = role; });
    unchanged(() => saveMemberInvitation(input, "越权"), /负责人|只读|外部/);
  }
  reset(); update((s) => { s.offline = true; });
  unchanged(() => saveMemberInvitation(input, "离线"), "离线");
});

test("generic invitation resource paths cannot bypass scoped invitation lifecycle", async () => {
  const {saveResource,resourceCommand}=await import("../src/store.js");
  for(const role of ["owner","executor"]){
    reset();update(s=>{s.role=role;});
    unchanged(()=>saveResource("invitations",{name:"非法邀请",email:"x@example.test",expires:"2020-01-01",role:"外部客户",scope:"全部组织"}),"邀请流程");
    unchanged(()=>resourceCommand("invitations","INV-01","待接受","跳过范围"),"邀请流程");
  }
});
