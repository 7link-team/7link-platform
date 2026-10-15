import { test, expect, beforeEach } from "bun:test";
import { knowledgeOwner, knowledgeReadProblem, knowledgeWriteProblem, visibleKnowledge, visibleActivityEvents } from "../src/knowledge.js";
const storage = new Map();
globalThis.localStorage = { getItem: key => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) };
const api = await import("../src/store.js");
beforeEach(() => api.reset());
const privateRecord = () => api.read().resources.knowledge.find(record => record.id === "K-03");
function denied(command) {
  const before = structuredClone(api.read()), saved = [...storage];
  expect(command).toThrow(/私人|无权|历史|资料|流程|范围/); expect(api.read()).toEqual(before); expect([...storage]).toEqual(saved);
}
test("all internal role views and mutations deny another Human's private original and history", () => {
  for (const role of ["owner", "executor"]) {
    api.reset(); api.update(state => state.role = role);
    const record = structuredClone(privateRecord());
    expect(visibleKnowledge(api.read()).some(item => item.id === record.id)).toBe(false);
    expect(knowledgeReadProblem(api.read(), record)).toContain("私人");
    denied(() => api.saveResource("knowledge", { ...record, content: "unauthorized edit" }));
    denied(() => api.saveResource("knowledge", { ...record, owner: "林予", scope: "租户" }));
    denied(() => api.saveResource("knowledge", { ...record, owner: "林予", ownerId: "H-lin", scope: "私人" }));
    denied(() => api.saveResource("knowledge", { ...record, scope: undefined }));
    for (const status of ["有效", "已禁用", "未知动作"]) denied(() => api.resourceCommand("knowledge", record.id, status, "unauthorized decision"));
    denied(() => api.exportData("knowledge", [record]));
    denied(() => api.exportData("knowledge", { ...record, owner: "林予", scope: "租户" }));
  }
});
test("own private notes resolve unique legacy names and persist stable owner identity", () => {
  const own = { name: "本人私人笔记", owner: "林予", scope: "私人", status: "待确认", kind: "记忆", version: "v1", content: "本人正文", source: "message-source" };
  api.saveResource("knowledge", own);
  const saved = api.read().resources.knowledge.find(record => record.name === own.name);
  expect(saved.ownerId).toBe("H-lin"); expect(saved.ownerKind).toBe("Human");
  expect(visibleKnowledge(api.read()).some(record => record.id === saved.id)).toBe(true);
  api.saveResource("knowledge", { ...saved, content: "本人修订正文" });
  const changed = api.read().resources.knowledge.find(record => record.id === saved.id);
  expect(changed.history[0].content).toBe(own.content);
  api.resourceCommand("knowledge", saved.id, "有效", "本人确认");
  expect(api.read().resources.knowledge.find(record => record.id === saved.id).status).toBe("有效");
  api.resourceCommand("knowledge", saved.id, "已禁用", "本人禁用");
  expect(api.read().resources.knowledge.find(record => record.id === saved.id).status).toBe("已禁用");
  for (const scope of ["租户", "当前项目", "", undefined]) denied(() => api.saveResource("knowledge", { ...changed, scope }));
  denied(() => api.saveResource("knowledge", { ...changed, owner: "许言", ownerId: "H-xu" }));
});
test("unknown, ambiguous or contradictory private identities fail closed; stable IDs survive a name change", () => {
  const s = structuredClone(api.read());
  const base = { owner: "林予", scope: "私人" };
  expect(knowledgeReadProblem(s, base)).toBe("");
  for (const record of [{ ...base, owner: "unknown" }, { ...base, ownerId: "missing" }, { ...base, ownerId: "H-lin", ownerKind: "Agent" }, { ...base, ownerId: "H-xu" }, { ...base, owner: "许言", ownerId: "H-lin" }]) expect(knowledgeReadProblem(s, record)).not.toBe("");
  s.organization.members.push({ id: "another-human", name: "林予", kind: "Human" });
  expect(knowledgeOwner(s, base)).toBeNull();
  expect(knowledgeOwner(s, { ...base, ownerId: "H-lin" }).id).toBe("H-lin");
  s.organization.members = s.organization.members.filter(person => person.id !== "another-human");
  s.organization.members.find(person => person.id === "H-lin").name = "Human renamed";
  expect(knowledgeReadProblem(s, { ...base, ownerId: "H-lin" })).toBe("");
  s.organization.members = s.organization.members.filter(person => person.id !== "H-lin");
  expect(knowledgeReadProblem(s, { ...base, ownerId: "H-lin" })).not.toBe("");
});
test("private guard leaves existing public management controls and empty-tenant isolation intact", () => {
  const record = api.read().resources.knowledge.find(record => record.id === "K-01");
  expect(visibleKnowledge(api.read()).some(item => item.id === record.id)).toBe(true);
  api.saveResource("knowledge", { ...record, content: "public revision" });
  api.resourceCommand("knowledge", record.id, "已禁用", "public management control");
  expect(knowledgeWriteProblem(api.read(), { ...record, content: "public revision" }, record)).toBe("");
  api.createTenant("Empty library tenant");
  expect(visibleKnowledge(api.read())).toEqual([]);
  denied(() => api.exportData("knowledge", record));
  api.update(state => state.role = "external");
  expect(visibleKnowledge(api.read())).toEqual([]);
});


