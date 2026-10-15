import React from 'react';
import { beforeEach, expect, test } from 'bun:test';
import { renderToStaticMarkup } from 'react-dom/server';
import { WorkList, WorkDetail } from '../src/pages-work';
import { DataList } from '../src/data-list';
import { Modal } from '../src/ui';
import { ViewContext } from '../src/view-state';
import { read, reset, update } from '../src/store';
import { queryList, projectListExport } from '../src/list-query';

const storage = new Map();
globalThis.localStorage = {getItem:key=>storage.get(key) ?? null,setItem:(key,value)=>storage.set(key,String(value)),removeItem:key=>storage.delete(key)};
beforeEach(() => reset());
function find(node, type) {
  if (!node || typeof node !== 'object') return null;
  if (node.type === type) return node;
  for (const child of React.Children.toArray(node.props?.children)) {
    const found = find(child, type); if (found) return found;
  }
  return null;
}
// The application store is client-only. Supply its snapshot to the server renderer
// without mocking business selectors or the list implementation.
function render(props = {}, values = {}) {
  let tree, dispatcher, original;
  const params = new URLSearchParams(props.params || '');
  for (const [key, value] of Object.entries(values)) params.set(`state.${key}`, JSON.stringify(value));
  function Harness() {
    dispatcher = React.__CLIENT_INTERNALS_DO_NOT_USE_OR_WARN_USERS_THEY_CANNOT_UPGRADE.H;
    original = dispatcher.useSyncExternalStore;
    dispatcher.useSyncExternalStore = (_subscribe, getSnapshot) => getSnapshot();
    tree = WorkList({params, viewPath:'/collaboration', openNew:()=>{}, toast:()=>{}, ...props});
    return tree;
  }
  let html;
  try { html = renderToStaticMarkup(<ViewContext.Provider value={{params,key:'work-list-test'}}><Harness /></ViewContext.Provider>); }
  finally { if (dispatcher) dispatcher.useSyncExternalStore = original; }
  return {html, tree, list:find(tree, DataList)?.props};
}
function payload(list) {
  const result = queryList(list.rows, {...list, ...list.queryState});
  return projectListExport(result.matchedRows, list.columns, {source:list.exportSource, query:list.queryState.query, sort:result.sort, order:result.order});
}

test('task table searches the full selected scope before paging and retains explicit safe export fields', () => {
  update(s => {s.project='launch'; const base=s.work.find(w=>w.project==='launch');
    s.work=Array.from({length:25},(_,i)=>({...structuredClone(base),id:`LIST-${i}`,title:`任务 ${String(i).padStart(2,'0')}`,version:i+1,notes:'NOT_EXPORTABLE_SECRET'}));
    s.work.push({...structuredClone(base),id:'OTHER',project:'film',title:'OTHER_PROJECT_SECRET'});
  });
  const scope='studio:launch:owner:/collaboration:TASK';
  const {list,html}=render({}, {[`task-list.${scope}`]:{page:2,pageSize:10,sort:'title',order:'desc'}});
  expect(list.rows).toHaveLength(25); expect(html).toContain('第 2 / 3 页');
  expect(html).toContain('position:sticky'); expect(html).toContain('>序号<'); expect(html).toContain('>操作<');
  expect(html).toContain('直接页码'); expect(html).toContain('定时刷新');
  expect(html).not.toContain('OTHER_PROJECT_SECRET'); expect(list.onImport).toBeUndefined();
  const exported=payload(list);expect(exported.count).toBe(25);expect(exported.records[0].id).toBe('LIST-24');
  expect(JSON.stringify(exported)).not.toContain('NOT_EXPORTABLE_SECRET');expect(list.onExport(exported)).toBe(true);
  const narrow=render({}, {'work-search':'LIST-24'}).list;
  expect(payload(narrow).records.map(row=>row.id)).toEqual(['LIST-24']);
});

