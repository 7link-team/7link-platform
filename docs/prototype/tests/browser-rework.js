(async () => {
 const wait=()=>new Promise(resolve=>setTimeout(resolve,140)), check=(value,message)=>{if(!value)throw Error(message)};
 const go=async(path)=>{location.hash=path;await wait()};
 const click=async(label,scope=document)=>{const target=[...scope.querySelectorAll('button,a')].find(el=>el.textContent.trim()===label);check(target,'Missing '+label);check(!target.disabled,'Disabled '+label);target.click();await wait()};
 const fill=async(label,value,scope=document)=>{const node=[...scope.querySelectorAll('.field')].find(el=>el.querySelector('label')?.textContent.replace(' *','').trim()===label)?.querySelector('input:not([type=hidden]),textarea');check(node,'Missing '+label);Object.getOwnPropertyDescriptor(node.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(node,value);node.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const choose=async(label,text)=>{document.querySelector(`[role=combobox][aria-label="${label}"]`).click();await wait();const option=[...document.querySelectorAll('[role=option]')].find(el=>el.textContent.trim()===text||el.textContent.trim().startsWith(text+' ·'));check(option,'Missing option '+text);option.click();await wait()};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1')), work=()=>state().work.find(w=>w.id==='W-104'), main=()=>document.querySelector('main');
 await go('/p/launch/work-items/W-104?tab=review');
 check(main().textContent.includes('0 / 5 轮')&&main().textContent.includes('0 次 · 3 次升级'),'Software default counters missing');
 const config=[...main().querySelectorAll('summary')].find(el=>el.textContent==='配置本工作返工上限');config.click();await wait();
 await fill('内部修复上限','1');await fill('正式返工升级阈值','2');await fill('返工处理依据','明确本工作可用预算与轮次');await click('保存返工上限');
 await click('开始独立验证');const plan=state().resources.testplans.find(item=>item.id===work().qaPlanId), batch=plan.batches.find(item=>item.id===plan.currentBatchId);
 await click(`记录结果 · ${batch.caseRefs[0].caseId}`);await choose('执行结论','失败');await choose('验证来源','平台独立验证');await choose('运行主体','Verifier');
 await fill('实际结果 / 跳过或阻塞原因','固定批次仍未满足原条款');await fill('证据引用',`mock-rework/${batch.id}`);await fill('工具版本（手测填写不适用）','mock-browser/1');await click('保存逐项结果');
 check((work().rework?.internalTotal||0)===0,'Unfinished failure counted as a round');
 await go('/p/launch/work-items/W-104?tab=review');check(main().textContent.includes('结束失败验证批次'),'No actionable pending-batch notice');check([...main().querySelectorAll('button')].find(el=>el.textContent.trim()==='提交内部修复').disabled,'Unfinished failed batch allowed repair');
 await go(`/testplans/${plan.id}`);await click('形成固定测试报告');await go('/p/launch/work-items/W-104?tab=review');
 check(work().rework.internal===1&&work().rework.internalTotal===1&&work().rework.hold.kind==='internal'&&work().status==='等待','Internal threshold did not stop');
 check([...main().querySelectorAll('button')].find(el=>el.textContent.trim()==='开始独立验证').disabled,'Stopped work can start verification');
 await fill('返工处理依据','检查修复结果后继续一次有限窗口');await fill('本次额外内部修复轮数','1');await click('明确继续当前返工');
 check(!work().rework.hold&&work().rework.internal===1&&work().rework.internalCeiling===2,'Continuation cleared accounting or lacked boundary');
 await click('提交内部修复');await fill('理由与交接说明','已修复本轮全部失败项',document.querySelector('dialog,[role=dialog]'));await click('确认',document.querySelector('dialog,[role=dialog]'));check(work().rework.internalTotal===1,'Repair submission double-counted round');
 for(let count=1;count<=2;count++){
   await click('退回修订');const dialog=document.querySelector('dialog,[role=dialog]');
   for(const [label,value] of [['成果位置','首页'],['期望结果','满足原条款'],['实际结果','仍有质量问题'],['理由与交接说明',`正式返工决定${count}`]])await fill(label,value,dialog);
   await click('确认',dialog);check(work().rework.formalTotal===count&&work().rework.internal===0&&work().rework.internalTotal===1,'Formal/internal counters mixed');
   if(count===1){await go('/p/launch/work-items/W-104');await click('提交新成果版本');await go('/p/launch/work-items/W-104?tab=review');}
 }
 check(work().rework.hold.kind==='formal','Formal threshold did not stop');
 await fill('返工处理依据','Human批准当前一次返工');await click('明确继续当前返工');check(work().rework.formalTotal===2&&!work().rework.hold,'Formal continuation reset business total');
 await click('Human 接管');await fill('理由与交接说明','Human核对停止中的部分成果',document.querySelector('dialog,[role=dialog]'));await click('确认',document.querySelector('dialog,[role=dialog]'));
 await click('取消工作');await fill('理由与交接说明','取消并保留修复历史',document.querySelector('dialog,[role=dialog]'));await click('确认',document.querySelector('dialog,[role=dialog]'));
 check(work().status==='已取消'&&work().rework.formalTotal===2&&work().rework.decisions.at(-1).action==='cancel','Cancellation erased history');
 check(document.documentElement.scrollWidth<=innerWidth,'Rework controls overflow viewport');
 await go('/p/launch/work-items/W-103');check(main().textContent.includes('未配置'),'Unknown profession inherited software limits');
 return {pass:true,width:innerWidth,checks:['software defaults','explicit configuration','one round per closed failed batch','unfinished batch blocks repair','repair does not double count','internal threshold stop','finite Human continuation','formal reset only declaration chain','formal threshold stop','business total retained','takeover and cancellation history','unknown profession explicit','no horizontal overflow']};
})()
