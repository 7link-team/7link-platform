// Run against a fresh prototype in an isolated agent-browser session.
(async () => {
 const wait=()=>new Promise(r=>setTimeout(r,180)), check=(v,m)=>{if(!v)throw Error(m)};
 const click=async(label,scope=document)=>{const button=[...scope.querySelectorAll('button,a')].find(e=>e.textContent.trim()===label);check(button,'Missing '+label);button.click();await wait()};
 const fill=async(label,value)=>{const field=[...document.querySelectorAll('.field')].find(e=>e.querySelector('label')?.textContent.replace(' *','').trim()===label);const el=field?.querySelector('input:not([type=hidden]),textarea');check(el,'Missing '+label);Object.getOwnPropertyDescriptor(el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 location.hash='/chat?channel_id=launch';await wait();
 await click("赞同");
 const count=state().work.length;
 await click('转为工作');
 check(location.hash.includes('work_draft='),'Conversion did not enter conversation draft');
 check(!document.querySelector('dialog'),'Creation still uses a modal form');
 const draftId=new URLSearchParams(location.hash.split('?')[1]).get('work_draft'), draft=state().drafts[draftId];
 check(draft.source?.messageId && draft.project==='launch' && draft.tenantId===state().activeTenant,'Source/project/tenant not preserved');
 check(state().work.length===count,'Draft prematurely created formal work');
 await fill('想要完成什么','会话讨论后确认的工作');await fill('怎样才算完成','完整保留来源、讨论与固定验收条件');await click('保存会话草稿');
 const composer=document.querySelector('textarea[aria-label="消息内容"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(composer,'补充：本次先验证可用原型');composer.dispatchEvent(new Event('input',{bubbles:true}));await wait();await click('发送');
 check(state().drafts[draftId].discussion.length===1,'Discussion not linked to draft');
 await click('确认生成工作与计划');
 const work=state().work.find(w=>w.draftId===draftId);
 check(work?.plan===true && work.flow===false && work.project==='launch','Confirmed work/plan or context incorrect');
 check(work.source===draft.source.messageId && work.sourceContext.text===draft.source.text,'Immutable source lost');
 check(work.planSnapshot.discussion[0].text==='补充：本次先验证可用原型','Discussion absent from plan');
 check(state().work.length===count+1,'Confirmation created wrong number of work items');
 location.hash=`/chat?channel_id=launch&work_draft=${draftId}`;await wait();check(document.body.textContent.includes('打开生成的工作与计划'),'Confirmed draft history is unavailable');
 return {pass:true,checks:['message to conversation draft','no premature formal work','source tenant and project retained','discussion linked','single confirmed work and plan','confirmed draft remains accessible']};
})()