test("unknown scopes and unreadable historical snapshots never become public through malformed metadata", () => {
  for (const scope of [undefined, null, {}, [], 42, "", " ", "私有", "个人", "unknown"]) {
    api.reset(); api.update(state => state.resources.knowledge.find(record => record.id === "K-03").scope = scope);
    expect(visibleKnowledge(api.read()).some(record => record.id === "K-03")).toBe(false);
    denied(() => api.saveResource("knowledge", { ...privateRecord(), content: "tampered" }));
    denied(() => api.exportData("knowledge", privateRecord()));
  }
  api.reset(); const privateSnapshot = structuredClone(privateRecord());
  for (const history of [[privateSnapshot], [{ scope: "租户", owner: "林予", history: [privateSnapshot] }], [null], {}, [{ owner: "林予" }]]) {
    api.update(state => { const record = state.resources.knowledge.find(record => record.id === "K-01"); record.history = history; });
    expect(visibleKnowledge(api.read()).some(record => record.id === "K-01")).toBe(false);
    denied(() => api.exportData("knowledge", api.read().resources.knowledge.find(record => record.id === "K-01")));
  }
});
test("ordinary edits cannot inject history or widen historical private access", () => {
  const secret = structuredClone(privateRecord());
  denied(() => api.saveResource("knowledge", { name: "public shell", owner: "林予", scope: "租户", content: "public", history: [secret] }));
  const publicRecord = structuredClone(api.read().resources.knowledge.find(record => record.id === "K-01"));
  denied(() => api.saveResource("knowledge", { ...publicRecord, history: [secret] }));
  denied(() => api.exportData("knowledge", { ...publicRecord, history: [secret] }));
});
test("knowledge exports authorize saved originals and history before constructing a real Blob", async () => {
  const originalDocument = globalThis.document, createURL = URL.createObjectURL, revokeURL = URL.revokeObjectURL;
  const blobs = [], clicks = [];
  globalThis.document = { createElement: () => ({ click() { clicks.push(this.download); } }) };
  URL.createObjectURL = blob => { blobs.push(blob); return "blob:export-control"; };
  URL.revokeObjectURL = () => {};
  try {
    api.saveResource("knowledge", { name: "本人可导出资料", owner: "林予", scope: "私人", content: "本人私有正文", status: "待确认" });
    const own = api.read().resources.knowledge.find(record => record.name === "本人可导出资料");
    api.saveResource("knowledge", { ...own, content: "修订正文" });
    const changed = api.read().resources.knowledge.find(record => record.id === own.id);
    api.exportData("knowledge", changed);
    expect(blobs).toHaveLength(1); expect(clicks).toEqual(["knowledge.json"]);
    expect(JSON.parse(await blobs[0].text())).toEqual(changed);
    denied(() => api.exportData("knowledge", { ...privateRecord(), owner: "林予", ownerId: "H-lin", scope: "租户" }));
    denied(() => api.exportData("knowledge", [changed, privateRecord()]));
    expect(blobs).toHaveLength(1); expect(clicks).toHaveLength(1);
    api.createTenant("Export tenant change");
    denied(() => api.exportData("knowledge", changed));
    denied(() => api.saveResource("knowledge", changed));
    expect(blobs).toHaveLength(1);
  } finally {
    if (originalDocument === undefined) delete globalThis.document; else globalThis.document = originalDocument;
    URL.createObjectURL = createURL; URL.revokeObjectURL = revokeURL;
  }
});

