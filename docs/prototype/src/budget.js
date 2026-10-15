import { executionSafetyBoundaryProblem } from "./executions";
// Tenant-local accounting scenarios. No supplier call or real charge is created here.
const clone = value => structuredClone(value);
const stamp = () => new Date().toISOString();
const key = prefix => `${prefix}-${crypto.randomUUID()}`;
function writable(s) {
  if (s.role !== 'owner' || s.offline) throw Error('仅在线租户管理员可维护预算与本地计量场景。');
  s.accounting ??= {policies:[], calls:[], adjustments:[]};
  return s.accounting;
}
function amount(value, positive=false) {
  const n=Number(value);
  if(!Number.isFinite(n)||n<0||(positive&&n===0)||n>1e9) throw Error('费用必须是有效非负数，预算与预留必须大于零。');
  const rounded=Math.round(n*1e6)/1e6;if(positive&&rounded===0)throw Error('最小计量单位为0.000001 USD，请增大预算或预留。');return rounded;
}
function context(s, input) {
  const w=s.work.find(w=>w.id===input.workId);
  const a=s.agents.find(a=>a.id===input.agentId);
  if(!w||!a||s.projects.find(p=>p.id===w.project)?.archivedAt||['退役','已退役'].includes(a.status)) throw Error('请选择未归档项目的任务与可用Agent。');
  return {workId:w.id,projectId:w.project,agentId:a.id};
}
export function budgetRows(s) {
  return (s.accounting?.calls||[]).filter(c=>c.tenantId===s.activeTenant&&(s.project==='all'||c.projectId===s.project));
}
export function policyMatches(p,c) {return p.subject==='tenant'||p.subject===`project:${c.projectId}`||p.subject===`agent:${c.agentId}`||p.subject===`work:${c.workId}`;}
function applicable(p,c) {
  return policyMatches(p,c)&&(!p.projectId||p.projectId===c.projectId);
}
export function budgetBalance(s,p,at=stamp()) {
  const calls=(s.accounting?.calls||[]).filter(c=>c.tenantId===s.activeTenant&&applicable(p,c));
  const current=calls.filter(c=>p.window==='day'?c.createdAt.slice(0,10)===at.slice(0,10):c.createdAt>=p.startsAt&&c.createdAt<p.endsAt);
  const used=current.reduce((n,c)=>n+(c.state==='RESERVED'?c.reserved:(c.corrections.at(-1)?.actual??c.actual??c.reserved)),0);
  return {used:amount(used),remaining:amount(Math.max(0,p.limit-used)),active:calls.filter(c=>c.state==='RESERVED').length,ratio:used/p.limit};
}
export function saveBudgetPolicyOn(s,input) {
  const db=writable(s), old=input.id&&db.policies.find(p=>p.id===input.id);
  if(input.id&&!old)throw Error('预算规则不存在。');
  if(old&&old.revision!==input.revision)throw Error('预算规则已变化，请重新加载。');
  const subjects=['tenant',...s.projects.filter(p=>!p.archivedAt).map(p=>`project:${p.id}`),...s.agents.map(a=>`agent:${a.id}`),...s.work.map(w=>`work:${w.id}`)];
  if(!subjects.includes(input.subject)||!['day','period'].includes(input.window))throw Error('预算范围或周期无效。');
  const concurrency=Number(input.concurrency),soft=Number(input.soft);
  if(!Number.isSafeInteger(concurrency)||concurrency<1||concurrency>1000||!Number.isFinite(soft)||soft<=0||soft>1)throw Error('并发上限须为1–1000，提醒阈值须为0–100%之间。');
  let startsAt=input.startsAt,endsAt=input.endsAt;
  if(input.window==='period'&&(!Number.isFinite(Date.parse(startsAt))||!Number.isFinite(Date.parse(endsAt))||Date.parse(endsAt)<=Date.parse(startsAt)))throw Error('请填写有效的周期开始、结束时间。');
  if(input.window==='period'){startsAt=new Date(startsAt).toISOString();endsAt=new Date(endsAt).toISOString();}
  if(!input.reason?.trim())throw Error('请说明创建或调整预算的理由。');
  const next={id:old?.id||key('BP'),revision:(old?.revision||0)+1,subject:input.subject,window:input.window,startsAt,endsAt,limit:amount(input.limit,true),soft,concurrency,enabled:input.enabled!==false&&input.enabled!=='false',tenantId:s.activeTenant,history:old?.history||[]};
  next.history=[...next.history,{at:stamp(),reason:input.reason.trim(),before:old?clone({...old,history:undefined}):null,after:clone({...next,history:undefined})}];
  if(old)Object.assign(old,next);else db.policies.push(next);
  return next;
}
export function reserveUsageOn(s,input) {
  const db=writable(s), ctx=context(s,input), reserved=amount(input.reserved,true),model=s.resources.models.find(m=>m.id===input.modelId&&m.status==='生效');
  if(!model)throw Error('请选择当前生效的授权模型。');
  if(!input.requestKey?.trim()||!input.purpose?.trim())throw Error('请填写调用编号与用途。');
  const duplicate=db.calls.find(c=>c.requestKey===input.requestKey&&c.tenantId===s.activeTenant);
  if(duplicate) {
    if(duplicate.workId!==ctx.workId||duplicate.agentId!==ctx.agentId||duplicate.modelId!==model.id||duplicate.reserved!==reserved||duplicate.purpose!==input.purpose.trim())throw Error('调用编号已用于另一组输入。');
    return duplicate;
  }
  const at=stamp(), policies=db.policies.filter(p=>p.enabled&&p.tenantId===s.activeTenant&&applicable(p,ctx)&&(p.window==='day'||at>=p.startsAt&&at<p.endsAt));
  for(const p of policies){const b=budgetBalance(s,p,at);if(b.used+reserved>p.limit||b.active>=p.concurrency)throw Error(`预算或并发限制阻止新调用：${p.subject}。`);}
  const call={id:key('UC'),requestKey:input.requestKey,tenantId:s.activeTenant,...ctx,modelId:model.id,reserved,actual:null,corrections:[],state:'RESERVED',purpose:input.purpose.trim(),createdAt:at,source:'LOCAL_SIMULATION',notices:policies.filter(p=>(budgetBalance(s,p,at).used+reserved)/p.limit>=p.soft).map(p=>`${p.subject}已达到提醒阈值；未撤销已发生费用。`)};
  db.calls.push(call);return call;
}
export function settleUsageOn(s,input) {
  const db=writable(s),c=db.calls.find(c=>c.id===input.id&&c.tenantId===s.activeTenant);
  if(!c||c.state!=='RESERVED')throw Error('仅待结算调用可以结束。');
  if(!['SUCCEEDED','FAILED','CANCELLED'].includes(input.outcome))throw Error('请选择调用结果。');
  const actual=input.actual===''||input.actual==null?null:amount(input.actual);
  c.state=actual==null?'PENDING':'SETTLED';c.actual=actual;c.outcome=input.outcome;c.closedAt=stamp();
  c.disposition=actual==null?'供应商用量未到，继续按预留费用占额；并发已释放。':actual>c.reserved?'实际费用超过预留：保留尾差，停止超额范围的新调用。':'实际费用已结算，释放预留尾差。';
  return c;
}
export function reconcileUsageOn(s,input) {
  const db=writable(s),c=db.calls.find(c=>c.id===input.id&&c.tenantId===s.activeTenant);
  if(!c||c.state==='RESERVED'||!input.reason?.trim())throw Error('请先结束调用，并填写对账依据。');
  const actual=amount(input.actual),before=c.corrections.at(-1)?.actual??c.actual;
  c.corrections.push({id:key('UR'),at:stamp(),before,actual,estimate:c.reserved,delta:actual-(before??c.reserved),reason:input.reason.trim(),source:'LOCAL_SIMULATION'});c.state='SETTLED';return c;
}
export function routeBudgetModel(s,input) {
  if(!Number.isFinite(input.minimumQuality)||!Number.isFinite(input.risk)||!Number.isFinite(input.maximumCost)||input.maximumCost<0||!input.capability||!input.dataBoundary)return {state:'PAUSED',reason:'路由要求未完整声明。'};
  const models=s.resources.models.filter(m=>m.status==='生效'&&(input.allowedModelIds||[]).includes(m.id));
  // Capability, risk, quality, boundary and price are explicit supplied declarations,
  // never guessed from a model name or protocol. Missing declarations fail closed.
  const eligible=models.map(m=>({model:m,declaration:input.declarations?.[m.id]})).filter(({declaration:d})=>d&&(d.capabilities||[]).includes(input.capability)&&d.quality>=input.minimumQuality&&d.maxRisk>=input.risk&&d.dataBoundary===input.dataBoundary&&Number.isFinite(d.estimatedCost)&&d.estimatedCost>=0&&d.estimatedCost<=input.maximumCost).sort((a,b)=>a.declaration.estimatedCost-b.declaration.estimatedCost);
  return eligible.length?{state:'READY',modelId:eligible[0].model.id,cost:eligible[0].declaration.estimatedCost}:{state:'PAUSED',reason:'授权模型中没有同时满足能力、质量、风险、数据边界与费用上限的模型。'};
}
export function recordBudgetAdjustmentOn(s,input) {
  const db=writable(s),ctx=context(s,input);
  if(!input.reason?.trim()||!input.impact?.trim()||!input.hardRequirements?.trim())throw Error('请填写调节原因、影响与必须保留的验收要求。');
  const limits=['contextTokens','candidates','concurrency','outputTokens'];
  for(const config of [input.before,input.after]) {
    if(!config||!s.resources.models.some(m=>m.id===config.modelId&&m.status==='生效'))throw Error('调节前后必须使用生效的授权模型。');
    for(const k of limits)if(!Number.isSafeInteger(config[k])||config[k]<1)throw Error('上下文、候选、并发与输出上限必须为正整数。');
  }
  if(input.before.modelId!==input.after.modelId) {
    if(!(input.allowedModelIds||[]).includes(input.after.modelId)||input.before.dataBoundary!==input.after.dataBoundary)throw Error('换模型必须保持数据边界且属于允许集合。');
    if(!input.safeBoundary||input.unfinishedTool||input.unknownSideEffects)throw Error('当前没有可验证的安全边界；未完成工具或未知副作用不能换模型。');
    const execution=s.executions?.find(e=>e.id===input.executionId&&e.tenantId===s.activeTenant&&e.workId===ctx.workId);
    if(!execution||!['CREATED','PAUSED','SUCCEEDED','FAILED','CANCELLED'].includes(execution.state)||executionSafetyBoundaryProblem(execution))throw Error('当前执行记录没有可验证的安全边界，不能只凭表单声明换模型。');
    if(!['ADAPTER_BOUNDARY','LINKED_EXECUTION'].includes(input.switchMode))throw Error('请明确适配器边界切换或关联新执行，不可声明轮次中无损换模型。');
    if(input.switchMode==='ADAPTER_BOUNDARY'&&execution.capabilities?.modelSwitchAtBoundary!==true)throw Error('当前适配器未声明边界换模型能力，请使用关联新执行。');
    if(input.switchMode==='LINKED_EXECUTION'){
      const prior=s.executions?.find(e=>e.id===input.executionId), next=s.executions?.find(e=>e.id===input.replacementExecutionId);
      if(!prior||!next||next.parentExecutionId!==prior.id||prior.workId!==ctx.workId||next.workId!==ctx.workId)throw Error('请选择同任务的真实关联执行记录。');
    }
  }
  const row={id:key('BA'),tenantId:s.activeTenant,...ctx,createdAt:stamp(),source:'LOCAL_SIMULATION',reason:input.reason.trim(),impact:input.impact.trim(),hardRequirements:input.hardRequirements.trim(),before:clone(input.before),after:clone(input.after),switchMode:input.switchMode||null,executionId:input.executionId||null,replacementExecutionId:input.replacementExecutionId||null,estimatedSaving:amount(input.estimatedSaving||0),qualityResult:'待复核',actualSaving:null};
  db.adjustments.push(row);return row;
}
export function evaluateBudgetAdjustmentOn(s,input) {
  const db=writable(s),r=db.adjustments.find(r=>r.id===input.id&&r.tenantId===s.activeTenant);
  if(!r||!input.qualityResult?.trim()||!input.evidence?.trim())throw Error('请选择调节记录并填写质量结果与证据。');
  const actualSaving=Number(input.actualSaving);if(!Number.isFinite(actualSaving)||Math.abs(actualSaving)>1e9)throw Error('请填写有效实际节省，费用增加可填写负数。');
  r.evaluations??=[];r.evaluations.push({at:stamp(),actualSaving,qualityResult:input.qualityResult.trim(),evidence:input.evidence.trim()});
  r.actualSaving=r.evaluations.at(-1).actualSaving;r.qualityResult=r.evaluations.at(-1).qualityResult;return r;
}
export function executionBudgetPolicies(s,e) {
  const at=stamp();return (s.accounting?.policies||[]).filter(p=>p.enabled&&p.tenantId===s.activeTenant&&applicable(p,{workId:e.workId,projectId:e.projectId,agentId:e.agentId})&&(p.window==='day'||at>=p.startsAt&&at<p.endsAt));
}
export function reserveExecutionBudgetOn(s,e,input) {
  const policies=executionBudgetPolicies(s,e);if(!policies.length)return;
  if(!e.agentId||!e.workId)throw Error('此执行缺少可核对的任务或Agent，不能在已启用预算下启动。');
  const reserved=amount(input.maxEstimatedCost,true),model=s.agents.find(a=>a.id===e.agentId)?.modelId;
  if(!s.resources.models.some(m=>m.id===model&&m.status==='生效'))throw Error('请先为执行Agent配置生效模型，不能在未知模型费用下启动。');
  const prior=s.accounting.calls.find(c=>c.executionId===e.id&&c.dispatchEpoch===e.dispatchEpoch+1);
  if(prior)throw Error('此派发已有预留，不得重复启动。');
  for(const p of policies){const b=budgetBalance(s,p);if(b.used+reserved>p.limit||b.active>=p.concurrency)throw Error(`预算或并发限制阻止启动：${p.subject}。请核对预算，不会新派发。`);}
  const c={id:key('UC'),requestKey:`execution:${e.id}:${e.dispatchEpoch+1}`,tenantId:s.activeTenant,workId:e.workId,projectId:e.projectId,agentId:e.agentId,executionId:e.id,dispatchEpoch:e.dispatchEpoch+1,modelId:model,reserved,actual:null,corrections:[],state:'RESERVED',purpose:'本地执行启动预留',createdAt:stamp(),source:'LOCAL_SIMULATION',notices:policies.filter(p=>(budgetBalance(s,p).used+reserved)/p.limit>=p.soft).map(p=>`${p.subject}已达到提醒阈值。`)};
  s.accounting.calls.push(c);e.budgetCallId=c.id;
}
export function closeExecutionBudgetOn(s,e) {
  const c=s.accounting?.calls.find(c=>c.id===e.budgetCallId);
  if(c?.state==='RESERVED'){c.state='PENDING';c.closedAt=stamp();c.outcome=e.state;c.disposition='执行已结束；未收到供应商用量，继续保留预留费用并等待对账。';}
}
