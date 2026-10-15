(async()=>{
 const checks=[],check=(ok,name)=>{checks.push({name,pass:Boolean(ok)});if(!ok)throw Error(name);},wait=()=>new Promise(r=>setTimeout(r,250));
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1')),ids=state().testExecutionMaintenance;
 const go=async id=>{location.hash='/chat?execution_id='+encodeURIComponent(id);await wait();};
 const control=label=>[...document.querySelectorAll('button')].find(el=>el.textContent.trim()===label);
 await go(ids.observer);
 check(['模拟Runtime输入请求','模拟暂停','模拟轮次完成并暂停','正式关联工作项','模拟时钟推进至到期','模拟决定送达'].every(label=>!control(label)),'Observer has no actionable scenario maintenance controls');
 check(control('提交结构化输入')?.disabled&&control('拒绝此请求')?.disabled,'Observer does not acquire response authority');
 check(state().executions.find(e=>e.id===ids.observer).interactions[0].state==='PENDING','Observer view preserves request state');
 await go(ids.own);
 for(let n=0;n<12&&state().executions.find(e=>e.id===ids.own).interactions[0].state==='PENDING';n++)await wait();
 const own=state().executions.find(e=>e.id===ids.own),i=own.interactions[0];
 check(i.state==='EXPIRED'&&i.expiryMode==='OBSERVED_DEADLINE','Own ADHOC records actual elapsed deadline despite absent response privilege');
 check(document.querySelector('.composer textarea')&&!document.querySelector('.composer textarea').disabled,'Expiry unblocks own execution input');
 check(control('模拟Runtime输入请求')&&control('模拟暂停'),'Own ADHOC retains scenario maintenance actions');
 check(!i.responderId&&!i.responseAction,'Maintenance never invents Human approval');
 await go(ids.legacy);
 check(document.querySelector('main').textContent.includes('此历史执行未记录派发纪元或交互能力'),'Legacy capability notice remains after epoch is present');
 check(!control('模拟Runtime输入请求')&&!control('模拟工具授权请求'),'Legacy capabilities are not invented');
 check(document.documentElement.scrollWidth<=innerWidth,'Maintenance views fit viewport');
 return {pass:true,width:innerWidth,checks};
})()
