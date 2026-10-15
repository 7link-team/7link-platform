// Isolated owner session with seed launch project. All fixture tasks and drafts
// are created through the real UI; no store command or export handler is mocked.
(async () => {
  const wait = () => new Promise(resolve => setTimeout(resolve, 90));
  const check = (value, message) => { if (!value) throw Error(message); };
  const until = async (predicate, message) => {
    const end = Date.now() + 10000;
    while (!predicate()) { if (Date.now() > end) throw Error(message); await wait(); }
  };
  const state = () => JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
  const params = () => new URLSearchParams(location.hash.split('?')[1] || '');
  const path = () => location.hash.split('?')[0];
  const modal = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const list = () => document.querySelector('main .resource-data-list[aria-label="任务列表"]');
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(element => element.textContent.trim() === text);
  const click = async (text, root = document) => {
    const target = button(text, root); check(target && !target.disabled, `Missing/enabled button: ${text}`);
    target.click(); await wait();
  };
  const fill = async (label, value, root = document) => {
    const input = root.querySelector(`[name="${CSS.escape(label)}"]`); check(input, `Missing field: ${label}`);
    const proto = input.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    Object.getOwnPropertyDescriptor(proto, 'value').set.call(input, value);
    input.dispatchEvent(new Event('input', {bubbles:true})); await wait();
  };
  const choose = async (label, text, root = document) => {
    const combo = root.querySelector(`[role="combobox"][aria-label="${CSS.escape(label)}"]`);
    check(combo && !combo.disabled, `Missing/enabled choice: ${label}`); combo.click(); await wait();
    const option = [...document.querySelectorAll('[role="option"]')].find(element => element.textContent.trim() === text);
    check(option, `Missing option: ${label}/${text}`); option.click(); await wait();
  };
  const close = async () => {
    const target = modal()?.querySelector('button[aria-label="关闭弹窗"]'); check(target, 'Missing modal close');
    target.click(); await until(() => !modal(), 'Modal did not close');
  };
  check(state().role === 'owner' && !state().offline, 'Suite needs an online local owner session');
  const tenant = state().activeTenant, base = `/p/launch/work-items?tenant=${encodeURIComponent(tenant)}&scope=launch`;
  location.hash = base; await until(() => list() && state().project === 'launch', 'Task list did not open in launch');
  const originalPath = path(), marker = `任务列表回归-${Date.now()}`, checks = [], created = [];
  const begin = async (title, confirm) => {
    const before = state().work.length;
    await click('发起任务', document.querySelector('main'));
    await until(() => modal() && params().get('quick_work'), 'Task draft URL did not appear');
    const draftId = params().get('quick_work');
    await fill('想要完成什么', title, modal());
    await fill('怎样才算完成', '明确验证任务列表与原位确认，不启动执行。', modal());
    await click('保存任务草稿', modal());
    check(state().drafts[draftId].title === title, 'Draft save did not persist the actual input');
    check(state().work.length === before && path() === originalPath, 'Saving draft created work or left source list');
    if (!confirm) { await close(); return draftId; }
    await click('确认生成工作与计划', modal());
    await until(() => !modal() && state().drafts[draftId]?.workId, 'Confirmation did not close draft and create work');
    const work = state().work.find(item => item.draftId === draftId);
    check(work?.title === title && work.project === 'launch', 'Confirmed task lost title/project');
    check(state().work.length === before + 1, 'Confirmation did not create exactly one task');
    check(path() === originalPath && !params().has('quick_work'), 'Confirmation left source list or retained quick_work');
    return work;
  };
  await click('发起任务', document.querySelector('main'));
  await until(()=>modal()&&params().get('quick_work'),'Draft guard setup missing');
  await fill('想要完成什么',`${marker} unsaved`,modal());
  modal().querySelector('[aria-label="关闭弹窗"]').click();await wait();
  check(modal()?.textContent.includes('放弃未保存的修改'),'Unsaved task input closed without a guard');
  await click('继续编辑',modal());
  check(modal()?.querySelector('[name="想要完成什么"]')?.value===`${marker} unsaved`,'Cancel discard lost entered task input');
  modal().querySelector('[aria-label="关闭弹窗"]').click();await wait();await click('放弃修改',modal());
  await until(()=>!modal(),'Confirmed discard did not close task editor');
  checks.push('unsaved task input has explicit discard protection and cancel retains actual input');
  // Twelve matching records ensure a real second page at page size ten.
  for (let i=0; i<12; i++) created.push(await begin(`${marker} ${String(i).padStart(2,'0')}`, true));
  checks.push('UI draft save creates no work; confirmation creates exactly one task and stays on source list');
  await fill('搜索当前授权列表', marker, list());
  await choose('每页条数', '10 条', list());
  check(list().querySelectorAll('tbody tr').length === 10 && list().textContent.includes('匹配 12 条'), 'Search/page-size did not query all twelve matching tasks');
  await click('下一页', list());
  check(list().querySelectorAll('tbody tr').length === 2, 'Second page did not contain remaining two tasks');
  check(list().querySelector('tbody tr td').textContent.trim() === '1', 'Page-local ordinal must restart at 1, per the list design contract');
  await fill('直接页码', '1', list());
  const sort = list().querySelector('button[aria-label^="按任务排序"]'); check(sort, 'Missing task sort'); sort.click(); await wait();
  const firstTitle = list().querySelector('tbody tr td:nth-child(2) button')?.textContent.trim();
  check(firstTitle === `${marker} 11`, 'Task descending sort did not apply before pagination');
  const th = list().querySelector('thead th'), firstCell = list().querySelector('tbody td'), lastCell = list().querySelector('tbody tr td:last-child');
  check([th,firstCell,lastCell].every(element=>getComputedStyle(element).position==='sticky'), 'Header, ordinal or action column is not sticky');
  await choose('任务状态', '已完成', list());
  check(list().textContent.includes('匹配 0 条'), 'Status filter did not intersect search');
  await choose('任务状态', '全部', list());
  check(list().textContent.includes('匹配 12 条'), 'Status reset did not restore matching tasks');
  checks.push('full-query search/status/sort/pagination and sticky header/ordinal/actions');

  const businessBefore = JSON.stringify(state().work);
  await click('刷新', list());
  await until(()=>list().textContent.includes('最近成功'), 'Manual refresh has no success receipt');
  check(JSON.stringify(state().work) === businessBefore, 'List refresh mutated tasks');
  await choose('定时刷新', '每 30 秒刷新', list());
  check(list().querySelector('[role="timer"]')?.textContent.includes('刷新'), 'Refresh countdown missing');
  await choose('定时刷新', '关闭定时刷新', list());
  check(!list().querySelector('[role="timer"]'), 'Disabling periodic refresh retained countdown');
  checks.push('manual refresh preserves facts; periodic refresh countdown can be enabled and disabled');

  const blobs=[], createURL=URL.createObjectURL, revokeURL=URL.revokeObjectURL, nativeClick=HTMLAnchorElement.prototype.click;
  URL.createObjectURL = blob => {blobs.push({blob});return 'blob:task-list-ui-export';};
  URL.revokeObjectURL = url => {if(url!=='blob:task-list-ui-export')revokeURL.call(URL,url);};
  HTMLAnchorElement.prototype.click = function(){if(this.download){blobs.at(-1).download=this.download;return;}return nativeClick.call(this);};
  try {
    const exportPath=path();await click('导出当前查询结果', list());
    await until(()=>blobs.length && blobs.at(-1).download, 'Actual Blob download was not requested');
    const payload=JSON.parse(await blobs.at(-1).blob.text());
    check(payload.count===12 && payload.records.length===12, 'Export contains only the current page or unrelated tasks');
    check(payload.records.every(row=>created.some(work=>work.id===row.id&&work.version===row.version)), 'Export lost exact authorized IDs/versions');
    check(payload.records[0].title===`${marker} 11`, 'Export ignored active descending order');
    check(payload.records.every(row=>!('sourceContext' in row)&&!('notes' in row)), 'Export included fields outside list projection');
    check(path()===exportPath && list() && !modal(), 'Export navigated or opened a detail modal');
    check(JSON.stringify(state().work)===businessBefore, 'Export mutated task facts');
  } finally {URL.createObjectURL=createURL;URL.revokeObjectURL=revokeURL;HTMLAnchorElement.prototype.click=nativeClick;}
  checks.push('actual full-query Blob/download with fixed IDs/versions and no navigation');

  await click('查看任务', list());await until(()=>modal()?.getAttribute('aria-label')==='任务详情','Task detail did not open in place');
  const detailId=created[11].id, detailKey=`state.task-detail.${tenant}:launch:owner:${originalPath.slice(1)}:TASK`;
  check(path()===originalPath && JSON.parse(params().get(detailKey))===detailId, 'Detail URL did not retain exact task and source path');
  const headerLink=modal().querySelector('.modal-heading-actions a');
  check(headerLink?.target==='_blank' && headerLink.getAttribute('href').includes(`/work-items/${detailId}?`), 'Detail header lacks canonical new-tab task link');
  const planTab=[...modal().querySelectorAll('.tabs a')].find(anchor=>anchor.textContent.trim()==='计划与契约');check(planTab,'Task detail plan tab missing');planTab.click();await wait();
  check(path()===originalPath && modal()?.querySelector('.tabs a.selected')?.textContent.trim()==='计划与契约','Detail tab escaped original list or failed to select plan');
  const detailTabKey=`state.task-detail-tab.${tenant}:launch:owner:${originalPath.slice(1)}:TASK.${detailId}`;
  check(JSON.parse(params().get(detailTabKey))==='plan','Detail tab selection missing from precise URL');
  const detailURL=location.hash;await close();
  location.hash=detailURL;await until(()=>modal()?.getAttribute('aria-label')==='任务详情','Copied detail URL did not restore modal');await close();
  checks.push('detail stays in place, exact URL restores modal, header exposes canonical new-tab link');

  await click('清空条件', list());
  const draftA=await begin(`${marker} draft-A`,false), draftB=await begin(`${marker} draft-B`,false);
  const beforeSwitch=state().work.length;
  location.hash=`${base}&quick_work=${encodeURIComponent(draftA)}`;
  await until(()=>modal()?.querySelector('[name="想要完成什么"]')?.value===`${marker} draft-A`,'Draft A deep link did not open A');
  // Do not close/unmount first: this is the stale initial-useState regression.
  location.hash=`${base}&quick_work=${encodeURIComponent(draftB)}`;
  await until(()=>modal()?.querySelector('[name="想要完成什么"]')?.value===`${marker} draft-B`,'Changing quick_work A to B still displays draft A');
  await click('确认生成工作与计划',modal());
  await until(()=>!modal()&&state().drafts[draftB]?.workId,'Draft B was not confirmed');
  check(!state().drafts[draftA].workId && !state().work.some(work=>work.draftId===draftA),'Switching to B incorrectly confirmed A');
  check(state().work.filter(work=>work.draftId===draftB).length===1 && state().work.length===beforeSwitch+1,'Switching draft created the wrong number of tasks');
  check(state().work.find(work=>work.draftId===draftB)?.title===`${marker} draft-B`,'Confirmed task contains stale A input');
  check(path()===originalPath&&!params().has('quick_work'),'B confirmation did not return to source list');
  checks.push('same-page quick_work A→B displays and confirms only B; A remains an unconfirmed draft');
  check(document.documentElement.scrollWidth<=innerWidth+2,'Task list causes viewport overflow');
  return {pass:true,width:innerWidth,checks,createdTaskIds:created.map(work=>work.id),draftA,draftB};
})();
