(async()=>{
 const wait=()=>new Promise(resolve=>setTimeout(resolve,180)),check=(ok,message)=>{if(!ok)throw Error(message)};
 const state=()=>JSON.parse(localStorage.getItem("collaboration-prototype-v1")),ids=state().testPlanLink;
 const click=async(text,root=document)=>{const button=[...root.querySelectorAll("button")].find(b=>b.textContent.trim()===text);check(button&&!button.disabled,`Missing ${text}`);button.click();await wait()};
 const choose=async(label,text)=>{const trigger=document.querySelector(`[role="combobox"][aria-label="${label}"]`);check(trigger,`Missing ${label}`);trigger.click();await wait();const options=[...document.querySelectorAll('[role="listbox"] [role="option"]')];const option=options.find(o=>o.textContent.includes(text));check(option&&!option.disabled,`Missing selectable ${text}`);option.click();await wait();check(document.activeElement===trigger,"Plan selection failed to restore trigger focus");return options.map(o=>o.textContent)};
 location.hash=`/reports/${ids.reportId}`;await wait();await click("编辑");
 const beforePlan=JSON.stringify(state().resources.plans.find(p=>p.id===ids.planId));
 const factButton=[...document.querySelectorAll('dialog[open] button')].find(b=>b.textContent.trim().startsWith("加入完成事实 · W-103"));check(factButton,"Accepted test event unavailable");factButton.click();await wait();
 const options=await choose("完成计划关联 1","LINK-DONE");
 check(!options.some(text=>text.includes("LINK-PENDING")||text.includes("调研仍待完成")),"Completed selector allows unrelated work/free task");
 await choose("未完成计划关联 1","LINK-PENDING");
 const dialog=document.querySelector('dialog[open]'),ack=dialog.querySelector('[aria-label="确认原因与处置 1"]');check(!ack.checked,"Relinking retained previous confirmation");ack.click();await wait();await click("保存",dialog);
 const report=state().resources.reports.find(r=>r.id===ids.reportId),done=report.content.completed[0],pending=report.content.uncompleted[0];
 check(done.plan_item_id==="LINK-DONE"&&done.fact_ref.id==="W-103"&&done.fact_ref.event_id==="LINK-SYNTHETIC-ACCEPTED","Completed association altered fixed accepted event");
 check(pending.plan_item_id==="LINK-PENDING"&&pending.work_item_id==="W-104"&&pending.detail_confirmation,"Incomplete work association/author confirmation not stored");
 check(JSON.stringify(state().resources.plans.find(p=>p.id===ids.planId))===beforePlan,"Report association edited or confirmed the Plan");
 const sources=[...document.querySelectorAll('[data-report-content] [data-report-plan-source]')].map(p=>p.textContent);
 check(sources.some(text=>text.includes("LINK-DONE")&&text.includes("W-103"))&&sources.some(text=>text.includes("LINK-PENDING")&&text.includes("W-104")),"Reader omits actual fixed correspondence");
 check(!document.querySelector("main").textContent.includes("计划项不存在"),"Valid linked report failed Plan check");
 check(document.documentElement.scrollWidth<=innerWidth+2,"Plan correspondence overflow");
 return {pass:true,width:innerWidth,checks:["explicit synthetic accepted-event fixture","only matching fixed work offered","selection focus restored","incomplete link confirmation invalidated","real save round-trip","accepted event unchanged","Plan unchanged","fixed source reader","viewport fit"]};
})()
