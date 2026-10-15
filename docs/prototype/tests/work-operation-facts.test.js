import { beforeEach, expect, test } from "bun:test";
import { completeValidation } from "./qa-helpers.js";
if (!globalThis.localStorage) { const entries=new Map();globalThis.localStorage={getItem:key=>entries.get(key)||null,setItem:(key,value)=>entries.set(key,value),removeItem:key=>entries.delete(key)}; }
const api=await import("../src/store.js?work-operation-facts");
const work=()=>api.read().work.find(w=>w.id==="W-103");
beforeEach(()=>api.reset());
function accepted(){completeValidation(api,"W-103");api.workCommand("W-103","accept","本地模拟：Human 核对并接受固定成果",work().version);}
test("new acceptance records the actual decision actor and original version, distinct from executors",()=>{
 accepted();const fact=work().decisionFacts[0];
 expect(fact).toMatchObject({kind:"DECISION",tenantId:"studio",workId:"W-103",version:2,action:"APPROVE",originalCommand:"accept",actor:{kind:"Human",id:"H-lin"},fromState:"待验收",toState:"待交接",source:"LOCAL_SIMULATION"});
 expect(fact.workSnapshot.title).toBe(work().title);expect(fact.workSnapshot.version).toBe(2);expect(fact.project).toBe(work().project);expect(fact.subjectKeys).toContain("human:H-lin");expect(fact.subjectKeys).toContain("tenant:studio");expect(Number.isFinite(Date.parse(fact.time))).toBe(true);expect(fact.reason).toContain("Human");expect(work().owner).not.toBe("林予");
});
test("deliver retains an explicit local SENT receipt with sender and immutable package identity",()=>{
 accepted();api.workCommand("W-103","deliver","本地模拟：固定包发送",2);const delivery=api.read().resources.deliveries.find(d=>d.id===work().currentDeliveryId),fact=delivery.sendFact;
 expect(fact).toMatchObject({kind:"DELIVERY",tenantId:"studio",workId:"W-103",version:2,status:"SENT",actor:{kind:"Human",id:"H-lin"},source:"LOCAL_SIMULATION",deliveryId:delivery.id});
 expect(fact.deliverySnapshot).toMatchObject({id:delivery.id,recipient:delivery.recipient,version:2,workId:"W-103",project:work().project});expect(fact.subjectKeys).toContain("human:H-lin");expect(Number.isFinite(Date.parse(fact.time))).toBe(true);expect(work().decisionFacts.map(f=>f.action)).toEqual(["APPROVE","DELIVER"]);expect(work().decisionFacts[1].id).not.toBe(work().decisionFacts[0].id);
});
test("failed or duplicate accept/deliver commands create no decision or send facts",()=>{
 const before=structuredClone(api.read());expect(()=>api.workCommand("W-103","deliver","未接受不得发送",2)).toThrow();expect(api.read()).toEqual(before);
 accepted();const after=structuredClone(api.read());expect(()=>api.workCommand("W-103","accept","重复接受",2)).toThrow();expect(api.read()).toEqual(after);
 api.workCommand("W-103","deliver","明确本地模拟发送",2);const sent=structuredClone(api.read());expect(()=>api.workCommand("W-103","deliver","重复发送",2)).toThrow();expect(api.read()).toEqual(sent);
});
test("new actual request-changes records feedback, duplicate short circuit keeps one event",()=>{
 const feedback={requirement:work().requirements[0],actual:"说明缺少固定依据",expected:"补齐可核对的出处",position:"交付材料第一段"};
 api.workCommand("W-103","return","请补充依据",2,feedback);const first=structuredClone(work().decisionFacts);expect(first).toHaveLength(1);expect(first[0].action).toBe("REQUEST_CHANGES");expect(first[0].actor.id).toBe("H-lin");expect(first[0].feedback).toEqual(feedback);
 api.workCommand("W-103","return","请补充依据",2,feedback);expect(work().decisionFacts).toEqual(first);
});
test("cancel, Human takeover and resume record their actual local semantics",()=>{
 api.workCommand("W-103","takeover","Human 接管后暂停 Agent",2);api.workCommand("W-103","resume","恢复原执行",2);api.workCommand("W-103","cancel","明确取消",2);
 expect(work().decisionFacts.map(f=>[f.action,f.originalCommand])).toEqual([["PAUSE","takeover"],["RESUME","resume"],["CANCEL","cancel"]]);expect(work().decisionFacts.map(f=>f.fromState)).toEqual(["待验收","等待","进行中"]);
});
test("corrupt decision history refuses new operations atomically without overwriting old evidence",()=>{
 completeValidation(api,"W-103");api.update(s=>s.work.find(w=>w.id==="W-103").decisionFacts=[null]);const before=structuredClone(api.read());expect(()=>api.workCommand("W-103","accept","核对接受",2)).toThrow("裁决历史损坏");expect(api.read()).toEqual(before);
});
test("legacy history is not fabricated and current owner/title edits cannot rewrite recorded decisions",()=>{
 expect(work().decisionFacts).toBeUndefined();expect(api.read().resources.deliveries.every(d=>d.sendFact===undefined)).toBe(true);accepted();const fixed=structuredClone(work().decisionFacts);
 api.update(s=>{const w=s.work.find(w=>w.id==="W-103");w.owner="其他负责人";w.title="后续修改的标题";w.version=3;});expect(work().decisionFacts).toEqual(fixed);expect(fixed[0].workSnapshot.title).not.toBe(work().title);
});

test("inherited command names are rejected before any store mutation or uncloneable fact",()=>{
 for(const command of ["toString","valueOf","constructor","__proto__","hasOwnProperty"]){const before=structuredClone(api.read());expect(()=>api.workCommand("W-103",command,"非法枚举",2)).toThrow("未知工作动作");expect(api.read()).toEqual(before);}
 api.workCommand("W-103","takeover","合法接管仍可完成",2);expect(work().decisionFacts[0].action).toBe("PAUSE");
});
