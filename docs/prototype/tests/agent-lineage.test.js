import { beforeEach, test, expect } from 'bun:test';
if (!globalThis.localStorage) { const entries=new Map();globalThis.localStorage={getItem:k=>entries.get(k)||null,setItem:(k,v)=>entries.set(k,v),removeItem:k=>entries.delete(k)}; }
const {read,reset,update,saveAgent,saveOrganization,createTenant,workCommand}=await import('../src/store.js');
beforeEach(()=>reset());
const config=()=>({name:'子研究员',role:'研究员',responsibilities:'整理证据',systemPrompt:'固定职责',taskTemplate:'任务 {{task}}',runtime:'Codex CLI',modelId:'MD-01',owner:'林予',skillIds:[],mcpIds:[],creatorKind:'Agent',creatorId:'atlas'});
test('Human organization rejects Agent membership while task and Agent identities remain separate',()=>{
  expect(read().organization.nodes.every(node=>node.memberIds.every(id=>id.startsWith('H-')))).toBe(true);
  const node=read().organization.nodes[1],before=structuredClone(read());
  expect(()=>saveOrganization('nodes',{...node,memberIds:['agent:atlas']},'混淆身份')).toThrow();
  expect(read()).toEqual(before);
});
test('Agent-created proposal preserves exact creator across edits, task cancellation and parent retirement',()=>{
  const org=structuredClone(read().organization),id=saveAgent(config());
  const find=()=>read().agents.find(a=>a.id===id);
  expect(find()).toMatchObject({creatorKind:'Agent',creatorId:'atlas',status:'待批准'});
  expect(read().organization).toEqual(org);
  saveAgent({...config(),creatorKind:'Human',creatorId:'H-zhou',name:'新岗位'},id,1);
  expect(find()).toMatchObject({creatorKind:'Agent',creatorId:'atlas'});
  update(s=>{s.agents.find(a=>a.id==='atlas').status='退役';});
  workCommand('W-104','cancel','结束本次工作');
  expect(find()).toMatchObject({creatorKind:'Agent',creatorId:'atlas'});
  expect(read().agents.find(a=>a.id==='quill')).toBeDefined();
});
test('invalid or retired Agent creator cannot produce a proposal; Human creator is actual current identity',()=>{
  for(const creatorId of ['foreign','H-lin']) {const before=structuredClone(read());expect(()=>saveAgent({...config(),creatorId})).toThrow();expect(read()).toEqual(before);}
  update(s=>{s.agents.find(a=>a.id==='atlas').status='退役';});
  expect(()=>saveAgent(config())).toThrow();
  const id=saveAgent({...config(),creatorKind:'Human',creatorId:'H-zhou'});
  expect(read().agents.find(a=>a.id===id)).toMatchObject({creatorKind:'Human',creatorId:'H-lin'});
});
test('legacy mixed organization links are archived without deleting Agent or task records',async()=>{
  const saved=structuredClone(read());saved.organization.nodes[1].memberIds.push('agent:atlas');
  for(const agent of saved.agents) {delete agent.creatorKind; delete agent.creatorId;}
  saved.agents.find(a=>a.id==='quill').origin='主管创建 · 已批准';
  localStorage.setItem('collaboration-prototype-v1',JSON.stringify(saved));
  const migrated=await import(`../src/store.js?lineage-migration=${Date.now()}`);
  expect(migrated.read().organization.nodes[1].memberIds).toEqual(['H-lin']);
  expect(migrated.read().organization.nodes[1].legacyMemberIds).toEqual(['agent:atlas']);
  expect(migrated.read().agents).toHaveLength(saved.agents.length);
  expect(migrated.read().work).toEqual(saved.work);
  expect(migrated.read().agents.find(a=>a.id==='quill')).toMatchObject({creatorKind:'Agent',creatorId:null});
});

test('renaming preserves stable task association and duplicate names cannot inherit identity',()=>{
  const before=structuredClone(read());
  expect(()=>saveAgent({...config(),name:'Atlas'})).toThrow('同名');
  expect(read()).toEqual(before);
  const original=read().agents.find(a=>a.id==='atlas');
  const tasks=read().work.filter(work=>work.agentId==='atlas').map(work=>work.id);
  expect(tasks.length).toBeGreaterThan(0);
  saveAgent({...original,name:'Atlas renamed'},original.id,original.revision||1);
  expect(read().work.filter(work=>work.agentId==='atlas').map(work=>work.id)).toEqual(tasks);
  expect(read().resources.worklogs.find(log=>log.id==='WL-03').ownerAgentId).toBe('atlas');
});