test("public export cannot relabel private content as its own saved original or history", async () => {
  const originalDocument = globalThis.document, createURL = URL.createObjectURL, revokeURL = URL.revokeObjectURL;
  const blobs = [], clicks = [];
  globalThis.document = { createElement: () => ({ click() { clicks.push(this.download); } }) };
  URL.createObjectURL = blob => { blobs.push(blob); return "blob:original-export"; };
  URL.revokeObjectURL = () => {};
  try {
    const original = structuredClone(api.read().resources.knowledge.find(record => record.id === "K-01"));
    const secret = structuredClone(privateRecord());
    const relabeled = { ...secret, id: original.id, scope: "租户", owner: "林予", ownerId: "H-lin", ownerKind: "Human" };
    for (const forged of [
      { ...original, history: [{ ...relabeled, id: secret.id }] },
      { ...original, history: [relabeled] },
      { ...original, content: secret.content },
      { ...original, extraDocument: relabeled },
    ]) {
      denied(() => api.exportData("knowledge", forged));
      denied(() => api.exportData("knowledge", [original, forged]));
      expect(blobs).toHaveLength(0); expect(clicks).toHaveLength(0);
    }
    api.exportData("knowledge", original);
    api.exportData("knowledge", [original]);
    expect(blobs).toHaveLength(2); expect(clicks).toEqual(["knowledge.json", "knowledge.json"]);
    expect(JSON.parse(await blobs[0].text())).toEqual(original);
    expect(JSON.parse(await blobs[1].text())).toEqual([original]);
    api.saveResource("knowledge", { ...original, content: "saved public revision" });
    denied(() => api.exportData("knowledge", original));
    expect(blobs).toHaveLength(2);
    const revised = structuredClone(api.read().resources.knowledge.find(record => record.id === original.id));
    api.exportData("knowledge", revised);
    expect(JSON.parse(await blobs[2].text())).toEqual(revised);
    expect(revised.history[0].content).toBe(original.content);
  } finally {
    if (originalDocument === undefined) delete globalThis.document; else globalThis.document = originalDocument;
    URL.createObjectURL = createURL; URL.revokeObjectURL = revokeURL;
  }
});

test("saved full histories require the same original identity and retain historical privacy", () => {
  const original = structuredClone(api.read().resources.knowledge.find(record => record.id === "K-01"));
  for (const historicalId of [undefined, "", "unknown", "K-03"]) {
    const malformed = { ...original, history: [{ ...original, id: historicalId }] };
    expect(knowledgeReadProblem(api.read(), malformed)).toContain("历史");
    api.update(state => state.resources.knowledge[0] = malformed);
    expect(visibleKnowledge(api.read()).some(record => record.id === original.id)).toBe(false);
    denied(() => api.exportData("knowledge", malformed));
  }
  api.reset();
  expect(knowledgeReadProblem(api.read(), { ...original, history: [{ ...original, content: "real old public version" }] })).toBe("");
  expect(knowledgeReadProblem(api.read(), { ...original, history: [{ ...original, scope: "私人", owner: "许言" }] })).toContain("私人");
});


test("status-only history stays readable but cannot disguise embedded document fields", () => {
  const publicRecord = structuredClone(api.read().resources.knowledge.find(record => record.id === "K-01"));
  for (const event of [{ status: "待确认", actor: "林予", reason: "原决定", time: "2026-10-14T00:00:00Z" }, { status: "有效" }]) {
    expect(knowledgeReadProblem(api.read(), { ...publicRecord, history: [event] })).toBe("");
  }
  for (const event of [{ status: "有效", content: "private document content" }, { status: "有效", owner: "许言" }, { status: "有效", history: [privateRecord()] }, { status: "有效", actor: {} }]) {
    expect(knowledgeReadProblem(api.read(), { ...publicRecord, history: [event] })).not.toBe("");
  }
});


