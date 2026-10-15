import { legacyPeriodFixture, prepareStructuredReport } from "./report-lifecycle-fixtures";
import { beforeEach, expect, test } from "bun:test";
import { reportAuthor, reportReviewAuthority, reportSubmissionContent } from "../src/report-authorization.js";
const storage = new Map();
Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: key => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) } });
const api = await import("../src/store.js?report-authorization-tests");
const { reset, read, update, generatePeriodicReport, resourceCommand, saveResource } = api;
beforeEach(reset);
const get = id => read().resources.reports.find(report => report.id === id);
const create = (subjectKey = "human:H-lin") => {
 const options={subjectKey,cadence:"日报",date:"2026-10-14",timezone:"Asia/Singapore"};
 // Incoming peer and aggregate legacy scenarios remain explicit compatibility cases.
 if(subjectKey!=="human:H-lin" && subjectKey!=="agent:atlas")return legacyPeriodFixture(api,options);
 const id=generatePeriodicReport(options);prepareStructuredReport(api,id);return id;
};
const act = (id, status, simulateAgent = false, receipt = {}) => resourceCommand("reports", id, status, "核对当前固定报告与依据", "", { revision: get(id).revision || 1, status: get(id).status, simulateAgent, ...receipt });
// Incoming peer submission fixture models another account's already-recorded action;
// this client cannot impersonate that Human to submit it through the current account.
const peerSubmission = () => {
  const id = create("human:H-zhou");
  update(s => { const report = s.resources.reports.find(item => item.id === id); report.status = "已提交";
    report.submission = { revision: report.revision || 1, authorId: "H-zhou", authorKind: "Human", actorId: "H-zhou", actorKind: "Human", confirmerId: "H-lin",
      content: reportSubmissionContent(report), time: "2026-10-13T08:00:00Z", source: "其他账号的本人提交" }; });
  return id;
};
test("Human member submission never authorizes self-review, including with a configured separate superior", () => {
  const id = create(); act(id, "已提交");
  expect(get(id).submission).toMatchObject({ authorId: "H-lin", authorKind: "Human", source: "Human 本人确认上报" });
  for (const configured of [false, true]) {
    if (configured) update(s => { s.planningSettings = { tenantOwnerId: "H-zhou" }; });
    for (const status of ["已接受", "草稿"]) { const before = structuredClone(read()); expect(() => act(id, status)).toThrow("本人"); expect(read()).toEqual(before); }
  }
});
test("author identity survives renames and cannot be replaced by same-name member or edited by a manager", () => {
  const id = create(); update(s => { s.organization.members.find(person => person.id === "H-lin").name = "更名后本人"; });
  act(id, "已提交"); expect(get(id).submission.authorId).toBe("H-lin");
  const peer = create("human:H-zhou"); const before = structuredClone(read());
  expect(() => saveResource("reports", { ...get(peer), owner: "更名后本人", summary: "代写正式正文" })).toThrow("本人"); expect(read()).toEqual(before);
  update(s => { s.organization.members.push({ id: "H-duplicate", name: "周宁", kind: "Human" }); });
  expect(reportAuthor(read(), { owner: "周宁" })).toBeNull();
  expect(reportAuthor(read(), get(peer)).id).toBe("H-zhou");
});
test("explicit Agent simulation submits a fixed record and Human confirmation preserves its author", () => {
  const id = create("agent:atlas");
  expect(() => act(id, "已提交")).toThrow("模拟上报");
  act(id, "已提交", true); const submitted = structuredClone(get(id).submission);
  expect(submitted).toMatchObject({ actorKind: "Agent", actorId: "atlas", recordedBy: "H-lin", source: "本地 Agent 模拟上报" });
  act(id, "已接受");
  expect(get(id).approval).toMatchObject({ actorKind: "Human", actorId: "H-lin", submissionRevision: submitted.revision });
  expect(get(id).approval.snapshot).toEqual(submitted);
  expect(get(id).submission).toEqual(submitted);
  expect(get(id).history.at(-1).snapshot.status).toBe("已提交");
});
test("designated reviewer can confirm a peer submission but a generic manager cannot replace that reviewer", () => {
  const id = peerSubmission(); act(id, "已接受"); expect(get(id).approval.actorId).toBe("H-lin");
  const other = create("agent:quill");
  update(s => { s.agents.find(agent => agent.id === "quill").ownerPrincipalId = "H-zhou"; });
  expect(reportReviewAuthority(read(), get(other)).id).toBe("H-zhou");
  const before = structuredClone(read()); expect(() => act(other, "已提交", true)).toThrow("负责人类"); expect(read()).toEqual(before);
});
test("missing, invalid, changed and self-test identities never fabricate reviewer authority", () => {
  const id = create("agent:atlas"); act(id, "已提交", true);
  for (const ownerPrincipalId of ["H-zhou", "missing", "agent:atlas"]) {
    update(s => { s.agents.find(agent => agent.id === "atlas").ownerPrincipalId = ownerPrincipalId; });
    const before = structuredClone(read()); expect(() => act(id, "已接受")).toThrow(); expect(read()).toEqual(before);
  }
});
test("TEAM owner submission follows the documented system-confirmation exception without manual self-review", () => {
  const id = create("tenant:studio");
  update(s => { s.role = "executor"; });
  const before = structuredClone(read()); expect(() => act(id, "已提交")).toThrow("所有者权限"); expect(read()).toEqual(before);
  update(s => { s.role = "owner"; }); act(id, "已提交");
  expect(get(id).status).toBe("已接受"); expect(get(id).approval).toMatchObject({ actorId: "SYSTEM:team_top", actorKind: "System" });
  expect(get(id).submission.authorId).toBe("H-lin"); expect(() => act(id, "已接受")).toThrow("已提交或待审核");
});
test("decisions bind author, exact body and revision; old forms and injected lifecycle metadata are rejected", () => {
  const id = create("agent:atlas"); act(id, "已提交", true); const old = { revision: get(id).revision, status: get(id).status };
  saveResource("reports", { ...get(id), summary: "作者修订", submission: { forged: true }, approval: { actorId: "H-lin" } });
  expect(get(id).submission).toBeNull(); expect(get(id).approval).toBeNull(); expect(get(id).status).toBe("草稿");
  act(id, "已提交", true); expect(() => act(id, "已接受", false, old)).toThrow("已变化");
  update(s => { s.resources.reports.find(item => item.id === id).summary = "提交后被替换"; });
  const before = structuredClone(read()); expect(() => act(id, "已接受")).toThrow("固定提交记录"); expect(read()).toEqual(before);
});
test("unknown legacy submission is returned for correction, never certified as a new fixed approval", () => {
  const id = peerSubmission(); update(s => { delete s.resources.reports.find(item => item.id === id).submission; });
  expect(() => act(id, "已接受")).toThrow("固定提交记录"); act(id, "草稿"); expect(get(id).status).toBe("草稿");
  expect(get(id).history.at(-1).snapshot.status).toBe("已提交");
});