test('task exports and refresh reject changed scope, permission, archive state, and version', () => {
  update(s=>{s.project='launch';});
  for (const mutate of [s=>{s.project='film';},s=>{s.role='external';},s=>{s.projects.find(p=>p.id==='launch').archivedAt='2026-10-14';},s=>{s.work.find(w=>w.project==='launch').version++;}]) {
    reset();update(s=>{s.project='launch';});const {list}=render();const exported=payload(list);
    expect(exported.count).toBeGreaterThan(0);update(mutate);expect(()=>list.onExport(exported)).toThrow();
  }
  reset();update(s=>{s.project='launch';});const {list}=render();update(s=>{s.project='film';});expect(()=>list.onRefresh()).toThrow('授权范围');
});

test('archived project routes remain readable while the picker and defect classification stay authoritative', () => {
  update(s=>{s.project='launch';s.projects.find(p=>p.id==='launch').archivedAt='2026-10-14';s.work.find(w=>w.project==='launch').workKind='BUG';});
  expect(render().list.rows).toHaveLength(0);
  const scoped=render({projectId:'launch',workKind:'BUG',viewPath:'/defects'}).list;
  expect(scoped.rows.length).toBeGreaterThan(0);expect(scoped.rows.every(w=>w.project==='launch'&&w.workKind==='BUG')).toBe(true);
  update(s=>{s.role='external';});expect(render({projectId:'launch'}).list.rows.every(w=>w.delivered)).toBe(true);
});

test('authorized detail opens embedded in place with an explicit new-tab header and unavailable IDs reveal nothing', () => {
  update(s=>{s.project='launch';});const work=read().work.find(w=>w.project==='launch');
  const key='task-detail.studio:launch:owner:/collaboration:TASK';
  const opened=render({}, {[key]:work.id});const modal=find(opened.tree,Modal);
  expect(find(modal,WorkDetail).props).toMatchObject({workId:work.id,embedded:true});
  expect(modal.props.headerActions.props.target).toBe('_blank');expect(modal.props.headerActions.props.to).toContain(work.id);
  expect(opened.list.editing).toBe(true);
  const tabKey=`task-detail-tab.studio:launch:owner:/collaboration:TASK.${work.id}`;
  const plan=render({}, {[key]:work.id,[tabKey]:'plan'});
  expect(find(plan.tree,WorkDetail).props.params.get('tab')).toBe('plan');
  expect(find(plan.tree,Modal).props.headerActions.props.to).toContain('&tab=plan');
  const panel=find(plan.tree,Modal).props.children;
  let prevented=false,stopped=false;
  panel.props.onClickCapture({target:{closest:()=>({getAttribute:()=>`#/p/${work.project}/work-items/${work.id}?tab=review`})},button:0,preventDefault:()=>{prevented=true;},stopPropagation:()=>{stopped=true;}});
  expect(prevented).toBe(true);expect(stopped).toBe(true);
  const denied=render({}, {[key]:'not-authorized'});
  expect(denied.html).toContain('任务不存在或已不在当前授权范围');expect(find(denied.tree,WorkDetail)).toBeNull();
  expect(find(denied.tree,Modal).props.headerActions).toBeUndefined();
});

test('board and calendar remain available without losing route scope on view switches', () => {
  const board=render({params:new URLSearchParams('view=board&scope=launch&tenant=studio')});
  expect(board.list).toBeUndefined();expect(board.html).toContain('kanban');expect(board.html).toContain('type="button" class="kanban-card"');expect(board.html).not.toMatch(/<a[^>]+class="kanban-card"/);
  expect(board.html).toContain('view=list&amp;scope=launch&amp;tenant=studio');
  const calendar=render({params:new URLSearchParams('view=calendar')});expect(calendar.list).toBeUndefined();
});

 test('cancelled tasks remain visible in the board when the shared status filter selects them', () => {
  update(s=>{s.project='launch';const work=s.work.find(w=>w.project==='launch');work.status='已取消';work.title='CANCELLED_TASK_VISIBLE';});
  const values={'work-status':'已取消'};
  const table=render({},values);expect(payload(table.list).records.every(row=>row.status==='已取消')).toBe(true);
  const board=render({params:new URLSearchParams('view=board')},values);
  expect(board.html).toContain('CANCELLED_TASK_VISIBLE');expect(board.html).toContain('<h2>已取消</h2>');
});
