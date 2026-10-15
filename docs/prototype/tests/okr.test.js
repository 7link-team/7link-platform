import { beforeEach, expect, test } from "bun:test";
import { goalNumber, goalPeriod, goalDefinition, goalResult, normalizeGoal, readGoalMetric, goalRisks, goalContributors, conversionDraft, goalEvidence, evidenceHref } from "../src/goals.js";
import { completeValidation } from "./qa-helpers.js";
const storage = new Map(); Object.defineProperty(globalThis, "localStorage", { configurable: true, value: { getItem: key => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) } });
const api = await import("../src/store.js?okr-tests");
const { read, reset, update, saveStructuredGoal, recordGoalObservation, resourceCommand, goalPeriodCommand, calibrateGoalProgress, refreshGoalMetrics, checkGoalRisks } = api;
beforeEach(reset);
const get = id => read().resources.goals.find(goal => goal.id === id);
const year = new Date().getUTCFullYear(), start = `${year}-01-01`, end = `${year}-12-31`, observedAt = new Date(Date.now() - 1000).toISOString();
const input = patch => ({ name: "缩短交付周期", subjectKey: "tenant:studio", ownerId: "H-lin", ownerKind: "Human", owner: "林予", periodKind: "YEAR", periodStart: start, periodEnd: end, timezone: "UTC", alignments: [], riskPolicy: {},
  krs: [{ id: "KR-test-1", title: "平均周期", unit: "天", baseline: 10, target: 5, direction: "DECREASE", weight: 1, source: "MANUAL", metric: "", evidenceRequired: false }], ...patch });
const create = patch => saveStructuredGoal(input(patch));
const decide = (id, status) => resourceCommand("goals", id, status, "核对当前定义与范围", "", { revision: get(id).revision, status: get(id).status });
const publish = id => { decide(id, "待审核"); decide(id, "进行中"); };
const observe = (id, value, patch={}) => recordGoalObservation(id, get(id).krs[0].id, { revision: get(id).revision, value, observedAt, reason: "实测业务周期", evidence: [], ...patch });

