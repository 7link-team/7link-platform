import { beforeEach, test, expect } from 'bun:test';
if (!globalThis.localStorage) { const data=new Map(); globalThis.localStorage={getItem:k=>data.get(k)||null,setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k)}; }
const {read, reset, update, joinTenant, declineTenantInvitation, switchTenant, createTenant, createProject}=await import('../src/store.js?tenant-inbox-tests');
beforeEach(()=>reset());
test('new projects bind a current-tenant Human identity and reject stale or fabricated ownership atomically',()=>{
  const tenantId=createTenant('项目身份验收');
  const input={tenantId,name:'  新项目  ',description:'  明确交付结果  ',ownerId:'H-lin'};
  for(const patch of [{ownerId:'H-zhou'},{ownerId:'agent:atlas'},{ownerId:'林予'},{ownerId:''},{tenantId:'studio'},{name:' '},{description:' '}]){
    const before=structuredClone(read());
    expect(()=>createProject({...input,...patch})).toThrow();
    expect(read()).toEqual(before);
  }
  // Display names may change or collide; the chosen identity remains authoritative.
  update(s=>{s.organization.members[0].name='新姓名';s.organization.members.push({id:'H-other',name:'新姓名',kind:'Human'});});
  const projectId=createProject(input);
  expect(read().projects.find(p=>p.id===projectId)).toMatchObject({name:'新项目',description:'明确交付结果',ownerId:'H-lin',owner:'新姓名',progress:0});
  expect(read().events[0].target).toBe(`/p/${projectId}`);
  switchTenant('studio');
  expect(read().projects.some(p=>p.id===projectId)).toBe(false);
  expect(()=>createProject(input)).toThrow('租户已切换');
  for(const patch of [{role:'external'},{role:'owner',offline:true}]){
    update(s=>Object.assign(s,patch));const before=structuredClone(read());
    expect(()=>createProject({...input,tenantId:'studio'})).toThrow();expect(read()).toEqual(before);
  }
});
test('account invitation acceptance is idempotent and keeps tenant data isolated',()=>{
  const work=structuredClone(read().work);
  expect(joinTenant('DEMO-JOIN')).toBe('partner');
  expect(read().work).toHaveLength(0);
  expect(read().tenantInvitations[0].status).toBe('已接受');
  joinTenant('DEMO-JOIN');
  expect(read().tenants.filter(t=>t.id==='partner')).toHaveLength(1);
  switchTenant('studio');expect(read().work).toEqual(work);
  expect(read().tenantInvitations[0].status).toBe('已接受');
});
test('invalid, expired, revoked and differently addressed invitations grant no membership',()=>{
  for(const change of [{expires:'2020-01-01'},{expires:'invalid'},{status:'已撤销'},{recipient:'其他人'}]){
    reset();update(s=>Object.assign(s.tenantInvitations[0],change));const before=structuredClone(read());
    expect(()=>joinTenant('DEMO-JOIN')).toThrow();expect(read()).toEqual(before);
  }
});
test('declining a pending invitation never joins or changes the active tenant',()=>{
  const current=read().activeTenant;declineTenantInvitation('invite-partner');
  expect(read().activeTenant).toBe(current);expect(read().tenantInvitations[0].status).toBe('已拒绝');
  expect(()=>joinTenant('DEMO-JOIN')).toThrow();expect(read().tenants.some(t=>t.id==='partner')).toBe(false);
});
