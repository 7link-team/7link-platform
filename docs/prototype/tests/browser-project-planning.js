// Requirements and iterations share existing tasks; neither action silently executes them.
(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,200)),check=(v,m)=>{if(!v)throw Error(m)};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const modal=()=>[...document.querySelectorAll('dialog[open]')].at(-1);
 const click=async(label,root=document)=>{const e=[...root.querySelectorAll('button,a')].find(e=>e.textContent.trim()===label||e.getAttribute('aria-label')===label);check(e,'Missing '+label);check(!e.disabled,'Disabled '+label);e.click();await wait()};
 const fill=async(label,value)=>{const el=modal()?.querySelector(`[name="${label}"]`);check(el,'Missing '+label);Object.getOwnPropertyDescriptor(el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const choose=async(label,value)=>{const el=modal()?.querySelector(`[role=combobox][aria-label="${label}"]`);check(el&&!el.disabled,'Missing choice '+label);el.click();await wait();await click(value,document.querySelector('[role=listbox]'))};
 const close=async()=>{await click('关闭弹窗',modal());if(modal()?.textContent.includes('放弃未保存的修改'))await click('放弃修改',modal())};
 location.hash='/requirements?tenant=studio&scope=launch';await wait();await click('新建需求');
 check(document.querySelector('dialog [aria-label="项目"]').disabled,'Requirement exposes a second scope selector');
 await fill('名称','跨行业交付约定');await choose('Human负责人','林予');await fill('说明','活动文案、视频与页面均须交付可核验版本。');await fill('验收标准','文案有出处，视频可播放，页面兼容手机。');await fill('变更理由','确认本期交付范围');await click('保存修订',modal());
 let requirement=state().resources.requirements.find(r=>r.name==='跨行业交付约定');check(requirement?.projectId==='launch'&&requirement.status==='草稿','Requirement save failed');
 check(location.hash.split('?')[0]==='#/requirements'&&decodeURIComponent(location.hash).includes('requirements.detail'),'Requirement detail escaped list or has no deep link');
 const detail=modal();check(detail.querySelector('.modal-head a[target="_blank"]')||detail.querySelector('a[target="_blank"]'),'Requirement has no explicit new-tab detail');
 await click('确认需求',detail);await fill('变更理由','负责人核对交付约定');await click('确认需求',modal());check(state().resources.requirements.find(r=>r.id===requirement.id).status==='已确认','Requirement confirmation missing');
 await click('修订需求',modal());await fill('验收标准','文案有出处，视频可播放，页面兼容手机并提供PDF。');await fill('变更理由','需求变更已讨论确认');await click('保存修订',modal());
 requirement=state().resources.requirements.find(r=>r.id===requirement.id);check(requirement.status==='草稿'&&requirement.history.some(h=>h.before?.status==='已确认'),'Revision lost previous confirmation');check(!modal().textContent.includes('"projectId"'),'History exposes raw storage fields');await close();
 const originalExecutions=JSON.stringify(state().executions);
 location.hash='/iterations?tenant=studio&scope=launch';await wait();await click('新建迭代');await fill('名称','本期内容交付');await choose('Human负责人','林予');await fill('说明','围绕当前项目交付推进');await fill('开始日期','2026-10-14');await fill('结束日期','2026-10-21');await fill('准入条件','范围和责任人已确认');await fill('准出条件','范围内任务已通过验收');await fill('变更理由','安排本期交付窗口');
 const linked=[...document.querySelectorAll('dialog:last-of-type .check-label')].find(e=>e.textContent.includes('W-104'));check(linked,'Iteration has no existing-task association');linked.querySelector('input').click();await wait();await click('保存修订',modal());
 let iteration=state().resources.iterations.find(r=>r.name==='本期内容交付');check(iteration?.workIds.includes('W-104'),'Iteration lost task scope');
 await click('开始迭代',modal());await fill('条件核对结果','固定任务范围和负责人已核对');await fill('变更理由','启动本期协作');await click('开始迭代',modal());check(state().resources.iterations.find(r=>r.id===iteration.id).status==='进行中','Iteration did not start');
 await click('完成迭代',modal());await fill('条件核对结果','申请检查准出');await fill('变更理由','请求完成检查');await click('完成迭代',modal());
 check(modal()?.querySelector('[role=alert]')?.textContent.includes('未完成'),'Iteration falsely completed pending tasks');check(state().resources.iterations.find(r=>r.id===iteration.id).status==='进行中','Failed completion changed state');await close();
 check(JSON.stringify(state().executions)===originalExecutions,'Planning silently created or started execution');await click('归档迭代',modal());await fill('变更理由','本期改用新窗口，保留历史');await click('归档记录',modal());check(state().resources.iterations.find(r=>r.id===iteration.id).status==='已归档','Archive lost history');await close();
 check(document.documentElement.scrollWidth<=innerWidth+2,'Planning overflows viewport');
 return {pass:true,checks:['unique project picker','requirement confirmation and revision history','iteration fixed existing tasks','entry evidence','pending tasks block completion atomically','no silent execution','archive history'],width:innerWidth};
})()
