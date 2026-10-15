import {test,expect} from 'bun:test';
import {initial} from '../src/data';
import {saveBudgetPolicyOn,reserveUsageOn,settleUsageOn,reconcileUsageOn,budgetBalance,routeBudgetModel,recordBudgetAdjustmentOn,evaluateBudgetAdjustmentOn,budgetRows} from '../src/budget';
const state=()=>({...structuredClone(initial),role:'owner',activeTenant:'studio',project:'launch'});
const policy=(s,subject='tenant',extra={})=>saveBudgetPolicyOn(s,{subject,window:'day',limit:5,soft:0.8,concurrency:2,reason:'按当前计划设置',...extra});
const reserve=(s,k='one',extra={})=>reserveUsageOn(s,{requestKey:k,workId:'W-104',agentId:'atlas',modelId:'MD-01',purpose:'候选生成',reserved:2,...extra});
test('all applicable scopes reserve atomically; idempotency never permits reusing a key for different inputs',()=>{
 const s=state(),p=policy(s);policy(s,'project:launch',{limit:3});reserve(s);
 expect(()=>reserve(s,'two')).toThrow('预算');expect(s.accounting.calls).toHaveLength(1);
 expect(reserve(s).id).toBe(s.accounting.calls[0].id);
 expect(()=>reserve(s,'one',{reserved:1})).toThrow('另一组');
 expect(budgetBalance(s,p).used).toBe(2);
});
test('concurrency is held until closure; unknown charge remains reserved and late usage appends differences',()=>{
 const s=state(),p=policy(s,'tenant',{concurrency:1}),c=reserve(s);
 expect(()=>reserve(s,'two')).toThrow('并发');settleUsageOn(s,{id:c.id,outcome:'FAILED',actual:''});
 expect(c.actual).toBeNull();expect(c.state).toBe('PENDING');expect(budgetBalance(s,p).active).toBe(0);expect(budgetBalance(s,p).used).toBe(2);
 reserve(s,'two');reconcileUsageOn(s,{id:c.id,actual:4,reason:'晚到供应商账单'});
 expect(c.actual).toBeNull();expect(c.corrections[0]).toMatchObject({before:null,actual:4,estimate:2,delta:2});
 expect(()=>reserve(s,'three')).toThrow('预算');
 reconcileUsageOn(s,{id:c.id,actual:1,reason:'供应商退款核对'});expect(c.corrections).toHaveLength(2);expect(c.corrections[1].before).toBe(4);
});
test('budget history preserves fixed revisions and rejects stale, invalid and unauthorized writes',()=>{
 const s=state(),p=policy(s);saveBudgetPolicyOn(s,{...p,limit:8,reason:'确认追加预算'});
 expect(p.history).toHaveLength(2);expect(p.history[1].before.limit).toBe(5);
 expect(()=>saveBudgetPolicyOn(s,{...p,revision:1,reason:'旧页'})).toThrow('已变化');
 for(const bad of [NaN,Infinity,-1,0])expect(()=>reserve(s,'bad',{reserved:bad})).toThrow();
 s.role='external';expect(()=>reserve(s)).toThrow('管理员');
});
test('model routing fails closed on missing declarations, boundary, quality, capability, risk and cost',()=>{
 const s=state(),input={allowedModelIds:['MD-01'],capability:'writing',minimumQuality:8,risk:2,dataBoundary:'tenant-vault',maximumCost:1,declarations:{'MD-01':{capabilities:['writing'],quality:9,maxRisk:2,dataBoundary:'tenant-vault',estimatedCost:0.5}}};
 expect(routeBudgetModel(s,input).state).toBe('READY');
 for(const change of [{declarations:{}},{minimumQuality:10},{risk:3},{dataBoundary:'public'},{maximumCost:0.1},{capability:'image'},{allowedModelIds:[]}])expect(routeBudgetModel(s,{...input,...change}).state).toBe('PAUSED');
});
test('adjustments keep before/after, hard requirements and append quality outcomes; unsafe switches are rejected',()=>{
 const s=state(),base={modelId:'MD-01',contextTokens:4000,candidates:3,concurrency:2,outputTokens:2000,dataBoundary:'same'},input={workId:'W-104',agentId:'atlas',reason:'接近软阈值',impact:'降低并发，延后完成',hardRequirements:'全部必需检查不变',before:base,after:{...base,candidates:1},estimatedSaving:1};
 const r=recordBudgetAdjustmentOn(s,input);input.before.candidates=99;expect(r.before.candidates).toBe(3);
 evaluateBudgetAdjustmentOn(s,{id:r.id,actualSaving:0.4,qualityResult:'全部必需项通过',evidence:'固定测试报告'});expect(r.evaluations).toHaveLength(1);
 s.resources.models.find(m=>m.id==='MD-02').status='生效';
 s.executions=[{id:'safe',tenantId:'studio',workId:'W-104',state:'PAUSED',capabilities:{modelSwitchAtBoundary:true},operations:[]}];
 const change={...input,executionId:'safe',before:base,after:{...base,modelId:'MD-02'},allowedModelIds:['MD-02'],safeBoundary:true,switchMode:'ADAPTER_BOUNDARY'};
 expect(()=>recordBudgetAdjustmentOn(s,{...change,unfinishedTool:true})).toThrow('安全边界');
 expect(()=>recordBudgetAdjustmentOn(s,{...change,unknownSideEffects:true})).toThrow('安全边界');
 expect(()=>recordBudgetAdjustmentOn(s,{...change,switchMode:'LINKED_EXECUTION'})).toThrow('关联执行');
 s.executions[0].operations=[{started:true,sideEffects:'UNKNOWN'}];
 expect(()=>recordBudgetAdjustmentOn(s,change)).toThrow('安全边界');
});
test('visible accounting rows stay within selected tenant/project',()=>{
 const s=state();reserve(s);s.accounting.calls.push({...s.accounting.calls[0],id:'other-tenant',tenantId:'other'},{...s.accounting.calls[0],id:'other-project',projectId:'other'});
 expect(budgetRows(s)).toHaveLength(1);s.project='all';expect(budgetRows(s)).toHaveLength(2);
});
