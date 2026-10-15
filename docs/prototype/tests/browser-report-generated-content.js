(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,180)),check=(ok,message)=>{if(!ok)throw Error(message)};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1')),ids=state().testGeneratedContent,report=()=>state().resources.reports.find(r=>r.id===ids.reportId);
 const click=async(text,root=document)=>{const b=[...root.querySelectorAll('button')].find(b=>b.textContent.trim()===text);check(b&&!b.disabled,`Missing ${text}`);b.click();await wait()};
 const fill=async(name,value)=>{const el=document.querySelector(`[name="${name}"]`);check(el,`Missing ${name}`);Object.getOwnPropertyDescriptor(el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 location.hash=`/reports/${ids.reportId}`;await wait();
 check(report().content.completed.length===1&&report().content.uncompleted.length===1,'New report did not auto-fill genuine source outcomes');
 check(report().content.summary.accepted_work_items===0,'Automatic FREE completion invented accepted work');
 const beforePlan=JSON.stringify(state().resources.plans.find(p=>p.id===ids.planId));await click('编辑');const dialog=document.querySelector('dialog[open]'),ack=dialog.querySelector('[aria-label="确认原因与处置 1"]');
 check(ack&&!ack.checked,'Auto-generated reason pretends author confirmation');check(dialog.textContent.includes('固定计划完成证据'),'Typed automatic evidence absent');ack.click();await wait();
 await fill('做得好的方面','真实调研证据可以追溯');await fill('问题与影响','部分调研仍待下游资料');await fill('根因分析','资料尚未完整到位');await fill('改进建议','下周期提前预约资料');await click('保存',dialog);
 check(report().content.uncompleted[0].detail_confirmation&&report().content.completed[0].fact_ref.event_id===ids.eventId,'Auto chapters failed save');
 await click('模拟 Agent 上报');await fill('依据与说明','明确本地模拟：核对自动预填条目及作者解释');await click('确认操作',document.querySelector('dialog[open]'));
 check(report().status==='已提交','Generated report could not submit after actual author review');check(report().submission.factSnapshot.structuredFacts.some(f=>f.id===ids.eventId),'Generated evidence not frozen');
 check(JSON.stringify(state().resources.plans.find(p=>p.id===ids.planId))===beforePlan,'Generation/confirmation changed Plan');
 check(document.documentElement.scrollWidth<=innerWidth+2,'Auto report overflow');
 return {pass:true,width:innerWidth,checks:['genuine source first-fill','zero invented work acceptance','reason initially unconfirmed','actual author reason and retro review','save and submit','frozen typed evidence','Plan unchanged','viewport fit']};
})()
