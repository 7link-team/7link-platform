import { test, expect, beforeEach } from "bun:test";
const storage = new Map();
globalThis.localStorage = { getItem: key => storage.get(key) || null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) };
const api = await import("../src/store.js");
beforeEach(() => api.reset());
const repo = id => api.read().resources.repositories.find(item => item.id === id);
const form = () => ({ name: "Repository archive fixture", owner: "林予", provider: "Git", branch: "main", scope: "Read only", projectId: "launch", tenantId: api.read().activeTenant });
const create = () => api.saveResource("repositories", form());
const receipt = id => ({ tenantId: api.read().activeTenant, revision: repo(id).revision || 1 });
const command = (id, status="已归档", label="归档仓库", reason="项目维护结束，保留历史", expected=receipt(id)) => api.resourceCommand("repositories", id, status, reason, label, expected);
const atomic = fn => { const before=structuredClone(api.read()), stored=[...storage]; expect(fn).toThrow(); expect(api.read()).toEqual(before); expect([...storage]).toEqual(stored); };
test("new repository records explicit current project without fabricated runtime or archive facts", () => {
 atomic(()=>api.saveResource("repositories", {...form(), archivedAt:"forged"}));
 const id=api.saveResource("repositories", {...form(), archivedBy:"other", archiveReason:"forged", status:"已连接", commit:"forged", history:[{actor:"forged"}]});
 expect(repo(id)).toMatchObject({id, projectId:"launch", tenantId:api.read().activeTenant, revision:1, status:"草稿"});
 expect(repo(id).archivedAt).toBeUndefined(); expect(repo(id).commit).toBeUndefined(); expect(repo(id).history).toHaveLength(1);
 expect(repo(id).projectBinding).toMatchObject({actorId:"H-lin", actorKind:"Human", source:"LOCAL_SIMULATION", projectId:"launch"});
 atomic(()=>api.saveResource("repositories", {...form(), id:"nonexistent"})); atomic(()=>api.saveResource("repositories", {...form(), projectId:"foreign"}));
});
test("legacy unknown project remains unknown until explicit binding and archive does not infer scope", () => {
 const legacy=structuredClone(repo("REPO-01")); expect(legacy.projectId).toBeUndefined(); atomic(()=>command(legacy.id));
 api.saveResource("repositories", {...legacy, tenantId:api.read().activeTenant, revision:1, projectId:"launch"});
 expect(repo(legacy.id).projectId).toBe("launch"); expect(repo(legacy.id).history[0].snapshot).toMatchObject(legacy);
 expect(repo(legacy.id).history[0].snapshot.projectId).toBeUndefined(); expect(repo(legacy.id).commit).toBe(legacy.commit);
 command(legacy.id); expect(repo(legacy.id).archivedAt).toBeTruthy();
});
test("archive keeps identity, version, sync facts and history without deleting external-resource projections", () => {
 const id=create(); api.update(s=>{const r=s.resources.repositories.find(item=>item.id===id);r.version="fixed-v7";r.commit="abc123";});
 command(id,"已连接","模拟增量同步","local simulated sync"); const before=structuredClone(api.read()), original=structuredClone(repo(id)), started=Date.now(); command(id);
 const saved=repo(id);expect(saved).toMatchObject({id,version:original.version,commit:original.commit,status:original.status,sync:original.sync,projectId:original.projectId,revision:original.revision+1,archivedBy:"H-lin",archiveSource:"LOCAL_SIMULATION"});
 expect(Date.parse(saved.archivedAt)).toBeGreaterThanOrEqual(started);expect(Date.parse(saved.archivedAt)).toBeLessThanOrEqual(Date.now());
 expect(saved.history.slice(0,-1)).toEqual(original.history);expect(saved.history.at(-1).snapshot).toMatchObject({...original,history:undefined});
 expect(api.read().work).toEqual(before.work);expect(api.read().nodes).toEqual(before.nodes);expect(api.read().projects).toEqual(before.projects);
});
test("archived canonical records reject stale config, forged clearing, all sync and restore routes atomically", () => {
 const id=create(), stale=structuredClone(repo(id));command(id);
 for (const patch of [{...stale},{...repo(id),archivedAt:null},{...repo(id),archivedAt:undefined,projectId:"film"},{...repo(id),id:"missing"}]) atomic(()=>api.saveResource("repositories",patch));
 for(const [status,label] of [["已连接","模拟增量同步"],["已暂停","暂停同步"],["已归档","归档仓库"],["草稿","恢复仓库"],["已连接",""]]) atomic(()=>command(id,status,label));
});
test("optimistic tenant/revision checks cover edits, archive and sync and never overwrite concurrent changes", () => {
 const id=create(), old=structuredClone(repo(id)), oldReceipt=receipt(id);
 api.saveResource("repositories",{...old,branch:"develop"});atomic(()=>api.saveResource("repositories",{...old,branch:"stale"}));atomic(()=>command(id,"已归档","归档仓库","stale",oldReceipt));
 command(id,"已连接","模拟增量同步","new observation");atomic(()=>api.saveResource("repositories",{...old,revision:2}));
 for(const tenantId of [undefined,"foreign"]) {atomic(()=>api.saveResource("repositories",{...repo(id),tenantId}));atomic(()=>command(id,"已归档","归档仓库","wrong tenant",{...receipt(id),tenantId}));}
 atomic(()=>api.saveResource("repositories",{...repo(id),projectId:"film"}));
 api.update(s=>s.activeTenant="foreign");atomic(()=>api.saveResource("repositories",{...old,tenantId:"foreign",revision:repo(id).revision}));atomic(()=>command(id));
});
test("role, identity, offline, missing project, reason and unsupported action checks are atomic", () => {
 for(const role of ["executor","external","agent"]) {api.reset();const id=create();api.update(s=>s.role=role);atomic(create);atomic(()=>api.saveResource("repositories",repo(id)));atomic(()=>command(id));}
 api.reset();const id=create();for(const reason of ["", "  ", null, {}]) atomic(()=>command(id,"已归档","归档仓库",reason));
 for(const [status,label] of [["已归档",""],["已连接","forged"],["已撤销","归档仓库"]]) atomic(()=>command(id,status,label));
 api.update(s=>s.offline=true);atomic(()=>command(id));api.update(s=>{s.offline=false;s.organization.members=s.organization.members.filter(p=>p.id!=="H-lin");});atomic(()=>command(id));
 api.reset();const missing=create();api.update(s=>s.projects=s.projects.filter(p=>p.id!=="launch"));atomic(()=>command(missing));
});
test("active archived and all filters retain direct historical lookup without foreign tenant or external leakage", () => {
 const id=create();command(id);expect(api.repositoryRecords(api.read()).some(r=>r.id===id)).toBe(false);
 expect(api.repositoryRecords(api.read(),{archive:"archived"}).map(r=>r.id)).toEqual([id]);expect(api.repositoryRecords(api.read(),{archive:"all"}).some(r=>r.id===id)).toBe(true);
 expect(api.repositoryRecords(api.read(),{archive:"archived",query:"does not exist"})).toHaveLength(0);
 api.update(s=>s.resources.repositories.push({...repo(id),id:"foreign",tenantId:"foreign"}));expect(api.repositoryRecords(api.read(),{archive:"all"}).some(r=>r.id==="foreign")).toBe(false);
 api.update(s=>s.role="external");expect(api.repositoryRecords(api.read(),{archive:"all"})).toHaveLength(0);
});
