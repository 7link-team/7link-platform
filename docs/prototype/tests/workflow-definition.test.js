import {test,expect} from 'bun:test';
import {initial} from '../src/data.js';
import {publishProjectFlow,flowDefinition,requestWorkFlowChangeOn,decideWorkFlowChangeOn} from '../src/store.js';
import {serializeWorkflowDefinition,parseWorkflowDefinition,replaceWorkflowDraft,evaluateLoopBoundary} from '../src/workflow-definition.js';
const fresh=()=>({...structuredClone(initial),activeTenant:'tenant-test',flowVersion:3});
const graph=s=>({baseVersion:s.flowVersion,nodes:structuredClone(s.flowNodes),edges:structuredClone(s.flowEdges)});
const atomic=(s,fn)=>{const before=structuredClone(s);expect(fn).toThrow();expect(s).toEqual(before);};
test('stale and missing publication baselines cannot overwrite graph or history',()=>{
 const s=fresh(),a=graph(s),b=graph(s);a.nodes[0].data.label='A';b.nodes[0].data.label='B';expect(publishProjectFlow(s,'launch',a)).toBe(4);
 atomic(s,()=>publishProjectFlow(s,'launch',b));const missing=graph(s);delete missing.baseVersion;atomic(s,()=>publishProjectFlow(s,'launch',missing));
 expect(flowDefinition(s,'launch',4).nodes[0].data.label).toBe('A');expect(flowDefinition(s,'launch',3).nodes).toEqual(initial.flowNodes);
});
test('same-version shared draft edits reject stale snapshots atomically',()=>{
 const s=fresh(),a=graph(s);s.flowDraft=null;replaceWorkflowDraft(s,a,'null');const expected=JSON.stringify(s.flowDraft),b=structuredClone(a);b.nodes[0].data.label='B';replaceWorkflowDraft(s,b,expected);atomic(s,()=>replaceWorkflowDraft(s,a,expected));
});
test('definition round trips without instance state and refuses list summaries',()=>{
 const s=fresh(),g=graph(s);g.nodes[0].data.executionId='private-run';g.work=[{id:'private'}];g.nodes[0].selected=true;
 const text=serializeWorkflowDefinition(g,{version:3}),parsed=parseWorkflowDefinition(text);expect(text).not.toContain('private');expect(parsed.nodes[0].selected).toBeUndefined();expect(parsed.edges).toEqual(g.edges);
 for(const bad of ['oops','{}',JSON.stringify({format:'workflow-definition',schemaVersion:2,nodes:[],edges:[]}),JSON.stringify({format:'workflow-definition',schemaVersion:1,nodes:[null],edges:[]})])expect(()=>parseWorkflowDefinition(bad)).toThrow();
});
test('rollback creates a new version preserving work bindings and history',()=>{
 const s=fresh(),old=flowDefinition(s,'launch',3),work=structuredClone(s.work),next=graph(s);next.nodes[0].data.label='v4';publishProjectFlow(s,'launch',next);
 replaceWorkflowDraft(s,{...old,baseVersion:4},JSON.stringify(s.flowDraft));expect(publishProjectFlow(s,'launch',s.flowDraft)).toBe(5);expect(s.flowNodes).toEqual(old.nodes);expect(s.flowVersions[4].nodes[0].data.label).toBe('v4');expect(s.work).toEqual(work);
});
test('migration pins patch and boundary; approval does not pretend runtime applied it',()=>{
 const s=fresh(),w=s.work.find(w=>w.project==='launch'&&w.status!=='已完成'),next=graph(s);w.flowVersion=3;next.nodes[0].data.label='target';publishProjectFlow(s,'launch',next);
 const input={tenantId:s.activeTenant,projectId:'launch',workId:w.id,baseVersion:3,targetVersion:4,reason:'change needed',boundary:'Only future unstarted nodes after human pause verification'};
 const id=requestWorkFlowChangeOn(s,input);expect(w.flowChangeRequests[0].patch).toHaveLength(1);atomic(s,()=>requestWorkFlowChangeOn(s,input));
 const decide={tenantId:s.activeTenant,projectId:'launch',workId:w.id,requestId:id,decision:'approve',reason:'boundary reviewed'};
 s.role='executor';atomic(s,()=>decideWorkFlowChangeOn(s,decide));s.role='owner';expect(decideWorkFlowChangeOn(s,decide)).toBe('已批准待应用');expect(w.flowVersion).toBe(3);atomic(s,()=>decideWorkFlowChangeOn(s,decide));
});
test('loop count time cost limits stop independently; successful exit stays distinct',()=>{
 const p={enabled:true,maxAttempts:3,maxSeconds:100,maxCost:10,exitCondition:'quality gate passed'};
 for(const attempts of [[{seconds:100,cost:0,solution:'a'}],[{seconds:0,cost:10,solution:'a'}],Array.from({length:3},()=>({seconds:1,cost:1,solution:'a'}))])expect(evaluateLoopBoundary(p,attempts).requiresHuman).toBe(true);
 expect(evaluateLoopBoundary(p,[{seconds:1,cost:1,solution:'a'}]).status).toBe('边界内');expect(evaluateLoopBoundary(p,[{seconds:100,cost:10,solution:'a',exitSatisfied:true}]).status).toBe('已退出');
 for(const key of ['maxAttempts','maxSeconds','maxCost'])expect(()=>evaluateLoopBoundary({...p,[key]:0},[])).toThrow();expect(()=>evaluateLoopBoundary(p,[{seconds:-1,cost:0,solution:'a'}])).toThrow();
});
