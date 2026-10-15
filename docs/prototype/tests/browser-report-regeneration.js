(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,180)),check=(ok,message)=>{if(!ok)throw Error(message)};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1')),ids=state().testRegeneration,report=()=>state().resources.reports.find(r=>r.id===ids.reportId);
 const click=async(text,root=document)=>{const b=[...root.querySelectorAll('button')].find(b=>b.textContent.trim()===text);check(b&&!b.disabled,`Missing ${text}`);b.click();await wait()};
 location.hash=`/reports/${ids.reportId}`;await wait();const before=structuredClone(report()),storage=localStorage.getItem('collaboration-prototype-v1');
 await click('编辑');const dialog=document.querySelector('dialog[open]'),section=dialog.querySelector('[data-report-regeneration]');check(section,'Missing regeneration controls');section.open=true;
 const targets=[...section.querySelectorAll('label')].map(label=>label.getBoundingClientRect());check(targets.length===3&&targets.every(rect=>rect.height>=44),'Chapter selection label touch targets smaller than 44px');
 await click('重填所选章节',section);check(section.querySelector('[role=alert]')?.textContent.includes('请选择有效'),'Empty selection not explained');
 section.querySelector('[name=regenerate-completed]').click();await click('重填所选章节',section);check(section.querySelector('[role=alert]')?.textContent.includes('未选章节'),'Partial section conflict not explained');
 check(localStorage.getItem('collaboration-prototype-v1')===storage,'Failed refill changed stored report');
 section.querySelector('[name=regenerate-uncompleted]').click();section.querySelector('[name=regenerate-next_plan]').click();await click('重填所选章节',section);
 check(!section.querySelector('[role=alert]'),'Valid refill failed');check(section.querySelector('[role=status]')?.textContent.includes('尚未保存'),'Form-only state not explicit');
 check(localStorage.getItem('collaboration-prototype-v1')===storage,'Refill auto-saved');
 check(dialog.textContent.includes('更新后的下期承诺'),'Latest next Plan not rendered');await click('保存',dialog);
 const saved=report();check(saved.content.completed.length===2&&saved.content.uncompleted.length===0,'Latest completion not refilled');
 check(saved.nextPlanBinding.revision===ids.nextRevision,'Latest next Plan revision not adopted');check(JSON.stringify(saved.content.retrospective)===JSON.stringify(before.content.retrospective),'Retrospective changed');
 check(saved.content.summary.narrative===before.content.summary.narrative,'Manual narrative changed');check(saved.history.some(h=>JSON.stringify(h.content)===JSON.stringify(before.content)),'Original report history missing');
 check(saved.status==='草稿'&&!saved.submission&&!saved.approval,'Refill implied acceptance');check(document.documentElement.scrollWidth<=innerWidth+2,'Regeneration overflow');
 return {pass:true,width:innerWidth,checks:['empty and partial selection errors','failure atomic','explicit form-only latest-fact refill','real latest next Plan','manual retrospective and narrative preserved','original history','no automatic acceptance','viewport fit']};
})()
