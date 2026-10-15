(async()=>{
 const checks=[],check=(ok,name)=>{checks.push({name,pass:Boolean(ok)});if(!ok)throw Error(name)},wait=()=>new Promise(r=>setTimeout(r,220));
 const go=async route=>{location.hash=route;await wait();};
 const click=async(label,root=document)=>{const el=[...root.querySelectorAll('button,a')].find(el=>el.textContent.trim()===label||el.getAttribute('aria-label')===label);check(el,`Control exists: ${label}`);el.click();await wait();};
 const fill=async(selector,value,root=document)=>{const el=root.querySelector(selector);check(el,'Input exists '+selector);const proto=el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:HTMLInputElement.prototype;Object.getOwnPropertyDescriptor(proto,'value').set.call(el,value);el.dispatchEvent(new Event('input',{bubbles:true}));await wait();};
 const saved=()=>JSON.parse(localStorage.getItem('collaboration-prototype-v1'));
 await go('/p/launch/work-items/W-104?tab=executions');
 check(document.querySelector('.tabs .selected')?.textContent.trim()==='执行记录','Work tab identifies execution registry');
 await click('创建任务交互执行');
 const dialog=document.querySelector('dialog[open]');dialog.querySelector('[role=combobox]').click();await wait();const option=[...document.querySelectorAll('[role=option]')].find(el=>el.textContent.includes('Codex CLI'));check(option,'Online Codex node option');option.click();await wait();
 await click('创建等待启动的执行',dialog);
 let e=saved().executions.at(-1);check(e.id!=='W-104' && e.state==='CREATED' && e.kind==='INTERACTIVE','Creation stores independent waiting identity');
 await go(`/chat?execution_id=${encodeURIComponent(e.id)}`);
 check(document.querySelector('h1').textContent.includes('新品落地页'),'Execution title uses its bound work');
 check(document.querySelector('.channel-list')?.getAttribute('aria-label')==='会话目录'&&(!document.querySelector('.chat-context')||document.querySelector('.chat-context').getAttribute('aria-label')==='执行成果预览'),'Conversation and preview landmarks have distinct accessible names');
 check(!document.body.textContent.includes('输入与权限已校验') && !document.body.textContent.includes('runtime.started'),'No unconditional fabricated process facts');
 const operation=async(label,reason,trigger=label)=>{await click(trigger);const modal=document.querySelector('dialog[open]');await fill('textarea',reason,modal);await click(label,modal);};
 await operation('模拟节点接受并启动','test local runtime acceptance');
 await fill('.composer textarea','Use the fixed requirements');await click('提交执行指令');
 e=saved().executions.find(row=>row.id===e.id);check(e.turns.length===1 && e.turns[0].status==='PENDING','Input has explicit pending receipt and unique turn');
 check(!document.querySelector('.message-actions') && !document.querySelector('.composer [aria-label="录音"]'),'Execution does not present mutable ordinary chat controls');
 await operation('模拟采用此轮次','test local adoption','模拟采用');e=saved().executions.find(row=>row.id===e.id);check(e.turns[0].status==='ACCEPTED' && !e.turns[0].runState,'Adoption does not invent a completed turn');
 await operation('模拟轮次完成并暂停','test simulated output end');e=saved().executions.find(row=>row.id===e.id);check(e.state==='PAUSED' && e.turns[0].runState==='COMPLETED','Completion pauses this execution');
 await operation('模拟结束交互会话','test explicit session end');e=saved().executions.find(row=>row.id===e.id);check(e.state==='SUCCEEDED' && saved().work.find(w=>w.id==='W-104').status==='待验收','Ending execution preserves work acceptance state');
 await click('查看工作');check(document.querySelector('dialog[open]') && location.hash.includes(`execution_id=${e.id}`),'Work opens in place above exact execution route');await click('关闭弹窗',document.querySelector('dialog[open]'));
 if(matchMedia('(max-width:1250px)').matches){await click('切换会话上下文');}
 check(document.body.textContent.includes('此执行尚无预览资源'),'New execution never borrows old output previews');
 if(document.querySelector('dialog[open]'))await click('关闭弹窗',document.querySelector('dialog[open]'));
 check(document.documentElement.scrollWidth<=innerWidth,'No viewport overflow');
 await go(`/chat?execution_id=${e.id}`);
 const createBranch=async(label)=>{await click(label);const form=document.querySelector('dialog[open]');form.querySelector('[role=combobox]').click();await wait();[...document.querySelectorAll('[role=option]')].find(el=>el.textContent.includes('Codex CLI')).click();await wait();await fill('textarea','Checked previous side effects; create a separate local attempt',form);await click('创建等待启动的执行',form);return saved().executions.at(-1);};
 const strategy=await createBranch('创建策略替代分支');check(strategy.parentExecutionId===e.id&&strategy.branchReason==='STRATEGY'&&strategy.state==='CREATED','Strategy creates an independent waiting child');check(strategy.turns.length===0&&strategy.workSnapshot.previewResources.length===0,'Strategy does not inherit old turns or outputs');check([...document.querySelectorAll('a')].some(a=>a.getAttribute('target')==='_blank'&&a.getAttribute('href').includes(strategy.id)),'Created branch has an exact new-tab result link');
 await go(`/chat?execution_id=${strategy.id}`);await operation('模拟节点接受并启动','start branch scenario');await operation('模拟暂停','pause before retry');check(![...document.querySelectorAll('button')].some(b=>b.textContent==='模拟结束交互会话'),'No guaranteed-failure finish before a completed turn');const retry=await createBranch('创建故障重试分支');check(retry.parentExecutionId===strategy.id&&retry.branchReason==='RETRY_INFRA'&&saved().executions.find(row=>row.id===strategy.id).endReason==='SUPERSEDED_BY_RETRY','Retry supersedes paused parent and preserves provenance');check(saved().work.find(w=>w.id==='W-104').status==='待验收','Branching does not accept or complete work');
 await go('/chat?channel_id=launch');if(innerWidth<768)await click('打开会话列表');
 check(![...document.querySelectorAll('button')].some(b=>b.textContent.trim()==='创建临时探索执行'),'Exploration must not create a parallel workless execution entry');
 while(document.querySelector('dialog[open]'))await click('关闭弹窗',[...document.querySelectorAll('dialog[open]')].at(-1));
 await go('/chat?execution_id=W-104');check(document.body.textContent.includes('任务编号不能作为执行编号') && !document.querySelector('.composer'),'Task-ID execution route explicitly rejected');
 return {pass:true,width:innerWidth,checks,executionId:e.id};
})()
