import { expect, test } from "bun:test";
import { read } from "../src/store";
import { planningCommandOn, planningReadProblem, planningRows } from "../src/project-planning.js";

const fresh=()=>{const s=structuredClone(read());s.role="owner";s.offline=false;s.project="all";s.resources.requirements=[];s.resources.iterations=[];return s;};
const input=s=>({tenantId:s.activeTenant,projectId:"launch",ownerId:"H-lin",name:"需求原件",description:"实际需求说明",acceptance:"明确验收标准",goalId:null,workIds:["W-104"],changeReason:"明确记录项目范围"});
const options={makeId:prefix=>`${prefix}-test`,now:()=>"2026-10-14T00:00:00.000Z"};
const command=(s,kind,data)=>planningCommandOn(s,kind,data,options);
test("requirements preserve originals through confirm revision and archive without creating work or executions",()=>{
  const s=fresh(),work=JSON.stringify(s.work),executions=JSON.stringify(s.executions);
  const id=command(s,"requirements",input(s));let record=s.resources.requirements[0];
  expect(record.status).toBe("草稿");expect(record.revision).toBe(1);expect(record.history[0].before).toBeNull();
  command(s,"requirements",{...record,action:"confirm",changeReason:"Human核对标准"});record=s.resources.requirements[0];
  expect(record.status).toBe("已确认");expect(record.confirmedBy).toBe("H-lin");
  command(s,"requirements",{...record,acceptance:"新增核对条件",changeReason:"标准明确变更"});record=s.resources.requirements[0];
  expect(record.status).toBe("草稿");expect(record.confirmedAt).toBeNull();expect(record.history[2].before.acceptance).toBe("明确验收标准");expect(record.history[2].before.status).toBe("已确认");
  command(s,"requirements",{...record,action:"archive",changeReason:"保留原件停止维护"});record=s.resources.requirements[0];
  expect(record.archivedAt).toBeTruthy();expect(record.id).toBe(id);expect(record.history).toHaveLength(4);
  expect(()=>command(s,"requirements",{...record,changeReason:"重写"})).toThrow("归档");
  expect(JSON.stringify(s.work)).toBe(work);expect(JSON.stringify(s.executions)).toBe(executions);
});
test("iteration completion checks actual task scope and completion and retains explicit condition evidence",()=>{
  const s=fresh();command(s,"iterations",{...input(s),name:"迭代",startDate:"2026-10-01",endDate:"2026-10-14",entryCriteria:"输入明确",exitCriteria:"范围任务已验收"});
  let record=s.resources.iterations[0];
  command(s,"iterations",{...record,action:"start",verification:"已核对输入",changeReason:"开始本轮"});record=s.resources.iterations[0];
  const before=JSON.stringify(s);
  expect(()=>command(s,"iterations",{...record,action:"complete",verification:"核对",changeReason:"尝试完成"})).toThrow("未完成");expect(JSON.stringify(s)).toBe(before);
  s.work.find(w=>w.id==="W-104").status="已完成";
  expect(()=>command(s,"iterations",{...record,action:"complete",verification:"",changeReason:"完成"})).toThrow("准出");
  command(s,"iterations",{...record,action:"complete",verification:"全部任务已验收并核对准出",changeReason:"结束本轮"});record=s.resources.iterations[0];
  expect(record.status).toBe("已完成");expect(record.exitVerification).toContain("核对准出");expect(record.history[2].before.status).toBe("进行中");
  expect(()=>command(s,"iterations",{...record,description:"重写",changeReason:"改版"})).toThrow("已完成");
});
test("invalid scope identity date revisions and source fail atomically",()=>{
  for(const patch of [{tenantId:"other"},{projectId:"private"},{ownerId:"atlas"},{workIds:["missing"]},{source:{channelId:"missing"}},{goalId:"missing"}]){
    const s=fresh(),before=JSON.stringify(s);expect(()=>command(s,"requirements",{...input(s),...patch})).toThrow();expect(JSON.stringify(s)).toBe(before);
  }
  const s=fresh();command(s,"requirements",input(s));const record=s.resources.requirements[0];
  for(const patch of [{revision:0},{projectId:"film"},{tenantId:"other"}]){const before=JSON.stringify(s);expect(()=>command(s,"requirements",{...record,...patch,changeReason:"修改"})).toThrow();expect(JSON.stringify(s)).toBe(before);}
  s.projects.find(p=>p.id==="launch").archivedAt="2026-10-14";expect(()=>command(s,"requirements",{...record,action:"confirm",changeReason:"确认"})).toThrow("归档");
  const dateState=fresh();expect(()=>command(dateState,"iterations",{...input(dateState),startDate:"2026-02-30",endDate:"2026-03-02",entryCriteria:"输入",exitCriteria:"输出"})).toThrow("日期");
});
test("picker and current membership bound reads; old resource arrays can be absent",()=>{
  const s=fresh();delete s.resources.requirements;expect(planningRows(s,"requirements")).toEqual([]);
  command(s,"requirements",input(s));const record=s.resources.requirements[0];
  s.project="film";expect(planningRows(s,"requirements")).toEqual([]);expect(planningReadProblem(s,record)).toBe("");
  s.project="private";expect(planningRows(s,"requirements")).toEqual([]);
  s.project="all";expect(planningRows(s,"requirements")).toHaveLength(1);
  s.role="external";expect(planningRows(s,"requirements")).toEqual([]);expect(()=>command(s,"requirements",{...record,action:"confirm",changeReason:"确认"})).toThrow();
  s.role="member";s.organization.projectRoles=s.organization.projectRoles.filter(r=>r.memberId!=="H-lin");s.projects.find(p=>p.id==="launch").ownerId="other";
  expect(planningRows(s,"requirements")).toEqual([]);
});
test("confirmation rechecks owner and task scope; completion cannot reuse a moved completed task",()=>{
  const s=fresh();command(s,"requirements",input(s));const requirement=s.resources.requirements[0];
  s.organization.members.find(p=>p.id==="H-lin").status="DISABLED";
  expect(()=>command(s,"requirements",{...requirement,action:"confirm",changeReason:"确认"})).toThrow();
  const moved=fresh();command(moved,"requirements",input(moved));moved.work.find(w=>w.id==="W-104").project="film";
  expect(()=>command(moved,"requirements",{...moved.resources.requirements[0],action:"confirm",changeReason:"确认"})).toThrow("任务范围");
  const iteration=fresh();command(iteration,"iterations",{...input(iteration),startDate:"2026-10-01",endDate:"2026-10-14",entryCriteria:"输入",exitCriteria:"输出"});
  command(iteration,"iterations",{...iteration.resources.iterations[0],action:"start",verification:"核对",changeReason:"开始"});
  const task=iteration.work.find(w=>w.id==="W-104");task.status="已完成";task.project="film";
  const before=JSON.stringify(iteration);
  expect(()=>command(iteration,"iterations",{...iteration.resources.iterations[0],action:"complete",verification:"核对",changeReason:"完成"})).toThrow("已移出");
  expect(JSON.stringify(iteration)).toBe(before);
});
