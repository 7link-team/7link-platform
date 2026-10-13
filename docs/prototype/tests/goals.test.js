import { beforeEach, expect, test } from "bun:test";
const entries = new Map();
Object.defineProperty(globalThis, "localStorage", { configurable:true, value:{getItem:key=>entries.get(key)||null,setItem:(key,value)=>entries.set(key,value),removeItem:key=>entries.delete(key)} });
const {read,reset,update,saveResource,goalProgress,calibrateGoalProgress}=await import('../src/store.js');
beforeEach(()=>reset());
const goal=()=>read().resources.goals.find(item=>item.id==='G-01');
const calibration=(progress=75)=>({progress,reason:'逐项核对当前指标证据后的人工判断',metricRevision:goal().metricRevision||1,revision:goal().revision||1});
test('each metric change invalidates old progress; nonmetric edits preserve calibrated progress',()=>{
 for(const field of ['metric','baseline','actual','target']){
  reset();calibrateGoalProgress('G-01',calibration(80));const prior=structuredClone(goal());
  saveResource('goals',{...goal(),[field]:`${goal()[field]} 修订`});
  expect(goal().metricRevision).toBe(2);expect(goalProgress(goal())).toBeNull();expect(goal().progressCalibration).toBeNull();
  expect(goal().history.at(-1).progressCalibration).toEqual(prior.progressCalibration);
 }
 calibrateGoalProgress('G-01',calibration(30));const metricRevision=goal().metricRevision,snapshot=structuredClone(goal().progressCalibration);
 saveResource('goals',{...goal(),name:'改标题而非指标',progress:99,progressCalibration:{progress:99}});
 expect(goal().metricRevision).toBe(metricRevision);expect(goalProgress(goal())).toBe(30);expect(goal().progressCalibration).toEqual(snapshot);
});
test('calibration binds snapshot and evidence without changing goal/work lifecycle',()=>{
 const works=structuredClone(read().work),status=goal().status;
 expect(goalProgress(goal())).toBe(60);
 calibrateGoalProgress('G-01',calibration(100));
 expect(goalProgress(goal())).toBe(100);expect(goal().status).toBe(status);expect(read().work).toEqual(works);
 expect(goal().progressCalibration).toMatchObject({progress:100,metricRevision:1,metric:{metric:goal().metric,baseline:goal().baseline,actual:goal().actual,target:goal().target},reason:calibration().reason,actor:'林予'});
 expect(goal().history[0].progress).toBe(60);
 const corrupted=structuredClone(goal());corrupted.actual='未经校准的新实际';expect(goalProgress(corrupted)).toBeNull();
});
test('stale metric or concurrent calibration rejects atomically and prevents stale editor overwrite',()=>{
 const stale=calibration(),oldEditor=structuredClone(goal());
 saveResource('goals',{...goal(),actual:'5 天'});const changed=structuredClone(read());
 expect(()=>calibrateGoalProgress('G-01',stale)).toThrow('指标已变更');expect(read()).toEqual(changed);
 const second=calibration();calibrateGoalProgress('G-01',second);const latest=structuredClone(read());
 expect(()=>calibrateGoalProgress('G-01',second)).toThrow('记录已变更');expect(read()).toEqual(latest);
 expect(()=>saveResource('goals',oldEditor)).toThrow('目标已变更');expect(read()).toEqual(latest);
});
test('calibration validates range, required evidence, offline and external access',()=>{
 const before=structuredClone(read());
 for(const progress of ['',null,' ',NaN,Infinity,-1,101]){expect(()=>calibrateGoalProgress('G-01',calibration(progress))).toThrow();expect(read()).toEqual(before);}
 expect(()=>calibrateGoalProgress('G-01',{...calibration(),reason:' '})).toThrow('依据');expect(read()).toEqual(before);
 for(const field of ['offline','role']){reset();update(s=>s[field]=field==='offline'?true:'external');const denied=structuredClone(read());expect(()=>calibrateGoalProgress('G-01',calibration())).toThrow();expect(read()).toEqual(denied);}
});
test('new goals cannot inherit or fabricate calibrated progress through generic save',()=>{
 saveResource('goals',{name:'新目标',owner:'林予',metric:'完成率',actual:'0',target:'1',baseline:'0',progress:80,progressCalibration:{progress:80,metricRevision:1}});
 const created=read().resources.goals[0];expect(goalProgress(created)).toBeNull();expect(created.metricRevision).toBe(1);expect(created.progressCalibration).toBeNull();
});

