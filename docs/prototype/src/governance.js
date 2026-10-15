import { channelPeople } from './channels.js';
const copy = value => structuredClone(value);
const uid = prefix => `${prefix}-${crypto.randomUUID()}`;
export const governanceHumans = s => channelPeople(s).filter(p => p.kind === 'Human' && !p.external && p.available);
function guard(s, input) {
  if (input.tenantId !== s.activeTenant) throw Error('租户已切换，请重新打开。');
  if (s.offline || s.role !== 'owner' || !governanceHumans(s).some(p => p.id === 'H-lin')) throw Error('需要当前有效 Human 负责人在线操作。');
  if (!input.reason?.trim()) throw Error('请填写依据。');
}
const human = (s,id) => governanceHumans(s).some(p => p.id === id);
export function approvalTally(record) {
  const flow = record.orchestration;
  if (!flow) return null;
  if (flow.expiredAt && flow.timeoutAction === '到期终止等待') return {approved:0,remaining:0,required:0,result:'待补充'};
  const approved = flow.votes.filter(v => v.choice === '已批准').length;
  const remaining = flow.participants.length - flow.votes.length;
  const required = flow.strategy === 'ALL' ? flow.participants.length : flow.strategy === 'ANY' ? 1 : flow.quorum;
  return { approved, remaining, required, result: approved >= required ? '已批准' : approved + remaining < required ? '已拒绝' : null };
}
export function approvalOrchestrationProblem(record, status) {
  if (!record.orchestration) return '';
  const result = approvalTally(record)?.result;
  return result === status ? '' : '多人审批尚未达到此结果，请按冻结规则完成参与者决定。';
}
// Final decisions still use resourceCommand; this module records only the frozen rule and votes.
export function approvalFlowOn(s, input, now = Date.now()) {
  guard(s,input);
  const record = s.resources.approvals.find(r => r.id === input.id);
  if (!record || record.status !== '待审核' || (record.revision || 1) !== input.revision) throw Error('审批已变化或已经终结。');
  if (input.action === 'configure') {
    if (record.orchestration) throw Error('已冻结审批规则不可重写；请从源对象重新提交。');
    if (!['顺序','并行'].includes(input.mode) || !['ALL','ANY','QUORUM'].includes(input.strategy)) throw Error('请选择阶段与通过策略。');
    if (!Array.isArray(input.humanIds) || !Array.isArray(input.roles)) throw Error('请选择参与者及角色。');
    const resolved = input.roles.flatMap(role => s.organization.projectRoles.filter(r => r.projectId === input.projectId && r.role === role).map(r => r.memberId));
    if (input.roles.length && (!s.projects.some(p => p.id === input.projectId && !p.archivedAt) || input.roles.some(role => !s.organization.projectRoles.some(r => r.projectId === input.projectId && r.role === role && human(s,r.memberId))))) throw Error('指定角色必须在当前项目解析为有效 Human。');
    const participants = [...new Set([...input.humanIds,...resolved])];
    if (!participants.length || participants.some(id => !human(s,id))) throw Error('参与者须为当前有效 Human。');
    const quorum = Number(input.quorum), timeout = Number(input.timeoutSeconds);
    if (input.strategy === 'QUORUM' && (!Number.isSafeInteger(quorum) || quorum < 1 || quorum > participants.length)) throw Error('法定人数须在参与者人数范围内。');
    if (!Number.isSafeInteger(timeout) || timeout < 1 || timeout > 31536000) throw Error('审批时限须为 1–31536000 秒。');
    if (!['提醒并保持待处理','请求负责人升级','到期终止等待'].includes(input.timeoutAction)) throw Error('请选择超时处置。');
    if (input.delegateId && (!human(s,input.delegateId) || participants.includes(input.delegateId))) throw Error('代理须为名单外的有效 Human。');
    if (input.timeoutAction === '请求负责人升级' && !human(s,input.escalationId)) throw Error('请选择有效升级接收 Human。');
    record.orchestration = { ruleVersion:1, mode:input.mode, strategy:input.strategy, quorum, participants, participantSnapshot:participants.map(id=>copy(governanceHumans(s).find(p=>p.id===id))), roles:copy(input.roles), projectId:input.projectId||null, timeoutAction:input.timeoutAction, timeoutSeconds:timeout, deadline:new Date(now+timeout*1000).toISOString(), delegateId:input.delegateId||null, escalationId:input.escalationId||null, votes:[], events:[], createdBy:'H-lin', source:'LOCAL_SIMULATION', reason:input.reason.trim() };
  } else {
    const flow = record.orchestration;
    if (!flow) throw Error('请先冻结审批规则。');
    if (input.action === 'vote') {
      if (approvalTally(record).result) throw Error('规则已有结果，请完成当前固定审批。');
      if (Date.parse(flow.deadline) <= now || flow.expiredAt) throw Error('审批时限已到，请先处置超时；旧票不可继续提交。');
      if (!flow.participants.includes(input.participantId) || flow.votes.some(v=>v.participantId===input.participantId)) throw Error('不在冻结名单中或此参与者已有决定。');
      if (!human(s,input.actorId)) throw Error('投票主体已不可用。');
      if (input.actorId !== input.participantId && input.actorId !== flow.delegateId) throw Error('未获得冻结代理授权。');
      if (input.actorId !== 'H-lin' && input.simulate !== true) throw Error('其他 Human 决定须明确使用本地模拟，不能冒充真实签名。');
      if (flow.mode === '顺序' && flow.participants.find(id=>!flow.votes.some(v=>v.participantId===id)) !== input.participantId) throw Error('请等待前序参与者完成。');
      if (!['已批准','已拒绝'].includes(input.choice)) throw Error('请选择批准或拒绝。');
      flow.votes.push({ participantId:input.participantId, actorId:input.actorId, recordedBy:'H-lin', choice:input.choice, reason:input.reason.trim(), time:new Date(now).toISOString(), source:'LOCAL_SIMULATION', delegated:input.actorId!==input.participantId });
    } else if (input.action === 'timeout') {
      if (Date.parse(flow.deadline) > now || flow.expiredAt) throw Error('尚未超时或已处置。');
      flow.expiredAt = new Date(now).toISOString();
      flow.events.push({action:flow.timeoutAction,recipientId:flow.escalationId,reason:input.reason.trim(),time:flow.expiredAt,source:'LOCAL_SIMULATION'});
    } else if (input.action === 'resume') {
      if (!flow.expiredAt || flow.timeoutAction === '到期终止等待') throw Error('此审批不能恢复等待。');
      if(flow.timeoutAction === '请求负责人升级' && (input.actorId !== flow.escalationId || !human(s,input.actorId) || input.actorId !== 'H-lin' && input.simulate !== true)) throw Error('须由冻结的升级接收人明确决定继续等待。');
      flow.events.push({action:'明确继续等待',actorId:input.actorId||'H-lin',recordedBy:'H-lin',reason:input.reason.trim(),time:new Date(now).toISOString(),previousDeadline:flow.deadline,source:'LOCAL_SIMULATION'});
      flow.deadline=new Date(now+flow.timeoutSeconds*1000).toISOString();delete flow.expiredAt;
    } else throw Error('未知审批动作。');
  }
  record.revision = (record.revision || 1)+1;
  return approvalTally(record)?.result;
}

