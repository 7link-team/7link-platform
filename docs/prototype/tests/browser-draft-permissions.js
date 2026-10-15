(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,200)),check=(v,m)=>{if(!v)throw Error(m)};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 const click=async(label,root=document)=>{const el=[...root.querySelectorAll('button,a')].find(el=>el.textContent.trim()===label||el.getAttribute('aria-label')===label);check(el,'Missing '+label);el.click();await wait()};
 location.hash='/chat?channel_id=launch';await wait();
 const message=document.querySelector('.message'),text=message.querySelector('.message-text')?.textContent||'';
 await click('转为工作',message);
 check(document.querySelectorAll('dialog[open]').length===1,'Conversion mounted duplicate editors');
 const draftId=new URLSearchParams(location.hash.split('?')[1]).get('quick_work');check(draftId,'Conversion draft is not deep linked');
 const draft=state().drafts[draftId];check(draft?.source?.messageId,'Conversion lost real message identity');
 await click('关闭弹窗',document.querySelector('dialog[open]'));
 location.hash='/settings';await wait();const role=document.querySelector('[aria-label="角色视角"]');role.click();await wait();await click('external',document.querySelector('[role=listbox]'));
 location.hash=`/chat?channel_id=external&quick_work=${draftId}`;await wait();
 const dialog=document.querySelector('dialog[open]');check(dialog?.textContent.includes('无权访问'),'External role can access private work draft');
 check(!dialog.querySelector('[name="想要完成什么"]')&&!dialog.textContent.includes(draft.source.text),'Cached source or derived title leaked through quick_work');
 check(!dialog.textContent.includes('确认生成工作与计划'),'Private draft exposes confirmation action');
 return {pass:true,checks:['one conversion editor','actual source identity','external deep link cannot read cached source or derived fields'],width:innerWidth};
})()
