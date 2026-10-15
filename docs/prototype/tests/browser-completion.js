if (typeof window.qaCompleteValidation !== "function") throw Error("Preload tests/qa-browser-helpers.js before this suite.");
// Run on a fresh prototype in an isolated agent-browser session.
(async () => {
 const wait=()=>new Promise(r=>setTimeout(r,180)), check=(v,m)=>{if(!v)throw Error(m)};
 const click=async(label,scope=document)=>{const b=[...scope.querySelectorAll('button')].find(e=>e.textContent.trim()===label);check(b,'Missing '+label);b.click();await wait()};
 const fill=async(label,value)=>{const f=[...document.querySelectorAll('.field')].find(e=>e.querySelector('label')?.textContent.replace(' *','').trim()===label);const i=f?.querySelector('input:not([type=hidden]),textarea');check(i,'Missing field '+label);Object.getOwnPropertyDescriptor(i.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(i,value);i.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 location.hash='/p/launch/work-items/W-104?tab=review';await wait();
 await window.qaCompleteValidation('W-104'); await click('接受当前版本');
 check(state().work.find(w=>w.id==='W-104').accepted===false,'Missing receiving contract did not block acceptance');
 await click('设置交付方式');document.querySelector('[role=combobox][aria-label="完成与接收方式"]').click();await wait();await click('内部使用，无外部接收方',document.querySelector('[role=listbox]'));
 await fill('内部使用说明','仅本人内部使用，不向外部交付');await click('保存交付方式');await click('接受当前版本');
 let internal=state().work.find(w=>w.id==='W-104');check(internal.status==='已完成'&&internal.l4.status==='不适用'&&internal.l4.reason==='仅本人内部使用，不向外部交付','Internal completion not recorded');
 check(state().resources.deliveries.length===0,'Internal completion fabricated customer delivery');
 check(![...document.querySelectorAll('button')].some(e=>e.textContent.trim()==='批准正式交付'),'Internal completion still offers external delivery');
 await window.qaCompleteValidation('W-103');await click('接受当前版本');await click('批准正式交付');await click('确认',document.querySelector('dialog'));
 const delivery=state().resources.deliveries[0];let external=state().work.find(w=>w.id==='W-103');
 check(external.status==='待交接'&&external.l4.status==='待接收','Sending falsely completed external work');
 location.hash='/deliveries/'+delivery.id;await wait();await click('记录接收');await fill('依据与说明','指定版本全部接受');await click('确认操作',document.querySelector('dialog'));
 external=state().work.find(w=>w.id==='W-103');check(external.status==='已完成'&&external.l4.status==='已接受','Current accepted package did not complete work');
 return {pass:true,checks:['missing contract blocks completion','L4 reason saved','internal L2/L3 completion without receipt','external delivery waits','all current items accepted completes work']};
})()
