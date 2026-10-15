// Genuine local operation fixture; no production sender, authority or publishing claimed.
(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,170)),check=(v,m)=>{if(!v)throw Error(m)},state=()=>JSON.parse(localStorage.getItem("collaboration-prototype-v1"));
 const click=async(text,root=document)=>{const b=[...root.querySelectorAll("button,a")].find(e=>e.textContent.trim()===text);check(b&&!b.disabled,`Missing enabled ${text}`);b.click();await wait();};
 const fill=async(label,value)=>{const e=document.querySelector(`[name="${label}"]`);check(e,`Missing ${label}`);Object.getOwnPropertyDescriptor(e.tagName==="TEXTAREA"?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,"value").set.call(e,value);e.dispatchEvent(new Event("input",{bubbles:true}));await wait();};
 const rid=state().testOperationFactsReportId,report=()=>state().resources.reports.find(r=>r.id===rid);location.hash=`/reports/${rid}`;await wait();await click("编辑");
 const saved=JSON.stringify(report()),picker=()=>[...document.querySelectorAll('dialog button')].filter(b=>b.textContent.trim().startsWith('加入完成事实 · '));check(picker().length===3,'Missing three actual operation facts');
 while(picker().length){picker()[0].click();await wait();}check(JSON.stringify(report())===saved,'Picker saved report without author consent');
 const evidence=[...document.querySelectorAll('dialog [data-report-operation-evidence]')];check(evidence.length===3&&evidence.every(e=>e.textContent.includes('明确本地模拟')&&e.textContent.includes('H-lin')),'Operation provenance missing in editor');
 check([...document.querySelectorAll('[role=combobox][aria-label^="完成计划关联"]')].length===3,'Missing custom Plan selectors');
 await click("保存",document.querySelector('dialog'));check(report().content.completed.length===3&&report().content.completed.every(i=>!i.plan_item_id),'Typed events were omitted or completed Plan items');check(report().references.length===0,'Operation selection created executor Work references');
 await click("本人确认并上报");await fill("依据与说明","本人核对本地裁决与发送事实，未声明执行者成果或真实发布");await click("确认操作",document.querySelector('dialog'));
 check(report().status==='已提交','Actual Human submission failed');check(report().submission.factSnapshot.structuredFacts.length===3&&report().submission.factSnapshot.totals.completed===0,'Typed fixed facts inflated accepted Work');
 check([...document.querySelectorAll('main [data-report-operation-evidence]')].length===3,'Fixed operation reader missing');check(document.documentElement.scrollWidth<=innerWidth+2,'Operation UI overflow');
 const s=state();s.work=s.work.filter(w=>w.id!=='W-103');s.resources.deliveries=s.resources.deliveries.filter(d=>d.work!=='W-103');localStorage.setItem('collaboration-prototype-v1',JSON.stringify(s));
 return {pass:true,width:innerWidth,checks:['typed manual picker','form-only additions','fixed actor and local provenance','no Plan completion','no executor Work reference','real Human submission','fixed structured facts','zero accepted Work inflation','viewport fit']};
})()
