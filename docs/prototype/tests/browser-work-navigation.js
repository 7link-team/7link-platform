(async()=>{
 const wait=()=>new Promise(resolve=>setTimeout(resolve,180)),check=(ok,message)=>{if(!ok)throw Error(message)},go=async route=>{location.hash=route;await wait()};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const click=async(label,root=document)=>{const element=[...root.querySelectorAll('a,button')].find(element=>element.textContent.trim()===label||element.getAttribute('aria-label')===label);check(element,'Missing '+label);element.click();await wait()};
 const selected=()=>[...document.querySelectorAll('[aria-label="区域导航"] .active')].map(element=>element.textContent.trim());
 const tenant=state().activeTenant,execution=state().executions.find(item=>item.workId==='W-104'&&item.tenantId===tenant);
 if(execution){
   await go(`/chat?tenant=${encodeURIComponent(tenant)}&execution_id=${encodeURIComponent(execution.id)}`);
   check(new URLSearchParams(location.hash.split('?')[1]).get('execution_id')===execution.id&&document.querySelector('main').textContent.includes(execution.id),'Real execution identity did not open');
   await click('查看工作');check(document.querySelector('dialog[open]')&&location.hash.split('?')[0]==='#/chat','Execution work preview left source conversation');await click('关闭弹窗',document.querySelector('dialog[open]'));
 }else{
   await go('/p/launch/work-items/W-104?tab=executions');
   check(document.querySelector('main').textContent.includes('尚无执行记录'),'Missing execution should show a truthful empty registry');
   check(!state().executions.some(item=>item.workId==='W-104'),'Viewing execution registry fabricated a runtime');
 }
 const card=state().messages.find(message=>message.channel==='launch'&&message.work==='W-104'&&!message.recalled);
 check(card,'Seed launch channel work card is missing');
 const threadId=card.thread||card.id;
 await go(`/chat?tenant=${encodeURIComponent(tenant)}&scope=all&channel_id=launch&thread_id=${encodeURIComponent(threadId)}`);
 const message=document.querySelector('.composer textarea'),text=`Navigation thread reply ${Date.now()}`;check(message,'Channel composer missing');
 Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(message,text);message.dispatchEvent(new Event('input',{bubbles:true}));await wait();await click('发送');
 const saved=state().messages.find(item=>item.text===text);
 check(saved?.channel==='launch'&&saved.thread===threadId,'Reply did not retain the real channel/thread');
 const openCard=async()=>{const row=[...document.querySelectorAll('.message-work')].find(element=>element.textContent.includes(state().work.find(work=>work.id==='W-104').title));check(row,'Thread work card missing');row.click();await wait();};
 await openCard();check(location.hash.split('?')[0]==='#/chat'&&new URLSearchParams(location.hash.split('?')[1]).get('thread_id')===threadId&&document.querySelector('dialog[open]'),'Work card must open above current channel/thread');
 await click('关闭弹窗',document.querySelector('dialog[open]'));
 const direct=document.createElement('a');direct.href=`#/p/launch/work-items/W-104?tenant=${encodeURIComponent(tenant)}&scope=all`;direct.textContent='Native history work';document.querySelector('main').append(direct);direct.click();await wait();
 check(location.hash.split('?')[0]==='#/p/launch/work-items/W-104','Wrong work target');check(document.querySelector('.app-shell').dataset.area==='collab','Primary area inactive');check(JSON.stringify(selected())==='["任务"]','Work detail lacks unified task parent');
 history.back();await wait();
 const restored=new URLSearchParams(location.hash.split('?')[1]);
 check(location.hash.split('?')[0]==='#/chat'&&restored.get('channel_id')==='launch'&&restored.get('thread_id')===threadId&&!restored.has('execution_id'),'Native back lost exact channel/thread');
 check(document.querySelector('.app-shell').dataset.area==='chat'&&selected().some(label=>label.includes('新品发布')),'Back did not restore conversation/channel menu');
 if(innerWidth<900){document.querySelector('[aria-label="展开导航"]').click();await wait()}
 document.querySelector('.scope-select [role=combobox]').click();await wait();const project=[...document.querySelectorAll('[role=option]')].find(element=>element.textContent.includes('新品发布'));check(project,'Project option missing');project.click();await wait();
 if(document.querySelector('[aria-label="关闭导航"]')){document.querySelector('[aria-label="关闭导航"]').click();await wait()}
 await openCard();check(document.querySelector('dialog[open]')&&document.querySelector('.app-shell').dataset.area==='chat','Project-scoped work preview replaced conversation');await click('关闭弹窗',document.querySelector('dialog[open]'));
 await go('/p/launch/work-items/W-104?scope=launch');check(JSON.stringify(selected())==='["任务"]','Project detail lacks unified task parent');
 await go('/p/launch/artifacts/W-104?scope=launch');check(JSON.stringify(selected())==='["任务"]','Artifact detached from task menu');
 await go('/reports/RP-01');check(JSON.stringify(selected())==='["周期报告"]','Resource detail lacks parent selection');
 await go(`/chat?channel_id=launch&thread_id=${encodeURIComponent(threadId)}`);check(selected().some(label=>label.includes('新品发布')),'Thread query removes channel menu selection');
 document.querySelector('.connection').click();await wait();check(document.querySelector('.app-shell').dataset.area==='governance'&&JSON.stringify(selected())==='["健康与告警"]','Panel navigation left stale area');
 return {pass:true,width:innerWidth,executionId:execution?.id||null,threadId,checks:['real execution identity or honest empty registry','channel reply retains thread','work card modal preserves source','native browser back retains channel/thread/menu','unified task and artifact parent','resource detail parent','cross-area panel navigation']};
})()
