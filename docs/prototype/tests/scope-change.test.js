import { beforeEach, test, expect } from 'bun:test';
if(!globalThis.localStorage){const data=new Map();globalThis.localStorage={getItem:k=>data.get(k)||null,setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k)};}
const api=await import('../src/store.js?scope-change-tests');
beforeEach(api.reset);
const original=()=>api.read().work.find(w=>w.id==='W-104');
const atomic=fn=>{const before=structuredClone(api.read());expect(fn).toThrow();expect(api.read()).toEqual(before);};
test('scope change uses existing draft confirmation with fixed original version and independent work',()=>{
 const before=structuredClone(original()),id=api.beginWorkDraft({scopeChangeFrom:before.id,scopeChangeReason:'新增验收范围'});
 expect(original()).toEqual(before);expect(api.read().drafts[id].scopeChange).toMatchObject({workId:before.id,version:before.version,reason:'新增验收范围'});
 api.saveWorkDraft(id,{title:'独立新范围',criteria:'新增结果的验收依据',project:before.project,type:before.type},0);
 const created=api.confirmWorkDraft(id,1);expect(created.id).not.toBe(before.id);expect(created.scopeChange.version).toBe(before.version);expect(created.rework).toBeUndefined();expect(original()).toEqual(before);
 expect(api.confirmWorkDraft(id,1).id).toBe(created.id);
});
test('scope source cannot be forged, revoked, archived or overwritten through draft fields',()=>{
 atomic(()=>api.beginWorkDraft({scopeChangeFrom:'missing',scopeChangeReason:'范围'}));atomic(()=>api.beginWorkDraft({scopeChangeFrom:'W-104',scopeChangeReason:''}));
 const id=api.beginWorkDraft({scopeChangeFrom:'W-104',scopeChangeReason:'范围变化'}),before=structuredClone(api.read().drafts[id].scopeChange);
 api.saveWorkDraft(id,{title:'新范围',criteria:'确认',type:'软件',project:original().project,scopeChange:{workId:'forged'}},0);expect(api.read().drafts[id].scopeChange).toEqual(before);
 api.update(s=>s.projects.find(p=>p.id===before.projectId).archivedAt='2026-10-14');atomic(()=>api.confirmWorkDraft(id,1));
 api.reset();const other=api.beginWorkDraft({scopeChangeFrom:'W-104',scopeChangeReason:'范围变化'});api.update(s=>{s.role='executor';s.organization.projectRoles=[];s.projects.find(p=>p.id===original().project).ownerId='H-zhou';s.projects.find(p=>p.id===original().project).owner='周宁';});
 expect(api.workDraftReadProblem(api.read(),api.read().drafts[other])).not.toBe('');atomic(()=>api.saveWorkDraft(other,{title:'不可写'},0));
});
