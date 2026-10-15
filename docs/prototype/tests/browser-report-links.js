// Preseeded typed drafts with actual current/next Plans; UI tests fixed child selection and histories.
(async()=>{
 const pause=()=>new Promise(resolve=>setTimeout(resolve,180));
 const check=(value,message)=>{if(!value)throw Error(message)};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const button=(label,root=document)=>[...root.querySelectorAll('button')].find(el=>el.textContent.trim()===label);
 const click=async(label,root=document)=>{const el=button(label,root);check(el&&!el.disabled,'Unavailable '+label);el.click();await pause()};
 const fill=async(label,value)=>{const el=document.querySelector(`dialog [name="${label}"]`);check(el,'Missing '+label);Object.getOwnPropertyDescriptor(el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await pause()};
 const choose=async(label,text)=>{document.querySelector(`[role=combobox][aria-label="${label}"]`).click();await pause();const option=[...document.querySelectorAll('[role=option]')].find(el=>el.textContent.trim()===text);check(option,'Missing option '+text);option.click();await pause()};
 const generate=async(cadence,date,subject)=>{location.hash='/reports';await pause();await click('生成 / 打开周期报告');if(subject)await choose('报告归属',subject);await choose('报告周期',cadence);await fill('周期内日期',date);await click('生成 / 打开',document.querySelector('dialog'));return location.hash.split('/').at(-1)};
 const submit=async label=>{await click(label);await fill('依据与说明','核对固定报告及周期事实，保留资料限制');await click('确认操作',document.querySelector('dialog'))};
 const child=await generate('周报','2000-01-31');await submit('本人确认并上报');
 const fixed=structuredClone(state().resources.reports.find(r=>r.id===child).submission);
 const agentName=state().agents.find(a=>a.id==='atlas').name;
 const foreign=await generate('周报','2000-01-31',`${agentName} · Agent`);await submit('模拟 Agent 上报');
 const parent=await generate('月报','2000-02-01');await click('编辑');
 check(document.querySelector(`dialog input[aria-label="引用 ${child}"]`),'Cross-month same-member submitted week unavailable');
 check(!document.querySelector(`dialog input[aria-label="引用 ${foreign}"]`),'Foreign personal report selectable');
 check(!document.querySelector('dialog input[aria-label="引用 RP-02"]'),'Unfixed legacy report selectable');
 document.querySelector(`dialog input[aria-label="引用 ${child}"]`).click();await pause();await click('保存',document.querySelector('dialog'));await submit('本人确认并上报');
 const parentFixed=structuredClone(state().resources.reports.find(r=>r.id===parent).submission);
 check(parentFixed.content.references.some(ref=>ref.id===child),'Fixed parent lost child reference');
 check(parentFixed.factSnapshot.sources.some(source=>source.id===child),'Child absent from fixed evidence');
 location.hash=`/reports/${child}`;await pause();await click('编辑');await fill('人工校准正文','新的人工判断，不覆盖原固定正文');await click('保存',document.querySelector('dialog'));
 check(state().resources.reports.find(r=>r.id===child).history.some(h=>JSON.stringify(h.submission)===JSON.stringify(fixed)),'Child edit erased submitted history');
 location.hash=`/reports/${parent}`;await pause();
 check(JSON.stringify(state().resources.reports.find(r=>r.id===parent).submission)===JSON.stringify(parentFixed),'Child edit rewrote fixed parent');
 await click('编辑');check(!document.querySelector(`dialog input[aria-label="引用 ${child}"]`),'Returned-to-draft child still eligible');
 check(button(`移除无效引用 ${child}`,document.querySelector('dialog')),'Invalidated child lacks removal action');
 await click('保存',document.querySelector('dialog'));check(document.querySelector('dialog').textContent.includes('版本已变化'),'Stale child save not rejected with explanation');
 check(JSON.stringify(state().resources.reports.find(r=>r.id===parent).submission)===JSON.stringify(parentFixed),'Rejected save mutated fixed parent');
 await click(`移除无效引用 ${child}`,document.querySelector('dialog'));await click('保存',document.querySelector('dialog'));
 check(state().resources.reports.find(r=>r.id===parent).references.length===0,'Invalid child could not be removed');
 check(document.documentElement.scrollWidth<=innerWidth+1,'Report links layout overflow');
 return {pass:true,checks:['cross-month eligible source','foreign and unfixed source excluded','fixed child and parent submissions','child edit preserves history','parent snapshot preserved','stale reference rejected','invalid source removable','responsive layout']};
})()
