// Run in an isolated agent-browser session: agent-browser eval --stdin < this-file
(async()=>{
const pause=()=>new Promise(r=>setTimeout(r,100));
const click=async(text)=>{const el=[...document.querySelectorAll('button')].find(e=>e.textContent.trim()===text);if(!el)throw Error('Missing button '+text);el.click();await pause()};
const check=(condition,text)=>{if(!condition)throw Error(text)};
location.hash='/p/launch/work-items/W-104?tab=review';await pause();
await click('接受当前版本');check(document.body.innerText.includes('Human 批准不能绕过质量门'),'failed gate not blocked');
await click('模拟独立复核通过');await click('接受当前版本');await click('批准正式交付');check(!!document.querySelector('dialog[open]'),'delivery confirmation missing');await click('确认');
let s=JSON.parse(localStorage.getItem('collaboration-prototype-v1'));check(s.work.find(w=>w.id==='W-104').status==='已完成','delivery not completed');check(s.resources.deliveries.some(d=>d.work==='W-104'&&d.version==='v3'),'fixed package missing');
location.hash='/deliveries';await pause();check(document.body.innerText.includes('新品落地页'),'delivery list not synchronized');
location.hash='/chat?channel_id=launch';await pause();const ta=document.querySelector('textarea[aria-label="消息内容"]');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(ta,'浏览器验收：请生成一份发布检查清单');ta.dispatchEvent(new Event('input',{bubbles:true}));await pause();await click('发送');
s=JSON.parse(localStorage.getItem('collaboration-prototype-v1'));check(s.messages.some(m=>m.text==='浏览器验收：请生成一份发布检查清单'),'chat message not persisted');
location.hash='/p/launch/artifacts/W-103';await pause();const note=[...document.querySelectorAll('label')].find(x=>x.textContent.includes('具体意见与期望')).closest('.field').querySelector('textarea');Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(note,'浏览器验收：请减弱阴影并保留构图');note.dispatchEvent(new Event('input',{bubbles:true}));await pause();await click('提交批注');s=JSON.parse(localStorage.getItem('collaboration-prototype-v1'));check(s.comments.some(c=>c.version===2&&c.text.startsWith('浏览器验收')),'annotation version missing');
location.hash='/p/launch/flow';await pause();await click('编辑新草案');await click('添加节点');const newNode=[...document.querySelectorAll('.node-library>button')].find(e=>e.querySelector('b')?.textContent==='AI 任务');newNode.click();await pause();await click('确认发布新版本');check(document.body.innerText.includes('缺少上游输入'),'invalid graph published');s=JSON.parse(localStorage.getItem('collaboration-prototype-v1'));check(s.flowVersion===3,'invalid graph changed published version');
return JSON.stringify({pass:true,checks:['failed gate blocks acceptance','independent check → Human acceptance → fixed delivery','delivery list synchronized','chat persisted','annotation pins version','invalid SOP cannot publish'],events:s.events.length});
})()