test("old tenant forms cannot resurrect documents under a new tenant even without legacy tenant metadata", () => {
  const stale = structuredClone(api.read().resources.knowledge.find(record => record.id === "K-01"));
  api.createTenant("Tenant stale form control");
  denied(() => api.saveResource("knowledge", stale));
  expect(api.read().resources.knowledge).toHaveLength(0);
  api.saveResource("knowledge", { name: "new tenant document", owner: "林予", scope: "租户", content: "new source" });
  expect(api.read().resources.knowledge[0].tenantId).toBe(api.read().activeTenant);
});

test("knowledge activity views and real audit exports follow original source permissions", async () => {
  const originalDocument = globalThis.document, createURL = URL.createObjectURL, revokeURL = URL.revokeObjectURL;
  const blobs = [];
  globalThis.document = { createElement: () => ({ click() {} }) };
  URL.createObjectURL = blob => { blobs.push(blob); return "blob:audit-export"; };
  URL.revokeObjectURL = () => {};
  try {
    const secret = structuredClone(privateRecord());
    const legacy = [
      { id: "legacy-private-action", target: `/knowledge/${secret.id}`, text: `${secret.name} → 已禁用 · 私人原因` },
      { id: "legacy-private-save", target: "/knowledge", text: `更新${secret.name}` },
      { id: "missing-knowledge-source", target: "/knowledge/missing", text: "来源已丢失的资料标题" },
    ];
    api.update(state => state.events.unshift(...legacy));
    const preserved = structuredClone(api.read().events);
    for (const role of ["owner", "executor", "external"]) {
      api.update(state => state.role = role);
      expect(visibleActivityEvents(api.read()).some(event => legacy.some(old => old.id === event.id))).toBe(false);
      if (role === "external") {
        expect(visibleActivityEvents(api.read())).toEqual([]);
        denied(() => api.exportData("audit", preserved.find(event => event.target !== "/knowledge" && !event.target.startsWith("/knowledge/"))));
      }
      for (const event of legacy) denied(() => api.exportData("audit", event));
      expect(blobs).toHaveLength(0);
    }
    expect(api.read().events).toEqual(preserved);
    api.update(state => state.role = "owner");
    api.saveResource("knowledge", { name: "本人活动来源", owner: "林予", scope: "私人", content: "本人正文", status: "待确认" });
    const own = api.read().resources.knowledge.find(record => record.name === "本人活动来源");
    const created = api.read().events[0];
    expect(created.target).toBe(`/knowledge/${own.id}`);
    expect(visibleActivityEvents(api.read()).some(event => event.id === created.id)).toBe(true);
    api.resourceCommand("knowledge", own.id, "有效", "本人确认");
    const readable = visibleActivityEvents(api.read());
    expect(readable.some(event => event.target === "/knowledge/K-03")).toBe(false);
    expect(readable.some(event => event.target === created.target && event.text.includes("本人确认"))).toBe(true);
    api.exportData("audit", readable);
    expect(JSON.parse(await blobs[0].text())).toEqual(readable);
    denied(() => api.exportData("audit", [readable[0], legacy[0]]));
    denied(() => api.exportData("audit", { ...created, text: secret.name }));
    expect(blobs).toHaveLength(1);
    api.update(state => state.organization.members = state.organization.members.filter(person => person.id !== "H-lin"));
    expect(visibleActivityEvents(api.read()).some(event => event.id === created.id)).toBe(false);
  } finally {
    if (originalDocument === undefined) delete globalThis.document; else globalThis.document = originalDocument;
    URL.createObjectURL = createURL; URL.revokeObjectURL = revokeURL;
  }
});

test("knowledge and material audit aliases apply identical source permissions without rewriting history", () => {
  const s=structuredClone(api.read());
  const privateSource=s.resources.knowledge.find(record=>record.id==='K-03');
  expect(knowledgeReadProblem(s,privateSource)).not.toBe('');
  s.events=['/knowledge/K-03','/materials/K-03','/knowledge','/materials?scope=all'].map((target,index)=>({id:'alias-'+index,target,text:privateSource.name,actor:'test',time:'09:00'}));
  const before=structuredClone(s.events);
  expect(visibleActivityEvents(s)).toEqual([]);
  expect(s.events).toEqual(before);
});
