(async()=>{
 const wait=()=>new Promise(resolve=>setTimeout(resolve,200)),check=(ok,message)=>{if(!ok)throw Error(message)};
 const state=()=>JSON.parse(localStorage.getItem("collaboration-prototype-v1")),ids=state().testFreeTask,report=()=>state().resources.reports.find(r=>r.id===ids.reportId);
 const click=async(text,root=document)=>{const b=[...root.querySelectorAll("button")].find(b=>b.textContent.trim()===text);check(b&&!b.disabled,`Missing ${text}`);b.click();await wait()};
 const fill=async(label,value)=>{const input=document.querySelector(`[name="${label}"]`);check(input,`Missing ${label}`);Object.getOwnPropertyDescriptor(Object.getPrototypeOf(input),'value').set.call(input,value);input.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 location.hash=`/reports/${ids.reportId}`;await wait();await click("编辑");
 const originalPlan=JSON.stringify(state().resources.plans.find(p=>p.id===ids.planId));
 const dialog=document.querySelector('dialog[open]');await click(`加入完成事实 · 非交付任务 ${ids.itemId}`,dialog);
 check(dialog.textContent.includes("明确本地模拟：下游方案比较结论与来源已记录"),"FREE evidence text missing");
 check(dialog.textContent.includes("本地 Agent 模拟，由 Human H-lin 记录"),"FREE evidence hides simulated author/recorder");
 check(!dialog.textContent.includes('undefined')&&!dialog.textContent.includes('vnull'),"FREE fact masquerades as work/version");
 const selected=dialog.querySelector('[aria-label="完成计划关联 1"]');check(selected.textContent.includes(ids.itemId),"FREE insertion did not link original fixed Plan item");
 await click("保存",dialog);const item=report().content.completed[0];
 check(item.fact_ref.kind==="FREE_TASK_EVIDENCE"&&item.fact_ref.id===ids.itemId&&item.fact_ref.event_id===ids.eventId&&item.fact_ref.version===null,"Typed FREE fact changed during save");
 check(item.plan_item_id===ids.itemId&&!report().references.some(r=>r.kind==='work'&&r.id==null),"FREE fact created invented work reference");
 await click("模拟 Agent 上报");await fill("依据与说明","核对固定调研证据，明确为本地 Agent 模拟");await click("确认操作",document.querySelector('dialog[open]'));
 check(report().status==="已提交","FREE completion report submission failed");
 check(report().submission.factSnapshot.structuredFacts.some(f=>f.id===ids.eventId&&f.kind==="FREE_TASK_EVIDENCE"),"Submission did not freeze FREE evidence");
 check(report().submission.factSnapshot.totals.completed===0,"FREE task counted as accepted work");
 check(JSON.stringify(state().resources.plans.find(p=>p.id===ids.planId))===originalPlan,"Report changed Plan commitment/evidence");
 // Explicit fixture source loss; reload must retain the fixed evidence, not live fallback.
 const s=state();s.resources.plans=s.resources.plans.filter(p=>p.id!==ids.planId);localStorage.setItem("collaboration-prototype-v1",JSON.stringify(s));
 return {pass:true,width:innerWidth,checks:["genuine local Plan evidence commands","typed insertion and fixed Plan link","honest Agent simulation provenance","save and submit","frozen selected evidence","zero invented work completion","Plan unchanged"],sourceLossStored:true};
})()
