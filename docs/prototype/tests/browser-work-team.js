// Run in a fresh session at desktop and mobile widths.
(async()=>{
 const wait=()=>new Promise(r=>setTimeout(r,160)),check=(value,message)=>{if(!value)throw Error(message)};
 const go=async(path)=>{location.hash=path;await wait()};
 const click=async(label,scope=document)=>{const button=[...scope.querySelectorAll('button,a')].find(el=>el.textContent.trim()===label||el.getAttribute('aria-label')===label);check(button,'Missing '+label);button.click();await wait()};
 const choose=async(label,value)=>{const select=document.querySelector(`[role=combobox][aria-label="${label}"]`);check(select,'Missing select '+label);select.click();await wait();await click(value,document.querySelector('[role=listbox]'))};
 const fill=async(label,value)=>{const el=[...document.querySelectorAll('dialog .field')].find(field=>field.querySelector('label')?.textContent.replace(' *','').trim()===label)?.querySelector('input:not([type=hidden]),textarea');check(el,'Missing '+label);Object.getOwnPropertyDescriptor(el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait()};
 const toggle=async(label,enabled)=>{const input=document.querySelector(`dialog input[aria-label="${label}"]`);check(input,'Missing '+label);if(input.checked!==enabled){input.click();await wait()}};
 const state=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 await go('/p/launch/work-items/W-103');await click('切换明暗主题');await click('切换明暗主题');
 const before=structuredClone(state().work.find(work=>work.id==='W-103')),organization=JSON.stringify(state().organization),agents=JSON.stringify(state().agents);
 await click('配置工作小团队');check(document.querySelector('dialog').textContent.includes('Agent 池与 Human 名册'),'No linked roster context');
 await choose('Human 结果负责人','许言');await choose('协调者','Atlas · Agent');
 for(const node of document.querySelectorAll('dialog input[aria-label^="执行者 "]'))if(node.checked&&!['执行者 Atlas','执行者 林予'].includes(node.getAttribute('aria-label'))){node.click();await wait()}
 await toggle('执行者 Atlas',true);await toggle('执行者 林予',true);await toggle('检查者 Verifier',true);
 document.querySelector('[role=combobox][aria-label="下游 Human 接收人"]').click();await wait();check(!document.querySelector('[role=listbox]').textContent.includes('Atlas'),'Agent can be L3 Human receiver');await click('林予',document.querySelector('[role=listbox]'));
 await fill('编组变更说明','当前任务由Agent与Human协同，固定包由林予接收');
 check(document.documentElement.scrollWidth<=innerWidth,'Team modal overflows');await click('保存工作小团队',document.querySelector('dialog'));
 const after=state().work.find(work=>work.id==='W-103');check(after.owner==='许言'&&after.team.executorIds.includes('agent:atlas')&&after.team.executorIds.includes('H-lin')&&after.team.checkerIds.includes('agent:verifier')&&after.team.receiverId==='H-lin','Team identity references missing');
 check(after.agent===before.agent&&JSON.stringify(after.history)===JSON.stringify(before.history),'Historical author or output changed');
 check(JSON.stringify(state().organization)===organization&&JSON.stringify(state().agents)===agents,'Composition changed organization or Agent lifecycle');
 check(JSON.stringify(after.completionContract)===JSON.stringify(before.completionContract),'Composition changed delivery contract');
 check(document.querySelector('main').textContent.includes('当前执行编组')&&document.querySelector('main').textContent.includes('Verifier'),'Detail did not update roles');
 await go('/p/launch/work-items/W-103?tab=plan');check(document.querySelector('main').textContent.includes('许言 负责 · Atlas、林予 执行'),'Plan still shows old executor');
 await go('/chat?execution_id=W-103');check(document.body.textContent.includes('当前执行 Atlas、林予'),'Execution conversation did not update');
 await go('/p/launch/work-items?view=calendar');await choose('排期成员','Atlas');check([...document.querySelectorAll('.calendar-task')].some(el=>el.textContent.includes(before.title)&&el.textContent.includes('许言 · Atlas、林予')),'Calendar assignment/filter stale');
 await go('/p/launch/artifacts/W-103?tab=来源与证据');
 // Artifact uses its own tab controls rather than URL tab values.
 await click('来源与证据');
 const author=[...document.querySelectorAll('main dt')].find(el=>el.textContent==='原记录制作主体');check(author?.nextElementSibling.textContent===before.agent,'Current composition rewrote artifact author');
 await go('/p/launch/work-items/W-103');check(document.documentElement.scrollWidth<=innerWidth,'Task detail overflows');
 return {pass:true,width:innerWidth,checks:['task-local team editor','linked Human and Agent identities','Human-only L3 receiver','organization and Agent lifecycle preserved','current detail/plan/chat/calendar synchronized','historical author and delivery retained','no horizontal overflow']};
})()
