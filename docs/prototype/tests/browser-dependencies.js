if (typeof window.qaCompleteValidation !== "function") throw Error("Preload tests/qa-browser-helpers.js before this suite.");
// Run on a fresh prototype in an isolated agent-browser session.
(async () => {
 const wait=()=>new Promise(r=>setTimeout(r,180)), check=(v,m)=>{if(!v)throw Error(m)};
 const click=async(label,scope=document)=>{const b=[...scope.querySelectorAll('button')].find(e=>e.textContent.trim()===label);check(b,'Missing '+label);b.click();await wait()};
 const fill=async(label,value)=>{const field=[...document.querySelectorAll('.field')].find(e=>e.querySelector('label')?.textContent.replace(' *','').trim()===label);const el=field?.querySelector('input:not([type=hidden]),textarea');check(el,'Missing '+label);Object.getOwnPropertyDescriptor(el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const go=async(id,tab='plan')=>{location.hash=`/p/${id==='W-201'?'film':'launch'}/work-items/${id}?tab=${tab}`;await wait()};
 const configure=async(id,source)=>{await go(id);await click('添加输入引用');document.querySelector('[role=combobox][aria-label="输入 1 工作"]').click();await wait();const option=[...document.querySelectorAll('[role=listbox] button')].find(e=>e.textContent.trim().startsWith(source+' ·'));check(option,'Missing source '+source);option.click();await wait();await fill('输入确认或采用说明','显式登记固定输入');document.querySelector('[role=combobox][aria-label="输入 1 指定 Human 接收人"]').click();await wait();await click('林予',document.querySelector('[role=listbox]'));await click('准备输入 1 固定交接包');await click('接收固定包并采用输入');};
 const submit=async(id)=>{const w=state().work.find(w=>w.id===id);if(state().resources.tests.some(t=>t.work===id&&t.version===`v${w.version}`&&t.status==='失败')){await go(id,'review');await click('提交内部修复');await fill('理由与交接说明','修复当前版本已记录的失败');await click('确认',document.querySelector('dialog'));}else{await go(id,'brief');await click('提交新成果版本');}await window.qaCompleteValidation(id);};
 await window.qaCompleteValidation('W-103');await window.qaConfigureRework('W-103');
 await go('W-104');document.querySelector('[role=combobox][aria-label="完成与接收方式"]').click();await wait();await click('内部使用，无外部接收方',document.querySelector('[role=listbox]'));await fill('L4 不适用理由','内部环节成果');await click('确认接收契约');
 await configure('W-104','W-103');await submit('W-104');await configure('W-201','W-104');await submit('W-201');
 const unaffected=JSON.stringify(state().work.find(w=>w.id==='W-301'));
 await go('W-103','review');await click('退回修订');await fill('成果位置','主视觉背景');await fill('期望结果','浅色背景');await fill('实际结果','背景过深');await fill('理由与交接说明','修订被引用输入');await click('确认',document.querySelector('dialog'));
 for(const id of ['W-104','W-201']){const w=state().work.find(w=>w.id===id);check(w.status==='等待'&&w.dependencyBlocks.length===1,'Missing propagated block '+id)}
 check(JSON.stringify(state().work.find(w=>w.id==='W-301'))===unaffected,'Unrelated work changed');
 await go('W-104','review');check(document.querySelector('main').textContent.includes('输入失效'),'No visible blocked state');await click('开始独立验证');check(state().work.find(w=>w.id==='W-104').qa===false,'Blocked work bypassed verification');
 await submit('W-103');await go('W-104');await fill('输入 1 固定版本','3');await fill('输入确认或采用说明','采用已验证的新主视觉');document.querySelector('[role=combobox][aria-label="输入 1 指定 Human 接收人"]').click();await wait();await click('林予',document.querySelector('[role=listbox]'));await click('准备输入 1 固定交接包');await click('接收新固定包并重新验收');
 let b=state().work.find(w=>w.id==='W-104');check(b.status==='进行中'&&!b.qa&&!b.accepted&&b.inputs[0].version===3,'Adoption incorrectly inherited acceptance');
 check(state().work.find(w=>w.id==='W-201').dependencyBlocks.length===1,'Transitive work cleared before new output');
 await submit('W-104');await go('W-201');await fill('输入 1 固定版本','5');await fill('输入确认或采用说明','逐级采用新输出');document.querySelector('[role=combobox][aria-label="输入 1 指定 Human 接收人"]').click();await wait();await click('林予',document.querySelector('[role=listbox]'));await click('准备输入 1 固定交接包');await click('接收新固定包并重新验收');
 const c=state().work.find(w=>w.id==='W-201');check(c.status==='进行中'&&!c.qa&&c.dependencyBlocks.length===0,'Downstream adoption failed');
 return {pass:true,checks:['explicit input version editing','direct and transitive pause','unrelated branch preserved','blocked verification rejected','explicit verified adoption','no inherited acceptance','stepwise downstream recovery']};
})()