test("typed numeric contracts reject every invalid class without partial state or fabricated zeros", () => {
  for (const invalid of ["", " ", null, undefined, NaN, Infinity, "nonsense", "12days", "0x10", true, [], {}]) expect(() => goalNumber(invalid)).toThrow();
  for (const field of ["baseline", "target", "weight"]) for (const value of ["nonsense", "", null, Infinity]) {
    const draft = input(); draft.krs[0][field] = value; const before = structuredClone(read());
    expect(() => saveStructuredGoal(draft)).toThrow(); expect(read()).toEqual(before);
  }
  for (const patch of [{ baseline: 5 }, { direction: "INCREASE" }, { weight: 0 }, { weight: 11 }]) {
    const draft = input(); Object.assign(draft.krs[0], patch); expect(() => saveStructuredGoal(draft)).toThrow();
  }
  expect(goalNumber("1.25")).toBe(1.25);
});
test("weighted KR progress uses independent formulas, never missing-as-zero or child totals", () => {
  const draft = input(); draft.krs.push({ ...draft.krs[0], id: "KR-test-2", title: "增长指标", baseline: 0, target: 100, direction: "INCREASE", weight: 3 });
  const id = saveStructuredGoal(draft); observe(id, 7.5);
  expect(goalResult(get(id)).progress).toBeNull(); expect(goalResult(get(id)).coverage).toBe(0.25);
  recordGoalObservation(id, "KR-test-2", { revision: get(id).revision, value: 100, observedAt, reason: "完整观察", evidence: [] });
  expect(goalResult(get(id)).progress).toBe(0.875);
  calibrateGoalProgress(id, { revision: get(id).revision, definitionRevision: get(id).definitionRevision, progress: 100, reason: "人工判断，仅作解释" });
  expect(get(id).manualAssessment.progress).toBe(100); expect(goalResult(get(id)).progress).toBe(0.875);
});
test("legacy conversion retains original strings and history without inventing observations or approval", () => {
  const legacy = structuredClone(get("G-01")), proposal = conversionDraft(read(), legacy, api.id);
  expect(proposal.krs[0].baseline).toBe("8 天"); expect(proposal.krs[0].unit).toBe("");
  expect(() => saveStructuredGoal(proposal)).toThrow(); expect(get("G-01")).toEqual(legacy);
  const converted = input({ id: legacy.id, revision: legacy.revision || 1, changeReason: "明确旧目标的数值口径并转换结构" }); saveStructuredGoal(converted);
  expect(get(legacy.id).legacy.scalar).toMatchObject({ name: legacy.name, baseline: "8 天", target: "5 天", actual: "6.2 天", progress: 60 });
  expect(get(legacy.id).krs[0].observations).toEqual([]); expect(get(legacy.id).approval).toBeNull(); expect(goalResult(get(legacy.id)).progress).toBeNull();
  const archived = structuredClone(get(legacy.id).legacy); saveStructuredGoal({ changeReason: "依据本期实际情况调整定义", ...get(legacy.id), name: "再次编辑" }); expect(get(legacy.id).legacy).toEqual(archived);
});
test("observations require real period, explanation and evidence and freeze referenced versions", () => {
  const draft = input(); draft.krs[0].evidenceRequired = true; const id = saveStructuredGoal(draft);
  for (const patch of [{}, { observedAt: "bad" }, { observedAt: `${year+1}-01-01T00:00:00Z` }, { reason: "" }, { evidence: [{kind:"work",id:"missing",version:1}] }]) {
    const before = structuredClone(read()); expect(() => observe(id, 5, patch)).toThrow(); expect(read()).toEqual(before);
  }
  const w = read().work.find(work => work.id === "W-104"); observe(id, 5, { evidence: [{ kind: "work", id: w.id, version: w.version }] });
  const observation = structuredClone(get(id).krs[0].observations[0]); update(s => s.work.find(work => work.id === w.id).notes = "后来修改");
  expect(get(id).krs[0].observations[0]).toEqual(observation); expect(goalResult(get(id)).progress).toBe(1);
});
test("definition edits retain old observations but invalidate only changed measurement contexts and approvals", () => {
  const id=create(); observe(id,5);publish(id);const old = structuredClone(get(id));
  saveStructuredGoal({ changeReason: "依据本期实际情况调整定义", ...get(id), krs: get(id).krs.map(kr => ({ ...kr, target: 4 })) });
  expect(get(id).status).toBe("草稿");expect(get(id).approval).toBeNull();expect(goalResult(get(id)).progress).toBeNull();
  expect(get(id).krs[0].observations).toEqual(old.krs[0].observations);expect(get(id).history.at(-1).approval).toEqual(old.approval);
  const before=structuredClone(read());expect(()=>saveStructuredGoal(old)).toThrow("目标已变更");expect(read()).toEqual(before);
});
test("many-to-many alignment is a cycle-free graph and parent calculation never sums child progress", () => {
  const parent=create();observe(parent,7.5);publish(parent);
  const second=create({name:"另一个上级目标"});publish(second);
  const child=create({name:"跨目标贡献",alignments:[{targetGoalId:parent,targetKrId:"KR-test-1",relation:"contribution",reason:"同一观察支撑"},{targetGoalId:second,targetKrId:"KR-test-1",relation:"contribution",reason:"另一指标关系"}]});observe(child,5);publish(child);
  expect(goalResult(get(parent)).progress).toBe(0.5);expect(goalResult(get(child)).progress).toBe(1);
  expect(goalContributors(read(),parent,"KR-test-1")).toEqual([{id:"H-lin",kind:"Human",sources:["OWNER","CHILD_GOAL"]}]);
  for(const patch of [
    {alignments:[{targetGoalId:child,targetKrId:"KR-test-1",relation:"contribution",reason:"形成循环"}]},
    {alignments:[{targetGoalId:"missing",targetKrId:"KR-test-1",relation:"contribution",reason:"外部目标"}]},
  ]) { const before=structuredClone(read());expect(()=>saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(parent),...patch})).toThrow();expect(read()).toEqual(before); }
  expect(()=>saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(child),alignments:[get(child).alignments[0],get(child).alignments[0]]})).toThrow("重复");
  expect(()=>saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(parent),krs:[{...get(parent).krs[0],id:"replacement"}]})).toThrow("引用被移除");
});
test("publication checks parent status, bounded periods, correct KR and member ownership", () => {
  const parent=create();const child=create({alignments:[{targetGoalId:parent,targetKrId:"KR-test-1",relation:"contribution",reason:"待发布父目标"}]});expect(()=>publish(child)).toThrow("先批准");publish(parent);publish(child);
  expect(()=>saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(child),periodStart:`${year-1}-01-01`,periodEnd:`${year-1}-12-31`})).toThrow("周期");
  expect(()=>saveStructuredGoal(input({subjectKey:"human:H-zhou"}))).toThrow();
});
test("stable permissions and CAS protect structured goals from takeover and forged draft evidence", () => {
  const id=create();update(s=>s.role="executor");const before=structuredClone(read());
  expect(()=>saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(id),ownerId:"H-zhou",owner:"周宁"})).toThrow();expect(read()).toEqual(before);
  update(s=>s.role="owner");observe(id,5);const stale=get(id).revision;observe(id,6);
  expect(()=>recordGoalObservation(id,"KR-test-1",{revision:stale,value:1,observedAt,reason:"stale"})).toThrow("已变更");
  saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(id),krs:get(id).krs.map(kr=>({...kr,observations:[{value:100}],currentObservationId:"fake"})),approval:{actor:"fake"}});
  expect(get(id).krs[0].observations).toHaveLength(2);expect(get(id).approval).toBeNull();
});
test("period close cannot substitute human assertion or delivered work for business observation", () => {
  const id=create({periodStart:`${year-1}-01-01`,periodEnd:`${year-1}-12-31`});publish(id);
  calibrateGoalProgress(id,{revision:get(id).revision,definitionRevision:get(id).definitionRevision,progress:100,reason:"主观判断"});
  goalPeriodCommand(id,"submit",{revision:get(id).revision,summary:"方案已交付但效果未知",reason:"没有观察数据",disposition:"结转"});
  const before=structuredClone(read());expect(()=>goalPeriodCommand(id,"confirm",{revision:get(id).revision,result:"已达成",reason:"交付就算达成"})).toThrow("缺少有效");expect(read()).toEqual(before);
  goalPeriodCommand(id,"confirm",{revision:get(id).revision,result:"未达成",reason:"保留未知数据结论"});expect(get(id).status).toBe("未达成");
});
test("period review freezes observations, supports return, and carryover is idempotent without inherited values", () => {
  const id=create();observe(id,5);publish(id);
  const submit=()=>goalPeriodCommand(id,"submit",{revision:get(id).revision,summary:"已达成目标",reason:"当前观察齐全",disposition:"结转"});submit();
  goalPeriodCommand(id,"return",{revision:get(id).revision,reason:"补充解释"});expect(get(id).review.status).toBe("已退回");submit();
  goalPeriodCommand(id,"confirm",{revision:get(id).revision,result:"已达成",reason:"固定观察核对通过"});const review=structuredClone(get(id).review);
  const next=goalPeriodCommand(id,"carry",{revision:get(id).revision,periodStart:`${year+1}-01-01`});
  expect(goalPeriodCommand(id,"carry",{revision:1,periodStart:`${year+1}-01-01`})).toBe(next);
  expect(get(next).status).toBe("草稿");expect(get(next).krs[0]).toMatchObject({baseline:null,target:null,observations:[],currentObservationId:null});expect(get(next).approval).toBeNull();expect(get(id).review).toEqual(review);
  expect(()=>publish(next)).toThrow();update(s=>s.role="executor");expect(()=>goalPeriodCommand(id,"carry",{revision:1})).toThrow("Human");
});
test("observations invalidate pending review snapshots and unknown or stale data never closes achieved", () => {
  const id=create();observe(id,5);publish(id);goalPeriodCommand(id,"submit",{revision:get(id).revision,summary:"自评",reason:"齐全",disposition:"停止"});const revision=get(id).revision;
  observe(id,6);expect(get(id).review).toBeNull();expect(()=>goalPeriodCommand(id,"confirm",{revision,result:"已达成",reason:"旧记录"})).toThrow("已变更");
  const stale = structuredClone(get(id));stale.riskPolicy={freshnessDays:0};expect(goalResult(stale).progress).toBeNull();
});
test("first accepted work metric deduplicates versions and preserves historical subject after reassignment", () => {
  update(s=>s.work=s.work.filter(work=>work.id==="W-104" || work.workKind === "BUG"));const id=create();
  saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(id),krs:get(id).krs.map(kr=>({...kr,title:"首次接受数",unit:"项",baseline:0,target:1,direction:"INCREASE",source:"AUTO",metric:"ACCEPTED_WORK_ITEMS"}))});
  expect(readGoalMetric(read(),get(id),get(id).krs[0]).value).toBe(0);
  completeValidation(api,"W-104");api.workCommand("W-104","contract","",3,{mode:"internal",reason:"内部使用"});api.workCommand("W-104","accept");
  const w=read().work.find(work=>work.id==="W-104");expect(w.acceptanceFacts).toHaveLength(1);expect(w.acceptanceFacts[0]).toMatchObject({workId:w.id,version:3,firstKnown:true});
  api.workCommand(w.id,"return","正式修订",3);api.workCommand(w.id,"submit");completeValidation(api,w.id);api.workCommand(w.id,"accept");expect(read().work.find(work=>work.id==="W-104").acceptanceFacts).toHaveLength(2);
  refreshGoalMetrics(id,{revision:get(id).revision});expect(goalResult(get(id)).results[0].value).toBe(1);
  update(s=>{const w=s.work.find(work=>work.id==="W-104");w.project="other";w.owner="其他人";});expect(readGoalMetric(read(),get(id),get(id).krs[0]).value).toBe(1);
});
test("legacy acceptance dates and unavailable AUTO inputs never fabricate zero", () => {
  const id=create();const kr={...get(id).krs[0],source:"AUTO",metric:"ACCEPTED_WORK_ITEMS"};
  expect(readGoalMetric(read(),get(id),kr).value).toBeNull();
  for(const metric of ["COST_PER_ACCEPTED","FIRST_PASS_RATE","ON_TIME_REPORT_RATE","CLIENT_FIRST_ACCEPTANCE","EVIDENCE_COVERAGE","REWORK_CYCLE_P50"]) expect(readGoalMetric(read(),get(id),{...kr,metric})).toMatchObject({value:null,availability:"unavailable"});
});
test("explicit risk settings govern staleness and local reminders dedupe and resolve", () => {
  const id=create({riskPolicy:{freshnessDays:0,nearDueDays:10,lagThreshold:0.15,workingWeekdays:[1,2,3,4,5]}});observe(id,9);
  const risks=goalRisks(read(),get(id));expect(risks.some(risk=>risk.type==="data")).toBe(true);
  checkGoalRisks(id,{revision:get(id).revision});const length=get(id).reminders.length;checkGoalRisks(id,{revision:get(id).revision});expect(get(id).reminders).toHaveLength(length);
  saveStructuredGoal({changeReason:"依据本期实际情况调整定义",...get(id),riskPolicy:{}});checkGoalRisks(id,{revision:get(id).revision});expect(get(id).reminders.filter(item=>item.status==="待处理").some(item=>item.type==="data")).toBe(false);
  expect(goalRisks(read(),get(id),new Date(`${year}-12-01T00:00:00Z`)).some(risk=>risk.type==="lag")).toBe(false);
});

