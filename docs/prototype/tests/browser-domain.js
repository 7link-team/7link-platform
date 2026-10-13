if (typeof window.qaCompleteValidation !== "function") throw Error("Preload tests/qa-browser-helpers.js before this suite.");
// Regression acceptance for task creation, quality facts, runtime receipts and workflow versions.
(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,160));
 const check=(v,m)=>{if(!v)throw Error(m)};
 const go=async(path)=>{location.hash=path;await wait()};
 const click=async(text,scope=document)=>{const e=[...scope.querySelectorAll('button,a')].find(e=>e.textContent.trim()===text);check(e,'Missing '+text);e.click();await wait()};
 const fill=async(label,value)=>{const el=[...document.querySelectorAll('.field')].find(e=>e.querySelector('label')?.textContent.replace(' *','').trim()===label)?.querySelector('input:not([type=hidden]),textarea');check(el,'Missing '+label);Object.getOwnPropertyDescriptor(el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const choose=async(label,value)=>{const el=document.querySelector(`[role=combobox][aria-label="${label}"]`);check(el,'Missing select '+label);el.click();await wait();await click(value,document.querySelector('[role=listbox]'))};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 await go('/p/launch/work-items');await click('发起工作');await fill('想要完成什么','浏览器新工作闭环');await choose('归属项目','Northstar · 新品发布');await fill('怎样才算完成','满足浏览器验收条款并通过内部检查');await click('确认生成工作与计划');
 const workId=state().work.find(w=>w.title==='浏览器新工作闭环').id;
 check(state().work.find(w=>w.id===workId).plan===true,'Conversation confirmation did not establish plan');await click('确认工作流版本');await click('开始执行');await click('提交新成果版本');
 check(state().work.find(w=>w.id===workId).status==='待验收','Fresh work submit failed without reload');
 await go(`/p/launch/work-items/${workId}?tab=review`);
 check(document.querySelector('main').textContent.includes('未执行'),'Unexecuted is mislabeled failed/passed');
 await go('/tests/TC-02');await click('记录失败');await fill('依据与说明','当前版本未满足条款');await click('确认操作');
 check(state().work.find(w=>w.id==='W-103').qa===false,'Failed record not reflected in gate');
 await go('/p/launch/work-items/W-103?tab=review');await click('接受当前版本');
 check(document.body.textContent.includes('不能绕过质量门'),'Human bypassed failed record');
 await window.qaCompleteValidation('W-103');
 check(state().work.find(w=>w.id==='W-103').qa===true,'Recheck not reflected in task');
 await go('/approvals/AP-01');await click('批准当前版本');await fill('依据与说明','限定当前版本');await click('确认操作');
 await go('/models/MD-02');await click('模拟应用配置');await fill('依据与说明','模拟下发');await click('确认操作');
 const receipts=JSON.stringify(state().resources.models.find(m=>m.id==='MD-02').nodeResults);
 await click('编辑');check(!document.querySelector('dialog').textContent.includes('nodeResults'),'Runtime receipts editable');await fill('名称','模型配置修改');await click('保存');
 check(!document.body.textContent.includes('页面发生错误'),'Model editor crashed');
 check(JSON.stringify(state().resources.models.find(m=>m.id==='MD-02').nodeResults)===receipts,'Editing lost runtime receipts');
 await go('/p/launch/flow');await click('编辑新草案');await fill('节点名称','新版执行节点');await click('确认发布新版本');
 check(state().flowVersion===4,'Valid gated workflow not published');
 await choose('已发布工作流版本','固定版本 v3');
 check(location.hash.includes('version=3'),'Version selector passed event instead of value');
 check(!document.querySelector('main').textContent.includes('新版执行节点'),'Historical definition overwritten');
 check(![...document.querySelectorAll('button')].some(e=>e.textContent.trim()==='编辑新草案'),'Historical definition editable');
 await go(`/p/launch/work-items/${workId}?tab=plan`);await click('打开 工作流');
 check(location.hash.includes('version=3'),'Existing work uses latest workflow');
 await go('/p/launch/flow?version=999');check(document.querySelector('main').textContent.includes('版本不存在'),'Missing version silently uses latest');
 return {pass:true,checks:['fresh creation to submission','unexecuted state','quality failure/recheck synchronization','runtime receipts immutable in editor','published version selector','historical work binding and unknown version']};
})()
