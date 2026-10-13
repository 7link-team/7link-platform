(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,180)),check=(ok,msg)=>{if(!ok)throw Error(msg)},go=async route=>{location.hash=route;await wait()};
 const click=async(label,root=document)=>{const el=[...root.querySelectorAll('a,button')].find(el=>el.textContent.trim()===label);check(el,'Missing '+label);el.click();await wait()};
 const selected=()=>[...document.querySelectorAll('[aria-label="区域导航"] .active')].map(el=>el.textContent.trim());
 const source='/chat?execution_id=W-104&thread_id=m-work-navigation';
 await go(source);await click('查看工作');check(location.hash==='#/p/launch/work-items/W-104','Wrong work target');check(document.querySelector('.app-shell').dataset.area==='collab','Primary area inactive');check(JSON.stringify(selected())==='["跨实例工作"]','All-project work detail lacks selected parent');
 history.back();await wait();check(location.hash==='#'+source,'Conversation/thread return context lost');check(document.querySelector('.app-shell').dataset.area==='chat','Back did not restore conversation area');
 if(innerWidth<900){document.querySelector('[aria-label="展开导航"]').click();await wait()}
 document.querySelector('.scope-select [role=combobox]').click();await wait();const project=[...document.querySelectorAll('[role=option]')].find(el=>el.textContent.includes('新品发布'));check(project,'Project option missing');project.click();await wait();
 if(document.querySelector('[aria-label="关闭导航"]')){document.querySelector('[aria-label="关闭导航"]').click();await wait()}
 await click('查看工作');check(JSON.stringify(selected())==='["项目工作"]','Project detail lacks selected project work parent');
 await go('/p/launch/artifacts/W-104');check(JSON.stringify(selected())==='["项目工作"]','Artifact detached from work menu');
 await go('/reports/RP-01');check(JSON.stringify(selected())==='["周期报告"]','Resource detail lacks parent selection');
 await go('/chat?channel_id=launch&thread_id=m-work-navigation');check(selected().some(label=>label.includes('新品发布')),'Thread query removes channel menu selection');
 document.querySelector('.connection').click();await wait();check(document.querySelector('.app-shell').dataset.area==='governance'&&JSON.stringify(selected())==='["健康与告警"]','Panel navigation left stale area');
 return {pass:true,width:innerWidth,checks:['all-project detail parent','primary area','browser back retains execution/thread','project detail parent','artifact parent','resource detail parent','thread channel parent','cross-area panel navigation']};
})()
