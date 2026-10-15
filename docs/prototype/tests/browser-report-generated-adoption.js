(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,180)),check=(ok,message)=>{if(!ok)throw Error(message)};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1')),ids=state().testGeneratedContent,report=()=>state().resources.reports.find(r=>r.id===ids.reportId);
 const click=async(text,root=document)=>{const b=[...root.querySelectorAll('button')].find(b=>b.textContent.trim()===text);check(b&&!b.disabled,`Missing ${text}`);b.click();await wait()};
 location.hash=`/reports/${ids.reportId}`;await wait();const original=structuredClone(report()),beforeStorage=localStorage.getItem('collaboration-prototype-v1');
 check(!original.content.completed.length&&original.suggestedContent.completed.length===1,'Refresh replaced author content or failed to produce suggestion');
 await click('编辑');const dialog=document.querySelector('dialog[open]');await click('采用本期条目建议',dialog);
 check(localStorage.getItem('collaboration-prototype-v1')===beforeStorage,'Suggestion adoption auto-saved');
 check(dialog.textContent.includes('固定计划完成证据'),'Adopted evidence not rendered');await click('保存',dialog);
 const saved=report();check(saved.content.completed.length===1&&saved.content.completed[0].fact_ref.event_id===ids.eventId,'Adopted typed source missing');
 check(JSON.stringify(saved.content.uncompleted)===JSON.stringify(original.content.uncompleted),'Adoption changed manual reason or confirmation');check(JSON.stringify(saved.content.retrospective)===JSON.stringify(original.content.retrospective),'Adoption changed manual retrospective');
 check(saved.content.summary.narrative===original.content.summary.narrative,'Adoption replaced author narrative');
 check(document.documentElement.scrollWidth<=innerWidth+2,'Suggestion adoption overflow');
 return {pass:true,width:innerWidth,checks:['refresh suggestion only','explicit form-only adoption','typed source persisted','manual reasons/confirmation/retro/narrative preserved','viewport fit']};
})()
