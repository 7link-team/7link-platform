(async()=>{
 const checks=[],check=(ok,name)=>{checks.push({name,pass:Boolean(ok)});if(!ok)throw Error(name);},wait=()=>new Promise(r=>setTimeout(r,250));
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1')),ids=state().testConversationOrigin;
 const go=async path=>{location.hash=path;await wait();};
 const click=async(label,scope=document)=>{const el=[...scope.querySelectorAll('button,a')].find(el=>el.textContent.trim()===label||el.getAttribute('aria-label')===label);check(el,'Control '+label);el.click();await wait();};
 await go(`/p/launch/work-items/${ids.workId}?tab=plan`);
 check(!document.querySelector('main').textContent.includes('SOURCE_BODY_MUST_DISAPPEAR_AFTER_RECALL'),'Task no longer renders cached source body');
 await click('查看来源会话与草稿');let popup=document.querySelector('dialog[open]');
 check(new URLSearchParams(location.hash.split('?')[1]).get(`state.conversation-origin.${ids.workId}`)===JSON.stringify(state().activeTenant),'Source modal state has a precise URL');
 check(popup?.textContent.includes('SOURCE_BODY_MUST_DISAPPEAR_AFTER_RECALL'),'Source modal resolves current authorized message');
 const work=state().work.find(w=>w.id===ids.workId),origin=work.conversationOrigins[0];
 check(origin.createdBy==='H-lin'&&origin.confirmedBy==='H-lin'&&origin.createdAt&&origin.confirmedAt&&origin.confirmation.revision===1,'New work retains stable creation and confirmation metadata');
 const link=[...popup.querySelectorAll('a')].find(el=>el.textContent==='在新标签页打开此来源会话'),target=link.getAttribute('href').slice(1),params=new URLSearchParams(target.split('?')[1]);
 check(link.target==='_blank'&&params.get('tenant')===state().activeTenant&&params.get('scope')==='launch'&&params.get('message_id')===ids.messageId,'Source link preserves tenant, real scope and exact message');
 await click('关闭弹窗',popup);check(location.hash.startsWith(`#/p/launch/work-items/${ids.workId}?`)&&new URLSearchParams(location.hash.split('?')[1]).get('tab')==='plan'&&!document.querySelector('dialog[open]'),'Closing source modal preserves task and tab');
 await go(target);const node=[...document.querySelectorAll('[data-segment]')].find(el=>el.dataset.segment===`message:${ids.messageId}`),scroller=document.querySelector('.messages');
 check(node?.classList.contains('message-location-highlight')&&document.activeElement===node,'Direct source URL focuses and highlights exact authorized message');
 const box=node.getBoundingClientRect(),area=scroller.getBoundingClientRect();check(box.top>=area.top-1&&box.top<area.bottom,'Located message is visible in conversation scroller');
 await click('更多',node);await click('撤回并保留历史',document.querySelector('dialog[open]'));
 check(state().messages.find(m=>m.id===ids.messageId).recalled,'Own source message is actually recalled');
 check(!document.querySelector('.message-location-highlight')&&document.querySelector('main').textContent.includes('线程不可访问')&&!document.querySelector('main').textContent.includes('SOURCE_BODY_MUST_DISAPPEAR_AFTER_RECALL'),'Recalled thread root is unavailable and exposes neither a readable highlight nor cached body');
 await click('返回当前会话');
 check(!new URLSearchParams(location.hash.split('?')[1]).has('thread')&&document.querySelector('.messages')&&!document.querySelector('main').textContent.includes('SOURCE_BODY_MUST_DISAPPEAR_AFTER_RECALL'),'Unavailable source thread offers a working return to authorized conversation history');
 await go(`/p/launch/work-items/${ids.workId}?tab=plan`);await click('查看来源会话与草稿');popup=document.querySelector('dialog[open]');
 check(popup.textContent.includes('来源消息已撤回')&&!popup.textContent.includes('SOURCE_BODY_MUST_DISAPPEAR_AFTER_RECALL')&&!popup.querySelector('a[target="_blank"]'),'Recalled source modal exposes neither cached body nor actionable source link');
 check(document.documentElement.scrollWidth<=innerWidth,'Source UI fits viewport');
 return {pass:true,width:innerWidth,checks};
})()
