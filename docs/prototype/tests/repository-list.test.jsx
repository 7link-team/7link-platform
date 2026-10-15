import React from "react";
import { beforeEach, expect, test } from "bun:test";
import { renderToStaticMarkup } from "react-dom/server";
import { ResourcePage } from "../src/pages-admin";
import { DataList } from "../src/data-list";
import { ViewContext } from "../src/view-state";
import { read, reset, update } from "../src/store";
import { queryList, projectListExport } from "../src/list-query";

const storage=new Map();
globalThis.localStorage={getItem:key=>storage.get(key)??null,setItem:(key,value)=>storage.set(key,String(value)),removeItem:key=>storage.delete(key)};
beforeEach(()=>{reset();update(s=>{s.project="launch";s.resources.repositories=Array.from({length:25},(_,i)=>({id:`REPO-${i}`,name:`Repository ${String(i).padStart(2,"0")}`,projectId:"launch",tenantId:s.activeTenant,owner:"林予",provider:"Git",branch:"main",status:"已连接",revision:1,secret:"SECRET",history:[{token:"HISTORY_SECRET"}]}));s.resources.repositories.push({id:"OTHER",name:"Other project",projectId:"film",status:"已连接"},{id:"UNKNOWN",name:"Unknown project",status:"已连接"},{id:"ARCHIVED",name:"Archived",projectId:"launch",status:"已归档",archivedAt:"2026-10-14"});});});
function find(node,type){if(!node||typeof node!=="object")return null;if(node.type===type)return node;for(const child of React.Children.toArray(node.props?.children)){const found=find(child,type);if(found)return found;}return null;}
function render(query="",listState){
  const params=new URLSearchParams(query);if(listState)params.set(`state.repositories.list.${read().activeTenant}:${read().project}:${read().role}`,JSON.stringify(listState));
  let tree,dispatcher,original;
  function Harness(){dispatcher=React.__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE.H;original=dispatcher.useSyncExternalStore;dispatcher.useSyncExternalStore=(_subscribe,getSnapshot)=>getSnapshot();const page=ResourcePage({kind:"repositories",params,toast:()=>{}});tree=page.type(page.props);return tree;}
  let html;try{html=renderToStaticMarkup(<ViewContext.Provider value={{params,key:"repository-list-test"}}><Harness/></ViewContext.Provider>);}finally{if(dispatcher)dispatcher.useSyncExternalStore=original;}
  return {html,list:find(tree,DataList)?.props};
}
function payload(list){const result=queryList(list.rows,{...list,...list.queryState});return projectListExport(result.matchedRows,list.columns,{source:list.exportSource});}
test("repository list scopes records, queries before pagination and exports only declared summaries",()=>{
  const {list,html}=render("",{page:2,pageSize:10,sort:"name",order:"desc"});
  expect(list.rows).toHaveLength(25);expect(html).toContain("第 2 / 3 页");expect(html).toContain("直接页码");expect(html).toContain("新标签页查看连接");expect(html).toContain("position:sticky");
  const exported=payload(list);expect(exported.count).toBe(25);expect(exported.records[0].id).toBe("REPO-24");expect(JSON.stringify(exported)).not.toContain("SECRET");expect(JSON.stringify(exported)).not.toContain("history");expect(list.onExport(exported)).toBe(true);expect(list.onImport).toBeUndefined();
  expect(payload(render("q=REPO-24").list).records.map(item=>item.id)).toEqual(["REPO-24"]);
  expect(payload(render("q=NO_MATCH",{query:""}).list).count).toBe(25);
  expect(render("archive=archived").list.rows.map(item=>item.id)).toEqual(["ARCHIVED"]);
  update(s=>{s.project="all";});expect(render().list.rows).toHaveLength(27);
});
test("repository export rechecks current scope, authorization, archive and summary changes",()=>{
  const mutations=[s=>{s.project="film";},s=>{s.role="external";},s=>{s.activeTenant="changed";},s=>{s.resources.repositories[0].archivedAt="2026-10-14";},s=>{s.resources.repositories[0].projectId="film";},s=>{s.resources.repositories[0].branch="changed";}];
  for(const mutate of mutations){const before=structuredClone(read());const {list}=render();const exported=payload(list);update(mutate);expect(()=>list.onExport(exported)).toThrow();update(s=>Object.assign(s,before));}
  const {list}=render();expect(()=>list.onRefresh()).not.toThrow();update(s=>{s.project="film";});expect(()=>list.onRefresh()).toThrow("授权范围");
});
test("repository empty and offline states retain safe list controls",()=>{
  update(s=>{s.resources.repositories=[];s.offline=true;});const {list,html}=render();expect(list.offline).toBe(true);expect(html).toContain("当前条件下没有仓库");expect(payload(list).count).toBe(0);
  update(s=>{s.role="external";});expect(render().list).toBeUndefined();
});