test("organization teams never inherit the one tenant-level system-confirmation exception", () => {
  update(s => { s.planningSettings = { tenantOwnerId: "H-zhou" }; });
  for (const node of read().organization.nodes.filter(node => node.ownerId === "H-lin")) {
    const id = create(`team:${node.id}`);
    expect(reportReviewAuthority(read(), get(id))).toMatchObject({ id: "H-zhou" });
    expect(reportReviewAuthority(read(), get(id)).system).toBeUndefined();
    act(id, "已提交");
    expect(get(id).status).toBe("已提交"); expect(get(id).approval).toBeNull();
    expect(get(id).submission.confirmerId).toBe("H-zhou");
    const before = structuredClone(read()); expect(() => act(id, "已接受")).toThrow("本人"); expect(read()).toEqual(before);
  }
});
test("a node owned by another Human requires tenant owner review of its fixed submission", () => {
  const id = create("team:ORG-creative");
  expect(reportReviewAuthority(read(), get(id))).toMatchObject({ id: "H-lin" });
  update(s => { const report = s.resources.reports.find(item => item.id === id); report.status = "已提交";
    report.submission = { revision: report.revision || 1, authorId: "H-zhou", authorKind: "Human", confirmerId: "H-lin", content: reportSubmissionContent(report) }; });
  act(id, "已接受"); expect(get(id).approval.actorId).toBe("H-lin"); expect(get(id).approval.actorKind).toBe("Human");
});
test("all legacy ownership representations reject reassignment atomically while preserving legal edits", () => {
  for (const identity of [{ owner: "林予" }, { owner: "old display", authorId: "H-lin", authorKind: "Human" }, { subjectKey: "human:H-lin", owner: "林予" }]) {
    reset(); const id = "legacy-author-probe";
    update(s => { s.resources.reports.push({ ...identity, id, name: "旧报告", status: "草稿", revision: 1, summary: "原文", references: [] }); });
    const period = { cadence: "日报", periodStart: "2026-10-14", periodEnd: "2026-10-14", timezone: "Asia/Singapore" };
    for (const subjectKey of ["human:H-zhou", "agent:atlas", ...(identity.subjectKey ? ["team:ORG-root", "project:launch"] : [])]) {
      const before = structuredClone(read());
      expect(() => saveResource("reports", { ...get(id), ...period, subjectKey, summary: "伪造他人归属" })).toThrow("归属不可改写");
      expect(read()).toEqual(before);
    }
    saveResource("reports", { ...get(id), ...(identity.subjectKey ? period : {}), summary: "本人更正正文" });
    expect(get(id).summary).toBe("本人更正正文"); expect(get(id).authorId).toBe("H-lin");
    saveResource("reports", { ...get(id), ...period, subjectKey: "human:H-lin" });
    expect(reportAuthor(read(), get(id)).id).toBe("H-lin"); expect(get(id).history.at(-1).summary).toBe("本人更正正文");
  }
});
test("missing submitted confirmer cannot acquire approval after current authority changes", () => {
  for (const missing of [null, undefined, ""]) {
    reset(); const id = peerSubmission();
    update(s => { s.resources.reports.find(item => item.id === id).submission.confirmerId = missing; });
    const before = structuredClone(read()); expect(() => act(id, "已接受")).toThrow("固定提交记录"); expect(read()).toEqual(before);
    act(id, "草稿"); expect(get(id).status).toBe("草稿");
    expect(get(id).history.at(-1).snapshot.submission.confirmerId).toBe(missing);
  }
});
test("all confirmed report kinds reject edits until explicit authorized version-bound REOPEN",()=>{
 for (const subject of ["agent:atlas","tenant:studio","human:H-zhou"]) {
  reset();const id=subject==="human:H-zhou"?peerSubmission():create(subject);
  if (subject!=="human:H-zhou") act(id,"已提交",subject.startsWith("agent:"));
  if (get(id).status!=="已接受") act(id,"已接受");
  const accepted=structuredClone(get(id)),before=structuredClone(read());
  expect(()=>saveResource("reports",{...get(id),summary:"绕过撤销确认"})).toThrow("不可编辑");expect(read()).toEqual(before);
  expect(()=>act(id,"草稿")).toThrow("已提交或待审核");expect(read()).toEqual(before);
  expect(()=>resourceCommand("reports",id,"草稿","更正","撤销确认",{action:"REOPEN"})).toThrow("修订或状态已变化");expect(read()).toEqual(before);
  expect(()=>resourceCommand("reports",id,"草稿","","撤销确认",{revision:accepted.revision,status:accepted.status,action:"REOPEN"})).toThrow("说明");expect(read()).toEqual(before);
  update(s=>{s.role="executor";});const denied=structuredClone(read());
  expect(()=>act(id,"草稿",false,{action:"REOPEN"})).toThrow("负责人");expect(read()).toEqual(denied);
  update(s=>{s.role="owner";s.planningSettings={tenantOwnerId:"H-zhou"};const r=s.resources.reports.find(r=>r.id===id);if(r.approval.actorKind==="Human")r.approval.actorId="H-zhou";});
  const foreign=structuredClone(read());expect(()=>act(id,"草稿",false,{action:"REOPEN"})).toThrow("原确认人");expect(read()).toEqual(foreign);
  update(s=>{s.planningSettings={tenantOwnerId:"H-lin"};});
  const fixed=structuredClone(get(id));act(id,"草稿",false,{action:"REOPEN"});
  expect(get(id)).toMatchObject({status:"草稿",submission:null,approval:null,revision:fixed.revision+1});
  expect(get(id).history.at(-1)).toMatchObject({action:"REOPEN",actorId:"H-lin",actorKind:"Human",snapshot:{...fixed,history:undefined}});
  expect(()=>act(id,"草稿",false,{action:"REOPEN"})).toThrow("只有已接受");
  if(subject!=="human:H-zhou"){
   saveResource("reports",{...get(id),summary:"显式撤销后的更正"});
   act(id,"已提交",subject.startsWith("agent:"));
   if(get(id).status!=="已接受")act(id,"已接受");
   expect(get(id).approval.snapshot.content.summary).toBe("显式撤销后的更正");
   expect(get(id).history.some(h=>h.action==="REOPEN"&&h.snapshot.approval)).toBe(true);
  }
 }
});
test("owner can reopen a fixed historical report after its author leaves without rewriting authorship",()=>{
 const id=peerSubmission();act(id,"已接受");
 update(s=>{s.organization.members=s.organization.members.filter(m=>m.id!=="H-zhou");});
 const fixed=structuredClone(get(id));act(id,"草稿",false,{action:"REOPEN"});
 expect(get(id)).toMatchObject({status:"草稿",authorId:fixed.authorId,authorKind:fixed.authorKind,owner:fixed.owner});
 expect(get(id).history.at(-1).snapshot.approval).toEqual(fixed.approval);
 expect(()=>saveResource("reports",{...get(id),summary:"替离职作者改写"})).toThrow("身份不明确");
});