// Only live responsibilities are transferred; fixed decisions, versions and historical snapshots stay intact.
export function humanResponsibilities(s, memberId) {
  const person=s.organization.members.find(p=>p.id===memberId); if(!person)return [];
  const result=[];
  const collect=(kind,records,fields)=>{for(const record of records||[]) {
    if(record.archivedAt || s.projects.some(p=>p.id===(record.project||record.projectId)&&p.archivedAt) || ['已完成','已取消','已归档','已接受','已批准','已拒绝','已撤销','已退役','退役'].includes(record.status))continue;
    for(const [field,identity] of fields) { const value=field.startsWith('team.')?record.team?.[field.slice(5)]:record[field];const target=identity==='name'?person.name:memberId;
      if(value===target || Array.isArray(value)&&value.includes(target))result.push({key:`${kind}:${record.id}:${field}`,kind,id:record.id,field,identity,value:copy(value),label:`${record.title||record.name||record.id} · ${field}`,projectId:record.project||record.projectId||null});
    }
  }};
  collect('work',s.work,[['owner','name'],['team.coordinatorId','id'],['team.receiverId','id'],['team.executorIds','id'],['team.checkerIds','id']]);
  collect('projects',s.projects,[['owner','name'],['ownerId','id']]);
  collect('nodes',s.organization.nodes,[['ownerId','id']]);
  collect('agents',s.agents,[['owner','name'],['ownerId','id']]);
  for(const kind of ['goals','plans','reports','requirements','iterations']) collect(kind,s.resources[kind],[['owner','name'],['ownerId','id'],['authorId','id'],['reviewerId','id']]);
  return result;
}
function objectFor(s,row) { return row.kind==='nodes'?s.organization.nodes.find(r=>r.id===row.id):['work','projects','agents'].includes(row.kind)?s[row.kind].find(r=>r.id===row.id):s.resources[row.kind]?.find(r=>r.id===row.id); }
export function humanChangeOn(s,input,now=Date.now()) {
  guard(s,input);
  const person=s.organization.members.find(p=>p.id===input.memberId&&p.kind==='Human');
  if(!person || person.id==='H-lin')throw Error('请选择其他 Human；当前负责人不能在自己的会话内撤销自身。');
  s.organization.memberChanges??=[];
  const changes=s.organization.memberChanges;
  const time=new Date(now).toISOString();
  if(input.action==='begin') {
    if(!human(s,person.id)||changes.some(c=>c.memberId===person.id&&c.status==='待接管'))throw Error('成员不可用或已有待接管变更。');
    if(!['离职','调岗'].includes(input.kind))throw Error('请选择离职或调岗。');
    if(input.kind==='调岗'&&!s.organization.nodes.some(n=>n.id===input.targetNodeId))throw Error('请选择调入组织。');
    const change={id:uid('human-change'),memberId:person.id,kind:input.kind,targetNodeId:input.targetNodeId||null,status:'待接管',revision:1,reason:input.reason.trim(),createdBy:'H-lin',time,responsibilities:humanResponsibilities(s,person.id),handoffs:[],access:{mode:'LOCAL_SIMULATION',disabledAt:time,sessionRevokedAt:time,credentialRevokedAt:time,message:'本地访问已停用；会话与凭据撤销仅为模拟记录，未调用真实身份服务。'},previous:{person:copy(person),projectRoles:copy(s.organization.projectRoles.filter(r=>r.memberId===person.id)),nodeIds:s.organization.nodes.filter(n=>n.memberIds.includes(person.id)).map(n=>n.id)}};
    person.disabledAt=time;person.status=input.kind==='离职'?'离职处理中':'调岗处理中';
    for(const channel of s.channels||[])for(const m of channel.members||[])if(m.principalId===person.id&&!m.leftAt){m.leftAt=time;channel.revision=(channel.revision||1)+1;channel.history??=[];channel.history.push({action:'人员变更撤销访问',actorId:'H-lin',memberId:person.id,time});}
    changes.push(change);return change.id;
  }
  const change=changes.find(c=>c.id===input.id&&c.memberId===person.id);
  if(!change||change.status!=='待接管'||change.revision!==input.revision)throw Error('人员变更已更新或已经完成。');
  if(input.action==='handoff') {
    const row=humanResponsibilities(s,person.id).find(r=>r.key===input.key);
    if(!row || JSON.stringify(row.value)!==JSON.stringify(input.expectedValue))throw Error('责任已变化，请重新核对。');
    if (!['work','projects','nodes','agents'].includes(row.kind)) throw Error('此责任绑定业务版本，请从原对象修订负责人并完成必要审核后重试，不能通过人员变更改写生效基线。');
    const successor=governanceHumans(s).find(p=>p.id===input.successorId&&p.id!==person.id);
    if(!successor||input.confirmed!==true)throw Error('请明确选择有效接管 Human 并确认接管。');
    const target=objectFor(s,row),value=row.identity==='name'?successor.name:successor.id;
    const next=Array.isArray(row.value)?[...new Set(row.value.map(v=>v===person.id?value:v))]:value;
    target.history??=[];target.history.push({action:'人员变更责任接管',field:row.field,previous:copy(row.value),next:copy(next),reason:input.reason.trim(),actorId:'H-lin',time,source:'LOCAL_SIMULATION'});
    if(row.field.startsWith('team.')) {target.team[row.field.slice(5)]=next;target.team.revision=(target.team.revision||0)+1;} else target[row.field]=next;
    // Name and ID are one identity, never permit a split project/goal owner.
    if(row.field==='owner' && target.ownerId===person.id)target.ownerId=successor.id;
    if(row.field==='ownerId' && target.owner===person.name)target.owner=successor.name;
    target.revision=(target.revision||1)+1;
    change.handoffs.push({...copy(row),successorId:successor.id,reason:input.reason.trim(),time,recordedBy:'H-lin',source:'LOCAL_SIMULATION'});
  } else if(input.action==='complete') {
    if(humanResponsibilities(s,person.id).length)throw Error('仍有责任未接管，不能完成离职或调岗。');
    if(change.kind==='调岗'&&!s.organization.nodes.some(n=>n.id===change.targetNodeId))throw Error('调入组织已不存在。');
    s.organization.projectRoles=s.organization.projectRoles.filter(r=>r.memberId!==person.id);
    for(const node of s.organization.nodes)node.memberIds=node.memberIds.filter(id=>id!==person.id);
    if(change.kind==='调岗') {s.organization.nodes.find(n=>n.id===change.targetNodeId).memberIds.push(person.id);person.status='在职';delete person.disabledAt;}else person.status='已离职';
    change.status='已完成';change.completedAt=time;change.completionReason=input.reason.trim();
  } else throw Error('未知人员变更动作。');
  change.revision++;return change.id;
}