const { resourceCommand, goalTransitionProblem } = await import('../src/store.js');
const receipt = () => ({ revision:goal().revision || 1, status:goal().status });
const draft = () => saveResource('goals', {...goal(),name:'本期目标草稿'});
const transition = status => resourceCommand('goals','G-01',status,'逐项核对本修订目标、指标和责任范围','',receipt());
test('goal lifecycle pins submitted revision; illegal transitions and stale decisions are atomic',()=>{
 draft();const before=structuredClone(read());
 for(const status of ['进行中','草稿','已接受','已完成']){expect(()=>transition(status)).toThrow();expect(read()).toEqual(before);}
 transition('待审核');const submitted=structuredClone(goal().submission),oldReceipt=receipt();
 expect(submitted.revision).toBe(goal().revision);expect(submitted.snapshot.metric).toBe(goal().metric);
 transition('进行中');expect(goal().approval.revision).toBe(submitted.revision);expect(goal().approval.snapshot).toEqual(submitted.snapshot);
 expect(goal().approval.actorKind).toBe('Human');expect(goal().approval.actorId).toBe('H-lin');
 const approved=structuredClone(read());expect(()=>resourceCommand('goals','G-01','进行中','重复确认','',oldReceipt)).toThrow('修订或状态');expect(read()).toEqual(approved);
 saveResource('goals',{...goal(),target:'4 天'});expect(goal().status).toBe('草稿');expect(goal().approval).toBeNull();expect(goal().submission).toBeNull();
 expect(goal().history.some(item=>item.approval?.revision===submitted.revision)).toBe(true);
 expect(()=>resourceCommand('goals','G-01','进行中','批准旧内容','',oldReceipt)).toThrow('修订或状态');
});
test('return and resubmit preserve fixed review history; concurrent progress invalidates pending submission',()=>{
 draft();transition('待审核');const submitted=structuredClone(goal().submission);transition('草稿');
 expect(goal().submission).toBeNull();expect(goal().history.at(-1).snapshot.submission).toEqual(submitted);
 transition('待审核');const decision=receipt();calibrateGoalProgress('G-01',calibration(40));
 expect(goal().status).toBe('草稿');expect(goal().submission).toBeNull();
 expect(()=>resourceCommand('goals','G-01','进行中','旧审批','',decision)).toThrow('修订或状态');
 transition('待审核');transition('进行中');expect(goal().approval.snapshot.progress).toBe(40);
});
test('goal decisions require known authorized Human and exact receipt; legacy pending is not fabricated evidence',()=>{
 const legacy=read().resources.goals.find(item=>item.id==='G-02');expect(goalTransitionProblem(read(),legacy,'进行中')).toContain('固定提交');
 draft();transition('待审核');const pending=structuredClone(read());
 expect(()=>resourceCommand('goals','G-01','进行中','没有固定修订')).toThrow('修订或状态');expect(read()).toEqual(pending);
 for(const role of ['executor','external','agent']){reset();draft();transition('待审核');update(s=>s.role=role);const denied=structuredClone(read());expect(()=>transition('进行中')).toThrow();expect(read()).toEqual(denied);}
 reset();draft();transition('待审核');update(s=>s.organization.members=s.organization.members.filter(person=>person.id!=='H-lin'));
 const denied=structuredClone(read());expect(()=>transition('进行中')).toThrow('Human');expect(read()).toEqual(denied);
});
test('creation cannot inject approved/submitted state or forged review and incomplete drafts cannot submit',()=>{
 saveResource('goals',{name:'未完成草稿',owner:'林予',status:'进行中',revision:999,submission:{revision:999},approval:{actor:'fake'},history:[{approval:'fake'}]});
 const created=read().resources.goals[0];expect(created.status).toBe('草稿');expect(created.revision).toBe(1);expect(created.approval).toBeNull();expect(created.submission).toBeNull();expect(created.history).toEqual([]);
 const before=structuredClone(read());expect(()=>resourceCommand('goals',created.id,'待审核','提交','',{revision:1,status:'草稿'})).toThrow('补齐');expect(read()).toEqual(before);
});

test('executor cannot edit, claim, or calibrate another owner goal, including forged owner metadata',()=>{
 update(s=>s.role='executor');const other=()=>read().resources.goals.find(item=>item.id==='G-02'),before=structuredClone(read());
 expect(()=>saveResource('goals',{...other(),owner:'林予',ownerId:'H-lin',ownerKind:'Human'})).toThrow('本人');expect(read()).toEqual(before);
 expect(()=>calibrateGoalProgress('G-02',{revision:1,metricRevision:1,progress:100,reason:'越权校准'})).toThrow('本人');expect(read()).toEqual(before);
 expect(()=>saveResource('goals',{name:'代别人创建',owner:'周宁'})).toThrow('本人');expect(read()).toEqual(before);
});
test('goal ownership uses stable identities; member writes only own drafts and progress',()=>{
 draft();update(s=>s.role='executor');saveResource('goals',{...goal(),name:'本人草稿'});
 expect(goal().ownerId).toBe('H-lin');expect(goal().ownerKind).toBe('Human');
 expect(()=>saveResource('goals',{...goal(),owner:'周宁'})).toThrow('转移');
 update(s=>s.organization.members.find(person=>person.id==='H-lin').name='林予改名');
 saveResource('goals',{...goal(),name:'更名后仍是本人草稿'});expect(goal().owner).toBe('林予改名');
 transition('待审核');expect(()=>saveResource('goals',{...goal(),name:'修改送审内容'})).toThrow('草稿');
 calibrateGoalProgress('G-01',calibration(70));expect(goal().status).toBe('草稿');expect(goalProgress(goal())).toBe(70);
});
