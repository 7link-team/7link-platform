// Execute against a fresh prototype session; supports desktop and 375px widths.
(async () => {
 const wait=()=>new Promise(resolve=>setTimeout(resolve,160)), check=(value,message)=>{if(!value)throw Error(message)};
 const go=async(path)=>{location.hash=path;await wait()};
 const click=async(label,scope=document)=>{const target=[...scope.querySelectorAll('button,a')].find(el=>el.textContent.trim()===label);check(target,'Missing '+label);target.click();await wait()};
 const fill=async(label,value)=>{const node=[...document.querySelectorAll('.field')].find(el=>el.querySelector('label')?.textContent.replace(' *','').trim()===label)?.querySelector('input:not([type=hidden]),textarea');check(node,'Missing '+label);Object.getOwnPropertyDescriptor(node.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(node,value);node.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const main=()=>document.querySelector('main');
 await go('/worklogs');check(main().textContent.includes('历史投入证据'),'Legacy evidence directory absent');check(!main().querySelector('input,textarea,[role=combobox]'),'Independent creation remains');
 check(![...document.querySelectorAll('.area-nav button,.area-nav a')].some(el=>/工作量|工时/.test(el.textContent)),'Independent effort menu remains');
 await go('/p/launch/work-items/W-104?tab=analysis');check(main().textContent.includes('投入与运行分析'),'Task analysis missing');
 await click('校准本人实际投入');await fill('投入日期','2026-10-14');await fill('投入时区','Asia/Singapore');await fill('投入类别','验收');await fill('实际参与投入（小时）','2');await fill('实际参与依据','本人实际核对布局与质量证据');await click('保存校准并核对');
 const record=state().resources.worklogs.find(item=>item.date==='2026-10-14'&&item.work==='W-104'&&item.ownerId==='H-lin');check(record?.effortState==='calibrated'&&record.hours===undefined,'Calibration was treated as actual');
 check(document.querySelector('dialog[open] [data-effort-actual]').textContent==='尚未本人确认','Calibration visually became actual');check(record.timezone==='Asia/Singapore'&&record.category==='验收','Effort metadata missing');check([...main().querySelectorAll('button')].find(el=>el.textContent.trim()==='审核通过').disabled,'Draft review enabled');
 check(!document.querySelector('dialog[open]').textContent.includes('返回任务分析'),'Modal exposes redundant return navigation');
 await fill('校准投入（小时）','1.25');await fill('校准依据','实际完成逐项质量核对');await click('保存校准');
 check(state().resources.worklogs.find(item=>item.id===record.id).confirmedHours===undefined,'Calibration auto-confirmed');
 await click('本人确认实际投入');check(state().resources.worklogs.find(item=>item.id===record.id).confirmedHours===1.25,'Actual confirmation missing');
 await fill('校准投入（小时）','1.5');check([...main().querySelectorAll('button')].find(button=>button.textContent.trim()==='本人确认实际投入').disabled,'Unsaved correction can be confirmed');
 await click('保存校准');check(main().querySelector('[data-effort-actual]').textContent==='尚未本人确认','Correction retained obsolete actual');await click('本人确认实际投入');
 const final=state().resources.worklogs.find(item=>item.id===record.id);check(final.confirmedHours===1.5&&final.history.some(entry=>entry.confirmedHours===1.25),'Historical confirmed value lost');
 await fill('审核或退回原因','投入记录需复核');await click('退回更正');check(state().resources.worklogs.find(item=>item.id===record.id).effortState==='returned','Return failed');check([...main().querySelectorAll('button')].find(el=>el.textContent.trim()==='本人确认实际投入').disabled,'Returned record can bypass calibration');
 await click('保存校准');await click('本人确认实际投入');await fill('审核或退回原因','复核通过');await click('审核通过');const reviewed=state().resources.worklogs.find(item=>item.id===record.id);check(reviewed.status==='已接受'&&reviewed.confirmedHours===1.5&&reviewed.reviewedBy==='H-lin','Review conflated with confirmation');
 check(document.documentElement.scrollWidth<=innerWidth,'Human detail overflows viewport');
 await go('/worklogs/WL-01');check(!main().textContent.includes('保存校准')&&main().textContent.includes('不能替他人'),'Other Human editable');check(main().querySelector('[data-effort-actual]').textContent==='1.5 小时','Legacy submitted fact was zeroed');
 await go('/worklogs/WL-03');check(main().textContent.includes('1 小时')&&main().textContent.includes('未采集'),'Telemetry facts/unknowns incorrect');
 check(!main().querySelector('input,textarea,[role=combobox]'),'Telemetry editable');check(!main().textContent.includes('本人确认实际投入'),'Telemetry exposes Human confirmation');
 check(document.documentElement.scrollWidth<=innerWidth,'Telemetry detail overflows viewport');
 await go('/p/launch/work-items/W-104?tab=analysis');check(main().textContent.includes('Agent 运行')&&main().textContent.includes('Token')&&main().textContent.includes('费用'),'Task telemetry missing');
 check(document.documentElement.scrollWidth<=innerWidth,'Telemetry list overflows viewport');
 return {pass:true,width:innerWidth,recordId:record.id,checks:['personal task estimate','calibration separate from actual','self confirmation','correction invalidates confirmation','immutable history','other Human readonly','legacy actual preserved','readonly independent telemetry','unknown metrics explicit','no horizontal overflow','timezone and category','review cannot precede confirmation','return recalibrate reconfirm review']};
})()