test("evidence links preserve fixed work versions and ambiguous legacy owners remain unknown", () => {
  const state=structuredClone(read()), work=state.work.find(item=>item.id==="W-104");
  const ref=goalEvidence(state,{kind:"work",id:work.id,version:work.version});
  expect(evidenceHref(ref)).toBe(`/p/${work.project}/artifacts/${work.id}?version=${work.version}`);
  work.version++;expect(ref.snapshot.version).toBe(work.version-1);
  const legacy=state.resources.goals.find(item=>item.id==="G-01");delete legacy.ownerId;delete legacy.ownerKind;
  state.organization.members.push({...state.organization.members.find(item=>item.name===legacy.owner),id:"H-duplicate"});
  expect(conversionDraft(state,legacy,api.id).ownerId).toBe("");
  legacy.ownerId="H-missing";legacy.ownerKind="Human";expect(conversionDraft(state,legacy,api.id).ownerId).toBe("");
});


test("every goal subject supports week month quarter and year without subject-specific bans", () => {
  const subjects = [
    { subjectKey: "tenant:studio", ownerId: "H-lin", ownerKind: "Human" },
    { subjectKey: "team:ORG-engineering", ownerId: "H-lin", ownerKind: "Human" },
    { subjectKey: "project:launch", ownerId: "H-lin", ownerKind: "Human" },
    { subjectKey: "human:H-lin", ownerId: "H-lin", ownerKind: "Human" },
    { subjectKey: "agent:atlas", ownerId: "atlas", ownerKind: "Agent" },
  ];
  const boundaries = {
    WEEK: { periodStart: "2026-12-28", periodEnd: "2027-01-03" },
    MONTH: { periodStart: "2027-01-01", periodEnd: "2027-01-31" },
    QUARTER: { periodStart: "2027-01-01", periodEnd: "2027-03-31" },
    YEAR: { periodStart: "2027-01-01", periodEnd: "2027-12-31" },
  };
  for (const subject of subjects) for (const [periodKind, bounds] of Object.entries(boundaries)) {
    expect(goalPeriod(periodKind, "2027-01-01", "Asia/Singapore")).toEqual(bounds);
    const id = create({ ...subject, periodKind, ...bounds, timezone: "Asia/Singapore" });
    expect(get(id)).toMatchObject({ ...subject, periodKind, ...bounds, status: "草稿" });
    expect(get(id).history).toEqual([]);
  }
  expect(goalPeriod("WEEK", "2024-02-29", "UTC")).toEqual({ periodStart: "2024-02-26", periodEnd: "2024-03-03" });
});
test("existing definition changes and legacy conversion require fresh textual reasons atomically", () => {
  const id = create(); observe(id, 7.5); publish(id);
  const proposal = input({ id: "G-01", revision: get("G-01").revision || 1 });
  for (const base of [{ ...get(id), name: "请假后调整目标" }, proposal]) {
    for (const changeReason of [undefined, null, "", " \n\t", 1, true, {}, []]) {
      const before = structuredClone(read()), persisted = [...storage.entries()];
      expect(() => saveStructuredGoal({ ...base, changeReason })).toThrow("本次调整理由");
      expect(read()).toEqual(before); expect([...storage.entries()]).toEqual(persisted);
    }
  }
  saveStructuredGoal({ ...get(id), name: "请假后调整目标", changeReason: "  因请假减少本周期可用时间  " });
  expect(get(id)).not.toHaveProperty("changeReason");
  const before = structuredClone(read()), persisted = [...storage.entries()];
  expect(() => saveStructuredGoal({ ...get(id), name: "临时任务再次调整" })).toThrow("本次调整理由");
  expect(read()).toEqual(before); expect([...storage.entries()]).toEqual(persisted);
});
test("fixed before and after histories retain removed KRs observations and old approvals", () => {
  const id = create(); observe(id, 7.5); publish(id);
  const before = structuredClone(get(id));
  const reason = "临时支持任务占用两天，调整周期目标与验收口径";
  saveStructuredGoal({ ...get(id), name: "调整后的业务结果", krs: [{ ...get(id).krs[0], id: "KR-replacement", target: 4 }], changeReason: reason });
  const current = structuredClone(get(id)), entry = current.history.at(-1);
  expect(entry.reason).toBe(reason);
  expect(entry.actor).toEqual({ id: "H-lin", kind: "Human" });
  expect(Number.isFinite(Date.parse(entry.time))).toBe(true);
  expect(entry.before).toEqual({ ...before, history: undefined });
  expect(entry.after).toEqual({ ...current, history: undefined });
  expect(entry.before.approval).toEqual(before.approval);
  expect(entry.before.krs[0].observations).toEqual(before.krs[0].observations);
  expect(entry.after.krs[0].id).toBe("KR-replacement");
  expect(entry.after.approval).toBeNull(); expect(entry.after.submission).toBeNull();
  expect(entry.after.krs[0].currentObservationId).toBeNull();
  expect(entry.after.krs[0].observations).toEqual([]);
  saveStructuredGoal({ ...get(id), name: "进一步澄清结果", changeReason: "接收方要求明确下一步使用场景" });
  expect(get(id).history[current.history.length - 1]).toEqual(entry);
});
test("definition reason covers every O and KR edit while creation makes no adjustment history", () => {
  const id = create({ changeReason: "创建说明不能制造一次调整", history: [{ reason: "伪造历史" }] });
  expect(get(id).history).toEqual([]); expect(get(id)).not.toHaveProperty("changeReason");
  const base = structuredClone(get(id));
  const patches = [
    { name: "另一结果" }, { ownerId: "H-zhou", ownerKind: "Human" },
    { subjectKey: "team:ORG-engineering" },
    { periodKind: "MONTH", periodStart: `${year}-01-01`, periodEnd: `${year}-01-31` },
    { timezone: "Asia/Singapore" }, { riskPolicy: { nearDueDays: 3 } },
    ...["title", "unit", "direction", "baseline", "target", "weight", "source", "metric", "evidenceRequired"].map(field => ({ krs: [{ ...base.krs[0], [field]: "changed" }] })),
    { krs: [...base.krs, { ...base.krs[0], id: "KR-added" }] }, { krs: [] },
    { alignments: [{ targetGoalId: "missing", targetKrId: "missing", relation: "contribution", reason: "新对齐" }] },
  ];
  for (const patch of patches) {
    const before = structuredClone(read()), persisted = [...storage.entries()];
    expect(() => saveStructuredGoal({ ...base, ...patch })).toThrow("本次调整理由");
    expect(read()).toEqual(before); expect([...storage.entries()]).toEqual(persisted);
  }
  const legacy = structuredClone(get("G-01"));
  saveStructuredGoal(input({ id: legacy.id, revision: legacy.revision || 1, changeReason: "将旧标量指标转成明确的O与KR" }));
  expect(get(legacy.id).history.at(-1).before).toEqual({ ...legacy, history: undefined });
  expect(goalDefinition(get(legacy.id).history.at(-1).after)).toEqual(goalDefinition(get(legacy.id)));
});
