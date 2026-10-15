import React from 'react';
import { beforeEach, expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import { TenantWorkflows } from '../src/pages-workflows';
import { DataList } from '../src/data-list';
import { ViewContext } from '../src/view-state';
import { read, reset, update } from '../src/store';
import { queryList, projectListExport } from '../src/list-query';
const memory=new Map();
globalThis.localStorage={getItem:key=>memory.get(key)??null,setItem:(key,value)=>memory.set(key,String(value)),removeItem:key=>memory.delete(key)};
beforeEach(()=>{reset();update(s=>{s.project='all';});});
function render(values={}) {
  let tree, dispatcher, original;
  const params=new URLSearchParams();for(const [key,value] of Object.entries(values))params.set(`state.${key}`,JSON.stringify(value));
  function Harness(){
    dispatcher=React.__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE.H;original=dispatcher.useSyncExternalStore;
    dispatcher.useSyncExternalStore=(_subscribe,getSnapshot)=>getSnapshot();tree=TenantWorkflows();return tree;
  }
  let html;try{html=renderToStaticMarkup(<ViewContext.Provider value={{params,key:'tenant-workflows-test'}}><Harness/></ViewContext.Provider>);}finally{if(dispatcher)dispatcher.useSyncExternalStore=original;}
  return {html,list:React.Children.toArray(tree.props.children).find(child=>child.type===DataList).props};
}
function project(list, query=''){
  const result=queryList(list.rows,{...list,query,pageSize:10,page:1});
  return projectListExport(result.matchedRows,list.columns,{source:list.exportSource,query,sort:result.sort,order:result.order});
}
test('tenant workflows aggregate authorized project definitions without mutating or colliding default identities',()=>{
  const before=structuredClone(read());const {list,html}=render();
  expect(list.rows.length).toBeGreaterThan(1);expect(new Set(list.rows.map(row=>row.id)).size).toBe(list.rows.length);
  for(const row of list.rows){expect(row.id).toBe(`${encodeURIComponent(row.projectId)}:${encodeURIComponent(row.workflowId)}`);}
  expect(list.rows.filter(row=>row.workflowId==='default')).toHaveLength(read().projects.length);
  expect(read()).toEqual(before);expect(html).toContain('先在左侧选择具体项目');expect(html).toContain('position:sticky');
  expect(list.onCreate).toBeUndefined();expect(list.onImport).toBeUndefined();
  const links=React.Children.toArray(list.renderActions(list.rows[0]).props.children);
  expect(links.every(link=>link.props.target==='_blank')).toBe(true);
  expect(links[0].props.to).toContain(`scope=${list.rows[0].projectId}`);expect(links[1].props.to).toContain('&view=runs');
});
test('aggregate preserves registry tenant boundaries, project picker scope, and read permissions',()=>{
  update(s=>{s.projectWorkflows??={};s.projectWorkflows.launch=[{id:'foreign',tenantId:'foreign',name:'FOREIGN_SECRET',revision:1},{id:'local',tenantId:s.activeTenant,name:'LOCAL_DEFINITION',revision:1,flow:{flowVersion:0}}];});
  const all=render().list;expect(all.rows.some(row=>row.workflowId==='foreign')).toBe(false);expect(all.rows.some(row=>row.workflowId==='local')).toBe(true);
  update(s=>{s.project='launch';});expect(render().list.rows.every(row=>row.projectId==='launch')).toBe(true);
  update(s=>{s.role='external';});expect(render().list.rows).toHaveLength(0);
  update(s=>{s.role='owner';s.organization.members=s.organization.members.filter(member=>member.id!=='H-lin');});expect(render().list.rows).toHaveLength(0);
});
test('search, archive filtering and complete-query exports preserve definition and management versions',()=>{
  update(s=>{s.projectWorkflows??={};s.projectWorkflows.launch=Array.from({length:24},(_,i)=>({id:`WF-${i}`,tenantId:s.activeTenant,name:`Definition ${i}`,revision:i+1,history:[],flow:{flowVersion:3},privateSecret:'NO_EXPORT'}));s.projects.find(project=>project.id==='film').archivedAt='2026-10-14';});
  const {list}=render();const exported=project(list,'Definition');
  expect(exported.count).toBe(24);expect(exported.records.every(row=>row.definitionVersion===3&&row.version>0)).toBe(true);
  expect(JSON.stringify(exported)).not.toContain('NO_EXPORT');expect(list.onExport(exported)).toBe(true);
  const archived=render({'tenant-workflows.status':'archived'}).list;
  expect(project(archived).records.every(row=>row.project.id==='film')).toBe(true);
});
test('export and refresh recheck fresh authorization, fixed versions and selected scope',()=>{
  for(const change of [s=>{s.project='launch';},s=>{s.role='external';},s=>{s.organization.members=[];},s=>{s.projects=s.projects.filter(project=>project.id!=='launch');},s=>{s.projects.find(project=>project.id==='launch').name='Changed project';}]){
    reset();update(s=>{s.project='all';});const {list}=render(),payload=project(list);update(change);expect(()=>list.onExport(payload)).toThrow();
  }
  reset();update(s=>{s.project='all';});const {list}=render();update(s=>{s.project='launch';});expect(()=>list.onRefresh()).toThrow('授权范围');
  reset();update(s=>{s.project='all';s.projectWorkflows??={};s.projectWorkflows.launch=[{id:'version-probe',name:'Version probe',tenantId:s.activeTenant,revision:1,flow:{flowVersion:2}}];});
  const versionList=render().list,versionExport=project(versionList,'Version probe');
  update(s=>{s.projectWorkflows.launch[0].flow.flowVersion=3;});expect(()=>versionList.onExport(versionExport)).toThrow('工作流或授权已变化');
});
